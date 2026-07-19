package runtime

import (
	"sort"
	"strings"
)

// Return summaries expose a known result fact for calls to unannotated
// script functions: a function that provably returns an int should not pass
// a string boundary just because its author omitted the annotation. A
// summary is computed once per checked script by a suppressed walk of the
// function body that records the inferred fact of every value-yielding path
// — explicit returns, the implicit final expression, and nil fallthrough —
// and joins them with the existing union rules. Any path the checker cannot
// prove (dynamic calls, recursion, unmodeled constructs) makes the whole
// summary unknown, and explicit return annotations stay authoritative.

// returnSummaryCollector accumulates the facts of every way a function body
// can yield a value during a summary walk. A single unknown path poisons the
// collector: the summary must describe every possible result or none.
type returnSummaryCollector struct {
	arms    []*TypeExpr
	unknown bool
}

type returnSummaryCacheKey struct {
	fn      *ScriptFunction
	context string
}

func (r *returnSummaryCollector) record(fact *TypeExpr) {
	if r == nil || r.unknown {
		return
	}
	if fact == nil {
		r.unknown = true
		r.arms = nil
		return
	}
	r.arms = append(r.arms, fact)
}

// sawReturn reports whether the collector reached any return path at all.
func (r *returnSummaryCollector) sawReturn() bool {
	return r != nil && (r.unknown || len(r.arms) > 0)
}

// scriptFunctionReturnSummary reports the summary for calls resolved to a
// plain function of the checked script. Summaries are computed lazily so a
// caller can recursively summarize its callees without depending on function
// name order. Annotated functions and functions of other scripts stay unknown.
func (c *scriptChecker) scriptFunctionReturnSummary(fn *ScriptFunction) *TypeExpr {
	owned, ok := c.resolveOwnedPlainFunction(fn)
	if !ok || owned.ReturnTy != nil {
		return nil
	}
	return c.functionReturnSummary(owned)
}

// resolveOwnedPlainFunction maps a resolved callee to the checked script's
// named function it dispatches to, whatever spelling the call resolved
// through (`transform` and `transform.call` dispatch to the same function).
// Callable resolution can hand back a per-call env clone
// (cloneFunctionForEnv), so a clone normalizes to its definition through the
// body both share; body identity also keeps a same-named method from
// borrowing the function's summary. Methods and other scripts' functions
// resolve to nothing, and shadowing and host overrides were already applied
// by callable resolution.
func (c *scriptChecker) resolveOwnedPlainFunction(fn *ScriptFunction) (*ScriptFunction, bool) {
	if fn == nil || fn.owner != c.script {
		return nil, false
	}
	owned, ok := c.script.functions[fn.Name]
	if !ok {
		return nil, false
	}
	if owned == fn {
		return owned, true
	}
	if len(owned.Body) > 0 && len(fn.Body) == len(owned.Body) && &fn.Body[0] == &owned.Body[0] {
		return owned, true
	}
	return nil, false
}

// functionReturnSummary computes (and caches) the summary for one function.
// A recursive or mutually recursive call reaches the in-progress guard,
// infers as unknown, and poisons the dependent summary, so cycles terminate
// with the conservative answer.
func (c *scriptChecker) functionReturnSummary(fn *ScriptFunction) *TypeExpr {
	if fn == nil || fn.ReturnTy != nil {
		return nil
	}
	key := c.returnSummaryCacheKey(fn)
	if summary, ok := c.returnSummaries[key]; ok {
		return summary
	}
	if c.summaryInProgress == nil {
		c.summaryInProgress = make(map[returnSummaryCacheKey]struct{})
	}
	if _, busy := c.summaryInProgress[key]; busy {
		return nil
	}
	c.summaryInProgress[key] = struct{}{}
	defer delete(c.summaryInProgress, key)

	var summary *TypeExpr
	if len(fn.Body) == 0 {
		summary = checkTypeNil
	} else {
		collector := c.collectFunctionReturnFacts(fn)
		if !collector.unknown && len(collector.arms) > 0 {
			summary = unionTypeExprs(collector.arms...)
		}
	}
	if c.returnSummaries == nil {
		c.returnSummaries = make(map[returnSummaryCacheKey]*TypeExpr)
	}
	c.returnSummaries[key] = summary
	return summary
}

