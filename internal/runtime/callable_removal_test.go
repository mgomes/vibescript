package runtime

import (
	"strings"
	"testing"
)

// callableConstructions is the population found by sweeping the parser, the
// AST, and the runtime value constructors for every way a script could obtain
// a value that carries executable code. It is the removal contract for
// ADR-006 item 4: each entry must be rejected, and the test fails the moment
// any one of them starts producing a value again. wantError, where set, pins
// the rejection to the removal's teaching message rather than an incidental
// resolution failure.
var callableConstructions = []struct {
	name      string
	source    string
	wantError string
}{
	{"stabby_lambda_paren_params", "def run\n  mapper = ->(person) { person }\n  mapper\nend", ""},
	{"stabby_lambda_no_params", "def run\n  fn = -> { 1 }\n  fn\nend", ""},
	{"stabby_lambda_do_body", "def run\n  fn = ->(n) do\n    n\n  end\n  fn\nend", ""},
	{"lambda_builtin_brace", "def run\n  fn = lambda { |a| a }\n  fn\nend", "lambda was removed; executable code is not a value"},
	{"lambda_builtin_do", "def run\n  fn = lambda do |a|\n    a\n  end\n  fn\nend", "lambda was removed; executable code is not a value"},
	{"proc_builtin", "def run\n  fn = proc { |a| a }\n  fn\nend", "proc was removed; executable code is not a value"},
	{"proc_new", "def run\n  fn = Proc.new { |a| a }\n  fn\nend", "Proc.new was removed; executable code is not a value"},
	{"lambda_builtin_bare_read", "def run\n  fn = lambda\n  fn\nend", "lambda was removed; executable code is not a value"},
	{"proc_builtin_bare_read", "def run\n  fn = proc\n  fn\nend", "proc was removed; executable code is not a value"},
	{"proc_new_bare_read", "def run\n  fn = Proc.new\n  fn\nend", "Proc.new was removed; executable code is not a value"},
	{"block_capture_param", "def takes(&blk)\n  blk\nend\n\ndef run\n  takes { 1 }\nend", ""},
	{"block_pass_parenthesized", "def run\n  blk = nil\n  [1].map(&blk)\nend", ""},
	{"block_pass_parenless", "def run\n  blk = nil\n  values.map &blk\nend", ""},
	{"symbol_to_proc", "def run\n  [\"a\"].map(&:upcase)\nend", ""},
	{"symbol_to_proc_operator", "def run\n  [1, 2].reduce(&:+)\nend", ""},
	{"function_value_from_bare_name", "def helper(a)\n  a\nend\n\ndef run\n  fn = helper\n  fn\nend", "helper is a function and cannot be used as a value"},
	{"function_call_member", "def helper(a)\n  a\nend\n\ndef run\n  helper.call(1)\nend", "helper is a function and cannot be used as a value"},
	{"bound_method_value", "class Counter\n  def bump(n)\n    n\n  end\nend\n\ndef run\n  c = Counter.new\n  m = c.bump\n  m\nend", ""},
	{"bound_method_call_member", "class Counter\n  def bump(n)\n    n\n  end\nend\n\ndef run\n  Counter.new.bump.call(1)\nend", ""},
	{"capability_method_detached", "def run\n  fn = JSON.stringify\n  fn\nend", "stringify is a method and cannot be used as a value"},
	{"builtin_member_detached", "def run\n  fn = Regexp.union\n  fn\nend", "union is a method and cannot be used as a value"},
	{"hash_default_proc", "def run\n  Hash.new { |h, k| k }\nend", ""},
	{"hash_default_proc_accessor", "def run\n  {}.default_proc\nend", ""},
	{"block_as_value", "def takes\n  yield 1\nend\n\ndef run\n  takes { |n| n }.call\nend", ""},
	{"lambda_predicate_member", "def takes\n  yield 1\nend\n\ndef run\n  takes { |n| n.lambda? }\nend", ""},
	{"function_type_annotation", "def run(cb: function)\n  cb\nend", ""},
	{"function_type_atom", "def run\n  1.is_a?(:function)\nend", ""},
}

// TestCallableValuesCannotBeConstructed is the removal-verification test. On
// master every entry below compiles and runs, so the test fails there; after
// the removal each one must be rejected at compile time or, where the form is
// only decidable at runtime, when it runs.
func TestCallableValuesCannotBeConstructed(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	for _, tc := range callableConstructions {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script, err := engine.Compile(tc.source)
			if err != nil {
				requireRemovalError(t, tc.name, tc.wantError, err)
				return
			}
			result, err := script.Call(t.Context(), "run", nil, CallOptions{})
			if err != nil {
				requireRemovalError(t, tc.name, tc.wantError, err)
				return
			}
			t.Fatalf("%s produced %v (kind %s); it must be rejected", tc.name, result, result.Kind())
		})
	}
}

