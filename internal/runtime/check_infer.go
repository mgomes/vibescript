package runtime

import (
	"reflect"
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

type checkLocalValueFact struct {
	classNames []string
	callables  []*ScriptFunction
}

type checkClassValueFrame map[string]checkLocalValueFact

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

func cloneCheckClassValueFrame(frame checkClassValueFrame) checkClassValueFrame {
	if len(frame) == 0 {
		return nil
	}
	clone := make(checkClassValueFrame, len(frame))
	for name, fact := range frame {
		clone[name] = checkLocalValueFact{
			classNames: append([]string(nil), fact.classNames...),
			callables:  append([]*ScriptFunction(nil), fact.callables...),
		}
	}
	return clone
}

func (c *scriptChecker) localClassValueFor(name string) (string, bool) {
	fact, ok := c.localValueFactFor(name)
	if !ok || len(fact.classNames) != 1 || len(fact.callables) > 0 {
		return "", false
	}
	return fact.classNames[0], true
}

func (c *scriptChecker) localClassValuesFor(name string) ([]string, bool) {
	fact, ok := c.localValueFactFor(name)
	return fact.classNames, ok && len(fact.classNames) > 0 && len(fact.callables) == 0
}

func (c *scriptChecker) localValueFactFor(name string) (checkLocalValueFact, bool) {
	for i := len(c.localTypes) - 1; i >= 0; i-- {
		if _, tracked := c.localTypes[i][name]; !tracked {
			continue
		}
		fact, ok := c.localClassValues[i][name]
		return fact, ok
	}
	return checkLocalValueFact{}, false
}

func (c *scriptChecker) bindLocalClassValue(name, className string) {
	if className == "" {
		c.bindLocalClassValues(name, nil)
		return
	}
	c.bindLocalClassValues(name, []string{className})
}

func (c *scriptChecker) bindLocalClassValues(name string, classNames []string) {
	if name == "" || len(c.localTypes) == 0 {
		return
	}
	for i := len(c.localTypes) - 1; i >= 0; i-- {
		if _, tracked := c.localTypes[i][name]; !tracked {
			continue
		}
		if len(classNames) == 0 {
			delete(c.localClassValues[i], name)
			return
		}
		if c.localClassValues[i] == nil {
			c.localClassValues[i] = make(checkClassValueFrame)
		}
		c.localClassValues[i][name] = checkLocalValueFact{classNames: normalizeCheckClassNames(classNames)}
		return
	}
}

func (c *scriptChecker) localCallableValueFor(name string) (*ScriptFunction, bool) {
	fact, ok := c.localValueFactFor(name)
	if !ok || len(fact.callables) != 1 || len(fact.classNames) > 0 {
		return nil, false
	}
	return fact.callables[0], true
}

func (c *scriptChecker) localCallableValuesFor(name string) ([]*ScriptFunction, bool) {
	fact, ok := c.localValueFactFor(name)
	return fact.callables, ok && len(fact.callables) > 0 && len(fact.classNames) == 0
}

func (c *scriptChecker) bindLocalCallableValues(name string, fns []*ScriptFunction) {
	if name == "" || len(c.localTypes) == 0 {
		return
	}
	for i := len(c.localTypes) - 1; i >= 0; i-- {
		if _, tracked := c.localTypes[i][name]; !tracked {
			continue
		}
		if len(fns) == 0 {
			delete(c.localClassValues[i], name)
			return
		}
		if c.localClassValues[i] == nil {
			c.localClassValues[i] = make(checkClassValueFrame)
		}
		c.localClassValues[i][name] = checkLocalValueFact{callables: normalizeCheckCallables(fns)}
		return
	}
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
	previousPinned := c.pinnedExpressionFacts
	c.typePoison = nil
	c.typeAliases = nil
	c.pinnedExpressionFacts = nil
	return func() {
		c.typePoison = previousPoison
		c.typeAliases = previousAliases
		c.pinnedExpressionFacts = previousPinned
	}
}

// withIsolatedLocalInference walls off the local type-fact environment so a
// module collection pass can run assignment inference without reading or
// corrupting the facts of whatever function walk is in flight.
func (c *scriptChecker) withIsolatedLocalInference() func() {
	previousTypes := c.localTypes
	previousClassValues := c.localClassValues
	previousLive := c.liveLocalNames
	previousDepth := c.mutationRegionDepth
	previousIsolated := c.isolatedCollectInference
	c.localTypes = nil
	c.localClassValues = nil
	c.liveLocalNames = nil
	c.mutationRegionDepth = 0
	c.isolatedCollectInference = true
	restoreScope := c.withFreshLocalInferenceScope()
	return func() {
		restoreScope()
		c.localTypes = previousTypes
		c.localClassValues = previousClassValues
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
	c.collectMutationCandidateRootsFromExpression(expr, &sites)
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
		c.bindLocalClassValue(name, "")
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
// Dispatches whose registered contracts preserve receiver facts are skipped
// with the same gate the walk-time poison uses; the caller applies the
// container-typed escape filter.
func (c *scriptChecker) collectMutationCandidateRoots(statements []Statement, out *[]Expression) {
	for _, stmt := range statements {
		switch typed := stmt.(type) {
		case nil:
		case *ReturnStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *RaiseStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Value, out)
			c.collectMutationCandidateRootsFromExpression(typed.Message, out)
		case *BreakStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *NextStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *AssignStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Target, out)
			c.collectMutationCandidateRootsFromExpression(typed.Value, out)
		case *ExprStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Expr, out)
		case *IfStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Condition, out)
			c.collectMutationCandidateRoots(typed.Consequent, out)
			for _, elseIf := range typed.ElseIf {
				c.collectMutationCandidateRootsFromExpression(elseIf.Condition, out)
				c.collectMutationCandidateRoots(elseIf.Consequent, out)
			}
			c.collectMutationCandidateRoots(typed.Alternate, out)
		case *ForStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Iterable, out)
			c.collectMutationCandidateRoots(typed.Body, out)
		case *WhileStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Condition, out)
			c.collectMutationCandidateRoots(typed.Body, out)
		case *UntilStmt:
			c.collectMutationCandidateRootsFromExpression(typed.Condition, out)
			c.collectMutationCandidateRoots(typed.Body, out)
		case *TryStmt:
			c.collectMutationCandidateRoots(typed.Body, out)
			for i := range typed.Rescues {
				c.collectMutationCandidateRoots(typed.Rescues[i].Body, out)
			}
			c.collectMutationCandidateRoots(typed.Else, out)
			c.collectMutationCandidateRoots(typed.Ensure, out)
		}
	}
}

