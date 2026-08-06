package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"
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
				valueInstance(peer).Ivars["call"] = NewBuiltin("peer.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
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

type siblingRootForeignLeakProbeCapability struct{}

func (siblingRootForeignLeakProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"publisher": NewObject(map[string]Value{
			"touch": NewBuiltin("publisher.touch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("ok"), nil
			}),
		}),
		"peer": NewObject(map[string]Value{}),
	}, nil
}

func (siblingRootForeignLeakProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
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

type hashStoreContractLeakProbeCapability struct{}

func (hashStoreContractLeakProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"touch": NewBuiltin("cap.touch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("ok"), nil
			}),
		}),
	}, nil
}

func (hashStoreContractLeakProbeCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"hash.store": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				return fmt.Errorf("hash.store contract should not bind to foreign builtin")
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
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  probe.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{contractProbeCapability{invokeCount: &invocations}},
	})
	if err == nil {
		t.Fatalf("expected contract validation error")
	}
	requireErrorContains(t, err, "probe.call expects a single int argument")
	if invocations != 0 {
		t.Fatalf("capability should not execute when arg contract fails")
	}
}

func TestCapabilityContractRejectsInvalidReturnValue(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  probe.call(1)
end`)
	var err error

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
	requireErrorContains(t, err, "probe.call must return string")
	if invocations != 1 {
		t.Fatalf("expected capability to execute once before return validation, got %d", invocations)
	}
}

func TestDuplicateCapabilityContractsFailBinding(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  1
end`)
	var err error

	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			duplicateContractCapability{},
			duplicateContractCapability{},
		},
	})
	if err == nil {
		t.Fatalf("expected duplicate contract error")
	}
	requireErrorContains(t, err, "duplicate capability contract for dup.call")
}

func TestCapabilityContractsDoNotAttachByGlobalBuiltinName(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  base = { a: 1 }
  override = { b: 2 }
  base.merge(override)
end`)
	var err error

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
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  box.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			instanceIvarContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected instance contract validation error")
	}
	requireErrorContains(t, err, "probe.call expects int")
	if invocations != 0 {
		t.Fatalf("instance capability should not execute when contract fails")
	}
}

func TestCapabilityContractsTraverseClassValues(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  holder.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			classVarContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected class contract validation error")
	}
	requireErrorContains(t, err, "probe.class_call expects int")
	if invocations != 0 {
		t.Fatalf("class capability should not execute when contract fails")
	}
}

func TestCapabilityContractsBindForFactoryReturnedBuiltins(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  worker = factory.make()
  worker.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			lazyFactoryContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected factory-returned contract validation error")
	}
	requireErrorContains(t, err, "factory.call expects int")
	if invocations != 0 {
		t.Fatalf("factory capability should not execute when contract fails")
	}
}

func TestCapabilityContractsBindAfterReceiverMutation(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  mut.install()
  mut.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			receiverMutationContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected receiver-mutation contract validation error")
	}
	requireErrorContains(t, err, "mut.call expects int")
	if invocations != 0 {
		t.Fatalf("mutated receiver capability should not execute when contract fails")
	}
}

func TestCapabilityContractsAreScopedPerAdapter(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  foo.call("ok")
end`)
	var err error

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
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  publisher.install()
  peer.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			siblingMutationContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected sibling-mutation contract validation error")
	}
	requireErrorContains(t, err, "peer.call expects int")
	if invocations != 0 {
		t.Fatalf("sibling mutation capability should not execute when contract fails")
	}
}

