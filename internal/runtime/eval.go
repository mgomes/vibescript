package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/mgomes/vibescript/internal/ast"
)

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
			return NewNil(), exec.errorAt(e.Pos(), "undefined variable %s%s", e.Name, didYouMean(e.Name, env.visibleNames()))
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
	case *ScopeExpr:
		obj, err := exec.evalExpressionWithAuto(e.Object, env, true)
		if err != nil {
			return NewNil(), err
		}
		member, err := exec.getScopedMember(obj, e.Property, e.Pos())
		if err != nil {
			return NewNil(), err
		}
		return member, nil
	case *IndexExpr:
		return exec.evalIndexExpr(e, env)
	case *IvarExpr:
		self, ok := env.Get("self")
		if !ok || self.Kind() != KindInstance {
			return NewNil(), exec.errorAt(e.Pos(), "no instance context for ivar")
		}
		val, ok := valueInstance(self).Ivars[e.Name]
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
			val, ok := valueInstance(self).Class.ClassVars[e.Name]
			if !ok {
				return NewNil(), nil
			}
			return val, nil
		case KindClass:
			val, ok := valueClass(self).ClassVars[e.Name]
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
	if err := exec.checkMemoryWith(left); err != nil {
		return NewNil(), err
	}
	switch expr.Operator {
	case tokenAnd:
		// Short-circuit and yield the operand value, not a coerced bool
		// (Ruby semantics): `a && b` is `a ? b : a`. A falsy left operand is
		// the result; otherwise the right operand is, whatever its value.
		if !left.Truthy() {
			return left, nil
		}
		right, err := exec.evalExpression(expr.Right, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(left, right); err != nil {
			return NewNil(), err
		}
		return right, nil
	case tokenOr:
		// Short-circuit and yield the operand value, not a coerced bool
		// (Ruby semantics): `a || b` is `a ? a : b`. This is what makes the
		// `value = optional || default` idiom work; previously it collapsed
		// to `true`/`false`.
		if left.Truthy() {
			return left, nil
		}
		right, err := exec.evalExpression(expr.Right, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(left, right); err != nil {
			return NewNil(), err
		}
		return right, nil
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
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported operator")
	}

	if err != nil {
		return NewNil(), exec.wrapError(err, expr.Pos())
	}
	return result, nil
}

func (exec *Execution) evalBlockLiteral(block *BlockLiteral, env *Env) (Value, error) {
	blockValue := NewBlock(block.Params, block.Body, env)
	blk := valueBlock(blockValue)
	if ctx := exec.currentModuleContext(); ctx != nil && ctx.script != nil {
		blk.owner = ctx.script
	} else {
		blk.owner = exec.script
	}
	if ctx := exec.currentModuleContext(); ctx != nil {
		blk.moduleKey = ctx.key
		blk.modulePath = ctx.path
		blk.moduleRoot = ctx.root
	}
	return blockValue, nil
}

func ensureBlock(block Value, name string) error {
	if valueBlock(block) == nil {
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
	blk := valueBlock(block)
	exec.pushModuleContext(moduleContext{
		key:    blk.moduleKey,
		path:   blk.modulePath,
		root:   blk.moduleRoot,
		script: blk.owner,
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
			normalized, err := normalizeValueForType(val, param.Type, typeContext{
				owner:    blk.owner,
				env:      blk.Env,
				fallback: exec.root,
			})
			if err != nil {
				return NewNil(), exec.errorAt(param.Type.Position, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
			val = normalized
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
	args := make([]Value, 0, len(expr.Args))
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

func (exec *Execution) assignToMember(obj Value, property string, value Value, pos Position) error {
	setterName := property + "="
	var methods map[string]*ScriptFunction
	var vars map[string]Value

	switch obj.Kind() {
	case KindInstance:
		methods = valueInstance(obj).Class.Methods
		vars = valueInstance(obj).Ivars
	case KindClass:
		methods = valueClass(obj).ClassMethods
		vars = valueClass(obj).ClassVars
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
		valueInstance(self).Ivars[t.Name] = value
		return nil
	case *ClassVarExpr:
		self, ok := env.Get("self")
		if !ok {
			return exec.errorAt(target.Pos(), "no class context for class var")
		}
		switch self.Kind() {
		case KindInstance:
			valueInstance(self).Class.ClassVars[t.Name] = value
			return nil
		case KindClass:
			valueClass(self).ClassVars[t.Name] = value
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

func (exec *Execution) evalCaseExpr(expr *CaseExpr, env *Env) (Value, error) {
	target, err := exec.evalExpression(expr.Target, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(target); err != nil {
		return NewNil(), err
	}

	for _, clause := range expr.Clauses {
		matched := false
		for _, candidateExpr := range clause.Values {
			candidate, err := exec.evalExpression(candidateExpr, env)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkMemoryWith(candidate); err != nil {
				return NewNil(), err
			}
			if target.Equal(candidate) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		result, err := exec.evalExpressionWithAuto(clause.Result, env, true)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}

	if expr.ElseExpr != nil {
		result, err := exec.evalExpressionWithAuto(expr.ElseExpr, env, true)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}

	return NewNil(), nil
}

func (exec *Execution) evalForStatement(stmt *ForStmt, env *Env) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

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
				if errors.Is(err, errLoopBreak) {
					return last, false, nil
				}
				if errors.Is(err, errLoopNext) {
					continue
				}
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
					if errors.Is(err, errLoopBreak) {
						return last, false, nil
					}
					if errors.Is(err, errLoopNext) {
						continue
					}
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
					if errors.Is(err, errLoopBreak) {
						return last, false, nil
					}
					if errors.Is(err, errLoopNext) {
						continue
					}
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

func (exec *Execution) evalWhileStatement(stmt *WhileStmt, env *Env) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

	last := NewNil()
	for {
		if err := exec.step(); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Pos())
		}
		condition, err := exec.evalExpression(stmt.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(condition); err != nil {
			return NewNil(), false, err
		}
		if !condition.Truthy() {
			return last, false, nil
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return last, false, nil
			}
			if errors.Is(err, errLoopNext) {
				continue
			}
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		last = val
	}
}

func (exec *Execution) evalUntilStatement(stmt *UntilStmt, env *Env) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

	last := NewNil()
	for {
		if err := exec.step(); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Pos())
		}
		condition, err := exec.evalExpression(stmt.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(condition); err != nil {
			return NewNil(), false, err
		}
		if condition.Truthy() {
			return last, false, nil
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return last, false, nil
			}
			if errors.Is(err, errLoopNext) {
				continue
			}
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		last = val
	}
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
		if s.Value == nil {
			return NewNil(), true, nil
		}
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

func (exec *Execution) evalRaiseStatement(stmt *RaiseStmt, env *Env) (Value, bool, error) {
	if stmt.Value != nil {
		val, err := exec.evalExpression(stmt.Value, env)
		if err != nil {
			return NewNil(), false, err
		}
		return NewNil(), false, exec.errorAt(stmt.Pos(), "%s", val.String())
	}

	err := exec.currentRescuedError()
	if err == nil {
		return NewNil(), false, exec.errorAt(stmt.Pos(), "raise used outside of rescue")
	}
	return NewNil(), false, err
}

func (exec *Execution) evalTryStatement(stmt *TryStmt, env *Env) (Value, bool, error) {
	val, returned, err := exec.evalStatements(stmt.Body, env)

	if err != nil && !isLoopControlSignal(err) && !isHostControlSignal(err) && len(stmt.Rescue) > 0 && runtimeErrorMatchesRescueType(err, stmt.RescueTy) {
		exec.pushRescuedError(err)
		rescueVal, rescueReturned, rescueErr := exec.evalStatements(stmt.Rescue, env)
		exec.popRescuedError()
		if rescueErr != nil {
			val = NewNil()
			returned = false
			err = rescueErr
		} else {
			val = rescueVal
			returned = rescueReturned
			err = nil
		}
	}

	if len(stmt.Ensure) > 0 {
		ensureVal, ensureReturned, ensureErr := exec.evalStatements(stmt.Ensure, env)
		if ensureErr != nil {
			return NewNil(), false, ensureErr
		}
		if ensureReturned {
			return ensureVal, true, nil
		}
	}

	if err != nil {
		return NewNil(), false, err
	}
	return val, returned, nil
}

func isLoopControlSignal(err error) bool {
	return errors.Is(err, errLoopBreak) || errors.Is(err, errLoopNext)
}

func isHostControlSignal(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, errStepQuotaExceeded) ||
		errors.Is(err, errMemoryQuotaExceeded)
}

func runtimeErrorMatchesRescueType(err error, rescueTy *TypeExpr) bool {
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		return false
	}
	if rescueTy == nil {
		return true
	}
	errKind := classifyRuntimeErrorType(err)
	return rescueTypeMatchesErrorKind(rescueTy, errKind)
}

func rescueTypeMatchesErrorKind(ty *TypeExpr, errKind string) bool {
	if ty == nil {
		return false
	}
	if ty.Kind == TypeUnion {
		for _, option := range ty.Union {
			if rescueTypeMatchesErrorKind(option, errKind) {
				return true
			}
		}
		return false
	}
	canonical, ok := ast.CanonicalRuntimeErrorType(ty.Name)
	if !ok {
		return false
	}
	if canonical == runtimeErrorTypeBase {
		return true
	}
	return canonical == errKind
}
