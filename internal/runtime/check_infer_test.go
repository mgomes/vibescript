package runtime

import (
	"context"
	"strings"
	"testing"
)

// ADR-004: locals take the types of the expressions assigned to them, and the
// checker errors wherever known types contradict a typed boundary.

func TestCheckInferLocalBindingFlowsToTypedArgument(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run()
  value = "1"
  takes_int(value)
end
`)

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckInferAnnotatedParamFlowsToTypedArgument(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(name: string)
  takes_int(name)
end
`)

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckInferUnknownValuesArePermitted(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(input)
  body = JSON.parse(input)
  takes_int(body["count"])
  value = body["count"]
  takes_int(value)
end
`))
}

func TestCheckInferReassignmentConflict(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  name = "Mauricio"
  count = 1
  count = name
end
`)

	requireCheckWarningContains(t, script, "reassignment of count expected int, got string")
}

func TestCheckInferReassignmentAllowances(t *testing.T) {
	t.Parallel()

	// nil is the neutral initializer, numeric retyping widens, container
	// re-initialization stays legal, and unknowns never conflict.
	requireNoCheckWarnings(t, compileScript(t, `
def run(input)
  a = nil
  a = "fresh"
  a = nil
  b = 1
  b = 2.5
  c = {}
  c = { name: "x" }
  d = JSON.parse(input)
  d = 1
end
`))
}

func TestCheckInferCompoundAssignment(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def run()
  total = 0
  total += 1
  total += 2.5
  greeting = "hi"
  greeting += " there"
end
`))

	script := compileScript(t, `
def run()
  greeting = "hi"
  greeting -= 1
end
`)
	requireCheckWarningContains(t, script, "unsupported subtraction operands string and int")
}

func TestCheckInferBranchAssignmentsMergeIntoUnions(t *testing.T) {
	t.Parallel()

	// x is int | string after the branches, which overlaps both int and
	// string boundaries, so neither call is a known contradiction.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag)
  if flag
    x = 1
  else
    x = "s"
  end
  takes_int(x)
end
`))
}

func TestCheckInferBranchOnlyAssignmentJoinsWithNil(t *testing.T) {
	t.Parallel()

	// x is int | nil after the if (branch locals predeclare as nil), which
	// is disjoint from string.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(flag)
  if flag
    x = 1
  end
  takes_string(x)
end
`)

	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int | nil")
}

func TestCheckInferLogicalStatementJoinsRightHandFacts(t *testing.T) {
	t.Parallel()

	// The right-hand side of a statement-level and/or may be skipped, so a
	// local bound there is type-or-nil afterwards, which still contradicts
	// a disjoint boundary.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(flag)
  flag and x = 1
  takes_string(x)
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int | nil")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def run(flag)
  flag and x = "s"
  takes_string(x)
end
`))
}

func TestCheckInferImplicitReturnHonorsInferredShortCircuit(t *testing.T) {
	t.Parallel()

	// x is a known string, so `x or 1` always returns x and the unreachable
	// int alternative must not report.
	requireNoCheckWarnings(t, compileScript(t, `
def run -> string
  x = "ok"
  x or 1
end
`))

	// An undecided left keeps both sides checked.
	script := compileScript(t, `
def run(flag) -> string
  x = flag
  x or 1
end
`)
	requireCheckWarningContains(t, script, "return value expected string, got int")
}

func TestCheckInferForcedLogicalRightSkipsImpossiblePath(t *testing.T) {
	t.Parallel()

	// A nil left forces `or` to evaluate the right side, so the impossible
	// skipped path (x still nil) must not corrupt the implicit return.
	requireNoCheckWarnings(t, compileScript(t, `
def run -> int
  x = nil
  x or x = 1
end
`))

	// The forced right side's own contradiction still reports.
	script := compileScript(t, `
def run -> int
  x = nil
  x or x = "s"
end
`)
	requireCheckWarningContains(t, script, "return value expected int, got string")
}

