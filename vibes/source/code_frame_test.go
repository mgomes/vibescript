package source

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"
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

// TestFormatterRetentionIsSparse pins that the retained line index stays a
// small fraction of the source. The formatter is held for as long as the
// compiled script is, and a long-lived engine caches many scripts, so an
// index proportional to the line count (one string header each) let a
// mostly-newline source near the size limit retain many times its own length.
func TestFormatterRetentionIsSparse(t *testing.T) {
	t.Parallel()

	// A 1 MiB mostly-newline source, the worst case for a per-line index.
	src := strings.Repeat("\n", 1<<20)
	formatter := NewCodeFrameFormatter(src)

	// One dense header per line would be ~16 MiB here.
	retained := len(formatter.checkpoints) * int(unsafe.Sizeof(int(0)))
	if limit := len(src) / 4; retained > limit {
		t.Fatalf("index retains %d bytes for a %d byte source, want under %d", retained, len(src), limit)
	}
}

// TestFormatterFindsLinesPastACheckpoint pins that the sparse lookup returns
// the right line at, before, and after a checkpoint boundary.
func TestFormatterFindsLinesPastACheckpoint(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	const lines = codeFrameCheckpointStride*3 + 7
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&b, "line%d\n", i)
	}
	formatter := NewCodeFrameFormatter(b.String())

	probes := []int{
		1,
		codeFrameCheckpointStride - 1,
		codeFrameCheckpointStride,
		codeFrameCheckpointStride + 1,
		codeFrameCheckpointStride * 2,
		lines,
	}
	for _, line := range probes {
		got, ok := formatter.lineText(line)
		if !ok {
			t.Fatalf("line %d not found", line)
		}
		if want := fmt.Sprintf("line%d", line); got != want {
			t.Fatalf("line %d = %q, want %q", line, got, want)
		}
	}
	if _, ok := formatter.lineText(lines + 100); ok {
		t.Fatal("a line past the end must not resolve")
	}
}
