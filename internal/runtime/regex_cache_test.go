package runtime

import "testing"

func TestRegexCacheReusesCompiledPattern(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(2)
	first, err := cache.compile("ID-[0-9]+")
	if err != nil {
		t.Fatalf("regexCache.compile(valid) error = %v, want nil", err)
	}
	second, err := cache.compile("ID-[0-9]+")
	if err != nil {
		t.Fatalf("regexCache.compile(cached) error = %v, want nil", err)
	}
	if first != second {
		t.Fatalf("regexCache.compile(cached) returned different regexp pointers")
	}
}

func TestRegexCacheEvictsLeastRecentlyUsedPattern(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(2)
	if _, err := cache.compile("first"); err != nil {
		t.Fatalf("regexCache.compile(first) error = %v, want nil", err)
	}
	if _, err := cache.compile("second"); err != nil {
		t.Fatalf("regexCache.compile(second) error = %v, want nil", err)
	}
	if _, err := cache.compile("third"); err != nil {
		t.Fatalf("regexCache.compile(third) error = %v, want nil", err)
	}
	if _, ok := cache.entries["first"]; ok {
		t.Fatalf("regexCache retained least recently used pattern")
	}
	if _, ok := cache.entries["second"]; !ok {
		t.Fatalf("regexCache evicted second pattern, want retained")
	}
	if _, ok := cache.entries["third"]; !ok {
		t.Fatalf("regexCache evicted third pattern, want retained")
	}
}

func TestRegexCacheDoesNotStoreInvalidPattern(t *testing.T) {
	t.Parallel()

	cache := newRegexCache(2)
	if _, err := cache.compile("["); err == nil {
		t.Fatalf("regexCache.compile(invalid) error = nil, want non-nil")
	}
	if len(cache.entries) != 0 {
		t.Fatalf("regexCache.compile(invalid) cached %d entries, want 0", len(cache.entries))
	}
}
