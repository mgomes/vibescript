package runtime

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestTypedBlockSignatures(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def increment_all(values)
      values.map do |n: int|
        n + 1
      end
    end

    def typed_union(values)
      values.map do |v: int | string|
        v
      end
    end

    def call_with_block(value)
      yield value
    end

    def enforce_yield_type(value)
      call_with_block(value) do |n: int|
        n
      end
    end

    def passthrough(values)
      values.map do |v|
        v
      end
    end
    `)

	inc := callFunc(t, script, "increment_all", []Value{
		NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
	})
	compareArrays(t, inc, []Value{NewInt(2), NewInt(3), NewInt(4)})

	unionResult := callFunc(t, script, "typed_union", []Value{
		NewArray([]Value{NewInt(7), NewString("ok")}),
	})
	compareArrays(t, unionResult, []Value{NewInt(7), NewString("ok")})

	if got := callFunc(t, script, "enforce_yield_type", []Value{NewInt(9)}); !got.Equal(NewInt(9)) {
		t.Fatalf("typed yield block mismatch: got %v", got)
	}

	untouched := callFunc(t, script, "passthrough", []Value{
		NewArray([]Value{NewInt(1), NewString("two")}),
	})
	compareArrays(t, untouched, []Value{NewInt(1), NewString("two")})

	errorCases := []struct {
		name string
		fn   string
		args []Value
		want string
	}{
		{
			name: "increment_all rejects string element",
			fn:   "increment_all",
			args: []Value{NewArray([]Value{NewInt(1), NewString("oops")})},
			want: "argument n expected int, got string",
		},
		{
			name: "typed_union rejects bool element",
			fn:   "typed_union",
			args: []Value{NewArray([]Value{NewBool(true)})},
			want: "argument v expected int | string, got bool",
		},
		{
			name: "enforce_yield_type rejects string yield",
			fn:   "enforce_yield_type",
			args: []Value{NewString("bad")},
			want: "argument n expected int, got string",
		},
	}
	for _, tc := range errorCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, tc.args, CallOptions{}, tc.want)
		})
	}
}

func TestReadmeLeaderboardExample(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def leaderboard(players: array, since: time? = nil, limit: int = 5) -> array
      cutoff = since
      if cutoff == nil
        cutoff = 7.days.ago(Time.now)
      end
      recent = players.select do |p|
        Time.parse(p[:last_seen]) >= cutoff
      end

      ranked = recent.map do |p|
        {
          name: p[:name],
          score: p[:score],
          last_seen: Time.parse(p[:last_seen])
        }
      end

      sorted = ranked.sort do |a, b|
        b[:score] - a[:score]
      end

      top = sorted.first(limit)

      top.map do |entry|
        {
          name: entry[:name],
          score: entry[:score],
          last_seen: entry[:last_seen].format("2006-01-02 15:04:05")
        }
      end
    end
    `)

	players := NewArray([]Value{
		NewHash(map[string]Value{
			"name":      NewString("alex"),
			"score":     NewInt(10),
			"last_seen": NewString("2024-01-10T10:00:00Z"),
		}),
		NewHash(map[string]Value{
			"name":      NewString("cam"),
			"score":     NewInt(15),
			"last_seen": NewString("2024-01-09T11:00:00Z"),
		}),
		NewHash(map[string]Value{
			"name":      NewString("old"),
			"score":     NewInt(99),
			"last_seen": NewString("2023-12-25T00:00:00Z"),
		}),
	})
	since := NewTime(time.Date(2024, time.January, 8, 0, 0, 0, 0, time.UTC))

	result := callFunc(t, script, "leaderboard", []Value{players, since, NewInt(2)})
	if result.Kind() != KindArray {
		t.Fatalf("expected array, got %v", result.Kind())
	}
	arr := result.Array()
	if len(arr) != 2 {
		t.Fatalf("expected 2 leaderboard rows, got %d", len(arr))
	}

	first := arr[0].Hash()
	if first["name"].String() != "cam" || first["score"].Int() != 15 || first["last_seen"].String() != "2024-01-09 11:00:00" {
		t.Fatalf("unexpected first row: %#v", first)
	}
	second := arr[1].Hash()
	if second["name"].String() != "alex" || second["score"].Int() != 10 || second["last_seen"].String() != "2024-01-10 10:00:00" {
		t.Fatalf("unexpected second row: %#v", second)
	}

	synctest.Test(t, func(t *testing.T) {
		players := NewArray([]Value{
			NewHash(map[string]Value{
				"name":      NewString("near"),
				"score":     NewInt(10),
				"last_seen": NewString("1999-12-31T10:00:00Z"),
			}),
			NewHash(map[string]Value{
				"name":      NewString("top"),
				"score":     NewInt(20),
				"last_seen": NewString("1999-12-30T10:00:00Z"),
			}),
			NewHash(map[string]Value{
				"name":      NewString("old"),
				"score":     NewInt(99),
				"last_seen": NewString("1999-12-24T23:59:59Z"),
			}),
		})

		result := callFunc(t, script, "leaderboard", []Value{players})
		if result.Kind() != KindArray {
			t.Fatalf("leaderboard(default since) = %v, want array", result.Kind())
		}
		arr := result.Array()
		if len(arr) != 2 {
			t.Fatalf("leaderboard(default since) returned %d rows, want 2", len(arr))
		}

		first := arr[0].Hash()
		if first["name"].String() != "top" || first["score"].Int() != 20 || first["last_seen"].String() != "1999-12-30 10:00:00" {
			t.Fatalf("leaderboard(default since) first row = %#v", first)
		}
		second := arr[1].Hash()
		if second["name"].String() != "near" || second["score"].Int() != 10 || second["last_seen"].String() != "1999-12-31 10:00:00" {
			t.Fatalf("leaderboard(default since) second row = %#v", second)
		}
	})
}

