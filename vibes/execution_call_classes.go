package vibes

func initializeClassBodiesForCall(exec *Execution, root *Env, callClasses map[string]*ClassDef) error {
	for name, classDef := range callClasses {
		if len(classDef.Body) == 0 {
			continue
		}
		classVal, _ := root.Get(name)
		env := newEnv(root)
		env.Define("self", classVal)
		exec.pushReceiver(classVal)
		_, _, err := exec.evalStatements(classDef.Body, env)
		exec.popReceiver()
		if err != nil {
			return err
		}
	}

	return nil
}
