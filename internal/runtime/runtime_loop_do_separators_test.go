package runtime

import "testing"

func TestLoopDoSeparators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  i = 0
  while i < 2 do
    i = i + 1
  end

  j = 0
  until j >= 2 do
    j = j + 1
  end

  total = 0
  for n in [1, 2, 3] do
    total = total + n
  end

  { while_count: i, until_count: j, for_total: total }
end`)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("run() = %v, want hash", result.Kind())
	}

	compareHash(t, result.Hash(), map[string]Value{
		"while_count": NewInt(2),
		"until_count": NewInt(2),
		"for_total":   NewInt(6),
	})
}
