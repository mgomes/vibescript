package runtime

import "strings"

// Implicit local type inference for the check path (ADR-004).
//
// The checker binds locals to the inferred *TypeExpr of the expressions
// assigned to them and reports an error wherever known types contradict a
// typed boundary. A nil *TypeExpr means "unknown": the checker proves nothing
// about the value and stays silent, deferring to the runtime contract. The
// governing rule is: error on known contradictions, permit unknowns.
//
// Two structures carry the facts:
//
//   - localTypes is a stack of frames parallel to scriptChecker.scopes. It is
//     snapshotted, restored, and merged together with the definedness scopes
//     at every branch point, so sibling branches join into unions and loop
//     bodies degrade to unknown.
//   - typePoison is a function-scoped, monotone set of locals whose values
//     escaped precise tracking (an in-place mutation through an index or
//     member write, a member call that may mutate a container, a container
//     passed by reference into a call). Poisoning survives branch and loop
//     state restores by design, so a fact killed inside a block body cannot
//     resurface after the walk backtracks.

var (
	checkTypeInt      = &TypeExpr{Kind: TypeInt}
	checkTypeFloat    = &TypeExpr{Kind: TypeFloat}
	checkTypeNumber   = &TypeExpr{Kind: TypeNumber}
	checkTypeString   = &TypeExpr{Kind: TypeString}
	checkTypeBool     = &TypeExpr{Kind: TypeBool}
	checkTypeNil      = &TypeExpr{Kind: TypeNil}
	checkTypeSymbol   = &TypeExpr{Kind: TypeSymbol}
	checkTypeArray    = &TypeExpr{Kind: TypeArray}
	checkTypeHash     = &TypeExpr{Kind: TypeHash}
	checkTypeRange    = &TypeExpr{Kind: TypeRange}
	checkTypeDuration = &TypeExpr{Kind: TypeDuration}
	checkTypeTime     = &TypeExpr{Kind: TypeTime}
	checkTypeMoney    = &TypeExpr{Kind: TypeMoney}
	checkTypeFunction = &TypeExpr{Kind: TypeFunction}
)

type checkTypeFrame map[string]*TypeExpr

func cloneCheckTypeFrame(frame checkTypeFrame) checkTypeFrame {
	if len(frame) == 0 {
		return nil
	}
	clone := make(checkTypeFrame, len(frame))
	for name, ty := range frame {
		clone[name] = ty
	}
	return clone
}

// localTypeFor returns the innermost inferred type fact for name, or nil when
// the checker knows nothing about it.
func (c *scriptChecker) localTypeFor(name string) *TypeExpr {
	if _, poisoned := c.typePoison[name]; poisoned {
		return nil
	}
	for i := len(c.localTypes) - 1; i >= 0; i-- {
		if ty, ok := c.localTypes[i][name]; ok {
			return ty
		}
	}
	return nil
}

// bindLocalType records an assignment fact in the innermost frame that
// already tracks the name, mirroring how runtime assignment writes through to
// a captured outer binding, or in the current frame for a fresh local.
func (c *scriptChecker) bindLocalType(name string, ty *TypeExpr) {
	if name == "" || len(c.localTypes) == 0 {
		return
	}
	for i := len(c.localTypes) - 1; i >= 0; i-- {
		if _, ok := c.localTypes[i][name]; ok {
			c.localTypes[i][name] = ty
			return
		}
	}
	c.bindLocalTypeInCurrentFrame(name, ty)
}

// bindLocalTypeInCurrentFrame records a fact in the innermost frame
// unconditionally. Parameter bindings use it so a block parameter that
// shadows an outer local does not overwrite the outer fact.
func (c *scriptChecker) bindLocalTypeInCurrentFrame(name string, ty *TypeExpr) {
	if name == "" || len(c.localTypes) == 0 {
		return
	}
	frame := c.localTypes[len(c.localTypes)-1]
	if frame == nil {
		frame = make(checkTypeFrame)
		c.localTypes[len(c.localTypes)-1] = frame
	}
	frame[name] = ty
}

// poisonLocalType marks a local as permanently unknown for the rest of the
// current function walk. The set is monotone: branch and loop restores do not
// clear it, so a fact invalidated inside a region the checker walks
// out-of-order can never resurface.
func (c *scriptChecker) poisonLocalType(name string) {
	if name == "" {
		return
	}
	if c.typePoison == nil {
		c.typePoison = make(map[string]struct{})
	}
	c.typePoison[name] = struct{}{}
}

func (c *scriptChecker) withFreshLocalInference(check func()) {
	defer c.withFreshLocalInferenceScope()()
	check()
}

func (c *scriptChecker) withFreshLocalInferenceScope() func() {
	previousPoison := c.typePoison
	c.typePoison = nil
	return func() { c.typePoison = previousPoison }
}

// forTargetElementType resolves a for-loop iterable to its element type when
// it is statically known (typed arrays and ranges). It runs before the loop
// degrades body-assigned locals, because the iterable evaluates once with
// pre-loop facts.
func (c *scriptChecker) forTargetElementType(stmt *ForStmt) *TypeExpr {
	iterable := c.inferExpressionType(stmt.Iterable)
	if iterable == nil || iterable.Nullable {
		return nil
	}
	switch iterable.Kind {
	case TypeArray:
		if len(iterable.TypeArgs) == 1 {
			return iterable.TypeArgs[0]
		}
	case TypeRange:
		return checkTypeInt
	}
	return nil
}

