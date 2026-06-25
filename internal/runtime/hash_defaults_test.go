package runtime

import (
	"context"
	"testing"
)

// hashDefaultsScript bundles every script function the default tests exercise so
// each subtest compiles once and calls the function it needs.
const hashDefaultsScript = `
def value_default_no_insert()
  h = Hash.new(0)
  missed = h[:missing]
  { missed: missed, size: h.size }
end

def value_default_reader()
  h = Hash.new(7)
  { default: h.default, with_key: h.default(:any) }
end

def bare_new_default()
  h = Hash.new
  { default: h.default, missed: h[:a], size: h.size }
end

def proc_default_with_insert()
  h = Hash.new { |hash, key| hash[key] = "made-" + key }
  first = h["a"]
  second = h["a"]
  { first: first, second: second, size: h.size }
end

def proc_default_no_insert()
  h = Hash.new { |hash, key| "computed-" + key }
  v = h["x"]
  { value: v, size: h.size }
end

def proc_default_value()
  h = Hash.new { |hash, key| 1 }
  h.default
end

def proc_default_with_key()
  h = Hash.new { |hash, key| "for-" + key }
  v = h.default("k")
  { value: v, size: h.size }
end

def merge_preserves_value_default()
  h = Hash.new(0)
  merged = h.merge({ a: 1 })
  { a: merged[:a], missing: merged[:b] }
end

def merge_preserves_proc_default()
  h = Hash.new { |hash, key| 42 }
  merged = h.merge({ a: 1 })
  { a: merged[:a], computed: merged[:b] }
end

def select_drops_default()
  h = Hash.new(0)
  h[:a] = 1
  filtered = h.select { |k, v| v > 0 }
  { a: filtered[:a], missing: filtered[:c] }
end

def transform_values_drops_default()
  h = Hash.new(0)
  h[:a] = 1
  doubled = h.transform_values { |v| v * 2 }
  { a: doubled[:a], missing: doubled[:c] }
end

def plain_literal_missing()
  h = { a: 1 }
  { present: h[:a], missing: h[:missing] }
end

def fetch_ignores_default()
  h = Hash.new(0)
  h.fetch(:missing, 99)
end

def dig_ignores_default()
  h = Hash.new(0)
  h.dig(:missing)
end

def default_proc_present()
  h = Hash.new { |hash, key| 1 }
  h.default_proc
end

def default_proc_absent()
  h = {}
  h.default_proc
end

def too_many_args()
  Hash.new(0, 1)
end

def value_and_block()
  Hash.new(0) { |hash, key| 1 }
end
`

func hashDefaultsField(t *testing.T, h Value, key string) Value {
	t.Helper()
	if h.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", h.Kind())
	}
	v, ok := h.Hash()[key]
	if !ok {
		t.Fatalf("missing field %q in %v", key, h.Hash())
	}
	return v
}

func TestHashNewDefaultValue(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	t.Run("missing key returns default without inserting", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "value_default_no_insert", nil)
		if missed := hashDefaultsField(t, got, "missed"); missed.Int() != 0 {
			t.Fatalf("missed = %v, want 0", missed.Int())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 0 {
			t.Fatalf("size = %v, want 0 (default lookup must not insert)", size.Int())
		}
	})

	t.Run("default reader returns configured value", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "value_default_reader", nil)
		if def := hashDefaultsField(t, got, "default"); def.Int() != 7 {
			t.Fatalf("default = %v, want 7", def.Int())
		}
		if withKey := hashDefaultsField(t, got, "with_key"); withKey.Int() != 7 {
			t.Fatalf("default(:any) = %v, want 7", withKey.Int())
		}
	})

	t.Run("bare new has nil default", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "bare_new_default", nil)
		if v := hashDefaultsField(t, got, "default"); v.Kind() != KindNil {
			t.Fatalf("default = %v, want nil for bare Hash.new", v.Kind())
		}
		if v := hashDefaultsField(t, got, "missed"); v.Kind() != KindNil {
			t.Fatalf("missing key = %v, want nil for bare Hash.new", v.Kind())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 0 {
			t.Fatalf("size = %v, want 0", size.Int())
		}
	})
}

