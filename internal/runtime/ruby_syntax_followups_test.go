package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestCommaSeparatedReturnValuesReturnArray(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def pair()
  return 1, 2
end

def run()
  a, b = pair()
  [a, b]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{NewInt(1), NewInt(2)})
}

func TestTypedRaiseMessageArgument(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def run()
  begin
    raise RuntimeError, "bad"
  rescue RuntimeError => e
    e.message
  end
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewString("bad")) {
		t.Fatalf("run() = %#v, want bad", got)
	}
}

func TestTypedRaiseRejectsInvalidMessageTarget(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def raise_string()
  raise "bad", "msg"
end

def raise_int()
  raise 1, "msg"
end

def raise_nil()
  raise nil, "msg"
end
`)

	for _, fn := range []string{"raise_string", "raise_int", "raise_nil"} {
		t.Run(fn, func(t *testing.T) {
			t.Parallel()
			err := callScriptErr(t, context.Background(), script, fn, nil, CallOptions{})
			var typeErr *RuntimeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("%s() error = %T, want RuntimeError", fn, err)
			}
			if typeErr.Type != runtimeErrorTypeType {
				t.Fatalf("%s() RuntimeError.Type = %s, want %s", fn, typeErr.Type, runtimeErrorTypeType)
			}
			if typeErr.Message != "exception class/object expected" {
				t.Fatalf("%s() message = %q, want exception class/object expected", fn, typeErr.Message)
			}
		})
	}
}

func TestTypedRaiseEvaluatesLowercaseTarget(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def run(error)
  raise error, "msg"
end
`)

	err := callScriptErr(t, context.Background(), script, "run", []Value{NewString("bad")}, CallOptions{})
	var typeErr *RuntimeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("run() error = %T, want RuntimeError", err)
	}
	if typeErr.Type != runtimeErrorTypeType {
		t.Fatalf("run() RuntimeError.Type = %s, want %s", typeErr.Type, runtimeErrorTypeType)
	}
	if typeErr.Message != "exception class/object expected" {
		t.Fatalf("run() message = %q, want exception class/object expected", typeErr.Message)
	}
}

func TestTypedRaiseEvaluatesShadowingConstantTarget(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def run(RuntimeError)
  raise RuntimeError, "msg"
end
`)

	err := callScriptErr(t, context.Background(), script, "run", []Value{NewString("bad")}, CallOptions{})
	var typeErr *RuntimeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("run() error = %T, want RuntimeError", err)
	}
	if typeErr.Type != runtimeErrorTypeType {
		t.Fatalf("run() RuntimeError.Type = %s, want %s", typeErr.Type, runtimeErrorTypeType)
	}
	if typeErr.Message != "exception class/object expected" {
		t.Fatalf("run() message = %q, want exception class/object expected", typeErr.Message)
	}
}

func TestTypedRaiseEvaluatesTargetBeforeMessage(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class RaiseOrder
  @@trace = []

  def self.reset()
    @@trace = []
  end

  def self.target()
    @@trace = @@trace + ["target"]
    1
  end

  def self.message()
    @@trace = @@trace + ["message"]
    "bad"
  end

  def self.bad_message()
    @@trace = @@trace + ["message"]
    1
  end

  def self.trace()
    @@trace
  end
end

def dynamic_target_order()
  RaiseOrder.reset()
  begin
    raise RaiseOrder.target(), RaiseOrder.message()
  rescue TypeError
    RaiseOrder.trace()
  end
end

def invalid_message_order()
  RaiseOrder.reset()
  begin
    raise RaiseOrder.target(), RaiseOrder.bad_message()
  rescue TypeError
    RaiseOrder.trace()
  end
end
`)

	want := []Value{NewString("target"), NewString("message")}
	compareArrays(t, callFunc(t, script, "dynamic_target_order", nil), want)
	compareArrays(t, callFunc(t, script, "invalid_message_order", nil), want)
}

func TestClassConstantAssignmentsDoNotCreateShadowingLocals(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class ConstantFlow
  LIMIT = 10
  LIMIT += 2
  LIMIT &&= LIMIT + 1
  DEFAULT ||= 7
  COPY = LIMIT

  def self.touch()
    COPY &&= LIMIT + DEFAULT
    FRESH ||= COPY + 1
    nil
  end
end

def run()
  ConstantFlow.touch()
  [ConstantFlow::LIMIT, ConstantFlow::COPY, ConstantFlow::DEFAULT, ConstantFlow::FRESH]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewInt(13),
		NewInt(20),
		NewInt(7),
		NewInt(21),
	})
}

