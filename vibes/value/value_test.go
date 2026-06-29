package value_test

import (
	"errors"
	"math"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

// TestHashDataRoundTrip pins the public Data payload of a hash to the bare
// entry map. A hash stores its entries inside an unexported wrapper to carry
// optional Ruby-style default metadata, but that wrapper must never leak
// through Data: embedders inspect entries via Data and reconstruct a value via
// NewValue(v.Kind(), v.Data()). The round-trip must yield a usable KindHash
// whose accessors do not panic on the wrong payload type.
func TestHashDataRoundTrip(t *testing.T) {
	t.Parallel()

	t.Run("data_exposes_entry_map", func(t *testing.T) {
		t.Parallel()
		entries := map[string]value.Value{"k": value.NewInt(1)}
		got, ok := value.NewHash(entries).Data().(map[string]value.Value)
		if !ok {
			t.Fatalf("hash Data() = %T, want map[string]value.Value", value.NewHash(entries).Data())
		}
		if got["k"].Int() != 1 {
			t.Fatalf("hash Data()[\"k\"] = %v, want 1", got["k"])
		}
	})

	t.Run("data_exposes_entry_map_with_defaults", func(t *testing.T) {
		t.Parallel()
		// Default metadata must not change the public payload type: a hash
		// carrying a default value still surfaces its bare entry map.
		entries := map[string]value.Value{"k": value.NewInt(1)}
		h := value.NewHashWithDefault(entries, value.NewInt(99), value.NewNil())
		got, ok := h.Data().(map[string]value.Value)
		if !ok {
			t.Fatalf("hash-with-default Data() = %T, want map[string]value.Value", h.Data())
		}
		if got["k"].Int() != 1 {
			t.Fatalf("hash-with-default Data()[\"k\"] = %v, want 1", got["k"])
		}
	})

	t.Run("round_trips_through_new_value", func(t *testing.T) {
		t.Parallel()
		entries := map[string]value.Value{"a": value.NewInt(1), "b": value.NewString("x")}
		original := value.NewHash(entries)

		rebuilt := value.NewValue(original.Kind(), original.Data())
		if rebuilt.Kind() != value.KindHash {
			t.Fatalf("rebuilt Kind() = %s, want hash", rebuilt.Kind())
		}
		// The accessor must not panic and must recover the entries; a value
		// whose payload was the unexported wrapper would panic here.
		if got := rebuilt.Hash(); got["a"].Int() != 1 || got["b"].String() != "x" {
			t.Fatalf("rebuilt Hash() = %v, want original entries", got)
		}
		if !rebuilt.Equal(original) {
			t.Fatalf("rebuilt hash = %s, want value equal to %s", rebuilt, original)
		}
	})

	t.Run("nil_entry_map_round_trips", func(t *testing.T) {
		t.Parallel()
		original := value.NewHash(nil)
		rebuilt := value.NewValue(original.Kind(), original.Data())
		if rebuilt.Kind() != value.KindHash {
			t.Fatalf("rebuilt Kind() = %s, want hash", rebuilt.Kind())
		}
		if got := rebuilt.Hash(); got != nil {
			t.Fatalf("rebuilt Hash() = %v, want nil", got)
		}
		if !rebuilt.Equal(original) {
			t.Fatalf("rebuilt empty hash = %s, want value equal to %s", rebuilt, original)
		}
	})
}

func TestTypedHashMaterializesLegacyMapLazily(t *testing.T) {
	t.Parallel()

	hash := value.NewTypedHash(0)
	if entries, ok := hash.HashStringMapIfMaterialized(); ok || entries != nil {
		t.Fatalf("new typed hash materialized legacy map = %v, %v; want nil, false", entries, ok)
	}
	if err := hash.HashSet(value.NewString("score"), value.NewInt(7)); err != nil {
		t.Fatalf("HashSet(\"score\") error = %v", err)
	}
	if got, ok, err := hash.HashGet(value.NewString("score")); err != nil || !ok || !got.Equal(value.NewInt(7)) {
		t.Fatalf("HashGet(\"score\") = %s, %v, %v; want 7, true, nil", got.Inspect(), ok, err)
	}
	if entries, ok := hash.HashStringMapIfMaterialized(); ok || entries != nil {
		t.Fatalf("typed hash materialized legacy map before Hash() = %v, %v; want nil, false", entries, ok)
	}

	entries := hash.Hash()
	if got := entries["score"]; !got.Equal(value.NewInt(7)) {
		t.Fatalf("Hash()[\"score\"] = %s, want 7", got.Inspect())
	}
	if materialized, ok := hash.HashStringMapIfMaterialized(); !ok || materialized == nil {
		t.Fatalf("typed hash legacy map materialized = %v, %v; want non-nil, true", materialized, ok)
	}

	if err := hash.HashSet(value.NewString("active"), value.NewBool(true)); err != nil {
		t.Fatalf("HashSet(\"active\") error = %v", err)
	}
	if got := entries["active"]; !got.Equal(value.NewBool(true)) {
		t.Fatalf("materialized Hash()[\"active\"] = %s, want true", got.Inspect())
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

	typedHash := value.NewTypedHash(0)
	if err := typedHash.HashSet(value.NewString("k"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(\"k\") error = %v", err)
	}

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
		{"empty_typed_hash", value.NewTypedHash(0), false},
		{"nonempty_typed_hash", typedHash, true},
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

func TestTypedHashStringPreservesDisplayCollidingEntries(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{})
	if err := hash.HashSet(value.NewSymbol("a"), value.NewInt(1)); err != nil {
		t.Fatalf("HashSet(:a) error = %v", err)
	}
	if err := hash.HashSet(value.NewString("a"), value.NewInt(2)); err != nil {
		t.Fatalf("HashSet(\"a\") error = %v", err)
	}

	rendered := hash.String()
	requireTypedHashCollisionString(t, rendered)
	if got, want := hash.StringByteLen(), len(rendered); got != want {
		t.Fatalf("StringByteLen() = %d, want len(String()) = %d", got, want)
	}
	if got, want := hash.StringRuneLen(), utf8.RuneCountInString(rendered); got != want {
		t.Fatalf("StringRuneLen() = %d, want rendered rune count %d", got, want)
	}

	bounded, err := hash.StringBounded(1 << 20)
	if err != nil {
		t.Fatalf("StringBounded() error = %v, want nil", err)
	}
	requireTypedHashCollisionString(t, bounded)

	boundedLen, err := hash.StringByteLenBounded(func() error { return nil })
	if err != nil {
		t.Fatalf("StringByteLenBounded() error = %v, want nil", err)
	}
	if want := hash.StringByteLen(); boundedLen != want {
		t.Fatalf("StringByteLenBounded() = %d, want StringByteLen() = %d", boundedLen, want)
	}

	limit := hash.StringByteLen() - 1
	got, truncated, err := hash.StringByteLenBoundedUpTo(limit, func() error { return nil })
	if err != nil {
		t.Fatalf("StringByteLenBoundedUpTo() error = %v, want nil", err)
	}
	if !truncated {
		t.Fatalf("StringByteLenBoundedUpTo() truncated = false, want true")
	}
	if got != limit+1 {
		t.Fatalf("StringByteLenBoundedUpTo() = %d, want limit+1", got)
	}
}

func TestTypedHashStringCycleDetection(t *testing.T) {
	t.Parallel()

	hash := value.NewHash(map[string]value.Value{})
	if err := hash.HashSet(value.NewSymbol("self"), hash); err != nil {
		t.Fatalf("HashSet(:self) error = %v", err)
	}
	if got := hash.String(); got != "{self: <cycle>}" {
		t.Fatalf("String() = %q, want typed hash cycle marker", got)
	}
	if got, want := hash.StringByteLen(), len(hash.String()); got != want {
		t.Fatalf("StringByteLen() = %d, want len(String()) = %d", got, want)
	}
}

func requireTypedHashCollisionString(t *testing.T, got string) {
	t.Helper()
	if !strings.HasPrefix(got, "{") || !strings.HasSuffix(got, "}") {
		t.Fatalf("typed hash String() = %q, want hash delimiters", got)
	}
	if strings.Count(got, "a: ") != 2 {
		t.Fatalf("typed hash String() = %q, want two display-colliding a entries", got)
	}
	if !strings.Contains(got, "a: 1") || !strings.Contains(got, "a: 2") {
		t.Fatalf("typed hash String() = %q, want both typed entries", got)
	}
}

func TestValueStringByteLen(t *testing.T) {
	t.Parallel()

	big := value.NewString(strings.Repeat("x", 1024))

	tests := []struct {
		name string
		val  value.Value
	}{
		{"nil", value.NewNil()},
		{"bool", value.NewBool(true)},
		{"int", value.NewInt(-42)},
		{"float", value.NewFloat(2.5)},
		{"float_infinity", value.NewFloat(math.Inf(1))},
		{"string", value.NewString("hello")},
		{"symbol", value.NewSymbol("status")},
		{"money", value.NewMoney(mustMoney(t, 1999, "usd"))},
		{"duration", value.NewDuration(value.DurationFromSeconds(90))},
		{"time", value.NewTime(time.Date(2024, 6, 1, 12, 30, 0, 500_000_000, time.UTC))},
		{"range", value.NewRange(value.Range{Start: 1, End: 5})},
		{"empty_array", value.NewArray(nil)},
		{"single_array", value.NewArray([]value.Value{value.NewInt(1)})},
		{
			"nested_array",
			value.NewArray([]value.Value{
				value.NewInt(1),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewNil(),
			}),
		},
		{"empty_hash", value.NewHash(nil)},
		{
			"single_hash",
			value.NewHash(map[string]value.Value{"name": value.NewString("acme")}),
		},
		{"runtime_kind_fallback", value.NewValue(value.KindBlock, fakeBlock{})},
		// An aggregate whose rendering expands far beyond its own footprint: a
		// short array holding many references to one large string materializes a
		// representation many times the value's memory. The projection must still
		// equal the eventual byte count exactly.
		{
			"array_of_repeated_large_string",
			value.NewArray([]value.Value{big, big, big, big, big}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := tc.val.StringByteLen(), len(tc.val.String()); got != want {
				t.Fatalf("StringByteLen() = %d, want len(String()) = %d", got, want)
			}
		})
	}
}

func TestValueStringRuneLen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  value.Value
	}{
		{"string", value.NewString("a🙂")},
		{"symbol", value.NewSymbol("status")},
		{
			"nested_array",
			value.NewArray([]value.Value{
				value.NewString("a🙂"),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewNil(),
			}),
		},
		{
			"single_hash",
			value.NewHash(map[string]value.Value{"na🙂me": value.NewString("acme🙂")}),
		},
		{"runtime_kind_fallback", value.NewValue(value.KindBlock, fakeBlock{})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got, want := tc.val.StringRuneLen(), utf8.RuneCountInString(tc.val.String()); got != want {
				t.Fatalf("StringRuneLen() = %d, want rendered rune count %d", got, want)
			}
		})
	}
}