func (c *scriptChecker) collectMutationCandidateRootsFromExpression(expr Expression, out *[]Expression) {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *RegexLiteral,
		*BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
	case *ArrayLiteral:
		for _, element := range typed.Elements {
			c.collectMutationCandidateRootsFromExpression(element, out)
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			c.collectMutationCandidateRootsFromExpression(pair.Key, out)
			c.collectMutationCandidateRootsFromExpression(pair.Value, out)
		}
	case *CallExpr:
		if member, ok := typed.Callee.(*MemberExpr); ok && c.memberCallPreservesReceiverFacts(typed) {
			// A dispatch proven to preserve receiver facts contributes no
			// mutation site of its own, matching the walk-time gate;
			// expressions nested inside the receiver still can.
			c.collectMutationCandidateRootsFromExpression(member.Object, out)
		} else {
			c.collectMutationCandidateRootsFromExpression(typed.Callee, out)
		}
		for _, arg := range typed.Args {
			*out = append(*out, arg)
			c.collectMutationCandidateRootsFromExpression(arg, out)
		}
		for _, kwarg := range typed.KwArgs {
			*out = append(*out, kwarg.Value)
			c.collectMutationCandidateRootsFromExpression(kwarg.Value, out)
		}
		c.collectMutationCandidateRootsFromExpression(typed.BlockArg, out)
		if typed.Block != nil {
			for _, param := range typed.Block.Params {
				c.collectMutationCandidateRootsFromExpression(param.DefaultVal, out)
			}
			c.collectMutationCandidateRoots(typed.Block.Body, out)
		}
	case *MemberExpr:
		if !c.memberDispatchPreservesReceiverFacts(typed) {
			*out = append(*out, typed.Object)
		}
		c.collectMutationCandidateRootsFromExpression(typed.Object, out)
	case *ScopeExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Object, out)
	case *IndexExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Object, out)
		for _, index := range typed.Indices {
			c.collectMutationCandidateRootsFromExpression(index, out)
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			c.collectMutationCandidateRootsFromExpression(element.Target, out)
		}
	case *SplatArg:
		c.collectMutationCandidateRootsFromExpression(typed.Value, out)
	case *TypeLiteral:
		if typed.Fallback != nil {
			c.collectMutationCandidateRootsFromExpression(typed.Fallback, out)
		}
	case *UnaryExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Right, out)
	case *BinaryExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Left, out)
		c.collectMutationCandidateRootsFromExpression(typed.Right, out)
	case *ConditionalExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Condition, out)
		c.collectMutationCandidateRootsFromExpression(typed.Consequent, out)
		c.collectMutationCandidateRootsFromExpression(typed.Alternate, out)
	case *RescueExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Body, out)
		c.collectMutationCandidateRootsFromExpression(typed.Fallback, out)
	case *IfExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Condition, out)
		c.collectMutationCandidateRootsFromExpression(typed.Consequent, out)
		for _, branch := range typed.ElseIf {
			c.collectMutationCandidateRootsFromExpression(branch.Condition, out)
			c.collectMutationCandidateRootsFromExpression(branch.Result, out)
		}
		c.collectMutationCandidateRootsFromExpression(typed.Alternate, out)
	case *RangeExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Start, out)
		c.collectMutationCandidateRootsFromExpression(typed.End, out)
	case *CaseExpr:
		c.collectMutationCandidateRootsFromExpression(typed.Target, out)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				c.collectMutationCandidateRootsFromExpression(value.Expr, out)
			}
			c.collectMutationCandidateRootsFromExpression(clause.Result, out)
		}
		c.collectMutationCandidateRootsFromExpression(typed.ElseExpr, out)
	case *BlockLiteral:
		for _, param := range typed.Params {
			c.collectMutationCandidateRootsFromExpression(param.DefaultVal, out)
		}
		c.collectMutationCandidateRoots(typed.Body, out)
	case *YieldExpr:
		for _, arg := range typed.Args {
			*out = append(*out, arg)
			c.collectMutationCandidateRootsFromExpression(arg, out)
		}
	case *InterpolatedString:
		for _, part := range typed.Parts {
			if exprPart, ok := part.(StringExpr); ok {
				c.collectMutationCandidateRootsFromExpression(exprPart.Expr, out)
			}
		}
	case *InterpolatedSymbol:
		for _, part := range typed.Parts {
			if exprPart, ok := part.(StringExpr); ok {
				c.collectMutationCandidateRootsFromExpression(exprPart.Expr, out)
			}
		}
	case *IfStmt, *ForStmt, *WhileStmt, *UntilStmt, *TryStmt:
		c.collectMutationCandidateRoots([]Statement{typed.(Statement)}, out)
	}
}

// degradeMutationCandidates clears container-typed locals a region may
// mutate through member dispatch or call arguments; scalar receivers keep
// their facts (immutable kinds cannot be mutated in place).
func (c *scriptChecker) degradeMutationCandidates(statements []Statement, names map[string]struct{}) {
	var sites []Expression
	c.collectMutationCandidateRoots(statements, &sites)
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
		c.bindLocalClassValue(name, "")
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

func (c *scriptChecker) snapshotLocalClassValues() []checkClassValueFrame {
	if len(c.localClassValues) == 0 {
		return nil
	}
	state := make([]checkClassValueFrame, len(c.localClassValues))
	for i, frame := range c.localClassValues {
		state[i] = cloneCheckClassValueFrame(frame)
	}
	return state
}

func (c *scriptChecker) restoreLocalClassValues(state []checkClassValueFrame) {
	if len(state) == 0 {
		c.localClassValues = nil
		return
	}
	c.localClassValues = make([]checkClassValueFrame, len(state))
	for i, frame := range state {
		c.localClassValues[i] = cloneCheckClassValueFrame(frame)
	}
}

func (c *scriptChecker) mergeLocalClassValueStates(states []checkScopeState) {
	if len(states) == 0 {
		return
	}
	for i := range c.localClassValues {
		if i >= len(states[0].classValues) {
			continue
		}
		common := cloneCheckClassValueFrame(states[0].classValues[i])
		for _, state := range states[1:] {
			if i >= len(state.classValues) {
				clear(common)
				break
			}
			for name, fact := range common {
				other, ok := state.classValues[i][name]
				if !ok {
					delete(common, name)
					continue
				}
				fact.classNames = normalizeCheckClassNames(append(fact.classNames, other.classNames...))
				fact.callables = normalizeCheckCallables(append(fact.callables, other.callables...))
				if len(fact.classNames) > 0 && len(fact.callables) > 0 {
					delete(common, name)
					continue
				}
				common[name] = fact
			}
		}
		c.localClassValues[i] = common
	}
}

func normalizeCheckClassNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	normalized := append([]string(nil), names...)
	sort.Strings(normalized)
	out := normalized[:0]
	for _, name := range normalized {
		if len(out) == 0 || out[len(out)-1] != name {
			out = append(out, name)
		}
	}
	return out
}

