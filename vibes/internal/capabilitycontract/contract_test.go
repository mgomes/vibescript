package capabilitycontract

import (
	"strings"
	"testing"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// runtimeKind models runtime-only kinds (block, function, builtin, class,
// instance) whose payload types are private to the runtime. The contract
// helpers only inspect Kind, so a nil payload is sufficient.
func runtimeKind(kind value.ValueKind) value.Value {
	return value.NewValue(kind, nil)
}

func moneyValue(t *testing.T) value.Value {
	t.Helper()
	m, err := value.NewMoneyFromCents(1999, "USD")
	if err != nil {
		t.Fatalf("NewMoneyFromCents(1999, USD) err = %v", err)
	}
	return value.NewMoney(m)
}

// cyclicArray builds a self-referential array. NewArray aliases the backing
// slice it is given, so assigning the wrapper back into the slice element
// creates a real cycle that SliceIdentity-based detection can observe.
func cyclicArray() value.Value {
	elems := make([]value.Value, 1)
	arr := value.NewArray(elems)
	elems[0] = arr
	return arr
}

// cyclicHash builds a self-referential hash; NewHash aliases the map.
func cyclicHash() value.Value {
	entries := map[string]value.Value{}
	hash := value.NewHash(entries)
	entries["self"] = hash
	return hash
}

// cyclicObject builds a self-referential object; NewObject aliases the map.
func cyclicObject() value.Value {
	attrs := map[string]value.Value{}
	obj := value.NewObject(attrs)
	attrs["self"] = obj
	return obj
}

// cyclicArrayThroughHash builds an indirect cycle: array -> hash -> array.
func cyclicArrayThroughHash() value.Value {
	elems := make([]value.Value, 1)
	arr := value.NewArray(elems)
	elems[0] = value.NewHash(map[string]value.Value{"back": arr})
	return arr
}

func deepCapabilityArray(depth int) value.Value {
	val := value.NewString("leaf")
	for range depth {
		val = value.NewArray([]value.Value{val})
	}
	return val
}

// cyclicHashThroughDefault builds a genuine cycle that runs through a hash's
// Ruby-style default value rather than its entries: the default value is an array
// that holds the hash itself, so the walk re-enters the wrapper it is already
// visiting. Cycle detection keys on the hash wrapper, so the back-reference is
// observed as a cycle the same as an entry-level one. NewArray aliases its
// backing slice, so writing the wrapper back into the slice closes the cycle.
func cyclicHashThroughDefault() value.Value {
	defaultElems := make([]value.Value, 1)
	hash := value.NewHashWithDefault(
		map[string]value.Value{},
		value.NewArray(defaultElems),
		value.NewNil(),
	)
	defaultElems[0] = hash
	return hash
}

// sharedEntryMapCallableDefaultBehindPlain builds two hash wrappers over one
// entry map: a plain data-only wrapper followed by one whose default proc is a
// callable. The plain wrapper is visited first, so a scanner that keys its
// seen-set on the entry-map pointer alone would mark the map seen and skip the
// callable-carrying wrapper. Keying on the whole hash wrapper keeps the callable
// default visible.
func sharedEntryMapCallableDefaultBehindPlain() value.Value {
	sharedEntries := map[string]value.Value{"k": value.NewInt(1)}
	plain := value.NewHashWithDefault(sharedEntries, value.NewInt(0), value.NewNil())
	withCallableDefault := value.NewHashWithDefault(
		sharedEntries,
		value.NewNil(),
		runtimeKind(value.KindBlock),
	)
	return value.NewArray([]value.Value{plain, withCallableDefault})
}

func TestNameArg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		val     value.Value
		want    string
		wantErr string
	}{
		{name: "string", val: value.NewString("users"), want: "users"},
		{name: "symbol", val: value.NewSymbol("users"), want: "users"},
		{name: "string_with_padding_kept_verbatim", val: value.NewString(" users "), want: " users "},
		{
			name:    "empty_string",
			val:     value.NewString(""),
			wantErr: "db.find expects collection as non-empty string or symbol",
		},
		{
			name:    "whitespace_only_string",
			val:     value.NewString(" \t\n"),
			wantErr: "db.find expects collection as non-empty string or symbol",
		},
		{
			name:    "whitespace_only_symbol",
			val:     value.NewSymbol("  "),
			wantErr: "db.find expects collection as non-empty string or symbol",
		},
		{
			name:    "int",
			val:     value.NewInt(7),
			wantErr: "db.find expects collection as string or symbol",
		},
		{
			name:    "nil",
			val:     value.NewNil(),
			wantErr: "db.find expects collection as string or symbol",
		},
		{
			name:    "array",
			val:     value.NewArray([]value.Value{value.NewString("users")}),
			wantErr: "db.find expects collection as string or symbol",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NameArg("db.find", "collection", tc.val)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("NameArg(%v) err = nil, want %q", tc.val, tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("NameArg(%v) err = %q, want %q", tc.val, err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NameArg(%v) err = %v, want nil", tc.val, err)
			}
			if got != tc.want {
				t.Fatalf("NameArg(%v) = %q, want %q", tc.val, got, tc.want)
			}
		})
	}
}

