package parser

import (
	"strings"
	"testing"
)

func TestParserRejectsSafeNavigation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "member",
			source: `def run
  user = {name: "Ada"}
  user&.name
end`,
		},
		{
			name: "nil_receiver",
			source: `def run
  nil&.name
end`,
		},
		{
			name: "method_call",
			source: `def run
  user&.profile("public")
end`,
		},
		{
			name: "call_argument",
			source: `def run
  inspect(user&.name, "fallback")
end`,
		},
		{
			name: "assignment",
			source: `def run
  user&.name = "Ada"
end`,
		},
		{
			name: "wrapped_method_call",
			source: `def run
  user&.
    profile("public")
  "fallback"
end`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			requireSingleSafeNavigationError(t, tc.source)
		})
	}
}

func requireSingleSafeNavigationError(t *testing.T, source string) {
	t.Helper()

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "safe navigation is not supported"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
