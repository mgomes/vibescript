package runtime

import "strconv"

// blockRestElementsMarker tags the exact positional array synthesized for a
// destructuring rest target. TypeArgs retains the ordinary element union for
// array compatibility, while Shape records each position (including an exact
// empty array) for recursive destructuring and indexing.
const blockRestElementsMarker = "\x00block-rest-elements"

type checkBlockInvocation struct {
	arguments []*TypeExpr
}

// blockLiteralInvocationMayEnter reports whether exact yielded arguments can
// bind a block literal. A block nil-fills missing parameters, so only a
// declared parameter type or destructuring shape can reject the invocation.
func (c *scriptChecker) blockLiteralInvocationMayEnter(
	block *BlockLiteral,
	invocation *checkBlockInvocation,
) bool {
	if block == nil || invocation == nil {
		return block != nil
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
	layout, decomposed := blockDestructureLayoutFor(value, destructure)
	for i, element := range destructure.Elements {
		var elementValue *TypeExpr
		if decomposed {
			elementValue = layout.elementType(i)
		}
		if element.Type != nil && typeExprsDisjoint(elementValue, element.Type, resolve) {
			return false
		}
		if !c.blockParamTargetMayBind(element.Target, elementValue, resolve) {
			return false
		}
	}
	return true
}

// blockDestructureLayout is the part of a destructuring bind that every element
// of one target shares: the positional values the destructured value supplies,
// and the rest element that decides which window each position reads from.
type blockDestructureLayout struct {
	values    []*TypeExpr
	elements  int
	restIndex int
}

// blockDestructureLayoutFor decomposes value against target, reporting false
// when the value carries no exact positional facts for the target to read.
//
// Deriving this once per bind is what keeps a wide destructured block parameter
// affordable. Resolving each element on its own re-decomposed the value and
// rescanned the whole target for its rest element, so a parameter like
// `(x1, ..., xN, *rest)` on an array.fill block cost N*N element inspections
// during CheckWarnings, before any runtime step or memory quota applies. A
// 2,000-target parameter inspected 4.0M elements and a 4,000-target one 16.0M,
// quadrupling per doubling; they inspect 2,002 and 4,002 now, and checking the
// 8,000-target case fell from 47ms to 5ms (#6).
func blockDestructureLayoutFor(
	value *TypeExpr,
	target *DestructureTarget,
) (blockDestructureLayout, bool) {
	if target == nil || value == nil {
		return blockDestructureLayout{}, false
	}
	arms, exact := typeExprArms(value, 0)
	if !exact || len(arms) == 0 {
		return blockDestructureLayout{}, false
	}
	values := []*TypeExpr{value}
	if elements, exact := exactBlockRestElementTypes(value); exact {
		values = elements
	} else {
		for _, arm := range arms {
			if arm.Kind == TypeArray {
				return blockDestructureLayout{}, false
			}
		}
	}
	layout := blockDestructureLayout{
		values:    values,
		elements:  len(target.Elements),
		restIndex: -1,
	}
	for i, element := range target.Elements {
		if element.Rest {
			layout.restIndex = i
			break
		}
	}
	noteCheckWork(len(target.Elements) + len(values))
	return layout, true
}

// elementType returns the value bound at one target position. A known scalar
// destructures as a one-element sequence; an exact rest array retains every
// generated position. The rest window mirrors assignDestructureWithNormalizer
// so leading, rest, and trailing targets see the same value (or nil/empty-array
// padding) the runtime binds.
func (l blockDestructureLayout) elementType(index int) *TypeExpr {
	if index < 0 || index >= l.elements {
		return nil
	}
	if l.restIndex < 0 {
		if index < len(l.values) {
			return l.values[index]
		}
		return checkTypeNil
	}
	restStart := min(l.restIndex, len(l.values))
	restEnd := max(restStart, len(l.values)-(l.elements-l.restIndex-1))
	switch {
	case index < l.restIndex:
		if index < len(l.values) {
			return l.values[index]
		}
		return checkTypeNil
	case index == l.restIndex:
		return exactBlockRestType(l.values[restStart:restEnd])
	default:
		valueIndex := restEnd + (index - l.restIndex - 1)
		if valueIndex < len(l.values) {
			return l.values[valueIndex]
		}
		return checkTypeNil
	}
}

func blockDestructureElementType(
	value *TypeExpr,
	target *DestructureTarget,
	index int,
) *TypeExpr {
	layout, decomposed := blockDestructureLayoutFor(value, target)
	if !decomposed {
		return nil
	}
	return layout.elementType(index)
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