// bindForTargetType binds a for-loop target to the pre-computed element type.
func (c *scriptChecker) bindForTargetType(stmt *ForStmt, elemType *TypeExpr) {
	if elemType == nil {
		return
	}
	if target, ok := stmt.Target.(*Identifier); ok {
		c.bindLocalType(target.Name, elemType)
	}
}

// degradeLocalTypesForBindings resets to unknown every local the statements
// (plus any extra binding targets) may assign. It runs before regions whose
// execution count the checker cannot know — loop and block bodies — so a
// first-iteration fact cannot leak into the region or survive it.
func (c *scriptChecker) degradeLocalTypesForBindings(statements []Statement, extraTargets ...Expression) {
	names := make(map[string]struct{})
	collectLocalBindings(statements, names)
	for _, target := range extraTargets {
		if target != nil {
			collectBindingTarget(target, names)
		}
	}
	for name := range names {
		c.bindLocalType(name, nil)
	}
}

func (c *scriptChecker) snapshotLocalTypes() []checkTypeFrame {
	if len(c.localTypes) == 0 {
		return nil
	}
	state := make([]checkTypeFrame, len(c.localTypes))
	for i, frame := range c.localTypes {
		state[i] = cloneCheckTypeFrame(frame)
	}
	return state
}

func (c *scriptChecker) restoreLocalTypes(state []checkTypeFrame) {
	if len(state) == 0 {
		c.localTypes = nil
		return
	}
	c.localTypes = make([]checkTypeFrame, len(state))
	for i, frame := range state {
		c.localTypes[i] = cloneCheckTypeFrame(frame)
	}
}

// mergeLocalTypeStates joins the branch type states into the live frames:
// each local becomes the union of its type on every fall-through path. A path
// that never bound a name contributes nil (branch-assigned locals are
// predeclared as nil at runtime) when the name is new, or the base fact when
// the name predates the branch.
func (c *scriptChecker) mergeLocalTypeStates(base checkScopeState, states []checkScopeState) {
	if len(states) == 0 {
		return
	}
	for i := range c.localTypes {
		names := make(map[string]struct{})
		for _, state := range states {
			if i < len(state.types) {
				for name := range state.types[i] {
					names[name] = struct{}{}
				}
			}
		}
		for name := range names {
			var baseTy *TypeExpr
			inBase := false
			if i < len(base.types) {
				baseTy, inBase = base.types[i][name]
			}
			var merged *TypeExpr
			known := true
			for _, state := range states {
				arm := checkTypeNil
				if inBase {
					arm = baseTy
				}
				if i < len(state.types) {
					if ty, ok := state.types[i][name]; ok {
						arm = ty
					}
				}
				if arm == nil {
					known = false
					break
				}
				if merged == nil {
					merged = arm
					continue
				}
				merged = unionTypeExprs(merged, arm)
				if merged == nil {
					known = false
					break
				}
			}
			if !known {
				merged = nil
			}
			if c.localTypes[i] == nil {
				c.localTypes[i] = make(checkTypeFrame)
			}
			c.localTypes[i][name] = merged
		}
	}
}

// --- assignment inference ---

// inferAssignStatementTypes updates the local type environment for an
// assignment and reports a reassignment that contradicts the local's known
// type (ADR-004: sequential reassignment to a conflicting type is an error).
func (c *scriptChecker) inferAssignStatementTypes(function string, stmt *AssignStmt) {
	switch target := stmt.Target.(type) {
	case *Identifier:
		current := c.localTypeFor(target.Name)
		next := c.inferExpressionType(stmt.Value)
		if stmt.Operator != "" {
			outcome := c.binaryOperationOutcome(stmt.Operator, current, next)
			if outcome.invalid {
				c.add(function, stmt.Pos(), "unsupported %s operands %s and %s",
					binaryOperatorNoun(stmt.Operator), formatTypeExpr(current), formatTypeExpr(next))
			}
			c.bindLocalType(target.Name, outcome.result)
			return
		}
		if reassignmentConflicts(current, next) {
			c.add(function, stmt.Pos(), "reassignment of %s expected %s, got %s",
				target.Name, formatTypeExpr(current), formatTypeExpr(next))
		}
		c.bindLocalType(target.Name, next)
	case *DestructureTarget:
		for _, element := range target.Elements {
			c.bindDestructureElementType(element)
		}
	case *IndexExpr, *MemberExpr:
		// An index or member write mutates the container in place, so any
		// structural fact about the root local (shape exactness in
		// particular) no longer holds.
		if name, ok := rootIdentifierName(stmt.Target); ok {
			c.poisonLocalType(name)
		}
	}
}

func (c *scriptChecker) bindDestructureElementType(element DestructureElement) {
	switch target := element.Target.(type) {
	case *Identifier:
		c.bindLocalType(target.Name, element.Type)
	case *DestructureTarget:
		for _, nested := range target.Elements {
			c.bindDestructureElementType(nested)
		}
	}
}

// bindParamLocalType seeds a parameter's declared type into the current
// frame: annotated parameters enter the body with their declared type,
// unannotated ones with an explicit unknown fact.
func (c *scriptChecker) bindParamLocalType(param Param) {
	if param.Name != "" {
		c.bindLocalTypeInCurrentFrame(param.Name, param.Type)
	}
	if target, ok := param.Target.(*DestructureTarget); ok {
		for _, element := range target.Elements {
			c.bindDestructureParamElementType(element)
		}
	}
}

