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

// Inverted deliberately for #1210 (host boundary hands out independent
// values): a host builtin can no longer publish behavior by mutating an
// argument. The argument crosses the boundary as an independent value, so
// the install lands in the host's copy and the script's hash is unchanged --
// there is nothing for a contract to bind and nothing to invoke.
func TestCapabilityCannotInstallABuiltinThroughAnArgument(t *testing.T) {
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
		t.Fatalf("expected the install to be invisible to the script")
	}
	requireErrorContains(t, err, "unknown hash method call")
	if invocations != 0 {
		t.Fatalf("a builtin installed into a host-side copy executed %d times, want never", invocations)
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

// TestYieldedBuiltinCannotBeDetached pins the removal fence on the one
// channel a host can still hand script a callable: a capability may yield a
// builtin to a block for direct use, but naming it as a value is an error,
// so no reference survives the call and the old retained-builtin contract
// sweeps have nothing left to guard.
func TestYieldedBuiltinCannotBeDetached(t *testing.T) {
	t.Parallel()

	uncontracted := 0
	script := compileScriptDefault(t, `def run()
  leaked = nil
  cap.factory do |fn|
    leaked = fn
    "fine"
  end
  leaked(42)
end`)
	_, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(yieldFactoryCapability{uncontractedCalls: &uncontracted}))
	requireErrorContains(t, err, "cannot be used as a value")
	if uncontracted != 0 {
		t.Fatalf("a detached builtin ran %d time(s), want never", uncontracted)
	}
}

// TestCapabilityYieldedBuiltinStaysUsableUnderContract pins the legitimate
// use: a yielded builtin called directly inside the block runs under its
// contract -- a conforming call succeeds and a violating one is rejected.
func TestCapabilityYieldedBuiltinStaysUsableUnderContract(t *testing.T) {
	t.Parallel()

	uncontracted := 0
	script := compileScriptDefault(t, `def run()
  out = nil
  cap.factory do |fn|
    out = fn("ok")
    "fine"
  end
  out
end`)
	result, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(yieldFactoryCapability{uncontractedCalls: &uncontracted}))
	if err != nil {
		t.Fatalf("a conforming direct call must succeed: %v", err)
	}
	if result.Kind() != KindString || result.String() != "invoked" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if uncontracted != 0 {
		t.Fatalf("contract counted %d uncontracted calls", uncontracted)
	}

	violating := compileScriptDefault(t, `def run()
  cap.factory do |fn|
    fn(42)
  end
end`)
	_, err = violating.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(yieldFactoryCapability{uncontractedCalls: &uncontracted}))
	requireErrorContains(t, err, "cap.made expects a single string argument")
	if uncontracted != 0 {
		t.Fatalf("a violating call ran uncontracted %d time(s)", uncontracted)
	}
}

// foreignFactoryCapability exposes a factory whose freshly created builtin is
// named like yieldFactoryCapability's contracted method. It stands in for an
// unrelated capability or host global that a block can call while a
// contracted capability drives it.
type foreignFactoryCapability struct {
	calls *int
}

func (c foreignFactoryCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	calls := c.calls
	return map[string]Value{
		"other": NewObject(map[string]Value{
			"make": NewBuiltin("other.make", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewBuiltin("cap.made", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					*calls = *calls + 1
					return NewString("foreign"), nil
				}), nil
			}),
		}),
	}, nil
}

// TestCapabilityContractsDoNotClaimForeignBuiltinsCreatedInBlocks pins that
// binding tracks what the capability actually yielded. A builtin another
// capability creates while the block runs shares a contracted name but was
// never published here, so this capability's validator must not be attached
// to it — doing so would reject valid calls and take scope ownership that
// prevents the correct binding later.
func TestCapabilityContractsDoNotClaimForeignBuiltinsCreatedInBlocks(t *testing.T) {
	t.Parallel()

	uncontracted := 0
	foreignCalls := 0
	script := compileScriptDefault(t, `def run()
  stolen = nil
  cap.factory do |fn|
    stolen = other.make()
    "fine"
  end
  stolen(42)
end`)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			yieldFactoryCapability{uncontractedCalls: &uncontracted},
			foreignFactoryCapability{calls: &foreignCalls},
		},
	})
	if err != nil {
		t.Fatalf("a foreign builtin must not inherit this capability's contract: %v", err)
	}
	if result.Kind() != KindString || result.String() != "foreign" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if foreignCalls != 1 {
		t.Fatalf("foreign builtin ran %d time(s), want 1", foreignCalls)
	}
}

// TestCapabilityContractsBindYieldsBeforeBlockRuns pins that a yielded
// builtin carries its contract while the block is still running. The block
// executes with the capability call still on the stack, so a contract
// attached only after that call returns arrives too late for a nested
// invocation made from inside the block.
func TestCapabilityContractsBindYieldsBeforeBlockRuns(t *testing.T) {
	t.Parallel()

	uncontracted := 0
	script := compileScriptDefault(t, `def run()
  cap.factory do |fn|
    fn(42)
  end
end`)
	_, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(yieldFactoryCapability{uncontractedCalls: &uncontracted}))
	requireErrorContains(t, err, "cap.made expects a single string argument")
	if uncontracted != 0 {
		t.Fatalf("yielded builtin ran without its contract %d time(s) inside the block", uncontracted)
	}
}

// TestCapabilityContractsDoNotClaimNestedScriptYields pins that a yield made
// by a script helper the block calls is not attributed to the capability. A
// script call does not change builtin nesting depth, so depth alone cannot
// tell the capability's own yield from one made by `def relay(v); yield v; end`
// running inside the block; claiming the latter would attach this
// capability's validator to an unrelated builtin and take scope ownership
// from whoever really published it.
func TestCapabilityContractsDoNotClaimNestedScriptYields(t *testing.T) {
	t.Parallel()

	uncontracted := 0
	foreignCalls := 0
	script := compileScriptDefault(t, `def relay()
  yield other.make()
end

def run()
  out = nil
  cap.factory do |fn|
    relay do |passed|
      out = passed(42)
    end
    "fine"
  end
  out
end`)
	result, err := script.Call(context.Background(), "run", nil, CallOptions{
		Capabilities: []CapabilityAdapter{
			yieldFactoryCapability{uncontractedCalls: &uncontracted},
			foreignFactoryCapability{calls: &foreignCalls},
		},
	})
	if err != nil {
		t.Fatalf("a nested script yield must not inherit this capability's contract: %v", err)
	}
	if result.Kind() != KindString || result.String() != "foreign" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if foreignCalls != 1 {
		t.Fatalf("foreign builtin ran %d time(s), want 1", foreignCalls)
	}
}
