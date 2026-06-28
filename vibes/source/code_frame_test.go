package source

import (
	"strings"
	"testing"
)

func TestCodeFrameFormatterReusesSourceLines(t *testing.T) {
	t.Parallel()
	formatter := NewCodeFrameFormatter("first\nsecond")

	got := formatter.Format(Position{Line: 2, Column: 3})
	if !strings.Contains(got, "second") {
		t.Fatalf("Format() = %q, want second line", got)
	}
	if !strings.Contains(got, "column 3") {
		t.Fatalf("Format() = %q, want original column", got)
	}
}

func TestCodeFrameFormatterTruncatesLongLines(t *testing.T) {
	t.Parallel()
	line := strings.Repeat("a", 512)
	got := FormatCodeFrame(line, Position{Line: 1, Column: 300})

	if len(got) > 512 {
		t.Fatalf("FormatCodeFrame() length = %d, want bounded frame", len(got))
	}
	if !strings.Contains(got, "column 300") {
		t.Fatalf("FormatCodeFrame() = %q, want original column", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("FormatCodeFrame() = %q, want truncation marker", got)
	}
}