func TestValueStringBounded(t *testing.T) {
	t.Parallel()

	t.Run("small_value_renders_fully", func(t *testing.T) {
		t.Parallel()
		v := value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2), value.NewInt(3)})
		got, err := v.StringBounded(1024)
		if err != nil {
			t.Fatalf("StringBounded() error = %v, want nil", err)
		}
		if want := "[1, 2, 3]"; got != want {
			t.Fatalf("StringBounded() = %q, want %q", got, want)
		}
	})

	t.Run("matches_String_for_composites_under_limit", func(t *testing.T) {
		t.Parallel()
		// A single-entry hash renders deterministically, so the bounded path
		// can be compared byte-for-byte against the unbounded one. Multi-entry
		// hashes iterate in Go's randomized map order, so two independent
		// renderings of the same hash need not match textually.
		v := value.NewHash(map[string]value.Value{
			"a": value.NewArray([]value.Value{value.NewInt(1), value.NewString("two")}),
		})
		got, err := v.StringBounded(1 << 20)
		if err != nil {
			t.Fatalf("StringBounded() error = %v, want nil", err)
		}
		if want := "{a: [1, two]}"; got != want {
			t.Fatalf("StringBounded() = %q, want %q", got, want)
		}
	})

	t.Run("non_positive_limit_is_unbounded", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 5000)
		for i := range elems {
			elems[i] = value.NewString("abcdefghij")
		}
		v := value.NewArray(elems)
		for _, limit := range []int{0, -1} {
			got, err := v.StringBounded(limit)
			if err != nil {
				t.Fatalf("StringBounded(%d) error = %v, want nil", limit, err)
			}
			if want := v.String(); got != want {
				t.Fatalf("StringBounded(%d) did not match String()", limit)
			}
		}
	})

	t.Run("large_array_trips_limit", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 100000)
		for i := range elems {
			elems[i] = value.NewString("abcdefghij")
		}
		v := value.NewArray(elems)

		const limit = 4096
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		// The partial output is bounded to roughly the limit plus one trailing
		// element, never the full multi-megabyte rendering.
		if len(got) > limit+64 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+64)
		}
		if full := v.String(); len(got) >= len(full) {
			t.Fatalf("partial rendering (%d bytes) was not shorter than full rendering (%d bytes)", len(got), len(full))
		}
	})

	t.Run("large_hash_trips_limit", func(t *testing.T) {
		t.Parallel()
		entries := make(map[string]value.Value, 100000)
		for i := range 100000 {
			entries[strconv.Itoa(i)] = value.NewString("abcdefghij")
		}
		v := value.NewHash(entries)

		const limit = 4096
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) > limit+64 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+64)
		}
	})

	t.Run("deeply_nested_array_trips_limit", func(t *testing.T) {
		t.Parallel()
		v := value.NewArray([]value.Value{value.NewInt(0)})
		for range 5000 {
			v = value.NewArray([]value.Value{v})
		}

		const limit = 1024
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) > limit+64 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+64)
		}
	})

	t.Run("oversized_scalar_trips_limit", func(t *testing.T) {
		t.Parallel()
		v := value.NewString(strings.Repeat("x", 1<<16))
		got, err := v.StringBounded(1024)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) != 1024 {
			t.Fatalf("partial scalar rendering = %d bytes, want 1024", len(got))
		}
	})

	t.Run("oversized_hash_key_trips_limit", func(t *testing.T) {
		t.Parallel()
		// A single key larger than the limit must trip the budget while it is
		// being written rather than copying the whole key into the buffer first.
		key := strings.Repeat("k", 1<<20)
		v := value.NewHash(map[string]value.Value{key: value.NewInt(1)})

		const limit = 1024
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		// The partial output never contains the full multi-megabyte key.
		if len(got) > limit+8 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+8)
		}
	})

	t.Run("oversized_nested_scalar_trips_limit", func(t *testing.T) {
		t.Parallel()
		// A huge scalar buried inside a composite must be capped to the budget;
		// the renderer must not materialize the whole element before checking.
		huge := value.NewString(strings.Repeat("x", 1<<20))
		v := value.NewArray([]value.Value{huge})

		const limit = 1024
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) > limit+8 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+8)
		}
	})

	t.Run("oversized_nested_hash_value_trips_limit", func(t *testing.T) {
		t.Parallel()
		// A huge scalar value (rather than key) inside a hash must also be
		// capped to the remaining budget as it is written.
		huge := value.NewString(strings.Repeat("y", 1<<20))
		v := value.NewHash(map[string]value.Value{"k": huge})

		const limit = 1024
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) > limit+8 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+8)
		}
	})

	t.Run("cycle_still_renders", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 1)
		arr := value.NewArray(elems)
		elems[0] = arr
		got, err := arr.StringBounded(1024)
		if err != nil {
			t.Fatalf("StringBounded() error = %v, want nil", err)
		}
		if want := "[<cycle>]"; got != want {
			t.Fatalf("StringBounded() = %q, want %q", got, want)
		}
	})

	// The closing delimiter counts against the byte budget. When the contents
	// fill the budget exactly, appending the trailing bracket/brace must trip
	// the limit rather than emit a result one byte over the configured cap.
	t.Run("closing_delimiter_respects_limit", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			value value.Value
			limit int
			want  string
		}{
			{
				// "[]" is two bytes; a one-byte budget cannot fit the closer.
				name:  "empty_array_at_limit",
				value: value.NewArray(nil),
				limit: 1,
				want:  "[",
			},
			{
				// "{}" is two bytes; a one-byte budget cannot fit the closer.
				name:  "empty_hash_at_limit",
				value: value.NewHash(nil),
				limit: 1,
				want:  "{",
			},
			{
				// "[1]" is three bytes; with a two-byte budget the element fills
				// the budget and the trailing "]" must trip the limit.
				name:  "single_element_array_at_limit",
				value: value.NewArray([]value.Value{value.NewInt(1)}),
				limit: 2,
				want:  "[1",
			},
			{
				// "{k: 1}" is six bytes; the contents fill a five-byte budget and
				// the trailing "}" must trip the limit.
				name:  "single_entry_hash_at_limit",
				value: value.NewHash(map[string]value.Value{"k": value.NewInt(1)}),
				limit: 5,
				want:  "{k: 1",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := tc.value.StringBounded(tc.limit)
				if !errors.Is(err, value.ErrStringRenderTruncated) {
					t.Fatalf("StringBounded(%d) error = %v, want ErrStringRenderTruncated", tc.limit, err)
				}
				if got != tc.want {
					t.Fatalf("StringBounded(%d) = %q, want %q", tc.limit, got, tc.want)
				}
				if len(got) > tc.limit {
					t.Fatalf("StringBounded(%d) = %d bytes, must not exceed the limit", tc.limit, len(got))
				}
			})
		}
	})

	// Element and key/value separators count against the byte budget just like
	// any other byte. A key that fills the budget exactly must trip the limit on
	// its ": " separator, and an element that fills the budget must trip on its
	// ", " separator, rather than letting the separators push the partial output
	// past the configured cap.
	t.Run("separators_respect_limit", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			value value.Value
			limit int
			want  string
		}{
			{
				// The finding's example: "{" + "k" fills a two-byte budget, so the
				// ": " separator must trip the limit instead of emitting "{k: ".
				name:  "hash_kv_separator_at_key_limit",
				value: value.NewHash(map[string]value.Value{"k": value.NewInt(1)}),
				limit: 2,
				want:  "{k",
			},
			{
				// "{" + "k" + ":" exhausts a three-byte budget mid-separator; only
				// the first separator byte fits, and the result stays at the cap.
				name:  "hash_kv_separator_partial",
				value: value.NewHash(map[string]value.Value{"k": value.NewInt(1)}),
				limit: 3,
				want:  "{k:",
			},
			{
				// "[1" fills a two-byte budget; the ", " element separator before
				// the second element must trip the limit, never reaching it.
				name:  "array_element_separator_at_limit",
				value: value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)}),
				limit: 2,
				want:  "[1",
			},
			{
				// "[1," exhausts a three-byte budget mid-separator; only the first
				// separator byte fits, and the result stays at the cap.
				name:  "array_element_separator_partial",
				value: value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)}),
				limit: 3,
				want:  "[1,",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := tc.value.StringBounded(tc.limit)
				if !errors.Is(err, value.ErrStringRenderTruncated) {
					t.Fatalf("StringBounded(%d) error = %v, want ErrStringRenderTruncated", tc.limit, err)
				}
				if got != tc.want {
					t.Fatalf("StringBounded(%d) = %q, want %q", tc.limit, got, tc.want)
				}
				if len(got) > tc.limit {
					t.Fatalf("StringBounded(%d) = %d bytes, must not exceed the limit", tc.limit, len(got))
				}
			})
		}
	})

	// A multi-entry hash iterates in randomized map order, so two entries can
	// appear in either sequence. Whichever entry comes first, the ", " separator
	// before the second entry must trip the budget and the partial output must
	// never exceed the configured cap.
	t.Run("hash_entry_separator_respects_limit", func(t *testing.T) {
		t.Parallel()
		v := value.NewHash(map[string]value.Value{
			"a": value.NewInt(1),
			"b": value.NewInt(2),
		})
		// "{a: 1" / "{b: 2" is five bytes; one byte more fits the closer but not
		// the ", " separator that would precede the second entry.
		const limit = 6
		got, err := v.StringBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("StringBounded(%d) error = %v, want ErrStringRenderTruncated", limit, err)
		}
		if len(got) > limit {
			t.Fatalf("StringBounded(%d) = %d bytes, must not exceed the limit", limit, len(got))
		}
	})

	// One byte past the boundary is enough for the closing delimiter, so these
	// render in full with no truncation.
	t.Run("closing_delimiter_fits_at_limit", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			value value.Value
			limit int
			want  string
		}{
			{
				name:  "empty_array",
				value: value.NewArray(nil),
				limit: 2,
				want:  "[]",
			},
			{
				name:  "empty_hash",
				value: value.NewHash(nil),
				limit: 2,
				want:  "{}",
			},
			{
				name:  "single_element_array",
				value: value.NewArray([]value.Value{value.NewInt(1)}),
				limit: 3,
				want:  "[1]",
			},
			{
				name:  "single_entry_hash",
				value: value.NewHash(map[string]value.Value{"k": value.NewInt(1)}),
				limit: 6,
				want:  "{k: 1}",
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := tc.value.StringBounded(tc.limit)
				if err != nil {
					t.Fatalf("StringBounded(%d) error = %v, want nil", tc.limit, err)
				}
				if got != tc.want {
					t.Fatalf("StringBounded(%d) = %q, want %q", tc.limit, got, tc.want)
				}
			})
		}
	})
}

