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
// body's final statement a discard position. Dispatch is untyped, so the
// classification is by name; a user method sharing one of these names keeps
// the conventional contract of ignoring block results.
//
// The list is deliberately only the iterators that can yield collections.
// The scalar-yielding ones (times, upto, step, each_char and kin) are
// absent because a scalar parameter never reaches a collection mutator past
// the receiver-type gate, so listing them would change nothing. Declaring
// the fact at builtin registration instead (the DeclareNonMutating
// precedent) was considered and declined while the check is warning-only:
// a missing name costs one missed warning, never a false positive.
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

// enterMutatorDiscardBlock arms the check for one block-body walk. The
// enclosing call's member name (empty for non-member calls) decides whether
// the block's final statement is a discard position, and the pushed context
// carries the parameter names for the block-parameter arm. The returned
// func pops the context.
func (c *scriptChecker) enterMutatorDiscardBlock(block *BlockLiteral, callMember string) func() {
	discards := blockResultDiscardingIterator(callMember)
	if !discards {
		c.markResultCarryingStatements(block, block.Body)
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
//
// The receiver-type gate (provablyNotCollection) runs only once an arm is
// otherwise ready to warn: inference is the expensive step, and the common
// legitimate statement -- a mutator on an addressable local -- never needs
// it.
func (c *scriptChecker) mutatorDiscardVerdict(function string, stmt *ExprStmt) func() {
	if _, carriesResult := c.resultCarryingStatements[stmt]; carriesResult {
		return nil
	}
	member, call := discardedMemberCall(stmt.Expr)
	if member == nil || !isCollectionMutator(member.Property) {
		return nil
	}
	temporary, root := c.mutatorReceiverShape(member.Object)
	if temporary {
		// Dispatch is untyped, so the mutator NAME is what classified the
		// call; a receiver whose type rules the collection kinds out (a
		// string's non-mutating delete) is not a collection mutation.
		if c.provablyNotCollection(member.Object) {
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
	if c.provablyNotCollection(member.Object) {
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
			// instance methods alike). Class and namespace references keep
			// their own member dispatch and stay out of scope.
			if isConstantIdentifier(typed.Name) && !c.scopeHas(typed.Name) {
				if c.script.classes[typed.Name] != nil ||
					c.recordedNamespaceMemberPrefix(typed.Name+".") {
					return false, ""
				}
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

// provablyLeavesCollectionStorage reports whether a member hop off expr
// provably dispatches ordinary member evaluation rather than reading a
// stored hash entry: every inferred arm is a plain data kind that stores no
// members. Class instances are left unproven -- their accessors also hand
// out temporaries, but the nominal arms carry too little here to build a
// warning on.
func (c *scriptChecker) provablyLeavesCollectionStorage(expr Expression) bool {
	return typeExprArmsAll(c.inferExpressionType(expr), func(arm *TypeExpr) bool {
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