func normalizeCheckCallables(fns []*ScriptFunction) []*ScriptFunction {
	if len(fns) == 0 {
		return nil
	}
	normalized := append([]*ScriptFunction(nil), fns...)
	sort.Slice(normalized, func(i, j int) bool {
		return reflect.ValueOf(normalized[i]).Pointer() < reflect.ValueOf(normalized[j]).Pointer()
	})
	out := normalized[:0]
	for _, fn := range normalized {
		if len(out) == 0 || out[len(out)-1] != fn {
			out = append(out, fn)
		}
	}
	return out
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
func (c *scriptChecker) inferAssignStatementTypes(function string, stmt *AssignStmt) {
	switch target := stmt.Target.(type) {
	case *Identifier:
		current := c.localTypeFor(target.Name)
		next := c.inferExpressionType(stmt.Value)
		if stmt.Operator == tokenOrAssign || stmt.Operator == tokenAndAssign {
			c.bindLocalType(target.Name, logicalAssignmentFact(stmt.Operator, current, next))
			c.bindLogicalAssignmentClassValueFact(target.Name, stmt.Operator, current, stmt.Value)
			return
		}
		if stmt.Operator != "" {
			outcome := c.binaryOperationOutcome(stmt.Operator, current, next)
			if outcome.invalid {
				c.add(function, stmt.Pos(), "unsupported %s operands %s and %s",
					binaryOperatorNoun(stmt.Operator), formatTypeExpr(current), formatTypeExpr(next))
			}
			c.bindLocalType(target.Name, outcome.result)
			c.bindLocalClassValue(target.Name, "")
			return
		}
		if reassignmentConflicts(current, next, c.checkNamedTypeResolver()) {
			c.add(function, stmt.Pos(), "reassignment of %s expected %s, got %s",
				target.Name, formatTypeExpr(current), formatTypeExpr(next))
		}
		c.bindLocalType(target.Name, next)
		if classNames, ok := c.classValueExpressionNames(stmt.Value); ok {
			c.bindLocalClassValues(target.Name, classNames)
		} else if fns, ok := c.callableExpressionFunctions(stmt.Value); ok {
			c.bindLocalCallableValues(target.Name, fns)
		} else {
			c.bindLocalClassValue(target.Name, "")
		}
		switch stmt.Value.(type) {
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
		}
	case *DestructureTarget:
		for _, element := range target.Elements {
			c.bindDestructureElementType(element)
		}
		c.bindDestructureValueFacts(target, stmt.Value)
	case *IndexExpr, *MemberExpr:
		// An index or member write mutates the container in place, so any
		// structural fact about the root local (shape exactness in
		// particular) no longer holds.
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

// bindLogicalAssignmentClassValueFact mirrors logicalAssignmentFact for exact
// class objects, which are always truthy even though they have no TypeExpr.
func (c *scriptChecker) bindLogicalAssignmentClassValueFact(
	name string,
	operator TokenType,
	currentType *TypeExpr,
	next Expression,
) {
	currentFact, currentTracked := c.localValueFactFor(name)
	currentExact := currentTracked && len(currentFact.classNames) > 0 && len(currentFact.callables) == 0
	bindNext := func() {
		if classNames, ok := c.classValueExpressionNames(next); ok {
			c.bindLocalClassValues(name, classNames)
			return
		}
		c.bindLocalClassValue(name, "")
	}

	switch operator {
	case tokenOrAssign:
		if currentExact {
			c.bindLocalClassValues(name, currentFact.classNames)
			return
		}
		if typeExprIsNilOnly(currentType) {
			bindNext()
			return
		}
	case tokenAndAssign:
		if currentExact || typeExprDefinitelyTruthy(currentType) {
			bindNext()
			return
		}
		if typeExprIsNilOnly(currentType) {
			c.bindLocalClassValue(name, "")
			return
		}
	}
	c.bindLocalClassValue(name, "")
}

func (c *scriptChecker) bindDestructureElementType(element DestructureElement) {
	switch target := element.Target.(type) {
	case *Identifier:
		c.bindLocalType(target.Name, element.Type)
		c.bindLocalClassValue(target.Name, "")
	case *DestructureTarget:
		for _, nested := range target.Elements {
			c.bindDestructureElementType(nested)
		}
	}
}

func (c *scriptChecker) bindDestructureValueFacts(target *DestructureTarget, value Expression) {
	if target == nil {
		return
	}
	array, ok := value.(*ArrayLiteral)
	if !ok {
		return
	}
	for _, expression := range array.Elements {
		if _, splat := expression.(*SplatArg); splat {
			return
		}
	}
	valueIndex := 0
	for _, element := range target.Elements {
		if element.Rest || valueIndex >= len(array.Elements) {
			return
		}
		c.bindDestructureElementValueFact(element, array.Elements[valueIndex])
		valueIndex++
	}
}

func (c *scriptChecker) bindDestructureElementValueFact(element DestructureElement, value Expression) {
	switch target := element.Target.(type) {
	case *Identifier:
		if classNames, ok := c.classValueExpressionNames(value); ok {
			c.bindLocalClassValues(target.Name, classNames)
		} else if fns, ok := c.callableExpressionFunctions(value); ok {
			c.bindLocalCallableValues(target.Name, fns)
		}
	case *DestructureTarget:
		c.bindDestructureValueFacts(target, value)
	}
}

// classValueExpressionNames returns the exhaustive script-class identities an
// expression can produce. It recognizes value-preserving branches and literal
// projections so dynamic dispatch can schedule only methods the receiver can
// actually reach; an incomplete result is rejected instead of widening to all
// same-named methods and hiding their independent pristine checks.
func (c *scriptChecker) classValueExpressionNames(expr Expression) ([]string, bool) {
	c.speculativeInference++
	defer func() { c.speculativeInference-- }()
	return c.classValueExpressionNamesSeen(expr, nil, false)
}

func (c *scriptChecker) dispatchClassValueExpressionNames(expr Expression) ([]string, bool) {
	c.speculativeInference++
	defer func() { c.speculativeInference-- }()
	return c.classValueExpressionNamesSeen(expr, nil, true)
}

func (c *scriptChecker) classValueExpressionNamesSeen(
	expr Expression,
	seen map[*ScriptFunction]struct{},
	allowFunctionReturns bool,
) ([]string, bool) {
	switch typed := expr.(type) {
	case *Identifier:
		if classNames, ok := c.localClassValuesFor(typed.Name); ok {
			return classNames, true
		}
		if typed.Name == "self" && c.selfClass != nil && c.selfClassContext {
			return []string{c.selfClass.Name}, true
		}
		classDef, ok := c.staticClassArgument(typed)
		if !ok {
			return nil, false
		}
		return []string{classDef.Name}, true
	case *ConditionalExpr:
		if branch, ok := staticConditionalExpressionBranch(typed); ok {
			return c.classValueExpressionNamesSeen(branch, seen, allowFunctionReturns)
		}
		left, leftOK := c.classValueExpressionNamesSeen(typed.Consequent, seen, allowFunctionReturns)
		right, rightOK := c.classValueExpressionNamesSeen(typed.Alternate, seen, allowFunctionReturns)
		return mergeCheckStringCandidates(left, leftOK, right, rightOK)
	case *IfExpr:
		branches := make([]Expression, 0, len(typed.ElseIf)+2)
		branches = append(branches, typed.Consequent)
		for _, branch := range typed.ElseIf {
			branches = append(branches, branch.Result)
		}
		branches = append(branches, typed.Alternate)
		return c.mergeClassValueExpressionCandidates(branches, seen, allowFunctionReturns)
	case *RescueExpr:
		body, bodyOK := c.classValueExpressionNamesSeen(typed.Body, seen, allowFunctionReturns)
		fallback, fallbackOK := c.classValueExpressionNamesSeen(typed.Fallback, seen, allowFunctionReturns)
		return mergeCheckStringCandidates(body, bodyOK, fallback, fallbackOK)
	case *BinaryExpr:
		if typed.Operator != tokenAnd && typed.Operator != tokenOr {
			return nil, false
		}
		if truthy, known := staticExpressionTruthiness(typed.Left); known {
			if truthy == (typed.Operator == tokenAnd) {
				return c.classValueExpressionNamesSeen(typed.Right, seen, allowFunctionReturns)
			}
			return c.classValueExpressionNamesSeen(typed.Left, seen, allowFunctionReturns)
		}
		if left, ok := c.classValueExpressionNamesSeen(typed.Left, seen, allowFunctionReturns); ok {
			if typed.Operator == tokenOr {
				return left, true
			}
			return c.classValueExpressionNamesSeen(typed.Right, seen, allowFunctionReturns)
		}
		return nil, false
	case *IndexExpr:
		projected, ok := c.staticLiteralProjection(typed)
		if !ok {
			return nil, false
		}
		return c.classValueExpressionNamesSeen(projected, seen, allowFunctionReturns)
	case *CallExpr:
		if member, ok := typed.Callee.(*MemberExpr); ok && member.Property == "itself" &&
			len(typed.Args) == 0 && len(typed.KwArgs) == 0 && typed.Block == nil && typed.BlockArg == nil {
			candidates, exact := c.classValueExpressionNamesSeen(member.Object, seen, allowFunctionReturns)
			if !exact {
				return nil, false
			}
			for _, className := range candidates {
				classDef := c.script.classes[className]
				if classDef == nil || classDef.ClassMethods["itself"] != nil {
					return nil, false
				}
			}
			return candidates, true
		}
		if !allowFunctionReturns {
			return nil, false
		}
		target, ok := c.resolveCallable(typed)
		if !ok || target.fn == nil || target.constructor || len(target.fn.Body) != 1 {
			return nil, false
		}
		if seen == nil {
			seen = make(map[*ScriptFunction]struct{})
		}
		if _, recursive := seen[target.fn]; recursive {
			return nil, false
		}
		seen[target.fn] = struct{}{}
		defer delete(seen, target.fn)
		switch stmt := target.fn.Body[0].(type) {
		case *ExprStmt:
			return c.classValueExpressionNamesSeen(stmt.Expr, seen, true)
		case *ReturnStmt:
			return c.classValueExpressionNamesSeen(stmt.Value, seen, true)
		}
	}
	return nil, false
}

func (c *scriptChecker) mergeClassValueExpressionCandidates(
	branches []Expression,
	seen map[*ScriptFunction]struct{},
	allowFunctionReturns bool,
) ([]string, bool) {
	var merged []string
	for _, branch := range branches {
		candidates, ok := c.classValueExpressionNamesSeen(branch, seen, allowFunctionReturns)
		if !ok {
			return nil, false
		}
		if merged == nil {
			merged = candidates
			continue
		}
		merged, _ = mergeCheckStringCandidates(merged, true, candidates, true)
	}
	return merged, len(merged) > 0
}

func mergeCheckStringCandidates(left []string, leftOK bool, right []string, rightOK bool) ([]string, bool) {
	if !leftOK || !rightOK || len(left) == 0 || len(right) == 0 {
		return nil, false
	}
	merged := append([]string(nil), left...)
	seen := make(map[string]struct{}, len(left)+len(right))
	for _, candidate := range left {
		seen[candidate] = struct{}{}
	}
	for _, candidate := range right {
		if _, duplicate := seen[candidate]; duplicate {
			continue
		}
		seen[candidate] = struct{}{}
		merged = append(merged, candidate)
	}
	return normalizeCheckClassNames(merged), true
}

func (c *scriptChecker) staticLiteralProjection(expr *IndexExpr) (Expression, bool) {
	if expr == nil || len(expr.Indices) != 1 {
		return nil, false
	}
	switch object := expr.Object.(type) {
	case *ArrayLiteral:
		for _, element := range object.Elements {
			if _, splat := element.(*SplatArg); splat {
				return nil, false
			}
		}
		value, ok := staticLiteralValue(expr.Indices[0])
		if !ok {
			return nil, false
		}
		index, ok := staticArrayFetchIndex(value)
		if !ok {
			return nil, false
		}
		if index < 0 {
			index += int64(len(object.Elements))
		}
		if index < 0 || index >= int64(len(object.Elements)) {
			return nil, false
		}
		return object.Elements[index], true
	case *HashLiteral:
		if object.ShapeType != nil && !c.hashShapeStaticallyShadowed(object) {
			return nil, false
		}
		want, ok := staticLiteralHashKey(expr.Indices[0])
		if !ok {
			return nil, false
		}
		var projected Expression
		for _, pair := range object.Pairs {
			key, ok := staticLiteralHashKey(pair.Key)
			if !ok {
				return nil, false
			}
			if key == want {
				projected = pair.Value
			}
		}
		return projected, projected != nil
	}
	return nil, false
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

// narrowClassPredicateMember narrows `x.is_a?(User)`, `x.kind_of?(Mod)`, and
// `x.instance_of?(User)` on a plain local against a statically resolved class
// or module argument. Both branches refine exactly: without inheritance an
// instance arm either always or never satisfies the predicate, so the true
// path keeps matching arms and the false path drops them. Narrowing applies
// only when every arm provably reaches the runtime universal predicate —
// named arms whose class overrides it, module-typed arms (their concrete
// class is unknown), and dynamic receivers stay unchanged.
func (c *scriptChecker) narrowClassPredicateMember(member *MemberExpr, arg Expression, truthy bool) bool {
	if member == nil || member.Safe {
		return true
	}
	exact := false
	switch member.Property {
	case isAMemberName, kindOfMemberName:
	case instanceOfMemberName:
		exact = true
	default:
		return true
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return true
	}
	want, ok := c.staticClassArgument(arg)
	if !ok {
		return true
	}
	arms, ok := typeExprArms(c.localTypeFor(ident.Name), 0)
	if !ok || len(arms) == 0 {
		return true
	}
	resolve := c.checkNamedTypeResolver()
	for _, arm := range arms {
		if !classPredicateArmUsesUniversalDispatch(arm, member.Property, resolve) {
			return true
		}
		if _, known := classPredicateArmMatch(arm, want, exact, resolve); !known {
			return true
		}
	}
	matches := func(arm *TypeExpr) bool {
		m, _ := classPredicateArmMatch(arm, want, exact, resolve)
		return m
	}
	if truthy {
		return c.narrowLocalArms(ident.Name, matches)
	}
	return c.narrowLocalArms(ident.Name, func(arm *TypeExpr) bool { return !matches(arm) })
}

// staticClassArgument resolves a predicate argument to the script class or
// module it names. Shadowed names, dynamic expressions, and names self's
// class may bind first (the runtime checks class constants before the
// top-level binding), including through a prior external member write, stay
// unknown.
func (c *scriptChecker) staticClassArgument(arg Expression) (*ClassDef, bool) {
	ident, ok := arg.(*Identifier)
	if !ok {
		return nil, false
	}
	if className, ok := c.localClassValueFor(ident.Name); ok {
		classDef, exists := c.script.classes[className]
		return classDef, exists && classDef != nil
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		return nil, false
	}
	if c.liveLocalNameHas(ident.Name) {
		// A partial-path assignment post-predeclares the name as a call
		// local (possibly nil), so the runtime reads the local — not the
		// class — even on paths that never assigned it.
		return nil, false
	}
	if c.selfClass != nil {
		if c.opaqueClassConstants || c.classConstantContext.opaque ||
			c.namespaceMemberMutated(c.selfClass.Name, ident.Name) ||
			c.selfClassMayBindConstant(c.selfClass, ident.Name) {
			return nil, false
		}
	}
	classDef, ok := c.script.classes[ident.Name]
	if !ok || classDef == nil {
		return nil, false
	}
	if val, bound := checkRootBinding(c.runtimeTypeRoot, ident.Name); bound {
		if val.Kind() != KindClass {
			return nil, false
		}
		runtimeClass := valueClass(val)
		if runtimeClass == nil || runtimeClass.Name != classDef.Name || runtimeClass.owner != classDef.owner {
			return nil, false
		}
	}
	return classDef, true
}

// selfClassMayBindConstant reports whether self's class can supply name
// ahead of the top-level binding: a class variable, a nested module, a
// class-body assignment that creates the variable when the body runs, or a
// constant adopted from an included module.
func (c *scriptChecker) selfClassMayBindConstant(cl *ClassDef, name string) bool {
	if cl == nil {
		return false
	}
	if _, ok := cl.ClassVars[name]; ok {
		return true
	}
	for _, nested := range cl.NestedModules {
		if nested == name {
			return true
		}
	}
	if classDefAssignsName(cl, name) {
		return true
	}
	// IncludedModules is the flattened transitive closure, so one level of
	// adopted-constant lookup covers every reachable module.
	for _, included := range cl.IncludedModules {
		moduleDef, ok := c.script.classes[included]
		if !ok || moduleDef == nil {
			continue
		}
		if _, ok := moduleDef.ClassVars[name]; ok {
			return true
		}
		for _, nested := range moduleDef.NestedModules {
			if nested == name {
				return true
			}
		}
		if classDefAssignsName(moduleDef, name) {
			return true
		}
	}
	return false
}

// classDefAssignsName reports whether the class body or one of its methods
// contains an assignment whose target can bind name on the class. Bare class
// body assignments, class-variable writes, and writes through the class value
// qualify; call-local and unrelated-receiver assignments do not. The walk is
// reflective so destructuring targets and future statement forms stay covered.
func classDefAssignsName(cl *ClassDef, name string) bool {
	if astAssignsClassName(cl.Body, cl, name, true, true) {
		return true
	}
	for _, fn := range cl.Methods {
		if fn != nil && astAssignsClassName(fn.Body, cl, name, false, false) {
			return true
		}
	}
	for _, fn := range cl.ClassMethods {
		if fn != nil && astAssignsClassName(fn.Body, cl, name, false, true) {
			return true
		}
	}
	return false
}

// astAssignsClassName walks assignment targets that can mutate cl's class
// constant. Bare identifiers do so only in the class body; call scopes bind
// them as locals. Explicit self writes require class-valued self, and writes
// through another class never affect cl.
func astAssignsClassName(root any, cl *ClassDef, name string, classBody, classSelf bool) bool {
	found := false
	walkASTValue(reflect.ValueOf(root), 0, func(node any) {
		assign, ok := node.(*AssignStmt)
		if !ok || found {
			return
		}
		walkASTValue(reflect.ValueOf(assign.Target), 0, func(target any) {
			switch typed := target.(type) {
			case *Identifier:
				if classBody && typed.Name == name {
					found = true
				}
			case *ClassVarExpr:
				if typed.Name == name {
					found = true
				}
			case *MemberExpr:
				if typed.Property != name || classMemberAssignmentIntercepted(cl, name) {
					return
				}
				ident, ok := typed.Object.(*Identifier)
				if !ok {
					return
				}
				if ident.Name == cl.Name || (ident.Name == "self" && classSelf) {
					found = true
				}
			}
		})
	})
	return found
}

func classMemberAssignmentIntercepted(cl *ClassDef, name string) bool {
	if cl == nil {
		return false
	}
	_, hasSetter := cl.ClassMethods[name+"="]
	_, hasGetter := cl.ClassMethods[name]
	return hasSetter || hasGetter
}

const maxASTWalkDepth = 200

// walkASTValue reflectively visits every node reachable from v, invoking
// visit for each addressable struct pointer. The AST is acyclic; the depth
// cap guards pathological inputs.
func walkASTValue(v reflect.Value, depth int, visit func(any)) {
	if depth > maxASTWalkDepth || !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		if v.Kind() == reflect.Pointer && v.Elem().Kind() == reflect.Struct && v.CanInterface() {
			visit(v.Interface())
		}
		walkASTValue(v.Elem(), depth+1, visit)
	case reflect.Struct:
		for i := range v.NumField() {
			field := v.Field(i)
			if !field.CanInterface() {
				continue
			}
			walkASTValue(field, depth+1, visit)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkASTValue(v.Index(i), depth+1, visit)
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			walkASTValue(v.MapIndex(key), depth+1, visit)
		}
	}
}

// classPredicateArmUsesUniversalDispatch reports whether a known fact arm must
// reach the universal class predicate. A named arm qualifies when it resolves
// to an enum (no user methods) or to a non-module class without its own
// override; module-typed arms stay conservative because their concrete class
// is unknown. Plain data arms defer to the shared dispatch proof.
func classPredicateArmUsesUniversalDispatch(arm *TypeExpr, property string, resolve namedTypeResolver) bool {
	if arm == nil {
		return false
	}
	if arm.Kind == TypeEnum {
		match, ok := resolve(arm)
		if !ok {
			return false
		}
		if match.enum != nil {
			return true
		}
		if match.class == nil || match.class.IsModule {
			return false
		}
		_, overridden := match.class.Methods[property]
		return !overridden
	}
	return typeArmUsesUniversalMemberDispatch(arm, property)
}

// classPredicateArmMatch reports how a value of the arm answers the
// predicate. The runtime compares class definitions by identity, so the
// positive direction requires the arm to resolve to the argument's own
// definition; module membership mirrors the runtime's name-based include
// walk. A same-named but distinct definition leaves identity unproven —
// known=false — and the caller must not narrow.
func classPredicateArmMatch(arm *TypeExpr, want *ClassDef, exact bool, resolve namedTypeResolver) (matches, known bool) {
	if arm == nil || arm.Kind != TypeEnum {
		return false, true
	}
	match, ok := resolve(arm)
	if !ok {
		return false, false
	}
	if match.enum != nil {
		// Enum values are never class instances.
		return false, true
	}
	if match.class == nil {
		return false, false
	}
	if classDefsIdentical(match.class, want) {
		return true, true
	}
	if !exact && want.IsModule && classIncludesModule(match.class, want.Name) {
		return true, true
	}
	if match.class.Name == want.Name {
		return false, false
	}
	return false, true
}

// classDefsIdentical mirrors enumDefsEqual for classes: definition clones
// keep their owner script, so identity survives cloning while same-named
// definitions from different scripts stay distinct.
func classDefsIdentical(left, right *ClassDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil || left.Name != right.Name {
		return false
	}
	return left.owner != nil && left.owner == right.owner
}

// narrowIsTypePredicateMember narrows `x.is_type?(:atom)` on a plain local
// with a literal builtin atom. Both branches refine: the true path keeps arms
// that may satisfy the atom, the false path drops arms that always satisfy
// it. Named atoms, non-literal atoms, and receivers without proven universal
// dispatch stay unchanged.
func (c *scriptChecker) narrowIsTypePredicateMember(member *MemberExpr, arg Expression, truthy bool) bool {
	if member == nil || member.Safe || member.Property != isTypeMemberName {
		return true
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return true
	}
	if c.memberDispatchEffect(member) != effectPure {
		return true
	}
	atomValue, ok := staticLiteralValue(arg)
	if !ok {
		return true
	}
	text, ok := typeAtomArg(atomValue)
	if !ok {
		return true
	}
	atom, err := parseTypeAtom(text)
	if err != nil || atom.Kind == TypeEnum {
		return true
	}
	if truthy {
		return c.narrowLocalArms(ident.Name, func(arm *TypeExpr) bool {
			may, _ := typeArmAtomMatch(arm, atom)
			return may
		})
	}
	return c.narrowLocalArms(ident.Name, func(arm *TypeExpr) bool {
		_, must := typeArmAtomMatch(arm, atom)
		return !must
	})
}

// typeArmAtomMatch reports whether a known fact arm may and must satisfy a
// builtin is_type? atom. Arms reaching this point passed the universal
// dispatch proof, so only plain data kinds appear. A number arm may hold an
// int or a float, so it may — but does not have to — satisfy either atom.
func typeArmAtomMatch(arm, atom *TypeExpr) (may, must bool) {
	if atom.Nullable {
		if arm.Kind == TypeNil {
			return true, true
		}
		bare := *atom
		bare.Nullable = false
		return typeArmAtomMatch(arm, &bare)
	}
	switch atom.Kind {
	case TypeNumber:
		switch arm.Kind {
		case TypeInt, TypeFloat, TypeNumber:
			return true, true
		}
		return false, false
	case TypeInt, TypeFloat:
		if arm.Kind == atom.Kind {
			return true, true
		}
		if arm.Kind == TypeNumber {
			return true, false
		}
		return false, false
	case TypeHash:
		// Hash atoms cover hash and object receivers, and shape facts
		// describe hash-shaped data.
		switch arm.Kind {
		case TypeHash, TypeShape:
			return true, true
		}
		return false, false
	default:
		exact := arm.Kind == atom.Kind
		return exact, exact
	}
}

// memberDispatchEffect resolves the registered receiver effect of a member
// dispatch from the receiver's known arms: effectPure only when every arm
// that can dispatch proves a pure registered contract, effectMutatesReceiver
// when every arm resolves and at least one is a registered mutator, and
// effectUnknown otherwise. Named receivers (user overrides may shadow any
// member), unregistered members, and unknown arms all stay unknown, so
// dynamic dispatch keeps its conservative treatment. Safe navigation skips
// nil arms: a nil receiver skips the dispatch entirely.
func (c *scriptChecker) memberDispatchEffect(member *MemberExpr) memberEffect {
	if member == nil {
		return effectUnknown
	}
	arms, ok := typeExprArms(c.inferExpressionType(member.Object), 0)
	if !ok || len(arms) == 0 {
		return effectUnknown
	}
	combined := effectPure
	dispatchArms := 0
	for _, arm := range arms {
		if member.Safe && arm.Kind == TypeNil {
			continue
		}
		combined = combineMemberEffects(combined, c.typeArmMemberEffect(arm, member.Property))
		if combined == effectUnknown {
			return effectUnknown
		}
		dispatchArms++
	}
	if dispatchArms == 0 {
		return effectUnknown
	}
	return combined
}

// typeArmMemberEffect resolves the registered effect a member dispatch has
// on one known receiver arm, mirroring runtime dispatch order: a typed
// contract wins over the universal fallback, a kind's own unregistered
// member shadows the universal helper with an unknown effect, and hash-like
// arms qualify only when no stored callable can shadow the helper.
func (c *scriptChecker) typeArmMemberEffect(arm *TypeExpr, property string) memberEffect {
	if arm == nil {
		return effectUnknown
	}
	switch arm.Kind {
	case TypeHash, TypeShape:
		if !typeArmUsesUniversalMemberDispatch(arm, property) {
			return effectUnknown
		}
		if effect, ok := universalMemberEffects[property]; ok {
			return effect
		}
		return effectUnknown
	case TypeNumber:
		return combineMemberEffects(kindMemberEffect("int", property), kindMemberEffect("float", property))
	case TypeEnum:
		// A nominal arm dispatches a class predicate through the pure
		// universal contract when its class provably lacks an override, so
		// mixed nominal and container unions keep their receiver facts for
		// the narrowing that follows.
		switch property {
		case isAMemberName, kindOfMemberName, instanceOfMemberName:
			if classPredicateArmUsesUniversalDispatch(arm, property, c.checkNamedTypeResolver()) {
				if effect, ok := universalMemberEffects[property]; ok {
					return effect
				}
			}
		}
		return effectUnknown
	case TypeAny, TypeUnknown, TypeUnion:
		return effectUnknown
	}
	kind, ok := receiverKindForTypeArm(arm)
	if !ok {
		return effectUnknown
	}
	return kindMemberEffect(kind, property)
}

// kindMemberEffect resolves the registered effect of a member on one fixed
// receiver kind. A registered typed contract answers directly; a member the
// kind dispatches itself without a contract stays unknown even when a
// universal helper shares its name; otherwise the universal contract's
// effect applies.
func kindMemberEffect(kind, property string) memberEffect {
	if effect, ok := staticMemberEffects[kind+"."+property]; ok {
		return effect
	}
	if memberKindOwns(kind, property) {
		return effectUnknown
	}
	if effect, ok := universalMemberEffects[property]; ok {
		return effect
	}
	return effectUnknown
}

// combineMemberEffects joins the effects of two possible dispatches: any
// unknown side stays unknown, a mutating side dominates a pure one.
func combineMemberEffects(a, b memberEffect) memberEffect {
	if a == effectUnknown || b == effectUnknown {
		return effectUnknown
	}
	if a == effectMutatesReceiver || b == effectMutatesReceiver {
		return effectMutatesReceiver
	}
	return effectPure
}

// memberDispatchPreservesReceiverFacts reports whether a member dispatch is
// proven pure by its registered contracts and cannot hand the caller a
// mutable alias into the receiver's interior. Purity alone is not enough to
// keep the receiver's facts: a pure read like at on array<array<int>>
// returns a nested container the caller can mutate through a chained call
// (`a.at(0).push("x")`), and that receiver spelling is not an identifier
// projection escapePoisonTarget could trace back to the root, so the deep
// fact would silently go stale.
func (c *scriptChecker) memberDispatchPreservesReceiverFacts(member *MemberExpr) bool {
	if c.memberDispatchEffect(member) != effectPure {
		return false
	}
	arms, ok := typeExprArms(c.inferExpressionType(member.Object), 0)
	if !ok || len(arms) == 0 {
		return false
	}
	for _, arm := range arms {
		if member.Safe && arm.Kind == TypeNil {
			continue
		}
		if typeArmMemberResultMayAliasInterior(arm, member.Property) {
			return false
		}
	}
	return true
}

// typeArmMemberResultMayAliasInterior reports whether the member's result on
// one receiver arm may still reach into the receiver. An arm whose interior
// provably holds neither mutable containers nor callables has nothing to
// hand out; otherwise only a declared result that can never be a mutable
// container or a callable (predicates and conversions return fresh scalars)
// proves the call does not leak an interior reference.
func typeArmMemberResultMayAliasInterior(arm *TypeExpr, property string) bool {
	if arm == nil {
		return true
	}
	if !typeArmInteriorMayEscape(arm) {
		return false
	}
	result, known := typeArmMemberResultType(arm, property)
	return !known || result == nil || typeExprMayEscapeReceiverInterior(result)
}

// typeArmMemberResultType resolves the declared result type of the
// registered contract governing a member dispatch on one receiver arm,
// mirroring the dispatch order of kindMemberEffect: typed contracts win, a
// kind's own unregistered member stays unknown, then the universal
// contract answers.
func typeArmMemberResultType(arm *TypeExpr, property string) (*TypeExpr, bool) {
	switch arm.Kind {
	case TypeHash, TypeShape:
		if spec, ok := universalMemberSpecs[property]; ok {
			return spec.resultType, true
		}
		return nil, false
	}
	kind, ok := receiverKindForTypeArm(arm)
	if !ok {
		return nil, false
	}
	if spec, ok := staticMemberSpecs[kind+"."+property]; ok {
		return spec.resultType, true
	}
	if valueType, ok := staticMemberValueTypes[kind+"."+property]; ok {
		return valueType, true
	}
	if memberKindOwns(kind, property) {
		return nil, false
	}
	if spec, ok := universalMemberSpecs[property]; ok {
		return spec.resultType, true
	}
	return nil, false
}

// typeArmInteriorMayEscape reports whether values stored inside one
// receiver arm could reach back into it after being read out: a nested
// mutable container aliases its storage directly, and a stored callable
// can close over the receiver and mutate it when invoked later.
func typeArmInteriorMayEscape(arm *TypeExpr) bool {
	switch arm.Kind {
	case TypeArray:
		if len(arm.TypeArgs) != 1 {
			return true
		}
		return typeExprMayEscapeReceiverInterior(arm.TypeArgs[0])
	case TypeHash:
		if len(arm.TypeArgs) != 2 {
			return true
		}
		return typeExprMayEscapeReceiverInterior(arm.TypeArgs[1])
	case TypeShape:
		for _, field := range arm.Shape {
			if typeExprMayEscapeReceiverInterior(field) {
				return true
			}
		}
		return false
	}
	// Scalar receivers store no mutable interior.
	return false
}

// typeExprMayEscapeReceiverInterior reports whether a value of this type,
// handed out of a receiver's interior, could still reach the receiver: a
// mutable container aliases its storage, and a callable can run user code
// that mutates the receiver through a captured alias. Unknown types stay
// conservative.
func typeExprMayEscapeReceiverInterior(ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if typeExprMayIncludeCallable(ty) {
		return true
	}
	arms, ok := typeExprArms(ty, 0)
	if !ok || len(arms) == 0 {
		return true
	}
	for _, arm := range arms {
		if arm == nil {
			return true
		}
		switch arm.Kind {
		case TypeArray, TypeHash, TypeShape, TypeAny, TypeUnknown:
			return true
		}
	}
	return false
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
		if !present {
			// An open shape may hold an undeclared callable export with the
			// same name, so only a closed shape rules the override out.
			return !arm.Open
		}
		return !typeExprMayIncludeCallable(field)
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

// memberCallPreservesReceiverFacts recognizes a call whose member dispatch
// preserves the receiver's facts (a registered pure contract that cannot
// alias the receiver's interior) and whose arguments provably run no user
// code. Arguments evaluate before the member dispatches, so an argument
// that can call into script code may mutate the receiver through an alias,
// and a block runs user code during the dispatch itself — the receiver's
// fact must not survive either.
func (c *scriptChecker) memberCallPreservesReceiverFacts(call *CallExpr) bool {
	if call == nil || len(call.KwArgs) > 0 || call.Block != nil || call.BlockArg != nil {
		return false
	}
	for _, arg := range call.Args {
		if !c.pureCallArgument(arg) {
			return false
		}
	}
	member, ok := call.Callee.(*MemberExpr)
	return ok && c.memberDispatchPreservesReceiverFacts(member)
}

// pureCallArgument reports whether a call argument provably runs no user
// code when it evaluates: literals and plain non-callable reads qualify. An
// identifier stays pure only when it cannot auto-invoke a callable —
// neither as a resolved zero-arity function or builtin, nor as a local
// whose value may itself be callable (a stored zero-arity function
// auto-invokes when the argument evaluates).
func (c *scriptChecker) pureCallArgument(expr Expression) bool {
	switch typed := expr.(type) {
	case *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral,
		*NilLiteral, *SymbolLiteral:
		return true
	case *Identifier:
		if _, autoCallable := c.resolveCallable(&CallExpr{Callee: typed}); autoCallable {
			return false
		}
		if _, ok := c.staticClassArgument(typed); ok {
			// A bare class or module reference evaluates to the definition
			// value without running any code.
			return true
		}
		return !typeExprMayIncludeCallable(c.inferExpressionType(typed))
	case *UnaryExpr:
		return c.pureCallArgument(typed.Right)
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
	// A pinned node keeps the fact captured at its own walk: a call whose
	// callee mutates a builtin namespace dispatched under the pre-mutation
	// bindings, so its result must not recompute under the context its own
	// write markers created.
	if fact, ok := c.pinnedExpressionFacts[expr]; ok {
		return fact
	}
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
	case *TypeLiteral:
		if c.typeLiteralStaticallyShadowed(typed) {
			return c.inferExpressionType(typed.Fallback)
		}
		return shapeValueType(typed.Type)
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

// inferExpressionTypeWithExpectation mirrors the runtime's typed argument
// evaluation. A callable expectation turns a bare bound method into a
// function value, and branch/container expectations flow to the expression
// that actually produces the value.
func (c *scriptChecker) inferExpressionTypeWithExpectation(expr Expression, expectation expressionExpectation) *TypeExpr {
	if expectation.empty() {
		return c.inferExpressionType(expr)
	}
	if expectation.includesCallable() {
		if _, ok := c.bareIdentifierCallableArgument(expr); ok {
			return checkTypeFunction
		}
		if callableFact, ok := c.bareMemberArgumentCallableFact(expr); ok {
			return callableFact
		}
	}
	switch typed := expr.(type) {
	case *ConditionalExpr:
		return c.inferConditionalExpressionTypeWithExpectation(typed, expectation)
	case *IfExpr:
		return c.inferIfExpressionTypeWithExpectation(typed, expectation)
	case *CaseExpr:
		branches := make([]Expression, 0, len(typed.Clauses)+1)
		for _, clause := range typed.Clauses {
			branches = append(branches, clause.Result)
		}
		branches = append(branches, typed.ElseExpr)
		return c.inferExpectedBranchUnion(expectation, branches...)
	case *RescueExpr:
		return c.inferExpectedBranchUnion(autoCallExpectation(!expectation.includesCallable()), typed.Body, typed.Fallback)
	case *ArrayLiteral:
		return c.inferExpectedArrayLiteralType(typed, expectation)
	case *HashLiteral:
		return c.inferExpectedHashLiteralType(typed, expectation)
	default:
		return c.inferExpressionType(expr)
	}
}

// bareIdentifierCallableArgument matches the identifier forms the runtime
// preserves under a callable expectation (`accept(rand)`).
func (c *scriptChecker) bareIdentifierCallableArgument(expr Expression) (Expression, bool) {
	var call *CallExpr
	switch typed := expr.(type) {
	case *Identifier:
		call = &CallExpr{Callee: typed}
	case *CallExpr:
		if typed.Parenthesized || len(typed.Args) > 0 || len(typed.KwArgs) > 0 ||
			typed.Block != nil || typed.BlockArg != nil {
			return nil, false
		}
		if _, ok := typed.Callee.(*Identifier); !ok {
			return nil, false
		}
		call = typed
	default:
		return nil, false
	}
	if _, ok := c.resolveCallable(call); !ok {
		return nil, false
	}
	return expr, true
}

func (c *scriptChecker) inferExpectedBranchUnion(expectation expressionExpectation, branches ...Expression) *TypeExpr {
	var merged *TypeExpr
	for i, branch := range branches {
		arm := checkTypeNil
		if branch != nil {
			arm = c.inferExpressionTypeWithExpectation(branch, expectation)
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

func (c *scriptChecker) inferExpectedArrayLiteralType(lit *ArrayLiteral, expectation expressionExpectation) *TypeExpr {
	elementExpectation, ok := expectation.arrayElementExpectation()
	if !ok || len(lit.Elements) == 0 {
		return c.inferArrayLiteralType(lit)
	}
	elements := make([]*TypeExpr, 0, len(lit.Elements))
	sawUnknown := false
	for i, element := range lit.Elements {
		if _, splat := element.(*SplatArg); splat {
			sawUnknown = true
			continue
		}
		elementType := c.inferExpressionTypeWithExpectation(element, elementExpectation(i, len(lit.Elements)))
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
		marker = literalPartialElementsMarker
	}
	return &TypeExpr{Kind: TypeArray, Name: marker, TypeArgs: []*TypeExpr{union}}
}

func (c *scriptChecker) inferExpectedHashLiteralType(lit *HashLiteral, expectation expressionExpectation) *TypeExpr {
	if !hashLiteralTypeHasValueSlots(expectation.ty) ||
		(lit.ShapeType != nil && !c.hashShapeStaticallyShadowed(lit)) {
		return c.inferHashLiteralType(lit)
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
			return checkTypeHash
		}
		key, ok := staticLiteralHashKey(pair.Key)
		if !ok {
			return checkTypeHash
		}
		valueExpectation := expressionExpectation{}
		if valueKey, ok := staticLiteralValue(pair.Key); ok {
			valueExpectation = typeExpressionExpectation(hashLiteralValueType(expectation.ty, valueKey))
		}
		fieldType := c.inferExpressionTypeWithExpectation(pair.Value, valueExpectation)
		if fieldType == nil {
			return checkTypeHash
		}
		if _, duplicate := shape[key]; duplicate {
			return checkTypeHash
		}
		shape[key] = fieldType
	}
	fact := &TypeExpr{Kind: TypeShape, Shape: shape}
	switch {
	case allSymbolKeys:
		fact.Name = shapeKeysSymbolMarker
	case allStringKeys:
		fact.Name = shapeKeysStringMarker
	}
	return fact
}

func (c *scriptChecker) inferConditionalExpressionType(expr *ConditionalExpr) *TypeExpr {
	return c.inferConditionalExpressionTypeWithExpectation(expr, expressionExpectation{})
}

// inferConditionalExpressionTypeWithExpectation infers a ternary under the
// narrowing each condition outcome proves, flowing any expectation into the
// branch results. Unreachable outcomes contribute no arm.
func (c *scriptChecker) inferConditionalExpressionTypeWithExpectation(expr *ConditionalExpr, expectation expressionExpectation) *TypeExpr {
	baseScopeState := c.snapshotScopeState()
	defer c.restoreScopeState(baseScopeState)

	branches := make([]*TypeExpr, 0, 2)
	if c.applyConditionOutcomeEffects(expr.Condition, true, nil) {
		branches = append(branches, c.inferExpressionTypeWithExpectation(expr.Consequent, expectation))
	}
	c.restoreScopeState(baseScopeState)
	if c.applyConditionOutcomeEffects(expr.Condition, false, nil) {
		branches = append(branches, c.inferExpressionTypeWithExpectation(expr.Alternate, expectation))
	}
	return unionTypeExprs(branches...)
}

func (c *scriptChecker) inferIfExpressionType(expr *IfExpr) *TypeExpr {
	return c.inferIfExpressionTypeWithExpectation(expr, expressionExpectation{})
}

func (c *scriptChecker) inferIfExpressionTypeWithExpectation(expr *IfExpr, expectation expressionExpectation) *TypeExpr {
	baseScopeState := c.snapshotScopeState()
	defer c.restoreScopeState(baseScopeState)

	branches := make([]*TypeExpr, 0, len(expr.ElseIf)+2)
	appendBranch := func(result Expression) {
		if result == nil {
			branches = append(branches, checkTypeNil)
			return
		}
		branches = append(branches, c.inferExpressionTypeWithExpectation(result, expectation))
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
	if ident, ok := condition.(*Identifier); ok {
		if fact, exact := c.localValueFactFor(ident.Name); exact &&
			len(fact.classNames) > 0 && len(fact.callables) == 0 {
			return true, true
		}
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

// inferCallExprType exposes an invariant result fact for a resolved call: a
// script function's annotation, a constructor's nominal class, or a builtin
// contract. Constructor identity survives splat expansion because expansion
// changes only argument binding, never the class produced by a successful
// call. Safe navigation returns nil when it must skip and otherwise adds nil
// unless the receiver is known non-nil.
func (c *scriptChecker) inferCallExprType(call *CallExpr) *TypeExpr {
	member, memberCall := call.Callee.(*MemberExpr)
	if memberCall && member.Safe && typeExprIsNilOnly(c.safeNavigationReceiverFact(member.Object)) {
		return checkTypeNil
	}
	target, ok := c.resolveCallable(call)
	if !ok {
		return nil
	}
	var result *TypeExpr
	if target.constructorClass != "" {
		result = &TypeExpr{Kind: TypeEnum, Name: target.constructorClass}
	} else if callExpandsArguments(call) {
		return nil
	} else if target.fn != nil {
		if target.constructor {
			return nil
		}
		result = target.fn.ReturnTy
		if result == nil {
			result = c.scriptFunctionReturnSummary(call, target.fn)
		}
	} else if target.name == "JSON.parse_as" && len(call.Args) == 2 {
		if shape, ok := shapeValuePayload(c.inferExpressionType(call.Args[1])); ok {
			// JSON object keys are strings, so the validated result and its
			// nested shapes are string-keyed stores.
			result = stringKeyedShapeFact(shape)
		}
	} else {
		result = target.spec.resultType
	}
	if memberCall {
		return c.safeNavigationMemberResultFact(member, result)
	}
	return result
}

// memberResultFact reports the result of a bare member read that auto-invokes
// in a value context: constructors carry their nominal class, script methods
// expose an explicit return annotation, builtins expose their invariant
// contract results, and temporal conversions surface as direct scalar values.
// A nil-only safe-navigation receiver yields nil without dispatch; safe
// navigation otherwise adds nil unless the receiver is known non-nil.
func (c *scriptChecker) memberResultFact(member *MemberExpr) *TypeExpr {
	if member.Safe && typeExprIsNilOnly(c.safeNavigationReceiverFact(member.Object)) {
		return checkTypeNil
	}
	if result := c.staticMemberValueResultFact(member); result != nil {
		return c.safeNavigationMemberResultFact(member, result)
	}
	target, ok := c.resolveMemberCallable(member)
	if !ok {
		return nil
	}
	var result *TypeExpr
	if target.constructorClass != "" {
		result = &TypeExpr{Kind: TypeEnum, Name: target.constructorClass}
	} else if target.fn != nil && !target.constructor {
		result = target.fn.ReturnTy
	} else if target.spec.autoInvoke {
		result = target.spec.resultType
	}
	return c.safeNavigationMemberResultFact(member, result)
}

func (c *scriptChecker) safeNavigationMemberResultFact(member *MemberExpr, result *TypeExpr) *TypeExpr {
	if result == nil || member == nil || !member.Safe || c.safeNavigationReceiverKnownNonNil(member.Object) {
		return result
	}
	if result.Kind == TypeUnion {
		return unionTypeExprs(result, checkTypeNil)
	}
	return nullableTypeExpr(result)
}

func (c *scriptChecker) safeNavigationReceiverKnownNonNil(expr Expression) bool {
	ident, ok := expr.(*Identifier)
	if !ok {
		return typeExprNeverNil(c.inferExpressionType(expr))
	}
	if typeExprNeverNil(c.localTypeFor(ident.Name)) {
		return true
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		return false
	}
	_, ok = c.script.classes[ident.Name]
	return ok
}

func (c *scriptChecker) safeNavigationReceiverFact(expr Expression) *TypeExpr {
	if ident, ok := expr.(*Identifier); ok {
		return c.localTypeFor(ident.Name)
	}
	return c.inferExpressionType(expr)
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
		// receiver; the outer appends are not retyped, so drop the root fact.
		if name, ok := rootIdentifierName(unwrapShovelChain(expr.Left)); ok {
			c.poisonLocalType(name)
		}
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
// a generic hash type: shapes witness every required field, so a required
// field type disjoint from the hash's value type contradicts it. Key types
// are left to runtime (key representation is not always known statically).
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
		// An optional field may be absent, so only a required field's type
		// is witnessed in every value of the shape.
		if shapeFieldOptional(field) {
			continue
		}
		if typeExprsDisjoint(field, valueType, resolve) {
			return true
		}
	}
	return false
}

// shapeFieldValueType strips the field-level optional marker for use as a
// value fact: optionality describes the field's presence in the store, not
// the value read from a present field.
func shapeFieldValueType(fieldType *TypeExpr) *TypeExpr {
	if fieldType == nil || !fieldType.Optional {
		return fieldType
	}
	clone := *fieldType
	clone.Optional = false
	return &clone
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
	return c.shapeTypeNamesStaticallyShadowed(lit.ShapeType)
}

// typeLiteralStaticallyShadowed mirrors the runtime's type-versus-value
// choice for an argument type literal: a literal without a value reading is
// always a type, and one with a value reading — always a bare identifier —
// keeps it only when that identifier's verbatim spelling resolves (`string?`
// is shadowed by a binding named `string?`, not by one named `string`).
func (c *scriptChecker) typeLiteralStaticallyShadowed(lit *TypeLiteral) bool {
	ident, ok := lit.Fallback.(*Identifier)
	if !ok {
		return false
	}
	return c.staticNameShadowed(ident.Name)
}

func (c *scriptChecker) shapeTypeNamesStaticallyShadowed(ty *TypeExpr) bool {
	shadowed := false
	walkShapeTypeNames(ty, func(name string) {
		if !shadowed && c.staticNameShadowed(name) {
			shadowed = true
		}
	})
	return shadowed
}

func (c *scriptChecker) staticNameShadowed(name string) bool {
	return c.identifierShadowed(name) || c.liveLocalNameHas(name) ||
		c.hostGlobalShadows(name) || c.typeRootResolvesName(name) ||
		c.hostBuiltinOverrides(name) || c.implicitSelfShadows(name)
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
				// An optional field may be absent, so its read joins nil.
				if shapeFieldOptional(fieldType) {
					return unionTypeExprs(shapeFieldValueType(fieldType), checkTypeNil)
				}
				return fieldType
			}
			// An open shape may hold an undeclared field of any type, so the
			// read stays unknown; a closed shape is known to miss.
			if objectType.Open {
				return nil
			}
			return checkTypeNil
		}
		// Unknown store representation: a present display name reads as the
		// field type or nil depending on the store's key kind; an absent one
		// misses either store.
		if present {
			return unionTypeExprs(shapeFieldValueType(fieldType), checkTypeNil)
		}
		if objectType.Open {
			return nil
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
	c.checkInferredExpressionAgainstTypeWithExpectation(function, expr, ty, subject, expressionExpectation{})
}

func (c *scriptChecker) checkInferredExpressionAgainstTypeWithExpectation(
	function string,
	expr Expression,
	ty *TypeExpr,
	subject string,
	expectation expressionExpectation,
) {
	if ty == nil {
		return
	}
	inferred := c.inferExpressionTypeWithExpectation(expr, expectation)
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
		inferred = c.inferExpressionTypeWithExpectation(expr, typeExpressionExpectation(ty))
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

// bareMemberArgumentCallableFact mirrors the runtime's callable-parameter
// expectation: a bound script method (including a constructor backed by
// initialize) is passed through instead of auto-invoked. Safe navigation adds
// nil when the receiver may skip dispatch. Generated getters still evaluate
// to their property value.
func (c *scriptChecker) bareMemberArgumentCallableFact(expr Expression) (*TypeExpr, bool) {
	member, ok := expr.(*MemberExpr)
	if !ok {
		return nil, false
	}
	target, ok := c.resolveMemberCallable(member)
	if !ok || target.fn == nil || target.fn.Accessor == functionAccessorGetter {
		return nil, false
	}
	if member.Safe && !c.safeNavigationReceiverKnownNonNil(member.Object) {
		if typeExprIsNilOnly(c.safeNavigationReceiverFact(member.Object)) {
			return checkTypeNil, true
		}
		return unionTypeExprs(checkTypeFunction, checkTypeNil), true
	}
	return checkTypeFunction, true
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
	// A statically shadowed type literal evaluates its value reading, so the
	// escaping value is the fallback identifier's (a container local named
	// array, for example); an unshadowed literal escapes only a fresh type
	// value and poisons nothing.
	if lit, ok := expr.(*TypeLiteral); ok {
		if !c.typeLiteralStaticallyShadowed(lit) {
			return "", false
		}
		expr = lit.Fallback
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
	if ty.Optional {
		b.WriteString("~")
	}
	if ty.Open {
		b.WriteString("+")
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

// shapeTypesDisjoint compares two shapes. A required field missing from the
// other shape's key set contradicts it only when that other shape is closed:
// the field must be present, and a closed shape rejects unknown fields, while
// an open shape admits (and leaves unvalidated) the extra. A field required
// on at least one side is witnessed in every common value, so a disjoint
// field type pair contradicts too; a field optional on both sides can be
// absent, satisfying either type.
func shapeTypesDisjoint(x, y *TypeExpr, resolve namedTypeResolver) bool {
	for field, xField := range x.Shape {
		yField, ok := y.Shape[field]
		if !ok {
			if !shapeFieldOptional(xField) && !y.Open {
				return true
			}
			continue
		}
		if shapeFieldOptional(xField) && shapeFieldOptional(yField) {
			continue
		}
		if typeExprsDisjoint(xField, yField, resolve) {
			return true
		}
	}
	for field, yField := range y.Shape {
		if _, ok := x.Shape[field]; !ok && !shapeFieldOptional(yField) && !x.Open {
			return true
		}
	}
	return false
}
