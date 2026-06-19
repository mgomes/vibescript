package runtime

import (
	"context"
	"fmt"
)

// ScriptFunction represents a user-defined function within a Vibescript module.
type ScriptFunction struct {
	Name     string
	Params   []Param
	ReturnTy *TypeExpr
	Body     []Statement
	Pos      Position
	Env      *Env
	Exported bool
	Private  bool
	owner    *Script
}

// Script represents a parsed Vibescript module ready for execution.
type Script struct {
	engine              *Engine
	functions           map[string]*ScriptFunction
	classes             map[string]*ClassDef
	classOrder          []string
	deferredClassBodies map[string]struct{}
	enums               map[string]*EnumDef
	source              string
	moduleKey           string
	modulePath          string
	moduleRoot          string
}

// CallOptions configures globals, capabilities, and other settings for a script invocation.
type CallOptions struct {
	Globals      map[string]Value
	Capabilities []CapabilityAdapter
	AllowRequire bool
	Keywords     map[string]Value
}

// Execution holds the runtime state for a single script evaluation.
type Execution struct {
	engine                    *Engine
	script                    *Script
	ctx                       context.Context
	quota                     int
	memoryQuota               int
	recursionCap              int
	steps                     int
	callStack                 []callFrame
	root                      *Env
	modules                   map[string]Value
	moduleLoading             map[string]bool
	moduleLoadStack           []string
	moduleStack               []moduleContext
	capabilityContracts       map[*Builtin]CapabilityMethodContract
	capabilityContractScopes  map[*Builtin]*capabilityContractScope
	capabilityContractsByName map[string]CapabilityMethodContract
	receiverStack             []Value
	envStack                  []*Env
	activeTaskGroups          []*taskGroup
	validatedCapabilityArgs   []string
	memoryEst                 memoryEstimator

	// Inline backing storage for the always-used per-call stacks, so a
	// fresh Execution costs one allocation instead of one per stack.
	// Appends beyond these capacities spill to the heap as usual.
	callStackArr               [8]callFrame
	receiverStackArr           [8]Value
	envStackArr                [8]*Env
	validatedCapabilityArgsArr [4]string
	loopDepth                  int
	rescuedErrors              []error
	strictEffects              bool
	allowRequire               bool
	callOptions                CallOptions
}

type capabilityContractScope struct {
	contracts     map[string]CapabilityMethodContract
	roots         []Value
	knownBuiltins map[*Builtin]struct{}
}

type moduleContext struct {
	key    string
	path   string
	root   string
	script *Script
}

type callFrame struct {
	Function       string
	Pos            Position
	callSiteScript *Script
	functionScript *Script
}

func (exec *Execution) pushReceiver(v Value) {
	exec.receiverStack = append(exec.receiverStack, v)
}

func (exec *Execution) popReceiver() {
	if len(exec.receiverStack) == 0 {
		return
	}
	exec.receiverStack = exec.receiverStack[:len(exec.receiverStack)-1]
}

func (exec *Execution) currentReceiver() Value {
	if len(exec.receiverStack) == 0 {
		return NewNil()
	}
	return exec.receiverStack[len(exec.receiverStack)-1]
}

func (exec *Execution) isCurrentReceiver(v Value) bool {
	cur := exec.currentReceiver()
	switch {
	case v.Kind() == KindInstance && cur.Kind() == KindInstance:
		return valueInstance(v) == valueInstance(cur)
	case v.Kind() == KindClass && cur.Kind() == KindClass:
		return valueClass(v) == valueClass(cur)
	default:
		return false
	}
}

func (exec *Execution) pushFrame(function string, pos Position, callSiteScript, functionScript *Script) error {
	if exec.recursionCap > 0 && len(exec.callStack) >= exec.recursionCap {
		return exec.newRuntimeErrorWithType(runtimeErrorTypeLimit, fmt.Sprintf("recursion depth exceeded (limit %d)", exec.recursionCap), pos)
	}
	exec.callStack = append(exec.callStack, callFrame{
		Function:       function,
		Pos:            pos,
		callSiteScript: callSiteScript,
		functionScript: functionScript,
	})
	return nil
}

func (exec *Execution) popFrame() {
	if len(exec.callStack) == 0 {
		return
	}
	exec.callStack = exec.callStack[:len(exec.callStack)-1]
}

func (exec *Execution) pushValidatedCapabilityArgs(method string) func() {
	exec.validatedCapabilityArgs = append(exec.validatedCapabilityArgs, method)
	return func() {
		exec.validatedCapabilityArgs = exec.validatedCapabilityArgs[:len(exec.validatedCapabilityArgs)-1]
	}
}

func (exec *Execution) capabilityArgsValidated(method string) bool {
	for i := len(exec.validatedCapabilityArgs) - 1; i >= 0; i-- {
		if exec.validatedCapabilityArgs[i] == method {
			return true
		}
	}
	return false
}

func (exec *Execution) pushEnv(env *Env) {
	exec.envStack = append(exec.envStack, env)
}

func (exec *Execution) popEnv() {
	if len(exec.envStack) == 0 {
		return
	}
	exec.envStack = exec.envStack[:len(exec.envStack)-1]
}

func (exec *Execution) pushTaskGroup(group *taskGroup) {
	exec.activeTaskGroups = append(exec.activeTaskGroups, group)
}

func (exec *Execution) popTaskGroup() {
	if len(exec.activeTaskGroups) == 0 {
		return
	}
	exec.activeTaskGroups = exec.activeTaskGroups[:len(exec.activeTaskGroups)-1]
}

func (exec *Execution) pushModuleContext(ctx moduleContext) {
	exec.moduleStack = append(exec.moduleStack, ctx)
}

func (exec *Execution) popModuleContext() {
	if len(exec.moduleStack) == 0 {
		return
	}
	exec.moduleStack = exec.moduleStack[:len(exec.moduleStack)-1]
}

func (exec *Execution) currentModuleContext() *moduleContext {
	if len(exec.moduleStack) == 0 {
		return nil
	}
	ctx := exec.moduleStack[len(exec.moduleStack)-1]
	return &ctx
}

func (exec *Execution) currentSourceScript() *Script {
	if ctx := exec.currentModuleContext(); ctx != nil && ctx.script != nil {
		return ctx.script
	}
	return exec.script
}

func (exec *Execution) pushRescuedError(err error) {
	exec.rescuedErrors = append(exec.rescuedErrors, err)
}

func (exec *Execution) popRescuedError() {
	if len(exec.rescuedErrors) == 0 {
		return
	}
	exec.rescuedErrors = exec.rescuedErrors[:len(exec.rescuedErrors)-1]
}

func (exec *Execution) currentRescuedError() error {
	if len(exec.rescuedErrors) == 0 {
		return nil
	}
	return exec.rescuedErrors[len(exec.rescuedErrors)-1]
}

// Context returns the execution's bound context. Capability adapters
// that have been carved into sibling packages (vibes/capability/...)
// rely on it to forward cancellation and request-scoped values to host
// callbacks without reaching into unexported runtime fields.
func (exec *Execution) Context() context.Context {
	return exec.ctx
}

// Step accounts for one interpreter step against quota and memory
// limits and returns the deadline error when the script's context has
// been canceled. Capability adapters call it inside per-row loops so
// long-running host callbacks honor the same budget as in-script work.
func (exec *Execution) Step() error {
	return exec.step()
}
