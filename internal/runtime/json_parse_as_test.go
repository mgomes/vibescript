package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR-004: JSON.parse_as parses JSON and validates the result against a
// shape literal in one step, with the same semantics as typed boundaries.

func TestJSONParseAsValidatesShape(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  body = JSON.parse_as("{\"name\": \"Ada\", \"age\": 36}", { name: string, age: int })
  body["name"]
end
`)

	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "Ada" {
		t.Fatalf("run() = %#v, want \"Ada\"", got)
	}
}

func TestJSONParseAsRejectsMismatchedPayload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name: "wrong field type",
			source: `
def run()
  JSON.parse_as("{\"name\": 42}", { name: string })
end
`,
			wantErr: "JSON.parse_as value expected { name: string }, got { name: int }",
		},
		{
			name: "missing field",
			source: `
def run()
  JSON.parse_as("{}", { name: string })
end
`,
			wantErr: "JSON.parse_as value expected { name: string }",
		},
		{
			name: "extra field",
			source: `
def run()
  JSON.parse_as("{\"name\": \"x\", \"extra\": 1}", { name: string })
end
`,
			wantErr: "JSON.parse_as value expected { name: string }",
		},
		{
			name: "invalid JSON",
			source: `
def run()
  JSON.parse_as("{", { name: string })
end
`,
			wantErr: "JSON.parse_as invalid JSON",
		},
		{
			name: "invalid number keeps the parse_as prefix",
			source: `
def run()
  JSON.parse_as("1e999999999", { n: number })
end
`,
			wantErr: "JSON.parse_as invalid number",
		},
		{
			name: "non-shape second argument",
			source: `
def run()
  JSON.parse_as("{}", 1)
end
`,
			wantErr: "JSON.parse_as expects a type literal as its second argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("run() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestJSONParseAsSupportsOptionalFields(t *testing.T) {
	t.Parallel()

	// An absent optional field passes validation and reads as nil; a present
	// one validates against the field type, including in nested shapes.
	script := compileScript(t, `
def run()
  body = JSON.parse_as("{\"name\": \"Ada\", \"contact\": {\"email\": \"a@example.com\"}}", {
    name: string,
    age?: int,
    contact?: { email: string, verified?: bool }
  })
  body["age"]
end
`)
	requireNoCheckWarnings(t, script)
	if got := callScript(t, context.Background(), script, "run", nil, CallOptions{}); got.Kind() != KindNil {
		t.Fatalf("run() = %#v, want nil", got)
	}

	invalid := compileScript(t, `
def run()
  JSON.parse_as("{\"name\": \"Ada\", \"age\": \"36\"}", { name: string, age?: int })
end
`)
	err := callScriptErr(t, context.Background(), invalid, "run", nil, CallOptions{})
	want := "JSON.parse_as value expected { age?: int, name: string }, got { age: string, name: string }"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run() error = %v, want substring %q", err, want)
	}

	missing := compileScript(t, `
def run()
  JSON.parse_as("{\"age\": 36}", { name: string, age?: int })
end
`)
	err = callScriptErr(t, context.Background(), missing, "run", nil, CallOptions{})
	want = "JSON.parse_as value expected { age?: int, name: string }, got { age: int }"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run() error = %v, want substring %q", err, want)
	}
}

func TestJSONParseAsAcceptsNestedShapesAndUnions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  body = JSON.parse_as("{\"user\": {\"name\": \"Ada\"}, \"note\": null, \"tags\": [1, 2]}", {
    user: { name: string },
    note: string | nil,
    tags: array<int>
  })
  body["user"]["name"]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "Ada" {
		t.Fatalf("run() = %#v, want \"Ada\"", got)
	}
}

func TestShapeLiteralIsFirstClassValue(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  user_shape = { name: string }
  body = JSON.parse_as("{\"name\": \"Ada\"}", user_shape)
  body["name"]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "Ada" {
		t.Fatalf("run() = %#v, want \"Ada\"", got)
	}
}

func TestJSONParseAsValidatesNonObjectRoots(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
		check  func(t *testing.T, got Value)
	}{
		{
			name: "array root",
			source: `
def run()
  JSON.parse_as("[1, 2, 3]", array<int>)
end
`,
			check: func(t *testing.T, got Value) {
				if got.Kind() != KindArray || len(got.Array()) != 3 {
					t.Fatalf("run() = %#v, want three-element array", got)
				}
			},
		},
		{
			name: "array of shapes root",
			source: `
def run()
  rows = JSON.parse_as("[{\"id\": \"a\"}, {\"id\": \"b\"}]", array<{ id: string }>)
  rows[1]["id"]
end
`,
			check: func(t *testing.T, got Value) {
				if got.Kind() != KindString || got.String() != "b" {
					t.Fatalf("run() = %#v, want \"b\"", got)
				}
			},
		},
		{
			name: "scalar root",
			source: `
def run()
  JSON.parse_as("42", int)
end
`,
			check: func(t *testing.T, got Value) {
				if got.Kind() != KindInt || got.Int() != 42 {
					t.Fatalf("run() = %#v, want 42", got)
				}
			},
		},
		{
			name: "nullable root null",
			source: `
def run()
  JSON.parse_as("null", int?)
end
`,
			check: func(t *testing.T, got Value) {
				if got.Kind() != KindNil {
					t.Fatalf("run() = %#v, want nil", got)
				}
			},
		},
		{
			name: "nullable root value",
			source: `
def run()
  JSON.parse_as("7", int?)
end
`,
			check: func(t *testing.T, got Value) {
				if got.Kind() != KindInt || got.Int() != 7 {
					t.Fatalf("run() = %#v, want 7", got)
				}
			},
		},
		{
			name: "union root",
			source: `
def run()
  JSON.parse_as("\"ready\"", int | string)
end
`,
			check: func(t *testing.T, got Value) {
				if got.Kind() != KindString || got.String() != "ready" {
					t.Fatalf("run() = %#v, want \"ready\"", got)
				}
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireNoCheckWarnings(t, script)
			tc.check(t, callScript(t, context.Background(), script, "run", nil, CallOptions{}))
		})
	}
}

func TestJSONParseAsNonObjectRootDiagnostics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			// The actual type renders per-element detail, identifying the
			// failing element kind.
			name: "array element mismatch",
			source: `
def run()
  JSON.parse_as("[1, \"x\"]", array<int>)
end
`,
			wantErr: "JSON.parse_as value expected array<int>, got array<int | string>",
		},
		{
			name: "nested shape field mismatch",
			source: `
def run()
  JSON.parse_as("[{\"id\": 1}]", array<{ id: string }>)
end
`,
			wantErr: "JSON.parse_as value expected array<{ id: string }>, got array<{ id: int }>",
		},
		{
			name: "scalar mismatch",
			source: `
def run()
  JSON.parse_as("\"x\"", int)
end
`,
			wantErr: "JSON.parse_as value expected int, got string",
		},
		{
			name: "union mismatch names the union",
			source: `
def run()
  JSON.parse_as("true", int | string)
end
`,
			wantErr: "JSON.parse_as value expected int | string, got bool",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("run() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestArgumentTypeLiteralShadowing(t *testing.T) {
	t.Parallel()

	// A local sharing a type name keeps the value reading: the argument
	// evaluates to the local, exactly as before type literals existed.
	shadowed := compileScript(t, `
def double(v)
  v * 2
end

def run()
  int = 21
  double(int)
end
`)
	got := callScript(t, context.Background(), shadowed, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 42 {
		t.Fatalf("run() = %#v, want 42", got)
	}

	// The shadowed reading flows into JSON.parse_as too: the argument is the
	// local's value, not a contract, and is rejected as such.
	rejected := compileScript(t, `
def run()
  int = 21
  JSON.parse_as("1", int)
end
`)
	err := callScriptErr(t, context.Background(), rejected, "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "expects a type literal as its second argument") {
		t.Fatalf("run() error = %v, want type-literal rejection", err)
	}

	// A binding whose exact spelling matches the nullable atom (`string?` as
	// a host global or zero-arity method) shadows the type reading too: leaf
	// normalization strips the `?`, but the value reading reads the
	// predicate-style name verbatim.
	predicate := compileScript(t, `
def echo(v)
  v
end

def run()
  echo(string?)
end
`)
	got = callScript(t, context.Background(), predicate, "run", nil, CallOptions{
		Globals: map[string]Value{"string?": NewString("shadowed")},
	})
	if got.Kind() != KindString || got.String() != "shadowed" {
		t.Fatalf("run() = %#v, want \"shadowed\"", got)
	}

	// Unshadowed, the same spelling stays the nullable type literal.
	unshadowed := compileScript(t, `
def run()
  JSON.parse_as("null", string?)
end
`)
	if got := callScript(t, context.Background(), unshadowed, "run", nil, CallOptions{}); got.Kind() != KindNil {
		t.Fatalf("run() = %#v, want nil", got)
	}

	// An unrelated binding of the BASE name does not shadow the nullable
	// spelling: the fallback reads `string?` verbatim, never `string`.
	baseName := compileScript(t, `
def run(string: string)
  JSON.parse_as("null", string?)
end
`)
	got = callScript(t, context.Background(), baseName, "run", []Value{NewString("unrelated")}, CallOptions{})
	if got.Kind() != KindNil {
		t.Fatalf("run(\"unrelated\") = %#v, want nil", got)
	}

	// The static mirror agrees: the literal stays the nullable contract, so
	// its result fact contradicts an int boundary.
	baseNameStatic := compileScript(t, `
def takes_int(value: int)
  value
end

def run(string: string, raw: string)
  takes_int(JSON.parse_as(raw, string?))
end
`)
	requireCheckWarningContains(t, baseNameStatic, "call to takes_int argument value expected int, got string?")

	// The static mirror sees implicit-self predicate methods the same way.
	selfShadowed := compileScript(t, `
class Probe
  def string?
    "predicate"
  end

  def run
    JSON.parse_as("null", string?)
  end
end
`)
	requireNoCheckWarnings(t, selfShadowed)

	// A zero-arity callable bound to a type-spelled name keeps the caller's
	// auto-call state: a function-typed parameter receives it bare, while an
	// untyped parameter auto-invokes it like any other identifier argument.
	autoCall := compileScript(t, `
def function
  "invoked"
end

def take(cb: function)
  cb.call
end

def echo(v)
  v
end

def run()
  [take(function), echo(function)]
end
`)
	// Pre-fix, the argument auto-invoked and the string failed the
	// `cb: function` boundary; bare passing must satisfy it and let the
	// callee invoke.
	got = callScript(t, context.Background(), autoCall, "run", nil, CallOptions{})
	if got.Kind() != KindArray || len(got.Array()) != 2 {
		t.Fatalf("run() = %#v, want two-element array", got)
	}
	if bare := got.Array()[0]; bare.Kind() != KindString || bare.String() != "invoked" {
		t.Fatalf("take(function) = %#v, want \"invoked\"", bare)
	}
	if invoked := got.Array()[1]; invoked.Kind() != KindString || invoked.String() != "invoked" {
		t.Fatalf("echo(function) = %#v, want auto-invoked \"invoked\"", invoked)
	}

	// A lambda-holding local behaves like any other local: passed bare to a
	// callable-typed parameter.
	lambdaLocal := compileScript(t, `
def take(cb: function)
  cb.call
end

def run()
  function = -> { "from lambda" }
  take(function)
end
`)
	got = callScript(t, context.Background(), lambdaLocal, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "from lambda" {
		t.Fatalf("take(lambda local) = %#v, want \"from lambda\"", got)
	}

	// Comparisons over non-type locals never read as types.
	comparison := compileScript(t, `
def truthy(v)
  v
end

def run(threshold: int, value: int)
  truthy(value < threshold)
end
`)
	got = callScript(t, context.Background(), comparison, "run", []Value{NewInt(10), NewInt(3)}, CallOptions{})
	if got.Kind() != KindBool || !got.Bool() {
		t.Fatalf("run(10, 3) = %#v, want true", got)
	}
}

func TestCheckInferParseAsNonObjectRootFacts(t *testing.T) {
	t.Parallel()

	// The declared root contract is the checker's result fact.
	scalar := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  takes_string(JSON.parse_as(raw, int))
end
`)
	requireCheckWarningContains(t, scalar, "call to takes_string argument value expected string, got int")

	union := compileScript(t, `
def takes_bool(value: bool)
  value
end

def run(raw: string)
  takes_bool(JSON.parse_as(raw, int | string))
end
`)
	requireCheckWarningContains(t, union, "call to takes_bool argument value expected bool, got int | string")

	// An array root's element reads infer the element type joined with nil,
	// and nested shapes keep string-keyed field facts.
	element := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  values = JSON.parse_as(raw, array<int>)
  takes_string(values[0])
