package vibes

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
