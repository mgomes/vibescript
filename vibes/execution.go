package vibes

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

type ScriptFunction struct {
	Name     string
	Params   []Param
	ReturnTy *TypeExpr
	Body     []Statement
	Pos      Position
	Env      *Env
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
	engine          *Engine
	script          *Script
	ctx             context.Context
	quota           int
	memoryQuota     int
	recursionCap    int
	steps           int
	callStack       []callFrame
	root            *Env
	modules         map[string]Value
	moduleLoading   map[string]bool
	moduleLoadStack []string
	moduleStack     []moduleContext
	receiverStack   []Value
	envStack        []*Env
	strictEffects   bool
	allowRequire    bool
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
	Message   string
	CodeFrame string
	Frames    []StackFrame
}

func (re *RuntimeError) Error() string {
	var b strings.Builder
	b.WriteString(re.Message)
	if re.CodeFrame != "" {
		b.WriteString("\n")
		b.WriteString(re.CodeFrame)
	}
	for _, frame := range re.Frames {
		// Show position if line number is valid (1-based)
		if frame.Pos.Line > 0 && frame.Pos.Column > 0 {
			fmt.Fprintf(&b, "\n  at %s (%d:%d)", frame.Function, frame.Pos.Line, frame.Pos.Column)
		} else if frame.Pos.Line > 0 {
			// Line valid but column missing
			fmt.Fprintf(&b, "\n  at %s (line %d)", frame.Function, frame.Pos.Line)
		} else {
			// No position information available
			fmt.Fprintf(&b, "\n  at %s", frame.Function)
		}
	}
	return b.String()
}

// Unwrap returns nil to satisfy the error unwrapping interface.
// RuntimeError is a terminal error that wraps the original error message but not the error itself.
func (re *RuntimeError) Unwrap() error {
	return nil
}

func (exec *Execution) step() error {
	exec.steps++
	if exec.quota > 0 && exec.steps > exec.quota {
		return fmt.Errorf("step quota exceeded (%d)", exec.quota)
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
	return &RuntimeError{Message: message, CodeFrame: codeFrame, Frames: frames}
}

func (exec *Execution) wrapError(err error, pos Position) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*RuntimeError); ok {
		return err
	}
	return exec.newRuntimeError(err.Error(), pos)
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
		entries := make(map[string]Value)
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
		return exec.callFunction(callee.Function(), receiver, args, kwargs, block, pos)
	case KindBuiltin:
		result, err := callee.Builtin().Fn(exec, receiver, args, kwargs, block)
		if err != nil {
			return NewNil(), exec.wrapError(err, pos)
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
	if fn.owner != nil && fn.owner.moduleKey != "" {
		exec.pushModuleContext(moduleContext{
			key:  fn.owner.moduleKey,
			path: fn.owner.modulePath,
			root: fn.owner.moduleRoot,
		})
	}
	exec.pushReceiver(receiver)
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	exec.popReceiver()
	if fn.owner != nil && fn.owner.moduleKey != "" {
		exec.popModuleContext()
	}
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if fn.ReturnTy != nil {
		if err := checkValueType(val, fn.ReturnTy); err != nil {
			return NewNil(), exec.errorAt(pos, "%s", err.Error())
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
	return NewBlock(block.Params, block.Body, env), nil
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
	blockEnv := newEnv(blk.Env)
	for i, param := range blk.Params {
		var val Value
		if i < len(args) {
			val = args[i]
		} else {
			val = NewNil()
		}
		blockEnv.Define(param, val)
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

func (exec *Execution) evalRangeExpr(expr *RangeExpr, env *Env) (Value, error) {
	startVal, err := exec.evalExpression(expr.Start, env)
	if err != nil {
		return NewNil(), err
	}
	endVal, err := exec.evalExpression(expr.End, env)
	if err != nil {
		return NewNil(), err
	}
	start, err := valueToInt64(startVal)
	if err != nil {
		return NewNil(), exec.errorAt(expr.Start.Pos(), "%s", err.Error())
	}
	end, err := valueToInt64(endVal)
	if err != nil {
		return NewNil(), exec.errorAt(expr.End.Pos(), "%s", err.Error())
	}
	return NewRange(Range{Start: start, End: end}), nil
}

func (exec *Execution) evalForStatement(stmt *ForStmt, env *Env) (Value, bool, error) {
	iterable, err := exec.evalExpression(stmt.Iterable, env)
	if err != nil {
		return NewNil(), false, err
	}
	if err := exec.checkMemoryWith(iterable); err != nil {
		return NewNil(), false, err
	}
	last := NewNil()

	switch iterable.Kind() {
	case KindArray:
		arr := iterable.Array()
		for _, item := range arr {
			env.Assign(stmt.Iterator, item)
			val, returned, err := exec.evalStatements(stmt.Body, env)
			if err != nil {
				return NewNil(), false, err
			}
			if returned {
				return val, true, nil
			}
			last = val
		}
	case KindRange:
		r := iterable.Range()
		if r.Start <= r.End {
			for i := r.Start; i <= r.End; i++ {
				env.Assign(stmt.Iterator, NewInt(i))
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					return NewNil(), false, err
				}
				if returned {
					return val, true, nil
				}
				last = val
			}
		} else {
			for i := r.Start; i >= r.End; i-- {
				env.Assign(stmt.Iterator, NewInt(i))
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					return NewNil(), false, err
				}
				if returned {
					return val, true, nil
				}
				last = val
			}
		}
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "cannot iterate over %s", iterable.Kind())
	}

	return last, false, nil
}

func (exec *Execution) getMember(obj Value, property string, pos Position) (Value, error) {
	switch obj.Kind() {
	case KindHash, KindObject:
		if val, ok := obj.Hash()[property]; ok {
			return val, nil
		}
		member, err := hashMember(obj, property)
		if err != nil {
			return NewNil(), err
		}
		return member, nil
	case KindMoney:
		return moneyMember(obj.Money(), property)
	case KindDuration:
		return durationMember(obj.Duration(), property, pos)
	case KindTime:
		return timeMember(obj.Time(), property)
	case KindArray:
		return arrayMember(obj, property)
	case KindString:
		return stringMember(obj, property)
	case KindClass:
		cl := obj.Class()
		if property == "new" {
			return NewAutoBuiltin(cl.Name+".new", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				inst := &Instance{Class: cl, Ivars: make(map[string]Value)}
				instVal := NewInstance(inst)
				if initFn, ok := cl.Methods["initialize"]; ok {
					if _, err := exec.callFunction(initFn, instVal, args, kwargs, block, pos); err != nil {
						return NewNil(), err
					}
				}
				return instVal, nil
			}), nil
		}
		if fn, ok := cl.ClassMethods[property]; ok {
			if fn.Private && !exec.isCurrentReceiver(obj) {
				return NewNil(), exec.errorAt(pos, "private method %s", property)
			}
			return NewAutoBuiltin(cl.Name+"."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return exec.callFunction(fn, obj, args, kwargs, block, pos)
			}), nil
		}
		if val, ok := cl.ClassVars[property]; ok {
			return val, nil
		}
		return NewNil(), exec.errorAt(pos, "unknown class member %s", property)
	case KindInstance:
		inst := obj.Instance()
		if property == "class" {
			return NewClass(inst.Class), nil
		}
		if fn, ok := inst.Class.Methods[property]; ok {
			if fn.Private && !exec.isCurrentReceiver(obj) {
				return NewNil(), exec.errorAt(pos, "private method %s", property)
			}
			return NewAutoBuiltin(inst.Class.Name+"#"+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return exec.callFunction(fn, obj, args, kwargs, block, pos)
			}), nil
		}
		if val, ok := inst.Ivars[property]; ok {
			return val, nil
		}
		return NewNil(), exec.errorAt(pos, "unknown member %s", property)
	case KindInt:
		switch property {
		case "seconds", "second", "minutes", "minute", "hours", "hour", "days", "day":
			return NewDuration(secondsDuration(obj.Int(), property)), nil
		case "weeks", "week":
			return NewDuration(secondsDuration(obj.Int(), property)), nil
		case "abs":
			return NewAutoBuiltin("int.abs", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("int.abs does not take arguments")
				}
				n := receiver.Int()
				if n == math.MinInt64 {
					return NewNil(), fmt.Errorf("int.abs overflow")
				}
				if n < 0 {
					return NewInt(-n), nil
				}
				return receiver, nil
			}), nil
		case "clamp":
			return NewAutoBuiltin("int.clamp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) != 2 {
					return NewNil(), fmt.Errorf("int.clamp expects min and max")
				}
				if args[0].Kind() != KindInt || args[1].Kind() != KindInt {
					return NewNil(), fmt.Errorf("int.clamp expects integer min and max")
				}
				minVal := args[0].Int()
				maxVal := args[1].Int()
				if minVal > maxVal {
					return NewNil(), fmt.Errorf("int.clamp min must be <= max")
				}
				n := receiver.Int()
				if n < minVal {
					return NewInt(minVal), nil
				}
				if n > maxVal {
					return NewInt(maxVal), nil
				}
				return receiver, nil
			}), nil
		case "even?":
			return NewAutoBuiltin("int.even?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("int.even? does not take arguments")
				}
				return NewBool(receiver.Int()%2 == 0), nil
			}), nil
		case "odd?":
			return NewAutoBuiltin("int.odd?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("int.odd? does not take arguments")
				}
				return NewBool(receiver.Int()%2 != 0), nil
			}), nil
		case "times":
			return NewAutoBuiltin("int.times", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("int.times does not take arguments")
				}
				if block.Block() == nil {
					return NewNil(), fmt.Errorf("int.times requires a block")
				}
				count := receiver.Int()
				if count <= 0 {
					return receiver, nil
				}
				if count > int64(math.MaxInt) {
					return NewNil(), fmt.Errorf("int.times value too large")
				}
				for i := range int(count) {
					if _, err := exec.CallBlock(block, []Value{NewInt(int64(i))}); err != nil {
						return NewNil(), err
					}
				}
				return receiver, nil
			}), nil
		default:
			return NewNil(), exec.errorAt(pos, "unknown int member %s", property)
		}
	case KindFloat:
		switch property {
		case "abs":
			return NewAutoBuiltin("float.abs", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("float.abs does not take arguments")
				}
				return NewFloat(math.Abs(receiver.Float())), nil
			}), nil
		case "clamp":
			return NewAutoBuiltin("float.clamp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) != 2 {
					return NewNil(), fmt.Errorf("float.clamp expects min and max")
				}
				if (args[0].Kind() != KindInt && args[0].Kind() != KindFloat) || (args[1].Kind() != KindInt && args[1].Kind() != KindFloat) {
					return NewNil(), fmt.Errorf("float.clamp expects numeric min and max")
				}
				minVal := args[0].Float()
				maxVal := args[1].Float()
				if minVal > maxVal {
					return NewNil(), fmt.Errorf("float.clamp min must be <= max")
				}
				n := receiver.Float()
				if n < minVal {
					return NewFloat(minVal), nil
				}
				if n > maxVal {
					return NewFloat(maxVal), nil
				}
				return receiver, nil
			}), nil
		case "round":
			return NewAutoBuiltin("float.round", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("float.round does not take arguments")
				}
				rounded := math.Round(receiver.Float())
				asInt, err := floatToInt64Checked(rounded, "float.round")
				if err != nil {
					return NewNil(), err
				}
				return NewInt(asInt), nil
			}), nil
		case "floor":
			return NewAutoBuiltin("float.floor", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("float.floor does not take arguments")
				}
				floored := math.Floor(receiver.Float())
				asInt, err := floatToInt64Checked(floored, "float.floor")
				if err != nil {
					return NewNil(), err
				}
				return NewInt(asInt), nil
			}), nil
		case "ceil":
			return NewAutoBuiltin("float.ceil", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				if len(args) > 0 {
					return NewNil(), fmt.Errorf("float.ceil does not take arguments")
				}
				ceiled := math.Ceil(receiver.Float())
				asInt, err := floatToInt64Checked(ceiled, "float.ceil")
				if err != nil {
					return NewNil(), err
				}
				return NewInt(asInt), nil
			}), nil
		default:
			return NewNil(), exec.errorAt(pos, "unknown float member %s", property)
		}
	default:
		return NewNil(), exec.errorAt(pos, "unsupported member access on %s", obj.Kind())
	}
}

