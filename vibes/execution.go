package vibes

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

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

type Script struct {
	engine     *Engine
	functions  map[string]*ScriptFunction
	classes    map[string]*ClassDef
	source     string
	moduleKey  string
	modulePath string
	moduleRoot string
}

type CallOptions struct {
	Globals      map[string]Value
	Capabilities []CapabilityAdapter
	AllowRequire bool
	Keywords     map[string]Value
}

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
	loopDepth                 int
	rescuedErrors             []error
	strictEffects             bool
	allowRequire              bool
}

type capabilityContractScope struct {
	contracts map[string]CapabilityMethodContract
	roots     []Value
}

type moduleContext struct {
	key  string
	path string
	root string
}

type callFrame struct {
	Function string
	Pos      Position
}

type StackFrame struct {
	Function string
	Pos      Position
}

type RuntimeError struct {
	Type      string
	Message   string
	CodeFrame string
	Frames    []StackFrame
}

type assertionFailureError struct {
	message string
}

func (e *assertionFailureError) Error() string {
	return e.message
}

const (
	runtimeErrorTypeBase      = "RuntimeError"
	runtimeErrorTypeAssertion = "AssertionError"
	runtimeErrorFrameHead     = 8
	runtimeErrorFrameTail     = 8
)

var (
	errLoopBreak           = errors.New("loop break")
	errLoopNext            = errors.New("loop next")
	errStepQuotaExceeded   = errors.New("step quota exceeded")
	errMemoryQuotaExceeded = errors.New("memory quota exceeded")
	stringTemplatePattern  = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)
)

func (re *RuntimeError) Error() string {
	var b strings.Builder
	b.WriteString(re.Message)
	if re.CodeFrame != "" {
		b.WriteString("\n")
		b.WriteString(re.CodeFrame)
	}
	renderFrame := func(frame StackFrame) {
		if frame.Pos.Line > 0 && frame.Pos.Column > 0 {
			fmt.Fprintf(&b, "\n  at %s (%d:%d)", frame.Function, frame.Pos.Line, frame.Pos.Column)
		} else if frame.Pos.Line > 0 {
			fmt.Fprintf(&b, "\n  at %s (line %d)", frame.Function, frame.Pos.Line)
		} else {
			fmt.Fprintf(&b, "\n  at %s", frame.Function)
		}
	}

	if len(re.Frames) <= runtimeErrorFrameHead+runtimeErrorFrameTail {
		for _, frame := range re.Frames {
			renderFrame(frame)
		}
		return b.String()
	}

	for _, frame := range re.Frames[:runtimeErrorFrameHead] {
		renderFrame(frame)
	}
	omitted := len(re.Frames) - (runtimeErrorFrameHead + runtimeErrorFrameTail)
	fmt.Fprintf(&b, "\n  ... %d frames omitted ...", omitted)
	for _, frame := range re.Frames[len(re.Frames)-runtimeErrorFrameTail:] {
		renderFrame(frame)
	}

	return b.String()
}

// Unwrap returns nil to satisfy the error unwrapping interface.
// RuntimeError is a terminal error that wraps the original error message but not the error itself.
func (re *RuntimeError) Unwrap() error {
	return nil
}

func canonicalRuntimeErrorType(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, runtimeErrorTypeBase), strings.EqualFold(name, "Error"):
		return runtimeErrorTypeBase, true
	case strings.EqualFold(name, runtimeErrorTypeAssertion):
		return runtimeErrorTypeAssertion, true
	default:
		return "", false
	}
}

func classifyRuntimeErrorType(err error) string {
	if err == nil {
		return runtimeErrorTypeBase
	}
	var assertionErr *assertionFailureError
	if errors.As(err, &assertionErr) {
		return runtimeErrorTypeAssertion
	}
	if runtimeErr, ok := err.(*RuntimeError); ok {
		if kind, known := canonicalRuntimeErrorType(runtimeErr.Type); known {
			return kind
		}
	}
	return runtimeErrorTypeBase
}