func (c *scriptChecker) bindDestructureParamElementType(element DestructureElement) {
	switch target := element.Target.(type) {
	case *Identifier:
		c.bindLocalTypeInCurrentFrame(target.Name, element.Type)
	case *DestructureTarget:
		for _, nested := range target.Elements {
			c.bindDestructureParamElementType(nested)
		}
	}
}

// reassignmentConflicts reports whether rebinding a local of type current to
// a value of type next is a known contradiction. Unknowns never conflict, nil
// acts as the neutral initializer in both directions, numeric retyping widens
// instead of erroring (arithmetic freely mixes int and float), and container
// re-initialization (hash/shape literals of different shapes) stays legal.
func reassignmentConflicts(current, next *TypeExpr) bool {
	if current == nil || next == nil {
		return false
	}
	if typeExprIsNilOnly(current) || typeExprIsNilOnly(next) {
		return false
	}
	if typeExprNumericOnly(current) && typeExprNumericOnly(next) {
		return false
	}
	if typeExprHashLikeOnly(current) && typeExprHashLikeOnly(next) {
		return false
	}
	if typeExprArrayOnly(current) && typeExprArrayOnly(next) {
		return false
	}
	return typeExprsDisjoint(current, next)
}

func typeExprIsNilOnly(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool { return arm.Kind == TypeNil })
}

func typeExprNumericOnly(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		return arm.Kind == TypeInt || arm.Kind == TypeFloat || arm.Kind == TypeNumber
	})
}

func typeExprArrayOnly(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool { return arm.Kind == TypeArray })
}

func typeExprHashLikeOnly(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		return arm.Kind == TypeHash || arm.Kind == TypeShape
	})
}

func typeExprArmsAll(ty *TypeExpr, pred func(*TypeExpr) bool) bool {
	arms, ok := typeExprArms(ty, 0)
	if !ok || len(arms) == 0 {
		return false
	}
	for _, arm := range arms {
		if !pred(arm) {
			return false
		}
	}
	return true
}

// --- expression type inference ---

// inferExpressionType computes the static type of an expression, or nil when
// it is not statically known. It is pure: it never emits warnings and never
// mutates checker state.
func (c *scriptChecker) inferExpressionType(expr Expression) *TypeExpr {
	switch typed := expr.(type) {
	case *IntegerLiteral:
		return checkTypeInt
	case *FloatLiteral:
		return checkTypeFloat
	case *StringLiteral, *InterpolatedString:
		return checkTypeString
	case *BoolLiteral:
		return checkTypeBool
	case *NilLiteral:
		return checkTypeNil
	case *SymbolLiteral, *InterpolatedSymbol:
		return checkTypeSymbol
	case *RangeExpr:
		return checkTypeRange
	case *ArrayLiteral:
		return c.inferArrayLiteralType(typed)
	case *HashLiteral:
		return c.inferHashLiteralType(typed)
	case *BlockLiteral:
		return checkTypeFunction
	case *Identifier:
		return c.localTypeFor(typed.Name)
	case *UnaryExpr:
		return c.inferUnaryExprType(typed)
	case *BinaryExpr:
		left := c.inferExpressionType(typed.Left)
		right := c.inferExpressionType(typed.Right)
		return c.binaryOperationOutcome(typed.Operator, left, right).result
	case *ConditionalExpr:
		if branch, ok := staticConditionalExpressionBranch(typed); ok {
			return c.inferExpressionType(branch)
		}
		return c.inferBranchUnionType(typed.Consequent, typed.Alternate)
	case *IfExpr:
		branches := make([]Expression, 0, len(typed.ElseIf)+2)
		branches = append(branches, typed.Consequent)
		for _, branch := range typed.ElseIf {
			branches = append(branches, branch.Result)
		}
		branches = append(branches, typed.Alternate)
		return c.inferBranchUnionType(branches...)
	case *RescueExpr:
		return c.inferBranchUnionType(typed.Body, typed.Fallback)
	case *CallExpr:
		return c.inferCallExprType(typed)
	case *IndexExpr:
		return c.inferIndexExprType(typed)
	}
	return nil
}

// inferBranchUnionType joins branch result types; a missing branch (an if
// expression without an else) contributes nil.
func (c *scriptChecker) inferBranchUnionType(branches ...Expression) *TypeExpr {
	var merged *TypeExpr
	for i, branch := range branches {
		arm := checkTypeNil
		if branch != nil {
			arm = c.inferExpressionType(branch)
		}
		if arm == nil {
			return nil
		}
		if i == 0 {
			merged = arm
			continue
		}
		merged = unionTypeExprs(merged, arm)
		if merged == nil {
			return nil
		}
	}
	return merged
}