func TestTypedFunctions(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def pick_second(n: int, m: int) -> int
      m
    end

    def pick_maybe(n: int, m: int = 0) -> int
      m
    end

    def nil_result() -> nil
      nil
    end

    def kw_only(n: int, m: int)
      m
    end

    def mixed(n: int, m: int) -> int
      n + m
    end

    def bad_return(n: int) -> int
      "oops"
    end

    def pick_optional(s: string? = nil) -> string?
      s
    end

    def union_echo(v: int | string) -> int | string
      v
    end

    def union_optional(v: int | nil = nil) -> int | nil
      v
    end

    def union_bad_return() -> int | string
      true
    end

    def ints_only(values: array<int>) -> array<int>
      values
    end

    def totals_by_player(totals: hash<string, int>) -> hash<string, int>
      totals
    end

    def mixed_items(values: array<int | string>) -> array<int | string>
      values
    end

    def range_echo(span: range) -> range
      span
    end

    def player_payload(payload: { id: string, score: int, active: bool? }) -> { id: string, score: int, active: bool? }
      payload
    end

    def shaped_rows(rows: array<{ id: string, stats: { wins: int } }>) -> array<{ id: string, stats: { wins: int } }>
      rows
    end
    `)

	if fn, ok := script.Function("bad_return"); !ok || fn.ReturnTy == nil {
		t.Fatalf("expected bad_return to have return type")
	} else if fn.ReturnTy.Name != "int" {
		t.Fatalf("unexpected return type name: %s", fn.ReturnTy.Name)
	}

	successCases := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{name: "pick_second_basic", fn: "pick_second", args: []Value{NewInt(1), NewInt(2)}, want: NewInt(2)},
		{name: "pick_maybe_default", fn: "pick_maybe", args: []Value{NewInt(1)}, want: NewInt(0)},
		{name: "pick_optional_nil_default", fn: "pick_optional", args: nil, want: NewNil()},
		{name: "union_echo_int", fn: "union_echo", args: []Value{NewInt(7)}, want: NewInt(7)},
		{name: "union_echo_string", fn: "union_echo", args: []Value{NewString("ok")}, want: NewString("ok")},
		{name: "union_optional_nil", fn: "union_optional", args: nil, want: NewNil()},
		{name: "union_optional_int", fn: "union_optional", args: []Value{NewInt(9)}, want: NewInt(9)},
		{name: "nil_result", fn: "nil_result", args: nil, want: NewNil()},
		{name: "kw_only_positional", fn: "kw_only", args: []Value{NewInt(1), NewInt(2)}, want: NewInt(2)},
		{name: "mixed_sum", fn: "mixed", args: []Value{NewInt(1), NewInt(2)}, want: NewInt(3)},
		{name: "range_echo", fn: "range_echo", args: []Value{NewRange(Range{Start: 1, End: 3})}, want: NewRange(Range{Start: 1, End: 3})},
	}
	for _, tc := range successCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, tc.args)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.fn, got, tc.want)
			}
		})
	}

	containerCases := []struct {
		name string
		fn   string
		args []Value
		kind ValueKind
	}{
		{name: "ints_only_returns_array", fn: "ints_only", args: []Value{NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})}, kind: KindArray},
		{name: "totals_by_player_returns_hash", fn: "totals_by_player", args: []Value{NewHash(map[string]Value{"alice": NewInt(10), "bob": NewInt(12)})}, kind: KindHash},
		{name: "mixed_items_returns_array", fn: "mixed_items", args: []Value{NewArray([]Value{NewInt(1), NewString("two"), NewInt(3)})}, kind: KindArray},
		{name: "player_payload_returns_hash", fn: "player_payload", args: []Value{NewHash(map[string]Value{"id": NewString("p-1"), "score": NewInt(42), "active": NewNil()})}, kind: KindHash},
		{
			name: "shaped_rows_returns_array",
			fn:   "shaped_rows",
			args: []Value{NewArray([]Value{NewHash(map[string]Value{"id": NewString("p-1"), "stats": NewHash(map[string]Value{"wins": NewInt(7)})})})},
			kind: KindArray,
		},
	}
	for _, tc := range containerCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, tc.args)
			if got.Kind() != tc.kind {
				t.Fatalf("%s expected %v result, got %v", tc.fn, tc.kind, got.Kind())
			}
		})
	}

	errorCases := []struct {
		name string
		fn   string
		args []Value
		opts CallOptions
		want string
	}{
		{name: "kw_only_missing_arg", fn: "kw_only", args: []Value{NewInt(1)}, opts: CallOptions{Globals: map[string]Value{}}, want: "missing argument m"},
		{name: "pick_second_string_arg", fn: "pick_second", args: []Value{NewString("bad"), NewInt(2)}, want: "argument n expected int, got string"},
		{name: "bad_return_type_mismatch", fn: "bad_return", args: []Value{NewInt(1)}, want: "return value for bad_return expected int, got string"},
		{name: "union_echo_bool_rejected", fn: "union_echo", args: []Value{NewBool(true)}, want: "argument v expected int | string, got bool"},
		{name: "union_bad_return", fn: "union_bad_return", args: nil, want: "return value for union_bad_return expected int | string, got bool"},
		{name: "range_echo_rejects_int", fn: "range_echo", args: []Value{NewInt(1)}, want: "argument span expected range, got int"},
		{
			name: "ints_only_with_string",
			fn:   "ints_only",
			args: []Value{NewArray([]Value{NewInt(1), NewString("oops")})},
			want: "argument values expected array<int>, got array<int | string>",
		},
		{
			name: "totals_by_player_with_string_value",
			fn:   "totals_by_player",
			args: []Value{NewHash(map[string]Value{"alice": NewString("oops")})},
			want: "argument totals expected hash<string, int>, got { alice: string }",
		},
		{
			name: "mixed_items_with_bool",
			fn:   "mixed_items",
			args: []Value{NewArray([]Value{NewBool(true)})},
			want: "argument values expected array<int | string>, got array<bool>",
		},
		{
			name: "player_payload_with_extra_field",
			fn:   "player_payload",
			args: []Value{NewHash(map[string]Value{"id": NewString("p-1"), "score": NewInt(42), "role": NewString("captain")})},
			want: "argument payload expected { active: bool?, id: string, score: int }, got { id: string, role: string, score: int }",
		},
		{
			name: "player_payload_score_wrong_type",
			fn:   "player_payload",
			args: []Value{NewHash(map[string]Value{"id": NewString("p-1"), "score": NewString("wrong"), "active": NewBool(true)})},
			want: "argument payload expected { active: bool?, id: string, score: int }, got { active: bool, id: string, score: string }",
		},
		{
			name: "shaped_rows_with_bad_wins",
			fn:   "shaped_rows",
			args: []Value{NewArray([]Value{NewHash(map[string]Value{"id": NewString("p-1"), "stats": NewHash(map[string]Value{"wins": NewString("bad")})})})},
			want: "argument rows expected array<{ id: string, stats: { wins: int } }>, got array<{ id: string, stats: { wins: string } }>",
		},
	}
	for _, tc := range errorCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, tc.args, tc.opts, tc.want)
		})
	}
}

func TestTypeSemanticsContainersNullabilityCoercionAndKeywordStrictness(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def accepts_numbers(values: array<number>) -> array<number>
      values
    end

    def accepts_ints(values: array<int>) -> array<int>
      values
    end

    def nullable_short(v: string?) -> string?
      v
    end

    def nullable_union(v: string | nil) -> string | nil
      v
    end

    def takes_int(v: int) -> int
      v
    end

    def typed_kw(a: int) -> int
      a
    end

    def untyped_kw(a)
      a
    end
    `)

	got := callFunc(t, script, "accepts_numbers", []Value{
		NewArray([]Value{NewInt(1), NewFloat(2.5)}),
	})
	if got.Kind() != KindArray {
		t.Fatalf("accepts_numbers mixed numeric mismatch: %v", got)
	}
	compareArrays(t, got, []Value{NewInt(1), NewFloat(2.5)})

	got = callFunc(t, script, "accepts_numbers", []Value{
		NewArray([]Value{NewInt(1), NewInt(2)}),
	})
	if got.Kind() != KindArray {
		t.Fatalf("accepts_numbers int-only mismatch: %v", got)
	}
	compareArrays(t, got, []Value{NewInt(1), NewInt(2)})

	got = callFunc(t, script, "accepts_ints", []Value{
		NewArray([]Value{NewInt(1), NewInt(2)}),
	})
	if got.Kind() != KindArray {
		t.Fatalf("accepts_ints int-only mismatch: %v", got)
	}
	compareArrays(t, got, []Value{NewInt(1), NewInt(2)})
	requireCallErrorContains(t, script, "accepts_ints", []Value{
		NewArray([]Value{NewInt(1), NewFloat(2.5)}),
	}, CallOptions{}, "argument values expected array<int>, got array<float | int>")

	if got := callFunc(t, script, "nullable_short", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("nullable_short nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "nullable_union", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("nullable_union nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "nullable_short", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("nullable_short string mismatch: %#v", got)
	}
	if got := callFunc(t, script, "nullable_union", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("nullable_union string mismatch: %#v", got)
	}
	requireCallErrorContains(t, script, "nullable_short", []Value{NewInt(1)}, CallOptions{}, "argument v expected string?, got int")
	requireCallErrorContains(t, script, "nullable_union", []Value{NewInt(1)}, CallOptions{}, "argument v expected string | nil, got int")
	requireCallErrorContains(t, script, "takes_int", []Value{NewString("1")}, CallOptions{}, "argument v expected int, got string")

	extraKw := map[string]Value{
		"a":     NewInt(1),
		"extra": NewInt(2),
	}
	requireCallErrorContains(t, script, "typed_kw", nil, CallOptions{Keywords: extraKw}, "unexpected keyword argument extra")
	requireCallErrorContains(t, script, "untyped_kw", nil, CallOptions{Keywords: extraKw}, "unexpected keyword argument extra")
}