func newAssertionFailureError(message string) error {
	return &assertionFailureError{message: message}
}

func (exec *Execution) step() error {
	exec.steps++
	if exec.quota > 0 && exec.steps > exec.quota {
		return fmt.Errorf("%w (%d)", errStepQuotaExceeded, exec.quota)
	}
	if exec.memoryQuota > 0 && (exec.steps&15) == 0 {
		if err := exec.checkMemory(); err != nil {
			return err
		}
	}
	if exec.ctx != nil {
		select {
		case <-exec.ctx.Done():
			return exec.ctx.Err()
		default:
		}
	}
	return nil
}

func (exec *Execution) errorAt(pos Position, format string, args ...any) error {
	return exec.newRuntimeError(fmt.Sprintf(format, args...), pos)
}

func (exec *Execution) newRuntimeError(message string, pos Position) error {
	return exec.newRuntimeErrorWithType(runtimeErrorTypeBase, message, pos)
}

func (exec *Execution) newRuntimeErrorWithType(kind string, message string, pos Position) error {
	if canonical, ok := canonicalRuntimeErrorType(kind); ok {
		kind = canonical
	} else {
		kind = runtimeErrorTypeBase
	}

	frames := make([]StackFrame, 0, len(exec.callStack)+1)

	if len(exec.callStack) > 0 {
		// First frame: where the error occurred (within the current function)
		current := exec.callStack[len(exec.callStack)-1]
		frames = append(frames, StackFrame{Function: current.Function, Pos: pos})

		// Remaining frames: the call stack (where each function was called from)
		for i := len(exec.callStack) - 1; i >= 0; i-- {
			cf := exec.callStack[i]
			frames = append(frames, StackFrame(cf))
		}
	} else {
		// No call stack means error at script top level
		frames = append(frames, StackFrame{Function: "<script>", Pos: pos})
	}
	codeFrame := ""
	if exec.script != nil {
		codeFrame = formatCodeFrame(exec.script.source, pos)
	}
	return &RuntimeError{Type: kind, Message: message, CodeFrame: codeFrame, Frames: frames}
}

func (exec *Execution) wrapError(err error, pos Position) error {
	if err == nil {
		return nil
	}
	if isHostControlSignal(err) {
		return err
	}
	if _, ok := err.(*RuntimeError); ok {
		return err
	}
	return exec.newRuntimeErrorWithType(classifyRuntimeErrorType(err), err.Error(), pos)
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
		return v.Instance() == cur.Instance()
	case v.Kind() == KindClass && cur.Kind() == KindClass:
		return v.Class() == cur.Class()
	default:
		return false
	}
}

func (exec *Execution) pushFrame(function string, pos Position) error {
	if exec.recursionCap > 0 && len(exec.callStack) >= exec.recursionCap {
		return exec.errorAt(pos, "recursion depth exceeded (limit %d)", exec.recursionCap)
	}
	exec.callStack = append(exec.callStack, callFrame{Function: function, Pos: pos})
	return nil
}

