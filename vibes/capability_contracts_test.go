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

type scopedContractCapability struct{}

func (scopedContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"foo": NewObject(map[string]Value{
			"call": NewBuiltin("foo.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("provider"), nil
			}),
		}),
	}, nil
}

func (scopedContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"foo.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("foo.call provider expects int")
				}
				return nil
			},
		},
	}
}

type legacyFooCapability struct {
	invokeCount *int
}

func (c legacyFooCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"foo": NewObject(map[string]Value{
			"call": NewBuiltin("foo.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				*c.invokeCount = *c.invokeCount + 1
				if len(args) != 1 || args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("legacy foo.call expects string")
				}
				return NewString("legacy"), nil
			}),
		}),
	}, nil
}

type siblingMutationContractCapability struct {
	invokeCount *int
}

func (c siblingMutationContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	peer := NewInstance(&Instance{
		Class: &ClassDef{
			Name:         "PeerHost",
			Methods:      map[string]*ScriptFunction{},
			ClassMethods: map[string]*ScriptFunction{},
			ClassVars:    map[string]Value{},
		},
		Ivars: map[string]Value{},
	})
	return map[string]Value{
		"publisher": NewObject(map[string]Value{
			"install": NewBuiltin("publisher.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				peer.Instance().Ivars["call"] = NewBuiltin("peer.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*c.invokeCount = *c.invokeCount + 1
					return NewString("ok"), nil
				})
				return NewString("installed"), nil
			}),
		}),
		"peer": peer,
	}, nil
}

func (c siblingMutationContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"peer.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("peer.call expects int")
				}
				return nil
			},
		},
	}
}

type foreignBuiltinRef struct {
	call Value
}

type legacyForeignFooCapability struct {
	shared      *foreignBuiltinRef
	invokeCount *int
}

func (c legacyForeignFooCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	call := NewBuiltin("foo.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		*c.invokeCount = *c.invokeCount + 1
		if len(args) != 1 || args[0].Kind() != KindString {
			return NewNil(), fmt.Errorf("legacy foreign foo.call expects string")
		}
		return NewString("legacy-foreign"), nil
	})
	c.shared.call = call
	return map[string]Value{
		"foreign": NewObject(map[string]Value{
			"call": call,
		}),
	}, nil
}

type importingContractCapability struct {
	shared *foreignBuiltinRef
}

func (c importingContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"publisher": NewObject(map[string]Value{
			"install": NewBuiltin("publisher.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				receiver.Hash()["call"] = c.shared.call
				return NewString("installed"), nil
			}),
		}),
	}, nil
}

func (c importingContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"foo.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("provider foo.call expects int")
				}
				return nil
			},
		},
	}
}

type argMutationContractCapability struct {
	invokeCount *int
}

func (c argMutationContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"install": NewBuiltin("cap.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
					return NewNil(), fmt.Errorf("cap.install expects target hash")
				}
				args[0].Hash()["call"] = NewBuiltin("cap.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*c.invokeCount = *c.invokeCount + 1
					return NewString("ok"), nil
				})
				return NewString("installed"), nil
			}),
		}),
	}, nil
}

func (c argMutationContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"cap.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("cap.call expects int")
				}
				return nil
			},
		},
	}
}

type foreignArgCapability struct {
	invokeCount *int
}

func (c foreignArgCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"foreign": NewObject(map[string]Value{
			"call": NewBuiltin("foo.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				*c.invokeCount = *c.invokeCount + 1
				if len(args) != 1 || args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("foreign foo.call expects string")
				}
				return NewString("foreign-ok"), nil
			}),
		}),
	}, nil
}

type argPassThroughContractCapability struct{}

func (argPassThroughContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap2": NewObject(map[string]Value{
			"install": NewBuiltin("cap2.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
					return NewNil(), fmt.Errorf("cap2.install expects target hash")
				}
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (argPassThroughContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"foo.call": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindInt {
					return fmt.Errorf("provider foo.call expects int")
				}
				return nil
			},
		},
	}
}

type hashMergeContractLeakProbeCapability struct{}

func (hashMergeContractLeakProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"touch": NewBuiltin("cap.touch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (hashMergeContractLeakProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"hash.merge": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				return fmt.Errorf("hash.merge contract should not bind to foreign builtin")
			},
		},
	}
}

type stdlibContractLeakProbeCapability struct{}

func (stdlibContractLeakProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"touch": NewBuiltin("cap.touch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (stdlibContractLeakProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	contract := func(name string) CapabilityMethodContract {
		return CapabilityMethodContract{
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				return fmt.Errorf("%s contract should not bind to foreign builtin", name)
			},
		}
	}
	return map[string]CapabilityMethodContract{
		"JSON.parse":      contract("JSON.parse"),
		"Regex.match":     contract("Regex.match"),
		"hash.remap_keys": contract("hash.remap_keys"),
		"array.chunk":     contract("array.chunk"),
		"string.template": contract("string.template"),
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

func TestCapabilityContractsAreScopedPerAdapter(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  foo.call("ok")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			scopedContractCapability{},
			legacyFooCapability{invokeCount: &invocations},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invocations != 1 {
		t.Fatalf("expected legacy capability call once, got %d", invocations)
	}
	if result.Kind() != KindString || result.String() != "legacy" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCapabilityContractsBindAfterSiblingScopeMutation(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  publisher.install()
  peer.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			siblingMutationContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected sibling-mutation contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "peer.call expects int") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("sibling mutation capability should not execute when contract fails")
	}
}

