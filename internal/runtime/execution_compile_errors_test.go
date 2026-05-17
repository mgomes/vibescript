package runtime

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCombineErrorsJoinsWithBlankLines(t *testing.T) {
	t.Parallel()
	got := combineErrors([]error{errors.New("a"), errors.New("b"), errors.New("c")})
	want := "a\n\nb\n\nc"
	if got.Error() != want {
		t.Fatalf("combineErrors = %q, want %q", got.Error(), want)
	}
}

func TestCombineErrorsSingleErrorPassesThrough(t *testing.T) {
	t.Parallel()
	orig := errors.New("only")
	if got := combineErrors([]error{orig}); got != orig { //nolint:errorlint // verifying identity, not wrapped equivalence
		t.Fatalf("combineErrors = %v, want original error instance", got)
	}
}

// Regression test for a quadratic-time bug in combineErrors that turned
// invalid-UTF-8 inputs into a remote DoS vector: every byte produced one
// parse error, and the joiner concatenated them with `msg +=` in a loop.
// At 4 KB of `\x80` bytes the old code took ~10s; the fix is sub-millisecond.
func TestCompileInvalidUTF8IsLinear(t *testing.T) {
	// not parallel-safe: enforces a wall-clock budget that other parallel
	// tests could starve under load, producing false-positive regressions.
	engine := MustNewEngine(Config{})
	src := strings.Repeat("\x80", 4096)

	start := time.Now()
	_, err := engine.Compile(src)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Compile(invalid utf-8) = nil, want error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Compile(4KB invalid utf-8) took %v, want <500ms (quadratic-error regression)", elapsed)
	}
}

func BenchmarkCombineErrors(b *testing.B) {
	errs := make([]error, 2048)
	for i := range errs {
		errs[i] = fmt.Errorf("error %d at column %d: unexpected byte", i, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = combineErrors(errs)
	}
}