func (exec *Execution) popFrame() {
	if len(exec.callStack) == 0 {
		return
	}
	exec.callStack = exec.callStack[:len(exec.callStack)-1]
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

func (exec *Execution) evalStatements(stmts []Statement, env *Env) (Value, bool, error) {
	exec.pushEnv(env)
	defer exec.popEnv()

	result := NewNil()
	for _, stmt := range stmts {
		if err := exec.step(); err != nil {
			return NewNil(), false, err
		}
		val, returned, err := exec.evalStatement(stmt, env)
		if err != nil {
			return NewNil(), false, err
		}
		if _, isAssign := stmt.(*AssignStmt); isAssign {
			if err := exec.checkMemory(); err != nil {
				return NewNil(), false, err
			}
		} else {
			if err := exec.checkMemoryWith(val); err != nil {
				return NewNil(), false, err
			}
		}
		if returned {
			return val, true, nil
		}
		result = val
	}
	if err := exec.checkMemory(); err != nil {
		return NewNil(), false, err
	}
	return result, false, nil
}

func (exec *Execution) evalStatement(stmt Statement, env *Env) (Value, bool, error) {
	switch s := stmt.(type) {
	case *ExprStmt:
		val, err := exec.evalExpression(s.Expr, env)
		return val, false, err
	case *ReturnStmt:
		val, err := exec.evalExpression(s.Value, env)
		return val, true, err
	case *RaiseStmt:
		return exec.evalRaiseStatement(s, env)
	case *AssignStmt:
		val, err := exec.evalExpression(s.Value, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), false, err
		}
		if err := exec.assign(s.Target, val, env); err != nil {
			return NewNil(), false, err
		}
		return val, false, nil
	case *IfStmt:
		val, err := exec.evalExpression(s.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), false, err
		}
		if val.Truthy() {
			return exec.evalStatements(s.Consequent, env)
		}
		for _, clause := range s.ElseIf {
			condVal, err := exec.evalExpression(clause.Condition, env)
			if err != nil {
				return NewNil(), false, err
			}
			if err := exec.checkMemoryWith(condVal); err != nil {
				return NewNil(), false, err
			}
			if condVal.Truthy() {
				return exec.evalStatements(clause.Consequent, env)
			}
		}
		if len(s.Alternate) > 0 {
			return exec.evalStatements(s.Alternate, env)
		}
		return NewNil(), false, nil
	case *ForStmt:
		return exec.evalForStatement(s, env)
	case *WhileStmt:
		return exec.evalWhileStatement(s, env)
	case *UntilStmt:
		return exec.evalUntilStatement(s, env)
	case *BreakStmt:
		if exec.loopDepth == 0 {
			return NewNil(), false, exec.errorAt(s.Pos(), "break used outside of loop")
		}
		return NewNil(), false, errLoopBreak
	case *NextStmt:
		if exec.loopDepth == 0 {
			return NewNil(), false, exec.errorAt(s.Pos(), "next used outside of loop")
		}
		return NewNil(), false, errLoopNext
	case *TryStmt:
		return exec.evalTryStatement(s, env)
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "unsupported statement")
	}
}

func (exec *Execution) assignToMember(obj Value, property string, value Value, pos Position) error {
	setterName := property + "="
	var methods map[string]*ScriptFunction
	var vars map[string]Value

	switch obj.Kind() {
	case KindInstance:
		methods = obj.Instance().Class.Methods
		vars = obj.Instance().Ivars
	case KindClass:
		methods = obj.Class().ClassMethods
		vars = obj.Class().ClassVars
	default:
		return exec.errorAt(pos, "cannot assign to %s", obj.Kind())
	}

	if fn, ok := methods[setterName]; ok {
		if fn.Private && !exec.isCurrentReceiver(obj) {
			return exec.errorAt(pos, "private method %s", setterName)
		}
		_, err := exec.callFunction(fn, obj, []Value{value}, nil, NewNil(), pos)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return exec.errorAt(pos, "next cannot cross call boundary")
			}
		}
		return err
	}

	if _, hasGetter := methods[property]; hasGetter {
		return exec.errorAt(pos, "cannot assign to read-only property %s", property)
	}

	vars[property] = value
	return nil
}