// inferHashLiteralType infers an exact shape for a hash literal whose keys
// and field types are all statically known, giving downstream indexing
// field-level facts. Anything less certain degrades to a plain hash.
func (c *scriptChecker) inferHashLiteralType(lit *HashLiteral) *TypeExpr {
	if lit.ShapeType != nil {
		if c.hashShapeStaticallyShadowed(lit) {
			// The group may take either reading at runtime.
			return nil
		}
		return shapeValueType(lit.ShapeType)
	}
	shape := make(map[string]*TypeExpr, len(lit.Pairs))
	allSymbolKeys, allStringKeys := true, true
	for _, pair := range lit.Pairs {
		switch pair.Key.(type) {
		case *SymbolLiteral:
			allStringKeys = false
		case *StringLiteral:
			allSymbolKeys = false
		default:
			allSymbolKeys, allStringKeys = false, false
		}
		key, ok := staticLiteralHashKey(pair.Key)
		if !ok {
			return checkTypeHash
		}
		fieldType := c.inferExpressionType(pair.Value)
		if fieldType == nil {
			return checkTypeHash
		}
		if _, duplicate := shape[key]; duplicate {
			return checkTypeHash
		}
		shape[key] = fieldType
	}
	fact := &TypeExpr{Kind: TypeShape, Shape: shape}
	// Runtime hashes distinguish symbol keys from string keys, so an exact
	// index fact needs the store's key representation.
	switch {
	case allSymbolKeys:
		fact.Name = shapeKeysSymbolMarker
	case allStringKeys:
		fact.Name = shapeKeysStringMarker
	}
	return fact
}

func (c *scriptChecker) inferUnaryExprType(expr *UnaryExpr) *TypeExpr {
	switch expr.Operator {
	case tokenBang, tokenNot:
		return checkTypeBool
	case tokenMinus:
		operand := c.inferExpressionType(expr.Right)
		if kind, ok := staticOperandKind(operand); ok {
			switch kind {
			case TypeInt, TypeFloat, TypeNumber:
				return operand
			}
		}
		return nil
	case tokenPlus:
		operand := c.inferExpressionType(expr.Right)
		if kind, ok := staticOperandKind(operand); ok {
			switch kind {
			case TypeInt, TypeFloat, TypeNumber, TypeString:
				return operand
			}
		}
		return nil
	}
	return nil
}

// inferCallExprType exposes a known callee's annotated return type to the
// caller. Safe navigation, splats, and constructors stay unknown. Among
// builtins the checker models only JSON.parse_as, whose result is the
// validated shape (ADR-004).
func (c *scriptChecker) inferCallExprType(call *CallExpr) *TypeExpr {
	if member, ok := call.Callee.(*MemberExpr); ok && member.Safe {
		return nil
	}
	target, ok := c.resolveCallable(call)
	if !ok || callExpandsArguments(call) {
		return nil
	}
	if target.fn != nil {
		if target.constructor {
			return nil
		}
		return target.fn.ReturnTy
	}
	if target.name == "JSON.parse_as" && len(call.Args) == 2 {
		if shape, ok := shapeValuePayload(c.inferExpressionType(call.Args[1])); ok {
			// JSON object keys are strings, so the validated result and its
			// nested shapes are string-keyed stores.
			return stringKeyedShapeFact(shape)
		}
	}
	return nil
}

// shapeValueMarkerName tags the synthetic type that carries a first-class
// shape value through the local type environment.
const shapeValueMarkerName = "shape"

// shapeValueType wraps a shape used as a first-class value so the fact can
// flow through locals into JSON.parse_as (schema = { ... } then
// JSON.parse_as(raw, schema)). Kind TypeUnknown keeps the marker inert
// everywhere else: unknowns never participate in contradictions or operand
// checks, so only the parse_as resolution above looks inside. The parser
// never produces this spelling — an annotation naming an unknown type
// resolves to TypeEnum, not TypeUnknown.
func shapeValueType(shape *TypeExpr) *TypeExpr {
	return &TypeExpr{Kind: TypeUnknown, Name: shapeValueMarkerName, TypeArgs: []*TypeExpr{shape}}
}

func shapeValuePayload(ty *TypeExpr) (*TypeExpr, bool) {
	if ty == nil || ty.Kind != TypeUnknown || ty.Name != shapeValueMarkerName || len(ty.TypeArgs) != 1 {
		return nil, false
	}
	return ty.TypeArgs[0], true
}

// Shape key-representation markers, stored in the Name field of inferred
// shape facts (unused by TypeShape formatting and comparison). The leading
// NUL keeps them disjoint from any parser-produced spelling.
const (
	shapeKeysStringMarker = "\x00string-keyed"
	shapeKeysSymbolMarker = "\x00symbol-keyed"
)

// literalElementsMarker tags an inferred array type whose element union is
// existential: every arm is witnessed by an actual element of a literal, so
// an arm that contradicts a declared element type proves the whole array
// does. It lives in the Name field, which TypeArray formatting ignores.
const literalElementsMarker = "\x00literal-elements"

// inferArrayLiteralType infers a witnessed element union for an array
// literal. Empty literals and literals with unknown elements stay a bare
// array.
func (c *scriptChecker) inferArrayLiteralType(lit *ArrayLiteral) *TypeExpr {
	if len(lit.Elements) == 0 {
		return checkTypeArray
	}
	elements := make([]*TypeExpr, 0, len(lit.Elements))
	for _, element := range lit.Elements {
		if _, splat := element.(*SplatArg); splat {
			return checkTypeArray
		}
		elementType := c.inferExpressionType(element)
		if elementType == nil {
			return checkTypeArray
		}
		elements = append(elements, elementType)
	}
	union := unionTypeExprs(elements...)
	if union == nil {
		return checkTypeArray
	}
	return &TypeExpr{Kind: TypeArray, Name: literalElementsMarker, TypeArgs: []*TypeExpr{union}}
}

// literalArrayDisjoint reports whether a witnessed-element array can never
// satisfy another array type: some witnessed element arm is disjoint from
// the other side's declared element type.
func literalArrayDisjoint(lit, other *TypeExpr) bool {
	if lit.Name != literalElementsMarker || len(lit.TypeArgs) != 1 || len(other.TypeArgs) != 1 {
		return false
	}
	arms, ok := typeExprArms(lit.TypeArgs[0], 0)
	if !ok {
		return false
	}
	for _, arm := range arms {
		if typeExprsDisjoint(arm, other.TypeArgs[0]) {
			return true
		}
	}
	return false
}