end
`)
	requireCheckWarningContains(t, element, "call to takes_string argument value expected string, got int | nil")

	requireNoCheckWarnings(t, compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  takes_int(JSON.parse_as(raw, int?) || 0)
end
`))

	// A statically shadowed spelling keeps the value reading, so the checker
	// reports the non-contract argument like any other value.
	shadowed := compileScript(t, `
def run(raw: string)
  int = 1
  JSON.parse_as(raw, int)
end
`)
	requireCheckWarningContains(t, shadowed, "call to JSON.parse_as expects a type literal as its second argument, got int")
}

func TestJSONParseAsSupportsOpenShapes(t *testing.T) {
	t.Parallel()

	// Declared fields validate; undeclared fields pass through and read back
	// with their raw parsed values.
	script := compileScript(t, `
def run()
  body = JSON.parse_as("{\"name\": \"Ada\", \"age\": 36, \"role\": \"captain\"}", { name: string, ... })
  body["role"]
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "captain" {
		t.Fatalf("run() = %#v, want \"captain\"", got)
	}

	invalid := compileScript(t, `
def run()
  JSON.parse_as("{\"name\": 1, \"role\": \"captain\"}", { name: string, ... })
end
`)
	err := callScriptErr(t, context.Background(), invalid, "run", nil, CallOptions{})
	want := "JSON.parse_as value expected { name: string, ... }"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run() error = %v, want substring %q", err, want)
	}

	missing := compileScript(t, `
def run()
  JSON.parse_as("{\"role\": \"captain\"}", { name: string, ... })
end
`)
	err = callScriptErr(t, context.Background(), missing, "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("run() error = %v, want substring %q", err, want)
	}
}

// Shape values compare by formatted text, so the contract with a literal
// `valid?` field must not equal the contract with an optional `valid` field.
func TestShapeValueEqualityDistinguishesOptionalFields(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  literal = { "valid?": bool }
  optional = { valid?: bool }
  [literal == optional, optional == { valid?: bool }]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindArray || len(got.Array()) != 2 {
		t.Fatalf("run() = %#v, want two-element array", got)
	}
	if cross := got.Array()[0]; cross.Kind() != KindBool || cross.Bool() {
		t.Fatalf("literal == optional = %#v, want false", cross)
	}
	if same := got.Array()[1]; same.Kind() != KindBool || !same.Bool() {
		t.Fatalf("optional == optional = %#v, want true", same)
	}
}

func TestShapeLiteralDisambiguation(t *testing.T) {
	t.Parallel()

	// A braced group whose values are value expressions, name locals, or
	// unknown identifiers stays a hash literal with unchanged semantics.
	script := compileScript(t, `
def run()
  string = "local"
  h = { name: string, retry: 3 }
  h[:name]
end
`)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "local" {
		t.Fatalf("run() = %#v, want \"local\"", got)
	}

	// An unknown identifier value keeps its undefined-variable diagnostic
	// instead of silently becoming an enum-typed shape field.
	requireCheckWarningContains(t, compileScript(t, `
def run()
  { status: pending }
end
`), "undefined variable pending")
}

func TestCheckInferJSONParseAsResultShape(t *testing.T) {
	t.Parallel()

	// The ADR's flagship flow: validate once at the edge, then everything
	// downstream is inferred and checked.
	script := compileScript(t, `
def create_user(name: string)
  name
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string, age: int })
  create_user(body["age"])
