package vibes

func bindGlobalsForCall(exec *Execution, root *Env, rebinder *callFunctionRebinder, globals map[string]Value) error {
	if exec.strictEffects {
		if err := validateStrictGlobals(globals); err != nil {
			return err
		}
	}

	for name, val := range globals {
		root.Define(name, rebinder.rebindValue(val))
	}

	return nil
}
