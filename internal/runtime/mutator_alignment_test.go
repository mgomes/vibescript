package runtime

import (
	"context"
	"testing"
)

// This file pins the Ruby-aligned mutator contracts introduced for issue #542:
// mutator-shaped array and hash methods mutate their receiver in place, return
// the receiver (or the removed value / nil-on-no-op for bang forms), and the
// mutation is visible through every alias of the receiver.

// TestArrayMutatorReturnContracts is the per-method contract matrix: each case
// runs a mutator on a fresh receiver and pins (1) the return value, (2) the
// receiver's state afterwards, and (3) whether the return value is the
// receiver itself (Ruby's `self`-returning mutators).
func TestArrayMutatorReturnContracts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		// script yields { ret:, values:, same: } — same is meaningful only
		// when wantSame is true.
		wantRet    Value
		wantValues []Value
		wantSame   bool
	}{
		{
			name:       "push returns self",
			expr:       "values = [1, 2]\n  ret = values.push(3, 4)",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)}),
			wantValues: []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4)},
			wantSame:   true,
		},
		{
			name:       "unshift returns self",
			expr:       "values = [3]\n  ret = values.unshift(1, 2)",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
			wantValues: []Value{NewInt(1), NewInt(2), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "pop returns removed element",
			expr:       "values = [1, 2, 3]\n  ret = values.pop",
			wantRet:    NewInt(3),
			wantValues: []Value{NewInt(1), NewInt(2)},
		},
		{
			name:       "pop on empty returns nil",
			expr:       "values = []\n  ret = values.pop",
			wantRet:    NewNil(),
			wantValues: []Value{},
		},
		{
			name:       "pop(n) returns removed tail in order",
			expr:       "values = [1, 2, 3]\n  ret = values.pop(2)",
			wantRet:    NewArray([]Value{NewInt(2), NewInt(3)}),
			wantValues: []Value{NewInt(1)},
		},
		{
			name:       "shift returns removed element",
			expr:       "values = [1, 2, 3]\n  ret = values.shift",
			wantRet:    NewInt(1),
			wantValues: []Value{NewInt(2), NewInt(3)},
		},
		{
			name:       "shift(n) returns removed head",
			expr:       "values = [1, 2, 3]\n  ret = values.shift(2)",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2)}),
			wantValues: []Value{NewInt(3)},
		},
		{
			name:       "delete returns the removed element",
			expr:       "values = [1, 2, 2, 3]\n  ret = values.delete(2)",
			wantRet:    NewInt(2),
			wantValues: []Value{NewInt(1), NewInt(3)},
		},
		{
			name:       "delete miss returns nil and keeps receiver",
			expr:       "values = [1, 2]\n  ret = values.delete(9)",
			wantRet:    NewNil(),
			wantValues: []Value{NewInt(1), NewInt(2)},
		},
		{
			name:       "insert returns self",
			expr:       "values = [1, 3]\n  ret = values.insert(1, 2)",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
			wantValues: []Value{NewInt(1), NewInt(2), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "clear returns self emptied",
			expr:       "values = [1, 2]\n  ret = values.clear",
			wantRet:    NewArray([]Value{}),
			wantValues: []Value{},
			wantSame:   true,
		},
		{
			name:       "fill returns self",
			expr:       "values = [1, 2, 3]\n  ret = values.fill(0)",
			wantRet:    NewArray([]Value{NewInt(0), NewInt(0), NewInt(0)}),
			wantValues: []Value{NewInt(0), NewInt(0), NewInt(0)},
			wantSame:   true,
		},
		{
			name:       "delete_if returns self even when unchanged",
			expr:       "values = [1, 3]\n  ret = values.delete_if { |v| v > 9 }",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(3)}),
			wantValues: []Value{NewInt(1), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "delete_if removes truthy matches",
			expr:       "values = [1, 2, 3, 4]\n  ret = values.delete_if { |v| v % 2 == 0 }",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(3)}),
			wantValues: []Value{NewInt(1), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "keep_if returns self",
			expr:       "values = [1, 2, 3, 4]\n  ret = values.keep_if { |v| v % 2 == 0 }",
			wantRet:    NewArray([]Value{NewInt(2), NewInt(4)}),
			wantValues: []Value{NewInt(2), NewInt(4)},
			wantSame:   true,
		},
		{
			name:       "select! returns self when changed",
			expr:       "values = [1, 2, 3]\n  ret = values.select! { |v| v > 1 }",
			wantRet:    NewArray([]Value{NewInt(2), NewInt(3)}),
			wantValues: []Value{NewInt(2), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "select! returns nil when unchanged",
			expr:       "values = [2, 3]\n  ret = values.select! { |v| v > 1 }",
			wantRet:    NewNil(),
			wantValues: []Value{NewInt(2), NewInt(3)},
		},
		{
			name:       "reject! returns self when changed",
			expr:       "values = [1, 2, 3]\n  ret = values.reject! { |v| v == 2 }",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(3)}),
			wantValues: []Value{NewInt(1), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "reject! returns nil when unchanged",
			expr:       "values = [1, 3]\n  ret = values.reject! { |v| v == 2 }",
			wantRet:    NewNil(),
			wantValues: []Value{NewInt(1), NewInt(3)},
		},
		{
			name:       "uniq! returns self when deduped",
			expr:       "values = [1, 2, 2]\n  ret = values.uniq!",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2)}),
			wantValues: []Value{NewInt(1), NewInt(2)},
			wantSame:   true,
		},
		{
			name:       "uniq! returns nil when already unique",
			expr:       "values = [1, 2]\n  ret = values.uniq!",
			wantRet:    NewNil(),
			wantValues: []Value{NewInt(1), NewInt(2)},
		},
		{
			name:       "compact! returns self when nils removed",
			expr:       "values = [1, nil, 2]\n  ret = values.compact!",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2)}),
			wantValues: []Value{NewInt(1), NewInt(2)},
			wantSame:   true,
		},
		{
			name:       "compact! returns nil when no nils",
			expr:       "values = [1, 2]\n  ret = values.compact!",
			wantRet:    NewNil(),
			wantValues: []Value{NewInt(1), NewInt(2)},
		},
		{
			name:       "reverse! always returns self",
			expr:       "values = [1, 2, 3]\n  ret = values.reverse!",
			wantRet:    NewArray([]Value{NewInt(3), NewInt(2), NewInt(1)}),
			wantValues: []Value{NewInt(3), NewInt(2), NewInt(1)},
			wantSame:   true,
		},
		{
			name:       "sort! always returns self",
			expr:       "values = [3, 1, 2]\n  ret = values.sort!",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
			wantValues: []Value{NewInt(1), NewInt(2), NewInt(3)},
			wantSame:   true,
		},
		{
			name:       "map! always returns self",
			expr:       "values = [1, 2]\n  ret = values.map! { |v| v * 10 }",
			wantRet:    NewArray([]Value{NewInt(10), NewInt(20)}),
			wantValues: []Value{NewInt(10), NewInt(20)},
			wantSame:   true,
		},
		{
			name:       "shovel returns self",
			expr:       "values = [1]\n  ret = values << 2",
			wantRet:    NewArray([]Value{NewInt(1), NewInt(2)}),
			wantValues: []Value{NewInt(1), NewInt(2)},
			wantSame:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\n  { ret: ret, values: values, same: ret.equal?(values) }\nend"
			script := compileScript(t, source)
			result := callFunc(t, script, "run", nil).Hash()
			if diff := valueDiff(tc.wantRet, result["ret"]); diff != "" {
				t.Fatalf("return value mismatch (-want +got):\n%s", diff)
			}
			compareArrays(t, result["values"], tc.wantValues)
			if tc.wantSame && !result["same"].Bool() {
				t.Fatal("mutator must return the receiver itself")
			}
		})
	}
}

