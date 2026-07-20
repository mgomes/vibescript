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

	// Statement-expressions traverse too: a mutation inside a begin/end
	// used as a value still degrades the container for the earlier read.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(user: { name: string }, flag)
  while flag
    takes_int(user["name"])
    y = begin
      user.delete("name")
      1
    end
    flag = y == 2
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

func TestCheckInferIfExpressionStaticConditionsPickReachableArms(t *testing.T) {
	t.Parallel()

	// A statically true condition decides the chain, so unreachable arms
	// must not widen the union and mask the contradiction.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  value = if true
    "x"
  else
    1
  end
  takes_int(value)
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	// A statically false arm drops out of the union entirely.
	falseArm := compileScript(t, `
def takes_string(value: string)
  value
end

def run
  value = if false
    "x"
  else
    1
  end
  takes_string(value)
end
`)
	requireCheckWarningContains(t, falseArm, "call to takes_string argument value expected string, got int")

	// A statically true elsif ends the chain: the else arm is unreachable,
	// so the union is the open condition's arm plus the deciding arm.
	elsifDecided := compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag)
  value = if flag
    "a"
  elsif true
    :sym
  else
    1
  end
  takes_int(value)
end
`)
	requireCheckWarningContains(t, elsifDecided, "call to takes_int argument value expected int, got string | symbol")

	// An unknown condition keeps every arm in the union: the overlap with
	// int stays permitted.
	unknown := compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag)
  value = if flag
    "x"
  else
    1
  end
  takes_int(value)
end
`)
	requireNoCheckWarnings(t, unknown)
}

func TestCheckInferConditionArmsPruneByInferredTruthiness(t *testing.T) {
	t.Parallel()

	// A local with a definitely-truthy inferred type (a shape value here)
	// decides the if expression, so the unreachable else arm must not
	// widen the union and mask the contradiction.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  schema = { name: string }
  value = if schema
    "bad"
  else
    1
  end
  takes_int(value)
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	// A nil-only condition proves the else arm runs instead.
	nilOnly := compileScript(t, `
def takes_string(value: string)
  value
end

def run
  flag = nil
  value = if flag
    "ok"
  else
    1
  end
  takes_string(value)
end
`)
	requireCheckWarningContains(t, nilOnly, "call to takes_string argument value expected string, got int")

	// Ternaries prune the same way.
	ternary := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  name = "n"
  takes_int(name ? "bad" : 1)
end
`)
	requireCheckWarningContains(t, ternary, "call to takes_int argument value expected int, got string")

	// A bool condition stays undecided: both arms join the union and the
	// overlap with int is permitted.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  value = if flag
    "x"
  else
    1
  end
  takes_int(value)
end
`))
}

func TestCheckInferDecidedExitsGateBlockLevelPaths(t *testing.T) {
	t.Parallel()

	// A begin body that provably exits never runs its else branch, so the
	// dead branch must not report.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run
  flag = "yes"
  begin
    if flag
      return 1
    end
  rescue
    nil
  else
    takes_int("bad")
  end
end
`))

	// Branch fallthrough decisions propagate: when both arms exit (one by
	// inferred decision), the statement after the if is unreachable.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(cond)
  flag = "yes"
  if cond
    if flag
      return 1
    end
  else
    return 2
  end
  takes_int("bad")
end
`))

	// An undecided body keeps the else branch reachable.
	undecided := compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  begin
    if flag
      return 1
    end
  rescue
    nil
  else
    takes_int("bad")
  end
end
`)
	requireCheckWarningContains(t, undecided, "call to takes_int argument value expected int, got string")
}

func TestCheckInferImplicitReturnsPruneDecidedArms(t *testing.T) {
	t.Parallel()

	// A nil-only condition proves the else arm is the implicit return, so
	// the dead string arm must not report against the annotation.
	requireNoCheckWarnings(t, compileScript(t, `
def run -> int
  x = nil
  if x
    "bad"
  else
    1
  end
end
`))

	// A definitely-truthy condition proves the consequent, and the missing
	// else is unreachable: no implicit-nil report.
	requireNoCheckWarnings(t, compileScript(t, `