func TestCloneKwargs(t *testing.T) {
	t.Parallel()
	t.Run("nil_returns_nil", func(t *testing.T) {
		if got := CloneKwargs(nil); got != nil {
			t.Fatalf("CloneKwargs(nil) = %v, want nil", got)
		}
	})
	t.Run("empty_returns_nil", func(t *testing.T) {
		if got := CloneKwargs(map[string]value.Value{}); got != nil {
			t.Fatalf("CloneKwargs(empty) = %v, want nil", got)
		}
	})
	t.Run("deep_copy_isolation", func(t *testing.T) {
		original := map[string]value.Value{
			"list": value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)}),
			"opts": value.NewHash(map[string]value.Value{"limit": value.NewInt(10)}),
		}
		clone := CloneKwargs(original)
		if len(clone) != len(original) {
			t.Fatalf("CloneKwargs len = %d, want %d", len(clone), len(original))
		}
		for key, want := range original {
			if !clone[key].Equal(want) {
				t.Fatalf("clone[%q] = %v, want %v", key, clone[key], want)
			}
		}

		clone["list"].Array()[0] = value.NewInt(99)
		clone["opts"].Hash()["limit"] = value.NewInt(0)
		if !original["list"].Array()[0].Equal(value.NewInt(1)) {
			t.Fatalf("mutating clone array leaked into original: %v", original["list"])
		}
		if !original["opts"].Hash()["limit"].Equal(value.NewInt(10)) {
			t.Fatalf("mutating clone hash leaked into original: %v", original["opts"])
		}

		original["list"].Array()[1] = value.NewInt(-1)
		original["opts"].Hash()["extra"] = value.NewBool(true)
		if !clone["list"].Array()[1].Equal(value.NewInt(2)) {
			t.Fatalf("mutating original array leaked into clone: %v", clone["list"])
		}
		if _, ok := clone["opts"].Hash()["extra"]; ok {
			t.Fatalf("mutating original hash leaked into clone: %v", clone["opts"])
		}
	})
}

func TestCloneKwargsDataOnly(t *testing.T) {
	t.Parallel()
	t.Run("valid_kwargs_deep_cloned", func(t *testing.T) {
		original := map[string]value.Value{
			"opts": value.NewHash(map[string]value.Value{
				"items": value.NewArray([]value.Value{value.NewString("a")}),
			}),
		}
		clone, err := CloneKwargsDataOnly("db.find", original)
		if err != nil {
			t.Fatalf("CloneKwargsDataOnly err = %v, want nil", err)
		}
		if !clone["opts"].Equal(original["opts"]) {
			t.Fatalf("clone[opts] = %v, want %v", clone["opts"], original["opts"])
		}

		clone["opts"].Hash()["items"].Array()[0] = value.NewString("changed")
		if !original["opts"].Hash()["items"].Array()[0].Equal(value.NewString("a")) {
			t.Fatalf("mutating clone leaked into original: %v", original["opts"])
		}
	})
	t.Run("callable_rejected", func(t *testing.T) {
		_, err := CloneKwargsDataOnly("db.find", map[string]value.Value{"fn": runtimeKind(value.KindBlock)})
		if err == nil {
			t.Fatal("CloneKwargsDataOnly err = nil, want data-only error")
		}
		if want := "db.find keyword fn must be data-only"; err.Error() != want {
			t.Fatalf("CloneKwargsDataOnly err = %q, want %q", err.Error(), want)
		}
	})
}

