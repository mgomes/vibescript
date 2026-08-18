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
//     slice (`xs[0..1]`), or a member hop off a receiver whose type proves
//     the hop dispatches a builtin rather than reading a stored entry.
//  2. The receiver's root is a parameter of the enclosing block, the block
//     is attached to a builtin iterator that discards block results (the
//     each family), and no later statement of the block body reads the
//     parameter again, so not even the local rebinding is observed.

// blockResultDiscardingIterator names the builtin iterator members whose
// block results the runtime provably discards, which is what makes a block
// body's final statement a discard position. Dispatch is untyped, so the
// classification is by name; a user method sharing one of these names keeps
// the conventional contract of ignoring block results.
func blockResultDiscardingIterator(member string) bool {
	switch member {
	case "each", "each_with_index", "each_slice", "each_cons",
		"reverse_each", "cycle", "each_key", "each_value":
		return true
	default:
		return false
	}
}

// mutatorDiscardBlock records one enclosing block-literal walk: the block,
// its parameter names, and whether the enclosing call provably discards the
// block's results.
type mutatorDiscardBlock struct {
	block           *BlockLiteral
	params          map[string]struct{}
	discardsResults bool
}

// enterMutatorDiscardFunctionBody arms the discarded-mutator check for one
// function-body walk: the body's effective final statements carry the
// function's implicit return value, so they are not discard positions, and
// any block contexts belong to the interrupted outer walk, not this one.
// The returned func restores the outer walk's contexts.
func (c *scriptChecker) enterMutatorDiscardFunctionBody(body []Statement) func() {
	c.markResultCarryingStatements(body)
	saved := c.mutatorDiscardBlocks
	c.mutatorDiscardBlocks = nil
	return func() { c.mutatorDiscardBlocks = saved }
}

// markResultCarryingStatements records the statements whose value survives
// the statement boundary -- the effective final statements of a body whose
// result the caller can observe. The marking is syntactic and stable across
// repeated walks of the same body.
func (c *scriptChecker) markResultCarryingStatements(body []Statement) {
	if c.resultCarryingStatements == nil {
		c.resultCarryingStatements = make(map[Statement]struct{})
	}
	collectImplicitReturnLeaves(body, c.resultCarryingStatements)
}

