package runtime

import "testing"

func TestParenlessSingleArgumentCalls(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def id(x)
      x * 10
    end

    def call_id()
      id 2
    end

    def assign_id()
      value = id 3
      value
    end

    def return_id()
      return id 4
    end

    def push_arg()
      [1].push 2
    end
    `)

	if got := callFunc(t, script, "call_id", nil); !got.Equal(NewInt(20)) {
		t.Fatalf("call_id mismatch: %v", got)
	}
	if got := callFunc(t, script, "assign_id", nil); !got.Equal(NewInt(30)) {
		t.Fatalf("assign_id mismatch: %v", got)
	}
	if got := callFunc(t, script, "return_id", nil); !got.Equal(NewInt(40)) {
		t.Fatalf("return_id mismatch: %v", got)
	}
	compareArrays(t, callFunc(t, script, "push_arg", nil), []Value{NewInt(1), NewInt(2)})
}
