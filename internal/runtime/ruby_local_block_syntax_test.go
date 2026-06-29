package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestRubyPredeclaresLocalsAssignedInScope(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  if false
    branch_local = 1
  end
  while false
    loop_local = 1
  end
  [branch_local, loop_local]
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewNil(), NewNil()})
}

func TestRubyBlockAssignmentsRespectLocalBoundary(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def mutate_outer
  x = 1
  [2].each do |n|
    x = n
  end
  x
end

def leak_new
  [1].each do |n|
    y = 3
  end
  y
end
`)

	if got := callFunc(t, script, "mutate_outer", nil); !got.Equal(NewInt(2)) {
		t.Fatalf("mutate_outer() = %s, want 2", got)
	}

	_, err := script.Call(context.Background(), "leak_new", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "undefined variable y") {
		t.Fatalf("leak_new() error = %v, want undefined variable y", err)
	}
}

func TestRubyBlockMultiParameterDestructuresSingleYieldedArray(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  out = []
  [[1, 2]].each do |a, b, c|
    out = out + [[a, b, c]]
  end
  out
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewNil()}),
	})
}

func TestRubyBlockAutosplatsBeforeNestedDestructuring(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  [[1, [2, 3], 4]].map do |a, (b, c), d|
    [a, b, c, d]
  end
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)}),
	})
}

func TestRubyForLoopDestructuresTargets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  out = []
  for a, b in [[1, 2], [3, 4]]
    out = out + [a + b]
  end
  for k, v in {a: 1, b: 2}
    out = out + [[k, v]]
  end
  out
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{
		NewInt(3),
		NewInt(7),
		NewArray([]Value{NewSymbol("a"), NewInt(1)}),
		NewArray([]Value{NewSymbol("b"), NewInt(2)}),
	})
}

func TestRubyWordBooleanOperatorsAndNotParenlessCall(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def allowed(user)
  user == "Ada"
end

def run
  x = true and false
  y = false or true
  state = "allowed"
  if not allowed "Bob"
    state = "blocked"
  end
  [x, y, state]
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewBool(true), NewBool(false), NewString("blocked")})
}

func TestRubyLogicalAssignmentsShortCircuitTargets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def explode
  raise "boom"
end

def run
  first ||= 1
  skipped &&= explode()
  a = nil
  a ||= 3
  b = true
  b &&= 4
  c = 5
  c ||= explode()
  d = false
  d &&= explode()
  values = [nil, 1, false]
  values[0] ||= 7
  values[1] ||= explode()
  values[2] &&= explode()
  {first: first, skipped: skipped, a: a, b: b, c: c, d: d, values: values}
end
`)

	got := callFunc(t, script, "run", nil)
	compareHash(t, got.Hash(), map[string]Value{
		"first":   NewInt(1),
		"skipped": NewNil(),
		"a":       NewInt(3),
		"b":       NewInt(4),
		"c":       NewInt(5),
		"d":       NewBool(false),
		"values":  NewArray([]Value{NewInt(7), NewInt(1), NewBool(false)}),
	})
}

func TestRubyLogicalStatementPredeclaresShortCircuitedRHSAssignments(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  x = true or y = 1
  a = false and b = 2
  [x, y, a, b]
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewBool(true), NewNil(), NewBool(false), NewNil()})
}

func TestRubyLogicalStatementMixedPrecedenceAssignsTrailingOr(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  z = false
  x = false and y = true or z = true
  z
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("run() = %s, want true", got)
	}
}

func TestRubyNestedZeroArgDoBlockInsideLoopCondition(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def wrapper(x)
  x
end

def run
  count = 0
  while count < 1 && wrapper([1].any? do
    true
  end) do
    count += 1
  end
  count
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(1)) {
		t.Fatalf("run() = %s, want 1", got)
	}
}

func TestRubyExplicitEmptyBlockParameters(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  out = []
  [1].each do ||
    out = out.push("do")
  end
  [2].each { || out = out.push("brace") }
  out
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewString("do"), NewString("brace")})
}

func TestRubyCompactKeywordHashLabels(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  {a:1,b:{c:2}}
end
`)

	got := callFunc(t, script, "run", nil)
	compareHash(t, got.Hash(), map[string]Value{
		"a": NewInt(1),
		"b": NewHash(map[string]Value{"c": NewInt(2)}),
	})
}
