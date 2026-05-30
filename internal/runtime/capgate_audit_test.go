package runtime

// White-box audit test for the capability-contract scan gate.
//
// Finding: capability-scan-skips-function-closures (CWE-862).
// The three scan helpers that walk values for capability builtins
// (valueCanContainBuiltins, capabilityContractScanner.collectBuiltins,
// capabilityContractScanner.bindContracts) previously omitted the
// KindFunction case. A *Builtin captured inside a script closure's
// environment was therefore invisible to the scan: its declared contract
// never bound, yet the builtin stayed callable through the closure.
//
// The fix adds a KindFunction case to all three helpers so a builtin
// captured in a closure's environment is discovered and contract-bound
// like any other reachable builtin. These tests pin the SECURE (fixed)
// behavior: the control test shows an object-nested builtin IS seen, and
// the closure test shows a closure-captured builtin is NOW seen too.
//
// The tests are white-box: they build runtime values directly and call
// the unexported scan helpers, so they live in package runtime.

import (
	"context"
	"testing"
)

// makeContractBuiltin returns a builtin Value, its *Builtin, and a
// capability contract scope that declares a contract for that method.
func makeContractBuiltin(name string) (Value, *Builtin, *capabilityContractScope) {
	bv := NewBuiltin(name, func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	builtin := valueBuiltin(bv)
	scope := &capabilityContractScope{
		contracts: map[string]CapabilityMethodContract{
			name: {
				ValidateArgs: func(_ []Value, _ map[string]Value, _ Value) error {
					return nil
				},
			},
		},
		knownBuiltins: make(map[*Builtin]struct{}),
	}
	return bv, builtin, scope
}

// TestCapabilityScanControlSeesObjectBuiltin is the control: a builtin
// nested inside an object value IS discovered by both scan helpers, and
// its contract binds. This path already includes KindObject, so it holds
// before and after the fix.
func TestCapabilityScanControlSeesObjectBuiltin(t *testing.T) {
	bv, builtin, scope := makeContractBuiltin("secret")
	obj := NewObject(map[string]Value{"secret": bv})

	if !valueCanContainBuiltins(obj) {
		t.Fatalf("control: valueCanContainBuiltins(object) = false, want true")
	}

	scanner := newCapabilityContractScanner()
	found := map[*Builtin]struct{}{}
	scanner.collectBuiltins(obj, found)
	if _, ok := found[builtin]; !ok {
		t.Fatalf("control: collectBuiltins did not find object-nested builtin")
	}

	target := map[*Builtin]CapabilityMethodContract{}
	scopes := map[*Builtin]*capabilityContractScope{}
	bindCapabilityContracts(obj, scope, target, scopes)
	if _, ok := target[builtin]; !ok {
		t.Fatalf("control: bindContracts did not bind object-nested builtin contract")
	}
}

// TestCapabilityScanNowSeesClosureBuiltin verifies the fix: a builtin
// captured in a closure's environment IS discovered by the scan helpers,
// and its contract binds -- matching the behavior for builtins reachable
// through objects, arrays, instances, etc. This test passes on the fixed
// source and would fail on the unfixed source (where the KindFunction
// case is absent).
func TestCapabilityScanNowSeesClosureBuiltin(t *testing.T) {
	bv, builtin, scope := makeContractBuiltin("secret")

	// Build a closure whose captured env holds the contract builtin.
	env := newEnv(nil)
	env.Define("secret", bv)
	fn := &ScriptFunction{
		Name: "closure",
		Env:  env,
	}
	closure := NewFunction(fn)

	// Candidate assertions (pin the SECURE behavior -- closure-captured
	// builtin is now visible to the scan and its contract binds):
	if !valueCanContainBuiltins(closure) {
		t.Fatalf("expected valueCanContainBuiltins(closure)=true (fixed), got false")
	}

	scanner := newCapabilityContractScanner()
	found := map[*Builtin]struct{}{}
	scanner.collectBuiltins(closure, found)
	if len(found) != 1 {
		t.Fatalf("expected collectBuiltins to find 1 closure-captured builtin (fixed), got %d", len(found))
	}
	if _, ok := found[builtin]; !ok {
		t.Fatalf("expected collectBuiltins to find the closure-captured builtin (fixed)")
	}

	target := map[*Builtin]CapabilityMethodContract{}
	scopes := map[*Builtin]*capabilityContractScope{}
	bindCapabilityContracts(closure, scope, target, scopes)
	if len(target) != 1 {
		t.Fatalf("expected bindContracts to bind 1 closure-captured contract (fixed), got %d", len(target))
	}
	if _, ok := target[builtin]; !ok {
		t.Fatalf("expected bindContracts to bind the secret method's contract (fixed)")
	}

	// Reachability sanity check: the builtin really is callable via env.
	if got, ok := env.Get("secret"); !ok || got.Kind() != KindBuiltin {
		t.Fatalf("expected closure env to expose callable builtin, got ok=%v kind=%v", ok, got.Kind())
	}
}

// TestCapabilityScanNowSeesBlockBuiltin verifies the Codex P2 follow-up on
// PR #119: blocks are closures too (NewBlock stores an Env, and capability
// adapters can execute them via Execution.CallBlock), so a builtin captured in
// a block's environment must be discovered and contract-bound just like one in
// a script function's closure.
func TestCapabilityScanNowSeesBlockBuiltin(t *testing.T) {
	bv, builtin, scope := makeContractBuiltin("secret")

	env := newEnv(nil)
	env.Define("secret", bv)
	block := NewBlock(nil, nil, env)

	if !valueCanContainBuiltins(block) {
		t.Fatalf("expected valueCanContainBuiltins(block)=true (fixed), got false")
	}

	scanner := newCapabilityContractScanner()
	found := map[*Builtin]struct{}{}
	scanner.collectBuiltins(block, found)
	if _, ok := found[builtin]; !ok {
		t.Fatalf("expected collectBuiltins to find the block-captured builtin (fixed)")
	}

	target := map[*Builtin]CapabilityMethodContract{}
	scopes := map[*Builtin]*capabilityContractScope{}
	bindCapabilityContracts(block, scope, target, scopes)
	if _, ok := target[builtin]; !ok {
		t.Fatalf("expected bindContracts to bind the block-captured builtin contract (fixed)")
	}
}

// TestCapabilityScanSkipsAmbientGlobalsInBlock mirrors the closure ambient-skip
// guard for blocks: a block whose env chains into the ambient root must not bind
// a contract to an unrelated same-named global builtin.
func TestCapabilityScanSkipsAmbientGlobalsInBlock(t *testing.T) {
	_, _, scope := makeContractBuiltin("secret")
	ambientVal := NewBuiltin("secret", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})

	root := newEnv(nil)
	root.Define("secret", ambientVal)

	childEnv := newEnv(root)
	block := NewBlock(nil, nil, childEnv)

	scanner := newCapabilityContractScanner()
	scanner.ambientEnvs = ambientEnvSet(root)
	target := map[*Builtin]CapabilityMethodContract{}
	scopes := map[*Builtin]*capabilityContractScope{}
	scanner.bindContracts(block, scope, target, scopes)
	if len(target) != 0 {
		t.Fatalf("over-bind regression: block walk bound %d ambient-global contract(s), want 0 [Codex P2 #119]", len(target))
	}

	collector := newCapabilityContractScanner()
	collector.ambientEnvs = ambientEnvSet(root)
	found := map[*Builtin]struct{}{}
	collector.collectBuiltins(block, found)
	if _, ok := found[valueBuiltin(ambientVal)]; ok {
		t.Fatalf("over-bind regression: collectBuiltins surfaced an ambient global builtin via block [Codex P2 #119]")
	}
}

