package runtime

// Static checking of direct writes to typed accessor-backed instance
// variables. The runtime normalizes and validates every direct ivar write
// against the declared property contract, so the checker mirrors that
// boundary: instance-method analysis seeds a fact for each typed
// accessor-backed ivar, a write whose known value is provably incompatible
// with the contract warns, and unknown values pass to the runtime guard.

import "errors"

// ivarFactKey names the local-inference fact slot for an instance variable.
// The @ prefix keeps ivar facts from colliding with same-named locals.
func ivarFactKey(name string) string { return "@" + name }

// instanceIvarContract returns the declared contract for an instance
// variable of the class under check. It is nil outside an instance-method
// scope and for instance variables without a typed generated accessor:
// undeclared and untyped ivars never gain an inferred contract.
func (c *scriptChecker) instanceIvarContract(name string) *TypeExpr {
	if c.selfClass == nil || c.selfClassContext {
		return nil
	}
	_, ty := propertyContract(c.selfClass, name)
	return ty
}

// ivarContractFact converts a declared contract into a read fact the
// analysis can hold across the whole method. Container contracts stay
// unknown: interior mutations of an ivar-rooted container are invisible to
// the local poisoning machinery, so only the write check uses them.
func (c *scriptChecker) ivarContractFact(ty *TypeExpr) *TypeExpr {
	if ty == nil || typeExprHasContainerArm(ty) {
		return nil
	}
	return ty
}

// seedInstanceIvarFacts binds entry facts for every typed accessor-backed
// instance variable of the class under check. The seed widens the declared
// contract with nil: an instance variable that was never written reads as
// nil regardless of its declared type, while every executed write satisfies
// the contract — statically or through the runtime guard. Ivar parameters
// are direct writes that bind before the body runs, so they refine their
// ivar's fact to the bare contract, and an annotation or default value that
// provably contradicts the contract warns at the definition: every call
// would fail during parameter binding.
func (c *scriptChecker) seedInstanceIvarFacts(function string, fn *ScriptFunction) {
	if c.selfClass == nil || c.selfClassContext {
		return
	}
	for _, method := range c.selfClass.Methods {
		if method.Accessor == functionAccessorNone || method.AccessorName == "" {
			continue
		}
		fact := c.ivarContractFact(c.instanceIvarContract(method.AccessorName))
		if fact == nil {
			continue
		}
		c.bindLocalTypeInCurrentFrame(ivarFactKey(method.AccessorName), unionTypeExprs(fact, checkTypeNil))
	}
	if fn == nil {
		return
	}
	for _, param := range fn.Params {
		ty := c.ivarParamContract(fn, param)
		if ty == nil || validateTypeExprResolved(ty, c.runtimeTypeContext()) != nil {
			continue
		}
		if param.Type != nil &&
			validateTypeExprResolved(param.Type, c.runtimeTypeContext()) == nil &&
			typeExprsDisjoint(param.Type, ty, c.checkNamedTypeResolver()) {
			c.add(function, param.Type.Position, "write to @%s expected %s, got %s",
				param.Name, formatTypeExpr(ty), formatTypeExpr(param.Type))
		}
		if fact := c.ivarContractFact(ty); fact != nil {
			c.bindLocalTypeInCurrentFrame(ivarFactKey(param.Name), fact)
		}
	}
}

// ivarParamContract returns the property contract backing an ivar parameter
// of fn, resolved through the class that owns the method. Parameters of
// plain functions and ivars without a typed generated accessor have none.
func (c *scriptChecker) ivarParamContract(fn *ScriptFunction, param Param) *TypeExpr {
	if !param.IsIvar {
		return nil
	}
	classDef := c.selfScopeFnClasses[fn]
	if classDef == nil {
		return nil
	}
	_, ty := propertyContract(classDef, param.Name)
	return ty
}

// checkIvarParamArgument checks a known call argument against both boundary
// contracts a parameter carries: its own annotation, and the property
// contract an ivar parameter stores into. A value can satisfy the annotation
// and still provably fail the ivar store, so both apply; the store check is
// skipped when the annotation already rejected the argument.
func (c *scriptChecker) checkIvarParamArgument(function string, arg Expression, fn *ScriptFunction, param Param, callName string) {
	before := len(c.warnings)
	c.checkArgumentExpression(function, arg, param.Type, callName, param.Name)
	if len(c.warnings) > before {
		return
	}
	ty := c.ivarParamContract(fn, param)
	if ty == nil {
		return
	}
	c.checkArgumentExpression(function, arg, ty, callName, param.Name)
}

// checkIvarParamDefault checks an ivar parameter's default value against the
// property contract it stores into. It runs from the parameter walk, after
// earlier parameters have bound their facts, matching the runtime's binding
// order for defaults that reference prior parameters. The annotation check
// for annotated parameters runs separately; this covers the store contract.
func (c *scriptChecker) checkIvarParamDefault(function string, fn *ScriptFunction, param Param) {
	if param.DefaultVal == nil {
		return
	}
	ty := c.ivarParamContract(fn, param)
	if ty == nil {
		return
	}
	c.checkRuntimeExpressionAgainstType(function, param.DefaultVal, ty,
		"default value for @"+param.Name)
}

// addIvarWriteWarning reports a direct-write contradiction in the standard
// write-to shape, unwrapping the normalization mismatch when present.
func (c *scriptChecker) addIvarWriteWarning(function string, pos Position, name string, err error) {
	var mismatch *typeMismatchError
	if errors.As(err, &mismatch) {
		c.add(function, pos, "write to @%s expected %s, got %s", name, mismatch.Expected, mismatch.Actual)
		return
	}
	c.add(function, pos, "write to @%s type check failed: %s", name, err)
}

// inferIvarAssignStatementTypes checks a direct write to an instance
// variable against its declared property contract and updates the ivar's
// fact. Plain writes warn when the value's known type is provably disjoint
// from the contract; compound and logical writes stay quiet and rely on the
// runtime guard. Any executed write leaves the ivar satisfying its contract
// and a typed ivar can never return to the unset state, so the post-write
// fact is the contract itself, without the entry nil arm.
func (c *scriptChecker) inferIvarAssignStatementTypes(function string, stmt *AssignStmt, target *IvarExpr) {
	ty := c.instanceIvarContract(target.Name)
	if ty == nil {
		return
	}
	if stmt.Operator == "" && validateTypeExprResolved(ty, c.runtimeTypeContext()) == nil {
		if val, ok := staticLiteralValue(stmt.Value); ok {
			// An exact literal validates through normalization, which keeps
			// value-level checks kind disjointness would miss: a symbol that
			// names no member of the declared enum, for example.
			if err := c.checkRuntimeStaticValueType(val, ty); err != nil {
				c.addIvarWriteWarning(function, stmt.Pos(), target.Name, err)
			}
		} else if next := c.inferExpressionType(stmt.Value); next != nil &&
			typeExprsDisjoint(next, ty, c.checkNamedTypeResolver()) {
			c.add(function, stmt.Pos(), "write to @%s expected %s, got %s",
				target.Name, formatTypeExpr(ty), formatTypeExpr(next))
		}
	}
	if fact := c.ivarContractFact(ty); fact != nil {
		c.bindLocalType(ivarFactKey(target.Name), fact)
	}
}
