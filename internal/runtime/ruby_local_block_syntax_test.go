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

def nested_after_compound
  if true
    if false
      nested_local = 1
    end
    nested_local
  end
end

def else_reads_prior_branch_local
  if false
    branch_local = 1
  else
    branch_local
  end
end

def elsif_condition_reads_prior_branch_local
  if false
    branch_local = 1
  elsif branch_local == nil
    "ok"
  else
    "miss"
  end
end

def rescue_reads_try_body_local
  begin
    raise "boom"
    body_local = 1
  rescue
    body_local
  end
end

def ensure_reads_try_body_local
  begin
    raise "boom"
    body_local = 1
  rescue
    "rescued"
  ensure
    seen = body_local
  end
  seen
end

def rescue_binding_preserves_handler_assignment
  begin
    raise "boom"
  rescue => err
    recovered = 1
  end
  recovered
end

def rescue_binding_does_not_leak
  begin
    raise "boom"
  rescue => err
    recovered = 1
  end
  err
end

def unless_true_else_reads_prior_body_local
  unless true
    unless_local = 1
  else
    unless_local
  end
end

def unless_false_body_before_later_else_assignment
  unless false
    later_unless_local
  else
    later_unless_local = 1
  end
end

def postfix_if_reads_body_local_condition
  postfix_if_local = 1 if postfix_if_local == nil
  postfix_if_local
end

def postfix_unless_reads_body_local_condition
  postfix_unless_local = 1 unless postfix_unless_local != nil
  postfix_unless_local
end

def postfix_while_reads_body_local_condition
  postfix_while_local = 1 while postfix_while_local == nil
  postfix_while_local
end

def postfix_until_reads_body_local_condition
  postfix_until_local = 1 until postfix_until_local != nil
  postfix_until_local
end

def while_next_predeclares_later_body_local
  count = 0
  while count == 0 || skipped_while_local == nil
    count = count + 1
    if count == 1
      next
    end
    skipped_while_local = 1
  end
  [count, skipped_while_local]
end

def until_next_predeclares_later_body_local
  count = 0
  until count != 0 && skipped_until_local != nil
    count = count + 1
    if count == 1
      next
    end
    skipped_until_local = 1
  end
  [count, skipped_until_local]
end
	`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewNil(), NewNil()})
	if got := callFunc(t, script, "nested_after_compound", nil); !got.Equal(NewNil()) {
		t.Fatalf("nested_after_compound() = %s, want nil", got)
	}
	if got := callFunc(t, script, "else_reads_prior_branch_local", nil); !got.Equal(NewNil()) {
		t.Fatalf("else_reads_prior_branch_local() = %s, want nil", got)
	}
	if got := callFunc(t, script, "elsif_condition_reads_prior_branch_local", nil); !got.Equal(NewString("ok")) {
		t.Fatalf("elsif_condition_reads_prior_branch_local() = %s, want ok", got)
	}
	if got := callFunc(t, script, "rescue_reads_try_body_local", nil); !got.Equal(NewNil()) {
		t.Fatalf("rescue_reads_try_body_local() = %s, want nil", got)
	}
	if got := callFunc(t, script, "ensure_reads_try_body_local", nil); !got.Equal(NewNil()) {
		t.Fatalf("ensure_reads_try_body_local() = %s, want nil", got)
	}
	if got := callFunc(t, script, "rescue_binding_preserves_handler_assignment", nil); !got.Equal(NewInt(1)) {
		t.Fatalf("rescue_binding_preserves_handler_assignment() = %s, want 1", got)
	}
	_, err := script.Call(context.Background(), "rescue_binding_does_not_leak", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "undefined variable err") {
		t.Fatalf("rescue_binding_does_not_leak() error = %v, want undefined variable err", err)
	}
	if got := callFunc(t, script, "unless_true_else_reads_prior_body_local", nil); !got.Equal(NewNil()) {
		t.Fatalf("unless_true_else_reads_prior_body_local() = %s, want nil", got)
	}
	_, err = script.Call(context.Background(), "unless_false_body_before_later_else_assignment", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "undefined variable later_unless_local") {
		t.Fatalf("unless_false_body_before_later_else_assignment() error = %v, want undefined variable later_unless_local", err)
	}
	for _, fn := range []string{
		"postfix_if_reads_body_local_condition",
		"postfix_unless_reads_body_local_condition",
		"postfix_while_reads_body_local_condition",
		"postfix_until_reads_body_local_condition",
	} {
		if got := callFunc(t, script, fn, nil); !got.Equal(NewInt(1)) {
			t.Fatalf("%s() = %s, want 1", fn, got)
		}
	}
	for _, fn := range []string{
		"while_next_predeclares_later_body_local",
		"until_next_predeclares_later_body_local",
	} {
		got := callFunc(t, script, fn, nil)
		compareArrays(t, got, []Value{NewInt(2), NewInt(1)})
	}
}

func TestRubyDoesNotPredeclareFutureNestedAssignmentsAtScopeStart(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def nested_branch
  before = later
  if false
    later = 1
  end
  before
end

def logical_rhs
  before = later
  true or later = 1
  before
end

def if_body_source_order
  if true
    before = later
    later = 1
  end
end

def taken_branch_before_later_else_assignment
  if true
    later_branch
  else
    later_branch = 1
  end
end
`)

	for _, fn := range []string{"nested_branch", "logical_rhs", "if_body_source_order", "taken_branch_before_later_else_assignment"} {
		t.Run(fn, func(t *testing.T) {
			t.Parallel()
			_, err := script.Call(context.Background(), fn, nil, CallOptions{})
			if err == nil || !strings.Contains(err.Error(), "undefined variable later") {
				t.Fatalf("%s() error = %v, want undefined variable later", fn, err)
			}
		})
	}
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

func TestRubyPredeclaredLocalsShadowGlobalHelpers(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def helper
  "global"
end

def function_local
  if false
    helper = 1
  end
  helper == nil
end

def block_local
  [1].each do
    helper = 1
  end
  helper()
end
`)

	if got := callFunc(t, script, "function_local", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("function_local() = %s, want true", got)
	}
	if got := callFunc(t, script, "block_local", nil); !got.Equal(NewString("global")) {
		t.Fatalf("block_local() = %s, want global", got)
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

func TestRubyNotPrecedenceInGroupedExpressions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def echo(value)
  value
end

def run
  grouped = (not true and false)
  arg = echo(not true and false)
  [grouped, arg]
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewBool(false), NewBool(false)})
}

func TestRubyLogicalAssignmentsShortCircuitTargets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def explode
  raise "boom"
end

def helper
  99
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
  outer = nil
  [1].each do
    outer ||= 8
  end
  {first: first, skipped: skipped, a: a, b: b, c: c, d: d, values: values, outer: outer}
end

def shadow_helper_with_or_assign
  helper ||= 2
  helper
end

def shadow_helper_with_and_assign
  helper &&= 2
  helper
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
		"outer":   NewInt(8),
	})
	if got := callFunc(t, script, "shadow_helper_with_or_assign", nil); !got.Equal(NewInt(2)) {
		t.Fatalf("shadow_helper_with_or_assign() = %s, want 2", got)
	}
	if got := callFunc(t, script, "shadow_helper_with_and_assign", nil); !got.Equal(NewNil()) {
		t.Fatalf("shadow_helper_with_and_assign() = %s, want nil", got)
	}
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