func moneyMember(m Money, property string) (Value, error) {
	switch property {
	case "currency":
		return NewString(m.Currency()), nil
	case "cents":
		return NewInt(m.Cents()), nil
	case "amount":
		return NewString(m.String()), nil
	case "format":
		return NewAutoBuiltin("money.format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewString(m.String()), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown money member %s", property)
	}
}

func hashMember(obj Value, property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("hash.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.size does not take arguments")
			}
			return NewInt(int64(len(receiver.Hash()))), nil
		}), nil
	case "length":
		return NewAutoBuiltin("hash.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.length does not take arguments")
			}
			return NewInt(int64(len(receiver.Hash()))), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("hash.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.empty? does not take arguments")
			}
			return NewBool(len(receiver.Hash()) == 0), nil
		}), nil
	case "key?", "has_key?", "include?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one key", name)
			}
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.%s key must be symbol or string", name)
			}
			_, ok := receiver.Hash()[key]
			return NewBool(ok), nil
		}), nil
	case "keys":
		return NewAutoBuiltin("hash.keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.keys does not take arguments")
			}
			keys := sortedHashKeys(receiver.Hash())
			values := make([]Value, len(keys))
			for i, k := range keys {
				values[i] = NewSymbol(k)
			}
			return NewArray(values), nil
		}), nil
	case "values":
		return NewAutoBuiltin("hash.values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.values does not take arguments")
			}
			entries := receiver.Hash()
			keys := sortedHashKeys(entries)
			values := make([]Value, len(keys))
			for i, k := range keys {
				values[i] = entries[k]
			}
			return NewArray(values), nil
		}), nil
	case "fetch":
		return NewAutoBuiltin("hash.fetch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("hash.fetch expects key and optional default")
			}
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.fetch key must be symbol or string")
			}
			if value, ok := receiver.Hash()[key]; ok {
				return value, nil
			}
			if len(args) == 2 {
				return args[1], nil
			}
			return NewNil(), nil
		}), nil
	case "dig":
		return NewAutoBuiltin("hash.dig", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("hash.dig expects at least one key")
			}
			current := receiver
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.dig path keys must be symbol or string")
				}
				if current.Kind() != KindHash && current.Kind() != KindObject {
					return NewNil(), nil
				}
				next, ok := current.Hash()[key]
				if !ok {
					return NewNil(), nil
				}
				current = next
			}
			return current, nil
		}), nil
	case "each":
		return NewAutoBuiltin("hash.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each does not take arguments")
			}
			if err := ensureBlock(block, "hash.each"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			for _, key := range sortedHashKeys(entries) {
				if _, err := exec.CallBlock(block, []Value{NewSymbol(key), entries[key]}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_key":
		return NewAutoBuiltin("hash.each_key", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_key does not take arguments")
			}
			if err := ensureBlock(block, "hash.each_key"); err != nil {
				return NewNil(), err
			}
			for _, key := range sortedHashKeys(receiver.Hash()) {
				if _, err := exec.CallBlock(block, []Value{NewSymbol(key)}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_value":
		return NewAutoBuiltin("hash.each_value", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_value does not take arguments")
			}
			if err := ensureBlock(block, "hash.each_value"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			for _, key := range sortedHashKeys(entries) {
				if _, err := exec.CallBlock(block, []Value{entries[key]}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "merge":
		return NewBuiltin("hash.merge", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
				return NewNil(), fmt.Errorf("hash.merge expects a single hash argument")
			}
			base := receiver.Hash()
			addition := args[0].Hash()
			out := make(map[string]Value, len(base)+len(addition))
			for k, v := range base {
				out[k] = v
			}
			for k, v := range addition {
				out[k] = v
			}
			return NewHash(out), nil
		}), nil
	case "slice":
		return NewBuiltin("hash.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			entries := receiver.Hash()
			out := make(map[string]Value, len(args))
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.slice keys must be symbol or string")
				}
				if value, ok := entries[key]; ok {
					out[key] = value
				}
			}
			return NewHash(out), nil
		}), nil
	case "except":
		return NewBuiltin("hash.except", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			excluded := make(map[string]struct{}, len(args))
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.except keys must be symbol or string")
				}
				excluded[key] = struct{}{}
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for key, value := range entries {
				if _, skip := excluded[key]; skip {
					continue
				}
				out[key] = value
			}
			return NewHash(out), nil
		}), nil
	case "select":
		return NewAutoBuiltin("hash.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.select does not take arguments")
			}
			if err := ensureBlock(block, "hash.select"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				include, err := exec.CallBlock(block, []Value{NewSymbol(key), entries[key]})
				if err != nil {
					return NewNil(), err
				}
				if include.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
		}), nil
	case "reject":
		return NewAutoBuiltin("hash.reject", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.reject does not take arguments")
			}
			if err := ensureBlock(block, "hash.reject"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				exclude, err := exec.CallBlock(block, []Value{NewSymbol(key), entries[key]})
				if err != nil {
					return NewNil(), err
				}
				if !exclude.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
		}), nil
	case "transform_keys":
		return NewAutoBuiltin("hash.transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_keys does not take arguments")
			}
			if err := ensureBlock(block, "hash.transform_keys"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				nextKey, err := exec.CallBlock(block, []Value{NewSymbol(key)})
				if err != nil {
					return NewNil(), err
				}
				resolved, err := valueToHashKey(nextKey)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block must return symbol or string")
				}
				out[resolved] = entries[key]
			}
			return NewHash(out), nil
		}), nil
	case "transform_values":
		return NewAutoBuiltin("hash.transform_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_values does not take arguments")
			}
			if err := ensureBlock(block, "hash.transform_values"); err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for _, key := range sortedHashKeys(entries) {
				nextValue, err := exec.CallBlock(block, []Value{entries[key]})
				if err != nil {
					return NewNil(), err
				}
				out[key] = nextValue
			}
			return NewHash(out), nil
		}), nil
	case "compact":
		return NewAutoBuiltin("hash.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.compact does not take arguments")
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for k, v := range entries {
				if v.Kind() != KindNil {
					out[k] = v
				}
			}
			return NewHash(out), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

