package runtime

import (
	"context"
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

func TestCheckJSONParseAsArity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw)
end
`)

	requireCheckWarningContains(t, script, "JSON.parse_as")
}