func (exec *Execution) assign(target Expression, value Value, env *Env) error {
	switch t := target.(type) {
	case *Identifier:
		env.Assign(t.Name, value)
		return nil
	case *MemberExpr:
		obj, err := exec.evalExpression(t.Object, env)
		if err != nil {
			return err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return err
		}
		switch obj.Kind() {
		case KindHash, KindObject:
			m := obj.Hash()
			m[t.Property] = value
			return nil
		case KindInstance, KindClass:
			return exec.assignToMember(obj, t.Property, value, t.Pos())
		default:
			return exec.errorAt(target.Pos(), "cannot assign to %s", obj.Kind())
		}
	case *IvarExpr:
		self, ok := env.Get("self")
		if !ok || self.Kind() != KindInstance {
			return exec.errorAt(target.Pos(), "no instance context for ivar")
		}
		self.Instance().Ivars[t.Name] = value
		return nil
	case *ClassVarExpr:
		self, ok := env.Get("self")
		if !ok {
			return exec.errorAt(target.Pos(), "no class context for class var")
		}
		switch self.Kind() {
		case KindInstance:
			self.Instance().Class.ClassVars[t.Name] = value
			return nil
		case KindClass:
			self.Class().ClassVars[t.Name] = value
			return nil
		default:
			return exec.errorAt(target.Pos(), "no class context for class var")
		}
	case *IndexExpr:
		obj, err := exec.evalExpression(t.Object, env)
		if err != nil {
			return err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return err
		}
		idx, err := exec.evalExpression(t.Index, env)
		if err != nil {
			return err
		}
		if err := exec.checkMemoryWith(idx); err != nil {
			return err
		}
		switch obj.Kind() {
		case KindArray:
			arr := obj.Array()
			i, err := valueToInt(idx)
			if err != nil {
				return exec.errorAt(t.Index.Pos(), "%s", err.Error())
			}
			if i < 0 || i >= len(arr) {
				return exec.errorAt(t.Index.Pos(), "array index out of bounds")
			}
			arr[i] = value
			return nil
		case KindHash, KindObject:
			key, err := valueToHashKey(idx)
			if err != nil {
				return exec.errorAt(t.Index.Pos(), "%s", err.Error())
			}
			obj.Hash()[key] = value
			return nil
		default:
			return exec.errorAt(t.Object.Pos(), "cannot index %s", obj.Kind())
		}
	default:
		return exec.errorAt(target.Pos(), "invalid assignment target")
	}
}

func (exec *Execution) evalExpression(expr Expression, env *Env) (Value, error) {
	return exec.evalExpressionWithAuto(expr, env, true)
}

