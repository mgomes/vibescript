package value_test

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestValueInspect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  value.Value
		want string
	}{
		{"nil", value.NewNil(), "nil"},
		{"bool_true", value.NewBool(true), "true"},
		{"bool_false", value.NewBool(false), "false"},
		{"int", value.NewInt(-42), "-42"},
		{"float", value.NewFloat(2.5), "2.5"},
		{"float_infinity", value.NewFloat(math.Inf(1)), "Infinity"},
		{"float_nan", value.NewFloat(math.NaN()), "NaN"},
		{"empty_string", value.NewString(""), `""`},
		{"plain_string", value.NewString("hello"), `"hello"`},
		{"string_with_newline", value.NewString("a\nb"), `"a\nb"`},
		{"string_with_tab", value.NewString("a\tb"), `"a\tb"`},
		{"string_with_quote", value.NewString(`say "hi"`), `"say \"hi\""`},
		{"string_with_backslash", value.NewString(`a\b`), `"a\\b"`},
		{"string_with_interpolation_marker", value.NewString("x#{y}"), `"x\#{y}"`},
		{"string_with_lone_hash", value.NewString("a # b"), `"a # b"`},
		{"string_with_carriage_return_is_literal", value.NewString("a\rb"), "\"a\rb\""},
		{"symbol_bare", value.NewSymbol("ok"), ":ok"},
		{"symbol_underscore", value.NewSymbol("_id"), ":_id"},
		{"symbol_with_space", value.NewSymbol("a b"), `:"a b"`},
		{"symbol_empty", value.NewSymbol(""), `:""`},
		{"symbol_leading_digit", value.NewSymbol("1up"), `:"1up"`},
		{"money", value.NewMoney(mustMoney(t, 1999, "usd")), "19.99 USD"},
		{"duration", value.NewDuration(value.DurationFromSeconds(90)), "90s"},
		{
			"time_rfc3339nano",
			value.NewTime(time.Date(2024, 6, 1, 12, 30, 0, 500_000_000, time.UTC)),
			"2024-06-01T12:30:00.5Z",
		},
		{"range", value.NewRange(value.Range{Start: 1, End: 5}), "1..5"},
		{"empty_array", value.NewArray(nil), "[]"},
		{
			"array_mixed",
			value.NewArray([]value.Value{
				value.NewInt(1),
				value.NewString("x"),
				value.NewNil(),
			}),
			`[1, "x", nil]`,
		},
		{
			"nested_array",
			value.NewArray([]value.Value{
				value.NewInt(1),
				value.NewArray([]value.Value{value.NewString("two")}),
				value.NewSymbol("ok"),
			}),
			`[1, ["two"], :ok]`,
		},
		{"empty_hash", value.NewHash(nil), "{}"},
		{
			"single_entry_hash",
			value.NewHash(map[string]value.Value{"name": value.NewString("acme")}),
			`{name: "acme"}`,
		},
		{
			"hash_value_recurses",
			value.NewHash(map[string]value.Value{
				"items": value.NewArray([]value.Value{value.NewInt(1), value.NewString("x")}),
			}),
			`{items: [1, "x"]}`,
		},
		{
			"hash_non_identifier_key_is_quoted",
			value.NewHash(map[string]value.Value{"a b": value.NewInt(1)}),
			`{"a b": 1}`,
		},
		{"runtime_kind_fallback", value.NewValue(value.KindBlock, fakeBlock{}), "<block>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.val.Inspect(); got != tc.want {
				t.Fatalf("Inspect() = %q, want %q", got, tc.want)
			}
			// InspectByteLen must match the rendered length exactly so callers
			// can project an allocation before building the string.
			if got, want := tc.val.InspectByteLen(), len(tc.val.Inspect()); got != want {
				t.Fatalf("InspectByteLen() = %d, want len(Inspect()) = %d", got, want)
			}
		})
	}
}

