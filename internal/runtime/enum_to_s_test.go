package runtime

import (
	"context"
	"strings"
	"testing"
)

const enumToStringSource = `
enum Status
  Active
  Done
end
`

// Interpolation rendered an enum value fine, but the explicit conversion every
// other kind supports was missing, so an author who wrote .to_s got an unknown
// member -- and no suggestion, because neither name nor symbol is close enough
// for didYouMean to fire.
func TestEnumValueToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "to_s", expr: `Status::Active.to_s`, want: "Status::Active"},
		{name: "to_s with parentheses", expr: `Status::Active.to_s()`, want: "Status::Active"},
		{name: "string alias", expr: `Status::Active.string`, want: "Status::Active"},
		{name: "inspect", expr: `Status::Active.inspect`, want: "Status::Active"},
		{name: "another member", expr: `Status::Done.to_s`, want: "Status::Done"},
		// The point of the conversion is that it agrees with interpolation,
		// which is what already worked.
		{name: "agrees with interpolation", expr: `(Status::Active.to_s == "#{Status::Active}").to_s`, want: "true"},
		{name: "concatenates", expr: `"status=" + Status::Done.to_s`, want: "status=Status::Done"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumToStringSource+"\ndef run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// The existing members must keep working.
func TestEnumValueExistingMembersUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "name", expr: `Status::Active.name`, want: "Active"},
		{name: "symbol", expr: `Status::Active.symbol.inspect`, want: ":active"},
		{name: "enum", expr: `"#{Status::Active.enum}"`, want: "<Enum Status>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumToStringSource+"\ndef run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// An unknown member still reports, and now suggests to_s for the near misses
// that previously produced no suggestion at all.
func TestEnumValueUnknownMemberSuggests(t *testing.T) {
	t.Parallel()
	script := compileScript(t, enumToStringSource+"\ndef run()\n  Status::Active.to_str\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected an unknown enum member to be reported")
	}
	if !strings.Contains(err.Error(), "to_s") {
		t.Fatalf("error = %v, want it to suggest to_s", err)
	}
}

// to_s takes no arguments, like every other scalar conversion.
func TestEnumValueToStringRejectsArguments(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`Status::Active.to_s(1)`, `Status::Active.inspect(1)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumToStringSource+"\ndef run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}
