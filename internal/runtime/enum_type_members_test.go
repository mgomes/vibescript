package runtime

import (
	"context"
	"strings"
	"testing"
)

const enumTypeSource = `
enum Status
  Active
  Done
end
`

// The enum type object supported no member access at all: it interpolated as
// <Enum Status> but Status::Active.enum.to_s reported "unsupported member
// access on enum", leaving it the one value kind you could not ask anything
// about. Anything reflecting over an enum had to go through interpolation.
func TestEnumTypeMembers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "name through a value", expr: `Status::Active.enum.name`, want: "Status"},
		{name: "name on the bare type", expr: `Status.name`, want: "Status"},
		{name: "to_s", expr: `Status::Active.enum.to_s`, want: "<Enum Status>"},
		{name: "string alias", expr: `Status::Active.enum.string`, want: "<Enum Status>"},
		{name: "inspect", expr: `Status::Active.enum.inspect`, want: "<Enum Status>"},
		{name: "to_s on the bare type", expr: `Status.to_s`, want: "<Enum Status>"},
		// The conversion agrees with interpolation, which is what already
		// worked.
		{name: "agrees with interpolation", expr: `(Status.to_s == "#{Status}").to_s`, want: "true"},
		// The enum's name and a member's name are different things and stay so.
		{name: "member name is unchanged", expr: `Status::Active.name`, want: "Active"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumTypeSource+"\ndef run()\n  "+tc.expr+"\nend")
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

// An unknown property reports and suggests, rather than the previous blanket
// "unsupported member access on enum".
func TestEnumTypeUnknownPropertySuggests(t *testing.T) {
	t.Parallel()
	script := compileScript(t, enumTypeSource+"\ndef run()\n  Status.to_str\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected an unknown enum property to be reported")
	}
	if !strings.Contains(err.Error(), "to_s") {
		t.Fatalf("error = %v, want it to suggest to_s", err)
	}
	if strings.Contains(err.Error(), "unsupported member access") {
		t.Fatalf("error = %v, want a property-specific message", err)
	}
}

// The conversions take no arguments, like every other scalar conversion.
func TestEnumTypeConversionsRejectArguments(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`Status.to_s(1)`, `Status.inspect(1)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumTypeSource+"\ndef run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}
