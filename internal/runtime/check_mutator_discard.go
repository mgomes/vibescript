package runtime

import "strconv"

// Under value semantics a mutator only matters where its receiver names a
// slot the runtime can rebind: a local, instance, or class variable root
// followed by index or stored-entry hops (mutablePathFor). Anywhere else the
// write lands on a temporary, and when the statement also discards the
// mutator's result, the update reaches nothing at all -- `a.dup.push(x)` as
// a statement is a no-op that looks like work. The same silence covers the
// each-block migration trap: `rows.each { |row| row.push(0) }` mutates a
// per-iteration binding whose rebinding never returns to rows.
//
// The check fires only in statement position -- an expression-position
// mutation of a temporary is legitimate (`f(list.push(x))`), and a
// statement in result position (a function body's or value-carrying block
// body's effective final statement) hands its value to the caller. Within
// that position it warns in exactly two provable shapes, and stays silent
// whenever the receiver may still name a slot:
//
//  1. The receiver is provably a temporary: a call result, a literal, a
//     slice (`xs[0..1]`), a constant read (mutablePathFor rejects constant
//     roots, so the write lands on a detached copy), or a member hop off a
//     receiver whose type proves the hop dispatches a builtin rather than
//     reading a stored entry.
//  2. The receiver's root is a parameter of the enclosing block, the block
//     is attached to a builtin iterator that discards block results (the
//     each family), and no later statement of the block body reads the
//     parameter again, so not even the local rebinding is observed.

// blockResultDiscardingIterator names the builtin iterator members whose
// block results the runtime provably discards, which is what makes a block
// body's final statement a discard position.
//
// The list is deliberately only the iterators that can yield collections.
// The scalar-yielding ones (times, upto, step, each_char and kin) are
// absent because a scalar parameter never reaches a collection mutator past
// the receiver-type gate, so listing them would change nothing. A user
// method sharing one of these names keeps its own block contract, so the
// check also requires a receiver whose type proves the call is the builtin.
func blockResultDiscardingIterator(member string) bool {
	switch member {
	case "each", "each_with_index", "each_slice", "each_cons",
		"reverse_each", "cycle", "each_key", "each_value":
		return true
	default:
		return false
	}
}

// mutatorDiscardCall is the enclosing call's contribution to the discarded-
// mutator check: whether that call is a builtin iterator that discards
// block results, and the types those results are yielded as when proven.
type mutatorDiscardCall struct {
	discardsResults bool
	yieldTypes      map[string]*TypeExpr
}

// mutatorDiscardBlock records one enclosing block-literal walk: the block,
// its parameter names, whether the enclosing call provably discards the
// block's results, and the types those parameters are yielded as.
type mutatorDiscardBlock struct {
	block           *BlockLiteral
	params          map[string]struct{}
	discardsResults bool
	yieldTypes      map[string]*TypeExpr
}

// enterMutatorDiscardFunctionBody arms the discarded-mutator check for one
// function-body walk: the body's effective final statements carry the
// function's implicit return value, so they are not discard positions, and
// any block contexts belong to the interrupted outer walk, not this one.
// The returned func restores the outer walk's contexts.
func (c *scriptChecker) enterMutatorDiscardFunctionBody(fn *ScriptFunction) func() {
	c.markResultCarryingStatements(fn, fn.Body)
	saved := c.mutatorDiscardBlocks
	c.mutatorDiscardBlocks = nil
	return func() { c.mutatorDiscardBlocks = saved }
}

// markResultCarryingStatements records the statements whose value survives
// the statement boundary -- the effective final statements of a body whose
// result the caller can observe. The marking is syntactic, so each body
// (keyed by its owning function or block) is collected once and the result
// holds for every later walk.
func (c *scriptChecker) markResultCarryingStatements(key any, body []Statement) {
	if _, done := c.resultBodiesMarked[key]; done {
		return
	}
	if c.resultBodiesMarked == nil {
		c.resultBodiesMarked = make(map[any]struct{})
		c.resultCarryingStatements = make(map[Statement]struct{})
	}
	c.resultBodiesMarked[key] = struct{}{}
	collectImplicitReturnLeaves(body, c.resultCarryingStatements)
}

// enterMutatorDiscardBlock arms the check for one block-body walk. facts
// decides whether the block's final statement is a discard position, and
// the pushed context carries the parameter names for the block-parameter
// arm. The returned func pops the context.
func (c *scriptChecker) enterMutatorDiscardBlock(block *BlockLiteral, facts mutatorDiscardCall) func() {
	if !facts.discardsResults {
		c.markResultCarryingStatements(block, block.Body)
	}
	c.mutatorDiscardBlocks = append(c.mutatorDiscardBlocks, mutatorDiscardBlock{
		block:           block,
		params:          blockParamNames(block),
		discardsResults: facts.discardsResults,
		yieldTypes:      facts.yieldTypes,
	})
	return func() {
		c.mutatorDiscardBlocks = c.mutatorDiscardBlocks[:len(c.mutatorDiscardBlocks)-1]
	}
}

