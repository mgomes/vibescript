package vibes

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