end
`)
	requireCheckWarningContains(t, script, "call to create_user argument name expected string, got int")

	requireNoCheckWarnings(t, compileScript(t, `
def create_user(name: string)
  name
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string, age: int })
  create_user(body["name"])
end
`))
}

func TestShapeLiteralHostGlobalShadowKeepsHashSemantics(t *testing.T) {
	t.Parallel()

	// A host-provided global that reuses a type name shadows the shape
	// reading, so the braced group keeps its pre-existing hash semantics
	// and reads the host value.
	script := compileScript(t, `
def run()
  h = { name: string }
  h[:name]
end
`)
	opts := CallOptions{Globals: map[string]Value{"string": NewString("Ada")}}
	got := callScript(t, context.Background(), script, "run", nil, opts)
	if got.Kind() != KindString || got.String() != "Ada" {
		t.Fatalf("run() with host global = %#v, want \"Ada\"", got)
	}

	// Without the global the same group is a first-class shape value, which
	// does not support indexing.
	err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "cannot index shape") {
		t.Fatalf("run() without host global err = %v, want cannot index shape", err)
	}

	// The checker mirrors the choice: with the global the parse_as schema
	// is a host hash, so no shape facts flow and the hash reading reports
	// the schema misuse the runtime rejects.
	checked := compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string })
  takes_int(body["name"])
end
`)
	requireCheckWarningContains(t, checked, "call to takes_int argument value expected int, got string")
	requireCheckWarningContainsWithOptions(t, checked, opts, "call to JSON.parse_as expects a type literal as its second argument, got hash")
}