func TestLogicalClassConstantAssignmentRespectsUppercaseLocals(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class ConstantShadow
  LIMIT = 10

  def self.or_local(LIMIT)
    LIMIT ||= 1
    LIMIT
  end

  def self.and_local(LIMIT)
    LIMIT &&= LIMIT + 1
    LIMIT
  end

  def self.plain_local(LIMIT)
    LIMIT = LIMIT + 1
    LIMIT
  end

  def self.compound_local(LIMIT)
    LIMIT += 1
    LIMIT
  end
end

def run()
  [
    ConstantShadow.or_local(nil),
    ConstantShadow.and_local(2),
    ConstantShadow.plain_local(3),
    ConstantShadow.compound_local(4),
    ConstantShadow::LIMIT,
  ]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewInt(1),
		NewInt(3),
		NewInt(4),
		NewInt(5),
		NewInt(10),
	})
}

func TestSymbolTypeAnnotations(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def echo(x: symbol) -> symbol
  x
end

def echo_nullable(x: symbol? = nil) -> symbol?
  x
end
`)

	if got := callFunc(t, script, "echo", []Value{NewSymbol("ok")}); !got.Equal(NewSymbol("ok")) {
		t.Fatalf("echo(:ok) = %#v, want :ok", got)
	}
	if got := callFunc(t, script, "echo_nullable", nil); !got.Equal(NewNil()) {
		t.Fatalf("echo_nullable() = %#v, want nil", got)
	}
	requireCallErrorContains(t, script, "echo", []Value{NewString("ok")}, CallOptions{}, "argument x expected symbol, got string")
}

func TestUnionEnumNormalizationDoesNotDependOnAnyOrder(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
enum Status
  Draft
end

def left(v: any | Status) -> any | Status
  v
end

def right(v: Status | any) -> Status | any
  v
end

def nested_left(v: array<any | Status>) -> array<any | Status>
  v
end

def nested_right(v: array<Status | any>) -> array<Status | any>
  v
end

def run()
  [
    left(:draft) == Status::Draft,
    right(:draft) == Status::Draft,
    nested_left([:draft])[0] == Status::Draft,
    nested_right([:draft])[0] == Status::Draft,
  ]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
	})
}

func TestScopedClassConstants(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Config
  LIMIT = 10
  LIMIT += 2

  def self.limit
    LIMIT
  end

  def self.scoped_limit
    Config::LIMIT
  end

  def self.bump_limit
    LIMIT += 1
  end

  def self.echo(LIMIT)
    LIMIT
  end

  def self.rescue_or_assign(LIMIT)
    begin
      raise "boom"
    rescue => err
      LIMIT ||= 1
      LIMIT
    end
  end

  def self.rescue_and_assign(LIMIT)
    begin
      raise "boom"
    rescue => err
      LIMIT &&= 4
      LIMIT
    end
  end
end

class Other
  LIMIT = 20

  def self.limit
    LIMIT
  end
end

def run()
  [
    Config.limit,
    Config.scoped_limit,
    Config::LIMIT,
    Config.echo(3),
    Config.bump_limit,
    Config::LIMIT,
    Config.limit,
    Other.limit,
    Config.rescue_or_assign(nil),
    Config::LIMIT,
    Config.rescue_and_assign(false),
    Config::LIMIT,
  ]
end
`)

	compareArrays(t, callScript(t, context.Background(), script, "run", nil, CallOptions{
		Globals: map[string]Value{"LIMIT": NewInt(99)},
	}), []Value{
		NewInt(12),
		NewInt(12),
		NewInt(12),
		NewInt(3),
		NewInt(13),
		NewInt(13),
		NewInt(13),
		NewInt(20),
		NewInt(1),
		NewInt(13),
		NewBool(false),
		NewInt(13),
	})
}

func TestRubyStyleFunctionAndMethodAliases(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def name()
  "Ada"
end
alias full_name name

class User
  def name()
    "Grace"
  end
  alias_method :full_name, :name
end

class Badge
  def code()
    "X"
  end
  alias label code
end

class ParenthesizedBadge
  def code()
    "Y"
  end
  alias_method(:label, :code)
end

class Ordered
  def name()
    "old"
  end
  alias label name
  def name()
    "new"
  end
end

def run()
  [full_name(), User.new.full_name, Badge.new.label, ParenthesizedBadge.new.label, Ordered.new.label, Ordered.new.name]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewString("Ada"),
		NewString("Grace"),
		NewString("X"),
		NewString("Y"),
		NewString("old"),
		NewString("new"),
	})
}

