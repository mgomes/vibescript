package runtime

import (
	"bytes"
	"context"
	"testing"
)

func TestKernelRandAndSrand(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def deterministic
  srand(1234)
  a = [rand, rand(10), rand(1..3), rand(1...3)]
  previous = srand(1234)
  b = [rand, rand(10), rand(1..3), rand(1...3)]
  [
    a == b,
    previous,
    a[0] >= 0.0 && a[0] < 1.0,
    a[1] >= 0 && a[1] < 10,
    a[2] >= 1 && a[2] <= 3,
    a[3] >= 1 && a[3] < 3
  ]
end

def bad_int
  rand(0)
end

def bad_range
  rand(3...3)
end

def bad_type
  rand("x")
end`)

	got := callScript(t, context.Background(), script, "deterministic", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewBool(true),
		NewInt(1234),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
	})

	requireCallErrorContains(t, script, "bad_int", nil, CallOptions{}, "rand integer bound must be positive")
	requireCallErrorContains(t, script, "bad_range", nil, CallOptions{}, "rand range is empty")
	requireCallErrorContains(t, script, "bad_type", nil, CallOptions{}, "rand expects an integer bound or integer range")
}

func TestKernelRandSeedStateIsPerCall(t *testing.T) {
	t.Parallel()

	reads := 0
	engine := MustNewEngine(Config{
		RandomReadFunc: func(_ context.Context, p []byte) (int, error) {
			reads++
			for i := range p {
				p[i] = 0
			}
			return len(p), nil
		},
	})
	script := compileScriptWithEngine(t, engine, `def seeded
  srand(99)
  rand(10)
end

def unseeded
  rand(10)
end`)

	_ = callScript(t, context.Background(), script, "seeded", nil, CallOptions{})
	if reads != 0 {
		t.Fatalf("seeded rand entropy reads = %d, want 0", reads)
	}
	if got := callScript(t, context.Background(), script, "unseeded", nil, CallOptions{}); !got.Equal(NewInt(0)) {
		t.Fatalf("unseeded rand after seeded call = %#v, want entropy-derived 0", got)
	}
	if reads != 1 {
		t.Fatalf("unseeded rand entropy reads = %d, want 1", reads)
	}
}

func TestKernelRandHonorsRandomSourceAndCancellation(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{
		RandomReader: bytes.NewReader(bytes.Repeat([]byte{0xff}, 16)),
	}, `def run
  rand
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindFloat || got.Float() < 0.9999999999999998 || got.Float() >= 1.0 {
		t.Fatalf("rand from fixed entropy = %#v, want float just below 1", got)
	}

	reads := 0
	engine := MustNewEngine(Config{
		RandomReadFunc: func(context.Context, []byte) (int, error) {
			reads++
			return 0, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := &Execution{ctx: ctx, engine: engine}
	_, err := builtinRand(exec, NewNil(), nil, nil, NewNil())
	requireErrorIs(t, err, context.Canceled)
	if reads != 0 {
		t.Fatalf("rand entropy reads = %d, want 0 after pre-canceled context", reads)
	}
}
