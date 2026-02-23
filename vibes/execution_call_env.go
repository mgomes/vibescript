package vibes

func prepareCallEnvForFunction(exec *Execution, root *Env, rebinder *callFunctionRebinder, fn *ScriptFunction, args []Value, keywords map[string]Value) (*Env, error) {
	callEnv := newEnvWithCapacity(root, len(fn.Params))
	callArgs := rebinder.rebindValues(args)
	callKeywords := rebinder.rebindKeywords(keywords)
	if err := exec.bindFunctionArgs(fn, callEnv, callArgs, callKeywords, fn.Pos); err != nil {
		return nil, err
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return nil, err
	}
	exec.popEnv()

	return callEnv, nil
}
