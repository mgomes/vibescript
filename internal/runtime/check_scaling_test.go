package runtime

import (
	"fmt"
	"strings"
	"testing"
)

// The static checker runs inside CheckWarnings and CheckedCall before any
// script step executes, so work it does per source element is metered by
// nothing. Each test below drives a source that reaches one such site and
// counts the elements that site handles, asserting the count grows with the
// source rather than with its square.
//
// None of them call t.Parallel: checkWorkCounting and checkWorkUnits are
// process-wide, so a concurrent check would fold its own units into the count.

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

func requireCheckWarning(t *testing.T, warnings []CheckWarning, want string) {
	t.Helper()

	for _, warning := range warnings {
		if warning.Message == want {
			return
		}
	}
	t.Fatalf("expected the warning %q, got %v", want, warnings)
}