func TestShapeLiteralImplicitSelfShadowKeepsHashSemantics(t *testing.T) {
	t.Parallel()

	// A zero-arity method named like a type resolves through implicit self,
	// so the braced group keeps its pre-existing hash semantics inside the
	// method.
	script := compileScript(t, `
class Formatter
  def string
    "fmt"
  end

  def build
    h = { name: string }
    h[:name]
  end
end

def run()
  Formatter.new.build
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "fmt" {
		t.Fatalf("run() = %#v, want \"fmt\"", got)
	}

	// A method without such a member still produces a shape value usable by
	// JSON.parse_as.
	builder := compileScript(t, `
class Builder
  def schema
    { name: string }
  end
end

def run()
  body = JSON.parse_as("{\"name\": \"Ada\"}", Builder.new.schema)
  body["name"]
end
`)
	got = callScript(t, context.Background(), builder, "run", nil, CallOptions{})
	if got.Kind() != KindString || got.String() != "Ada" {
		t.Fatalf("run() = %#v, want \"Ada\"", got)
	}
}

func TestCheckInferShadowedShapeLiteralsInferAsHashes(t *testing.T) {
	t.Parallel()

	// A proven shadow forces the hash reading, so the literal carries hash
	// facts instead of dropping to unknown.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  string = "x"
  takes_int({ name: string })
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got")

	// The hash reading's field facts flow through indexing.
	fields := compileScript(t, `
def takes_int(value: int)
  value
end

def run
  string = "x"
  h = { name: string }
  takes_int(h[:name])
end
`)
	requireCheckWarningContains(t, fields, "call to takes_int argument value expected int, got string")
}

func TestCheckInferFutureLocalsDoNotShadowShapeLeaves(t *testing.T) {
	t.Parallel()

	// Runtime shadowing uses the live env at evaluation: a local assigned
	// only later does not exist yet, so the literal stays a shape and its
	// facts flow to the typed boundary.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  schema = { name: string }
  string = "x"
  body = JSON.parse_as(raw, schema)
  takes_int(body["name"]) if string
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	// After a compound statement completes its bindings predeclare even on
	// skipped paths, so a later literal is shadowed into a hash.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_hash(value: hash<symbol, any>)
  value
end

def run(flag: bool)
  if flag
    string = "x"
  end
  takes_hash({ name: string })
end
`))
}

