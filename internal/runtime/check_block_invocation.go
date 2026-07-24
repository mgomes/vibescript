package runtime

import "strconv"

// blockRestElementsMarker tags the exact positional array synthesized for a
// destructuring rest target. TypeArgs retains the ordinary element union for
// array compatibility, while Shape records each position (including an exact
// empty array) for recursive destructuring and indexing.
const blockRestElementsMarker = "\x00block-rest-elements"

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
	if value == nil {
		return nil
	}
	arms, exact := typeExprArms(value, 0)
	if !exact || len(arms) == 0 {
		return nil
	}
	values := []*TypeExpr{value}
	if elements, exact := exactBlockRestElementTypes(value); exact {
		values = elements
	} else {
		for _, arm := range arms {
			if arm.Kind == TypeArray {
				return nil
			}
		}
	}
	// A known scalar destructures as a one-element sequence; an exact rest
	// array retains every generated position. Mirror
	// assignDestructureWithNormalizer's rest window so leading, rest, and
	// trailing targets see the same value (or nil/empty-array padding) the
	// runtime binds.
	restIndex := -1
	for i, element := range target.Elements {
		if element.Rest {
			restIndex = i
			break
		}
	}
	if restIndex < 0 {
		if index < len(values) {
			return values[index]
		}
		return checkTypeNil
	}
	restStart := min(restIndex, len(values))
	restEnd := max(restStart, len(values)-(len(target.Elements)-restIndex-1))
	switch {
	case index < restIndex:
		if index < len(values) {
			return values[index]
		}
		return checkTypeNil
	case index == restIndex:
		return exactBlockRestType(values[restStart:restEnd])
	default:
		valueIndex := restEnd + (index - restIndex - 1)
		if valueIndex < len(values) {
			return values[valueIndex]
		}
		return checkTypeNil
	}
}

func exactBlockRestType(elements []*TypeExpr) *TypeExpr {
	shape := make(map[string]*TypeExpr, len(elements))
	for i, element := range elements {
		shape[strconv.Itoa(i)] = element
	}
	result := &TypeExpr{
		Kind:  TypeArray,
		Name:  blockRestElementsMarker,
		Shape: shape,
	}
	if len(elements) > 0 {
		result.TypeArgs = []*TypeExpr{unionTypeExprs(elements...)}
	}
	return result
}

func exactBlockRestElementTypes(value *TypeExpr) ([]*TypeExpr, bool) {
	if value == nil || value.Kind != TypeArray || value.Name != blockRestElementsMarker {
		return nil, false
	}
	elements := make([]*TypeExpr, len(value.Shape))
	for i := range elements {
		element, ok := value.Shape[strconv.Itoa(i)]
		if !ok {
			return nil, false
		}
		elements[i] = element
	}
	return elements, true
}