// returnSummaryCacheKey separates summaries computed under different runtime
// bindings. A required module or a reassigned namespace member can change a
// callee from statically known to dynamic, so reusing a fact across those
// states would make the checker unsound.
func (c *scriptChecker) returnSummaryCacheKey(fn *ScriptFunction) returnSummaryCacheKey {
	root := c.runtimeTypeRoot
	if root == nil {
		root = c.typeRoot
	}
	context := moduleCheckContextKey(root)
	if len(c.runtimeNamespaceMembers) > 0 {
		members := make([]string, 0, len(c.runtimeNamespaceMembers))
		for member := range c.runtimeNamespaceMembers {
			members = append(members, member)
		}
		sort.Strings(members)
		context += "\x00" + strings.Join(members, "\x00")
	}
	return returnSummaryCacheKey{fn: fn, context: context}
}

// collectFunctionReturnFacts walks the function body with warnings
// suppressed and every piece of walk state walled off, recording return-site
// facts as the walk reaches them and implicit-final facts afterwards.
func (c *scriptChecker) collectFunctionReturnFacts(fn *ScriptFunction) *returnSummaryCollector {
	collector := &returnSummaryCollector{}
	c.withSuppressedWarnings(func() {
		runtimeState := c.snapshotRuntimeState()
		defer c.restoreRuntimeState(runtimeState)

		previousScopes := c.scopes
		previousLocalTypes := c.localTypes
		previousLive := c.liveLocalNames
		previousUnions := c.localNameUnions
		previousDepth := c.mutationRegionDepth
		previousArgFacts := c.callArgumentFacts
		previousDeferred := c.deferredReturnSites
		previousLeaves := c.implicitReturnLeaves
		previousStates := c.implicitReturnStates
		previousDecisions := c.implicitIfDecisions
		previousCollector := c.returnCollector
		restoreFresh := c.withFreshLocalInferenceScope()
		c.scopes = nil
		c.localTypes = nil
		c.liveLocalNames = nil
		c.localNameUnions = nil
		c.mutationRegionDepth = 0
		c.callArgumentFacts = nil
		c.deferredReturnSites = nil
		c.returnCollector = collector
		leaves := make(map[Statement]struct{})
		collectImplicitReturnLeaves(fn.Body, leaves)
		c.implicitReturnLeaves = leaves
		c.implicitReturnStates = make(map[Statement]checkStateSnapshot, len(leaves))
		c.implicitIfDecisions = make(map[*IfStmt][]conditionDecision)
		defer func() {
			c.scopes = previousScopes
			c.localTypes = previousLocalTypes
			c.liveLocalNames = previousLive
			c.localNameUnions = previousUnions
			c.mutationRegionDepth = previousDepth
			c.callArgumentFacts = previousArgFacts
			c.deferredReturnSites = previousDeferred
			c.implicitReturnLeaves = previousLeaves
			c.implicitReturnStates = previousStates
			c.implicitIfDecisions = previousDecisions
			c.returnCollector = previousCollector
			restoreFresh()
		}()

		popScope := c.pushScope(make(map[string]struct{}))
		defer popScope()
		popNameScope := c.pushFunctionNameScope(fn)
		defer popNameScope()
		for _, param := range fn.Params {
			c.recordParamBinding(param)
		}
		if c.checkStatements(fn.Name, nil, fn.Body) {
			c.collectImplicitResultFacts(fn.Body)
		}
	})
	return collector
}

// recordDeferredReturnSummaryFacts records returns after a non-exiting
// ensure has applied its mutation effects. The return expression itself is
// inferred under the state at which it was evaluated, while monotone local
// poisoning from the ensure remains live so a mutated returned container
// cannot retain stale structural facts.
func (c *scriptChecker) recordDeferredReturnSummaryFacts(sites []deferredReturnSite) {
	runtimeState := c.snapshotRuntimeState()
	scopeState := c.snapshotScopeState()
	defer func() {
		c.restoreRuntimeState(runtimeState)
		c.restoreScopeState(scopeState)
	}()

	for _, site := range sites {
		c.restoreRuntimeState(site.runtimeState)
		c.restoreScopeState(site.scopeState)
		if site.stmt.Value == nil {
			c.returnCollector.record(checkTypeNil)
			continue
		}
		c.returnCollector.record(c.inferExpressionType(site.stmt.Value))
	}
}

