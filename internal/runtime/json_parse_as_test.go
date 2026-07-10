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
			wantErr: "JSON.parse_as expects a shape literal as its second argument",
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
	// is a host hash, so no shape facts flow and nothing is claimed.
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
	requireNoCheckWarningsWithOptions(t, checked, opts)
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
	requireCheckWarningContains(t, scalar, "call to JSON.parse_as expects a shape literal as its second argument, got int")

	// A hash of data values is a hash at runtime, not a shape.
	dataHash := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw, { name: "Ada" })
end
`)
	requireCheckWarningContains(t, dataHash, "call to JSON.parse_as expects a shape literal as its second argument")

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
	// shape facts (a stale claim would report a nil field read the runtime
	// never performs).
	script := compileScript(t, `
def takes_string(value: string)
  value
end

def run(raw: string)
  body = JSON.parse_as(raw, { price: money })
  takes_string(body[:price])
end
`)
	requireNoCheckWarnings(t, script)

	// And at runtime the shadowed group really is a hash, which parse_as
	// rejects as a schema.
	err := callScriptErr(t, context.Background(), script, "run", []Value{NewString("{}")}, CallOptions{})
	if err == nil || !strings.Contains(err.Error(), "expects a shape literal as its second argument") {
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

func TestCheckJSONParseAsArity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw)
end
`)

	requireCheckWarningContains(t, script, "JSON.parse_as")
}
