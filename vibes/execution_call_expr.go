package vibes

func (exec *Execution) evalCallTarget(call *CallExpr, env *Env) (Value, Value, error) {
	if member, ok := call.Callee.(*MemberExpr); ok {
		receiver, err := exec.evalExpression(member.Object, env)
		if err != nil {
			return NewNil(), NewNil(), err
		}
		if directCallee, handled, err := exec.evalDirectMemberMethodCall(receiver, member.Property, member.Pos()); handled || err != nil {
			if err != nil {
				return NewNil(), NewNil(), err
			}
			return directCallee, receiver, nil
		}
		callee, err := exec.getMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), NewNil(), err
		}
		return callee, receiver, nil
	}

	callee, err := exec.evalExpressionWithAuto(call.Callee, env, false)
	if err != nil {
		return NewNil(), NewNil(), err
	}
	return callee, NewNil(), nil
}

func (exec *Execution) evalDirectMemberMethodCall(receiver Value, property string, pos Position) (Value, bool, error) {
	switch receiver.Kind() {
	case KindClass:
		if property == "new" {
			return NewNil(), false, nil
		}
		classDef := receiver.Class()
		fn, ok := classDef.ClassMethods[property]
		if !ok {
			return NewNil(), false, nil
		}
		if fn.Private && !exec.isCurrentReceiver(receiver) {
			return NewNil(), true, exec.errorAt(pos, "private method %s", property)
		}
		return NewFunction(fn), true, nil
	case KindInstance:
		instance := receiver.Instance()
		fn, ok := instance.Class.Methods[property]
		if !ok {
			return NewNil(), false, nil
		}
		if fn.Private && !exec.isCurrentReceiver(receiver) {
			return NewNil(), true, exec.errorAt(pos, "private method %s", property)
		}
		return NewFunction(fn), true, nil
	default:
		return NewNil(), false, nil
	}
}

func (exec *Execution) evalCallArgs(call *CallExpr, env *Env) ([]Value, error) {
	args := make([]Value, len(call.Args))
	for i, arg := range call.Args {
		val, err := exec.evalExpressionWithAuto(arg, env, true)
		if err != nil {
			return nil, err
		}
		args[i] = val
	}
	return args, nil
}

func (exec *Execution) evalCallKwArgs(call *CallExpr, env *Env) (map[string]Value, error) {
	if len(call.KwArgs) == 0 {
		return nil, nil
	}
	kwargs := make(map[string]Value, len(call.KwArgs))
	for _, kw := range call.KwArgs {
		val, err := exec.evalExpressionWithAuto(kw.Value, env, true)
		if err != nil {
			return nil, err
		}
		kwargs[kw.Name] = val
	}
	return kwargs, nil
}

func (exec *Execution) evalCallBlock(call *CallExpr, env *Env) (Value, error) {
	if call.Block == nil {
		return NewNil(), nil
	}
	return exec.evalBlockLiteral(call.Block, env)
}

func (exec *Execution) checkCallMemoryRoots(receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if receiver.Kind() == KindNil && len(kwargs) == 0 && block.IsNil() {
		if len(args) == 0 {
			return nil
		}
		return exec.checkMemoryWith(args...)
	}
	combined := make([]Value, 0, len(args)+len(kwargs)+2)
	if receiver.Kind() != KindNil {
		combined = append(combined, receiver)
	}
	combined = append(combined, args...)
	for _, kwVal := range kwargs {
		combined = append(combined, kwVal)
	}
	if !block.IsNil() {
		combined = append(combined, block)
	}
	if len(combined) == 0 {
		return nil
	}
	return exec.checkMemoryWith(combined...)
}

func (exec *Execution) evalCallExpr(call *CallExpr, env *Env) (Value, error) {
	callee, receiver, err := exec.evalCallTarget(call, env)
	if err != nil {
		return NewNil(), err
	}
	args, err := exec.evalCallArgs(call, env)
	if err != nil {
		return NewNil(), err
	}
	kwargs, err := exec.evalCallKwArgs(call, env)
	if err != nil {
		return NewNil(), err
	}
	block, err := exec.evalCallBlock(call, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkCallMemoryRoots(receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}

	result, callErr := exec.invokeCallable(callee, receiver, args, kwargs, block, call.Pos())
	if callErr != nil {
		return NewNil(), callErr
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}
