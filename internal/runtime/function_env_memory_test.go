package runtime

import "testing"

// TestFunctionValuesChargeTheirCapturedEnv pins that a function value costs
// its captured environment. The compiled body is a static artifact, but the
// environment is not: a module exporting any function retains its whole
// module env through that closure, so treating the value as wholly static let
// a large top-level local drop out of the quota once initialization returned
// and requiring many modules accumulated unbounded memory (#48).
//
// The block arm charges its env for the same reason, so the two are compared
// directly here: a function retaining an env was charged 32 bytes where a
// block retaining the same env was charged its full size.
func TestFunctionValuesChargeTheirCapturedEnv(t *testing.T) {
	t.Parallel()

	payload := make([]Value, 50_000)
	for i := range payload {
		payload[i] = NewInt(int64(i))
	}
	env := newEnv(nil)
	env.Define("payload", NewArray(payload))

	envBytes := newMemoryEstimator().env(env)
	if envBytes <= 0 {
		t.Fatalf("the probe env must cost something, got %d", envBytes)
	}

	fnBytes := newMemoryEstimator().value(NewFunction(&ScriptFunction{Name: "exported", Env: env}))
	blockBytes := newMemoryEstimator().value(NewBlock(nil, nil, env))

	if fnBytes < envBytes {
		t.Fatalf("a function retaining a %d-byte env is charged only %d; a block retaining it is charged %d",
			envBytes, fnBytes, blockBytes)
	}
}

// TestFunctionEnvIsNotChargedTwice pins that the env dedup keeps a shared
// environment charged once: a function reachable beside the env it captured
// must not double the estimate.
func TestFunctionEnvIsNotChargedTwice(t *testing.T) {
	t.Parallel()

	payload := make([]Value, 20_000)
	for i := range payload {
		payload[i] = NewInt(int64(i))
	}
	env := newEnv(nil)
	env.Define("payload", NewArray(payload))
	fn := NewFunction(&ScriptFunction{Name: "exported", Env: env})

	once := newMemoryEstimator().value(fn)

	est := newMemoryEstimator()
	twice := est.value(fn) + est.value(fn)
	if twice > once+1024 {
		t.Fatalf("charging the same function twice cost %d, want about the single %d", twice, once)
	}
}