// collectImplicitResultFacts mirrors checkImplicitFinalBlock, recording the
// implicit result facts of a fallen-through block instead of warning.
func (c *scriptChecker) collectImplicitResultFacts(statements []Statement) {
	if len(statements) == 0 {
		c.returnCollector.record(checkTypeNil)
		return
	}
	c.collectImplicitResultStatementFacts(effectiveFinalStatement(statements))
}

func (c *scriptChecker) collectImplicitResultStatementFacts(stmt Statement) {
	switch typed := stmt.(type) {
	case *ReturnStmt, *RaiseStmt:
		// Explicit returns were recorded during the walk; a raising final
		// statement yields no value.
		return
	case *ExprStmt:
		c.recordImplicitLeafFact(typed, typed.Expr)
	case *AssignStmt:
		c.recordImplicitLeafFact(typed, typed.Value)
	case *IfStmt:
		c.collectImplicitResultIfFacts(typed)
	case *ForStmt, *WhileStmt, *UntilStmt:
		// A loop's value depends on break semantics the summary does not
		// model, so a loop-final body stays unknown.
		c.returnCollector.record(nil)
	case *TryStmt:
		if blockAlwaysExits(typed.Ensure) {
			return
		}
		if len(typed.Else) > 0 && !blockAlwaysExits(typed.Body) {
			c.collectImplicitResultFacts(typed.Else)
		} else {
			c.collectImplicitResultFacts(typed.Body)
		}
		for i := range typed.Rescues {
			clause := &typed.Rescues[i]
			c.collectImplicitResultFacts(clause.Body)
		}
	default:
		// A construct the implicit-return model does not cover keeps the
		// summary unknown rather than guessing.
		c.returnCollector.record(nil)
	}
}

// collectImplicitResultIfFacts mirrors checkImplicitFinalIfStatement: every
// reachable branch contributes its final fact, and a missing else adds the
// nil fallthrough arm.
func (c *scriptChecker) collectImplicitResultIfFacts(stmt *IfStmt) {
	if stmt == nil {
		c.returnCollector.record(checkTypeNil)
		return
	}
	truthy, known := c.implicitConditionDecision(stmt, 0, stmt.Condition)
	if !known || truthy {
		c.collectImplicitResultFacts(stmt.Consequent)
		if known {
			return
		}
	}
	for i, elseIf := range stmt.ElseIf {
		truthy, known = c.implicitConditionDecision(stmt, i+1, elseIf.Condition)
		if known && !truthy {
			continue
		}
		c.collectImplicitResultFacts(elseIf.Consequent)
		if known {
			return
		}
	}
	if len(stmt.Alternate) == 0 {
		c.returnCollector.record(checkTypeNil)
		return
	}
	c.collectImplicitResultFacts(stmt.Alternate)
}

// recordImplicitLeafFact records a leaf expression's fact under the state
// captured at the leaf's own walk, adding the nil arm when the expression
// can implicitly yield nil.
func (c *scriptChecker) recordImplicitLeafFact(stmt Statement, expr Expression) {
	if expressionCanImplicitlyYieldNil(expr) {
		c.returnCollector.record(checkTypeNil)
	}
	state, ok := c.implicitReturnStates[stmt]
	if !ok {
		c.returnCollector.record(c.inferExpressionType(expr))
		return
	}
	currentRuntime := c.snapshotRuntimeState()
	currentScope := c.snapshotScopeState()
	c.restoreRuntimeState(state.runtimeState)
	c.restoreScopeState(state.scopeState)
	c.returnCollector.record(c.inferExpressionType(expr))
	c.restoreRuntimeState(currentRuntime)
	c.restoreScopeState(currentScope)
}