func TestCapabilityContractsDoNotAttachToForeignBuiltinsByName(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  publisher.install()
  publisher.call("ok")
end`)
	var err error

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

func TestCapabilityContractsDoNotHijackForeignBuiltinsFromSiblingRoots(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  peer.call = foreign.call
  publisher.touch()
  peer.call("ok")
end`)
	var err error

	shared := &foreignBuiltinRef{}
	invocations := 0
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			legacyForeignFooCapability{shared: shared, invokeCount: &invocations},
			siblingRootForeignLeakProbeCapability{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if invocations != 1 {
		t.Fatalf("expected foreign call once, got %d", invocations)
	}
	if result.Kind() != KindString || result.String() != "legacy-foreign" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestCapabilityContractsBindAfterArgumentMutation(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  target = {}
  cap.install(target)
  target.call("bad")
end`)
	var err error

	invocations := 0
	_, err = script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			argMutationContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected argument-mutation contract validation error")
	}
	requireErrorContains(t, err, "cap.call expects int")
	if invocations != 0 {
		t.Fatalf("argument mutation capability should not execute when contract fails")
	}
}

func TestCapabilityContractsDoNotHijackForeignBuiltinsFromArguments(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
  target = { passthrough: foreign.call }
  cap2.install(target)
  target.passthrough("ok")
end`)
	var err error

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
	t.Parallel()
	// hash.store stays a non-auto-invoked builtin (it requires a key and value),
	// so a bare `{ a: 1 }.store` yields the unbound method value rather than
	// auto-invoking. The test stores that foreign builtin on a capability slot and
	// confirms the capability's hash.store contract does not bind to it.
	script := compileScriptDefault(t, `def run()
  cap.foreign = { a: 1 }.store
  cap.touch()
  cap.foreign(:b, 2)
end`)
	var err error

	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			hashStoreContractLeakProbeCapability{},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// hash.store mutates its bound receiver in place and returns the stored
	// value; the foreign builtin surfacing that contract proves the
	// capability's own store contract did not bind to it.
	if result.Kind() != KindInt || result.Int() != 2 {
		t.Fatalf("expected the stored value 2, got %#v", result)
	}
}

func TestCapabilityContractsDoNotAttachToExpandedStdlibBuiltinsByName(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run()
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
	var err error

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
	t.Parallel()
	script := compileScriptDefault(t, `def call_through_transforms()
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
	var err error

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
	requireErrorContains(t, err, "probe.call expects a single int argument")
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
	requireErrorContains(t, err, "probe.call expects a single int argument")
	if chunkInvocations != 0 {
		t.Fatalf("contract should reject chunk path before invoke, got %d calls", chunkInvocations)
	}
}

// breakPublishingContractCapability publishes a second builtin as a side
// effect, yields to its block, and declares a return contract the absorbed
// break value fails. It exercises the order between return validation and the
// post-call publication scan.
type breakPublishingContractCapability struct {
	invokeCount *int
}

func (c breakPublishingContractCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"mut": NewObject(map[string]Value{
			"install": NewBuiltin("mut.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				receiver.Hash()["call"] = NewBuiltin("mut.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*c.invokeCount = *c.invokeCount + 1
					return NewString("ok"), nil
				})
				if block.IsNil() {
					return NewString("installed"), nil
				}
				return exec.callBlockValue(block, []Value{NewInt(1)}, Position{})
			}),
		}),
	}, nil
}

func (c breakPublishingContractCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"mut.install": {
			ValidateReturn: func(result Value) error {
				if result.Kind() != KindString {
					return fmt.Errorf("mut.install must return string")
				}
				return nil
			},
		},
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

// A rejected return value does not un-publish what the call already made
// reachable. Returning the validation error before the post-call scan left the
// newly published builtin bound to no contract, so script code could rescue
// the error and then call it with arguments the contract forbids.
func TestPublicationScanRunsWhenAnAbsorbedBreakFailsValidation(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  begin
    mut.install { break 42 }
  rescue => e
    nil
  end
  mut.call("bad")
end`)

	invocations := 0
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			breakPublishingContractCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected the published builtin to still enforce its contract")
	}
	requireErrorContains(t, err, "mut.call expects int")
	if invocations != 0 {
		t.Fatalf("contract-violating call executed %d times, want it blocked", invocations)
	}
}

