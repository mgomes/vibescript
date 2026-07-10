package runtime

import (
	"context"
	"fmt"
	"math/rand"
)

type functionAccessorKind uint8

const (
	functionAccessorNone functionAccessorKind = iota
	functionAccessorGetter
	functionAccessorSetter
)

// ScriptFunction represents a user-defined function within a Vibescript module.
type ScriptFunction struct {
	Name         string
	Params       []Param
	ReturnTy     *TypeExpr
	Body         []Statement
	Pos          Position
	Env          *Env
	Exported     bool
	Private      bool
	Protected    bool
	Accessor     functionAccessorKind
	AccessorName string
	owner        *Script

	// reuseCallEnv records whether this function's call frame may be recycled
	// after the call returns (see functionCanReuseCallEnv). It is a pure function
	// of Params and Body, computed once at construction (compileFunctionDef and
	// the accessor builders) and never mutated afterward, so it is read lock-free
	// during calls without racing a ScriptFunction shared across goroutines.
	// Struct-copy clones (cloneFunctionForEnv, cloneFunctionForHostWithState)
	// inherit the flag; a body clone preserves capture structure, so the flag
	// stays valid. The zero value is false, which disables reuse — the safe
	// default for any construction site that forgets to set it.
	reuseCallEnv bool
}

// Script represents a parsed Vibescript module ready for execution.
type Script struct {
	engine              *Engine
	entrypoint          string
	functions           map[string]*ScriptFunction
	classes             map[string]*ClassDef
	classOrder          []string
	deferredClassBodies map[string]struct{}
	enums               map[string]*EnumDef
	symbolLiterals      map[*SymbolLiteral]Value
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
	reservedScratchBytes      int

	// baseWalkCache memoizes the reachable-graph portion of the memory
	// estimator's base walk (see beginBaseWalk). It is allocated lazily on the
	// first memoizable check, so executions that never reach one — no memory
	// quota, or only memo-bypassed checks — pay neither the struct nor its
	// journal backing. baseWalkOpen marks a base-walk session in flight (both
	// memoized and bypass sessions), guarding against nested sessions on the
	// shared estimator without touching the cache. baseTopoVersion invalidates
	// the memo whenever the walk's root set changes shape (env stack push/pop,
	// task group push/pop); the process-wide mutation epoch invalidates it
	// whenever any reachable state mutates. builtinDepth counts the Go builtins
	// currently on the call stack: while it is non-zero the memo is neither
	// used nor refreshed, because builtin Go code can mutate containers through
	// raw slice/map writes between its own memory checks without bumping the
	// epoch.
	baseWalkCache   *baseWalkCache
	baseWalkOpen    bool
	baseTopoVersion uint64
	builtinDepth    int

	// Dormant-frame accounting (see memory_dormant.go) makes the estimator's
	// env-stack walk incremental for block-free function recursion. dormant holds
	// the committed dormant prefix, dormantSet the same frames for O(1) dedup
	// during a walk, dormantBytes their cached byte sum, and dormantSlots the
	// number of leading env-stack slots they cover. nonBaseParentDepth counts the
	// active scopes whose parent is not a base env (blocks, closures, nested
	// lexical scopes); while it is non-zero the optimization disengages because
	// such a scope could rebind a dormant frame. All are maintained only under a
	// positive memory quota, since an unlimited quota skips the estimator walk.
	dormant            []dormantFrame
	dormantSet         map[*Env]struct{}
	dormantBytes       int
	dormantSlots       int
	nonBaseParentDepth int

	// accumMeteredSections counts the accumulator-metered sections currently
	// active (see beginAccumulatorMeteredSection). While non-zero, step()'s
	// periodic slow path skips the full reachable-graph memory walk: an
	// active section is a blockless native build loop that charges every
	// allocation against the quota before performing it, so the walk's answer
	// cannot change while one runs. Script re-entry and nested builtin
	// dispatch suspend the counter (see callBlock, callFunctionWithBoundEnv,
	// and the builtin dispatch in invokeCallable), so a section can never
	// weaken checks across code it does not meter.
	accumMeteredSections int

	// Block-iteration region accounting (see memory_blockregion.go). A pure
	// block driver (map/select/group_by/reduce/each and kin) marks the
	// env-stack prefix beneath its block scopes as stable for the duration of
	// its iteration. While a region is active the per-check base walk memoizes
	// that prefix once and re-walks only the block's own scopes (the active
	// suffix) each check, instead of re-charging the whole — often large —
	// input collection every periodic check. That turns quota-metered block
	// iteration from O(n^2) back to O(n). blockRegionActive is the switch;
	// blockRegionBoundary is the env-stack length at the outermost active
	// region's start (scopes at or above it are the region, walked fresh);
	// blockRegionBuiltinDepth is builtinDepth at the innermost active region's
	// driver, so the memo is engaged only in the block body that region drives
	// and not inside a deeper builtin whose raw writes the epoch cannot
	// observe. All are maintained only under a positive memory quota.
	blockRegionActive       bool
	blockRegionBoundary     int
	blockRegionBuiltinDepth int

	// Inline backing storage for the always-used per-call stacks, so a
	// fresh Execution costs one allocation instead of one per stack.
	// Appends beyond these capacities spill to the heap as usual.
	callStackArr               [8]callFrame
	receiverStackArr           [8]Value
	envStackArr                [8]*Env
	validatedCapabilityArgsArr [4]string
	loopDepth                  int
	blockDepth                 int
	lambdaDepth                int
	rescueDepth                int
	rescuedErrors              []error
	returnTokens               []uint64
	homeTokens                 []uint64
	localCallBypassStack       []localCallBypass
	randSource                 *rand.Rand
	randSeed                   int64
	randSeeded                 bool
	strictEffects              bool
	allowRequire               bool
	callOptions                CallOptions

	// argBufferPool is a free list of positional-argument backing slices,
	// reused across script-function calls. A call to a script function borrows
	// a buffer to evaluate its arguments into and returns it once the call has
	// fully unwound; bindFunctionArgs copies every element into the callee's
	// environment and never retains the slice, so the backing is free to reuse.
	// Only KindFunction calls pool: builtins and capabilities can retain the
	// args slice they are handed.
	argBufferPool [][]Value

	// callEnvFreeList is a free list of call-frame environments, reused across
	// script-function calls whose bodies provably cannot capture the frame
	// (ScriptFunction.reuseCallEnv). acquireCallEnv borrows a frame and
	// recycleCallEnv returns it once the call has fully unwound and any escaping
	// array-append result has settled. Like argBufferPool it lives on the
	// Execution, which is single-goroutine (each task job builds its own via
	// newExecutionForCall), so the pool never needs synchronization. The memory
	// estimator does not walk it, so pooled dead frames never inflate the quota.
	callEnvFreeList []*Env
}

