package main

import (
	"strings"
	"testing"
)

func FuzzFormatVibeSource(f *testing.F) {
	for _, seed := range []string{
		"",
		"def run()  \n  1\t \nend",
		"def run()\r\n  1\r\nend\r\n",
		"\t \r\n",
		"# comment\n\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		source = limitFormatFuzzString(source, 4096)
		formatted := formatVibeSource(source)

		if !strings.HasSuffix(formatted, "\n") {
			t.Fatalf("formatVibeSource(%q) = %q, want trailing newline", source, formatted)
		}
		if strings.Contains(formatted, "\r") {
			t.Fatalf("formatVibeSource(%q) = %q, want no carriage returns", source, formatted)
		}

		lines := strings.Split(strings.TrimSuffix(formatted, "\n"), "\n")
		for i, line := range lines {
			if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
				t.Fatalf("formatVibeSource(%q) line %d = %q, want no trailing horizontal whitespace", source, i+1, line)
			}
		}

		if second := formatVibeSource(formatted); second != formatted {
			t.Fatalf("formatVibeSource is not idempotent: first %q, second %q", formatted, second)
		}
	})
}

func limitFormatFuzzString(raw string, limit int) string {
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}
