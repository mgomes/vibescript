package vibes

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