func TestCapabilityContractsDoNotAttachToForeignBuiltinsByName(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  publisher.install()
  publisher.call("ok")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	shared := &foreignBuiltinRef{}
	invocations := 0
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			legacyForeignFooCapability{shared: shared, invokeCount: &invocations},
			importingContractCapability{shared: shared},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invocations != 1 {
		t.Fatalf("expected legacy foreign call once, got %d", invocations)
	}
	if result.Kind() != KindString || result.String() != "legacy-foreign" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCapabilityContractsBindAfterArgumentMutation(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  target = {}
  cap.install(target)
  target.call("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			argMutationContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected argument-mutation contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "cap.call expects int") {
		t.Fatalf("unexpected error: %s", got)
	}
	if invocations != 0 {
		t.Fatalf("argument mutation capability should not execute when contract fails")
	}
}

func TestCapabilityContractsDoNotHijackForeignBuiltinsFromArguments(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  target = { passthrough: foreign.call }
  cap2.install(target)
  target.passthrough("ok")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	invocations := 0
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			foreignArgCapability{invokeCount: &invocations},
			argPassThroughContractCapability{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invocations != 1 {
		t.Fatalf("expected foreign call once, got %d", invocations)
	}
	if result.Kind() != KindString || result.String() != "foreign-ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCapabilityContractsDoNotHijackReceiverStoredForeignBuiltins(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  cap.foreign = { a: 1 }.merge
  cap.touch()
  cap.foreign({ b: 2 })
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			hashMergeContractLeakProbeCapability{},
		},
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

func TestCapabilityContractsDoNotAttachToExpandedStdlibBuiltinsByName(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def run()
  cap.touch()
  parsed = JSON.parse("{\"name\":\"alex\"}")
  {
    json_name: parsed.fetch("name"),
    regex: Regex.match("ID-[0-9]+", "ID-12"),
    squish: "  hi \n there  ".squish,
    template: "Hi {{name}}".template({ name: "Alex" }),
    chunk_size: [1, 2, 3, 4].chunk(2).size,
    remap_value: { name: "Alex" }.remap_keys({ name: :player_name }).fetch(:player_name)
  }
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			stdlibContractLeakProbeCapability{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["json_name"].Equal(NewString("alex")) {
		t.Fatalf("json_name mismatch: %v", got["json_name"])
	}
	if !got["regex"].Equal(NewString("ID-12")) {
		t.Fatalf("regex mismatch: %v", got["regex"])
	}
	if !got["squish"].Equal(NewString("hi there")) {
		t.Fatalf("squish mismatch: %v", got["squish"])
	}
	if !got["template"].Equal(NewString("Hi Alex")) {
		t.Fatalf("template mismatch: %v", got["template"])
	}
	if !got["chunk_size"].Equal(NewInt(2)) {
		t.Fatalf("chunk_size mismatch: %v", got["chunk_size"])
	}
	if !got["remap_value"].Equal(NewString("Alex")) {
		t.Fatalf("remap_value mismatch: %v", got["remap_value"])
	}
}

func TestCapabilityContractsStayEnforcedThroughExpandedStdlibTransforms(t *testing.T) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`def call_through_transforms()
  hash_handler = { handler: probe.call }.remap_keys({ handler: :run }).fetch(:run)
  chunk_handler = [probe.call].chunk(1).first.first
  {
    hash_ok: hash_handler(1),
    chunk_ok: chunk_handler(2)
  }
end

def fail_through_remap()
  handler = { handler: probe.call }.remap_keys({ handler: :run }).fetch(:run)
  handler("bad")
end

def fail_through_chunk()
  handler = [probe.call].chunk(1).first.first
  handler("bad")
end`)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}

	successInvocations := 0
	okResult, err := script.Call(context.Background(), "call_through_transforms", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			contractProbeCapability{invokeCount: &successInvocations},
		},
	})
	if err != nil {
		t.Fatalf("unexpected success-call error: %v", err)
	}
	if successInvocations != 2 {
		t.Fatalf("expected two successful capability invocations, got %d", successInvocations)
	}
	if okResult.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", okResult.Kind())
	}
	if !okResult.Hash()["hash_ok"].Equal(NewString("ok")) {
		t.Fatalf("hash_ok mismatch: %v", okResult.Hash()["hash_ok"])
	}
	if !okResult.Hash()["chunk_ok"].Equal(NewString("ok")) {
		t.Fatalf("chunk_ok mismatch: %v", okResult.Hash()["chunk_ok"])
	}

	remapInvocations := 0
	_, err = script.Call(context.Background(), "fail_through_remap", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			contractProbeCapability{invokeCount: &remapInvocations},
		},
	})
	if err == nil {
		t.Fatalf("expected remap path contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "probe.call expects a single int argument") {
		t.Fatalf("unexpected remap path error: %s", got)
	}
	if remapInvocations != 0 {
		t.Fatalf("contract should reject remap path before invoke, got %d calls", remapInvocations)
	}

	chunkInvocations := 0
	_, err = script.Call(context.Background(), "fail_through_chunk", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			contractProbeCapability{invokeCount: &chunkInvocations},
		},
	})
	if err == nil {
		t.Fatalf("expected chunk path contract validation error")
	}
	if got := err.Error(); !strings.Contains(got, "probe.call expects a single int argument") {
		t.Fatalf("unexpected chunk path error: %s", got)
	}
	if chunkInvocations != 0 {
		t.Fatalf("contract should reject chunk path before invoke, got %d calls", chunkInvocations)
	}
}
