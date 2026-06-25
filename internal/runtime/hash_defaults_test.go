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

def dig_value_default()
  h = Hash.new(0)
  h.dig(:missing)
end

def dig_value_default_does_not_insert()
  h = Hash.new(0)
  dug = h.dig(:missing)
  { dug: dug, size: h.size }
end

def dig_into_default_value()
  h = Hash.new({ inner: 42 })
  { top: h.dig(:missing), deep: h.dig(:missing, :inner) }
end

def dig_through_scalar_default()
  h = Hash.new(0)
  h.dig(:missing, :deeper)
end

def dig_proc_default_inserts()
  h = Hash.new { |hash, key| hash[key] = "dug-" + key }
  dug = h.dig("a")
  { dug: dug, size: h.size, again: h["a"] }
end

def dig_nested_consults_inner_default()
  inner = Hash.new(7)
  outer = { a: inner }
  outer.dig(:a, :missing)
end

def values_at_value_default()
  h = Hash.new(0)
  h.values_at(:a, :b)
end

def values_at_value_default_does_not_insert()
  h = Hash.new(0)
  vals = h.values_at(:a, :b)
  { vals: vals, size: h.size }
end

def values_at_proc_default_inserts()
  h = Hash.new { |hash, key| hash[key] = key.to_s.upcase }
  vals = h.values_at(:a, :b)
  { vals: vals, size: h.size }
end

def values_at_plain_literal()
  h = { a: 1, b: 2 }
  h.values_at(:b, :c, :a)
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

func TestHashDefaultAcrossAccessors(t *testing.T) {
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

	// fetch is the one accessor that ignores the hash default, matching Ruby:
	// it falls back only to its own optional argument.
	t.Run("fetch uses its own default not the hash default", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "fetch_ignores_default", nil)
		if got.Int() != 99 {
			t.Fatalf("fetch = %v, want 99 (hash default must not apply)", got.Int())
		}
	})
}

// TestHashDefaultDig pins Hash#dig's Ruby-faithful default handling: each hash
// step is a [] access, so a missing key consults that level's value default or
// default proc (which may insert), and dig descends into whatever it resolves.
func TestHashDefaultDig(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	t.Run("value default returned for missing key", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_value_default", nil)
		if got.Int() != 0 {
			t.Fatalf("dig of a missing key = %v, want 0 (the value default)", got.Kind())
		}
	})

	t.Run("value default does not insert", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_value_default_does_not_insert", nil)
		if dug := hashDefaultsField(t, got, "dug"); dug.Int() != 0 {
			t.Fatalf("dug = %v, want 0", dug.Int())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 0 {
			t.Fatalf("size = %v, want 0 (a value default never inserts)", size.Int())
		}
	})

	t.Run("dig descends into a hash default value", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_into_default_value", nil)
		top := hashDefaultsField(t, got, "top")
		if top.Kind() != KindHash {
			t.Fatalf("top = %v, want the default hash", top.Kind())
		}
		if deep := hashDefaultsField(t, got, "deep"); deep.Int() != 42 {
			t.Fatalf("deep = %v, want 42 (dig into the default value)", deep.Int())
		}
	})

	// Vibescript deliberately returns nil rather than raising when a path
	// continues through a non-collection default (MRI raises a TypeError here).
	t.Run("digging past a scalar default yields nil", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_through_scalar_default", nil)
		if got.Kind() != KindNil {
			t.Fatalf("dig past scalar default = %v, want nil", got.Kind())
		}
	})

	t.Run("default proc fires per dig step and may insert", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_proc_default_inserts", nil)
		if dug := hashDefaultsField(t, got, "dug"); dug.String() != "dug-a" {
			t.Fatalf("dug = %q, want dug-a", dug.String())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 1 {
			t.Fatalf("size = %v, want 1 (proc inserted)", size.Int())
		}
		if again := hashDefaultsField(t, got, "again"); again.String() != "dug-a" {
			t.Fatalf("again = %q, want dug-a (entry persisted)", again.String())
		}
	})

	// Each dig level is its own [] access, so a missing key in a nested hash
	// consults that inner hash's default, not the outer receiver's.
	t.Run("nested step consults the inner hash default", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "dig_nested_consults_inner_default", nil)
		if got.Int() != 7 {
			t.Fatalf("dig nested missing key = %v, want 7 (inner default)", got.Kind())
		}
	})
}

