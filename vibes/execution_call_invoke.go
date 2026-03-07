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
		normalized, err := normalizeValueForType(val, fn.ReturnTy, typeContext{
			owner:    fn.owner,
			env:      fn.Env,
			fallback: exec.root,
		})
		if err != nil {
			return NewNil(), exec.errorAt(fn.Pos, "%s", formatReturnTypeMismatch(fn.Name, err))
		}
		val = normalized
	}
	if err := exec.checkMemoryWith(val); err != nil {
		return NewNil(), err
	}
	if returned {
		return val, nil
	}
	return val, nil
}
