package runtime

import (
	"fmt"
	"math"
	"runtime"
	"slices"
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

// Modeling a rest parameter rebuilt the whole aggregate at every supplied
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

func shapeFieldWriteSource(writes int) string {
	var body strings.Builder
	for i := range writes {
		fmt.Fprintf(&body, "  h[:k%d] = 1\n", i)
	}
	return fmt.Sprintf(`
def f()
  h = { seed: 1 }
%s  h
end
`, body.String())
}

// Refining a witnessed literal shape copied the whole shape for every field
// write, so a symbol-keyed literal followed by N writes copied 1 + 2 + ... + N
// entries (#14).
func TestCheckShapeFieldWriteCopiesStayLinear(t *testing.T) {
	small := measureCheckWork(t, shapeFieldWriteSource(1000))
	large := measureCheckWork(t, shapeFieldWriteSource(2000))

	// Measured 4,032 copied nodes for both sizes: refinement stops once the
	// budget is spent, so a longer write run copies nothing more. Before, the
	// same pair copied 500,500 and 2.0M, a 4.00x step. The assertion allows up
	// to 3x so it states the complexity rather than pinning counts.
	if large > small*3 {
		t.Fatalf("doubling the field writes copied %d shape nodes against %d -- over 3x,"+
			" so refining a witnessed shape is superlinear in the write count again", large, small)
	}
}

// A write to a key the shape already holds never widens it, so counting fields
// rather than the nodes each copy walks left this shape unbounded: the literal
// below carries one wide nested field that every refinement deep-copies (#14).
func repeatedKeyShapeWriteSource(nested, writes int) string {
	pairs := make([]string, 0, nested)
	for i := range nested {
		pairs = append(pairs, fmt.Sprintf("k%d: %d", i, i))
	}
	var body strings.Builder
	for i := range writes {
		fmt.Fprintf(&body, "  h[:b] = %d\n", i)
	}
	return fmt.Sprintf(`
def f()
  h = { a: { %s }, b: 0 }
%s  h
end
`, strings.Join(pairs, ", "), body.String())
}

// Every write here restates the type the field already holds, so this workload
// settles at no work at all rather than at a bounded amount of it, and the
// assertion says so: a ratio between two sizes could not tell zero from zero.
// Both halves of "no work" are pinned here, because both are ways this run has
// been paid for. The write keeps the receiver's fact instead of copying it, and
// deciding that it may is free too, since the write hands back the very node
// the field already holds. Counting fields rather than the nodes each copy
// walks measured neither: the same pair copied 800 and 1,600 fields while
// allocating 26MB and 97MB, since no write here widens the shape it deep-copies.
//
// Zero is also what a workload that stopped reaching the write path would
// measure, so this test does not stand on its own. The alternating test below
// drives the same literal and the same key and asserts a nonzero charge, which
// is what says the path is still reached.
func TestCheckRepeatedShapeKeyWriteCostsNothing(t *testing.T) {
	for _, size := range []int{400, 800} {
		if units := measureCheckWork(t, repeatedKeyShapeWriteSource(size, size)); units != 0 {
			t.Fatalf("%d writes of one key against a %d-field nested shape handled %d units,"+
				" want none: a write restating a field's type copies nothing and, being"+
				" handed the fact it is compared against, compares nothing either", size, size, units)
		}
	}
}

// The comparison the no-copy path makes only serializes when the two sides are
// distinct nodes, which is what a key alternating between two types produces.
// Nothing budgets that walk -- the node budget pays for copies -- so what bounds
// it is that the source has to spell the written value out to produce a node.
func alternatingKeyShapeWriteSource(nested, writes int) string {
	pairs := make([]string, 0, nested)
	for i := range nested {
		pairs = append(pairs, fmt.Sprintf("k%d: %d", i, i))
	}
	var body strings.Builder
	for i := range writes {
		if i%2 == 0 {
			body.WriteString("  h[:b] = 1\n")
		} else {
			body.WriteString("  h[:b] = \"s\"\n")
		}
	}
	return fmt.Sprintf(`
def f()
  h = { a: { %s }, b: 0 }
%s  h
end
`, strings.Join(pairs, ", "), body.String())
}

func TestCheckAlternatingShapeKeyWriteComparisonsStayLinear(t *testing.T) {
	small := measureCheckWork(t, alternatingKeyShapeWriteSource(400, 400))
	large := measureCheckWork(t, alternatingKeyShapeWriteSource(800, 800))

	// A cell that reaches nothing measures zero and passes every ratio, which is
	// how two cells of the budget walk came to assert nothing. This one has to
	// charge to count, and it is what proves the repeated-key zero above is a
	// path taken for free rather than a path not taken.
	if small == 0 || large == 0 {
		t.Fatalf("alternating one key between two types charged %d then %d units of fact"+
			" comparison -- the write path is not being reached, so neither this test nor"+
			" the repeated-key one is measuring anything", small, large)
	}

	// Measured 4,942 then 5,647 compared nodes, a 1.14x step for a doubled
	// source. The assertion allows up to 3x so it states the complexity rather
	// than pinning counts that ordinary checker changes would shift.
	if large > small*3 {
		t.Fatalf("doubling the nested shape and the writes compared %d type fact nodes"+
			" against %d -- over 3x, so deciding whether a write restates a field is"+
			" superlinear in the source", large, small)
	}
}

// The budget trades precision for a bound, so everything it pays for has to
// keep the precision: a field a write added is still read back exactly, and a
// shape spelled wide enough that the old field cap refused it outright is now
// refined once, since the source spells that shape only once. Once the budget
// is spent the fact weakens, which can only drop a diagnostic, never add one.
func TestShapeFieldWriteRefinementKeepsFieldFacts(t *testing.T) {
	literal := func(fields int) string {
		pairs := make([]string, 0, fields)
		for i := range fields {
			pairs = append(pairs, fmt.Sprintf("k%d: %d", i, i))
		}
		return "{ " + strings.Join(pairs, ", ") + " }"
	}
	source := func(fields int) string {
		return fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  h = %s
  h[:added] = "bad"
  take(h[:added])
end
`, literal(fields))
	}
	const want = "call to take argument v expected int, got string"

	for _, fields := range []int{1, 64, 200} {
		requireCheckWarning(t, compileScript(t, source(fields)).CheckWarnings(), want)
	}

	// Writes of distinct new keys are what spends the budget. Past it the fact
	// stops naming every key the script may add, so a key written after that is
	// simply not claimed and reads as unknown: the write reports nothing rather
	// than a fact the checker no longer holds.
	spent := fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  h = { seed: 1 }
%s  h[:added] = "bad"
  take(h[:added])
end
`, distinctShapeKeyWrites(200))
	if warnings := compileScript(t, spent).CheckWarnings(); len(warnings) != 0 {
		t.Fatalf("a shape past the refinement budget reported %v", warnings)
	}
}

func distinctShapeKeyWrites(writes int) string {
	var body strings.Builder
	for i := range writes {
		fmt.Fprintf(&body, "  h[:k%d] = 1\n", i)
	}
	return body.String()
}

// A write whose value the field's type already admits still narrows it, and the
// narrowed field is what decides a branch reading it: `nil | int` proves
// nothing, `int` proves the else arm dead, since nil and false are the only
// falsey values. Skipping the copy whenever the old type merely admitted the
// written value kept the broader claim and reported the call in that arm (#14).
// A write is treated as a restatement only when the two facts really are the
// same fact. Deciding that from `typeFactKey` could not tell: the key stops at
// maxTypeArmDepth and renders everything below it as `?`, so two shapes alike
// down to that depth and different underneath produced one key, the write was
// taken for a restatement, and the receiver kept a fact naming the type it held
// before. A read of the replaced field then answered from a value the script
// had overwritten.
//
// The depths bracket the cutoff on both sides so the test states where the
// boundary is rather than that one exists, and so a cutoff that moves does not
// quietly leave this asserting nothing: the shallow cases have to keep passing
// for the deep ones to mean anything.
func TestShapeFieldWriteTracksFactsBelowTheKeyDepth(t *testing.T) {
	nest := func(depth int, leaf string) string {
		return strings.Repeat("{ a: ", depth) + leaf + strings.Repeat(" }", depth)
	}
	for _, depth := range []int{2, 8, 9, 12, 20} {
		t.Run(fmt.Sprintf("depth %d", depth), func(t *testing.T) {
			source := fmt.Sprintf(`
def take(v: string)
  v
end

def f()
  h = { w: %s }
  h[:w] = %s
  take(h[:w]%s)
end
`, nest(depth, "1"), nest(depth, `"s"`), strings.Repeat("[:a]", depth))
			if warnings := compileScript(t, source).CheckWarnings(); len(warnings) != 0 {
				t.Fatalf("a %d-level write was taken for a restatement, so the field still"+
					" reads as what it held before the write: %v", depth, warnings)
			}
		})
	}
}

func TestShapeFieldWriteNarrowsCompatibleField(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		write string
	}{
		{name: "nullable field", field: "flag ? nil : 1", write: `h[:a] = 1`},
		{name: "union field", field: `flag ? "s" : 1`, write: `h[:a] = 1`},
		{name: "repeated narrowing write", field: "flag ? nil : 1", write: "h[:a] = 1\n  h[:a] = 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fmt.Sprintf(`
def take(v: int)
  v
end

def f(flag)
  h = { a: %s }
  %s
  if h[:a]
    1
  else
    take("bad")
  end
end
`, tc.field, tc.write)
			if warnings := compileScript(t, source).CheckWarnings(); len(warnings) != 0 {
				t.Fatalf("a write that narrows the field left the dead else branch live: %v", warnings)
			}
		})
	}

	// The same holds while the budget still pays for the copy, which is every
	// run of writes an ordinary hash reaches.
	for _, writes := range []int{0, 10, 50} {
		source := fmt.Sprintf(`
def take(v: int)
  v
end

def f(flag)
  h = { a: flag ? nil : 1 }
%s  h[:a] = 1
  if h[:a]
    1
  else
    take("bad")
  end
end
`, distinctShapeKeyWrites(writes))
		if warnings := compileScript(t, source).CheckWarnings(); len(warnings) != 0 {
			t.Fatalf("%d writes before the narrowing one left the dead else branch live: %v",
				writes, warnings)
		}
	}
}

// Every answer the shape write gives when it cannot restate the fact exactly is
// a place the checker can withdraw something it still knows, and withdrawing a
// field type is what adds diagnostics: a branch reading that field stops being
// decided and whatever is written in its dead arm gets checked. Five separate
// reports found five such answers one at a time, so this walks them and
// compares against the checker with the budget raised out of the way, which is
// the only reference that says what the bound cost rather than what it happens
// to do. Every configuration has to report a subset of what the unbudgeted run
// reports (#14).
//
// The axes have to compose, which is what an earlier version of this walk got
// wrong: it drew the written field and the branch subject from the same pair of
// names, so every write that reached the give-up landed on the field the branch
// read, and the shape that fails -- a write landing on one field while the
// branch reads another the writes never touch -- had no cell at all. The
// written field, the branch subject, and whether the branch subject is ever
// written are separate here, and `keep` is truthy and written by nothing.
func TestShapeWriteBudgetNeverAddsDiagnostics(t *testing.T) {
	literal := func(written string, fields int) string {
		pairs := make([]string, 0, fields+3)
		pairs = append(pairs, "w: "+written, "keep: 1", "maybe: flag ? nil : 1")
		for i := range fields {
			pairs = append(pairs, fmt.Sprintf("f%d: %d", i, i))
		}
		return "{ " + strings.Join(pairs, ", ") + " }"
	}
	source := func(written string, fields int, padding, writes, subject string) string {
		return fmt.Sprintf(`
def take(v: int)
  v
end

def f(flag)
  h = %s
%s%s  if h[:%s]
    1
  else
    take("bad")
  end
end
`, literal(written, fields), padding, writes, subject)
	}

	// Padding drives how much of the budget is gone before the write under test.
	// Distinct new keys spend the first allowance. Spending the second takes
	// contradicting writes to keys the fact still names, which means the
	// literal's own fields: a key added after the first allowance is gone was
	// never named, so writing it again is free and spends nothing. An earlier
	// version of this walk contradicted the padding's own keys and therefore
	// never reached the second allowance at all, which is how it kept passing a
	// shape that crosses both.
	padding := func(newKeys, claimedOverwrites, fields int) string {
		var body strings.Builder
		for i := range newKeys {
			fmt.Fprintf(&body, "  h[:p%d] = 1\n", i)
		}
		for i := range claimedOverwrites {
			fmt.Fprintf(&body, "  h[:f%d] = \"s\"\n", i%max(fields, 1))
		}
		return body.String()
	}

	// Each write kind names the type the written field starts as, so that the
	// kind is what the write does to it rather than an accident of the literal.
	writes := []struct{ name, written, text string }{
		{"new key", "1", "  h[:fresh] = 1\n"},
		{"same type", "1", "  h[:w] = 2\n"},
		{"narrowing", "flag ? nil : 1", "  h[:w] = 1\n"},
		{"widening", "1", "  h[:w] = flag ? 2 : \"s\"\n"},
		{"disjoint", "1", "  h[:w] = \"s\"\n"},
		{"alternating", "1", strings.Repeat("  h[:w] = \"s\"\n  h[:w] = 2\n", 25)},
		{"narrowing then widening", "flag ? nil : 1", "  h[:w] = 1\n  h[:w] = \"s\"\n  h[:w] = 1\n"},
	}
	budgets := []struct {
		name    string
		padding func(fields int) string
	}{
		{"unspent", func(int) string { return "" }},
		{"budget spent", func(fields int) string { return padding(200, 0, fields) }},
	}
	widths := []struct {
		name   string
		fields int
	}{
		{"narrow", 0},
		{"wide", 300},
		{"wider than the budget", 1000},
		{"far wider than the budget", 6000},
	}

	for _, width := range widths {
		for _, budget := range budgets {
			for _, write := range writes {
				for _, subject := range []string{"w", "keep"} {
					name := fmt.Sprintf("%s/%s/%s/branch on %s",
						width.name, budget.name, write.name, subject)
					t.Run(name, func(t *testing.T) {
						script := source(write.written, width.fields, budget.padding(width.fields), write.text, subject)
						unbudgeted := checkWarningMessagesWithShapeBudget(t, script, math.MaxInt/4)
						budgeted := checkWarningMessages(compileScript(t, script).CheckWarnings())

						for _, message := range budgeted {
							if !slices.Contains(unbudgeted, message) {
								t.Fatalf("the budget added %q, which the same script reports"+
									" nothing of without it: %v against %v",
									message, budgeted, unbudgeted)
							}
						}
					})
				}
			}
		}
	}
}

// checkWarningMessagesWithShapeBudget checks one script under a given shape
// refinement budget. It restores the budget before returning, and the tests in
// this file do not run in parallel, so no other check sees the raised one.
// Widening a field's claim joins what the field held to what a write put there,
// and the join deduplicates its arms. Deduplicating them on typeFactKey dropped
// an arm differing from one already kept only below maxTypeArmDepth, so a write
// that differed from the field that deep was joined away and the field went on
// claiming the shape it had before the write. A call annotated with that stale
// shape was then accepted.
//
// Reaching the join needs the two budgets apart, which is how production sets
// them and is not what checkWarningMessagesWithShapeBudget does: with both the
// same, crossing the exact budget crosses the widening budget in the same
// breath, so the fact is given up before anything can widen and this route is
// unreachable under it. That is why the configuration walk never covered this.
//
// The depths bracket the cutoff, and the shallow ones are the control: they
// have to keep reporting the joined union for the deep ones to mean anything.
func TestShapeFieldWideningKeepsDeepArms(t *testing.T) {
	nest := func(depth int, leaf string) string {
		return strings.Repeat("{ a: ", depth) + leaf + strings.Repeat(" }", depth)
	}
	for _, depth := range []int{2, 8, 9, 12, 20} {
		t.Run(fmt.Sprintf("depth %d", depth), func(t *testing.T) {
			// take is annotated with the shape the field held before the write,
			// so the call is only reported if the written arm survived the join.
			source := fmt.Sprintf(`
def take(v: %s)
  v
end

def f()
  h = { w: %s, pad: 1 }
  h[:pad] = 2
  h[:pad] = "x"
  h[:w] = %s
  take(h[:w])
end
`, nest(depth, "int"), nest(depth, "1"), nest(depth, `"s"`))

			warnings := checkWarningMessagesWithSplitShapeBudget(t, source, 4, 1<<30)
			if len(warnings) != 1 || !strings.Contains(warnings[0], "string") {
				t.Fatalf("widening a %d-level field joined the written shape away, so the field"+
					" still claims what it held before the write: %v", depth, warnings)
			}
		})
	}
}

// mutatorReceiverRebindSource builds a witnessed shape whose `w` field nests
// depth levels deep, rebinds the local to a shape differing only at that leaf
// while the mutator's own value operand is being walked, and then reads the
// leaf. write is the mutator statement; the rebind happens inside a string
// interpolation so the written value stays a known type and the write lands.
func mutatorReceiverRebindSource(depth int, write string) string {
	nest := func(leaf string) string {
		return strings.Repeat("{ a: ", depth) + leaf + strings.Repeat(" }", depth)
	}
	return fmt.Sprintf(`
def take(v: string)
  v
end

def f()
  h = { w: %s, pad: 1 }
  rebind = -> { h = { w: %s, pad: 1 }; "x" }
%s
  take(h[:w]%s)
end
`, nest("1"), nest(`"s"`), write, strings.Repeat("[:a]", depth))
}

// A mutator preserves its receiver's fact only when the local still carries the
// fact the writes were checked against, and the write it applies lands on that
// captured fact and is bound back to the local. Deciding "still carries" from
// typeFactKey could not tell: the key stops at maxTypeArmDepth and renders
// everything below as `?`, so a value operand that rebound the local to a shape
// differing only that deep produced a matching key, the rebind was read as
// having left the local alone, and the write put the shape the script had
// stopped holding back in place. Reading the rebound field then answered from
// the old shape and the call was reported against correct code.
//
// Both predicates that decide this are covered: the indexed write reaches
// mutatorReceiverFactIntact and the store reaches mutatorCallPreservable.
//
// The depths bracket the cutoff on both sides so the test states where the
// boundary is rather than that one exists. The control is the same body without
// the rebind, which must stay diagnosed at every depth -- otherwise the shallow
// cases pass because the read stopped resolving rather than because the fact
// survived, and the deep ones would mean nothing.
func TestMutatorPreservationTracksFactsBelowTheKeyDepth(t *testing.T) {
	for _, write := range []string{
		`  h[:pad] = "v#{rebind.call}"`,
		`  h.store(:pad, "v#{rebind.call}")`,
	} {
		for _, depth := range []int{2, 7, 8, 9, 10, 14} {
			t.Run(fmt.Sprintf("%s depth %d", strings.TrimSpace(write), depth), func(t *testing.T) {
				source := mutatorReceiverRebindSource(depth, write)
				if warnings := checkWarningMessages(compileScript(t, source).CheckWarnings()); len(warnings) != 0 {
					t.Fatalf("a %d-level rebind was read as leaving the receiver alone, so the"+
						" mutator put back the shape the local stopped holding: %v", depth, warnings)
				}

				control := mutatorReceiverRebindSource(depth, `  h[:pad] = "v"`)
				warnings := checkWarningMessages(compileScript(t, control).CheckWarnings())
				if len(warnings) != 1 || !strings.Contains(warnings[0], "expected string, got int") {
					t.Fatalf("the un-rebound control must still be diagnosed at depth %d, or the"+
						" assertion above passes for the wrong reason: %v", depth, warnings)
				}
			})
		}
	}
}

// checkWarningMessagesWithSplitShapeBudget checks one script with the exact and
// widening budgets set independently, so a fact can cross the first without
// crossing the second and take the widening route rather than being given up.
// It restores both before returning, and the tests in this file do not run in
// parallel, so no other check sees the changed ones.
func checkWarningMessagesWithSplitShapeBudget(t *testing.T, source string, exact, widened int) []string {
	t.Helper()

	previousExact, previousWidened := maxRefinedShapeNodes, maxWidenedShapeNodes
	maxRefinedShapeNodes, maxWidenedShapeNodes = exact, widened
	defer func() {
		maxRefinedShapeNodes, maxWidenedShapeNodes = previousExact, previousWidened
	}()
	return checkWarningMessages(compileScript(t, source).CheckWarnings())
}

func checkWarningMessagesWithShapeBudget(t *testing.T, source string, budget int) []string {
	t.Helper()

	previousExact, previousWidened := maxRefinedShapeNodes, maxWidenedShapeNodes
	maxRefinedShapeNodes, maxWidenedShapeNodes = budget, budget
	defer func() {
		maxRefinedShapeNodes, maxWidenedShapeNodes = previousExact, previousWidened
	}()
	return checkWarningMessages(compileScript(t, source).CheckWarnings())
}

// The boundary the walk above stops at, generated the same way it is: a fact
// that crosses the exact-refinement budget and then takes contradicting writes
// to enough keys it still names to cross the widening budget too. Every write
// past that point would have to copy the whole fact to restate one claim, and
// paying for them is the quadratic this budget exists to refuse -- contradicting
// every key of a literal allocated 11.6MB, 46.7MB and 184MB at 200, 400 and 800
// fields with the cap removed, against 5.4MB, 7.1MB and 10.0MB with it.
//
// What that costs is not precision alone, which is why it is pinned in this
// detail. Giving the fact up leaves a branch on a field it had vouched for
// undecided, and the checker then walks the arm it can no longer prove dead and
// reports what it finds there. The reports are real errors in code that cannot
// run rather than invented ones -- a well-typed arm stays silent, and reading
// the same field in reachable code stays silent too, so the loss travels only
// through reachability -- but they are still diagnostics the unbudgeted checker
// does not produce.
func TestShapeWideningBudgetGivesUpTheFact(t *testing.T) {
	source := func(fields int, arm string) string {
		pairs := make([]string, 0, fields+1)
		for i := range fields {
			pairs = append(pairs, fmt.Sprintf("f%d: %d", i, i))
		}
		pairs = append(pairs, "keep: 1")
		var body strings.Builder
		body.WriteString("  h[:fresh] = 1\n")
		for i := range fields {
			fmt.Fprintf(&body, "  h[:f%d] = \"s\"\n", i)
		}
		return fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  h = { %s }
%s  if h[:keep]
    1
  else
%s
  end
end
`, strings.Join(pairs, ", "), body.String(), arm)
	}
	const illTyped = `    take("bad")`

	// Short of the widening budget every claim is restated and the branch stays
	// decided, which is the property the walk above asserts.
	for _, fields := range []int{50, 100} {
		if messages := checkWarningMessages(compileScript(t, source(fields, illTyped)).CheckWarnings()); len(messages) != 0 {
			t.Fatalf("%d contradicted fields reported %v inside the budget", fields, messages)
		}
	}

	// Past it the fact is given up, the branch is undecided, and the arm is
	// walked. Each of these is a diagnostic the unbudgeted checker does not
	// produce, about code it proves cannot run.
	for _, arm := range []struct{ name, body, want string }{
		{"typed argument", illTyped, "call to take argument v expected int, got string"},
		{"undefined name", "    missing", "undefined variable missing"},
		{"unsupported operator", `    y = -"s"`, "unsupported unary - operand string"},
		{"arity", "    take(1, 2)", "call to take has unexpected positional arguments"},
	} {
		t.Run(arm.name, func(t *testing.T) {
			script := source(300, arm.body)
			requireCheckWarningMessage(t,
				checkWarningMessages(compileScript(t, script).CheckWarnings()), arm.want)

			unbudgeted := checkWarningMessagesWithShapeBudget(t, script, math.MaxInt/4)
			if len(unbudgeted) != 0 {
				t.Fatalf("the unbudgeted checker reported %v on an arm it proves dead", unbudgeted)
			}
		})
	}

	// The loss travels through reachability and nothing else: an arm with
	// nothing wrong in it stays silent, and so does a read of the same field in
	// code that runs either way.
	if messages := checkWarningMessages(compileScript(t, source(300, "    take(1)")).CheckWarnings()); len(messages) != 0 {
		t.Fatalf("a well-typed dead arm reported %v", messages)
	}
	reachable := fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  h = { %s, keep: 1 }
%s  take(h[:keep])
end
`, strings.Join(func() []string {
		pairs := make([]string, 0, 300)
		for i := range 300 {
			pairs = append(pairs, fmt.Sprintf("f%d: %d", i, i))
		}
		return pairs
	}(), ", "), func() string {
		var body strings.Builder
		body.WriteString("  h[:fresh] = 1\n")
		for i := range 300 {
			fmt.Fprintf(&body, "  h[:f%d] = \"s\"\n", i)
		}
		return body.String()
	}())
	if messages := checkWarningMessages(compileScript(t, reachable).CheckWarnings()); len(messages) != 0 {
		t.Fatalf("reading the untouched field in reachable code reported %v", messages)
	}
}

func requireCheckWarningMessage(t *testing.T, messages []string, want string) {
	t.Helper()

	if !slices.Contains(messages, want) {
		t.Fatalf("expected %q, got %v", want, messages)
	}
}

// The boundary the walk above stops at. A literal wider than the whole
// refinement budget cannot be restated once a chain has spent both allowances:
// the copy that would keep its fields is the per-write cost the budget exists
// to stop, and paying it per write is the quadratic itself. Such a write keeps
// the field it lands on and gives up the rest, so this pins what a shape that
// wide does keep rather than leaving it unstated.
func TestShapeWiderThanBudgetKeepsWhatItCanCopy(t *testing.T) {
	pairs := make([]string, 0, 6002)
	pairs = append(pairs, "n: 1", "other: 1")
	for i := range 6000 {
		pairs = append(pairs, fmt.Sprintf("f%d: %d", i, i))
	}
	source := fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  h = { %s }
  h[:n] = "s"
  if h[:n]
    1
  else
    take("bad")
  end
end
`, strings.Join(pairs, ", "))

	// One contradicting write is still inside the allowance, so the fact keeps
	// every field and the branch on the written one stays decided.
	if warnings := compileScript(t, source).CheckWarnings(); len(warnings) != 0 {
		t.Fatalf("the first contradicting write to a very wide shape reported %v", warnings)
	}
}