def run -> int
  flag = "yes"
  if flag
    1
  end
end
`))

	// An undecided condition keeps both arms in the implicit return.
	undecided := compileScript(t, `
def run(flag: bool) -> int
  if flag
    "bad"
  else
    1
  end
end
`)
	requireCheckWarningContains(t, undecided, "return value expected int, got string")
}

func TestCheckInferDecidedExitsStopStatementLists(t *testing.T) {
	t.Parallel()

	// A decided condition whose branch always exits ends the enclosing
	// list: the dead tail must not report.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run
  flag = "yes"
  if flag
    return 1
  end
  takes_int("bad")
end
`))

	// A decided elsif ends an undecided chain the same way when every
	// reachable branch exits.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(other)
  flag = "yes"
  if other
    return 2
  elsif flag
    return 1
  end
  takes_int("bad")
end
`))

	// An undecided condition keeps the tail reachable.
	undecided := compileScript(t, `
def takes_int(value: int)
  value
end

def run(flag: bool)
  if flag
    return 1
  end
  takes_int("bad")
end
`)
	requireCheckWarningContains(t, undecided, "call to takes_int argument value expected int, got string")

	// A decided branch that falls through keeps the tail reachable too.
	openBranch := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  flag = "yes"
  if flag
    x = 1
  end
  takes_int("bad")
end
`)
	requireCheckWarningContains(t, openBranch, "call to takes_int argument value expected int, got string")
}

func TestCheckInferStatementBranchesPruneByInferredTruthiness(t *testing.T) {
	t.Parallel()

	// A definitely-truthy condition makes the else branch unreachable, so
	// its contradictions (bad reassignment, bad return) must not report.
	requireNoCheckWarnings(t, compileScript(t, `
def run(flag: bool) -> int
  guard = "yes"
  x = 1
  if guard
    x = 2
    return x
  else
    x = "bad"
    return "bad"
  end
end
`))

	// The unreachable arm also stays out of the branch merge, so the
	// post-if fact keeps its precision.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run
  guard = "yes"
  x = 1
  if guard
    x = 2
  else
    x = "bad"
  end
  takes_string(x)
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")

	// A nil-only condition proves the consequent never runs.
	requireNoCheckWarnings(t, compileScript(t, `
def run -> int
  guard = nil
  x = 1
  if guard
    x = "bad"
  end
  x
end
`))

	// If-expression arms prune at the walk level too: an unreachable arm
	// must not produce call diagnostics.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run
  guard = "yes"
  value = if guard
    1
  else
    takes_int("bad")
  end
  value
end
`))
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

func TestCheckInferOptionalShapeFieldReadsNullable(t *testing.T) {
	t.Parallel()

	// Reading an optional field of a known-representation shape infers the
	// field type joined with nil: the field may be absent.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string, age?: int })
  takes_string(body["age"])
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int | nil")

	// A required field of the same shape stays exact.
	exact := compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string, age?: int })
  takes_int(body["name"])
end
`)
	requireCheckWarningContains(t, exact, "call to takes_int argument value expected int, got string")
}