func sortedHashKeys(entries map[string]Value) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func chompDefault(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return text[:len(text)-2]
	}
	if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r") {
		return text[:len(text)-1]
	}
	return text
}

func stringRuneIndex(text, needle string, offset int) int {
	hayRunes := []rune(text)
	needleRunes := []rune(needle)
	if offset < 0 || offset > len(hayRunes) {
		return -1
	}
	if len(needleRunes) == 0 {
		return offset
	}
	limit := len(hayRunes) - len(needleRunes)
	if limit < offset {
		return -1
	}
	for i := offset; i <= limit; i++ {
		match := true
		for j := range len(needleRunes) {
			if hayRunes[i+j] != needleRunes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func stringRuneRIndex(text, needle string, offset int) int {
	hayRunes := []rune(text)
	needleRunes := []rune(needle)
	if offset < 0 {
		return -1
	}
	if offset > len(hayRunes) {
		offset = len(hayRunes)
	}
	if len(needleRunes) == 0 {
		return offset
	}
	if len(needleRunes) > len(hayRunes) {
		return -1
	}
	start := offset
	maxStart := len(hayRunes) - len(needleRunes)
	if start > maxStart {
		start = maxStart
	}
	for i := start; i >= 0; i-- {
		match := true
		for j := range len(needleRunes) {
			if hayRunes[i+j] != needleRunes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func stringRuneSlice(text string, start, length int) (string, bool) {
	runes := []rune(text)
	if start < 0 || start >= len(runes) {
		return "", false
	}
	if length < 0 {
		return "", false
	}
	remaining := len(runes) - start
	if length >= remaining {
		return string(runes[start:]), true
	}
	end := start + length
	return string(runes[start:end]), true
}

func stringCapitalize(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

func stringSwapCase(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			runes[i] = unicode.ToLower(r)
			continue
		}
		if unicode.IsLower(r) {
			runes[i] = unicode.ToUpper(r)
		}
	}
	return string(runes)
}

func stringReverse(text string) string {
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func stringRegexOption(method string, kwargs map[string]Value) (bool, error) {
	if len(kwargs) == 0 {
		return false, nil
	}
	regexVal, ok := kwargs["regex"]
	if !ok || len(kwargs) > 1 {
		return false, fmt.Errorf("string.%s supports only regex keyword", method)
	}
	if regexVal.Kind() != KindBool {
		return false, fmt.Errorf("string.%s regex keyword must be bool", method)
	}
	return regexVal.Bool(), nil
}

func stringSub(text, pattern, replacement string, regex bool) (string, error) {
	if !regex {
		return strings.Replace(text, pattern, replacement, 1), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return text, nil
	}
	replaced := re.ExpandString(nil, replacement, text, loc)
	return text[:loc[0]] + string(replaced) + text[loc[1]:], nil
}

func stringGSub(text, pattern, replacement string, regex bool) (string, error) {
	if !regex {
		return strings.ReplaceAll(text, pattern, replacement), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	return re.ReplaceAllString(text, replacement), nil
}

func stringBangResult(original, updated string) Value {
	if updated == original {
		return NewNil()
	}
	return NewString(updated)
}

func stringMember(str Value, property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("string.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.size does not take arguments")
			}
			return NewInt(int64(len([]rune(receiver.String())))), nil
		}), nil
	case "length":
		return NewAutoBuiltin("string.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.length does not take arguments")
			}
			return NewInt(int64(len([]rune(receiver.String())))), nil
		}), nil
	case "bytesize":
		return NewAutoBuiltin("string.bytesize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.bytesize does not take arguments")
			}
			return NewInt(int64(len(receiver.String()))), nil
		}), nil
	case "ord":
		return NewAutoBuiltin("string.ord", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.ord does not take arguments")
			}
			runes := []rune(receiver.String())
			if len(runes) == 0 {
				return NewNil(), fmt.Errorf("string.ord requires non-empty string")
			}
			return NewInt(int64(runes[0])), nil
		}), nil
	case "chr":
		return NewAutoBuiltin("string.chr", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.chr does not take arguments")
			}
			runes := []rune(receiver.String())
			if len(runes) == 0 {
				return NewNil(), nil
			}
			return NewString(string(runes[0])), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("string.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.empty? does not take arguments")
			}
			return NewBool(len(receiver.String()) == 0), nil
		}), nil
	case "clear":
		return NewAutoBuiltin("string.clear", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.clear does not take arguments")
			}
			return NewString(""), nil
		}), nil
	case "concat":
		return NewAutoBuiltin("string.concat", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			var b strings.Builder
			b.WriteString(receiver.String())
			for _, arg := range args {
				if arg.Kind() != KindString {
					return NewNil(), fmt.Errorf("string.concat expects string arguments")
				}
				b.WriteString(arg.String())
			}
			return NewString(b.String()), nil
		}), nil
	case "replace":
		return NewAutoBuiltin("string.replace", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.replace expects exactly one replacement")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.replace replacement must be string")
			}
			return NewString(args[0].String()), nil
		}), nil
	case "start_with?":
		return NewAutoBuiltin("string.start_with?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.start_with? expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.start_with? prefix must be string")
			}
			return NewBool(strings.HasPrefix(receiver.String(), args[0].String())), nil
		}), nil
	case "end_with?":
		return NewAutoBuiltin("string.end_with?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.end_with? expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.end_with? suffix must be string")
			}
			return NewBool(strings.HasSuffix(receiver.String(), args[0].String())), nil
		}), nil
	case "include?":
		return NewAutoBuiltin("string.include?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.include? expects exactly one substring")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.include? substring must be string")
			}
			return NewBool(strings.Contains(receiver.String(), args[0].String())), nil
		}), nil
	case "match":
		return NewAutoBuiltin("string.match", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.match does not take keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.match expects exactly one pattern")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.match pattern must be string")
			}
			pattern := args[0].String()
			re, err := regexp.Compile(pattern)
			if err != nil {
				return NewNil(), fmt.Errorf("string.match invalid regex: %v", err)
			}
			text := receiver.String()
			indices := re.FindStringSubmatchIndex(text)
			if indices == nil {
				return NewNil(), nil
			}
			values := make([]Value, len(indices)/2)
			for i := range values {
				start := indices[i*2]
				end := indices[i*2+1]
				if start < 0 || end < 0 {
					values[i] = NewNil()
					continue
				}
				values[i] = NewString(text[start:end])
			}
			return NewArray(values), nil
		}), nil
	case "scan":
		return NewAutoBuiltin("string.scan", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.scan does not take keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.scan expects exactly one pattern")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.scan pattern must be string")
			}
			pattern := args[0].String()
			re, err := regexp.Compile(pattern)
			if err != nil {
				return NewNil(), fmt.Errorf("string.scan invalid regex: %v", err)
			}
			matches := re.FindAllString(receiver.String(), -1)
			values := make([]Value, len(matches))
			for i, m := range matches {
				values[i] = NewString(m)
			}
			return NewArray(values), nil
		}), nil
	case "index":
		return NewAutoBuiltin("string.index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.index expects substring and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.index substring must be string")
			}
			offset := 0
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.index offset must be non-negative integer")
				}
				offset = i
			}
			index := stringRuneIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "rindex":
		return NewAutoBuiltin("string.rindex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.rindex expects substring and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.rindex substring must be string")
			}
			offset := len([]rune(receiver.String()))
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.rindex offset must be non-negative integer")
				}
				offset = i
			}
			index := stringRuneRIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "slice":
		return NewAutoBuiltin("string.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.slice expects index and optional length")
			}
			start, err := valueToInt(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("string.slice index must be integer")
			}
			runes := []rune(receiver.String())
			if len(args) == 1 {
				if start < 0 || start >= len(runes) {
					return NewNil(), nil
				}
				return NewString(string(runes[start])), nil
			}
			length, err := valueToInt(args[1])
			if err != nil {
				return NewNil(), fmt.Errorf("string.slice length must be integer")
			}
			substr, ok := stringRuneSlice(receiver.String(), start, length)
			if !ok {
				return NewNil(), nil
			}
			return NewString(substr), nil
		}), nil
	case "strip":
		return NewAutoBuiltin("string.strip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip does not take arguments")
			}
			return NewString(strings.TrimSpace(receiver.String())), nil
		}), nil
	case "strip!":
		return NewAutoBuiltin("string.strip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip! does not take arguments")
			}
			updated := strings.TrimSpace(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "lstrip":
		return NewAutoBuiltin("string.lstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip does not take arguments")
			}
			return NewString(strings.TrimLeftFunc(receiver.String(), unicode.IsSpace)), nil
		}), nil
	case "lstrip!":
		return NewAutoBuiltin("string.lstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip! does not take arguments")
			}
			updated := strings.TrimLeftFunc(receiver.String(), unicode.IsSpace)
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "rstrip":
		return NewAutoBuiltin("string.rstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip does not take arguments")
			}
			return NewString(strings.TrimRightFunc(receiver.String(), unicode.IsSpace)), nil
		}), nil
	case "rstrip!":
		return NewAutoBuiltin("string.rstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip! does not take arguments")
			}
			updated := strings.TrimRightFunc(receiver.String(), unicode.IsSpace)
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "chomp":
		return NewAutoBuiltin("string.chomp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.chomp accepts at most one separator")
			}
			text := receiver.String()
			if len(args) == 0 {
				return NewString(chompDefault(text)), nil
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.chomp separator must be string")
			}
			sep := args[0].String()
			if sep == "" {
				return NewString(strings.TrimRight(text, "\r\n")), nil
			}
			if strings.HasSuffix(text, sep) {
				return NewString(text[:len(text)-len(sep)]), nil
			}
			return NewString(text), nil
		}), nil
	case "chomp!":
		return NewAutoBuiltin("string.chomp!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.chomp! accepts at most one separator")
			}
			original := receiver.String()
			if len(args) == 0 {
				return stringBangResult(original, chompDefault(original)), nil
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.chomp! separator must be string")
			}
			sep := args[0].String()
			if sep == "" {
				return stringBangResult(original, strings.TrimRight(original, "\r\n")), nil
			}
			if strings.HasSuffix(original, sep) {
				return stringBangResult(original, original[:len(original)-len(sep)]), nil
			}
			return NewNil(), nil
		}), nil
	case "delete_prefix":
		return NewAutoBuiltin("string.delete_prefix", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_prefix expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_prefix prefix must be string")
			}
			return NewString(strings.TrimPrefix(receiver.String(), args[0].String())), nil
		}), nil
	case "delete_prefix!":
		return NewAutoBuiltin("string.delete_prefix!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_prefix! expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_prefix! prefix must be string")
			}
			updated := strings.TrimPrefix(receiver.String(), args[0].String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "delete_suffix":
		return NewAutoBuiltin("string.delete_suffix", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_suffix expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_suffix suffix must be string")
			}
			return NewString(strings.TrimSuffix(receiver.String(), args[0].String())), nil
		}), nil
	case "delete_suffix!":
		return NewAutoBuiltin("string.delete_suffix!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_suffix! expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_suffix! suffix must be string")
			}
			updated := strings.TrimSuffix(receiver.String(), args[0].String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "upcase":
		return NewAutoBuiltin("string.upcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase does not take arguments")
			}
			return NewString(strings.ToUpper(receiver.String())), nil
		}), nil
	case "upcase!":
		return NewAutoBuiltin("string.upcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase! does not take arguments")
			}
			updated := strings.ToUpper(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "downcase":
		return NewAutoBuiltin("string.downcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase does not take arguments")
			}
			return NewString(strings.ToLower(receiver.String())), nil
		}), nil
	case "downcase!":
		return NewAutoBuiltin("string.downcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase! does not take arguments")
			}
			updated := strings.ToLower(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "capitalize":
		return NewAutoBuiltin("string.capitalize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.capitalize does not take arguments")
			}
			return NewString(stringCapitalize(receiver.String())), nil
		}), nil
	case "capitalize!":
		return NewAutoBuiltin("string.capitalize!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.capitalize! does not take arguments")
			}
			updated := stringCapitalize(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "swapcase":
		return NewAutoBuiltin("string.swapcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.swapcase does not take arguments")
			}
			return NewString(stringSwapCase(receiver.String())), nil
		}), nil
	case "swapcase!":
		return NewAutoBuiltin("string.swapcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.swapcase! does not take arguments")
			}
			updated := stringSwapCase(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "reverse":
		return NewAutoBuiltin("string.reverse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.reverse does not take arguments")
			}
			return NewString(stringReverse(receiver.String())), nil
		}), nil
	case "reverse!":
		return NewAutoBuiltin("string.reverse!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.reverse! does not take arguments")
			}
			updated := stringReverse(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "sub":
		return NewAutoBuiltin("string.sub", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.sub expects pattern and replacement")
			}
			regex, err := stringRegexOption("sub", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub replacement must be string")
			}
			updated, err := stringSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.sub invalid regex: %v", err)
			}
			return NewString(updated), nil
		}), nil
	case "sub!":
		return NewAutoBuiltin("string.sub!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.sub! expects pattern and replacement")
			}
			regex, err := stringRegexOption("sub!", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub! pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub! replacement must be string")
			}
			updated, err := stringSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.sub! invalid regex: %v", err)
			}
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "gsub":
		return NewAutoBuiltin("string.gsub", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.gsub expects pattern and replacement")
			}
			regex, err := stringRegexOption("gsub", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub replacement must be string")
			}
			updated, err := stringGSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.gsub invalid regex: %v", err)
			}
			return NewString(updated), nil
		}), nil
	case "gsub!":
		return NewAutoBuiltin("string.gsub!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.gsub! expects pattern and replacement")
			}
			regex, err := stringRegexOption("gsub!", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub! pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub! replacement must be string")
			}
			updated, err := stringGSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.gsub! invalid regex: %v", err)
			}
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "split":
		return NewAutoBuiltin("string.split", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.split accepts at most one separator")
			}
			text := receiver.String()
			var parts []string
			if len(args) == 0 {
				parts = strings.Fields(text)
			} else {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("string.split separator must be string")
				}
				parts = strings.Split(text, args[0].String())
			}
			values := make([]Value, len(parts))
			for i, part := range parts {
				values[i] = NewString(part)
			}
			return NewArray(values), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