func TestCheckInferClassBodyPredeclareShadowsShapeLeaves(t *testing.T) {
	t.Parallel()

	// A class-body assignment target predeclares before the body runs, so
	// a target named after a type shadows the leaf: the braces stay a hash
	// at runtime and the checker must not bind a shape-value fact that
	// contradicts hash-typed boundaries.
	requireNoCheckWarnings(t, compileScript(t, `
def takes_hash(value: hash<symbol, any>)
  value
end

class Api
  string = { name: string }
  takes_hash(string)
end

def run
  Api.new
end
`))
}

func TestCheckInferSelfShadowDistinguishesMemberKind(t *testing.T) {
	t.Parallel()

	// A class method dispatches implicit self through class members only,
	// so an instance method named string does not shadow: the shape fact
	// holds and the parse_as contradiction reports.
	classMethod := compileScript(t, `
def takes_int(value: int)
  value
end

class Api
  def string
    "instance"
  end

  def self.load(raw: string)
    body = JSON.parse_as(raw, { age: string })
    takes_int(body["age"])
  end
end

def run(raw: string)
  Api.load(raw)
end
`)
	requireCheckWarningContains(t, classMethod, "call to takes_int argument value expected int, got string")

	// The converse: an instance method sees only instance members, so a
	// class method named string does not shadow.
	instanceMethod := compileScript(t, `
def takes_int(value: int)
  value
end

class Api
  def self.string
    "class"
  end

  def load(raw: string)
    body = JSON.parse_as(raw, { age: string })
    takes_int(body["age"])
  end
end

def run(raw: string)
  Api.new.load(raw)
end
`)
	requireCheckWarningContains(t, instanceMethod, "call to takes_int argument value expected int, got string")

	// A class body is a class context too: an instance method named string
	// does not shadow a shape literal evaluated in the body.
	classBody := compileScript(t, `
def takes_int(value: int)
  value
end

class Api
  def string
    "instance"
  end

  takes_int({ age: string })
end

def run
  Api.new
end
`)
	requireCheckWarningContains(t, classBody, "call to takes_int argument value expected int, got shape")
}