func TestCheckInferOptionalShapeFieldCompatibility(t *testing.T) {
	t.Parallel()

	// A literal without the optional field still satisfies the declared
	// shape, and an optional field disjoint from a generic hash's value type
	// is not a contradiction (the field may be absent).
	requireNoCheckWarnings(t, compileScript(t, `
def accept(payload: { name: string, age?: int })
  payload
end

def strings_only(opts: hash<string, string>)
  opts
end

def run(raw: string)
  v = "Ada"
  accept({ name: v })
  accept({ name: v, age: 36 })
  strings_only(JSON.parse_as(raw, { name: string, age?: int }))
end
`))

	missing := compileScript(t, `
def accept(payload: { name: string, age?: int })
  payload
end

def run()
  v = 36
  accept({ age: v })
end
`)
	requireCheckWarningContains(t, missing, "call to accept argument payload expected { age?: int, name: string }, got { age: int }")

	invalid := compileScript(t, `
def accept(payload: { name: string, age?: int })
  payload
end

def run()
  v = "36"
  accept({ name: "Ada", age: v })
end
`)
	requireCheckWarningContains(t, invalid, "call to accept argument payload expected { age?: int, name: string }, got { age: string, name: string }")
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

func TestCheckInferSymbolicOperatorsShortCircuitInExpressions(t *testing.T) {
	t.Parallel()

	// A conditional append inside a short-circuit expression joins with the
	// skipped path.
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def run(flag)
  values = [1]
  (flag && (values << "bad"))
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
  (true && (values << "bad"))
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<int | string>")
}

func TestCheckInferAnnotatedArrayAppendsKeepWitnesses(t *testing.T) {
	t.Parallel()

	// The appended string is witnessed even though the receiver's prior
	// elements come from an annotation.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run(values: array<int>)
  values << "bad"
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>, got array<string>")

	// A compatible append stays silent, and prior unwitnessed elements
	// never count as witnesses (an empty receiver makes [\"bad\"] a valid
	// array<string>).
	requireNoCheckWarnings(t, compileScript(t, `
def ints(values: array<int>)
  values
end

def strings(values: array<string>)
  values
end

def run(a: array<int>, b: array<int>)
  a << 1
  ints(a)
  b << "bad"
  strings(b)
end
`))
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

	// An absent optional field is not a missing keyword.
	requireNoCheckWarnings(t, compileScript(t, `
def accept(**opts: { a: int, b?: int })
  opts
end

def run()
  x = 1
  accept(a: x)
end
`))
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
  opts = { "limit": 3 }
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

func TestCheckInferLogicalCompoundAssignments(t *testing.T) {
	t.Parallel()

	// A nil-only current means ||= definitely binds the right side.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  x = nil
  x ||= "bad"
  takes_int(x)
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	// A definitely-truthy current short-circuits ||=: the right side never
	// binds, so the fact stays the current type.
	truthy := compileScript(t, `
def takes_string(value: string)
  value
end

def run
  x = 1
  x ||= "s"
  takes_string(x)
end
`)
	requireCheckWarningContains(t, truthy, "call to takes_string argument value expected string, got int")

	// &&= binds the right side only for a truthy current.
	andAssign := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  x = 1
  x &&= "bad"
  takes_int(x)
end
`)
	requireCheckWarningContains(t, andAssign, "call to takes_int argument value expected int, got string")

	// A nil-only current short-circuits &&=: x stays nil.
	requireNoCheckWarnings(t, compileScript(t, `
def run
  x = nil
  x &&= "s"
  x
end
`))

	// An unknown current keeps the compound result unknown.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(v)
  v ||= "s"
  takes_int(v)
end
`))
}

func TestCheckInferHostKeywordsSeedKeywordRestParams(t *testing.T) {
	t.Parallel()

	// Host keywords bind the keyword-rest hash with per-entry facts, so
	// string-indexed reads carry the concrete argument types.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(**opts)
  takes_int(opts["limit"])
end
`)
	warnings := script.CheckWarningsForCall("run", nil, CallOptions{Keywords: map[string]Value{"limit": NewString("x")}})
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want keyword-rest contradiction", warnings)
	}

	// Compatible keywords stay silent.
	if warnings := script.CheckWarningsForCall("run", nil, CallOptions{Keywords: map[string]Value{"limit": NewInt(5)}}); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForCall() = %v, want none", warnings)
	}

	// Keywords claimed by named parameters stay out of the rest hash.
	claimed := compileScript(t, `
def takes_int(value: int)
  value
end

def run(limit: 0, **opts)
  takes_int(opts["mode"])
end
`)
	claimedWarnings := claimed.CheckWarningsForCall("run", nil, CallOptions{Keywords: map[string]Value{"limit": NewInt(1), "mode": NewString("fast")}})
	found = false
	for _, warning := range claimedWarnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want unclaimed-keyword contradiction", claimedWarnings)
	}
}

func TestCheckInferPerCallRefinesUnionAnnotations(t *testing.T) {
	t.Parallel()

	// The concrete argument picks its union arm, so the impossible arm no
	// longer masks the body contradiction.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(v: int | string)
  takes_int(v)
end
`)
	warnings := script.CheckWarningsForCall("run", []Value{NewString("s")}, CallOptions{})
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want refined union contradiction", warnings)
	}

	// The compatible arm stays silent.
	if warnings := script.CheckWarningsForCall("run", []Value{NewInt(1)}, CallOptions{}); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForCall() = %v, want none", warnings)
	}

	// Whole-script checks keep the full annotation: no call picks an arm.
	requireNoCheckWarnings(t, script)

	// An omitted argument binds the default, whose type picks the arm the
	// same way.
	defaulted := compileScript(t, `
def takes_int(value: int)
  value
end

def run(v: int | string = "x")
  takes_int(v)
end
`)
	defaultWarnings := defaulted.CheckWarningsForCall("run", nil, CallOptions{})
	found = false
	for _, warning := range defaultWarnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want refined default contradiction", defaultWarnings)
	}
	if warnings := defaulted.CheckWarningsForCall("run", []Value{NewInt(1)}, CallOptions{}); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForCall() = %v, want none", warnings)
	}
}