func TestObjectTypeAliasPreservesDiagnosticSpelling(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def object_passthrough(payload: object) -> object
      payload
    end

    def object_scores(payload: object<string, int>) -> object<string, int>
      payload
    end
    `)

	requireCallErrorContains(t, script, "object_passthrough", []Value{NewInt(1)}, CallOptions{}, "argument payload expected object, got int")
	requireCallErrorContains(t, script, "object_scores", []Value{NewHash(map[string]Value{"score": NewString("high")})}, CallOptions{}, "argument payload expected object<string, int>, got { score: string }")
}

func TestFormatValueTypeExprBoundsCompositeSamples(t *testing.T) {
	t.Parallel()

	ints := make([]Value, maxValueTypeFormatArraySamples+4)
	for i := range ints {
		ints[i] = NewInt(int64(i))
	}
	if got, want := formatValueTypeExpr(NewArray(ints)), "array<int | ...>"; got != want {
		t.Fatalf("large array type = %q, want %q", got, want)
	}

	rows := make([]Value, maxValueTypeFormatArraySamples+4)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{"id": NewInt(int64(i))})
	}
	if got, want := formatValueTypeExpr(NewArray(rows)), "array<{ id: int } | ...>"; got != want {
		t.Fatalf("large row array type = %q, want %q", got, want)
	}

	fields := make(map[string]Value, maxValueTypeFormatHashSamples+4)
	for i := range maxValueTypeFormatHashSamples + 4 {
		fields[fmt.Sprintf("key_%02d", i)] = NewInt(int64(i))
	}
	if got, want := formatValueTypeExpr(NewHash(fields)), "hash<string, int | ...>"; got != want {
		t.Fatalf("large hash type = %q, want %q", got, want)
	}

	mixedFields := make(map[string]Value, maxValueTypeFormatHashSamples+4)
	mixedFields["key_00"] = NewString("first")
	for i := 1; i < maxValueTypeFormatHashSamples+4; i++ {
		mixedFields[fmt.Sprintf("key_%02d", i)] = NewInt(int64(i))
	}
	if got, want := formatValueTypeExpr(NewHash(mixedFields)), "hash<string, int | string | ...>"; got != want {
		t.Fatalf("large mixed hash type = %q, want %q", got, want)
	}
}

func TestBoundedSortedHashFieldsKeepsSmallestKeys(t *testing.T) {
	t.Parallel()

	fields := map[string]Value{
		"z": NewInt(1),
		"b": NewInt(2),
		"y": NewInt(3),
		"a": NewInt(4),
		"c": NewInt(5),
	}

	got := boundedSortedHashFields(fields, 3)
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("boundedSortedHashFields() = %v, want %v", got, want)
	}
}

func TestTypeMismatchFormattingBoundsLargeCompositeValues(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def ints(values: array<int>)
      values
    end
    `)

	rows := make([]Value, 10_000)
	for i := range rows {
		rows[i] = NewHash(map[string]Value{
			"id":    NewString("row"),
			"score": NewInt(int64(i)),
		})
	}
	requireCallErrorContains(t, script, "ints", []Value{NewArray(rows)}, CallOptions{}, "argument values expected array<int>, got array<{ id: string, score: int } | ...>")
}

