package runtime

import (
	"fmt"
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
	arms              []*TypeExpr
	unknown           bool
	failureParamFacts map[string]checkReachableParamFact
}

type returnSummaryCacheKey struct {
	fn      *ScriptFunction
	context string
}

type functionReturnAnalysis struct {
	result            *TypeExpr
	mayComplete       bool
	bodyMayComplete   bool
	failureParamFacts map[string]checkReachableParamFact
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
func (c *scriptChecker) scriptFunctionReturnSummary(call *CallExpr, fn *ScriptFunction) *TypeExpr {
	owned, ok := c.resolveOwnedPlainFunction(fn)
	if !ok || owned.ReturnTy != nil {
		return nil
	}
	runnable, hashSupplied, definite := callRunnableDefaults(call, owned)
	paramFacts := c.summaryCallParamFacts(call, staticCallable{fn: owned}, definite)
	return c.functionReturnAnalysis(
		owned,
		runnable,
		hashSupplied,
		definite,
		paramFacts,
		callMaySupplyBlock(call),
	).result
}

// scriptFunctionCallMayComplete reports whether an owned script function has
// any normal result for this call shape. Foreign functions and recursive
// in-progress analyses remain gradual.
func (c *scriptChecker) scriptFunctionCallMayComplete(
	call *CallExpr,
	fn *ScriptFunction,
	ignoreReturnType bool,
) bool {
	if fn == nil || fn.owner != c.script {
		return true
	}
	runnable, hashSupplied, definite := callRunnableDefaults(call, fn)
	paramFacts := c.summaryCallParamFacts(call, staticCallable{fn: fn, constructor: ignoreReturnType}, definite)
	analysis := c.functionReturnAnalysis(
		fn,
		runnable,
		hashSupplied,
		definite,
		paramFacts,
		callMaySupplyBlock(call),
	)
	if ignoreReturnType {
		return analysis.bodyMayComplete
	}
	return analysis.mayComplete
}

func (c *scriptChecker) summaryCallParamFacts(
	call *CallExpr,
	target staticCallable,
	definite bool,
) map[string]checkReachableParamFact {
	if !definite || call == nil {
		return nil
	}
	return c.reachableCallParamFacts(call, target)
}

func callMaySupplyBlock(call *CallExpr) bool {
	return call != nil && (call.Block != nil || call.BlockArg != nil)
}

func (c *scriptChecker) scriptCallFailureArgumentFacts(expr Expression) map[string]checkReachableParamFact {
	call, ok := expr.(*CallExpr)
	if !ok || call == nil || callExpandsArguments(call) {
		return nil
	}
	target, resolved := c.resolveCallable(call)
	if !resolved || target.fn == nil || target.fn.owner != c.script ||
		!c.scriptCallBindingPlan(call, target).bodyMayEnter ||
		staticCallCollapsesOptionsHash(call, target) {
		return nil
	}
	runnable, hashSupplied, definite := callRunnableDefaults(call, target.fn)
	paramFacts := c.summaryCallParamFacts(call, target, definite)
	analysis := c.functionReturnAnalysis(
		target.fn,
		runnable,
		hashSupplied,
		definite,
		paramFacts,
		callMaySupplyBlock(call),
	)
	if len(analysis.failureParamFacts) == 0 {
		return nil
	}
	view := staticCallViewFor(call, target)
	result := make(map[string]checkReachableParamFact)
	ambiguous := make(map[string]struct{})
	bind := func(param Param, arg Expression) {
		if param.Name == "" || param.Kind == ParamRest || param.Kind == ParamKeywordRest {
			return
		}
		ident, ok := arg.(*Identifier)
		if !ok || !typeExprHasContainerArm(c.localTypeFor(ident.Name)) {
			return
		}
		if _, skip := ambiguous[ident.Name]; skip {
			return
		}
		fact, ok := analysis.failureParamFacts[param.Name]
		if !ok {
			return
		}
		if _, duplicate := result[ident.Name]; duplicate {
			delete(result, ident.Name)
			ambiguous[ident.Name] = struct{}{}
			return
		}
		result[ident.Name] = fact
	}
	for i, arg := range view.args {
		if param, ok := positionalCallableParam(target.fn.Params, i); ok {
			bind(param, arg)
		}
	}
	for _, kwarg := range view.kwargs {
		if kwarg.Splat {
			continue
		}
		for _, param := range target.fn.Params {
			if param.Name == kwarg.Name && (param.Kind == ParamNormal || param.Kind == ParamKeyword) {
				bind(param, kwarg.Value)
				break
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// callRunnableDefaults reports the defaulted parameter indices this call
// shape may leave unsupplied, so the summary walk evaluates exactly the
// defaults the runtime would, plus the indices a collapsed keyword options
// hash supplies instead (those parameters always bind a hash for this
// shape). definite reports whether those conclusions provably hold: a
// literal shape omits or supplies its parameters outright, while a splatted
// shape only may, so no value facts can bind. A nil call (a bare
// auto-invoke) passes no arguments, so every default runs.
func callRunnableDefaults(call *CallExpr, fn *ScriptFunction) ([]int, []int, bool) {
	var indices []int
	var hashSupplied []int
	collapseOptionsHash := call != nil && staticCallCollapsesOptionsHash(call, staticCallable{fn: fn})
	for i, param := range fn.Params {
		if call == nil {
			if param.DefaultVal != nil {
				indices = append(indices, i)
			}
			continue
		}
		optionsHash, mayDefault := callParamSupply(call, fn, i, collapseOptionsHash)
		if optionsHash {
			hashSupplied = append(hashSupplied, i)
		} else if mayDefault && param.DefaultVal != nil {
			indices = append(indices, i)
		}
	}
	return indices, hashSupplied, call == nil || !callExpandsArguments(call)
}

// resolveOwnedPlainFunction maps a resolved callee to the checked script's
// named function it dispatches to, whatever spelling the call resolved
// through (`transform` and `transform.call` dispatch to the same function).
// Callable resolution can hand back a per-call env clone
// (cloneFunctionForEnv), so a clone normalizes to its definition through the
// declaration position both share; within one script the position also keeps
// a same-named method from borrowing the function's summary. Methods and
// other scripts' functions resolve to nothing, and shadowing and host
// overrides were already applied by callable resolution.
func (c *scriptChecker) resolveOwnedPlainFunction(fn *ScriptFunction) (*ScriptFunction, bool) {
	if fn == nil || fn.owner != c.script {
		return nil, false
	}
	owned, ok := c.script.functions[fn.Name]
	if !ok {
		return nil, false
	}
	if owned == fn || owned.Pos == fn.Pos {
		return owned, true
	}
	return nil, false
}

func (c *scriptChecker) functionReturnAnalysis(
	fn *ScriptFunction,
	runnableDefaults, hashSuppliedParams []int,
	definiteDefaults bool,
	paramFacts map[string]checkReachableParamFact,
	blockAvailable bool,
) functionReturnAnalysis {
	if fn == nil {
		return functionReturnAnalysis{mayComplete: true, bodyMayComplete: true}
	}
	key := c.returnSummaryCacheKey(
		fn,
		runnableDefaults,
		hashSuppliedParams,
		definiteDefaults,
		paramFacts,
		blockAvailable,
	)
	if analysis, ok := c.returnAnalyses[key]; ok {
		return analysis
	}
	if c.summaryInProgress == nil {
		c.summaryInProgress = make(map[returnSummaryCacheKey]struct{})
	}
	if _, busy := c.summaryInProgress[key]; busy {
		return functionReturnAnalysis{mayComplete: true, bodyMayComplete: true}
	}
	c.summaryInProgress[key] = struct{}{}
	defer delete(c.summaryInProgress, key)

	collector := c.collectFunctionReturnFacts(
		fn,
		runnableDefaults,
		hashSuppliedParams,
		definiteDefaults,
		paramFacts,
		blockAvailable,
	)
	analysis := functionReturnAnalysis{
		mayComplete:       collector.unknown || len(collector.arms) > 0,
		bodyMayComplete:   collector.unknown || len(collector.arms) > 0,
		failureParamFacts: cloneReachableParamFacts(collector.failureParamFacts),
	}
	if !collector.unknown && len(collector.arms) > 0 {
		if fn.ReturnTy == nil {
			analysis.result = unionTypeExprs(collector.arms...)
		} else if validateTypeExprResolved(fn.ReturnTy, c.runtimeTypeContext()) == nil {
			analysis.mayComplete = false
			for _, arm := range collector.arms {
				if !typeExprsDisjoint(arm, fn.ReturnTy, c.checkNamedTypeResolver()) {
					analysis.mayComplete = true
					break
				}
			}
		}
	}
	if c.returnAnalyses == nil {
		c.returnAnalyses = make(map[returnSummaryCacheKey]functionReturnAnalysis)
	}
	c.returnAnalyses[key] = analysis
	return analysis
}

// returnSummaryCacheKey separates summaries computed under different runtime
// bindings. A required module or a reassigned namespace member can change a
// callee from statically known to dynamic, so reusing a fact across those
// states would make the checker unsound; different call shapes run different
// parameter defaults, so their effects separate the key too.
func (c *scriptChecker) returnSummaryCacheKey(
	fn *ScriptFunction,
	runnableDefaults, hashSuppliedParams []int,
	definiteDefaults bool,
	paramFacts map[string]checkReachableParamFact,
	blockAvailable bool,
) returnSummaryCacheKey {
	context := c.runtimeCheckContextKey()
	if len(runnableDefaults) > 0 {
		context += "\x01defaults:" + fmt.Sprint(runnableDefaults)
		if definiteDefaults {
			context += "!"
		}
	}
	if len(hashSuppliedParams) > 0 {
		context += "\x01options:" + fmt.Sprint(hashSuppliedParams)
		if definiteDefaults {
			context += "!"
		}
	}
	if factsKey := reachableParamFactsKey(paramFacts); factsKey != "" {
		context += "\x01params:" + factsKey
	}
	if blockAvailable {
		context += "\x01block"
	}
	return returnSummaryCacheKey{fn: fn, context: context}
}

// collectFunctionReturnFacts walks the function body with warnings
// suppressed and every piece of walk state walled off, recording return-site
// facts as the walk reaches them and implicit-final facts afterwards. Only
// the defaults the summarized call shape may evaluate are walked.
func (c *scriptChecker) collectFunctionReturnFacts(
	fn *ScriptFunction,
	runnableDefaults, hashSuppliedParams []int,
	definiteDefaults bool,
	paramFacts map[string]checkReachableParamFact,
	blockAvailable bool,
) *returnSummaryCollector {
	collector := &returnSummaryCollector{}
	c.withSuppressedWarnings(func() {
		runtimeState := c.snapshotRuntimeState()
		defer c.restoreRuntimeState(runtimeState)

		previousScopes := c.scopes
		previousLocalTypes := c.localTypes
		previousLocalClassValues := c.localClassValues
		previousLive := c.liveLocalNames
		previousUnions := c.localNameUnions
		previousDepth := c.mutationRegionDepth
		previousArgFacts := c.callArgumentFacts
		previousArgClassValues := c.callArgumentClassValues
		previousArgCallables := c.callArgumentCallables
		previousArgStaticValues := c.callArgumentStaticValues
		previousReachableParamFacts := c.reachableParamFacts
		previousDeferred := c.deferredReturnSites
		previousExceptionExits := c.exceptionExitSites
		previousExpressionExits := c.expressionExitSites
		previousNonLocalReturnExits := c.nonLocalReturnExitSites
		previousEnsureExits := c.ensureExitSites
		previousExpressionReturnsNonLocally := c.expressionReturnsNonLocally
		previousClassConstantCaptures := c.classConstantCaptures
		previousLoopExitEffects := c.loopExitEffects
		previousLeaves := c.implicitReturnLeaves
		previousStates := c.implicitReturnStates
		previousDecisions := c.implicitIfDecisions
		previousCollector := c.returnCollector
		previousYieldCollector := c.summaryYieldCollector
		previousYieldBlock := c.summaryYieldBlock
		previousYieldsActive := c.summaryYieldsActive
		previousBlockAvailable := c.summaryBlockAvailable
		previousPinned := c.pinnedExpressionFacts
		previousReachableChecks := c.checkReachableCalls
		restoreFresh := c.withFreshLocalInferenceScope()
		c.scopes = nil
		c.localTypes = nil
		c.localClassValues = nil
		c.liveLocalNames = nil
		c.localNameUnions = nil
		c.mutationRegionDepth = 0
		c.callArgumentFacts = nil
		c.callArgumentClassValues = nil
		c.callArgumentCallables = nil
		c.callArgumentStaticValues = nil
		c.reachableParamFacts = cloneReachableParamFacts(paramFacts)
		c.deferredReturnSites = nil
		var exceptionExitSites []checkStateSnapshot
		c.exceptionExitSites = &exceptionExitSites
		var expressionExitSites []checkStateSnapshot
		c.expressionExitSites = &expressionExitSites
		c.nonLocalReturnExitSites = nil
		c.ensureExitSites = nil
		c.expressionReturnsNonLocally = false
		c.classConstantCaptures = nil
		c.loopExitEffects = nil
		c.returnCollector = collector
		c.summaryYieldCollector = collector
		c.summaryYieldBlock = nil
		c.summaryYieldsActive = true
		c.summaryBlockAvailable = blockAvailable
		c.pinnedExpressionFacts = nil
		// Summary inference may inspect calls on paths the real checker has
		// already proved unreachable. Those synthetic walks must not enqueue
		// callees or mark them checked under the speculative runtime state.
		c.checkReachableCalls = false
		leaves := make(map[Statement]struct{})
		collectImplicitReturnLeaves(fn.Body, leaves)
		c.implicitReturnLeaves = leaves
		c.implicitReturnStates = make(map[Statement]checkStateSnapshot, len(leaves))
		c.implicitIfDecisions = make(map[*IfStmt][]conditionDecision)
		defer func() {
			c.scopes = previousScopes
			c.localTypes = previousLocalTypes
			c.localClassValues = previousLocalClassValues
			c.liveLocalNames = previousLive
			c.localNameUnions = previousUnions
			c.mutationRegionDepth = previousDepth
			c.callArgumentFacts = previousArgFacts
			c.callArgumentClassValues = previousArgClassValues
			c.callArgumentCallables = previousArgCallables
			c.callArgumentStaticValues = previousArgStaticValues
			c.reachableParamFacts = previousReachableParamFacts
			c.deferredReturnSites = previousDeferred
			c.exceptionExitSites = previousExceptionExits
			c.expressionExitSites = previousExpressionExits
			c.nonLocalReturnExitSites = previousNonLocalReturnExits
			c.ensureExitSites = previousEnsureExits
			c.expressionReturnsNonLocally = previousExpressionReturnsNonLocally
			c.classConstantCaptures = previousClassConstantCaptures
			c.loopExitEffects = previousLoopExitEffects
			c.implicitReturnLeaves = previousLeaves
			c.implicitReturnStates = previousStates
			c.implicitIfDecisions = previousDecisions
			c.returnCollector = previousCollector
			c.summaryYieldCollector = previousYieldCollector
			c.summaryYieldBlock = previousYieldBlock
			c.summaryYieldsActive = previousYieldsActive
			c.summaryBlockAvailable = previousBlockAvailable
			c.pinnedExpressionFacts = previousPinned
			c.checkReachableCalls = previousReachableChecks
			restoreFresh()
		}()

		popScope := c.pushScope(make(map[string]struct{}))
		defer popScope()
		popNameScope := c.pushFunctionNameScope(fn)
		defer popNameScope()
		runnable := make(map[int]struct{}, len(runnableDefaults))
		for _, index := range runnableDefaults {
			runnable[index] = struct{}{}
		}
		hashSupplied := make(map[int]struct{}, len(hashSuppliedParams))
		for _, index := range hashSuppliedParams {
			hashSupplied[index] = struct{}{}
		}
		if definiteDefaults {
			c.linkReachableParamAliases(fn.Params)
		}
		for i, param := range fn.Params {
			expectation := typeExpressionExpectation(param.Type)
			// An omitted argument runs the default expression before the
			// body, so its effects (a require's exports, a namespace write)
			// must be live for the body walk, mirroring checkFunction. A
			// default the summarized call shape provably supplies never
			// runs and contributes nothing.
			_, mayRun := runnable[i]
			if mayRun {
				completed := c.checkExpressionWithExpectation(fn.Name, param.DefaultVal, expectation)
				c.collectRuntimeRequireCallExportsFromExpression(param.DefaultVal)
				if definiteDefaults && !completed {
					return
				}
			}
			c.recordParamBinding(param)
			if !definiteDefaults {
				continue
			}
			c.applyReachableParamFact(param)
			// A literal call shape omits or supplies the parameter outright:
			// an omitted default is exactly the value the runtime binds, and
			// a collapsed keyword options hash always binds a hash. A
			// splatted shape may do either, so no value fact holds.
			if mayRun {
				c.bindParamDefaultFact(param)
				c.refineAnnotatedParamFact(param, c.inferExpressionTypeWithExpectation(param.DefaultVal, expectation))
			} else if _, viaHash := hashSupplied[i]; viaHash {
				if param.Name != "" && param.Type == nil {
					c.bindLocalTypeInCurrentFrame(param.Name, checkTypeHash)
				} else {
					c.refineAnnotatedParamFact(param, checkTypeHash)
				}
			}
		}
		if c.checkStatements(fn.Name, nil, fn.Body) {
			c.collectImplicitResultFacts(fn.Body)
		}
		rebound := make(map[string]struct{})
		collectLocalBindings(fn.Body, rebound)
		failureExitSites := make(
			[]checkStateSnapshot,
			0,
			len(expressionExitSites)+len(exceptionExitSites),
		)
		failureExitSites = append(failureExitSites, expressionExitSites...)
		failureExitSites = append(failureExitSites, exceptionExitSites...)
		collector.failureParamFacts = c.mergedFailureParamFacts(fn.Params, failureExitSites, rebound)
	})
	return collector
}

func (c *scriptChecker) mergedFailureParamFacts(
	params []Param,
	sites []checkStateSnapshot,
	rebound map[string]struct{},
) map[string]checkReachableParamFact {
	if len(sites) == 0 {
		return nil
	}
	result := make(map[string]checkReachableParamFact)
	for _, param := range params {
		if param.Name == "" {
			continue
		}
		if _, assigned := rebound[param.Name]; assigned {
			continue
		}
		facts := make([]checkReachableParamFact, 0, len(sites))
		complete := true
		for _, site := range sites {
			if _, poisoned := site.typePoison[param.Name]; poisoned {
				complete = false
				break
			}
			fact, ok := scopeStateParamFact(site.scopeState, param.Name)
			if !ok {
				complete = false
				break
			}
			if _, poisoned := site.staticValuePoison[param.Name]; poisoned {
				fact.staticVals = nil
			}
			facts = append(facts, fact)
		}
		if !complete {
			continue
		}
		fact, ok := c.mergeFailureParamFactAlternatives(facts)
		if ok {
			result[param.Name] = fact
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func scopeStateParamFact(state checkScopeState, name string) (checkReachableParamFact, bool) {
	for i := len(state.types) - 1; i >= 0; i-- {
		ty, tracked := state.types[i][name]
		if !tracked {
			continue
		}
		fact := checkReachableParamFact{typeExpr: ty}
		if i < len(state.classValues) {
			value := state.classValues[i][name]
			fact.classNames = append([]string(nil), value.classNames...)
			fact.callables = append([]*ScriptFunction(nil), value.callables...)
			fact.staticVals = append([]Expression(nil), value.staticVals...)
		}
		return fact, true
	}
	return checkReachableParamFact{}, false
}

func (c *scriptChecker) mergeFailureParamFactAlternatives(
	facts []checkReachableParamFact,
) (checkReachableParamFact, bool) {
	if len(facts) == 0 {
		return checkReachableParamFact{}, false
	}
	merged := checkReachableParamFact{typeExpr: facts[0].typeExpr}
	knownType := merged.typeExpr != nil
	staticExact := len(facts[0].staticVals) > 0
	classExact := len(facts[0].classNames) > 0
	callableExact := len(facts[0].callables) > 0
	merged.staticVals = append([]Expression(nil), facts[0].staticVals...)
	merged.classNames = append([]string(nil), facts[0].classNames...)
	merged.callables = append([]*ScriptFunction(nil), facts[0].callables...)
	for _, fact := range facts[1:] {
		if knownType {
			if fact.typeExpr == nil {
				knownType = false
				merged.typeExpr = nil
			} else {
				merged.typeExpr = unionTypeExprs(merged.typeExpr, fact.typeExpr)
				knownType = merged.typeExpr != nil
			}
		}
		if staticExact {
			if len(fact.staticVals) == 0 {
				staticExact = false
				merged.staticVals = nil
			} else {
				merged.staticVals = c.normalizeCheckStaticValues(append(merged.staticVals, fact.staticVals...))
			}
		}
		if classExact {
			if len(fact.classNames) == 0 {
				classExact = false
				merged.classNames = nil
			} else {
				merged.classNames = normalizeCheckClassNames(append(merged.classNames, fact.classNames...))
			}
		}
		if callableExact {
			if len(fact.callables) == 0 {
				callableExact = false
				merged.callables = nil
			} else {
				merged.callables = normalizeCheckCallables(append(merged.callables, fact.callables...))
			}
		}
	}
	return merged, knownType || staticExact || classExact || callableExact
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
		if site.stmt == nil {
			continue
		}
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
		if typed.Operator == "" {
			c.recordImplicitLeafFact(typed, typed.Value)
		} else {
			// A compound or logical assignment yields its combined result
			// (x ||= y is x-or-y, x += y is x plus y), which is the
			// target's post-assignment fact, not the bare right-hand side.
			c.recordImplicitLeafFact(typed, typed.Target)
		}
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
		if statementsProvenNonRaising(typed.Body) {
			return
		}
		selected, exact := c.staticallySelectedRescue(typed.Body, typed.Rescues)
		for i := range typed.Rescues {
			if exact && i != selected {
				continue
			}
			clause := &typed.Rescues[i]
			// An empty matched clause propagates the error after ensure
			// instead of yielding a value (the fallthrough merge skips these
			// clauses the same way), so it contributes no arm.
			if len(clause.Body) == 0 {
				continue
			}
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
	state, ok := c.implicitReturnStates[stmt]
	if !ok && c.implicitReturnStates != nil {
		// The real walk did not reach a normal result for this syntactic
		// leaf, so it cannot contribute to the function's return summary.
		return
	}
	if expressionCanImplicitlyYieldNil(expr) {
		c.returnCollector.record(checkTypeNil)
	}
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
