package vibes

import (
	"context"
	"testing"
)

func compileScript(t *testing.T, source string) *Script {
	t.Helper()
	engine := NewEngine(Config{})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}
	return script
}

func callFunc(t *testing.T, script *Script, name string, args []Value) Value {
	t.Helper()
	result, err := script.Call(context.Background(), name, args, CallOptions{})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func TestHashMergeAndKeys(t *testing.T) {
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
	arr := value.Array()
	if len(arr) != len(want) {
		t.Fatalf("length mismatch: got %d want %d", len(arr), len(want))
	}
	for i := range arr {
		if !arr[i].Equal(want[i]) {
			t.Fatalf("element %d mismatch: got %v want %v", i, arr[i], want[i])
		}
	}
}

func TestArrayPushPopAndSum(t *testing.T) {
	script := compileScript(t, `
    def push_and_pop(values, extra)
      pushed = values.push(extra)
      result = pushed.pop()
      result
    end

    def uniq_sum(values)
      values.uniq().sum()
    end
    `)

	base := NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)})
	result := callFunc(t, script, "push_and_pop", []Value{base, NewInt(4)})
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", result.Kind())
	}
	resHash := result.Hash()
	compareArrays(t, resHash["array"], []Value{NewInt(1), NewInt(2), NewInt(3)})
	if popped := resHash["popped"]; !popped.Equal(NewInt(4)) {
		t.Fatalf("popped mismatch: %v", popped)
	}

	uniq := callFunc(t, script, "uniq_sum", []Value{NewArray([]Value{NewInt(1), NewInt(1), NewInt(3)})})
	if !uniq.Equal(NewInt(4)) {
		t.Fatalf("uniq sum mismatch: got %v", uniq)
	}
}

func TestArrayConcatAndSubtract(t *testing.T) {
	script := compileScript(t, `
    def concat(first, second)
      first + second
    end

    def subtract(first, second)
      first - second
    end
    `)

	first := NewArray([]Value{NewInt(1), NewInt(2)})
	second := NewArray([]Value{NewInt(3), NewInt(2)})

	concatenated := callFunc(t, script, "concat", []Value{first, second})
	compareArrays(t, concatenated, []Value{NewInt(1), NewInt(2), NewInt(3), NewInt(2)})

	subtracted := callFunc(t, script, "subtract", []Value{first, second})
	compareArrays(t, subtracted, []Value{NewInt(1)})
}

func TestHashLiteralSyntaxRestriction(t *testing.T) {
	engine := NewEngine(Config{})
	_, err := engine.Compile(`
    def broken()
      { "name" => "alex" }
    end
    `)
	if err == nil {
		t.Fatalf("expected compile error for legacy hash syntax")
	}
}

func TestArraySumRejectsNonNumeric(t *testing.T) {
	engine := NewEngine(Config{})
	script, err := engine.Compile(`
    def bad()
      ["a"].sum()
    end
    `)
	if err != nil {
		t.Fatalf("compile error: %v", err)
	}

	_, err = script.Call(context.Background(), "bad", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error for non-numeric sum")
	}
}