func (exec *Execution) evalExpressionWithAuto(expr Expression, env *Env, autoCall bool) (Value, error) {
	if err := exec.step(); err != nil {
		return NewNil(), err
	}
	switch e := expr.(type) {
	case *Identifier:
		val, ok := env.Get(e.Name)
		if !ok {
			// allow implicit self method lookup
			if self, hasSelf := env.Get("self"); hasSelf && (self.Kind() == KindInstance || self.Kind() == KindClass) {
				member, err := exec.getMember(self, e.Name, e.Pos())
				if err != nil {
					return NewNil(), err
				}
				if autoCall {
					return exec.autoInvokeIfNeeded(e, member, self)
				}
				return member, nil
			}
			return NewNil(), exec.errorAt(e.Pos(), "undefined variable %s", e.Name)
		}
		if autoCall {
			return exec.autoInvokeIfNeeded(e, val, NewNil())
		}
		return val, nil
	case *IntegerLiteral:
		return NewInt(e.Value), nil
	case *FloatLiteral:
		return NewFloat(e.Value), nil
	case *StringLiteral:
		return NewString(e.Value), nil
	case *BoolLiteral:
		return NewBool(e.Value), nil
	case *NilLiteral:
		return NewNil(), nil
	case *SymbolLiteral:
		return NewSymbol(e.Name), nil
	case *ArrayLiteral:
		elems := make([]Value, len(e.Elements))
		for i, el := range e.Elements {
			val, err := exec.evalExpressionWithAuto(el, env, true)
			if err != nil {
				return NewNil(), err
			}
			elems[i] = val
		}
		return NewArray(elems), nil
	case *HashLiteral:
		entries := make(map[string]Value, len(e.Pairs))
		for _, pair := range e.Pairs {
			keyVal, err := exec.evalExpressionWithAuto(pair.Key, env, true)
			if err != nil {
				return NewNil(), err
			}
			key, err := valueToHashKey(keyVal)
			if err != nil {
				return NewNil(), exec.errorAt(pair.Key.Pos(), "%s", err.Error())
			}
			val, err := exec.evalExpressionWithAuto(pair.Value, env, true)
			if err != nil {
				return NewNil(), err
			}
			entries[key] = val
		}
		return NewHash(entries), nil
	case *UnaryExpr:
		return exec.evalUnaryExpr(e, env)
	case *BinaryExpr:
		return exec.evalBinaryExpr(e, env)
	case *RangeExpr:
		return exec.evalRangeExpr(e, env)
	case *CaseExpr:
		return exec.evalCaseExpr(e, env)
	case *MemberExpr:
		obj, err := exec.evalExpressionWithAuto(e.Object, env, true)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return NewNil(), err
		}
		member, err := exec.getMember(obj, e.Property, e.Pos())
		if err != nil {
			return NewNil(), err
		}
		if autoCall {
			return exec.autoInvokeIfNeeded(e, member, obj)
		}
		return member, nil
	case *IndexExpr:
		return exec.evalIndexExpr(e, env)
	case *IvarExpr:
		self, ok := env.Get("self")
		if !ok || self.Kind() != KindInstance {
			return NewNil(), exec.errorAt(e.Pos(), "no instance context for ivar")
		}
		val, ok := self.Instance().Ivars[e.Name]
		if !ok {
			return NewNil(), nil
		}
		return val, nil
	case *ClassVarExpr:
		self, ok := env.Get("self")
		if !ok {
			return NewNil(), exec.errorAt(e.Pos(), "no class context")
		}
		switch self.Kind() {
		case KindInstance:
			val, ok := self.Instance().Class.ClassVars[e.Name]
			if !ok {
				return NewNil(), nil
			}
			return val, nil
		case KindClass:
			val, ok := self.Class().ClassVars[e.Name]
			if !ok {
				return NewNil(), nil
			}
			return val, nil
		default:
			return NewNil(), exec.errorAt(e.Pos(), "no class context")
		}
	case *CallExpr:
		return exec.evalCallExpr(e, env)
	case *BlockLiteral:
		return exec.evalBlockLiteral(e, env)
	case *YieldExpr:
		return exec.evalYield(e, env)
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported expression")
	}
}

func (exec *Execution) autoInvokeIfNeeded(expr Expression, val Value, receiver Value) (Value, error) {
	switch val.Kind() {
	case KindFunction:
		fn := val.Function()
		if fn != nil && len(fn.Params) == 0 {
			return exec.invokeCallable(val, receiver, nil, nil, NewNil(), expr.Pos())
		}
	case KindBuiltin:
		builtin := val.Builtin()
		if builtin != nil && builtin.AutoInvoke {
			return exec.invokeCallable(val, receiver, nil, nil, NewNil(), expr.Pos())
		}
	}
	return val, nil
}