func TestCheckInferReturnTypeContradiction(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(x: int) -> string
  x + 1
end
`)
	requireCheckWarningContains(t, script, "return value expected string, got int")

	explicit := compileScript(t, `
def run(x: int) -> string
  return x + 1
end
`)
	requireCheckWarningContains(t, explicit, "return value expected string, got int")
}

func TestCheckInferKnownCallReturnTypeFlows(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def build_count() -> int
  1
end

def takes_string(value: string)
  value
end

def run()
  count = build_count()
  takes_string(count)
end
`)

	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

func TestCheckInferOperatorRejections(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "addition int and nil",
			source: `
def run()
  x = 1
  x + nil
end
`,
			warning: "unsupported addition operands int and nil",
		},
		{
			name: "subtraction on strings",
			source: `
def run()
  a = "x"
  a - "y"
end
`,
			warning: "unsupported subtraction operands string and string",
		},
		{
			name: "comparison across kinds",
			source: `
def run()
  a = "x"
  a < 1
end
`,
			warning: "unsupported comparison operands string and int",
		},
		{
			name: "unary minus on string",
			source: `
def run()
  a = "x"
  -a
end
`,
			warning: "unsupported unary - operand string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScript(t, tc.source), tc.warning)
		})
	}
}

func TestCheckInferOperatorAllowances(t *testing.T) {
	t.Parallel()

	// Mirrors the runtime kind matrix: string concatenation coerces either
	// side, logical operators pass values through, equality is universal,
	// and unknown operands stay silent.
	requireNoCheckWarnings(t, compileScript(t, `
def run(input)
  a = "n=" + 1
  b = 1 == "1"
  c = input || "default"
  d = [1] + [2]
  e = 2 ** -1
  f = input + 1
  [a, b, c, d, e, f]
end
`))
}

func TestCheckInferLoopBodiesDegradeAssignedLocals(t *testing.T) {
	t.Parallel()

	// x changes type across iterations; the checker must not pin the
	// first-iteration type inside or after the loop.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag)
  x = 1
  while flag
    x = "s"
    flag = false
  end
  takes_int(x)
end
`))
}

func TestCheckInferUnreachableShortCircuitRightIsNotChecked(t *testing.T) {
	t.Parallel()

	// An annotated always-truthy left short-circuits ||, so the right side
	// never runs and must not produce diagnostics.
	requireNoCheckWarnings(t, compileScript(t, `
def name -> string
  "n"
end

def str -> string
  "s"
end

def takes_int(value: int)
  value
end

def run()
  name() || takes_int(str())
end
`))

	// With && the truthy left makes the right side always run, so its
	// contradiction still reports.
	script := compileScript(t, `
def name -> string
  "n"
end

def str -> string
  "s"
end

def takes_int(value: int)
  value
end

def run()
  name() && takes_int(str())
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckInferLoopBodiesDegradeMutatedContainers(t *testing.T) {
	t.Parallel()

	// The body mutates the container in place, so a read earlier in the
	// body must not use the pre-loop field fact (a later iteration observes
	// the mutation).
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string }, flag)
  while flag
    takes_int(user["name"])
    user["name"] = 1
    flag = false
  end
end
`))

	// A member call later in the body may mutate the container, so an
	// earlier read must not use the pre-region fact either.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string }, flag)
  while flag
    takes_int(user["name"])
    user.delete("name")
    flag = false
  end
end
`))

	// Without a mutation in the body the pre-loop fact stays checkable.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string }, flag)
  while flag
    takes_int(user["name"])
    flag = false
  end
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string | nil")
}

func TestCheckInferLoopHeadersSeePreLoopFacts(t *testing.T) {
	t.Parallel()

	// The iterable evaluates once before any body iteration, so its facts
	// (and the element type they yield) survive a body reassignment of the
	// iterable local.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(items: array<int>)
  for item in items
    items = []
    takes_string(item)
  end
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")

	// A while condition's first evaluation also sees pre-loop facts.
	condition := compileScript(t, `
def run()
  x = nil
  while x > 1
    x = 1
  end
end
`)
	requireCheckWarningContains(t, condition, "unsupported comparison operands nil and int")
}