func TestCloneHash(t *testing.T) {
	t.Parallel()
	t.Run("nil_returns_empty_non_nil", func(t *testing.T) {
		got := CloneHash(nil)
		if got == nil {
			t.Fatal("CloneHash(nil) = nil, want non-nil empty map")
		}
		if len(got) != 0 {
			t.Fatalf("CloneHash(nil) len = %d, want 0", len(got))
		}
	})
	t.Run("empty_returns_empty_non_nil", func(t *testing.T) {
		got := CloneHash(map[string]value.Value{})
		if got == nil {
			t.Fatal("CloneHash(empty) = nil, want non-nil empty map")
		}
		if len(got) != 0 {
			t.Fatalf("CloneHash(empty) len = %d, want 0", len(got))
		}
	})
	t.Run("deep_copy_isolation", func(t *testing.T) {
		original := map[string]value.Value{
			"nested": value.NewHash(map[string]value.Value{
				"items": value.NewArray([]value.Value{value.NewString("a")}),
			}),
		}
		clone := CloneHash(original)
		if !clone["nested"].Equal(original["nested"]) {
			t.Fatalf("clone[nested] = %v, want %v", clone["nested"], original["nested"])
		}

		clone["nested"].Hash()["items"].Array()[0] = value.NewString("changed")
		if !original["nested"].Hash()["items"].Array()[0].Equal(value.NewString("a")) {
			t.Fatalf("mutating clone leaked into original: %v", original["nested"])
		}

		original["nested"].Hash()["added"] = value.NewInt(1)
		if _, ok := clone["nested"].Hash()["added"]; ok {
			t.Fatalf("mutating original leaked into clone: %v", clone["nested"])
		}
	})
}

func TestCloneHashValue(t *testing.T) {
	t.Parallel()
	t.Run("object_value_accepted_and_cloned", func(t *testing.T) {
		original := value.NewObject(map[string]value.Value{
			"items": value.NewArray([]value.Value{value.NewInt(1)}),
		})
		clone, err := CloneHashValue("payload", original)
		if err != nil {
			t.Fatalf("CloneHashValue err = %v, want nil", err)
		}
		if !clone["items"].Equal(original.Hash()["items"]) {
			t.Fatalf("clone[items] = %v, want %v", clone["items"], original.Hash()["items"])
		}

		clone["items"].Array()[0] = value.NewInt(99)
		if !original.Hash()["items"].Array()[0].Equal(value.NewInt(1)) {
			t.Fatalf("mutating clone leaked into original: %v", original)
		}
	})
	t.Run("data_only_checked_before_kind", func(t *testing.T) {
		_, err := CloneHashValue("payload", runtimeKind(value.KindBlock))
		if err == nil {
			t.Fatal("CloneHashValue err = nil, want data-only error")
		}
		if want := "payload must be data-only"; err.Error() != want {
			t.Fatalf("CloneHashValue err = %q, want %q", err.Error(), want)
		}
	})
	t.Run("non_hash_rejected", func(t *testing.T) {
		_, err := CloneHashValue("payload", value.NewInt(1))
		if err == nil {
			t.Fatal("CloneHashValue err = nil, want kind error")
		}
		if want := "payload expected hash, got int"; err.Error() != want {
			t.Fatalf("CloneHashValue err = %q, want %q", err.Error(), want)
		}
	})
}

