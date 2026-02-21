package vibes

func (exec *Execution) classMember(obj Value, property string, pos Position) (Value, error) {
	cl := obj.Class()
	if property == "new" {
		return NewAutoBuiltin(cl.Name+".new", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			inst := &Instance{Class: cl, Ivars: make(map[string]Value)}
			instVal := NewInstance(inst)
			if initFn, ok := cl.Methods["initialize"]; ok {
				if _, err := exec.callFunction(initFn, instVal, args, kwargs, block, pos); err != nil {
					return NewNil(), err
				}
			}
			return instVal, nil
		}), nil
	}
	if fn, ok := cl.ClassMethods[property]; ok {
		if fn.Private && !exec.isCurrentReceiver(obj) {
			return NewNil(), exec.errorAt(pos, "private method %s", property)
		}
		return NewAutoBuiltin(cl.Name+"."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.callFunction(fn, obj, args, kwargs, block, pos)
		}), nil
	}
	if val, ok := cl.ClassVars[property]; ok {
		return val, nil
	}
	return NewNil(), exec.errorAt(pos, "unknown class member %s", property)
}

func (exec *Execution) instanceMember(obj Value, property string, pos Position) (Value, error) {
	inst := obj.Instance()
	if property == "class" {
		return NewClass(inst.Class), nil
	}
	if fn, ok := inst.Class.Methods[property]; ok {
		if fn.Private && !exec.isCurrentReceiver(obj) {
			return NewNil(), exec.errorAt(pos, "private method %s", property)
		}
		return NewAutoBuiltin(inst.Class.Name+"#"+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.callFunction(fn, obj, args, kwargs, block, pos)
		}), nil
	}
	if val, ok := inst.Ivars[property]; ok {
		return val, nil
	}
	return NewNil(), exec.errorAt(pos, "unknown member %s", property)
}