// stringKeyedShapeFact clones a shape and marks it (and every nested shape)
// as a string-keyed store, so literal string indexing yields exact field
// facts and symbol indexing is known to miss.
func stringKeyedShapeFact(shape *TypeExpr) *TypeExpr {
	clone := cloneTypeExpr(shape)
	markShapeNodesStringKeyed(clone)
	return clone
}

func markShapeNodesStringKeyed(ty *TypeExpr) {
	if ty == nil {
		return
	}
	if ty.Kind == TypeShape {
		ty.Name = shapeKeysStringMarker
		for _, field := range ty.Shape {
			markShapeNodesStringKeyed(field)
		}
	}
	for _, option := range ty.Union {
		markShapeNodesStringKeyed(option)
	}
	for _, arg := range ty.TypeArgs {
		markShapeNodesStringKeyed(arg)
	}
}

// walkShapeTypeNames visits the identifier spelling of every named leaf in a
// shape type: built-in atoms (string, array<...>), including nested shape
// fields, union arms, and generic arguments. nil atoms and structural nodes
// carry no identifier.
func walkShapeTypeNames(ty *TypeExpr, visit func(string)) {
	if ty == nil {
		return
	}
	switch ty.Kind {
	case TypeShape:
		for _, field := range ty.Shape {
			walkShapeTypeNames(field, visit)
		}
		return
	case TypeUnion:
		for _, option := range ty.Union {
			walkShapeTypeNames(option, visit)
		}
		return
	case TypeNil:
		return
	}
	if name := strings.TrimSuffix(ty.Name, "?"); name != "" {
		visit(name)
	}
	for _, arg := range ty.TypeArgs {
		walkShapeTypeNames(arg, visit)
	}
}

// hashShapeStaticallyShadowed mirrors the runtime's shape-versus-hash choice
// for a dual-reading braced group: when any of its type names resolves to a
// known binding (a local, host global, script definition, or host builtin),
// the group keeps hash semantics. A group without a hash reading is always a
// shape.
func (c *scriptChecker) hashShapeStaticallyShadowed(lit *HashLiteral) bool {
	if len(lit.Pairs) == 0 {
		return false
	}
	// Inside a method or class body a bare identifier may resolve through
	// implicit self, which the checker cannot rule out statically.
	if c.selfScope {
		return true
	}
	shadowed := false
	walkShapeTypeNames(lit.ShapeType, func(name string) {
		if shadowed {
			return
		}
		if c.identifierShadowed(name) || c.hostGlobalShadows(name) ||
			c.typeRootHasBinding(name) || c.hostBuiltinOverrides(name) {
			shadowed = true
		}
	})
	return shadowed
}

// inferIndexExprType propagates field-level facts out of shape-typed values:
// indexing with a known key yields the field's type, and a key outside an
// exact shape is known to read nil. Typed arrays and hashes yield their
// element type joined with nil (missing index).
func (c *scriptChecker) inferIndexExprType(expr *IndexExpr) *TypeExpr {
	if len(expr.Indices) != 1 {
		return nil
	}
	objectType := c.inferExpressionType(expr.Object)
	if objectType == nil || objectType.Nullable {
		return nil
	}
	index := expr.Indices[0]
	switch objectType.Kind {
	case TypeShape:
		key, ok := staticLiteralHashKey(index)
		if !ok {
			return nil
		}
		fieldType, present := objectType.Shape[key]
		indexKeyMarker := ""
		switch index.(type) {
		case *SymbolLiteral:
			indexKeyMarker = shapeKeysSymbolMarker
		case *StringLiteral:
			indexKeyMarker = shapeKeysStringMarker
		}
		switch objectType.Name {
		case shapeKeysStringMarker, shapeKeysSymbolMarker:
			// Known store representation: a lookup of the other key kind
			// (or a non-string, non-symbol key) always misses.
			if indexKeyMarker != objectType.Name {
				return checkTypeNil
			}
			if present {
				return fieldType
			}
			return checkTypeNil
		}
		// Unknown store representation: a present display name reads as the
		// field type or nil depending on the store's key kind; an absent one
		// misses either store.
		if present {
			return unionTypeExprs(fieldType, checkTypeNil)
		}
		return checkTypeNil
	case TypeArray:
		if len(objectType.TypeArgs) != 1 {
			return nil
		}
		indexKind, ok := staticOperandKind(c.inferExpressionType(index))
		if !ok || indexKind != TypeInt {
			return nil
		}
		return unionTypeExprs(objectType.TypeArgs[0], checkTypeNil)
	case TypeHash:
		if len(objectType.TypeArgs) != 2 {
			return nil
		}
		return unionTypeExprs(objectType.TypeArgs[1], checkTypeNil)
	}
	return nil
}

// --- contradiction checks at typed boundaries ---

// checkInferredExpressionAgainstType reports a boundary contradiction when
// the inferred type of a non-literal expression is provably disjoint from the
// declared type. The declared annotation is validated silently here; the
// regular annotation checks report unresolved names.
func (c *scriptChecker) checkInferredExpressionAgainstType(function string, expr Expression, ty *TypeExpr, subject string) {
	if ty == nil {
		return
	}
	inferred := c.inferExpressionType(expr)
	if inferred == nil {
		return
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return
	}
	if typeExprsDisjoint(inferred, ty) {
		c.add(function, expr.Pos(), "%s expected %s, got %s", subject, formatTypeExpr(ty), formatTypeExpr(inferred))
	}
}

