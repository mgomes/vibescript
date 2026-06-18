package runtime

import "testing"

func TestLoopDoSeparators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def below_two(value)
  value < 2
end

def loop_values
  [1, 2, 3]
end

def run
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

  call_while = 0
  while below_two(call_while) do
    call_while = call_while + 1
  end

  call_for = 0
  for n in loop_values() do
    call_for = call_for + n
  end

  {
    while_count: i,
    until_count: j,
    for_total: total,
    call_while: call_while,
    call_for: call_for,
  }
end`)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("run() = %v, want hash", result.Kind())
	}

	compareHash(t, result.Hash(), map[string]Value{
		"while_count": NewInt(2),
		"until_count": NewInt(2),
		"for_total":   NewInt(6),
		"call_while":  NewInt(2),
		"call_for":    NewInt(6),
	})
}
