package runtime

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

// summarizeScriptFunctionReturns computes return summaries for every plain
// script function before the main walk, so call-site inference only ever
// reads the cache and never nests a summary walk inside a live function walk.
func (c *scriptChecker) summarizeScriptFunctionReturns() {
	for _, fn := range c.sortedScriptFunctions() {
		c.withFreshRuntimeTypeRootForCallable(fn, func() {
			c.functionReturnSummary(fn)
		})
	}
}

// scriptFunctionReturnSummary reports the cached summary for calls resolved
// to a plain function of the checked script; annotated functions and
// functions of other scripts stay unknown.
func (c *scriptChecker) scriptFunctionReturnSummary(name string, fn *ScriptFunction) *TypeExpr {
	if fn == nil || fn.ReturnTy != nil {
		return nil
	}
	if owned, ok := c.script.functions[name]; !ok || owned != fn {
		return nil
	}
	return c.returnSummaries[fn]
}

// functionReturnSummary computes (and caches) the summary for one function.
// A recursive or mutually recursive call reaches the in-progress guard,
// infers as unknown, and poisons the dependent summary, so cycles terminate
// with the conservative answer.
func (c *scriptChecker) functionReturnSummary(fn *ScriptFunction) *TypeExpr {
	if fn == nil || fn.ReturnTy != nil {
		return nil
	}
	if summary, ok := c.returnSummaries[fn]; ok {
		return summary
	}
	if c.summaryInProgress == nil {
		c.summaryInProgress = make(map[*ScriptFunction]struct{})
	}
	if _, busy := c.summaryInProgress[fn]; busy {
		return nil
	}
	c.summaryInProgress[fn] = struct{}{}
	defer delete(c.summaryInProgress, fn)

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
		c.returnSummaries = make(map[*ScriptFunction]*TypeExpr)
	}
	c.returnSummaries[fn] = summary
	return summary
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
