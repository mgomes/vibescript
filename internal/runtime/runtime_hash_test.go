package runtime

import (
	"context"
	"testing"
)

func compileScript(t *testing.T, source string) *Script {
	t.Helper()
	return compileScriptDefault(t, source)
}

func callFunc(t *testing.T, script *Script, name string, args []Value) Value {
	t.Helper()
	result, err := script.Call(context.Background(), name, args, CallOptions{})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func mustMoneyValue(t *testing.T, literal string) Value {
	t.Helper()
	money, err := parseMoneyLiteral(literal)
	if err != nil {
		t.Fatalf("parse money: %v", err)
	}
	return NewMoney(money)
}

func compareArrays(t *testing.T, value Value, want []Value) {
	t.Helper()
	if value.Kind() != KindArray {
		t.Fatalf("expected array, got %v", value.Kind())
	}
	if diff := valuesDiff(want, value.Array()); diff != "" {
		t.Fatalf("array mismatch (-want +got):\n%s", diff)
	}
}

func compareHash(t *testing.T, got, want map[string]Value) {
	t.Helper()
	if diff := valueMapDiff(want, got); diff != "" {
		t.Fatalf("hash mismatch (-want +got):\n%s", diff)
	}
}

func TestHashMergeAndKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def merged()
      base = { name: "Alex", raised: money("10.00 USD") }
      override = { raised: money("25.00 USD") }
      base.merge(override)
    end

    def sorted_keys()
      record = { b: 2, a: 1 }
      record.keys
    end
    `)

	merged := callFunc(t, script, "merged", nil)
	if merged.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", merged.Kind())
	}
	result := merged.Hash()
	if got, want := result["name"], NewString("Alex"); !got.Equal(want) {
		t.Fatalf("name mismatch: got %v want %v", got, want)
	}
	if got, want := result["raised"], mustMoneyValue(t, "25.00 USD"); !got.Equal(want) {
		t.Fatalf("raised mismatch: got %v want %v", got, want)
	}

	keys := callFunc(t, script, "sorted_keys", nil)
	wantKeys := []Value{NewSymbol("a"), NewSymbol("b")}
	compareArrays(t, keys, wantKeys)
}

func TestQuotedHashLiteralKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def payload()
      {"name": "Ada", "first-name": "Lovelace", active: true}
    end

    def lookups()
      row = {"name": "Ada", "first-name": "Lovelace"}
      {
        symbol_name: row[:name],
        string_name: row["name"],
        hyphenated: row["first-name"]
      }
    end

    def collision()
      {name: "symbol", "name": "string"}
    end
    `)

	payload := callFunc(t, script, "payload", nil)
	if payload.Kind() != KindHash {
		t.Fatalf("payload() = %s, want hash", payload.Kind())
	}
	compareHash(t, payload.Hash(), map[string]Value{
		"name":       NewString("Ada"),
		"first-name": NewString("Lovelace"),
		"active":     NewBool(true),
	})

	lookups := callFunc(t, script, "lookups", nil)
	if lookups.Kind() != KindHash {
		t.Fatalf("lookups() = %s, want hash", lookups.Kind())
	}
	compareHash(t, lookups.Hash(), map[string]Value{
		"symbol_name": NewString("Ada"),
		"string_name": NewString("Ada"),
		"hyphenated":  NewString("Lovelace"),
	})

	collision := callFunc(t, script, "collision", nil)
	if collision.Kind() != KindHash {
		t.Fatalf("collision() = %s, want hash", collision.Kind())
	}
	compareHash(t, collision.Hash(), map[string]Value{"name": NewString("string")})
}

func TestMemberAccessAllowsKeywordNamedHashKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def keyword_members()
      payload = JSON.parse("{\"raise\":1,\"begin\":2,\"rescue\":3,\"ensure\":4,\"export\":5}")
      payload.raise + payload.begin + payload.rescue + payload.ensure + payload.export
    end
    `)

	result := callFunc(t, script, "keyword_members", nil)
	if !result.Equal(NewInt(15)) {
		t.Fatalf("keyword_members = %v, want 15", result)
	}
}

func TestHashMethodNamesWinOverKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def hash_collisions()
      record = { size: "XL", keys: "raw keys", fetch: "raw fetch" }
      {
        size_method: record.size,
        size_key: record[:size],
        keys_method: record.keys,
        keys_key: record[:keys],
        fetch_method: record.fetch(:fetch),
        fetch_key: record[:fetch]
      }
    end

    def object_collisions(record)
      {
        size: record.size,
        keys: record.keys
      }
    end
    `)

	collisions := callFunc(t, script, "hash_collisions", nil).Hash()
	if !collisions["size_method"].Equal(NewInt(3)) {
		t.Fatalf("size_method = %v, want 3", collisions["size_method"])
	}
	if !collisions["size_key"].Equal(NewString("XL")) {
		t.Fatalf("size_key = %v, want XL", collisions["size_key"])
	}
	compareArrays(t, collisions["keys_method"], []Value{NewSymbol("fetch"), NewSymbol("keys"), NewSymbol("size")})
	if !collisions["keys_key"].Equal(NewString("raw keys")) {
		t.Fatalf("keys_key = %v, want raw keys", collisions["keys_key"])
	}
	if !collisions["fetch_method"].Equal(NewString("raw fetch")) {
		t.Fatalf("fetch_method = %v, want raw fetch", collisions["fetch_method"])
	}
	if !collisions["fetch_key"].Equal(NewString("raw fetch")) {
		t.Fatalf("fetch_key = %v, want raw fetch", collisions["fetch_key"])
	}

	object := NewObject(map[string]Value{
		"size": NewString("object size"),
		"keys": NewString("object keys"),
	})
	objectCollisions := callFunc(t, script, "object_collisions", []Value{object}).Hash()
	if !objectCollisions["size"].Equal(NewString("object size")) {
		t.Fatalf("object size = %v, want object size", objectCollisions["size"])
	}
	if !objectCollisions["keys"].Equal(NewString("object keys")) {
		t.Fatalf("object keys = %v, want object keys", objectCollisions["keys"])
	}
}

func TestHashExpandedHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      record = { b: 2, a: 1, c: 3 }
      with_nil = { a: 1, b: nil, c: 3 }
      nested = { user: { profile: { name: "Alex" } } }

      each_pairs = []
      record.each do |k, v|
        each_pairs = each_pairs.push(k + "=" + v)
      end

      each_keys = []
      record.each_key do |k|
        each_keys = each_keys.push(k)
      end

	      each_values = []
	      record.each_value do |v|
	        each_values = each_values.push(v)
	      end

	      select_gt1 = record.select do |k, v|
	        v > 1
	      end

	      reject_even = record.reject do |k, v|
	        v % 2 == 0
	      end

	      transform_keys = record.transform_keys do |k|
	        "x_" + k
	      end

	      transform_values = record.transform_values do |v|
	        v * 10
	      end

	      collision = { b: 2, a: 1 }.transform_keys do |k|
	        "same"
	      end

	      {
	        size: record.size,
	        length: record.length,
        empty_false: record.empty?,
        empty_true: {}.empty?,
        key_symbol: record.key?(:a),
        key_string: record.has_key?("b"),
        include_symbol: record.include?(:c),
        missing_key: record.key?(:missing),
        keys: record.keys,
        values: record.values,
        fetch_hit: record.fetch(:a),
        fetch_default: record.fetch(:missing, 99),
        fetch_nil: record.fetch(:missing),
        dig_hit: nested.dig(:user, :profile, :name),
        dig_miss: nested.dig(:user, :profile, :missing),
        dig_through_scalar: nested.dig(:user, :profile, :name, :length),
        slice: record.slice(:a, "c"),
	        except: record.except(:b),
	        each_pairs: each_pairs,
	        each_keys: each_keys,
	        each_values: each_values,
	        select_gt1: select_gt1,
	        reject_even: reject_even,
	        transform_keys: transform_keys,
	        transform_values: transform_values,
	        compact: with_nil.compact(),
	        collision: collision
	      }
	    end
	    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["size"].Equal(NewInt(3)) {
		t.Fatalf("size mismatch: %v", got["size"])
	}
	if !got["length"].Equal(NewInt(3)) {
		t.Fatalf("length mismatch: %v", got["length"])
	}
	if got["empty_false"].Bool() {
		t.Fatalf("expected empty_false to be false")
	}
	if !got["empty_true"].Bool() {
		t.Fatalf("expected empty_true to be true")
	}
	if !got["key_symbol"].Bool() || !got["key_string"].Bool() || !got["include_symbol"].Bool() {
		t.Fatalf("key/include mismatch: %#v", got)
	}
	if got["missing_key"].Bool() {
		t.Fatalf("missing_key should be false")
	}
	compareArrays(t, got["keys"], []Value{NewSymbol("a"), NewSymbol("b"), NewSymbol("c")})
	compareArrays(t, got["values"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	if !got["fetch_hit"].Equal(NewInt(1)) {
		t.Fatalf("fetch_hit mismatch: %v", got["fetch_hit"])
	}
	if !got["fetch_default"].Equal(NewInt(99)) {
		t.Fatalf("fetch_default mismatch: %v", got["fetch_default"])
	}
	if got["fetch_nil"].Kind() != KindNil {
		t.Fatalf("fetch_nil expected nil, got %v", got["fetch_nil"])
	}
	if got["dig_hit"].Kind() != KindString || got["dig_hit"].String() != "Alex" {
		t.Fatalf("dig_hit mismatch: %v", got["dig_hit"])
	}
	if got["dig_miss"].Kind() != KindNil {
		t.Fatalf("dig_miss expected nil, got %v", got["dig_miss"])
	}
	if got["dig_through_scalar"].Kind() != KindNil {
		t.Fatalf("dig_through_scalar expected nil, got %v", got["dig_through_scalar"])
	}

	slice := got["slice"]
	if slice.Kind() != KindHash {
		t.Fatalf("slice expected hash, got %v", slice.Kind())
	}
	compareHash(t, slice.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	except := got["except"]
	if except.Kind() != KindHash {
		t.Fatalf("except expected hash, got %v", except.Kind())
	}
	compareHash(t, except.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	compareArrays(t, got["each_pairs"], []Value{NewString("a=1"), NewString("b=2"), NewString("c=3")})
	compareArrays(t, got["each_keys"], []Value{NewSymbol("a"), NewSymbol("b"), NewSymbol("c")})
	compareArrays(t, got["each_values"], []Value{NewInt(1), NewInt(2), NewInt(3)})

	selectGT1 := got["select_gt1"]
	if selectGT1.Kind() != KindHash {
		t.Fatalf("select_gt1 expected hash, got %v", selectGT1.Kind())
	}
	compareHash(t, selectGT1.Hash(), map[string]Value{"b": NewInt(2), "c": NewInt(3)})

	rejectEven := got["reject_even"]
	if rejectEven.Kind() != KindHash {
		t.Fatalf("reject_even expected hash, got %v", rejectEven.Kind())
	}
	compareHash(t, rejectEven.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	transformedKeys := got["transform_keys"]
	if transformedKeys.Kind() != KindHash {
		t.Fatalf("transform_keys expected hash, got %v", transformedKeys.Kind())
	}
	compareHash(t, transformedKeys.Hash(), map[string]Value{"x_a": NewInt(1), "x_b": NewInt(2), "x_c": NewInt(3)})

	transformedValues := got["transform_values"]
	if transformedValues.Kind() != KindHash {
		t.Fatalf("transform_values expected hash, got %v", transformedValues.Kind())
	}
	compareHash(t, transformedValues.Hash(), map[string]Value{"a": NewInt(10), "b": NewInt(20), "c": NewInt(30)})

	compacted := got["compact"]
	if compacted.Kind() != KindHash {
		t.Fatalf("compact expected hash, got %v", compacted.Kind())
	}
	compareHash(t, compacted.Hash(), map[string]Value{"a": NewInt(1), "c": NewInt(3)})

	collision := got["collision"]
	if collision.Kind() != KindHash {
		t.Fatalf("collision expected hash, got %v", collision.Kind())
	}
	compareHash(t, collision.Hash(), map[string]Value{"same": NewInt(2)})
}

func TestHashHelpersSupportObjectReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def lookup(record)
	      {
	        fetch: record.fetch(:name),
	        has_key: record.key?(:name),
	        dig: record.dig(:meta, :title)
	      }
	    end
	    `)

	arg := NewObject(map[string]Value{
		"name": NewString("Alex"),
		"meta": NewHash(map[string]Value{
			"title": NewString("Captain"),
		}),
	})
	result := callFunc(t, script, "lookup", []Value{arg})
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["fetch"].Equal(NewString("Alex")) {
		t.Fatalf("fetch mismatch: %v", got["fetch"])
	}
	if got["has_key"].Kind() != KindBool || !got["has_key"].Bool() {
		t.Fatalf("has_key mismatch: %v", got["has_key"])
	}
	if !got["dig"].Equal(NewString("Captain")) {
		t.Fatalf("dig mismatch: %v", got["dig"])
	}
}

func TestHashRocketLiteralKeys(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def literal_keys()
      { :name => "Ada", "first-name" => "Lovelace" }
    end

    def dynamic_keys(symbol_key, string_key)
      { symbol_key => "symbol", string_key => "string" }
    end

    def unsupported_key()
      { ["name"] => "Ada" }
    end
    `)

	literalKeys := callFunc(t, script, "literal_keys", nil)
	if literalKeys.Kind() != KindHash {
		t.Fatalf("literal_keys() = %s, want hash", literalKeys.Kind())
	}
	compareHash(t, literalKeys.Hash(), map[string]Value{
		"name":       NewString("Ada"),
		"first-name": NewString("Lovelace"),
	})

	dynamicKeys := callFunc(t, script, "dynamic_keys", []Value{NewSymbol("status"), NewString("label")})
	if dynamicKeys.Kind() != KindHash {
		t.Fatalf("dynamic_keys() = %s, want hash", dynamicKeys.Kind())
	}
	compareHash(t, dynamicKeys.Hash(), map[string]Value{
		"status": NewString("symbol"),
		"label":  NewString("string"),
	})

	requireCallErrorContains(t, script, "unsupported_key", nil, CallOptions{}, "unsupported hash key type array")
}

func TestReservedWordLabelsInHashesAndCallKwargs(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def hash_payload(cursor)
      { next: cursor, break: cursor + 1 }
    end

    def call_payload(cursor)
      list(next: cursor, break: cursor + 1)
    end
    `)

	payload := callFunc(t, script, "hash_payload", []Value{NewInt(7)})
	if payload.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", payload.Kind())
	}
	compareHash(t, payload.Hash(), map[string]Value{
		"next":  NewInt(7),
		"break": NewInt(8),
	})
}
