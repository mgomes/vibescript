package value_test

import (
	"math"
	"testing"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// Local marker-implementing payloads for runtime-only kinds. The concrete
// runtime types are private; any type satisfying the marker interface is a
// valid payload from the value package's perspective.
type (
	fakeClass     struct{}
	fakeInstance  struct{}
	fakeBlock     struct{}
	fakeFunction  struct{}
	fakeBuiltin   struct{}
	fakeEnum      struct{}
	fakeEnumValue struct{}
)

func (fakeClass) ValueClassMarker()         {}
func (fakeInstance) ValueInstanceMarker()   {}
func (fakeBlock) ValueBlockMarker()         {}
func (fakeFunction) ValueFunctionMarker()   {}
func (fakeBuiltin) ValueBuiltinMarker()     {}
func (fakeEnum) ValueEnumMarker()           {}
func (fakeEnumValue) ValueEnumValueMarker() {}

func mustMoney(t *testing.T, cents int64, currency string) value.Money {
	t.Helper()
	m, err := value.NewMoneyFromCents(cents, currency)
	if err != nil {
		t.Fatalf("NewMoneyFromCents(%d, %q): %v", cents, currency, err)
	}
	return m
}

func TestValueKindString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind value.ValueKind
		want string
	}{
		{value.KindNil, "nil"},
		{value.KindBool, "bool"},
		{value.KindInt, "int"},
		{value.KindFloat, "float"},
		{value.KindString, "string"},
		{value.KindArray, "array"},
		{value.KindHash, "hash"},
		{value.KindFunction, "function"},
		{value.KindBuiltin, "builtin"},
		{value.KindMoney, "money"},
		{value.KindDuration, "duration"},
		{value.KindTime, "time"},
		{value.KindSymbol, "symbol"},
		{value.KindObject, "object"},
		{value.KindRange, "range"},
		{value.KindBlock, "block"},
		{value.KindEnum, "enum"},
		{value.KindEnumValue, "enum value"},
		{value.ValueKind(99), "kind(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.want+"_"+tc.kind.String(), func(t *testing.T) {
			t.Parallel()
			if got := tc.kind.String(); got != tc.want {
				t.Fatalf("ValueKind(%d).String() = %q, want %q", int(tc.kind), got, tc.want)
			}
		})
	}
}

func TestValueConstructorKinds(t *testing.T) {
	t.Parallel()

	money := mustMoney(t, 100, "USD")
	tests := []struct {
		name string
		val  value.Value
		want value.ValueKind
	}{
		{"nil", value.NewNil(), value.KindNil},
		{"bool", value.NewBool(true), value.KindBool},
		{"int", value.NewInt(1), value.KindInt},
		{"float", value.NewFloat(1.5), value.KindFloat},
		{"string", value.NewString("s"), value.KindString},
		{"array", value.NewArray(nil), value.KindArray},
		{"hash", value.NewHash(nil), value.KindHash},
		{"money", value.NewMoney(money), value.KindMoney},
		{"duration", value.NewDuration(value.DurationFromSeconds(1)), value.KindDuration},
		{"time", value.NewTime(time.Unix(0, 0)), value.KindTime},
		{"symbol", value.NewSymbol("ok"), value.KindSymbol},
		{"object", value.NewObject(nil), value.KindObject},
		{"range", value.NewRange(value.Range{Start: 1, End: 3}), value.KindRange},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.val.Kind(); got != tc.want {
				t.Fatalf("Kind() = %s, want %s", got, tc.want)
			}
			if isNil := tc.val.IsNil(); isNil != (tc.want == value.KindNil) {
				t.Fatalf("IsNil() = %t for kind %s", isNil, tc.want)
			}
		})
	}
}

func TestNewValueDataRoundTrip(t *testing.T) {
	t.Parallel()

	payload := fakeBlock{}
	v := value.NewValue(value.KindBlock, payload)
	if v.Kind() != value.KindBlock {
		t.Fatalf("Kind() = %s, want block", v.Kind())
	}
	if got, ok := v.Data().(fakeBlock); !ok || got != payload {
		t.Fatalf("Data() = %v (%T), want original payload", v.Data(), v.Data())
	}
}