func TestCheckInferParseAsShapesInsideMethods(t *testing.T) {
	t.Parallel()

	// A method whose class defines no colliding member keeps the shape
	// facts, so parse_as flows are checked inside methods too.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

class Api
  def load(raw: string)
    body = JSON.parse_as(raw, { age: string })
    takes_int(body["age"])
  end
end

def run(raw: string)
  Api.new.load(raw)
end
`)

	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
}

func TestCheckInferJSONParseAsShapeThroughLocal(t *testing.T) {
	t.Parallel()

	// A shape stored in a local keeps the static checking promise: the
	// parse_as result is still typed as the schema.
	script := compileScript(t, `
def create_user(name: string)
  name
end

def run(raw: string)
  schema = { name: string, age: int }
  body = JSON.parse_as(raw, schema)
  create_user(body["age"])
end
`)

	requireCheckWarningContains(t, script, "call to create_user argument name expected string, got int")
}

func TestCheckJSONParseAsRejectsKnownNonShapeArguments(t *testing.T) {
	t.Parallel()

	scalar := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw, 1)
end
`)
	requireCheckWarningContains(t, scalar, "call to JSON.parse_as expects a type literal as its second argument, got int")

	// A hash of data values is a hash at runtime, not a shape.
	dataHash := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw, { name: "Ada" })
end
`)
	requireCheckWarningContains(t, dataHash, "call to JSON.parse_as expects a type literal as its second argument")

	// Dynamic schemas stay a runtime concern.
	requireNoCheckWarnings(t, compileScript(t, `
def run(raw: string, schema)
  JSON.parse_as(raw, schema)
end
`))
}

func TestShapeLiteralKeepsShapeSemanticsUnderTypedParams(t *testing.T) {
	t.Parallel()

	// The typed-argument fast path must not force the hash reading: a shape
	// literal passed to a hash-annotated parameter is a shape value that
	// fails the boundary, not a hash whose values are undefined variables.
	script := compileScript(t, `
def wants_hash(x: hash<symbol, any>)
  x
end

def run()
  wants_hash({ name: string })
