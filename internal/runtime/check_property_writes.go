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
// the contract — statically or through the runtime guard.
func (c *scriptChecker) seedInstanceIvarFacts() {
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
}

// checkIvarParamBinding checks one ivar parameter as the direct write it
// performs when it binds, at its own position in the parameter walk so
// earlier defaults still read later ivars as unset. An annotation that
// provably contradicts the property contract warns at the definition (every
// call would fail during binding), the default value checks under the same
// expectation the runtime evaluates it with (a callable-typed contract
// keeps a bare callable un-invoked), and the ivar's fact refines to the
// bare contract once the parameter has bound.
func (c *scriptChecker) checkIvarParamBinding(function string, fn *ScriptFunction, param Param) {
	ty := c.ivarParamContract(fn, param)
	if ty == nil || validateTypeExprResolved(ty, c.runtimeTypeContext()) != nil {
		return
	}
	if param.Type != nil &&
		validateTypeExprResolved(param.Type, c.runtimeTypeContext()) == nil &&
		typeExprsDisjoint(param.Type, ty, c.checkNamedTypeResolver()) {
		c.add(function, param.Type.Position, "write to @%s expected %s, got %s",
			param.Name, formatTypeExpr(ty), formatTypeExpr(param.Type))
	}
	if param.DefaultVal != nil {
		subject := "default value for @" + param.Name
		if val, ok := staticLiteralValue(param.DefaultVal); ok {
			if err := c.checkRuntimeStaticValueType(val, ty); err != nil {
				c.addValueTypeWarning(function, param.DefaultVal.Pos(), subject, err)
			}
		} else if inferred := c.inferExpressionTypeWithExpectation(param.DefaultVal, positionalArgumentExpectation(param)); inferred != nil &&
			typeExprsDisjoint(inferred, ty, c.checkNamedTypeResolver()) {
			c.add(function, param.DefaultVal.Pos(), "%s expected %s, got %s",
				subject, formatTypeExpr(ty), formatTypeExpr(inferred))
		}
	}
	if fact := c.ivarContractFact(ty); fact != nil {
		c.bindLocalTypeInCurrentFrame(ivarFactKey(param.Name), fact)
	}
}

// ivarParamContract returns the property contract backing an ivar parameter
// of fn, resolved through the class that owns the method. Parameters of
// plain functions, class methods (whose ivar parameters only bind a local —
// the runtime writes the ivar only when self is an instance), and ivars
// without a typed generated accessor have none.
func (c *scriptChecker) ivarParamContract(fn *ScriptFunction, param Param) *TypeExpr {
	if !param.IsIvar {
		return nil
	}
	c.prepareSelfScopeFunctions()
	if _, classMethod := c.selfScopeClassFns[fn]; classMethod {
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
// fact. Plain writes warn when the value's known type is provably
// incompatible with the contract; compound and logical writes stay quiet
// and rely on the runtime guard.
func (c *scriptChecker) inferIvarAssignStatementTypes(function string, stmt *AssignStmt, target *IvarExpr) {
	if stmt.Operator == tokenAndAssign {
		// A falsey current value short-circuits &&= without assigning, so an
		// unset property keeps its nil arm and the fact must not refine to
		// the bare contract.
		return
	}
	var value Expression
	if stmt.Operator == "" {
		value = stmt.Value
	}
	c.checkIvarWrite(function, stmt.Pos(), target.Name, value)
}

// checkIvarWrite applies the property-contract check for one direct write
// to the named instance variable and refines its fact. value is nil when
// the written value is not statically known (compound operators,
// destructured elements without a literal source); unknown writes stay
// quiet and rely on the runtime guard. Any executed write leaves the ivar
// satisfying its contract and a typed ivar can never return to the unset
// state, so the post-write fact is the contract itself, without the entry
// nil arm.
func (c *scriptChecker) checkIvarWrite(function string, pos Position, name string, value Expression) {
	ty := c.instanceIvarContract(name)
	if ty == nil {
		return
	}
	if value != nil && validateTypeExprResolved(ty, c.runtimeTypeContext()) == nil {
		if val, ok := staticLiteralValue(value); ok {
			// An exact literal validates through normalization, which keeps
			// value-level checks kind disjointness would miss: a symbol that
			// names no member of the declared enum, for example.
			if err := c.checkRuntimeStaticValueType(val, ty); err != nil {
				c.addIvarWriteWarning(function, pos, name, err)
			}
		} else {
			// The runtime evaluates the right-hand side under the property
			// expectation for the same value shapes as a member setter, so a
			// bare callable assigned to a function-typed property is stored
			// un-invoked; the inference mirrors that before comparing.
			expectation := expressionExpectation{}
			if memberAssignmentValueCanUseExpectation(value) {
				expectation = typeExpressionExpectation(ty)
			}
			if next := c.inferExpressionTypeWithExpectation(value, expectation); next != nil &&
				typeExprsDisjoint(next, ty, c.checkNamedTypeResolver()) {
				c.add(function, pos, "write to @%s expected %s, got %s",
					name, formatTypeExpr(ty), formatTypeExpr(next))
			}
		}
	}
	if fact := c.ivarContractFact(ty); fact != nil {
		c.bindLocalType(ivarFactKey(name), fact)
	}
}

// inferDestructureIvarWrites routes instance-variable targets inside a
// destructuring assignment through the property-contract check. Element
// values are known only when the right-hand side is a literal element list
// that maps index for index with no rest element on the target side and no
// splat on the value side; every other spelling checks as an unknown write
// and still refines the ivar's fact.
func (c *scriptChecker) inferDestructureIvarWrites(function string, value Expression, target *DestructureTarget) {
	values := destructureElementValueExprs(value, target)
	for i, element := range target.Elements {
		var elementValue Expression
		if values != nil {
			elementValue = values[i]
		}
		switch elementTarget := element.Target.(type) {
		case *IvarExpr:
			c.checkIvarWrite(function, elementTarget.Pos(), elementTarget.Name, elementValue)
		case *DestructureTarget:
			c.inferDestructureIvarWrites(function, elementValue, elementTarget)
		}
	}
}

// destructureElementValueExprs returns the per-element value expressions of
// a destructuring assignment when they map index for index, or nil when the
// element values are not statically known.
func destructureElementValueExprs(value Expression, target *DestructureTarget) []Expression {
	arr, ok := value.(*ArrayLiteral)
	if !ok || len(arr.Elements) != len(target.Elements) {
		return nil
	}
	for _, element := range target.Elements {
		if element.Rest {
			return nil
		}
	}
	for _, expr := range arr.Elements {
		if _, ok := expr.(*SplatArg); ok {
			return nil
		}
	}
	return arr.Elements
}