func TestCheckInferDeferredEnsureReturnsUseBranchTypes(t *testing.T) {
	t.Parallel()

	// A return value is evaluated before the ensure runs, so the deferred
	// check must not see the ensure's reassignment (nil keeps the ensure
	// itself free of reassignment diagnostics).
	requireNoCheckWarnings(t, compileScript(t, `
def run -> int
  begin
    x = 1
    return x
  ensure
    x = nil
  end
end
`))

	// The inverse still errors: the branch's own type contradicts the
	// annotated return even though the ensure would retype it.
	script := compileScript(t, `
def run -> int
  begin
    x = "value"
    return x
  ensure
    x = 1
  end
end
`)
	requireCheckWarningContains(t, script, "return value expected int, got string")
}

func TestCheckInferDeferredEnsureReturnsKeepBranchLocalFacts(t *testing.T) {
	t.Parallel()

	// Both branches exit, so the branch merge discards their facts before
	// the ensure walk; the deferred check must still see the state each
	// return was evaluated under.
	script := compileScript(t, `
def run(flag) -> int
  begin
    if flag
      x = 1
      return x
    else
      x = "s"
      return x
    end
  ensure
    puts "done"
  end
end
`)
	requireCheckWarningContains(t, script, "return value expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def run(flag) -> int
  begin
    if flag
      x = 1
      return x
    else
      x = 2
      return x
    end
  ensure
    puts "done"
  end
end
`))
}

func TestCheckInferEnsureSeesReturningBranchFacts(t *testing.T) {
	t.Parallel()

	// The ensure block runs in the returning branch's environment, so its
	// boundary checks use the facts captured at the return site.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run -> int
  begin
    x = "s"
    return 1
  ensure
    takes_int(x)
  end
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run -> int
  begin
    x = 2
    return 1
  ensure
    takes_int(x)
  end
end
`))
}

func TestCheckInferImplicitReturnsKeepBranchLocalFacts(t *testing.T) {
	t.Parallel()

	// The else branch always yields a string; the branch merge must not
	// dilute that into int | string before the implicit-return check.
	script := compileScript(t, `
def run(flag) -> int
  if flag
    x = 1
    x
  else
    x = "s"
    x
  end
end
`)
	requireCheckWarningContains(t, script, "return value expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def run(flag) -> int
  if flag
    x = 1
    x
  else
    x = 2
    x
  end
end
`))
}

func TestCheckInferNestedEnsuresSeeReturnPathFacts(t *testing.T) {
	t.Parallel()

	// A return escaping through both ensures carries its facts to the
	// outer ensure walk too.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(flag) -> int
  begin
    begin
      x = 1
      return 2
    ensure
      puts "inner"
    end
  ensure
    takes_string(x)
  end
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def run(flag) -> int
  begin
    begin
      x = "ok"
      return 2
    ensure
      puts "inner"
    end
  ensure
    takes_string(x)
  end
end
`))
}

func TestCheckInferForLoopElementTypes(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(items: array<int>)
  for item in items
    takes_string(item)
  end
end
`)

	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

func TestCheckInferBlockBodiesDegradeAssignedOuterLocals(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run()
  x = 1
  [1, 2].each do |item|
    x = "s"
  end
  takes_int(x)
end
`))
}

func TestCheckInferShadowingBlockParamsKeepOuterFacts(t *testing.T) {
	t.Parallel()

	// The block's x shadows the outer local, so body assignments to it never
	// write through and the outer fact still contradicts the boundary.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run()
  x = 1
  [0].each do |x|
    x = "s"
  end
  takes_string(x)
end
`)

	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

func TestCheckInferShapeParamFieldTypes(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string, age: int })
  takes_int(user["name"])
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string, age: int })
  takes_int(user["age"])
end
`))
}