// TestValueStringBoundedNeverExceedsLimit is a property-style sweep over deeply
// nested arrays and hashes. Across a range of small limits it asserts two
// invariants for every value:
//
//   - the returned partial output is never longer than the requested limit, so
//     no individual write site (opening or closing delimiter, separator, key,
//     value, or scalar repr) can push the result past the cap, and
//   - ErrStringRenderTruncated is returned exactly when the full rendering would
//     not have fit, i.e. when len(value.String()) > limit, so truncation is
//     reported neither too eagerly nor too late.
//
// The nested fixtures exercise the delimiter-on-descent paths that previously
// overshot the budget by emitting a child's opening "[" or "{" after the parent
// had already filled the cap.
func TestValueStringBoundedNeverExceedsLimit(t *testing.T) {
	t.Parallel()

	fixtures := []struct {
		name  string
		value value.Value
	}{
		{name: "scalar_int", value: value.NewInt(1234567)},
		{name: "scalar_string", value: value.NewString("hello world")},
		{name: "empty_array", value: value.NewArray(nil)},
		{name: "empty_hash", value: value.NewHash(nil)},
		{name: "flat_array", value: value.NewArray([]value.Value{
			value.NewInt(1), value.NewInt(22), value.NewInt(333),
		})},
		{name: "flat_hash", value: value.NewHash(map[string]value.Value{
			"k": value.NewInt(1),
		})},
		{name: "nested_array", value: value.NewArray([]value.Value{
			value.NewArray([]value.Value{value.NewInt(1)}),
			value.NewArray([]value.Value{value.NewInt(2)}),
		})},
		{name: "nested_hash", value: value.NewHash(map[string]value.Value{
			"a": value.NewHash(map[string]value.Value{"b": value.NewInt(1)}),
		})},
		{name: "deeply_nested_array", value: value.NewArray([]value.Value{
			value.NewArray([]value.Value{
				value.NewArray([]value.Value{
					value.NewArray([]value.Value{value.NewString("x")}),
				}),
			}),
		})},
		{name: "mixed_array_of_hashes", value: value.NewArray([]value.Value{
			value.NewHash(map[string]value.Value{"id": value.NewInt(1)}),
			value.NewHash(map[string]value.Value{"id": value.NewInt(2)}),
		})},
		{name: "hash_of_arrays", value: value.NewHash(map[string]value.Value{
			"xs": value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)}),
		})},
		{name: "scalar_variety", value: value.NewArray([]value.Value{
			value.NewNil(), value.NewBool(true), value.NewFloat(1.5),
			value.NewSymbol("sym"), value.NewString("str"),
		})},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			full := tc.value.String()
			for limit := range 41 {
				got, err := tc.value.StringBounded(limit)

				if limit > 0 && len(got) > limit {
					t.Fatalf("StringBounded(%d) = %d bytes (%q), must not exceed the limit", limit, len(got), got)
				}

				// A non-positive limit means unbounded: it must render in full and
				// never report truncation.
				if limit <= 0 {
					if err != nil {
						t.Fatalf("StringBounded(%d) error = %v, want nil for unbounded render", limit, err)
					}
					if got != full {
						t.Fatalf("StringBounded(%d) = %q, want full render %q", limit, got, full)
					}
					continue
				}

				wantTruncated := len(full) > limit
				gotTruncated := errors.Is(err, value.ErrStringRenderTruncated)
				if gotTruncated != wantTruncated {
					t.Fatalf("StringBounded(%d) truncated = %v (err %v), want %v (full %q is %d bytes)",
						limit, gotTruncated, err, wantTruncated, full, len(full))
				}
				if err != nil && !gotTruncated {
					t.Fatalf("StringBounded(%d) error = %v, want only ErrStringRenderTruncated", limit, err)
				}
				if !wantTruncated && got != full {
					t.Fatalf("StringBounded(%d) = %q, want full render %q", limit, got, full)
				}
				if wantTruncated && got != full[:len(got)] {
					t.Fatalf("StringBounded(%d) = %q, want a prefix of full render %q", limit, got, full)
				}
			}
		})
	}
}