// TestCapabilityScanSkipsAmbientGlobalsInClosure pins the fix for the Codex P2
// on PR #119. When the closure-env walk follows the lexical parent chain into
// the ambient global environment, an UNRELATED global builtin whose name happens
// to match a capability contract method must NOT be bound to that scope --
// otherwise a script-supplied closure could attach a capability contract to an
// arbitrary same-named global (CWE-862 regression).
func TestCapabilityScanSkipsAmbientGlobalsInClosure(t *testing.T) {
	_, _, scope := makeContractBuiltin("secret")
	ambientVal := NewBuiltin("secret", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})

	// Ambient root env holds the unrelated global builtin named "secret".
	root := newEnv(nil)
	root.Define("secret", ambientVal)

	// A closure whose env is a child frame of the ambient root, capturing no
	// capability value of its own.
	childEnv := newEnv(root)
	closure := NewFunction(&ScriptFunction{Name: "closure", Env: childEnv})

	scanner := newCapabilityContractScanner()
	scanner.ambientEnvs = ambientEnvSet(root)
	target := map[*Builtin]CapabilityMethodContract{}
	scopes := map[*Builtin]*capabilityContractScope{}
	scanner.bindContracts(closure, scope, target, scopes)
	if len(target) != 0 {
		t.Fatalf("over-bind regression: closure walk bound %d ambient-global contract(s), want 0 [Codex P2 #119]", len(target))
	}

	collector := newCapabilityContractScanner()
	collector.ambientEnvs = ambientEnvSet(root)
	found := map[*Builtin]struct{}{}
	collector.collectBuiltins(closure, found)
	if _, ok := found[valueBuiltin(ambientVal)]; ok {
		t.Fatalf("over-bind regression: collectBuiltins surfaced an ambient global builtin [Codex P2 #119]")
	}

	// Control: a capability-OWNED builtin captured in the closure's own
	// (non-ambient) frame IS still bound -- the original finding fix stays intact.
	capVal, capBuiltin, capScope := makeContractBuiltin("op")
	ownFrame := newEnv(root)
	ownFrame.Define("op", capVal)
	ownClosure := NewFunction(&ScriptFunction{Name: "factoryClosure", Env: ownFrame})

	scanner2 := newCapabilityContractScanner()
	scanner2.ambientEnvs = ambientEnvSet(root)
	target2 := map[*Builtin]CapabilityMethodContract{}
	scopes2 := map[*Builtin]*capabilityContractScope{}
	scanner2.bindContracts(ownClosure, capScope, target2, scopes2)
	if _, ok := target2[capBuiltin]; !ok {
		t.Fatalf("fix overcorrected: a capability builtin in the closure's own frame was not bound")
	}
}

