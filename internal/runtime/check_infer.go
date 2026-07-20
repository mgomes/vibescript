package runtime

import (
	"sort"
	"strconv"
	"strings"
)

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
	checkTypeIntArray = &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{checkTypeInt}}
	checkTypeHash     = &TypeExpr{Kind: TypeHash}
	checkTypeRange    = &TypeExpr{Kind: TypeRange}
	checkTypeDuration = &TypeExpr{Kind: TypeDuration}
	checkTypeTime     = &TypeExpr{Kind: TypeTime}
	checkTypeMoney    = &TypeExpr{Kind: TypeMoney}
	checkTypeFunction = &TypeExpr{Kind: TypeFunction}

	// checkTypeMethodName matches respond_to?'s method-name argument, which
	// the runtime accepts as a symbol or string.
	checkTypeMethodName = &TypeExpr{Kind: TypeUnion, Union: []*TypeExpr{checkTypeSymbol, checkTypeString}}
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
	if _, done := c.typePoison[name]; done {
		return
	}
	c.typePoison[name] = struct{}{}
	// Containers assign by reference, so a mutation through any alias
	// invalidates every name sharing the value.
	for alias := range c.typeAliases[name] {
		c.poisonLocalType(alias)
	}
}

// linkContainerAlias records that two locals may share one mutable
// container, so poisoning either cascades to the other. Links are
// function-scoped and monotone like the poison set: branch restores never
// unlink, which can only over-poison.
func (c *scriptChecker) linkContainerAlias(a, b string) {
	if a == "" || b == "" || a == b {
		return
	}
	if c.typeAliases == nil {
		c.typeAliases = make(map[string]map[string]struct{})
	}
	if c.typeAliases[a] == nil {
		c.typeAliases[a] = make(map[string]struct{})
	}
	if c.typeAliases[b] == nil {
		c.typeAliases[b] = make(map[string]struct{})
	}
	c.typeAliases[a][b] = struct{}{}
	c.typeAliases[b][a] = struct{}{}
}

func (c *scriptChecker) withFreshLocalInference(check func()) {
	defer c.withFreshLocalInferenceScope()()
	check()
}

func (c *scriptChecker) withFreshLocalInferenceScope() func() {
	previousPoison := c.typePoison
	previousAliases := c.typeAliases
	c.typePoison = nil
	c.typeAliases = nil
	return func() {
		c.typePoison = previousPoison
		c.typeAliases = previousAliases
	}
}

// withIsolatedLocalInference walls off the local type-fact environment so a
// module collection pass can run assignment inference without reading or
// corrupting the facts of whatever function walk is in flight.
func (c *scriptChecker) withIsolatedLocalInference() func() {
	previousTypes := c.localTypes
	previousLive := c.liveLocalNames
	previousDepth := c.mutationRegionDepth
	previousIsolated := c.isolatedCollectInference
	c.localTypes = nil
	c.liveLocalNames = nil
	c.mutationRegionDepth = 0
	c.isolatedCollectInference = true
	restoreScope := c.withFreshLocalInferenceScope()
	return func() {
		restoreScope()
		c.localTypes = previousTypes
		c.liveLocalNames = previousLive
		c.mutationRegionDepth = previousDepth
		c.isolatedCollectInference = previousIsolated
	}
}

// poisonSkippedMutationFacts drops the container facts a subexpression the
// collection pass does not walk may invalidate: a maybe-evaluated operand is
// skipped entirely, but any mutation inside it must still degrade facts.
func (c *scriptChecker) poisonSkippedMutationFacts(expr Expression) {
	var sites []Expression
	collectMutationCandidateRootsFromExpression(expr, &sites)
	for _, site := range sites {
		if name, ok := c.escapePoisonTarget(site); ok {
			c.poisonLocalType(name)
		}
	}
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
		if len(iterable.TypeArgs) == 1 && iterable.Name != literalPartialElementsMarker {
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

// degradeBlockBodyBindings resets to unknown the outer locals a block body
// may assign, excluding the names the block binds itself: a body assignment
// to a shadowing block parameter writes the block-local, so the outer fact
// still holds.
func (c *scriptChecker) degradeBlockBodyBindings(block *BlockLiteral) {
	names := make(map[string]struct{})
	collectLocalBindings(block.Body, names)
	collectMutatedContainerRoots(block.Body, names)
	c.degradeMutationCandidates(block.Body, names)
	blockBound := make(map[string]struct{})
	for _, param := range block.Params {
		if param.Name != "" {
			blockBound[param.Name] = struct{}{}
		}
		collectBindingTarget(param.Target, blockBound)
	}
	for _, name := range block.ImplicitParams {
		if name != "" {
			blockBound[name] = struct{}{}
		}
	}
	for name := range names {
		if _, bound := blockBound[name]; bound {
			continue
		}
		c.bindLocalType(name, nil)
	}
}

// binaryRightUnreachable reports whether inferred facts prove a short-circuit
// right operand never evaluates. Condition-outcome reachability covers both
// direct value facts and predicates whose requested outcome contradicts an
// already narrowed receiver.
func (c *scriptChecker) binaryRightUnreachable(expr *BinaryExpr) bool {
	var evaluatingOutcome bool
	switch expr.Operator {
	case tokenOr:
		evaluatingOutcome = false
	case tokenAnd:
		evaluatingOutcome = true
	default:
		return false
	}
	_, reachable := c.probeConditionOutcome(expr.Left, evaluatingOutcome)
	return !reachable
}

// binaryRightAlwaysEvaluatesInferred reports whether inferred facts prove the
// skipped left outcome unreachable, so the right operand's effects must not be
// rolled back into a branch join.
func (c *scriptChecker) binaryRightAlwaysEvaluatesInferred(expr *BinaryExpr) bool {
	var skippedOutcome bool
	switch expr.Operator {
	case tokenAnd:
		skippedOutcome = false
	case tokenOr:
		skippedOutcome = true
	default:
		return false
	}
	_, reachable := c.probeConditionOutcome(expr.Left, skippedOutcome)
	return !reachable
}

// collectMutatedContainerRoots gathers the root identifiers of index and
// member writes so loop and block degradation clears facts about containers
// the region mutates in place, not only locals it rebinds.
func collectMutatedContainerRoots(statements []Statement, out map[string]struct{}) {
	for _, stmt := range statements {
		switch typed := stmt.(type) {
		case *AssignStmt:
			switch typed.Target.(type) {
			case *IndexExpr, *MemberExpr:
				if name, ok := rootIdentifierName(typed.Target); ok {
					out[name] = struct{}{}
				}
			}
		case *IfStmt:
			collectMutatedContainerRoots(typed.Consequent, out)
			for _, elseIf := range typed.ElseIf {
				collectMutatedContainerRoots(elseIf.Consequent, out)
			}
			collectMutatedContainerRoots(typed.Alternate, out)
		case *ForStmt:
			collectMutatedContainerRoots(typed.Body, out)
		case *WhileStmt:
			collectMutatedContainerRoots(typed.Body, out)
		case *UntilStmt:
			collectMutatedContainerRoots(typed.Body, out)
		case *TryStmt:
			collectMutatedContainerRoots(typed.Body, out)
			for i := range typed.Rescues {
				collectMutatedContainerRoots(typed.Rescues[i].Body, out)
			}
			collectMutatedContainerRoots(typed.Else, out)
			collectMutatedContainerRoots(typed.Ensure, out)
		}
	}
}

// collectMutationCandidateRoots gathers the escape-site expressions a
// region contains (member-call receivers, call and yield arguments — the
// walk-time poison sources), so pre-region degradation can clear the
// affected container facts before reads earlier in the region are checked.
// The caller applies the same gate the walk-time poison uses.
func collectMutationCandidateRoots(statements []Statement, out *[]Expression) {
	for _, stmt := range statements {
		switch typed := stmt.(type) {
		case nil:
		case *ReturnStmt:
			collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *RaiseStmt:
			collectMutationCandidateRootsFromExpression(typed.Value, out)
			collectMutationCandidateRootsFromExpression(typed.Message, out)
		case *BreakStmt:
			collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *NextStmt:
			collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *AssignStmt:
			collectMutationCandidateRootsFromExpression(typed.Target, out)
			collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *ExprStmt:
			collectMutationCandidateRootsFromExpression(typed.Expr, out)
		case *IfStmt:
			collectMutationCandidateRootsFromExpression(typed.Condition, out)
			collectMutationCandidateRoots(typed.Consequent, out)
			for _, elseIf := range typed.ElseIf {
				collectMutationCandidateRootsFromExpression(elseIf.Condition, out)
				collectMutationCandidateRoots(elseIf.Consequent, out)
			}
			collectMutationCandidateRoots(typed.Alternate, out)
		case *ForStmt:
			collectMutationCandidateRootsFromExpression(typed.Iterable, out)
			collectMutationCandidateRoots(typed.Body, out)
		case *WhileStmt:
			collectMutationCandidateRootsFromExpression(typed.Condition, out)
			collectMutationCandidateRoots(typed.Body, out)
		case *UntilStmt:
			collectMutationCandidateRootsFromExpression(typed.Condition, out)
			collectMutationCandidateRoots(typed.Body, out)
		case *TryStmt:
			collectMutationCandidateRoots(typed.Body, out)
			for i := range typed.Rescues {
				collectMutationCandidateRoots(typed.Rescues[i].Body, out)
			}
			collectMutationCandidateRoots(typed.Else, out)
			collectMutationCandidateRoots(typed.Ensure, out)
		}
	}
}

func collectMutationCandidateRootsFromExpression(expr Expression, out *[]Expression) {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *RegexLiteral,
		*BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
	case *ArrayLiteral:
		for _, element := range typed.Elements {
			collectMutationCandidateRootsFromExpression(element, out)
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			collectMutationCandidateRootsFromExpression(pair.Key, out)
			collectMutationCandidateRootsFromExpression(pair.Value, out)
		}
	case *CallExpr:
		collectMutationCandidateRootsFromExpression(typed.Callee, out)
		for _, arg := range typed.Args {
			*out = append(*out, arg)
			collectMutationCandidateRootsFromExpression(arg, out)
		}
		for _, kwarg := range typed.KwArgs {
			*out = append(*out, kwarg.Value)
			collectMutationCandidateRootsFromExpression(kwarg.Value, out)
		}
		collectMutationCandidateRootsFromExpression(typed.BlockArg, out)
		if typed.Block != nil {
			for _, param := range typed.Block.Params {
				collectMutationCandidateRootsFromExpression(param.DefaultVal, out)
			}
			collectMutationCandidateRoots(typed.Block.Body, out)
		}
	case *MemberExpr:
		*out = append(*out, typed.Object)
		collectMutationCandidateRootsFromExpression(typed.Object, out)
	case *ScopeExpr:
		collectMutationCandidateRootsFromExpression(typed.Object, out)
	case *IndexExpr:
		collectMutationCandidateRootsFromExpression(typed.Object, out)
		for _, index := range typed.Indices {
			collectMutationCandidateRootsFromExpression(index, out)
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			collectMutationCandidateRootsFromExpression(element.Target, out)
		}
	case *SplatArg:
		collectMutationCandidateRootsFromExpression(typed.Value, out)
	case *UnaryExpr:
		collectMutationCandidateRootsFromExpression(typed.Right, out)
	case *BinaryExpr:
		collectMutationCandidateRootsFromExpression(typed.Left, out)
		collectMutationCandidateRootsFromExpression(typed.Right, out)
	case *ConditionalExpr:
		collectMutationCandidateRootsFromExpression(typed.Condition, out)
		collectMutationCandidateRootsFromExpression(typed.Consequent, out)
		collectMutationCandidateRootsFromExpression(typed.Alternate, out)
	case *RescueExpr:
		collectMutationCandidateRootsFromExpression(typed.Body, out)
		collectMutationCandidateRootsFromExpression(typed.Fallback, out)
	case *IfExpr:
		collectMutationCandidateRootsFromExpression(typed.Condition, out)
		collectMutationCandidateRootsFromExpression(typed.Consequent, out)
		for _, branch := range typed.ElseIf {
			collectMutationCandidateRootsFromExpression(branch.Condition, out)
			collectMutationCandidateRootsFromExpression(branch.Result, out)
		}
		collectMutationCandidateRootsFromExpression(typed.Alternate, out)
	case *RangeExpr:
		collectMutationCandidateRootsFromExpression(typed.Start, out)
		collectMutationCandidateRootsFromExpression(typed.End, out)
	case *CaseExpr:
		collectMutationCandidateRootsFromExpression(typed.Target, out)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				collectMutationCandidateRootsFromExpression(value.Expr, out)
			}
			collectMutationCandidateRootsFromExpression(clause.Result, out)
		}
		collectMutationCandidateRootsFromExpression(typed.ElseExpr, out)
	case *BlockLiteral:
		for _, param := range typed.Params {
			collectMutationCandidateRootsFromExpression(param.DefaultVal, out)
		}
		collectMutationCandidateRoots(typed.Body, out)
	case *YieldExpr:
		for _, arg := range typed.Args {
			*out = append(*out, arg)
			collectMutationCandidateRootsFromExpression(arg, out)
		}
	case *InterpolatedString:
		for _, part := range typed.Parts {
			if exprPart, ok := part.(StringExpr); ok {
				collectMutationCandidateRootsFromExpression(exprPart.Expr, out)
			}
		}
	case *InterpolatedSymbol:
		for _, part := range typed.Parts {
			if exprPart, ok := part.(StringExpr); ok {
				collectMutationCandidateRootsFromExpression(exprPart.Expr, out)
			}
		}
	case *IfStmt, *ForStmt, *WhileStmt, *UntilStmt, *TryStmt:
		collectMutationCandidateRoots([]Statement{typed.(Statement)}, out)
	}
}