// Spending the budget must not withdraw a field the fact already vouched for.
// A field read decides the branch below -- every value but nil and false is
// truthy, so a shape that still names an int field proves the else branch dead
// -- and a run of writes long enough to exhaust the budget used to poison the
// whole fact, which made that branch live and reported the call inside it. That
// is a diagnostic the same program one write shorter does not produce, which is
// the one thing this bound must never do (#14).
func TestShapeBudgetKeepsBranchFactsWhenExhausted(t *testing.T) {
	source := func(writes int) string {
		return fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  h = { n: 1 }
%s  if h[:n]
    1
  else
    take("bad")
  end
end
`, distinctShapeKeyWrites(writes))
	}

	for _, writes := range []int{0, 10, 50, 100, 200} {
		if warnings := compileScript(t, source(writes)).CheckWarnings(); len(warnings) != 0 {
			t.Fatalf("%d field writes made the dead else branch live: %v", writes, warnings)
		}
	}
}

func namespaceWriteCallSource(writes int, callee string) string {
	var body strings.Builder
	for i := range writes {
		fmt.Fprintf(&body, "  JSON.m%d = 1\n", i)
		body.WriteString("  take(callee())\n")
	}
	return fmt.Sprintf(`
def callee()
  %s
end

def take(x: int)
  x
end

def main()
%send
`, callee, body.String())
}

// A return summary was cached under a context naming every recorded namespace
// member, so a script that assigns a new member before each call to an
// unannotated function in a typed position re-sorted and re-joined the whole
// member list for every call, and kept the result as a map key (#18).
func TestCheckReturnSummaryContextStaysLinear(t *testing.T) {
	small := measureCheckWork(t, namespaceWriteCallSource(400, "1"))
	large := measureCheckWork(t, namespaceWriteCallSource(800, "1"))

	// Measured 2 member names joined at both sizes: the callee's summary never
	// reads the recorded members, so it is kept under a context that does not
	// name them and the list is never built again. Before, the same pair joined
	// 721,800 and 2,883,600 names, a 4.00x step. The assertion allows up to 3x
	// so it states the complexity rather than pinning counts.
	if large > small*3 {
		t.Fatalf("doubling the namespace writes joined %d member names against %d -- over 3x,"+
			" so keying a return summary is superlinear in the recorded members again", large, small)
	}
}

// A summary that does read the recorded members still has to be separated by
// them: the namespace write below turns a statically known class method into a
// dynamic one, and the callee's result has to stop being a known string at that
// point rather than staying whatever the earlier call proved.
func TestNamespaceDependentReturnSummarySeparatesContexts(t *testing.T) {
	const callee = `
def take(v: int)
  v
end

def stringified()
  JSON.stringify(1)
end
`
	before := compileScript(t, callee+`
def f()
  take(stringified())
  JSON.stringify = 1
  take(stringified())
end
`)
	warnings := before.CheckWarnings()
	requireCheckWarning(t, warnings, "call to take argument v expected int, got string")
	if len(warnings) != 1 {
		t.Fatalf("only the call before the namespace write is decided, got %v", warnings)
	}

	after := compileScript(t, callee+`
def f()
  JSON.stringify = 1
  take(stringified())
end
`)
	if warnings := after.CheckWarnings(); len(warnings) != 0 {
		t.Fatalf("a reassigned namespace member left the callee decided: %v", warnings)
	}
}

func storedBlockFillSource(n int) string {
	var body strings.Builder
	for i := range n {
		fmt.Fprintf(&body, "    v%d = index + %d\n", i, i)
	}
	return fmt.Sprintf(`
def f(items: array<int>)
  callback = proc do |index|
%s    1
  end
%send
`, body.String(), strings.Repeat("  items.fill(&callback)\n", n))
}

// A block reached through a stored callable is written once and walked again
// for its result at every site that passes it, so a proc body and the sites
// passing it multiplied: N body statements filled N times walked N*N (#10).
func TestCheckStoredBlockResultWalksStayLinear(t *testing.T) {
	small := measureCheckWork(t, storedBlockFillSource(200))
	large := measureCheckWork(t, storedBlockFillSource(400))

	// Measured 8,030 then 8,015 nodes walked, which is the cap plus the walk
	// that reached it. Before, the same pair walked 40,200 and 160,400
	// statements, a 3.99x step. The assertion allows up to 3x so it states the
	// complexity rather than pinning counts.
	if large > small*3 {
		t.Fatalf("doubling the source walked %d stored block nodes against %d -- over 3x,"+
			" so resolving a stored block's result is superlinear in the sites passing it again", large, small)
	}
}

// A body of one wide expression is one statement, so charging the budget per
// statement left it able to walk any number of nodes per site: the proc below
// rechecks every element of its array literal at every site that passes it
// (#10).
func wideExpressionStoredBlockSource(n int) string {
	elements := strings.TrimSuffix(strings.Repeat("1, ", n), ", ")
	return fmt.Sprintf(`
def f(items: array<int>)
  callback = proc do |index|
    [%s]
  end
%send
`, elements, strings.Repeat("  items.fill(&callback)\n", n))
}

func TestCheckWideExpressionStoredBlockWalksStayLinear(t *testing.T) {
	small := measureCheckAllocation(t, wideExpressionStoredBlockSource(400))
	large := measureCheckAllocation(t, wideExpressionStoredBlockSource(800))

	// Measured 1.4MB then 1.6MB. Charging the budget per statement instead, the
	// same pair allocated 19MB and 62MB while the budget recorded 400 and 800
	// units, since one statement is one unit however wide it is. This counts
	// allocated bytes because the point is what the repeated walks cost, not
	// what the budget believes they cost.
	if large > small*3 {
		t.Fatalf("doubling the source allocated %d bytes against %d -- over 3x, so a wide"+
			" expression in a stored block escapes the walk budget again", large, small)
	}
}

// The cap has to leave ordinary code alone: a proc of a few statements is
// walked for its result at thousands of sites before it binds, and every one of
// those sites still decides the receiver's element type exactly, which is what
// the append below contradicts.
func TestStoredBlockFillKeepsResultDiagnostics(t *testing.T) {
	source := func(sites int) string {
		return fmt.Sprintf(`
def f(items: array<int>)
  callback = proc do |index|
    1
  end
%s  items << true
end
`, strings.Repeat("  items.fill(&callback)\n", sites))
	}
	const want = "write to items expected element int, got bool"

	requireCheckWarning(t, compileScript(t, source(1)).CheckWarnings(), want)
	requireCheckWarning(t, compileScript(t, source(1000)).CheckWarnings(), want)

	// Past the cap the result reads as inexact, which weakens the receiver's
	// fact. That can only cost the diagnostic above, never produce another.
	warnings := compileScript(t, storedBlockFillSource(200)+"").CheckWarnings()
	if len(warnings) != 0 {
		t.Fatalf("a stored block past the walk cap reported %v", warnings)
	}
}

// rescuedStoredBlockFillSource repeats a fill of the same stored block inside a
// rescue so that a body which always raises still lets the next site be
// reached, which is what lets the sites accumulate against the walk cap before
// the unrescued fill at the end.
func rescuedStoredBlockFillSource(body, sites int, result string) string {
	var statements strings.Builder
	for i := range body {
		fmt.Fprintf(&statements, "    v%d = index + %d\n", i, i)
	}
	guarded := strings.Repeat(`  begin
    items.fill(&callback)
  rescue
    nil
  end
`, sites)
	return fmt.Sprintf(`
def take(v: int)
  v
end

def f()
  items = [1, 2, 3]
  callback = proc do |index|
%s    %s
  end
%s  items.fill(&callback)
  take("bad")
end
`, statements.String(), result, guarded)
}

// Declining the walk must report no more than performing it would. A stored
// block that always raises leaves the code after the fill unreachable, so the
// call to take is never diagnosed -- and a run of sites long enough to exhaust
// the walk cap must not make it reachable again by assuming the body it skipped
// completes (#10).
func TestStoredBlockWalkCapAddsNoDiagnostics(t *testing.T) {
	for _, result := range []string{`raise "boom"`, "1"} {
		t.Run(result, func(t *testing.T) {
			underCap := checkWarningMessages(compileScript(t, rescuedStoredBlockFillSource(10, 10, result)).CheckWarnings())
			overCap := checkWarningMessages(compileScript(t, rescuedStoredBlockFillSource(100, 100, result)).CheckWarnings())

			for _, message := range overCap {
				if !slices.Contains(underCap, message) {
					t.Fatalf("exhausting the walk cap reported %q, which the same shape under the"+
						" cap does not: %v against %v", message, overCap, underCap)
				}
			}
		})
	}

	// Exhausting the cap also weakens the receiver the fills write to, and a
	// weakened fact stops proving the branches it decided. The fills below are
	// rescued, so the branch after them is reachable whatever the cap did, and
	// it must not start reporting the call in its dead arm.
	branch := func(body, sites int) string {
		return strings.TrimSuffix(rescuedStoredBlockFillSource(body, sites, "1"), `  items.fill(&callback)
  take("bad")
end
`) + `  if items[0]
    1
  else
    take("bad")
  end
end
`
	}
	underCap := checkWarningMessages(compileScript(t, branch(10, 10)).CheckWarnings())
	overCap := checkWarningMessages(compileScript(t, branch(100, 100)).CheckWarnings())
	for _, message := range overCap {
		if !slices.Contains(underCap, message) {
			t.Fatalf("exhausting the walk cap reported %q on a branch the same shape under the"+
				" cap decides: %v against %v", message, overCap, underCap)
		}
	}

	// The raising body is the shape the cap could have made reachable, so pin
	// it directly rather than only as a subset.
	if messages := checkWarningMessages(compileScript(t, rescuedStoredBlockFillSource(100, 100, `raise "boom"`)).CheckWarnings()); len(messages) != 0 {
		t.Fatalf("code after a fill whose block always raises was diagnosed: %v", messages)
	}
}

// selfCallingLambdaSource builds a lambda body holding sites recursive
// `h.call` statements. The repeated-region ivar effect walk resolves each one
// back to the body it is written in, so the body is a region that reaches
// itself once per site.
func selfCallingLambdaSource(sites int) string {
	var body strings.Builder
	for range sites {
		body.WriteString("    h.call\n")
	}
	return fmt.Sprintf(`
def f()
  h = -> {
%s  }
  h.call
  0
end
`, body.String())
}

// A body that reaches itself is walked twice: once under the caller's facts and
// once under the state the recursive call left behind. Restoring the walk count
// when a nested walk returned handed that second walk back to every later site,
// so a body holding N recursive calls started N walks over the same N
// statements. CheckWarnings and CheckedCall run before any script step quota,
// so an embedder statically checking a tenant script paid it in full.
// This counts allocated bytes because the repeated walks are the allocation:
// each one pushes a block scope and collects the body's local bindings afresh.
func TestCheckReentrantIvarEffectWalkStaysLinear(t *testing.T) {
	small := measureCheckAllocation(t, selfCallingLambdaSource(400))
	large := measureCheckAllocation(t, selfCallingLambdaSource(800))

	// Measured 882KB then 1.7MB, a 1.97x step for a doubled body. Before, the
	// same pair allocated 28MB and 109MB, a 3.94x step. The assertion allows up
	// to 3x so it states the complexity rather than pinning byte counts that
	// ordinary checker changes would shift.
	if large > small*3 {
		t.Fatalf("doubling the recursive calls in one lambda body allocated %d bytes"+
			" against %d -- over 3x, so collecting a re-entrant region's ivar effects is"+
			" superlinear in the source", large, small)
	}
}

// yieldingSelfCallingLambdaSource builds a summarized function holding a lambda
// that reaches itself at sites call sites and yields at the end. The summary
// walk re-checks the whole body once per site to find the yields a recursive
// call can enable, so the body is walked once per site it holds.
func yieldingSelfCallingLambdaSource(sites int) string {
	var body strings.Builder
	for range sites {
		body.WriteString("    h.call\n")
	}
	return fmt.Sprintf(`
def f()
  h = -> {
%s    yield
  }
  h.call
  0
end

def g()
  f() do
    1
  end
end
`, body.String())
}

// The walk that lets a reachable yield poison a function summary re-enters the
// lambda body at every recursive call site, because a yield only a later site
// enables is reachable only from there. The count that bounds re-entry is
// restored when a nested walk returns and has to be, so a body holding N
// recursive calls walked its own N statements N times. CheckWarnings and
// CheckedCall run before any script step quota, so an embedder statically
// checking a tenant script paid that on a source well inside MaxSourceBytes:
// 320 recursive calls in one yielding body took 4m14s.
//
// What bounds it instead is that a re-entry can only ever record one thing, and
// recording it saturates the summary. This counts the statements the summary
// walks visit, which is the work itself rather than a proxy for it.
func TestCheckSummaryYieldReentryWalkStaysLinear(t *testing.T) {
	small := measureCheckWork(t, yieldingSelfCallingLambdaSource(40))
	large := measureCheckWork(t, yieldingSelfCallingLambdaSource(80))

	// Measured 164 then 324 statement visits, a 1.98x step for a doubled body.
	// Before, the same pair visited 3,362 and 13,122, a 3.90x step, and took
	// 608ms and 4.5s against 25ms and 97ms. The assertion allows up to 3x so it
	// states the complexity rather than pinning counts that ordinary checker
	// changes would shift.
	if large > small*3 {
		t.Fatalf("doubling the recursive calls in one yielding lambda body visited %d"+
			" statements against %d -- over 3x, so re-checking an invoked lambda for"+
			" summary yields is superlinear in the source", large, small)
	}
}