// TestArrayMutatorAliasVisibility pins Ruby's reference semantics: two
// variables bound to the same array both observe every in-place mutation,
// including mutations reached through hash values and nested arrays.
func TestArrayMutatorAliasVisibility(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def direct_alias()
      a = [1, 2, 3]
      b = a
      a.push(4)
      a.shift
      a.map! { |v| v * 10 }
      b
    end

    def hash_value_alias()
      a = [1]
      h = { list: a }
      a.push(2)
      h[:list].push(3)
      { via_hash: h[:list], direct: a }
    end

    def nested_alias()
      inner = [1]
      outer = [inner, inner]
      outer[0].push(2)
      outer[1]
    end
    `)

	compareArrays(t, callFunc(t, script, "direct_alias", nil),
		[]Value{NewInt(20), NewInt(30), NewInt(40)})

	hashAlias := callFunc(t, script, "hash_value_alias", nil).Hash()
	compareArrays(t, hashAlias["via_hash"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, hashAlias["direct"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	compareArrays(t, callFunc(t, script, "nested_alias", nil),
		[]Value{NewInt(1), NewInt(2)})
}

// TestArrayMutationDuringIteration pins the iteration convention for the
// mutable-collection era: iteration helpers walk the elements captured when
// iteration began. Structural changes (push/pop) made by the block take effect
// on the receiver but never extend or shorten the in-flight iteration, so a
// self-appending each terminates.
func TestArrayMutationDuringIteration(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def push_during_each()
      values = [1, 2]
      count = 0
      values.each do |v|
        values.push(v)
        count += 1
      end
      { values: values, count: count }
    end

    def clear_during_each()
      values = [1, 2, 3]
      seen = []
      values.each do |v|
        seen = seen.push(v)
        values.clear if v == 1
      end
      { values: values, seen: seen }
    end
    `)

	pushed := callFunc(t, script, "push_during_each", nil).Hash()
	compareArrays(t, pushed["values"], []Value{NewInt(1), NewInt(2), NewInt(1), NewInt(2)})
	if got := pushed["count"]; !got.Equal(NewInt(2)) {
		t.Fatalf("iteration count = %v, want 2 (iteration is fixed at entry)", got)
	}

	// clear drops the backing entirely, so the snapshot iteration still sees
	// the original elements while the receiver ends up holding only what was
	// pushed afterwards (nothing here).
	cleared := callFunc(t, script, "clear_during_each", nil).Hash()
	compareArrays(t, cleared["values"], []Value{})
	compareArrays(t, cleared["seen"], []Value{NewInt(1), NewInt(2), NewInt(3)})
}