func TestCheckInferShapeUnknownFieldReadsNil(t *testing.T) {
	t.Parallel()

	// Shapes are exact, so a key outside the shape is known to read nil.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(user: { name: string })
  takes_string(user["username"])
end
`)

	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got nil")
}

func TestCheckInferShovelAppendsWitnessElements(t *testing.T) {
	t.Parallel()

	// The shovel operator appends in place, so the appended element joins
	// the array's witnessed elements.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  values = [1]
  values << "bad"
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")

	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run(extra)
  values = [1]
  values << 2
  ints(values)
  mixed = [1]
  mixed << extra
  ints(mixed)
end
`))
}

func TestCheckInferConditionalEvaluationJoinsMutationFacts(t *testing.T) {
	t.Parallel()

	// A safe-navigation receiver may skip the arguments entirely, so an
	// append inside them holds on only one path and is not a known
	// contradiction afterwards.
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run(obj)
  values = [1]
  obj&.record(values << "bad")
  ints(values)
end
`))

	// A short-circuited right operand may not run at all.
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run(flag)
  values = [1]
  flag && (values << "bad")
  ints(values)
end
`))

	// An unconditional append still contradicts the boundary.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run(obj)
  values = [1]
  obj.record(values << "bad")
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")
}

func TestCheckInferShovelArgumentCarriesMutatedFact(t *testing.T) {
	t.Parallel()

	// The shovel expression returns its mutated receiver, so passing it
	// directly as an argument carries the post-append witnesses.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  values = [1]
  ints(values << "bad")
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")

	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  values = [1]
  ints(values << 2)
end
`))
}

func TestCheckInferReturnSeesExpressionSideEffects(t *testing.T) {
	t.Parallel()

	// The returned expression's own append applies before the value leaves
	// the function.
	script := compileScript(t, `
def run -> array<int>
  values = [1]
  return values << "x"
end
`)
	requireCheckWarningContains(t, script, "return value expected array<int>, got array<int | string>")

	requireNoCheckWarnings(t, compileScript(t, `
def run -> array<int>
  values = [1]
  return values << 2
end
`))
}

func TestCheckInferWordOperatorsShortCircuitInExpressions(t *testing.T) {
	t.Parallel()

	// Word-form and/or short-circuit exactly like &&/||, so a conditional
	// append inside an expression joins with the skipped path.
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run(flag)
  values = [1]
  (flag and (values << "bad"))
  ints(values)
end
`))

	// A statically truthy left keeps the append unconditional.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  values = [1]
  (true and (values << "bad"))
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")
}

func TestCheckInferRegionShovelsPoisonReceivers(t *testing.T) {
	t.Parallel()

	// An append inside a block or loop body survives the region's state
	// restore as a poison: the outer witnessed fact no longer holds in
	// either direction.
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def strings(values: array<string>)
  values
end

def run(flag)
  a = [1]
  [0].each do |i|
    a << "bad"
  end
  ints(a)
  strings(a)

  b = [1]
  while flag
    b << "bad"
    flag = false
  end
  ints(b)
  strings(b)
end
`))
}

func TestCheckInferShovelOnLiteralReceiversCarriesAppend(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  xs = [1] << "bad"
  ints(xs)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")

	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  xs = [1] << 2
  ints(xs)
end
`))
}

func TestCheckInferShortCircuitDecidedByKnownLeft(t *testing.T) {
	t.Parallel()

	// A truthy left operand means || always yields the left value, so the
	// int alternative never dilutes the fact.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run()
  x = "s" || 1
  takes_int(x)
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	// Type-level truthiness decides too: an annotated string return is
	// always truthy.
	typed := compileScript(t, `
def name -> string
  "n"
end

def takes_int(value: int)
  value
end

def run()
  x = name() || "default"
  takes_int(x)
end
`)
	requireCheckWarningContains(t, typed, "call to takes_int argument value expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def run(maybe)
  x = maybe || "default"
  takes_string(x)
  y = nil || "fallback"
  takes_string(y)
end
`))
}

func TestCheckInferShapeValuesAreTruthy(t *testing.T) {
	t.Parallel()

	// Shape values are always truthy, so || short-circuits and the right
	// side never produces diagnostics.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run()
  schema = { name: string }
  schema || takes_int("bad")
end
`))
}