type localCallBypass struct {
	bindings map[string]*Env
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

// protectedInstanceAccessAllowed reports whether the currently executing
// method may invoke a protected instance method defined on cl: the caller's
// self must itself be an instance of cl, mirroring Ruby's rule that protected
// methods are callable between instances of the same class (Vibescript has no
// inheritance, so the class itself is the whole family).
func (exec *Execution) protectedInstanceAccessAllowed(cl *ClassDef) bool {
	cur := exec.currentReceiver()
	return cur.Kind() == KindInstance && valueInstance(cur).Class == cl
}

// protectedClassAccessAllowed reports whether the currently executing method
// may invoke a protected class method defined on cl: the caller's self must
// be the class itself, i.e. the call happens inside another class method of
// the same class.
func (exec *Execution) protectedClassAccessAllowed(cl *ClassDef) bool {
	cur := exec.currentReceiver()
	return cur.Kind() == KindClass && valueClass(cur) == cl
}

// assignTargetProtectedAccessAllowed applies the protected rules to a setter
// assignment target, which may be an instance (protected instance setter) or
// a class (protected class setter).
func (exec *Execution) assignTargetProtectedAccessAllowed(obj Value) bool {
	switch obj.Kind() {
	case KindInstance:
		return exec.protectedInstanceAccessAllowed(valueInstance(obj).Class)
	case KindClass:
		return exec.protectedClassAccessAllowed(valueClass(obj))
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
	if exec.blockRegionActive && env != nil {
		// Every scope pushed inside an active block-iteration region is part of
		// the region's active suffix: the base walk re-walks it fresh each check
		// (see memory_blockregion.go), so its binding writes are epoch-neutral
		// and its push neither changes the memoized prefix nor the dormant
		// prefix's shape. Skipping the topo bump keeps the prefix memo valid
		// across every iteration; popEnv skips it symmetrically under the same
		// blockRegionActive condition, so the counters stay balanced regardless
		// of a mid-region capture stripping neutrality.
		//
		// Restore neutrality unless a closure already captured this frame during
		// pre-push argument or default binding: revokeBlockRegionNeutrality
		// cleared it so the escaped frame's later writes stay charged, and
		// blindly re-neutralizing here would undercount them (a security-boundary
		// escape). The scope was marked neutral at acquisition, so this only
		// re-asserts it for the common uncaptured case.
		if !env.neutralityRevoked {
			env.epochNeutral = true
		}
		exec.envStack = append(exec.envStack, env)
		return
	}
	exec.baseTopoVersion++
	if exec.memoryQuota > 0 && env != nil && !exec.isBaseEnv(env.parent) {
		exec.nonBaseParentDepth++
	}
	exec.envStack = append(exec.envStack, env)
}

func (exec *Execution) currentEnv() *Env {
	if len(exec.envStack) == 0 {
		return nil
	}
	return exec.envStack[len(exec.envStack)-1]
}

func (exec *Execution) popEnv() {
	if len(exec.envStack) == 0 {
		return
	}
	env := exec.envStack[len(exec.envStack)-1]
	if exec.blockRegionActive && env != nil {
		// A block-iteration region scope: its push skipped the topo and
		// non-base-parent bookkeeping (see pushEnv), so its pop must too. The
		// decision is keyed on blockRegionActive, not the scope's epochNeutral
		// flag, so it holds even when a mid-region capture revoked neutrality —
		// push and pop of one scope always see the same region state, so the
		// skips stay balanced. Clear the transient neutrality so a scope that
		// escapes the region (captured by a closure and re-pushed later, perhaps
		// outside any region) does not carry it past the region that justified it.
		//
		// Do NOT clear neutralityRevoked here: a capture is a property of the frame
		// for its whole lifetime, not of a single push. A call frame is pushed
		// twice — once for the pre-body memory check, once for the body — and the
		// revocation, which happens during pre-push binding, must survive the
		// intervening pop or the body push would re-neutralize the escaped frame
		// and undercount its later writes. The flag is reset per lifetime at frame
		// acquisition (markRegionNeutral). Leaving it set at worst over-charges a
		// reused scope, never undercounts.
		env.epochNeutral = false
		exec.envStack = exec.envStack[:len(exec.envStack)-1]
		return
	}
	exec.baseTopoVersion++
	if exec.memoryQuota > 0 && env != nil && !exec.isBaseEnv(env.parent) {
		if estimatorVerify && exec.nonBaseParentDepth <= 0 {
			// This decrement must pair with an increment from the same scope's
			// push. An underflow means a scope was pushed and popped through
			// asymmetric branches — the failure mode when the region skip was keyed
			// on a flag a mid-region capture could revoke — which can later drive
			// the counter back to zero while a non-base scope is live and wrongly
			// re-engage the dormant-frame memo. Trip loudly under the oracle.
			panic("runtime: nonBaseParentDepth underflow on env pop")
		}
		exec.nonBaseParentDepth--
	}
	exec.envStack = exec.envStack[:len(exec.envStack)-1]
	if len(exec.dormant) > 0 {
		exec.retractDormantBeyond(len(exec.envStack))
	}
}

func (exec *Execution) pushTaskGroup(group *taskGroup) {
	exec.baseTopoVersion++
	exec.activeTaskGroups = append(exec.activeTaskGroups, group)
}

func (exec *Execution) popTaskGroup() {
	if len(exec.activeTaskGroups) == 0 {
		return
	}
	exec.baseTopoVersion++
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
