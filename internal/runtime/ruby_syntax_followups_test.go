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