// TestHashDefaultValuesAt pins Hash#values_at's Ruby-faithful default handling:
// each key is a [] access, so a missing key consults the value default or fires
// the default proc (which may insert), while a plain literal still yields nil.
func TestHashDefaultValuesAt(t *testing.T) {
	t.Parallel()
	script := compileScript(t, hashDefaultsScript)

	zeros := NewArray([]Value{NewInt(0), NewInt(0)})

	t.Run("value default fills each missing key", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "values_at_value_default", nil)
		if diff := valueDiff(zeros, got); diff != "" {
			t.Fatalf("values_at mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("value default does not insert", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "values_at_value_default_does_not_insert", nil)
		if diff := valueDiff(zeros, hashDefaultsField(t, got, "vals")); diff != "" {
			t.Fatalf("values_at mismatch (-want +got):\n%s", diff)
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 0 {
			t.Fatalf("size = %v, want 0 (a value default never inserts)", size.Int())
		}
	})

	t.Run("default proc fires per missing key and inserts", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "values_at_proc_default_inserts", nil)
		vals := hashDefaultsField(t, got, "vals")
		if vals.Kind() != KindArray || len(vals.Array()) != 2 {
			t.Fatalf("vals = %v, want a two-element array", vals.Kind())
		}
		if first := vals.Array()[0]; first.String() != "A" {
			t.Fatalf("vals[0] = %q, want A", first.String())
		}
		if second := vals.Array()[1]; second.String() != "B" {
			t.Fatalf("vals[1] = %q, want B", second.String())
		}
		if size := hashDefaultsField(t, got, "size"); size.Int() != 2 {
			t.Fatalf("size = %v, want 2 (proc inserted both keys)", size.Int())
		}
	})

	t.Run("plain literal yields nil for missing keys", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "values_at_plain_literal", nil)
		if got.Kind() != KindArray || len(got.Array()) != 3 {
			t.Fatalf("values_at = %v, want a three-element array", got.Kind())
		}
		arr := got.Array()
		if arr[0].Int() != 2 {
			t.Fatalf("values_at[0] = %v, want 2", arr[0].Int())
		}
		if arr[1].Kind() != KindNil {
			t.Fatalf("values_at[1] = %v, want nil (missing key in a plain literal)", arr[1].Kind())
		}
		if arr[2].Int() != 1 {
			t.Fatalf("values_at[2] = %v, want 1", arr[2].Int())
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

// TestHashDefaultSurvivesInboundRebind pins that a host-supplied hash carrying a
// Ruby-style default keeps it when bound as an argument or global. The inbound
// rebinder used to rebuild every KindHash with NewHash(entries), dropping the
// default, so a missing-key lookup inside the script returned nil instead of the
// configured default.
func TestHashDefaultSurvivesInboundRebind(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def from_arg(h)
  h[:missing]
end

def from_global()
  config[:missing]
end
`)

	t.Run("argument hash default applied", func(t *testing.T) {
		t.Parallel()
		arg := NewHashWithDefault(map[string]Value{}, NewInt(5), NewNil())
		got := callScript(t, context.Background(), script, "from_arg", []Value{arg}, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 5 {
			t.Fatalf("from_arg missing-key lookup = %#v, want int 5 from the inbound default", got)
		}
	})

	t.Run("global hash default applied", func(t *testing.T) {
		t.Parallel()
		global := NewHashWithDefault(map[string]Value{}, NewInt(9), NewNil())
		got := callScript(t, context.Background(), script, "from_global", nil, CallOptions{
			Globals: map[string]Value{"config": global},
		})
		if got.Kind() != KindInt || got.Int() != 9 {
			t.Fatalf("from_global missing-key lookup = %#v, want int 9 from the inbound default", got)
		}
	})
}

// TestHostCloneScanTerminatesOnDefaultCycle pins that the host-return clone scan
// threads its cycle state through a hash's default value. A default that points
// back to its own hash through another collection (d = {}; h = Hash.new(d);
// d[:h] = h) used to restart the scan with fresh state for each default, so the
// h.default -> d[:h] -> h back-edge was never recorded and Script.Call recursed
// until the stack overflowed while deciding whether the return value needed a
// host clone. The call must terminate and return the cyclic structure.
func TestHostCloneScanTerminatesOnDefaultCycle(t *testing.T) {
	t.Parallel()

	// d is a plain hash; h is Hash.new(d) so h's default value is d. Closing
	// d[:h] = h forms the cycle h.default(=d) -> d[:h](=h) -> h.
	dEntries := map[string]Value{}
	d := NewHash(dEntries)
	h := NewHashWithDefault(map[string]Value{}, d, NewNil())
	dEntries["h"] = h

	script := compileScript(t, `
def echo(value)
  value
end
`)

	// Before the fix this call recursed without bound and overflowed the stack;
	// now the shared scan state records the back-edge and the call returns.
	result := callScript(t, context.Background(), script, "echo", []Value{h}, CallOptions{})
	if result.Kind() != KindHash {
		t.Fatalf("echo(cyclic hash) = %v, want a hash returned without overflow", result.Kind())
	}
}
