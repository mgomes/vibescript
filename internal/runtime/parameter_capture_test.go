package runtime

import (
	"context"
	"testing"
)

func TestFunctionRestAndKeywordRestParameters(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def collect(prefix, *items, **opts)
      [prefix, items, opts]
    end
    `)

	got, err := script.Call(context.Background(), "collect", []Value{
		NewString("first"),
		NewInt(1),
		NewInt(2),
	}, CallOptions{
		Keywords: map[string]Value{
			"flag":  NewBool(true),
			"limit": NewInt(3),
		},
	})
	if err != nil {
		t.Fatalf("Script.Call(collect) error = %v, want nil", err)
	}
	if got.Kind() != KindArray {
		t.Fatalf("Script.Call(collect) = %v, want array", got.Kind())
	}
	values := got.Array()
	if len(values) != 3 {
		t.Fatalf("Script.Call(collect) array len = %d, want 3", len(values))
	}
	if !values[0].Equal(NewString("first")) {
		t.Fatalf("collect prefix = %#v, want first", values[0])
	}
	compareArrays(t, values[1], []Value{NewInt(1), NewInt(2)})
	compareHash(t, values[2].Hash(), map[string]Value{
		"flag":  NewBool(true),
		"limit": NewInt(3),
	})
}

func TestFunctionCaptureParametersValidateProducedValues(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def typed_rest(*items: array<int>)
      items.size
    end

    def typed_opts(**opts: hash<string, int>)
      opts.size
    end
    `)

	if got := callFunc(t, script, "typed_rest", []Value{NewInt(1), NewInt(2)}); !got.Equal(NewInt(2)) {
		t.Fatalf("typed_rest(1, 2) = %#v, want 2", got)
	}
	if got := callScript(t, context.Background(), script, "typed_opts", nil, CallOptions{
		Keywords: map[string]Value{"limit": NewInt(3)},
	}); !got.Equal(NewInt(1)) {
		t.Fatalf("typed_opts(limit: 3) = %#v, want 1", got)
	}

	requireCallErrorContains(t, script, "typed_rest", []Value{NewString("bad")}, CallOptions{}, "argument items expected array<int>")
	requireCallErrorContains(t, script, "typed_opts", nil, CallOptions{
		Keywords: map[string]Value{"limit": NewString("bad")},
	}, "argument opts expected hash<string, int>")
}

func TestFunctionBlockCaptureParameter(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def block_missing(&block)
      block == nil
    end

    def block_present(&block: any)
      block != nil
    end

    def run_missing
      block_missing()
    end

    def run_present
      block_present do
        1
      end
    end

    def yield_through(&block)
      yield 4
    end

    def run_yield
      yield_through do |value|
        value + 1
      end
    end
    `)

	if got := callFunc(t, script, "run_missing", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("run_missing() = %#v, want true", got)
	}
	if got := callFunc(t, script, "run_present", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("run_present() = %#v, want true", got)
	}
	if got := callFunc(t, script, "run_yield", nil); !got.Equal(NewInt(5)) {
		t.Fatalf("run_yield() = %#v, want 5", got)
	}
}

func TestParenlessYieldMultipleArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def yield_pair
      yield 4, 6
    end

    def run
      yield_pair do |left, right|
        left + right
      end
    end
    `)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(10)) {
		t.Fatalf("run() = %#v, want 10", got)
	}
}
