package runtime

import "testing"

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

  def self.limit
    LIMIT
  end

  def self.scoped_limit
    Config::LIMIT
  end
end

class Other
  LIMIT = 20

  def self.limit
    LIMIT
  end
end

def run()
  [Config.limit, Config.scoped_limit, Config::LIMIT, Other.limit]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewInt(10),
		NewInt(10),
		NewInt(10),
		NewInt(20),
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

def run()
  [full_name(), User.new.full_name, Badge.new.label]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewString("Ada"),
		NewString("Grace"),
		NewString("X"),
	})
}

func TestQualifiedModuleEnumTypeAnnotations(t *testing.T) {
	t.Parallel()
	engine := moduleTestEngine(t)
	script, err := engine.CompileSnippet(`
Status = "local"
status_mod = require("enum_status", as: "status_mod")

def echo(status: status_mod.Status) -> status_mod.Status
  status
end

def echo_nullable(status: status_mod.Status? = nil) -> status_mod.Status?
  status
end

def run()
  [echo(:draft).name, echo_nullable()]
end
run()
`, "__main")
	if err != nil {
		t.Fatalf("compile snippet: %v", err)
	}

	compareArrays(t, callScript(t, t.Context(), script, "__main", nil, CallOptions{}), []Value{
		NewString("Draft"),
		NewNil(),
	})
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
  parenless = id 5..
    6

  [
    assigned.first,
    assigned.last,
    parenthesized.first,
    parenthesized.last,
    parenless.first,
    parenless.last,
  ]
end
`)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{
		NewInt(1),
		NewInt(2),
		NewInt(3),
		NewInt(4),
		NewInt(5),
		NewInt(6),
	})
}
