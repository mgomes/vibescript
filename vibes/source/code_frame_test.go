package source

import (
	"runtime"
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

func TestCodeFrameFormatterWindowsLongLinesWithoutFullRuneSlice(t *testing.T) {
	line := strings.Repeat("🙂", 256*1024)
	var got string
	allocated := allocBytes(t, func() {
		got = FormatCodeFrame(line, Position{Line: 1, Column: 100_000})
	})

	if !strings.Contains(got, "column 100000") {
		t.Fatalf("FormatCodeFrame() = %q, want original column", got)
	}
	if len(got) > 2*1024 {
		t.Fatalf("FormatCodeFrame() length = %d, want bounded frame", len(got))
	}
	if allocated > 256*1024 {
		t.Fatalf("FormatCodeFrame() allocated %d bytes, want bounded allocation", allocated)
	}
}

func allocBytes(t *testing.T, fn func()) uint64 {
	t.Helper()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}