func TestCloneDataOnlyValue(t *testing.T) {
	t.Parallel()
	t.Run("callable_priority_over_cycle", func(t *testing.T) {
		val := value.NewArray([]value.Value{cyclicArray(), runtimeKind(value.KindBlock)})
		_, err := CloneDataOnlyValue("payload", val)
		if err == nil {
			t.Fatal("CloneDataOnlyValue err = nil, want data-only error")
		}
		if want := "payload must be data-only"; err.Error() != want {
			t.Fatalf("CloneDataOnlyValue err = %q, want %q", err.Error(), want)
		}
	})

	t.Run("default_proc_rejected", func(t *testing.T) {
		val := value.NewHashWithDefault(
			map[string]value.Value{"k": value.NewInt(1)},
			value.NewNil(),
			runtimeKind(value.KindBlock),
		)
		_, err := CloneDataOnlyValue("payload", val)
		if err == nil {
			t.Fatal("CloneDataOnlyValue err = nil, want data-only error for default proc")
		}
		if want := "payload must be data-only"; err.Error() != want {
			t.Fatalf("CloneDataOnlyValue err = %q, want %q", err.Error(), want)
		}
	})

	t.Run("callable_in_default_value_rejected", func(t *testing.T) {
		val := value.NewHashWithDefault(
			map[string]value.Value{},
			value.NewArray([]value.Value{runtimeKind(value.KindFunction)}),
			value.NewNil(),
		)
		if _, err := CloneDataOnlyValue("payload", val); err == nil {
			t.Fatal("CloneDataOnlyValue err = nil, want data-only error for callable default value")
		}
	})

	t.Run("data_only_default_value_preserved_and_isolated", func(t *testing.T) {
		original := value.NewHashWithDefault(
			map[string]value.Value{"k": value.NewInt(1)},
			value.NewArray([]value.Value{value.NewString("dv")}),
			value.NewNil(),
		)
		cloned, err := CloneDataOnlyValue("payload", original)
		if err != nil {
			t.Fatalf("CloneDataOnlyValue err = %v, want nil", err)
		}
		clonedDefault := value.HashDefaultValue(cloned)
		if clonedDefault.Kind() != value.KindArray || len(clonedDefault.Array()) != 1 {
			t.Fatalf("cloned default = %v, want a one-element array", clonedDefault.Kind())
		}
		if !clonedDefault.Array()[0].Equal(value.NewString("dv")) {
			t.Fatalf("cloned default[0] = %v, want \"dv\"", clonedDefault.Array()[0])
		}
		// The clone must be isolated: mutating its default must not leak back.
		clonedDefault.Array()[0] = value.NewString("changed")
		if !value.HashDefaultValue(original).Array()[0].Equal(value.NewString("dv")) {
			t.Fatalf("mutating cloned default leaked into original: %v", value.HashDefaultValue(original))
		}
	})

	t.Run("deep_traversal_limit", func(t *testing.T) {
		_, err := CloneDataOnlyValue("payload", deepCapabilityArray(MaxDataOnlyTraversalDepth+1))
		requireLimitError(t, err)
		if !strings.Contains(err.Error(), "payload exceeds maximum depth") {
			t.Fatalf("CloneDataOnlyValue deep traversal err = %v, want maximum depth error", err)
		}
	})
}

func TestCloneMethodResultRejectsDeepTraversal(t *testing.T) {
	t.Parallel()

	_, err := CloneMethodResult("events.publish", deepCapabilityArray(MaxDataOnlyTraversalDepth+1))
	requireLimitError(t, err)
	if !strings.Contains(err.Error(), "events.publish return value exceeds maximum depth") {
		t.Fatalf("CloneMethodResult deep traversal err = %v, want return-value depth error", err)
	}
}

func TestValidateDataOnlyValueRejectsDeepTraversal(t *testing.T) {
	t.Parallel()

	err := ValidateDataOnlyValue("payload", deepCapabilityArray(MaxDataOnlyTraversalDepth+1))
	requireLimitError(t, err)
}

func requireLimitError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want limit error")
	}
	limit, ok := err.(interface{ LimitError() bool })
	if !ok || !limit.LimitError() {
		t.Fatalf("err = %T %v, want LimitError marker", err, err)
	}
}