func TestTypedFunctionsRegressionAnyAndNullableBehavior(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def takes_any(v: any) -> any
      v
    end

    def takes_nullable(v: string? = nil) -> string?
      v
    end

    def takes_nullable_union(v: string | nil) -> string | nil
      v
    end
    `)

	anyBuiltin := NewBuiltin("tmp.any", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewNil(), nil
	})
	if got := callFunc(t, script, "takes_any", []Value{anyBuiltin}); got.Kind() != KindBuiltin {
		t.Fatalf("takes_any builtin mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_any", []Value{NewHash(map[string]Value{"x": NewInt(1)})}); got.Kind() != KindHash {
		t.Fatalf("takes_any hash mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_any", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("takes_any nil mismatch: %#v", got)
	}

	if got := callFunc(t, script, "takes_nullable", nil); got.Kind() != KindNil {
		t.Fatalf("takes_nullable default nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_nullable", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("takes_nullable string mismatch: %#v", got)
	}
	requireCallErrorContains(t, script, "takes_nullable", []Value{NewInt(1)}, CallOptions{}, "argument v expected string?, got int")

	if got := callFunc(t, script, "takes_nullable_union", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("takes_nullable_union nil mismatch: %#v", got)
	}
	if got := callFunc(t, script, "takes_nullable_union", []Value{NewString("ok")}); got.Kind() != KindString || got.String() != "ok" {
		t.Fatalf("takes_nullable_union string mismatch: %#v", got)
	}
	requireCallErrorContains(t, script, "takes_nullable_union", []Value{NewInt(1)}, CallOptions{}, "argument v expected string | nil, got int")
}

func TestFunctionTypeAnnotationAcceptsCallableValues(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def takes_callable(fn: function)
  fn
end

def inc(n)
  n + 1
end

def script_function_ok
  takes_callable(inc)(2)
end

def builtin_ok
  takes_callable(assert)(true)
end

def block_rejected(&block)
  takes_callable(block)
end

def call_block_rejected
  block_rejected do |n|
    n * 2
  end
end

def block_annotation_rejected(&block: function)
  block
end

def call_block_annotation_rejected
  block_annotation_rejected do |n|
    n * 2
  end
end

def reject_non_callable
  takes_callable(1)
end
`)

	if got := callFunc(t, script, "script_function_ok", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("script function annotation = %v, want 3", got)
	}
	if got := callFunc(t, script, "builtin_ok", nil); got.Kind() != KindNil {
		t.Fatalf("builtin function annotation = %v, want nil", got)
	}
	requireCallErrorContains(t, script, "call_block_rejected", nil, CallOptions{}, "argument fn expected function, got block")
	requireCallErrorContains(t, script, "call_block_annotation_rejected", nil, CallOptions{}, "argument block expected function, got block")
	requireCallErrorContains(t, script, "reject_non_callable", nil, CallOptions{}, "argument fn expected function, got int")
}

