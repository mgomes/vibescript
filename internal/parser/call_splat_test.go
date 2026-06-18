package parser

import (
	"strings"
	"testing"
)

func TestParserRejectsCallSplat(t *testing.T) {
	t.Parallel()

	source := `def run
  sum(*xs)
end`

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "call splat is not supported"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}

func TestParserRejectsKeywordSplat(t *testing.T) {
	t.Parallel()

	source := `def run
  fetch(**opts)
end`

	_, errs := parseSource(t, source)
	if len(errs) != 1 {
		t.Fatalf("parseSource(%q) errors = %d, want 1: %v", source, len(errs), errs)
	}
	if got, want := errs[0].Error(), "keyword splat is not supported"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}

func TestParserRejectsTaskSpawnForwardingSplats(t *testing.T) {
	t.Parallel()

	source := `def run
  args = [1]
  opts = {}
  Tasks.run do |tasks|
    tasks.spawn(:prepare_user, *args, **opts)
  end
end`

	_, errs := parseSource(t, source)
	if len(errs) != 2 {
		t.Fatalf("parseSource(%q) errors = %d, want 2: %v", source, len(errs), errs)
	}
	for _, want := range []string{"call splat is not supported", "keyword splat is not supported"} {
		found := false
		for _, err := range errs {
			if strings.Contains(err.Error(), want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("parseSource(%q) errors = %v, want substring %q", source, errs, want)
		}
	}
}
