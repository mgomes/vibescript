package vibes

func (exec *Execution) bindFunctionArgs(fn *ScriptFunction, env *Env, args []Value, kwargs map[string]Value, pos Position) error {
	usedKw := make(map[string]bool, len(kwargs))
	argIdx := 0

	for _, param := range fn.Params {
		var val Value
		if argIdx < len(args) {
			val = args[argIdx]
			argIdx++
		} else if kw, ok := kwargs[param.Name]; ok {
			val = kw
			usedKw[param.Name] = true
		} else if param.DefaultVal != nil {
			defaultVal, err := exec.evalExpressionWithAuto(param.DefaultVal, env, true)
			if err != nil {
				return err
			}
			val = defaultVal
		} else {
			return exec.errorAt(pos, "missing argument %s", param.Name)
		}

		if param.Type != nil {
			if err := checkValueType(val, param.Type); err != nil {
				return exec.errorAt(pos, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
		}
		env.Define(param.Name, val)
		if param.IsIvar {
			if selfVal, ok := env.Get("self"); ok && selfVal.Kind() == KindInstance {
				inst := selfVal.Instance()
				if inst != nil {
					inst.Ivars[param.Name] = val
				}
			}
		}
	}

	if argIdx < len(args) {
		return exec.errorAt(pos, "unexpected positional arguments")
	}
	for name := range kwargs {
		if !usedKw[name] {
			return exec.errorAt(pos, "unexpected keyword argument %s", name)
		}
	}
	return nil
}