func TestDeepCloneValue(t *testing.T) {
	t.Parallel()
	t.Run("scalars_returned_unchanged", func(t *testing.T) {
		tests := []struct {
			name string
			val  value.Value
		}{
			{name: "nil", val: value.NewNil()},
			{name: "bool", val: value.NewBool(true)},
			{name: "int", val: value.NewInt(42)},
			{name: "float", val: value.NewFloat(3.5)},
			{name: "string", val: value.NewString("hello")},
			{name: "symbol", val: value.NewSymbol("ok")},
			{name: "money", val: moneyValue(t)},
			{name: "duration", val: value.NewDuration(value.DurationFromSeconds(90))},
			{name: "time", val: value.NewTime(time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC))},
			{name: "range", val: value.NewRange(value.Range{Start: 1, End: 5})},
			{name: "exclusive_range", val: value.NewRange(value.Range{Start: 1, End: 5, Exclusive: true})},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				if got := DeepCloneValue(tc.val); got != tc.val {
					t.Fatalf("DeepCloneValue(%v) = %v, want identical value", tc.val, got)
				}
			})
		}
	})
	t.Run("array_isolation", func(t *testing.T) {
		original := value.NewArray([]value.Value{
			value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)}),
			value.NewString("keep"),
		})
		clone := DeepCloneValue(original)
		if !clone.Equal(original) {
			t.Fatalf("clone = %v, want %v", clone, original)
		}

		clone.Array()[0].Array()[0] = value.NewInt(99)
		if !original.Array()[0].Array()[0].Equal(value.NewInt(1)) {
			t.Fatalf("mutating clone leaked into original: %v", original)
		}

		original.Array()[1] = value.NewString("changed")
		if !clone.Array()[1].Equal(value.NewString("keep")) {
			t.Fatalf("mutating original leaked into clone: %v", clone)
		}
	})
	t.Run("hash_isolation", func(t *testing.T) {
		original := value.NewHash(map[string]value.Value{
			"inner": value.NewHash(map[string]value.Value{"k": value.NewString("v")}),
		})
		clone := DeepCloneValue(original)
		if !clone.Equal(original) {
			t.Fatalf("clone = %v, want %v", clone, original)
		}

		clone.Hash()["inner"].Hash()["k"] = value.NewString("changed")
		if !original.Hash()["inner"].Hash()["k"].Equal(value.NewString("v")) {
			t.Fatalf("mutating clone leaked into original: %v", original)
		}

		original.Hash()["inner"].Hash()["added"] = value.NewInt(1)
		if _, ok := clone.Hash()["inner"].Hash()["added"]; ok {
			t.Fatalf("mutating original leaked into clone: %v", clone)
		}
	})
	t.Run("object_isolation_preserves_kind", func(t *testing.T) {
		original := value.NewObject(map[string]value.Value{
			"tags": value.NewArray([]value.Value{value.NewString("a")}),
		})
		clone := DeepCloneValue(original)
		if clone.Kind() != value.KindObject {
			t.Fatalf("clone kind = %v, want KindObject", clone.Kind())
		}
		if !clone.Equal(original) {
			t.Fatalf("clone = %v, want %v", clone, original)
		}

		clone.Hash()["tags"].Array()[0] = value.NewString("changed")
		if !original.Hash()["tags"].Array()[0].Equal(value.NewString("a")) {
			t.Fatalf("mutating clone leaked into original: %v", original)
		}
	})
	t.Run("deep_nesting_isolation", func(t *testing.T) {
		original := value.NewHash(map[string]value.Value{
			"rows": value.NewArray([]value.Value{
				value.NewObject(map[string]value.Value{"name": value.NewString("ada")}),
			}),
		})
		clone := DeepCloneValue(original)
		if !clone.Equal(original) {
			t.Fatalf("clone = %v, want %v", clone, original)
		}

		clone.Hash()["rows"].Array()[0].Hash()["name"] = value.NewString("changed")
		if !original.Hash()["rows"].Array()[0].Hash()["name"].Equal(value.NewString("ada")) {
			t.Fatalf("mutating clone at depth 3 leaked into original: %v", original)
		}
	})
	t.Run("hash_default_value_preserved_and_isolated", func(t *testing.T) {
		original := value.NewHashWithDefault(
			map[string]value.Value{"k": value.NewInt(1)},
			value.NewArray([]value.Value{value.NewString("dv")}),
			value.NewNil(),
		)
		clone := DeepCloneValue(original)
		clonedDefault := value.HashDefaultValue(clone)
		if clonedDefault.Kind() != value.KindArray || len(clonedDefault.Array()) != 1 {
			t.Fatalf("cloned default = %v, want a one-element array", clonedDefault.Kind())
		}
		clonedDefault.Array()[0] = value.NewString("changed")
		if !value.HashDefaultValue(original).Array()[0].Equal(value.NewString("dv")) {
			t.Fatalf("mutating cloned default leaked into original: %v", value.HashDefaultValue(original))
		}
	})
	t.Run("hash_default_proc_preserved", func(t *testing.T) {
		proc := runtimeKind(value.KindBlock)
		original := value.NewHashWithDefault(map[string]value.Value{}, value.NewNil(), proc)
		clone := DeepCloneValue(original)
		if got := value.HashDefaultProc(clone); got.Kind() != value.KindBlock {
			t.Fatalf("cloned default proc kind = %v, want KindBlock (preserved by reference)", got.Kind())
		}
	})
}