func TestHashNewDefaultProc(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	t.Run("proc that stores inserts and persists", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "proc_default_with_insert", nil)
		if first := hashDefaultsField(t, got, "first"); first.String() != "made-a" {
			t.Fatalf("first = %q, want made-a", first.String())
		}
		if second := hashDefaultsField(t, got, "second"); second.String() != "made-a" {
			t.Fatalf("second = %q, want made-a", second.String())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 1 {
			t.Fatalf("size = %v, want 1 (proc inserted once)", size.Int())
		}
	})

	t.Run("proc without storing does not insert", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "proc_default_no_insert", nil)
		if v := hashDefaultsField(t, got, "value"); v.String() != "computed-x" {
			t.Fatalf("value = %q, want computed-x", v.String())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 0 {
			t.Fatalf("size = %v, want 0 (proc did not store)", size.Int())
		}
	})

	t.Run("default value is nil when proc configured", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "proc_default_value", nil)
		if got.Kind() != KindNil {
			t.Fatalf("default = %v, want nil when only a proc is configured", got.Kind())
		}
	})

	t.Run("default with a key invokes the proc", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "proc_default_with_key", nil)
		if v := hashDefaultsField(t, got, "value"); v.String() != "for-k" {
			t.Fatalf("default(\"k\") = %q, want for-k", v.String())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 0 {
			t.Fatalf("size = %v, want 0 (proc returned without storing)", size.Int())
		}
	})

	t.Run("default_proc returns the block", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "default_proc_present", nil)
		if got.Kind() != KindBlock {
			t.Fatalf("default_proc = %v, want a block", got.Kind())
		}
	})

	t.Run("default_proc is nil for a plain hash", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "default_proc_absent", nil)
		if got.Kind() != KindNil {
			t.Fatalf("default_proc = %v, want nil for a plain hash", got.Kind())
		}
	})
}

func TestHashDefaultTransformPropagation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	tests := []struct {
		name      string
		fn        string
		field     string
		wantInt   int64
		preserves bool
	}{
		{name: "merge preserves value default", fn: "merge_preserves_value_default", field: "missing", wantInt: 0, preserves: true},
		{name: "merge preserves proc default", fn: "merge_preserves_proc_default", field: "computed", wantInt: 42, preserves: true},
		{name: "select drops default", fn: "select_drops_default", field: "missing"},
		{name: "transform_values drops default", fn: "transform_values_drops_default", field: "missing"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			v := hashDefaultsField(t, got, tc.field)
			if tc.preserves {
				if v.Int() != tc.wantInt {
					t.Fatalf("%s = %v, want %v (default preserved)", tc.field, v.Int(), tc.wantInt)
				}
				return
			}
			if v.Kind() != KindNil {
				t.Fatalf("%s = %v, want nil (default dropped)", tc.field, v.Kind())
			}
		})
	}
}

func TestHashDefaultIgnoredByOtherAccessors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	t.Run("plain literal missing key stays nil", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "plain_literal_missing", nil)
		if v := hashDefaultsField(t, got, "present"); v.Int() != 1 {
			t.Fatalf("present = %v, want 1", v.Int())
		}
		if v := hashDefaultsField(t, got, "missing"); v.Kind() != KindNil {
			t.Fatalf("plain hash missing key = %v, want nil", v.Kind())
		}
	})

	t.Run("fetch uses its own default not the hash default", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "fetch_ignores_default", nil)
		if got.Int() != 99 {
			t.Fatalf("fetch = %v, want 99 (hash default must not apply)", got.Int())
		}
	})

	t.Run("dig returns nil for missing key", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_ignores_default", nil)
		if got.Kind() != KindNil {
			t.Fatalf("dig of a missing key = %v, want nil (hash default must not apply)", got.Kind())
		}
	})
}

func TestHashNewArgumentErrors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	tests := []struct {
		name string
		fn   string
		want string
	}{
		{name: "too many positional args", fn: "too_many_args", want: "Hash.new expects at most one default value"},
		{name: "value and block together", fn: "value_and_block", want: "Hash.new cannot take both a default value and a block"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestHashDefaultEqualityAndRendering(t *testing.T) {
	t.Parallel()

	// Two hashes with identical entries but different defaults compare equal:
	// Ruby's Hash#== ignores default metadata.
	withDefault := NewHashWithDefault(map[string]Value{"a": NewInt(1)}, NewInt(0), NewNil())
	plain := NewHash(map[string]Value{"a": NewInt(1)})
	if !withDefault.Equal(plain) {
		t.Fatalf("hashes with same entries should be equal regardless of default")
	}

	// Rendering shows only the entries, never the default.
	if got := withDefault.String(); got != "{a: 1}" {
		t.Fatalf("String() = %q, want {a: 1}", got)
	}
}

func TestHashDefaultSurvivesHostBoundary(t *testing.T) {
	t.Parallel()

	// A hash returned to the host carries its default through the host clone, so
	// re-entering a missing-key lookup still yields the default value. The script
	// returns the hash directly; Call clones it for the host boundary.
	script := compileScript(t, `
def make()
  Hash.new(5)
end
`)
	result, err := script.Call(context.Background(), "make", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call make: %v", err)
	}
	if got := hashDefaultValue(result); got.Int() != 5 {
		t.Fatalf("cloned hash default = %v, want 5", got.Int())
	}
}
