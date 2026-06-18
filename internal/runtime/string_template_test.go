package runtime

import (
	"strings"
	"testing"
)

func TestStringTemplateScanner(t *testing.T) {
	t.Parallel()
	context := NewHash(map[string]Value{
		"name": NewString("Alex"),
	})
	malformedPrefix := strings.Repeat("{{1", 8) + "}} "
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "invalid_placeholder_preserved",
			text: "{{1}} {{ name }}",
			want: "{{1}} Alex",
		},
		{
			name: "missing_placeholder_preserved",
			text: "Hello {{missing}}",
			want: "Hello {{missing}}",
		},
		{
			name: "nested_valid_placeholder_after_invalid_open",
			text: "{{ bad {{name}}",
			want: "{{ bad Alex",
		},
		{
			name: "overlapping_valid_placeholder_after_invalid_open",
			text: "{{{name}}",
			want: "{Alex",
		},
		{
			name: "malformed_openers_before_valid_placeholder",
			text: malformedPrefix + "{{name}}",
			want: malformedPrefix + "Alex",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stringTemplate(tc.text, context, false)
			if err != nil {
				t.Fatalf("stringTemplate(%q) error = %v", tc.text, err)
			}
			if got != tc.want {
				t.Fatalf("stringTemplate(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
