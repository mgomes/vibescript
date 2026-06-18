package runtime

import "testing"

func TestTrailingCommasInLiteralsAndCalls(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def collect(a, b, label:)
  { array: a, hash: b, label: label }
end

def run
  collect(
    [
      1,
      2,
    ],
    {
      a: 3,
      b: 4,
    },
    label: "ok",
  )
end`)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("run() = %v, want hash", result.Kind())
	}

	compareHash(t, result.Hash(), map[string]Value{
		"array": NewArray([]Value{NewInt(1), NewInt(2)}),
		"hash":  NewHash(map[string]Value{"a": NewInt(3), "b": NewInt(4)}),
		"label": NewString("ok"),
	})
}