// currentBlockDiscardsStatement reports whether stmt sits in the innermost
// enclosing block whose results this walk discards. The global
// resultCarryingStatements set is sticky across reachable-call walks, so a
// prior walk of a user each that consumes the block result must not suppress
// a later builtin Array#each walk of the same body.
func (c *scriptChecker) currentBlockDiscardsStatement(stmt *ExprStmt) bool {
	if len(c.mutatorDiscardBlocks) == 0 {
		return false
	}
	enclosing := &c.mutatorDiscardBlocks[len(c.mutatorDiscardBlocks)-1]
	if !enclosing.discardsResults {
		return false
	}
	leaves := make(map[Statement]struct{})
	collectImplicitReturnLeaves(enclosing.block.Body, leaves)
	_, ok := leaves[stmt]
	return ok
}

// mutatorDiscardCallFacts reports whether call is a builtin iterator that
// discards block results, and the types it yields into the block's
// parameters when that is proven. A resolved script method of the same
// name keeps its own block contract; an untyped or named-class receiver
// is left unproven rather than assumed to be the builtin.
func (c *scriptChecker) mutatorDiscardCallFacts(call *CallExpr, target staticCallable, resolved bool) mutatorDiscardCall {
	if call == nil {
		return mutatorDiscardCall{}
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || !blockResultDiscardingIterator(member.Property) {
		return mutatorDiscardCall{}
	}
	if resolved && target.fn != nil {
		return mutatorDiscardCall{}
	}
	if !c.receiverOwnsBuiltinIterator(member.Object, member.Property) {
		return mutatorDiscardCall{}
	}
	return mutatorDiscardCall{
		discardsResults: true,
		yieldTypes:      c.iteratorYieldTypes(member.Object, member.Property, call.Block),
	}
}

// receiverOwnsBuiltinIterator reports whether every inferred arm of
// receiver dispatches property as an array or hash builtin, so a same-
// named user method cannot be the callee.
func (c *scriptChecker) receiverOwnsBuiltinIterator(receiver Expression, property string) bool {
	return typeExprArmsAll(c.inferExpressionType(receiver), func(arm *TypeExpr) bool {
		var kind string
		switch arm.Kind {
		case TypeArray:
			kind = "array"
		case TypeHash, TypeShape:
			kind = "hash"
		default:
			return false
		}
		return memberKindOwns(kind, property)
	})
}

// iteratorYieldTypes maps the block's parameter names to the types the
// builtin iterator yields into them, when those types are proven. Missing
// entries stay unknown and cannot support a discarded-mutator warning.
func (c *scriptChecker) iteratorYieldTypes(receiver Expression, property string, block *BlockLiteral) map[string]*TypeExpr {
	if block == nil {
		return nil
	}
	recv := c.inferExpressionType(receiver)
	hashLike := typeExprHashLikeOnly(recv)
	var paramYields []*TypeExpr
	switch property {
	case "each":
		if hashLike {
			if len(block.Params)+len(block.ImplicitParams) >= 2 {
				paramYields = []*TypeExpr{
					collectionIteratorKeyType(recv),
					collectionIteratorValueType(recv),
				}
			} else {
				paramYields = []*TypeExpr{checkTypeArray}
			}
		} else {
			paramYields = []*TypeExpr{collectionIteratorElementType(recv)}
		}
	case "each_with_index":
		if hashLike {
			paramYields = []*TypeExpr{checkTypeArray, checkTypeInt}
		} else {
			paramYields = []*TypeExpr{collectionIteratorElementType(recv), checkTypeInt}
		}
	default:
		paramYields = []*TypeExpr{c.builtinIteratorYieldedValue(receiver, property)}
	}
	out := make(map[string]*TypeExpr)
	for i, param := range block.Params {
		var yield *TypeExpr
		if i < len(paramYields) {
			yield = paramYields[i]
		}
		projectYieldOntoParam(out, param, yield)
	}
	for i, name := range block.ImplicitParams {
		if name == "" || i >= len(paramYields) || paramYields[i] == nil {
			continue
		}
		out[name] = paramYields[i]
	}
	if hashLike && len(block.Params) >= 1 {
		pairParam := 0
		switch property {
		case "each":
			if len(block.Params)+len(block.ImplicitParams) >= 2 {
				pairParam = -1
			}
		case "each_with_index":
			pairParam = 0
		default:
			pairParam = -1
		}
		if pairParam >= 0 {
			if target, ok := block.Params[pairParam].Target.(*DestructureTarget); ok {
				projectHashPairOntoDestructure(out, target, recv)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func projectYieldOntoParam(out map[string]*TypeExpr, param Param, yield *TypeExpr) {
	if param.Name != "" && yield != nil {
		out[param.Name] = yield
	}
	if target, ok := param.Target.(*DestructureTarget); ok {
		projectYieldOntoDestructure(out, target, yield)
	}
}

func projectHashPairOntoDestructure(out map[string]*TypeExpr, target *DestructureTarget, recv *TypeExpr) {
	if out == nil || target == nil {
		return
	}
	key := collectionIteratorKeyType(recv)
	val := collectionIteratorValueType(recv)
	pair := []*TypeExpr{key, val}
	var pre, post []DestructureElement
	sawRest := false
	for _, el := range target.Elements {
		if el.Rest {
			sawRest = true
			continue
		}
		if sawRest {
			post = append(post, el)
		} else {
			pre = append(pre, el)
		}
	}
	bind := func(el DestructureElement, elType *TypeExpr) {
		switch nested := el.Target.(type) {
		case *Identifier:
			if nested.Name != "" && elType != nil {
				out[nested.Name] = elType
			}
		case *DestructureTarget:
			projectYieldOntoDestructure(out, nested, elType)
		}
	}
	for i, el := range pre {
		if i < len(pair) {
			bind(el, pair[i])
		}
	}
	for i, el := range post {
		pairIdx := len(pair) - len(post) + i
		if pairIdx >= 0 && pairIdx < len(pair) {
			bind(el, pair[pairIdx])
		}
	}
}

func projectYieldOntoDestructure(out map[string]*TypeExpr, target *DestructureTarget, yield *TypeExpr) {
	if target == nil {
		return
	}
	var slot *TypeExpr
	if yield != nil && yield.Kind == TypeArray && len(yield.TypeArgs) == 1 {
		slot = yield.TypeArgs[0]
	}
	for _, el := range target.Elements {
		elType := el.Type
		if elType == nil {
			if el.Rest {
				elType = checkTypeArray
			} else {
				elType = slot
			}
		}
		switch nested := el.Target.(type) {
		case *Identifier:
			if nested.Name != "" && elType != nil {
				out[nested.Name] = elType
			}
		case *DestructureTarget:
			projectYieldOntoDestructure(out, nested, elType)
		}
	}
}

// builtinIteratorYieldedValue is the value the named builtin iterator
// yields as the block's first argument: an array's element, a hash/shape
// value for each_value, a key for each_key, or a fresh array window for
// each_slice and each_cons. Unknown or partial element facts stay nil.
func (c *scriptChecker) builtinIteratorYieldedValue(receiver Expression, property string) *TypeExpr {
	recv := c.inferExpressionType(receiver)
	switch property {
	case "each_slice", "each_cons":
		return checkTypeArray
	case "each_key":
		return collectionIteratorKeyType(recv)
	case "each_value":
		return collectionIteratorValueType(recv)
	default:
		return collectionIteratorElementType(recv)
	}
}

// temporaryMutatorDiscardApplies is the temporary-arm receiver-type gate:
// untyped receivers keep the name-based warning unless they are proven not
// to be collections, but a known type (including any) must be
// collection-only so a union or any-typed Widget#push cannot claim the
// update reaches nothing.
func (c *scriptChecker) temporaryMutatorDiscardApplies(recv Expression) bool {
	if c.provablyNotCollection(recv) {
		return false
	}
	ty := c.inferExpressionType(recv)
	if ty == nil || ty.Kind == TypeUnknown {
		return true
	}
	return typeExprIsCollection(ty)
}

func collectionIteratorElementType(recv *TypeExpr) *TypeExpr {
	arms, ok := typeExprArms(recv, 0)
	if !ok || len(arms) == 0 {
		return nil
	}
	elems := make([]*TypeExpr, 0, len(arms))
	for _, arm := range arms {
		if arm.Kind != TypeArray || len(arm.TypeArgs) != 1 ||
			literalArrayElementsPartial(arm) {
			return nil
		}
		elems = append(elems, arm.TypeArgs[0])
	}
	return unionTypeExprs(elems...)
}

func collectionIteratorValueType(recv *TypeExpr) *TypeExpr {
	arms, ok := typeExprArms(recv, 0)
	if !ok || len(arms) == 0 {
		return nil
	}
	vals := make([]*TypeExpr, 0, len(arms))
	for _, arm := range arms {
		v := collectionIteratorValueTypeArm(arm)
		if v == nil {
			return nil
		}
		vals = append(vals, v)
	}
	return unionTypeExprs(vals...)
}

func collectionIteratorValueTypeArm(arm *TypeExpr) *TypeExpr {
	switch arm.Kind {
	case TypeHash:
		if len(arm.TypeArgs) == 2 {
			return arm.TypeArgs[1]
		}
	case TypeShape:
		if len(arm.Shape) == 0 {
			return nil
		}
		vals := make([]*TypeExpr, 0, len(arm.Shape))
		for _, field := range arm.Shape {
			vals = append(vals, shapeFieldValueType(field))
		}
		return unionTypeExprs(vals...)
	}
	return nil
}

func collectionIteratorKeyType(recv *TypeExpr) *TypeExpr {
	arms, ok := typeExprArms(recv, 0)
	if !ok || len(arms) == 0 {
		return nil
	}
	keys := make([]*TypeExpr, 0, len(arms))
	for _, arm := range arms {
		k := collectionIteratorKeyTypeArm(arm)
		if k == nil {
			return nil
		}
		keys = append(keys, k)
	}
	return unionTypeExprs(keys...)
}

func collectionIteratorKeyTypeArm(arm *TypeExpr) *TypeExpr {
	switch arm.Kind {
	case TypeHash:
		if len(arm.TypeArgs) == 2 {
			return arm.TypeArgs[0]
		}
	case TypeShape:
		if arm.Name == shapeKeysStringMarker {
			return checkTypeString
		}
		if arm.Name == shapeKeysSymbolMarker {
			return checkTypeSymbol
		}
	}
	return nil
}

func blockParamNames(block *BlockLiteral) map[string]struct{} {
	names := make(map[string]struct{}, len(block.Params)+len(block.ImplicitParams))
	for _, param := range block.Params {
		if param.Name != "" {
			names[param.Name] = struct{}{}
		}
		collectBindingTarget(param.Target, names)
	}
	for _, name := range block.ImplicitParams {
		if name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

// mutatorDiscardVerdict decides, against the facts holding BEFORE the
// statement's own walk, whether a statement-position mutator call provably
// updates nothing. It must run before the walk because the walk itself
// poisons the receiver's structural facts (an unproven dispatch may mutate
// its receiver), which are exactly the facts the receiver shape needs. The
// returned func emits the warning; the caller invokes it only once the
// statement's walk proves the call can complete, and it is nil when there
// is nothing to report.
//
// The receiver-type gate (provablyNotCollection) runs only once an arm is
// otherwise ready to warn: inference is the expensive step, and the common
// legitimate statement -- a mutator on an addressable local -- never needs
// it.
func (c *scriptChecker) mutatorDiscardVerdict(function string, stmt *ExprStmt) func() {
	if _, carriesResult := c.resultCarryingStatements[stmt]; carriesResult &&
		!c.currentBlockDiscardsStatement(stmt) {
		return nil
	}
	var reports []func()
	for _, site := range c.discardedMutatorCalls(stmt.Expr) {
		if report := c.oneMutatorDiscardVerdict(function, stmt, site.member, site.call); report != nil {
			reports = append(reports, report)
		}
	}
	if len(reports) == 0 {
		return nil
	}
	return func() {
		for _, report := range reports {
			report()
		}
	}
}

func (c *scriptChecker) oneMutatorDiscardVerdict(function string, stmt *ExprStmt, member *MemberExpr, call *CallExpr) func() {
	if member == nil || !isCollectionMutator(member.Property) {
		return nil
	}
	temporary, root := c.mutatorReceiverShape(member.Object)
	if temporary {
		if !c.temporaryMutatorDiscardApplies(member.Object) {
			return nil
		}
		return func() {
			c.add(function, member.Pos(),
				"%s updates a temporary; the update reaches nothing. Assign the result, as in `x = %s.%s%s`",
				member.Property,
				mutatorDiscardReceiverSource(member.Object),
				member.Property,
				mutatorDiscardCallSuffix(call),
			)
		}
	}
	if root == "" {
		return nil
	}
	return c.blockParamMutatorDiscardVerdict(function, stmt, member, call, root)
}

// blockParamMutatorDiscardVerdict is the block-parameter arm: the
// receiver's root names a parameter of the innermost enclosing block, the
// block's results are provably discarded, and no later statement of the
// block body reads the parameter again. Only the innermost block's
// parameters qualify: an outer parameter mutated inside a nested block
// interleaves with that block's iterations, and a read the source places
// earlier can then run after the write. Nested statements still qualify:
// "later" is any statement that runs after the write in the same block,
// including after a nested if or loop that contained it.
func (c *scriptChecker) blockParamMutatorDiscardVerdict(function string, stmt *ExprStmt, member *MemberExpr, call *CallExpr, root string) func() {
	if len(c.mutatorDiscardBlocks) == 0 {
		return nil
	}
	enclosing := &c.mutatorDiscardBlocks[len(c.mutatorDiscardBlocks)-1]
	if !enclosing.discardsResults {
		return nil
	}
	if _, isParam := enclosing.params[root]; !isParam {
		return nil
	}
	names := map[string]struct{}{root: {}}
	found, laterObserves := statementsAfterObserveName(enclosing.block.Body, stmt, names)
	if !found {
		return nil
	}
	if laterObserves {
		return nil
	}
	if call != nil && call.Block != nil && statementsObserveName(call.Block.Body, names) {
		return nil
	}
	if c.provablyNotCollection(member.Object) {
		return nil
	}
	if !c.blockParamMutatorProvablyCollection(member.Object, enclosing, root) {
		return nil
	}
	return func() {
		c.add(function, member.Pos(),
			"mutating block parameter %s does not update the collection it was yielded from; build the result with map, or index the original",
			root)
	}
}

type discardedMutatorSite struct {
	member *MemberExpr
	call   *CallExpr
}

// discardedMutatorCalls collects mutator sites in a statement expression,
// including discarded arms of a ternary, if, case, rescue, or short-circuit
// expression. Unreachable arms follow the same truthiness decision as the
// expression walk.
func (c *scriptChecker) discardedMutatorCalls(expr Expression) []discardedMutatorSite {
	var sites []discardedMutatorSite
	var walk func(Expression)
	walk = func(e Expression) {
		if e == nil {
			return
		}
		if member, call := discardedMemberCall(e); member != nil {
			sites = append(sites, discardedMutatorSite{member: member, call: call})
			return
		}
		switch typed := e.(type) {
		case *ConditionalExpr:
			truthy, known := c.inferredConditionTruthiness(typed.Condition)
			if !known || truthy {
				walk(typed.Consequent)
			}
			if !known || !truthy {
				walk(typed.Alternate)
			}
		case *RescueExpr:
			walk(typed.Body)
			walk(typed.Fallback)
		case *IfExpr:
			if branch, ok := c.inferredIfExpressionBranch(typed); ok {
				walk(branch)
				return
			}
			walk(typed.Consequent)
			for _, branch := range typed.ElseIf {
				walk(branch.Result)
			}
			walk(typed.Alternate)
		case *CaseExpr:
			for _, clause := range typed.Clauses {
				walk(clause.Result)
			}
			walk(typed.ElseExpr)
		case *BinaryExpr:
			if typed.Operator == tokenAnd || typed.Operator == tokenOr {
				if binaryRightMayEvaluate(typed) && !c.binaryRightUnreachable(typed) {
					walk(typed.Right)
				}
			}
		}
	}
	walk(expr)
	return sites
}

// discardedMemberCall unwraps a statement expression into the member call it
// dispatches: a call through a member callee, or a bare member read the
// runtime auto-invokes (`f().clear`). The call is nil for the bare form.
func discardedMemberCall(expr Expression) (*MemberExpr, *CallExpr) {
	switch typed := expr.(type) {
	case *MemberExpr:
		return typed, nil
	case *CallExpr:
		if member, ok := typed.Callee.(*MemberExpr); ok {
			return member, typed
		}
	}
	return nil, nil
}

func statementsAfterObserveName(body []Statement, stmt *ExprStmt, names map[string]struct{}) (found, observes bool) {
	var search func([]Statement) bool
	search = func(stmts []Statement) bool {
		for i, s := range stmts {
			if s == Statement(stmt) {
				if statementsObserveName(stmts[i+1:], names) {
					observes = true
				}
				return true
			}
			switch typed := s.(type) {
			case *IfStmt:
				nested := search(typed.Consequent)
				for _, branch := range typed.ElseIf {
					nested = search(branch.Consequent) || nested
				}
				nested = search(typed.Alternate) || nested
				if nested {
					if statementsObserveName(stmts[i+1:], names) {
						observes = true
					}
					return true
				}
			case *TryStmt:
				inBody := search(typed.Body)
				inElse := false
				inRescue := false
				inEnsure := false
				if !inBody {
					inElse = search(typed.Else)
				}
				if !inBody && !inElse {
					for _, clause := range typed.Rescues {
						if search(clause.Body) {
							inRescue = true
							break
						}
					}
				}
				if !inBody && !inElse && !inRescue {
					inEnsure = search(typed.Ensure)
				}
				if inBody || inElse || inRescue || inEnsure {
					if inBody && statementsObserveName(typed.Else, names) {
						observes = true
					}
					if (inBody || inElse || inRescue) &&
						statementsObserveName(typed.Ensure, names) {
						observes = true
					}
					if statementsObserveName(stmts[i+1:], names) {
						observes = true
					}
					return true
				}
			case *WhileStmt:
				if search(typed.Body) {
					if loopBackEdgeObservesName(typed.Condition, typed.Body, stmt, names) ||
						statementsObserveName(stmts[i+1:], names) {
						observes = true
					}
					return true
				}
			case *UntilStmt:
				if search(typed.Body) {
					if loopBackEdgeObservesName(typed.Condition, typed.Body, stmt, names) ||
						statementsObserveName(stmts[i+1:], names) {
						observes = true
					}
					return true
				}
			case *ForStmt:
				if search(typed.Body) {
					if statementsObserveNameExcept(typed.Body, names, stmt) ||
						statementsObserveName(stmts[i+1:], names) {
						observes = true
					}
					return true
				}
			}
		}
		return false
	}
	found = search(body)
	return found, observes
}

func statementsObserveName(statements []Statement, names map[string]struct{}) bool {
	return statementsObserveNameExcept(statements, names, nil)
}

func loopBackEdgeObservesName(condition Expression, body []Statement, stmt *ExprStmt, names map[string]struct{}) bool {
	return expressionReferencesAnyName(condition, names) ||
		statementsObserveNameExcept(body, names, stmt)
}

func statementsObserveNameExcept(statements []Statement, names map[string]struct{}, except *ExprStmt) bool {
	for _, statement := range statements {
		if except != nil && statement == Statement(except) {
			continue
		}
		switch typed := statement.(type) {
		case *AssignStmt:
			if assignmentTargetObservesName(typed.Target, names) ||
				expressionReferencesAnyName(typed.Value, names) {
				return true
			}
		case *ExprStmt:
			if expressionReferencesAnyName(typed.Expr, names) {
				return true
			}
		case *IfStmt:
			if expressionReferencesAnyName(typed.Condition, names) ||
				statementsObserveNameExcept(typed.Consequent, names, except) ||
				statementsObserveNameExcept(typed.Alternate, names, except) {
				return true
			}
			for _, branch := range typed.ElseIf {
				if expressionReferencesAnyName(branch.Condition, names) ||
					statementsObserveNameExcept(branch.Consequent, names, except) {
					return true
				}
			}
		case *ReturnStmt:
			if expressionReferencesAnyName(typed.Value, names) {
				return true
			}
		case *RaiseStmt:
			if expressionReferencesAnyName(typed.Value, names) ||
				expressionReferencesAnyName(typed.Message, names) {
				return true
			}
		default:
			if statementsReferenceAnyName([]Statement{statement}, names) {
				return true
			}
		}
	}
	return false
}

// assignmentTargetObservesName reports a later use of names in an assignment
// target. A bare identifier is the slot being overwritten, not a read; an
// index or member target still reads the receiver.
func assignmentTargetObservesName(target Expression, names map[string]struct{}) bool {
	switch typed := target.(type) {
	case *Identifier:
		return false
	case *IndexExpr:
		if expressionReferencesAnyName(typed.Object, names) {
			return true
		}
		for _, index := range typed.Indices {
			if expressionReferencesAnyName(index, names) {
				return true
			}
		}
		return false
	case *MemberExpr:
		return expressionReferencesAnyName(typed.Object, names)
	default:
		return expressionReferencesAnyName(target, names)
	}
}

// mutatorReceiverShape mirrors mutablePathFor syntactically: a local,
// instance-variable, or class-variable root followed by index and
// stored-entry hops names a slot; anything else is a temporary. Where the
// runtime decides a hop at execution time (a member hop reads a stored
// entry only when one exists), the mirror answers temporary only on proof
// and stays quiet otherwise. The root name feeds the block-parameter arm;
// it is empty when the root is not a bare identifier.
func (c *scriptChecker) mutatorReceiverShape(expr Expression) (temporary bool, root string) {
	for {
		switch typed := expr.(type) {
		case *Identifier:
			// An uppercase name that no scope can bind is a constant read.
			// mutablePathFor rejects constant roots where self is a class,
			// and elsewhere a constant is no local either, so the write
			// lands on a detached copy both ways (verified for static and
			// instance methods alike). A host option global of the same
			// name stays an addressable Env binding (Env.Assign rebinds
			// it), so it is not a temporary. Class and namespace
			// references keep their own member dispatch and stay out of
			// scope.
			if isConstantIdentifier(typed.Name) && !c.scopeHas(typed.Name) &&
				!c.liveLocalNameHas(typed.Name) {
				if c.optionGlobalSeeded(typed.Name) {
					return false, typed.Name
				}
				if c.script.classes[typed.Name] != nil ||
					c.recordedNamespaceMemberPrefix(typed.Name+".") {
					return false, ""
				}
				return true, ""
			}
			// A zero-argument function used without parentheses auto-invokes
			// (walkMutablePath rejects rootAutoInvokes roots), so rows.push
			// mutates the detached return value the same way rows().push does.
			if c.identifierIsAutoInvokedTemporary(typed.Name) {
				return true, ""
			}
			return false, typed.Name
		case *IvarExpr, *ClassVarExpr:
			return false, ""
		case *IndexExpr:
			// A multi-selector index is Ruby's start/length slice, and a
			// range selector always slices: range hash keys are rejected at
			// runtime ("hash keys must be strings or symbols"), so no range
			// hop can name a slot. Both produce fresh subarrays.
			if len(typed.Indices) != 1 {
				return true, ""
			}
			if _, isRange := typed.Indices[0].(*RangeExpr); isRange {
				return true, ""
			}
			expr = typed.Object
		case *MemberExpr:
			// A member hop stays in collection storage only when it reads a
			// stored hash entry, which needs a hash-like object and an
			// entry present at runtime. An object whose type excludes
			// hashes proves the hop dispatches a builtin instead (`a.dup`),
			// so what it yields is a temporary; otherwise the hop may name
			// a slot, class-1 proof is gone, and only the root survives for
			// the block-parameter arm.
			if c.provablyLeavesCollectionStorage(typed.Object) {
				return true, ""
			}
			expr = typed.Object
		default:
			return true, ""
		}
	}
}

// identifierIsAutoInvokedTemporary reports whether name would auto-invoke as
// a zero-argument function rather than name a local slot. A scope binding
// or option global wins, matching ordinary evaluation's lookup order.
func (c *scriptChecker) identifierIsAutoInvokedTemporary(name string) bool {
	if c.scopeHas(name) || c.liveLocalNameHas(name) || c.optionGlobalSeeded(name) {
		return false
	}
	if c.script == nil {
		return false
	}
	if fn, ok := c.script.functions[name]; ok {
		return len(fn.Params) == 0
	}
	if fn, ok := c.typeRootFunction(name); ok {
		return len(fn.Params) == 0
	}
	if spec, ok := c.defaultBuiltinCallSpec(name); ok {
		return spec.autoInvoke
	}
	return c.script.engine != nil && c.script.engine.builtinAutoInvokes(name)
}

// provablyNotCollection reports whether expr's inferred type excludes every
// collection kind a registered mutator can update (array, hash, shape).
func (c *scriptChecker) provablyNotCollection(expr Expression) bool {
	return typeExprArmsAll(c.inferExpressionType(expr), func(arm *TypeExpr) bool {
		switch arm.Kind {
		case TypeArray, TypeHash, TypeShape, TypeAny, TypeUnknown, TypeUnion:
			return false
		default:
			return true
		}
	})
}

// blockParamMutatorProvablyCollection reports whether the mutator receiver
// is a collection, either from its own inferred type or from the enclosing
// iterator's proven yield type along the path from the block parameter.
// Unknown and named-class yields stay silent: a user method named push on
// a Widget is not a collection mutator.
func (c *scriptChecker) blockParamMutatorProvablyCollection(
	recv Expression,
	enclosing *mutatorDiscardBlock,
	root string,
) bool {
	if typeExprIsCollection(c.inferExpressionType(recv)) {
		return true
	}
	if enclosing == nil {
		return false
	}
	yield := enclosing.yieldTypes[root]
	if yield == nil {
		return false
	}
	return typeExprIsCollection(typeAlongPathFromRoot(recv, root, yield))
}

func typeExprIsCollection(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		switch arm.Kind {
		case TypeArray, TypeHash, TypeShape:
			return true
		default:
			return false
		}
	})
}

// typeAlongPathFromRoot types expr as if root had rootType, walking only
// identifier, index, and member hops so an unannotated block parameter can
// still prove collection dispatch from the iterator's yield.
func typeAlongPathFromRoot(expr Expression, root string, rootType *TypeExpr) *TypeExpr {
	switch typed := expr.(type) {
	case *Identifier:
		if typed.Name == root {
			return rootType
		}
	case *IndexExpr:
		return indexCollectionElementType(typeAlongPathFromRoot(typed.Object, root, rootType), typed)
	case *MemberExpr:
		return memberCollectionEntryType(typeAlongPathFromRoot(typed.Object, root, rootType), typed.Property)
	}
	return nil
}

func indexCollectionElementType(obj *TypeExpr, expr *IndexExpr) *TypeExpr {
	if obj == nil || expr == nil || len(expr.Indices) != 1 {
		return nil
	}
	if _, isRange := expr.Indices[0].(*RangeExpr); isRange {
		return nil
	}
	switch obj.Kind {
	case TypeArray:
		if len(obj.TypeArgs) == 1 {
			return obj.TypeArgs[0]
		}
	case TypeHash:
		if len(obj.TypeArgs) == 2 {
			return obj.TypeArgs[1]
		}
	case TypeShape:
		key, ok := staticLiteralHashKey(expr.Indices[0])
		if !ok {
			return nil
		}
		field, present := obj.Shape[key]
		if !present {
			return nil
		}
		return shapeFieldValueType(field)
	}
	return nil
}

func memberCollectionEntryType(obj *TypeExpr, property string) *TypeExpr {
	if obj == nil || obj.Kind != TypeShape {
		return nil
	}
	field, present := obj.Shape[property]
	if !present {
		return nil
	}
	return shapeFieldValueType(field)
}

// provablyLeavesCollectionStorage reports whether a member hop off expr
// provably dispatches ordinary member evaluation rather than reading a
// stored hash entry: every inferred arm is a plain data kind that stores no
// members. Class instances are left unproven -- their accessors also hand
// out temporaries, but the nominal arms carry too little here to build a
// warning on.
func (c *scriptChecker) provablyLeavesCollectionStorage(expr Expression) bool {
	ty := c.inferExpressionType(expr)
	if ty == nil {
		if ident, ok := expr.(*Identifier); ok {
			ty = c.blockParamYieldType(ident.Name)
		}
	}
	return typeExprLeavesCollectionStorage(ty)
}

func (c *scriptChecker) blockParamYieldType(name string) *TypeExpr {
	if len(c.mutatorDiscardBlocks) == 0 {
		return nil
	}
	enclosing := &c.mutatorDiscardBlocks[len(c.mutatorDiscardBlocks)-1]
	if enclosing.yieldTypes == nil {
		return nil
	}
	return enclosing.yieldTypes[name]
}

func typeExprLeavesCollectionStorage(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		switch arm.Kind {
		case TypeArray, TypeString, TypeInt, TypeFloat, TypeNumber, TypeBool,
			TypeSymbol, TypeNil, TypeRange, TypeTime, TypeDuration, TypeMoney,
			TypeFunction:
			return true
		default:
			return false
		}
	})
}

// mutatorDiscardCallSuffix renders the call tail of the teaching example:
// nothing for a bare member read, the call's own argument shape otherwise,
// and the block spelled out so following the advice keeps the block.
func mutatorDiscardCallSuffix(call *CallExpr) string {
	if call == nil {
		return ""
	}
	args := "()"
	if len(call.Args) > 0 || len(call.KwArgs) > 0 {
		args = "(...)"
	}
	if call.Block != nil {
		if args == "()" {
			return " { ... }"
		}
		return args + " { ... }"
	}
	return args
}

// mutatorDiscardReceiverSource renders a receiver expression compactly for
// the teaching example, eliding what it cannot spell.
func mutatorDiscardReceiverSource(expr Expression) string {
	switch typed := expr.(type) {
	case *Identifier:
		return typed.Name
	case *IvarExpr:
		return "@" + typed.Name
	case *ClassVarExpr:
		return "@@" + typed.Name
	case *MemberExpr:
		return mutatorDiscardReceiverSource(typed.Object) + "." + typed.Property
	case *ScopeExpr:
		return mutatorDiscardReceiverSource(typed.Object) + "::" + typed.Property
	case *IndexExpr:
		selectors := ""
		for i, index := range typed.Indices {
			if i > 0 {
				selectors += ", "
			}
			selectors += mutatorDiscardReceiverSource(index)
		}
		return mutatorDiscardReceiverSource(typed.Object) + "[" + selectors + "]"
	case *CallExpr:
		callee := mutatorDiscardReceiverSource(typed.Callee)
		if len(typed.Args) == 0 && len(typed.KwArgs) == 0 {
			return callee + "()"
		}
		return callee + "(...)"
	case *IntegerLiteral:
		if typed.Big != nil {
			return typed.Big.String()
		}
		return strconv.FormatInt(typed.Value, 10)
	case *SymbolLiteral:
		return ":" + typed.Name
	case *StringLiteral:
		return strconv.Quote(typed.Value)
	case *RangeExpr:
		op := ".."
		if typed.Exclusive {
			op = "..."
		}
		return mutatorDiscardReceiverSource(typed.Start) + op + mutatorDiscardReceiverSource(typed.End)
	case *ArrayLiteral:
		return "[...]"
	case *HashLiteral:
		return "{...}"
	default:
		return "..."
	}
}