func TestScalarValueData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  value.Value
		want any
	}{
		{"bool", value.NewBool(true), true},
		{"int", value.NewInt(-7), int64(-7)},
		{"float", value.NewFloat(2.5), 2.5},
		{"duration", value.NewDuration(value.DurationFromSeconds(90)), value.DurationFromSeconds(90)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.val.Data(); got != tc.want {
				t.Fatalf("Data() = %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
			rebuilt := value.NewValue(tc.val.Kind(), tc.want)
			if got := rebuilt.Data(); got != tc.want {
				t.Fatalf("NewValue(%s, payload).Data() = %v (%T), want %v (%T)", tc.val.Kind(), got, got, tc.want, tc.want)
			}
			if !rebuilt.Equal(tc.val) {
				t.Fatalf("NewValue(%s, payload) = %s, want value equal to %s", tc.val.Kind(), rebuilt, tc.val)
			}
		})
	}
}

func TestValueScalarAccessors(t *testing.T) {
	t.Parallel()

	t.Run("bool", func(t *testing.T) {
		t.Parallel()
		if !value.NewBool(true).Bool() {
			t.Error("NewBool(true).Bool() = false")
		}
		if value.NewInt(1).Bool() {
			t.Error("Int value Bool() = true, want false for wrong kind")
		}
	})

	t.Run("int", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			val  value.Value
			want int64
		}{
			{"int", value.NewInt(-7), -7},
			{"float_truncates", value.NewFloat(3.9), 3},
			{"negative_float_truncates", value.NewFloat(-3.9), -3},
			{"string_is_zero", value.NewString("42"), 0},
			{"nil_is_zero", value.NewNil(), 0},
		}
		for _, tc := range tests {
			if got := tc.val.Int(); got != tc.want {
				t.Errorf("%s: Int() = %d, want %d", tc.name, got, tc.want)
			}
		}
	})

	t.Run("float", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			val  value.Value
			want float64
		}{
			{"float", value.NewFloat(2.5), 2.5},
			{"int_coerces", value.NewInt(4), 4},
			{"bool_is_zero", value.NewBool(true), 0},
		}
		for _, tc := range tests {
			if got := tc.val.Float(); got != tc.want {
				t.Errorf("%s: Float() = %g, want %g", tc.name, got, tc.want)
			}
		}
	})
}

func TestValueCompositeAccessors(t *testing.T) {
	t.Parallel()

	t.Run("array", func(t *testing.T) {
		t.Parallel()
		elems := []value.Value{value.NewInt(1)}
		if got := value.NewArray(elems).Array(); len(got) != 1 || got[0].Int() != 1 {
			t.Errorf("Array() = %v, want one-element slice", got)
		}
		if got := value.NewString("nope").Array(); got != nil {
			t.Errorf("string Array() = %v, want nil", got)
		}
	})

	t.Run("hash_and_object", func(t *testing.T) {
		t.Parallel()
		entries := map[string]value.Value{"k": value.NewInt(1)}
		if got := value.NewHash(entries).Hash(); got["k"].Int() != 1 {
			t.Errorf("hash Hash() = %v, want entries", got)
		}
		if got := value.NewObject(entries).Hash(); got["k"].Int() != 1 {
			t.Errorf("object Hash() = %v, want entries", got)
		}
		if got := value.NewArray(nil).Hash(); got != nil {
			t.Errorf("array Hash() = %v, want nil", got)
		}
	})

	t.Run("domain_scalars_wrong_kind_zero", func(t *testing.T) {
		t.Parallel()
		v := value.NewString("nope")
		if got := v.Money(); got != (value.Money{}) {
			t.Errorf("Money() = %v, want zero", got)
		}
		if got := v.Duration(); got != (value.Duration{}) {
			t.Errorf("Duration() = %v, want zero", got)
		}
		if got := v.Time(); !got.IsZero() {
			t.Errorf("Time() = %v, want zero", got)
		}
		if got := v.Range(); got != (value.Range{}) {
			t.Errorf("Range() = %v, want zero", got)
		}
	})

	t.Run("domain_scalars_round_trip", func(t *testing.T) {
		t.Parallel()
		money := mustMoney(t, 1999, "USD")
		if got := value.NewMoney(money).Money(); got != money {
			t.Errorf("Money() = %v, want %v", got, money)
		}
		dur := value.DurationFromSeconds(90)
		if got := value.NewDuration(dur).Duration(); got != dur {
			t.Errorf("Duration() = %v, want %v", got, dur)
		}
		ts := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
		if got := value.NewTime(ts).Time(); !got.Equal(ts) {
			t.Errorf("Time() = %v, want %v", got, ts)
		}
		rng := value.Range{Start: -2, End: 5}
		if got := value.NewRange(rng).Range(); got != rng {
			t.Errorf("Range() = %v, want %v", got, rng)
		}
	})
}

