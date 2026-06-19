package parser

import (
	"strings"
	"testing"
)

func TestParserRejectsAmpersandBlockArgument(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "block_forwarding",
			source: `def run
  mapper = nil
  [1, 2].map(&mapper)
end`,
		},
		{
			name: "symbol_to_proc",
			source: `def run
  ["a", "b"].map(&:upcase)
end`,
		},
		{
			name: "parenless_block_forwarding",
			source: `def run
  mapper = nil
  values.map &mapper
end`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, errs := parseSource(t, tc.source)
			if len(errs) != 1 {
				t.Fatalf("parseSource(%q) errors = %d, want 1: %v", tc.source, len(errs), errs)
			}
			if got, want := errs[0].Error(), "ampersand block forwarding and symbol-to-proc shorthand are not supported"; !strings.Contains(got, want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", tc.source, got, want)
			}
		})
	}
}