// requireRemovalError accepts any rejection when the row pins no message, and
// otherwise requires the rejection to be the removal's own teaching error.
func requireRemovalError(t *testing.T, name, want string, err error) {
	t.Helper()
	if want == "" {
		return
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s rejected with %q, want the teaching error containing %q", name, err, want)
	}
}

// The checker must reject what the runtime rejects: hosts gate deploys on
// CheckWarnings, so a removed spelling that only failed at run time would
// pass the gate. Every statically decidable removed spelling therefore
// produces a check warning carrying the same teaching text as the runtime
// error.
func TestRemovedCallableSpellingsProduceCheckWarnings(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"function_value_from_bare_name", "def helper(a)\n  a\nend\n\ndef run\n  fn = helper\n  fn\nend", "helper is a function and cannot be used as a value; call it with helper(...)"},
		{"function_call_member", "def helper(a)\n  a\nend\n\ndef run\n  helper.call(1)\nend", "helper is a function and cannot be used as a value; call it with helper(...)"},
		{"builtin_value_from_bare_name", "def run\n  x = format\n  x\nend", "format is a method and cannot be used as a value; call it with format(...)"},
		{"builtin_member_detached", "def run\n  u = Regexp.union\n  u\nend", "union is a method and cannot be used as a value; call it with union(...)"},
		{"capability_method_detached", "def run\n  fn = JSON.stringify\n  fn\nend", "stringify is a method and cannot be used as a value; call it with stringify(...)"},
		{"bound_method_value", "class Counter\n  def bump(n)\n    n\n  end\nend\n\ndef run\n  c = Counter.new\n  m = c.bump\n  m\nend", "call to Counter#bump is missing argument n"},
		{"lambda_builtin_brace", "def run\n  fn = lambda { |a| a }\n  fn\nend", "lambda was removed; executable code is not a value"},
		{"lambda_builtin_bare_read", "def run\n  fn = lambda\n  fn\nend", "lambda was removed; executable code is not a value"},
		{"proc_builtin", "def run\n  fn = proc { |a| a }\n  fn\nend", "proc was removed; executable code is not a value"},
		{"proc_new", "def run\n  fn = Proc.new { |a| a }\n  fn\nend", "Proc.new was removed; executable code is not a value"},
		{"proc_new_bare_read", "def run\n  fn = Proc.new\n  fn\nend", "Proc.new was removed; executable code is not a value"},
		{"function_type_annotation", "def run(cb: function)\n  cb\nend", "the function type was removed with first-class callables"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.want)
		})
	}
}

// The removal is about values, not about the syntax that remains. A block
// attached to a call, invoked with yield, is the supported replacement and
// must keep working.
func TestSynchronousBlocksSurviveTheRemoval(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	for _, tc := range []struct {
		name   string
		source string
		want   int64
	}{
		{"enumerable_block", "def run\n  [1, 2, 3].map { |n| n * 2 }.sum\nend", 12},
		{"do_end_block", "def run\n  total = 0\n  [1, 2, 3].each do |n|\n    total = total + n\n  end\n  total\nend", 6},
		{"yield", "def twice\n  yield(1) + yield(2)\nend\n\ndef run\n  twice { |n| n * 10 }\nend", 30},
		{"block_given", "def maybe\n  if block_given?\n    yield\n  else\n    0\n  end\nend\n\ndef run\n  maybe { 7 } + maybe\nend", 7},
		{"implicit_param", "def run\n  [1, 2, 3].map { it + 1 }.sum\nend", 9},
		{"destructuring_param", "def run\n  [[1, 2]].map { |(a, b)| a + b }.sum\nend", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script, err := engine.Compile(tc.source)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			result, err := script.Call(t.Context(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if result.Int() != tc.want {
				t.Fatalf("result = %d, want %d", result.Int(), tc.want)
			}
		})
	}
}

// The ADR's own example is the shape an author is most likely to reach for, so
// its diagnostic is pinned end to end: it must fail at compile time and the
// message must name the replacements rather than only the syntax error.
func TestADRLambdaExampleIsACompileError(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	_, err := engine.Compile("def run(people)\n  mapper = ->(person) { person.name }\n  people.map { |p| mapper.call(p) }\nend")
	if err == nil {
		t.Fatal("expected a compile error for the lambda literal")
	}
	if !strings.Contains(err.Error(), "lambda literals are not supported") {
		t.Fatalf("error = %v, want it to name the removal", err)
	}
	if !strings.Contains(err.Error(), "named function") || !strings.Contains(err.Error(), "block") {
		t.Fatalf("error = %v, want it to name both replacements", err)
	}
}