func TestNullableUnknownAnnotationsMustResolveBeforeAcceptingNil(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def arg_nil(value: Typo?)
  value
end

def return_nil() -> Typo?
  nil
end
`)

	requireCallErrorContains(t, script, "arg_nil", []Value{NewNil()}, CallOptions{}, "unknown type Typo")
	requireCallErrorContains(t, script, "return_nil", nil, CallOptions{}, "unknown type Typo")
}

func TestTypedFunctionsRejectCyclicHashInputWithoutInfiniteRecursion(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run(payload: hash<string, hash<string, int>>) -> hash<string, hash<string, int>>
      payload
    end
    `)

	entries := map[string]Value{}
	payload := NewHash(entries)
	entries["self"] = payload

	done := make(chan error, 1)
	go func() {
		_, err := script.Call(context.Background(), "run", []Value{payload}, CallOptions{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected type validation error for cyclic payload")
		}
		requireErrorContains(t, err, "argument payload expected hash<string, hash<string, int>>")
	case <-time.After(2 * time.Second):
		t.Fatalf("type validation did not terminate for cyclic payload")
	}
}

// TestTypedHashValidatesDefaults pins that a Ruby-style hash default is part of
// a typed hash's value type. A missing-key lookup returns the default, so an
// int-typed hash must reject a string default (or any default proc, whose result
// the type checker cannot inspect) and must carry a conforming default through
// normalization rather than dropping it.
func TestTypedHashValidatesDefaults(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def missing_lookup(h: hash<string, int>) -> int
      h[:missing]
    end

    def passes_string_default()
      missing_lookup(Hash.new("oops"))
    end

    def passes_proc_default()
      missing_lookup(Hash.new { |hash, key| 1 })
    end

    def preserves_int_default()
      missing_lookup(Hash.new(42))
    end

	    def preserves_default_with_entries(present: int)
	      base = Hash.new(0)
	      base["present"] = present
	      missing_lookup(base)
	    end
    `)

	t.Run("rejects_string_default", func(t *testing.T) {
		t.Parallel()
		requireCallErrorContains(t, script, "passes_string_default", nil, CallOptions{}, "argument h expected hash<string, int>, got {}")
	})

	t.Run("rejects_proc_default", func(t *testing.T) {
		t.Parallel()
		requireCallErrorContains(t, script, "passes_proc_default", nil, CallOptions{}, "argument h expected hash<string, int>, got {} with a default proc")
	})

	t.Run("preserves_conforming_default", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "preserves_int_default", nil)
		if got.Kind() != KindInt || got.Int() != 42 {
			t.Fatalf("expected missing-key lookup to return preserved default 42, got %#v", got)
		}
	})

	t.Run("preserves_default_when_entries_change", func(t *testing.T) {
		t.Parallel()
		got := callFunc(t, script, "preserves_default_with_entries", []Value{NewInt(5)})
		if got.Kind() != KindInt || got.Int() != 0 {
			t.Fatalf("expected missing-key lookup to return preserved default 0, got %#v", got)
		}
	})
}

func TestExistingUntypedScriptsRemainCompatible(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def identity(v)
      v
    end

    def run()
      first = identity(1)
      second = identity("two")
      third = identity({ ok: true })
      {
        first: first,
        second: second,
        third_ok: third[:ok]
      }
    end
    `)

	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", got.Kind())
	}
	hash := got.Hash()
	if hash["first"].Kind() != KindInt || hash["first"].Int() != 1 {
		t.Fatalf("unexpected first value: %#v", hash["first"])
	}
	if hash["second"].Kind() != KindString || hash["second"].String() != "two" {
		t.Fatalf("unexpected second value: %#v", hash["second"])
	}
	if hash["third_ok"].Kind() != KindBool || !hash["third_ok"].Bool() {
		t.Fatalf("unexpected third_ok value: %#v", hash["third_ok"])
	}
}