func TestValueStringByteLenCycleDetection(t *testing.T) {
	t.Parallel()

	t.Run("self_referential_array", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 1)
		arr := value.NewArray(elems)
		elems[0] = arr
		if got, want := arr.StringByteLen(), len(arr.String()); got != want {
			t.Fatalf("StringByteLen() = %d, want len(String()) = %d", got, want)
		}
	})

	t.Run("self_referential_hash", func(t *testing.T) {
		t.Parallel()
		entries := make(map[string]value.Value)
		hash := value.NewHash(entries)
		entries["self"] = hash
		if got, want := hash.StringByteLen(), len(hash.String()); got != want {
			t.Fatalf("StringByteLen() = %d, want len(String()) = %d", got, want)
		}
	})

	t.Run("shared_subtree_is_counted_each_appearance", func(t *testing.T) {
		t.Parallel()
		shared := value.NewArray([]value.Value{value.NewInt(1)})
		outer := value.NewArray([]value.Value{shared, shared})
		if got, want := outer.StringByteLen(), len(outer.String()); got != want {
			t.Fatalf("StringByteLen() = %d, want len(String()) = %d", got, want)
		}
	})
}

func TestValueStringByteLenBounded(t *testing.T) {
	t.Parallel()

	t.Run("matches_unbounded_count", func(t *testing.T) {
		t.Parallel()
		vals := []value.Value{
			value.NewInt(-42),
			value.NewString("hello"),
			value.NewArray([]value.Value{
				value.NewInt(1),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewNil(),
			}),
			value.NewHash(map[string]value.Value{"name": value.NewString("acme")}),
		}
		for _, v := range vals {
			got, err := v.StringByteLenBounded(func() error { return nil })
			if err != nil {
				t.Fatalf("StringByteLenBounded() error = %v", err)
			}
			if want := v.StringByteLen(); got != want {
				t.Fatalf("StringByteLenBounded() = %d, want StringByteLen() = %d", got, want)
			}
		}
	})

	t.Run("propagates_step_error", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("budget exhausted")
		arr := value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)})

		calls := 0
		_, err := arr.StringByteLenBounded(func() error {
			calls++
			if calls >= 2 {
				return sentinel
			}
			return nil
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("StringByteLenBounded() error = %v, want %v", err, sentinel)
		}
	})

	t.Run("shared_exponential_graph_trips_budget", func(t *testing.T) {
		t.Parallel()

		// A compact aggregate with an exponentially shared, acyclic graph: each
		// level holds two references to the same child array (the shape of
		// repeatedly evaluating a = [a, a]). The cycle marker bounds the rendering
		// and the memory stays small, but the projection re-walks every shared
		// subtree, so the traversal is exponential in the depth. Charging the step
		// callback per node lets a bounded budget trip the walk instead of hanging.
		const depth = 25
		const budget = 1_000_000

		cur := value.NewArray([]value.Value{value.NewInt(0)})
		for range depth {
			cur = value.NewArray([]value.Value{cur, cur})
		}

		sentinel := errors.New("step budget exhausted")
		calls := 0
		_, err := cur.StringByteLenBounded(func() error {
			calls++
			if calls > budget {
				return sentinel
			}
			return nil
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("StringByteLenBounded() error = %v, want %v (calls=%d)", err, sentinel, calls)
		}
	})
}

