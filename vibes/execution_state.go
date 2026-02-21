package vibes

func (exec *Execution) pushReceiver(v Value) {
	exec.receiverStack = append(exec.receiverStack, v)
}

func (exec *Execution) popReceiver() {
	if len(exec.receiverStack) == 0 {
		return
	}
	exec.receiverStack = exec.receiverStack[:len(exec.receiverStack)-1]
}

func (exec *Execution) currentReceiver() Value {
	if len(exec.receiverStack) == 0 {
		return NewNil()
	}
	return exec.receiverStack[len(exec.receiverStack)-1]
}

func (exec *Execution) isCurrentReceiver(v Value) bool {
	cur := exec.currentReceiver()
	switch {
	case v.Kind() == KindInstance && cur.Kind() == KindInstance:
		return v.Instance() == cur.Instance()
	case v.Kind() == KindClass && cur.Kind() == KindClass:
		return v.Class() == cur.Class()
	default:
		return false
	}
}

func (exec *Execution) pushFrame(function string, pos Position) error {
	if exec.recursionCap > 0 && len(exec.callStack) >= exec.recursionCap {
		return exec.errorAt(pos, "recursion depth exceeded (limit %d)", exec.recursionCap)
	}
	exec.callStack = append(exec.callStack, callFrame{Function: function, Pos: pos})
	return nil
}

func (exec *Execution) popFrame() {
	if len(exec.callStack) == 0 {
		return
	}
	exec.callStack = exec.callStack[:len(exec.callStack)-1]
}

func (exec *Execution) pushEnv(env *Env) {
	exec.envStack = append(exec.envStack, env)
}

func (exec *Execution) popEnv() {
	if len(exec.envStack) == 0 {
		return
	}
	exec.envStack = exec.envStack[:len(exec.envStack)-1]
}

func (exec *Execution) pushModuleContext(ctx moduleContext) {
	exec.moduleStack = append(exec.moduleStack, ctx)
}

func (exec *Execution) popModuleContext() {
	if len(exec.moduleStack) == 0 {
		return
	}
	exec.moduleStack = exec.moduleStack[:len(exec.moduleStack)-1]
}

func (exec *Execution) currentModuleContext() *moduleContext {
	if len(exec.moduleStack) == 0 {
		return nil
	}
	ctx := exec.moduleStack[len(exec.moduleStack)-1]
	return &ctx
}

func (exec *Execution) pushRescuedError(err error) {
	exec.rescuedErrors = append(exec.rescuedErrors, err)
}

func (exec *Execution) popRescuedError() {
	if len(exec.rescuedErrors) == 0 {
		return
	}
	exec.rescuedErrors = exec.rescuedErrors[:len(exec.rescuedErrors)-1]
}

func (exec *Execution) currentRescuedError() error {
	if len(exec.rescuedErrors) == 0 {
		return nil
	}
	return exec.rescuedErrors[len(exec.rescuedErrors)-1]
}
