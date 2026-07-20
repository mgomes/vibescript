package runtime

// propertyContract returns the generated accessor whose annotation declares
// the contract for the named instance variable, along with the declared
// type. A generated setter's parameter annotation wins when one exists (an
// untyped generated setter keeps the ivar dynamic even beside a typed
// getter, matching the unchecked public write path); otherwise a generated
// getter's return annotation declares the contract. Handwritten methods
// never declare an ivar contract: a handwritten setter takes over the write
// path entirely, so direct writes stay dynamic even when a generated typed
// getter remains, and a handwritten getter leaves a generated typed setter's
// contract in force.
func propertyContract(classDef *ClassDef, name string) (*ScriptFunction, *TypeExpr) {
	if classDef == nil {
		return nil, nil
	}
	if setter, ok := classDef.Methods[name+"="]; ok {
		if setter.Accessor != functionAccessorSetter || setter.AccessorName != name {
			return nil, nil
		}
		if len(setter.Params) == 1 {
			return setter, setter.Params[0].Type
		}
		return nil, nil
	}
	if getter, ok := classDef.Methods[name]; ok && getter.Accessor == functionAccessorGetter && getter.AccessorName == name {
		return getter, getter.ReturnTy
	}
	return nil, nil
}

// normalizeIvarWrite applies the declared property contract, if any, to a
// direct instance-variable write. Values normalize exactly like the
// generated setter's argument boundary (a matching symbol coerces into a
// declared enum, for example) and incompatible values raise when the write
// executes. Instance variables without a typed generated accessor pass
// through untouched.
func (exec *Execution) normalizeIvarWrite(inst *Instance, name string, value Value, pos Position) (Value, error) {
	accessor, ty := propertyContract(inst.Class, name)
	if ty == nil {
		return value, nil
	}
	owner := accessor.owner
	if owner == nil {
		owner = inst.Class.owner
	}
	normalized, err := normalizeValueForType(value, ty, typeContext{
		owner:    owner,
		env:      accessor.Env,
		fallback: exec.root,
		exec:     exec,
	})
	if err != nil {
		if isHostControlSignal(err) {
			return NewNil(), err
		}
		if isNormalizationLimitError(err) {
			return NewNil(), exec.wrapError(err, pos)
		}
		return NewNil(), exec.errorAt(pos, "%s", formatIvarTypeMismatch(name, err))
	}
	return normalized, nil
}