func TestValueStringRuneLenBounded(t *testing.T) {
	t.Parallel()

	t.Run("matches_unbounded_count", func(t *testing.T) {
		t.Parallel()
		vals := []value.Value{
			value.NewInt(-42),
			value.NewString("a🙂"),
			value.NewArray([]value.Value{
				value.NewString("a🙂"),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewNil(),
			}),
			value.NewHash(map[string]value.Value{"na🙂me": value.NewString("acme🙂")}),
		}
		for _, v := range vals {
			got, err := v.StringRuneLenBounded(func() error { return nil })
			if err != nil {
				t.Fatalf("StringRuneLenBounded() error = %v", err)
			}
			if want := v.StringRuneLen(); got != want {
				t.Fatalf("StringRuneLenBounded() = %d, want StringRuneLen() = %d", got, want)
			}
		}
	})

	t.Run("propagates_step_error", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("budget exhausted")
		arr := value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)})

		calls := 0
		_, err := arr.StringRuneLenBounded(func() error {
			calls++
			if calls >= 2 {
				return sentinel
			}
			return nil
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("StringRuneLenBounded() error = %v, want %v", err, sentinel)
		}
	})
}

func TestValueStringByteLenBoundedUpTo(t *testing.T) {
	t.Parallel()

	t.Run("matches_exact_count_within_limit", func(t *testing.T) {
		t.Parallel()

		val := value.NewArray([]value.Value{
			value.NewString("one"),
			value.NewArray([]value.Value{value.NewInt(2)}),
		})
		want := val.StringByteLen()
		got, truncated, err := val.StringByteLenBoundedUpTo(want, func() error { return nil })
		if err != nil {
			t.Fatalf("StringByteLenBoundedUpTo() error = %v", err)
		}
		if truncated {
			t.Fatalf("StringByteLenBoundedUpTo() truncated, want false")
		}
		if got != want {
			t.Fatalf("StringByteLenBoundedUpTo() = %d, want %d", got, want)
		}
	})

	t.Run("stops_after_limit", func(t *testing.T) {
		t.Parallel()

		val := value.NewArray([]value.Value{value.NewString("one"), value.NewString("two")})
		got, truncated, err := val.StringByteLenBoundedUpTo(4, func() error { return nil })
		if err != nil {
			t.Fatalf("StringByteLenBoundedUpTo() error = %v", err)
		}
		if !truncated {
			t.Fatalf("StringByteLenBoundedUpTo() truncated = false, want true")
		}
		if got != 5 {
			t.Fatalf("StringByteLenBoundedUpTo() = %d, want limit+1", got)
		}
	})

	t.Run("shared_exponential_graph_stops_at_cap", func(t *testing.T) {
		t.Parallel()

		cur := value.NewArray([]value.Value{value.NewInt(0)})
		for range 25 {
			cur = value.NewArray([]value.Value{cur, cur})
		}

		calls := 0
		got, truncated, err := cur.StringByteLenBoundedUpTo(4, func() error {
			calls++
			if calls > 1_000 {
				return errors.New("walk did not stop at cap")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("StringByteLenBoundedUpTo() error = %v", err)
		}
		if !truncated {
			t.Fatalf("StringByteLenBoundedUpTo() truncated = false, want true")
		}
		if got != 5 {
			t.Fatalf("StringByteLenBoundedUpTo() = %d, want limit+1", got)
		}
		if calls > 25 {
			t.Fatalf("StringByteLenBoundedUpTo() walked %d nodes, want capped walk", calls)
		}
	})

	t.Run("propagates_step_error", func(t *testing.T) {
		t.Parallel()

		sentinel := errors.New("budget exhausted")
		_, _, err := value.NewArray([]value.Value{value.NewInt(1)}).StringByteLenBoundedUpTo(1024, func() error {
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("StringByteLenBoundedUpTo() error = %v, want %v", err, sentinel)
		}
	})
}

func BenchmarkValueStringLargeComposite(b *testing.B) {
	b.Run("array_100000", func(b *testing.B) {
		elems := make([]value.Value, 100000)
		for i := range elems {
			elems[i] = value.NewString("abcdefghij")
		}
		v := value.NewArray(elems)

		b.ReportAllocs()
		for b.Loop() {
			benchValueStringSink = v.String()
		}
	})

	b.Run("hash_10000", func(b *testing.B) {
		entries := make(map[string]value.Value, 10000)
		for i := range 10000 {
			entries[strconv.Itoa(i)] = value.NewString("abcdefghij")
		}
		v := value.NewHash(entries)

		b.ReportAllocs()
		for b.Loop() {
			benchValueStringSink = v.String()
		}
	})

	b.Run("bounded_array_100000", func(b *testing.B) {
		elems := make([]value.Value, 100000)
		for i := range elems {
			elems[i] = value.NewString("abcdefghij")
		}
		v := value.NewArray(elems)

		b.ReportAllocs()
		for b.Loop() {
			benchValueStringSink, _ = v.StringBounded(4096)
		}
	})
}

func TestValueStringByteLenDoesNotMaterializeRendering(t *testing.T) {
	// Deliberately not parallel: this measures heap bytes via runtime.MemStats,
	// which observes the whole process. A non-parallel top-level test runs while
	// every parallel sibling is paused, so the only allocations during the
	// measured window are this test's own.

	// An aggregate whose rendering expands far beyond its footprint: a short
	// array holding many references to one large string. String joins the large
	// string once per element, allocating a representation many times the value's
	// memory. StringByteLen must report the same byte length without allocating
	// that rendering, so a sandbox can reject an oversized interpolation before
	// the join happens rather than after.
	const elementBytes = 8192
	const elementCount = 64

	large := value.NewString(strings.Repeat("x", elementBytes))
	elems := make([]value.Value, elementCount)
	for i := range elems {
		elems[i] = large
	}
	arr := value.NewArray(elems)

	stringBytes := allocBytes(t, func() { _ = arr.String() })
	byteLenBytes := allocBytes(t, func() { _ = arr.StringByteLen() })

	rendered := uint64(elementBytes * elementCount)
	if stringBytes < rendered {
		t.Fatalf("String allocated %d bytes, want at least the rendered %d", stringBytes, rendered)
	}
	// StringByteLen walks the structure with only small bookkeeping allocations;
	// it must not allocate anything close to the rendered representation. A guard
	// that projected the size by calling String first would allocate as String
	// does.
	if byteLenBytes >= rendered {
		t.Fatalf("StringByteLen allocated %d bytes, want well below the rendered %d", byteLenBytes, rendered)
	}
}

// allocBytes reports the heap bytes fn allocates. It disables the garbage
// collector for the measurement window and reads the cumulative allocation
// counter before and after, so a sweep cannot reclaim memory mid-measurement
// and skew the delta. Callers must invoke it from a non-parallel test so no
// sibling goroutine allocates concurrently.
func allocBytes(t *testing.T, fn func()) uint64 {
	t.Helper()
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestValueWriteStringTo(t *testing.T) {
	t.Parallel()

	big := value.NewString(strings.Repeat("x", 1024))

	tests := []struct {
		name string
		val  value.Value
	}{
		{"nil", value.NewNil()},
		{"bool", value.NewBool(true)},
		{"int", value.NewInt(-42)},
		{"float", value.NewFloat(2.5)},
		{"float_infinity", value.NewFloat(math.Inf(1))},
		{"string", value.NewString("hello")},
		{"symbol", value.NewSymbol("status")},
		{"money", value.NewMoney(mustMoney(t, 1999, "usd"))},
		{"duration", value.NewDuration(value.DurationFromSeconds(90))},
		{"time", value.NewTime(time.Date(2024, 6, 1, 12, 30, 0, 500_000_000, time.UTC))},
		{"range", value.NewRange(value.Range{Start: 1, End: 5})},
		{"empty_array", value.NewArray(nil)},
		{"single_array", value.NewArray([]value.Value{value.NewInt(1)})},
		{
			"nested_array",
			value.NewArray([]value.Value{
				value.NewInt(1),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewNil(),
			}),
		},
		{"empty_hash", value.NewHash(nil)},
		{
			"single_hash",
			value.NewHash(map[string]value.Value{"name": value.NewString("acme")}),
		},
		{"runtime_kind_fallback", value.NewValue(value.KindBlock, fakeBlock{})},
		{
			"array_of_repeated_large_string",
			value.NewArray([]value.Value{big, big, big, big, big}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var sb strings.Builder
			tc.val.WriteStringTo(&sb)
			if got, want := sb.String(), tc.val.String(); got != want {
				t.Fatalf("WriteStringTo wrote %q, want String() %q", got, want)
			}
		})
	}
}

func TestValueWriteStringToCycleDetection(t *testing.T) {
	t.Parallel()

	t.Run("self_referential_array", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 1)
		arr := value.NewArray(elems)
		elems[0] = arr
		var sb strings.Builder
		arr.WriteStringTo(&sb)
		if got, want := sb.String(), arr.String(); got != want {
			t.Fatalf("WriteStringTo wrote %q, want String() %q", got, want)
		}
	})

	t.Run("self_referential_hash", func(t *testing.T) {
		t.Parallel()
		entries := make(map[string]value.Value)
		hash := value.NewHash(entries)
		entries["self"] = hash
		var sb strings.Builder
		hash.WriteStringTo(&sb)
		if got, want := sb.String(), hash.String(); got != want {
			t.Fatalf("WriteStringTo wrote %q, want String() %q", got, want)
		}
	})

	t.Run("shared_subtree_is_not_a_cycle", func(t *testing.T) {
		t.Parallel()
		shared := value.NewArray([]value.Value{value.NewInt(1)})
		outer := value.NewArray([]value.Value{shared, shared})
		var sb strings.Builder
		outer.WriteStringTo(&sb)
		if got, want := sb.String(), "[[1], [1]]"; got != want {
			t.Fatalf("WriteStringTo wrote %q, want %q", got, want)
		}
	})
}

func TestValueWriteStringToDoesNotMaterializeRendering(t *testing.T) {
	// Deliberately not parallel: this measures heap bytes via runtime.MemStats,
	// which observes the whole process. A non-parallel top-level test runs while
	// every parallel sibling is paused, so the only allocations during the
	// measured window are this test's own.

	// An aggregate whose rendering expands far beyond its footprint: a short
	// array holding many references to one large string. An implementation that
	// rendered the value to a temporary string and then copied it into the
	// destination would transiently hold both the temporary and the destination
	// copy. WriteStringTo must stream straight into the builder, so writing into a
	// builder already grown to the rendered size allocates nothing close to that
	// rendering. The sandbox relies on this so a quota that passed the projected
	// length is not blown by a second full copy.
	const elementBytes = 8192
	const elementCount = 64

	large := value.NewString(strings.Repeat("x", elementBytes))
	elems := make([]value.Value, elementCount)
	for i := range elems {
		elems[i] = large
	}
	arr := value.NewArray(elems)

	rendered := len(arr.String())

	var sb strings.Builder
	sb.Grow(rendered)
	writeBytes := allocBytes(t, func() {
		arr.WriteStringTo(&sb)
	})

	// With the builder pre-grown to hold the result, a streaming writer copies
	// each chunk in place and allocates nothing close to the rendered size. An
	// implementation that built the full string first (WriteString(val.String()))
	// would allocate at least the rendered bytes for that temporary.
	if writeBytes >= uint64(rendered) {
		t.Fatalf("WriteStringTo allocated %d bytes, want well below the rendered %d", writeBytes, rendered)
	}
}

var benchValueStringSink string

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

// TestValueEql checks the hash-key equality predicate: operands must share a
// kind and value, so an Int never eql-matches a Float even when the numeric
// values coincide, while composites still compare by content.
func TestValueEql(t *testing.T) {
	t.Parallel()

	sharedSlice := []value.Value{value.NewInt(1)}

	tests := []struct {
		name  string
		left  value.Value
		right value.Value
		want  bool
	}{
		{"ints", value.NewInt(1), value.NewInt(1), true},
		{"int_vs_float", value.NewInt(1), value.NewFloat(1), false},
		{"floats", value.NewFloat(1), value.NewFloat(1), true},
		{"strings", value.NewString("a"), value.NewString("a"), true},
		{"string_vs_symbol", value.NewString("a"), value.NewSymbol("a"), false},
		{"nil_vs_bool", value.NewNil(), value.NewBool(false), false},
		{"arrays_by_content", value.NewArray([]value.Value{value.NewInt(1)}), value.NewArray([]value.Value{value.NewInt(1)}), true},
		{"arrays_shared", value.NewArray(sharedSlice), value.NewArray(sharedSlice), true},
		{"hashes_by_content", value.NewHash(map[string]value.Value{"a": value.NewInt(1)}), value.NewHash(map[string]value.Value{"a": value.NewInt(1)}), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.left.Eql(tc.right); got != tc.want {
				t.Fatalf("Eql(%s, %s) = %t, want %t", tc.left, tc.right, got, tc.want)
			}
			if got := tc.right.Eql(tc.left); got != tc.want {
				t.Fatalf("Eql is not symmetric for %s and %s", tc.left, tc.right)
			}
		})
	}
}

// TestValueIdentical checks the identity predicate: immutable scalars are
// identical when they share a kind and value, while mutable composites are
// identical only when they share the same backing storage.
func TestValueIdentical(t *testing.T) {
	t.Parallel()

	sharedSlice := []value.Value{value.NewInt(1)}
	// A hash's identity is its hashData wrapper, not its entry map. Aliasing a
	// hash Value (the way `b = a` copies the struct, including its wrapper
	// pointer) preserves identity, so sharedHash compared against itself is
	// identical. Two distinct NewHash calls that merely share an entry map are
	// not, because each call allocates its own wrapper.
	sharedHash := value.NewHash(map[string]value.Value{"a": value.NewInt(1)})
	sharedMap := map[string]value.Value{"a": value.NewInt(1)}
	// A single empty hash must remain identical to itself even though its
	// backing map is allocated from nil input; the fix must not break self
	// identity while making independent empties distinct.
	sameEmptyHash := value.NewHash(nil)

	tests := []struct {
		name  string
		left  value.Value
		right value.Value
		want  bool
	}{
		{"ints", value.NewInt(1), value.NewInt(1), true},
		{"int_vs_float", value.NewInt(1), value.NewFloat(1), false},
		{"floats", value.NewFloat(1.5), value.NewFloat(1.5), true},
		// IEEE NaN != NaN, so value equality alone would make a NaN receiver fail
		// reflexivity (x.equal?(x) == false). Identity treats NaN floats as
		// identical to keep equal? reflexive.
		{"nan_floats_identical", value.NewFloat(math.NaN()), value.NewFloat(math.NaN()), true},
		{"nan_vs_finite_float_distinct", value.NewFloat(math.NaN()), value.NewFloat(1.5), false},
		{"strings_same_content", value.NewString("a"), value.NewString("a"), true},
		{"strings_diff_content", value.NewString("a"), value.NewString("b"), false},
		{"symbols", value.NewSymbol("a"), value.NewSymbol("a"), true},
		{"nils", value.NewNil(), value.NewNil(), true},
		{"ranges", value.NewRange(value.Range{Start: 1, End: 3}), value.NewRange(value.Range{Start: 1, End: 3}), true},
		{"arrays_shared_backing", value.NewArray(sharedSlice), value.NewArray(sharedSlice), true},
		{"arrays_distinct_backing", value.NewArray([]value.Value{value.NewInt(1)}), value.NewArray([]value.Value{value.NewInt(1)}), false},
		// An empty array has no element storage to alias, so two empties report
		// identical regardless of their backing storage.
		{"empty_arrays_identical", value.NewArray([]value.Value{}), value.NewArray([]value.Value{}), true},
		// An empty array produced with spare capacity (the way array.select and
		// peers preallocate via make([]Value, 0, len(arr))) carries a distinct,
		// non-zerobase backing pointer and a non-zero capacity, yet it must still
		// be identical to a literal empty array under the all-empties-alike
		// contract.
		{"empty_array_with_spare_cap_identical", value.NewArray(make([]value.Value, 0, 4)), value.NewArray([]value.Value{}), true},
		{"empty_arrays_distinct_spare_cap_identical", value.NewArray(make([]value.Value, 0, 4)), value.NewArray(make([]value.Value, 0, 8)), true},
		// A non-empty array is never identical to an empty one even though the
		// empty case short-circuits before comparing backing storage.
		{"empty_array_vs_nonempty_distinct", value.NewArray([]value.Value{}), value.NewArray([]value.Value{value.NewInt(1)}), false},
		// Aliasing a hash Value preserves its wrapper, so it is identical to itself.
		{"hashes_shared_wrapper", sharedHash, sharedHash, true},
		// Two NewHash calls allocate distinct wrappers even with a shared entry map.
		{"hashes_shared_entry_map_distinct", value.NewHash(sharedMap), value.NewHash(sharedMap), false},
		{"hashes_distinct_backing", value.NewHash(map[string]value.Value{"a": value.NewInt(1)}), value.NewHash(map[string]value.Value{"a": value.NewInt(1)}), false},
		// Each hash carries its own wrapper, so empties are distinct unlike empty slices.
		{"empty_hashes_distinct_backing", value.NewHash(map[string]value.Value{}), value.NewHash(map[string]value.Value{}), false},
		// Nil-backed hashes built the way the JSON parser builds {} stay distinct
		// objects because each NewHash call allocates a fresh wrapper.
		{"nil_backed_hashes_distinct", value.NewHash(nil), value.NewHash(nil), false},
		{"nil_backed_objects_distinct", value.NewObject(nil), value.NewObject(nil), false},
		{"empty_hash_vs_nil_backed_hash_distinct", value.NewHash(map[string]value.Value{}), value.NewHash(nil), false},
		{"nil_backed_hash_identical_to_itself", sameEmptyHash, sameEmptyHash, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.left.Identical(tc.right); got != tc.want {
				t.Fatalf("Identical(%s, %s) = %t, want %t", tc.left, tc.right, got, tc.want)
			}
			if got := tc.right.Identical(tc.left); got != tc.want {
				t.Fatalf("Identical is not symmetric for %s and %s", tc.left, tc.right)
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
// package-level RuntimeStringer/RuntimeEqualer/RuntimeIdenticaler hooks, and Go
// defers all parallel tests until sequential tests like this one have finished.
func TestRuntimeHooks(t *testing.T) {
	t.Cleanup(func() {
		value.RuntimeStringer = nil
		value.RuntimeEqualer = nil
		value.RuntimeIdenticaler = nil
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
	value.RuntimeIdenticaler = func(left, right value.Value) (bool, bool) {
		if left.Kind() == value.KindEnum {
			return false, true
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
	// Identical consults RuntimeIdenticaler, which here reports the enums as
	// distinct storage even though Equal considers them equivalent. This is the
	// crux of the enum equal? contract: structural equivalence is not identity.
	if enum.Identical(value.NewValue(value.KindEnum, fakeEnum{})) {
		t.Error("hooked enum Identical() = true, want false from RuntimeIdenticaler")
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