// checkInferredArgument is the call-argument variant of
// checkInferredExpressionAgainstType, matching the existing literal-argument
// diagnostic format.
func (c *scriptChecker) checkInferredArgument(function string, expr Expression, ty *TypeExpr, callName, paramName string) {
	if ty == nil {
		return
	}
	inferred := c.inferExpressionType(expr)
	if inferred == nil {
		return
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return
	}
	if typeExprsDisjoint(inferred, ty) {
		c.add(function, expr.Pos(), "call to %s argument %s expected %s, got %s",
			callName, paramName, formatTypeExpr(ty), formatTypeExpr(inferred))
	}
}

// checkBinaryOperandTypes rejects operator uses whose operand types are known
// to be invalid at runtime, mirroring evalBinaryOperator's kind matrix.
func (c *scriptChecker) checkBinaryOperandTypes(function string, expr *BinaryExpr) {
	left := c.inferExpressionType(expr.Left)
	right := c.inferExpressionType(expr.Right)
	outcome := c.binaryOperationOutcome(expr.Operator, left, right)
	if outcome.invalid {
		c.add(function, expr.Pos(), "unsupported %s operands %s and %s",
			binaryOperatorNoun(expr.Operator), formatTypeExpr(left), formatTypeExpr(right))
	}
}

// checkUnaryOperandTypes rejects unary operator uses on operand kinds the
// runtime provably refuses.
func (c *scriptChecker) checkUnaryOperandTypes(function string, expr *UnaryExpr) {
	kind, ok := staticOperandKind(c.inferExpressionType(expr.Right))
	if !ok {
		return
	}
	switch expr.Operator {
	case tokenMinus:
		if kind != TypeInt && kind != TypeFloat && kind != TypeNumber {
			c.add(function, expr.Pos(), "unsupported unary - operand %s", formatTypeExpr(c.inferExpressionType(expr.Right)))
		}
	case tokenPlus:
		if kind != TypeInt && kind != TypeFloat && kind != TypeNumber && kind != TypeString {
			c.add(function, expr.Pos(), "unsupported unary + operand %s", formatTypeExpr(c.inferExpressionType(expr.Right)))
		}
	}
}

// poisonEscapedIdentifier drops tracking for a mutable container local whose
// value escapes into code the checker cannot follow: a member call that may
// mutate it in place, or a by-reference argument position. An indexed or
// member projection (user["profile"]) escapes the root's interior, so the
// root local is poisoned too whenever the projected value may itself be a
// mutable container.
func (c *scriptChecker) poisonEscapedIdentifier(expr Expression) {
	name, ok := rootIdentifierName(expr)
	if !ok {
		return
	}
	rootType := c.localTypeFor(name)
	if rootType == nil || !typeExprHasContainerArm(rootType) {
		return
	}
	if _, isIdent := expr.(*Identifier); !isIdent {
		projected := c.inferExpressionType(expr)
		if projected != nil && !typeExprHasContainerArm(projected) {
			return
		}
	}
	c.poisonLocalType(name)
}

func typeExprHasContainerArm(ty *TypeExpr) bool {
	arms, ok := typeExprArms(ty, 0)
	if !ok {
		return false
	}
	for _, arm := range arms {
		switch arm.Kind {
		case TypeArray, TypeHash, TypeShape:
			return true
		}
	}
	return false
}

func rootIdentifierName(expr Expression) (string, bool) {
	for {
		switch typed := expr.(type) {
		case *Identifier:
			return typed.Name, true
		case *IndexExpr:
			expr = typed.Object
		case *MemberExpr:
			expr = typed.Object
		default:
			return "", false
		}
	}
}

// --- binary operator matrix ---

type binaryOutcome struct {
	result  *TypeExpr
	invalid bool
}

// binaryOperationOutcome mirrors evalBinaryOperator and the values.go kind
// matrices on inferred operand types. invalid is reported only when every
// combination of the operands' possible kinds is rejected at runtime; a
// number operand expands to both int and float before deciding.
func (c *scriptChecker) binaryOperationOutcome(op TokenType, left, right *TypeExpr) binaryOutcome {
	switch op {
	case tokenAnd, tokenOr, tokenWordAnd, tokenWordOr:
		if left == nil || right == nil {
			return binaryOutcome{}
		}
		return binaryOutcome{result: unionTypeExprs(left, right)}
	case tokenEQ, tokenNotEQ, tokenCaseEQ:
		return binaryOutcome{result: checkTypeBool}
	case tokenSpaceship:
		return binaryOutcome{result: unionTypeExprs(checkTypeInt, checkTypeNil)}
	case tokenPlus, tokenMinus, tokenAsterisk, tokenSlash, tokenPercent,
		tokenPower, tokenShovel, tokenAmpersand,
		tokenLT, tokenLTE, tokenGT, tokenGTE:
	default:
		return binaryOutcome{}
	}

	leftKind, leftOK := staticOperandKind(left)
	rightKind, rightOK := staticOperandKind(right)
	if !leftOK || !rightOK {
		// Partial knowledge decides a couple of left-driven results but never
		// an invalidity.
		if leftOK {
			switch {
			case op == tokenPercent && leftKind == TypeString:
				return binaryOutcome{result: checkTypeString}
			case op == tokenShovel && leftKind == TypeArray:
				return binaryOutcome{result: checkTypeArray}
			}
		}
		return binaryOutcome{}
	}

	var results []*TypeExpr
	anyValid := false
	for _, lk := range expandNumericKinds(leftKind) {
		for _, rk := range expandNumericKinds(rightKind) {
			result, valid := binaryScalarOutcome(op, lk, rk)
			if !valid {
				continue
			}
			anyValid = true
			if result != nil {
				results = append(results, result)
			}
		}
	}
	if !anyValid {
		return binaryOutcome{invalid: true}
	}
	if len(results) == 0 {
		return binaryOutcome{}
	}
	return binaryOutcome{result: unionTypeExprs(results...)}
}

