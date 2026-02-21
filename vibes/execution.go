package vibes

import (
	"context"
	"errors"
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