// A break value the caller already owned is not something the call published.
// The pre-call block scan stops at ambient environments, so binding contracts
// from a rejected result attached the capability's contract to an unrelated
// global and made the caller's own later calls to it fail validation.
func TestRejectedResultDoesNotBindContractsToACallerOwnedBuiltin(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  begin
    mut.install { break helper }
  rescue => e
    nil
  end
  helper("anything", 2)
end`)

	invocations := 0
	helperCalls := 0
	helper := NewBuiltin("mut.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		helperCalls++
		return NewString("helper ok"), nil
	})
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Globals: map[string]Value{"helper": helper},
		Capabilities: []CapabilityAdapter{
			breakPublishingContractCapability{invokeCount: &invocations},
		},
	})
	if err != nil {
		t.Fatalf("a caller-owned builtin was validated against the capability's contract: %v", err)
	}
	if helperCalls != 1 {
		t.Fatalf("caller-owned builtin ran %d times, want 1", helperCalls)
	}
}

// unvalidatedBreakCapability publishes a second builtin and yields, but
// declares no return contract on install, so an absorbed break is accepted as
// the call's result.
type unvalidatedBreakCapability struct {
	invokeCount *int
}

func (c unvalidatedBreakCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"mut": NewObject(map[string]Value{
			"install": NewBuiltin("mut.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				receiver.Hash()["call"] = NewBuiltin("mut.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*c.invokeCount = *c.invokeCount + 1
					return NewString("ok"), nil
				})
				if block.IsNil() {
					return NewString("installed"), nil
				}
				return exec.callBlockValue(block, []Value{NewInt(1)}, Position{})
			}),
		}),
	}, nil
}

func (c unvalidatedBreakCapability) CapabilityContracts() map[string]CapabilityMethodContract {
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

// The publication scan itself must still run on the absorbed-break path: a
// builtin the call really did publish stays contract-bound.
func TestPublicationScanStillRunsForAnAcceptedBreak(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  mut.install { break "accepted" }
  mut.call("bad")
end`)

	invocations := 0
	_, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			unvalidatedBreakCapability{invokeCount: &invocations},
		},
	})
	if err == nil {
		t.Fatalf("expected the published builtin to still enforce its contract")
	}
	requireErrorContains(t, err, "mut.call expects int")
	if invocations != 0 {
		t.Fatalf("contract-violating call executed %d times, want it blocked", invocations)
	}
}

// yieldingPublisherCapability creates a contract-covered builtin and yields it,
// so a block can make it the call's result by breaking with it.
type yieldingPublisherCapability struct {
	invokeCount *int
}

func (c yieldingPublisherCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"mut": NewObject(map[string]Value{
			"install": NewBuiltin("mut.install", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				fresh := NewBuiltin("mut.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*c.invokeCount = *c.invokeCount + 1
					return NewString("ok"), nil
				})
				if block.IsNil() {
					return NewString("installed"), nil
				}
				return exec.callBlockValue(block, []Value{fresh}, Position{})
			}),
		}),
	}, nil
}

func (c yieldingPublisherCapability) CapabilityContracts() map[string]CapabilityMethodContract {
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

// A builtin the capability itself created and yielded becomes the call's
// result when the block breaks with it, while staying absent from
// preCallKnownBuiltins and unreachable through the receiver, roots, or
// arguments. Suppressing the result scan for absorbed breaks let it escape
// without its contract.
func TestYieldedBuiltinBrokenOutStillEnforcesItsContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "assigned", body: "published = mut.install { |b| break b }\n  published(\"bad\", 2)"},
		{name: "called immediately", body: "mut.install { |b| break b }(\"bad\", 2)"},
		{name: "wrapped in an array", body: "out = mut.install { |b| break [b] }\n  out[0](\"bad\", 2)"},
		{name: "wrapped in a hash", body: "out = mut.install { |b| break {fn: b} }\n  out[:fn](\"bad\", 2)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend")
			invocations := 0
			_, err := script.Call(context.Background(), "run", nil, CallOptions{
				Capabilities: []CapabilityAdapter{yieldingPublisherCapability{invokeCount: &invocations}},
			})
			if err == nil {
				t.Fatalf("%s: a yielded builtin escaped its contract", tc.name)
			}
			requireErrorContains(t, err, "mut.call expects int")
			if invocations != 0 {
				t.Fatalf("%s: contract-violating call executed %d times, want it blocked", tc.name, invocations)
			}
		})
	}
}