func TestCheckInferKeywordRestKeyKinds(t *testing.T) {
	t.Parallel()

	// Rest keywords bind as a string-keyed hash, so a symbol key type
	// always fails at call binding even with compatible values.
	script := compileScript(t, `
def accept(**opts: hash<symbol, int>)
  opts
end

def run()
  v = 1
  accept(limit: v)
end
`)
	requireCheckWarningContains(t, script, "call to accept argument opts expected hash<symbol, int>, got string-keyed keywords")

	// Shape-annotated keyword rests check per field: unknown keywords fail
	// the exact shape, known ones check their field types.
	extra := compileScript(t, `
def accept(**opts: { limit: int })
  opts
end

def run()
  v = 1
  accept(limit: v, extra: v)
end
`)
	requireCheckWarningContains(t, extra, "call to accept argument opts expected { limit: int }, got keyword extra")

	field := compileScript(t, `
def accept(**opts: { limit: int })
  opts
end

def run()
  v = "slow"
  accept(limit: v)
end
`)
	requireCheckWarningContains(t, field, "call to accept argument opts expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def accept(**opts: { limit: int })
  opts
end

def strings(**opts: hash<string, int>)
  opts
end

def run(dynamic)
  v = 1
  accept(limit: v)
  strings(limit: v)
  strings(limit: dynamic)
end
`))

	// Exact shapes require every field even in the inferred fallback.
	missing := compileScript(t, `
def accept(**opts: { a: int, b: int })
  opts
end

def run()
  x = 1
  accept(a: x)
end
`)
	requireCheckWarningContains(t, missing, "call to accept argument opts expected { a: int, b: int }, missing keyword b")
}

func TestCheckInferRestArgumentsUseInferredFacts(t *testing.T) {
	t.Parallel()

	rest := compileScript(t, `
def collect(*items: array<int>)
  items
end

def run()
  x = "bad"
  collect(1, x)
end
`)
	requireCheckWarningContains(t, rest, "call to collect argument items expected int, got string")

	kwrest := compileScript(t, `
def accept(**opts: hash<string, int>)
  opts
end

def run()
  value = "bad"
  accept(limit: value)
end
`)
	requireCheckWarningContains(t, kwrest, "call to accept argument opts expected int, got string")

	requireNoCheckWarnings(t, compileScript(t, `
def collect(*items: array<int>)
  items
end

def accept(**opts: hash<string, int>)
  opts
end

def run(dynamic)
  n = 2
  collect(1, n)
  collect(1, dynamic)
  limit = 3
  accept(limit: limit)
  accept(limit: dynamic)
end
`))
}

func TestCheckInferEarlierArgumentsKeepPreMutationFacts(t *testing.T) {
	t.Parallel()

	// The first argument evaluates before the second's mutation, so its
	// contract violation is provable even though the container's facts are
	// poisoned afterwards.
	script := compileScript(t, `
def accept(a: int, b)
  [a, b]
end

def run()
  h = { name: "x" }
  accept(h[:name], h.delete(:name))
end
`)

	requireCheckWarningContains(t, script, "call to accept argument a expected int, got string")
}

func TestCheckInferMutatingArgumentPoisonsSiblingArguments(t *testing.T) {
	t.Parallel()

	// Arguments evaluate left to right before the call dispatches, so a
	// mutating earlier argument invalidates the container's facts for the
	// later arguments' checks.
	requireNoCheckWarnings(t, compileScript(t, `
def accept(a, b: int)
  [a, b]
end

def run()
  h = { name: "x" }
  accept(h.delete(:name), h[:name])
end
`))
}

func TestCheckInferEscapedNestedContainerPoisonsRoot(t *testing.T) {
	t.Parallel()

	// Passing a nested container by reference lets the callee mutate it, so
	// the root's field facts stop holding.
	requireNoCheckWarnings(t, compileScript(t, `
def mutate(profile)
  profile["name"] = 1
end

def takes_int(value: int)
  value
end

def run(user: { profile: { name: string } })
  mutate(user["profile"])
  takes_int(user["profile"]["name"])
end
`))

	// A projected scalar cannot be mutated in place, so the root keeps its
	// facts and the boundary check still fires.
	script := compileScript(t, `
def consume(value)
  value
end

def takes_int(value: int)
  value
end

def run(user: { name: string })
  consume(user["name"])
  takes_int(user["name"])
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckInferLiteralArrayElementsFlowThroughBindings(t *testing.T) {
	t.Parallel()

	// Every element of a literal is a witness, so a binding cannot hide a
	// contradicting element from a typed array boundary.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run()
  values = [1, "bad"]
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")

	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run(extra)
  values = [1, 2]
  ints(values)
  mixed = [1, extra]
  ints(mixed)
  empty = []
  ints(empty)
  reinit = [1]
  reinit = ["a"]
  reinit
end
`))
}

func TestCheckInferShapeContradictsGenericHashBoundary(t *testing.T) {
	t.Parallel()

	// Shapes witness every field, so a field type that contradicts a
	// generic hash's value type cannot hide behind a binding.
	script := compileScript(t, `
def configure(opts: hash<string, int>)
  opts
end

def run()
  opts = { limit: "slow" }
  configure(opts)
end
`)
	requireCheckWarningContains(t, script, "call to configure argument opts expected hash<string, int>, got { limit: string }")

	// A string-keyed literal with compatible values satisfies the boundary;
	// a label-keyed literal is symbol-keyed and now contradicts the string
	// key type just as the runtime does.
	requireNoCheckWarnings(t, compileScript(t, `
def configure(opts: hash<string, int>)
  opts
end

def run()
  opts = { "limit" => 3 }
  configure(opts)
  configure({})
end
`))
}

func TestCheckInferPartialArrayWitnesses(t *testing.T) {
	t.Parallel()

	// A known bad element stays a witness even when siblings are unknown.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run(dynamic)
  ints([dynamic, "bad"])
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>")

	// The partial union is not an element bound: indexing and iteration
	// make no claims about the unknown members.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def ints(values: array<int>)
  values
end

def run(dynamic)
  xs = [dynamic, 1]
  takes_string(xs[0])
  ints([dynamic, 2])
end
`))
}

