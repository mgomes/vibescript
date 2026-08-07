package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

// TestModuleSuggestionScanIsBounded pins that building did-you-mean
// candidates for a failed require does not read a module directory whole.
//
// The walk counted entries it had visited, but filepath.WalkDir reads and
// sorts each directory in full before invoking the callback, so the counter
// could not stop a single huge directory; the relative-require path called
// os.ReadDir, which does the same. Suggestions are built off a failed
// require, outside the step and memory quotas, so a tenant able to grow a
// module directory could make a typoed require read all of it (#53).
//
// Not parallel: it measures process-wide allocation.
func TestModuleSuggestionScanIsBounded(t *testing.T) {
	root := t.TempDir()
	const entries = 40000
	for i := range entries {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%06d.txt", i)), nil, 0o600); err != nil {
			t.Fatalf("seeding the directory failed: %v", err)
		}
	}
	engine := MustNewEngine(Config{ModulePaths: []string{root}})

	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	engine.moduleCandidatesUnderRoot(root)
	goruntime.ReadMemStats(&after)

	allocated := after.TotalAlloc - before.TotalAlloc
	// Reading the directory whole allocated about 10 MiB for 60k entries; the
	// bounded read stays near the walk limit's own footprint.
	if limit := uint64(4 << 20); allocated > limit {
		t.Fatalf("suggestion walk allocated %.2f MiB over a %d-entry directory, want under %.2f MiB",
			float64(allocated)/(1<<20), entries, float64(limit)/(1<<20))
	}
}

// TestModuleSuggestionsStillFound pins that bounding the read did not break
// the suggestions themselves: a near-miss require still gets its candidate.
func TestModuleSuggestionsStillFound(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "helpers.vibe"), []byte("def helper()\n  1\nend"), 0o600); err != nil {
		t.Fatalf("write module: %v", err)
	}
	engine := MustNewEngine(Config{ModulePaths: []string{root}})
	names := engine.moduleCandidatesUnderRoot(root)
	if len(names) != 1 || names[0] != "helpers" {
		t.Fatalf("candidates = %v, want [helpers]", names)
	}
}
