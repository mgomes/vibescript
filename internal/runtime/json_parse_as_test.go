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

func TestCheckJSONParseAsArity(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run(raw: string)
  JSON.parse_as(raw)
end
`)

	requireCheckWarningContains(t, script, "JSON.parse_as")
}