func TestValueInspectCycleDetection(t *testing.T) {
	t.Parallel()

	t.Run("self_referential_array", func(t *testing.T) {
		t.Parallel()
		elems := make([]value.Value, 1)
		arr := value.NewArray(elems)
		elems[0] = arr
		if got := arr.Inspect(); got != "[<cycle>]" {
			t.Fatalf("Inspect() = %q, want %q", got, "[<cycle>]")
		}
	})

	t.Run("self_referential_hash", func(t *testing.T) {
		t.Parallel()
		entries := make(map[string]value.Value)
		hash := value.NewHash(entries)
		entries["self"] = hash
		if got := hash.Inspect(); got != "{self: <cycle>}" {
			t.Fatalf("Inspect() = %q, want %q", got, "{self: <cycle>}")
		}
	})

	t.Run("shared_subtree_is_not_a_cycle", func(t *testing.T) {
		t.Parallel()
		shared := value.NewArray([]value.Value{value.NewString("x")})
		outer := value.NewArray([]value.Value{shared, shared})
		if got := outer.Inspect(); got != `[["x"], ["x"]]` {
			t.Fatalf("Inspect() = %q, want %q", got, `[["x"], ["x"]]`)
		}
	})
}

func TestValueInspectRoundTripsThroughString(t *testing.T) {
	t.Parallel()

	// Inspect on a string is a double-quoted Vibescript literal: its String form
	// (the decoded text) must reproduce the original through Go's strconv.Unquote,
	// which understands the same \\, \", \n, \t escapes Vibescript does. Bytes
	// without a Vibescript escape ride through verbatim.
	inputs := []string{
		"hello",
		"a\nb",
		"a\tb",
		`say "hi"`,
		`a\b`,
		"plain",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			quoted := value.NewString(in).Inspect()
			unquoted, err := strconv.Unquote(quoted)
			if err != nil {
				t.Fatalf("strconv.Unquote(%q): %v", quoted, err)
			}
			if unquoted != in {
				t.Fatalf("round trip = %q, want %q", unquoted, in)
			}
		})
	}
}

func TestValueInspectBounded(t *testing.T) {
	t.Parallel()

	t.Run("small_value_renders_fully", func(t *testing.T) {
		t.Parallel()
		v := value.NewArray([]value.Value{value.NewInt(1), value.NewString("x"), value.NewNil()})
		got, err := v.InspectBounded(1024)
		if err != nil {
			t.Fatalf("InspectBounded() error = %v, want nil", err)
		}
		if want := `[1, "x", nil]`; got != want {
			t.Fatalf("InspectBounded() = %q, want %q", got, want)
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
			got, err := v.InspectBounded(limit)
			if err != nil {
				t.Fatalf("InspectBounded(%d) error = %v, want nil", limit, err)
			}
			if want := v.Inspect(); got != want {
				t.Fatalf("InspectBounded(%d) did not match Inspect()", limit)
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
		got, err := v.InspectBounded(limit)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("InspectBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) > limit+64 {
			t.Fatalf("partial rendering = %d bytes, want <= %d", len(got), limit+64)
		}
		if full := v.Inspect(); len(got) >= len(full) {
			t.Fatalf("partial rendering (%d bytes) was not shorter than full (%d bytes)", len(got), len(full))
		}
	})

	t.Run("oversized_scalar_trips_limit", func(t *testing.T) {
		t.Parallel()
		v := value.NewString(strings.Repeat("x", 1<<16))
		got, err := v.InspectBounded(1024)
		if !errors.Is(err, value.ErrStringRenderTruncated) {
			t.Fatalf("InspectBounded() error = %v, want ErrStringRenderTruncated", err)
		}
		if len(got) > 1024 {
			t.Fatalf("partial scalar rendering = %d bytes, want <= 1024", len(got))
		}
	})
}

func TestValueInspectByteLenBounded(t *testing.T) {
	t.Parallel()

	t.Run("matches_unbounded_count", func(t *testing.T) {
		t.Parallel()
		v := value.NewArray([]value.Value{
			value.NewString("x"),
			value.NewHash(map[string]value.Value{"a": value.NewInt(1)}),
		})
		got, err := v.InspectByteLenBounded(func() error { return nil })
		if err != nil {
			t.Fatalf("InspectByteLenBounded() error = %v, want nil", err)
		}
		if want := v.InspectByteLen(); got != want {
			t.Fatalf("InspectByteLenBounded() = %d, want %d", got, want)
		}
	})

	t.Run("step_error_aborts_walk", func(t *testing.T) {
		t.Parallel()
		sentinel := errors.New("budget exhausted")
		v := value.NewArray([]value.Value{value.NewInt(1), value.NewInt(2)})
		_, err := v.InspectByteLenBounded(func() error { return sentinel })
		if !errors.Is(err, sentinel) {
			t.Fatalf("InspectByteLenBounded() error = %v, want %v", err, sentinel)
		}
	})
}