// TestArrayMutatorHostIsolation pins the host boundary: arguments and globals
// are deep-cloned per call, so in-place mutators applied inside the script
// never leak back into the host's original values.
func TestArrayMutatorHostIsolation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def mutate_arg(values)
      values.push(99)
      values.shift
      values
    end

    def mutate_global()
      items.push(99)
      items
    end
    `)

	original := NewArray([]Value{NewInt(1), NewInt(2)})
	got := callFunc(t, script, "mutate_arg", []Value{original})
	compareArrays(t, got, []Value{NewInt(2), NewInt(99)})
	compareArrays(t, original, []Value{NewInt(1), NewInt(2)})

	global := NewArray([]Value{NewInt(1)})
	result, err := script.Call(context.Background(), "mutate_global", nil, CallOptions{
		Globals: map[string]Value{"items": global},
	})
	if err != nil {
		t.Fatalf("mutate_global: %v", err)
	}
	compareArrays(t, result, []Value{NewInt(1), NewInt(99)})
	compareArrays(t, global, []Value{NewInt(1)})
}

// TestArrayMutatorAsHashKey pins the interaction between mutable arrays and
// hash keys: the key is canonicalized from its contents at insertion time, so
// mutating the array afterwards does not re-home the entry — lookups by the
// original contents still hit, and lookups by the mutated contents miss,
// mirroring Ruby's behavior before Hash#rehash.
func TestArrayMutatorAsHashKey(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def mutated_key()
      k = [1]
      h = {}
      h[k] = "v"
      k.push(2)
      { by_original: h[[1]], by_mutated: h[[1, 2]], size: h.size }
    end
    `)

	result := callFunc(t, script, "mutated_key", nil).Hash()
	if got := result["by_original"]; !got.Equal(NewString("v")) {
		t.Fatalf("lookup by original contents = %v, want \"v\"", got)
	}
	if got := result["by_mutated"]; got.Kind() != KindNil {
		t.Fatalf("lookup by mutated contents = %v, want nil", got)
	}
	if got := result["size"]; !got.Equal(NewInt(1)) {
		t.Fatalf("hash size = %v, want 1", got)
	}
}

// TestArrayInPlaceGrowthTripsMemoryQuota mirrors the classic push-reassignment
// quota fixture with the bare in-place forms: growth through push and << must
// charge the quota before allocating, so an unbounded accumulation loop is
// rejected with a limit error rather than allocating past the cap.
func TestArrayInPlaceGrowthTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "bare_push_loop",
			source: `def run()
  items = []
  for i in 1..200
    items.push("abcdefghij")
  end
  items.size
end`,
		},
		{
			name: "bare_shovel_loop",
			source: `def run()
  items = []
  for i in 1..200
    items << "abcdefghij"
  end
  items.size
end`,
		},
		{
			name: "unshift_loop",
			source: `def run()
  items = []
  for i in 1..200
    items.unshift("abcdefghij")
  end
  items.size
end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 2048}, tc.source)
			requireRunMemoryQuotaError(t, script, nil, CallOptions{})

			allowed := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 1 << 20}, tc.source)
			got := callFunc(t, allowed, "run", nil)
			if !got.Equal(NewInt(200)) {
				t.Fatalf("under a generous quota run() = %v, want 200", got)
			}
		})
	}
}

// TestArrayMutatorsShareIdentityAcrossDup pins that dup still detaches: a
// deep-cloned copy has its own wrapper, so mutating the original never touches
// the copy and vice versa.
func TestArrayMutatorsShareIdentityAcrossDup(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def dup_detaches()
      a = [1, 2]
      b = a.dup
      a.push(3)
      b.push(9)
      { a: a, b: b, same: a.equal?(b) }
    end
    `)

	result := callFunc(t, script, "dup_detaches", nil).Hash()
	compareArrays(t, result["a"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, result["b"], []Value{NewInt(1), NewInt(2), NewInt(9)})
	if result["same"].Bool() {
		t.Fatal("dup must produce a distinct array object")
	}
}
