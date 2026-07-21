package runtime

import (
	"context"
	"fmt"
	"testing"
)

// forgingReturnCapability models a hostile host adapter that used to set the
// public ReturnValidatedByBuiltin flag to skip its declared return contract.
// With the flag removed, its only remaining move is returning a violating
// value and hoping validation does not run.
type forgingReturnCapability struct {
	validations *int
}

func (c forgingReturnCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"forge": NewObject(map[string]Value{
			"call": NewBuiltin("forge.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewInt(42), nil
			}),
		}),
	}, nil
}

func (c forgingReturnCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"forge.call": {
			ValidateReturn: func(result Value) error {
				*c.validations = *c.validations + 1
				if result.Kind() != KindString {
					return fmt.Errorf("forge.call must return string")
				}
				return nil
			},
		},
	}
}

func TestCapabilityReturnValidationCannotBeBypassedByCustomAdapter(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  forge.call()
end`)

	validations := 0
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{forgingReturnCapability{validations: &validations}},
	})
	if err == nil {
		t.Fatalf("expected return contract validation error")
	}
	requireErrorContains(t, err, "forge.call must return string")
	if validations != 1 {
		t.Fatalf("expected ValidateReturn to run exactly once, got %d", validations)
	}
}

// proofProbeCapability is a white-box stand-in for a first-party adapter: its
// builtin validates and isolates its own result, then records the internal
// proof exactly as the jobqueue and events adapters do. The configurable
// proof method and value let tests show that a proof for a different method
// or a different value does not cover the returned result.
type proofProbeCapability struct {
	validations *int
	proofMethod string
	proofResult func(returned Value) Value
	markProof   bool
}

func (c proofProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"proofprobe": NewObject(map[string]Value{
			"call": NewBuiltin("proofprobe.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				result := NewString("validated")
				if c.markProof {
					proofResult := result
					if c.proofResult != nil {
						proofResult = c.proofResult(result)
					}
					exec.markValidatedCapabilityReturn(c.proofMethod, proofResult)
				}
				return result, nil
			}),
		}),
	}, nil
}

func (c proofProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"proofprobe.call": {
			ValidateReturn: func(result Value) error {
				*c.validations = *c.validations + 1
				return nil
			},
		},
	}
}

func TestCapabilityReturnProofSkipsDoubleValidation(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  proofprobe.call()
end`)

	validations := 0
	got, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{proofProbeCapability{
			validations: &validations,
			proofMethod: "proofprobe.call",
			markProof:   true,
		}},
	})
	if err != nil {
		t.Fatalf("Script.Call(run) error = %v, want nil", err)
	}
	if got.Kind() != KindString || got.String() != "validated" {
		t.Fatalf("run = %#v, want \"validated\"", got)
	}
	if validations != 0 {
		t.Fatalf("expected ValidateReturn to be skipped for a proven result, got %d validations", validations)
	}
}

func TestCapabilityReturnProofRequiresMatchingMethod(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  proofprobe.call()
end`)

	validations := 0
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{proofProbeCapability{
			validations: &validations,
			proofMethod: "other.method",
			markProof:   true,
		}},
	}); err != nil {
		t.Fatalf("Script.Call(run) error = %v, want nil", err)
	}
	if validations != 1 {
		t.Fatalf("expected ValidateReturn to run for a proof naming another method, got %d validations", validations)
	}
}

func TestCapabilityReturnProofRequiresIdenticalResult(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  proofprobe.call()
end`)

	validations := 0
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{proofProbeCapability{
			validations: &validations,
			proofMethod: "proofprobe.call",
			proofResult: func(Value) Value { return NewString("different") },
			markProof:   true,
		}},
	}); err != nil {
		t.Fatalf("Script.Call(run) error = %v, want nil", err)
	}
	if validations != 1 {
		t.Fatalf("expected ValidateReturn to run for a proof over a different value, got %d validations", validations)
	}
}

// nestedProofCapability exposes an outer contract-bound builtin that invokes
// an inner proof-marking builtin through normal dispatch and returns the
// inner result unchanged. The inner dispatch must consume the proof at its
// own boundary, so the outer method's return contract still runs.
type nestedProofCapability struct {
	validations *int
	inner       Value
}

func (c *nestedProofCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	c.inner = NewBuiltin("nested.inner", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		result := NewHash(map[string]Value{"ok": NewBool(true)})
		exec.markValidatedCapabilityReturn("nested.outer", result)
		return result, nil
	})
	return map[string]Value{
		"nested": NewObject(map[string]Value{
			"outer": NewBuiltin("nested.outer", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return exec.invokeCallable(c.inner, NewNil(), nil, nil, NewNil(), Position{})
			}),
		}),
	}, nil
}

func (c *nestedProofCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"nested.outer": {
			ValidateReturn: func(result Value) error {
				*c.validations = *c.validations + 1
				return nil
			},
		},
	}
}

func TestCapabilityReturnProofDoesNotLeakAcrossNestedDispatch(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  nested.outer()
end`)

	validations := 0
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{&nestedProofCapability{validations: &validations}},
	}); err != nil {
		t.Fatalf("Script.Call(run) error = %v, want nil", err)
	}
	if validations != 1 {
		t.Fatalf("expected outer ValidateReturn to run despite the nested proof, got %d validations", validations)
	}
}
