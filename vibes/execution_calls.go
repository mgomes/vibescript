package vibes

import (
	"errors"
)

func (exec *Execution) autoInvokeIfNeeded(expr Expression, val Value, receiver Value) (Value, error) {
	switch val.Kind() {
	case KindFunction:
		fn := val.Function()
		if fn != nil && len(fn.Params) == 0 {
			return exec.invokeCallable(val, receiver, nil, nil, NewNil(), expr.Pos())
		}
	case KindBuiltin:
		builtin := val.Builtin()
		if builtin != nil && builtin.AutoInvoke {
			return exec.invokeCallable(val, receiver, nil, nil, NewNil(), expr.Pos())
		}
	}
	return val, nil
}

func (exec *Execution) invokeCallable(callee Value, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	switch callee.Kind() {
	case KindFunction:
		result, err := exec.callFunction(callee.Function(), receiver, args, kwargs, block, pos)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.errorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), err
		}
		return result, nil
	case KindBuiltin:
		builtin := callee.Builtin()
		scope := exec.capabilityContractScopes[builtin]
		var preCallKnownBuiltins map[*Builtin]struct{}
		if scope != nil && len(scope.contracts) > 0 {
			preCallKnownBuiltins = make(map[*Builtin]struct{})
			if receiver.Kind() != KindNil {
				collectCapabilityBuiltins(receiver, preCallKnownBuiltins)
			}
			for _, root := range scope.roots {
				collectCapabilityBuiltins(root, preCallKnownBuiltins)
			}
			for _, arg := range args {
				collectCapabilityBuiltins(arg, preCallKnownBuiltins)
			}
			for _, kwarg := range kwargs {
				collectCapabilityBuiltins(kwarg, preCallKnownBuiltins)
			}
		}
		contract, hasContract := exec.capabilityContracts[builtin]
		if hasContract && contract.ValidateArgs != nil {
			if err := contract.ValidateArgs(args, kwargs, block); err != nil {
				return NewNil(), exec.wrapError(err, pos)
			}
		}

		result, err := builtin.Fn(exec, receiver, args, kwargs, block)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return NewNil(), exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return NewNil(), exec.errorAt(pos, "next cannot cross call boundary")
			}
			return NewNil(), exec.wrapError(err, pos)
		}
		if hasContract && contract.ValidateReturn != nil {
			if err := contract.ValidateReturn(result); err != nil {
				return NewNil(), exec.wrapError(err, pos)
			}
		}
		if scope != nil && len(scope.contracts) > 0 {
			// Capability methods can lazily publish additional builtins at runtime
			// (e.g. through factory return values or receiver mutation). Re-scan
			// these values so future calls still enforce declared contracts.
			bindCapabilityContractsExcluding(result, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			if receiver.Kind() != KindNil {
				bindCapabilityContractsExcluding(receiver, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
			// Methods can mutate sibling scope roots via captured references; refresh
			// all adapter roots so newly exposed builtins also get bound.
			for _, root := range scope.roots {
				bindCapabilityContractsExcluding(root, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
			// Methods can also publish builtins by mutating positional or keyword
			// argument objects supplied by script code.
			for _, arg := range args {
				bindCapabilityContractsExcluding(arg, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
			for _, kwarg := range kwargs {
				bindCapabilityContractsExcluding(kwarg, scope, exec.capabilityContracts, exec.capabilityContractScopes, preCallKnownBuiltins)
			}
		}
		return result, nil
	default:
		return NewNil(), exec.errorAt(pos, "attempted to call non-callable value")
	}
}

func (exec *Execution) callFunction(fn *ScriptFunction, receiver Value, args []Value, kwargs map[string]Value, block Value, pos Position) (Value, error) {
	callEnv := newEnv(fn.Env)
	if receiver.Kind() != KindNil {
		callEnv.Define("self", receiver)
	}
	callEnv.Define("__block__", block)
	if err := exec.bindFunctionArgs(fn, callEnv, args, kwargs, pos); err != nil {
		return NewNil(), err
	}
	exec.pushEnv(callEnv)
	if err := exec.checkMemory(); err != nil {
		exec.popEnv()
		return NewNil(), err
	}
	exec.popEnv()
	if err := exec.pushFrame(fn.Name, pos); err != nil {
		return NewNil(), err
	}

	ctx := moduleContext{}
	if fn.owner != nil {
		ctx = moduleContext{
			key:  fn.owner.moduleKey,
			path: fn.owner.modulePath,
			root: fn.owner.moduleRoot,
		}
	}
	exec.pushModuleContext(ctx)
	exec.pushReceiver(receiver)
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	exec.popReceiver()
	exec.popModuleContext()
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if fn.ReturnTy != nil {
		if err := checkValueType(val, fn.ReturnTy); err != nil {
			return NewNil(), exec.errorAt(pos, "%s", formatReturnTypeMismatch(fn.Name, err))
		}
	}
	if returned {
		return val, nil
	}
	return val, nil
}