func TestCheckWarningsForCallSkipsBodyAfterBindingFailure(t *testing.T) {
	t.Parallel()

	// The runtime rejects the call at argument binding, so per-call checks
	// must report the binding mismatch alone: body diagnostics would
	// describe an execution that cannot happen.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(x: int)
  takes_string(x)
end
`)
	warnings := script.CheckWarningsForCall("run", []Value{NewString("s")}, CallOptions{})
	foundBinding := false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "argument x expected int, got string") {
			foundBinding = true
		}
		if strings.Contains(warning.Message, "takes_string") {
			t.Fatalf("CheckWarningsForCall() = %v, body diagnostic reported after binding failure", warnings)
		}
	}
	if !foundBinding {
		t.Fatalf("CheckWarningsForCall() = %v, want binding mismatch", warnings)
	}

	// A call that binds still checks the body.
	bodyWarnings := script.CheckWarningsForCall("run", []Value{NewInt(1)}, CallOptions{})
	foundBody := false
	for _, warning := range bodyWarnings {
		if strings.Contains(warning.Message, "call to takes_string argument value expected string, got int") {
			foundBody = true
		}
	}
	if !foundBody {
		t.Fatalf("CheckWarningsForCall() = %v, want body diagnostic for a binding call", bodyWarnings)
	}
}

func TestCheckInferHostArgumentsSeedRestParams(t *testing.T) {
	t.Parallel()

	// The rest local binds the remaining positional arguments as an array,
	// so per-call checks see its element facts.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(*items)
  takes_int(items[0])
end
`)
	warnings := script.CheckWarningsForCall("run", []Value{NewString("x")}, CallOptions{})
	found := false
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			found = true
		}
	}
	if !found {
		t.Fatalf("CheckWarningsForCall() = %v, want rest-argument contradiction", warnings)
	}

	// Compatible rest arguments stay silent.
	if warnings := script.CheckWarningsForCall("run", []Value{NewInt(1), NewInt(2)}, CallOptions{}); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForCall() = %v, want none", warnings)
	}
}

func TestCheckInferReceiverFactsHoldThroughCallArguments(t *testing.T) {
	t.Parallel()

	// Arguments evaluate before member dispatch, so an argument reading
	// the receiver still sees its element facts.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  a = ["x"]
  a.join(takes_int(a[0]))
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	// Dispatch still poisons: facts read after the call stay unknown.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run
  a = ["x"]
  a.clear
  takes_int(a[0])
end
`))
}

func TestCheckInferSafeNavigationUsesReceiverFacts(t *testing.T) {
	t.Parallel()

	// A receiver that is provably non-nil always evaluates the arguments,
	// so the impossible skipped path must not wash out their facts.
	script := compileScript(t, `
def ints(values: array<int>)
  values
end

def run
  obj = [1]
  values = [1]
  obj&.push(values << "bad")
  ints(values)
end
`)
	requireCheckWarningContains(t, script, "call to ints argument values expected array<int>")

	// A nil-only receiver skips the call and its arguments entirely: their
	// dead contradictions must not report.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run
  obj = nil
  obj&.record(takes_int("bad"))
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
    h = { "name": "Ada" }
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
    h = { "profile": { name: "Ada" } }
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
