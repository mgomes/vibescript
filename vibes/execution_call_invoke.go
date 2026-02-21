package vibes

func executeFunctionForCall(exec *Execution, fn *ScriptFunction, callEnv *Env) (Value, error) {
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
			return NewNil(), exec.errorAt(fn.Pos, "%s", formatReturnTypeMismatch(fn.Name, err))
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