func (exec *Execution) invokeCallable(callee Value, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	switch callee.Kind() {
	case KindFunction:
		result, err := exec.callFunction(callee.Function(), receiver, args, kwargs, block, pos)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.errorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), err
		}
		return result, nil
	case KindBuiltin:
		builtin := callee.Builtin()
		scope := exec.capabilityContractScopes[builtin]
		var preCallKnownBuiltins map[*Builtin]struct{}
		if scope != nil && len(scope.contracts) > 0 {
			preCallKnownBuiltins = make(map[*Builtin]struct{})
			if receiver.Kind() != KindNil {
				collectCapabilityBuiltins(receiver, preCallKnownBuiltins)
			}
			for _, root := range scope.roots {
				collectCapabilityBuiltins(root, preCallKnownBuiltins)
			}
			for _, arg := range args {
				collectCapabilityBuiltins(arg, preCallKnownBuiltins)
			}
			for _, kwarg := range kwargs {
				collectCapabilityBuiltins(kwarg, preCallKnownBuiltins)
			}
		}
		contract, hasContract := exec.capabilityContracts[builtin]
		if hasContract && contract.ValidateArgs != nil {
			if err := contract.ValidateArgs(args, kwargs, block); err != nil {
				return NewNil(), exec.wrapError(err, pos)
			}
		}

		result, err := builtin.Fn(exec, receiver, args, kwargs, block)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.errorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), exec.wrapError(err, pos)
		}
		if hasContract && contract.ValidateReturn != nil {
			if err := contract.ValidateReturn(result); err != nil {
				return NewNil(), exec.wrapError(err, pos)
			}
		}
		if scope != nil && len(scope.contracts) > 0 {
			// Capability methods can lazily publish additional builtins at runtime
			// (e.g. through factory return values or receiver mutation). Re-scan
			// these values so future calls still enforce declared contracts.
			bindCapabilityContractsExcluding(result, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			if receiver.Kind() != KindNil {
				bindCapabilityContractsExcluding(receiver, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
			// Methods can mutate sibling scope roots via captured references; refresh
			// all adapter roots so newly exposed builtins also get bound.
			for _, root := range scope.roots {
				bindCapabilityContractsExcluding(root, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
			// Methods can also publish builtins by mutating positional or keyword
			// argument objects supplied by script code.
			for _, arg := range args {
				bindCapabilityContractsExcluding(arg, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
			for _, kwarg := range kwargs {
				bindCapabilityContractsExcluding(kwarg, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
		}
		return result, nil
	default:
		return NewNil(), exec.errorAt(pos, "attempted to call non-callable value")
	}
}

func (exec *Execution) callFunction(fn *ScriptFunction, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	callEnv := newEnv(fn.Env)
	if receiver.Kind() != KindNil {
		callEnv.Define("self", receiver)
	}
	callEnv.Define("__block__", block)
	if err := exec.bindFunctionArgs(fn, callEnv, args, kwargs, pos); err != nil {
		return NewNil(), err
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return NewNil(), err
	}
	exec.popEnv()
	if err := exec.pushFrame(fn.Name, pos); err != nil {
		return NewNil(), err
	}

	ctx := moduleContext{}
	if fn.owner != nil {
		ctx = moduleContext{
			key:  fn.owner.moduleKey,
			path: fn.owner.modulePath,
			root: fn.owner.moduleRoot,
		}
	}
	exec.pushModuleContext(ctx)
	exec.pushReceiver(receiver)
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	exec.popReceiver()
	exec.popModuleContext()
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if fn.ReturnTy != nil {
		if err := checkValueType(val, fn.ReturnTy); err != nil {
			return NewNil(), exec.errorAt(pos, "%s", formatReturnTypeMismatch(fn.Name, err))
		}
	}
	if returned {
		return val, nil
	}
	return val, nil
}

func (exec *Execution) evalUnaryExpr(e *UnaryExpr, env *Env) (Value, error) {
	right, err := exec.evalExpressionWithAuto(e.Right, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(right); err != nil {
		return NewNil(), err
	}
	switch e.Operator {
	case tokenMinus:
		switch right.Kind() {
		case KindInt:
			return NewInt(-right.Int()), nil
		case KindFloat:
			return NewFloat(-right.Float()), nil
		default:
			return NewNil(), exec.errorAt(e.Pos(), "unsupported unary - operand")
		}
	case tokenBang:
		return NewBool(!right.Truthy()), nil
	default:
		return NewNil(), exec.errorAt(e.Pos(), "unsupported unary operator")
	}
}

func (exec *Execution) evalIndexExpr(e *IndexExpr, env *Env) (Value, error) {
	obj, err := exec.evalExpressionWithAuto(e.Object, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(obj); err != nil {
		return NewNil(), err
	}
	idx, err := exec.evalExpressionWithAuto(e.Index, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(idx); err != nil {
		return NewNil(), err
	}
	switch obj.Kind() {
	case KindString:
		i, err := valueToInt(idx)
		if err != nil {
			return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
		}
		runes := []rune(obj.String())
		if i < 0 || i >= len(runes) {
			return NewNil(), exec.errorAt(e.Index.Pos(), "string index out of bounds")
		}
		return NewString(string(runes[i])), nil
	case KindArray:
		i, err := valueToInt(idx)
		if err != nil {
			return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
		}
		arr := obj.Array()
		if i < 0 || i >= len(arr) {
			return NewNil(), exec.errorAt(e.Index.Pos(), "array index out of bounds")
		}
		return arr[i], nil
	case KindHash, KindObject:
		key, err := valueToHashKey(idx)
		if err != nil {
			return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
		}
		val, ok := obj.Hash()[key]
		if !ok {
			return NewNil(), nil
		}
		return val, nil
	default:
		return NewNil(), exec.errorAt(e.Object.Pos(), "cannot index %s", obj.Kind())
	}
}

func (exec *Execution) evalBinaryExpr(expr *BinaryExpr, env *Env) (Value, error) {
	left, err := exec.evalExpression(expr.Left, env)
	if err != nil {
		return NewNil(), err
	}
	right, err := exec.evalExpression(expr.Right, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(left, right); err != nil {
		return NewNil(), err
	}

	var result Value
	switch expr.Operator {
	case tokenPlus:
		result, err = addValues(left, right)
	case tokenMinus:
		result, err = subtractValues(left, right)
	case tokenAsterisk:
		result, err = multiplyValues(left, right)
	case tokenSlash:
		result, err = divideValues(left, right)
	case tokenPercent:
		result, err = moduloValues(left, right)
	case tokenEQ:
		return NewBool(left.Equal(right)), nil
	case tokenNotEQ:
		return NewBool(!left.Equal(right)), nil
	case tokenLT:
		return compareValues(expr, left, right, func(c int) bool { return c < 0 })
	case tokenLTE:
		return compareValues(expr, left, right, func(c int) bool { return c <= 0 })
	case tokenGT:
		return compareValues(expr, left, right, func(c int) bool { return c > 0 })
	case tokenGTE:
		return compareValues(expr, left, right, func(c int) bool { return c >= 0 })
	case tokenAnd:
		return NewBool(left.Truthy() && right.Truthy()), nil
	case tokenOr:
		return NewBool(left.Truthy() || right.Truthy()), nil
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported operator")
	}

	if err != nil {
		return NewNil(), exec.wrapError(err, expr.Pos())
	}
	return result, nil
}

func (exec *Execution) evalCallTarget(call *CallExpr, env *Env) (Value, Value, error) {
	if member, ok := call.Callee.(*MemberExpr); ok {
		receiver, err := exec.evalExpression(member.Object, env)
		if err != nil {
			return NewNil(), NewNil(), err
		}
		if err := exec.checkMemoryWith(receiver); err != nil {
			return NewNil(), NewNil(), err
		}
		callee, err := exec.getMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), NewNil(), err
		}
		return callee, receiver, nil
	}

	callee, err := exec.evalExpressionWithAuto(call.Callee, env, false)
	if err != nil {
		return NewNil(), NewNil(), err
	}
	return callee, NewNil(), nil
}

func (exec *Execution) evalCallArgs(call *CallExpr, env *Env) ([]Value, error) {
	args := make([]Value, len(call.Args))
	for i, arg := range call.Args {
		val, err := exec.evalExpressionWithAuto(arg, env, true)
		if err != nil {
			return nil, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return nil, err
		}
		args[i] = val
	}
	return args, nil
}

func (exec *Execution) evalCallKwArgs(call *CallExpr, env *Env) (map[string]Value, error) {
	if len(call.KwArgs) == 0 {
		return nil, nil
	}
	kwargs := make(map[string]Value, len(call.KwArgs))
	for _, kw := range call.KwArgs {
		val, err := exec.evalExpressionWithAuto(kw.Value, env, true)
		if err != nil {
			return nil, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return nil, err
		}
		kwargs[kw.Name] = val
	}
	return kwargs, nil
}

func (exec *Execution) evalCallBlock(call *CallExpr, env *Env) (Value, error) {
	if call.Block == nil {
		return NewNil(), nil
	}
	return exec.evalBlockLiteral(call.Block, env)
}

func (exec *Execution) checkCallMemoryRoots(receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if receiver.Kind() == KindNil && len(kwargs) == 0 && block.IsNil() {
		if len(args) == 0 {
			return nil
		}
		return exec.checkMemoryWith(args...)
	}
	combined := make([]Value, 0, len(args)+len(kwargs)+2)
	if receiver.Kind() != KindNil {
		combined = append(combined, receiver)
	}
	combined = append(combined, args...)
	for _, kwVal := range kwargs {
		combined = append(combined, kwVal)
	}
	if !block.IsNil() {
		combined = append(combined, block)
	}
	if len(combined) == 0 {
		return nil
	}
	return exec.checkMemoryWith(combined...)
}

func (exec *Execution) evalCallExpr(call *CallExpr, env *Env) (Value, error) {
	callee, receiver, err := exec.evalCallTarget(call, env)
	if err != nil {
		return NewNil(), err
	}
	args, err := exec.evalCallArgs(call, env)
	if err != nil {
		return NewNil(), err
	}
	kwargs, err := exec.evalCallKwArgs(call, env)
	if err != nil {
		return NewNil(), err
	}
	block, err := exec.evalCallBlock(call, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkCallMemoryRoots(receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	result, callErr := exec.invokeCallable(callee, receiver, args, kwargs, block, call.Pos())
	if callErr != nil {
		return NewNil(), callErr
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) evalBlockLiteral(block *BlockLiteral, env *Env) (Value, error) {
	blockValue := NewBlock(block.Params, block.Body, env)
	if ctx := exec.currentModuleContext(); ctx != nil {
		blk := blockValue.Block()
		blk.moduleKey = ctx.key
		blk.modulePath = ctx.path
		blk.moduleRoot = ctx.root
	}
	return blockValue, nil
}

func ensureBlock(block Value, name string) error {
	if block.Block() == nil {
		if name != "" {
			return fmt.Errorf("%s requires a block", name)
		}
		return fmt.Errorf("block required")
	}
	return nil
}

// CallBlock invokes a block value with the provided arguments.
// This is the public entry point for capability adapters that need to
// call user-supplied blocks (e.g. db.each, db.tx).
func (exec *Execution) CallBlock(block Value, args []Value) (Value, error) {
	if err := ensureBlock(block, ""); err != nil {
		return NewNil(), err
	}
	blk := block.Block()
	exec.pushModuleContext(moduleContext{
		key:  blk.moduleKey,
		path: blk.modulePath,
		root: blk.moduleRoot,
	})
	defer exec.popModuleContext()

	blockEnv := newEnv(blk.Env)
	for i, param := range blk.Params {
		var val Value
		if i < len(args) {
			val = args[i]
		} else {
			val = NewNil()
		}
		if param.Type != nil {
			if err := checkValueType(val, param.Type); err != nil {
				return NewNil(), exec.errorAt(param.Type.position, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
		}
		blockEnv.Define(param.Name, val)
	}
	val, returned, err := exec.evalStatements(blk.Body, blockEnv)
	if err != nil {
		return NewNil(), err
	}
	if returned {
		return val, nil
	}
	return val, nil
}

func (exec *Execution) evalYield(expr *YieldExpr, env *Env) (Value, error) {
	block, ok := env.Get("__block__")
	if !ok || block.Kind() == KindNil {
		return NewNil(), exec.errorAt(expr.Pos(), "no block given")
	}
	var args []Value
	for _, arg := range expr.Args {
		val, err := exec.evalExpression(arg, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), err
		}
		args = append(args, val)
	}
	if len(args) > 0 {
		if err := exec.checkMemoryWith(args...); err != nil {
			return NewNil(), err
		}
	}
	return exec.CallBlock(block, args)
}