func TestIsNilImplementation(t *testing.T) {
	t.Parallel()
	var (
		nilPtr   *int
		nilMap   map[string]int
		nilSlice []int
		nilFunc  func()
		nilChan  chan int
	)
	n := 7
	tests := []struct {
		name string
		impl any
		want bool
	}{
		{name: "nil_interface", impl: nil, want: true},
		{name: "typed_nil_pointer", impl: nilPtr, want: true},
		{name: "typed_nil_map", impl: nilMap, want: true},
		{name: "typed_nil_slice", impl: nilSlice, want: true},
		{name: "typed_nil_func", impl: nilFunc, want: true},
		{name: "typed_nil_chan", impl: nilChan, want: true},
		{name: "non_nil_pointer", impl: &n, want: false},
		{name: "non_nil_map", impl: map[string]int{}, want: false},
		{name: "non_nil_slice", impl: []int{}, want: false},
		{name: "non_nil_func", impl: func() {}, want: false},
		{name: "non_nil_chan", impl: make(chan int), want: false},
		{name: "int", impl: 0, want: false},
		{name: "string", impl: "", want: false},
		{name: "bool", impl: false, want: false},
		{name: "struct", impl: struct{}{}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNilImplementation(tc.impl); got != tc.want {
				t.Fatalf("IsNilImplementation(%#v) = %v, want %v", tc.impl, got, tc.want)
			}
		})
	}
}

func TestEnsureBlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		block   value.Value
		method  string
		wantErr string
	}{
		{name: "block_accepted", block: runtimeKind(value.KindBlock), method: "each"},
		{name: "non_block_with_name", block: value.NewString("x"), method: "each", wantErr: "each requires a block"},
		{name: "non_block_without_name", block: value.NewNil(), method: "", wantErr: "block required"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureBlock(tc.block, tc.method)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("EnsureBlock(%v, %q) err = %v, want nil", tc.block, tc.method, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("EnsureBlock(%v, %q) err = nil, want %q", tc.block, tc.method, tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("EnsureBlock(%v, %q) err = %q, want %q", tc.block, tc.method, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateDataOnlyValue(t *testing.T) {
	t.Parallel()
	sharedArray := value.NewArray([]value.Value{value.NewString("leaf")})
	sharedHash := value.NewHash(map[string]value.Value{"k": value.NewInt(1)})
	tests := []struct {
		name    string
		val     value.Value
		wantErr string
	}{
		{name: "nil", val: value.NewNil()},
		{name: "bool", val: value.NewBool(true)},
		{name: "int", val: value.NewInt(1)},
		{name: "float", val: value.NewFloat(1.5)},
		{name: "string", val: value.NewString("ok")},
		{name: "symbol", val: value.NewSymbol("ok")},
		{name: "money", val: moneyValue(t)},
		{name: "duration", val: value.NewDuration(value.DurationFromSeconds(5))},
		{name: "time", val: value.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))},
		{name: "range", val: value.NewRange(value.Range{Start: 0, End: 3})},
		{name: "exclusive_range", val: value.NewRange(value.Range{Start: 0, End: 3, Exclusive: true})},
		{name: "empty_array", val: value.NewArray(nil)},
		{name: "empty_hash", val: value.NewHash(map[string]value.Value{})},
		{
			name: "nested_data",
			val: value.NewArray([]value.Value{
				value.NewHash(map[string]value.Value{"a": value.NewArray([]value.Value{value.NewInt(1)})}),
				value.NewObject(map[string]value.Value{"b": value.NewString("x")}),
			}),
		},
		{
			name: "shared_array_diamond_is_acyclic",
			val:  value.NewArray([]value.Value{sharedArray, sharedArray}),
		},
		{
			name: "shared_hash_diamond_is_acyclic",
			val:  value.NewHash(map[string]value.Value{"a": sharedHash, "b": sharedHash}),
		},
		{
			name:    "function",
			val:     runtimeKind(value.KindFunction),
			wantErr: "payload must be data-only",
		},
		{
			name:    "builtin",
			val:     runtimeKind(value.KindBuiltin),
			wantErr: "payload must be data-only",
		},
		{
			name:    "block",
			val:     runtimeKind(value.KindBlock),
			wantErr: "payload must be data-only",
		},
		{
			name:    "class",
			val:     runtimeKind(value.KindClass),
			wantErr: "payload must be data-only",
		},
		{
			name:    "instance",
			val:     runtimeKind(value.KindInstance),
			wantErr: "payload must be data-only",
		},
		{
			name:    "block_in_array",
			val:     value.NewArray([]value.Value{value.NewInt(1), runtimeKind(value.KindBlock)}),
			wantErr: "payload must be data-only",
		},
		{
			name:    "builtin_in_hash",
			val:     value.NewHash(map[string]value.Value{"cb": runtimeKind(value.KindBuiltin)}),
			wantErr: "payload must be data-only",
		},
		{
			name:    "function_in_object",
			val:     value.NewObject(map[string]value.Value{"fn": runtimeKind(value.KindFunction)}),
			wantErr: "payload must be data-only",
		},
		{
			name: "data_only_default_value_is_ok",
			val: value.NewHashWithDefault(
				map[string]value.Value{"k": value.NewInt(1)},
				value.NewInt(0),
				value.NewNil(),
			),
		},
		{
			name: "default_proc_is_callable",
			val: value.NewHashWithDefault(
				map[string]value.Value{},
				value.NewNil(),
				runtimeKind(value.KindBlock),
			),
			wantErr: "payload must be data-only",
		},
		{
			name: "callable_in_default_value",
			val: value.NewHashWithDefault(
				map[string]value.Value{},
				value.NewArray([]value.Value{runtimeKind(value.KindBuiltin)}),
				value.NewNil(),
			),
			wantErr: "payload must be data-only",
		},
		{
			name:    "cycle_through_default_value",
			val:     cyclicHashThroughDefault(),
			wantErr: "payload must not contain cyclic references",
		},
		{
			name:    "callable_default_behind_shared_entry_map",
			val:     sharedEntryMapCallableDefaultBehindPlain(),
			wantErr: "payload must be data-only",
		},
		{
			name: "class_deeply_nested",
			val: value.NewArray([]value.Value{
				value.NewHash(map[string]value.Value{"inner": runtimeKind(value.KindClass)}),
			}),
			wantErr: "payload must be data-only",
		},
		{
			name:    "cyclic_array",
			val:     cyclicArray(),
			wantErr: "payload must not contain cyclic references",
		},
		{
			name:    "cyclic_hash",
			val:     cyclicHash(),
			wantErr: "payload must not contain cyclic references",
		},
		{
			name:    "cyclic_object",
			val:     cyclicObject(),
			wantErr: "payload must not contain cyclic references",
		},
		{
			name:    "indirect_cycle_array_through_hash",
			val:     cyclicArrayThroughHash(),
			wantErr: "payload must not contain cyclic references",
		},
		{
			name:    "callable_priority_over_cycle",
			val:     value.NewArray([]value.Value{cyclicArray(), runtimeKind(value.KindBlock)}),
			wantErr: "payload must be data-only",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDataOnlyValue("payload", tc.val)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateDataOnlyValue(payload, %s) err = %v, want nil", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateDataOnlyValue(payload, %s) err = nil, want %q", tc.name, tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("ValidateDataOnlyValue(payload, %s) err = %q, want %q", tc.name, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateHashValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		val     value.Value
		wantErr string
	}{
		{name: "hash", val: value.NewHash(map[string]value.Value{"k": value.NewInt(1)})},
		{name: "empty_hash", val: value.NewHash(map[string]value.Value{})},
		{name: "object", val: value.NewObject(map[string]value.Value{"k": value.NewInt(1)})},
		{name: "string", val: value.NewString("x"), wantErr: "payload expected hash, got string"},
		{name: "int", val: value.NewInt(1), wantErr: "payload expected hash, got int"},
		{name: "nil", val: value.NewNil(), wantErr: "payload expected hash, got nil"},
		{name: "symbol", val: value.NewSymbol("x"), wantErr: "payload expected hash, got symbol"},
		{
			name:    "array",
			val:     value.NewArray([]value.Value{value.NewInt(1)}),
			wantErr: "payload expected hash, got array",
		},
		{
			name:    "data_only_checked_before_kind",
			val:     runtimeKind(value.KindBlock),
			wantErr: "payload must be data-only",
		},
		{
			name:    "callable_inside_hash",
			val:     value.NewHash(map[string]value.Value{"fn": runtimeKind(value.KindFunction)}),
			wantErr: "payload must be data-only",
		},
		{
			name:    "cyclic_hash",
			val:     cyclicHash(),
			wantErr: "payload must not contain cyclic references",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHashValue("payload", tc.val)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateHashValue(payload, %s) err = %v, want nil", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateHashValue(payload, %s) err = nil, want %q", tc.name, tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("ValidateHashValue(payload, %s) err = %q, want %q", tc.name, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateKwargsDataOnly(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		kwargs  map[string]value.Value
		wantErr string
	}{
		{name: "nil_kwargs", kwargs: nil},
		{
			name: "valid_kwargs",
			kwargs: map[string]value.Value{
				"limit": value.NewInt(10),
				"opts":  value.NewHash(map[string]value.Value{"sort": value.NewSymbol("asc")}),
			},
		},
		{
			name:    "callable_keyword",
			kwargs:  map[string]value.Value{"fn": runtimeKind(value.KindBlock)},
			wantErr: "db.find keyword fn must be data-only",
		},
		{
			name:    "cyclic_keyword",
			kwargs:  map[string]value.Value{"loop": cyclicArray()},
			wantErr: "db.find keyword loop must not contain cyclic references",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateKwargsDataOnly("db.find", tc.kwargs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateKwargsDataOnly err = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateKwargsDataOnly err = nil, want %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("ValidateKwargsDataOnly err = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateAnyReturn(t *testing.T) {
	t.Parallel()
	validate := ValidateAnyReturn("db.find")
	tests := []struct {
		name    string
		result  value.Value
		wantErr string
	}{
		{name: "nil_result", result: value.NewNil()},
		{
			name:   "data_result",
			result: value.NewHash(map[string]value.Value{"id": value.NewInt(1)}),
		},
		{
			name:    "callable_result",
			result:  runtimeKind(value.KindBuiltin),
			wantErr: "db.find return value must be data-only",
		},
		{
			name:    "cyclic_result",
			result:  cyclicHash(),
			wantErr: "db.find return value must not contain cyclic references",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.result)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAnyReturn validator err = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateAnyReturn validator err = nil, want %q", tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("ValidateAnyReturn validator err = %q, want %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCloneMethodResult(t *testing.T) {
	t.Parallel()
	t.Run("callable_result_rejected", func(t *testing.T) {
		got, err := CloneMethodResult("db.find", runtimeKind(value.KindBlock))
		if err == nil {
			t.Fatal("CloneMethodResult err = nil, want data-only error")
		}
		if want := "db.find return value must be data-only"; err.Error() != want {
			t.Fatalf("CloneMethodResult err = %q, want %q", err.Error(), want)
		}
		if got.Kind() != value.KindNil {
			t.Fatalf("CloneMethodResult value kind = %v, want KindNil", got.Kind())
		}
	})
	t.Run("cyclic_result_rejected", func(t *testing.T) {
		got, err := CloneMethodResult("db.find", cyclicArray())
		if err == nil {
			t.Fatal("CloneMethodResult err = nil, want cyclic error")
		}
		if want := "db.find return value must not contain cyclic references"; err.Error() != want {
			t.Fatalf("CloneMethodResult err = %q, want %q", err.Error(), want)
		}
		if got.Kind() != value.KindNil {
			t.Fatalf("CloneMethodResult value kind = %v, want KindNil", got.Kind())
		}
	})
	t.Run("valid_result_deep_cloned", func(t *testing.T) {
		result := value.NewHash(map[string]value.Value{
			"items": value.NewArray([]value.Value{value.NewInt(1)}),
		})
		got, err := CloneMethodResult("db.find", result)
		if err != nil {
			t.Fatalf("CloneMethodResult err = %v, want nil", err)
		}
		if !got.Equal(result) {
			t.Fatalf("CloneMethodResult = %v, want %v", got, result)
		}

		result.Hash()["items"].Array()[0] = value.NewInt(99)
		if !got.Hash()["items"].Array()[0].Equal(value.NewInt(1)) {
			t.Fatalf("mutating host result leaked into clone: %v", got)
		}

		got.Hash()["added"] = value.NewBool(true)
		if _, ok := result.Hash()["added"]; ok {
			t.Fatalf("mutating clone leaked into host result: %v", result)
		}
	})
}
