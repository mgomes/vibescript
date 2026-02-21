package vibes

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