func durationMember(d Duration, property string, pos Position) (Value, error) {
	switch property {
	case "seconds", "second":
		return NewInt(d.Seconds()), nil
	case "minutes", "minute":
		return NewInt(d.Seconds() / 60), nil
	case "hours", "hour":
		return NewInt(d.Seconds() / 3600), nil
	case "days", "day":
		return NewInt(d.Seconds() / 86400), nil
	case "weeks", "week":
		return NewInt(d.Seconds() / 604800), nil
	case "in_seconds":
		return NewFloat(float64(d.Seconds())), nil
	case "in_minutes":
		return NewFloat(float64(d.Seconds()) / 60), nil
	case "in_hours":
		return NewFloat(float64(d.Seconds()) / 3600), nil
	case "in_days":
		return NewFloat(float64(d.Seconds()) / 86400), nil
	case "in_weeks":
		return NewFloat(float64(d.Seconds()) / 604800), nil
	case "in_months":
		return NewFloat(float64(d.Seconds()) / (30 * 86400)), nil
	case "in_years":
		return NewFloat(float64(d.Seconds()) / (365 * 86400)), nil
	case "iso8601":
		return NewString(d.iso8601()), nil
	case "parts":
		p := d.parts()
		return NewHash(map[string]Value{
			"days":    NewInt(p["days"]),
			"hours":   NewInt(p["hours"]),
			"minutes": NewInt(p["minutes"]),
			"seconds": NewInt(p["seconds"]),
		}), nil
	case "to_i":
		return NewInt(d.Seconds()), nil
	case "to_s":
		return NewString(d.String()), nil
	case "format":
		return NewString(d.String()), nil
	case "eql?":
		return NewBuiltin("duration.eql?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindDuration {
				return NewNil(), fmt.Errorf("duration.eql? expects a duration")
			}
			return NewBool(d.Seconds() == args[0].Duration().Seconds()), nil
		}), nil
	case "after", "since", "from_now":
		return NewBuiltin("duration.after", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			start, err := durationTimeArg(args, true, "after")
			if err != nil {
				return NewNil(), err
			}
			result := start.Add(time.Duration(d.Seconds()) * time.Second).UTC()
			return NewTime(result), nil
		}), nil
	case "ago", "before", "until":
		return NewBuiltin("duration.before", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			start, err := durationTimeArg(args, true, "before")
			if err != nil {
				return NewNil(), err
			}
			result := start.Add(-time.Duration(d.Seconds()) * time.Second).UTC()
			return NewTime(result), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown duration method %s", property)
	}
}