func expandNumericKinds(kind TypeKind) []TypeKind {
	if kind == TypeNumber {
		return []TypeKind{TypeInt, TypeFloat}
	}
	return []TypeKind{kind}
}

// binaryScalarOutcome is the per-kind matrix for a single operand-kind pair
// (no TypeNumber inputs; callers expand it first). It mirrors addValues,
// subtractValues, multiplyValues, powerValues, divideValues, moduloValues,
// shovelValues, intersectValues, and compareValueOrder, including case order
// (string concatenation is the late fallback for +).
func binaryScalarOutcome(op TokenType, lk, rk TypeKind) (*TypeExpr, bool) {
	isNum := func(kind TypeKind) bool { return kind == TypeInt || kind == TypeFloat }
	numResult := func() *TypeExpr {
		if lk == TypeInt && rk == TypeInt {
			return checkTypeInt
		}
		return checkTypeFloat
	}
	switch op {
	case tokenPlus:
		switch {
		case isNum(lk) && isNum(rk):
			return numResult(), true
		case lk == TypeTime && (rk == TypeDuration || isNum(rk)):
			return checkTypeTime, true
		case rk == TypeTime && (lk == TypeDuration || isNum(lk)):
			return checkTypeTime, true
		case lk == TypeDuration && (rk == TypeDuration || isNum(rk)):
			return checkTypeDuration, true
		case rk == TypeDuration && isNum(lk):
			return checkTypeDuration, true
		case lk == TypeArray && rk == TypeArray:
			return checkTypeArray, true
		case lk == TypeString || rk == TypeString:
			return checkTypeString, true
		case lk == TypeMoney && rk == TypeMoney:
			return checkTypeMoney, true
		}
	case tokenMinus:
		switch {
		case isNum(lk) && isNum(rk):
			return numResult(), true
		case lk == TypeTime && (rk == TypeDuration || isNum(rk)):
			return checkTypeTime, true
		case lk == TypeTime && rk == TypeTime:
			return checkTypeFloat, true
		case lk == TypeDuration && (rk == TypeDuration || isNum(rk)):
			return checkTypeDuration, true
		case lk == TypeArray && rk == TypeArray:
			return checkTypeArray, true
		case lk == TypeMoney && rk == TypeMoney:
			return checkTypeMoney, true
		}
	case tokenAsterisk:
		switch {
		case isNum(lk) && isNum(rk):
			return numResult(), true
		case lk == TypeDuration && isNum(rk):
			return checkTypeDuration, true
		case rk == TypeDuration && isNum(lk):
			return checkTypeDuration, true
		case lk == TypeMoney && rk == TypeInt:
			return checkTypeMoney, true
		case lk == TypeInt && rk == TypeMoney:
			return checkTypeMoney, true
		}
	case tokenPower:
		switch {
		case lk == TypeInt && rk == TypeInt:
			// Negative exponents fall through to float exponentiation.
			return checkTypeNumber, true
		case isNum(lk) && isNum(rk):
			return checkTypeFloat, true
		}
	case tokenSlash:
		switch {
		case lk == TypeInt && rk == TypeInt:
			return checkTypeInt, true
		case isNum(lk) && isNum(rk):
			return checkTypeFloat, true
		case lk == TypeDuration && rk == TypeDuration:
			return checkTypeFloat, true
		case lk == TypeDuration && isNum(rk):
			return checkTypeDuration, true
		case lk == TypeMoney && rk == TypeInt:
			return checkTypeMoney, true
		}
	case tokenPercent:
		switch {
		case lk == TypeString:
			return checkTypeString, true
		case lk == TypeInt && rk == TypeInt:
			return checkTypeInt, true
		case lk == TypeDuration && rk == TypeDuration:
			return checkTypeDuration, true
		}
	case tokenShovel:
		if lk == TypeArray {
			return checkTypeArray, true
		}
	case tokenAmpersand:
		if lk == TypeArray && rk == TypeArray {
			return checkTypeArray, true
		}
	case tokenLT, tokenLTE, tokenGT, tokenGTE:
		switch {
		case isNum(lk) && isNum(rk),
			lk == TypeString && rk == TypeString,
			lk == TypeMoney && rk == TypeMoney,
			lk == TypeDuration && rk == TypeDuration,
			lk == TypeTime && rk == TypeTime:
			return checkTypeBool, true
		}
	}
	return nil, false
}

func binaryOperatorNoun(op TokenType) string {
	switch op {
	case tokenPlus:
		return "addition"
	case tokenMinus:
		return "subtraction"
	case tokenAsterisk:
		return "multiplication"
	case tokenSlash:
		return "division"
	case tokenPercent:
		return "modulo"
	case tokenPower:
		return "exponentiation"
	case tokenShovel:
		return "shovel"
	case tokenAmpersand:
		return "intersection"
	case tokenLT, tokenLTE, tokenGT, tokenGTE:
		return "comparison"
	}
	return "operator"
}