func TestClassAliasRequiresEarlierTarget(t *testing.T) {
	t.Parallel()
	engine := MustNewEngine(Config{})
	_, err := engine.Compile(`
class User
  alias label name
  def name()
    "Ada"
  end
end
`)
	requireErrorContains(t, err, "alias target method name is not defined on class User")
}

func TestNestedAliasRejectedAtCompileTime(t *testing.T) {
	t.Parallel()
	engine := MustNewEngine(Config{})
	_, err := engine.Compile(`
def run()
  alias bar foo
end
`)
	requireErrorContains(t, err, "alias declarations are only supported at the top level or in class bodies")
}

func TestQualifiedModuleEnumTypeAnnotations(t *testing.T) {
	t.Parallel()
	engine := moduleTestEngine(t)
	script, err := engine.CompileSnippet(`
Status = "local"
require("enum_status", as: "status_mod")
require("enum_status", as: "Types")

def echo(status: status_mod.Status) -> status_mod.Status
  status
end

def echo_upper(status: Types.Status) -> Types.Status
  status
end

def echo_nullable(status: status_mod.Status? = nil) -> status_mod.Status?
  status
end

def run()
  [echo(:draft).name, echo_upper(:published).name, echo_nullable()]
end
run()
`, "__main")
	if err != nil {
		t.Fatalf("compile snippet: %v", err)
	}

	compareArrays(t, callScript(t, t.Context(), script, "__main", nil, CallOptions{}), []Value{
		NewString("Draft"),
		NewString("Published"),
		NewNil(),
	})
}

// TestDottedScreamingCaseMemberStaysKeywordDefault pins the annotation
// discriminator: a SCREAMING_CASE dotted member after a parameter colon is a
// constant default expression by convention, never a qualified type
// annotation, so the parameter stays a callable keyword. Qualified type
// annotations require the canonical PascalCase member (Types.Status).
func TestDottedScreamingCaseMemberStaysKeywordDefault(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def f(cap: Data.RESULT)
  cap
end

def run()
  f(cap: 5)
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(5)) {
		t.Fatalf("run() = %#v, want 5", got)
	}
}

func TestParenlessCallBinaryExpressionOperand(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def add(a, b)
  a + b
end

def run()
  1 + add 2, 3
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(6)) {
		t.Fatalf("run() = %#v, want 6", got)
	}
}

func TestParenlessSameLineDoBlockAttachesToOuterCall(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def wrap(x)
  yield x
end

def combine(a, b)
  yield a + b
end

def run()
  first = wrap 1 do |x|
    x + 1
  end

  second = combine 1, 2 do |x|
    x * 2
  end

  [first, second]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewInt(2),
		NewInt(6),
	})
}

// TestMultilineRangeEndpoints pins the Ruby rule that a newline terminates a
// range at statement level (the dots form an endless range and the next line
// is a separate statement) while grouped forms — parens, brackets, call
// arguments — continue the bounded endpoint onto the next line.
func TestMultilineRangeEndpoints(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def id(x)
  x
end

def run()
  assigned = 1..
    2
  parenthesized = id(3..
    4)

  [
    assigned.first,
    assigned === 100,
    parenthesized.first,
    parenthesized.last,
  ]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewInt(1),
		NewBool(true),
		NewInt(3),
		NewInt(4),
	})
}

// TestClassBodyLogicalAssignmentTargetsConstants pins ||=/&&= scoping in class
// bodies: a constant-shaped target binds the class constant (creating it for
// ||= on unset, reassigning for &&= on set) and never clobbers a same-named
// local outside the class body, matching = and the arithmetic compound forms.
func TestClassBodyLogicalAssignmentTargetsConstants(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`
DEFAULT = 42

class C
  DEFAULT &&= 9
  MODE ||= "on"
  MODE &&= "off"
end

[DEFAULT, C::MODE]
`, "__main")
	if err != nil {
		t.Fatalf("compile snippet: %v", err)
	}

	compareArrays(t, callScript(t, context.Background(), script, "__main", nil, CallOptions{}), []Value{
		NewInt(42),
		NewString("off"),
	})
}