func TestCheckInferHostArgumentsSeedParams(t *testing.T) {
	t.Parallel()

	// A concrete host argument gives an unannotated parameter a fact, so
	// per-call checks catch the guaranteed failure.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(x)
  takes_int(x)
end
`)
	warnings := script.CheckWarningsForCall("run", []Value{NewString("bad")}, CallOptions{})
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want host-argument contradiction", warnings)
	}

	// A compatible argument stays silent.
	if warnings := script.CheckWarningsForCall("run", []Value{NewInt(3)}, CallOptions{}); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForCall() = %v, want none", warnings)
	}

	// An omitted argument binds the default's inferred type: that is the
	// value the runtime will use.
	defaulted := compileScript(t, `
def takes_int(value: int)
  value
end

def run(x = "bad")
  takes_int(x)
end
`)
	warnings = defaulted.CheckWarningsForCall("run", nil, CallOptions{})
	found = false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want default-value contradiction", warnings)
	}
	if warnings := defaulted.CheckWarningsForCall("run", []Value{NewInt(3)}, CallOptions{}); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForCall() with argument = %v, want none", warnings)
	}
}

func TestCheckInferShapeFactsRespectKeyKinds(t *testing.T) {
	t.Parallel()

	// A label-keyed hash literal is a symbol-keyed store: a string lookup is
	// known to miss (reads nil), a symbol lookup yields the exact field.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run()
  h = { name: "Ada" }
  takes_string(h["name"])
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got nil")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def run()
  h = { name: "Ada" }
  takes_string(h[:name])
end
`))

	// JSON stores are string-keyed, so a symbol lookup on a parse_as result
	// is known to miss.
	parseAs := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string })
  takes_string(body[:name])
