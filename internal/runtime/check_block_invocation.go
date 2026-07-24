package runtime

type checkBlockInvocation struct {
	arguments   []*TypeExpr
	strictArity bool
}

// blockLiteralInvocationMayEnter reports whether exact yielded arguments can
// bind a block literal. Plain blocks and procs nil-fill missing parameters;
// lambdas opt into strict arity through the invocation.
func (c *scriptChecker) blockLiteralInvocationMayEnter(
	block *BlockLiteral,
	invocation *checkBlockInvocation,
) bool {
	if block == nil || invocation == nil {
		return block != nil
	}
	if invocation.strictArity && lambdaLiteralArity(block) != len(invocation.arguments) {
		return false
	}
	resolve := c.checkNamedTypeResolver()
	for i, param := range block.Params {
		argument := blockInvocationArgument(invocation, i)
		if param.Type != nil && typeExprsDisjoint(argument, param.Type, resolve) {
			return false
		}
		if !c.blockParamTargetMayBind(param.Target, argument, resolve) {
			return false
		}
	}
	return true
}

func blockInvocationArgument(invocation *checkBlockInvocation, index int) *TypeExpr {
	if invocation == nil || index < 0 {
		return nil
	}
	if index < len(invocation.arguments) {
		return invocation.arguments[index]
	}
	return checkTypeNil
}

func (c *scriptChecker) blockParamTargetMayBind(
	target Expression,
	value *TypeExpr,
	resolve namedTypeResolver,
) bool {
	destructure, ok := target.(*DestructureTarget)
	if !ok {
		return true
	}
	for i, element := range destructure.Elements {
		elementValue := blockDestructureElementType(value, destructure, i)
		if element.Type != nil && typeExprsDisjoint(elementValue, element.Type, resolve) {
			return false
		}
		if !c.blockParamTargetMayBind(element.Target, elementValue, resolve) {
			return false
		}
	}
	return true
}

func blockDestructureElementType(
	value *TypeExpr,
	target *DestructureTarget,
	index int,
) *TypeExpr {
	if target == nil || index < 0 || index >= len(target.Elements) {
		return nil
	}
	if target.Elements[index].Rest {
		return checkTypeArray
	}
	if value == nil {
		return nil
	}
	arms, exact := typeExprArms(value, 0)
	if !exact || len(arms) == 0 {
		return nil
	}
	for _, arm := range arms {
		if arm.Kind == TypeArray {
			return nil
		}
	}
	if index == 0 {
		return value
	}
	return checkTypeNil
}