// enterMutatorDiscardBlock arms the check for one block-body walk. The
// enclosing call's member name (empty for non-member calls) decides whether
// the block's final statement is a discard position, and the pushed context
// carries the parameter names for the block-parameter arm. The returned
// func pops the context.
func (c *scriptChecker) enterMutatorDiscardBlock(block *BlockLiteral, callMember string) func() {
	discards := blockResultDiscardingIterator(callMember)
	if !discards {
		c.markResultCarryingStatements(block.Body)
	}
	c.mutatorDiscardBlocks = append(c.mutatorDiscardBlocks, mutatorDiscardBlock{
		block:           block,
		params:          blockParamNames(block),
		discardsResults: discards,
	})
	return func() {
		c.mutatorDiscardBlocks = c.mutatorDiscardBlocks[:len(c.mutatorDiscardBlocks)-1]
	}
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
func (c *scriptChecker) mutatorDiscardVerdict(function string, stmt *ExprStmt) func() {
	if _, carriesResult := c.resultCarryingStatements[stmt]; carriesResult {
		return nil
	}
	member, call := discardedMemberCall(stmt.Expr)
	if member == nil || !isCollectionMutator(member.Property) {
		return nil
	}
	// Dispatch is untyped, so a mutator NAME is what classifies the call; a
	// receiver whose type rules the collection kinds out (a string's
	// non-mutating delete, a class instance's own method) is not a
	// collection mutation and stays out of scope.
	if c.provablyNotCollection(member.Object) {
		return nil
	}
	shape, root := c.mutatorReceiverShape(member.Object)
	if shape == mutatorReceiverTemporary {
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
	return c.blockParamMutatorDiscardVerdict(function, stmt, member, root)
}

// blockParamMutatorDiscardVerdict is the block-parameter arm: the
// receiver's root names a parameter of the innermost enclosing block, the
// block's results are provably discarded, and no later statement of the
// block body reads the parameter again. Only the innermost block's
// parameters qualify: an outer parameter mutated inside a nested block
// interleaves with that block's iterations, and a read the source places
// earlier can then run after the write. The statement must also sit
// directly in the block body's own statement list, so "later" has a
// provable meaning.
func (c *scriptChecker) blockParamMutatorDiscardVerdict(function string, stmt *ExprStmt, member *MemberExpr, root string) func() {
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
	body := enclosing.block.Body
	index := -1
	for i, bodyStmt := range body {
		if bodyStmt == Statement(stmt) {
			index = i
			break
		}
	}
	if index < 0 {
		return nil
	}
	if statementsReferenceAnyName(body[index+1:], map[string]struct{}{root: {}}) {
		return nil
	}
	return func() {
		c.add(function, member.Pos(),
			"mutating block parameter %s does not update the collection it was yielded from; build the result with map, or index the original",
			root)
	}
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

// mutatorReceiverShapeKind classifies a mutator's receiver expression
// against the runtime's mutable-path shape (mutablePathFor).
type mutatorReceiverShapeKind int

const (
	// mutatorReceiverUnknown covers receivers that may still name a slot --
	// a member hop that can be a stored-entry read, an index whose selector
	// kind is unproven. The check stays silent for them.
	mutatorReceiverUnknown mutatorReceiverShapeKind = iota
	// mutatorReceiverPath is the provable mutable-path shape: a bare root
	// with index hops whose selectors are not slices.
	mutatorReceiverPath
	// mutatorReceiverTemporary is a receiver that provably names no slot:
	// the write lands on a value the statement can never hand back.
	mutatorReceiverTemporary
)

// mutatorReceiverShape mirrors mutablePathFor syntactically: a local,
// instance-variable, or class-variable root followed by index and
// stored-entry hops names a slot; anything else is a temporary. Where the
// runtime decides a hop at execution time (a member hop reads a stored
// entry only when one exists; a range selector slices only arrays), the
// mirror answers temporary only on proof and unknown otherwise. The root
// name is returned for the block-parameter arm; it is empty when the root
// is not a bare identifier.
func (c *scriptChecker) mutatorReceiverShape(expr Expression) (mutatorReceiverShapeKind, string) {
	shape := mutatorReceiverPath
	for {
		switch typed := expr.(type) {
		case *Identifier:
			return shape, typed.Name
		case *IvarExpr, *ClassVarExpr:
			return shape, ""
		case *IndexExpr:
			// A multi-selector index is Ruby's start/length slice and a
			// range selector slices an array: both produce fresh subarrays,
			// not slots. A hash can store a range key, so a provably
			// hash-like object keeps the hop addressable.
			if len(typed.Indices) != 1 {
				return mutatorReceiverTemporary, ""
			}
			if _, isRange := typed.Indices[0].(*RangeExpr); isRange && !c.provablyHashLike(typed.Object) {
				return mutatorReceiverTemporary, ""
			}
			expr = typed.Object
		case *MemberExpr:
			// A member hop stays in collection storage only when it reads a
			// stored hash entry, which needs a hash-like object and an
			// entry present at runtime. An object whose type excludes
			// hashes proves the hop dispatches a builtin instead (`a.dup`),
			// so what it yields is a temporary; otherwise the hop may name
			// a slot and the shape is no longer provable either way.
			if c.provablyLeavesCollectionStorage(typed.Object) {
				return mutatorReceiverTemporary, ""
			}
			shape = mutatorReceiverUnknown
			expr = typed.Object
		default:
			return mutatorReceiverTemporary, ""
		}
	}
}

// provablyNotCollection reports whether expr's inferred type excludes every
// collection kind a registered mutator can update (array, hash, shape).
func (c *scriptChecker) provablyNotCollection(expr Expression) bool {
	arms, ok := typeExprArms(c.inferExpressionType(expr), 0)
	if !ok || len(arms) == 0 {
		return false
	}
	for _, arm := range arms {
		switch arm.Kind {
		case TypeArray, TypeHash, TypeShape, TypeAny, TypeUnknown, TypeUnion:
			return false
		}
	}
	return true
}

// provablyHashLike reports whether expr's inferred type proves a hash-like
// value, whose member and index hops read stored entries.
func (c *scriptChecker) provablyHashLike(expr Expression) bool {
	arms, ok := typeExprArms(c.inferExpressionType(expr), 0)
	if !ok || len(arms) == 0 {
		return false
	}
	for _, arm := range arms {
		switch arm.Kind {
		case TypeHash, TypeShape:
		default:
			return false
		}
	}
	return true
}

// provablyLeavesCollectionStorage reports whether a member hop off expr
// provably dispatches ordinary member evaluation rather than reading a
// stored hash entry: every inferred arm is a plain data kind that stores no
// members. Class instances are left unproven -- their accessors also hand
// out temporaries, but the nominal arms carry too little here to build a
// warning on.
func (c *scriptChecker) provablyLeavesCollectionStorage(expr Expression) bool {
	arms, ok := typeExprArms(c.inferExpressionType(expr), 0)
	if !ok || len(arms) == 0 {
		return false
	}
	for _, arm := range arms {
		switch arm.Kind {
		case TypeArray, TypeString, TypeInt, TypeFloat, TypeNumber, TypeBool,
			TypeSymbol, TypeNil, TypeRange, TypeTime, TypeDuration, TypeMoney,
			TypeFunction:
		default:
			return false
		}
	}
	return true
}

// mutatorDiscardCallSuffix renders the call tail of the teaching example:
// nothing for a bare member read, the call's own argument shape otherwise.
func mutatorDiscardCallSuffix(call *CallExpr) string {
	if call == nil {
		return ""
	}
	if len(call.Args) == 0 && len(call.KwArgs) == 0 {
		return "()"
	}
	return "(...)"
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