func TestValuePayloadAccessors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    value.ValueKind
		payload any
		get     func(value.Value) any
	}{
		{"class", value.KindClass, fakeClass{}, func(v value.Value) any { return v.Class() }},
		{"instance", value.KindInstance, fakeInstance{}, func(v value.Value) any { return v.Instance() }},
		{"block", value.KindBlock, fakeBlock{}, func(v value.Value) any { return v.Block() }},
		{"function", value.KindFunction, fakeFunction{}, func(v value.Value) any { return v.Function() }},
		{"builtin", value.KindBuiltin, fakeBuiltin{}, func(v value.Value) any { return v.Builtin() }},
		{"enum", value.KindEnum, fakeEnum{}, func(v value.Value) any { return v.Enum() }},
		{"enum_value", value.KindEnumValue, fakeEnumValue{}, func(v value.Value) any { return v.EnumValue() }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matching := value.NewValue(tc.kind, tc.payload)
			if got := tc.get(matching); got != tc.payload {
				t.Errorf("accessor on matching kind = %v, want payload", got)
			}
			if got := tc.get(value.NewString("nope")); got != nil {
				t.Errorf("accessor on wrong kind = %v, want nil", got)
			}
			foreign := value.NewValue(tc.kind, "not a payload")
			if got := tc.get(foreign); got != nil {
				t.Errorf("accessor on non-marker payload = %v, want nil", got)
			}
		})
	}
}

