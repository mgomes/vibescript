package runtime

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// The static checker runs inside CheckWarnings and CheckedCall before any
// script step executes, so work it does per source element is metered by
// nothing. Each test below drives a source that reaches one such site and
// measures what that site handles -- the elements it inspects or copies, or the
// bytes the check allocates where the copies are the allocation -- asserting
// the measurement grows with the source rather than with its square.
//
// None of them call t.Parallel: the counters and the process allocation total
// are process-wide, so a concurrent check would fold its own work into them.

func measureCheckWork(t *testing.T, source string) uint64 {
	t.Helper()

	script := compileScript(t, source)
	checkWorkUnits.Store(0)
	checkWorkCounting.Store(true)
	defer checkWorkCounting.Store(false)
	script.CheckWarnings()
	return checkWorkUnits.Load()
}

func destructuredFillBlockSource(params int) string {
	names := make([]string, 0, params+1)
	for i := range params {
		names = append(names, fmt.Sprintf("a%d", i))
	}
	names = append(names, "*rest")
	return fmt.Sprintf(`
def f(items: array<int>)
  items.fill() do |(%s)|
    1
  end
end
`, strings.Join(names, ", "))
}

// A destructured block parameter resolved one element at a time rescanned the
// whole target for its rest element and re-decomposed the yielded value, so an
// array.fill block taking `(x1, ..., xN, *rest)` cost N*N element inspections
// during a check (#6).
func TestCheckDestructuredBlockParamBindStaysLinear(t *testing.T) {
	small := measureCheckWork(t, destructuredFillBlockSource(2000))
	large := measureCheckWork(t, destructuredFillBlockSource(4000))

	// Measured 2,002 then 4,002 inspections, a 2.00x step for a doubled
	// parameter list. Before, the same pair inspected 4.0M and 16.0M, a 4.00x
	// step. The assertion allows up to 3x so it states the complexity rather
	// than pinning counts that ordinary checker changes would shift.
	if large > small*3 {
		t.Fatalf("doubling the destructured parameters inspected %d elements against %d --"+
			" over 3x, so binding a destructured block parameter is superlinear again", large, small)
	}
}

// The layout is derived once per bind, so it has to keep answering for every
// position: a rest element whose own head cannot hold the yielded value still
// has to prove the block never enters, which is what leaves the fill receiver's
// declared element type standing.
func TestDestructuredFillBlockKeepsBindingDiagnostics(t *testing.T) {
	binds := compileScript(t, `
def f(items: array<int>)
  items.fill() do |(a, b)|
    "bad"
  end
end
`)
	requireCheckWarning(t, binds.CheckWarnings(),
		"write to items expected element int, got string")

	// A fill yields an int index, which destructures as a one-element sequence,
	// so the leading target is an int the string annotation can never hold and
	// the "bad" result never reaches the receiver. items keeps array<int>, and
	// only the later append contradicts it.
	neverBinds := compileScript(t, `
def f(items: array<int>)
  items.fill() do |(a: string, *rest)|
    "bad"
  end
  items << true
end
`)
	warnings := neverBinds.CheckWarnings()
	requireCheckWarning(t, warnings, "write to items expected element int, got bool")
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "got string") {
			t.Fatalf("a block that cannot bind still wrote its result: %v", warnings)
		}
	}
}

func measureCheckAllocation(t *testing.T, source string) uint64 {
	t.Helper()

	script := compileScript(t, source)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	script.CheckWarnings()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func variadicRestCallSource(args int) string {
	return fmt.Sprintf(`
def sink(*xs)
  xs
end

def f()
  sink(%s)
end
`, strings.TrimSuffix(strings.Repeat("0, ", args), ", "))
}

// Modelling a rest parameter rebuilt the whole aggregate at every supplied
// argument, so a call with one static value per argument kept a single
// alternative and still copied 1 + 2 + ... + N expressions to reach it (#9).
// This counts allocated bytes because the copies are the allocation.
func TestCheckVariadicRestCallAllocationStaysLinear(t *testing.T) {
	small := measureCheckAllocation(t, variadicRestCallSource(1000))
	large := measureCheckAllocation(t, variadicRestCallSource(2000))

	// Measured 2.7MB then 5.5MB, a 2.0x step for a doubled argument list.
	// Before, the same pair allocated 71MB and 274MB, a 3.8x step. The
	// assertion allows up to 3x so it states the complexity rather than pinning
	// byte counts that ordinary checker changes would shift.
	if large > small*3 {
		t.Fatalf("doubling the rest arguments allocated %d bytes against %d -- over 3x,"+
			" so binding a rest parameter is superlinear in the argument count again", large, small)
	}
}

// The aggregate exists so that indexing a rest parameter recovers the exact
// argument that landed at that position, which is the fact the diagnostic below
// depends on. Appending in place has to leave every position where it was.
func TestVariadicRestCallKeepsExactArgumentPositions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		index string
		call  string
	}{
		{name: "first argument", index: "xs[0]", call: `sink("bad", 1)`},
		{name: "later argument", index: "xs[2]", call: `sink(1, 2, "bad", 4)`},
		{name: "last argument", index: "xs[3]", call: `sink(1, 2, 3, "bad")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := compileScript(t, fmt.Sprintf(`
def sink(*xs)
  %s
end

def take(v: int)
  v
end

def f()
  take(%s)
end
`, tc.index, tc.call))
			requireCheckWarning(t, script.CheckWarnings(),
				"call to take argument v expected int, got string")
		})
	}
}

func requireCheckWarning(t *testing.T, warnings []CheckWarning, want string) {
	t.Helper()

	for _, warning := range warnings {
		if warning.Message == want {
			return
		}
	}
	t.Fatalf("expected the warning %q, got %v", want, warnings)
}
