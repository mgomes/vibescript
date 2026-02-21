package vibes

import (
	"context"
	"errors"
)

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
	canonical, ok := canonicalRuntimeErrorType(ty.Name)
	if !ok {
		return false
	}
	if canonical == runtimeErrorTypeBase {
		return true
	}
	return canonical == errKind
}
