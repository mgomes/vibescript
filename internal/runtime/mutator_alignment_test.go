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

// TestArrayMutatorIndependence is the deliberate inversion of the alias
// visibility this suite used to pin. Arrays are values (ADR-006 item 2): a
// second binding, a hash entry, and a second element slot are each another
// logical value, so a write through one of them cannot be seen through any
// other. Every case below asserted the opposite before the change.
func TestArrayMutatorIndependence(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def direct_alias()
      a = [1, 2, 3]
      b = a
      a.push(4)
      a.shift
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

    def addressed_path()
      outer = [[1]]
      outer[0].push(2)
      outer[0]
    end
    `)

	// b keeps the value a had when it was bound, not the one a went on to have.
	compareArrays(t, callFunc(t, script, "direct_alias", nil),
		[]Value{NewInt(1), NewInt(2), NewInt(3)})

	// The hash entry and the local are two values from the moment the literal
	// stored one into the other, so each sees only its own write.
	hashAlias := callFunc(t, script, "hash_value_alias", nil).Hash()
	compareArrays(t, hashAlias["via_hash"], []Value{NewInt(1), NewInt(3)})
	compareArrays(t, hashAlias["direct"], []Value{NewInt(1), NewInt(2)})

	// Two element slots holding one value are still two values.
	compareArrays(t, callFunc(t, script, "nested_alias", nil), []Value{NewInt(1)})

	// The write does reach the path it addresses: outer[0] is an addressable
	// root, so pushing through it updates the element the source names.
	compareArrays(t, callFunc(t, script, "addressed_path", nil),
		[]Value{NewInt(1), NewInt(2)})
}

// TestArrayConcatAccumulatorPreservesIdentity pins that the `x = x + [...]`
// accumulator fast path never forks array identity: reading the variable
// settles the hidden buffer but keeps the same wrapper, so an alias taken
// after a concat-reassignment observes every later in-place mutation, exactly
// like an alias of a literal-built array. Expectations verified against Ruby
// 3.4 (`a = a + [x]` rebinds to a new object; aliases of the pre-rebind object
// keep its state).
func TestArrayConcatAccumulatorPreservesIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def direct_alias()
      a = [1]
      a = a + [2]
      b = a
      a << 3
      { a: a, b: b, same: a.equal?(b) }
    end

    def hash_value_alias()
      a = [1]
      a = a + [2]
      h = { list: a }
      a << 3
      { a: a, via_hash: h[:list] }
    end

    def element_alias()
      a = [1]
      a = a + [2]
      outer = [a]
      a << 3
      { a: a, via_element: outer[0] }
    end

    def interleaved()
      a = []
      a = a + [1]
      b = a
      a = a + [2]
      a << 3
      { a: a, b: b }
    end

    def element_write_after_rebind()
      a = [1]
      a = a + [2]
      b = a
      a = a + [3]
      a[0] = 9
      { a: a, b: b }
    end

    def loop_alias()
      a = []
      b = nil
      for i in 1..4
        a = a + [i]
        if i == 2
          b = a
        end
      end
      a << 9
      { a: a, b: b }
    end
    `)

	direct := callFunc(t, script, "direct_alias", nil).Hash()
	compareArrays(t, direct["a"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, direct["b"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	if !direct["same"].Bool() {
		t.Fatal("alias of a concat-built array must be the same object")
	}

	hashAlias := callFunc(t, script, "hash_value_alias", nil).Hash()
	compareArrays(t, hashAlias["a"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, hashAlias["via_hash"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	element := callFunc(t, script, "element_alias", nil).Hash()
	compareArrays(t, element["a"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, element["via_element"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	// Ruby: b aliases the [1] object; the later a = a + [2] rebinds a to a
	// fresh object, so b keeps [1] while a accumulates [1, 2, 3].
	interleaved := callFunc(t, script, "interleaved", nil).Hash()
	compareArrays(t, interleaved["a"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	compareArrays(t, interleaved["b"], []Value{NewInt(1)})

	// Ruby: the rebound a is a fresh copy, so a[0] = 9 must not reach b even
	// though the fast path built both from one backing buffer.
	written := callFunc(t, script, "element_write_after_rebind", nil).Hash()
	compareArrays(t, written["a"], []Value{NewInt(9), NewInt(2), NewInt(3)})
	compareArrays(t, written["b"], []Value{NewInt(1), NewInt(2)})

	loop := callFunc(t, script, "loop_alias", nil).Hash()
	compareArrays(t, loop["a"], []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(4), NewInt(9)})
	compareArrays(t, loop["b"], []Value{NewInt(1), NewInt(2)})
}

// TestArrayConcatBuiltArraysMutateLikeLiterals pins that an array grown via
// the concat fast path is indistinguishable from a push-built or literal one
// when passed to a function that mutates its argument (Ruby reference
// semantics: the callee mutates the caller's object).
func TestArrayConcatBuiltArraysMutateLikeLiterals(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def addto(t)
      t << 100
    end

    def via_concat()
      a = [1]
      a = a + [2]
      addto(a)
      a
    end

    def via_push()
      a = [1]
      a.push(2)
      addto(a)
      a
    end

    def via_literal()
      a = [1, 2]
      addto(a)
      a
    end
    `)

	want := []Value{NewInt(1), NewInt(2), NewInt(100)}
	for _, fn := range []string{"via_concat", "via_push", "via_literal"} {
		compareArrays(t, callFunc(t, script, fn, nil), want)
	}
}

// TestArrayConcatAccumulatorEscapeSettles pins the two non-read escape routes
// of a live accumulator. A block whose last statement is the concat
// reassignment hands the accumulator itself out as the block value (Ruby: the
// assignment's value is the new object bound to the variable), and a method
// whose implicit return is the reassignment hands it to the caller. The
// escaping value and the variable must stay one object, and later concats
// through the variable must never share backing with the escaped value.
func TestArrayConcatAccumulatorEscapeSettles(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def block_result_identity()
      a = [0]
      r = [1, 2].map { |i| a = a + [i] }
      a << 99
      { last_same: r[1].equal?(a), last: r[1], first: r[0] }
    end
    `)

	block := callFunc(t, script, "block_result_identity", nil).Hash()
	if !block["last_same"].Bool() {
		t.Fatal("last block result must be the accumulator object itself")
	}
	compareArrays(t, block["last"], []Value{NewInt(0), NewInt(1), NewInt(2), NewInt(99)})
	compareArrays(t, block["first"], []Value{NewInt(0), NewInt(1)})
}

// TestArrayConcatAccumulatorSurvivesInterleavedMutators pins the fast path's
// staleness guard: an in-place mutator that replaces the binding's element
// backing between two concat-reassignments must not resurrect the stale
// buffer contents.
func TestArrayConcatAccumulatorSurvivesInterleavedMutators(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def pop_between_concats()
      a = [1]
      a = a + [2]
      a.pop
      a = a + [7]
      a
    end

    def shift_between_concats()
      a = [1]
      a = a + [2]
      a = a + [3]
      a.shift
      a = a + [8]
      a
    end
    `)

	compareArrays(t, callFunc(t, script, "pop_between_concats", nil),
		[]Value{NewInt(1), NewInt(7)})
	compareArrays(t, callFunc(t, script, "shift_between_concats", nil),
		[]Value{NewInt(2), NewInt(3), NewInt(8)})
}

// TestArrayConcatHostIsolation re-pins the host boundary through the concat
// fast path: growing a host-provided argument or global with x = x + [...]
// and then mutating in place must never leak back into the host's value.
func TestArrayConcatHostIsolation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def concat_arg(values)
      values = values + [10]
      values << 11
      values
    end

    def concat_global()
      items = items + [10]
      items << 11
      items
    end
    `)

	original := NewArray([]Value{NewInt(1), NewInt(2)})
	got := callFunc(t, script, "concat_arg", []Value{original})
	compareArrays(t, got, []Value{NewInt(1), NewInt(2), NewInt(10), NewInt(11)})
	compareArrays(t, original, []Value{NewInt(1), NewInt(2)})

	global := NewArray([]Value{NewInt(1)})
	result, err := script.Call(context.Background(), "concat_global", nil, CallOptions{
		Globals: map[string]Value{"items": global},
	})
	if err != nil {
		t.Fatalf("concat_global: %v", err)
	}
	compareArrays(t, result, []Value{NewInt(1), NewInt(10), NewInt(11)})
	compareArrays(t, global, []Value{NewInt(1)})
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

// TestHashMutatorReturnContracts is the per-method contract matrix for the
// mutator-shaped hash methods: each case pins the return value, the receiver's
// state afterwards, and whether the return value is the receiver itself.
func TestHashMutatorReturnContracts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		expr     string
		wantRet  Value
		wantHash map[string]Value
		wantSame bool
	}{
		{
			name:     "delete returns the removed value",
			expr:     "h = { a: 1, b: 2 }\n  ret = h.delete(:a)",
			wantRet:  NewInt(1),
			wantHash: map[string]Value{"b": NewInt(2)},
		},
		{
			name:     "delete miss returns nil and keeps receiver",
			expr:     "h = { a: 1 }\n  ret = h.delete(:z)",
			wantRet:  NewNil(),
			wantHash: map[string]Value{"a": NewInt(1)},
		},
		{
			name:     "delete miss returns the block result",
			expr:     "h = { a: 1 }\n  ret = h.delete(:z) { |k| k }",
			wantRet:  NewSymbol("z"),
			wantHash: map[string]Value{"a": NewInt(1)},
		},
		{
			name:     "clear returns self emptied",
			expr:     "h = { a: 1, b: 2 }\n  ret = h.clear",
			wantRet:  NewHash(map[string]Value{}),
			wantHash: map[string]Value{},
			wantSame: true,
		},
		{
			name:     "delete_if returns self",
			expr:     "h = { a: 1, b: 2, c: 3 }\n  ret = h.delete_if { |k, v| v > 1 }",
			wantRet:  NewHash(map[string]Value{"a": NewInt(1)}),
			wantHash: map[string]Value{"a": NewInt(1)},
			wantSame: true,
		},
		{
			name:     "keep_if returns self",
			expr:     "h = { a: 1, b: 2, c: 3 }\n  ret = h.keep_if { |k, v| v > 1 }",
			wantRet:  NewHash(map[string]Value{"b": NewInt(2), "c": NewInt(3)}),
			wantHash: map[string]Value{"b": NewInt(2), "c": NewInt(3)},
			wantSame: true,
		},
		{
			name:     "store returns the stored value",
			expr:     "h = { a: 1 }\n  ret = h.store(:b, 2)",
			wantRet:  NewInt(2),
			wantHash: map[string]Value{"a": NewInt(1), "b": NewInt(2)},
		},
		{
			name:     "replace returns self with the adopted entries",
			expr:     "h = { a: 1 }\n  ret = h.replace({ x: 9 })",
			wantRet:  NewHash(map[string]Value{"x": NewInt(9)}),
			wantHash: map[string]Value{"x": NewInt(9)},
			wantSame: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := "def run()\n  " + tc.expr + "\n  { ret: ret, hash: h, same: ret.equal?(h) }\nend"
			script := compileScript(t, source)
			result := callFunc(t, script, "run", nil).Hash()
			if diff := valueDiff(tc.wantRet, result["ret"]); diff != "" {
				t.Fatalf("return value mismatch (-want +got):\n%s", diff)
			}
			compareHash(t, result["hash"].Hash(), tc.wantHash)
			if tc.wantSame && !result["same"].Bool() {
				t.Fatal("mutator must return the receiver itself")
			}
		})
	}
}

// TestHashMutatorIndependence is the hash-side inversion of the alias
// visibility this suite used to pin: a second binding and a nested entry are
// each another value, and a write through one is invisible to the others.
func TestHashMutatorIndependence(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def direct_alias()
      a = { x: 1 }
      b = a
      a["y"] = 2
      a.delete("x")
      b
    end

    def nested_alias()
      inner = { n: 1 }
      outer = { a: inner, b: inner }
      outer["a"]["n"] = 9
      outer["b"]
    end

    def addressed_path()
      outer = { a: { n: 1 } }
      outer["a"]["n"] = 9
      outer["a"]
    end
    `)

	direct := callFunc(t, script, "direct_alias", nil)
	if got := direct.Inspect(); got != `{x: 1}` {
		t.Fatalf("aliased binding = %s, want {x: 1}", got)
	}
	nested := callFunc(t, script, "nested_alias", nil)
	if got := nested.Inspect(); got != `{n: 1}` {
		t.Fatalf("sibling entry = %s, want {n: 1}", got)
	}
	addressed := callFunc(t, script, "addressed_path", nil)
	if got := addressed.Inspect(); got != `{n: 9}` {
		t.Fatalf("addressed entry = %s, want {n: 9}", got)
	}
}

// TestHashMutatorsPreserveInsertionOrder pins the Ruby insertion-order
// contract across the in-place mutators: surviving keys keep their recorded
// positions, new keys append, and an overwritten key stays put.
func TestHashMutatorsPreserveInsertionOrder(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def store_order()
      h = { c: 3, a: 1 }
      h["b"] = 2
      h["a"] = 9
      h.keys
    end

    def delete_then_insert_order()
      h = { c: 3, a: 1, b: 2 }
      h.delete(:a)
      h[:d] = 4
      h.keys
    end

    def delete_if_order()
      h = { d: 4, a: 1, c: 3, b: 2 }
      h.delete_if { |k, v| v == 1 }
      h.keys
    end

    def replace_adopts_argument_order()
      h = { a: 1 }
      h.replace({ z: 9, b: 2 })
      h.keys
    end

    def clear_then_fill_order()
      h = { b: 2, a: 1 }
      h.clear
      h[:z] = 1
      h[:y] = 2
      h.keys
    end
    `)

	sym := func(names ...string) []Value {
		out := make([]Value, len(names))
		for i, name := range names {
			out[i] = NewString(name)
		}
		return out
	}

	compareArrays(t, callFunc(t, script, "store_order", nil), sym("c", "a", "b"))
	compareArrays(t, callFunc(t, script, "delete_then_insert_order", nil), sym("c", "b", "d"))
	compareArrays(t, callFunc(t, script, "delete_if_order", nil), sym("d", "c", "b"))
	compareArrays(t, callFunc(t, script, "replace_adopts_argument_order", nil), sym("z", "b"))
	compareArrays(t, callFunc(t, script, "clear_then_fill_order", nil), sym("z", "y"))
}

// TestHashMutationDuringIteration pins the snapshot convention for hashes:
// each walks the entries captured at entry, so deleting or inserting inside
// the block never skews the in-flight iteration.
func TestHashMutationDuringIteration(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def delete_during_each()
      h = { a: 1, b: 2, c: 3 }
      seen = []
      h.each do |k, v|
        seen = seen.push(k)
        h.delete(:c) if k == "a"
      end
      { seen: seen, keys: h.keys }
    end

    def insert_during_each()
      h = { a: 1 }
      count = 0
      h.each do |k, v|
        h[:extra] = 9
        count += 1
      end
      { count: count, size: h.size }
    end
    `)

	deleted := callFunc(t, script, "delete_during_each", nil).Hash()
	compareArrays(t, deleted["seen"], []Value{NewString("a"), NewString("b"), NewString("c")})
	compareArrays(t, deleted["keys"], []Value{NewString("a"), NewString("b")})

	inserted := callFunc(t, script, "insert_during_each", nil).Hash()
	if got := inserted["count"]; !got.Equal(NewInt(1)) {
		t.Fatalf("iteration count = %v, want 1 (iteration is fixed at entry)", got)
	}
	if got := inserted["size"]; !got.Equal(NewInt(2)) {
		t.Fatalf("size after insert-during-each = %v, want 2", got)
	}
}

// TestHashMutatorHostIsolation pins the host boundary for hashes: arguments
// are deep-cloned per call, so in-place hash mutators never leak back into the
// host's original map.
func TestHashMutatorHostIsolation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def mutate_arg(h)
      h["added"] = true
      h.delete("a")
      h.size
    end
    `)

	original := map[string]Value{"a": NewInt(1), "b": NewInt(2)}
	got := callFunc(t, script, "mutate_arg", []Value{NewHash(original)})
	if !got.Equal(NewInt(2)) {
		t.Fatalf("script-side size = %v, want 2", got)
	}
	if len(original) != 2 {
		t.Fatalf("host map has %d entries after the call, want 2 (no leak)", len(original))
	}
	if _, leaked := original["added"]; leaked {
		t.Fatal("script-side store leaked into the host's original map")
	}
}

// TestHashGrowthTripsMemoryQuota pins that in-place hash growth charges the
// quota before the receiver grows: an unbounded fill loop is rejected with a
// limit error, while a generous quota admits the same program.
func TestHashGrowthTripsMemoryQuota(t *testing.T) {
	t.Parallel()
	source := `def run()
  h = {}
  for i in 1..200
    h["key#{i}"] = "abcdefghij"
  end
  h.size
end`

	script := compileScriptWithConfig(t, Config{StepQuota: 100000, MemoryQuotaBytes: 4096}, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})

	allowed := compileScriptWithConfig(t, Config{StepQuota: 100000, MemoryQuotaBytes: 1 << 20}, source)
	got := callFunc(t, allowed, "run", nil)
	if !got.Equal(NewInt(200)) {
		t.Fatalf("under a generous quota run() = %v, want 200", got)
	}
}

// TestHashClearKeepsDefaultAndIdentity pins Ruby's Hash#clear contract: the
// receiver keeps its object identity and its default metadata.
func TestHashClearKeepsIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def clear_keeps_identity()
      h = {}
      h[:a] = 1
      cleared = h.clear
      { same: cleared.equal?(h), missing: h[:missing], size: h.size }
    end
    `)

	got := callFunc(t, script, "clear_keeps_identity", nil).Hash()
	if !got["same"].Bool() {
		t.Fatal("clear must return the receiver itself")
	}
	if missing := got["missing"]; !missing.IsNil() {
		t.Fatalf("missing key after clear = %v, want nil", missing)
	}
	if size := got["size"]; !size.Equal(NewInt(0)) {
		t.Fatalf("size after clear = %v, want 0", size)
	}
}