func TestValueTruthy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  value.Value
		want bool
	}{
		{"nil", value.NewNil(), false},
		{"false", value.NewBool(false), false},
		{"true", value.NewBool(true), true},
		{"zero_int", value.NewInt(0), false},
		{"negative_int", value.NewInt(-1), true},
		{"zero_float", value.NewFloat(0), false},
		{"nonzero_float", value.NewFloat(0.5), true},
		{"empty_string", value.NewString(""), false},
		{"nonempty_string", value.NewString("x"), true},
		{"empty_array", value.NewArray(nil), false},
		{"nonempty_array", value.NewArray([]value.Value{value.NewNil()}), true},
		{"empty_hash", value.NewHash(nil), false},
		{"nonempty_hash", value.NewHash(map[string]value.Value{"k": value.NewNil()}), true},
		{"zero_money", value.NewMoney(value.Money{}), true},
		{"symbol", value.NewSymbol("ok"), true},
		{"range", value.NewRange(value.Range{}), true},
		{"enum_payload", value.NewValue(value.KindEnum, fakeEnum{}), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.val.Truthy(); got != tc.want {
				t.Fatalf("Truthy() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestValueString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  value.Value
		want string
	}{
		{"nil_is_empty", value.NewNil(), ""},
		{"bool_true", value.NewBool(true), "true"},
		{"bool_false", value.NewBool(false), "false"},
		{"int", value.NewInt(-42), "-42"},
		{"float", value.NewFloat(2.5), "2.5"},
		{"float_compact_exponent", value.NewFloat(1e21), "1e+21"},
		{"float_positive_infinity", value.NewFloat(math.Inf(1)), "Infinity"},
		{"float_negative_infinity", value.NewFloat(math.Inf(-1)), "-Infinity"},
		{"float_nan", value.NewFloat(math.NaN()), "NaN"},
		{"string", value.NewString("hello"), "hello"},
		{"symbol_renders_bare", value.NewSymbol("status"), "status"},
		{"money", value.NewMoney(mustMoney(t, 1999, "usd")), "19.99 USD"},
		{"duration", value.NewDuration(value.DurationFromSeconds(90)), "90s"},
		{
			"time_rfc3339nano",
			value.NewTime(time.Date(2024, 6, 1, 12, 30, 0, 500_000_000, time.UTC)),
			"2024-06-01T12:30:00.5Z",
		},
		{"range", value.NewRange(value.Range{Start: 1, End: 5}), "1..5"},
		{"exclusive_range", value.NewRange(value.Range{Start: 1, End: 5, Exclusive: true}), "1...5"},
		{"negative_range", value.NewRange(value.Range{Start: -3, End: -1}), "-3..-1"},
		{"empty_array", value.NewArray(nil), "[]"},
		{
			"nested_array",
			value.NewArray([]value.Value{
				value.NewInt(1),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewNil(),
			}),
			"[1, [two], ]",
		},
		{"empty_hash", value.NewHash(nil), "{}"},
		{
			"single_entry_hash",
			value.NewHash(map[string]value.Value{"name": value.NewString("acme")}),
			"{name: acme}",
		},
		{"runtime_kind_fallback", value.NewValue(value.KindBlock, fakeBlock{}), "<block>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.val.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   float64
		want string
	}{
		{"finite", 2.5, "2.5"},
		{"negative_finite", -0.125, "-0.125"},
		{"zero", 0, "0"},
		{"large_exponent", 1e21, "1e+21"},
		{"positive_infinity", math.Inf(1), "Infinity"},
		{"negative_infinity", math.Inf(-1), "-Infinity"},
		{"nan", math.NaN(), "NaN"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := value.FormatFloat(tc.in); got != tc.want {
				t.Fatalf("FormatFloat(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestValueStringCycleDetection(t *testing.T) {
	t.Parallel()

	t.Run("self_referential_array", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 1)
		arr := value.NewArray(elems)
		elems[0] = arr
		if got := arr.String(); got != "[<cycle>]" {
			t.Fatalf("String() = %q, want %q", got, "[<cycle>]")
		}
	})

	t.Run("self_referential_hash", func(t *testing.T) {
		t.Parallel()
		entries := make(map[string]value.Value)
		hash := value.NewHash(entries)
		entries["self"] = hash
		if got := hash.String(); got != "{self: <cycle>}" {
			t.Fatalf("String() = %q, want %q", got, "{self: <cycle>}")
		}
	})

	t.Run("shared_subtree_is_not_a_cycle", func(t *testing.T) {
		t.Parallel()
		shared := value.NewArray([]value.Value{value.NewInt(1)})
		outer := value.NewArray([]value.Value{shared, shared})
		if got := outer.String(); got != "[[1], [1]]" {
			t.Fatalf("String() = %q, want %q", got, "[[1], [1]]")
		}
	})
}

func TestValueEqual(t *testing.T) {
	t.Parallel()

	usd100 := mustMoney(t, 100, "USD")
	eur100 := mustMoney(t, 100, "EUR")
	instant := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	sharedMap := map[string]value.Value{"k": value.NewInt(1)}

	tests := []struct {
		name  string
		left  value.Value
		right value.Value
		want  bool
	}{
		{"nils", value.NewNil(), value.NewNil(), true},
		{"bools", value.NewBool(true), value.NewBool(true), true},
		{"bool_mismatch", value.NewBool(true), value.NewBool(false), false},
		{"ints", value.NewInt(7), value.NewInt(7), true},
		{"int_vs_float_kind_mismatch", value.NewInt(1), value.NewFloat(1), false},
		{"floats", value.NewFloat(2.5), value.NewFloat(2.5), true},
		{"nan_not_equal", value.NewFloat(math.NaN()), value.NewFloat(math.NaN()), false},
		{"strings", value.NewString("a"), value.NewString("a"), true},
		{"string_vs_symbol_kind_mismatch", value.NewString("a"), value.NewSymbol("a"), false},
		{"symbols", value.NewSymbol("a"), value.NewSymbol("a"), true},
		{"money", value.NewMoney(usd100), value.NewMoney(usd100), true},
		{"money_currency_mismatch", value.NewMoney(usd100), value.NewMoney(eur100), false},
		{
			"durations",
			value.NewDuration(value.DurationFromSeconds(60)),
			value.NewDuration(value.DurationFromSeconds(60)),
			true,
		},
		{
			"time_same_instant_different_zone",
			value.NewTime(instant),
			value.NewTime(instant.In(time.FixedZone("plus2", 2*3600))),
			true,
		},
		{
			"ranges",
			value.NewRange(value.Range{Start: 1, End: 3}),
			value.NewRange(value.Range{Start: 1, End: 3}),
			true,
		},
		{
			"range_mismatch",
			value.NewRange(value.Range{Start: 1, End: 3}),
			value.NewRange(value.Range{Start: 1, End: 4}),
			false,
		},
		{
			"exclusive_ranges",
			value.NewRange(value.Range{Start: 1, End: 3, Exclusive: true}),
			value.NewRange(value.Range{Start: 1, End: 3, Exclusive: true}),
			true,
		},
		{
			"range_exclusivity_mismatch",
			value.NewRange(value.Range{Start: 1, End: 3}),
			value.NewRange(value.Range{Start: 1, End: 3, Exclusive: true}),
			false,
		},
		{
			"arrays_deep",
			value.NewArray([]value.Value{value.NewInt(1), value.NewArray([]value.Value{value.NewString("x")})}),
			value.NewArray([]value.Value{value.NewInt(1), value.NewArray([]value.Value{value.NewString("x")})}),
			true,
		},
		{
			"array_length_mismatch",
			value.NewArray([]value.Value{value.NewInt(1)}),
			value.NewArray(nil),
			false,
		},
		{
			"array_element_mismatch",
			value.NewArray([]value.Value{value.NewInt(1)}),
			value.NewArray([]value.Value{value.NewInt(2)}),
			false,
		},
		{
			"hashes_deep",
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
			true,
		},
		{
			"hash_key_mismatch",
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
			value.NewHash(map[string]value.Value{"b": value.NewInt(1)}),
			false,
		},
		{
			"hash_value_mismatch",
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
			value.NewHash(map[string]value.Value{"a": value.NewInt(2)}),
			false,
		},
		{
			"hash_length_mismatch",
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
			value.NewHash(map[string]value.Value{"a": value.NewInt(1), "b": value.NewInt(2)}),
			false,
		},
		{
			"hash_shared_backing_map",
			value.NewHash(sharedMap),
			value.NewHash(sharedMap),
			true,
		},
		{
			"hash_vs_object_kind_mismatch",
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
			value.NewObject(map[string]value.Value{"a": value.NewInt(1)}),
			false,
		},
		{
			"objects_deep",
			value.NewObject(map[string]value.Value{"a": value.NewInt(1)}),
			value.NewObject(map[string]value.Value{"a": value.NewInt(1)}),
			true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.left.Equal(tc.right); got != tc.want {
				t.Fatalf("Equal(%s, %s) = %t, want %t", tc.left, tc.right, got, tc.want)
			}
			if got := tc.right.Equal(tc.left); got != tc.want {
				t.Fatalf("Equal is not symmetric for %s and %s", tc.left, tc.right)
			}
		})
	}
}

func TestValueEqualCyclicStructuresTerminate(t *testing.T) {
	t.Parallel()

	makeCyclicArray := func() value.Value {
		elems := make([]value.Value, 1)
		arr := value.NewArray(elems)
		elems[0] = arr
		return arr
	}

	left := makeCyclicArray()
	right := makeCyclicArray()
	if !left.Equal(right) {
		t.Error("structurally identical cyclic arrays compare unequal")
	}
	if !left.Equal(left) {
		t.Error("cyclic array does not equal itself")
	}

	makeCyclicHash := func() value.Value {
		entries := make(map[string]value.Value)
		hash := value.NewHash(entries)
		entries["self"] = hash
		return hash
	}

	leftHash := makeCyclicHash()
	rightHash := makeCyclicHash()
	if !leftHash.Equal(rightHash) {
		t.Error("structurally identical cyclic hashes compare unequal")
	}
}

// TestRuntimeHooks intentionally runs without t.Parallel: it mutates the
// package-level RuntimeStringer/RuntimeEqualer hooks, and Go defers all
// parallel tests until sequential tests like this one have finished.
func TestRuntimeHooks(t *testing.T) {
	t.Cleanup(func() {
		value.RuntimeStringer = nil
		value.RuntimeEqualer = nil
	})

	value.RuntimeStringer = func(v value.Value) (string, bool) {
		if v.Kind() == value.KindEnum {
			return "<Enum Color>", true
		}
		return "", false
	}
	value.RuntimeEqualer = func(left, right value.Value) (bool, bool) {
		if left.Kind() == value.KindEnum {
			return true, true
		}
		return false, false
	}

	enum := value.NewValue(value.KindEnum, fakeEnum{})
	if got := enum.String(); got != "<Enum Color>" {
		t.Errorf("hooked enum String() = %q, want %q", got, "<Enum Color>")
	}
	if !enum.Equal(value.NewValue(value.KindEnum, fakeEnum{})) {
		t.Error("hooked enum Equal() = false, want true")
	}

	// A hook that declines (ok=false) falls back to the generic rendering
	// and reflect.DeepEqual comparison.
	block := value.NewValue(value.KindBlock, fakeBlock{})
	if got := block.String(); got != "<block>" {
		t.Errorf("declined-hook block String() = %q, want %q", got, "<block>")
	}
	if !block.Equal(value.NewValue(value.KindBlock, fakeBlock{})) {
		t.Error("declined-hook block Equal() = false, want DeepEqual fallback true")
	}
	if block.Equal(value.NewValue(value.KindBlock, fakeFunction{})) {
		t.Error("declined-hook block Equal() across payload types = true, want false")
	}
}

func TestValueToInt64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		val     value.Value
		want    int64
		wantErr string
	}{
		{name: "int", val: value.NewInt(42), want: 42},
		{name: "float_truncates", val: value.NewFloat(3.9), want: 3},
		{name: "negative_float_truncates", val: value.NewFloat(-3.9), want: -3},
		{name: "string", val: value.NewString("42"), wantErr: "expected integer value"},
		{name: "nil", val: value.NewNil(), wantErr: "expected integer value"},
		{name: "nan", val: value.NewFloat(math.NaN()), wantErr: "cannot convert NaN to integer"},
		{name: "positive_infinity", val: value.NewFloat(math.Inf(1)), wantErr: "cannot convert Infinity to integer"},
		{name: "negative_infinity", val: value.NewFloat(math.Inf(-1)), wantErr: "cannot convert -Infinity to integer"},
		{name: "overflow_high", val: value.NewFloat(1e19), wantErr: "float 1e+19 is out of integer range"},
		{name: "overflow_low", val: value.NewFloat(-1e19), wantErr: "float -1e+19 is out of integer range"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := value.ValueToInt64(tc.val)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("ValueToInt64 error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValueToInt64 error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("ValueToInt64 = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRuntimeKindStringFallback(t *testing.T) {
	t.Parallel()

	// Without the vibes runtime linked into this test binary the
	// RuntimeStringer hook is nil, so runtime-only kinds fall back to a
	// generic rendering. This pins the fallback contract embedders see
	// when they consume vibes/value standalone.
	if got := value.NewValue(value.KindEnum, fakeEnum{}).String(); got != "<enum>" {
		t.Errorf("enum fallback String() = %q, want %q", got, "<enum>")
	}
	if got := value.NewValue(value.KindFunction, fakeFunction{}).String(); got != "<function>" {
		t.Errorf("function fallback String() = %q, want %q", got, "<function>")
	}
}