// degradeMutationCandidates clears container-typed locals a region may
// mutate through member dispatch or call arguments; scalar receivers keep
// their facts (immutable kinds cannot be mutated in place).
func (c *scriptChecker) degradeMutationCandidates(statements []Statement, names map[string]struct{}) {
	var sites []Expression
	collectMutationCandidateRoots(statements, &sites)
	for _, site := range sites {
		if name, ok := c.escapePoisonTarget(site); ok {
			names[name] = struct{}{}
		}
	}
}

// degradeLocalTypesForBindings resets to unknown every local the statements
// (plus any extra binding targets) may assign. It runs before regions whose
// execution count the checker cannot know — loop and block bodies — so a
// first-iteration fact cannot leak into the region or survive it.
func (c *scriptChecker) degradeLocalTypesForBindings(statements []Statement, extraTargets ...Expression) {
	names := make(map[string]struct{})
	collectLocalBindings(statements, names)
	collectMutatedContainerRoots(statements, names)
	c.degradeMutationCandidates(statements, names)
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

// applyLoopEntryTypeRefinements overlays only facts changed by a condition
// outcome. Loop bodies use it after degrading their assignments so the first
// entry's proven scalar refinement remains visible without restoring
// unrelated first-iteration facts or container interiors that later
// iterations may mutate. The degraded state still survives the loop.
func (c *scriptChecker) applyLoopEntryTypeRefinements(base, refined []checkTypeFrame) {
	for i, refinedFrame := range refined {
		if i >= len(base) || i >= len(c.localTypes) {
			continue
		}
		for name, refinedType := range refinedFrame {
			baseType, ok := base[i][name]
			if !ok || refinedType == nil || refinedType == baseType ||
				typeExprHasContainerArm(refinedType) {
				continue
			}
			if c.localTypes[i] == nil {
				c.localTypes[i] = make(checkTypeFrame)
			}
			c.localTypes[i][name] = refinedType
		}
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
// indexedReceiverFact carries an indexed-write target's receiver fact as
// captured before the value expression walked (the runtime evaluates the
// receiver first); nil defers to the current state.
func (c *scriptChecker) inferAssignStatementTypes(function string, stmt *AssignStmt, indexedReceiverFact *TypeExpr) {
	switch target := stmt.Target.(type) {
	case *Identifier:
		current := c.localTypeFor(target.Name)
		next := c.inferExpressionType(stmt.Value)
		if stmt.Operator == tokenOrAssign || stmt.Operator == tokenAndAssign {
			c.bindLocalType(target.Name, logicalAssignmentFact(stmt.Operator, current, next))
			return
		}
		if stmt.Operator != "" {
			outcome := c.binaryOperationOutcome(stmt.Operator, current, next)
			if outcome.invalid {
				c.add(function, stmt.Pos(), "unsupported %s operands %s and %s",
					binaryOperatorNoun(stmt.Operator), formatTypeExpr(current), formatTypeExpr(next))
			}
			c.bindLocalType(target.Name, outcome.result)
			return
		}
		if reassignmentConflicts(current, next, c.checkNamedTypeResolver()) {
			c.add(function, stmt.Pos(), "reassignment of %s expected %s, got %s",
				target.Name, formatTypeExpr(current), formatTypeExpr(next))
		}
		c.bindLocalType(target.Name, next)
		switch value := stmt.Value.(type) {
		case *Identifier:
			if next != nil && typeExprHasContainerArm(next) {
				if root, ok := rootIdentifierName(stmt.Value); ok {
					c.linkContainerAlias(target.Name, root)
				}
			}
		case *IndexExpr, *MemberExpr:
			// A projection may hand out a nested mutable container, so the
			// root's structural facts share its fate; unknown projections
			// link conservatively.
			if next == nil || typeExprHasContainerArm(next) {
				if root, ok := rootIdentifierName(stmt.Value); ok {
					c.linkContainerAlias(target.Name, root)
				}
			}
		case *BinaryExpr:
			// A shovel value evaluates to its mutated receiver, so the
			// target shares that container with the chain's root local.
			if value.Operator == tokenShovel {
				if root, ok := rootIdentifierName(unwrapShovelChain(value.Left)); ok {
					c.linkContainerAlias(target.Name, root)
				}
			}
		}
	case *DestructureTarget:
		for _, element := range target.Elements {
			c.bindDestructureElementType(element)
		}
	case *IndexExpr:
		// An index write mutates the container in place; a direct element
		// write against a declared array<T> is checked and may preserve the
		// fact, while every other write drops the root's structural facts.
		if c.applyIndexedElementWriteFacts(function, stmt, target, indexedReceiverFact) {
			return
		}
		if name, ok := rootIdentifierName(stmt.Target); ok {
			c.poisonLocalType(name)
		}
	case *MemberExpr:
		// A member write mutates the container in place, so any structural
		// fact about the root local (shape exactness in particular) no
		// longer holds.
		if name, ok := rootIdentifierName(stmt.Target); ok {
			c.poisonLocalType(name)
		}
	}
}

// logicalAssignmentFact models x ||= v and x &&= v: the runtime keeps the
// current value when the short circuit takes it and otherwise binds the
// right-hand side directly, so a decided current picks one side and an
// undecided one joins both.
func logicalAssignmentFact(operator TokenType, current, next *TypeExpr) *TypeExpr {
	switch operator {
	case tokenOrAssign:
		if typeExprDefinitelyTruthy(current) {
			return current
		}
		if typeExprIsNilOnly(current) {
			return next
		}
	case tokenAndAssign:
		if typeExprIsNilOnly(current) {
			return current
		}
		if typeExprDefinitelyTruthy(current) {
			return next
		}
	}
	if current == nil || next == nil {
		return nil
	}
	return unionTypeExprs(current, next)
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

// typeFactForValue derives a static fact from a concrete host-supplied
// argument value, so per-call checks treat CLI and host arguments like local
// literals. Kinds the inference does not model stay unknown.
func typeFactForValue(val Value) *TypeExpr {
	switch val.Kind() {
	case KindNil:
		return checkTypeNil
	case KindBool:
		return checkTypeBool
	case KindInt:
		return checkTypeInt
	case KindFloat:
		return checkTypeFloat
	case KindString:
		return checkTypeString
	case KindSymbol:
		return checkTypeSymbol
	case KindRange:
		return checkTypeRange
	case KindDuration:
		return checkTypeDuration
	case KindTime:
		return checkTypeTime
	case KindMoney:
		return checkTypeMoney
	case KindArray:
		elements := val.Array()
		if len(elements) == 0 {
			return checkTypeArray
		}
		arms := make([]*TypeExpr, 0, len(elements))
		for _, element := range elements {
			arm := typeFactForValue(element)
			if arm == nil {
				return checkTypeArray
			}
			arms = append(arms, arm)
		}
		union := unionTypeExprs(arms...)
		if union == nil {
			return checkTypeArray
		}
		return &TypeExpr{Kind: TypeArray, Name: literalElementsMarker, TypeArgs: []*TypeExpr{union}}
	case KindHash, KindObject:
		return checkTypeHash
	case KindShape:
		if shape := valueShape(val); shape != nil {
			return shapeValueType(shape)
		}
	}
	return nil
}

// bindParamValueFact refines an unannotated parameter with the concrete
// argument value a per-call check received; annotated parameters keep their
// declared seed (the value was validated against it already).
func (c *scriptChecker) bindParamValueFact(param Param, val Value, present bool) {
	if !present || param.Name == "" || param.Type != nil {
		return
	}
	fact := typeFactForValue(val)
	if param.Kind == ParamKeywordRest && val.Kind() == KindHash {
		fact = keywordRestFact(val.Hash())
	}
	if fact != nil {
		c.bindLocalTypeInCurrentFrame(param.Name, fact)
	}
}

// keywordRestFact models the hash the runtime binds for a keyword-rest
// parameter: raw keyword names make a string-keyed store, and the concrete
// argument facts fill an exact shape. Empty or unmodeled entries degrade to
// a plain hash fact.
func keywordRestFact(entries map[string]Value) *TypeExpr {
	if len(entries) == 0 {
		return checkTypeHash
	}
	shape := make(map[string]*TypeExpr, len(entries))
	for name, val := range entries {
		fact := typeFactForValue(val)
		if fact == nil {
			return checkTypeHash
		}
		shape[name] = fact
	}
	return &TypeExpr{Kind: TypeShape, Name: shapeKeysStringMarker, Shape: shape}
}

// bindParamDefaultFact refines an unannotated defaulted parameter with the
// default expression's inferred type when a per-call check omits the
// argument: the default is exactly the value the runtime binds.
func (c *scriptChecker) bindParamDefaultFact(param Param) {
	if param.Name == "" || param.Type != nil || param.DefaultVal == nil {
		return
	}
	if fact := c.inferExpressionType(param.DefaultVal); fact != nil {
		c.bindLocalTypeInCurrentFrame(param.Name, fact)
	}
}

// refineAnnotatedParamFact narrows an annotated parameter's declared seed
// with the concrete fact a per-call binding established: the runtime bound
// exactly one annotation arm, so arms the fact contradicts cannot be this
// call's value. Coercing arms survive (a named type is never disjoint from
// the fact that coerces into it), and a fact that eliminates nothing or
// everything keeps the declared seed.
func (c *scriptChecker) refineAnnotatedParamFact(param Param, fact *TypeExpr) {
	if param.Name == "" || param.Type == nil || fact == nil {
		return
	}
	arms, ok := typeExprArms(param.Type, 0)
	if !ok || len(arms) < 2 {
		return
	}
	resolve := c.checkNamedTypeResolver()
	kept := make([]*TypeExpr, 0, len(arms))
	for _, arm := range arms {
		if !typeExprsDisjoint(fact, arm, resolve) {
			kept = append(kept, arm)
		}
	}
	if len(kept) == 0 || len(kept) == len(arms) {
		return
	}
	if refined := unionTypeExprs(kept...); refined != nil {
		c.bindLocalTypeInCurrentFrame(param.Name, refined)
	}
}

// reassignmentConflicts reports whether rebinding a local of type current to
// a value of type next is a known contradiction. Unknowns never conflict, nil
// acts as the neutral initializer in both directions, numeric retyping widens
// instead of erroring (arithmetic freely mixes int and float), and container
// re-initialization (hash/shape literals of different shapes) stays legal.
func reassignmentConflicts(current, next *TypeExpr, resolve namedTypeResolver) bool {
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
	return typeExprsDisjoint(current, next, resolve)
}

// narrowLocalArms rebinds a local to the subset of its known arms that keep
// and reports whether that subset is reachable. Unknown, poisoned, and
// any-typed locals stay unknown, and a filter that changes nothing binds
// nothing. An empty subset proves the requested outcome cannot occur.
func (c *scriptChecker) narrowLocalArms(name string, keep func(*TypeExpr) bool) bool {
	current := c.localTypeFor(name)
	if current == nil {
		return true
	}
	arms, ok := typeExprArms(current, 0)
	if !ok || len(arms) == 0 {
		return true
	}
	kept := make([]*TypeExpr, 0, len(arms))
	for _, arm := range arms {
		if keep(arm) {
			kept = append(kept, arm)
		}
	}
	if len(kept) == 0 {
		return false
	}
	if len(kept) == len(arms) {
		return true
	}
	if refined := unionTypeExprs(kept...); refined != nil {
		c.bindLocalType(name, refined)
	}
	return true
}

// narrowLocalTruthiness refines a local after a truthiness test: the truthy
// path drops nil arms, and the falsy path keeps only arms that can be falsy
// (nil, and bool for false).
func (c *scriptChecker) narrowLocalTruthiness(name string, truthy bool) bool {
	if truthy {
		return c.narrowLocalArms(name, func(arm *TypeExpr) bool { return arm.Kind != TypeNil })
	}
	return c.narrowLocalArms(name, func(arm *TypeExpr) bool {
		if _, isShapeValue := shapeValuePayload(arm); isShapeValue {
			// Runtime shape values are always truthy.
			return false
		}
		return arm.Kind == TypeNil || arm.Kind == TypeBool
	})
}

// narrowLocalNilness refines a local after an explicit nil test (`x == nil`,
// `x.nil?`): the nil path keeps only nil arms, and the non-nil path drops
// them (bool arms survive: false is falsy but not nil). The non-nil direction
// is always sound — a nil receiver or operand answers the test itself — but
// the nil-only direction trusts the test's positive answer, which a class
// instance can forge by overriding nil? or ==, so named arms disable it.
func (c *scriptChecker) narrowLocalNilness(name string, isNil bool) bool {
	if !isNil {
		return c.narrowLocalArms(name, func(arm *TypeExpr) bool { return arm.Kind != TypeNil })
	}
	arms, ok := typeExprArms(c.localTypeFor(name), 0)
	if !ok {
		return true
	}
	for _, arm := range arms {
		if arm.Kind == TypeEnum {
			return true
		}
	}
	return c.narrowLocalArms(name, func(arm *TypeExpr) bool { return arm.Kind == TypeNil })
}

// narrowNilPredicateMember narrows a bare `x.nil?` receiver. Safe navigation
// is excluded: `x&.nil?` yields nil (falsy) for a nil receiver, so its falsy
// path does not prove the receiver non-nil.
func (c *scriptChecker) narrowNilPredicateMember(member *MemberExpr, truthy bool) bool {
	if member == nil || member.Safe || member.Property != "nil?" {
		return true
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return true
	}
	return c.narrowLocalNilness(ident.Name, truthy)
}

// knownPureUniversalPredicateMember reports whether every receiver arm that
// can dispatch is guaranteed to use one of the pure universal predicates.
// Named arms are excluded because a class may override the member. Hash-like
// facts must also rule out a callable field with the same name.
func (c *scriptChecker) knownPureUniversalPredicateMember(member *MemberExpr) bool {
	if member == nil {
		return false
	}
	if _, ok := universalMemberSpecs[member.Property]; !ok {
		return false
	}
	arms, ok := typeExprArms(c.inferExpressionType(member.Object), 0)
	if !ok || len(arms) == 0 {
		return false
	}
	dispatchArms := 0
	for _, arm := range arms {
		if member.Safe && arm.Kind == TypeNil {
			continue
		}
		if !typeArmUsesUniversalMemberDispatch(arm, member.Property) {
			return false
		}
		dispatchArms++
	}
	return dispatchArms > 0
}

// typeArmUsesUniversalMemberDispatch reports whether a known fact arm must
// reach the universal implementation of property. Named values may override
// it. Hash-like facts are safe only when their exact value or field contract
// rules out a callable export with the same name. Primitive kinds with their
// own typed contract also dispatch before the universal fallback.
func typeArmUsesUniversalMemberDispatch(arm *TypeExpr, property string) bool {
	if arm == nil {
		return false
	}
	switch arm.Kind {
	case TypeEnum:
		return false
	case TypeHash:
		return len(arm.TypeArgs) == 2 && !typeExprMayIncludeCallable(arm.TypeArgs[1])
	case TypeShape:
		field, present := arm.Shape[property]
		return !present || !typeExprMayIncludeCallable(field)
	case TypeNumber:
		_, intOverride := staticMemberSpecs["int."+property]
		_, floatOverride := staticMemberSpecs["float."+property]
		return !intOverride && !floatOverride
	case TypeAny, TypeUnknown, TypeUnion:
		return false
	}
	kind, ok := receiverKindForTypeArm(arm)
	if !ok {
		return false
	}
	_, overridden := staticMemberSpecs[kind+"."+property]
	return !overridden
}

func typeExprMayIncludeCallable(ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	switch ty.Kind {
	case TypeAny, TypeUnknown, TypeFunction:
		return true
	case TypeUnion:
		for _, option := range ty.Union {
			if typeExprMayIncludeCallable(option) {
				return true
			}
		}
	}
	return false
}

// knownPureUniversalPredicateCall recognizes a call whose member dispatch is
// guaranteed to reach a pure universal predicate and whose arguments provably
// run no user code. Arguments evaluate before the predicate dispatches, so an
// argument that can call into script code may mutate the receiver through an
// alias — the receiver's fact must not survive such a call.
func (c *scriptChecker) knownPureUniversalPredicateCall(call *CallExpr) bool {
	if call == nil || len(call.KwArgs) > 0 || call.Block != nil || call.BlockArg != nil {
		return false
	}
	for _, arg := range call.Args {
		if !c.predicateArgumentIsPure(arg) {
			return false
		}
	}
	member, ok := call.Callee.(*MemberExpr)
	return ok && c.knownPureUniversalPredicateMember(member)
}

// predicateArgumentIsPure reports whether a predicate argument provably runs
// no user code when it evaluates: literals and plain non-callable reads
// qualify. An identifier stays pure only when it cannot auto-invoke a
// callable — neither as a resolved zero-arity function or builtin, nor as a
// local whose value may itself be callable (a stored zero-arity function
// auto-invokes when the argument evaluates).
func (c *scriptChecker) predicateArgumentIsPure(expr Expression) bool {
	switch typed := expr.(type) {
	case *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral,
		*NilLiteral, *SymbolLiteral:
		return true
	case *Identifier:
		if _, autoCallable := c.resolveCallable(&CallExpr{Callee: typed}); autoCallable {
			return false
		}
		return !typeExprMayIncludeCallable(c.inferExpressionType(typed))
	case *UnaryExpr:
		return c.predicateArgumentIsPure(typed.Right)
	}
	return false
}

// typeExprDefinitelyTruthy reports whether every possible value of the type
// is truthy: everything except nil and false is truthy, so any arm that can
// be nil or bool (or is unknown) stays undecided.
func typeExprDefinitelyTruthy(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		if _, isShapeValue := shapeValuePayload(arm); isShapeValue {
			// Runtime shape values are always truthy.
			return true
		}
		return arm.Kind != TypeNil && arm.Kind != TypeBool &&
			arm.Kind != TypeAny && arm.Kind != TypeUnknown
	})
}

// typeExprNeverNil reports whether no arm can hold nil. Unlike definite
// truthiness, bool arms qualify: false is a legal value but not nil, and
// safe navigation skips only on nil.
func typeExprNeverNil(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		if _, isShapeValue := shapeValuePayload(arm); isShapeValue {
			return true
		}
		return arm.Kind != TypeNil && arm.Kind != TypeAny && arm.Kind != TypeUnknown
	})
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

// safeNavigationReceiver returns the receiver of a safe-navigation call
// with argument-skip semantics.
func safeNavigationReceiver(call *CallExpr) (Expression, bool) {
	if call == nil || !call.Safe {
		return nil, false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || !member.Safe {
		return nil, false
	}
	return member.Object, true
}

// safeNavigationCallSkipsInferred reports whether inferred facts prove a
// safe-navigation receiver is nil, so the dispatch and its arguments never
// evaluate.
func (c *scriptChecker) safeNavigationCallSkipsInferred(call *CallExpr) bool {
	obj, ok := safeNavigationReceiver(call)
	if !ok {
		return false
	}
	return typeExprIsNilOnly(c.inferExpressionType(obj))
}

// safeNavigationArgumentsAlwaysEvaluateInferred reports whether inferred
// facts prove a safe-navigation receiver is never nil, so the skipped-
// arguments path is impossible and must not join the merge.
func (c *scriptChecker) safeNavigationArgumentsAlwaysEvaluateInferred(call *CallExpr) bool {
	obj, ok := safeNavigationReceiver(call)
	if !ok {
		return false
	}
	return typeExprNeverNil(c.inferExpressionType(obj))
}

// --- expression type inference ---

// inferExpressionType computes the static type of an expression, or nil when
// it is not statically known. It is pure: it never emits warnings and never
// mutates checker state.
func (c *scriptChecker) inferExpressionType(expr Expression) *TypeExpr {
	// Inference computes facts speculatively (branch results, argument
	// captures) and must not mutate runtime module state along the way.
	c.speculativeInference++
	defer func() { c.speculativeInference-- }()
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
		if ty := c.localTypeFor(typed.Name); ty != nil {
			return ty
		}
		return c.autoInvokedBuiltinResultFact(typed.Name)
	case *MemberExpr:
		return c.memberResultFact(typed)
	case *UnaryExpr:
		return c.inferUnaryExprType(typed)
	case *BinaryExpr:
		left := c.inferExpressionType(typed.Left)
		right := c.inferExpressionType(typed.Right)
		switch typed.Operator {
		case tokenAnd, tokenOr:
			// `a && b` is `a ? b : a`: a left operand whose truthiness is
			// statically known picks one side instead of the union.
			isAnd := typed.Operator == tokenAnd
			if val, ok := staticLiteralValue(typed.Left); ok {
				if val.Truthy() == isAnd {
					return right
				}
				return left
			}
			if typeExprDefinitelyTruthy(left) {
				if isAnd {
					return right
				}
				return left
			}
			if typeExprIsNilOnly(left) {
				if isAnd {
					return left
				}
				return right
			}
		}
		return c.binaryOperationOutcome(typed.Operator, left, right).result
	case *ConditionalExpr:
		return c.inferConditionalExpressionType(typed)
	case *IfExpr:
		return c.inferIfExpressionType(typed)
	case *RescueExpr:
		return c.inferBranchUnionType(typed.Body, typed.Fallback)
	case *CallExpr:
		return c.inferCallExprType(typed)
	case *IndexExpr:
		return c.inferIndexExprType(typed)
	}
	return nil
}

func (c *scriptChecker) inferConditionalExpressionType(expr *ConditionalExpr) *TypeExpr {
	baseScopeState := c.snapshotScopeState()
	defer c.restoreScopeState(baseScopeState)

	branches := make([]*TypeExpr, 0, 2)
	if c.applyConditionOutcomeEffects(expr.Condition, true, nil) {
		branches = append(branches, c.inferExpressionType(expr.Consequent))
	}
	c.restoreScopeState(baseScopeState)
	if c.applyConditionOutcomeEffects(expr.Condition, false, nil) {
		branches = append(branches, c.inferExpressionType(expr.Alternate))
	}
	return unionTypeExprs(branches...)
}

func (c *scriptChecker) inferIfExpressionType(expr *IfExpr) *TypeExpr {
	baseScopeState := c.snapshotScopeState()
	defer c.restoreScopeState(baseScopeState)

	branches := make([]*TypeExpr, 0, len(expr.ElseIf)+2)
	appendBranch := func(result Expression) {
		if result == nil {
			branches = append(branches, checkTypeNil)
			return
		}
		branches = append(branches, c.inferExpressionType(result))
	}
	collectCondition := func(condition, result Expression) bool {
		conditionScopeState := c.snapshotScopeState()
		if c.applyConditionOutcomeEffects(condition, true, nil) {
			appendBranch(result)
		}
		c.restoreScopeState(conditionScopeState)
		return c.applyConditionOutcomeEffects(condition, false, nil)
	}

	falseReachable := collectCondition(expr.Condition, expr.Consequent)
	for _, branch := range expr.ElseIf {
		if !falseReachable {
			break
		}
		falseReachable = collectCondition(branch.Condition, branch.Result)
	}
	if falseReachable {
		appendBranch(expr.Alternate)
	}
	return unionTypeExprs(branches...)
}

// inferredConditionTruthiness resolves a condition's truthiness from static
// literals first, then from inferred facts, mirroring the short-circuit
// operators: a definitely-truthy type proves the true path runs, a nil-only
// type proves it never does.
func (c *scriptChecker) inferredConditionTruthiness(condition Expression) (bool, bool) {
	if truthy, known := staticExpressionTruthiness(condition); known {
		return truthy, known
	}
	ty := c.inferExpressionType(condition)
	if typeExprDefinitelyTruthy(ty) {
		return true, true
	}
	if typeExprIsNilOnly(ty) {
		return false, true
	}
	return false, false
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
		if !c.hashShapeStaticallyShadowed(lit) {
			return shapeValueType(lit.ShapeType)
		}
		// A proven shadow forces the hash reading at runtime, so the
		// literal infers its hash facts below like any other braced group.
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
	case tokenBang:
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
// caller. Splats and constructors stay unknown; script functions reached
// through safe navigation stay unknown too. A nil-only safe-navigation
// receiver yields nil without dispatch; otherwise a builtin contract's
// invariant result flows through with nil added. JSON.parse_as models its
// validated shape (ADR-004).
func (c *scriptChecker) inferCallExprType(call *CallExpr) *TypeExpr {
	if c.safeNavigationCallSkipsInferred(call) {
		return checkTypeNil
	}
	safeNavigation := false
	if member, ok := call.Callee.(*MemberExpr); ok && member.Safe {
		safeNavigation = true
	}
	target, ok := c.resolveCallable(call)
	if !ok || callExpandsArguments(call) {
		return nil
	}
	if target.fn != nil {
		if target.constructor || safeNavigation {
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
	if safeNavigation {
		return nullableTypeExpr(target.spec.resultType)
	}
	return target.spec.resultType
}

// nullableTypeExpr returns the type with a nil arm added, sharing the
// original when it already admits nil.
func nullableTypeExpr(ty *TypeExpr) *TypeExpr {
	if ty == nil || ty.Nullable || ty.Kind == TypeNil {
		return ty
	}
	clone := *ty
	clone.Nullable = true
	return &clone
}

// memberResultFact reports the invariant result of a bare member read. It
// covers both builtins that auto-invoke (`s.to_i` or `Time.now` without
// parentheses) and temporal conversions exposed directly as scalar values
// (`d.to_i`). A nil-only safe-navigation receiver yields nil without dispatch.
// Other members that resolve to script functions or carry no result contract
// stay unknown; safe navigation adds nil.
func (c *scriptChecker) memberResultFact(member *MemberExpr) *TypeExpr {
	if member.Safe && typeExprIsNilOnly(c.inferExpressionType(member.Object)) {
		return checkTypeNil
	}
	if result := c.staticMemberValueResultFact(member); result != nil {
		if member.Safe {
			return nullableTypeExpr(result)
		}
		return result
	}
	target, ok := c.resolveMemberCallable(member)
	if !ok || target.fn != nil || !target.spec.autoInvoke {
		return nil
	}
	if member.Safe {
		return nullableTypeExpr(target.spec.resultType)
	}
	return target.spec.resultType
}

// staticMemberValueResultFact resolves a direct scalar value only when every
// known receiver arm dispatches through the same built-in kind. Named or
// dynamic receivers stay unknown so user-defined members keep precedence.
func (c *scriptChecker) staticMemberValueResultFact(member *MemberExpr) *TypeExpr {
	kinds, ok := c.staticMemberReceiverKinds(member)
	if !ok {
		return nil
	}
	kind := kinds[0]
	for _, candidate := range kinds[1:] {
		if candidate != kind {
			return nil
		}
	}
	return staticMemberValueTypes[kind+"."+member.Property]
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

// literalPartialElementsMarker tags an inferred array whose witnessed arms
// are real elements but do not cover the whole literal (some elements were
// unknown): sound for disjointness, unusable as an element bound.
const literalPartialElementsMarker = "\x00literal-partial-elements"

// inferArrayLiteralType infers a witnessed element union for an array
// literal. Empty literals and literals with unknown elements stay a bare
// array.
func (c *scriptChecker) inferArrayLiteralType(lit *ArrayLiteral) *TypeExpr {
	if len(lit.Elements) == 0 {
		return checkTypeArray
	}
	elements := make([]*TypeExpr, 0, len(lit.Elements))
	sawUnknown := false
	for _, element := range lit.Elements {
		if _, splat := element.(*SplatArg); splat {
			sawUnknown = true
			continue
		}
		elementType := c.inferExpressionType(element)
		if elementType == nil {
			sawUnknown = true
			continue
		}
		elements = append(elements, elementType)
	}
	if len(elements) == 0 {
		return checkTypeArray
	}
	union := unionTypeExprs(elements...)
	if union == nil {
		return checkTypeArray
	}
	marker := literalElementsMarker
	if sawUnknown {
		// Known elements stay witnesses even when others are unknown, but
		// the union no longer bounds every element.
		marker = literalPartialElementsMarker
	}
	return &TypeExpr{Kind: TypeArray, Name: marker, TypeArgs: []*TypeExpr{union}}
}

// applyShovelMutationFacts accounts for the in-place append the shovel
// operator performs on its receiver: a witnessed-element array gains the
// appended element's type as a new witness, and any other container fact is
// poisoned since the checker no longer describes the mutated value.
func (c *scriptChecker) applyShovelMutationFacts(expr *BinaryExpr) {
	if expr.Operator != tokenShovel {
		return
	}
	if _, chained := expr.Left.(*BinaryExpr); chained {
		// A chained shovel ((values << a) << b) appends through the returned
		// receiver. A compatible append against the chain root's declared
		// bound keeps the fact true exactly like the direct form; anything
		// else drops the root fact — the outer appends are not retyped.
		root, isIdent := unwrapShovelChain(expr.Left).(*Identifier)
		if !isIdent {
			if name, ok := rootIdentifierName(unwrapShovelChain(expr.Left)); ok {
				c.poisonLocalType(name)
			}
			return
		}
		if elem := declaredArrayElementType(c.localTypeFor(root.Name)); elem != nil {
			if appended := c.inferExpressionType(expr.Right); appended != nil &&
				typeExprSatisfies(appended, elem, c.checkNamedTypeResolver()) {
				c.linkContainerWriteAlias(root.Name, expr.Right, appended)
				return
			}
		}
		c.poisonLocalType(root.Name)
		return
	}
	ident, ok := expr.Left.(*Identifier)
	if !ok {
		if name, ok := rootIdentifierName(expr.Left); ok {
			c.poisonLocalType(name)
		}
		return
	}
	current := c.localTypeFor(ident.Name)
	if current == nil {
		return
	}
	if elem := declaredArrayElementType(current); elem != nil {
		if appended := c.inferExpressionType(expr.Right); appended != nil {
			if typeExprSatisfies(appended, elem, c.checkNamedTypeResolver()) {
				// A compatible append keeps the declared fact true for the
				// receiver and every alias — inside regions too: nothing
				// rebinds, so the region's state restore stays correct. The
				// receiver retains an appended container, so its root local
				// links in: a later mutation through it weakens both.
				c.linkContainerWriteAlias(ident.Name, expr.Right, appended)
				return
			}
			if c.mutationRegionDepth == 0 && len(c.typeAliases[ident.Name]) == 0 {
				if refined := appendedArrayFact(current, appended); refined != nil {
					c.bindLocalType(ident.Name, refined)
					return
				}
			}
		}
		c.poisonLocalType(ident.Name)
		return
	}
	// Inside a loop or block body the walk's retypes are rolled back by the
	// region's state restore, so an in-place append there must poison
	// (monotone, survives the restore) rather than refine; an aliased
	// receiver likewise poisons, since the refinement cannot reach the
	// other names sharing the array.
	if c.mutationRegionDepth == 0 && len(c.typeAliases[ident.Name]) == 0 &&
		current.Kind == TypeArray && !current.Nullable {
		if appended := c.inferExpressionType(expr.Right); appended != nil {
			if refined := appendedArrayFact(current, appended); refined != nil {
				c.bindLocalType(ident.Name, refined)
				return
			}
		}
	}
	if typeExprHasContainerArm(current) {
		c.poisonLocalType(ident.Name)
	}
}

func unwrapShovelChain(expr Expression) Expression {
	for {
		binary, ok := expr.(*BinaryExpr)
		if !ok || binary.Operator != tokenShovel {
			return expr
		}
		expr = binary.Left
	}
}

// appendedArrayFact derives the array fact after appending a value of a
// known type: witnessed receivers join the new arm, while annotation-typed
// and bare arrays start a partial witness set with just the appended arm —
// their prior elements were never witnessed (the array may have been
// empty), so only the append may prove a contradiction.
func appendedArrayFact(current, appended *TypeExpr) *TypeExpr {
	switch current.Name {
	case literalElementsMarker, literalPartialElementsMarker:
		if len(current.TypeArgs) != 1 {
			return nil
		}
		union := unionTypeExprs(current.TypeArgs[0], appended)
		if union == nil {
			return nil
		}
		return &TypeExpr{Kind: TypeArray, Name: current.Name, TypeArgs: []*TypeExpr{union}}
	default:
		return &TypeExpr{Kind: TypeArray, Name: literalPartialElementsMarker, TypeArgs: []*TypeExpr{appended}}
	}
}

// declaredArrayElementType returns the element bound of a definite
// annotation-derived array<T> fact: a boundary-validated array whose every
// element is known to satisfy T, so an element write can be checked against
// it. Witnessed literal facts carry no such bound (their unions describe
// elements that exist, not a constraint on writes), and nullable or union
// facts are not definitely arrays.
func declaredArrayElementType(ty *TypeExpr) *TypeExpr {
	if ty == nil || ty.Kind != TypeArray || ty.Nullable {
		return nil
	}
	if ty.Name == literalElementsMarker || ty.Name == literalPartialElementsMarker {
		return nil
	}
	if len(ty.TypeArgs) != 1 {
		return nil
	}
	return ty.TypeArgs[0]
}

// reportIncompatibleElementWrite records the diagnostic shared by shovel,
// indexed, and mutator-call element writes whose value can never satisfy the
// receiver's declared element type.
func (c *scriptChecker) reportIncompatibleElementWrite(function string, pos Position, name string, elem, written *TypeExpr) {
	c.add(function, pos, "write to %s expected element %s, got %s",
		name, formatTypeExpr(elem), formatTypeExpr(written))
}

// checkShovelElementWrite reports a shovel append whose value is provably
// disjoint from the receiver's declared element type. Chained shovels append
// through the returned receiver, so the chain root's fact is the one the
// append contradicts. The receiver fact arrives as captured before the right
// operand evaluated — a right side that escapes the same local cannot erase
// the bound the append contradicts — and the check runs before
// applyShovelMutationFacts updates the receiver's fact for the append.
func (c *scriptChecker) checkShovelElementWrite(function string, expr *BinaryExpr, receiverFact *TypeExpr) {
	if expr.Operator != tokenShovel {
		return
	}
	ident, ok := unwrapShovelChain(expr.Left).(*Identifier)
	if !ok {
		return
	}
	elem := declaredArrayElementType(receiverFact)
	if elem == nil {
		return
	}
	written := c.inferExpressionType(expr.Right)
	if written == nil {
		return
	}
	if typeExprsDisjoint(written, elem, c.checkNamedTypeResolver()) {
		c.reportIncompatibleElementWrite(function, expr.Pos(), ident.Name, elem, written)
	}
}

// applyIndexedElementWriteFacts checks a direct arr[i] = value element write
// against the receiver's declared element type and reports whether the fact
// still holds: a compatible write replaces one element with another admitted
// one (for the receiver and every alias, inside regions too — nothing
// rebinds), while every other write weakens through the caller's poison.
// receiverFact arrives as captured before the value expression walked; a
// value that escaped the same local keeps the bound for diagnosis, while
// preservation requires the local's fact to have survived unchanged.
func (c *scriptChecker) applyIndexedElementWriteFacts(function string, stmt *AssignStmt, target *IndexExpr, receiverFact *TypeExpr) bool {
	if stmt.Operator != "" {
		return false
	}
	ident, ok := target.Object.(*Identifier)
	if !ok {
		return false
	}
	current := c.localTypeFor(ident.Name)
	if receiverFact == nil {
		receiverFact = current
	}
	elem := declaredArrayElementType(receiverFact)
	if elem == nil {
		return false
	}
	if len(target.Indices) != 1 {
		return false
	}
	// A provably non-numeric index raises before any element is written, so
	// the write never lands and neither diagnosis nor preservation applies.
	if kind, known := staticOperandKind(c.inferExpressionType(target.Indices[0])); known &&
		kind != TypeInt && kind != TypeFloat && kind != TypeNumber {
		return false
	}
	written := c.inferExpressionType(stmt.Value)
	if written == nil {
		return false
	}
	resolve := c.checkNamedTypeResolver()
	if typeExprsDisjoint(written, elem, resolve) {
		c.reportIncompatibleElementWrite(function, stmt.Pos(), ident.Name, elem, written)
		return false
	}
	if !typeExprSatisfies(written, elem, resolve) || !mutatorReceiverFactIntact(current, receiverFact) {
		return false
	}
	// The receiver retains a written container element, so its root local
	// links in: a later mutation through it weakens both.
	c.linkContainerWriteAlias(ident.Name, stmt.Value, written)
	return true
}

// arrayMutatorElementWrites returns the argument expressions an in-place
// builtin array mutator call writes as new elements, and whether a fully
// compatible call can preserve the receiver's declared fact. insert may pad
// the gap to a beyond-end index with nils, so its fact never survives; a
// keyword argument makes every mutator raise before writing.
func arrayMutatorElementWrites(call *CallExpr, property string) (elements []Expression, preservable, ok bool) {
	if len(call.KwArgs) != 0 {
		return nil, false, false
	}
	switch property {
	case "push", "append", "prepend", "unshift":
		return call.Args, true, true
	case "insert":
		if len(call.Args) == 0 {
			return nil, false, false
		}
		// An index-only insert writes nothing: the runtime returns the
		// receiver unchanged after validating the index, so the fact
		// survives. With values the beyond-end nil padding still applies.
		return call.Args[1:], len(call.Args) == 1, true
	}
	return nil, false, false
}

// applyArrayMutatorCallFacts checks the elements an in-place builtin array
// mutator writes against the receiver's declared element type. preserved
// reports whether every write is provably compatible, in which case the
// receiver's fact still holds and the caller skips its escape poison.
// modeled reports whether the call's argument effects are fully accounted
// for — the builtin only reads and retains its arguments, with retention
// tracked through container write aliases — so the caller also skips the
// generic argument escape poison that would otherwise cascade through those
// aliases and undo the preservation. Both the receiver fact and the
// argument facts are read as captured at their own evaluation points: the
// receiver evaluates before any argument, so an argument that escapes the
// same local cannot erase the bound the writes contradict. Preservation
// additionally requires the local's fact to have survived the argument walk
// unchanged.
func (c *scriptChecker) applyArrayMutatorCallFacts(function string, call *CallExpr, member *MemberExpr, argumentFacts map[Expression]*TypeExpr, receiverFact *TypeExpr) (preserved, modeled bool) {
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return false, false
	}
	elem := declaredArrayElementType(receiverFact)
	if elem == nil {
		return false, false
	}
	elements, preservable, ok := arrayMutatorElementWrites(call, member.Property)
	if !ok {
		return false, false
	}
	// insert validates its index before any element lands, so a provably
	// non-numeric index means the call raises without writing and neither
	// diagnosis nor preservation applies. A splatted index makes the
	// argument positions (and an empty expansion, which raises) unknowable.
	if member.Property == "insert" {
		if _, isSplat := call.Args[0].(*SplatArg); isSplat {
			return false, true
		}
		index, captured := argumentFacts[call.Args[0]]
		if !captured {
			index = c.inferExpressionType(call.Args[0])
		}
		if kind, known := staticOperandKind(index); known &&
			kind != TypeInt && kind != TypeFloat && kind != TypeNumber {
			return false, true
		}
	}
	resolve := c.checkNamedTypeResolver()
	// The mutators return their receiver, so a consumed result (a chained
	// call, an argument, an assignment value) hands the array to code the
	// checker cannot follow: only a statement-level call, whose value is
	// discarded, can keep the declared bound — and only when the argument
	// walk left the local's fact unchanged (an argument may poison or
	// rebind the same local).
	preserved = preservable && Expression(call) == c.expressionStatementRoot &&
		mutatorReceiverFactIntact(c.localTypeFor(ident.Name), receiverFact)
	for _, arg := range elements {
		if splat, isSplat := arg.(*SplatArg); isSplat {
			if !c.applySplattedElementWriteFacts(function, splat, ident.Name, elem, resolve) {
				preserved = false
			}
			continue
		}
		written, captured := argumentFacts[arg]
		if !captured {
			written = c.inferExpressionType(arg)
		}
		// The receiver retains every written element regardless of
		// compatibility, so a container-rooted element's local links in: a
		// later mutation through it weakens both.
		c.linkContainerWriteAlias(ident.Name, arg, written)
		if written == nil {
			preserved = false
			continue
		}
		if typeExprsDisjoint(written, elem, resolve) {
			c.reportIncompatibleElementWrite(function, arg.Pos(), ident.Name, elem, written)
			preserved = false
			continue
		}
		if !typeExprSatisfies(written, elem, resolve) {
			preserved = false
		}
	}
	return preserved, true
}

// applySplattedElementWriteFacts checks a splatted mutator element argument:
// the runtime expands the splatted array's elements into the written
// positions, so a witnessed element arm provably disjoint from the bound is
// reported (witness arms are real elements), and the write is compatible
// only when the splatted array's own element bound — a declared array<V> or
// a full witness union — satisfies the receiver's. A typed but possibly
// empty splat stays silent: no element is proven to land.
func (c *scriptChecker) applySplattedElementWriteFacts(function string, splat *SplatArg, name string, elem *TypeExpr, resolve namedTypeResolver) bool {
	written := c.inferExpressionType(splat.Value)
	if written == nil || written.Kind != TypeArray || written.Nullable {
		// The receiver retains whatever elements the splat expands to, so
		// an unknown splatted local links in conservatively.
		c.linkContainerWriteAlias(name, splat.Value, nil)
		return false
	}
	// The receiver retains the splatted array's elements regardless of
	// compatibility, so its root local links in when those elements may be
	// containers: a later mutation through it weakens both.
	bound := splattedElementBound(written)
	c.linkContainerWriteAlias(name, splat.Value, bound)
	if written.Name == literalElementsMarker || written.Name == literalPartialElementsMarker {
		if len(written.TypeArgs) == 1 {
			if arms, ok := typeExprArms(written.TypeArgs[0], 0); ok {
				for _, arm := range arms {
					if typeExprsDisjoint(arm, elem, resolve) {
						c.reportIncompatibleElementWrite(function, splat.Pos(), name, elem, arm)
						return false
					}
				}
			}
		}
	}
	return bound != nil && typeExprSatisfies(bound, elem, resolve)
}

// splattedElementBound returns the element bound of a splatted array fact:
// declared array<V> facts and full witness unions bound every element, while
// partial witnesses and bare arrays do not.
func splattedElementBound(ty *TypeExpr) *TypeExpr {
	if ty == nil || ty.Kind != TypeArray || ty.Nullable || len(ty.TypeArgs) != 1 {
		return nil
	}
	if ty.Name == literalPartialElementsMarker {
		return nil
	}
	return ty.TypeArgs[0]
}

// mutatorReceiverFactIntact reports whether a mutator receiver's local fact
// survived the argument walk unchanged, so preserving it is still about the
// same fact the writes were checked against.
func mutatorReceiverFactIntact(current, captured *TypeExpr) bool {
	return current != nil && typeFactKey(current) == typeFactKey(captured)
}

// expressionContainsBlockLiteral reports whether evaluating expr can run a
// block or lambda body written inline, the constructs whose assignments
// write through to enclosing locals mid-expression. Lambdas defined earlier
// already degraded the locals their bodies assign when their literals
// walked, so only inline literals need detecting here.
func expressionContainsBlockLiteral(expr Expression) bool {
	contains := false
	var visit func(Expression)
	visitAll := func(exprs ...Expression) {
		for _, e := range exprs {
			visit(e)
		}
	}
	visit = func(e Expression) {
		if contains || e == nil {
			return
		}
		switch typed := e.(type) {
		case *BlockLiteral:
			contains = true
		case *ArrayLiteral:
			visitAll(typed.Elements...)
		case *HashLiteral:
			for _, pair := range typed.Pairs {
				visitAll(pair.Key, pair.Value)
			}
		case *CallExpr:
			if typed.Block != nil {
				contains = true
				return
			}
			visit(typed.Callee)
			visitAll(typed.Args...)
			for _, kwarg := range typed.KwArgs {
				visit(kwarg.Value)
			}
			visit(typed.BlockArg)
		case *MemberExpr:
			visit(typed.Object)
		case *ScopeExpr:
			visit(typed.Object)
		case *IndexExpr:
			visit(typed.Object)
			visitAll(typed.Indices...)
		case *SplatArg:
			visit(typed.Value)
		case *UnaryExpr:
			visit(typed.Right)
		case *BinaryExpr:
			visitAll(typed.Left, typed.Right)
		case *ConditionalExpr:
			visitAll(typed.Condition, typed.Consequent, typed.Alternate)
		case *RescueExpr:
			visitAll(typed.Body, typed.Fallback)
		case *IfExpr:
			visitAll(typed.Condition, typed.Consequent, typed.Alternate)
			for _, branch := range typed.ElseIf {
				visitAll(branch.Condition, branch.Result)
			}
		case *RangeExpr:
			visitAll(typed.Start, typed.End)
		case *CaseExpr:
			visit(typed.Target)
			for _, clause := range typed.Clauses {
				for _, value := range clause.Values {
					visit(value.Expr)
				}
				visit(clause.Result)
			}
			visit(typed.ElseExpr)
		case *YieldExpr:
			visitAll(typed.Args...)
		case *InterpolatedString:
			for _, part := range typed.Parts {
				if exprPart, ok := part.(StringExpr); ok {
					visit(exprPart.Expr)
				}
			}
		case *InterpolatedSymbol:
			for _, part := range typed.Parts {
				if exprPart, ok := part.(StringExpr); ok {
					visit(exprPart.Expr)
				}
			}
		}
	}
	visit(expr)
	return contains
}

// linkContainerWriteAlias links a receiver whose fact a compatible write
// preserved to the root local of the written container value: the receiver
// retains the value, so a later mutation or escape through the original
// name invalidates the receiver's bound and must weaken both facts.
func (c *scriptChecker) linkContainerWriteAlias(receiver string, value Expression, written *TypeExpr) {
	if written != nil && !typeExprHasContainerArm(written) {
		return
	}
	switch value.(type) {
	case *Identifier, *IndexExpr, *MemberExpr:
		if root, ok := rootIdentifierName(value); ok {
			c.linkContainerAlias(receiver, root)
		}
	}
}

// literalArrayDisjoint reports whether a witnessed-element array can never
// satisfy another array type: some witnessed element arm is disjoint from
// the other side's declared element type.
func literalArrayDisjoint(lit, other *TypeExpr, resolve namedTypeResolver) bool {
	if lit.Name != literalElementsMarker && lit.Name != literalPartialElementsMarker {
		return false
	}
	if len(lit.TypeArgs) != 1 || len(other.TypeArgs) != 1 {
		return false
	}
	arms, ok := typeExprArms(lit.TypeArgs[0], 0)
	if !ok {
		return false
	}
	for _, arm := range arms {
		if typeExprsDisjoint(arm, other.TypeArgs[0], resolve) {
			return true
		}
	}
	return false
}

// shapeVsTypedHashDisjoint reports whether an exact shape can never satisfy
// a generic hash type: shapes witness every field, so a field type disjoint
// from the hash's value type contradicts it. Key types are left to runtime
// (key representation is not always known statically).
func shapeVsTypedHashDisjoint(shape, hash *TypeExpr, resolve namedTypeResolver) bool {
	if len(hash.TypeArgs) != 2 {
		return false
	}
	// A known key representation contradicts a disjoint hash key type: a
	// string-keyed store never satisfies hash<symbol, ...> and vice versa.
	if len(shape.Shape) > 0 {
		var keyType *TypeExpr
		switch shape.Name {
		case shapeKeysStringMarker:
			keyType = checkTypeString
		case shapeKeysSymbolMarker:
			keyType = checkTypeSymbol
		}
		if keyType != nil && typeExprsDisjoint(keyType, hash.TypeArgs[0], resolve) {
			return true
		}
	}
	valueType := hash.TypeArgs[1]
	for _, field := range shape.Shape {
		if typeExprsDisjoint(field, valueType, resolve) {
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
// known binding (a local — including one the function predeclares by
// assigning it anywhere, matching runtime predeclaration — a host global,
// script definition, or host builtin), the group keeps hash semantics. A
// group without a hash reading is always a shape.
func (c *scriptChecker) hashShapeStaticallyShadowed(lit *HashLiteral) bool {
	if len(lit.Pairs) == 0 {
		return false
	}
	shadowed := false
	walkShapeTypeNames(lit.ShapeType, func(name string) {
		if shadowed {
			return
		}
		if c.identifierShadowed(name) || c.liveLocalNameHas(name) ||
			c.hostGlobalShadows(name) || c.typeRootResolvesName(name) ||
			c.hostBuiltinOverrides(name) || c.implicitSelfShadows(name) {
			shadowed = true
		}
	})
	return shadowed
}

// implicitSelfShadows reports whether a bare identifier would resolve
// through implicit self in the current context, mirroring the runtime probe:
// instance methods dispatch through the class's instance methods, class
// methods and class bodies through its class methods, so only the matching
// member kind shadows. An unknown receiver context stays conservative.
func (c *scriptChecker) implicitSelfShadows(name string) bool {
	if !c.selfScope {
		return false
	}
	if c.selfClass == nil {
		return true
	}
	if c.selfClassContext {
		_, ok := c.selfClass.ClassMethods[name]
		return ok
	}
	_, ok := c.selfClass.Methods[name]
	return ok
}

// typeRootResolvesName mirrors the runtime's env.Get chain walk: engine
// builtins (a lowercase money, for example) resolve in every environment,
// and an exported module function invoked by its caller executes under the
// caller's root, so caller-context parent roots shadow too.
func (c *scriptChecker) typeRootResolvesName(name string) bool {
	for _, root := range []*Env{c.runtimeTypeRoot, c.typeRoot} {
		if root == nil {
			continue
		}
		if _, ok := root.Get(name); ok {
			return true
		}
	}
	return false
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
		if len(objectType.TypeArgs) != 1 || objectType.Name == literalPartialElementsMarker {
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
	if typeExprsDisjoint(inferred, ty, c.checkNamedTypeResolver()) {
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
	// Prefer the fact captured at the argument's own evaluation point: a
	// later argument's mutations must not erase (or supply) facts for an
	// earlier one.
	inferred, captured := c.callArgumentFacts[expr]
	if !captured {
		inferred = c.inferExpressionType(expr)
	}
	if inferred == nil {
		return
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return
	}
	if typeExprsDisjoint(inferred, ty, c.checkNamedTypeResolver()) {
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
	if name, ok := c.escapePoisonTarget(expr); ok {
		c.poisonLocalType(name)
	}
}

// escapePoisonTarget reports the root local whose container facts stop
// holding when expr escapes into unfollowed code: a bare container-typed
// identifier, or a projection whose value may itself be a mutable container.
func (c *scriptChecker) escapePoisonTarget(expr Expression) (string, bool) {
	if binary, ok := expr.(*BinaryExpr); ok && binary.Operator == tokenShovel {
		// A shovel expression evaluates to its mutated receiver, so the
		// receiver escapes wherever the expression's value does. A declared
		// element bound cannot survive code that may append arbitrary
		// elements; witnessed facts stay (existing elements keep their
		// witnesses through appends).
		if ident, ok := unwrapShovelChain(binary.Left).(*Identifier); ok {
			if declaredArrayElementType(c.localTypeFor(ident.Name)) != nil {
				return ident.Name, true
			}
		}
		return "", false
	}
	name, ok := rootIdentifierName(expr)
	if !ok {
		return "", false
	}
	rootType := c.localTypeFor(name)
	if rootType == nil || !typeExprHasContainerArm(rootType) {
		return "", false
	}
	if _, isIdent := expr.(*Identifier); !isIdent {
		projected := c.inferExpressionType(expr)
		if projected != nil && !typeExprHasContainerArm(projected) {
			return "", false
		}
	}
	return name, true
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
	case tokenAnd, tokenOr:
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
	if op == tokenShovel && leftOK && leftKind == TypeArray {
		// A compatible append returns the receiver still satisfying its
		// declared bound, so an assignment of the result carries the bound
		// (and the alias link) instead of degrading to a partial witness.
		if elem := declaredArrayElementType(left); elem != nil {
			if right != nil && typeExprSatisfies(right, elem, c.checkNamedTypeResolver()) {
				return binaryOutcome{result: left}
			}
		}
		// The shovel operator returns its receiver with the element
		// appended, so the appended type joins (or seeds) the witnessed
		// arms; anything less certain degrades to a bare array.
		if right != nil {
			if refined := appendedArrayFact(left, right); refined != nil {
				return binaryOutcome{result: refined}
			}
		}
		return binaryOutcome{result: checkTypeArray}
	}
	if !leftOK || !rightOK {
		// Partial knowledge decides a couple of left-driven results but never
		// an invalidity.
		if leftOK && op == tokenPercent && leftKind == TypeString {
			return binaryOutcome{result: checkTypeString}
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
		// The dedup key canonicalizes the whole fact, including the internal
		// Name markers at every nesting level (shape key kinds, witnessed
		// array elements), so arms that render identically but carry
		// different markers stay distinct instead of collapsing to whichever
		// branch was joined first.
		key := typeFactKey(arm)
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

// typeFactKey canonicalizes a type fact for deduplication. Unlike
// formatTypeExpr it includes the Name field (which carries the internal
// key-kind and witnessed-element markers) at every nesting level.
func typeFactKey(ty *TypeExpr) string {
	var b strings.Builder
	appendTypeFactKey(&b, ty, 0)
	return b.String()
}

func appendTypeFactKey(b *strings.Builder, ty *TypeExpr, depth int) {
	if ty == nil || depth > maxTypeArmDepth {
		b.WriteString("?")
		return
	}
	b.WriteString(strconv.Itoa(int(ty.Kind)))
	b.WriteString(":")
	b.WriteString(ty.Name)
	if ty.Nullable {
		b.WriteString("?")
	}
	if len(ty.TypeArgs) > 0 {
		b.WriteString("<")
		for i, arg := range ty.TypeArgs {
			if i > 0 {
				b.WriteString(",")
			}
			appendTypeFactKey(b, arg, depth+1)
		}
		b.WriteString(">")
	}
	if len(ty.Shape) > 0 {
		fields := make([]string, 0, len(ty.Shape))
		for field := range ty.Shape {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		b.WriteString("{")
		for _, field := range fields {
			b.WriteString(field)
			b.WriteString(":")
			appendTypeFactKey(b, ty.Shape[field], depth+1)
			b.WriteString(",")
		}
		b.WriteString("}")
	}
	if len(ty.Union) > 0 {
		b.WriteString("(")
		for i, option := range ty.Union {
			if i > 0 {
				b.WriteString("|")
			}
			appendTypeFactKey(b, option, depth+1)
		}
		b.WriteString(")")
	}
}

// namedTypeResolver resolves a named (TypeEnum kind) annotation to its enum
// or class definition. A nil resolver, or a miss, keeps named arms
// conservatively compatible with everything, preserving gradual behavior for
// unresolved and host-supplied names.
type namedTypeResolver func(*TypeExpr) (namedTypeMatch, bool)

// checkNamedTypeResolver resolves named annotations through the same
// environments the runtime binds, so two spellings of one definition compare
// equal by resolved identity.
func (c *scriptChecker) checkNamedTypeResolver() namedTypeResolver {
	return func(ty *TypeExpr) (namedTypeMatch, bool) {
		match, ok, err := lookupNamedTypeForType(ty, c.runtimeTypeContext())
		if err != nil || !ok {
			return namedTypeMatch{}, false
		}
		return match, true
	}
}

// typeExprsDisjoint reports whether no runtime value can satisfy both types.
// It is deliberately conservative: unknown, any, and unresolved named types
// never count as disjoint, empty containers make all array and hash types
// overlap, and only exact shapes compare structurally. Resolved named types
// compare by definition identity through resolve.
func typeExprsDisjoint(a, b *TypeExpr, resolve namedTypeResolver) bool {
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
			if !typeArmPairDisjoint(x, y, resolve) {
				return false
			}
		}
	}
	return true
}

// typeExprSatisfies reports whether every runtime value of the written type
// provably satisfies the declared annotation — the dual of typeExprsDisjoint,
// and just as deliberately conservative: unknown or any-typed writes and
// relations the checker does not model all report false, so callers weaken a
// fact instead of preserving it.
func typeExprSatisfies(written, declared *TypeExpr, resolve namedTypeResolver) bool {
	if written == nil || declared == nil {
		return false
	}
	if declared.Kind == TypeAny && !declared.Nullable {
		return true
	}
	writtenArms, ok := typeExprArms(written, 0)
	if !ok || len(writtenArms) == 0 {
		return false
	}
	declaredArms, ok := typeExprArms(declared, 0)
	if !ok || len(declaredArms) == 0 {
		return false
	}
	for _, w := range writtenArms {
		admitted := false
		for _, d := range declaredArms {
			if typeArmAdmits(d, w, resolve) {
				admitted = true
				break
			}
		}
		if !admitted {
			return false
		}
	}
	return true
}

// typeArmAdmits reports whether every value of the written arm satisfies the
// declared arm, mirroring runtime annotation normalization: exact kind
// matches, number admitting int and float, containers whose facts bound
// every contained value, and resolved named types by definition identity.
// First-class shape values satisfy no annotation the arms model here.
func typeArmAdmits(declared, written *TypeExpr, resolve namedTypeResolver) bool {
	if _, isShapeValue := shapeValuePayload(written); isShapeValue {
		return false
	}
	if _, isShapeValue := shapeValuePayload(declared); isShapeValue {
		return false
	}
	switch declared.Kind {
	case TypeNumber:
		return written.Kind == TypeInt || written.Kind == TypeFloat || written.Kind == TypeNumber
	case TypeInt, TypeFloat, TypeString, TypeBool, TypeNil, TypeSymbol,
		TypeDuration, TypeTime, TypeMoney, TypeRange, TypeFunction:
		return written.Kind == declared.Kind
	case TypeArray:
		if written.Kind != TypeArray {
			return false
		}
		if len(declared.TypeArgs) != 1 {
			// A bare array annotation admits every array.
			return len(declared.TypeArgs) == 0
		}
		if written.Name == literalPartialElementsMarker || len(written.TypeArgs) != 1 {
			// Partial witnesses and bare arrays do not bound their elements.
			return false
		}
		return typeExprSatisfies(written.TypeArgs[0], declared.TypeArgs[0], resolve)
	case TypeHash:
		switch written.Kind {
		case TypeHash:
			if len(declared.TypeArgs) == 0 {
				return true
			}
			if len(declared.TypeArgs) != 2 || len(written.TypeArgs) != 2 {
				return false
			}
			return typeExprSatisfies(written.TypeArgs[0], declared.TypeArgs[0], resolve) &&
				typeExprSatisfies(written.TypeArgs[1], declared.TypeArgs[1], resolve)
		case TypeShape:
			// A shape is a hash at runtime, but its key representation and
			// field bounds against hash<K, V> are left to runtime checks.
			return len(declared.TypeArgs) == 0
		}
		return false
	case TypeShape:
		// Runtime shape normalization matches fields by display name with
		// the entry count pinned to the declared field count, so an exact
		// shape fact satisfies a shape annotation when the key sets match
		// and every witnessed field satisfies its declared type.
		if written.Kind != TypeShape || len(written.Shape) != len(declared.Shape) {
			return false
		}
		for field, fieldType := range written.Shape {
			declaredField, ok := declared.Shape[field]
			if !ok {
				return false
			}
			if !typeExprSatisfies(fieldType, declaredField, resolve) {
				return false
			}
		}
		return true
	case TypeEnum:
		if written.Kind != TypeEnum || resolve == nil {
			return false
		}
		dm, ok := resolve(declared)
		if !ok {
			return false
		}
		wm, ok := resolve(written)
		if !ok {
			return false
		}
		if dm.enum != nil || wm.enum != nil {
			return dm.enum != nil && dm.enum == wm.enum
		}
		if dm.class == nil || wm.class == nil {
			return false
		}
		if dm.class == wm.class {
			return true
		}
		// A module annotation admits instances of classes that include it,
		// mirroring runtime named normalization.
		return dm.class.IsModule && !wm.class.IsModule && classIncludesModule(wm.class, dm.class.Name)
	}
	return false
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
		if _, isShapeValue := shapeValuePayload(ty); !isShapeValue {
			return nil, false
		}
		// A first-class shape value is a concrete runtime kind and takes
		// part in disjointness like any other arm.
		arms = append(arms, ty)
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

func typeArmPairDisjoint(x, y *TypeExpr, resolve namedTypeResolver) bool {
	// A first-class shape value satisfies no annotation but any (the
	// runtime has no kind that matches it), and overlaps another shape
	// value.
	if _, isShapeValue := shapeValuePayload(x); isShapeValue {
		return shapeValueArmDisjoint(y)
	}
	if _, isShapeValue := shapeValuePayload(y); isShapeValue {
		return shapeValueArmDisjoint(x)
	}
	kx, ky := x.Kind, y.Kind
	if kx == TypeAny || ky == TypeAny || kx == TypeUnknown || ky == TypeUnknown {
		return false
	}
	if kx == TypeEnum || ky == TypeEnum {
		return namedArmPairDisjoint(x, y, resolve)
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
		switch {
		case kx == TypeShape && ky == TypeShape:
			return shapeTypesDisjoint(x, y, resolve)
		case kx == TypeShape && ky == TypeHash:
			return shapeVsTypedHashDisjoint(x, y, resolve)
		case kx == TypeHash && ky == TypeShape:
			return shapeVsTypedHashDisjoint(y, x, resolve)
		}
		return false
	}
	if kx == TypeArray && ky == TypeArray {
		return literalArrayDisjoint(x, y, resolve) || literalArrayDisjoint(y, x, resolve)
	}
	return kx != ky
}

// namedArmPairDisjoint decides disjointness for arm pairs involving a named
// (enum, class, or module) annotation, mirroring runtime named normalization:
// an enum admits its own values and coercible symbols, a class admits its
// instances, and a module admits instances of classes that include it.
// Unresolved names stay conservatively compatible.
func namedArmPairDisjoint(x, y *TypeExpr, resolve namedTypeResolver) bool {
	if resolve == nil {
		return false
	}
	if x.Kind == TypeEnum && y.Kind == TypeEnum {
		mx, ok := resolve(x)
		if !ok {
			return false
		}
		my, ok := resolve(y)
		if !ok {
			return false
		}
		return namedMatchesDisjoint(mx, my)
	}
	named, other := x, y
	if named.Kind != TypeEnum {
		named, other = y, x
	}
	match, ok := resolve(named)
	if !ok {
		return false
	}
	if match.enum != nil {
		// Runtime enum normalization admits the enum's own values and
		// coercible symbols; every other kind is rejected
		// (normalizeEnumValueForDef).
		return other.Kind != TypeSymbol
	}
	// A class or module annotation admits only instances, which no plain
	// runtime kind produces (normalizeClassInstanceForDef).
	return true
}

// namedMatchesDisjoint compares two resolved named definitions. Plain classes
// have no inheritance, so distinct definitions never share an instance; a
// module stays compatible with classes that include it and with other modules
// (one class can include both).
func namedMatchesDisjoint(x, y namedTypeMatch) bool {
	if x.enum != nil || y.enum != nil {
		if x.enum != nil && y.enum != nil {
			return x.enum != y.enum
		}
		// An enum value is never a class instance and vice versa.
		return true
	}
	cx, cy := x.class, y.class
	if cx == nil || cy == nil || cx == cy {
		return false
	}
	switch {
	case cx.IsModule && cy.IsModule:
		return false
	case cx.IsModule:
		return !classIncludesModule(cy, cx.Name)
	case cy.IsModule:
		return !classIncludesModule(cx, cy.Name)
	default:
		return true
	}
}

func shapeValueArmDisjoint(other *TypeExpr) bool {
	if _, isShapeValue := shapeValuePayload(other); isShapeValue {
		return false
	}
	switch other.Kind {
	case TypeAny, TypeUnknown:
		return false
	}
	// Named annotations (TypeEnum) included: runtime named normalization
	// admits only enum values and class instances, never a shape value.
	return true
}

// shapeTypesDisjoint compares two exact shapes: differing key sets or any
// field pair with disjoint types means no value can satisfy both.
func shapeTypesDisjoint(x, y *TypeExpr, resolve namedTypeResolver) bool {
	if len(x.Shape) != len(y.Shape) {
		return true
	}
	for field, xField := range x.Shape {
		yField, ok := y.Shape[field]
		if !ok {
			return true
		}
		if typeExprsDisjoint(xField, yField, resolve) {
			return true
		}
	}
	return false
}