// ambientClosureCapability returns a single global closure whose captured
// environment chains into the ambient call root, and declares a contract for a
// method name that collides with a pre-existing global builtin in that root.
type ambientClosureCapability struct {
	globalName  string
	closure     Value
	contractFor string
}

func (c ambientClosureCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{c.globalName: c.closure}, nil
}

func (c ambientClosureCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		c.contractFor: {
			ValidateArgs: func(_ []Value, _ map[string]Value, _ Value) error {
				return nil
			},
		},
	}
}

// TestBindCapabilitiesForCallSkipsAmbientClosureGlobals pins the Codex P2 on
// PR #119 for the INITIAL capability-binding path. bindCapabilitiesForCall runs
// after the root env is seeded with stdlib/global builtins, so a capability that
// exposes a closure whose env chains into that root must not bind its contract to
// an unrelated same-named ambient global. The pre/post-call scanners already pass
// ambientEnvSet(root); this verifies the initial binding scan does too.
func TestBindCapabilitiesForCallSkipsAmbientClosureGlobals(t *testing.T) {
	script := compileScriptDefault(t, "def run()\n  nil\nend")

	root := newEnv(nil)
	ambientVal := NewBuiltin("secret", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	root.Define("secret", ambientVal)

	// A capability-supplied closure that captures nothing of its own but whose
	// env parent is the ambient root holding the unrelated "secret" builtin.
	childEnv := newEnv(root)
	closure := NewFunction(&ScriptFunction{Name: "exposed", Env: childEnv})

	adapter := ambientClosureCapability{
		globalName:  "exposed",
		closure:     closure,
		contractFor: "secret",
	}

	rebinder := newCallFunctionRebinder(script, root, map[string]*ClassDef{}, map[string]*EnumDef{})
	exec := newExecutionForCall(script, context.Background(), root, CallOptions{})

	if err := bindCapabilitiesForCall(exec, root, rebinder, []CapabilityAdapter{adapter}); err != nil {
		t.Fatalf("bindCapabilitiesForCall: %v", err)
	}

	if _, bound := exec.capabilityContracts[valueBuiltin(ambientVal)]; bound {
		t.Fatalf("over-bind regression: initial binding path attached a contract to an ambient global builtin [Codex P2 #119]")
	}
}

// TestInvokeCallableSnapshotsBlockBuiltins pins the Codex P2 follow-up on
// PR #119: now that block environments are traversed for contract binding, a
// builtin the caller already captured in a script block must be recorded in the
// pre-call known-builtin snapshot. Otherwise, when a contracted capability
// returns that same block, the post-call scan treats the captured builtin as
// newly published and binds the capability's contract to it -- even though the
// builtin was supplied by the caller, not exposed by the capability.
//
// White-box: the victim builtin is reachable ONLY through the block, with no
// prior owner scope, so the post-call block scan is its first contract-binding
// encounter -- the one case the snapshot is load-bearing for (the owner-scope
// guard already protects builtins owned by another scope).
func TestInvokeCallableSnapshotsBlockBuiltins(t *testing.T) {
	script := compileScriptDefault(t, "def run()\n  nil\nend")
	root := newEnv(nil)
	exec := newExecutionForCall(script, context.Background(), root, CallOptions{})
	exec.capabilityContracts = map[*Builtin]CapabilityMethodContract{}
	exec.capabilityContractScopes = map[*Builtin]*capabilityContractScope{}

	// A builtin named "victim" captured only inside a block's environment.
	victimVal := NewBuiltin("victim", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewNil(), nil
	})
	blockEnv := newEnv(root)
	blockEnv.Define("captured", victimVal)
	block := NewBlock(nil, nil, blockEnv)

	// The contracted capability method declares a contract for "victim" and
	// returns the caller-supplied block verbatim.
	keepVal := NewBuiltin("keeper.keep", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, blk Value) (Value, error) {
		return blk, nil
	})
	keep := valueBuiltin(keepVal)
	scope := &capabilityContractScope{
		contracts: map[string]CapabilityMethodContract{
			"victim": {
				ValidateArgs: func(_ []Value, _ map[string]Value, _ Value) error {
					return nil
				},
			},
		},
		knownBuiltins: map[*Builtin]struct{}{},
		roots:         []Value{keepVal},
	}
	exec.capabilityContractScopes[keep] = scope

	if _, err := exec.invokeCallable(keepVal, NewNil(), nil, nil, block, Position{}); err != nil {
		t.Fatalf("invokeCallable: %v", err)
	}

	if _, bound := exec.capabilityContracts[valueBuiltin(victimVal)]; bound {
		t.Fatalf("hijack regression: a caller's block-captured builtin was contract-bound by the capability [Codex P2 #119]")
	}
}