func durationTimeArg(args []Value, allowEmpty bool, name string) (time.Time, error) {
	if len(args) == 0 {
		if allowEmpty {
			return time.Now().UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%s expects a time argument", name)
	}
	if len(args) != 1 {
		return time.Time{}, fmt.Errorf("%s expects at most one time argument", name)
	}
	val := args[0]
	switch val.Kind() {
	case KindString:
		t, err := time.Parse(time.RFC3339, val.String())
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time: %v", err)
		}
		return t.UTC(), nil
	case KindTime:
		return val.Time(), nil
	default:
		return time.Time{}, fmt.Errorf("%s expects a Time or RFC3339 string", name)
	}
}

func arrayMember(array Value, property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("array.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.size does not take arguments")
			}
			return NewInt(int64(len(receiver.Array()))), nil
		}), nil
	case "each":
		return NewAutoBuiltin("array.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.each"); err != nil {
				return NewNil(), err
			}
			for _, item := range receiver.Array() {
				if _, err := exec.CallBlock(block, []Value{item}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "map":
		return NewAutoBuiltin("array.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.map"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			result := make([]Value, len(arr))
			for i, item := range arr {
				val, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				result[i] = val
			}
			return NewArray(result), nil
		}), nil
	case "select":
		return NewAutoBuiltin("array.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.select"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			for _, item := range arr {
				val, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if val.Truthy() {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "find":
		return NewAutoBuiltin("array.find", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.find does not take arguments")
			}
			if err := ensureBlock(block, "array.find"); err != nil {
				return NewNil(), err
			}
			for _, item := range receiver.Array() {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					return item, nil
				}
			}
			return NewNil(), nil
		}), nil
	case "find_index":
		return NewAutoBuiltin("array.find_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.find_index does not take arguments")
			}
			if err := ensureBlock(block, "array.find_index"); err != nil {
				return NewNil(), err
			}
			for idx, item := range receiver.Array() {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					return NewInt(int64(idx)), nil
				}
			}
			return NewNil(), nil
		}), nil
	case "reduce":
		return NewAutoBuiltin("array.reduce", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := ensureBlock(block, "array.reduce"); err != nil {
				return NewNil(), err
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.reduce accepts at most one initial value")
			}
			arr := receiver.Array()
			if len(arr) == 0 && len(args) == 0 {
				return NewNil(), fmt.Errorf("array.reduce on empty array requires an initial value")
			}
			var acc Value
			start := 0
			if len(args) == 1 {
				acc = args[0]
			} else {
				acc = arr[0]
				start = 1
			}
			for i := start; i < len(arr); i++ {
				next, err := exec.CallBlock(block, []Value{acc, arr[i]})
				if err != nil {
					return NewNil(), err
				}
				acc = next
			}
			return acc, nil
		}), nil
	case "include?":
		return NewAutoBuiltin("array.include?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("array.include? expects exactly one value")
			}
			for _, item := range receiver.Array() {
				if item.Equal(args[0]) {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "index":
		return NewAutoBuiltin("array.index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("array.index expects value and optional offset")
			}
			offset := 0
			if len(args) == 2 {
				n, err := valueToInt(args[1])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.index offset must be non-negative integer")
				}
				offset = n
			}
			arr := receiver.Array()
			if offset >= len(arr) {
				return NewNil(), nil
			}
			for idx := offset; idx < len(arr); idx++ {
				if arr[idx].Equal(args[0]) {
					return NewInt(int64(idx)), nil
				}
			}
			return NewNil(), nil
		}), nil
	case "rindex":
		return NewAutoBuiltin("array.rindex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("array.rindex expects value and optional offset")
			}
			offset := -1
			if len(args) == 2 {
				n, err := valueToInt(args[1])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.rindex offset must be non-negative integer")
				}
				offset = n
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewNil(), nil
			}
			if offset < 0 {
				offset = len(arr) - 1
			}
			if offset >= len(arr) {
				offset = len(arr) - 1
			}
			for idx := offset; idx >= 0; idx-- {
				if arr[idx].Equal(args[0]) {
					return NewInt(int64(idx)), nil
				}
			}
			return NewNil(), nil
		}), nil
	case "count":
		return NewAutoBuiltin("array.count", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.count accepts at most one value argument")
			}
			arr := receiver.Array()
			if len(args) == 1 {
				if block.Block() != nil {
					return NewNil(), fmt.Errorf("array.count does not accept both argument and block")
				}
				total := int64(0)
				for _, item := range arr {
					if item.Equal(args[0]) {
						total++
					}
				}
				return NewInt(total), nil
			}
			if block.Block() == nil {
				return NewInt(int64(len(arr))), nil
			}
			total := int64(0)
			for _, item := range arr {
				include, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if include.Truthy() {
					total++
				}
			}
			return NewInt(total), nil
		}), nil
	case "any?":
		return NewAutoBuiltin("array.any?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.any? does not take arguments")
			}
			for _, item := range receiver.Array() {
				if block.Block() != nil {
					val, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					if val.Truthy() {
						return NewBool(true), nil
					}
					continue
				}
				if item.Truthy() {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "all?":
		return NewAutoBuiltin("array.all?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.all? does not take arguments")
			}
			for _, item := range receiver.Array() {
				if block.Block() != nil {
					val, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					if !val.Truthy() {
						return NewBool(false), nil
					}
					continue
				}
				if !item.Truthy() {
					return NewBool(false), nil
				}
			}
			return NewBool(true), nil
		}), nil
	case "none?":
		return NewAutoBuiltin("array.none?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.none? does not take arguments")
			}
			for _, item := range receiver.Array() {
				if block.Block() != nil {
					val, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					if val.Truthy() {
						return NewBool(false), nil
					}
					continue
				}
				if item.Truthy() {
					return NewBool(false), nil
				}
			}
			return NewBool(true), nil
		}), nil
	case "push":
		return NewBuiltin("array.push", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("array.push expects at least one argument")
			}
			base := receiver.Array()
			out := make([]Value, len(base)+len(args))
			copy(out, base)
			copy(out[len(base):], args)
			return NewArray(out), nil
		}), nil
	case "pop":
		return NewAutoBuiltin("array.pop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.pop accepts at most one argument")
			}
			count := 1
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.pop expects non-negative integer")
				}
				count = n
			}
			arr := receiver.Array()
			if count == 0 {
				return NewHash(map[string]Value{
					"array":  NewArray(arr),
					"popped": NewNil(),
				}), nil
			}
			if len(arr) == 0 {
				popped := NewNil()
				if len(args) == 1 {
					popped = NewArray([]Value{})
				}
				return NewHash(map[string]Value{
					"array":  NewArray([]Value{}),
					"popped": popped,
				}), nil
			}
			if count > len(arr) {
				count = len(arr)
			}
			remaining := make([]Value, len(arr)-count)
			copy(remaining, arr[:len(arr)-count])
			removed := make([]Value, count)
			copy(removed, arr[len(arr)-count:])
			result := map[string]Value{
				"array": NewArray(remaining),
			}
			if count == 1 && len(args) == 0 {
				result["popped"] = removed[0]
			} else {
				result["popped"] = NewArray(removed)
			}
			return NewHash(result), nil
		}), nil
	case "uniq":
		return NewAutoBuiltin("array.uniq", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.uniq does not take arguments")
			}
			arr := receiver.Array()
			unique := make([]Value, 0, len(arr))
			for _, item := range arr {
				found := false
				for _, existing := range unique {
					if item.Equal(existing) {
						found = true
						break
					}
				}
				if !found {
					unique = append(unique, item)
				}
			}
			return NewArray(unique), nil
		}), nil
	case "first":
		return NewAutoBuiltin("array.first", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			if len(args) == 0 {
				if len(arr) == 0 {
					return NewNil(), nil
				}
				return arr[0], nil
			}
			n, err := valueToInt(args[0])
			if err != nil || n < 0 {
				return NewNil(), fmt.Errorf("array.first expects non-negative integer")
			}
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[:n])
			return NewArray(out), nil
		}), nil
	case "last":
		return NewAutoBuiltin("array.last", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			if len(args) == 0 {
				if len(arr) == 0 {
					return NewNil(), nil
				}
				return arr[len(arr)-1], nil
			}
			n, err := valueToInt(args[0])
			if err != nil || n < 0 {
				return NewNil(), fmt.Errorf("array.last expects non-negative integer")
			}
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[len(arr)-n:])
			return NewArray(out), nil
		}), nil
	case "sum":
		return NewAutoBuiltin("array.sum", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			total := NewInt(0)
			for _, item := range arr {
				switch item.Kind() {
				case KindInt, KindFloat:
				default:
					return NewNil(), fmt.Errorf("array.sum supports numeric values")
				}
				sum, err := addValues(total, item)
				if err != nil {
					return NewNil(), fmt.Errorf("array.sum supports numeric values")
				}
				total = sum
			}
			return total, nil
		}), nil
	case "compact":
		return NewAutoBuiltin("array.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.compact does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			for _, item := range arr {
				if item.Kind() != KindNil {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "flatten":
		return NewAutoBuiltin("array.flatten", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// depth=-1 is a sentinel value meaning "flatten fully" (no depth limit)
			depth := -1
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.flatten accepts at most one depth argument")
			}
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.flatten depth must be non-negative integer")
				}
				depth = n
			}
			arr := receiver.Array()
			out := flattenValues(arr, depth)
			return NewArray(out), nil
		}), nil
	case "join":
		return NewAutoBuiltin("array.join", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.join accepts at most one separator")
			}
			sep := ""
			if len(args) == 1 {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("array.join separator must be string")
				}
				sep = args[0].String()
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewString(""), nil
			}
			// Use strings.Builder for efficient concatenation
			var b strings.Builder
			for i, item := range arr {
				if i > 0 {
					b.WriteString(sep)
				}
				b.WriteString(item.String())
			}
			return NewString(b.String()), nil
		}), nil
	case "reverse":
		return NewAutoBuiltin("array.reverse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.reverse does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, len(arr))
			for i, item := range arr {
				out[len(arr)-1-i] = item
			}
			return NewArray(out), nil
		}), nil
	case "sort":
		return NewAutoBuiltin("array.sort", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, len(arr))
			copy(out, arr)
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				if block.Block() != nil {
					cmpValue, err := exec.CallBlock(block, []Value{out[i], out[j]})
					if err != nil {
						sortErr = err
						return false
					}
					cmp, err := sortComparisonResult(cmpValue)
					if err != nil {
						sortErr = fmt.Errorf("array.sort block must return numeric comparator")
						return false
					}
					return cmp < 0
				}
				cmp, err := arraySortCompareValues(out[i], out[j])
				if err != nil {
					sortErr = fmt.Errorf("array.sort values are not comparable")
					return false
				}
				return cmp < 0
			})
			if sortErr != nil {
				return NewNil(), sortErr
			}
			return NewArray(out), nil
		}), nil
	case "sort_by":
		return NewAutoBuiltin("array.sort_by", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort_by does not take arguments")
			}
			if err := ensureBlock(block, "array.sort_by"); err != nil {
				return NewNil(), err
			}
			type itemWithSortKey struct {
				item  Value
				key   Value
				index int
			}
			arr := receiver.Array()
			withKeys := make([]itemWithSortKey, len(arr))
			for i, item := range arr {
				sortKey, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				withKeys[i] = itemWithSortKey{item: item, key: sortKey, index: i}
			}
			var sortErr error
			sort.SliceStable(withKeys, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := arraySortCompareValues(withKeys[i].key, withKeys[j].key)
				if err != nil {
					sortErr = fmt.Errorf("array.sort_by block values are not comparable")
					return false
				}
				if cmp == 0 {
					return withKeys[i].index < withKeys[j].index
				}
				return cmp < 0
			})
			if sortErr != nil {
				return NewNil(), sortErr
			}
			out := make([]Value, len(withKeys))
			for i, item := range withKeys {
				out[i] = item.item
			}
			return NewArray(out), nil
		}), nil
	case "partition":
		return NewAutoBuiltin("array.partition", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.partition does not take arguments")
			}
			if err := ensureBlock(block, "array.partition"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			left := make([]Value, 0, len(arr))
			right := make([]Value, 0, len(arr))
			for _, item := range arr {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					left = append(left, item)
				} else {
					right = append(right, item)
				}
			}
			return NewArray([]Value{NewArray(left), NewArray(right)}), nil
		}), nil
	case "group_by":
		return NewAutoBuiltin("array.group_by", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.group_by does not take arguments")
			}
			if err := ensureBlock(block, "array.group_by"); err != nil {
				return NewNil(), err
			}
			groups := make(map[string][]Value)
			for _, item := range receiver.Array() {
				groupValue, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				key, err := valueToHashKey(groupValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.group_by block must return symbol or string")
				}
				groups[key] = append(groups[key], item)
			}
			result := make(map[string]Value, len(groups))
			for key, items := range groups {
				result[key] = NewArray(items)
			}
			return NewHash(result), nil
		}), nil
	case "tally":
		return NewAutoBuiltin("array.tally", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.tally does not take arguments")
			}
			counts := make(map[string]int64)
			for _, item := range receiver.Array() {
				keyValue := item
				if block.Block() != nil {
					mapped, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					keyValue = mapped
				}
				key, err := valueToHashKey(keyValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.tally values must be symbol or string")
				}
				counts[key]++
			}
			result := make(map[string]Value, len(counts))
			for key, count := range counts {
				result[key] = NewInt(count)
			}
			return NewHash(result), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

