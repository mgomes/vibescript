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

// sharedFactDagSource builds a fact that binds the same node under both keys at
// every level, so levels lines produce a fact with levels nodes and 2^levels
// paths through them, then hands two independently built copies of it to the
// exact comparison: the branch below joins the two, and the join's arm dedup
// has to confirm they are the same fact before dropping one. Nothing shares a
// node between the two, so the pointer-identity shortcut never fires and every
// pair has to be compared on its own.
func sharedFactDagSource(levels int) string {
	build := func(name string) string {
		var b strings.Builder
		fmt.Fprintf(&b, "  %s = { a: 1, b: 1 }\n", name)
		for range levels {
			fmt.Fprintf(&b, "  %s = { a: %s, b: %s }\n", name, name, name)
		}
		return b.String()
	}
	return fmt.Sprintf(`
def f(flag)
%s%s  h = flag ? x : y
  h
end
`, build("x"), build("y"))
}

// A fact is a DAG, not a tree. Walking one as a tree reaches the same left/right
// child pair once per path rather than once per pair, so comparing two
// independently built copies of a fact that shares its nodes cost 2^levels
// comparisons: 20 lines took 2,097,151 of them and every line after that
// doubled it, inside CheckWarnings where no script step or memory quota applies.
// Only pairs that came back equal can be reached twice -- the first difference
// ends the whole comparison -- so remembering those walks the DAG as a DAG.
func TestExactFactComparisonWalksSharedNodesOnce(t *testing.T) {
	small := measureCheckWork(t, sharedFactDagSource(10))
	large := measureCheckWork(t, sharedFactDagSource(20))

	// Measured 65 then 67 node-pair comparisons, a 1.03x step for ten more
	// levels of sharing. Before, the same pair compared 2,047 and 2,097,151, a
	// 1,024x step. The assertion allows up to 3x so it states the complexity
	// rather than pinning counts that ordinary checker changes would shift.
	if large > small*3 {
		t.Fatalf("ten more levels of shared-node nesting compared %d node pairs against"+
			" %d -- over 3x, so the exact fact comparison walks a DAG as a tree", large, small)
	}
}

// sharedFactDag builds the same shape the source above does, directly, so the
// exactness of the memo can be pinned without going through inference.
func sharedFactDag(levels int, leaf *TypeExpr) *TypeExpr {
	node := leaf
	for range levels {
		node = &TypeExpr{
			Kind:  TypeShape,
			Name:  shapeKeysSymbolMarker,
			Shape: map[string]*TypeExpr{"a": node, "b": node},
		}
	}
	return node
}

// The memo short-circuits a pair it has already proved equal, so it must record
// only pairs that are equal and must not let a difference below it pass. The
// equal case also has to prove the memo engaged at all: without it the same
// comparison costs 2^levels, so a visit count near the level count is what says
// the DAG was walked as a DAG rather than that the facts were small.
func TestExactFactComparisonKeepsSharedNodesExact(t *testing.T) {
	const levels = 24

	var walk typeFactWalk
	if !typeFactsIdenticalCounted(
		sharedFactDag(levels, checkTypeInt),
		sharedFactDag(levels, checkTypeInt),
		&walk,
	) {
		t.Fatalf("two independently built copies of the same %d-level fact compared unequal", levels)
	}
	if walk.visited > maxUnmemoizedFactPairVisits+levels {
		t.Fatalf("comparing two %d-level shared-node facts visited %d pairs, so the memo"+
			" never engaged and the assertion above proves nothing about a DAG", levels, walk.visited)
	}

	if typeFactsIdentical(
		sharedFactDag(levels, checkTypeInt),
		sharedFactDag(levels, checkTypeString),
	) {
		t.Fatalf("a %d-level fact differing only at its leaf compared equal", levels)
	}

	// A difference reachable through one key but not the other is what a memo
	// that recorded pairs before proving them would let through.
	left := sharedFactDag(levels, checkTypeInt)
	right := sharedFactDag(levels, checkTypeInt)
	right.Shape["b"] = sharedFactDag(levels-1, checkTypeString)
	if typeFactsIdentical(left, right) {
		t.Fatalf("a %d-level fact differing under one key of the root compared equal", levels)
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