// The ambient walk's budget must actually stop it. Treating zero as
// "unbounded" meant an exhausted walk became unbounded again on the next
// node, restoring the very cost the budget exists to cap.
func TestAmbientCollectBudgetStopsTheWalk(t *testing.T) {
	t.Parallel()

	// A graph far larger than the budget, reachable from one binding.
	deep := NewInt(0)
	for range ambientCollectNodeBudget * 4 {
		deep = NewArray([]Value{deep, NewBuiltin("probe", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewNil(), nil
		})})
	}

	scanner := newCapabilityContractScanner()
	scanner.collectBounded, scanner.collectBudget = true, ambientCollectNodeBudget
	out := map[*Builtin]struct{}{}
	scanner.collectBuiltins(deep, out)

	if scanner.collectBudget > 0 {
		t.Fatalf("the walk ended with %d budget left, so it did not reach the cap", scanner.collectBudget)
	}
	if len(out) > ambientCollectNodeBudget {
		t.Fatalf("the walk collected %d builtins past a %d-node budget", len(out), ambientCollectNodeBudget)
	}
}

// Exhausting the budget must stop traversal, not merely make each remaining
// element a no-op recursive call. The observable difference is cost: with the
// loop guards the walk is independent of container size, without them it stays
// linear in it (measured 345us at 100k elements and 1.01ms at 400k, against
// tens of microseconds either way once traversal actually stops).
func TestAmbientCollectCostIsIndependentOfContainerSize(t *testing.T) {
	walk := func(n int) time.Duration {
		flat := make([]Value, n)
		for i := range flat {
			flat[i] = NewInt(int64(i))
		}
		val := NewArray(flat)
		best := time.Hour
		for range 5 {
			scanner := newCapabilityContractScanner()
			scanner.collectBounded, scanner.collectBudget = true, ambientCollectNodeBudget
			out := map[*Builtin]struct{}{}
			start := time.Now()
			scanner.collectBuiltins(val, out)
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}

	const small, large = 100_000, 800_000
	smallCost, largeCost := walk(small), walk(large)

	// An 8x larger container must not cost 4x more. Linear traversal would.
	if largeCost > smallCost*4 {
		t.Fatalf("walking %d elements took %v against %v for %d: traversal is not stopping at the budget",
			large, largeCost, smallCost, small)
	}
}

// A bounded walk must stop inside captured environments too. An ambient global
// can be an escaped block whose captured frame holds many bindings, and
// scanClosureEnv visited every one (and every ancestor frame) with a no-op
// visitor once the budget was spent, leaving the cost linear in the frame.
func TestAmbientCollectStopsInsideCapturedEnvironments(t *testing.T) {
	capturedBlock := func(bindings int) Value {
		captured := newEnv(nil)
		for i := range bindings {
			captured.Define(fmt.Sprintf("c%06d", i), NewInt(int64(i)))
		}
		return NewBlock(nil, nil, captured)
	}

	walk := func(val Value) time.Duration {
		best := time.Hour
		for range 5 {
			scanner := newCapabilityContractScanner()
			scanner.collectBounded, scanner.collectBudget = true, ambientCollectNodeBudget
			out := map[*Builtin]struct{}{}
			start := time.Now()
			scanner.collectBuiltins(val, out)
			if d := time.Since(start); d < best {
				best = d
			}
		}
		return best
	}

	small := walk(capturedBlock(ambientCollectNodeBudget * 2))
	large := walk(capturedBlock(ambientCollectNodeBudget * 16))

	// 8x the captured bindings must not cost 4x. Measured about 7x before.
	if large > small*4 {
		t.Fatalf("walking a %d-binding captured frame took %v against %v for %d: closure traversal is not stopping at the budget",
			ambientCollectNodeBudget*16, large, small, ambientCollectNodeBudget*2)
	}
}

// yieldFactoryCapability yields a freshly created, contract-covered builtin
// to a script block. cap.factory itself declares a return validator that
// rejects non-strings, so a block that breaks with the yielded builtin makes
// the call fail while the script keeps a reference to what was yielded.
type yieldFactoryCapability struct {
	uncontractedCalls *int
}

func (c yieldFactoryCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	uncontracted := c.uncontractedCalls
	return map[string]Value{
		"cap": NewObject(map[string]Value{
			"factory": NewBuiltin("cap.factory", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				made := NewBuiltin("cap.made", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					if len(args) > 0 && args[0].Kind() != KindString {
						*uncontracted = *uncontracted + 1
					}
					return NewString("invoked"), nil
				})
				if block.IsNil() {
					return made, nil
				}
				return exec.callBlockValue(block, []Value{made}, Position{})
			}),
		}),
	}, nil
}

