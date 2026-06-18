package runtime

import "testing"

func TestStringSquish(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "already_squished",
			text: "hello world",
			want: "hello world",
		},
		{
			name: "leading_trailing_and_runs",
			text: "  hello \n\t world  ",
			want: "hello world",
		},
		{
			name: "unicode_whitespace",
			text: "hello\u00a0\u2003world",
			want: "hello world",
		},
		{
			name: "all_whitespace",
			text: " \n\t",
			want: "",
		},
		{
			name: "empty",
			text: "",
			want: "",
		},
		{
			name: "invalid_utf8_bytes",
			text: "a\xff  b",
			want: "a\xff b",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := stringSquish(tc.text); got != tc.want {
				t.Fatalf("stringSquish(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}