// staticOperandKind resolves an inferred type to a single operand kind for
// the operator matrix. Unions collapse only when purely numeric; nullable,
// enum, any, and unknown types stay undecided.
func staticOperandKind(ty *TypeExpr) (TypeKind, bool) {
	if ty == nil || ty.Nullable {
		return TypeUnknown, false
	}
	switch ty.Kind {
	case TypeAny, TypeUnknown, TypeEnum:
		return TypeUnknown, false
	case TypeUnion:
		for _, option := range ty.Union {
			kind, ok := staticOperandKind(option)
			if !ok {
				return TypeUnknown, false
			}
			switch kind {
			case TypeInt, TypeFloat, TypeNumber:
			default:
				return TypeUnknown, false
			}
		}
		return TypeNumber, true
	case TypeShape:
		return TypeHash, true
	}
	return ty.Kind, true
}

// --- type joins and disjointness ---

const maxInferredUnionArms = 6

// unionTypeExprs joins types into a deduplicated union; any unknown input or
// an oversized result collapses to unknown.
func unionTypeExprs(types ...*TypeExpr) *TypeExpr {
	arms := make([]*TypeExpr, 0, len(types))
	seen := make(map[string]struct{}, len(types))
	appendArm := func(arm *TypeExpr) {
		key := formatTypeExpr(arm)
		if _, duplicate := seen[key]; duplicate {
			return
		}
		seen[key] = struct{}{}
		arms = append(arms, arm)
	}
	for _, ty := range types {
		if ty == nil {
			return nil
		}
		if ty.Kind == TypeUnion && !ty.Nullable {
			for _, option := range ty.Union {
				if option == nil {
					return nil
				}
				appendArm(option)
			}
			continue
		}
		appendArm(ty)
	}
	if len(arms) == 0 || len(arms) > maxInferredUnionArms {
		return nil
	}
	if len(arms) == 1 {
		return arms[0]
	}
	return &TypeExpr{Kind: TypeUnion, Union: arms}
}

// typeExprsDisjoint reports whether no runtime value can satisfy both types.
// It is deliberately conservative: unknown, any, and named (enum/class) types
// never count as disjoint, empty containers make all array and hash types
// overlap, and only exact shapes compare structurally.
func typeExprsDisjoint(a, b *TypeExpr) bool {
	aArms, ok := typeExprArms(a, 0)
	if !ok || len(aArms) == 0 {
		return false
	}
	bArms, ok := typeExprArms(b, 0)
	if !ok || len(bArms) == 0 {
		return false
	}
	for _, x := range aArms {
		for _, y := range bArms {
			if !typeArmPairDisjoint(x, y) {
				return false
			}
		}
	}
	return true
}

const maxTypeArmDepth = 8

// typeExprArms flattens a type into its non-union arms; a nullable type
// contributes an extra nil arm. ok is false when the type contains an arm the
// checker cannot reason about (any or unknown).
func typeExprArms(ty *TypeExpr, depth int) ([]*TypeExpr, bool) {
	if ty == nil || depth > maxTypeArmDepth {
		return nil, false
	}
	var arms []*TypeExpr
	switch ty.Kind {
	case TypeAny, TypeUnknown:
		return nil, false
	case TypeUnion:
		for _, option := range ty.Union {
			sub, ok := typeExprArms(option, depth+1)
			if !ok {
				return nil, false
			}
			arms = append(arms, sub...)
		}
	default:
		if ty.Nullable {
			clone := *ty
			clone.Nullable = false
			arms = append(arms, &clone)
		} else {
			arms = append(arms, ty)
		}
	}
	if ty.Nullable {
		arms = append(arms, checkTypeNil)
	}
	return arms, true
}

func typeArmPairDisjoint(x, y *TypeExpr) bool {
	kx, ky := x.Kind, y.Kind
	if kx == TypeEnum || ky == TypeEnum || kx == TypeAny || ky == TypeAny ||
		kx == TypeUnknown || ky == TypeUnknown {
		return false
	}
	numeric := func(kind TypeKind) bool {
		return kind == TypeInt || kind == TypeFloat || kind == TypeNumber
	}
	if numeric(kx) && numeric(ky) {
		if kx == TypeNumber || ky == TypeNumber {
			return false
		}
		return kx != ky
	}
	hashLike := func(kind TypeKind) bool { return kind == TypeHash || kind == TypeShape }
	if hashLike(kx) && hashLike(ky) {
		if kx == TypeShape && ky == TypeShape {
			return shapeTypesDisjoint(x, y)
		}
		return false
	}
	if kx == TypeArray && ky == TypeArray {
		return literalArrayDisjoint(x, y) || literalArrayDisjoint(y, x)
	}
	return kx != ky
}

// shapeTypesDisjoint compares two exact shapes: differing key sets or any
// field pair with disjoint types means no value can satisfy both.
func shapeTypesDisjoint(x, y *TypeExpr) bool {
	if len(x.Shape) != len(y.Shape) {
		return true
	}
	for field, xField := range x.Shape {
		yField, ok := y.Shape[field]
		if !ok {
			return true
		}
		if typeExprsDisjoint(xField, yField) {
			return true
		}
	}
	return false
}