end
`)
	err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "argument x expected") {
		t.Fatalf("run() err = %v, want typed-boundary mismatch", err)
	}
	if strings.Contains(err.Error(), "undefined variable") {
		t.Fatalf("run() err = %v, must not evaluate type names as variables", err)
	}

	// A host global shadowing the type name keeps hash semantics on the
	// same typed path.
	opts := CallOptions{Globals: map[string]Value{"string": NewString("Ada")}}
	got := callScript(t, context.Background(), script, "run", nil, opts)
	if got.Kind() != KindHash && got.Kind() != KindObject {
		t.Fatalf("run() with host global = %#v, want hash", got)
	}
}

func TestShapeLiteralStructuralErrorsSurface(t *testing.T) {
	t.Parallel()

	// A group that reads only under the type grammar surfaces its
	// structural shape diagnostic instead of a confusing expression error.
	err := compileScriptErrorDefault(t, `
def run(raw: string)
  JSON.parse_as(raw, { name: string | nil, name: int })
end
`)
	if err == nil || !strings.Contains(err.Error(), "duplicate shape field name") {
		t.Fatalf("compile error = %v, want duplicate shape field diagnostic", err)
	}

	// A duplicate-key group that still reads as a hash keeps the hash
	// reading: host globals may shadow the type names, and the unshadowed
	// typo still fails the check path through its undefined identifiers.
	dup := compileScript(t, `
def run()
  h = { name: string, name: int }
  h[:name]
end
`)
	requireCheckWarningContains(t, dup, "undefined variable")
	opts := CallOptions{Globals: map[string]Value{
		"string": NewString("A"),
		"int":    NewString("B"),
	}}
	got := callScript(t, context.Background(), dup, "run", nil, opts)
	if got.Kind() != KindString {
		t.Fatalf("run() with shadowing globals = %#v, want a host string value", got)
	}

	// Duplicate data keys keep their hash reading.
	script := compileScript(t, `
def run()
  h = { a: 1, a: 2 }
  h[:a]
end
`)
	data := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if data.Kind() != KindInt {
		t.Fatalf("run() = %#v, want int", data)
	}
}

func TestShapeLiteralEngineBuiltinShadowMatchesRuntime(t *testing.T) {
	t.Parallel()

	// The lowercase money builtin resolves in every runtime env, so
	// { price: money } keeps hash semantics; the checker must not claim
	// shape facts, and the hash reading statically reports the schema
	// misuse the runtime rejects.
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { price: money })
  takes_string(body[:price])
end
`)
	requireCheckWarningContains(t, script, "call to JSON.parse_as expects a type literal as its second argument, got hash")

	// And at runtime the shadowed group really is a hash, which parse_as
	// rejects as a schema.
	err := callScriptErr(t, context.Background(), script, "run", []Value{NewString("{}")}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "expects a type literal as its second argument") {
		t.Fatalf("run() err = %v, want non-shape schema rejection", err)
	}
}

