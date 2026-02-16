package vibes

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type contractProbeCapability struct {
	invokeCount *int
	result      Value
}

func (c contractProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"call": NewBuiltin("probe.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				*c.invokeCount = *c.invokeCount + 1
				if c.result.Kind() != KindNil {
					return c.result, nil
				}
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (c contractProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"probe.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("probe.call expects a single int argument")
				}
				if !block.IsNil() {
					return fmt.Errorf("probe.call does not accept blocks")
				}
				return nil
			},
			ValidateReturn: func(result Value) error {
				if result.Kind() != KindString {
					return fmt.Errorf("probe.call must return string")
				}
				return nil
			},
		},
	}
}

type duplicateContractCapability struct{}

func (duplicateContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{}, nil
}

func (duplicateContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"dup.call": {},
	}
}

type unrelatedNamedContractCapability struct{}

func (unrelatedNamedContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"call": NewBuiltin("probe.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (unrelatedNamedContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"hash.merge": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				return fmt.Errorf("hash.merge contract should not be applied")
			},
		},
	}
}

type instanceIvarContractCapability struct {
	invokeCount *int
}

func (c instanceIvarContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	classDef := &ClassDef{
		Name:         "CapabilityBox",
		Methods:      map[string]*ScriptFunction{},
		ClassMethods: map[string]*ScriptFunction{},
		ClassVars:    map[string]Value{},
	}
	instance := &Instance{
		Class: classDef,
		Ivars: map[string]Value{
			"call": NewBuiltin("probe.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				*c.invokeCount = *c.invokeCount + 1
				return NewString("ok"), nil
			}),
		},
	}
	return map[string]Value{"box": NewInstance(instance)}, nil
}

func (c instanceIvarContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"probe.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("probe.call expects int")
				}
				return nil
			},
		},
	}
}

type classVarContractCapability struct {
	invokeCount *int
}

func (c classVarContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	classDef := &ClassDef{
		Name:         "CapabilityHolder",
		Methods:      map[string]*ScriptFunction{},
		ClassMethods: map[string]*ScriptFunction{},
		ClassVars: map[string]Value{
			"call": NewBuiltin("probe.class_call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				*c.invokeCount = *c.invokeCount + 1
				return NewString("ok"), nil
			}),
		},
	}
	return map[string]Value{"holder": NewClass(classDef)}, nil
}

func (c classVarContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"probe.class_call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("probe.class_call expects int")
				}
				return nil
			},
		},
	}
}

type lazyFactoryContractCapability struct {
	invokeCount *int
}

func (c lazyFactoryContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"factory": NewObject(map[string]Value{
			"make": NewBuiltin("factory.make", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewObject(map[string]Value{
					"call": NewBuiltin("factory.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
						*c.invokeCount = *c.invokeCount + 1
						return NewString("ok"), nil
					}),
				}), nil
			}),
		}),
	}, nil
}

func (c lazyFactoryContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"factory.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("factory.call expects int")
				}
				return nil
			},
		},
	}
}

type receiverMutationContractCapability struct {
	invokeCount *int
}

func (c receiverMutationContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"mut": NewObject(map[string]Value{
			"install": NewBuiltin("mut.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				receiver.Hash()["call"] = NewBuiltin("mut.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*c.invokeCount = *c.invokeCount + 1
					return NewString("ok"), nil
				})
				return NewString("installed"), nil
			}),
		}),
	}, nil
}

func (c receiverMutationContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"mut.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("mut.call expects int")
				}
				return nil
			},
		},
	}
}

func TestCapabilityContractRejectsInvalidArguments(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  probe.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{contractProbeCapability{invokeCount: &invocations}},
	})
	if err == nil {
		t.Fatalf("expected contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "probe.call expects a single int argument") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("capability should not execute when arg contract fails")
	}
}

func TestCapabilityContractRejectsInvalidReturnValue(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  probe.call(1)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{contractProbeCapability{
			invokeCount: &invocations,
			result: NewObject(map[string]Value{
				"save": NewBuiltin("leak.save", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					return NewString("ok"), nil
				}),
			}),
		}},
	})
	if err == nil {
		t.Fatalf("expected return contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "probe.call must return string") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 1 {
		t.Fatalf("expected capability to execute once before return validation, got %d", invocations)
	}
}

func TestDuplicateCapabilityContractsFailBinding(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  1
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			duplicateContractCapability{},
			duplicateContractCapability{},
		},
	})
	if err == nil {
		t.Fatalf("expected duplicate contract error")
	}
	if got := err.Error(); !strings.Contains(got, "duplicate capability contract for dup.call") {
		t.Fatalf("unexpected error: %s", got)
	}
}

func TestCapabilityContractsDoNotAttachByGlobalBuiltinName(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  base = { a: 1 }
  override = { b: 2 }
  base.merge(override)
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{unrelatedNamedContractCapability{}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", result.Kind())
	}
	if got, ok := result.Hash()["b"]; !ok || got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("unexpected merge result: %#v", result.Hash())
	}
}

func TestCapabilityContractsTraverseInstanceValues(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  box.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			instanceIvarContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected instance contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "probe.call expects int") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("instance capability should not execute when contract fails")
	}
}

func TestCapabilityContractsTraverseClassValues(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  holder.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			classVarContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected class contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "probe.class_call expects int") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("class capability should not execute when contract fails")
	}
}

func TestCapabilityContractsBindForFactoryReturnedBuiltins(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  worker = factory.make()
  worker.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			lazyFactoryContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected factory-returned contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "factory.call expects int") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("factory capability should not execute when contract fails")
	}
}

func TestCapabilityContractsBindAfterReceiverMutation(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  mut.install()
  mut.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			receiverMutationContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected receiver-mutation contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "mut.call expects int") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("mutated receiver capability should not execute when contract fails")
	}
}