func (c yieldFactoryCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		"cap.factory": {
			ValidateReturn: func(result Value) error {
				if result.Kind() != KindString {
					return fmt.Errorf("cap.factory must return string")
				}
				return nil
			},
		},
		"cap.made": {
			ValidateArgs: func(args []Value, kwargs map[string]Value, block Value) error {
				if len(args) != 1 || args[0].Kind() != KindString {
					return fmt.Errorf("cap.made expects a single string argument")
				}
				return nil
			},
		},
	}
}

// TestCapabilityContractsBindBuiltinsRetainedByBlocks pins that a builtin a
// capability yields to a script block keeps its contract when the block
// retains it in an enclosing local. The block's captured environment is the
// one place a published builtin can survive that the receiver, capability
// roots, arguments, and result do not reach — and when the block breaks with
// the value, the return validator rejects it, so the result sweep is skipped
// entirely and rescuing the error left the retained builtin uncontracted.
func TestCapabilityContractsBindBuiltinsRetainedByBlocks(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"rejected break": `def run()
  leaked = nil
  begin
    cap.factory do |fn|
      leaked = fn
      break fn
    end
  rescue
    nil
  end
  leaked(42)
end`,
		"plain yield": `def run()
  leaked = nil
  cap.factory do |fn|
    leaked = fn
    "fine"
  end
  leaked(42)
end`,
	}
	for name, src := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			uncontracted := 0
			script := compileScriptDefault(t, src)
			_, err := script.Call(context.Background(), "run", nil,
				callOptionsWithCapabilities(yieldFactoryCapability{uncontractedCalls: &uncontracted}))
			requireErrorContains(t, err, "cap.made expects a single string argument")
			if uncontracted != 0 {
				t.Fatalf("retained builtin ran without its contract %d time(s)", uncontracted)
			}
		})
	}
}

// TestCapabilityYieldedBuiltinStaysUsableUnderContract pins the other side of
// the binding: attaching the contract must not break the legitimate use, so a
// conforming call through the retained reference still succeeds.
func TestCapabilityYieldedBuiltinStaysUsableUnderContract(t *testing.T) {
	t.Parallel()

	uncontracted := 0
	script := compileScriptDefault(t, `def run()
  leaked = nil
  cap.factory do |fn|
    leaked = fn
    "fine"
  end
  leaked("ok")
end`)
	result, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(yieldFactoryCapability{uncontractedCalls: &uncontracted}))
	if err != nil {
		t.Fatalf("a conforming call through the retained builtin must succeed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "invoked" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if uncontracted != 0 {
		t.Fatalf("contract counted %d uncontracted calls", uncontracted)
	}
}