func (e *Engine) Compile(source string) (*Script, error) {
	p := newParser(source)
	program, parseErrors := p.ParseProgram()
	if len(parseErrors) > 0 {
		return nil, combineErrors(parseErrors)
	}

	functions := make(map[string]*ScriptFunction)
	classes := make(map[string]*ClassDef)

	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *FunctionStmt:
			if _, exists := functions[s.Name]; exists {
				return nil, fmt.Errorf("duplicate function %s", s.Name)
			}
			functions[s.Name] = &ScriptFunction{Name: s.Name, Params: s.Params, ReturnTy: s.ReturnTy, Body: s.Body, Pos: s.Pos()}
		case *ClassStmt:
			if _, exists := classes[s.Name]; exists {
				return nil, fmt.Errorf("duplicate class %s", s.Name)
			}
			classDef := &ClassDef{
				Name:         s.Name,
				Methods:      make(map[string]*ScriptFunction),
				ClassMethods: make(map[string]*ScriptFunction),
				ClassVars:    make(map[string]Value),
				Body:         s.Body,
			}
			for _, prop := range s.Properties {
				for _, name := range prop.Names {
					if prop.Kind == "property" || prop.Kind == "getter" {
						getter := &ScriptFunction{
							Name: name,
							Body: []Statement{&ReturnStmt{Value: &IvarExpr{Name: name, position: prop.position}, position: prop.position}},
							Pos:  prop.position,
						}
						classDef.Methods[name] = getter
					}
					if prop.Kind == "property" || prop.Kind == "setter" {
						setter := &ScriptFunction{
							Name: name + "=",
							Params: []Param{{
								Name: "value",
							}},
							Body: []Statement{
								&AssignStmt{
									Target:   &IvarExpr{Name: name, position: prop.position},
									Value:    &Identifier{Name: "value", position: prop.position},
									position: prop.position,
								},
								&ReturnStmt{Value: &Identifier{Name: "value", position: prop.position}, position: prop.position},
							},
							Pos: prop.position,
						}
						classDef.Methods[name+"="] = setter
					}
				}
			}
			for _, fn := range s.Methods {
				classDef.Methods[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, ReturnTy: fn.ReturnTy, Body: fn.Body, Pos: fn.Pos(), Private: fn.Private}
			}
			for _, fn := range s.ClassMethods {
				classDef.ClassMethods[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, ReturnTy: fn.ReturnTy, Body: fn.Body, Pos: fn.Pos(), Private: fn.Private}
			}
			classes[s.Name] = classDef
		default:
			return nil, fmt.Errorf("unsupported top-level statement %T", stmt)
		}
	}

	script := &Script{engine: e, functions: functions, classes: classes, source: source}
	script.bindFunctionOwnership()
	return script, nil
}

func combineErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := ""
	for _, err := range errs {
		if msg != "" {
			msg += "\n\n"
		}
		msg += err.Error()
	}
	return errors.New(msg)
}

func (exec *Execution) bindFunctionArgs(fn *ScriptFunction, env *Env, args []Value, kwargs map[string]Value, pos Position) error {
	usedKw := make(map[string]bool, len(kwargs))
	argIdx := 0

	for _, param := range fn.Params {
		var val Value
		if argIdx < len(args) {
			val = args[argIdx]
			argIdx++
		} else if kw, ok := kwargs[param.Name]; ok {
			val = kw
			usedKw[param.Name] = true
		} else if param.DefaultVal != nil {
			defaultVal, err := exec.evalExpressionWithAuto(param.DefaultVal, env, true)
			if err != nil {
				return err
			}
			val = defaultVal
		} else {
			return exec.errorAt(pos, "missing argument %s", param.Name)
		}

		if param.Type != nil {
			if err := checkValueType(val, param.Type); err != nil {
				return exec.errorAt(pos, "%s", err.Error())
			}
		}
		env.Define(param.Name, val)
		if param.IsIvar {
			if selfVal, ok := env.Get("self"); ok && selfVal.Kind() == KindInstance {
				inst := selfVal.Instance()
				if inst != nil {
					inst.Ivars[param.Name] = val
				}
			}
		}
	}

	if argIdx < len(args) {
		return exec.errorAt(pos, "unexpected positional arguments")
	}
	for name := range kwargs {
		if !usedKw[name] {
			return exec.errorAt(pos, "unexpected keyword argument %s", name)
		}
	}
	return nil
}

func checkValueType(val Value, ty *TypeExpr) error {
	if ty.Nullable && val.Kind() == KindNil {
		return nil
	}
	switch ty.Kind {
	case TypeAny:
		return nil
	case TypeInt:
		if val.Kind() == KindInt {
			return nil
		}
		return fmt.Errorf("expected int")
	case TypeFloat:
		if val.Kind() == KindFloat {
			return nil
		}
		return fmt.Errorf("expected float")
	case TypeNumber:
		if val.Kind() == KindInt || val.Kind() == KindFloat {
			return nil
		}
		return fmt.Errorf("expected number")
	case TypeString:
		if val.Kind() == KindString {
			return nil
		}
		return fmt.Errorf("expected string")
	case TypeBool:
		if val.Kind() == KindBool {
			return nil
		}
		return fmt.Errorf("expected bool")
	case TypeNil:
		if val.Kind() == KindNil {
			return nil
		}
		return fmt.Errorf("expected nil")
	case TypeDuration:
		if val.Kind() == KindDuration {
			return nil
		}
		return fmt.Errorf("expected duration")
	case TypeTime:
		if val.Kind() == KindTime {
			return nil
		}
		return fmt.Errorf("expected time")
	case TypeMoney:
		if val.Kind() == KindMoney {
			return nil
		}
		return fmt.Errorf("expected money")
	case TypeArray:
		if val.Kind() == KindArray {
			return nil
		}
		return fmt.Errorf("expected array")
	case TypeHash:
		if val.Kind() == KindHash || val.Kind() == KindObject {
			return nil
		}
		return fmt.Errorf("expected hash")
	case TypeFunction:
		if val.Kind() == KindFunction {
			return nil
		}
		return fmt.Errorf("expected function")
	default:
		return fmt.Errorf("unknown type %s", ty.Name)
	}
}

// Function looks up a compiled function by name.
func (s *Script) Function(name string) (*ScriptFunction, bool) {
	fn, ok := s.functions[name]
	return fn, ok
}

func (s *Script) bindFunctionOwnership() {
	for _, fn := range s.functions {
		fn.owner = s
	}
	for _, classDef := range s.classes {
		classDef.owner = s
		for _, fn := range classDef.Methods {
			fn.owner = s
		}
		for _, fn := range classDef.ClassMethods {
			fn.owner = s
		}
	}
}

func cloneFunctionsForCall(functions map[string]*ScriptFunction, env *Env) map[string]*ScriptFunction {
	cloned := make(map[string]*ScriptFunction, len(functions))
	for name, fn := range functions {
		cloned[name] = cloneFunctionForEnv(fn, env)
	}
	return cloned
}

func cloneClassesForCall(classes map[string]*ClassDef, env *Env) map[string]*ClassDef {
	cloned := make(map[string]*ClassDef, len(classes))
	for name, classDef := range classes {
		classClone := &ClassDef{
			Name:         classDef.Name,
			Methods:      make(map[string]*ScriptFunction, len(classDef.Methods)),
			ClassMethods: make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
			ClassVars:    make(map[string]Value),
			Body:         classDef.Body,
			owner:        classDef.owner,
		}
		for methodName, method := range classDef.Methods {
			classClone.Methods[methodName] = cloneFunctionForEnv(method, env)
		}
		for methodName, method := range classDef.ClassMethods {
			classClone.ClassMethods[methodName] = cloneFunctionForEnv(method, env)
		}
		cloned[name] = classClone
	}
	return cloned
}

func (s *Script) Call(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	_, ok := s.functions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}

	root := newEnv(nil)
	for n, builtin := range s.engine.builtins {
		root.Define(n, builtin)
	}

	callFunctions := cloneFunctionsForCall(s.functions, root)
	fn, ok := callFunctions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}
	for n, fnDecl := range callFunctions {
		root.Define(n, NewFunction(fnDecl))
	}

	callClasses := cloneClassesForCall(s.classes, root)
	for n, classDef := range callClasses {
		root.Define(n, NewClass(classDef))
	}
	rebinder := newCallFunctionRebinder(s, root, callClasses)

	exec := &Execution{
		engine:          s.engine,
		script:          s,
		ctx:             ctx,
		quota:           s.engine.config.StepQuota,
		memoryQuota:     s.engine.config.MemoryQuotaBytes,
		recursionCap:    s.engine.config.RecursionLimit,
		callStack:       make([]callFrame, 0, 8),
		root:            root,
		modules:         make(map[string]Value),
		moduleLoading:   make(map[string]bool),
		moduleLoadStack: make([]string, 0, 8),
		moduleStack:     make([]moduleContext, 0, 8),
		receiverStack:   make([]Value, 0, 8),
		envStack:        make([]*Env, 0, 8),
		strictEffects:   s.engine.config.StrictEffects,
		allowRequire:    opts.AllowRequire,
	}

	if len(opts.Capabilities) > 0 {
		binding := CapabilityBinding{Context: exec.ctx, Engine: s.engine}
		for _, adapter := range opts.Capabilities {
			if adapter == nil {
				continue
			}
			globals, err := adapter.Bind(binding)
			if err != nil {
				return NewNil(), err
			}
			for name, val := range globals {
				root.Define(name, rebinder.rebindValue(val))
			}
		}
	}

	if exec.strictEffects {
		if err := validateStrictGlobals(opts.Globals); err != nil {
			return NewNil(), err
		}
	}

	for n, val := range opts.Globals {
		root.Define(n, rebinder.rebindValue(val))
	}

	if err := exec.checkMemory(); err != nil {
		return NewNil(), err
	}

	// initialize class bodies (class vars)
	for name, classDef := range callClasses {
		if len(classDef.Body) == 0 {
			continue
		}
		classVal, _ := root.Get(name)
		env := newEnv(root)
		env.Define("self", classVal)
		exec.pushReceiver(classVal)
		_, _, err := exec.evalStatements(classDef.Body, env)
		exec.popReceiver()
		if err != nil {
			return NewNil(), err
		}
	}

	callEnv := newEnv(root)
	callArgs := rebinder.rebindValues(args)
	callKeywords := rebinder.rebindKeywords(opts.Keywords)
	if err := exec.bindFunctionArgs(fn, callEnv, callArgs, callKeywords, fn.Pos); err != nil {
		return NewNil(), err
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return NewNil(), err
	}
	exec.popEnv()

	if err := exec.pushFrame(fn.Name, fn.Pos); err != nil {
		return NewNil(), err
	}
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if fn.ReturnTy != nil {
		if err := checkValueType(val, fn.ReturnTy); err != nil {
			return NewNil(), err
		}
	}
	if err := exec.checkMemoryWith(val); err != nil {
		return NewNil(), err
	}
	if returned {
		return val, nil
	}
	return val, nil
}