end
`)
	requireCheckWarningContains(t, parseAs, "call to takes_string argument value expected string, got nil")

	// An annotated shape parameter has an unknown key representation: a
	// present field reads as field-or-nil, so it still contradicts a
	// disjoint boundary but never over-claims the field type.
	param := compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string })
  takes_int(user["name"])
end
`)
	requireCheckWarningContains(t, param, "call to takes_int argument value expected int, got string | nil")
}

func TestCheckInferBranchJoinsKeepDistinctMarkers(t *testing.T) {
	t.Parallel()

	// Two branches assigning the same displayed shape with different key
	// representations must not collapse to one marker: h["name"] hits in
	// the string-keyed branch, so the call is not a known contradiction.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def run(flag)
  if flag
    h = { name: "Ada" }
  else
    h = { "name" => "Ada" }
  end
  takes_string(h["name"])
end
`))

	// A witnessed literal array joined with an annotation-typed array keeps
	// both arms: the second branch's value could be empty, so the boundary
	// is not provably violated.
	requireNoCheckWarnings(t, compileScript(t, `
def build -> array<int>
  []
end

def strings(values: array<string>)
  values
end

def run(flag)
  if flag
    xs = [1]
  else
    xs = build
  end
  strings(xs)
end
`))
}

func TestCheckInferNestedMarkersSurviveBranchJoins(t *testing.T) {
	t.Parallel()

	// The arms render identically and share a top-level string-key marker,
	// but their nested profile shapes differ in key representation; joining
	// them must not collapse to the symbol-keyed arm and claim the lookup
	// reads nil.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string, flag)
  if flag
    h = { "profile" => { name: "Ada" } }
  else
    h = JSON.parse_as(raw, { profile: { name: string } })
  end
  takes_string(h["profile"]["name"])
end
`))
}

func TestCheckInferAliasMutationsPoisonTheGroup(t *testing.T) {
	t.Parallel()

	// Containers assign by reference: mutating through an alias must drop
	// the original's precise facts too, so no stale index claim survives.
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def takes_string(value: string)
  value
end

def run()
  values = [1]
  alias = values
  alias << "bad"
  ints(values)
  ints(alias)
  takes_string(values[1])
end
`))

	// A copy without mutation keeps the shared fact checkable through both
	// names.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { age: int })
  body2 = body
  takes_string(body2["age"])
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

func TestCheckInferProjectedContainerAliases(t *testing.T) {
	t.Parallel()

	// child shares the nested container inside user, so mutating through
	// child drops user's stale nested field claim (which would otherwise
	// contradict a boundary the runtime accepts).
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  user = JSON.parse_as(raw, { child: { name: string } })
  child = user["child"]
  child["name"] = 1
  takes_int(user["child"]["name"])
end
`))

	// Without the mutation the nested claim stays checkable.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  user = JSON.parse_as(raw, { child: { name: string } })
  child = user["child"]
  takes_int(user["child"]["name"])
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckInferMutationPoisonsContainerFacts(t *testing.T) {
	t.Parallel()

	// An index write or member call may restructure the container, so its
	// shape facts must stop holding (no stale-field diagnostics afterwards).
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string })
  user["name"] = 42
  takes_int(user["name"])
end
`))

	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string })
  user.delete("name")
  takes_int(user["name"])
end
`))
}

func TestCheckInferHashLiteralShapes(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def create_user(user: { name: string })
  user
end

def run()
  create_user({ username: "typo" })
end
`)
	requireCheckWarningContains(t, script, "call to create_user argument user expected { name: string }, got { username: string }")

	requireNoCheckWarnings(t, compileScript(t, `
def create_user(user: { name: string })
  user
end

def run(name)
  create_user({ name: name })
end
`))
}

func TestCheckInferConflictsStillExecuteDynamically(t *testing.T) {
	t.Parallel()

	// The reassignment rule is a check-path contract only: the same program
	// still runs under the dynamic runtime semantics.
	script := compileScript(t, `
def run()
  count = 1
  count = "one"
  count
end
`)

	requireCheckWarningContains(t, script, "reassignment of count expected int, got string")
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "one" {
		t.Fatalf("run() = %#v, want \"one\"", got)
	}
}
