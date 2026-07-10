package runtime

import (
	"context"
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
	// re-walk must not see the ensure's reassignments.
	requireNoCheckWarnings(t, compileScript(t, `
def run -> int
  begin
    x = 1
    return x
  ensure
    x = "cleanup"
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

	requireNoCheckWarnings(t, compileScript(t, `
def configure(opts: hash<string, int>)
  opts
end

def run()
  opts = { limit: 3 }
  configure(opts)
  configure({})
end
`))
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