func valueToHashKey(val Value) (string, error) {
	switch val.Kind() {
	case KindSymbol:
		return val.String(), nil
	case KindString:
		return val.String(), nil
	default:
		return "", fmt.Errorf("unsupported hash key type %v", val.Kind())
	}
}

func valueToInt64(val Value) (int64, error) {
	switch val.Kind() {
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		return int64(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer value")
	}
}

func valueToInt(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		return int(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer index")
	}
}

func sortComparisonResult(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		switch {
		case val.Int() < 0:
			return -1, nil
		case val.Int() > 0:
			return 1, nil
		default:
			return 0, nil
		}
	case KindFloat:
		switch {
		case val.Float() < 0:
			return -1, nil
		case val.Float() > 0:
			return 1, nil
		default:
			return 0, nil
		}
	default:
		return 0, fmt.Errorf("comparator must be numeric")
	}
}

func arraySortCompareValues(left, right Value) (int, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		switch {
		case left.Int() < right.Int():
			return -1, nil
		case left.Int() > right.Int():
			return 1, nil
		default:
			return 0, nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		switch {
		case left.Float() < right.Float():
			return -1, nil
		case left.Float() > right.Float():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindSymbol && right.Kind() == KindSymbol:
		switch {
		case left.String() < right.String():
			return -1, nil
		case left.String() > right.String():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindBool && right.Kind() == KindBool:
		switch {
		case !left.Bool() && right.Bool():
			return -1, nil
		case left.Bool() && !right.Bool():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		switch {
		case left.Duration().Seconds() < right.Duration().Seconds():
			return -1, nil
		case left.Duration().Seconds() > right.Duration().Seconds():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return -1, nil
		case left.Time().After(right.Time()):
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return 0, fmt.Errorf("money values with different currencies")
		}
		switch {
		case left.Money().Cents() < right.Money().Cents():
			return -1, nil
		case left.Money().Cents() > right.Money().Cents():
			return 1, nil
		default:
			return 0, nil
		}
	case left.Kind() == KindNil && right.Kind() == KindNil:
		return 0, nil
	default:
		return 0, fmt.Errorf("values are not comparable")
	}
}

// flattenValues recursively flattens nested arrays up to the specified depth.
// depth=-1 means flatten completely (no limit).
// depth=0 means don't flatten at all.
// depth=1 means flatten one level, etc.
func flattenValues(values []Value, depth int) []Value {
	out := make([]Value, 0, len(values))
	for _, v := range values {
		if v.Kind() == KindArray && depth != 0 {
			nextDepth := depth
			if nextDepth > 0 {
				nextDepth--
			}
			out = append(out, flattenValues(v.Array(), nextDepth)...)
		} else {
			out = append(out, v)
		}
	}
	return out
}

func floatToInt64Checked(v float64, method string) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s result out of int64 range", method)
	}
	// float64(math.MaxInt64) rounds to 2^63, so use >= 2^63 as the true upper bound.
	if v < float64(math.MinInt64) || v >= math.Exp2(63) {
		return 0, fmt.Errorf("%s result out of int64 range", method)
	}
	return int64(v), nil
}

func addValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() + right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() + right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		return NewTime(left.Time().Add(time.Duration(right.Duration().Seconds()) * time.Second)), nil
	case right.Kind() == KindTime && left.Kind() == KindDuration:
		return NewTime(right.Time().Add(time.Duration(left.Duration().Seconds()) * time.Second)), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		return NewDuration(Duration{seconds: left.Duration().Seconds() + right.Duration().Seconds()}), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() + secs}), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported addition operands")
		}
		return NewDuration(Duration{seconds: right.Duration().Seconds() + secs}), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, len(lArr)+len(rArr))
		copy(out, lArr)
		copy(out[len(lArr):], rArr)
		return NewArray(out), nil
	case left.Kind() == KindString || right.Kind() == KindString:
		return NewString(left.String() + right.String()), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		sum, err := left.Money().add(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(sum), nil
	default:
		return NewNil(), fmt.Errorf("unsupported addition operands")
	}
}

func subtractValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() - right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() - right.Float()), nil
	case left.Kind() == KindTime && right.Kind() == KindDuration:
		return NewTime(left.Time().Add(-time.Duration(right.Duration().Seconds()) * time.Second)), nil
	case left.Kind() == KindTime && right.Kind() == KindTime:
		diff := left.Time().Sub(right.Time())
		return NewDuration(Duration{seconds: int64(diff / time.Second)}), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		return NewDuration(Duration{seconds: left.Duration().Seconds() - right.Duration().Seconds()}), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported subtraction operands")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() - secs}), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, 0, len(lArr))
		for _, item := range lArr {
			found := false
			for _, remove := range rArr {
				if item.Equal(remove) {
					found = true
					break
				}
			}
			if !found {
				out = append(out, item)
			}
		}
		return NewArray(out), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		diff, err := left.Money().sub(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(diff), nil
	default:
		return NewNil(), fmt.Errorf("unsupported subtraction operands")
	}
}

func multiplyValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() * right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() * right.Float()), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported multiplication operands")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() * secs}), nil
	case right.Kind() == KindDuration && (left.Kind() == KindInt || left.Kind() == KindFloat):
		secs, err := valueToInt64(left)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported multiplication operands")
		}
		return NewDuration(Duration{seconds: right.Duration().Seconds() * secs}), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		return NewMoney(left.Money().mulInt(right.Int())), nil
	case left.Kind() == KindInt && right.Kind() == KindMoney:
		return NewMoney(right.Money().mulInt(left.Int())), nil
	default:
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}
}

func divideValues(left, right Value) (Value, error) {
	switch {
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		if right.Float() == 0 {
			return NewNil(), errors.New("division by zero")
		}
		return NewFloat(left.Float() / right.Float()), nil
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		if right.Duration().Seconds() == 0 {
			return NewNil(), errors.New("division by zero")
		}
		return NewFloat(float64(left.Duration().Seconds()) / float64(right.Duration().Seconds())), nil
	case left.Kind() == KindDuration && (right.Kind() == KindInt || right.Kind() == KindFloat):
		secs, err := valueToInt64(right)
		if err != nil {
			return NewNil(), fmt.Errorf("unsupported division operands")
		}
		if secs == 0 {
			return NewNil(), errors.New("division by zero")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() / secs}), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		res, err := left.Money().divInt(right.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(res), nil
	default:
		return NewNil(), fmt.Errorf("unsupported division operands")
	}
}

func moduloValues(left, right Value) (Value, error) {
	if left.Kind() == KindInt && right.Kind() == KindInt {
		if right.Int() == 0 {
			return NewNil(), errors.New("modulo by zero")
		}
		return NewInt(left.Int() % right.Int()), nil
	}
	if left.Kind() == KindDuration && right.Kind() == KindDuration {
		if right.Duration().Seconds() == 0 {
			return NewNil(), errors.New("modulo by zero")
		}
		return NewDuration(Duration{seconds: left.Duration().Seconds() % right.Duration().Seconds()}), nil
	}
	return NewNil(), fmt.Errorf("unsupported modulo operands")
}

func compareValues(expr *BinaryExpr, left, right Value, cmp func(int) bool) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		diff := left.Int() - right.Int()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		lf, rf := left.Float(), right.Float()
		switch {
		case lf < rf:
			return NewBool(cmp(-1)), nil
		case lf > rf:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return NewBool(cmp(-1)), nil
		case left.String() > right.String():
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return NewNil(), fmt.Errorf("money currency mismatch for comparison")
		}
		diff := left.Money().Cents() - right.Money().Cents()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindDuration && right.Kind() == KindDuration:
		diff := left.Duration().Seconds() - right.Duration().Seconds()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindTime && right.Kind() == KindTime:
		switch {
		case left.Time().Before(right.Time()):
			return NewBool(cmp(-1)), nil
		case left.Time().After(right.Time()):
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	default:
		return NewNil(), fmt.Errorf("unsupported comparison operands")
	}
}