func TestShapeLiteralModuleContextSeesCallerBindings(t *testing.T) {
	t.Parallel()

	// An exported module function invoked by its caller executes under the
	// caller's root, so a caller function named string shadows the module's
	// shape literal into hash semantics; the checker mirrors that and makes
	// no shape claims.
	moduleDir := t.TempDir()
	module := `def build
  { name: string }
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "shaper.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleDir}})
	script, err := engine.CompileSnippet(`def string
  "CALLER"
end

require "shaper"

build`, "<script>")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "<script>", nil, CallOptions{})
	if got.Kind() != KindHash && got.Kind() != KindObject {
		t.Fatalf("build inside module = %#v, want hash semantics", got)
	}
	if entry, ok := got.Hash()["name"]; !ok || entry.Kind() != KindString || entry.String() != "CALLER" {
		t.Fatalf("build inside module = %#v, want name => CALLER", got)
	}
}

func TestCheckShapeFactsContradictNamedAnnotations(t *testing.T) {
	t.Parallel()

	// Named annotations admit only enum values and class instances at
	// runtime, so a first-class shape value is a known mismatch for both.
	enumScript := compileScript(t, `
enum Status
  Draft
end

def takes_status(value: Status)
  value
end

def run()
  takes_status({ name: string })
end
`)
	requireCheckWarningContains(t, enumScript, "call to takes_status argument value expected Status")

	classScript := compileScript(t, `
class User
  def initialize(name: string)
    @name = name
  end
end

def takes_user(value: User)
  value
end

def run()
  takes_user({ name: string })
end
`)
	requireCheckWarningContains(t, classScript, "call to takes_user argument value expected User")
}

func TestCheckShapeFactsContradictTypedBoundaries(t *testing.T) {
	t.Parallel()

	// A first-class shape value satisfies no annotation but any, so a
	// typed parameter rejects it statically like the runtime does.
	script := compileScript(t, `
def wants_hash(x: hash<symbol, any>)
  x
end

def run()
  wants_hash({ name: string })
end
`)
	requireCheckWarningContains(t, script, "call to wants_hash argument x expected hash<symbol, any>")

	requireNoCheckWarnings(t, compileScript(t, `
def wants_any(x: any)
  x
end

def run()
  wants_any({ name: string })
end
`))

	// Known key representations contradict disjoint hash key types: a
	// parse_as result is string-keyed.
	keys := compileScript(t, `
def sym_hash(x: hash<symbol, any>)
  x
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string })
  sym_hash(body)
end
`)
	requireCheckWarningContains(t, keys, "call to sym_hash argument x expected hash<symbol, any>, got { name: string }")

	requireNoCheckWarnings(t, compileScript(t, `
def str_hash(x: hash<string, string>)
  x
end

def run(raw: string)
  body = JSON.parse_as(raw, { name: string })
  str_hash(body)
end
`))
}

func TestCheckJSONParseAsRejectsNonStringInput(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  raw = 1
  JSON.parse_as(raw, { name: string })
end
`)
	requireCheckWarningContains(t, script, "call to JSON.parse_as expects a JSON string as its first argument, got int")

	requireNoCheckWarnings(t, compileScript(t, `
def run(raw)
  JSON.parse_as(raw, { name: string })
end
`))
}

func TestShapeLiteralAssignmentTargetPredeclarationShadows(t *testing.T) {
	t.Parallel()

	// Runtime predeclares the assignment target before the RHS runs, so
	// string resolves as a (nil) local and the group keeps hash semantics;
	// the checker must not claim shape facts for it, and the hash reading
	// statically reports the schema misuse the runtime rejects.
	script := compileScript(t, `
def takes_int(value: int)
  value
end

def run(raw: string)
  string = { name: string }
  body = JSON.parse_as(raw, string)
  takes_int(body["name"])
end
`)
	requireCheckWarningContains(t, script, "call to JSON.parse_as expects a type literal as its second argument, got hash")

	err := callScriptErr(t, context.Background(), script, "run", []Value{NewString("{}")}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "expects a type literal as its second argument") {
		t.Fatalf("run() err = %v, want non-shape schema rejection", err)
	}
}

func TestCheckInferredForcedRequireRetainsExports(t *testing.T) {
	t.Parallel()

	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleDir}})
	// A nil left forces || to evaluate the require, so its exports hold for
	// the statements after it.
	script, err := engine.CompileSnippet(`x = nil
x || require("helpers")
shout(1)`, "<script>")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	requireCheckWarningContains(t, script, "call to shout argument value expected string, got int")
}

func TestCheckConditionRequiresCarryIntoShortCircuitBranches(t *testing.T) {
	t.Parallel()

	moduleDir := t.TempDir()
	module := `def shout(value: string)
  value
end`
	if err := os.WriteFile(filepath.Join(moduleDir, "helpers.vibe"), []byte(module+"\n"), 0o644); err != nil {
		t.Fatalf("write module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{moduleDir}})
	script, err := engine.CompileSnippet(`def run(flag)
  if flag && require("helpers")
    shout("x")
  end
end`, "<script>")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	requireNoCheckWarnings(t, script)
}

func TestCheckJSONParseAsArity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw)
end
`)

	requireCheckWarningContains(t, script, "JSON.parse_as")
}
