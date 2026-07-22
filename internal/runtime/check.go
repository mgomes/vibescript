package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// CheckWarning describes a statically checkable contract issue.
type CheckWarning struct {
	Function string
	Pos      Position
	Message  string
	// Source is the file path of the required module the warning originates
	// in; empty for warnings in the checked script itself.
	Source string
}

// CheckWarnings returns statically checkable contract issues for the compiled
// script. It reports only facts that are known from the AST and compiled script
// metadata; dynamic calls remain runtime-checked.
func (s *Script) CheckWarnings() []CheckWarning {
	return s.CheckWarningsWithOptions(CallOptions{})
}

// CheckWarningsWithOptions returns statically checkable contract issues using
// the same host globals that a later Call would receive.
func (s *Script) CheckWarningsWithOptions(opts CallOptions) []CheckWarning {
	return s.checkWarnings(opts, checkTarget{})
}

// CheckWarningsForFunction returns statically checkable contract issues for the
// execution path of a single function call.
func (s *Script) CheckWarningsForFunction(name string) []CheckWarning {
	return s.CheckWarningsForFunctionWithOptions(name, CallOptions{})
}

// CheckWarningsForFunctionWithOptions returns statically checkable contract
// issues for a single function call using the same host globals that Call would
// receive.
func (s *Script) CheckWarningsForFunctionWithOptions(name string, opts CallOptions) []CheckWarning {
	return s.checkWarnings(opts, checkTarget{Function: name})
}

// CheckWarningsForCall returns statically checkable contract issues for a
// single function call, including host-supplied arguments and keywords.
func (s *Script) CheckWarningsForCall(name string, args []Value, opts CallOptions) []CheckWarning {
	return s.checkWarnings(opts, checkTarget{
		Function:     name,
		Args:         args,
		ValidateCall: true,
	})
}

type checkTarget struct {
	Function     string
	Args         []Value
	ValidateCall bool
}

func (s *Script) checkWarnings(opts CallOptions, target checkTarget) []CheckWarning {
	return s.checkWarningsMode(context.Background(), opts, target, false)
}

func (s *Script) checkWarningsMode(ctx context.Context, opts CallOptions, target checkTarget, orderIndependentOnly bool) []CheckWarning {
	optionGlobals, _ := checkOptionGlobals(ctx, s, opts)
	return s.checkWarningsWithGlobals(optionGlobals, opts, target, orderIndependentOnly)
}

func (s *Script) checkWarningsWithGlobals(optionGlobals map[string]Value, opts CallOptions, target checkTarget, orderIndependentOnly bool) []CheckWarning {
	if s == nil {
		return nil
	}
	checker := scriptChecker{
		script:                s,
		callOptions:           opts,
		optionGlobals:         optionGlobals,
		optionGlobalsOverride: true,
		typeRoot:              checkTypeRoot(s, optionGlobals),
		runtimeTypeRoot:       checkTypeRoot(s, optionGlobals),
		hostGlobals:           checkHostGlobals(optionGlobals),
		orderIndependentOnly:  orderIndependentOnly,
	}
	checker.moduleExportRoot = checker.typeRoot
	if depthWarnings := checker.nestingDepthWarnings(); len(depthWarnings) > 0 {
		checker.warnings = depthWarnings
	} else if target.Function == "" {
		checker.checkScript()
	} else {
		checker.checkFunctionExecution(target)
	}
	sort.SliceStable(checker.warnings, func(i, j int) bool {
		if checker.warnings[i].Pos.Line != checker.warnings[j].Pos.Line {
			return checker.warnings[i].Pos.Line < checker.warnings[j].Pos.Line
		}
		if checker.warnings[i].Pos.Column != checker.warnings[j].Pos.Column {
			return checker.warnings[i].Pos.Column < checker.warnings[j].Pos.Column
		}
		return checker.warnings[i].Function < checker.warnings[j].Function
	})
	return checker.warnings
}

type scriptChecker struct {
	script                   *Script
	callOptions              CallOptions
	optionGlobals            map[string]Value
	optionGlobalsOverride    bool
	typeRoot                 *Env
	runtimeTypeRoot          *Env
	hostGlobals              map[string]struct{}
	warnings                 []CheckWarning
	scopes                   []map[string]struct{}
	localTypes               []checkTypeFrame
	localClassValues         []checkClassValueFrame
	typePoison               map[string]struct{}
	typeAliases              map[string]map[string]struct{}
	mutationRegionDepth      int
	speculativeInference     int
	callArgumentFacts        map[Expression]*TypeExpr
	callArgumentClassValues  map[Expression][]string
	callArgumentCallables    map[Expression][]*ScriptFunction
	callArgumentStaticValues map[Expression][]Expression
	reachableParamFacts      map[string]checkReachableParamFact
	reachableBindingPlan     *scriptCallBindingPlan
	deferredReturnSites      *[]deferredReturnSite
	exceptionExitSites       *[]checkStateSnapshot
	expressionExitSites      *[]checkStateSnapshot
	ensureExitSites          *[]checkStateSnapshot
	retryExitSites           *[]checkStateSnapshot
	implicitReturnLeaves     map[Statement]struct{}
	implicitReturnStates     map[Statement]checkStateSnapshot
	returnAnalyses           map[returnSummaryCacheKey]functionReturnAnalysis
	summaryInProgress        map[returnSummaryCacheKey]struct{}
	bindingCompletionProbes  map[Expression]struct{}
	returnCollector          *returnSummaryCollector
	summaryYieldCollector    *returnSummaryCollector
	summaryYieldBlock        *BlockLiteral
	summaryYieldsActive      bool
	summaryBlockAvailable    bool
	pinnedExpressionFacts    map[Expression]*TypeExpr
	constructorInstanceFacts map[Expression]checkInstanceClassFact
	requiredModules          map[string]struct{}
	runtimeModules           map[string]struct{}
	runtimeNamespaceMembers  map[string]struct{}
	opaqueClassConstants     bool
	classConstantContext     checkClassConstantEffects
	classConstantCaptures    []checkClassConstantEffects
	loopExitEffects          *checkLoopExitEffects
	moduleEntries            map[string]moduleEntry
	moduleExportValues       map[string]Value
	moduleCheckedFunctions   map[string]struct{}
	moduleCheckContext       string
	moduleCaller             *moduleContext
	moduleExportRoot         *Env
	runtimeTypeRootParent    *Env
	checkReachableCalls      bool
	checkedReachableFuncs    map[string]struct{}
	reachableFuncQueue       []reachableFunction
	selfScope                bool
	selfClass                *ClassDef
	selfClassContext         bool
	selfScopeFnClasses       map[*ScriptFunction]*ClassDef
	selfScopeClassFns        map[*ScriptFunction]struct{}
	localNameUnions          []map[string]struct{}
	liveLocalNames           []map[string]struct{}
	nameFactsCache           *checkNameFacts
	selfScopeFns             map[*ScriptFunction]struct{}
	orderIndependentOnly     bool
	// implicitIfDecisions records, per if statement of an armed
	// implicit-return walk, the branch reachability the walker decided at
	// each condition's own evaluation point (index 0 the main condition,
	// then the elsifs in order), so the post-walk implicit-return pass
	// prunes the same unreachable arms without re-inferring under merged
	// state.
	implicitIfDecisions map[*IfStmt][]conditionDecision
	// stmtNoFallthroughInferred reports that the statement just walked
	// provably never falls through — every reachable branch exits under
	// inferred conditions — so the enclosing statement list stops like it
	// does for statically exiting statements. Statement-list loops consume
	// and reset it after every statement.
	stmtNoFallthroughInferred bool
	// isolatedCollectInference marks a module collection pass running on its
	// own walled-off type-fact environment (seedEntrypointRequireExports,
	// module entrypoints). The pass then maintains facts itself; the runtime
	// collection path stays read-only because it shares the live walk's facts,
	// which the real walk already updates at the correct evaluation points.
	isolatedCollectInference bool
}

// conditionDecision is a walker's reachability verdict for one branch
// condition, captured at that condition's evaluation point.
type conditionDecision struct {
	truthy bool
	known  bool
}

func conditionDecisionFromOutcomes(truthy, known, trueReachable, falseReachable bool) conditionDecision {
	switch {
	case trueReachable && !falseReachable:
		return conditionDecision{truthy: true, known: true}
	case !trueReachable && falseReachable:
		return conditionDecision{truthy: false, known: true}
	default:
		return conditionDecision{truthy: truthy, known: known}
	}
}

type reachableFunction struct {
	label        string
	fn           *ScriptFunction
	runtimeState checkRuntimeState
	paramFacts   map[string]checkReachableParamFact
	bindingPlan  *scriptCallBindingPlan
}

type checkReachableParamFact struct {
	typeExpr    *TypeExpr
	classNames  []string
	callables   []*ScriptFunction
	staticVals  []Expression
	usesDefault bool
}

type checkInstanceClassFact struct {
	classNames []string
	exact      bool
}

type checkDynamicCallResolution struct {
	targets              []checkDynamicCallTarget
	exact                bool
	diagnoseTargets      bool
	targetExists         bool
	targetMayEnter       bool
	nonScriptMayComplete bool
	lookupFails          bool
}

type checkDynamicCallCandidates struct {
	instanceClasses  []string
	instancesExact   bool
	instancesMayNil  bool
	classValues      []string
	classValuesExact bool
	callables        []*ScriptFunction
	callablesExact   bool
}

type checkDynamicCallTarget struct {
	call          *CallExpr
	target        staticCallable
	bindingStarts bool
	mayEnter      bool
}

type checkForwardedCallVariant struct {
	call   *CallExpr
	method string
	known  bool
	valid  bool
}

func (c *scriptChecker) callStaticValueAlternatives(expr Expression) ([]Expression, bool) {
	if values, captured := c.callArgumentStaticValues[expr]; captured {
		return append([]Expression(nil), values...), len(values) > 0
	}
	return c.staticValueExpressionAlternatives(expr)
}

type scriptCallBindingPlan struct {
	defaultParams []int
	bodyMayEnter  bool
}

// checkOptionGlobals resolves the host globals a call would receive. Bind
// failures leave the adapter's names unbound and are also returned so a
// combined check-and-call gate can surface them; the pure CheckWarnings*
// queries ignore the error and stay best-effort.
func checkOptionGlobals(ctx context.Context, script *Script, opts CallOptions) (map[string]Value, error) {
	if len(opts.Capabilities) == 0 && len(opts.Globals) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var bindErr error
	globals := make(map[string]Value, len(opts.Globals)+len(opts.Capabilities)*2)
	if script != nil {
		binding := CapabilityBinding{Context: ctx, Engine: script.engine}
		seenContracts := make(map[string]struct{})
		for _, adapter := range opts.Capabilities {
			if adapter == nil {
				continue
			}
			// Execution validates contract names before invoking Bind and
			// stops at the first failure (bindCapabilitiesForCall); the gate
			// mirrors that order so it never touches a surface the call
			// would not.
			if provider, ok := adapter.(CapabilityContractProvider); ok {
				for methodName := range provider.CapabilityContracts() {
					name := strings.TrimSpace(methodName)
					if name == "" {
						bindErr = fmt.Errorf("capability contract method name must be non-empty")
						break
					}
					if _, exists := seenContracts[name]; exists {
						bindErr = fmt.Errorf("duplicate capability contract for %s", name)
						break
					}
					seenContracts[name] = struct{}{}
				}
				if bindErr != nil {
					break
				}
			}
			bound, err := adapter.Bind(binding)
			if err != nil {
				bindErr = err
				break
			}
			// Execution checks the context after each successful bind; a
			// cancellation must stop the gate before any checker work runs.
			if err := ctx.Err(); err != nil {
				bindErr = err
				break
			}
			for name, val := range bound {
				globals[name] = val
			}
		}
	}
	for name, val := range opts.Globals {
		globals[name] = val
	}
	if len(globals) == 0 {
		return nil, bindErr
	}
	return globals, bindErr
}

func checkHostGlobals(globals map[string]Value) map[string]struct{} {
	if len(globals) == 0 {
		return nil
	}
	names := make(map[string]struct{}, len(globals))
	for name := range globals {
		names[name] = struct{}{}
	}
	return names
}

func checkTypeRoot(script *Script, globals map[string]Value) *Env {
	return checkTypeRootWithParentAndGlobals(script, globals, nil, true)
}

func checkTypeRootWithParentAndGlobals(script *Script, globals map[string]Value, parent *Env, overrideGlobals bool) *Env {
	if script == nil {
		return nil
	}
	root := newEnvWithCapacity(nil, len(script.classes)+len(globals))
	script.engine.attachBuiltins(root, len(script.functions)+len(script.enums))
	if parent != nil {
		if parent.parent == nil {
			parent = cloneCheckRoot(parent)
			parent.parent = root.parent
		}
		root.parent = parent
	}
	callFunctions := cloneFunctionsForCall(script.functions, root)
	for name, fn := range callFunctions {
		root.DefineStatic(name, NewFunction(fn))
	}
	callClasses := cloneClassesForCall(script.classes, root)
	for name, classDef := range callClasses {
		root.Define(name, NewClass(classDef))
	}
	callEnums := cloneEnumsForCall(script.enums)
	for name, enumDef := range callEnums {
		root.DefineStatic(name, NewEnum(enumDef))
	}
	rebinder := newCallFunctionRebinder(script, root, callClasses, callEnums)
	for name, val := range globals {
		if !overrideGlobals && root.hasOwnBinding(name) {
			continue
		}
		root.Define(name, rebinder.rebindValue(val))
	}
	return root
}

func (c *scriptChecker) typeContext() typeContext {
	return typeContext{owner: c.script, env: c.typeRoot, fallback: c.typeRoot}
}

func (c *scriptChecker) runtimeTypeContext() typeContext {
	if c.runtimeTypeRoot == nil {
		return c.typeContext()
	}
	return typeContext{owner: c.script, env: c.runtimeTypeRoot, fallback: c.runtimeTypeRoot}
}

func (c *scriptChecker) collectFunctionRequiredModuleExports(fn *ScriptFunction) {
	if fn == nil {
		return
	}
	restoreInference := c.withIsolatedLocalInference()
	defer restoreInference()
	popScope := c.pushScope(make(map[string]struct{}))
	defer popScope()

	for _, param := range fn.Params {
		c.collectRequiredModuleExportsFromExpression(param.DefaultVal)
		c.recordParamBinding(param)
		c.bindParamLocalType(param)
	}
	c.collectRequiredModuleExportsFromStatements(fn.Body)
}

// collectAssignLocalTypes mirrors the real walk's assignment inference during
// a module collection pass so guaranteed-evaluation gating sees the same
// local facts. Diagnostics stay suppressed: the pass only collects, and the
// real walks report assignment warnings themselves.
func (c *scriptChecker) collectAssignLocalTypes(stmt *AssignStmt) {
	if !c.isolatedCollectInference {
		return
	}
	c.withSuppressedWarnings(func() {
		c.inferAssignStatementTypes("", stmt)
	})
}

// collectRequiredModuleExportsFromStatements walks a statement list for
// module collection and reports whether it can fall through, mirroring
// checkStatements so unreachable requires stay unbound.
func (c *scriptChecker) collectRequiredModuleExportsFromStatements(statements []Statement) bool {
	for _, stmt := range statements {
		c.collectRequiredModuleExportsFromStatement(stmt)
		noFallthrough := c.stmtNoFallthroughInferred
		c.stmtNoFallthroughInferred = false
		if statementAlwaysExits(stmt) || noFallthrough {
			return false
		}
	}
	return true
}

func (c *scriptChecker) collectRequiredModuleExportsFromStatement(stmt Statement) {
	if c.isolatedCollectInference {
		c.predeclareStatementLiveNames(stmt)
		defer c.postdeclareStatementLiveNames(stmt)
	}
	switch typed := stmt.(type) {
	case nil:
		return
	case *ReturnStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Value)
	case *RaiseStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Value)
		c.collectRequiredModuleExportsFromExpression(typed.Message)
	case *BreakStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Value)
	case *AssignStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Target)
		c.collectRequiredModuleExportsFromExpression(typed.Value)
		c.collectAssignLocalTypes(typed)
		c.recordBindingTarget(typed.Target)
	case *ExprStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Expr)
	case *IfStmt:
		baseState := c.snapshotModuleCollectionState()
		baseScopeState := c.snapshotScopeState()
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		conditionState := c.snapshotModuleCollectionState()
		conditionScopeState := c.snapshotScopeState()
		fallthroughStates := make([]checkModuleCollectionState, 0, len(typed.ElseIf)+2)
		fallthroughScopeStates := make([]checkScopeState, 0, len(typed.ElseIf)+2)
		finish := func() {
			c.mergeModuleCollectionStates(baseState, fallthroughStates)
			c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
			c.stmtNoFallthroughInferred = len(fallthroughStates) == 0
		}

		conditionTruthy, conditionKnown := c.inferredConditionTruthiness(typed.Condition)
		trueReachable := !conditionKnown || conditionTruthy
		if trueReachable {
			trueReachable = c.applyConditionOutcomeEffects(typed.Condition, true, c.collectRequiredModuleExportsFromExpression)
		}
		if trueReachable {
			if c.collectRequiredModuleExportsFromStatements(typed.Consequent) {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		c.restoreModuleCollectionState(conditionState)
		c.restoreScopeState(conditionScopeState)
		falseReachable := !conditionKnown || !conditionTruthy
		if falseReachable {
			falseReachable = c.applyConditionOutcomeEffects(typed.Condition, false, c.collectRequiredModuleExportsFromExpression)
		}
		if !falseReachable {
			finish()
			return
		}
		falseState := c.snapshotModuleCollectionState()
		falseScopeState := c.snapshotScopeState()
		for _, elseIf := range typed.ElseIf {
			c.restoreModuleCollectionState(falseState)
			c.restoreScopeState(falseScopeState)
			c.collectRequiredModuleExportsFromExpression(elseIf.Condition)
			conditionState = c.snapshotModuleCollectionState()
			conditionScopeState = c.snapshotScopeState()
			branchTruthy, branchKnown := c.inferredConditionTruthiness(elseIf.Condition)
			trueReachable = !branchKnown || branchTruthy
			if trueReachable {
				trueReachable = c.applyConditionOutcomeEffects(elseIf.Condition, true, c.collectRequiredModuleExportsFromExpression)
			}
			if trueReachable {
				if c.collectRequiredModuleExportsFromStatements(elseIf.Consequent) {
					fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
					fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
				}
			}
			c.restoreModuleCollectionState(conditionState)
			c.restoreScopeState(conditionScopeState)
			falseReachable = !branchKnown || !branchTruthy
			if falseReachable {
				falseReachable = c.applyConditionOutcomeEffects(elseIf.Condition, false, c.collectRequiredModuleExportsFromExpression)
			}
			if !falseReachable {
				finish()
				return
			}
			falseState = c.snapshotModuleCollectionState()
			falseScopeState = c.snapshotScopeState()
		}
		c.restoreModuleCollectionState(falseState)
		c.restoreScopeState(falseScopeState)
		if c.collectRequiredModuleExportsFromStatements(typed.Alternate) {
			fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
			fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
		}
		finish()
	case *ForStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Iterable)
		if c.isolatedCollectInference {
			c.recordLiveStatementNames(typed.Body)
			c.degradeLocalTypesForBindings(typed.Body, typed.Target)
		}
		c.recordBindingTarget(typed.Target)
		bodyState := c.snapshotModuleCollectionState()
		bodyScopeState := c.snapshotScopeState()
		c.mutationRegionDepth++
		c.collectRequiredModuleExportsFromStatements(typed.Body)
		c.mutationRegionDepth--
		c.restoreModuleCollectionState(bodyState)
		c.restoreScopeState(bodyScopeState)
		c.recordLocalBindings(typed.Body)
	case *WhileStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		conditionScopeState := c.snapshotScopeState()
		conditionRefinedScopeState, bodyReachable := c.probeConditionOutcome(typed.Condition, true)
		if bodyReachable {
			if c.isolatedCollectInference {
				c.recordLiveStatementNames(typed.Body)
				c.degradeLocalTypesForBindings(typed.Body)
			}
			bodyState := c.snapshotModuleCollectionState()
			bodyScopeState := c.snapshotScopeState()
			c.applyLoopEntryTypeRefinements(conditionScopeState.types, conditionRefinedScopeState.types)
			if c.applyConditionOutcomeEffects(typed.Condition, true, c.collectRequiredModuleExportsFromExpression) {
				c.mutationRegionDepth++
				c.collectRequiredModuleExportsFromStatements(typed.Body)
				c.mutationRegionDepth--
			}
			c.restoreModuleCollectionState(bodyState)
			c.restoreScopeState(bodyScopeState)
		}
		c.recordLocalBindings(typed.Body)
	case *UntilStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		conditionScopeState := c.snapshotScopeState()
		conditionRefinedScopeState, bodyReachable := c.probeConditionOutcome(typed.Condition, false)
		if bodyReachable {
			if c.isolatedCollectInference {
				c.recordLiveStatementNames(typed.Body)
				c.degradeLocalTypesForBindings(typed.Body)
			}
			bodyState := c.snapshotModuleCollectionState()
			bodyScopeState := c.snapshotScopeState()
			c.applyLoopEntryTypeRefinements(conditionScopeState.types, conditionRefinedScopeState.types)
			if c.applyConditionOutcomeEffects(typed.Condition, false, c.collectRequiredModuleExportsFromExpression) {
				c.mutationRegionDepth++
				c.collectRequiredModuleExportsFromStatements(typed.Body)
				c.mutationRegionDepth--
			}
			c.restoreModuleCollectionState(bodyState)
			c.restoreScopeState(bodyScopeState)
		}
		c.recordLocalBindings(typed.Body)
	case *TryStmt:
		baseState := c.snapshotModuleCollectionState()
		baseScopeState := c.snapshotScopeState()
		fallthroughStates := make([]checkModuleCollectionState, 0, 2)
		fallthroughScopeStates := make([]checkScopeState, 0, 2)

		if c.collectRequiredModuleExportsFromStatements(typed.Body) {
			if c.collectRequiredModuleExportsFromStatements(typed.Else) {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		for i := range typed.Rescues {
			clause := &typed.Rescues[i]
			// An empty clause never falls through: it consumes the match but the
			// original error propagates after ensure, so it contributes no state
			// to the code after the block.
			if len(clause.Body) == 0 {
				continue
			}
			c.restoreModuleCollectionState(baseState)
			c.restoreScopeState(baseScopeState)
			popScope := c.pushRescueScope(clause)
			clauseFallsThrough := c.collectRequiredModuleExportsFromStatements(clause.Body)
			popScope()
			if clauseFallsThrough {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		c.mergeModuleCollectionStates(baseState, fallthroughStates)
		c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
		c.collectRequiredModuleExportsFromStatements(typed.Ensure)
		c.stmtNoFallthroughInferred = len(fallthroughStates) == 0
	case *ClassStmt:
		c.collectRequiredModuleExportsFromClassBody(typed.Body)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromClassBody(body []Statement) {
	if len(body) == 0 {
		return
	}
	restoreInference := c.withIsolatedLocalInference()
	defer restoreInference()
	popScope := c.pushScope(make(map[string]struct{}))
	defer popScope()
	c.collectRequiredModuleExportsFromStatements(body)
}

func (c *scriptChecker) collectRequiredModuleExportsFromExpression(expr Expression) {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			c.collectRequiredModuleExportsFromExpression(elem)
			if !c.expressionMayCompleteForBinding(elem) {
				return
			}
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			c.collectRequiredModuleExportsFromExpression(pair.Key)
			if !c.expressionMayCompleteForBinding(pair.Key) {
				return
			}
			c.collectRequiredModuleExportsFromExpression(pair.Value)
			if !c.expressionMayCompleteForBinding(pair.Value) {
				return
			}
		}
	case *CallExpr:
		callSkipsInferred := c.safeNavigationCallSkipsInferred(typed)
		argumentsAlwaysEvaluate := c.safeNavigationArgumentsAlwaysEvaluateInferred(typed)
		if member, ok := typed.Callee.(*MemberExpr); ok {
			// The callee's receiver walks directly so its facts survive
			// until dispatch, mirroring the real walk's poison ordering.
			c.collectRequiredModuleExportsFromExpression(member.Object)
		} else {
			c.collectRequiredModuleExportsFromExpression(typed.Callee)
		}
		if !c.expressionMayCompleteForBinding(typed.Callee) {
			return
		}
		if staticNilSafeNavigationCall(typed) || callSkipsInferred {
			return
		}
		if safeNavigationCallMaySkipArguments(typed) && !argumentsAlwaysEvaluate {
			baseState := c.snapshotModuleCollectionState()
			baseScopeState := c.snapshotScopeState()
			c.collectRequiredModuleExportsFromCallArguments(typed)
			callState := c.snapshotModuleCollectionState()
			c.mergeModuleCollectionStates(baseState, []checkModuleCollectionState{baseState, callState})
			if c.isolatedCollectInference {
				callScopeState := c.snapshotScopeState()
				c.mergeScopeStates(baseScopeState, []checkScopeState{baseScopeState, callScopeState})
			}
		} else {
			c.collectRequiredModuleExportsFromCallArguments(typed)
		}
		if !c.callArgumentsMayCompleteForBinding(typed) {
			return
		}
		c.collectRequireCallExports(typed)
		if c.isolatedCollectInference {
			if typed.Block != nil {
				c.degradeBlockBodyBindings(typed.Block)
			}
			if member, ok := typed.Callee.(*MemberExpr); ok && !c.memberCallPreservesReceiverFacts(typed) {
				c.poisonEscapedIdentifier(member.Object)
			}
			for _, arg := range typed.Args {
				c.poisonEscapedIdentifier(arg)
			}
			for _, kwarg := range typed.KwArgs {
				c.poisonEscapedIdentifier(kwarg.Value)
			}
		}
	case *MemberExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
		if c.isolatedCollectInference && !c.memberDispatchPreservesReceiverFacts(typed) {
			c.poisonEscapedIdentifier(typed.Object)
		}
	case *ScopeExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
	case *IndexExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
		if !c.expressionMayCompleteForBinding(typed.Object) {
			return
		}
		for _, index := range typed.Indices {
			c.collectRequiredModuleExportsFromExpression(index)
			if !c.expressionMayCompleteForBinding(index) {
				return
			}
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			c.collectRequiredModuleExportsFromExpression(element.Target)
		}
	case *SplatArg:
		c.collectRequiredModuleExportsFromExpression(typed.Value)
	case *UnaryExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Right)
	case *BinaryExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Left)
		if binaryRightAlwaysEvaluates(typed) || c.binaryRightAlwaysEvaluatesInferred(typed) {
			c.collectRequiredModuleExportsFromExpression(typed.Right)
		} else if c.isolatedCollectInference && binaryRightMayEvaluate(typed) && !c.binaryRightUnreachable(typed) {
			c.poisonSkippedMutationFacts(typed.Right)
		}
		if c.isolatedCollectInference {
			c.applyShovelMutationFacts(typed)
		}
	case *ConditionalExpr:
		c.collectRequiredModuleExportsFromConditionalExpression(typed)
	case *RescueExpr:
		baseState := c.snapshotModuleCollectionState()
		baseScopeState := c.snapshotScopeState()
		c.collectRequiredModuleExportsFromExpression(typed.Body)
		bodyState := c.snapshotModuleCollectionState()
		bodyScopeState := c.snapshotScopeState()
		c.restoreModuleCollectionState(baseState)
		c.restoreScopeState(baseScopeState)
		c.collectRequiredModuleExportsFromExpression(typed.Fallback)
		fallbackState := c.snapshotModuleCollectionState()
		fallbackScopeState := c.snapshotScopeState()
		c.mergeModuleCollectionStates(baseState, []checkModuleCollectionState{bodyState, fallbackState})
		c.mergeScopeStates(baseScopeState, []checkScopeState{bodyScopeState, fallbackScopeState})
	case *IfExpr:
		c.collectRequiredModuleExportsFromIfExpression(typed)
	case *RangeExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Start)
		if !c.expressionMayCompleteForBinding(typed.Start) {
			return
		}
		c.collectRequiredModuleExportsFromExpression(typed.End)
	case *CaseExpr:
		c.collectRequiredModuleExportsFromCaseExpression(typed)
	case *BlockLiteral:
		return
	case *YieldExpr:
		for _, arg := range typed.Args {
			c.collectRequiredModuleExportsFromExpression(arg)
			if !c.expressionMayCompleteForBinding(arg) {
				return
			}
		}
	case *InterpolatedString:
		c.collectStringPartRequiredModuleExports(typed.Parts)
	case *InterpolatedSymbol:
		c.collectStringPartRequiredModuleExports(typed.Parts)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromConditionalExpression(expr *ConditionalExpr) {
	baseState := c.snapshotModuleCollectionState()
	baseScopeState := c.snapshotScopeState()
	c.collectRequiredModuleExportsFromExpression(expr.Condition)
	if !c.expressionMayCompleteForBinding(expr.Condition) {
		c.restoreModuleCollectionState(baseState)
		c.restoreScopeState(baseScopeState)
		return
	}
	conditionState := c.snapshotModuleCollectionState()
	conditionScopeState := c.snapshotScopeState()
	branchStates := make([]checkModuleCollectionState, 0, 2)
	branchScopeStates := make([]checkScopeState, 0, 2)
	finish := func() {
		c.mergeModuleCollectionStates(baseState, branchStates)
		if c.isolatedCollectInference {
			c.mergeScopeStates(baseScopeState, branchScopeStates)
			return
		}
		c.restoreScopeState(baseScopeState)
	}

	conditionTruthy, conditionKnown := c.inferredConditionTruthiness(expr.Condition)
	trueReachable := !conditionKnown || conditionTruthy
	if trueReachable {
		trueReachable = c.applyConditionOutcomeEffects(expr.Condition, true, c.collectRequiredModuleExportsFromExpression)
	}
	if trueReachable {
		c.collectRequiredModuleExportsFromExpression(expr.Consequent)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}

	c.restoreModuleCollectionState(conditionState)
	c.restoreScopeState(conditionScopeState)
	falseReachable := !conditionKnown || !conditionTruthy
	if falseReachable {
		falseReachable = c.applyConditionOutcomeEffects(expr.Condition, false, c.collectRequiredModuleExportsFromExpression)
	}
	if falseReachable {
		c.collectRequiredModuleExportsFromExpression(expr.Alternate)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}

	finish()
}

func (c *scriptChecker) collectRequiredModuleExportsFromIfExpression(expr *IfExpr) {
	baseState := c.snapshotModuleCollectionState()
	baseScopeState := c.snapshotScopeState()
	c.collectRequiredModuleExportsFromExpression(expr.Condition)
	if !c.expressionMayCompleteForBinding(expr.Condition) {
		c.restoreModuleCollectionState(baseState)
		c.restoreScopeState(baseScopeState)
		return
	}
	conditionState := c.snapshotModuleCollectionState()
	conditionScopeState := c.snapshotScopeState()
	branchStates := make([]checkModuleCollectionState, 0, len(expr.ElseIf)+2)
	branchScopeStates := make([]checkScopeState, 0, len(expr.ElseIf)+2)
	finish := func() {
		c.mergeModuleCollectionStates(baseState, branchStates)
		if c.isolatedCollectInference {
			c.mergeScopeStates(baseScopeState, branchScopeStates)
			return
		}
		c.restoreScopeState(baseScopeState)
	}

	conditionTruthy, conditionKnown := c.inferredConditionTruthiness(expr.Condition)
	trueReachable := !conditionKnown || conditionTruthy
	if trueReachable {
		trueReachable = c.applyConditionOutcomeEffects(expr.Condition, true, c.collectRequiredModuleExportsFromExpression)
	}
	if trueReachable {
		c.collectRequiredModuleExportsFromExpression(expr.Consequent)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}

	c.restoreModuleCollectionState(conditionState)
	c.restoreScopeState(conditionScopeState)
	falseReachable := !conditionKnown || !conditionTruthy
	if falseReachable {
		falseReachable = c.applyConditionOutcomeEffects(expr.Condition, false, c.collectRequiredModuleExportsFromExpression)
	}
	if !falseReachable {
		finish()
		return
	}
	falseState := c.snapshotModuleCollectionState()
	falseScopeState := c.snapshotScopeState()

	for _, branch := range expr.ElseIf {
		c.restoreModuleCollectionState(falseState)
		c.restoreScopeState(falseScopeState)
		c.collectRequiredModuleExportsFromExpression(branch.Condition)
		if !c.expressionMayCompleteForBinding(branch.Condition) {
			finish()
			return
		}
		conditionState = c.snapshotModuleCollectionState()
		conditionScopeState = c.snapshotScopeState()
		branchTruthy, branchKnown := c.inferredConditionTruthiness(branch.Condition)
		trueReachable = !branchKnown || branchTruthy
		if trueReachable {
			trueReachable = c.applyConditionOutcomeEffects(branch.Condition, true, c.collectRequiredModuleExportsFromExpression)
		}
		if trueReachable {
			c.collectRequiredModuleExportsFromExpression(branch.Result)
			branchStates = append(branchStates, c.snapshotModuleCollectionState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		}

		c.restoreModuleCollectionState(conditionState)
		c.restoreScopeState(conditionScopeState)
		falseReachable = !branchKnown || !branchTruthy
		if falseReachable {
			falseReachable = c.applyConditionOutcomeEffects(branch.Condition, false, c.collectRequiredModuleExportsFromExpression)
		}
		if !falseReachable {
			finish()
			return
		}
		falseState = c.snapshotModuleCollectionState()
		falseScopeState = c.snapshotScopeState()
	}

	c.restoreModuleCollectionState(falseState)
	c.restoreScopeState(falseScopeState)
	c.collectRequiredModuleExportsFromExpression(expr.Alternate)
	branchStates = append(branchStates, c.snapshotModuleCollectionState())
	branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	finish()
}

func (c *scriptChecker) collectRequiredModuleExportsFromCallArguments(call *CallExpr) {
	for _, arg := range call.Args {
		c.collectRequiredModuleExportsFromExpression(arg)
		if !c.expressionMayCompleteForBinding(arg) || !c.positionalArgumentExpansionMaySucceed(arg) {
			return
		}
	}
	for _, kwarg := range call.KwArgs {
		c.collectRequiredModuleExportsFromExpression(kwarg.Value)
		if !c.expressionMayCompleteForBinding(kwarg.Value) || !c.keywordArgumentExpansionMaySucceed(kwarg) {
			return
		}
	}
	if call.BlockArg != nil {
		c.collectRequiredModuleExportsFromExpression(call.BlockArg)
	}
}

func (c *scriptChecker) callArgumentsMayCompleteForBinding(call *CallExpr) bool {
	if call == nil {
		return true
	}
	for _, arg := range call.Args {
		if !c.expressionMayCompleteForBinding(arg) || !c.positionalArgumentExpansionMaySucceed(arg) {
			return false
		}
	}
	for _, kwarg := range call.KwArgs {
		if !c.expressionMayCompleteForBinding(kwarg.Value) || !c.keywordArgumentExpansionMaySucceed(kwarg) {
			return false
		}
	}
	return c.expressionMayCompleteForBinding(call.BlockArg)
}

func (c *scriptChecker) collectRequiredModuleExportsFromCaseExpression(expr *CaseExpr) {
	baseState := c.snapshotModuleCollectionState()
	c.collectRequiredModuleExportsFromExpression(expr.Target)
	if !c.expressionMayCompleteForBinding(expr.Target) {
		c.restoreModuleCollectionState(baseState)
		return
	}
	fallthroughState := c.snapshotModuleCollectionState()
	branchStates := make([]checkModuleCollectionState, 0, len(expr.Clauses)+1)

	for _, clause := range expr.Clauses {
		for _, value := range clause.Values {
			c.restoreModuleCollectionState(fallthroughState)
			c.collectRequiredModuleExportsFromExpression(value.Expr)
			if !c.expressionMayCompleteForBinding(value.Expr) ||
				!c.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
				c.mergeModuleCollectionStates(baseState, branchStates)
				return
			}
			matchState := c.snapshotModuleCollectionState()
			c.collectRequiredModuleExportsFromExpression(clause.Result)
			branchStates = append(branchStates, c.snapshotModuleCollectionState())
			fallthroughState = matchState
		}
	}

	c.restoreModuleCollectionState(fallthroughState)
	c.collectRequiredModuleExportsFromExpression(expr.ElseExpr)
	branchStates = append(branchStates, c.snapshotModuleCollectionState())
	c.mergeModuleCollectionStates(baseState, branchStates)
}

func (c *scriptChecker) collectStringPartRequiredModuleExports(parts []StringPart) {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			c.collectRequiredModuleExportsFromExpression(exprPart.Expr)
		}
	}
}

func binaryRightAlwaysEvaluates(expr *BinaryExpr) bool {
	switch expr.Operator {
	case tokenAnd:
		val, ok := staticLiteralValue(expr.Left)
		return ok && val.Truthy()
	case tokenOr:
		val, ok := staticLiteralValue(expr.Left)
		return ok && !val.Truthy()
	default:
		return true
	}
}

func binaryRightMayEvaluate(expr *BinaryExpr) bool {
	switch expr.Operator {
	case tokenAnd:
		val, ok := staticLiteralValue(expr.Left)
		return !ok || val.Truthy()
	case tokenOr:
		val, ok := staticLiteralValue(expr.Left)
		return !ok || !val.Truthy()
	default:
		return true
	}
}

func (c *scriptChecker) collectRequireCallExports(call *CallExpr) {
	moduleName, alias, ok := c.staticRequireCall(call)
	if !ok {
		return
	}
	if c.requiredModules == nil {
		c.requiredModules = make(map[string]struct{})
	}
	entry, err := c.script.engine.loadModule(moduleName, c.moduleCaller, nil)
	if err != nil {
		return
	}
	exports := c.moduleExportValue(entry)
	if !c.canBindRequireAlias(alias, exports) {
		return
	}
	c.collectModuleExports(entry)
	c.bindRequireAlias(alias, exports)
}

func (c *scriptChecker) staticRequireCall(call *CallExpr) (string, string, bool) {
	if call == nil || len(call.Args) != 1 || call.Block != nil || call.BlockArg != nil || c.requireCallShadowed() {
		return "", "", false
	}
	callee, ok := call.Callee.(*Identifier)
	if !ok || callee.Name != "require" {
		return "", "", false
	}
	moduleName, ok := staticRequireModuleName(call.Args[0])
	if !ok {
		return "", "", false
	}
	kwargs := make(map[string]Value, len(call.KwArgs))
	for _, kwarg := range call.KwArgs {
		val, ok := staticLiteralValue(kwarg.Value)
		if !ok {
			return "", "", false
		}
		kwargs[kwarg.Name] = val
	}
	alias, err := parseRequireAlias(kwargs)
	if err != nil {
		return "", "", false
	}
	return moduleName, alias, true
}

func (c *scriptChecker) requireCallShadowed() bool {
	return c.identifierShadowed("require") ||
		c.hostGlobalShadows("require") ||
		c.typeRootHasBinding("require") ||
		c.hostBuiltinOverrides("require")
}

func (c *scriptChecker) collectModuleExports(entry moduleEntry) {
	if c.requiredModules == nil {
		c.requiredModules = make(map[string]struct{})
	}
	if _, loaded := c.requiredModules[entry.key]; loaded {
		return
	}
	c.requiredModules[entry.key] = struct{}{}
	if c.moduleEntries == nil {
		c.moduleEntries = make(map[string]moduleEntry)
	}
	c.moduleEntries[entry.key] = entry
	c.collectRequiredModuleExportsFromModuleInitialization(entry)
	c.checkRequiredModuleExportedFunctions(entry)
	root := c.moduleExportRoot
	if root == nil {
		root = c.typeRoot
	}
	for name, val := range c.moduleExportValue(entry).Hash() {
		if _, exists := root.Get(name); exists {
			continue
		}
		root.DefineStatic(name, val)
	}
}

func (c *scriptChecker) moduleExportValue(entry moduleEntry) Value {
	if c.moduleExportValues == nil {
		c.moduleExportValues = make(map[string]Value)
	}
	if val, ok := c.moduleExportValues[entry.key]; ok {
		return val
	}
	exports := make(map[string]Value, len(entry.script.enums)+len(entry.script.functions))
	for name, enumDef := range cloneEnumsForCall(entry.script.enums) {
		exports[name] = NewEnum(enumDef)
	}
	for name, fn := range entry.script.functions {
		if name == moduleEntrypointFunction || !shouldExportModuleFunction(fn) {
			continue
		}
		exports[name] = NewFunction(fn)
	}
	val := NewObject(exports)
	c.moduleExportValues[entry.key] = val
	return val
}

func (c *scriptChecker) checkRequiredModuleExportedFunctions(entry moduleEntry) {
	if entry.script == nil {
		return
	}
	functions := sortedRequiredModuleExportedFunctions(entry.script.functions)
	parentRoot := c.runtimeTypeRoot
	if parentRoot == nil {
		parentRoot = c.typeRoot
	}
	parentRoot = cloneCheckRoot(parentRoot)
	moduleCheckContext := moduleCheckContextKey(parentRoot)
	checker := scriptChecker{
		script:                 entry.script,
		callOptions:            c.callOptions,
		optionGlobals:          c.optionGlobals,
		optionGlobalsOverride:  false,
		typeRoot:               checkTypeRootWithParentAndGlobals(entry.script, c.optionGlobals, cloneCheckRoot(parentRoot), false),
		runtimeTypeRoot:        checkTypeRootWithParentAndGlobals(entry.script, c.optionGlobals, cloneCheckRoot(parentRoot), false),
		hostGlobals:            c.hostGlobals,
		moduleEntries:          c.moduleEntries,
		moduleExportValues:     c.moduleExportValues,
		moduleCheckedFunctions: c.moduleCheckedFunctions,
		moduleCheckContext:     moduleCheckContext,
		runtimeTypeRootParent:  parentRoot,
		orderIndependentOnly:   c.orderIndependentOnly,
	}
	caller := moduleContextForEntry(entry)
	checker.moduleCaller = &caller
	checker.moduleExportRoot = checker.typeRoot
	checker.collectRequiredModuleExportsFromModuleInitialization(entry)
	checker.checkRequiredModuleInitialization(entry)

	checker.withReachableCallChecks(func() {
		for _, fn := range functions {
			if !checker.markModuleFunctionChecked(entry.key, fn.Name) {
				continue
			}
			label := moduleDisplayName(entry.key) + "." + fn.Name
			checker.withFreshRuntimeTypeRoot(func() {
				checker.withRuntimeModuleCollection(func() {
					checker.collectRequiredModuleExportsFromModuleInitialization(entry)
				})
				checker.checkRuntimeClassBodies(deferredClassBodiesForFunction(fn, checker.script.deferredClassBodies), true)
				checker.markReachableFunctionChecked(fn)
				checker.checkFunction(label, fn)
				checker.checkReachableFunctions()
			})
		}
	})

	for i := range checker.warnings {
		// Attribute module diagnostics to the module's own file so callers
		// printing path-prefixed warnings point at the right source; nested
		// modules keep the source their own sub-checker recorded.
		if checker.warnings[i].Source == "" {
			checker.warnings[i].Source = entry.script.modulePath
		}
	}
	c.warnings = append(c.warnings, checker.warnings...)
	c.moduleEntries = checker.moduleEntries
	c.moduleExportValues = checker.moduleExportValues
	c.moduleCheckedFunctions = checker.moduleCheckedFunctions
}

func (c *scriptChecker) checkRequiredModuleInitialization(entry moduleEntry) {
	if !c.markModuleFunctionChecked(entry.key, moduleEntrypointFunction) {
		return
	}
	c.withFreshRuntimeTypeRoot(func() {
		c.withRuntimeModuleCollection(func() {
			c.collectRequiredModuleExportsFromModuleInitialization(entry)
		})
		c.checkRuntimeClassBodies(c.script.deferredClassBodies, false)
		fn := c.script.functions[moduleEntrypointFunction]
		if fn != nil {
			c.checkFunction(moduleDisplayName(entry.key), fn)
		}
	})
}

func sortedRequiredModuleExportedFunctions(functions map[string]*ScriptFunction) []*ScriptFunction {
	names := make([]string, 0, len(functions))
	for name, fn := range functions {
		if name == moduleEntrypointFunction || !shouldExportModuleFunction(fn) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ScriptFunction, 0, len(names))
	for _, name := range names {
		out = append(out, functions[name])
	}
	return out
}

func (c *scriptChecker) markModuleFunctionChecked(moduleKey, functionName string) bool {
	contextKey := c.moduleCheckContext
	if contextKey == "" {
		root := c.runtimeTypeRoot
		if root == nil {
			root = c.typeRoot
		}
		contextKey = moduleCheckContextKey(root)
	}
	key := moduleKey + "\x00" + functionName + "\x00" + contextKey
	if c.moduleCheckedFunctions == nil {
		c.moduleCheckedFunctions = make(map[string]struct{})
	}
	if _, ok := c.moduleCheckedFunctions[key]; ok {
		return false
	}
	c.moduleCheckedFunctions[key] = struct{}{}
	return true
}

func moduleCheckContextKey(root *Env) string {
	if root == nil {
		return ""
	}
	scopes := make([]string, 0, 4)
	for scope := root; scope != nil; scope = scope.parent {
		bindings := make([]string, 0, scope.dynamicLen()+len(scope.statics))
		scope.rangeDynamicBindings(func(name string, val Value) {
			bindings = append(bindings, "d:"+name+"="+moduleCheckValueKey(val))
		})
		scope.rangeStaticBindings(func(name string, val Value) {
			bindings = append(bindings, "s:"+name+"="+moduleCheckValueKey(val))
		})
		sort.Strings(bindings)
		scopes = append(scopes, strings.Join(bindings, ","))
	}
	return strings.Join(scopes, "|")
}

func moduleCheckValueKey(val Value) string {
	switch val.Kind() {
	case KindEnum:
		enumDef := valueEnum(val)
		if enumDef != nil {
			return fmt.Sprintf("enum:%s:%p", enumDef.Name, enumDef)
		}
	case KindEnumValue:
		member := valueEnumValue(val)
		if member != nil {
			enumName := ""
			if member.Enum != nil {
				enumName = member.Enum.Name
			}
			return fmt.Sprintf("enum-value:%s:%s:%p", enumName, member.Name, member)
		}
	case KindFunction:
		fn := valueFunction(val)
		if fn != nil {
			return fmt.Sprintf("function:%s:%p", fn.Name, fn)
		}
	case KindBuiltin:
		builtin := valueBuiltin(val)
		if builtin != nil {
			return fmt.Sprintf("builtin:%s:%p", builtin.Name, builtin)
		}
	case KindClass:
		classDef := valueClass(val)
		if classDef != nil {
			return fmt.Sprintf("class:%s:%p", classDef.Name, classDef)
		}
	case KindObject, KindHash:
		return fmt.Sprintf("%s:%x", val.Kind(), hashIdentity(val))
	}
	return val.Kind().String() + ":" + val.String()
}

func (c *scriptChecker) canBindRequireAlias(alias string, module Value) bool {
	if alias == "" {
		return true
	}
	if c.identifierShadowed(alias) {
		return false
	}
	root := c.moduleExportRoot
	if root == nil {
		root = c.typeRoot
	}
	if root == nil {
		return false
	}
	existing, ok := checkRootBinding(root, alias)
	if !ok {
		return true
	}
	return sameObjectValue(existing, module)
}

func (c *scriptChecker) bindRequireAlias(alias string, module Value) {
	if alias == "" {
		return
	}
	root := c.moduleExportRoot
	if root == nil {
		root = c.typeRoot
	}
	if root != nil {
		root.Define(alias, module)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromModuleInitialization(entry moduleEntry) {
	caller := moduleContextForEntry(entry)
	previousCaller := c.moduleCaller
	previousScopes := c.scopes
	previousLocalTypes := c.localTypes
	previousLocalClassValues := c.localClassValues
	c.moduleCaller = &caller
	c.scopes = nil
	c.localTypes = nil
	c.localClassValues = nil
	defer func() {
		c.moduleCaller = previousCaller
		c.scopes = previousScopes
		c.localTypes = previousLocalTypes
		c.localClassValues = previousLocalClassValues
	}()

	c.collectRequiredModuleExportsFromModuleClassBodies(entry)
	c.collectRequiredModuleExportsFromModuleEntrypoint(entry)
}

func (c *scriptChecker) collectRequiredModuleExportsFromModuleClassBodies(entry moduleEntry) {
	for _, name := range entry.script.classOrder {
		if _, deferred := entry.script.deferredClassBodies[name]; deferred {
			continue
		}
		classDef := entry.script.classes[name]
		if classDef == nil {
			continue
		}
		c.collectRequiredModuleExportsFromClassBody(classDef.Body)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromModuleEntrypoint(entry moduleEntry) {
	fn := entry.script.functions[moduleEntrypointFunction]
	if fn == nil {
		return
	}
	c.collectFunctionRequiredModuleExports(fn)
}

func (c *scriptChecker) collectRuntimeRequireCallExportsFromExpression(expr Expression) {
	if c.runtimeTypeRoot == nil {
		return
	}
	c.withRuntimeModuleCollection(func() {
		c.collectRequiredModuleExportsFromExpression(expr)
	})
}

func (c *scriptChecker) withRuntimeModuleCollection(collect func()) {
	previousRoot := c.moduleExportRoot
	previousModules := c.requiredModules
	c.moduleExportRoot = c.runtimeTypeRoot
	c.requiredModules = c.runtimeModules
	defer func() {
		c.runtimeModules = c.requiredModules
		c.moduleExportRoot = previousRoot
		c.requiredModules = previousModules
	}()

	collect()
}

func (c *scriptChecker) checkScript() {
	// The entrypoint executes its statements in order, so it is checked
	// before the require-export seed: a top-level call before its require
	// must not resolve the exports early. Its own walk binds each require
	// as it reaches it.
	entrypoint := c.script.entrypoint
	entrypointReached := make(map[*ScriptFunction]struct{})
	if entry := c.script.functions[entrypoint]; entrypoint != "" && entry != nil {
		c.withFreshRuntimeTypeRootForCallable(entry, func() {
			// Functions the top-level code calls run under the runtime root
			// as it exists at each call, so they are checked reachably here
			// with that root — a callee invoked before a later require must
			// not resolve its exports — and skipped in the seeded pass.
			c.withReachableCallChecks(func() {
				c.markReachableFunctionChecked(entry)
				c.checkFunction(entrypoint, entry)
				c.drainReachableFunctions(entrypointReached)
			})
		})
	}
	c.seedEntrypointRequireExports()
	for _, fn := range c.sortedScriptFunctions() {
		if entrypoint != "" && fn.Name == entrypoint {
			continue
		}
		if _, reached := entrypointReached[fn]; reached {
			continue
		}
		c.withFreshRuntimeTypeRootForCallable(fn, func() {
			c.checkFunction(fn.Name, fn)
		})
	}
	c.withFreshRuntimeTypeRoot(func() {
		c.checkRuntimeClassBodies(c.script.deferredClassBodies, false)
	})
	for _, classDef := range c.sortedClasses() {
		for _, method := range sortedCheckFunctions(classDef.Methods) {
			if _, reached := entrypointReached[method]; reached {
				continue
			}
			c.withFreshRuntimeTypeRootForCallable(method, func() {
				c.checkFunction(classDef.Name+"#"+method.Name, method)
			})
		}
		for _, method := range sortedCheckFunctions(classDef.ClassMethods) {
			if _, reached := entrypointReached[method]; reached {
				continue
			}
			c.withFreshRuntimeTypeRootForCallable(method, func() {
				c.checkFunction(classDef.Name+"."+method.Name, method)
			})
		}
	}
}

// seedEntrypointRequireExports binds the module exports required by the
// script's top-level code into the shared type root before the per-function
// walks. This encodes program semantics: whole-script checking (vibes check,
// CheckWarnings) validates the script as vibes run executes it, where the
// entrypoint's top-level requires run before any function can be invoked. A
// host that calls a named function directly without running the entrypoint
// must use the per-call APIs (CheckWarningsForCall / CheckWarningsForFunction,
// the run -check -function path), which deliberately stay unseeded and still
// report exports and their types as unresolved.
// Warnings stay suppressed here; the per-function walks re-collect and
// report module diagnostics exactly as before.
func (c *scriptChecker) seedEntrypointRequireExports() {
	if c.script == nil || c.script.entrypoint == "" {
		return
	}
	entry := c.script.functions[c.script.entrypoint]
	if entry == nil {
		return
	}
	// The seed's collection dedup state is restored afterwards so the real
	// walks re-collect and report module diagnostics themselves; only the
	// bindings in the type root persist. (withSuppressedWarnings already
	// restores moduleCheckedFunctions, which gates the module function
	// checks.)
	previousModules := cloneCheckModuleSet(c.requiredModules)
	c.withSuppressedWarnings(func() {
		c.collectFunctionRequiredModuleExports(entry)
	})
	c.requiredModules = previousModules
	// Annotation validation resolves against each function's fresh runtime
	// root, so the seeded exports (required enums used in annotations, for
	// example) chain in as that root's parent.
	c.runtimeTypeRootParent = cloneCheckRoot(c.typeRoot)
}

func (c *scriptChecker) checkFunctionExecution(target checkTarget) {
	fn := c.script.functions[target.Function]
	if fn == nil {
		return
	}
	c.withFreshRuntimeTypeRoot(func() {
		c.checkRuntimeClassBodies(deferredClassBodiesForFunction(fn, c.script.deferredClassBodies), false)
		if target.ValidateCall {
			c.withReachableCallChecks(func() {
				c.markReachableFunctionChecked(fn)
				c.checkFunctionCall(fn.Name, fn, target.Args, c.callOptions.Keywords)
				c.checkReachableFunctions()
			})
			return
		}
		c.withReachableCallChecks(func() {
			c.enqueueReachableFunction(fn.Name, fn)
			c.checkReachableFunctions()
		})
	})
}

func (c *scriptChecker) checkFunctionCall(label string, fn *ScriptFunction, args []Value, kwargs map[string]Value) {
	if fn == nil || !c.checkCallValueShape(label, fn.Name, fn.Pos, fn, args, kwargs) {
		return
	}
	defer c.withFreshLocalInferenceScope()()
	defer c.withImplicitReturnCapture(fn)()
	popScope := c.pushScope(make(map[string]struct{}))
	defer popScope()
	popNameScope := c.pushFunctionNameScope(fn)
	defer popNameScope()

	var usedKw map[string]bool
	if len(kwargs) > 0 {
		usedKw = make(map[string]bool, len(kwargs))
	}
	argIdx := 0
	warningsBeforeBinding := len(c.warnings)
	for _, param := range fn.Params {
		var boundValue Value
		boundPresent := false
		usedDefault := false
		switch param.Kind {
		case ParamNormal:
			if argIdx < len(args) {
				if normalized, ok := c.checkArgumentValue(label, fn.Pos, args[argIdx], param.Type, fn.Name, param.Name); ok {
					boundValue, boundPresent = normalized, true
				}
				argIdx++
			} else if val, ok := kwargs[param.Name]; ok {
				if normalized, ok := c.checkArgumentValue(label, fn.Pos, val, param.Type, fn.Name, param.Name); ok {
					boundValue, boundPresent = normalized, true
				}
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else {
				c.checkParamDefault(label, param)
				usedDefault = true
			}
		case ParamKeyword:
			if val, ok := kwargs[param.Name]; ok {
				if normalized, ok := c.checkArgumentValue(label, fn.Pos, val, param.Type, fn.Name, param.Name); ok {
					boundValue, boundPresent = normalized, true
				}
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else {
				c.checkParamDefault(label, param)
				usedDefault = true
			}
		case ParamRest:
			c.checkRestArgumentValues(label, fn.Pos, args[argIdx:], param.Type, fn.Name, param.Name)
			boundValue, boundPresent = NewArray(append([]Value(nil), args[argIdx:]...)), true
			argIdx = len(args)
		case ParamKeywordRest:
			c.checkKeywordRestArgumentValues(label, fn.Pos, kwargs, usedKw, param.Type, fn.Name, param.Name)
			rest := make(map[string]Value)
			for _, name := range sortedValueKeywordNames(kwargs) {
				if usedKw != nil && usedKw[name] {
					continue
				}
				rest[name] = kwargs[name]
			}
			for _, name := range sortedValueKeywordNames(kwargs) {
				if usedKw != nil {
					usedKw[name] = true
				}
			}
			boundValue, boundPresent = NewHash(rest), true
		case ParamBlock:
			c.checkBlockArgumentValue(label, fn.Pos, nil, param.Type, fn.Name, param.Name)
		}
		c.recordParamBinding(param)
		c.bindParamValueFact(param, boundValue, boundPresent)
		if boundPresent {
			c.refineAnnotatedParamFact(param, typeFactForValue(boundValue))
		}
		if usedDefault {
			c.bindParamDefaultFact(param)
			if param.DefaultVal != nil {
				c.refineAnnotatedParamFact(
					param,
					c.inferExpressionTypeWithExpectation(param.DefaultVal, typeExpressionExpectation(param.Type)),
				)
			}
		}
	}
	if len(c.warnings) > warningsBeforeBinding {
		// The runtime stops at the failed binding, so the body never runs
		// under this call; its diagnostics would describe executions that
		// cannot happen.
		return
	}
	c.checkStatements(label, fn.ReturnTy, fn.Body)
	if fn.ReturnTy != nil {
		c.checkImplicitReturn(label, fn.ReturnTy, fn.Body, fn.Pos)
	}
}

func (c *scriptChecker) checkParamDefault(function string, param Param) {
	expectation := typeExpressionExpectation(param.Type)
	c.checkExpressionWithExpectation(function, param.DefaultVal, expectation)
	c.collectRuntimeRequireCallExportsFromExpression(param.DefaultVal)
	if param.Type == nil {
		return
	}
	c.checkRuntimeTypeAnnotation(function, param.Type)
	if param.DefaultVal != nil {
		c.checkRuntimeExpressionAgainstTypeWithExpectation(
			function,
			param.DefaultVal,
			param.Type,
			fmt.Sprintf("default value for %s", param.Name),
			expectation,
		)
	}
}

func (c *scriptChecker) withReachableCallChecks(check func()) {
	previousEnabled := c.checkReachableCalls
	previousChecked := c.checkedReachableFuncs
	previousQueue := c.reachableFuncQueue
	c.checkReachableCalls = true
	c.checkedReachableFuncs = nil
	c.reachableFuncQueue = nil
	defer func() {
		c.checkReachableCalls = previousEnabled
		c.checkedReachableFuncs = previousChecked
		c.reachableFuncQueue = previousQueue
	}()
	check()
}

func (c *scriptChecker) enqueueReachableFunction(label string, fn *ScriptFunction) {
	c.enqueueReachableFunctionWithParamFacts(label, fn, nil)
}

func (c *scriptChecker) enqueueReachableFunctionWithParamFacts(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
) {
	if !c.checkReachableCalls || fn == nil || fn.owner != c.script {
		return
	}
	if !c.markReachableFunctionCheckedWithParamFacts(fn, paramFacts) {
		return
	}
	c.reachableFuncQueue = append(c.reachableFuncQueue, reachableFunction{
		label:        label,
		fn:           fn,
		runtimeState: c.snapshotRuntimeState(),
		paramFacts:   cloneReachableParamFacts(paramFacts),
	})
}

func (c *scriptChecker) enqueueReachableFunctionBinding(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	plan scriptCallBindingPlan,
) {
	if !c.checkReachableCalls || fn == nil || fn.owner != c.script {
		return
	}
	key := c.reachableFunctionCheckKey(fn, paramFacts) + "\x00binding:" +
		strconv.FormatBool(plan.bodyMayEnter) + ":" + fmt.Sprint(plan.defaultParams)
	if c.checkedReachableFuncs == nil {
		c.checkedReachableFuncs = make(map[string]struct{})
	}
	if _, checked := c.checkedReachableFuncs[key]; checked {
		return
	}
	c.checkedReachableFuncs[key] = struct{}{}
	planCopy := plan
	planCopy.defaultParams = append([]int(nil), plan.defaultParams...)
	c.reachableFuncQueue = append(c.reachableFuncQueue, reachableFunction{
		label:        label,
		fn:           fn,
		runtimeState: c.snapshotRuntimeState(),
		paramFacts:   cloneReachableParamFacts(paramFacts),
		bindingPlan:  &planCopy,
	})
}

// enqueueReachableIdentifierCall covers bare auto-calls: a top-level `run`
// dispatches like run(), so the callee checks under the call-time runtime
// root exactly as a spelled-out call does.
func (c *scriptChecker) enqueueReachableIdentifierCall(ident *Identifier) {
	if ident == nil {
		return
	}
	if _, ok := c.localClassValueFor(ident.Name); ok {
		return
	}
	if fns, exact := c.localCallableValuesFor(ident.Name); exact {
		for _, fn := range fns {
			if len(fn.Params) == 0 {
				c.enqueueReachableFunction(ident.Name, fn)
				if !c.scriptFunctionClassConstantEffectsProvenAbsent(fn) {
					c.markOpaqueClassConstants()
				}
			}
		}
		return
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		if typeExprMayIncludeCallable(c.inferExpressionType(ident)) {
			c.markOpaqueClassConstants()
		}
		return
	}
	if _, ok := c.staticClassArgument(ident); ok {
		return
	}
	implicitSelf := false
	var implicitCall *CallExpr
	fn := c.script.functions[ident.Name]
	target := staticCallable{name: ident.Name, fn: fn}
	if fn == nil {
		fn, _ = c.typeRootFunction(ident.Name)
		target.fn = fn
	}
	if fn == nil {
		if c.typeRootHasBinding(ident.Name) || c.hostBuiltinOverrides(ident.Name) {
			if typeExprMayIncludeCallable(c.inferExpressionType(ident)) {
				c.markOpaqueClassConstants()
			}
			return
		}
		var ok bool
		implicitCall, target, ok = c.implicitSelfAutoCall(ident)
		if !ok {
			return
		}
		fn = target.fn
		implicitSelf = true
	}
	if fn == nil {
		return
	}
	if !implicitSelf && len(fn.Params) > 0 {
		return
	}
	bindingStarts := true
	if implicitSelf {
		plan := c.scriptCallBindingPlan(implicitCall, target)
		bindingStarts = plan.bodyMayEnter || plan.defaultParams != nil
		if plan.bodyMayEnter {
			c.enqueueReachableFunction(target.name, fn)
		} else if plan.defaultParams != nil {
			c.enqueueReachableFunctionBinding(target.name, fn, nil, plan)
		}
	} else if c.checkReachableCalls {
		c.enqueueReachableFunction(ident.Name, fn)
	}
	if bindingStarts && !c.scriptFunctionClassConstantEffectsProvenAbsent(fn) {
		c.markOpaqueClassConstants()
	}
}

func (c *scriptChecker) autoInvokedIdentifierMayComplete(ident *Identifier) bool {
	if ident == nil {
		return true
	}
	if fns, exact := c.localCallableValuesFor(ident.Name); exact {
		for _, fn := range fns {
			if len(fn.Params) > 0 || c.scriptFunctionCallMayComplete(nil, staticCallable{fn: fn}) {
				return true
			}
		}
		return false
	}
	if _, classValue := c.localClassValueFor(ident.Name); classValue {
		return true
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		return true
	}
	if fn := c.script.functions[ident.Name]; fn != nil {
		return len(fn.Params) > 0 || c.scriptFunctionCallMayComplete(nil, staticCallable{fn: fn})
	}
	if fn, ok := c.typeRootFunction(ident.Name); ok {
		return len(fn.Params) > 0 || c.scriptFunctionCallMayComplete(nil, staticCallable{fn: fn})
	}
	if c.typeRootHasBinding(ident.Name) || c.hostBuiltinOverrides(ident.Name) {
		return true
	}
	if spec, ok := c.defaultBuiltinCallSpec(ident.Name); ok {
		if !spec.autoInvoke {
			return true
		}
		view := staticCallView{pos: ident.Pos()}
		return c.builtinCallMayEnter(view, spec) && c.builtinCallMayComplete(spec)
	}
	if call, target, ok := c.implicitSelfAutoCall(ident); ok {
		if target.fn == nil {
			return true
		}
		plan := c.scriptCallBindingPlan(call, target)
		return plan.bodyMayEnter && c.scriptFunctionCallMayComplete(call, target)
	}
	if c.implicitSelfConstructorLookupFails(&CallExpr{Callee: ident, Position: ident.Pos()}) {
		return false
	}
	return true
}

func (c *scriptChecker) markReachableFunctionChecked(fn *ScriptFunction) bool {
	return c.markReachableFunctionCheckedWithParamFacts(fn, nil)
}

func (c *scriptChecker) markReachableFunctionCheckedWithParamFacts(
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
) bool {
	if fn == nil {
		return false
	}
	if c.checkedReachableFuncs == nil {
		c.checkedReachableFuncs = make(map[string]struct{})
	}
	key := c.reachableFunctionCheckKey(fn, paramFacts)
	if _, ok := c.checkedReachableFuncs[key]; ok {
		return false
	}
	c.checkedReachableFuncs[key] = struct{}{}
	return true
}

func (c *scriptChecker) reachableFunctionCheckKey(fn *ScriptFunction, paramFacts map[string]checkReachableParamFact) string {
	return fmt.Sprintf("%p\x00%s\x00%s", fn, c.runtimeCheckContextKey(), reachableParamFactsKey(paramFacts))
}

func (c *scriptChecker) runtimeCheckContextKey() string {
	root := c.runtimeTypeRoot
	if root == nil {
		root = c.typeRoot
	}
	memberSet := cloneCheckStringSet(c.runtimeNamespaceMembers)
	if memberSet == nil && len(c.classConstantContext.namespaceMembers) > 0 {
		memberSet = make(map[string]struct{}, len(c.classConstantContext.namespaceMembers))
	}
	for member := range c.classConstantContext.namespaceMembers {
		memberSet[member] = struct{}{}
	}
	members := make([]string, 0, len(memberSet))
	for member := range memberSet {
		members = append(members, member)
	}
	sort.Strings(members)
	return fmt.Sprintf(
		"%s\x00%t\x00%s",
		moduleCheckContextKey(root),
		c.opaqueClassConstants || c.classConstantContext.opaque,
		strings.Join(members, "\x00"),
	)
}

func cloneReachableParamFacts(facts map[string]checkReachableParamFact) map[string]checkReachableParamFact {
	if len(facts) == 0 {
		return nil
	}
	clone := make(map[string]checkReachableParamFact, len(facts))
	for name, fact := range facts {
		fact.classNames = append([]string(nil), fact.classNames...)
		fact.callables = append([]*ScriptFunction(nil), fact.callables...)
		fact.staticVals = append([]Expression(nil), fact.staticVals...)
		clone[name] = fact
	}
	return clone
}

func reachableParamFactsKey(facts map[string]checkReachableParamFact) string {
	names := make([]string, 0, len(facts))
	for name := range facts {
		names = append(names, name)
	}
	sort.Strings(names)
	var key strings.Builder
	for _, name := range names {
		key.WriteString(name)
		key.WriteByte('=')
		fact := facts[name]
		key.WriteString(formatTypeExpr(fact.typeExpr))
		key.WriteByte(':')
		key.WriteString(strings.Join(fact.classNames, ","))
		key.WriteByte(':')
		for _, fn := range fact.callables {
			fmt.Fprintf(&key, "%p,", fn)
		}
		key.WriteByte(':')
		for _, value := range fact.staticVals {
			fmt.Fprintf(&key, "%T:%p,", value, value)
		}
		key.WriteByte(':')
		key.WriteString(strconv.FormatBool(fact.usesDefault))
		key.WriteByte('\x00')
	}
	return key.String()
}

func (c *scriptChecker) checkReachableFunctions() {
	c.drainReachableFunctions(nil)
}

// drainReachableFunctions checks every enqueued reachable callee under its
// call-time runtime state, recording the drained functions when the caller
// needs to know which ones were covered.
func (c *scriptChecker) drainReachableFunctions(reached map[*ScriptFunction]struct{}) {
	for len(c.reachableFuncQueue) > 0 {
		next := c.reachableFuncQueue[0]
		c.reachableFuncQueue = c.reachableFuncQueue[1:]
		if reached != nil {
			reached[next.fn] = struct{}{}
		}
		scopeState := c.snapshotScopeState()
		c.restoreRuntimeState(next.runtimeState)
		c.scopes = nil
		c.localTypes = nil
		c.localClassValues = nil
		previousParamFacts := c.reachableParamFacts
		previousBindingPlan := c.reachableBindingPlan
		c.reachableParamFacts = next.paramFacts
		c.reachableBindingPlan = next.bindingPlan
		c.checkFunction(next.label, next.fn)
		c.reachableParamFacts = previousParamFacts
		c.reachableBindingPlan = previousBindingPlan
		c.restoreScopeState(scopeState)
	}
}

func (c *scriptChecker) withFreshRuntimeTypeRootForCallable(fn *ScriptFunction, check func()) {
	c.withFreshRuntimeTypeRoot(func() {
		c.checkRuntimeClassBodies(deferredClassBodiesForFunction(fn, c.script.deferredClassBodies), true)
		check()
	})
}

func (c *scriptChecker) withFreshRuntimeTypeRoot(check func()) {
	previousRoot := c.runtimeTypeRoot
	previousModules := c.runtimeModules
	previousNamespaceMembers := c.runtimeNamespaceMembers
	previousOpaqueClassConstants := c.opaqueClassConstants
	previousClassConstantContext := c.classConstantContext
	c.runtimeTypeRoot = checkTypeRootWithParentAndGlobals(c.script, c.optionGlobals, cloneCheckRoot(c.runtimeTypeRootParent), c.optionGlobalsOverride)
	c.runtimeModules = nil
	c.runtimeNamespaceMembers = nil
	c.opaqueClassConstants = false
	c.classConstantContext = checkClassConstantEffects{}
	defer func() {
		c.runtimeTypeRoot = previousRoot
		c.runtimeModules = previousModules
		c.runtimeNamespaceMembers = previousNamespaceMembers
		c.opaqueClassConstants = previousOpaqueClassConstants
		c.classConstantContext = previousClassConstantContext
	}()
	check()
}

func (c *scriptChecker) checkRuntimeClassBodies(skip map[string]struct{}, suppressWarnings bool) {
	if c.runtimeTypeRoot == nil {
		return
	}
	for _, name := range c.script.classOrder {
		if _, deferred := skip[name]; deferred {
			continue
		}
		classDef := c.script.classes[name]
		if classDef == nil || len(classDef.Body) == 0 {
			continue
		}
		c.checkRuntimeClassBody(classDef, suppressWarnings)
	}
}

func (c *scriptChecker) checkRuntimeClassBody(classDef *ClassDef, suppressWarnings bool) {
	check := func() {
		defer c.withFreshLocalInferenceScope()()
		popScope := c.pushScope(make(map[string]struct{}))
		defer popScope()
		// Class bodies run with self bound to the class, so bare identifiers
		// can resolve through implicit self class members.
		previousSelf := c.selfScope
		previousClass := c.selfClass
		previousClassContext := c.selfClassContext
		c.selfScope = true
		c.selfClass = classDef
		c.selfClassContext = true
		defer func() {
			c.selfScope = previousSelf
			c.selfClass = previousClass
			c.selfClassContext = previousClassContext
		}()
		c.checkStatements(classDef.Name+".<class body>", nil, classDef.Body)
	}
	if !suppressWarnings {
		check()
		return
	}
	c.withSuppressedWarnings(check)
}

func (c *scriptChecker) withSuppressedWarnings(check func()) {
	previousWarnings := c.warnings
	previousModuleCheckedFunctions := cloneCheckStringSet(c.moduleCheckedFunctions)
	c.warnings = nil
	defer func() {
		c.warnings = previousWarnings
		c.moduleCheckedFunctions = previousModuleCheckedFunctions
	}()
	check()
}

type checkRuntimeState struct {
	root                 *Env
	modules              map[string]struct{}
	namespaceMembers     map[string]struct{}
	opaqueClassConstants bool
	classConstantContext checkClassConstantEffects
}

type checkClassConstantEffects struct {
	opaque           bool
	namespaceMembers map[string]struct{}
}

type checkLoopExitEffects struct {
	effects checkClassConstantEffects
	seen    bool
}

type checkModuleCollectionState struct {
	root    *Env
	modules map[string]struct{}
}

type checkScopeState struct {
	defined     []map[string]struct{}
	types       []checkTypeFrame
	classValues []checkClassValueFrame
}

func (c *scriptChecker) snapshotRuntimeState() checkRuntimeState {
	state := checkRuntimeState{
		modules:              cloneCheckModuleSet(c.runtimeModules),
		namespaceMembers:     cloneCheckStringSet(c.runtimeNamespaceMembers),
		opaqueClassConstants: c.opaqueClassConstants,
		classConstantContext: cloneCheckClassConstantEffects(c.classConstantContext),
	}
	if c.runtimeTypeRoot != nil {
		state.root = c.runtimeTypeRoot.CloneShallow()
	}
	return state
}

func (c *scriptChecker) restoreRuntimeState(state checkRuntimeState) {
	c.runtimeTypeRoot = cloneCheckRoot(state.root)
	c.runtimeModules = cloneCheckModuleSet(state.modules)
	c.runtimeNamespaceMembers = cloneCheckStringSet(state.namespaceMembers)
	c.opaqueClassConstants = state.opaqueClassConstants
	c.classConstantContext = cloneCheckClassConstantEffects(state.classConstantContext)
}

func cloneCheckClassConstantEffects(effects checkClassConstantEffects) checkClassConstantEffects {
	return checkClassConstantEffects{
		opaque:           effects.opaque,
		namespaceMembers: cloneCheckStringSet(effects.namespaceMembers),
	}
}

func mergeCheckClassConstantEffects(dst *checkClassConstantEffects, src checkClassConstantEffects) {
	if dst == nil {
		return
	}
	dst.opaque = dst.opaque || src.opaque
	if len(src.namespaceMembers) == 0 {
		return
	}
	if dst.namespaceMembers == nil {
		dst.namespaceMembers = make(map[string]struct{}, len(src.namespaceMembers))
	}
	for member := range src.namespaceMembers {
		dst.namespaceMembers[member] = struct{}{}
	}
}

func (c *scriptChecker) currentClassConstantEffects() checkClassConstantEffects {
	return checkClassConstantEffects{
		opaque:           c.opaqueClassConstants,
		namespaceMembers: cloneCheckStringSet(c.runtimeNamespaceMembers),
	}
}

func (c *scriptChecker) applyClassConstantEffects(effects checkClassConstantEffects) {
	c.opaqueClassConstants = c.opaqueClassConstants || effects.opaque
	if len(effects.namespaceMembers) == 0 {
		return
	}
	if c.runtimeNamespaceMembers == nil {
		c.runtimeNamespaceMembers = make(map[string]struct{}, len(effects.namespaceMembers))
	}
	for member := range effects.namespaceMembers {
		c.runtimeNamespaceMembers[member] = struct{}{}
	}
}

func runtimeStatesClassConstantEffects(base checkRuntimeState, states []checkRuntimeState) checkClassConstantEffects {
	effects := checkClassConstantEffects{
		opaque:           base.opaqueClassConstants,
		namespaceMembers: cloneCheckStringSet(base.namespaceMembers),
	}
	for _, state := range states {
		mergeCheckClassConstantEffects(&effects, checkClassConstantEffects{
			opaque:           state.opaqueClassConstants,
			namespaceMembers: state.namespaceMembers,
		})
	}
	return effects
}

func (c *scriptChecker) setClassConstantEffects(effects checkClassConstantEffects) {
	c.opaqueClassConstants = effects.opaque
	c.runtimeNamespaceMembers = cloneCheckStringSet(effects.namespaceMembers)
}

func (c *scriptChecker) restoreRuntimeStatePreservingClassConstantEffects(state checkRuntimeState) {
	effects := c.currentClassConstantEffects()
	c.restoreRuntimeState(state)
	c.applyClassConstantEffects(effects)
}

func (c *scriptChecker) markOpaqueClassConstants() {
	c.opaqueClassConstants = true
	for i := range c.classConstantCaptures {
		c.classConstantCaptures[i].opaque = true
	}
}

func (c *scriptChecker) captureClassConstantEffects(check func()) checkClassConstantEffects {
	index := len(c.classConstantCaptures)
	c.classConstantCaptures = append(c.classConstantCaptures, checkClassConstantEffects{})
	defer func() {
		c.classConstantCaptures = c.classConstantCaptures[:index]
	}()
	check()
	return cloneCheckClassConstantEffects(c.classConstantCaptures[index])
}

func (c *scriptChecker) checkLoopStatements(function string, returnType *TypeExpr, statements []Statement) checkClassConstantEffects {
	previous := c.loopExitEffects
	var exits checkLoopExitEffects
	c.loopExitEffects = &exits
	defer func() {
		c.loopExitEffects = previous
	}()
	if c.checkStatements(function, returnType, statements) {
		mergeCheckClassConstantEffects(&exits.effects, c.currentClassConstantEffects())
	}
	return exits.effects
}

func (c *scriptChecker) captureLoopExitClassConstantEffects() {
	if c.loopExitEffects == nil {
		return
	}
	c.loopExitEffects.seen = true
	mergeCheckClassConstantEffects(&c.loopExitEffects.effects, c.currentClassConstantEffects())
	mergeCheckClassConstantEffects(&c.loopExitEffects.effects, c.classConstantContext)
}

func cloneCheckRoot(root *Env) *Env {
	if root == nil {
		return nil
	}
	return root.CloneShallow()
}

func cloneCheckModuleSet(modules map[string]struct{}) map[string]struct{} {
	return cloneCheckStringSet(modules)
}

func cloneCheckStringSet(modules map[string]struct{}) map[string]struct{} {
	if len(modules) == 0 {
		return nil
	}
	clone := make(map[string]struct{}, len(modules))
	for key := range modules {
		clone[key] = struct{}{}
	}
	return clone
}

func unionCheckStringSet(dst, src map[string]struct{}) map[string]struct{} {
	for key := range src {
		if dst == nil {
			dst = make(map[string]struct{}, len(src))
		}
		dst[key] = struct{}{}
	}
	return dst
}

func (c *scriptChecker) mergeRuntimeStates(base checkRuntimeState, states []checkRuntimeState) {
	c.restoreRuntimeState(base)
	if len(states) == 0 {
		return
	}
	for _, state := range states {
		c.opaqueClassConstants = c.opaqueClassConstants || state.opaqueClassConstants
	}
	common := cloneCheckModuleSet(states[0].modules)
	for _, state := range states[1:] {
		for key := range common {
			if _, ok := state.modules[key]; !ok {
				delete(common, key)
			}
		}
	}
	for key := range base.modules {
		delete(common, key)
	}
	commonAliases := commonObjectAliasBindings(base.root, runtimeStateRoots(states))
	if len(common) != 0 || len(commonAliases) != 0 {
		c.withRuntimeModuleCollection(func() {
			for key := range common {
				entry, ok := c.moduleEntries[key]
				if !ok {
					continue
				}
				c.collectModuleExports(entry)
			}
			for alias, module := range commonAliases {
				c.bindRequireAlias(alias, module)
			}
		})
	}

	// Namespace writes are possible-write markers: a member reassigned on
	// any joining path may govern dispatch after the join, so the branches
	// union rather than intersect (unlike module requires, which must hold
	// on every path to count as definitely bound).
	for _, state := range states {
		c.preserveRuntimeNamespaceMembers(state.namespaceMembers)
	}
}

// preserveRuntimeNamespaceMembers reunions possible namespace writes into
// the live state after a restore or join of code that may have run.
func (c *scriptChecker) preserveRuntimeNamespaceMembers(members map[string]struct{}) {
	for member := range members {
		if _, exists := c.runtimeNamespaceMembers[member]; exists {
			continue
		}
		c.recordRuntimeNamespaceMember(member)
	}
}

func runtimeStateRoots(states []checkRuntimeState) []*Env {
	roots := make([]*Env, 0, len(states))
	for _, state := range states {
		roots = append(roots, state.root)
	}
	return roots
}

func (c *scriptChecker) snapshotModuleCollectionState() checkModuleCollectionState {
	state := checkModuleCollectionState{modules: cloneCheckModuleSet(c.requiredModules)}
	if c.moduleExportRoot != nil {
		state.root = c.moduleExportRoot.CloneShallow()
	}
	return state
}

func (c *scriptChecker) restoreModuleCollectionState(state checkModuleCollectionState) {
	previousRoot := c.moduleExportRoot
	root := cloneCheckRoot(state.root)
	c.moduleExportRoot = root
	c.requiredModules = cloneCheckModuleSet(state.modules)
	if c.runtimeTypeRoot == previousRoot {
		c.runtimeTypeRoot = root
	}
	if c.typeRoot == previousRoot {
		c.typeRoot = root
	}
}

func (c *scriptChecker) mergeModuleCollectionStates(base checkModuleCollectionState, states []checkModuleCollectionState) {
	c.restoreModuleCollectionState(base)
	if len(states) == 0 {
		return
	}
	common := cloneCheckModuleSet(states[0].modules)
	for _, state := range states[1:] {
		for key := range common {
			if _, ok := state.modules[key]; !ok {
				delete(common, key)
			}
		}
	}
	for key := range base.modules {
		delete(common, key)
	}
	for key := range common {
		entry, ok := c.moduleEntries[key]
		if !ok {
			continue
		}
		c.collectModuleExports(entry)
	}
	for alias, module := range commonObjectAliasBindings(base.root, moduleCollectionStateRoots(states)) {
		c.bindRequireAlias(alias, module)
	}
}

func moduleCollectionStateRoots(states []checkModuleCollectionState) []*Env {
	roots := make([]*Env, 0, len(states))
	for _, state := range states {
		roots = append(roots, state.root)
	}
	return roots
}

func commonObjectAliasBindings(base *Env, roots []*Env) map[string]Value {
	if len(roots) == 0 || roots[0] == nil {
		return nil
	}
	common := make(map[string]Value)
	roots[0].rangeDynamicBindings(func(name string, val Value) {
		if val.Kind() != KindObject {
			return
		}
		if _, exists := checkRootBinding(base, name); exists {
			return
		}
		common[name] = val
	})
	for _, root := range roots[1:] {
		for name, val := range common {
			other, ok := checkRootOwnBinding(root, name)
			if !ok || !sameObjectValue(val, other) {
				delete(common, name)
			}
		}
	}
	return common
}

func (c *scriptChecker) snapshotScopeState() checkScopeState {
	state := checkScopeState{
		types:       c.snapshotLocalTypes(),
		classValues: c.snapshotLocalClassValues(),
	}
	if len(c.scopes) > 0 {
		state.defined = make([]map[string]struct{}, len(c.scopes))
		for i, scope := range c.scopes {
			state.defined[i] = cloneCheckScope(scope)
		}
	}
	return state
}

func (c *scriptChecker) restoreScopeState(state checkScopeState) {
	c.restoreLocalTypes(state.types)
	c.restoreLocalClassValues(state.classValues)
	if len(state.defined) == 0 {
		c.scopes = nil
		return
	}
	c.scopes = make([]map[string]struct{}, len(state.defined))
	for i, scope := range state.defined {
		c.scopes[i] = cloneCheckScope(scope)
	}
}

func cloneCheckScope(scope map[string]struct{}) map[string]struct{} {
	if len(scope) == 0 {
		return nil
	}
	clone := make(map[string]struct{}, len(scope))
	for key := range scope {
		clone[key] = struct{}{}
	}
	return clone
}

func (c *scriptChecker) mergeScopeStates(base checkScopeState, states []checkScopeState) {
	c.restoreScopeState(base)
	if len(states) == 0 {
		return
	}
	for i := range c.scopes {
		if i >= len(states[0].defined) {
			continue
		}
		common := cloneCheckScope(states[0].defined[i])
		for _, state := range states[1:] {
			if i >= len(state.defined) {
				clear(common)
				break
			}
			for key := range common {
				if _, ok := state.defined[i][key]; !ok {
					delete(common, key)
				}
			}
		}
		if i < len(base.defined) {
			for key := range base.defined[i] {
				delete(common, key)
			}
		}
		if len(common) == 0 {
			continue
		}
		if c.scopes[i] == nil {
			c.scopes[i] = make(map[string]struct{}, len(common))
		}
		for key := range common {
			c.scopes[i][key] = struct{}{}
		}
	}
	c.mergeLocalTypeStates(base, states)
	c.mergeLocalClassValueStates(states)
}

func (c *scriptChecker) sortedScriptFunctions() []*ScriptFunction {
	names := make([]string, 0, len(c.script.functions))
	for name := range c.script.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ScriptFunction, 0, len(names))
	for _, name := range names {
		out = append(out, c.script.functions[name])
	}
	return out
}

func (c *scriptChecker) sortedClasses() []*ClassDef {
	names := make([]string, 0, len(c.script.classes))
	for name := range c.script.classes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ClassDef, 0, len(names))
	for _, name := range names {
		out = append(out, c.script.classes[name])
	}
	return out
}

func sortedCheckFunctions(functions map[string]*ScriptFunction) []*ScriptFunction {
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ScriptFunction, 0, len(names))
	for _, name := range names {
		out = append(out, functions[name])
	}
	return out
}

func (c *scriptChecker) checkFunction(label string, fn *ScriptFunction) {
	if fn == nil {
		return
	}
	c.withFreshLocalInference(func() {
		defer c.withImplicitReturnCapture(fn)()
		popScope := c.pushScope(make(map[string]struct{}))
		defer popScope()
		popNameScope := c.pushFunctionNameScope(fn)
		defer popNameScope()

		for i, param := range fn.Params {
			expectation := typeExpressionExpectation(param.Type)
			defaultRuns := c.reachableParamDefaultRuns(param)
			if c.reachableBindingPlan != nil {
				defaultRuns = slices.Contains(c.reachableBindingPlan.defaultParams, i)
			}
			if defaultRuns {
				c.checkExpressionWithExpectation(label, param.DefaultVal, expectation)
				c.collectRuntimeRequireCallExportsFromExpression(param.DefaultVal)
			} else {
				c.checkNonExecutingDefaultExpression(label, param.DefaultVal, expectation)
			}
			if param.Type != nil {
				c.checkRuntimeTypeAnnotation(label, param.Type)
				if param.DefaultVal != nil {
					c.checkRuntimeExpressionAgainstTypeWithExpectation(
						label,
						param.DefaultVal,
						param.Type,
						fmt.Sprintf("default value for %s", param.Name),
						expectation,
					)
				}
			}
			c.recordParamBinding(param)
			c.applyReachableParamFact(param)
		}
		if c.reachableBindingPlan != nil && !c.reachableBindingPlan.bodyMayEnter {
			return
		}
		c.checkStatements(label, fn.ReturnTy, fn.Body)
		if fn.ReturnTy != nil {
			c.checkImplicitReturn(label, fn.ReturnTy, fn.Body, fn.Pos)
		}
	})
}

func (c *scriptChecker) reachableParamDefaultRuns(param Param) bool {
	if param.DefaultVal == nil || param.Name == "" {
		return false
	}
	fact, callBound := c.reachableParamFacts[param.Name]
	if !callBound {
		// A pristine function walk or an expanded call has no exact binding
		// shape, so the default remains a possible runtime path.
		return true
	}
	return fact.usesDefault
}

func (c *scriptChecker) checkNonExecutingDefaultExpression(
	function string,
	expr Expression,
	expectation expressionExpectation,
) {
	if expr == nil {
		return
	}
	runtimeState := c.snapshotRuntimeState()
	scopeState := c.snapshotScopeState()
	restoreInference := c.withFreshLocalInferenceScope()
	reachableChecks := c.checkReachableCalls
	captures := make([]checkClassConstantEffects, len(c.classConstantCaptures))
	for i, capture := range c.classConstantCaptures {
		captures[i] = cloneCheckClassConstantEffects(capture)
	}
	c.checkReachableCalls = false
	c.checkExpressionWithExpectation(function, expr, expectation)
	c.checkReachableCalls = reachableChecks
	restoreInference()
	c.classConstantCaptures = captures
	c.restoreRuntimeState(runtimeState)
	c.restoreScopeState(scopeState)
}

func (c *scriptChecker) applyReachableParamFact(param Param) {
	fact, ok := c.reachableParamFacts[param.Name]
	if !ok || param.Name == "" {
		return
	}
	if fact.usesDefault && param.DefaultVal != nil {
		fact.typeExpr = c.inferExpressionTypeWithExpectation(
			param.DefaultVal,
			positionalArgumentExpectation(param),
		)
		if classNames, exact := c.classValueExpressionNames(param.DefaultVal); exact {
			fact.classNames = classNames
		} else if fns, exact := c.callableExpressionFunctions(param.DefaultVal); exact {
			fact.callables = fns
		} else if values, exact := c.staticValueExpressionAlternatives(param.DefaultVal); exact {
			fact.staticVals = values
		}
	}
	if fact.typeExpr != nil {
		if param.Type == nil {
			c.bindLocalTypeInCurrentFrame(param.Name, fact.typeExpr)
		} else {
			c.refineAnnotatedParamFact(param, fact.typeExpr)
		}
	}
	if len(fact.classNames) > 0 {
		c.bindLocalClassValues(param.Name, fact.classNames)
	} else if len(fact.callables) > 0 {
		c.bindLocalCallableValues(param.Name, fact.callables)
	} else if len(fact.staticVals) > 0 {
		c.bindLocalStaticValues(param.Name, fact.staticVals)
	}
}

// checkStatements walks a statement list and reports whether it can fall
// through: false when some statement provably exits, statically or from
// inferred branch decisions, so block-level callers gate dead paths the
// same way the list itself stops.
func (c *scriptChecker) checkStatements(function string, returnType *TypeExpr, statements []Statement) bool {
	for _, stmt := range statements {
		c.checkStatement(function, returnType, stmt)
		noFallthrough := c.stmtNoFallthroughInferred
		c.stmtNoFallthroughInferred = false
		if statementAlwaysExits(stmt) || noFallthrough {
			return false
		}
	}
	return true
}

func (c *scriptChecker) recordNonCompletingExpression() {
	c.captureExceptionExitState()
	c.captureEnsureExitState()
	c.stmtNoFallthroughInferred = true
}

func (c *scriptChecker) checkStatement(function string, returnType *TypeExpr, stmt Statement) {
	c.predeclareStatementLiveNames(stmt)
	defer c.postdeclareStatementLiveNames(stmt)
	switch typed := stmt.(type) {
	case nil:
		return
	case *ReturnStmt:
		// The return value's own side effects (a shovel append, for example)
		// apply before the value leaves the function, so the expression is
		// walked before the annotation check reads its inferred type.
		if !c.checkExpression(function, typed.Value) {
			c.recordNonCompletingExpression()
			return
		}
		if c.returnCollector != nil && c.deferredReturnSites == nil {
			if typed.Value == nil {
				c.returnCollector.record(checkTypeNil)
			} else {
				c.returnCollector.record(c.inferExpressionType(typed.Value))
			}
		}
		c.captureEnsureExitState()
		if returnType != nil {
			c.checkReturnStatementType(function, returnType, typed)
		} else if c.deferredReturnSites != nil {
			// The enclosing begin/ensure deferred this check; capture the
			// state at the return itself so branch-local facts survive the
			// branch merges that run before the ensure walk.
			*c.deferredReturnSites = append(*c.deferredReturnSites, deferredReturnSite{
				runtimeState: c.snapshotRuntimeState(),
				scopeState:   c.snapshotScopeState(),
				stmt:         typed,
			})
		}
	case *RaiseStmt:
		// raise RuntimeError, "boom" resolves a bare canonical error class
		// name without an env binding, so it is not an identifier reference.
		if !staticRaiseErrorClass(typed) && !c.checkExpression(function, typed.Value) {
			c.recordNonCompletingExpression()
			return
		}
		if !c.checkExpression(function, typed.Message) {
			c.recordNonCompletingExpression()
			return
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Message)
		c.captureExceptionExitState()
		c.captureEnsureExitState()
	case *BreakStmt:
		if !c.checkExpression(function, typed.Value) {
			c.recordNonCompletingExpression()
			return
		}
		c.captureLoopExitClassConstantEffects()
		c.captureEnsureExitState()
	case *NextStmt:
		if !c.checkExpression(function, typed.Value) {
			c.recordNonCompletingExpression()
			return
		}
		c.captureLoopExitClassConstantEffects()
		c.captureEnsureExitState()
	case *RetryStmt:
		if c.retryExitSites != nil {
			*c.retryExitSites = append(*c.retryExitSites, checkStateSnapshot{
				runtimeState: c.snapshotRuntimeState(),
				scopeState:   c.snapshotScopeState(),
			})
		}
		c.stmtNoFallthroughInferred = true
		return
	case *AssignStmt:
		targetMayWrite := true
		inferWrite := true
		switch typed.Operator {
		case "":
			// Plain assignment evaluates its value before the target receiver and
			// selectors, and it dispatches only the setter (never [] or a getter).
			expectation := c.assignmentValueExpectation(typed.Target, typed.Value)
			if !c.checkExpressionWithExpectation(function, typed.Value, expectation) {
				c.recordNonCompletingExpression()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
			if !c.checkPlainAssignmentTarget(function, typed.Target, typed.Value) {
				c.recordNonCompletingExpression()
				return
			}
		case tokenOrAssign, tokenAndAssign:
			if !c.checkExpression(function, typed.Target) {
				c.recordNonCompletingExpression()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Target)
			truthy, known := c.inferredConditionTruthiness(typed.Target)
			rhsReachable := true
			if known {
				if typed.Operator == tokenOrAssign {
					rhsReachable = !truthy
				} else {
					rhsReachable = truthy
				}
			}
			targetMayWrite = rhsReachable
			rhsAlwaysEvaluates := rhsReachable && known
			if rhsReachable {
				runtimeState := c.snapshotRuntimeState()
				scopeState := c.snapshotScopeState()
				opaqueDispatch := false
				var indexSetterType *TypeExpr
				var memberSetter *MemberExpr
				switch target := typed.Target.(type) {
				case *IndexExpr:
					indexSetterType = c.inferExpressionType(target.Object)
					opaqueDispatch = c.instanceDispatchHasOpaqueClassConstantEffects(
						indexSetterType,
						"[]=",
					)
				case *MemberExpr:
					memberSetter = target
					opaqueDispatch = c.memberSetterHasOpaqueClassConstantEffects(target)
				}
				expectation := c.assignmentValueExpectation(typed.Target, typed.Value)
				rhsCompleted := c.checkExpressionWithExpectation(function, typed.Value, expectation)
				c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
				if !rhsCompleted {
					if rhsAlwaysEvaluates {
						c.recordNonCompletingExpression()
						return
					}
					targetMayWrite = false
					inferWrite = false
					c.restoreRuntimeState(runtimeState)
					c.restoreScopeState(scopeState)
					break
				}
				if indexSetterType != nil {
					c.enqueueReachableInstanceDispatch(indexSetterType, "[]=")
				}
				if memberSetter != nil {
					c.enqueueReachableMemberSetter(memberSetter)
				}
				if opaqueDispatch {
					c.markOpaqueClassConstants()
				}
				if !c.assignmentSetterMayComplete(typed.Target, typed.Value) {
					if rhsAlwaysEvaluates {
						c.recordNonCompletingExpression()
						return
					}
					targetMayWrite = false
					inferWrite = false
					c.restoreRuntimeState(runtimeState)
					c.restoreScopeState(scopeState)
					break
				}
				if !rhsAlwaysEvaluates {
					c.restoreRuntimeStatePreservingClassConstantEffects(runtimeState)
					evaluatedScopeState := c.snapshotScopeState()
					c.mergeScopeStates(scopeState, []checkScopeState{scopeState, evaluatedScopeState})
				}
			}
		default:
			if !c.checkExpression(function, typed.Target) {
				c.recordNonCompletingExpression()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Target)
			operatorType := c.inferExpressionType(typed.Target)
			opaqueOperator := c.binaryDispatchHasOpaqueClassConstantEffects(operatorType, typed.Operator)
			opaqueSetter := false
			var indexSetterType *TypeExpr
			var memberSetter *MemberExpr
			switch target := typed.Target.(type) {
			case *IndexExpr:
				indexSetterType = c.inferExpressionType(target.Object)
				opaqueSetter = c.instanceDispatchHasOpaqueClassConstantEffects(
					indexSetterType,
					"[]=",
				)
			case *MemberExpr:
				memberSetter = target
				opaqueSetter = c.memberSetterHasOpaqueClassConstantEffects(target)
			}
			if !c.checkExpression(function, typed.Value) {
				c.recordNonCompletingExpression()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
			c.enqueueReachableInstanceDispatch(operatorType, binaryDispatchMethodNames(typed.Operator)...)
			if opaqueOperator {
				c.markOpaqueClassConstants()
			}
			operatorValue := &BinaryExpr{
				Left:     typed.Target,
				Operator: typed.Operator,
				Right:    typed.Value,
				Position: typed.Pos(),
			}
			if !c.binaryExpressionMayComplete(operatorValue) {
				c.inferAssignStatementTypes(function, typed)
				c.recordNonCompletingExpression()
				return
			}
			if indexSetterType != nil {
				c.enqueueReachableInstanceDispatch(indexSetterType, "[]=")
			}
			if memberSetter != nil {
				c.enqueueReachableMemberSetter(memberSetter)
			}
			if opaqueSetter {
				c.markOpaqueClassConstants()
			}
			if !c.assignmentSetterMayComplete(typed.Target, operatorValue) {
				c.recordNonCompletingExpression()
				return
			}
		}
		if inferWrite {
			c.inferAssignStatementTypes(function, typed)
		}
		if targetMayWrite {
			c.recordRuntimeBindingTarget(typed.Target)
		}
		c.recordBindingTarget(typed.Target)
		c.captureImplicitReturnState(typed)
	case *ExprStmt:
		completed := c.checkExpression(function, typed.Expr)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Expr)
		if !completed {
			c.recordNonCompletingExpression()
			return
		}
		c.captureImplicitReturnState(typed)
	case *ClassStmt:
		classDef := c.script.classes[typed.Name]
		if classDef != nil {
			c.checkRuntimeClassBody(classDef, false)
		}
	case *IfStmt:
		baseRuntimeState := c.snapshotRuntimeState()
		baseScopeState := c.snapshotScopeState()
		if !c.checkExpression(function, typed.Condition) {
			c.recordNonCompletingExpression()
			return
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Condition)
		conditionRuntimeState := c.snapshotRuntimeState()
		conditionScopeState := c.snapshotScopeState()
		fallthroughRuntimeStates := make([]checkRuntimeState, 0, len(typed.ElseIf)+2)
		fallthroughScopeStates := make([]checkScopeState, 0, len(typed.ElseIf)+2)
		finish := func() {
			c.mergeRuntimeStates(baseRuntimeState, fallthroughRuntimeStates)
			c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
			c.stmtNoFallthroughInferred = len(fallthroughRuntimeStates) == 0
		}

		conditionTruthy, conditionKnown := c.inferredConditionTruthiness(typed.Condition)
		trueReachable := !conditionKnown || conditionTruthy
		if trueReachable {
			trueReachable = c.collectRuntimeConditionOutcomeEffects(typed.Condition, true)
		}
		if trueReachable {
			if c.checkStatements(function, returnType, typed.Consequent) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		falseReachable := !conditionKnown || !conditionTruthy
		if falseReachable {
			falseReachable = c.collectRuntimeConditionOutcomeEffects(typed.Condition, false)
		}
		if c.implicitIfDecisions != nil {
			c.implicitIfDecisions[typed] = append(c.implicitIfDecisions[typed][:0],
				conditionDecisionFromOutcomes(conditionTruthy, conditionKnown, trueReachable, falseReachable))
		}
		if !falseReachable {
			finish()
			return
		}
		falseRuntimeState := c.snapshotRuntimeState()
		falseScopeState := c.snapshotScopeState()
		for _, elseIf := range typed.ElseIf {
			c.restoreRuntimeState(falseRuntimeState)
			c.restoreScopeState(falseScopeState)
			if !c.checkExpression(function, elseIf.Condition) {
				c.captureExceptionExitState()
				c.captureEnsureExitState()
				finish()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(elseIf.Condition)
			conditionRuntimeState = c.snapshotRuntimeState()
			conditionScopeState = c.snapshotScopeState()
			branchTruthy, branchKnown := c.inferredConditionTruthiness(elseIf.Condition)
			trueReachable = !branchKnown || branchTruthy
			if trueReachable {
				trueReachable = c.collectRuntimeConditionOutcomeEffects(elseIf.Condition, true)
			}
			if trueReachable {
				if c.checkStatements(function, returnType, elseIf.Consequent) {
					fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
					fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
				}
			}
			c.restoreRuntimeState(conditionRuntimeState)
			c.restoreScopeState(conditionScopeState)
			falseReachable = !branchKnown || !branchTruthy
			if falseReachable {
				falseReachable = c.collectRuntimeConditionOutcomeEffects(elseIf.Condition, false)
			}
			if c.implicitIfDecisions != nil {
				c.implicitIfDecisions[typed] = append(c.implicitIfDecisions[typed],
					conditionDecisionFromOutcomes(branchTruthy, branchKnown, trueReachable, falseReachable))
			}
			if !falseReachable {
				finish()
				return
			}
			falseRuntimeState = c.snapshotRuntimeState()
			falseScopeState = c.snapshotScopeState()
		}
		c.restoreRuntimeState(falseRuntimeState)
		c.restoreScopeState(falseScopeState)
		if c.checkStatements(function, returnType, typed.Alternate) {
			fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
			fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
		}
		finish()
	case *ForStmt:
		// The iterable evaluates once with pre-loop facts before any body
		// iteration, so it is checked (and the element type captured) before
		// body-assigned locals degrade; the body itself may run zero or many
		// times, so those locals lose their facts for the walk and stay
		// unknown after it.
		if !c.checkExpression(function, typed.Iterable) {
			c.recordNonCompletingExpression()
			return
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Iterable)
		elemType := c.forTargetElementType(typed)
		c.recordLiveStatementNames(typed.Body)
		c.degradeLocalTypesForBindings(typed.Body, typed.Target)
		c.recordBindingTarget(typed.Target)
		c.bindForTargetType(typed, elemType)
		bodyRuntimeState := c.snapshotRuntimeState()
		bodyScopeState := c.snapshotScopeState()
		c.mutationRegionDepth++
		loopEffects := c.checkLoopStatements(function, returnType, typed.Body)
		c.mutationRegionDepth--
		c.restoreRuntimeState(bodyRuntimeState)
		c.applyClassConstantEffects(loopEffects)
		c.restoreScopeState(bodyScopeState)
		c.degradeLocalTypesForBindings(nil, typed.Target)
		c.recordLocalBindings(typed.Body)
	case *WhileStmt:
		// The condition's first evaluation sees pre-loop facts, so it is
		// checked before body-assigned locals degrade to unknown. Prove the
		// body outcome reachable against those facts before degradation, then
		// reapply it to the conservative loop-body state for the actual walk.
		if !c.checkExpression(function, typed.Condition) {
			c.recordNonCompletingExpression()
			return
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Condition)
		conditionRuntimeState := c.snapshotRuntimeState()
		conditionScopeState := c.snapshotScopeState()
		bodyReachable := c.collectRuntimeConditionOutcomeEffects(typed.Condition, true)
		conditionRefinedScopeState := c.snapshotScopeState()
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		if bodyReachable {
			c.recordLiveStatementNames(typed.Body)
			c.degradeLocalTypesForBindings(typed.Body)
			bodyRuntimeState := c.snapshotRuntimeState()
			bodyScopeState := c.snapshotScopeState()
			c.applyLoopEntryTypeRefinements(conditionScopeState.types, conditionRefinedScopeState.types)
			if c.collectRuntimeConditionOutcomeEffects(typed.Condition, true) {
				c.mutationRegionDepth++
				loopEffects := c.checkLoopStatements(function, returnType, typed.Body)
				c.mutationRegionDepth--
				c.restoreRuntimeState(bodyRuntimeState)
				c.applyClassConstantEffects(loopEffects)
			} else {
				c.restoreRuntimeState(bodyRuntimeState)
			}
			c.restoreScopeState(bodyScopeState)
		}
		c.recordLocalBindings(typed.Body)
	case *UntilStmt:
		if !c.checkExpression(function, typed.Condition) {
			c.recordNonCompletingExpression()
			return
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Condition)
		conditionRuntimeState := c.snapshotRuntimeState()
		conditionScopeState := c.snapshotScopeState()
		bodyReachable := c.collectRuntimeConditionOutcomeEffects(typed.Condition, false)
		conditionRefinedScopeState := c.snapshotScopeState()
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		if bodyReachable {
			c.recordLiveStatementNames(typed.Body)
			c.degradeLocalTypesForBindings(typed.Body)
			bodyRuntimeState := c.snapshotRuntimeState()
			bodyScopeState := c.snapshotScopeState()
			c.applyLoopEntryTypeRefinements(conditionScopeState.types, conditionRefinedScopeState.types)
			if c.collectRuntimeConditionOutcomeEffects(typed.Condition, false) {
				c.mutationRegionDepth++
				loopEffects := c.checkLoopStatements(function, returnType, typed.Body)
				c.mutationRegionDepth--
				c.restoreRuntimeState(bodyRuntimeState)
				c.applyClassConstantEffects(loopEffects)
			} else {
				c.restoreRuntimeState(bodyRuntimeState)
			}
			c.restoreScopeState(bodyScopeState)
		}
		c.recordLocalBindings(typed.Body)
	case *TryStmt:
		selectedRescue, rescueSelectionExact := c.staticallySelectedRescue(typed.Body, typed.Rescues)
		rescueBodiesReachable := !statementsProvenNonRaising(typed.Body)
		ensureAlwaysExits := blockAlwaysExits(typed.Ensure)
		deferReturnType := returnType != nil && len(typed.Ensure) > 0 && !ensureAlwaysExits
		branchReturnType := returnType
		if deferReturnType || ensureAlwaysExits {
			branchReturnType = nil
		}
		// An always-exiting ensure replaces every value that the body, else,
		// or rescue would otherwise return. Keep those overridden facts out of
		// an unannotated function summary, then collect the ensure itself into
		// the caller's active collector.
		summaryCollector := c.returnCollector
		if summaryCollector != nil && ensureAlwaysExits {
			c.returnCollector = &returnSummaryCollector{}
		}
		baseRuntimeState := c.snapshotRuntimeState()
		baseScopeState := c.snapshotScopeState()
		previousExceptionExitSites := c.exceptionExitSites
		var exceptionExitSites []checkStateSnapshot
		if len(typed.Rescues) > 0 {
			c.exceptionExitSites = &exceptionExitSites
		}
		previousEnsureExitSites := c.ensureExitSites
		var ensureExitSites []checkStateSnapshot
		if len(typed.Ensure) > 0 {
			c.ensureExitSites = &ensureExitSites
		}
		fallthroughRuntimeStates := make([]checkRuntimeState, 0, 2)
		fallthroughScopeStates := make([]checkScopeState, 0, 2)
		// Every ensure collects the returns entering it so the ensure walk sees
		// their states. A non-exiting ensure hands those returns up to an outer
		// ensure; an exiting ensure replaces them and discards the captured
		// sites before checking its own return paths.
		captureReturnSites := len(typed.Ensure) > 0
		armCapture := captureReturnSites && !ensureAlwaysExits
		var deferredSites []deferredReturnSite
		var previousSites *[]deferredReturnSite
		if captureReturnSites {
			previousSites = c.deferredReturnSites
			c.deferredReturnSites = &deferredSites
		}
		previousLoopExitEffects := c.loopExitEffects
		previousRetryExitSites := c.retryExitSites
		var retryExitSites []checkStateSnapshot
		c.retryExitSites = &retryExitSites
		var protectedLoopExitEffects *checkLoopExitEffects
		if len(typed.Ensure) > 0 && previousLoopExitEffects != nil {
			protectedLoopExitEffects = &checkLoopExitEffects{}
			c.loopExitEffects = protectedLoopExitEffects
		}

		bodyFallsThrough := false
		bodyEffects := c.captureClassConstantEffects(func() {
			bodyFallsThrough = c.checkStatements(function, branchReturnType, typed.Body)
		})
		if len(typed.Rescues) > 0 {
			c.exceptionExitSites = previousExceptionExitSites
		}
		ensureEffects := cloneCheckClassConstantEffects(bodyEffects)
		if bodyFallsThrough {
			elseFallsThrough := false
			elseEffects := c.captureClassConstantEffects(func() {
				elseFallsThrough = c.checkStatements(function, branchReturnType, typed.Else)
			})
			mergeCheckClassConstantEffects(&ensureEffects, elseEffects)
			if elseFallsThrough {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		// When handler i runs, every earlier clause was skipped and predeclared
		// its body locals as surrounding-scope nils, so each clause is checked
		// with the accumulated locals of the clauses before it in scope.
		earlierClauseLocals := map[string]struct{}{}
		recordClauseLocals := func(clause *RescueClause) {
			clauseLocals := map[string]struct{}{}
			collectLocalBindings(clause.Body, clauseLocals)
			delete(clauseLocals, clause.Binding)
			for name := range clauseLocals {
				earlierClauseLocals[name] = struct{}{}
			}
		}
		for i := range typed.Rescues {
			clause := &typed.Rescues[i]
			if !rescueBodiesReachable || rescueSelectionExact && selectedRescue < 0 {
				break
			}
			if rescueSelectionExact && i < selectedRescue {
				recordClauseLocals(clause)
				continue
			}
			if rescueSelectionExact && i > selectedRescue {
				break
			}
			// An empty clause never falls through (the matched error propagates
			// after ensure), so it must not merge the base state into the paths
			// that reach the code after the block.
			if len(clause.Body) == 0 {
				if rescueSelectionExact {
					break
				}
				continue
			}
			if len(exceptionExitSites) == 0 {
				c.restoreRuntimeState(baseRuntimeState)
				c.applyClassConstantEffects(bodyEffects)
				c.restoreScopeState(baseScopeState)
			} else {
				runtimeStates := make([]checkRuntimeState, 0, len(exceptionExitSites))
				scopeStates := make([]checkScopeState, 0, len(exceptionExitSites))
				for _, site := range exceptionExitSites {
					runtimeStates = append(runtimeStates, site.runtimeState)
					scopeStates = append(scopeStates, site.scopeState)
				}
				c.mergeRuntimeStates(baseRuntimeState, runtimeStates)
				c.mergeScopeStates(baseScopeState, scopeStates)
			}
			popEarlier := func() {}
			if len(earlierClauseLocals) > 0 {
				scope := make(map[string]struct{}, len(earlierClauseLocals))
				for name := range earlierClauseLocals {
					scope[name] = struct{}{}
				}
				popEarlier = c.pushScope(scope)
			}
			popScope := c.pushRescueScope(clause)
			clauseFallsThrough := false
			clauseEffects := c.captureClassConstantEffects(func() {
				clauseFallsThrough = c.checkStatements(function, branchReturnType, clause.Body)
			})
			mergeCheckClassConstantEffects(&ensureEffects, clauseEffects)
			popScope()
			popEarlier()
			recordClauseLocals(clause)
			if clauseFallsThrough {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
			if rescueSelectionExact {
				break
			}
		}
		c.retryExitSites = previousRetryExitSites
		for _, retrySite := range retryExitSites {
			c.restoreRuntimeState(retrySite.runtimeState)
			c.restoreScopeState(retrySite.scopeState)
			retryBodyFallsThrough := false
			retryEffects := c.captureClassConstantEffects(func() {
				retryBodyFallsThrough = c.checkStatements(function, branchReturnType, typed.Body)
			})
			mergeCheckClassConstantEffects(&ensureEffects, retryEffects)
			if !retryBodyFallsThrough {
				continue
			}
			retryElseFallsThrough := false
			retryElseEffects := c.captureClassConstantEffects(func() {
				retryElseFallsThrough = c.checkStatements(function, branchReturnType, typed.Else)
			})
			mergeCheckClassConstantEffects(&ensureEffects, retryElseEffects)
			if retryElseFallsThrough {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		if captureReturnSites {
			c.deferredReturnSites = previousSites
		}
		if protectedLoopExitEffects != nil {
			c.loopExitEffects = previousLoopExitEffects
		}
		if len(typed.Ensure) > 0 {
			c.ensureExitSites = previousEnsureExitSites
		}
		mergeRuntimeStates := fallthroughRuntimeStates
		mergeScopeStates := fallthroughScopeStates
		fallthroughClassConstantEffects := runtimeStatesClassConstantEffects(
			baseRuntimeState,
			fallthroughRuntimeStates,
		)
		// The ensure block runs on every path into it — fallthrough or
		// deferred return — so the return sites' states join the merge the
		// ensure walk (and the code after the block) sees.
		for _, site := range deferredSites {
			mergeRuntimeStates = append(mergeRuntimeStates, site.runtimeState)
			mergeScopeStates = append(mergeScopeStates, site.scopeState)
		}
		for _, site := range ensureExitSites {
			mergeRuntimeStates = append(mergeRuntimeStates, site.runtimeState)
			mergeScopeStates = append(mergeScopeStates, site.scopeState)
		}
		// An exiting path's namespace writes reach the ensure walk but never
		// the code after the block, which runs only on a fall-through path.
		// Remember the continuation markers before the exit states join.
		var continuationMembers map[string]struct{}
		if len(ensureExitSites) > 0 {
			continuationMembers = cloneCheckStringSet(baseRuntimeState.namespaceMembers)
			for _, state := range fallthroughRuntimeStates {
				continuationMembers = unionCheckStringSet(continuationMembers, state.namespaceMembers)
			}
		}
		c.mergeRuntimeStates(baseRuntimeState, mergeRuntimeStates)
		c.mergeScopeStates(baseScopeState, mergeScopeStates)
		if summaryCollector != nil && ensureAlwaysExits {
			c.returnCollector = summaryCollector
		}
		// Deferred returns reach ensure, but not the statement after the try.
		// Feed their class-constant effects to ensure through the context below
		// while keeping only normal fallthrough effects in the continuing state.
		c.setClassConstantEffects(fallthroughClassConstantEffects)
		ensureFallsThrough := true
		if len(typed.Ensure) > 0 {
			previousContext := cloneCheckClassConstantEffects(c.classConstantContext)
			mergeCheckClassConstantEffects(&c.classConstantContext, ensureEffects)
			ensureFallsThrough = c.checkStatements(function, returnType, typed.Ensure)
			c.classConstantContext = previousContext
			if ensureFallsThrough && protectedLoopExitEffects != nil && protectedLoopExitEffects.seen {
				mergeCheckClassConstantEffects(
					&protectedLoopExitEffects.effects,
					c.currentClassConstantEffects(),
				)
				previousLoopExitEffects.seen = true
				mergeCheckClassConstantEffects(
					&previousLoopExitEffects.effects,
					protectedLoopExitEffects.effects,
				)
			}
		}
		// An ensure the walk proves always exits replaces every deferred
		// body return, even when the proof is inferred rather than
		// syntactic, so those arms must not widen the summary.
		if c.returnCollector != nil && armCapture && previousSites == nil && ensureFallsThrough {
			c.recordDeferredReturnSummaryFacts(deferredSites)
		}
		if deferReturnType && ensureFallsThrough {
			c.checkDeferredReturnSitesAfterEnsure(function, returnType, typed.Ensure, deferredSites)
		}
		if len(ensureExitSites) > 0 {
			// The ensure body's own possible writes run on the fall-through
			// path too, so they persist past the block; only the returned
			// paths' markers scope back out.
			ensureScan := c.newNamespaceMutationScan()
			ensureScan.statements(typed.Ensure)
			c.runtimeNamespaceMembers = unionCheckStringSet(continuationMembers, ensureScan.out)
		}
		if armCapture && previousSites != nil && ensureFallsThrough {
			*previousSites = append(*previousSites, deferredSites...)
		}
		// No fallthrough path means the code after the block is
		// unreachable: deferred returns exit through the ensure. An ensure
		// the walk proves always exits blocks every path the same way.
		c.stmtNoFallthroughInferred = len(fallthroughRuntimeStates) == 0 || !ensureFallsThrough
		if !c.stmtNoFallthroughInferred {
			c.bindUnreachedRescueLocalsAsNil(typed)
		}
	}
}

// withImplicitReturnCapture arms leaf-state capture for a function whose
// return annotation the implicit-return pass will check, and returns the
// restore for the previous capture maps.
func (c *scriptChecker) withImplicitReturnCapture(fn *ScriptFunction) func() {
	previousLeaves := c.implicitReturnLeaves
	previousStates := c.implicitReturnStates
	previousDecisions := c.implicitIfDecisions
	c.implicitReturnLeaves = nil
	c.implicitReturnStates = nil
	c.implicitIfDecisions = nil
	if fn != nil && fn.ReturnTy != nil && len(fn.Body) > 0 {
		leaves := make(map[Statement]struct{})
		collectImplicitReturnLeaves(fn.Body, leaves)
		if len(leaves) > 0 {
			c.implicitReturnLeaves = leaves
			c.implicitReturnStates = make(map[Statement]checkStateSnapshot, len(leaves))
			c.implicitIfDecisions = make(map[*IfStmt][]conditionDecision)
		}
	}
	return func() {
		c.implicitReturnLeaves = previousLeaves
		c.implicitReturnStates = previousStates
		c.implicitIfDecisions = previousDecisions
	}
}

// checkStateSnapshot pairs the runtime and scope state at a point of the
// walk so a later, out-of-order check can run under the facts that held
// there.
type checkStateSnapshot struct {
	runtimeState checkRuntimeState
	scopeState   checkScopeState
	typePoison   map[string]struct{}
}

func (c *scriptChecker) captureExceptionExitState() {
	if c.exceptionExitSites == nil {
		return
	}
	*c.exceptionExitSites = append(*c.exceptionExitSites, checkStateSnapshot{
		runtimeState: c.snapshotRuntimeState(),
		scopeState:   c.snapshotScopeState(),
	})
}

func (c *scriptChecker) captureEnsureExitState() {
	if c.ensureExitSites == nil {
		return
	}
	*c.ensureExitSites = append(*c.ensureExitSites, checkStateSnapshot{
		runtimeState: c.snapshotRuntimeState(),
		scopeState:   c.snapshotScopeState(),
	})
}

// collectImplicitReturnLeaves gathers the statements whose expressions the
// implicit-return pass will check (the effective final statement's yielding
// leaves across branches), so the walk can capture their branch-local state
// before the branch merges run.
func collectImplicitReturnLeaves(statements []Statement, out map[Statement]struct{}) {
	if len(statements) == 0 {
		return
	}
	collectImplicitReturnLeafStatement(effectiveFinalStatement(statements), out)
}

func collectImplicitReturnLeafStatement(stmt Statement, out map[Statement]struct{}) {
	switch typed := stmt.(type) {
	case *ExprStmt, *AssignStmt:
		out[stmt] = struct{}{}
	case *IfStmt:
		collectImplicitReturnLeaves(typed.Consequent, out)
		for _, elseIf := range typed.ElseIf {
			collectImplicitReturnLeaves(elseIf.Consequent, out)
		}
		collectImplicitReturnLeaves(typed.Alternate, out)
	case *TryStmt:
		collectImplicitReturnLeaves(typed.Body, out)
		collectImplicitReturnLeaves(typed.Else, out)
		for i := range typed.Rescues {
			collectImplicitReturnLeaves(typed.Rescues[i].Body, out)
		}
	}
}

// captureImplicitReturnState snapshots the state right after an
// implicit-return leaf's own walk, before enclosing branch merges dilute its
// branch-local facts.
func (c *scriptChecker) captureImplicitReturnState(stmt Statement) {
	if c.implicitReturnLeaves == nil {
		return
	}
	if _, ok := c.implicitReturnLeaves[stmt]; !ok {
		return
	}
	c.implicitReturnStates[stmt] = checkStateSnapshot{
		runtimeState: c.snapshotRuntimeState(),
		scopeState:   c.snapshotScopeState(),
	}
}

// checkImplicitLeafAgainstType checks an implicit return expression under the
// state captured at its own walk when available.
func (c *scriptChecker) checkImplicitLeafAgainstType(function string, stmt Statement, expr Expression, ty *TypeExpr) {
	state, ok := c.implicitReturnStates[stmt]
	if !ok {
		if c.implicitReturnStates != nil {
			return
		}
		c.checkRuntimeExpressionAgainstType(function, expr, ty, "return value")
		return
	}
	currentRuntime := c.snapshotRuntimeState()
	currentScope := c.snapshotScopeState()
	c.restoreRuntimeState(state.runtimeState)
	c.restoreScopeState(state.scopeState)
	c.checkRuntimeExpressionAgainstType(function, expr, ty, "return value")
	c.restoreRuntimeState(currentRuntime)
	c.restoreScopeState(currentScope)
}

// deferredReturnSite records the state at a return statement whose type
// check the enclosing begin/ensure deferred, so the check runs against the
// facts the return expression was actually evaluated under — including
// branch-local facts that the branch merges discard before the ensure walk.
type deferredReturnSite struct {
	runtimeState checkRuntimeState
	scopeState   checkScopeState
	stmt         *ReturnStmt
}

func (c *scriptChecker) checkDeferredReturnSitesAfterEnsure(function string, returnType *TypeExpr, ensure []Statement, sites []deferredReturnSite) {
	runtimeState := c.snapshotRuntimeState()
	scopeState := c.snapshotScopeState()
	defer func() {
		c.restoreRuntimeState(runtimeState)
		c.restoreScopeState(scopeState)
	}()

	for _, site := range sites {
		c.restoreRuntimeState(site.runtimeState)
		c.restoreScopeState(site.scopeState)
		c.withSuppressedWarnings(func() {
			c.withRuntimeModuleCollection(func() {
				c.collectRequiredModuleExportsFromStatements(ensure)
			})
		})
		c.checkReturnStatementType(function, returnType, site.stmt)
	}
}

// collectRuntimeConditionOutcomeEffects applies what a condition's requested
// outcome implies to the current branch state and reports whether that outcome
// remains reachable. Require exports that must have bound for the outcome to
// hold and narrowed local facts are retained only by callers that observe a
// reachable result. Truthiness tests, explicit nil comparisons, nil?,
// negation, and short-circuit compositions narrow in both directions;
// everything else leaves facts untouched.
func (c *scriptChecker) collectRuntimeConditionOutcomeEffects(expr Expression, truthy bool) bool {
	return c.applyConditionOutcomeEffects(expr, truthy, c.collectRuntimeRequireCallExportsFromExpression)
}

// probeConditionOutcome tests an outcome using the same narrowing rules and
// returns its refined scope without retaining facts or collecting runtime
// module effects.
func (c *scriptChecker) probeConditionOutcome(expr Expression, truthy bool) (checkScopeState, bool) {
	scopeState := c.snapshotScopeState()
	reachable := c.applyConditionOutcomeEffects(expr, truthy, nil)
	refinedScopeState := c.snapshotScopeState()
	c.restoreScopeState(scopeState)
	return refinedScopeState, reachable
}

func (c *scriptChecker) applyConditionOutcomeEffects(expr Expression, truthy bool, collectRequired func(Expression)) bool {
	if inferred, known := c.inferredConditionTruthiness(expr); known && inferred != truthy {
		return false
	}

	switch typed := expr.(type) {
	case *Identifier:
		return c.narrowLocalTruthiness(typed.Name, truthy)
	case *UnaryExpr:
		if typed.Operator == tokenBang {
			return c.applyConditionOutcomeEffects(typed.Right, !truthy, collectRequired)
		}
	case *MemberExpr:
		return c.narrowNilPredicateMember(typed, truthy)
	case *CallExpr:
		if member, ok := typed.Callee.(*MemberExpr); ok &&
			len(typed.KwArgs) == 0 && typed.Block == nil && typed.BlockArg == nil {
			switch len(typed.Args) {
			case 0:
				return c.narrowNilPredicateMember(member, truthy)
			case 1:
				switch member.Property {
				case isAMemberName, kindOfMemberName, instanceOfMemberName:
					return c.narrowClassPredicateMember(member, typed.Args[0], truthy)
				case isTypeMemberName:
					return c.narrowIsTypePredicateMember(member, typed.Args[0], truthy)
				}
			}
		}
	case *BinaryExpr:
		switch typed.Operator {
		case tokenAnd:
			if truthy {
				if !c.applyConditionOutcomeEffects(typed.Left, true, collectRequired) {
					return false
				}
				if collectRequired != nil {
					collectRequired(typed.Right)
				}
				return c.applyConditionOutcomeEffects(typed.Right, true, collectRequired)
			}
			if binaryRightAlwaysEvaluates(typed) || c.binaryRightAlwaysEvaluatesInferred(typed) {
				if !c.applyConditionOutcomeEffects(typed.Left, true, collectRequired) {
					return false
				}
				return c.applyConditionOutcomeEffects(typed.Right, false, collectRequired)
			}
		case tokenOr:
			if !truthy {
				if !c.applyConditionOutcomeEffects(typed.Left, false, collectRequired) {
					return false
				}
				if collectRequired != nil {
					collectRequired(typed.Right)
				}
				return c.applyConditionOutcomeEffects(typed.Right, false, collectRequired)
			}
			if binaryRightAlwaysEvaluates(typed) || c.binaryRightAlwaysEvaluatesInferred(typed) {
				if !c.applyConditionOutcomeEffects(typed.Left, false, collectRequired) {
					return false
				}
				return c.applyConditionOutcomeEffects(typed.Right, true, collectRequired)
			}
		case tokenEQ, tokenNotEQ:
			if name, ok := identifierNilComparison(typed); ok {
				return c.narrowLocalNilness(name, (typed.Operator == tokenEQ) == truthy)
			}
		}
	}
	return true
}

// identifierNilComparison matches `x == nil`, `nil == x`, `x != nil`, and
// `nil != x` for a plain identifier x.
func identifierNilComparison(expr *BinaryExpr) (string, bool) {
	if _, ok := expr.Left.(*NilLiteral); ok {
		ident, ok := expr.Right.(*Identifier)
		if !ok {
			return "", false
		}
		return ident.Name, true
	}
	if _, ok := expr.Right.(*NilLiteral); ok {
		ident, ok := expr.Left.(*Identifier)
		if !ok {
			return "", false
		}
		return ident.Name, true
	}
	return "", false
}

func (c *scriptChecker) checkReturnStatementType(function string, returnType *TypeExpr, stmt *ReturnStmt) {
	if stmt == nil {
		return
	}
	if stmt.Value == nil {
		c.checkRuntimeNilAgainstType(function, stmt.Pos(), returnType, "return value")
		return
	}
	c.checkRuntimeExpressionAgainstType(function, stmt.Value, returnType, "return value")
}

func (c *scriptChecker) checkExpression(function string, expr Expression) bool {
	return c.checkExpressionWithAuto(function, expr, true)
}

func (c *scriptChecker) checkExpressionWithExpectation(
	function string,
	expr Expression,
	expectation expressionExpectation,
) bool {
	if expectation.empty() {
		return c.checkExpression(function, expr)
	}
	if expectation.includesCallable() {
		if _, bindable := c.bareMemberArgumentCallableFact(expr); bindable {
			return c.checkExpressionWithAuto(function, expr, false)
		}
		if callableExpr, bindable := c.bareIdentifierCallableArgument(expr); bindable {
			if call, ok := callableExpr.(*CallExpr); ok {
				callableExpr = call.Callee
			}
			return c.checkExpressionWithAuto(function, callableExpr, false)
		}
	}
	switch typed := expr.(type) {
	case *ConditionalExpr:
		return c.checkConditionalExpression(function, typed, expectation)
	case *IfExpr:
		return c.checkIfExpression(function, typed, expectation)
	case *CaseExpr:
		return c.checkCaseExpression(function, typed, expectation)
	case *ArrayLiteral:
		elementExpectation, ok := expectation.arrayElementExpectation()
		if !ok {
			break
		}
		for i, element := range typed.Elements {
			if !c.checkExpressionWithExpectation(function, element, elementExpectation(i, len(typed.Elements))) {
				return false
			}
		}
		return true
	case *HashLiteral:
		if typed.ShapeType != nil && !c.hashShapeStaticallyShadowed(typed) {
			return true
		}
		if !hashLiteralTypeHasValueSlots(expectation.ty) {
			break
		}
		for _, pair := range typed.Pairs {
			if !c.checkExpression(function, pair.Key) {
				return false
			}
			valueExpectation := expressionExpectation{}
			if key, ok := staticLiteralValue(pair.Key); ok {
				valueExpectation = typeExpressionExpectation(hashLiteralValueType(expectation.ty, key))
			}
			if !c.checkExpressionWithExpectation(function, pair.Value, valueExpectation) {
				return false
			}
		}
		return true
	case *MemberExpr:
		// Callable expectations preserve bound script methods only. Runtime
		// still auto-invokes generated getters and non-bindable builtins.
		return c.checkExpressionWithAuto(function, typed, true)
	}
	return c.checkExpressionWithAuto(function, expr, !expectation.includesCallable())
}

func autoCallExpectation(autoCall bool) expressionExpectation {
	if autoCall {
		return expressionExpectation{}
	}
	return typeExpressionExpectation(checkTypeFunction)
}

func (c *scriptChecker) checkExpressionWithAuto(function string, expr Expression, autoCall bool) bool {
	completed := c.checkExpressionWithAutoInner(function, expr, autoCall)
	if !completed && c.expressionExitSites != nil {
		*c.expressionExitSites = append(*c.expressionExitSites, checkStateSnapshot{
			runtimeState: c.snapshotRuntimeState(),
			scopeState:   c.snapshotScopeState(),
			typePoison:   cloneCheckStringSet(c.typePoison),
		})
	}
	return completed
}

func (c *scriptChecker) checkExpressionWithAutoInner(function string, expr Expression, autoCall bool) bool {
	switch typed := expr.(type) {
	case nil, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return true
	case *Identifier:
		c.checkIdentifierResolved(function, typed)
		if autoCall {
			c.enqueueReachableIdentifierCall(typed)
			c.applyAutoInvokedIdentifierNamespaceMutations(typed)
			return c.autoInvokedIdentifierMayComplete(typed)
		}
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if !c.checkExpressionWithAuto(function, elem, true) {
				return false
			}
		}
	case *HashLiteral:
		// A dual-reading braced group evaluates as a shape unless one of its
		// type names is shadowed, so its identifier values are type spellings
		// rather than variable reads and must not warn as undefined.
		if typed.ShapeType != nil && !c.hashShapeStaticallyShadowed(typed) {
			return true
		}
		for _, pair := range typed.Pairs {
			if !c.checkExpressionWithAuto(function, pair.Key, true) ||
				!c.checkExpressionWithAuto(function, pair.Value, true) {
				return false
			}
		}
	case *TypeLiteral:
		// An unshadowed type literal's identifiers are type spellings rather
		// than variable reads and must not warn as undefined.
		if !c.typeLiteralStaticallyShadowed(typed) {
			return true
		}
		if !c.checkExpressionWithAuto(function, typed.Fallback, true) {
			return false
		}
	case *CallExpr:
		// The receiver's nil-ness resolves from the facts at its evaluation
		// point, before member dispatch poisons the receiver's own facts.
		callSkipsInferred := c.safeNavigationCallSkipsInferred(typed)
		argumentsAlwaysEvaluate := c.safeNavigationArgumentsAlwaysEvaluateInferred(typed)
		var invokedLambda *BlockLiteral
		if member, ok := typed.Callee.(*MemberExpr); ok && member.Property == "call" {
			invokedLambda = c.resolveImmediateLambdaBlock(member.Object)
		}
		if !c.checkExpressionWithAuto(function, typed.Callee, false) {
			return false
		}
		if staticNilSafeNavigationCall(typed) || callSkipsInferred {
			return true
		}
		argumentsMayBeSkipped := safeNavigationCallMaySkipArguments(typed) && !argumentsAlwaysEvaluate
		var argumentState checkRuntimeState
		var argumentScopeState checkScopeState
		if argumentsMayBeSkipped {
			argumentState = c.snapshotRuntimeState()
			argumentScopeState = c.snapshotScopeState()
		}
		// The runtime resolves the call target when the callee evaluates,
		// before any argument runs, so a require inside an argument must
		// not make the callee's contract visible to this same call. The
		// callee body still checks under the post-argument state: dispatch
		// happens after the arguments (and their requires) evaluate.
		target, targetResolved := c.resolveCallable(typed)
		if targetResolved && target.fn == nil && target.spec.resultType != nil && !callExpandsArguments(typed) {
			// The invariant result belongs to the target selected before an
			// argument can rebind the same builtin namespace member.
			result := target.spec.resultType
			if member, ok := typed.Callee.(*MemberExpr); ok {
				result = c.safeNavigationMemberResultFact(member, result)
			}
			c.pinExpressionFact(typed, result)
		}
		dynamicCandidates := c.captureDynamicCallCandidates(typed)
		if c.pinDirectConstructorInstanceFact(typed) {
			// A direct member call resolves its callee before evaluating any
			// argument. An exhaustive set of only plain modules therefore
			// raises at `.new` without running argument or block effects.
			return false
		}
		deferForwardedTargets := callResolvesForwardedTargetAfterArguments(typed, target, targetResolved)
		var dynamicResolution checkDynamicCallResolution
		if !deferForwardedTargets {
			dynamicResolution = c.exactDynamicCallTargets(typed, target, targetResolved, dynamicCandidates)
		}
		if c.callCalleeLookupFails(
			typed,
			target,
			targetResolved,
			deferForwardedTargets,
			dynamicCandidates,
			dynamicResolution,
		) {
			if argumentsMayBeSkipped {
				c.pinExpressionFact(typed, checkTypeNil)
				return true
			}
			return false
		}
		opaqueCallEffects := c.callHasOpaqueClassConstantEffects(typed, target, targetResolved)
		// Arguments evaluate left to right before the call dispatches, so
		// each argument's inferred type is captured at its own evaluation
		// point: a mutating earlier argument (h.delete(:name)) poisons its
		// container's facts for later arguments, while a mutating later
		// argument cannot erase the facts an earlier argument was evaluated
		// under. checkCall consumes the captured facts afterwards.
		argumentFacts := make(map[Expression]*TypeExpr, len(typed.Args)+len(typed.KwArgs))
		argumentClassValues := make(map[Expression][]string, len(typed.Args)+len(typed.KwArgs))
		argumentCallables := make(map[Expression][]*ScriptFunction, len(typed.Args)+len(typed.KwArgs))
		argumentStaticValues := make(map[Expression][]Expression, len(typed.Args)+len(typed.KwArgs))
		captureArgumentFacts := func(expr Expression, expectation expressionExpectation, autoCall bool) {
			argumentFacts[expr] = c.inferExpressionTypeWithExpectation(expr, expectation)
			identitySource := expr
			if !autoCall {
				if callableExpr, bindable := c.bareIdentifierCallableArgument(expr); bindable {
					identitySource = callableExpr
					if call, ok := identitySource.(*CallExpr); ok {
						identitySource = call.Callee
					}
				}
			}
			identityExpr, autoInvoked := c.evaluatedIdentityExpression(identitySource, autoCall)
			classNames, classExact := c.classValueExpressionNames(identityExpr)
			if autoInvoked {
				classNames, classExact = c.dispatchClassValueExpressionNames(identityExpr)
			}
			if classExact {
				argumentClassValues[expr] = classNames
			}
			if fns, ok := c.callableExpressionFunctions(identityExpr); ok {
				argumentCallables[expr] = fns
			}
			staticExpr := expr
			if splat, ok := expr.(*SplatArg); ok {
				staticExpr = splat.Value
			}
			if values, ok := c.staticValueExpressionAlternatives(staticExpr); ok {
				argumentStaticValues[staticExpr] = append([]Expression(nil), values...)
			}
		}
		positionalSplatSeen := false
		argumentEvaluationFailed := false
		for i, arg := range typed.Args {
			expectation := expressionExpectation{}
			_, isSplat := arg.(*SplatArg)
			if invokedLambda != nil && !positionalSplatSeen && !isSplat {
				expectation = blockArgumentExpectation(invokedLambda.Params, i, len(typed.Args))
			} else if targetResolved && !positionalSplatSeen && !isSplat {
				expectation = staticCallablePositionalArgumentExpectation(target, i)
			}
			completed := true
			manuallyWalked := false
			if splat, ok := arg.(*SplatArg); ok {
				if array, ok := splat.Value.(*ArrayLiteral); ok {
					manuallyWalked = true
					for _, elem := range array.Elements {
						completed = c.checkExpressionWithAuto(function, elem, true)
						c.collectRuntimeRequireCallExportsFromExpression(elem)
						if !completed {
							break
						}
						captureArgumentFacts(elem, expressionExpectation{}, true)
					}
				}
			}
			if !manuallyWalked {
				completed = c.checkExpressionWithExpectation(function, arg, expectation)
			}
			// The argument's value and effects materialize once its own
			// evaluation completes, before the next argument runs: a shovel
			// append lands in the facts, and a require binds its exports
			// for the arguments after it, never the ones before.
			if !manuallyWalked {
				c.collectRuntimeRequireCallExportsFromExpression(arg)
			}
			if !completed {
				argumentEvaluationFailed = true
				break
			}
			captureArgumentFacts(arg, expectation, !expectation.includesCallable())
			positionalSplatSeen = positionalSplatSeen || isSplat
			if !c.positionalArgumentExpansionMaySucceed(arg) {
				argumentEvaluationFailed = true
				break
			}
		}
		for _, kwarg := range typed.KwArgs {
			if argumentEvaluationFailed {
				break
			}
			expectation := expressionExpectation{}
			if targetResolved && !kwarg.Splat {
				expectation = staticCallableKeywordArgumentExpectation(typed, target, kwarg.Name)
			}
			completed := true
			manuallyWalked := false
			if kwarg.Splat {
				if hash, ok := kwarg.Value.(*HashLiteral); ok &&
					(hash.ShapeType == nil || c.hashShapeStaticallyShadowed(hash)) {
					manuallyWalked = true
					for _, pair := range hash.Pairs {
						completed = c.checkExpressionWithAuto(function, pair.Key, true)
						c.collectRuntimeRequireCallExportsFromExpression(pair.Key)
						if !completed {
							break
						}
						completed = c.checkExpressionWithAuto(function, pair.Value, true)
						c.collectRuntimeRequireCallExportsFromExpression(pair.Value)
						if !completed {
							break
						}
						captureArgumentFacts(pair.Value, expressionExpectation{}, true)
					}
				}
			}
			if !manuallyWalked {
				completed = c.checkExpressionWithExpectation(function, kwarg.Value, expectation)
				c.collectRuntimeRequireCallExportsFromExpression(kwarg.Value)
			}
			if !completed {
				argumentEvaluationFailed = true
				break
			}
			captureArgumentFacts(kwarg.Value, expectation, !expectation.includesCallable())
			if !c.keywordArgumentExpansionMaySucceed(kwarg) {
				argumentEvaluationFailed = true
				break
			}
		}
		blockArgEvaluated := false
		if !argumentEvaluationFailed && typed.BlockArg != nil {
			blockArgEvaluated = true
			completed := c.checkExpressionWithAuto(function, typed.BlockArg, false)
			if !completed {
				argumentEvaluationFailed = true
			} else {
				captureArgumentFacts(
					typed.BlockArg,
					typeExpressionExpectation(checkTypeFunction),
					false,
				)
				if !c.blockArgumentConversionMaySucceed(typed.BlockArg, argumentFacts[typed.BlockArg]) {
					argumentEvaluationFailed = true
				}
			}
			// A block argument evaluates with the other arguments, before
			// dispatch, so its require effects are live for the call checks.
			c.collectRuntimeRequireCallExportsFromExpression(typed.BlockArg)
		}
		previousFacts := c.callArgumentFacts
		previousClassValues := c.callArgumentClassValues
		previousCallables := c.callArgumentCallables
		previousStaticValues := c.callArgumentStaticValues
		c.callArgumentFacts = argumentFacts
		c.callArgumentClassValues = argumentClassValues
		c.callArgumentCallables = argumentCallables
		c.callArgumentStaticValues = argumentStaticValues
		c.pinForwardedConstructorInstanceFact(typed, dynamicCandidates)
		if deferForwardedTargets {
			dynamicResolution = c.exactDynamicCallTargets(typed, target, targetResolved, dynamicCandidates)
		}
		callMayEnter := !argumentEvaluationFailed
		checkedCall := typed
		if expanded, exact := c.staticallyExpandedCall(typed); exact {
			checkedCall = expanded
		}
		targetMayEnter := callMayEnter
		callMayComplete := callMayEnter
		if targetResolved && target.fn != nil {
			targetMayEnter = targetMayEnter && c.scriptCallBindingPlan(checkedCall, target).bodyMayEnter
			callMayComplete = targetMayEnter &&
				c.scriptFunctionCallMayComplete(checkedCall, target)
		} else if targetResolved {
			view := staticCallViewFor(checkedCall, target)
			targetMayEnter = targetMayEnter && c.builtinCallMayEnter(view, target.spec)
			targetMayEnter = targetMayEnter && c.specialBuiltinCallMayComplete(checkedCall, target.name)
			callMayComplete = targetMayEnter && c.builtinCallMayComplete(target.spec)
		} else if !targetResolved {
			dynamicBodyMayEnter := c.refineDynamicCallTargetEntry(dynamicResolution.targets)
			if dynamicResolution.exact {
				targetMayEnter = targetMayEnter && (dynamicBodyMayEnter ||
					dynamicResolution.nonScriptMayComplete)
				callMayComplete = callMayEnter && (dynamicResolution.nonScriptMayComplete ||
					c.dynamicScriptCallTargetsMayComplete(dynamicResolution.targets))
			} else {
				callMayComplete = targetMayEnter
			}
		}
		if targetResolved && (callMayComplete || argumentsMayBeSkipped) {
			result := c.inferResolvedCallExprType(checkedCall, target)
			if argumentsMayBeSkipped {
				if !callMayComplete {
					result = checkTypeNil
				} else {
					result = unionTypeExprs(result, checkTypeNil)
				}
			}
			c.pinExpressionFact(typed, result)
		} else if !targetResolved && dynamicResolution.exact &&
			(callMayComplete || argumentsMayBeSkipped) {
			if !callMayComplete {
				c.pinExpressionFact(typed, checkTypeNil)
			} else if result, ok := c.inferDynamicCallExprType(dynamicResolution); ok {
				if argumentsMayBeSkipped {
					result = unionTypeExprs(result, checkTypeNil)
				}
				c.pinExpressionFact(typed, result)
			}
		}
		if callMayEnter {
			checkCall := checkedCall
			if targetResolved && target.fn == nil && callExpandsArguments(typed) {
				// Static expansion is precise enough to decide whether dispatch can
				// run, but builtin argument diagnostics remain gradual for splat
				// spellings because the runtime owns their normalized call shape.
				checkCall = typed
			}
			c.checkCallResolved(
				function,
				checkCall,
				target,
				targetResolved,
				dynamicResolution.targets,
				dynamicResolution.diagnoseTargets,
			)
		}
		if targetMayEnter && c.returnCollector != nil && !targetResolved && c.callMayDispatchDynamicValue(typed) {
			c.returnCollector.record(nil)
		}
		if callMayEnter && targetResolved && target.fn != nil {
			c.applyScriptFunctionNamespaceMutations(typed, target)
			c.checkScriptCallInvokedLambdaSummaryYields(function, typed, target)
		}
		if callMayEnter {
			c.applyDynamicCallNamespaceMutations(typed, dynamicResolution.targets)
			for _, candidate := range dynamicResolution.targets {
				if candidate.bindingStarts {
					c.checkScriptCallInvokedLambdaSummaryYields(
						function,
						candidate.call,
						candidate.target,
					)
				}
			}
		}
		if targetMayEnter && opaqueCallEffects {
			c.markOpaqueClassConstants()
		}
		callBlockMayRun := targetMayEnter && invokedLambda == nil && c.callMayEvaluateBlock(typed)
		if callMayEnter && targetResolved && target.fn != nil &&
			(typed.Block != nil || typed.BlockArg != nil) {
			callBlockMayRun = c.scriptFunctionCallBlockMayRun(typed, target)
		}
		if callMayEnter && !targetResolved && (typed.Block != nil || typed.BlockArg != nil) {
			for _, candidate := range dynamicResolution.targets {
				if candidate.bindingStarts &&
					c.functionMayEvaluateCallBlock(candidate.call, candidate.target, nil) {
					callBlockMayRun = true
					break
				}
			}
		}
		immediateLambdaEnters := targetMayEnter && c.immediateLambdaCallMayEnter(invokedLambda, typed)
		if immediateLambdaEnters {
			c.applyLambdaBlockNamespaceMutations(invokedLambda)
			c.checkInvokedLambdaSummaryYields(function, invokedLambda)
		}
		// Exact script targets carry callable arguments through their parameter
		// facts, so their body scan applies a lambda only at an actual `.call`.
		// Opaque dynamic and builtin targets may invoke any escaping lambda.
		escapingLambdaMayRun := targetMayEnter &&
			(targetResolved && target.fn == nil || !targetResolved && !dynamicResolution.exact)
		if escapingLambdaMayRun && (invokedLambda == nil || immediateLambdaEnters) {
			for _, arg := range typed.Args {
				c.applyLambdaLiteralNamespaceMutations(arg)
				c.checkLambdaLiteralSummaryYields(function, arg)
			}
			for _, kwarg := range typed.KwArgs {
				c.applyLambdaLiteralNamespaceMutations(kwarg.Value)
				c.checkLambdaLiteralSummaryYields(function, kwarg.Value)
			}
			c.applyLambdaLiteralNamespaceMutations(typed.BlockArg)
			c.checkLambdaLiteralSummaryYields(function, typed.BlockArg)
		} else if callBlockMayRun {
			c.applyLambdaLiteralNamespaceMutations(typed.BlockArg)
			c.checkLambdaLiteralSummaryYields(function, typed.BlockArg)
		}
		if callBlockMayRun {
			c.applyCallableNamespaceMutations(argumentCallables[typed.BlockArg])
		}
		if callBlockMayRun && typed.Block != nil {
			c.checkLiteralArrayBlockParamTypes(function, typed)
			// The lambda builtin converts its literal block to local return
			// semantics, so those returns cannot unwind the enclosing
			// function.
			localReturns := typed.Block.Lambda || c.callTargetsCoreLambda(typed, target, targetResolved)
			c.checkBlockLiteral(function, typed.Block, localReturns)
		}
		c.callArgumentFacts = previousFacts
		c.callArgumentClassValues = previousClassValues
		c.callArgumentCallables = previousCallables
		c.callArgumentStaticValues = previousStaticValues
		if argumentsMayBeSkipped {
			if !callMayComplete {
				// The non-nil arm cannot reach the expression result, so only
				// the nil short-circuit contributes state after the call.
				c.restoreRuntimeState(argumentState)
				c.restoreScopeState(argumentScopeState)
			} else {
				c.restoreRuntimeStatePreservingClassConstantEffects(argumentState)
				// A nil receiver skips the arguments entirely, so type facts the
				// argument walk established (a shovel append, for example) hold
				// on only one of the two paths and must merge as a branch join.
				evaluatedScopeState := c.snapshotScopeState()
				c.mergeScopeStates(argumentScopeState, []checkScopeState{argumentScopeState, evaluatedScopeState})
			}
		}
		// Containers pass by reference, so a callee may mutate an argument
		// in place; the caller's structural facts stop holding. Dispatch
		// happens after the arguments evaluate, so the receiver's facts
		// stop holding here too, not during the callee walk. A dispatch
		// proven pure by its registered member contract preserves the
		// receiver's facts for outer inference and condition-outcome
		// narrowing; known mutators and unknown dispatch keep poisoning.
		if targetMayEnter {
			c.applyKeywordSplatDeleteFact(typed)
			if member, ok := typed.Callee.(*MemberExpr); ok && !c.memberCallPreservesReceiverFacts(typed) {
				c.poisonEscapedIdentifier(member.Object)
			}
			if blockArgEvaluated {
				if member, ok := typed.BlockArg.(*MemberExpr); ok {
					c.poisonEscapedIdentifier(member.Object)
				}
			}
			for arg := range argumentFacts {
				c.poisonEscapedIdentifier(arg)
			}
			for _, kwarg := range typed.KwArgs {
				if _, evaluated := argumentFacts[kwarg.Value]; evaluated {
					c.poisonEscapedIdentifier(kwarg.Value)
				}
			}
		}
		if (!callMayEnter || !callMayComplete) && !argumentsMayBeSkipped {
			return false
		}
	case *MemberExpr:
		var invokedLambda *BlockLiteral
		if autoCall && typed.Property == "call" {
			invokedLambda = c.resolveImmediateLambdaBlock(typed.Object)
		}
		objectAutoCall := true
		if typed.Property == "call" && typeExprMayIncludeCallable(c.inferExpressionType(typed.Object)) {
			objectAutoCall = false
		}
		if !c.checkExpressionWithAuto(function, typed.Object, objectAutoCall) {
			return false
		}
		if autoCall {
			if typed.Property == "new" {
				classes, exact := c.constructorInstanceClassNames(typed.Object, "")
				c.pinConstructorInstanceFact(typed, classes, exact)
				if exact && len(classes) == 0 {
					// Parenless `.new` also resolves before any outer dispatch.
					// Plain modules fail here and cannot contribute opaque effects.
					return false
				}
			}
			if lambdaLiteralArity(invokedLambda) == 0 {
				c.applyLambdaBlockNamespaceMutations(invokedLambda)
				c.checkInvokedLambdaSummaryYields(function, invokedLambda)
			}
			target, resolved, invoked, completed := c.checkMemberAutoCall(function, typed)
			if !completed {
				if typed.Safe && !c.safeNavigationReceiverKnownNonNil(typed.Object) {
					c.pinExpressionFact(typed, checkTypeNil)
					return true
				}
				return false
			}
			if invoked {
				if target.fn != nil && !c.scriptFunctionClassConstantEffectsProvenAbsent(target.fn) {
					c.markOpaqueClassConstants()
				} else if target.fn == nil && target.spec.fromSignature {
					c.markOpaqueClassConstants()
				}
			} else if !resolved {
				// An unresolved member value may still auto-invoke a callable at
				// runtime, so its class-constant effects stay opaque.
				c.markOpaqueClassConstants()
			}
			// Member dispatch on a container may mutate it in place (push,
			// delete, ...), so the receiver's structural facts stop
			// holding. A call callee poisons after its arguments instead:
			// they evaluate before dispatch and still see the facts. A
			// dispatch proven pure by its registered member contract
			// preserves the receiver fact that outer inference or
			// narrowing consumes next.
			if !c.memberDispatchPreservesReceiverFacts(typed) {
				c.poisonEscapedIdentifier(typed.Object)
			}
		}
	case *ScopeExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
	case *IndexExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
		dispatchType := c.inferExpressionType(typed.Object)
		opaqueDispatch := c.indexReadHasOpaqueClassConstantEffects(typed.Object)
		for _, index := range typed.Indices {
			if !c.checkExpressionWithAuto(function, index, true) {
				return false
			}
		}
		c.enqueueReachableInstanceDispatch(dispatchType, "[]")
		if opaqueDispatch {
			c.markOpaqueClassConstants()
		}
		return c.indexExpressionMayComplete(typed)
	case *DestructureTarget:
		for _, element := range typed.Elements {
			if !c.checkExpressionWithAuto(function, element.Target, true) {
				return false
			}
		}
	case *SplatArg:
		return c.checkExpressionWithAuto(function, typed.Value, true)
	case *UnaryExpr:
		if !c.checkExpressionWithAuto(function, typed.Right, true) {
			return false
		}
		c.checkUnaryOperandTypes(function, typed)
		return c.unaryExpressionMayComplete(typed)
	case *BinaryExpr:
		if !c.checkExpressionWithAuto(function, typed.Left, true) {
			return false
		}
		dispatchType := c.inferExpressionType(typed.Left)
		opaqueDispatch := c.binaryDispatchHasOpaqueClassConstantEffects(dispatchType, typed.Operator)
		if binaryRightMayEvaluate(typed) && !c.binaryRightUnreachable(typed) {
			state := c.snapshotRuntimeState()
			scopeState := c.snapshotScopeState()
			c.collectRuntimeRequireCallExportsFromExpression(typed.Left)
			// Whether the right side provably always runs is a property of
			// the left's value before the right evaluates, so it is decided
			// on the pre-narrowing facts.
			rightAlwaysRuns := binaryRightAlwaysEvaluates(typed) || c.binaryRightAlwaysEvaluatesInferred(typed)
			// The right operand only runs when the left picked its
			// short-circuit outcome, so the left's implied narrowing holds
			// while the right evaluates (`x && x.length`). The surrounding
			// snapshot/merge scopes the narrowed facts to this region.
			rightReachable := true
			switch typed.Operator {
			case tokenAnd:
				rightReachable = c.collectRuntimeConditionOutcomeEffects(typed.Left, true)
			case tokenOr:
				rightReachable = c.collectRuntimeConditionOutcomeEffects(typed.Left, false)
			}
			rightCompleted := true
			if rightReachable {
				rightCompleted = c.checkExpressionWithAuto(function, typed.Right, true)
			}
			if !rightReachable {
				c.restoreRuntimeState(state)
				c.restoreScopeState(scopeState)
			} else if !rightCompleted {
				if rightAlwaysRuns {
					return false
				}
				c.restoreRuntimeState(state)
				c.restoreScopeState(scopeState)
			} else if !rightAlwaysRuns {
				// A short-circuited right operand may not run, so its
				// runtime effects roll back and its type facts (a shovel
				// append, for example) merge as a branch join instead of
				// surviving unconditionally. A right side that provably
				// always runs keeps both — including exports from a
				// guaranteed require.
				c.restoreRuntimeStatePreservingClassConstantEffects(state)
				evaluatedScopeState := c.snapshotScopeState()
				c.mergeScopeStates(scopeState, []checkScopeState{scopeState, evaluatedScopeState})
			}
		}
		c.checkBinaryOperandTypes(function, typed)
		c.applyShovelMutationFacts(typed)
		c.enqueueReachableInstanceDispatch(
			dispatchType,
			binaryDispatchMethodNames(typed.Operator)...,
		)
		if opaqueDispatch {
			c.markOpaqueClassConstants()
		}
		if !c.binaryExpressionMayComplete(typed) {
			return false
		}
	case *ConditionalExpr:
		return c.checkConditionalExpression(function, typed, autoCallExpectation(autoCall))
	case *RescueExpr:
		return c.checkRescueExpression(function, typed, autoCall)
	case *IfExpr:
		return c.checkIfExpression(function, typed, autoCallExpectation(autoCall))
	case *RangeExpr:
		if !c.checkExpressionWithAuto(function, typed.Start, true) ||
			!c.checkExpressionWithAuto(function, typed.End, true) {
			return false
		}
	case *CaseExpr:
		return c.checkCaseExpression(function, typed, autoCallExpectation(autoCall))
	case *BlockLiteral:
		// A standalone block literal is a stabby lambda; its body checks like a
		// call block's. Plain call blocks are checked from the CallExpr case.
		if typed.Lambda {
			c.checkBlockLiteral(function, typed, true)
		}
		return true
	case *YieldExpr:
		for _, arg := range typed.Args {
			if !c.checkExpressionWithAuto(function, arg, true) {
				return false
			}
			c.poisonEscapedIdentifier(arg)
		}
		if c.returnCollector != nil && !c.summaryBlockAvailable {
			return false
		}
		c.markOpaqueClassConstants()
		// The caller-supplied block may return non-locally instead of
		// letting the summarized function produce its later result.
		if c.summaryYieldsActive {
			c.summaryYieldCollector.record(nil)
		}
	case *InterpolatedString:
		return c.checkStringParts(function, typed.Parts)
	case *InterpolatedSymbol:
		return c.checkStringParts(function, typed.Parts)
	}
	return true
}

// callHasOpaqueClassConstantEffects reports calls whose implementation is not
// proven unable to install a class constant. The verdict is captured when the
// callee evaluates because later arguments can change the receiver's facts.
func (c *scriptChecker) callHasOpaqueClassConstantEffects(call *CallExpr, target staticCallable, resolved bool) bool {
	if call == nil {
		return false
	}
	blockMayRun := call.BlockArg != nil || c.resolvedCallMayEvaluateBlock(call, target, resolved)
	if !blockMayRun {
		if member, ok := call.Callee.(*MemberExpr); ok && c.memberDispatchEffect(member) == effectPure {
			return false
		}
		if resolved && target.fn != nil && c.scriptCallClassConstantEffectsProvenAbsent(call, target) {
			return false
		}
		if resolved && target.fn == nil && !target.spec.fromSignature {
			return false
		}
		if !resolved {
			if ident, ok := call.Callee.(*Identifier); ok &&
				!c.identifierShadowed(ident.Name) &&
				!c.hostGlobalShadows(ident.Name) &&
				!c.typeRootHasBinding(ident.Name) &&
				!c.hostBuiltinOverrides(ident.Name) {
				fn := c.implicitSelfFunction(ident.Name)
				if c.scriptCallClassConstantEffectsProvenAbsent(call, staticCallable{
					name:       ident.Name,
					fn:         fn,
					resolution: calleeMemberMethod,
				}) {
					return false
				}
			}
		}
	}
	return true
}

func (c *scriptChecker) callableExpressionFunctions(expr Expression) ([]*ScriptFunction, bool) {
	c.speculativeInference++
	defer func() { c.speculativeInference-- }()
	return c.callableExpressionFunctionsSeen(expr, nil)
}

func (c *scriptChecker) callableExpressionFunctionsAfterEvaluation(
	expr Expression,
	autoCall bool,
) ([]*ScriptFunction, bool) {
	identityExpr, _ := c.evaluatedIdentityExpression(expr, autoCall)
	return c.callableExpressionFunctions(identityExpr)
}

func (c *scriptChecker) evaluatedIdentityExpression(expr Expression, autoCall bool) (Expression, bool) {
	if !autoCall {
		return expr, false
	}
	switch expr.(type) {
	case *Identifier, *MemberExpr:
	default:
		return expr, false
	}
	fns, exact := c.callableExpressionFunctions(expr)
	if !exact || len(fns) == 0 {
		return expr, false
	}
	for _, fn := range fns {
		if len(fn.Params) != 0 {
			return expr, false
		}
	}
	// Ordinary value evaluation auto-invokes an exact zero-parameter
	// function. Reusing a synthetic call lets the existing single-return
	// traversal recover the class or callable identity of its result.
	return &CallExpr{Callee: expr, Position: expr.Pos()}, true
}

func (c *scriptChecker) callableExpressionFunctionsSeen(
	expr Expression,
	seen map[*ScriptFunction]struct{},
) ([]*ScriptFunction, bool) {
	switch typed := expr.(type) {
	case *Identifier:
		if fns, ok := c.localCallableValuesFor(typed.Name); ok {
			return fns, true
		}
		if c.identifierShadowed(typed.Name) || c.hostGlobalShadows(typed.Name) {
			return nil, false
		}
		if fn, ok := c.script.functions[typed.Name]; ok {
			return []*ScriptFunction{fn}, true
		}
		if fn, ok := c.typeRootFunction(typed.Name); ok {
			return []*ScriptFunction{fn}, true
		}
		if c.typeRootHasBinding(typed.Name) || c.hostBuiltinOverrides(typed.Name) {
			return nil, false
		}
		if fn := c.implicitSelfFunction(typed.Name); fn != nil {
			return []*ScriptFunction{fn}, true
		}
		return nil, false
	case *MemberExpr:
		target, ok := c.resolveMemberCallable(typed)
		if !ok || target.fn == nil || target.constructor {
			return nil, false
		}
		return []*ScriptFunction{target.fn}, true
	case *ConditionalExpr:
		if branch, ok := staticConditionalExpressionBranch(typed); ok {
			return c.callableExpressionFunctionsSeen(branch, seen)
		}
		left, leftOK := c.callableExpressionFunctionsSeen(typed.Consequent, seen)
		right, rightOK := c.callableExpressionFunctionsSeen(typed.Alternate, seen)
		return mergeCheckFunctionCandidates(left, leftOK, right, rightOK)
	case *IfExpr:
		if branch, known := c.inferredIfExpressionBranch(typed); known {
			return c.callableExpressionFunctionsSeen(branch, seen)
		}
		branches := make([]Expression, 0, len(typed.ElseIf)+2)
		branches = append(branches, typed.Consequent)
		for _, branch := range typed.ElseIf {
			branches = append(branches, branch.Result)
		}
		branches = append(branches, typed.Alternate)
		var merged []*ScriptFunction
		for _, branch := range branches {
			candidates, ok := c.callableExpressionFunctionsSeen(branch, seen)
			if !ok {
				return nil, false
			}
			if merged == nil {
				merged = candidates
				continue
			}
			merged, _ = mergeCheckFunctionCandidates(merged, true, candidates, true)
		}
		return merged, len(merged) > 0
	case *RescueExpr:
		body, bodyOK := c.callableExpressionFunctionsSeen(typed.Body, seen)
		fallback, fallbackOK := c.callableExpressionFunctionsSeen(typed.Fallback, seen)
		return mergeCheckFunctionCandidates(body, bodyOK, fallback, fallbackOK)
	case *BinaryExpr:
		if typed.Operator != tokenAnd && typed.Operator != tokenOr {
			return nil, false
		}
		if truthy, known := staticExpressionTruthiness(typed.Left); known {
			if truthy == (typed.Operator == tokenAnd) {
				return c.callableExpressionFunctionsSeen(typed.Right, seen)
			}
			return c.callableExpressionFunctionsSeen(typed.Left, seen)
		}
		if left, ok := c.callableExpressionFunctionsSeen(typed.Left, seen); ok {
			if typed.Operator == tokenOr {
				return left, true
			}
			return c.callableExpressionFunctionsSeen(typed.Right, seen)
		}
		return nil, false
	case *IndexExpr:
		projected, ok := c.staticLiteralProjections(typed)
		if !ok {
			return nil, false
		}
		var merged []*ScriptFunction
		for _, candidate := range projected {
			functions, exact := c.callableExpressionFunctionsSeen(candidate, seen)
			if !exact {
				return nil, false
			}
			if merged == nil {
				merged = functions
				continue
			}
			merged, _ = mergeCheckFunctionCandidates(merged, true, functions, true)
		}
		return merged, len(merged) > 0
	case *CallExpr:
		if member, ok := typed.Callee.(*MemberExpr); ok && member.Property == "itself" &&
			len(typed.Args) == 0 && len(typed.KwArgs) == 0 && typed.Block == nil && typed.BlockArg == nil {
			return c.callableExpressionFunctionsSeen(member.Object, seen)
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
			return c.callableExpressionFunctionsSeen(stmt.Expr, seen)
		case *ReturnStmt:
			return c.callableExpressionFunctionsSeen(stmt.Value, seen)
		}
	}
	return nil, false
}

func mergeCheckFunctionCandidates(
	left []*ScriptFunction,
	leftOK bool,
	right []*ScriptFunction,
	rightOK bool,
) ([]*ScriptFunction, bool) {
	if !leftOK || !rightOK || len(left) == 0 || len(right) == 0 {
		return nil, false
	}
	merged := append([]*ScriptFunction(nil), left...)
	seen := make(map[*ScriptFunction]struct{}, len(left)+len(right))
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
	return normalizeCheckCallables(merged), true
}

func (c *scriptChecker) implicitSelfFunction(name string) *ScriptFunction {
	if c.selfClass == nil {
		return nil
	}
	if c.selfClassContext {
		return c.selfClass.ClassMethods[name]
	}
	return c.selfClass.Methods[name]
}

func (c *scriptChecker) implicitSelfSummaryCallable(name string) (staticCallable, bool) {
	if c.selfClass == nil ||
		(!c.selfClassContext && name == "class") {
		return staticCallable{}, false
	}
	if c.selfClassContext && name == "new" && !c.selfClass.IsModule {
		return dynamicConstructorTarget(c.selfClass, calleeMemberValue), true
	}
	fn := c.implicitSelfFunction(name)
	if fn == nil {
		return staticCallable{}, false
	}
	separator := "#"
	if c.selfClassContext {
		separator = "."
	}
	return staticCallable{
		name:       c.selfClass.Name + separator + name,
		fn:         fn,
		resolution: calleeMemberMethod,
	}, true
}

// implicitSelfAutoCall resolves the synthetic zero-argument call performed by
// runtime value evaluation of a bare method identifier.
func (c *scriptChecker) implicitSelfAutoCall(ident *Identifier) (*CallExpr, staticCallable, bool) {
	if ident == nil {
		return nil, staticCallable{}, false
	}
	target, ok := c.implicitSelfSummaryCallable(ident.Name)
	if !ok {
		return nil, staticCallable{}, false
	}
	return &CallExpr{Callee: ident, Position: ident.Pos()}, target, true
}

// scriptFunctionClassConstantEffectsProvenAbsent recognizes the bounded set
// of script functions whose reachable expressions cannot write class state.
// Unknown dispatch, callable bindings, control flow, and mutation all remain
// opaque; exact owned calls recurse and resolved runtime builtins are safe.
func (c *scriptChecker) scriptFunctionClassConstantEffectsProvenAbsent(fn *ScriptFunction) bool {
	return c.scriptCallClassConstantEffectsProvenAbsent(nil, staticCallable{fn: fn})
}

func (c *scriptChecker) scriptCallClassConstantEffectsProvenAbsent(
	call *CallExpr,
	target staticCallable,
) bool {
	return c.scriptCallClassConstantEffectsProvenAbsentSeen(
		call,
		target,
		make(map[*ScriptFunction]struct{}),
	)
}

func (c *scriptChecker) scriptCallClassConstantEffectsProvenAbsentSeen(
	call *CallExpr,
	target staticCallable,
	seen map[*ScriptFunction]struct{},
) bool {
	fn := target.fn
	if fn == nil || fn.owner != c.script {
		return false
	}
	if _, recursive := seen[fn]; recursive {
		return false
	}
	seen[fn] = struct{}{}
	defer delete(seen, fn)

	bindings := scriptFunctionBindings(fn)
	restoreResolution := c.withClassConstantProofResolution(fn, bindings)
	defer restoreResolution()

	collapseOptionsHash := call != nil && staticCallCollapsesOptionsHash(call, target)
	for i, param := range fn.Params {
		if param.DefaultVal == nil {
			continue
		}
		if call != nil && !callMayEvaluateParamDefault(call, fn, i, collapseOptionsHash) {
			continue
		}
		if !c.scriptExpressionClassConstantEffectsProvenAbsent(param.DefaultVal, bindings, seen) {
			return false
		}
	}
	for _, stmt := range fn.Body {
		var expr Expression
		switch typed := stmt.(type) {
		case *ExprStmt:
			expr = typed.Expr
		case *ReturnStmt:
			expr = typed.Value
		default:
			return false
		}
		if !c.scriptExpressionClassConstantEffectsProvenAbsent(expr, bindings, seen) {
			return false
		}
	}
	return true
}

func scriptFunctionBindings(fn *ScriptFunction) map[string]*TypeExpr {
	if fn == nil {
		return nil
	}
	bindings := make(map[string]*TypeExpr, len(fn.Params))
	for _, param := range fn.Params {
		if param.Name != "" {
			bindings[param.Name] = param.Type
		}
		bound := make(map[string]struct{})
		collectBindingTarget(param.Target, bound)
		for name := range bound {
			bindings[name] = nil
		}
	}
	locals := make(map[string]struct{})
	collectLocalBindings(fn.Body, locals)
	for name := range locals {
		if _, param := bindings[name]; !param {
			bindings[name] = nil
		}
	}
	return bindings
}

// withClassConstantProofResolution isolates a callee proof from the caller's
// local facts and resolves bare method calls under the callee's actual self.
func (c *scriptChecker) withClassConstantProofResolution(
	fn *ScriptFunction,
	bindings map[string]*TypeExpr,
) func() {
	previousScopes := c.scopes
	previousTypes := c.localTypes
	previousClassValues := c.localClassValues
	previousLive := c.liveLocalNames
	previousUnions := c.localNameUnions
	previousSelfScope := c.selfScope
	previousSelfClass := c.selfClass
	previousSelfClassContext := c.selfClassContext

	scope := make(map[string]struct{}, len(bindings))
	for name := range bindings {
		scope[name] = struct{}{}
	}
	c.scopes = []map[string]struct{}{scope}
	c.localTypes = []checkTypeFrame{nil}
	c.localClassValues = []checkClassValueFrame{nil}
	c.liveLocalNames = []map[string]struct{}{nil}
	c.localNameUnions = nil
	c.selfScope = false
	c.selfClass = nil
	c.selfClassContext = false
	if c.functionHasSelfScope(fn) {
		c.selfScope = true
		c.selfClass = c.selfScopeFnClasses[fn]
		_, c.selfClassContext = c.selfScopeClassFns[fn]
	}

	return func() {
		c.scopes = previousScopes
		c.localTypes = previousTypes
		c.localClassValues = previousClassValues
		c.liveLocalNames = previousLive
		c.localNameUnions = previousUnions
		c.selfScope = previousSelfScope
		c.selfClass = previousSelfClass
		c.selfClassContext = previousSelfClassContext
	}
}

func (c *scriptChecker) scriptExpressionClassConstantEffectsProvenAbsent(
	expr Expression,
	bindings map[string]*TypeExpr,
	seen map[*ScriptFunction]struct{},
) bool {
	if expr == nil {
		return true
	}
	if _, literal := staticLiteralValue(expr); literal {
		return true
	}
	switch typed := expr.(type) {
	case *Identifier:
		ty, bound := bindings[typed.Name]
		return bound && ty != nil && !typeExprMayIncludeCallable(ty)
	case *ArrayLiteral:
		for _, element := range typed.Elements {
			if !c.scriptExpressionClassConstantEffectsProvenAbsent(element, bindings, seen) {
				return false
			}
		}
		return true
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			if !c.scriptExpressionClassConstantEffectsProvenAbsent(pair.Key, bindings, seen) ||
				!c.scriptExpressionClassConstantEffectsProvenAbsent(pair.Value, bindings, seen) {
				return false
			}
		}
		return true
	case *TypeLiteral:
		return typed.Fallback == nil ||
			c.scriptExpressionClassConstantEffectsProvenAbsent(typed.Fallback, bindings, seen)
	case *SplatArg:
		return c.scriptExpressionClassConstantEffectsProvenAbsent(typed.Value, bindings, seen)
	case *CallExpr:
		if typed.Block != nil || typed.BlockArg != nil {
			return false
		}
		switch callee := typed.Callee.(type) {
		case *Identifier:
			if _, bound := bindings[callee.Name]; bound {
				return false
			}
		case *MemberExpr:
			ident, plain := callee.Object.(*Identifier)
			if !plain {
				return false
			}
			if _, bound := bindings[ident.Name]; bound {
				return false
			}
		default:
			return false
		}
		for _, arg := range typed.Args {
			if !c.scriptExpressionClassConstantEffectsProvenAbsent(arg, bindings, seen) {
				return false
			}
		}
		for _, kwarg := range typed.KwArgs {
			if !c.scriptExpressionClassConstantEffectsProvenAbsent(kwarg.Value, bindings, seen) {
				return false
			}
		}
		target, resolved := c.resolveCallable(typed)
		if !resolved {
			return false
		}
		if target.fn != nil {
			return c.scriptCallClassConstantEffectsProvenAbsentSeen(typed, target, seen)
		}
		return !target.spec.fromSignature && target.name != "send" && target.name != "public_send"
	}
	return false
}

func (c *scriptChecker) binaryDispatchHasOpaqueClassConstantEffects(ty *TypeExpr, operator TokenType) bool {
	return c.instanceDispatchHasOpaqueClassConstantEffects(ty, binaryDispatchMethodNames(operator)...)
}

func binaryDispatchMethodNames(operator TokenType) []string {
	switch operator {
	case tokenPlus, tokenMinus, tokenAsterisk, tokenSlash, tokenPercent, tokenPower,
		tokenShovel, tokenAmpersand, tokenLT, tokenLTE, tokenGT, tokenGTE, tokenSpaceship,
		tokenEQ:
		return []string{string(operator)}
	case tokenNotEQ:
		return []string{"!=", "=="}
	default:
		return nil
	}
}

func (c *scriptChecker) binaryExpressionMayComplete(expr *BinaryExpr) bool {
	if expr == nil || expr.Operator == tokenAnd || expr.Operator == tokenOr {
		return true
	}
	if c.binaryOperationOutcome(
		expr.Operator,
		c.inferExpressionType(expr.Left),
		c.inferExpressionType(expr.Right),
	).invalid {
		return false
	}
	methods := binaryDispatchMethodNames(expr.Operator)
	if len(methods) == 0 {
		return true
	}
	for _, method := range methods {
		call := &CallExpr{
			Callee: &MemberExpr{
				Object:   expr.Left,
				Property: method,
				Position: expr.Pos(),
			},
			Args:     []Expression{expr.Right},
			Position: expr.Pos(),
		}
		if c.expressionMayCompleteForBinding(call) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) indexExpressionMayComplete(expr *IndexExpr) bool {
	if expr == nil {
		return true
	}
	call := &CallExpr{
		Callee: &MemberExpr{
			Object:   expr.Object,
			Property: "[]",
			Position: expr.Pos(),
		},
		Args:     append([]Expression(nil), expr.Indices...),
		Position: expr.Pos(),
	}
	target, resolved := c.resolveCallable(call)
	if resolved {
		if target.fn != nil {
			plan := c.scriptCallBindingPlan(call, target)
			return plan.bodyMayEnter &&
				c.scriptFunctionCallMayComplete(call, target)
		}
		view := staticCallViewFor(call, target)
		return c.builtinCallMayEnter(view, target.spec) &&
			c.specialBuiltinCallMayComplete(call, target.name) &&
			c.builtinCallMayComplete(target.spec)
	}
	candidates := c.captureDynamicCallCandidates(call)
	resolution := c.exactDynamicCallTargets(call, target, false, candidates)
	if resolution.lookupFails {
		return false
	}
	if !resolution.exact {
		return true
	}
	c.refineDynamicCallTargetEntry(resolution.targets)
	return resolution.nonScriptMayComplete ||
		c.dynamicScriptCallTargetsMayComplete(resolution.targets)
}

func (c *scriptChecker) instanceDispatchHasOpaqueClassConstantEffects(ty *TypeExpr, methods ...string) bool {
	arms, ok := typeExprArms(ty, 0)
	if !ok || len(arms) == 0 {
		return true
	}
	resolve := c.checkNamedTypeResolver()
	for _, arm := range arms {
		if arm.Kind != TypeEnum {
			continue
		}
		match, ok := resolve(arm)
		if !ok {
			return true
		}
		if match.enum != nil {
			continue
		}
		if match.class == nil || match.class.IsModule {
			return true
		}
		for _, method := range methods {
			fn, exists := match.class.Methods[method]
			if !exists {
				continue
			}
			if !c.scriptFunctionClassConstantEffectsProvenAbsent(fn) {
				return true
			}
			break
		}
	}
	return false
}

func (c *scriptChecker) enqueueReachableInstanceDispatch(ty *TypeExpr, methods ...string) {
	arms, ok := typeExprArms(ty, 0)
	if !ok {
		return
	}
	resolve := c.checkNamedTypeResolver()
	for _, arm := range arms {
		if arm.Kind != TypeEnum {
			continue
		}
		match, ok := resolve(arm)
		if !ok || match.class == nil || match.class.IsModule || match.enum != nil {
			continue
		}
		for _, method := range methods {
			fn, exists := match.class.Methods[method]
			if !exists {
				continue
			}
			c.enqueueReachableFunction(match.class.Name+"#"+method, fn)
			break
		}
	}
}

func (c *scriptChecker) indexReadHasOpaqueClassConstantEffects(receiver Expression) bool {
	if _, literal := receiver.(*HashLiteral); literal {
		return false
	}
	ty := c.inferExpressionType(receiver)
	arms, ok := typeExprArms(ty, 0)
	if !ok || len(arms) == 0 {
		return true
	}
	for _, arm := range arms {
		if arm.Kind == TypeHash || arm.Kind == TypeShape {
			return true
		}
	}
	return c.instanceDispatchHasOpaqueClassConstantEffects(ty, "[]")
}

func (c *scriptChecker) memberSetterHasOpaqueClassConstantEffects(member *MemberExpr) bool {
	if member == nil {
		return false
	}
	setter := member.Property + "="
	if ident, ok := member.Object.(*Identifier); ok {
		if ident.Name == "self" && c.selfClass != nil {
			methods := c.selfClass.Methods
			if c.selfClassContext {
				methods = c.selfClass.ClassMethods
			}
			fn, exists := methods[setter]
			return exists && !c.scriptFunctionClassConstantEffectsProvenAbsent(fn)
		}
		if classDef, ok := c.staticClassArgument(ident); ok {
			fn, exists := classDef.ClassMethods[setter]
			return exists && !c.scriptFunctionClassConstantEffectsProvenAbsent(fn)
		}
	}
	return c.instanceDispatchHasOpaqueClassConstantEffects(c.inferExpressionType(member.Object), setter)
}

func (c *scriptChecker) enqueueReachableMemberSetter(member *MemberExpr) {
	if member == nil {
		return
	}
	setter := member.Property + "="
	if ident, ok := member.Object.(*Identifier); ok {
		if ident.Name == "self" && c.selfClass != nil {
			methods := c.selfClass.Methods
			owner := c.selfClass.Name + "#"
			if c.selfClassContext {
				methods = c.selfClass.ClassMethods
				owner = c.selfClass.Name + "."
			}
			if fn, exists := methods[setter]; exists {
				c.enqueueReachableFunction(owner+setter, fn)
			}
			return
		}
		if classDef, ok := c.staticClassArgument(ident); ok {
			if fn, exists := classDef.ClassMethods[setter]; exists {
				c.enqueueReachableFunction(classDef.Name+"."+setter, fn)
			}
			return
		}
	}
	c.enqueueReachableInstanceDispatch(c.inferExpressionType(member.Object), setter)
}

func (c *scriptChecker) checkPlainAssignmentTarget(
	function string,
	target Expression,
	value Expression,
) bool {
	switch typed := target.(type) {
	case nil, *Identifier, *IvarExpr, *ClassVarExpr:
		return true
	case *MemberExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Object)
		c.enqueueReachableMemberSetter(typed)
		if c.memberSetterHasOpaqueClassConstantEffects(typed) {
			c.markOpaqueClassConstants()
		}
		c.recordRuntimeBindingTarget(typed)
		return c.assignmentSetterMayComplete(typed, value)
	case *IndexExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Object)
		dispatchType := c.inferExpressionType(typed.Object)
		opaqueDispatch := c.instanceDispatchHasOpaqueClassConstantEffects(
			dispatchType,
			"[]=",
		)
		for _, index := range typed.Indices {
			if !c.checkExpressionWithAuto(function, index, true) {
				return false
			}
			c.collectRuntimeRequireCallExportsFromExpression(index)
		}
		c.enqueueReachableInstanceDispatch(dispatchType, "[]=")
		if opaqueDispatch {
			c.markOpaqueClassConstants()
		}
		return c.assignmentSetterMayComplete(typed, value)
	case *DestructureTarget:
		values := destructureAssignmentExpressions(typed, value)
		for i, element := range typed.Elements {
			var elementValue Expression
			if i < len(values) {
				elementValue = values[i]
			}
			if !c.checkPlainAssignmentTarget(function, element.Target, elementValue) {
				return false
			}
		}
	default:
		if !c.checkExpression(function, target) {
			return false
		}
		c.collectRuntimeRequireCallExportsFromExpression(target)
	}
	return true
}

func destructureAssignmentExpressions(target *DestructureTarget, value Expression) []Expression {
	if target == nil {
		return nil
	}
	var source []Expression
	if array, ok := value.(*ArrayLiteral); ok {
		for _, element := range array.Elements {
			if _, splat := element.(*SplatArg); splat {
				return make([]Expression, len(target.Elements))
			}
		}
		source = array.Elements
	} else if value != nil {
		source = []Expression{value}
	}
	missing := func() Expression {
		return &NilLiteral{Position: target.Pos()}
	}
	valueAt := func(index int) Expression {
		if index < 0 || index >= len(source) {
			return missing()
		}
		return source[index]
	}

	result := make([]Expression, len(target.Elements))
	restIndex := -1
	for i, element := range target.Elements {
		if element.Rest {
			restIndex = i
			break
		}
	}
	if restIndex < 0 {
		for i := range target.Elements {
			result[i] = valueAt(i)
		}
		return result
	}

	trailing := len(target.Elements) - restIndex - 1
	restStart := min(restIndex, len(source))
	restEnd := max(restStart, len(source)-trailing)
	for i, element := range target.Elements {
		switch {
		case i < restIndex:
			result[i] = valueAt(i)
		case i == restIndex:
			if element.Target != nil {
				result[i] = &ArrayLiteral{
					Elements: append([]Expression(nil), source[restStart:restEnd]...),
					Position: target.Pos(),
				}
			}
		default:
			result[i] = valueAt(restEnd + i - restIndex - 1)
		}
	}
	return result
}

type assignmentMemberReceiver struct {
	class       *ClassDef
	classMethod bool
}

func (c *scriptChecker) exactAssignmentMemberReceivers(
	member *MemberExpr,
) ([]assignmentMemberReceiver, bool) {
	if member == nil {
		return nil, false
	}
	call := &CallExpr{
		Callee: &MemberExpr{
			Object:   member.Object,
			Property: member.Property + "=",
			Position: member.Pos(),
		},
		Position: member.Pos(),
	}
	candidates := c.captureDynamicCallCandidates(call)
	receivers := make([]assignmentMemberReceiver, 0, len(candidates.instanceClasses)+len(candidates.classValues))
	exact := false
	if candidates.instancesExact {
		exact = true
		for _, className := range candidates.instanceClasses {
			classDef := c.script.classes[className]
			if classDef == nil {
				return nil, false
			}
			receivers = append(receivers, assignmentMemberReceiver{class: classDef})
		}
	}
	if candidates.classValuesExact {
		exact = true
		for _, className := range candidates.classValues {
			classDef := c.script.classes[className]
			if classDef == nil {
				return nil, false
			}
			receivers = append(receivers, assignmentMemberReceiver{
				class:       classDef,
				classMethod: true,
			})
		}
	}
	return receivers, exact
}

func (c *scriptChecker) assignmentValueExpectation(
	target Expression,
	value Expression,
) expressionExpectation {
	member, ok := target.(*MemberExpr)
	if !ok || !memberAssignmentValueCanUseExpectation(value) {
		return expressionExpectation{}
	}
	receivers, exact := c.exactAssignmentMemberReceivers(member)
	if !exact || len(receivers) == 0 {
		return expressionExpectation{}
	}
	var merged *TypeExpr
	var sole expressionExpectation
	for _, receiver := range receivers {
		methods := receiver.class.Methods
		if receiver.classMethod {
			methods = receiver.class.ClassMethods
		}
		fn := methods[member.Property+"="]
		if fn == nil {
			return expressionExpectation{}
		}
		expectation := setterFunctionValueExpectation(fn)
		if expectation.empty() {
			return expressionExpectation{}
		}
		if len(receivers) == 1 {
			sole = expectation
		}
		if expectation.ty == nil {
			return expressionExpectation{}
		}
		merged = unionTypeExprs(merged, expectation.ty)
	}
	if len(receivers) == 1 {
		return sole
	}
	return typeExpressionExpectation(merged)
}

func (c *scriptChecker) assignmentSetterMayComplete(target, value Expression) bool {
	var call *CallExpr
	rawMemberWrite := false
	switch typed := target.(type) {
	case *MemberExpr:
		rawMemberWrite = true
		setter := *typed
		setter.Property += "="
		call = &CallExpr{
			Callee:   &setter,
			Args:     []Expression{value},
			Position: typed.Pos(),
		}
	case *IndexExpr:
		args := append([]Expression(nil), typed.Indices...)
		args = append(args, value)
		call = &CallExpr{
			Callee: &MemberExpr{
				Object:   typed.Object,
				Property: "[]=",
				Position: typed.Pos(),
			},
			Args:     args,
			Position: typed.Pos(),
		}
	default:
		return true
	}
	if member, ok := target.(*MemberExpr); ok {
		if receivers, exact := c.exactAssignmentMemberReceivers(member); exact {
			for _, receiver := range receivers {
				methods := receiver.class.Methods
				resolution := calleeMemberMethod
				name := receiver.class.Name + "#" + member.Property + "="
				if receiver.classMethod {
					methods = receiver.class.ClassMethods
					name = receiver.class.Name + "." + member.Property + "="
				}
				fn := methods[member.Property+"="]
				if fn == nil {
					if methods[member.Property] == nil {
						return true
					}
					continue
				}
				target := staticCallable{name: name, fn: fn, resolution: resolution}
				if !c.dynamicCallTargetVisible(receiver.class, receiver.classMethod, fn, false) {
					continue
				}
				plan := c.scriptCallBindingPlan(call, target)
				if plan.bodyMayEnter && c.scriptFunctionCallMayComplete(call, target) {
					return true
				}
			}
			return false
		}
	}

	resolvedTarget, resolved := c.resolveCallable(call)
	if resolved {
		if resolvedTarget.fn != nil {
			plan := c.scriptCallBindingPlan(call, resolvedTarget)
			return plan.bodyMayEnter &&
				c.scriptFunctionCallMayComplete(call, resolvedTarget)
		}
		view := staticCallViewFor(call, resolvedTarget)
		return c.builtinCallMayEnter(view, resolvedTarget.spec) &&
			c.specialBuiltinCallMayComplete(call, resolvedTarget.name) &&
			c.builtinCallMayComplete(resolvedTarget.spec)
	}

	candidates := c.captureDynamicCallCandidates(call)
	resolution := c.exactDynamicCallTargets(call, resolvedTarget, false, candidates)
	if resolution.lookupFails {
		// A missing member setter falls back to binding the member itself;
		// a missing []= implementation raises instead.
		return rawMemberWrite
	}
	if !resolution.exact {
		return true
	}
	c.refineDynamicCallTargetEntry(resolution.targets)
	return resolution.nonScriptMayComplete ||
		c.dynamicScriptCallTargetsMayComplete(resolution.targets)
}

func staticCallablePositionalArgumentExpectation(target staticCallable, index int) expressionExpectation {
	if target.fn != nil {
		param, ok := positionalCallableParam(target.fn.Params, index)
		if !ok {
			return expressionExpectation{}
		}
		return positionalArgumentExpectation(param)
	}
	if index < len(target.spec.paramTypes) {
		return typeExpressionExpectation(target.spec.paramTypes[index])
	}
	return expressionExpectation{}
}

func staticCallableKeywordArgumentExpectation(call *CallExpr, target staticCallable, name string) expressionExpectation {
	if target.fn != nil {
		if expected := keywordArgumentExpectedType(target.fn.Params, name); expected != nil {
			return typeExpressionExpectation(expected)
		}
		if staticCallCollapsesOptionsHash(call, target) {
			optionsType, ok := optionsHashArgumentType(target.fn, len(call.Args), func(candidate string) bool {
				for _, kwarg := range call.KwArgs {
					if kwarg.Name == candidate {
						return true
					}
				}
				return false
			})
			if ok {
				return typeExpressionExpectation(optionsHashArgumentValueType(optionsType, name))
			}
		}
		return expressionExpectation{}
	}
	return typeExpressionExpectation(target.spec.keywordTypes[name])
}

func (c *scriptChecker) checkConditionalExpression(
	function string,
	expr *ConditionalExpr,
	expectation expressionExpectation,
) bool {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	if !c.checkExpressionWithAuto(function, expr.Condition, true) {
		return false
	}
	c.collectRuntimeRequireCallExportsFromExpression(expr.Condition)
	conditionRuntimeState := c.snapshotRuntimeState()
	conditionScopeState := c.snapshotScopeState()
	branchRuntimeStates := make([]checkRuntimeState, 0, 2)
	branchScopeStates := make([]checkScopeState, 0, 2)
	finish := func() {
		c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
		c.mergeScopeStates(baseScopeState, branchScopeStates)
	}

	conditionTruthy, conditionKnown := c.inferredConditionTruthiness(expr.Condition)
	trueReachable := !conditionKnown || conditionTruthy
	if trueReachable {
		trueReachable = c.collectRuntimeConditionOutcomeEffects(expr.Condition, true)
	}
	if trueReachable {
		completed := c.checkExpressionWithExpectation(function, expr.Consequent, expectation)
		c.collectRuntimeRequireCallExportsFromExpression(expr.Consequent)
		if completed {
			branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		}
		if conditionKnown && conditionTruthy {
			return completed
		}
	}

	c.restoreRuntimeState(conditionRuntimeState)
	c.restoreScopeState(conditionScopeState)
	falseReachable := !conditionKnown || !conditionTruthy
	if falseReachable {
		falseReachable = c.collectRuntimeConditionOutcomeEffects(expr.Condition, false)
	}
	if !falseReachable {
		if len(branchRuntimeStates) == 0 {
			return false
		}
		finish()
		return true
	}
	completed := c.checkExpressionWithExpectation(function, expr.Alternate, expectation)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Alternate)
	if completed {
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}
	if conditionKnown {
		return completed
	}

	if len(branchRuntimeStates) == 0 {
		return false
	}
	finish()
	return true
}

func (c *scriptChecker) checkIfExpression(
	function string,
	expr *IfExpr,
	expectation expressionExpectation,
) bool {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	if !c.checkExpressionWithAuto(function, expr.Condition, true) {
		return false
	}
	c.collectRuntimeRequireCallExportsFromExpression(expr.Condition)
	conditionRuntimeState := c.snapshotRuntimeState()
	conditionScopeState := c.snapshotScopeState()
	branchRuntimeStates := make([]checkRuntimeState, 0, len(expr.ElseIf)+2)
	branchScopeStates := make([]checkScopeState, 0, len(expr.ElseIf)+2)
	finish := func() {
		c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
		c.mergeScopeStates(baseScopeState, branchScopeStates)
	}

	conditionTruthy, conditionKnown := c.inferredConditionTruthiness(expr.Condition)
	trueReachable := !conditionKnown || conditionTruthy
	if trueReachable {
		trueReachable = c.collectRuntimeConditionOutcomeEffects(expr.Condition, true)
	}
	if trueReachable {
		completed := c.checkExpressionWithExpectation(function, expr.Consequent, expectation)
		if completed {
			branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		}
		if conditionKnown && conditionTruthy {
			return completed
		}
	}
	c.restoreRuntimeState(conditionRuntimeState)
	c.restoreScopeState(conditionScopeState)
	falseReachable := !conditionKnown || !conditionTruthy
	if falseReachable {
		falseReachable = c.collectRuntimeConditionOutcomeEffects(expr.Condition, false)
	}
	if !falseReachable {
		if len(branchRuntimeStates) == 0 {
			return false
		}
		finish()
		return true
	}
	falseRuntimeState := c.snapshotRuntimeState()
	falseScopeState := c.snapshotScopeState()
	for _, branch := range expr.ElseIf {
		c.restoreRuntimeState(falseRuntimeState)
		c.restoreScopeState(falseScopeState)
		if !c.checkExpressionWithAuto(function, branch.Condition, true) {
			if len(branchRuntimeStates) == 0 {
				return false
			}
			finish()
			return true
		}
		c.collectRuntimeRequireCallExportsFromExpression(branch.Condition)
		conditionRuntimeState = c.snapshotRuntimeState()
		conditionScopeState = c.snapshotScopeState()
		branchTruthy, branchKnown := c.inferredConditionTruthiness(branch.Condition)
		trueReachable = !branchKnown || branchTruthy
		if trueReachable {
			trueReachable = c.collectRuntimeConditionOutcomeEffects(branch.Condition, true)
		}
		if trueReachable {
			completed := c.checkExpressionWithExpectation(function, branch.Result, expectation)
			if completed {
				branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
				branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
			}
			if branchKnown && branchTruthy {
				if len(branchRuntimeStates) == 0 {
					return false
				}
				finish()
				return true
			}
		}
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		falseReachable = !branchKnown || !branchTruthy
		if falseReachable {
			falseReachable = c.collectRuntimeConditionOutcomeEffects(branch.Condition, false)
		}
		if !falseReachable {
			if len(branchRuntimeStates) == 0 {
				return false
			}
			finish()
			return true
		}
		falseRuntimeState = c.snapshotRuntimeState()
		falseScopeState = c.snapshotScopeState()
	}
	c.restoreRuntimeState(falseRuntimeState)
	c.restoreScopeState(falseScopeState)
	if c.checkExpressionWithExpectation(function, expr.Alternate, expectation) {
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}
	if len(branchRuntimeStates) == 0 {
		return false
	}
	finish()
	return true
}

func (c *scriptChecker) checkRescueExpression(function string, expr *RescueExpr, autoCall bool) bool {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	baseTypePoison := cloneCheckStringSet(c.typePoison)
	failureArgumentFacts := c.scriptCallFailureArgumentFacts(expr.Body)
	bodyCompleted := false
	previousExpressionExitSites := c.expressionExitSites
	var expressionExitSites []checkStateSnapshot
	c.expressionExitSites = &expressionExitSites
	bodyEffects := c.captureClassConstantEffects(func() {
		bodyCompleted = c.checkExpressionWithAuto(function, expr.Body, autoCall)
		c.collectRuntimeRequireCallExportsFromExpression(expr.Body)
	})
	c.expressionExitSites = previousExpressionExitSites
	bodyRuntimeState := c.snapshotRuntimeState()
	bodyScopeState := c.snapshotScopeState()
	bodyTypePoison := cloneCheckStringSet(c.typePoison)
	if expressionProvenNonRaising(expr.Body) {
		return bodyCompleted
	}
	if errorKind, exact := c.staticallyRaisedExpressionErrorKind(expr.Body); exact &&
		!staticErrorKindMatchesRescue(errorKind, nil) {
		return false
	}

	if len(expressionExitSites) > 0 {
		runtimeStates := make([]checkRuntimeState, 0, len(expressionExitSites))
		scopeStates := make([]checkScopeState, 0, len(expressionExitSites))
		for _, site := range expressionExitSites {
			runtimeStates = append(runtimeStates, site.runtimeState)
			scopeStates = append(scopeStates, site.scopeState)
		}
		c.mergeRuntimeStates(baseRuntimeState, runtimeStates)
		c.mergeScopeStates(baseScopeState, scopeStates)
	} else if bodyCompleted {
		c.restoreRuntimeState(baseRuntimeState)
		c.applyClassConstantEffects(bodyEffects)
		c.restoreScopeState(baseScopeState)
	} else {
		// A definitely failing body reaches the fallback after every operand
		// and argument the checker already walked. Preserve those completed
		// mutations instead of rewinding to the pre-body scope.
		c.restoreRuntimeState(bodyRuntimeState)
		c.restoreScopeState(bodyScopeState)
	}
	for name, fact := range failureArgumentFacts {
		if _, alreadyPoisoned := baseTypePoison[name]; alreadyPoisoned {
			continue
		}
		delete(c.typePoison, name)
		c.bindLocalType(name, fact.typeExpr)
		c.bindLocalClassValue(name, "")
		switch {
		case len(fact.classNames) > 0:
			c.bindLocalClassValues(name, fact.classNames)
		case len(fact.callables) > 0:
			c.bindLocalCallableValues(name, fact.callables)
		case len(fact.staticVals) > 0:
			c.bindLocalStaticValues(name, fact.staticVals)
		}
	}
	fallbackCompleted := c.checkExpressionWithAuto(function, expr.Fallback, autoCall)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Fallback)
	fallbackRuntimeState := c.snapshotRuntimeState()
	fallbackScopeState := c.snapshotScopeState()
	c.typePoison = unionCheckStringSet(bodyTypePoison, c.typePoison)

	runtimeStates := make([]checkRuntimeState, 0, 2)
	scopeStates := make([]checkScopeState, 0, 2)
	if bodyCompleted {
		runtimeStates = append(runtimeStates, bodyRuntimeState)
		scopeStates = append(scopeStates, bodyScopeState)
	}
	if fallbackCompleted {
		runtimeStates = append(runtimeStates, fallbackRuntimeState)
		scopeStates = append(scopeStates, fallbackScopeState)
	}
	if len(runtimeStates) == 0 {
		return false
	}
	c.mergeRuntimeStates(baseRuntimeState, runtimeStates)
	c.mergeScopeStates(baseScopeState, scopeStates)
	return true
}

func (c *scriptChecker) checkCaseExpression(
	function string,
	expr *CaseExpr,
	expectation expressionExpectation,
) bool {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	if !c.checkExpressionWithAuto(function, expr.Target, true) {
		return false
	}
	c.collectRuntimeRequireCallExportsFromExpression(expr.Target)
	if result, known := c.inferredCaseExpressionResult(expr); known {
		completed := c.checkExpressionWithExpectation(function, result, expectation)
		c.collectRuntimeRequireCallExportsFromExpression(result)
		return completed
	}
	fallthroughRuntimeState := c.snapshotRuntimeState()
	fallthroughScopeState := c.snapshotScopeState()
	branchRuntimeStates := make([]checkRuntimeState, 0, len(expr.Clauses)+1)
	branchScopeStates := make([]checkScopeState, 0, len(expr.Clauses)+1)

	for _, clause := range expr.Clauses {
		for _, value := range clause.Values {
			c.restoreRuntimeState(fallthroughRuntimeState)
			c.restoreScopeState(fallthroughScopeState)
			if !c.checkExpressionWithAuto(function, value.Expr, true) {
				if len(branchRuntimeStates) == 0 {
					return false
				}
				c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
				c.mergeScopeStates(baseScopeState, branchScopeStates)
				return true
			}
			c.collectRuntimeRequireCallExportsFromExpression(value.Expr)
			if !c.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
				if len(branchRuntimeStates) == 0 {
					return false
				}
				c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
				c.mergeScopeStates(baseScopeState, branchScopeStates)
				return true
			}
			matchRuntimeState := c.snapshotRuntimeState()
			matchScopeState := c.snapshotScopeState()

			completed := c.checkExpressionWithExpectation(function, clause.Result, expectation)
			c.collectRuntimeRequireCallExportsFromExpression(clause.Result)
			if completed {
				branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
				branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
			}
			fallthroughRuntimeState = matchRuntimeState
			fallthroughScopeState = matchScopeState
		}
	}

	c.restoreRuntimeState(fallthroughRuntimeState)
	c.restoreScopeState(fallthroughScopeState)
	completed := c.checkExpressionWithExpectation(function, expr.ElseExpr, expectation)
	c.collectRuntimeRequireCallExportsFromExpression(expr.ElseExpr)
	if completed {
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}
	if len(branchRuntimeStates) == 0 {
		return false
	}

	c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
	c.mergeScopeStates(baseScopeState, branchScopeStates)
	return true
}

func staticNilSafeNavigationCall(call *CallExpr) bool {
	if call == nil || !call.Safe {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || !member.Safe {
		return false
	}
	val, ok := staticLiteralValue(member.Object)
	return ok && val.Kind() == KindNil
}

func safeNavigationCallMaySkipArguments(call *CallExpr) bool {
	if call == nil || !call.Safe {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || !member.Safe {
		return false
	}
	val, ok := staticLiteralValue(member.Object)
	return !ok || val.Kind() == KindNil
}

func (c *scriptChecker) checkMemberAutoCall(
	function string,
	member *MemberExpr,
) (staticCallable, bool, bool, bool) {
	target, ok := c.resolveMemberCallable(member)
	if !ok {
		call := &CallExpr{Callee: member, Position: member.Pos()}
		candidates := c.captureDynamicCallCandidates(call)
		resolution := c.exactMemberCallTargets(member.Property, call, candidates)
		if resolution.lookupFails {
			return staticCallable{}, false, false, false
		}
		previousFacts := c.callArgumentFacts
		previousClassValues := c.callArgumentClassValues
		previousCallables := c.callArgumentCallables
		previousStaticValues := c.callArgumentStaticValues
		c.callArgumentFacts = map[Expression]*TypeExpr{}
		c.callArgumentClassValues = map[Expression][]string{}
		c.callArgumentCallables = map[Expression][]*ScriptFunction{}
		c.callArgumentStaticValues = map[Expression][]Expression{}
		bodyMayEnter := c.refineDynamicCallTargetEntry(resolution.targets)
		c.checkDynamicCallTargets(function, resolution.targets, resolution.diagnoseTargets)
		c.applyDynamicCallNamespaceMutations(call, resolution.targets)
		c.callArgumentFacts = previousFacts
		c.callArgumentClassValues = previousClassValues
		c.callArgumentCallables = previousCallables
		c.callArgumentStaticValues = previousStaticValues
		if !resolution.exact {
			return staticCallable{}, false, true, true
		}
		invoked := bodyMayEnter || resolution.nonScriptMayComplete
		completed := resolution.nonScriptMayComplete ||
			c.dynamicScriptCallTargetsMayComplete(resolution.targets)
		return staticCallable{}, false, invoked, completed
	}
	view := staticCallView{pos: member.Pos()}
	if target.fn != nil {
		if !c.staticScriptCallTargetVisible(target) {
			return target, true, false, false
		}
		// A bare member read dispatches like a call, so snapshot the callee's
		// call-time runtime root before carrying its possible writes forward.
		autoInvokes := target.resolution != calleeMemberValue || target.constructor || len(target.fn.Params) == 0
		if autoInvokes {
			c.checkCallShape(function, view, target.name, target.fn)
			call := &CallExpr{Callee: member, Position: member.Pos()}
			plan := c.scriptCallBindingPlan(call, target)
			if plan.bodyMayEnter {
				c.enqueueReachableFunction(target.name, target.fn)
			} else if plan.defaultParams != nil {
				c.enqueueReachableFunctionBinding(target.name, target.fn, nil, plan)
			}
			c.applyAutoInvokedMemberNamespaceMutations(member, call, target)
			completed := plan.bodyMayEnter &&
				c.scriptFunctionCallMayComplete(call, target)
			return target, true, true, completed
		}
		return target, true, false, true
	}
	if target.spec.autoInvoke {
		c.checkBuiltinCallShape(function, view, target.name, target.spec)
		mayEnter := builtinCallShapeMayEnter(view, target.spec)
		return target, true, true, mayEnter && c.builtinCallMayComplete(target.spec)
	}
	return target, true, false, true
}

// checkBlockLiteral walks a block or lambda body. localReturns marks blocks
// whose returns stay inside the block itself (stabby lambdas and the lambda
// builtin's literal block); a plain block's return unwinds the enclosing
// function instead.
func (c *scriptChecker) checkBlockLiteral(function string, block *BlockLiteral, localReturns bool) {
	if block == nil {
		return
	}
	previousSummaryYieldsActive := c.summaryYieldsActive
	if localReturns {
		c.summaryYieldsActive = block == c.summaryYieldBlock
	}
	defer func() { c.summaryYieldsActive = previousSummaryYieldsActive }()
	// The restore models a block that may run zero times, but a namespace
	// write inside a call block that does run must keep governing dispatch,
	// so possible-write markers survive the restore. A lambda's body does
	// not run when the literal evaluates, so its writes count only where a
	// call can reach them (an escaping argument, a resolvable invocation),
	// never at the definition.
	runtimeState := c.snapshotRuntimeState()
	defer func() {
		walkMembers := c.runtimeNamespaceMembers
		c.restoreRuntimeState(runtimeState)
		if !localReturns {
			c.preserveRuntimeNamespaceMembers(walkMembers)
		}
	}()

	// Block and lambda returns are not checked against the enclosing
	// function's annotation today, so an active begin/ensure deferral must
	// not capture them either (a lambda return is local to the lambda).
	previousSites := c.deferredReturnSites
	previousExceptionExitSites := c.exceptionExitSites
	previousEnsureExitSites := c.ensureExitSites
	c.deferredReturnSites = nil
	c.exceptionExitSites = nil
	c.ensureExitSites = nil
	defer func() {
		c.deferredReturnSites = previousSites
		c.exceptionExitSites = previousExceptionExitSites
		c.ensureExitSites = previousEnsureExitSites
	}()
	// The enclosing call already accounts for any block effects that can run.
	// Keep the out-of-order block walk from reporting inert lambda effects as
	// exits or exceptions in the surrounding control-flow region.
	previousClassConstantCaptures := c.classConstantCaptures
	previousLoopExitEffects := c.loopExitEffects
	c.classConstantCaptures = nil
	c.loopExitEffects = nil
	defer func() {
		c.classConstantCaptures = previousClassConstantCaptures
		c.loopExitEffects = previousLoopExitEffects
	}()
	// A lambda's returns never leave the lambda, so a summary walk ignores
	// them. A plain block's return exits the enclosing function, but its
	// walk-time fact can be stale by the time the block actually runs (a
	// callee may mutate a captured container before yielding, or a stored
	// proc may fire after later reassignments), so any return the block walk
	// reaches poisons the summary instead of contributing a guessed arm.
	previousCollector := c.returnCollector
	var blockCollector *returnSummaryCollector
	if previousCollector != nil && !localReturns {
		blockCollector = &returnSummaryCollector{}
	}
	c.returnCollector = blockCollector
	defer func() {
		c.returnCollector = previousCollector
		if blockCollector.sawReturn() {
			previousCollector.record(nil)
		}
	}()

	// A call block may run zero or many times, so outer locals its body
	// assigns lose their facts before the walk. A lambda literal only creates
	// a callable; its body has not run yet, so creation preserves captured
	// outer facts. The walk's own bindings are rolled back in either case.
	if !block.Lambda {
		c.degradeBlockBodyBindings(block)
	}
	typesState := c.snapshotLocalTypes()
	defer c.restoreLocalTypes(typesState)
	classValuesState := c.snapshotLocalClassValues()
	defer c.restoreLocalClassValues(classValuesState)

	popScope := c.pushBlockCheckScope(block)
	defer popScope()
	popNameScope := c.pushBlockNameScope(block)
	defer popNameScope()
	// A block may run many times, so every body binding is live for shape
	// shadowing from the first walk on, like a loop body.
	c.recordLiveStatementNames(block.Body)

	for _, param := range block.Params {
		c.checkRuntimeTypeAnnotation(function, param.Type)
		c.checkDestructureTargetTypeAnnotations(function, param.Target)
		c.checkExpressionWithExpectation(function, param.DefaultVal, positionalArgumentExpectation(param))
		c.bindParamLocalType(param)
	}
	label := fmt.Sprintf("%s block at %d:%d", function, block.Pos().Line, block.Pos().Column)
	c.mutationRegionDepth++
	c.checkStatements(label, nil, block.Body)
	c.mutationRegionDepth--
}

// literalArrayElementYieldMethods are the builtin array iterators that yield
// each element as the block's single argument, in element order, so a typed
// block parameter can be validated against a literal receiver's elements.
var literalArrayElementYieldMethods = map[string]struct{}{
	"each":   {},
	"map":    {},
	"select": {},
	"reject": {},
	"find":   {},
}

// checkLiteralArrayBlockParamTypes validates typed block parameters against a
// literal array receiver of a builtin element iterator: ["x"].map do |v: int|
// fails on the first yield at runtime, so the contradiction is statically
// known. The check stays silent unless the receiver is an array literal whose
// elements are all scalar literals and the block declares exactly the plain
// named parameters the iterator yields (one element parameter, plus an index
// parameter for each_with_index); destructuring, rest parameters, implicit
// parameters, and every other receiver or method shape are left to runtime
// enforcement.
func (c *scriptChecker) checkLiteralArrayBlockParamTypes(function string, call *CallExpr) {
	if call == nil || call.Block == nil {
		return
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return
	}
	withIndex := member.Property == "each_with_index"
	if !withIndex {
		if _, ok := literalArrayElementYieldMethods[member.Property]; !ok {
			return
		}
	}
	arrayLit, ok := member.Object.(*ArrayLiteral)
	if !ok {
		return
	}
	elements := make([]Value, 0, len(arrayLit.Elements))
	for _, elem := range arrayLit.Elements {
		val, ok := staticLiteralValue(elem)
		if !ok || !scalarLiteralElementKind(val.Kind()) {
			return
		}
		elements = append(elements, val)
	}
	block := call.Block
	wantParams := 1
	if withIndex {
		wantParams = 2
	}
	if len(block.Params) != wantParams || len(block.ImplicitParams) > 0 {
		return
	}
	for _, param := range block.Params {
		if param.Kind != ParamNormal || param.Name == "" || param.Target != nil {
			return
		}
	}
	// The runtime rejects the first yielded value that misses its annotation,
	// so report only the first contradicting element in iteration order. Only
	// the first yield is guaranteed to happen: find stops on the first truthy
	// block result, and a break, return, or raise in the body can end the
	// iteration before a later element is reached, so later elements are
	// checked only when no early exit is possible.
	guaranteed := elements
	if len(elements) > 1 && (member.Property == "find" || blockBodyMayEscapeIteration(block.Body)) {
		guaranteed = elements[:1]
	}
	for index, element := range guaranteed {
		if c.addLiteralBlockParamMismatch(function, block.Params[0], element) {
			return
		}
		if withIndex && c.addLiteralBlockParamMismatch(function, block.Params[1], NewInt(int64(index))) {
			return
		}
	}
}

// blockBodyMayEscapeIteration reports whether the block body contains a
// statement that can stop the receiver's iteration before every element has
// been yielded: break ends the loop, return exits the enclosing method
// non-locally, and raise abandons the iteration even when a surrounding
// rescue keeps the script alive. Occurrences count at any nesting depth — a
// break inside a nested loop or block only exits that inner construct, but
// proving that statically is not worth risking a false positive, so any
// occurrence restricts the literal-receiver check to the first yield.
func blockBodyMayEscapeIteration(statements []Statement) bool {
	for _, stmt := range statements {
		if statementMayEscapeIteration(stmt) {
			return true
		}
	}
	return false
}

func statementMayEscapeIteration(stmt Statement) bool {
	switch typed := stmt.(type) {
	case nil:
		return false
	case *BreakStmt, *ReturnStmt, *RaiseStmt:
		return true
	case *RetryStmt:
		// A retry in a block body either unwinds to a rescue outside the
		// block or fails at the yield boundary with a local-jump error;
		// both stop the receiver's iteration early.
		return true
	case *AliasStmt, *EnumStmt:
		// Declarations without expression operands cannot escape (the
		// parser also rejects both inside block bodies).
		return false
	case *NextStmt:
		return expressionMayEscapeIteration(typed.Value)
	case *AssignStmt:
		return expressionMayEscapeIteration(typed.Target) || expressionMayEscapeIteration(typed.Value)
	case *ExprStmt:
		return expressionMayEscapeIteration(typed.Expr)
	case *IfStmt:
		if expressionMayEscapeIteration(typed.Condition) || blockBodyMayEscapeIteration(typed.Consequent) {
			return true
		}
		for _, elseIf := range typed.ElseIf {
			if statementMayEscapeIteration(elseIf) {
				return true
			}
		}
		return blockBodyMayEscapeIteration(typed.Alternate)
	case *ForStmt:
		return expressionMayEscapeIteration(typed.Iterable) || blockBodyMayEscapeIteration(typed.Body)
	case *WhileStmt:
		return expressionMayEscapeIteration(typed.Condition) || blockBodyMayEscapeIteration(typed.Body)
	case *UntilStmt:
		return expressionMayEscapeIteration(typed.Condition) || blockBodyMayEscapeIteration(typed.Body)
	case *TryStmt:
		if blockBodyMayEscapeIteration(typed.Body) || blockBodyMayEscapeIteration(typed.Else) || blockBodyMayEscapeIteration(typed.Ensure) {
			return true
		}
		for i := range typed.Rescues {
			if blockBodyMayEscapeIteration(typed.Rescues[i].Body) {
				return true
			}
		}
		return false
	case *FunctionStmt:
		return blockBodyMayEscapeIteration(typed.Body)
	case *ClassStmt:
		return blockBodyMayEscapeIteration(typed.Body)
	default:
		return false
	}
}

func expressionMayEscapeIteration(expr Expression) bool {
	switch typed := expr.(type) {
	case nil:
		return false
	case *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *RegexLiteral,
		*BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		// Leaves: no nested expressions that could escape.
		return false
	case *DestructureTarget:
		// Element targets can embed escaping expressions through index
		// selectors (a begin/end selector that raises, for example).
		for _, element := range typed.Elements {
			if expressionMayEscapeIteration(element.Target) {
				return true
			}
		}
		return false
	case *TryStmt, *IfStmt, *WhileStmt, *UntilStmt, *ForStmt:
		return statementMayEscapeIteration(typed.(Statement))
	case *BlockLiteral:
		for _, param := range typed.Params {
			if expressionMayEscapeIteration(param.DefaultVal) {
				return true
			}
		}
		return blockBodyMayEscapeIteration(typed.Body)
	case *CallExpr:
		if expressionMayEscapeIteration(typed.Callee) {
			return true
		}
		for _, arg := range typed.Args {
			if expressionMayEscapeIteration(arg) {
				return true
			}
		}
		for _, kwarg := range typed.KwArgs {
			if expressionMayEscapeIteration(kwarg.Value) {
				return true
			}
		}
		if typed.BlockArg != nil && expressionMayEscapeIteration(typed.BlockArg) {
			return true
		}
		return typed.Block != nil && expressionMayEscapeIteration(typed.Block)
	case *TypeLiteral:
		return typed.Fallback != nil && expressionMayEscapeIteration(typed.Fallback)
	case *SplatArg:
		return expressionMayEscapeIteration(typed.Value)
	case *UnaryExpr:
		return expressionMayEscapeIteration(typed.Right)
	case *BinaryExpr:
		return expressionMayEscapeIteration(typed.Left) || expressionMayEscapeIteration(typed.Right)
	case *ConditionalExpr:
		return expressionMayEscapeIteration(typed.Condition) ||
			expressionMayEscapeIteration(typed.Consequent) ||
			expressionMayEscapeIteration(typed.Alternate)
	case *IfExpr:
		if expressionMayEscapeIteration(typed.Condition) || expressionMayEscapeIteration(typed.Consequent) {
			return true
		}
		for _, branch := range typed.ElseIf {
			if expressionMayEscapeIteration(branch.Condition) || expressionMayEscapeIteration(branch.Result) {
				return true
			}
		}
		return expressionMayEscapeIteration(typed.Alternate)
	case *RescueExpr:
		return expressionMayEscapeIteration(typed.Body) || expressionMayEscapeIteration(typed.Fallback)
	case *RangeExpr:
		return expressionMayEscapeIteration(typed.Start) || expressionMayEscapeIteration(typed.End)
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if expressionMayEscapeIteration(elem) {
				return true
			}
		}
		return false
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			if expressionMayEscapeIteration(pair.Key) || expressionMayEscapeIteration(pair.Value) {
				return true
			}
		}
		return false
	case *IndexExpr:
		if expressionMayEscapeIteration(typed.Object) {
			return true
		}
		for _, index := range typed.Indices {
			if expressionMayEscapeIteration(index) {
				return true
			}
		}
		return false
	case *MemberExpr:
		return expressionMayEscapeIteration(typed.Object)
	case *ScopeExpr:
		return expressionMayEscapeIteration(typed.Object)
	case *CaseExpr:
		if expressionMayEscapeIteration(typed.Target) {
			return true
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if expressionMayEscapeIteration(value.Expr) {
					return true
				}
			}
			if expressionMayEscapeIteration(clause.Result) {
				return true
			}
		}
		return expressionMayEscapeIteration(typed.ElseExpr)
	case *YieldExpr:
		for _, arg := range typed.Args {
			if expressionMayEscapeIteration(arg) {
				return true
			}
		}
		return false
	case *InterpolatedString:
		return stringPartsMayEscapeIteration(typed.Parts)
	case *InterpolatedSymbol:
		return stringPartsMayEscapeIteration(typed.Parts)
	default:
		return false
	}
}

func stringPartsMayEscapeIteration(parts []StringPart) bool {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok && expressionMayEscapeIteration(exprPart.Expr) {
			return true
		}
	}
	return false
}

func scalarLiteralElementKind(kind ValueKind) bool {
	switch kind {
	case KindInt, KindFloat, KindString, KindBool, KindSymbol, KindNil:
		return true
	default:
		return false
	}
}

// addLiteralBlockParamMismatch reports whether the yielded value misses the
// block parameter's annotation, recording a warning that mirrors the runtime
// failure ("argument NAME expected TYPE, got KIND" at the annotation).
func (c *scriptChecker) addLiteralBlockParamMismatch(function string, param Param, val Value) bool {
	if param.Type == nil || !c.checkRuntimeTypeAnnotation(function, param.Type) {
		return false
	}
	err := c.checkRuntimeStaticValueType(val, param.Type)
	if err == nil {
		return false
	}
	var mismatch *typeMismatchError
	if errors.As(err, &mismatch) {
		c.addOrderIndependent(function, param.Type.Position, "argument %s expected %s, got %s", param.Name, mismatch.Expected, mismatch.Actual)
	} else {
		c.addOrderIndependent(function, param.Type.Position, "argument %s type check failed: %s", param.Name, err)
	}
	return true
}

func (c *scriptChecker) checkDestructureTargetTypeAnnotations(function string, target Expression) {
	destructure, ok := target.(*DestructureTarget)
	if !ok {
		return
	}
	for _, element := range destructure.Elements {
		c.checkRuntimeTypeAnnotation(function, element.Type)
		c.checkDestructureTargetTypeAnnotations(function, element.Target)
	}
}

func (c *scriptChecker) callMayEvaluateBlock(call *CallExpr) bool {
	return c.callMayEvaluateBlockWithSeen(call, nil)
}

func (c *scriptChecker) resolvedCallMayEvaluateBlock(call *CallExpr, target staticCallable, resolved bool) bool {
	if call == nil || call.Block == nil || staticNilSafeNavigationCall(call) {
		return false
	}
	if !resolved {
		return !staticallyNonCallableCallee(call.Callee)
	}
	if target.fn != nil {
		return c.functionMayEvaluateCallBlock(call, target, nil)
	}
	if target.name == "array.fetch" {
		return staticArrayFetchBlockMayEvaluate(call)
	}
	return target.spec.usesBlock
}

func (c *scriptChecker) callMayEvaluateBlockWithSeen(call *CallExpr, seen map[*ScriptFunction]struct{}) bool {
	if call == nil || call.Block == nil {
		return false
	}
	if staticNilSafeNavigationCall(call) {
		return false
	}
	target, ok := c.resolveCallable(call)
	if !ok {
		return !staticallyNonCallableCallee(call.Callee)
	}
	if target.fn != nil {
		return c.functionMayEvaluateCallBlock(call, target, seen)
	}
	if target.name == "array.fetch" {
		return staticArrayFetchBlockMayEvaluate(call)
	}
	return target.spec.usesBlock
}

func staticArrayFetchBlockMayEvaluate(call *CallExpr) bool {
	if call == nil || call.Block == nil {
		return false
	}
	if len(call.Args) < 1 || len(call.Args) > 2 {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return true
	}
	receiver, ok := staticLiteralValue(member.Object)
	if !ok || receiver.Kind() != KindArray {
		return true
	}
	indexValue, ok := staticLiteralValue(call.Args[0])
	if !ok {
		return true
	}
	index, ok := staticArrayFetchIndex(indexValue)
	if !ok {
		return false
	}
	normalized := index
	length := int64(len(receiver.Array()))
	if normalized < 0 {
		normalized += length
	}
	return normalized < 0 || normalized >= length
}

func staticArrayFetchIndex(value Value) (int64, bool) {
	switch value.Kind() {
	case KindInt:
		return value.Int(), true
	case KindFloat:
		floatIndex := value.Float()
		if math.Trunc(floatIndex) != floatIndex {
			return 0, false
		}
		return int64(floatIndex), true
	default:
		return 0, false
	}
}

func staticallyNonCallableCallee(expr Expression) bool {
	switch expr.(type) {
	case nil, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral,
		*ArrayLiteral, *HashLiteral, *RangeExpr, *BlockLiteral, *InterpolatedString, *InterpolatedSymbol:
		return true
	default:
		return false
	}
}

func (c *scriptChecker) functionMayEvaluateCallBlock(
	call *CallExpr,
	target staticCallable,
	seen map[*ScriptFunction]struct{},
) bool {
	fn := target.fn
	if fn == nil {
		return false
	}
	if seen == nil {
		seen = make(map[*ScriptFunction]struct{})
	}
	if _, ok := seen[fn]; ok {
		return true
	}
	seen[fn] = struct{}{}
	defer delete(seen, fn)

	plan := c.scriptCallBindingPlan(call, target)
	for _, paramIndex := range plan.defaultParams {
		if paramIndex < 0 || paramIndex >= len(fn.Params) {
			continue
		}
		if c.expressionMayEvaluateCallBlock(fn.Params[paramIndex].DefaultVal, seen) {
			return true
		}
	}
	if !plan.bodyMayEnter {
		return false
	}
	return c.statementsMayEvaluateCallBlock(fn.Body, seen)
}

func (c *scriptChecker) scriptFunctionCallBlockMayRun(call *CallExpr, target staticCallable) bool {
	if target.fn == nil {
		return false
	}
	restoreFresh := c.withFreshLocalInferenceScope()
	defer restoreFresh()
	popScope := c.pushScope(make(map[string]struct{}))
	defer popScope()
	popNameScope := c.pushFunctionNameScope(target.fn)
	defer popNameScope()
	restoreResolution := c.withClassConstantProofResolution(target.fn, scriptFunctionBindings(target.fn))
	defer restoreResolution()

	facts := c.reachableCallParamFacts(call, target)
	previousFacts := c.reachableParamFacts
	c.reachableParamFacts = facts
	defer func() { c.reachableParamFacts = previousFacts }()
	for _, param := range target.fn.Params {
		c.recordParamBinding(param)
		c.applyReachableParamFact(param)
	}
	return c.functionMayEvaluateCallBlock(call, target, nil)
}

func (c *scriptChecker) statementsMayEvaluateCallBlock(statements []Statement, seen map[*ScriptFunction]struct{}) bool {
	for _, stmt := range statements {
		if c.statementMayEvaluateCallBlock(stmt, seen) {
			return true
		}
		if statementAlwaysExits(stmt) || !c.statementMayCompleteForBinding(stmt) {
			return false
		}
	}
	return false
}

func (c *scriptChecker) statementMayCompleteForBinding(stmt Statement) bool {
	switch typed := stmt.(type) {
	case nil:
		return true
	case *ExprStmt:
		return c.expressionMayCompleteForBinding(typed.Expr)
	case *AssignStmt:
		if typed.Operator == "" {
			return c.expressionMayCompleteForBinding(typed.Value) &&
				c.plainAssignmentTargetMayCompleteForBinding(typed.Target, typed.Value)
		}
		if !c.expressionMayCompleteForBinding(typed.Target) {
			return false
		}
		if typed.Operator == tokenOrAssign || typed.Operator == tokenAndAssign {
			truthy, known := c.inferredConditionTruthiness(typed.Target)
			rhsReachable := !known ||
				(typed.Operator == tokenOrAssign && !truthy) ||
				(typed.Operator == tokenAndAssign && truthy)
			if !rhsReachable {
				return true
			}
			rhsCompletes := c.expressionMayCompleteForBinding(typed.Value) &&
				c.assignmentSetterMayComplete(typed.Target, typed.Value)
			return !known || rhsCompletes
		}
		if !c.expressionMayCompleteForBinding(typed.Value) {
			return false
		}
		operatorValue := &BinaryExpr{
			Left:     typed.Target,
			Operator: typed.Operator,
			Right:    typed.Value,
			Position: typed.Pos(),
		}
		return c.binaryExpressionMayComplete(operatorValue) &&
			c.assignmentSetterMayComplete(typed.Target, operatorValue)
	case *IfStmt:
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if !known {
			return true
		}
		if truthy {
			return c.statementsMayCompleteForBinding(typed.Consequent)
		}
		for _, branch := range typed.ElseIf {
			if !c.expressionMayCompleteForBinding(branch.Condition) {
				return false
			}
			truthy, known = c.inferredConditionTruthiness(branch.Condition)
			if !known {
				return true
			}
			if truthy {
				return c.statementsMayCompleteForBinding(branch.Consequent)
			}
		}
		return c.statementsMayCompleteForBinding(typed.Alternate)
	case *ReturnStmt, *RaiseStmt, *BreakStmt, *NextStmt, *RetryStmt:
		return false
	default:
		return true
	}
}

func (c *scriptChecker) statementsMayCompleteForBinding(statements []Statement) bool {
	for _, stmt := range statements {
		if statementAlwaysExits(stmt) || !c.statementMayCompleteForBinding(stmt) {
			return false
		}
	}
	return true
}

func (c *scriptChecker) plainAssignmentTargetMayCompleteForBinding(
	target, value Expression,
) bool {
	switch typed := target.(type) {
	case nil, *Identifier, *IvarExpr, *ClassVarExpr:
		return true
	case *MemberExpr:
		return c.expressionMayCompleteForBinding(typed.Object) &&
			c.assignmentSetterMayComplete(typed, value)
	case *IndexExpr:
		if !c.expressionMayCompleteForBinding(typed.Object) {
			return false
		}
		for _, index := range typed.Indices {
			if !c.expressionMayCompleteForBinding(index) {
				return false
			}
		}
		return c.assignmentSetterMayComplete(typed, value)
	case *DestructureTarget:
		values := destructureAssignmentExpressions(typed, value)
		for i, element := range typed.Elements {
			var elementValue Expression
			if i < len(values) {
				elementValue = values[i]
			}
			if !c.plainAssignmentTargetMayCompleteForBinding(element.Target, elementValue) {
				return false
			}
		}
		return true
	default:
		return c.expressionMayCompleteForBinding(target)
	}
}

func (c *scriptChecker) statementMayEvaluateCallBlock(stmt Statement, seen map[*ScriptFunction]struct{}) bool {
	switch typed := stmt.(type) {
	case nil, *NextStmt, *EnumStmt:
		return false
	case *ReturnStmt:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *RaiseStmt:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Message, seen)
	case *BreakStmt:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *AssignStmt:
		return c.expressionMayEvaluateCallBlock(typed.Target, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *ExprStmt:
		return c.expressionMayEvaluateCallBlock(typed.Expr, seen)
	case *IfStmt:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		truthy, known := staticExpressionTruthiness(typed.Condition)
		if !known || truthy {
			if c.statementsMayEvaluateCallBlock(typed.Consequent, seen) {
				return true
			}
			if known {
				return false
			}
		}
		for _, branch := range typed.ElseIf {
			if c.expressionMayEvaluateCallBlock(branch.Condition, seen) {
				return true
			}
			truthy, known = staticExpressionTruthiness(branch.Condition)
			if known && !truthy {
				continue
			}
			if c.statementsMayEvaluateCallBlock(branch.Consequent, seen) {
				return true
			}
			if known {
				return false
			}
		}
		return c.statementsMayEvaluateCallBlock(typed.Alternate, seen)
	case *ForStmt:
		return c.expressionMayEvaluateCallBlock(typed.Target, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Iterable, seen) ||
			c.statementsMayEvaluateCallBlock(typed.Body, seen)
	case *WhileStmt:
		return c.expressionMayEvaluateCallBlock(typed.Condition, seen) ||
			c.statementsMayEvaluateCallBlock(typed.Body, seen)
	case *UntilStmt:
		return c.expressionMayEvaluateCallBlock(typed.Condition, seen) ||
			c.statementsMayEvaluateCallBlock(typed.Body, seen)
	case *TryStmt:
		for i := range typed.Rescues {
			if c.statementsMayEvaluateCallBlock(typed.Rescues[i].Body, seen) {
				return true
			}
		}
		return c.statementsMayEvaluateCallBlock(typed.Body, seen) ||
			c.statementsMayEvaluateCallBlock(typed.Else, seen) ||
			c.statementsMayEvaluateCallBlock(typed.Ensure, seen)
	case *ClassStmt:
		return c.statementsMayEvaluateCallBlock(typed.Body, seen)
	default:
		return false
	}
}

func (c *scriptChecker) expressionMayEvaluateCallBlock(expr Expression, seen map[*ScriptFunction]struct{}) bool {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return false
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if c.expressionMayEvaluateCallBlock(elem, seen) {
				return true
			}
		}
		return false
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			if c.expressionMayEvaluateCallBlock(pair.Key, seen) ||
				c.expressionMayEvaluateCallBlock(pair.Value, seen) {
				return true
			}
		}
		return false
	case *CallExpr:
		if c.expressionMayEvaluateCallBlock(typed.Callee, seen) {
			return true
		}
		for _, arg := range typed.Args {
			if c.expressionMayEvaluateCallBlock(arg, seen) {
				return true
			}
		}
		for _, kwarg := range typed.KwArgs {
			if c.expressionMayEvaluateCallBlock(kwarg.Value, seen) {
				return true
			}
		}
		if typed.BlockArg != nil {
			// A forwarded block argument may wrap or be the current call
			// block, and the callee may invoke it.
			return true
		}
		return c.callMayEvaluateBlockWithSeen(typed, seen) &&
			c.blockLiteralMayEvaluateCallBlock(typed.Block, seen)
	case *MemberExpr:
		return c.expressionMayEvaluateCallBlock(typed.Object, seen)
	case *ScopeExpr:
		return c.expressionMayEvaluateCallBlock(typed.Object, seen)
	case *IndexExpr:
		if c.expressionMayEvaluateCallBlock(typed.Object, seen) {
			return true
		}
		for _, index := range typed.Indices {
			if c.expressionMayEvaluateCallBlock(index, seen) {
				return true
			}
		}
		return false
	case *DestructureTarget:
		for _, element := range typed.Elements {
			if c.expressionMayEvaluateCallBlock(element.Target, seen) {
				return true
			}
		}
		return false
	case *SplatArg:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *UnaryExpr:
		return c.expressionMayEvaluateCallBlock(typed.Right, seen)
	case *BinaryExpr:
		if c.expressionMayEvaluateCallBlock(typed.Left, seen) {
			return true
		}
		return binaryRightMayEvaluate(typed) && c.expressionMayEvaluateCallBlock(typed.Right, seen)
	case *ConditionalExpr:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		if branch, ok := staticConditionalExpressionBranch(typed); ok {
			return c.expressionMayEvaluateCallBlock(branch, seen)
		}
		return c.expressionMayEvaluateCallBlock(typed.Consequent, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Alternate, seen)
	case *RescueExpr:
		return c.expressionMayEvaluateCallBlock(typed.Body, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Fallback, seen)
	case *IfExpr:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Consequent, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Alternate, seen) {
			return true
		}
		for _, branch := range typed.ElseIf {
			if c.expressionMayEvaluateCallBlock(branch.Condition, seen) ||
				c.expressionMayEvaluateCallBlock(branch.Result, seen) {
				return true
			}
		}
		return false
	case *RangeExpr:
		return c.expressionMayEvaluateCallBlock(typed.Start, seen) ||
			c.expressionMayEvaluateCallBlock(typed.End, seen)
	case *CaseExpr:
		if c.expressionMayEvaluateCallBlock(typed.Target, seen) ||
			c.expressionMayEvaluateCallBlock(typed.ElseExpr, seen) {
			return true
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if c.expressionMayEvaluateCallBlock(value.Expr, seen) {
					return true
				}
			}
			if c.expressionMayEvaluateCallBlock(clause.Result, seen) {
				return true
			}
		}
		return false
	case *BlockLiteral:
		return false
	case *YieldExpr:
		return true
	case *InterpolatedString:
		return c.stringPartsMayEvaluateCallBlock(typed.Parts, seen)
	case *InterpolatedSymbol:
		return c.stringPartsMayEvaluateCallBlock(typed.Parts, seen)
	default:
		return false
	}
}

func (c *scriptChecker) blockLiteralMayEvaluateCallBlock(block *BlockLiteral, seen map[*ScriptFunction]struct{}) bool {
	if block == nil {
		return false
	}
	for _, param := range block.Params {
		if c.expressionMayEvaluateCallBlock(param.DefaultVal, seen) {
			return true
		}
	}
	return c.statementsMayEvaluateCallBlock(block.Body, seen)
}

func (c *scriptChecker) stringPartsMayEvaluateCallBlock(parts []StringPart, seen map[*ScriptFunction]struct{}) bool {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok && c.expressionMayEvaluateCallBlock(exprPart.Expr, seen) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) checkStringParts(function string, parts []StringPart) bool {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			if !c.checkExpression(function, exprPart.Expr) {
				return false
			}
		}
	}
	return true
}

func (c *scriptChecker) checkRuntimeTypeAnnotation(function string, ty *TypeExpr) bool {
	return c.checkTypeAnnotationWithContext(function, ty, c.runtimeTypeContext())
}

func (c *scriptChecker) checkTypeAnnotationWithContext(function string, ty *TypeExpr, ctx typeContext) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, ctx); err != nil {
		c.add(function, typeExprPosition(ty), "%s", err)
		return false
	}
	return true
}

func (c *scriptChecker) checkRuntimeExpressionAgainstType(function string, expr Expression, ty *TypeExpr, subject string) {
	val, ok := staticLiteralValue(expr)
	if !ok {
		c.checkInferredExpressionAgainstType(function, expr, ty, subject)
		return
	}
	c.checkRuntimeValueAgainstType(function, expr.Pos(), val, ty, subject)
}

func (c *scriptChecker) checkRuntimeExpressionAgainstTypeWithExpectation(
	function string,
	expr Expression,
	ty *TypeExpr,
	subject string,
	expectation expressionExpectation,
) {
	val, ok := staticLiteralValue(expr)
	if !ok {
		c.checkInferredExpressionAgainstTypeWithExpectation(function, expr, ty, subject, expectation)
		return
	}
	c.checkRuntimeValueAgainstType(function, expr.Pos(), val, ty, subject)
}

func (c *scriptChecker) checkRuntimeNilAgainstType(function string, pos Position, ty *TypeExpr, subject string) {
	c.checkRuntimeValueAgainstType(function, pos, NewNil(), ty, subject)
}

func (c *scriptChecker) checkRuntimeValueAgainstType(function string, pos Position, val Value, ty *TypeExpr, subject string) {
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	if err := c.checkRuntimeStaticValueType(val, ty); err != nil {
		c.addValueTypeWarning(function, pos, subject, err)
	}
}

func (c *scriptChecker) addValueTypeWarning(function string, pos Position, subject string, err error) {
	var mismatch *typeMismatchError
	if errors.As(err, &mismatch) {
		c.add(function, pos, "%s expected %s, got %s", subject, mismatch.Expected, mismatch.Actual)
		return
	}
	c.add(function, pos, "%s type check failed: %s", subject, err)
}

func (c *scriptChecker) checkRuntimeStaticValueType(val Value, ty *TypeExpr) error {
	_, err := normalizeValueForType(val, ty, c.runtimeTypeContext())
	return err
}

func (c *scriptChecker) checkImplicitReturn(function string, ty *TypeExpr, statements []Statement, pos Position) {
	if !c.checkRuntimeTypeAnnotation(function, ty) || typeAllowsNilReturn(ty) {
		return
	}
	c.checkImplicitFinalBlock(function, ty, statements, pos)
}

func (c *scriptChecker) checkImplicitFinalStatement(function string, ty *TypeExpr, stmt Statement) {
	switch typed := stmt.(type) {
	case *ReturnStmt, *RaiseStmt:
		return
	case *ExprStmt:
		if expressionCanImplicitlyYieldNil(typed.Expr) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
			return
		}
		c.checkImplicitLeafAgainstType(function, typed, typed.Expr, ty)
	case *AssignStmt:
		result := typed.Value
		if typed.Operator != "" {
			result = typed.Target
		}
		if expressionCanImplicitlyYieldNil(result) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
			return
		}
		c.checkImplicitLeafAgainstType(function, typed, result, ty)
	case *IfStmt:
		c.checkImplicitFinalIfStatement(function, ty, typed)
	case *ForStmt, *WhileStmt, *UntilStmt:
		c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
	case *TryStmt:
		if blockAlwaysExits(typed.Ensure) {
			return
		}
		if len(typed.Else) > 0 && !blockAlwaysExits(typed.Body) {
			// The else branch only runs when the body completes without an
			// explicit return or raise (see evalTryStatement: runElse requires
			// !returned and no error). When it runs, its final statement is the
			// result.
			c.checkImplicitFinalBlock(function, ty, typed.Else, typed.Pos())
		} else {
			// No reachable else: the body's final statement is the result. When
			// the body always exits, the else is unreachable dead code.
			c.checkImplicitFinalBlock(function, ty, typed.Body, typed.Pos())
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
			if len(clause.Body) > 0 {
				c.checkImplicitFinalBlock(function, ty, clause.Body, clause.Position)
			}
		}
	}
}

func (c *scriptChecker) checkImplicitFinalIfStatement(function string, ty *TypeExpr, stmt *IfStmt) {
	if stmt == nil {
		c.add(function, Position{}, "typed return %s can implicitly return nil", formatTypeExpr(ty))
		return
	}
	truthy, known := c.implicitConditionDecision(stmt, 0, stmt.Condition)
	if !known || truthy {
		c.checkImplicitFinalBlock(function, ty, stmt.Consequent, stmt.Pos())
		if known {
			return
		}
	}
	for i, elseIf := range stmt.ElseIf {
		truthy, known = c.implicitConditionDecision(stmt, i+1, elseIf.Condition)
		if known && !truthy {
			continue
		}
		c.checkImplicitFinalBlock(function, ty, elseIf.Consequent, elseIf.Pos())
		if known {
			return
		}
	}
	if len(stmt.Alternate) == 0 {
		c.add(function, stmt.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
		return
	}
	c.checkImplicitFinalBlock(function, ty, stmt.Alternate, stmt.Pos())
}

// implicitConditionDecision resolves a final if statement's branch
// reachability from the walker's recorded verdict — decided at the
// condition's own evaluation point — falling back to literal truthiness
// when the walk did not reach the condition.
func (c *scriptChecker) implicitConditionDecision(stmt *IfStmt, index int, condition Expression) (bool, bool) {
	if decisions, ok := c.implicitIfDecisions[stmt]; ok && index < len(decisions) {
		return decisions[index].truthy, decisions[index].known
	}
	return staticExpressionTruthiness(condition)
}

func (c *scriptChecker) checkImplicitFinalBlock(function string, ty *TypeExpr, statements []Statement, pos Position) {
	if len(statements) == 0 {
		c.add(function, pos, "typed return %s can implicitly return nil", formatTypeExpr(ty))
		return
	}
	c.checkImplicitFinalStatement(function, ty, effectiveFinalStatement(statements))
}

// maxCheckNestingDepth caps how deeply control flow may nest in a checked
// script. Check mode deliberately runs without step or memory quotas, and the
// checker re-walks enclosing bodies while descending (exit analysis,
// local-binding collection, branch state merges), so check time grows
// quadratically with nesting depth. The parser and the runtime accept nesting
// far past this cap (depth 50000 executes in well under a second), so the cap
// never rejects realistic scripts; it is purely an anti-hang backstop for
// hosts that check untrusted sources.
const maxCheckNestingDepth = 512

// nestingDepthWarnings scans every checkable body for control-flow nesting
// beyond maxCheckNestingDepth. Any hit yields one deterministic warning per
// offending callable and the caller skips the main check pass, so pathological
// nesting reports a diagnostic instead of stalling the checker.
func (c *scriptChecker) nestingDepthWarnings() []CheckWarning {
	var warnings []CheckWarning
	exceeded := func(label string, pos Position) {
		warnings = append(warnings, CheckWarning{
			Function: label,
			Pos:      pos,
			Message:  fmt.Sprintf("check exceeded maximum nesting depth of %d", maxCheckNestingDepth),
		})
	}
	for _, fn := range c.sortedScriptFunctions() {
		if functionExceedsCheckDepth(fn) {
			exceeded(fn.Name, fn.Pos)
		}
	}
	for _, classDef := range c.sortedClasses() {
		if len(classDef.Body) > 0 && statementsExceedCheckDepth(classDef.Body, maxCheckNestingDepth) {
			exceeded(classDef.Name+".<class body>", classDef.Body[0].Pos())
		}
		for _, method := range sortedCheckFunctions(classDef.Methods) {
			if functionExceedsCheckDepth(method) {
				exceeded(classDef.Name+"#"+method.Name, method.Pos)
			}
		}
		for _, method := range sortedCheckFunctions(classDef.ClassMethods) {
			if functionExceedsCheckDepth(method) {
				exceeded(classDef.Name+"."+method.Name, method.Pos)
			}
		}
	}
	return warnings
}

func functionExceedsCheckDepth(fn *ScriptFunction) bool {
	if fn == nil {
		return false
	}
	for _, param := range fn.Params {
		if expressionExceedsCheckDepth(param.DefaultVal, maxCheckNestingDepth) {
			return true
		}
	}
	return statementsExceedCheckDepth(fn.Body, maxCheckNestingDepth)
}

// statementsExceedCheckDepth reports whether any construct in the statements
// nests more than remaining levels deep. Only constructs the checker descends
// with per-level state (conditionals, loops, begin/rescue, class bodies,
// block literals, and expression-level control flow) count as a level;
// ordinary expression nesting is traversed without charging depth. Descent
// stops at the first construct past the budget, so the scan itself recurses
// at most remaining levels through counted constructs.
func statementsExceedCheckDepth(statements []Statement, remaining int) bool {
	for _, stmt := range statements {
		if statementExceedsCheckDepth(stmt, remaining) {
			return true
		}
	}
	return false
}

func statementExceedsCheckDepth(stmt Statement, remaining int) bool {
	switch typed := stmt.(type) {
	case nil:
		return false
	case *ReturnStmt:
		return expressionExceedsCheckDepth(typed.Value, remaining)
	case *RaiseStmt:
		return expressionExceedsCheckDepth(typed.Value, remaining) ||
			expressionExceedsCheckDepth(typed.Message, remaining)
	case *BreakStmt:
		return expressionExceedsCheckDepth(typed.Value, remaining)
	case *NextStmt:
		return expressionExceedsCheckDepth(typed.Value, remaining)
	case *AssignStmt:
		return expressionExceedsCheckDepth(typed.Target, remaining) ||
			expressionExceedsCheckDepth(typed.Value, remaining)
	case *ExprStmt:
		return expressionExceedsCheckDepth(typed.Expr, remaining)
	case *IfStmt:
		if remaining <= 0 {
			return true
		}
		if expressionExceedsCheckDepth(typed.Condition, remaining-1) ||
			statementsExceedCheckDepth(typed.Consequent, remaining-1) {
			return true
		}
		for _, elseIf := range typed.ElseIf {
			if expressionExceedsCheckDepth(elseIf.Condition, remaining-1) ||
				statementsExceedCheckDepth(elseIf.Consequent, remaining-1) {
				return true
			}
		}
		return statementsExceedCheckDepth(typed.Alternate, remaining-1)
	case *ForStmt:
		if remaining <= 0 {
			return true
		}
		return expressionExceedsCheckDepth(typed.Target, remaining-1) ||
			expressionExceedsCheckDepth(typed.Iterable, remaining-1) ||
			statementsExceedCheckDepth(typed.Body, remaining-1)
	case *WhileStmt:
		if remaining <= 0 {
			return true
		}
		return expressionExceedsCheckDepth(typed.Condition, remaining-1) ||
			statementsExceedCheckDepth(typed.Body, remaining-1)
	case *UntilStmt:
		if remaining <= 0 {
			return true
		}
		return expressionExceedsCheckDepth(typed.Condition, remaining-1) ||
			statementsExceedCheckDepth(typed.Body, remaining-1)
	case *TryStmt:
		if remaining <= 0 {
			return true
		}
		if statementsExceedCheckDepth(typed.Body, remaining-1) ||
			statementsExceedCheckDepth(typed.Else, remaining-1) ||
			statementsExceedCheckDepth(typed.Ensure, remaining-1) {
			return true
		}
		for i := range typed.Rescues {
			if statementsExceedCheckDepth(typed.Rescues[i].Body, remaining-1) {
				return true
			}
		}
		return false
	case *ClassStmt:
		if remaining <= 0 {
			return true
		}
		return statementsExceedCheckDepth(typed.Body, remaining-1)
	}
	return false
}

func expressionExceedsCheckDepth(expr Expression, remaining int) bool {
	switch typed := expr.(type) {
	case nil:
		return false
	case *TryStmt, *IfStmt, *WhileStmt, *UntilStmt, *ForStmt:
		return statementExceedsCheckDepth(typed.(Statement), remaining)
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if expressionExceedsCheckDepth(elem, remaining) {
				return true
			}
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			if expressionExceedsCheckDepth(pair.Key, remaining) ||
				expressionExceedsCheckDepth(pair.Value, remaining) {
				return true
			}
		}
	case *CallExpr:
		if expressionExceedsCheckDepth(typed.Callee, remaining) ||
			expressionExceedsCheckDepth(typed.BlockArg, remaining) {
			return true
		}
		for _, arg := range typed.Args {
			if expressionExceedsCheckDepth(arg, remaining) {
				return true
			}
		}
		for _, kwarg := range typed.KwArgs {
			if expressionExceedsCheckDepth(kwarg.Value, remaining) {
				return true
			}
		}
		return blockLiteralExceedsCheckDepth(typed.Block, remaining)
	case *SplatArg:
		return expressionExceedsCheckDepth(typed.Value, remaining)
	case *MemberExpr:
		return expressionExceedsCheckDepth(typed.Object, remaining)
	case *ScopeExpr:
		return expressionExceedsCheckDepth(typed.Object, remaining)
	case *IndexExpr:
		if expressionExceedsCheckDepth(typed.Object, remaining) {
			return true
		}
		for _, index := range typed.Indices {
			if expressionExceedsCheckDepth(index, remaining) {
				return true
			}
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			if expressionExceedsCheckDepth(element.Target, remaining) {
				return true
			}
		}
	case *UnaryExpr:
		return expressionExceedsCheckDepth(typed.Right, remaining)
	case *BinaryExpr:
		return expressionExceedsCheckDepth(typed.Left, remaining) ||
			expressionExceedsCheckDepth(typed.Right, remaining)
	case *RangeExpr:
		return expressionExceedsCheckDepth(typed.Start, remaining) ||
			expressionExceedsCheckDepth(typed.End, remaining)
	case *YieldExpr:
		for _, arg := range typed.Args {
			if expressionExceedsCheckDepth(arg, remaining) {
				return true
			}
		}
	case *InterpolatedString:
		return stringPartsExceedCheckDepth(typed.Parts, remaining)
	case *InterpolatedSymbol:
		return stringPartsExceedCheckDepth(typed.Parts, remaining)
	case *ConditionalExpr:
		if remaining <= 0 {
			return true
		}
		return expressionExceedsCheckDepth(typed.Condition, remaining-1) ||
			expressionExceedsCheckDepth(typed.Consequent, remaining-1) ||
			expressionExceedsCheckDepth(typed.Alternate, remaining-1)
	case *RescueExpr:
		if remaining <= 0 {
			return true
		}
		return expressionExceedsCheckDepth(typed.Body, remaining-1) ||
			expressionExceedsCheckDepth(typed.Fallback, remaining-1)
	case *IfExpr:
		if remaining <= 0 {
			return true
		}
		if expressionExceedsCheckDepth(typed.Condition, remaining-1) ||
			expressionExceedsCheckDepth(typed.Consequent, remaining-1) {
			return true
		}
		for _, branch := range typed.ElseIf {
			if expressionExceedsCheckDepth(branch.Condition, remaining-1) ||
				expressionExceedsCheckDepth(branch.Result, remaining-1) {
				return true
			}
		}
		return expressionExceedsCheckDepth(typed.Alternate, remaining-1)
	case *CaseExpr:
		if remaining <= 0 {
			return true
		}
		if expressionExceedsCheckDepth(typed.Target, remaining-1) {
			return true
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if expressionExceedsCheckDepth(value.Expr, remaining-1) {
					return true
				}
			}
			if expressionExceedsCheckDepth(clause.Result, remaining-1) {
				return true
			}
		}
		return expressionExceedsCheckDepth(typed.ElseExpr, remaining-1)
	case *BlockLiteral:
		return blockLiteralExceedsCheckDepth(typed, remaining)
	}
	return false
}

func blockLiteralExceedsCheckDepth(block *BlockLiteral, remaining int) bool {
	if block == nil {
		return false
	}
	if remaining <= 0 {
		return true
	}
	for _, param := range block.Params {
		if expressionExceedsCheckDepth(param.DefaultVal, remaining-1) {
			return true
		}
	}
	return statementsExceedCheckDepth(block.Body, remaining-1)
}

func stringPartsExceedCheckDepth(parts []StringPart, remaining int) bool {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			if expressionExceedsCheckDepth(exprPart.Expr, remaining) {
				return true
			}
		}
	}
	return false
}

// statementAlwaysExits reports whether evaluating stmt always terminates the
// current statement list, mirroring the runtime's "returned"/error signals. It
// lets the implicit-return check tell when a begin/else's else branch is
// unreachable: evalTryStatement only runs the else branch when the body did not
// return and did not raise. The analysis is conservative—it returns true only
// when every path is known to exit—so a false result never suppresses a real
// warning.
func statementAlwaysExits(stmt Statement) bool {
	switch typed := stmt.(type) {
	case *ReturnStmt, *RaiseStmt, *BreakStmt, *NextStmt, *RetryStmt:
		return true
	case *IfStmt:
		truthy, known := staticExpressionTruthiness(typed.Condition)
		if !known || truthy {
			if !blockAlwaysExits(typed.Consequent) {
				return false
			}
			if known {
				return true
			}
		}
		for _, elseIf := range typed.ElseIf {
			truthy, known = staticExpressionTruthiness(elseIf.Condition)
			if known && !truthy {
				continue
			}
			if !blockAlwaysExits(elseIf.Consequent) {
				return false
			}
			if known {
				return true
			}
		}
		return len(typed.Alternate) > 0 && blockAlwaysExits(typed.Alternate)
	case *TryStmt:
		if blockAlwaysExits(typed.Ensure) {
			return true
		}
		for i := range typed.Rescues {
			body := typed.Rescues[i].Body
			if len(body) > 0 && !blockAlwaysExits(body) {
				return false
			}
		}
		if blockAlwaysExits(typed.Body) {
			return true
		}
		return len(typed.Else) > 0 && blockAlwaysExits(typed.Else)
	default:
		return false
	}
}

// blockAlwaysExits reports whether a block always terminates, i.e. whether any
// reachable statement always exits. This is equivalent to testing the block's
// effective final statement (the first always-exiting statement, or the
// syntactic last one), but evaluates statementAlwaysExits once per statement:
// re-testing the statement effectiveFinalStatement returns would double the
// recursion at every nesting level and made deeply nested conditionals take
// exponential time to check.
func blockAlwaysExits(statements []Statement) bool {
	for _, stmt := range statements {
		if statementAlwaysExits(stmt) {
			return true
		}
	}
	return false
}

func effectiveFinalStatement(statements []Statement) Statement {
	for _, stmt := range statements {
		if statementAlwaysExits(stmt) {
			return stmt
		}
	}
	return statements[len(statements)-1]
}

func expressionCanImplicitlyYieldNil(expr Expression) bool {
	switch typed := expr.(type) {
	case nil:
		return true
	case *NilLiteral:
		return true
	case *IfExpr:
		truthy, known := staticExpressionTruthiness(typed.Condition)
		if !known || truthy {
			if expressionCanImplicitlyYieldNil(typed.Consequent) {
				return true
			}
			if known {
				return false
			}
		}
		for _, branch := range typed.ElseIf {
			truthy, known = staticExpressionTruthiness(branch.Condition)
			if known && !truthy {
				continue
			}
			if expressionCanImplicitlyYieldNil(branch.Result) {
				return true
			}
			if known {
				return false
			}
		}
		return typed.Alternate == nil || expressionCanImplicitlyYieldNil(typed.Alternate)
	case *ConditionalExpr:
		if branch, ok := staticConditionalExpressionBranch(typed); ok {
			return expressionCanImplicitlyYieldNil(branch)
		}
		return expressionCanImplicitlyYieldNil(typed.Consequent) ||
			expressionCanImplicitlyYieldNil(typed.Alternate)
	case *RescueExpr:
		return expressionCanImplicitlyYieldNil(typed.Body) ||
			expressionCanImplicitlyYieldNil(typed.Fallback)
	}
	return false
}

func typeAllowsNilReturn(ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if ty.Nullable || ty.Kind == TypeAny || ty.Kind == TypeNil {
		return true
	}
	if ty.Kind != TypeUnion {
		return false
	}
	for _, option := range ty.Union {
		if typeAllowsNilReturn(option) {
			return true
		}
	}
	return false
}

type staticCallable struct {
	name        string
	fn          *ScriptFunction
	spec        staticCallSpec
	resolution  calleeResolution
	constructor bool
	// constructorClass names the statically resolved class a constructor
	// call instantiates, so the call's result carries a nominal fact.
	constructorClass string
}

// staticCallSpec is the static contract of a builtin callable. A builtin
// without a spec is entirely unchecked; once a spec is attached the whole
// shape is enforced, so a registration gaining a spec must set minArgs and
// maxArgs deliberately (the zero value rejects all arguments).
type staticCallSpec struct {
	minArgs         int
	maxArgs         int
	rejectKeywords  bool
	allowedKeywords map[string]struct{}
	rejectBlock     bool
	autoInvoke      bool
	usesBlock       bool
	// paramTypes declares positional parameter types, index-aligned with the
	// call's arguments; a nil entry or an index past the end stays unknown,
	// so a variadic tail simply ends the slice.
	paramTypes []*TypeExpr
	// keywordTypes declares the types of known keyword arguments; absent
	// names stay unknown. allowedKeywords, not this map, decides which
	// keywords a call may pass.
	keywordTypes map[string]*TypeExpr
	// paramNames labels positional parameters in diagnostics, index-aligned
	// with paramTypes; a missing or empty entry falls back to the position.
	paramNames []string
	// fromSignature marks a contract published by a host Signature. Host
	// signature types resolve against the script's own scope, so an
	// unresolved name is a guaranteed runtime rejection worth reporting;
	// runtime-owned specs only use built-in kinds and never set this.
	fromSignature bool
	// resultType is the builtin's invariant result type; nil keeps the
	// result unknown for argument-dependent or unmodeled builtins.
	resultType *TypeExpr
}

func (c *scriptChecker) checkCallResolved(
	function string,
	call *CallExpr,
	target staticCallable,
	ok bool,
	dynamicTargets []checkDynamicCallTarget,
	diagnoseDynamicTargets bool,
) {
	if !ok {
		c.checkDynamicCallTargets(function, dynamicTargets, diagnoseDynamicTargets)
		return
	}
	if target.fn == nil && (target.name == "send" || target.name == "public_send") {
		c.checkDynamicCallTargets(function, dynamicTargets, diagnoseDynamicTargets)
	}
	if callExpandsArguments(call) {
		// Splat expansion makes the argument shape dynamic: the runtime
		// validates the expanded call with the same binding errors a literal
		// spelling would raise, so static shape checks step aside.
		if target.fn != nil {
			c.enqueueReachableFunction(target.name, target.fn)
		}
		return
	}
	if target.fn != nil {
		view := staticCallViewFor(call, target)
		c.checkCallShape(function, view, target.name, target.fn)
		c.checkCallArgumentTypes(function, view, target.name, target.fn)
		plan := c.scriptCallBindingPlan(call, target)
		facts := c.reachableCallParamFacts(call, target)
		if plan.bodyMayEnter {
			c.enqueueReachableFunctionWithParamFacts(
				target.name,
				target.fn,
				facts,
			)
		} else if plan.defaultParams != nil {
			c.enqueueReachableFunctionBinding(target.name, target.fn, facts, plan)
		}
		return
	}
	view := staticCallViewFor(call, target)
	c.checkBuiltinCallShape(function, view, target.name, target.spec)
	c.checkBuiltinArgumentTypes(function, view, target.name, target.spec)
	if target.name == "JSON.parse_as" {
		c.checkParseAsShapeArgument(function, call)
	}
	if _, isClassPredicate := classPredicateNames[target.name]; isClassPredicate {
		c.checkClassPredicateArgument(function, call, target.name)
	}
	if target.name == isTypeMemberName {
		c.checkIsTypeAtomArgument(function, call)
	}
}

func (c *scriptChecker) checkDynamicCallTargets(
	function string,
	targets []checkDynamicCallTarget,
	diagnose bool,
) {
	for _, candidate := range targets {
		if candidate.target.fn == nil || candidate.call == nil {
			continue
		}
		if diagnose && !callExpandsArguments(candidate.call) {
			view := staticCallViewFor(candidate.call, candidate.target)
			c.checkCallShape(function, view, candidate.target.name, candidate.target.fn)
			c.checkCallArgumentTypes(function, view, candidate.target.name, candidate.target.fn)
		}
		facts := c.reachableCallParamFacts(candidate.call, candidate.target)
		if candidate.mayEnter {
			c.enqueueReachableFunctionWithParamFacts(
				candidate.target.name,
				candidate.target.fn,
				facts,
			)
		} else if candidate.bindingStarts {
			c.enqueueReachableFunctionBinding(
				candidate.target.name,
				candidate.target.fn,
				facts,
				c.scriptCallBindingPlan(candidate.call, candidate.target),
			)
		}
	}
}

// checkIsTypeAtomArgument reports a literal is_type? atom the runtime always
// rejects. Non-literal atoms stay gradual, and the paramTypes contract already
// rejects provably non-symbol/string arguments.
func (c *scriptChecker) checkIsTypeAtomArgument(function string, call *CallExpr) {
	if len(call.Args) != 1 || callExpandsArguments(call) {
		return
	}
	arg := call.Args[0]
	values, exact := c.callStaticValueAlternatives(arg)
	if !exact {
		return
	}
	var firstErr error
	sawAtomArg := false
	for _, candidate := range values {
		value, static := staticLiteralValue(candidate)
		if !static {
			return
		}
		if _, validArg := typeAtomArg(value); !validArg {
			continue
		}
		sawAtomArg = true
		err := c.isTypeAtomValueError(value)
		if err == nil {
			return
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if sawAtomArg && firstErr != nil {
		c.add(function, arg.Pos(), "%s", firstErr)
	}
}

// captureDynamicCallCandidates snapshots exhaustive receiver and callable
// facts when the callee evaluates, before argument effects can change them.
func (c *scriptChecker) captureDynamicCallCandidates(call *CallExpr) checkDynamicCallCandidates {
	if call == nil {
		return checkDynamicCallCandidates{}
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return checkDynamicCallCandidates{}
	}
	instances, instancesExact := c.instanceClassExpressionNames(member.Object)
	instancesMayNil := instancesExact && !c.instanceExpressionNeverNil(member.Object)
	classes, classesExact := c.dispatchClassValueExpressionNames(member.Object)
	var callables []*ScriptFunction
	callablesExact := false
	if member.Property == "call" && typeExprMayIncludeCallable(c.inferExpressionType(member.Object)) {
		callables, callablesExact = c.callableExpressionFunctions(member.Object)
	}
	return checkDynamicCallCandidates{
		instanceClasses:  instances,
		instancesExact:   instancesExact,
		instancesMayNil:  instancesMayNil,
		classValues:      classes,
		classValuesExact: classesExact,
		callables:        callables,
		callablesExact:   callablesExact,
	}
}

func (c *scriptChecker) instanceExpressionNeverNil(expr Expression) bool {
	if fact, ok := c.constructorInstanceFacts[expr]; ok && fact.exact && len(fact.classNames) > 0 {
		// A constructor fact describes only calls that successfully produced
		// an instance. Their general expression type can remain unknown for a
		// dynamically selected class, but the successful result is still
		// necessarily non-nil.
		return true
	}
	return typeExprNeverNil(c.inferExpressionType(expr))
}

func callResolvesForwardedTargetAfterArguments(
	call *CallExpr,
	target staticCallable,
	resolved bool,
) bool {
	if call == nil {
		return false
	}
	if resolved {
		return target.fn == nil && (target.name == "send" || target.name == "public_send")
	}
	member, ok := call.Callee.(*MemberExpr)
	return ok && (member.Property == "send" || member.Property == "public_send")
}

func (c *scriptChecker) callCalleeLookupFails(
	call *CallExpr,
	target staticCallable,
	resolved bool,
	deferredForwarder bool,
	candidates checkDynamicCallCandidates,
	resolution checkDynamicCallResolution,
) bool {
	if call == nil {
		return false
	}
	if resolved {
		return target.fn != nil && !c.staticMemberCallTargetVisible(call, target)
	}
	if deferredForwarder {
		member, ok := call.Callee.(*MemberExpr)
		return ok && c.forwarderCalleeLookupFails(member.Property, candidates)
	}
	if c.implicitSelfConstructorLookupFails(call) {
		return true
	}
	return resolution.lookupFails
}

func (c *scriptChecker) staticMemberCallTargetVisible(call *CallExpr, target staticCallable) bool {
	if call == nil || target.fn == nil || target.constructor {
		return true
	}
	if _, memberCall := call.Callee.(*MemberExpr); !memberCall {
		return true
	}
	return c.staticScriptCallTargetVisible(target)
}

func (c *scriptChecker) staticScriptCallTargetVisible(target staticCallable) bool {
	if target.fn == nil || target.constructor {
		return true
	}
	c.prepareSelfScopeFunctions()
	classDef := c.selfScopeFnClasses[target.fn]
	if classDef == nil {
		return true
	}
	_, classMethod := c.selfScopeClassFns[target.fn]
	return c.dynamicCallTargetVisible(classDef, classMethod, target.fn, false)
}

func (c *scriptChecker) forwarderCalleeLookupFails(
	property string,
	candidates checkDynamicCallCandidates,
) bool {
	if c.script == nil {
		return false
	}
	if candidates.instancesExact {
		if len(candidates.instanceClasses) == 0 {
			return false
		}
		for _, className := range candidates.instanceClasses {
			classDef := c.script.classes[className]
			if classDef == nil {
				return false
			}
			override := classDef.Methods[property]
			if override == nil || c.dynamicCallTargetVisible(classDef, false, override, false) {
				return false
			}
		}
		return true
	}
	if !candidates.classValuesExact || len(candidates.classValues) == 0 {
		return false
	}
	for _, className := range candidates.classValues {
		classDef := c.script.classes[className]
		if classDef == nil {
			return false
		}
		override := classDef.ClassMethods[property]
		if override == nil || c.dynamicCallTargetVisible(classDef, true, override, false) {
			return false
		}
	}
	return true
}

// exactDynamicCallTargets resolves the known dynamic targets and records
// whether they exhaust every callable body the runtime may enter. Arguments
// may mutate forwarded member lookup before the body runs, while constructor
// result exactness is a separate question from callable-target exactness.
func (c *scriptChecker) exactDynamicCallTargets(
	call *CallExpr,
	resolvedTarget staticCallable,
	resolved bool,
	dynamicCandidates checkDynamicCallCandidates,
) checkDynamicCallResolution {
	if call == nil {
		return checkDynamicCallResolution{}
	}
	if !c.callArgumentExpansionMaySucceed(call) {
		return checkDynamicCallResolution{exact: true}
	}
	if expanded, exact := c.staticallyExpandedCall(call); exact {
		call = expanded
	}
	if resolved {
		if resolvedTarget.fn == nil && (resolvedTarget.name == "send" || resolvedTarget.name == "public_send") {
			return c.exactForwardedCallTargets(call, resolvedTarget.name == "send", dynamicCandidates)
		}
		return checkDynamicCallResolution{}
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return checkDynamicCallResolution{}
	}
	if member.Property == "send" || member.Property == "public_send" {
		return c.exactForwardedCallTargets(call, member.Property == "send", dynamicCandidates)
	}
	if member.Property == "call" && dynamicCandidates.callablesExact {
		targets := make([]checkDynamicCallTarget, 0, len(dynamicCandidates.callables))
		for _, fn := range dynamicCandidates.callables {
			targets = appendDynamicCallTarget(targets, call, staticCallable{
				name:       fn.Name + ".call",
				fn:         fn,
				resolution: calleeDirect,
			}, true)
		}
		return finalizeDynamicCallResolution(checkDynamicCallResolution{
			targets:         targets,
			exact:           true,
			diagnoseTargets: true,
		})
	}
	return c.exactMemberCallTargets(member.Property, call, dynamicCandidates)
}

// exactForwardedCallTargets models Object#send/public_send. Overrides receive
// the original call; universal forwarding removes the method-name argument.
func (c *scriptChecker) exactForwardedCallTargets(
	call *CallExpr,
	allowPrivate bool,
	dynamicCandidates checkDynamicCallCandidates,
) checkDynamicCallResolution {
	if call == nil || c.script == nil {
		return checkDynamicCallResolution{}
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return checkDynamicCallResolution{}
	}
	variants, variantsExact := c.forwardedCallVariants(call)
	appendTarget := func(
		targets []checkDynamicCallTarget,
		effectiveCall *CallExpr,
		classDef *ClassDef,
		classMethod bool,
		target staticCallable,
	) []checkDynamicCallTarget {
		visible := target.constructor || c.dynamicCallTargetVisible(
			classDef,
			classMethod,
			target.fn,
			allowPrivate,
		)
		return appendDynamicCallTarget(targets, effectiveCall, target, visible)
	}
	mergeNested := func(resolution *checkDynamicCallResolution, nested checkDynamicCallResolution) {
		resolution.targets = append(resolution.targets, nested.targets...)
		resolution.exact = resolution.exact && nested.exact
		resolution.diagnoseTargets = resolution.diagnoseTargets && nested.diagnoseTargets
		resolution.targetExists = resolution.targetExists || nested.targetExists
		resolution.targetMayEnter = resolution.targetMayEnter || nested.targetMayEnter
		resolution.nonScriptMayComplete = resolution.nonScriptMayComplete || nested.nonScriptMayComplete
	}
	resolution := checkDynamicCallResolution{exact: true}
	if dynamicCandidates.instancesExact {
		resolution.diagnoseTargets = !dynamicCandidates.instancesMayNil
		for _, className := range dynamicCandidates.instanceClasses {
			classDef := c.script.classes[className]
			if classDef == nil {
				resolution.exact = false
				continue
			}
			if override := classDef.Methods[member.Property]; override != nil {
				visible := c.dynamicCallTargetVisible(classDef, false, override, false)
				resolution.targets = appendDynamicCallTarget(resolution.targets, call, staticCallable{
					name:       classDef.Name + "#" + member.Property,
					fn:         override,
					resolution: calleeMemberMethod,
				}, visible)
				resolution.targetMayEnter = resolution.targetMayEnter ||
					visible && scriptCallBodyMayEnter(call, staticCallable{fn: override})
				continue
			}
			for _, variant := range variants {
				if !variant.valid {
					continue
				}
				if !variant.known {
					resolution.exact = false
					resolution.targetMayEnter = true
					resolution.nonScriptMayComplete = true
					continue
				}
				method := variant.method
				if (method == "send" || method == "public_send") && classDef.Methods[method] == nil {
					mergeNested(&resolution, c.exactForwardedCallTargets(
						variant.call,
						method == "send",
						dynamicCandidates,
					))
					continue
				}
				fn := classDef.Methods[method]
				visible := c.dynamicCallTargetVisible(classDef, false, fn, allowPrivate)
				resolution.targets = appendTarget(resolution.targets, variant.call, classDef, false, staticCallable{
					name:       classDef.Name + "#" + method,
					fn:         fn,
					resolution: calleeForwardedMethod,
				})
				resolution.targetMayEnter = resolution.targetMayEnter ||
					visible && scriptCallBodyMayEnter(variant.call, staticCallable{fn: fn})
				if fn == nil && !isUniversalDataSafe(method) && method != "class" {
					// An instance field may supply an unmodeled callable member.
					resolution.exact = false
					resolution.targetMayEnter = true
					resolution.nonScriptMayComplete = true
				} else if fn == nil {
					resolution.targetMayEnter = true
					resolution.nonScriptMayComplete = true
				}
			}
		}
		resolution.exact = resolution.exact && variantsExact
		return finalizeDynamicCallResolution(resolution)
	}
	if !dynamicCandidates.classValuesExact {
		return checkDynamicCallResolution{}
	}
	resolution.diagnoseTargets = true
	for _, className := range dynamicCandidates.classValues {
		classDef := c.script.classes[className]
		if classDef == nil {
			resolution.exact = false
			continue
		}
		if override := classDef.ClassMethods[member.Property]; override != nil {
			visible := c.dynamicCallTargetVisible(classDef, true, override, false)
			resolution.targets = appendDynamicCallTarget(resolution.targets, call, staticCallable{
				name:       classDef.Name + "." + member.Property,
				fn:         override,
				resolution: calleeMemberMethod,
			}, visible)
			resolution.targetMayEnter = resolution.targetMayEnter ||
				visible && scriptCallBodyMayEnter(call, staticCallable{fn: override})
			continue
		}
		for _, variant := range variants {
			if !variant.valid {
				continue
			}
			if !variant.known {
				resolution.exact = false
				resolution.targetMayEnter = true
				resolution.nonScriptMayComplete = true
				continue
			}
			method := variant.method
			if method == "new" && !classDef.IsModule {
				constructor := dynamicConstructorTarget(classDef, calleeForwardedMethod)
				resolution.targetExists = true
				resolution.targets = appendTarget(resolution.targets, variant.call, classDef, true, constructor)
				constructorMayEnter := dynamicCallableTargetMayEnter(variant.call, constructor)
				resolution.targetMayEnter = resolution.targetMayEnter || constructorMayEnter
				resolution.nonScriptMayComplete = resolution.nonScriptMayComplete || constructorMayEnter && constructor.fn == nil
				continue
			}
			if (method == "send" || method == "public_send") && classDef.ClassMethods[method] == nil {
				mergeNested(&resolution, c.exactForwardedCallTargets(
					variant.call,
					method == "send",
					dynamicCandidates,
				))
				continue
			}
			fn := classDef.ClassMethods[method]
			visible := c.dynamicCallTargetVisible(classDef, true, fn, allowPrivate)
			resolution.targets = appendTarget(resolution.targets, variant.call, classDef, true, staticCallable{
				name:       classDef.Name + "." + method,
				fn:         fn,
				resolution: calleeForwardedMethod,
			})
			resolution.targetMayEnter = resolution.targetMayEnter ||
				visible && scriptCallBodyMayEnter(variant.call, staticCallable{fn: fn})
			if fn == nil && c.classValueMemberMayChange(classDef, method) {
				resolution.exact = false
				resolution.targetMayEnter = true
				resolution.nonScriptMayComplete = true
			} else if fn == nil && isUniversalMember(method) {
				resolution.targetMayEnter = true
				resolution.nonScriptMayComplete = true
			}
		}
	}
	resolution.exact = resolution.exact && variantsExact
	return finalizeDynamicCallResolution(resolution)
}

func (c *scriptChecker) exactMemberCallTargets(
	method string,
	call *CallExpr,
	dynamicCandidates checkDynamicCallCandidates,
) checkDynamicCallResolution {
	if c.script == nil {
		return checkDynamicCallResolution{}
	}
	resolution := checkDynamicCallResolution{}
	if dynamicCandidates.instancesExact {
		resolution.exact = true
		resolution.diagnoseTargets = !dynamicCandidates.instancesMayNil
		for _, className := range dynamicCandidates.instanceClasses {
			classDef := c.script.classes[className]
			if classDef == nil {
				resolution.exact = false
				continue
			}
			if method == "" {
				resolution.exact = false
				resolution.targetMayEnter = true
				resolution.nonScriptMayComplete = true
				for _, fn := range sortedCheckFunctions(classDef.Methods) {
					resolution.targets = appendDynamicCallTarget(resolution.targets, call, staticCallable{
						name:       classDef.Name + "#" + fn.Name,
						fn:         fn,
						resolution: calleeMemberMethod,
					}, c.dynamicCallTargetVisible(classDef, false, fn, false))
				}
			} else {
				fn := classDef.Methods[method]
				visible := c.dynamicCallTargetVisible(classDef, false, fn, false)
				resolution.targets = appendDynamicCallTarget(resolution.targets, call, staticCallable{
					name:       classDef.Name + "#" + method,
					fn:         fn,
					resolution: calleeMemberMethod,
				}, visible)
				resolution.targetMayEnter = resolution.targetMayEnter ||
					visible && scriptCallBodyMayEnter(call, staticCallable{fn: fn})
				if fn == nil && !isUniversalDataSafe(method) && method != "class" {
					resolution.exact = false
					resolution.targetMayEnter = true
					resolution.nonScriptMayComplete = true
				} else if fn == nil {
					resolution.targetMayEnter = true
					resolution.nonScriptMayComplete = true
				}
			}
		}
		return finalizeDynamicCallResolution(resolution)
	}
	if !dynamicCandidates.classValuesExact {
		return checkDynamicCallResolution{}
	}
	resolution.exact = true
	resolution.diagnoseTargets = true
	for _, className := range dynamicCandidates.classValues {
		classDef := c.script.classes[className]
		if classDef == nil {
			resolution.exact = false
			continue
		}
		if method == "new" && !classDef.IsModule {
			constructor := dynamicConstructorTarget(classDef, calleeMemberValue)
			resolution.targetExists = true
			resolution.targets = appendDynamicCallTarget(resolution.targets, call, constructor, true)
			constructorMayEnter := dynamicCallableTargetMayEnter(call, constructor)
			resolution.targetMayEnter = resolution.targetMayEnter || constructorMayEnter
			resolution.nonScriptMayComplete = resolution.nonScriptMayComplete || constructorMayEnter && constructor.fn == nil
			continue
		}
		if method == "" {
			resolution.targetMayEnter = true
			resolution.nonScriptMayComplete = true
			if c.classValueHasDynamicCallableMembers(classDef) {
				resolution.exact = false
			}
			for _, fn := range sortedCheckFunctions(classDef.ClassMethods) {
				resolution.targets = appendDynamicCallTarget(resolution.targets, call, staticCallable{
					name:       classDef.Name + "." + fn.Name,
					fn:         fn,
					resolution: calleeMemberMethod,
				}, c.dynamicCallTargetVisible(classDef, true, fn, false))
			}
			continue
		}
		fn := classDef.ClassMethods[method]
		visible := c.dynamicCallTargetVisible(classDef, true, fn, false)
		resolution.targets = appendDynamicCallTarget(resolution.targets, call, staticCallable{
			name:       classDef.Name + "." + method,
			fn:         fn,
			resolution: calleeMemberMethod,
		}, visible)
		resolution.targetMayEnter = resolution.targetMayEnter ||
			visible && scriptCallBodyMayEnter(call, staticCallable{fn: fn})
		if fn == nil && c.classValueMemberMayChange(classDef, method) {
			resolution.exact = false
			resolution.targetMayEnter = true
			resolution.nonScriptMayComplete = true
		} else if fn == nil && isUniversalMember(method) {
			resolution.targetMayEnter = true
			resolution.nonScriptMayComplete = true
		}
	}
	return finalizeDynamicCallResolution(resolution)
}

func finalizeDynamicCallResolution(resolution checkDynamicCallResolution) checkDynamicCallResolution {
	resolution.targetExists = resolution.targetExists || resolution.targetMayEnter
	for _, candidate := range resolution.targets {
		resolution.targetExists = true
		if candidate.mayEnter {
			resolution.targetMayEnter = true
		}
	}
	resolution.lookupFails = resolution.exact && !resolution.targetExists
	return resolution
}

func dynamicConstructorTarget(classDef *ClassDef, resolution calleeResolution) staticCallable {
	return staticCallable{
		name:             classDef.Name + ".new",
		fn:               classDef.Methods["initialize"],
		resolution:       resolution,
		constructor:      true,
		constructorClass: classDef.Name,
	}
}

func (c *scriptChecker) dynamicCallTargetVisible(
	classDef *ClassDef,
	classMethod bool,
	fn *ScriptFunction,
	callerIsReceiver bool,
) bool {
	if fn == nil {
		return false
	}
	if callerIsReceiver {
		return true
	}
	if fn.Private {
		return false
	}
	if !fn.Protected {
		return true
	}
	return c.selfClass == classDef && c.selfClassContext == classMethod
}

func appendDynamicCallTarget(
	targets []checkDynamicCallTarget,
	call *CallExpr,
	target staticCallable,
	include bool,
) []checkDynamicCallTarget {
	if !include || call == nil || target.fn == nil {
		return targets
	}
	return append(targets, checkDynamicCallTarget{
		call:          call,
		target:        target,
		bindingStarts: scriptCallBodyMayEnter(call, target),
		mayEnter:      scriptCallBodyMayEnter(call, target),
	})
}

func (c *scriptChecker) refineDynamicCallTargetEntry(targets []checkDynamicCallTarget) bool {
	mayEnter := false
	for i := range targets {
		candidate := &targets[i]
		candidate.mayEnter = candidate.bindingStarts &&
			c.scriptCallBindingPlan(candidate.call, candidate.target).bodyMayEnter
		mayEnter = mayEnter || candidate.mayEnter
	}
	return mayEnter
}

func (c *scriptChecker) dynamicScriptCallTargetsMayComplete(targets []checkDynamicCallTarget) bool {
	for _, candidate := range targets {
		if candidate.mayEnter && c.scriptFunctionCallMayComplete(candidate.call, candidate.target) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) instanceClassExpressionNames(receiver Expression) ([]string, bool) {
	if fact, ok := c.constructorInstanceFacts[receiver]; ok {
		return append([]string(nil), fact.classNames...), fact.exact
	}
	var member *MemberExpr
	switch typed := receiver.(type) {
	case *CallExpr:
		member, _ = typed.Callee.(*MemberExpr)
	case *MemberExpr:
		member = typed
	}
	if member != nil {
		switch member.Property {
		case "new":
			if call, ok := receiver.(*CallExpr); ok && !c.callArgumentExpansionMaySucceed(call) {
				return nil, true
			}
			return c.constructorInstanceClassNames(member.Object, "")
		case "send", "public_send":
			call, ok := receiver.(*CallExpr)
			if !ok {
				break
			}
			if !c.callArgumentExpansionMaySucceed(call) {
				return nil, true
			}
			name, known, valid, _ := forwardedCallMethodName(call)
			if !known || !valid || name != "new" {
				break
			}
			return c.constructorInstanceClassNames(member.Object, member.Property)
		}
	}
	arms, ok := typeExprArms(c.inferExpressionType(receiver), 0)
	if !ok || len(arms) == 0 {
		return nil, false
	}
	classes := make([]string, 0, len(arms))
	seen := make(map[string]struct{}, len(arms))
	for _, arm := range arms {
		if arm.Kind == TypeNil {
			continue
		}
		if arm.Kind != TypeEnum {
			return nil, false
		}
		classDef := c.script.classes[arm.Name]
		if classDef == nil || classDef.IsModule {
			return nil, false
		}
		if _, duplicate := seen[classDef.Name]; duplicate {
			continue
		}
		seen[classDef.Name] = struct{}{}
		classes = append(classes, classDef.Name)
	}
	return classes, len(classes) > 0
}

func (c *scriptChecker) constructorInstanceClassNames(receiver Expression, forwarder string) ([]string, bool) {
	classes, exact := c.dispatchClassValueExpressionNames(receiver)
	return c.constructorInstanceClassNamesForClasses(classes, exact, forwarder)
}

func (c *scriptChecker) constructorInstanceClassNamesForClasses(
	classes []string,
	exact bool,
	forwarder string,
) ([]string, bool) {
	if !exact || c.script == nil {
		return nil, false
	}
	instances := make([]string, 0, len(classes))
	for _, className := range classes {
		classDef := c.script.classes[className]
		if classDef == nil {
			return nil, false
		}
		if forwarder != "" && classValueMethodMayOverride(classDef, forwarder) {
			return nil, false
		}
		if classDef.IsModule {
			// A plain module raises before producing a receiver, so it can be
			// removed from the successful-path set. A module-provided new may
			// return any value, making every downstream receiver gradual.
			if classValueMemberMayOverride(classDef, "new") ||
				c.opaqueClassConstants || c.classConstantContext.opaque ||
				c.namespaceMemberMutated(classDef.Name, "new") {
				return nil, false
			}
			continue
		}
		instances = append(instances, classDef.Name)
	}
	return instances, true
}

// pinDirectConstructorInstanceFact captures the receiver set when a direct
// `new` member is resolved, before arguments or the call itself can change
// class constants that govern the lookup.
func (c *scriptChecker) pinDirectConstructorInstanceFact(call *CallExpr) bool {
	if call == nil {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || member.Property != "new" {
		return false
	}
	classes, exact := c.constructorInstanceClassNames(member.Object, "")
	lookupFails := exact && len(classes) == 0
	if c.callArgumentExpansionMaySucceed(call) {
		c.pinConstructorInstanceFact(call, classes, exact)
	} else {
		// The callee resolves, but argument expansion prevents construction.
		// Its operands still run before this exact-empty result is observed.
		c.pinConstructorInstanceFact(call, nil, true)
	}
	return lookupFails
}

// pinForwardedConstructorInstanceFact captures `send(:new)` after its
// arguments run because forwarding performs the nested `new` lookup then.
func (c *scriptChecker) pinForwardedConstructorInstanceFact(
	call *CallExpr,
	candidates checkDynamicCallCandidates,
) {
	if call == nil {
		return
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || (member.Property != "send" && member.Property != "public_send") {
		return
	}
	if !c.callArgumentExpansionMaySucceed(call) {
		c.pinConstructorInstanceFact(call, nil, true)
		return
	}
	classes, exact := c.forwardedConstructorInstanceClassNames(call, candidates, 0)
	c.pinConstructorInstanceFact(call, classes, exact)
}

func (c *scriptChecker) forwardedConstructorInstanceClassNames(
	call *CallExpr,
	candidates checkDynamicCallCandidates,
	depth int,
) ([]string, bool) {
	if call == nil || depth >= maxCheckNestingDepth {
		return nil, false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || (member.Property != "send" && member.Property != "public_send") ||
		!candidates.classValuesExact {
		return nil, false
	}
	for _, className := range candidates.classValues {
		classDef := c.script.classes[className]
		if classDef == nil || c.classValueMemberMayChange(classDef, member.Property) {
			return nil, false
		}
	}
	variants, exact := c.forwardedCallVariants(call)
	if !exact {
		return nil, false
	}
	var instances []string
	for _, variant := range variants {
		if !variant.valid {
			continue
		}
		if !variant.known {
			return nil, false
		}
		switch variant.method {
		case "new":
			classes, classesExact := c.constructorInstanceClassNamesForClasses(
				candidates.classValues,
				true,
				"",
			)
			if !classesExact {
				return nil, false
			}
			instances = append(instances, classes...)
		case "send", "public_send":
			nested := *variant.call
			nested.Callee = &MemberExpr{
				Object:   member.Object,
				Property: variant.method,
				Position: member.Pos(),
			}
			classes, classesExact := c.forwardedConstructorInstanceClassNames(&nested, candidates, depth+1)
			if !classesExact {
				return nil, false
			}
			instances = append(instances, classes...)
		default:
			provablyFails := true
			for _, className := range candidates.classValues {
				classDef := c.script.classes[className]
				if classDef == nil || classDef.ClassMethods[variant.method] != nil ||
					isUniversalMember(variant.method) ||
					c.classValueMemberMayChange(classDef, variant.method) {
					provablyFails = false
					break
				}
			}
			if !provablyFails {
				return nil, false
			}
		}
	}
	return normalizeCheckClassNames(instances), true
}

func (c *scriptChecker) pinConstructorInstanceFact(expr Expression, classes []string, exact bool) {
	if expr == nil {
		return
	}
	if c.constructorInstanceFacts == nil {
		c.constructorInstanceFacts = make(map[Expression]checkInstanceClassFact)
	}
	c.constructorInstanceFacts[expr] = checkInstanceClassFact{
		classNames: append([]string(nil), classes...),
		exact:      exact,
	}
}

func classValueMethodMayOverride(classDef *ClassDef, member string) bool {
	if classDef == nil {
		return false
	}
	_, ok := classDef.ClassMethods[member]
	return ok
}

func (c *scriptChecker) classValueMemberMayChange(classDef *ClassDef, member string) bool {
	if classDef == nil || isUniversalDataSafe(member) {
		return false
	}
	if _, ok := classDef.ClassVars[member]; ok {
		return true
	}
	return c.opaqueClassConstants || c.classConstantContext.opaque ||
		c.namespaceMemberMutated(classDef.Name, member)
}

func (c *scriptChecker) classValueHasDynamicCallableMembers(classDef *ClassDef) bool {
	if classDef == nil {
		return true
	}
	if len(classDef.ClassVars) > 0 || c.opaqueClassConstants || c.classConstantContext.opaque {
		return true
	}
	prefix := classDef.Name + "."
	for member := range c.classConstantContext.namespaceMembers {
		if strings.HasPrefix(member, prefix) {
			return true
		}
	}
	for member := range c.runtimeNamespaceMembers {
		if strings.HasPrefix(member, prefix) {
			return true
		}
	}
	return false
}

func classValueMemberMayOverride(classDef *ClassDef, member string) bool {
	if classDef == nil {
		return false
	}
	if _, ok := classDef.ClassMethods[member]; ok {
		return true
	}
	_, ok := classDef.ClassVars[member]
	return ok
}

func (c *scriptChecker) reachableCallParamFacts(
	call *CallExpr,
	target staticCallable,
) map[string]checkReachableParamFact {
	fn := target.fn
	if call == nil || fn == nil || callExpandsArguments(call) {
		return nil
	}
	view := staticCallViewFor(call, target)
	argumentIdentityFacts := func(
		expr Expression,
		expectation expressionExpectation,
	) ([]string, []*ScriptFunction) {
		identitySource := expr
		if expectation.includesCallable() {
			if callableExpr, bindable := c.bareIdentifierCallableArgument(expr); bindable {
				identitySource = callableExpr
				if call, ok := identitySource.(*CallExpr); ok {
					identitySource = call.Callee
				}
			}
		}
		identityExpr, autoInvoked := c.evaluatedIdentityExpression(
			identitySource,
			!expectation.includesCallable(),
		)
		classNames, classExact := c.classValueExpressionNames(identityExpr)
		if autoInvoked {
			classNames, classExact = c.dispatchClassValueExpressionNames(identityExpr)
		}
		if !classExact {
			classNames = nil
		}
		callables, callableExact := c.callableExpressionFunctions(identityExpr)
		if !callableExact {
			callables = nil
		}
		return classNames, callables
	}
	facts := make(map[string]checkReachableParamFact)
	positionallyBound := make(map[string]struct{})
	for i, arg := range view.args {
		param, ok := positionalCallableParam(fn.Params, i)
		if !ok || param.Name == "" {
			continue
		}
		if _, exists := facts[param.Name]; !exists {
			facts[param.Name] = checkReachableParamFact{}
		}
		if param.Kind == ParamRest {
			continue
		}
		positionallyBound[param.Name] = struct{}{}
		fact := c.callArgumentFacts[arg]
		if fact == nil {
			fact = c.inferExpressionType(arg)
		}
		classNames, classCaptured := c.callArgumentClassValues[arg]
		callables, callablesCaptured := c.callArgumentCallables[arg]
		if !classCaptured || !callablesCaptured {
			fallbackClasses, fallbackCallables := argumentIdentityFacts(
				arg,
				staticCallablePositionalArgumentExpectation(target, i),
			)
			if !classCaptured {
				classNames = fallbackClasses
			}
			if !callablesCaptured {
				callables = fallbackCallables
			}
		}
		staticVals, staticExact := c.callStaticValueAlternatives(arg)
		if fact != nil || len(classNames) > 0 || len(callables) > 0 || staticExact {
			facts[param.Name] = checkReachableParamFact{
				typeExpr:   fact,
				classNames: append([]string(nil), classNames...),
				callables:  append([]*ScriptFunction(nil), callables...),
				staticVals: append([]Expression(nil), staticVals...),
			}
		}
	}
	for _, kwarg := range view.kwargs {
		if kwarg.Splat {
			continue
		}
		for _, param := range fn.Params {
			if param.Name != kwarg.Name || (param.Kind != ParamNormal && param.Kind != ParamKeyword) {
				continue
			}
			if param.Kind == ParamNormal {
				if _, supplied := positionallyBound[param.Name]; supplied {
					continue
				}
			}
			if _, exists := facts[param.Name]; !exists {
				facts[param.Name] = checkReachableParamFact{}
			}
			fact := c.callArgumentFacts[kwarg.Value]
			if fact == nil {
				fact = c.inferExpressionType(kwarg.Value)
			}
			classNames, classCaptured := c.callArgumentClassValues[kwarg.Value]
			callables, callablesCaptured := c.callArgumentCallables[kwarg.Value]
			if !classCaptured || !callablesCaptured {
				fallbackClasses, fallbackCallables := argumentIdentityFacts(
					kwarg.Value,
					staticCallableKeywordArgumentExpectation(call, target, kwarg.Name),
				)
				if !classCaptured {
					classNames = fallbackClasses
				}
				if !callablesCaptured {
					callables = fallbackCallables
				}
			}
			staticVals, staticExact := c.callStaticValueAlternatives(kwarg.Value)
			if fact != nil || len(classNames) > 0 || len(callables) > 0 || staticExact {
				facts[param.Name] = checkReachableParamFact{
					typeExpr:   fact,
					classNames: append([]string(nil), classNames...),
					callables:  append([]*ScriptFunction(nil), callables...),
					staticVals: append([]Expression(nil), staticVals...),
				}
			}
			break
		}
	}
	// The normalized view supplies inferred values, while callParamSupply is
	// authoritative about defaults. In particular, an options-hash collapse
	// consumes every raw keyword, so a later same-named parameter must discard
	// the fact captured from the original spelling and run its default.
	collapseOptionsHash := staticCallCollapsesOptionsHash(call, target)
	for i, param := range fn.Params {
		if param.Name == "" {
			continue
		}
		_, mayDefault := callParamSupply(call, fn, i, collapseOptionsHash)
		if mayDefault {
			if param.DefaultVal == nil {
				delete(facts, param.Name)
				continue
			}
			facts[param.Name] = checkReachableParamFact{usesDefault: true}
			continue
		}
		fact := facts[param.Name]
		fact.usesDefault = false
		facts[param.Name] = fact
	}
	if len(facts) == 0 {
		return nil
	}
	return facts
}

func callableParamLambdaArguments(
	call *CallExpr,
	target staticCallable,
	facts map[string]checkReachableParamFact,
) map[string][]Expression {
	if call == nil || target.fn == nil || callExpandsArguments(call) {
		return nil
	}
	view := staticCallViewFor(call, target)
	lambdas := make(map[string][]Expression)
	positionallyBound := make(map[string]struct{})
	for i, arg := range view.args {
		param, ok := positionalCallableParam(target.fn.Params, i)
		if !ok || param.Name == "" || param.Kind == ParamRest {
			continue
		}
		positionallyBound[param.Name] = struct{}{}
		if lambdaLiteralBlock(arg) != nil {
			lambdas[param.Name] = append(lambdas[param.Name], arg)
		}
	}
	lastKeyword := make(map[string]int, len(view.kwargs))
	for i, kwarg := range view.kwargs {
		lastKeyword[kwarg.Name] = i
	}
	for i, kwarg := range view.kwargs {
		if kwarg.Splat || lastKeyword[kwarg.Name] != i {
			continue
		}
		if _, supplied := positionallyBound[kwarg.Name]; supplied {
			continue
		}
		if lambdaLiteralBlock(kwarg.Value) != nil {
			lambdas[kwarg.Name] = append(lambdas[kwarg.Name], kwarg.Value)
		}
	}
	for _, param := range target.fn.Params {
		fact, ok := facts[param.Name]
		if !ok || !fact.usesDefault || lambdaLiteralBlock(param.DefaultVal) == nil {
			continue
		}
		lambdas[param.Name] = append(lambdas[param.Name], param.DefaultVal)
	}
	if len(lambdas) == 0 {
		return nil
	}
	return lambdas
}

// checkParseAsShapeArgument reports a JSON.parse_as call whose second
// argument is provably not a shape value — a scalar, a data hash, or any
// other fully known non-shape type — since the runtime always rejects it.
func (c *scriptChecker) checkParseAsShapeArgument(function string, call *CallExpr) {
	if len(call.Args) != 2 || callExpandsArguments(call) {
		return
	}
	raw := call.Args[0]
	if classes, exact := c.classValueExpressionNames(raw); exact && len(classes) > 0 {
		c.add(function, raw.Pos(), "call to JSON.parse_as expects a JSON string as its first argument, got class %s", strings.Join(classes, " or "))
	}
	rawType, rawCaptured := c.callArgumentFacts[raw]
	if !rawCaptured {
		rawType = c.inferExpressionType(raw)
	}
	if rawType != nil && typeExprsDisjoint(rawType, checkTypeString, c.checkNamedTypeResolver()) {
		c.add(function, raw.Pos(), "call to JSON.parse_as expects a JSON string as its first argument, got %s", formatTypeExpr(rawType))
	}
	arg := call.Args[1]
	if classes, exact := c.classValueExpressionNames(arg); exact && len(classes) > 0 {
		c.add(function, arg.Pos(), "call to JSON.parse_as expects a type literal as its second argument, got class %s", strings.Join(classes, " or "))
		return
	}
	inferred, captured := c.callArgumentFacts[arg]
	if !captured {
		inferred = c.inferExpressionType(arg)
	}
	if inferred == nil {
		return
	}
	arms, ok := typeExprArms(inferred, 0)
	if !ok || len(arms) == 0 {
		return
	}
	for _, arm := range arms {
		if _, isShape := shapeValuePayload(arm); isShape {
			return
		}
	}
	c.add(function, arg.Pos(), "call to JSON.parse_as expects a type literal as its second argument, got %s", formatTypeExpr(inferred))
}

// classPredicateNames are the universal predicates whose single argument the
// runtime requires to be a class or module value (newClassPredicateBuiltin).
var classPredicateNames = map[string]struct{}{
	"is_a?":        {},
	"kind_of?":     {},
	"instance_of?": {},
}

// checkClassPredicateArgument reports a class-predicate call whose argument is
// provably not a class or module value — every known arm describes a plain
// runtime value, which the runtime predicate always rejects. Unknown arms stay
// silent: a bare class reference carries no inferred fact, so real class
// arguments never reach the diagnostic.
func (c *scriptChecker) checkClassPredicateArgument(function string, call *CallExpr, name string) {
	if len(call.Args) != 1 || callExpandsArguments(call) {
		return
	}
	arg := call.Args[0]
	inferred, captured := c.callArgumentFacts[arg]
	if !captured {
		inferred = c.inferExpressionType(arg)
	}
	if inferred == nil {
		return
	}
	arms, ok := typeExprArms(inferred, 0)
	if !ok || len(arms) == 0 {
		return
	}
	for _, arm := range arms {
		if !typeArmProvablyNotClass(arm) {
			return
		}
	}
	c.add(function, arg.Pos(), "call to %s expects a class argument, got %s", name, formatTypeExpr(inferred))
}

// typeArmProvablyNotClass reports whether a known type arm can never describe
// a class or module value. Only the explicitly listed value kinds qualify:
// unknown markers (including first-class shape values) and any future kinds
// stay gradual.
func typeArmProvablyNotClass(arm *TypeExpr) bool {
	switch arm.Kind {
	case TypeInt, TypeFloat, TypeNumber, TypeString, TypeBool, TypeNil,
		TypeDuration, TypeTime, TypeMoney, TypeArray, TypeHash, TypeRange,
		TypeSymbol, TypeFunction, TypeShape, TypeEnum:
		return true
	}
	return false
}

// callExpandsArguments reports whether a call carries a positional or
// keyword splat, so its final argument shape is only known at runtime.
func callExpandsArguments(call *CallExpr) bool {
	for _, kw := range call.KwArgs {
		if kw.Splat {
			return true
		}
	}
	return callHasSplatArg(call)
}

// callArgumentExpansionMaySucceed proves expansion failures from literals and
// evaluation-point type facts. Unknown or compatible union arms remain
// possible so their dispatch effects stay conservative.
func (c *scriptChecker) callArgumentExpansionMaySucceed(call *CallExpr) bool {
	if call == nil {
		return false
	}
	for _, arg := range call.Args {
		if !c.positionalArgumentExpansionMaySucceed(arg) {
			return false
		}
	}
	for _, kwarg := range call.KwArgs {
		if !c.keywordArgumentExpansionMaySucceed(kwarg) {
			return false
		}
	}
	return true
}

func (c *scriptChecker) positionalArgumentExpansionMaySucceed(arg Expression) bool {
	splat, ok := arg.(*SplatArg)
	if !ok {
		return true
	}
	return c.expressionMayHaveExpansionType(splat.Value, KindArray, checkTypeArray)
}

func (c *scriptChecker) keywordArgumentExpansionMaySucceed(kwarg KeywordArg) bool {
	if !kwarg.Splat {
		return true
	}
	if c.keywordSplatExpressionAlwaysFails(kwarg.Value) {
		return false
	}
	return c.expressionMayHaveExpansionType(kwarg.Value, KindHash, checkTypeHash)
}

func (c *scriptChecker) blockArgumentConversionMaySucceed(expr Expression, inferred *TypeExpr) bool {
	if expr == nil {
		return true
	}
	if classNames, exact := c.classValueExpressionNames(expr); exact && len(classNames) > 0 {
		return false
	}
	if value, literal := staticLiteralValue(expr); literal {
		switch value.Kind() {
		case KindNil, KindSymbol, KindFunction, KindBuiltin, KindBlock:
			return true
		default:
			return false
		}
	}
	if inferred == nil {
		return true
	}
	allowed := unionTypeExprs(checkTypeNil, checkTypeSymbol, checkTypeFunction)
	return !typeExprsDisjoint(inferred, allowed, c.checkNamedTypeResolver())
}

func (c *scriptChecker) expressionMayHaveExpansionType(
	expr Expression,
	kind ValueKind,
	typeExpr *TypeExpr,
) bool {
	if classNames, exact := c.classValueExpressionNames(expr); exact && len(classNames) > 0 {
		return false
	}
	if value, literal := staticLiteralValue(expr); literal {
		return value.Kind() == kind
	}
	inferred := c.inferExpressionType(expr)
	return inferred == nil || !typeExprsDisjoint(inferred, typeExpr, c.checkNamedTypeResolver())
}

func (c *scriptChecker) caseWhenSplatExpansionMaySucceed(expr Expression, splat bool) bool {
	return !splat || c.expressionMayHaveExpansionType(expr, KindArray, checkTypeArray)
}

// staticallyExpandedCall rewrites literal array/hash splats into the argument
// shape the callee receives. The original expressions are retained so type and
// effect facts still refer to their evaluation-point AST nodes.
func (c *scriptChecker) staticallyExpandedCall(call *CallExpr) (*CallExpr, bool) {
	if call == nil {
		return nil, false
	}
	if !callExpandsArguments(call) {
		return call, true
	}
	expanded := *call
	expanded.Args = make([]Expression, 0, len(call.Args))
	for _, arg := range call.Args {
		splat, ok := arg.(*SplatArg)
		if !ok {
			expanded.Args = append(expanded.Args, arg)
			continue
		}
		array, ok := splat.Value.(*ArrayLiteral)
		if !ok {
			values, exact := c.callStaticValueAlternatives(splat.Value)
			if !exact || len(values) != 1 {
				return call, false
			}
			array, ok = values[0].(*ArrayLiteral)
			if !ok {
				return call, false
			}
		}
		expanded.Args = append(expanded.Args, array.Elements...)
	}
	expanded.KwArgs = make([]KeywordArg, 0, len(call.KwArgs))
	for _, kwarg := range call.KwArgs {
		if !kwarg.Splat {
			expanded.KwArgs = append(expanded.KwArgs, kwarg)
			continue
		}
		hash, ok := kwarg.Value.(*HashLiteral)
		if !ok {
			values, exact := c.callStaticValueAlternatives(kwarg.Value)
			if !exact || len(values) != 1 {
				return call, false
			}
			hash, ok = values[0].(*HashLiteral)
		}
		if !ok || hash.ShapeType != nil && !c.hashShapeStaticallyShadowed(hash) {
			return call, false
		}
		for _, pair := range hash.Pairs {
			key, literal := staticLiteralValue(pair.Key)
			if !literal || (key.Kind() != KindString && key.Kind() != KindSymbol) {
				return call, false
			}
			expanded.KwArgs = append(expanded.KwArgs, KeywordArg{
				Name:  key.String(),
				Value: pair.Value,
			})
		}
	}
	return &expanded, true
}

func dynamicCallableTargetMayEnter(call *CallExpr, target staticCallable) bool {
	if target.fn != nil {
		return scriptCallBodyMayEnter(call, target)
	}
	if !target.constructor || call == nil || callExpandsArguments(call) {
		return false
	}
	return len(call.Args) == 0 && len(call.KwArgs) == 0
}

// scriptCallBodyMayEnter mirrors function argument binding without checking
// types. A shape failure raises before defaults or the function body execute.
func scriptCallBodyMayEnter(call *CallExpr, target staticCallable) bool {
	if call == nil || target.fn == nil {
		return false
	}
	if callExpandsArguments(call) {
		return true
	}
	view := staticCallViewFor(call, target)
	usedKeywords := make(map[string]struct{}, len(view.kwargs))
	argIndex := 0
	for _, param := range target.fn.Params {
		switch param.Kind {
		case ParamKeyword:
			if keywordIndex(view, param.Name) >= 0 {
				usedKeywords[param.Name] = struct{}{}
			} else if param.DefaultVal == nil {
				return false
			}
		case ParamRest:
			argIndex = len(view.args)
		case ParamKeywordRest:
			for _, kwarg := range view.kwargs {
				usedKeywords[kwarg.Name] = struct{}{}
			}
		case ParamBlock:
		case ParamNormal:
			if argIndex < len(view.args) {
				argIndex++
			} else if keywordIndex(view, param.Name) >= 0 {
				usedKeywords[param.Name] = struct{}{}
			} else if param.DefaultVal == nil {
				return false
			}
		}
	}
	if argIndex < len(view.args) {
		return false
	}
	for _, kwarg := range view.kwargs {
		if _, used := usedKeywords[kwarg.Name]; !used {
			return false
		}
	}
	return true
}

// scriptCallBindingPlan mirrors the runtime's post-evaluation binding pass.
// The runtime validates the complete shape before evaluating any defaults,
// then binds parameters in declaration order and stops at the first
// deterministic normalization failure. The plan therefore distinguishes the
// default prefix that can run from entry into the function body itself.
func (c *scriptChecker) scriptCallBindingPlan(call *CallExpr, target staticCallable) scriptCallBindingPlan {
	if call == nil || target.fn == nil {
		return scriptCallBindingPlan{}
	}
	if callExpandsArguments(call) {
		defaults := make([]int, 0, len(target.fn.Params))
		for i, param := range target.fn.Params {
			if param.DefaultVal != nil {
				defaults = append(defaults, i)
			}
		}
		return scriptCallBindingPlan{defaultParams: defaults, bodyMayEnter: true}
	}
	if !scriptCallBodyMayEnter(call, target) {
		return scriptCallBindingPlan{}
	}

	view := staticCallViewFor(call, target)
	usedKeywords := make(map[string]struct{}, len(view.kwargs))
	argIndex := 0
	plan := scriptCallBindingPlan{bodyMayEnter: true}
	for i, param := range target.fn.Params {
		switch param.Kind {
		case ParamNormal:
			var value Expression
			if argIndex < len(view.args) {
				value = view.args[argIndex]
				argIndex++
			} else if kwIndex := keywordIndex(view, param.Name); kwIndex >= 0 {
				value = view.kwargs[kwIndex].Value
				usedKeywords[param.Name] = struct{}{}
			} else {
				plan.defaultParams = append(plan.defaultParams, i)
				value = param.DefaultVal
				if !c.defaultExpressionMayBindType(value, param.Type) ||
					!c.expressionMayCompleteForBinding(value) {
					plan.bodyMayEnter = false
					return plan
				}
				continue
			}
			if !c.callArgumentMayBindType(value, param.Type) {
				plan.bodyMayEnter = false
				return plan
			}
		case ParamKeyword:
			if kwIndex := keywordIndex(view, param.Name); kwIndex >= 0 {
				usedKeywords[param.Name] = struct{}{}
				if !c.callArgumentMayBindType(view.kwargs[kwIndex].Value, param.Type) {
					plan.bodyMayEnter = false
					return plan
				}
				continue
			}
			plan.defaultParams = append(plan.defaultParams, i)
			if !c.defaultExpressionMayBindType(param.DefaultVal, param.Type) ||
				!c.expressionMayCompleteForBinding(param.DefaultVal) {
				plan.bodyMayEnter = false
				return plan
			}
		case ParamRest:
			if !c.callRestArgumentsMayBindType(view.args[argIndex:], param.Type) {
				plan.bodyMayEnter = false
				return plan
			}
			argIndex = len(view.args)
		case ParamKeywordRest:
			if !c.callKeywordRestArgumentsMayBindType(view.kwargs, usedKeywords, param.Type) {
				plan.bodyMayEnter = false
				return plan
			}
			for _, kwarg := range view.kwargs {
				usedKeywords[kwarg.Name] = struct{}{}
			}
		case ParamBlock:
			if !c.callBlockMayBindType(call, param.Type) {
				plan.bodyMayEnter = false
				return plan
			}
		}
	}
	return plan
}

func (c *scriptChecker) callArgumentMayBindType(expr Expression, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	if value, literal := staticLiteralValue(expr); literal {
		return c.checkRuntimeStaticValueType(value, ty) == nil
	}
	inferred, captured := c.callArgumentFacts[expr]
	if !captured {
		inferred = c.inferExpressionTypeWithExpectation(expr, typeExpressionExpectation(ty))
	}
	return inferred == nil || !typeExprsDisjoint(inferred, ty, c.checkNamedTypeResolver())
}

func (c *scriptChecker) defaultExpressionMayBindType(expr Expression, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	if value, literal := staticLiteralValue(expr); literal {
		return c.checkRuntimeStaticValueType(value, ty) == nil
	}
	inferred := c.inferExpressionTypeWithExpectation(expr, typeExpressionExpectation(ty))
	return inferred == nil || !typeExprsDisjoint(inferred, ty, c.checkNamedTypeResolver())
}

// expressionMayCompleteForBinding is a side-effect-free completion probe for
// parameter defaults. It is deliberately conservative outside statically
// resolved calls: false is used to stop later defaults and the function body,
// so an unknown expression must remain potentially completing.
func (c *scriptChecker) expressionMayCompleteForBinding(expr Expression) bool {
	if expr == nil {
		return true
	}
	if c.bindingCompletionProbes == nil {
		c.bindingCompletionProbes = make(map[Expression]struct{})
	}
	if _, busy := c.bindingCompletionProbes[expr]; busy {
		return true
	}
	c.bindingCompletionProbes[expr] = struct{}{}
	defer delete(c.bindingCompletionProbes, expr)

	switch typed := expr.(type) {
	case *CallExpr:
		if member, ok := typed.Callee.(*MemberExpr); ok {
			if !c.expressionMayCompleteForBinding(member.Object) {
				return false
			}
			if member.Safe && !c.safeNavigationReceiverKnownNonNil(member.Object) {
				return true
			}
			if member.Property == "new" {
				classes, exact := c.constructorInstanceClassNames(member.Object, "")
				if exact && len(classes) == 0 {
					return false
				}
			}
		} else if nested, ok := typed.Callee.(*CallExpr); ok &&
			!c.expressionMayCompleteForBinding(nested) {
			return false
		}
		for _, arg := range typed.Args {
			if !c.expressionMayCompleteForBinding(arg) ||
				!c.positionalArgumentExpansionMaySucceed(arg) {
				return false
			}
		}
		for _, kwarg := range typed.KwArgs {
			if !c.expressionMayCompleteForBinding(kwarg.Value) ||
				!c.keywordArgumentExpansionMaySucceed(kwarg) {
				return false
			}
		}
		if !c.expressionMayCompleteForBinding(typed.BlockArg) ||
			!c.blockArgumentConversionMaySucceed(typed.BlockArg, c.inferExpressionType(typed.BlockArg)) {
			return false
		}
		target, resolved := c.resolveCallable(typed)
		if !resolved {
			candidates := c.captureDynamicCallCandidates(typed)
			resolution := c.exactDynamicCallTargets(typed, target, false, candidates)
			if resolution.lookupFails {
				return false
			}
			if resolution.exact {
				c.refineDynamicCallTargetEntry(resolution.targets)
				return resolution.nonScriptMayComplete ||
					c.dynamicScriptCallTargetsMayComplete(resolution.targets)
			}
			return true
		}
		checkedCall := typed
		if expanded, exact := c.staticallyExpandedCall(typed); exact {
			checkedCall = expanded
		}
		if target.fn != nil {
			plan := c.scriptCallBindingPlan(checkedCall, target)
			return plan.bodyMayEnter &&
				c.scriptFunctionCallMayComplete(checkedCall, target)
		}
		view := staticCallViewFor(checkedCall, target)
		return c.builtinCallMayEnter(view, target.spec) &&
			c.specialBuiltinCallMayComplete(checkedCall, target.name) &&
			c.builtinCallMayComplete(target.spec)
	case *Identifier:
		if fns, exact := c.localCallableValuesFor(typed.Name); exact {
			for _, fn := range fns {
				if len(fn.Params) > 0 || c.scriptFunctionCallMayComplete(nil, staticCallable{fn: fn}) {
					return true
				}
			}
			return false
		}
		if c.identifierShadowed(typed.Name) || c.hostGlobalShadows(typed.Name) {
			return true
		}
		if fn := c.script.functions[typed.Name]; fn != nil {
			return len(fn.Params) > 0 || c.scriptFunctionCallMayComplete(nil, staticCallable{fn: fn})
		}
		if fn, ok := c.typeRootFunction(typed.Name); ok {
			return len(fn.Params) > 0 || c.scriptFunctionCallMayComplete(nil, staticCallable{fn: fn})
		}
		if c.typeRootHasBinding(typed.Name) || c.hostBuiltinOverrides(typed.Name) {
			return true
		}
		if spec, ok := c.defaultBuiltinCallSpec(typed.Name); ok {
			if !spec.autoInvoke {
				return true
			}
			view := staticCallView{pos: typed.Pos()}
			return c.builtinCallMayEnter(view, spec) && c.builtinCallMayComplete(spec)
		}
		if call, target, ok := c.implicitSelfAutoCall(typed); ok {
			if target.fn == nil {
				return true
			}
			plan := c.scriptCallBindingPlan(call, target)
			return plan.bodyMayEnter && c.scriptFunctionCallMayComplete(call, target)
		}
		if c.implicitSelfConstructorLookupFails(&CallExpr{Callee: typed, Position: typed.Pos()}) {
			return false
		}
		return true
	case *MemberExpr:
		if !c.expressionMayCompleteForBinding(typed.Object) {
			return false
		}
		if typed.Safe && !c.safeNavigationReceiverKnownNonNil(typed.Object) {
			return true
		}
		if typed.Property == "new" {
			classes, exact := c.constructorInstanceClassNames(typed.Object, "")
			if exact && len(classes) == 0 {
				return false
			}
		}
		target, resolved := c.resolveMemberCallable(typed)
		if !resolved {
			return true
		}
		if target.fn != nil {
			autoInvokes := target.resolution != calleeMemberValue || target.constructor || len(target.fn.Params) == 0
			if !autoInvokes {
				return true
			}
			call := &CallExpr{Callee: typed, Position: typed.Pos()}
			plan := c.scriptCallBindingPlan(call, target)
			return plan.bodyMayEnter &&
				c.scriptFunctionCallMayComplete(call, target)
		}
		if !target.spec.autoInvoke {
			return true
		}
		view := staticCallView{pos: typed.Pos()}
		return c.builtinCallMayEnter(view, target.spec) && c.builtinCallMayComplete(target.spec)
	case *IndexExpr:
		if !c.expressionMayCompleteForBinding(typed.Object) {
			return false
		}
		for _, index := range typed.Indices {
			if !c.expressionMayCompleteForBinding(index) {
				return false
			}
		}
		return c.indexExpressionMayComplete(typed)
	case *ArrayLiteral:
		for _, element := range typed.Elements {
			if !c.expressionMayCompleteForBinding(element) {
				return false
			}
		}
	case *HashLiteral:
		if typed.ShapeType != nil && !c.hashShapeStaticallyShadowed(typed) {
			return true
		}
		for _, pair := range typed.Pairs {
			if !c.expressionMayCompleteForBinding(pair.Key) ||
				!c.expressionMayCompleteForBinding(pair.Value) {
				return false
			}
		}
	case *TypeLiteral:
		return !c.typeLiteralStaticallyShadowed(typed) ||
			c.expressionMayCompleteForBinding(typed.Fallback)
	case *SplatArg:
		return c.expressionMayCompleteForBinding(typed.Value)
	case *UnaryExpr:
		return c.expressionMayCompleteForBinding(typed.Right) &&
			c.unaryExpressionMayComplete(typed)
	case *BinaryExpr:
		if !c.expressionMayCompleteForBinding(typed.Left) {
			return false
		}
		if typed.Operator != tokenAnd && typed.Operator != tokenOr {
			return c.expressionMayCompleteForBinding(typed.Right) &&
				c.binaryExpressionMayComplete(typed)
		}
		truthy, known := c.inferredConditionTruthiness(typed.Left)
		if !known || truthy != (typed.Operator == tokenAnd) {
			return true
		}
		return c.expressionMayCompleteForBinding(typed.Right)
	case *ConditionalExpr:
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		if branch, known := staticConditionalExpressionBranch(typed); known {
			return c.expressionMayCompleteForBinding(branch)
		}
		return c.expressionMayCompleteForBinding(typed.Consequent) ||
			c.expressionMayCompleteForBinding(typed.Alternate)
	case *IfExpr:
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if (!known || truthy) && c.expressionMayCompleteForBinding(typed.Consequent) {
			return true
		}
		if known && truthy {
			return false
		}
		for _, branch := range typed.ElseIf {
			if !c.expressionMayCompleteForBinding(branch.Condition) {
				return false
			}
			branchTruthy, branchKnown := c.inferredConditionTruthiness(branch.Condition)
			if (!branchKnown || branchTruthy) && c.expressionMayCompleteForBinding(branch.Result) {
				return true
			}
			if branchKnown && branchTruthy {
				return false
			}
		}
		return c.expressionMayCompleteForBinding(typed.Alternate)
	case *CaseExpr:
		if !c.expressionMayCompleteForBinding(typed.Target) {
			return false
		}
		if result, known := c.inferredCaseExpressionResult(typed); known {
			return c.expressionMayCompleteForBinding(result)
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if !c.expressionMayCompleteForBinding(value.Expr) ||
					!c.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
					return false
				}
				if c.expressionMayCompleteForBinding(clause.Result) {
					return true
				}
			}
		}
		return c.expressionMayCompleteForBinding(typed.ElseExpr)
	case *RescueExpr:
		bodyCompletes := c.expressionMayCompleteForBinding(typed.Body)
		if errorKind, exact := c.staticallyRaisedExpressionErrorKind(typed.Body); exact &&
			!staticErrorKindMatchesRescue(errorKind, nil) {
			return bodyCompletes
		}
		return bodyCompletes || c.expressionMayCompleteForBinding(typed.Fallback)
	case *RangeExpr:
		return c.expressionMayCompleteForBinding(typed.Start) &&
			c.expressionMayCompleteForBinding(typed.End)
	case *InterpolatedString:
		for _, part := range typed.Parts {
			if expression, ok := part.(StringExpr); ok &&
				!c.expressionMayCompleteForBinding(expression.Expr) {
				return false
			}
		}
	case *InterpolatedSymbol:
		for _, part := range typed.Parts {
			if expression, ok := part.(StringExpr); ok &&
				!c.expressionMayCompleteForBinding(expression.Expr) {
				return false
			}
		}
	}
	return true
}

func (c *scriptChecker) callRestArgumentsMayBindType(args []Expression, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	values := make([]Value, 0, len(args))
	allStatic := true
	for _, arg := range args {
		value, literal := staticLiteralValue(arg)
		if !literal {
			allStatic = false
			break
		}
		values = append(values, value)
	}
	if allStatic {
		return c.checkRuntimeStaticValueType(NewArray(values), ty) == nil
	}
	if typeExprsDisjoint(checkTypeArray, ty, c.checkNamedTypeResolver()) {
		return false
	}
	return c.restArgumentsMayBindTypeArm(args, ty)
}

func (c *scriptChecker) restArgumentsMayBindTypeArm(args []Expression, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	switch ty.Kind {
	case TypeAny, TypeUnknown:
		return true
	case TypeArray:
		if len(ty.TypeArgs) != 1 {
			return true
		}
		for _, arg := range args {
			if !c.callArgumentMayBindType(arg, ty.TypeArgs[0]) {
				return false
			}
		}
		return true
	case TypeUnion:
		for _, arm := range ty.Union {
			if c.restArgumentsMayBindTypeArm(args, arm) {
				return true
			}
		}
	}
	return false
}

func (c *scriptChecker) callKeywordRestArgumentsMayBindType(
	kwargs []KeywordArg,
	used map[string]struct{},
	ty *TypeExpr,
) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	last := make(map[string]Expression, len(kwargs))
	for _, kwarg := range kwargs {
		if _, consumed := used[kwarg.Name]; !consumed {
			last[kwarg.Name] = kwarg.Value
		}
	}
	values := make(map[string]Value, len(last))
	allStatic := true
	for name, expr := range last {
		value, literal := staticLiteralValue(expr)
		if !literal {
			allStatic = false
			break
		}
		values[name] = value
	}
	if allStatic {
		return c.checkRuntimeStaticValueType(NewHash(values), ty) == nil
	}
	return c.keywordRestArgumentsMayBindTypeArm(last, ty)
}

func (c *scriptChecker) keywordRestArgumentsMayBindTypeArm(
	values map[string]Expression,
	ty *TypeExpr,
) bool {
	if ty == nil {
		return true
	}
	switch ty.Kind {
	case TypeAny, TypeUnknown:
		return true
	case TypeHash:
		if len(ty.TypeArgs) != 2 {
			return true
		}
		if typeExprsDisjoint(checkTypeString, ty.TypeArgs[0], c.checkNamedTypeResolver()) {
			return false
		}
		for _, expr := range values {
			if !c.callArgumentMayBindType(expr, ty.TypeArgs[1]) {
				return false
			}
		}
		return true
	case TypeShape:
		for name, fieldType := range ty.Shape {
			if _, supplied := values[name]; !supplied && !shapeFieldOptional(fieldType) {
				return false
			}
		}
		for name, expr := range values {
			fieldType, known := ty.Shape[name]
			if !known {
				if !ty.Open {
					return false
				}
				continue
			}
			if !c.callArgumentMayBindType(expr, fieldType) {
				return false
			}
		}
		return true
	case TypeUnion:
		for _, arm := range ty.Union {
			if c.keywordRestArgumentsMayBindTypeArm(values, arm) {
				return true
			}
		}
	}
	return false
}

func (c *scriptChecker) callBlockMayBindType(call *CallExpr, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	boundType := checkTypeNil
	if call.Block != nil {
		boundType = checkTypeFunction
	} else if call.BlockArg != nil {
		inferred, captured := c.callArgumentFacts[call.BlockArg]
		if !captured {
			inferred = c.inferExpressionTypeWithExpectation(call.BlockArg, typeExpressionExpectation(checkTypeFunction))
		}
		if inferred == nil {
			return true
		}
		if typeExprIsNilOnly(inferred) {
			boundType = checkTypeNil
		} else if typeExprNeverNil(inferred) {
			boundType = checkTypeFunction
		} else {
			boundType = unionTypeExprs(checkTypeFunction, checkTypeNil)
		}
	}
	return !typeExprsDisjoint(boundType, ty, c.checkNamedTypeResolver())
}

func (c *scriptChecker) forwardedCallVariants(call *CallExpr) ([]checkForwardedCallVariant, bool) {
	if call == nil {
		return []checkForwardedCallVariant{{known: true}}, true
	}
	const maxVariants = 32
	makeCall := func(args []Expression) *CallExpr {
		forwarded := *call
		forwarded.Args = append([]Expression(nil), args...)
		forwarded.Parenthesized = true
		forwarded.KeywordOptionsHash = len(call.KwArgs) > 0
		return &forwarded
	}
	classify := func(methodExpr Expression, args []Expression) ([]checkForwardedCallVariant, bool) {
		if values, exact := c.callStaticValueAlternatives(methodExpr); exact {
			variants := make([]checkForwardedCallVariant, 0, len(values))
			for _, expression := range values {
				value, _ := staticLiteralValue(expression)
				method, valid := methodNameArg(value)
				variants = append(variants, checkForwardedCallVariant{
					call:   makeCall(args),
					method: method,
					known:  true,
					valid:  valid,
				})
			}
			return variants, true
		}
		inferred, captured := c.callArgumentFacts[methodExpr]
		if !captured {
			inferred = c.inferExpressionType(methodExpr)
		}
		if inferred != nil && typeExprsDisjoint(inferred, checkTypeMethodName, c.checkNamedTypeResolver()) {
			return []checkForwardedCallVariant{{call: makeCall(args), known: true}}, true
		}
		return []checkForwardedCallVariant{{call: makeCall(args), valid: true}}, false
	}

	var walk func(int) ([]checkForwardedCallVariant, bool)
	walk = func(index int) ([]checkForwardedCallVariant, bool) {
		if index >= len(call.Args) {
			return []checkForwardedCallVariant{{call: makeCall(nil), known: true}}, true
		}
		arg := call.Args[index]
		splat, isSplat := arg.(*SplatArg)
		if !isSplat {
			return classify(arg, call.Args[index+1:])
		}
		values, exact := c.callStaticValueAlternatives(splat.Value)
		if !exact {
			return []checkForwardedCallVariant{{call: makeCall(call.Args[index:]), valid: true}}, false
		}
		variants := make([]checkForwardedCallVariant, 0, len(values))
		allExact := true
		for _, expression := range values {
			value, _ := staticLiteralValue(expression)
			if value.Kind() != KindArray {
				variants = append(variants, checkForwardedCallVariant{call: makeCall(nil), known: true})
				if len(variants) > maxVariants {
					return []checkForwardedCallVariant{{call: makeCall(call.Args[index:]), valid: true}}, false
				}
				continue
			}
			array, ok := expression.(*ArrayLiteral)
			if !ok {
				allExact = false
				continue
			}
			if len(array.Elements) == 0 {
				next, nextExact := walk(index + 1)
				variants = append(variants, next...)
				allExact = allExact && nextExact
				if len(variants) > maxVariants {
					return []checkForwardedCallVariant{{call: makeCall(call.Args[index:]), valid: true}}, false
				}
				continue
			}
			args := append(append([]Expression(nil), array.Elements[1:]...), call.Args[index+1:]...)
			resolved, resolvedExact := classify(array.Elements[0], args)
			variants = append(variants, resolved...)
			allExact = allExact && resolvedExact
			if len(variants) > maxVariants {
				return []checkForwardedCallVariant{{call: makeCall(call.Args[index:]), valid: true}}, false
			}
		}
		return variants, allExact
	}
	return walk(0)
}

// forwardedCallMethodName resolves the first positional value after static
// splat expansion. A dynamic splat is distinguished from an ordinary dynamic
// name because it may expand to no method name at all.
func forwardedCallMethodName(call *CallExpr) (name string, known, valid, dynamicSplat bool) {
	if call == nil {
		return "", true, false, false
	}
	for _, arg := range call.Args {
		if splat, ok := arg.(*SplatArg); ok {
			if array, ok := splat.Value.(*ArrayLiteral); ok {
				if len(array.Elements) == 0 {
					continue
				}
				value, literal := staticLiteralValue(array.Elements[0])
				if !literal {
					return "", false, true, false
				}
				name, valid := methodNameArg(value)
				return name, true, valid, false
			}
			value, literal := staticLiteralValue(splat.Value)
			if !literal {
				return "", false, true, true
			}
			if value.Kind() != KindArray {
				return "", true, false, false
			}
			elements := value.Array()
			if len(elements) == 0 {
				continue
			}
			name, valid := methodNameArg(elements[0])
			return name, true, valid, false
		}
		value, literal := staticLiteralValue(arg)
		if !literal {
			return "", false, true, false
		}
		name, valid := methodNameArg(value)
		return name, true, valid, false
	}
	return "", true, false, false
}

func (c *scriptChecker) resolveCallable(call *CallExpr) (staticCallable, bool) {
	switch callee := call.Callee.(type) {
	case *Identifier:
		if c.identifierShadowed(callee.Name) {
			return staticCallable{}, false
		}
		if c.hostGlobalShadows(callee.Name) && c.optionGlobalsOverride {
			// Call-option globals shadow same-named script bindings only in
			// the checked script itself; a required module's own functions
			// win at runtime over the parent call's globals.
			return c.hostGlobalCallable(callee.Name)
		}
		if fn, ok := c.script.functions[callee.Name]; ok {
			return staticCallable{name: callee.Name, fn: fn, resolution: calleeDirect}, true
		}
		if fn, ok := c.typeRootFunction(callee.Name); ok {
			return staticCallable{name: callee.Name, fn: fn, resolution: calleeDirect}, true
		}
		if c.optionGlobalSeeded(callee.Name) {
			return c.hostGlobalCallable(callee.Name)
		}
		if c.typeRootHasBinding(callee.Name) {
			return staticCallable{}, false
		}
		if c.hostBuiltinOverrides(callee.Name) {
			// A host override that publishes a signature keeps a static
			// contract; one registered without a signature stays dynamic.
			if spec, ok := c.defaultBuiltinCallSpec(callee.Name); ok {
				return staticCallable{name: callee.Name, spec: spec}, true
			}
			return staticCallable{}, false
		}
		if spec, ok := c.defaultBuiltinCallSpec(callee.Name); ok {
			return staticCallable{name: callee.Name, spec: spec}, true
		}
	case *MemberExpr:
		if target, ok := c.resolveMemberCallable(callee); ok {
			return target, true
		}
	}
	if c.returnCollector != nil {
		return c.implicitSelfCallSummaryTarget(call.Callee)
	}
	return staticCallable{}, false
}

// implicitSelfConstructorReceiver reports whether expr is a bare new or
// explicit self.new expression in a class-self mutation scan.
func implicitSelfConstructorReceiver(expr Expression) bool {
	switch typed := expr.(type) {
	case *Identifier:
		return typed.Name == "new"
	case *CallExpr:
		return implicitSelfConstructorReceiver(typed.Callee)
	case *MemberExpr:
		ident, isIdentifier := typed.Object.(*Identifier)
		return isIdentifier && ident.Name == "self" && typed.Property == "new"
	}
	return false
}

// implicitSelfConstructorLookupFails recognizes a class-self new lookup that
// cannot resolve to either a constructor or a module-provided member. Dynamic
// module state keeps the lookup gradual.
func (c *scriptChecker) implicitSelfConstructorLookupFails(call *CallExpr) bool {
	if call == nil || c.selfClass == nil || !c.selfClassContext ||
		!implicitSelfConstructorReceiver(call.Callee) || c.callMayDispatchDynamicValue(call) {
		return false
	}
	if _, ok := c.implicitSelfSummaryCallable("new"); ok {
		return false
	}
	classes, exact := c.constructorInstanceClassNamesForClasses(
		[]string{c.selfClass.Name},
		true,
		"",
	)
	return exact && len(classes) == 0
}

// callMayDispatchDynamicValue distinguishes unresolved first-class dispatch
// from fixed builtin dispatch that simply has no checker contract. An
// unshadowed identifier or fixed receiver selects fixed runtime dispatch (or
// fails before dispatch), while a bound local, host override, or dynamic
// receiver can select an arbitrary callee.
func (c *scriptChecker) callMayDispatchDynamicValue(call *CallExpr) bool {
	switch callee := call.Callee.(type) {
	case *Identifier:
		return c.identifierShadowed(callee.Name) ||
			c.hostGlobalShadows(callee.Name) ||
			c.typeRootHasBinding(callee.Name) ||
			c.hostBuiltinOverrides(callee.Name)
	case *MemberExpr:
		if callee.Property == "call" {
			return true
		}
		_, fixedReceiver := c.staticMemberReceiverKinds(callee)
		return !fixedReceiver
	default:
		return true
	}
}

func (c *scriptChecker) typeRootFunction(name string) (*ScriptFunction, bool) {
	if fn, ok := checkRootFunction(c.runtimeTypeRoot, name); ok {
		return fn, true
	}
	return checkRootFunction(c.typeRoot, name)
}

func checkRootFunction(root *Env, name string) (*ScriptFunction, bool) {
	// The raw chain read matters: resolving through Env.Get caches
	// call-clones of frozen builtin bindings into the mutable type root,
	// which would make later own-binding checks see names the script never
	// bound.
	val, ok := checkRootBinding(root, name)
	if !ok || val.Kind() != KindFunction {
		return nil, false
	}
	fn := valueFunction(val)
	return fn, fn != nil
}

func (c *scriptChecker) typeRootFunctionValue(name string) (*ScriptFunction, bool) {
	fn, ok := c.typeRootFunction(name)
	if !ok || len(fn.Params) == 0 {
		return nil, false
	}
	return fn, true
}

func (c *scriptChecker) typeRootHasBinding(name string) bool {
	for _, root := range []*Env{c.runtimeTypeRoot, c.typeRoot} {
		if root != nil && root.hasOwnBinding(name) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) hostBuiltinOverrides(name string) bool {
	if c.script == nil || c.script.engine == nil {
		return false
	}
	return c.script.engine.hasHostBuiltin(name)
}

func (c *scriptChecker) hostGlobalShadows(name string) bool {
	_, ok := c.hostGlobals[name]
	return ok
}

// optionGlobalSeeded reports whether name resolves to the seeded call-option
// global in this checking context: the global exists and the script's own
// static bindings (functions, classes, enums) do not claim the name, which is
// exactly when checkTypeRootWithParentAndGlobals defines the global on the
// root. Required modules keep their own bindings ahead of parent globals.
func (c *scriptChecker) optionGlobalSeeded(name string) bool {
	if !c.hostGlobalShadows(name) {
		return false
	}
	if c.script == nil {
		return true
	}
	if _, ok := c.script.functions[name]; ok {
		return false
	}
	if _, ok := c.script.classes[name]; ok {
		return false
	}
	if _, ok := c.script.enums[name]; ok {
		return false
	}
	return true
}

// hostGlobalCallable resolves a call-option global's published contract. A
// host global without a signature stays fully dynamic.
func (c *scriptChecker) hostGlobalCallable(name string) (staticCallable, bool) {
	val, ok := c.optionGlobals[name]
	if !ok {
		return staticCallable{}, false
	}
	if spec, ok := builtinValueCallSpec(val); ok {
		return staticCallable{name: name, spec: spec}, true
	}
	return staticCallable{}, false
}

// hostGlobalMemberCallable resolves a published contract from a host global
// namespace member — a capability method, for example. Members without a
// signature, non-namespace globals, and script-mutated members stay dynamic.
func (c *scriptChecker) hostGlobalMemberCallable(object, property string) (staticCallable, bool) {
	val, ok := c.optionGlobals[object]
	if !ok || val.Kind() != KindObject {
		return staticCallable{}, false
	}
	memberVal, ok := val.Hash()[property]
	if !ok {
		return staticCallable{}, false
	}
	if c.namespaceMemberMutated(object, property) {
		return staticCallable{}, false
	}
	if spec, ok := builtinValueCallSpec(memberVal); ok {
		return staticCallable{name: object + "." + property, spec: spec}, true
	}
	return staticCallable{}, false
}

func builtinValueCallSpec(val Value) (staticCallSpec, bool) {
	builtin := valueBuiltin(val)
	if builtin == nil || builtin.checkSpec == nil {
		return staticCallSpec{}, false
	}
	return *builtin.checkSpec, true
}

func (c *scriptChecker) resolveMemberCallable(member *MemberExpr) (staticCallable, bool) {
	// A local whose fact is a single script class resolves instance methods
	// before the identifier paths below: typed locals are scope bindings, so
	// they would otherwise bail at the shadowing guard.
	if target, ok := c.nominalReceiverMethodCallable(member); ok {
		return target, true
	}
	// A receiver whose fact pins the dispatch kind resolves member contracts
	// the same way. Named class arms resolve above, so the fact paths only
	// see plain data kinds.
	if target, ok := c.factReceiverMemberCallable(member); ok {
		return target, true
	}
	if ident, ok := member.Object.(*Identifier); ok {
		if member.Property == "call" {
			if fn, ok := c.localCallableValueFor(ident.Name); ok {
				return staticCallable{
					name:       fn.Name + ".call",
					fn:         fn,
					resolution: calleeDirect,
				}, true
			}
		}
		if classDef, ok := c.staticClassArgument(ident); ok {
			if member.Property == "new" && !classDef.IsModule {
				if initFn, exists := classDef.Methods["initialize"]; exists {
					return staticCallable{
						name:             classDef.Name + ".new",
						fn:               initFn,
						resolution:       calleeMemberValue,
						constructor:      true,
						constructorClass: classDef.Name,
					}, true
				}
				return staticCallable{
					name:             classDef.Name + ".new",
					spec:             staticCallSpec{minArgs: 0, maxArgs: 0},
					constructor:      true,
					constructorClass: classDef.Name,
				}, true
			}
			if fn, exists := classDef.ClassMethods[member.Property]; exists {
				return staticCallable{
					name:       classDef.Name + "." + member.Property,
					fn:         fn,
					resolution: calleeMemberMethod,
				}, true
			}
			if spec, exists := universalMemberSpecs[member.Property]; exists &&
				!c.classValueMemberMayChange(classDef, member.Property) {
				return staticCallable{name: member.Property, spec: spec}, true
			}
		}
		if c.identifierShadowed(ident.Name) {
			return staticCallable{}, false
		}
		if c.hostGlobalShadows(ident.Name) && c.optionGlobalsOverride {
			return c.hostGlobalMemberCallable(ident.Name, member.Property)
		}
		if member.Property == "call" {
			if fn, ok := c.typeRootFunctionValue(ident.Name); ok {
				return staticCallable{name: ident.Name + ".call", fn: fn, resolution: calleeDirect}, true
			}
		}
		if fn, ok := c.typeRootObjectFunction(ident.Name, member.Property); ok {
			return staticCallable{name: ident.Name + "." + member.Property, fn: fn, resolution: calleeMemberValue}, true
		}
		if c.optionGlobalSeeded(ident.Name) {
			return c.hostGlobalMemberCallable(ident.Name, member.Property)
		}
		if c.typeRootHasBinding(ident.Name) {
			return staticCallable{}, false
		}
		if spec, ok := c.defaultBuiltinCallSpec(ident.Name + "." + member.Property); ok {
			// A host override without a published signature stays dynamic,
			// and a script that reassigns the namespace member (e.g.
			// JSON.parse = parse) dispatches through the assigned value at
			// runtime, so the builtin contract no longer applies.
			if c.namespaceMemberMutated(ident.Name, member.Property) {
				return staticCallable{}, false
			}
			return staticCallable{name: ident.Name + "." + member.Property, spec: spec}, true
		}
		if c.hostBuiltinOverrides(ident.Name) {
			return staticCallable{}, false
		}
	}
	if className, ok := c.staticInstanceClass(member.Object); ok {
		if classDef, ok := c.script.classes[className]; ok {
			if fn, ok := classDef.Methods[member.Property]; ok {
				return staticCallable{name: className + "#" + member.Property, fn: fn, resolution: calleeMemberMethod}, true
			}
		}
	}
	if name, fn, ok := c.requiredModuleObjectFunction(member.Object, member.Property); ok {
		return staticCallable{name: name, fn: fn, resolution: calleeMemberValue}, true
	}
	return staticCallable{}, false
}

// factReceiverMemberCallable resolves a member contract from the receiver's
// literal spelling or inferred fact: scalar member contracts are keyed by the
// dispatch kind every arm shares, and universal predicates apply whenever no
// arm can override universal dispatch. Named receivers (user methods take
// precedence) and unknown facts resolve nothing. Literal hashes are safe for
// universal predicates because the runtime gives data-safe helpers precedence
// over same-named stored entries.
func (c *scriptChecker) factReceiverMemberCallable(member *MemberExpr) (staticCallable, bool) {
	kinds, ok := c.staticMemberReceiverKinds(member)
	if !ok {
		return c.factReceiverUniversalMemberCallable(member)
	}
	uniform := true
	for _, kind := range kinds[1:] {
		if kind != kinds[0] {
			uniform = false
			break
		}
	}
	if uniform {
		if spec, exists := staticMemberSpecs[kinds[0]+"."+member.Property]; exists {
			return staticCallable{name: kinds[0] + "." + member.Property, spec: spec}, true
		}
	}
	universalSpec, hasUniversal := universalMemberSpecs[member.Property]
	if !hasUniversal {
		return staticCallable{}, false
	}
	var typedSpec staticCallSpec
	typedOverrides := 0
	for _, kind := range kinds {
		spec, exists := staticMemberSpecs[kind+"."+member.Property]
		if !exists {
			continue
		}
		if typedOverrides > 0 && !reflect.DeepEqual(typedSpec, spec) {
			return staticCallable{}, false
		}
		typedSpec = spec
		typedOverrides++
	}
	if typedOverrides > 0 {
		// A universal fallback is unsound once any possible receiver owns the
		// member: mixed dispatch can have different block or argument rules.
		// Resolve only when every arm owns an equivalent typed contract.
		if typedOverrides != len(kinds) {
			return staticCallable{}, false
		}
		return staticCallable{name: member.Property, spec: typedSpec}, true
	}
	return staticCallable{name: member.Property, spec: universalSpec}, true
}

// factReceiverUniversalMemberCallable resolves a universal contract when the
// receiver fact cannot be reduced to one fixed runtime kind, but every arm is
// still proven to reach the universal implementation. This covers typed
// hash-like values whose exact value contracts rule out callable overrides.
func (c *scriptChecker) factReceiverUniversalMemberCallable(member *MemberExpr) (staticCallable, bool) {
	spec, ok := universalMemberSpecs[member.Property]
	if !ok {
		return staticCallable{}, false
	}
	arms, ok := typeExprArms(c.inferExpressionType(member.Object), 0)
	if !ok || len(arms) == 0 {
		return staticCallable{}, false
	}
	dispatchArms := 0
	for _, arm := range arms {
		if member.Safe && arm.Kind == TypeNil {
			continue
		}
		if !typeArmUsesUniversalMemberDispatch(arm, member.Property) {
			return staticCallable{}, false
		}
		dispatchArms++
	}
	if dispatchArms == 0 {
		return staticCallable{}, false
	}
	return staticCallable{name: member.Property, spec: spec}, true
}

// staticMemberReceiverKinds returns the runtime dispatch kinds of every known
// arm of a member's receiver: the literal spelling when there is one, else
// the receiver's inferred fact. Safe navigation drops nil arms — a nil
// receiver skips the dispatch entirely — and any arm without a fixed
// overridable-free dispatch kind makes the receiver unknown.
func (c *scriptChecker) staticMemberReceiverKinds(member *MemberExpr) ([]string, bool) {
	if kind, ok := staticBuiltinReceiverKind(member.Object); ok {
		return []string{kind}, true
	}
	arms, ok := typeExprArms(c.inferExpressionType(member.Object), 0)
	if !ok || len(arms) == 0 {
		return nil, false
	}
	kinds := make([]string, 0, len(arms))
	for _, arm := range arms {
		if member.Safe && arm.Kind == TypeNil {
			continue
		}
		if _, isShapeValue := shapeValuePayload(arm); isShapeValue {
			return nil, false
		}
		kind, ok := receiverKindForTypeArm(arm)
		if !ok {
			return nil, false
		}
		kinds = append(kinds, kind)
	}
	if len(kinds) == 0 {
		return nil, false
	}
	return kinds, true
}

// receiverKindForTypeArm maps a known fact arm to the member-dispatch kind it
// selects at runtime. Named types stay unknown (user-defined methods take
// precedence over builtin and universal dispatch), hash-like stores stay
// unknown (a stored callable can shadow a universal helper), and number is
// ambiguous between int and float dispatch.
func receiverKindForTypeArm(arm *TypeExpr) (string, bool) {
	switch arm.Kind {
	case TypeInt:
		return "int", true
	case TypeFloat:
		return "float", true
	case TypeString:
		return "string", true
	case TypeBool:
		return "bool", true
	case TypeSymbol:
		return "symbol", true
	case TypeNil:
		return "nil", true
	case TypeMoney:
		return "money", true
	case TypeDuration:
		return "duration", true
	case TypeTime:
		return "time", true
	case TypeRange:
		return "range", true
	case TypeArray:
		return "array", true
	case TypeFunction:
		return "function", true
	}
	return "", false
}

func (c *scriptChecker) defaultBuiltinCallSpec(name string) (staticCallSpec, bool) {
	if c.script == nil || c.script.engine == nil {
		return staticCallSpec{}, false
	}
	return c.script.engine.builtinCallSpec(name)
}

// autoInvokedIdentifierResultFact reports the result type of a bare identifier
// that auto-invokes at runtime: a script callable's annotated return or summary
// (`x = build_count`), or a builtin's invariant result (`t = uuid`). The guard
// chain mirrors resolveCallable: any shadowing binding or host override
// dispatches elsewhere, so no fact applies.
func (c *scriptChecker) autoInvokedIdentifierResultFact(name string) *TypeExpr {
	if c.identifierShadowed(name) || c.hostGlobalShadows(name) {
		return nil
	}
	if fn, ok := c.script.functions[name]; ok {
		if len(fn.Params) > 0 {
			return checkTypeFunction
		}
		if fn.ReturnTy != nil {
			return fn.ReturnTy
		}
		return c.scriptCallableReturnSummary(nil, staticCallable{
			name:       name,
			fn:         fn,
			resolution: calleeDirect,
		})
	}
	if fn, ok := c.typeRootFunction(name); ok {
		if len(fn.Params) > 0 {
			return checkTypeFunction
		}
		return nil
	}
	if c.typeRootHasBinding(name) {
		return nil
	}
	if c.hostBuiltinOverrides(name) {
		return nil
	}
	if spec, ok := c.defaultBuiltinCallSpec(name); ok {
		if !spec.autoInvoke {
			return nil
		}
		return spec.resultType
	}
	target, ok := c.implicitSelfSummaryCallable(name)
	if !ok {
		return nil
	}
	if target.constructorClass != "" {
		return &TypeExpr{Kind: TypeEnum, Name: target.constructorClass}
	}
	if target.fn.ReturnTy != nil {
		return target.fn.ReturnTy
	}
	return c.scriptCallableReturnSummary(nil, target)
}

func (c *scriptChecker) typeRootObjectFunction(name, property string) (*ScriptFunction, bool) {
	for _, root := range []*Env{c.runtimeTypeRoot, c.typeRoot} {
		val, ok := checkRootOwnBinding(root, name)
		if !ok || val.Kind() != KindObject {
			continue
		}
		member, ok := val.Hash()[property]
		if !ok || member.Kind() != KindFunction {
			continue
		}
		fn := valueFunction(member)
		if fn != nil {
			return fn, true
		}
	}
	return nil, false
}

func (c *scriptChecker) requiredModuleObjectFunction(expr Expression, property string) (string, *ScriptFunction, bool) {
	call, ok := expr.(*CallExpr)
	if !ok {
		return "", nil, false
	}
	moduleName, alias, ok := c.staticRequireCall(call)
	if !ok {
		return "", nil, false
	}
	entry, err := c.script.engine.loadModule(moduleName, c.moduleCaller, nil)
	if err != nil {
		return "", nil, false
	}
	exports := c.moduleExportValue(entry)
	if !c.canBindRequireAlias(alias, exports) {
		return "", nil, false
	}
	member, ok := exports.Hash()[property]
	if !ok || member.Kind() != KindFunction {
		return "", nil, false
	}
	fn := valueFunction(member)
	if fn == nil {
		return "", nil, false
	}
	// Speculative inference must stay effect-free: a require inside one
	// branch of a conditional would otherwise bind its exports for code the
	// branch may never run. The checking walk re-resolves and binds at the
	// expression's own evaluation point.
	if c.speculativeInference == 0 {
		c.withRuntimeModuleCollection(func() {
			c.collectModuleExports(entry)
			c.bindRequireAlias(alias, exports)
		})
	}
	return moduleName + "." + property, fn, true
}

func checkRootOwnBinding(root *Env, name string) (Value, bool) {
	if root == nil {
		return Value{}, false
	}
	if idx, ok := root.inlineIndex(name); ok {
		val := root.inline[idx].value
		if _, lazy := lazyValue(val); lazy {
			return Value{}, false
		}
		return val, true
	}
	if val, ok := root.values[name]; ok {
		if _, lazy := lazyValue(val); lazy {
			return Value{}, false
		}
		return val, true
	}
	val, ok := root.statics[name]
	return val, ok
}

func checkRootBinding(root *Env, name string) (Value, bool) {
	for scope := root; scope != nil; scope = scope.parent {
		if val, ok := checkRootOwnBinding(scope, name); ok {
			return val, true
		}
	}
	return Value{}, false
}

// nominalReceiverMethodCallable resolves an instance-method call whose
// receiver has one script-class identity. The fact may come from a local, a
// constructor expression, a branch join, or an annotated call result. A safe
// navigation receiver may additionally contain nil because dispatch skips
// that arm; other unions, modules, and unknown facts stay dynamic.
func (c *scriptChecker) nominalReceiverMethodCallable(member *MemberExpr) (staticCallable, bool) {
	var receiverFact *TypeExpr
	if ident, ok := member.Object.(*Identifier); ok {
		// Resolving a bare namespace identifier through the runtime type root
		// can materialize its lazy binding and shadow the builtin contract that
		// this same member lookup still needs. Locals already carry every
		// nominal fact an identifier receiver can contribute.
		receiverFact = c.localTypeFor(ident.Name)
	} else {
		receiverFact = c.inferExpressionType(member.Object)
	}
	arms, ok := typeExprArms(receiverFact, 0)
	if !ok || len(arms) == 0 {
		return staticCallable{}, false
	}
	className := ""
	for _, arm := range arms {
		if member.Safe && arm.Kind == TypeNil {
			continue
		}
		if arm.Kind != TypeEnum || (className != "" && className != arm.Name) {
			return staticCallable{}, false
		}
		classDef, exists := c.script.classes[arm.Name]
		if !exists || classDef.IsModule {
			return staticCallable{}, false
		}
		className = arm.Name
	}
	if className == "" || member.Property == "initialize" {
		return staticCallable{}, false
	}
	classDef := c.script.classes[className]
	fn, ok := classDef.Methods[member.Property]
	if !ok {
		return staticCallable{}, false
	}
	return staticCallable{name: className + "#" + member.Property, fn: fn, resolution: calleeMemberMethod}, true
}

func (c *scriptChecker) staticInstanceClass(expr Expression) (string, bool) {
	switch typed := expr.(type) {
	case *CallExpr:
		member, ok := typed.Callee.(*MemberExpr)
		if !ok {
			return "", false
		}
		return c.staticConstructorClass(member)
	case *MemberExpr:
		return c.staticConstructorClass(typed)
	}
	return "", false
}

func (c *scriptChecker) staticConstructorClass(member *MemberExpr) (string, bool) {
	if member.Property != "new" {
		return "", false
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return "", false
	}
	classDef, ok := c.staticClassArgument(ident)
	if !ok || classDef.IsModule {
		return "", false
	}
	return classDef.Name, true
}

func staticBuiltinReceiverKind(expr Expression) (string, bool) {
	switch expr.(type) {
	case *ArrayLiteral:
		return "array", true
	case *HashLiteral:
		return "hash", true
	case *StringLiteral, *InterpolatedString:
		return "string", true
	case *IntegerLiteral:
		return "int", true
	case *FloatLiteral:
		return "float", true
	}
	return "", false
}

func keywordSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func (c *scriptChecker) checkBuiltinCallShape(function string, call staticCallView, name string, spec staticCallSpec) {
	if len(call.args) < spec.minArgs {
		c.add(function, call.pos, "call to %s has too few arguments: got %d, want at least %d", name, len(call.args), spec.minArgs)
	}
	if spec.maxArgs >= 0 && len(call.args) > spec.maxArgs {
		c.add(function, call.pos, "call to %s has too many arguments: got %d, want at most %d", name, len(call.args), spec.maxArgs)
	}
	if spec.rejectKeywords && len(call.kwargs) > 0 {
		c.add(function, call.pos, "call to %s does not accept keyword arguments", name)
	}
	if len(spec.allowedKeywords) > 0 {
		for _, kwarg := range call.kwargs {
			if _, ok := spec.allowedKeywords[kwarg.Name]; !ok {
				c.add(function, kwarg.Value.Pos(), "call to %s has unexpected keyword argument %s", name, kwarg.Name)
			}
		}
	}
	if spec.rejectBlock && call.block != nil {
		c.add(function, call.block.Pos(), "call to %s does not accept a block", name)
	}
	if spec.rejectBlock && call.blockArg != nil {
		// A forwarded `&blk` only reaches the builtin as a block when it is
		// non-nil at runtime, so the contract violation is provable only for
		// a never-nil argument.
		if typeExprNeverNil(c.inferExpressionType(call.blockArg)) {
			c.add(function, call.blockArg.Pos(), "call to %s does not accept a block", name)
		}
	}
}

func builtinCallShapeMayEnter(call staticCallView, spec staticCallSpec) bool {
	if len(call.args) < spec.minArgs || spec.maxArgs >= 0 && len(call.args) > spec.maxArgs {
		return false
	}
	if spec.rejectKeywords && len(call.kwargs) > 0 {
		return false
	}
	if len(spec.allowedKeywords) > 0 {
		for _, kwarg := range call.kwargs {
			if _, allowed := spec.allowedKeywords[kwarg.Name]; !allowed {
				return false
			}
		}
	}
	if spec.rejectBlock && (call.block != nil || call.blockArg != nil) {
		return false
	}
	return true
}

func (c *scriptChecker) builtinCallMayEnter(call staticCallView, spec staticCallSpec) bool {
	if len(call.args) < spec.minArgs || spec.maxArgs >= 0 && len(call.args) > spec.maxArgs {
		return false
	}
	if spec.rejectKeywords && len(call.kwargs) > 0 {
		return false
	}
	if len(spec.allowedKeywords) > 0 {
		for _, kwarg := range call.kwargs {
			if _, allowed := spec.allowedKeywords[kwarg.Name]; !allowed {
				return false
			}
		}
	}
	if spec.rejectBlock {
		if call.block != nil {
			return false
		}
		if call.blockArg != nil {
			blockType, captured := c.callArgumentFacts[call.blockArg]
			if !captured {
				blockType = c.inferExpressionTypeWithExpectation(
					call.blockArg,
					typeExpressionExpectation(checkTypeFunction),
				)
			}
			if typeExprNeverNil(blockType) {
				return false
			}
		}
	}
	for i, arg := range call.args {
		if i >= len(spec.paramTypes) {
			break
		}
		if !c.callArgumentMayBindType(arg, spec.paramTypes[i]) {
			return false
		}
	}
	seenKeywords := make(map[string]struct{}, len(call.kwargs))
	for i := len(call.kwargs) - 1; i >= 0; i-- {
		kwarg := call.kwargs[i]
		if _, duplicate := seenKeywords[kwarg.Name]; duplicate {
			continue
		}
		seenKeywords[kwarg.Name] = struct{}{}
		if !c.callArgumentMayBindType(kwarg.Value, spec.keywordTypes[kwarg.Name]) {
			return false
		}
	}
	return true
}

func (c *scriptChecker) builtinCallMayComplete(spec staticCallSpec) bool {
	if !spec.fromSignature || spec.resultType == nil {
		return true
	}
	return validateTypeExprResolved(spec.resultType, c.runtimeTypeContext()) == nil
}

// specialBuiltinCallMayComplete mirrors deterministic validation that lives
// outside staticCallSpec. All call arguments have already evaluated when this
// runs; false therefore stops only the enclosing expression's continuation.
func (c *scriptChecker) specialBuiltinCallMayComplete(call *CallExpr, name string) bool {
	switch name {
	case "JSON.parse_as":
		return c.parseAsCallMayComplete(call)
	case isTypeMemberName:
		return c.isTypeCallMayComplete(call)
	default:
		if _, predicate := classPredicateNames[name]; predicate {
			return c.classPredicateCallMayComplete(call)
		}
		return true
	}
}

func (c *scriptChecker) parseAsCallMayComplete(call *CallExpr) bool {
	if call == nil || len(call.Args) != 2 || callExpandsArguments(call) {
		return true
	}
	raw := call.Args[0]
	if classNames, exact := c.classValueExpressionNames(raw); exact && len(classNames) > 0 {
		return false
	}
	rawType, captured := c.callArgumentFacts[raw]
	if !captured {
		rawType = c.inferExpressionType(raw)
	}
	if rawType != nil && typeExprsDisjoint(rawType, checkTypeString, c.checkNamedTypeResolver()) {
		return false
	}
	shapeArg := call.Args[1]
	if classNames, exact := c.classValueExpressionNames(shapeArg); exact && len(classNames) > 0 {
		return false
	}
	shapeType, captured := c.callArgumentFacts[shapeArg]
	if !captured {
		shapeType = c.inferExpressionType(shapeArg)
	}
	arms, known := typeExprArms(shapeType, 0)
	if !known || len(arms) == 0 {
		return true
	}
	for _, arm := range arms {
		if _, shape := shapeValuePayload(arm); shape {
			return true
		}
	}
	return false
}

func (c *scriptChecker) classPredicateCallMayComplete(call *CallExpr) bool {
	if call == nil || len(call.Args) != 1 || callExpandsArguments(call) {
		return true
	}
	arg := call.Args[0]
	if classNames, exact := c.classValueExpressionNames(arg); exact && len(classNames) > 0 {
		return true
	}
	inferred, captured := c.callArgumentFacts[arg]
	if !captured {
		inferred = c.inferExpressionType(arg)
	}
	arms, known := typeExprArms(inferred, 0)
	if !known || len(arms) == 0 {
		return true
	}
	for _, arm := range arms {
		if !typeArmProvablyNotClass(arm) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) isTypeCallMayComplete(call *CallExpr) bool {
	if call == nil || len(call.Args) != 1 || callExpandsArguments(call) {
		return true
	}
	values, exact := c.callStaticValueAlternatives(call.Args[0])
	if !exact {
		return true
	}
	for _, candidate := range values {
		value, static := staticLiteralValue(candidate)
		if !static || c.isTypeAtomValueMayComplete(value) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) isTypeAtomValueMayComplete(value Value) bool {
	return c.isTypeAtomValueError(value) == nil
}

func (c *scriptChecker) isTypeAtomValueError(value Value) error {
	text, validArg := typeAtomArg(value)
	if !validArg {
		return fmt.Errorf("%s expects a symbol or string type atom, got %s", isTypeMemberName, value.Kind())
	}
	typeAtom, err := parseTypeAtom(text)
	if err != nil {
		return err
	}
	if typeAtom.Kind != TypeEnum || !strings.Contains(typeAtom.Name, ".") {
		return nil
	}
	match, ok, lookupErr := lookupNamedTypeForType(typeAtom, c.runtimeTypeContext())
	if lookupErr != nil || !ok || !typeAtomSpellingExact(typeAtom.Name, match) {
		return fmt.Errorf("unknown type atom %q in %s", text, isTypeMemberName)
	}
	return nil
}

// checkBuiltinArgumentTypes reports call arguments whose inferred types are
// provably disjoint from the builtin's declared parameter types. Positions
// past the declared list and keywords without a declared type stay unknown.
func (c *scriptChecker) checkBuiltinArgumentTypes(function string, call staticCallView, name string, spec staticCallSpec) {
	for i, arg := range call.args {
		if i >= len(spec.paramTypes) {
			break
		}
		label := strconv.Itoa(i + 1)
		if i < len(spec.paramNames) && spec.paramNames[i] != "" {
			label = spec.paramNames[i]
		}
		ty := spec.paramTypes[i]
		if spec.fromSignature && ty != nil {
			// A host signature naming a type the script never defines fails
			// every call at the runtime boundary before the host function
			// runs, so the call site reports it instead of silently skipping
			// the unresolved declaration.
			if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
				c.add(function, arg.Pos(), "call to %s argument %s uses unknown type %s", name, label, formatTypeExpr(ty))
				continue
			}
		}
		c.checkInferredArgument(function, arg, ty, name, label)
	}
	for _, kwarg := range call.kwargs {
		c.checkInferredArgument(function, kwarg.Value, spec.keywordTypes[kwarg.Name], name, kwarg.Name)
	}
	if spec.fromSignature && spec.resultType != nil {
		// An unresolved result type rejects every successful call at the
		// return boundary, whether or not the caller uses the result.
		if err := validateTypeExprResolved(spec.resultType, c.runtimeTypeContext()); err != nil {
			c.add(function, call.pos, "call to %s result uses unknown type %s", name, formatTypeExpr(spec.resultType))
		}
	}
}

type staticCallView struct {
	pos      Position
	args     []Expression
	kwargs   []KeywordArg
	block    *BlockLiteral
	blockArg Expression
}

func staticCallViewFor(call *CallExpr, target staticCallable) staticCallView {
	view := staticCallView{
		pos:      call.Pos(),
		args:     call.Args,
		kwargs:   call.KwArgs,
		block:    call.Block,
		blockArg: call.BlockArg,
	}
	if !staticCallCollapsesOptionsHash(call, target) {
		return view
	}
	hash := &HashLiteral{
		Position: call.Pos(),
		Pairs:    make([]HashPair, 0, len(call.KwArgs)),
	}
	for _, kwarg := range call.KwArgs {
		hash.Pairs = append(hash.Pairs, HashPair{
			Key: &StringLiteral{
				Value:    kwarg.Name,
				Position: kwarg.Value.Pos(),
			},
			Value: kwarg.Value,
		})
	}
	args := make([]Expression, 0, len(call.Args)+1)
	args = append(args, call.Args...)
	args = append(args, hash)
	view.args = args
	view.kwargs = nil
	return view
}

func staticCallCollapsesOptionsHash(call *CallExpr, target staticCallable) bool {
	if !call.KeywordOptionsHash || len(call.KwArgs) == 0 || target.fn == nil {
		return false
	}
	if call.Parenthesized && !target.constructor && target.resolution == calleeMemberMethod {
		return false
	}
	return functionCanReceiveOptionsHash(target.fn, len(call.Args), func(name string) bool {
		for _, kwarg := range call.KwArgs {
			if kwarg.Name == name {
				return true
			}
		}
		return false
	})
}

func sortedValueKeywordNames(kwargs map[string]Value) []string {
	names := make([]string, 0, len(kwargs))
	for name := range kwargs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *scriptChecker) checkCallShape(function string, call staticCallView, name string, fn *ScriptFunction) {
	var usedKw map[string]bool
	if len(call.kwargs) > 0 {
		usedKw = make(map[string]bool, len(call.kwargs))
	}
	argIdx := 0

	for _, param := range fn.Params {
		switch param.Kind {
		case ParamKeyword:
			if keywordIndex(call, param.Name) >= 0 {
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal == nil {
				c.add(function, call.pos, "call to %s is missing keyword argument %s", name, param.Name)
			}
		case ParamRest:
			argIdx = len(call.args)
		case ParamKeywordRest:
			for _, kwarg := range call.kwargs {
				if usedKw != nil {
					usedKw[kwarg.Name] = true
				}
			}
		case ParamBlock:
		case ParamNormal:
			if argIdx < len(call.args) {
				argIdx++
			} else if keywordIndex(call, param.Name) >= 0 {
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal == nil {
				c.add(function, call.pos, "call to %s is missing argument %s", name, param.Name)
			}
		}
	}

	if argIdx < len(call.args) {
		c.add(function, call.pos, "call to %s has unexpected positional arguments", name)
	}
	if usedKw != nil {
		lastIndex := make(map[string]int, len(call.kwargs))
		for i, kwarg := range call.kwargs {
			lastIndex[kwarg.Name] = i
		}
		for i, kwarg := range call.kwargs {
			if lastIndex[kwarg.Name] == i && !usedKw[kwarg.Name] {
				c.add(function, kwarg.Value.Pos(), "call to %s has unexpected keyword argument %s", name, kwarg.Name)
			}
		}
	}
}

func (c *scriptChecker) checkCallValueShape(function, callName string, pos Position, fn *ScriptFunction, args []Value, kwargs map[string]Value) bool {
	ok := true
	var usedKw map[string]bool
	if len(kwargs) > 0 {
		usedKw = make(map[string]bool, len(kwargs))
	}
	argIdx := 0

	for _, param := range fn.Params {
		switch param.Kind {
		case ParamKeyword:
			if _, present := kwargs[param.Name]; present {
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal == nil {
				c.add(function, pos, "call to %s is missing keyword argument %s", callName, param.Name)
				ok = false
			}
		case ParamRest:
			argIdx = len(args)
		case ParamKeywordRest:
			for _, name := range sortedValueKeywordNames(kwargs) {
				if usedKw != nil {
					usedKw[name] = true
				}
			}
		case ParamBlock:
		case ParamNormal:
			if argIdx < len(args) {
				argIdx++
			} else if _, present := kwargs[param.Name]; present {
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else if param.DefaultVal == nil {
				c.add(function, pos, "call to %s is missing argument %s", callName, param.Name)
				ok = false
			}
		}
	}

	if argIdx < len(args) {
		c.add(function, pos, "call to %s has unexpected positional arguments", callName)
		ok = false
	}
	if usedKw != nil {
		for _, name := range sortedValueKeywordNames(kwargs) {
			if !usedKw[name] {
				c.add(function, pos, "call to %s has unexpected keyword argument %s", callName, name)
				ok = false
			}
		}
	}
	return ok
}

func (c *scriptChecker) checkCallArgumentTypes(function string, call staticCallView, name string, fn *ScriptFunction) {
	var usedKw map[string]bool
	if len(call.kwargs) > 0 {
		usedKw = make(map[string]bool, len(call.kwargs))
	}
	argIdx := 0
	for _, param := range fn.Params {
		switch param.Kind {
		case ParamNormal:
			if argIdx < len(call.args) {
				c.checkArgumentExpression(function, call.args[argIdx], param.Type, name, param.Name)
				argIdx++
				continue
			}
			if kwIndex := keywordIndex(call, param.Name); kwIndex >= 0 {
				c.checkArgumentExpression(function, call.kwargs[kwIndex].Value, param.Type, name, param.Name)
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			}
		case ParamKeyword:
			if kwIndex := keywordIndex(call, param.Name); kwIndex >= 0 {
				c.checkArgumentExpression(function, call.kwargs[kwIndex].Value, param.Type, name, param.Name)
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			}
		case ParamRest:
			c.checkRestArgumentExpressions(function, call.pos, call.args[argIdx:], param.Type, name, param.Name)
			argIdx = len(call.args)
		case ParamKeywordRest:
			c.checkKeywordRestArgumentExpressions(function, call.pos, call.kwargs, usedKw, param.Type, name, param.Name)
		case ParamBlock:
			pos := call.pos
			if call.block != nil {
				pos = call.block.Pos()
			}
			c.checkBlockArgumentValue(function, pos, call.block, param.Type, name, param.Name)
		}
	}
}

func (c *scriptChecker) checkRestArgumentValues(function string, pos Position, args []Value, ty *TypeExpr, callName, paramName string) {
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	if err := c.checkRuntimeStaticValueType(NewArray(args), ty); err != nil {
		c.addArgumentValueWarning(function, pos, callName, paramName, err)
	}
}

func (c *scriptChecker) checkKeywordRestArgumentValues(function string, pos Position, kwargs map[string]Value, usedKw map[string]bool, ty *TypeExpr, callName, paramName string) {
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	values := make(map[string]Value)
	for name, val := range kwargs {
		if usedKw != nil && usedKw[name] {
			continue
		}
		values[name] = val
	}
	if err := c.checkRuntimeStaticValueType(NewHash(values), ty); err != nil {
		c.addArgumentValueWarning(function, pos, callName, paramName, err)
	}
}

func (c *scriptChecker) checkRestArgumentExpressions(function string, pos Position, args []Expression, ty *TypeExpr, callName, paramName string) {
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	values := make([]Value, 0, len(args))
	for _, arg := range args {
		val, ok := staticLiteralValue(arg)
		if !ok {
			// The collected values are no longer fully static: fall back to
			// checking each argument's inferred type against the rest
			// annotation's element type.
			if ty.Kind == TypeUnion && !c.restArgumentsMayBindTypeArm(args, ty) {
				c.add(function, arg.Pos(), "call to %s argument %s expected %s, got incompatible rest arguments",
					callName, paramName, formatTypeExpr(ty))
				return
			}
			if ty.Kind == TypeArray && len(ty.TypeArgs) == 1 {
				for _, rest := range args {
					c.checkInferredArgument(function, rest, ty.TypeArgs[0], callName, paramName)
				}
			}
			return
		}
		values = append(values, val)
	}
	if err := c.checkRuntimeStaticValueType(NewArray(values), ty); err != nil {
		warningPos := pos
		if len(args) > 0 {
			warningPos = args[0].Pos()
		}
		var mismatch *typeMismatchError
		if errors.As(err, &mismatch) {
			c.add(function, warningPos, "call to %s argument %s expected %s, got %s", callName, paramName, mismatch.Expected, mismatch.Actual)
			return
		}
		c.add(function, warningPos, "call to %s argument %s type check failed: %s", callName, paramName, err)
	}
}

func (c *scriptChecker) checkKeywordRestArgumentExpressions(function string, pos Position, kwargs []KeywordArg, usedKw map[string]bool, ty *TypeExpr, callName, paramName string) {
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	lastIndex := make(map[string]int, len(kwargs))
	for i, kwarg := range kwargs {
		lastIndex[kwarg.Name] = i
	}
	canonical := make([]KeywordArg, 0, len(lastIndex))
	for i, kwarg := range kwargs {
		if lastIndex[kwarg.Name] == i {
			canonical = append(canonical, kwarg)
		}
	}
	kwargs = canonical
	values := make(map[string]Value)
	var warningPos Position
	for _, kwarg := range kwargs {
		if usedKw != nil && usedKw[kwarg.Name] {
			continue
		}
		if warningPos == (Position{}) {
			warningPos = kwarg.Value.Pos()
		}
		val, ok := staticLiteralValue(kwarg.Value)
		if !ok {
			// The collected keyword values are no longer fully static: fall
			// back to inferred checks. Rest keywords bind as a string-keyed
			// hash at runtime, so a disjoint declared key type always fails
			// at call binding, and an exact shape checks per field.
			if ty.Kind == TypeUnion {
				last := make(map[string]Expression, len(kwargs))
				for _, rest := range kwargs {
					if usedKw == nil || !usedKw[rest.Name] {
						last[rest.Name] = rest.Value
					}
				}
				if !c.keywordRestArgumentsMayBindTypeArm(last, ty) {
					c.add(function, kwarg.Value.Pos(), "call to %s argument %s expected %s, got incompatible keyword rest arguments",
						callName, paramName, formatTypeExpr(ty))
				}
				return
			}
			switch {
			case ty.Kind == TypeHash && len(ty.TypeArgs) == 2:
				if typeExprsDisjoint(checkTypeString, ty.TypeArgs[0], c.checkNamedTypeResolver()) {
					c.add(function, kwarg.Value.Pos(), "call to %s argument %s expected %s, got string-keyed keywords",
						callName, paramName, formatTypeExpr(ty))
					return
				}
				for _, rest := range kwargs {
					if usedKw != nil && usedKw[rest.Name] {
						continue
					}
					c.checkInferredArgument(function, rest.Value, ty.TypeArgs[1], callName, paramName)
				}
			case ty.Kind == TypeShape:
				supplied := make(map[string]struct{}, len(kwargs))
				for _, rest := range kwargs {
					if usedKw != nil && usedKw[rest.Name] {
						continue
					}
					supplied[rest.Name] = struct{}{}
					fieldType, known := ty.Shape[rest.Name]
					if !known {
						// An open shape admits undeclared keywords unchecked.
						if !ty.Open {
							c.add(function, rest.Value.Pos(), "call to %s argument %s expected %s, got keyword %s",
								callName, paramName, formatTypeExpr(ty), rest.Name)
						}
						continue
					}
					c.checkInferredArgument(function, rest.Value, fieldType, callName, paramName)
				}
				// Exact shapes require every non-optional field, so an absent
				// required keyword is a known normalization failure.
				missingPos := warningPos
				if missingPos == (Position{}) {
					missingPos = pos
				}
				fields := make([]string, 0, len(ty.Shape))
				for field := range ty.Shape {
					if shapeFieldOptional(ty.Shape[field]) {
						continue
					}
					fields = append(fields, field)
				}
				sort.Strings(fields)
				for _, field := range fields {
					if _, ok := supplied[field]; !ok {
						c.add(function, missingPos, "call to %s argument %s expected %s, missing keyword %s",
							callName, paramName, formatTypeExpr(ty), field)
					}
				}
			}
			return
		}
		values[kwarg.Name] = val
	}
	if warningPos == (Position{}) {
		warningPos = pos
	}
	if err := c.checkRuntimeStaticValueType(NewHash(values), ty); err != nil {
		var mismatch *typeMismatchError
		if errors.As(err, &mismatch) {
			c.add(function, warningPos, "call to %s argument %s expected %s, got %s", callName, paramName, mismatch.Expected, mismatch.Actual)
			return
		}
		c.add(function, warningPos, "call to %s argument %s type check failed: %s", callName, paramName, err)
	}
}

// checkArgumentValue validates val against ty and returns the value the
// runtime would bind: normalization may coerce (a symbol into an enum
// member, for example), so the bound value is the per-call fact source.
func (c *scriptChecker) checkArgumentValue(function string, pos Position, val Value, ty *TypeExpr, callName, paramName string) (Value, bool) {
	if ty == nil {
		return val, true
	}
	if !c.checkRuntimeTypeAnnotation(function, ty) {
		return val, false
	}
	normalized, err := normalizeValueForType(val, ty, c.runtimeTypeContext())
	if err != nil {
		c.addArgumentValueWarning(function, pos, callName, paramName, err)
		return val, false
	}
	return normalized, true
}

func (c *scriptChecker) checkBlockArgumentValue(function string, pos Position, block *BlockLiteral, ty *TypeExpr, callName, paramName string) {
	val := NewNil()
	if block != nil {
		val = NewBlock(block.Params, block.Body, newEnv(nil))
	}
	c.checkArgumentValue(function, pos, val, ty, callName, paramName)
}

func (c *scriptChecker) addArgumentValueWarning(function string, pos Position, callName, paramName string, err error) {
	var mismatch *typeMismatchError
	if errors.As(err, &mismatch) {
		c.add(function, pos, "call to %s argument %s expected %s, got %s", callName, paramName, mismatch.Expected, mismatch.Actual)
		return
	}
	c.add(function, pos, "call to %s argument %s type check failed: %s", callName, paramName, err)
}

func (c *scriptChecker) checkArgumentExpression(function string, expr Expression, ty *TypeExpr, callName, paramName string) {
	val, ok := staticLiteralValue(expr)
	if !ok {
		c.checkInferredArgument(function, expr, ty, callName, paramName)
		return
	}
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	if err := c.checkRuntimeStaticValueType(val, ty); err != nil {
		var mismatch *typeMismatchError
		if errors.As(err, &mismatch) {
			c.add(function, expr.Pos(), "call to %s argument %s expected %s, got %s", callName, paramName, mismatch.Expected, mismatch.Actual)
			return
		}
		c.add(function, expr.Pos(), "call to %s argument %s type check failed: %s", callName, paramName, err)
	}
}

func keywordIndex(call staticCallView, name string) int {
	for i := len(call.kwargs) - 1; i >= 0; i-- {
		if call.kwargs[i].Name == name {
			return i
		}
	}
	return -1
}

func staticLiteralValue(expr Expression) (Value, bool) {
	switch typed := expr.(type) {
	case *IntegerLiteral:
		if typed.Big != nil {
			// Big literals stay non-static: every folding consumer reads the
			// int64 field, and treating the literal as dynamic is always safe.
			return NewNil(), false
		}
		return NewInt(typed.Value), true
	case *FloatLiteral:
		return NewFloat(typed.Value), true
	case *StringLiteral:
		return NewString(typed.Value), true
	case *BoolLiteral:
		return NewBool(typed.Value), true
	case *NilLiteral:
		return NewNil(), true
	case *SymbolLiteral:
		return NewSymbol(typed.Name), true
	case *UnaryExpr:
		return staticUnaryLiteralValue(typed)
	case *ArrayLiteral:
		items := make([]Value, 0, len(typed.Elements))
		for _, elem := range typed.Elements {
			item, ok := staticLiteralValue(elem)
			if !ok {
				return NewNil(), false
			}
			items = append(items, item)
		}
		return NewArray(items), true
	case *TypeLiteral:
		// The argument may evaluate as a first-class type value, so it has
		// no static value reading.
		return NewNil(), false
	case *HashLiteral:
		if typed.ShapeType != nil {
			// The group may evaluate as a first-class shape value, so it has
			// no static hash reading.
			return NewNil(), false
		}
		entries := make(map[string]Value, len(typed.Pairs))
		for _, pair := range typed.Pairs {
			key, ok := staticLiteralHashKey(pair.Key)
			if !ok {
				return NewNil(), false
			}
			val, ok := staticLiteralValue(pair.Value)
			if !ok {
				return NewNil(), false
			}
			entries[key] = val
		}
		return NewHash(entries), true
	case *RangeExpr:
		start, ok := staticLiteralRangeEndpoint(typed.Start)
		if !ok {
			return NewNil(), false
		}
		end, ok := staticLiteralRangeEndpoint(typed.End)
		if !ok {
			return NewNil(), false
		}
		return NewRange(Range{Start: start, End: end, Exclusive: typed.Exclusive}), true
	}
	return NewNil(), false
}

func staticExpressionTruthiness(expr Expression) (bool, bool) {
	if _, ok := expr.(*RegexLiteral); ok {
		// A regex literal always builds a (truthy) regex value, so a condition
		// that is just a regex literal is statically true. The checker does not
		// compile it here — pattern validity stays a runtime concern — it only
		// needs the truthiness so an if without an else is not treated as a path
		// that can fall through to nil.
		return true, true
	}
	val, ok := staticLiteralValue(expr)
	if !ok {
		return false, false
	}
	return val.Truthy(), true
}

func staticConditionalExpressionBranch(expr *ConditionalExpr) (Expression, bool) {
	truthy, ok := staticExpressionTruthiness(expr.Condition)
	if !ok {
		return nil, false
	}
	if truthy {
		return expr.Consequent, true
	}
	return expr.Alternate, true
}

func staticIfExpressionBranch(expr *IfExpr) (Expression, bool) {
	if expr == nil {
		return nil, false
	}
	truthy, known := staticExpressionTruthiness(expr.Condition)
	if !known {
		return nil, false
	}
	if truthy {
		return expr.Consequent, true
	}
	for _, branch := range expr.ElseIf {
		truthy, known = staticExpressionTruthiness(branch.Condition)
		if !known {
			return nil, false
		}
		if truthy {
			return branch.Result, true
		}
	}
	return expr.Alternate, true
}

func staticCaseExpressionResult(expr *CaseExpr) (Expression, bool) {
	if expr == nil {
		return nil, false
	}
	var target Value
	if expr.Target != nil {
		var ok bool
		target, ok = staticLiteralValue(expr.Target)
		if !ok {
			return nil, false
		}
	}
	for _, clause := range expr.Clauses {
		for _, candidate := range clause.Values {
			if candidate.Splat {
				return nil, false
			}
			value, ok := staticLiteralValue(candidate.Expr)
			if !ok {
				return nil, false
			}
			matched := value.Truthy()
			if expr.Target != nil {
				var err error
				matched, err = caseCandidateMatches(target, value)
				if err != nil {
					return nil, false
				}
			}
			if matched {
				return clause.Result, true
			}
		}
	}
	return expr.ElseExpr, true
}

func (c *scriptChecker) inferredCaseExpressionResult(expr *CaseExpr) (Expression, bool) {
	if result, known := staticCaseExpressionResult(expr); known {
		return result, true
	}
	if expr == nil || expr.Target == nil {
		return nil, false
	}
	targets, exact := c.staticValueExpressionAlternatives(expr.Target)
	if !exact || len(targets) == 0 {
		return nil, false
	}
	var selected Expression
	selectedSet := false
	for _, targetExpr := range targets {
		target, static := staticLiteralValue(targetExpr)
		if !static {
			return nil, false
		}
		result := expr.ElseExpr
		matched := false
		for _, clause := range expr.Clauses {
			for _, candidate := range clause.Values {
				if candidate.Splat {
					return nil, false
				}
				value, static := staticLiteralValue(candidate.Expr)
				if !static {
					return nil, false
				}
				candidateMatched, err := caseCandidateMatches(target, value)
				if err != nil {
					return nil, false
				}
				if candidateMatched {
					result = clause.Result
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !selectedSet {
			selected = result
			selectedSet = true
			continue
		}
		if selected != result {
			return nil, false
		}
	}
	return selected, selectedSet
}

func staticRequireModuleName(expr Expression) (string, bool) {
	switch typed := expr.(type) {
	case *StringLiteral:
		return typed.Value, true
	case *SymbolLiteral:
		return typed.Name, true
	default:
		return "", false
	}
}

func staticUnaryLiteralValue(expr *UnaryExpr) (Value, bool) {
	val, ok := staticLiteralValue(expr.Right)
	if !ok || expr.Operator != tokenMinus {
		return NewNil(), false
	}
	switch val.Kind() {
	case KindInt:
		return NewInt(-val.Int()), true
	case KindFloat:
		return NewFloat(-val.Float()), true
	default:
		return NewNil(), false
	}
}

func staticLiteralHashKey(expr Expression) (string, bool) {
	val, ok := staticLiteralValue(expr)
	if !ok {
		return "", false
	}
	key, err := valueToHashKey(val)
	if err != nil {
		return "", false
	}
	return key, true
}

func staticLiteralRangeEndpoint(expr Expression) (int64, bool) {
	val, ok := staticLiteralValue(expr)
	if !ok || val.Kind() != KindInt {
		return 0, false
	}
	return val.Int(), true
}

func typeExprPosition(ty *TypeExpr) Position {
	if ty == nil {
		return Position{}
	}
	if ty.Position.Line > 0 || ty.Position.Column > 0 {
		return ty.Position
	}
	for _, option := range ty.Union {
		if pos := typeExprPosition(option); pos.Line > 0 || pos.Column > 0 {
			return pos
		}
	}
	for _, arg := range ty.TypeArgs {
		if pos := typeExprPosition(arg); pos.Line > 0 || pos.Column > 0 {
			return pos
		}
	}
	for _, field := range ty.Shape {
		if pos := typeExprPosition(field); pos.Line > 0 || pos.Column > 0 {
			return pos
		}
	}
	return Position{}
}

func (c *scriptChecker) pushBlockCheckScope(block *BlockLiteral) func() {
	scope := make(map[string]struct{})
	for _, param := range block.Params {
		if param.Name != "" {
			scope[param.Name] = struct{}{}
		}
		collectBindingTarget(param.Target, scope)
	}
	for _, name := range block.ImplicitParams {
		if name != "" {
			scope[name] = struct{}{}
		}
	}
	return c.pushScope(scope)
}

func (c *scriptChecker) pushRescueScope(clause *RescueClause) func() {
	if clause == nil || clause.Binding == "" {
		return func() {}
	}
	return c.pushScope(map[string]struct{}{clause.Binding: {}})
}

func (c *scriptChecker) pushScope(scope map[string]struct{}) func() {
	c.scopes = append(c.scopes, scope)
	c.localTypes = append(c.localTypes, nil)
	c.localClassValues = append(c.localClassValues, nil)
	c.liveLocalNames = append(c.liveLocalNames, nil)
	return func() {
		c.scopes = c.scopes[:len(c.scopes)-1]
		c.localTypes = c.localTypes[:len(c.localTypes)-1]
		c.localClassValues = c.localClassValues[:len(c.localClassValues)-1]
		c.liveLocalNames = c.liveLocalNames[:len(c.liveLocalNames)-1]
	}
}

func (c *scriptChecker) recordBindingTarget(target Expression) {
	if len(c.scopes) == 0 {
		return
	}
	scope := c.scopes[len(c.scopes)-1]
	if scope == nil {
		scope = make(map[string]struct{})
		c.scopes[len(c.scopes)-1] = scope
	}
	collectBindingTarget(target, scope)
}

func (c *scriptChecker) recordRuntimeBindingTarget(target Expression) {
	switch typed := target.(type) {
	case *MemberExpr:
		if c.memberAssignmentIntercepted(typed) {
			return
		}
		memberName, ok := c.runtimeNamespaceMemberName(typed)
		if ok {
			c.recordRuntimeNamespaceMember(memberName)
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			c.recordRuntimeBindingTarget(element.Target)
		}
	}
}

func (c *scriptChecker) memberAssignmentIntercepted(member *MemberExpr) bool {
	if member == nil {
		return false
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return false
	}
	var methods map[string]*ScriptFunction
	if ident.Name == "self" && c.selfClass != nil {
		methods = c.selfClass.Methods
		if c.selfClassContext {
			methods = c.selfClass.ClassMethods
		}
	} else if classDef, ok := c.assignmentClassValue(ident); ok {
		methods = classDef.ClassMethods
	} else {
		return false
	}
	_, hasSetter := methods[member.Property+"="]
	_, hasGetter := methods[member.Property]
	return hasSetter || hasGetter
}

func (c *scriptChecker) assignmentClassValue(ident *Identifier) (*ClassDef, bool) {
	if ident == nil {
		return nil, false
	}
	if className, ok := c.localClassValueFor(ident.Name); ok {
		classDef, exists := c.script.classes[className]
		return classDef, exists && classDef != nil
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		return nil, false
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

func (c *scriptChecker) runtimeNamespaceMemberName(target Expression) (string, bool) {
	member, ok := target.(*MemberExpr)
	if !ok {
		return "", false
	}
	switch obj := member.Object.(type) {
	case *Identifier:
		if obj.Name == "self" {
			if c.selfClass == nil || !c.selfClassContext {
				return "", false
			}
			return c.selfClass.Name + "." + member.Property, true
		}
		namespace := obj.Name
		if className, ok := c.localClassValueFor(obj.Name); ok {
			namespace = className
		}
		return namespace + "." + member.Property, true
	case *MemberExpr:
		ident, selfClass := obj.Object.(*Identifier)
		if !selfClass || ident.Name != "self" || obj.Property != "class" || c.selfClass == nil ||
			classMemberAssignmentIntercepted(c.selfClass, member.Property) {
			return "", false
		}
		return c.selfClass.Name + "." + member.Property, true
	}
	return "", false
}

func (c *scriptChecker) recordRuntimeNamespaceMember(memberName string) {
	if memberName == "" {
		return
	}
	if c.runtimeNamespaceMembers == nil {
		c.runtimeNamespaceMembers = make(map[string]struct{})
	}
	c.runtimeNamespaceMembers[memberName] = struct{}{}
	for i := range c.classConstantCaptures {
		if c.classConstantCaptures[i].namespaceMembers == nil {
			c.classConstantCaptures[i].namespaceMembers = make(map[string]struct{})
		}
		c.classConstantCaptures[i].namespaceMembers[memberName] = struct{}{}
	}
}

// scriptFunctionNamespaceMutations reports the builtin namespace members a
// call to fn may reassign, transitively through the owned functions it
// references. A non-nil call refines the root function's defaults to the
// ones this call's shape can leave unsupplied; a nil call (a bare reference
// or unknown shape) counts every default. Transitive references always count
// every default, since their future call shapes are unknowable.
func (c *scriptChecker) scriptFunctionNamespaceMutations(
	call *CallExpr,
	target staticCallable,
) map[string]struct{} {
	scan := c.scriptFunctionEffectScan(call, target)
	if scan == nil {
		return nil
	}
	return scan.out
}

func (c *scriptChecker) scriptFunctionEffectScan(
	call *CallExpr,
	target staticCallable,
) *namespaceMutationScan {
	fn := target.fn
	if fn == nil || fn.owner != c.script {
		return nil
	}
	scan := c.newNamespaceMutationScan()
	if call == nil {
		scan.active[fn] = struct{}{}
		scan.function(fn)
	} else {
		scan.scanFunctionCall(fn, call, target)
	}
	return scan
}

// callMayEvaluateParamDefault mirrors the runtime's binding bookkeeping
// (bindFunctionArgs): a parameter default runs only when the call leaves the
// parameter unsupplied by position, keyword, or a collapsed options hash. A
// splatted call's shape is dynamic, so every default may run. The collapse
// decision comes from the resolved target because parenthesized ordinary
// methods keep keyword binding strict.
func callMayEvaluateParamDefault(
	call *CallExpr,
	fn *ScriptFunction,
	paramIndex int,
	collapseOptionsHash bool,
) bool {
	_, mayDefault := callParamSupply(call, fn, paramIndex, collapseOptionsHash)
	return mayDefault
}

// callParamSupply reports how a non-splatted call shape treats one
// parameter: optionsHash marks the open positional parameter a collapsed
// keyword options hash supplies (resolveKeywordOptionsHash), and mayDefault
// reports whether the parameter's default may still evaluate. The caller
// supplies the target-aware collapse decision; this helper only mirrors the
// binding loop once that dispatch rule is known.
func callParamSupply(
	call *CallExpr,
	fn *ScriptFunction,
	paramIndex int,
	collapseOptionsHash bool,
) (optionsHash, mayDefault bool) {
	if callExpandsArguments(call) {
		return false, true
	}
	collapse := collapseOptionsHash
	keywordsConsumed := false
	argIdx := 0
	for i, param := range fn.Params {
		switch param.Kind {
		case ParamNormal:
			supplied := false
			hashSupplied := false
			if argIdx < len(call.Args) {
				argIdx++
				supplied = true
			} else if !keywordsConsumed && callHasKeywordArg(call, param.Name) {
				supplied = true
			} else if collapse {
				supplied = true
				hashSupplied = true
				collapse = false
				// The runtime clears the keyword map after appending the
				// synthetic options hash, so later parameters cannot bind
				// any of the original keyword names.
				keywordsConsumed = true
			}
			if i == paramIndex {
				return hashSupplied, !supplied
			}
		case ParamKeyword:
			if i == paramIndex {
				return false, keywordsConsumed || !callHasKeywordArg(call, param.Name)
			}
		case ParamRest:
			argIdx = len(call.Args)
			// A rest parameter absorbs the collapsed hash as its final
			// element, so no later positional parameter receives it.
			keywordsConsumed = keywordsConsumed || collapse
			collapse = false
			if i == paramIndex {
				return false, false
			}
		default:
			if i == paramIndex {
				return false, false
			}
		}
	}
	return false, true
}

func callHasKeywordArg(call *CallExpr, name string) bool {
	if name == "" {
		return false
	}
	for _, kw := range call.KwArgs {
		if kw.Name == name {
			return true
		}
	}
	return false
}

// applyScriptFunctionNamespaceMutations carries a statically resolved
// callee's possible namespace writes back to its caller. Function checking
// itself runs against an isolated call-time snapshot, but these writes can
// change later dispatch (for example JSON.stringify = replacement), so a
// return summary computed before the call must not be reused afterwards.
// This call itself dispatched under the pre-mutation bindings, so its own
// result fact pins before the write markers change the summary context for
// the code after the call.
func (c *scriptChecker) applyScriptFunctionNamespaceMutations(call *CallExpr, target staticCallable) {
	effectCall := call
	if expanded, exact := c.staticallyExpandedCall(call); exact {
		effectCall = expanded
	}
	members := c.scriptFunctionNamespaceMutations(effectCall, target)
	if len(members) == 0 {
		return
	}
	if call != nil {
		c.pinExpressionFact(call, c.inferCallExprType(call))
	}
	for member := range members {
		c.recordRuntimeNamespaceMember(member)
	}
}

// applyDynamicCallNamespaceMutations carries the union of an exact dynamic
// target set back to the caller. The original call is pinned once before any
// member marker changes inference, keeping the result independent of candidate
// order while invalidating summaries that rely on a mutated builtin binding.
func (c *scriptChecker) applyDynamicCallNamespaceMutations(
	call *CallExpr,
	targets []checkDynamicCallTarget,
) {
	var members map[string]struct{}
	for _, candidate := range targets {
		if !candidate.bindingStarts {
			continue
		}
		for member := range c.scriptFunctionNamespaceMutations(candidate.call, candidate.target) {
			if members == nil {
				members = make(map[string]struct{})
			}
			members[member] = struct{}{}
		}
	}
	if len(members) == 0 {
		return
	}
	if call != nil {
		c.pinExpressionFact(call, c.inferCallExprType(call))
	}
	for member := range members {
		c.recordRuntimeNamespaceMember(member)
	}
}

func (c *scriptChecker) applyCallableNamespaceMutations(fns []*ScriptFunction) {
	for _, fn := range fns {
		for member := range c.scriptFunctionNamespaceMutations(nil, staticCallable{fn: fn}) {
			c.recordRuntimeNamespaceMember(member)
		}
	}
}

func (c *scriptChecker) applyAutoInvokedIdentifierNamespaceMutations(ident *Identifier) {
	if ident == nil {
		return
	}
	if fns, exact := c.localCallableValuesFor(ident.Name); exact {
		for _, fn := range fns {
			if len(fn.Params) != 0 {
				continue
			}
			for member := range c.scriptFunctionNamespaceMutations(nil, staticCallable{fn: fn}) {
				c.recordRuntimeNamespaceMember(member)
			}
		}
		return
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		return
	}
	fn := c.script.functions[ident.Name]
	call := (*CallExpr)(nil)
	target := staticCallable{name: ident.Name, fn: fn}
	if fn == nil {
		fn, _ = c.typeRootFunction(ident.Name)
		target.fn = fn
	}
	if fn == nil {
		if c.typeRootHasBinding(ident.Name) || c.hostBuiltinOverrides(ident.Name) {
			return
		}
		if _, ok := c.defaultBuiltinCallSpec(ident.Name); ok {
			return
		}
		var ok bool
		call, target, ok = c.implicitSelfAutoCall(ident)
		if !ok || target.fn == nil {
			return
		}
		fn = target.fn
	} else if len(fn.Params) != 0 {
		return
	}
	members := c.scriptFunctionNamespaceMutations(call, target)
	if len(members) == 0 {
		return
	}
	c.pinExpressionFact(ident, c.autoInvokedIdentifierResultFact(ident.Name))
	for member := range members {
		c.recordRuntimeNamespaceMember(member)
	}
}

func (c *scriptChecker) applyAutoInvokedMemberNamespaceMutations(
	member *MemberExpr,
	call *CallExpr,
	target staticCallable,
) {
	members := c.scriptFunctionNamespaceMutations(call, target)
	if len(members) == 0 {
		return
	}
	c.pinExpressionFact(member, c.memberResultFact(member))
	for name := range members {
		c.recordRuntimeNamespaceMember(name)
	}
}

// resolveImmediateLambdaBlock recognizes only syntactically direct lambda
// receivers. The named form counts when an unshadowed call resolves to the
// core lambda constructor with its ordinary zero-argument literal-block shape.
func (c *scriptChecker) resolveImmediateLambdaBlock(expr Expression) *BlockLiteral {
	switch typed := expr.(type) {
	case *BlockLiteral:
		if typed.Lambda {
			return typed
		}
	case *CallExpr:
		callee, ok := typed.Callee.(*Identifier)
		if !ok || callee.Name != "lambda" || typed.Block == nil || typed.BlockArg != nil ||
			len(typed.Args) != 0 || len(typed.KwArgs) != 0 ||
			!c.coreLambdaBinding() {
			return nil
		}
		target, resolved := c.resolveCallable(typed)
		if resolved && target.fn == nil && target.name == "lambda" {
			return typed.Block
		}
	}
	return nil
}

func (c *scriptChecker) callTargetsCoreLambda(
	call *CallExpr,
	target staticCallable,
	resolved bool,
) bool {
	if call == nil || !resolved || target.fn != nil || target.name != "lambda" ||
		!c.coreLambdaBinding() {
		return false
	}
	callee, ok := call.Callee.(*Identifier)
	return ok && callee.Name == "lambda"
}

// coreLambdaBinding reports whether a bare lambda call resolves to the
// language's lambda constructor. Call-option globals normally shadow core
// builtins, but a host may pass back the cloned lambda value returned by
// Engine.Builtins; its function identity preserves the same local-return
// semantics. Checking the function itself avoids treating a mutated snapshot
// as core.
func (c *scriptChecker) coreLambdaBinding() bool {
	if c.hostGlobalShadows("lambda") &&
		(c.optionGlobalsOverride || c.optionGlobalSeeded("lambda")) {
		builtin := valueBuiltin(c.optionGlobals["lambda"])
		return builtin != nil && builtin.Fn != nil &&
			reflect.ValueOf(builtin.Fn).Pointer() == reflect.ValueOf(BuiltinFunc(builtinLambda)).Pointer()
	}
	return !c.hostBuiltinOverrides("lambda")
}

func lambdaLiteralArity(block *BlockLiteral) int {
	if block == nil {
		return -1
	}
	if len(block.Params) > 0 {
		return len(block.Params)
	}
	return implicitBlockParamArity(block.ImplicitParams)
}

// immediateLambdaCallMayEnter reports whether the direct receiver's body can
// run. Exact arity and scalar literal type contradictions are rejected;
// dynamic splats stay conservative because they may provide the missing slots.
func (c *scriptChecker) immediateLambdaCallMayEnter(block *BlockLiteral, call *CallExpr) bool {
	if block == nil || call == nil || call.Block != nil {
		return false
	}
	if call.BlockArg != nil {
		if _, nilBlock := call.BlockArg.(*NilLiteral); !nilBlock {
			return false
		}
	}
	for _, kwarg := range call.KwArgs {
		hash, empty := kwarg.Value.(*HashLiteral)
		if !kwarg.Splat || !empty || hash.ShapeType != nil || len(hash.Pairs) != 0 {
			return false
		}
	}
	arguments := make([]Expression, 0, len(call.Args))
	dynamicSplat := false
	for _, arg := range call.Args {
		splat, ok := arg.(*SplatArg)
		if !ok {
			arguments = append(arguments, arg)
			continue
		}
		array, exact := splat.Value.(*ArrayLiteral)
		if !exact {
			dynamicSplat = true
			continue
		}
		arguments = append(arguments, array.Elements...)
	}
	if len(arguments) > lambdaLiteralArity(block) ||
		!dynamicSplat && len(arguments) != lambdaLiteralArity(block) {
		return false
	}
	if dynamicSplat || len(block.Params) == 0 {
		return true
	}
	for i, argument := range arguments {
		param := block.Params[i]
		if param.Type == nil {
			continue
		}
		value, static := staticLiteralValue(argument)
		if !static {
			continue
		}
		if _, err := normalizeValueForType(value, param.Type, c.runtimeTypeContext()); err != nil {
			return false
		}
	}
	return true
}

// applyLambdaLiteralNamespaceMutations records the possible namespace
// writes of a lambda expression passed directly to a call: the callee may
// invoke it during the call. A bare lambda definition leaks nothing — its
// body runs only if a later resolvable call reaches it.
func (c *scriptChecker) applyLambdaLiteralNamespaceMutations(arg Expression) {
	block := lambdaLiteralBlock(arg)
	if block == nil {
		return
	}
	c.applyLambdaBlockNamespaceMutations(block)
}

func lambdaLiteralBlock(arg Expression) *BlockLiteral {
	switch typed := arg.(type) {
	case *BlockLiteral:
		if typed.Lambda {
			return typed
		}
	case *CallExpr:
		callee, ok := typed.Callee.(*Identifier)
		if ok && callee.Name == "lambda" {
			return typed.Block
		}
	}
	return nil
}

func (c *scriptChecker) checkLambdaLiteralSummaryYields(function string, arg Expression) {
	c.checkInvokedLambdaSummaryYields(function, lambdaLiteralBlock(arg))
}

func (c *scriptChecker) checkScriptCallInvokedLambdaSummaryYields(
	function string,
	call *CallExpr,
	target staticCallable,
) {
	if c.summaryYieldCollector == nil {
		return
	}
	scan := c.scriptFunctionEffectScan(call, target)
	if scan == nil {
		return
	}
	for block := range scan.invokedLambdas {
		c.checkInvokedLambdaSummaryYields(function, block)
	}
}

// checkInvokedLambdaSummaryYields rechecks an executed lambda with local
// return semantics intact while allowing reachable yields to poison the
// enclosing function summary. Merely defining a lambda keeps yields inert.
func (c *scriptChecker) checkInvokedLambdaSummaryYields(function string, block *BlockLiteral) {
	if block == nil || c.summaryYieldCollector == nil || !c.summaryYieldsActive {
		return
	}
	previousBlock := c.summaryYieldBlock
	c.summaryYieldBlock = block
	defer func() { c.summaryYieldBlock = previousBlock }()
	c.checkBlockLiteral(function, block, true)
}

// applyLambdaBlockNamespaceMutations records the namespace members a lambda
// may rewrite once its body can run.
func (c *scriptChecker) applyLambdaBlockNamespaceMutations(block *BlockLiteral) {
	if block == nil {
		return
	}
	scan := c.newNamespaceMutationScan()
	scan.scanLambdaBlock(block)
	for member := range scan.out {
		c.recordRuntimeNamespaceMember(member)
	}
}

// pinExpressionFact fixes the fact of one walked expression node so later
// inference does not recompute it under state that arose after it evaluated.
// A re-walk of the node overwrites the pin with the new walk's fact.
func (c *scriptChecker) pinExpressionFact(expr Expression, fact *TypeExpr) {
	if c.pinnedExpressionFacts == nil {
		c.pinnedExpressionFacts = make(map[Expression]*TypeExpr)
	}
	c.pinnedExpressionFacts[expr] = fact
}

func (c *scriptChecker) recordBindingName(name string) {
	if name == "" || len(c.scopes) == 0 {
		return
	}
	scope := c.scopes[len(c.scopes)-1]
	if scope == nil {
		scope = make(map[string]struct{})
		c.scopes[len(c.scopes)-1] = scope
	}
	scope[name] = struct{}{}
}

func (c *scriptChecker) recordParamBinding(param Param) {
	if param.Name != "" {
		c.recordBindingName(param.Name)
	}
	c.recordBindingTarget(param.Target)
	c.bindParamLocalType(param)
}

func (c *scriptChecker) recordLocalBindings(statements []Statement) {
	if len(c.scopes) == 0 {
		return
	}
	scope := c.scopes[len(c.scopes)-1]
	if scope == nil {
		scope = make(map[string]struct{})
		c.scopes[len(c.scopes)-1] = scope
	}
	collectLocalBindings(statements, scope)
}

func (c *scriptChecker) identifierShadowed(name string) bool {
	return c.scopeHas(name)
}

// namespaceMemberMutated reports whether a `namespace.property` member (such as
// the builtin JSON.parse) is reassigned anywhere in scope. Member writes are
// recorded under their dotted path, which never collides with a plain
// identifier binding.
func (c *scriptChecker) namespaceMemberMutated(namespace, property string) bool {
	memberName := namespace + "." + property
	if c.scopeHas(memberName) {
		return true
	}
	if _, ok := c.classConstantContext.namespaceMembers[memberName]; ok {
		return true
	}
	_, ok := c.runtimeNamespaceMembers[memberName]
	return ok
}

func (c *scriptChecker) scopeHas(key string) bool {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][key]; ok {
			return true
		}
	}
	return false
}

func collectLocalBindings(statements []Statement, out map[string]struct{}) {
	for _, stmt := range statements {
		switch typed := stmt.(type) {
		case *AssignStmt:
			collectBindingTarget(typed.Target, out)
		case *IfStmt:
			collectLocalBindings(typed.Consequent, out)
			for _, elseIf := range typed.ElseIf {
				collectLocalBindings(elseIf.Consequent, out)
			}
			collectLocalBindings(typed.Alternate, out)
		case *ForStmt:
			collectBindingTarget(typed.Target, out)
			collectLocalBindings(typed.Body, out)
		case *WhileStmt:
			collectLocalBindings(typed.Body, out)
		case *UntilStmt:
			collectLocalBindings(typed.Body, out)
		case *TryStmt:
			collectLocalBindings(typed.Body, out)
			for i := range typed.Rescues {
				clause := &typed.Rescues[i]
				if clause.Binding != "" {
					out[clause.Binding] = struct{}{}
				}
				collectLocalBindings(clause.Body, out)
			}
			collectLocalBindings(typed.Else, out)
			collectLocalBindings(typed.Ensure, out)
		}
	}
}

func (c *scriptChecker) bindUnreachedRescueLocalsAsNil(stmt *TryStmt) {
	if stmt == nil {
		return
	}
	locals := make(map[string]struct{})
	for i := range stmt.Rescues {
		clause := &stmt.Rescues[i]
		if clause.Binding != "" {
			locals[clause.Binding] = struct{}{}
		}
		collectLocalBindings(clause.Body, locals)
	}
	for name := range locals {
		tracked := false
		for i := len(c.localTypes) - 1; i >= 0; i-- {
			if _, tracked = c.localTypes[i][name]; tracked {
				break
			}
		}
		if tracked {
			continue
		}
		c.bindLocalType(name, checkTypeNil)
		c.bindLocalClassValue(name, "")
	}
}

// namespaceMutationScan records every builtin namespace member a function's
// execution can reassign, descending into any code the body may run — block
// and lambda bodies, statement expressions, and nested definitions — so a
// caller invalidates cached facts even when the write hides inside a block
// ([1].each { JSON.stringify = replacement }). A reference to another owned
// function recurses into that function's body (visited breaks call cycles),
// so writes reached only through helpers, stored function values, or
// transitive calls still count. Exact class parameter facts keep same-named
// instance methods separated while the scan descends through helper calls.
type namespaceMutationScan struct {
	checker          *scriptChecker
	out              map[string]struct{}
	functions        map[string]*ScriptFunction
	classes          map[string]*ClassDef
	active           map[*ScriptFunction]struct{}
	activeDefaults   map[*ScriptFunction]map[int]struct{}
	methodClasses    map[*ScriptFunction]*ClassDef
	classMethodFns   map[*ScriptFunction]struct{}
	selfClass        *ClassDef
	selfClassContext bool
	// A nil class records a parameter that shadows a class name without
	// proving one exact instance dispatch.
	nominalReceivers map[string]*ClassDef
	callableParams   map[string][]*ScriptFunction
	callableLambdas  map[string][]Expression
	invokedLambdas   map[*BlockLiteral]struct{}
}

func (c *scriptChecker) newNamespaceMutationScan() *namespaceMutationScan {
	c.prepareSelfScopeFunctions()
	scan := &namespaceMutationScan{
		checker:          c,
		out:              make(map[string]struct{}),
		functions:        c.script.functions,
		classes:          c.script.classes,
		active:           make(map[*ScriptFunction]struct{}),
		activeDefaults:   make(map[*ScriptFunction]map[int]struct{}),
		methodClasses:    c.selfScopeFnClasses,
		classMethodFns:   c.selfScopeClassFns,
		selfClass:        c.selfClass,
		selfClassContext: c.selfClassContext,
		invokedLambdas:   make(map[*BlockLiteral]struct{}),
	}
	return scan
}

func (s *namespaceMutationScan) visit(fn *ScriptFunction) {
	if fn == nil {
		return
	}
	if _, active := s.active[fn]; active {
		return
	}
	s.active[fn] = struct{}{}
	defer delete(s.active, fn)
	s.function(fn)
}

// functionReference unions in the writes of an owned top-level function or
// implicit-self method the scanned body mentions. Any mention counts — callee,
// bare auto-invoke, or escaping value — since a stored function can run later;
// a shadowed name only over-invalidates, which is the sound direction.
func (s *namespaceMutationScan) functionReference(name string) {
	if fns, bound := s.callableParams[name]; bound {
		for _, fn := range fns {
			if len(fn.Params) == 0 {
				s.scanFunctionCall(fn, nil, staticCallable{fn: fn})
			}
		}
		return
	}
	if _, bound := s.callableLambdas[name]; bound {
		return
	}
	if fn := s.functions[name]; fn != nil {
		s.scanFunctionCall(fn, nil, staticCallable{name: name, fn: fn})
		return
	}
	if s.selfClass != nil {
		var fn *ScriptFunction
		if s.selfClassContext {
			fn = s.selfClass.ClassMethods[name]
		} else {
			fn = s.selfClass.Methods[name]
		}
		if fn != nil {
			s.scanFunctionCall(fn, nil, staticCallable{name: name, fn: fn})
		}
	}
}

func (s *namespaceMutationScan) functionReferenceWithCall(name string, call *CallExpr) {
	if fns, bound := s.callableParams[name]; bound {
		for _, fn := range fns {
			s.scanFunctionCall(fn, call, staticCallable{name: fn.Name + ".call", fn: fn})
		}
		return
	}
	if lambdas, bound := s.callableLambdas[name]; bound {
		for _, lambda := range lambdas {
			if block := lambdaLiteralBlock(lambda); block != nil &&
				s.checker.immediateLambdaCallMayEnter(block, call) {
				s.invokedLambdas[block] = struct{}{}
				s.scanLambdaBlock(block)
			}
		}
		return
	}
	if fn, ok := s.functions[name]; ok {
		s.scanFunctionCall(fn, call, staticCallable{name: name, fn: fn})
		return
	}
	s.selfCallReference(name, call)
}

func (s *namespaceMutationScan) selfReference(name string) {
	s.selfCallReference(name, &CallExpr{})
}

func (s *namespaceMutationScan) selfCallReference(name string, call *CallExpr) {
	if s.selfClass == nil {
		return
	}
	if s.selfClassContext {
		if name == "new" && !s.selfClass.IsModule {
			fn := s.selfClass.Methods["initialize"]
			if fn != nil {
				s.scanFunctionCall(fn, call, staticCallable{
					name:        s.selfClass.Name + ".new",
					fn:          fn,
					constructor: true,
				})
			}
			return
		}
		fn := s.selfClass.ClassMethods[name]
		if fn != nil {
			s.scanFunctionCall(fn, call, staticCallable{name: s.selfClass.Name + "." + name, fn: fn})
		}
		return
	}
	fn := s.selfClass.Methods[name]
	if fn != nil {
		s.scanFunctionCall(fn, call, staticCallable{name: s.selfClass.Name + "#" + name, fn: fn})
	}
}

// memberReference descends into the statically resolvable methods a member
// dispatch may run: a class method (Mutator.m), a constructor (Mutator.new
// runs initialize), or an instance method on a constructor-fact or exactly
// annotated receiver (Mutator.new.m or m: Mutator). Receivers the scan cannot
// resolve stay unscanned — the checker applies their writes when it walks the
// call itself.
func (s *namespaceMutationScan) memberReference(member *MemberExpr) {
	if implicitSelfConstructorReceiver(member.Object) && s.selfClassContext && s.selfClass != nil && !s.selfClass.IsModule {
		s.visit(s.selfClass.Methods["initialize"])
		s.visit(s.selfClass.Methods[member.Property])
		return
	}
	switch object := member.Object.(type) {
	case *Identifier:
		if object.Name == "self" {
			s.selfReference(member.Property)
			return
		}
		if classDef, bound := s.nominalReceivers[object.Name]; bound {
			if classDef != nil && member.Property != "initialize" {
				s.visit(classDef.Methods[member.Property])
			}
			return
		}
		classDef, ok := s.classes[object.Name]
		if !ok {
			return
		}
		if member.Property == "new" {
			s.visit(classDef.Methods["initialize"])
			return
		}
		s.visit(classDef.ClassMethods[member.Property])
	case *CallExpr:
		if className, ok := staticConstructorReceiverClass(object); ok {
			if classDef, ok := s.classes[className]; ok {
				s.visit(classDef.Methods[member.Property])
			}
		}
	case *MemberExpr:
		// A parenless constructor receiver (Mutator.new.m) reads as a
		// nested member.
		if ident, ok := object.Object.(*Identifier); ok && object.Property == "new" {
			if classDef, ok := s.classes[ident.Name]; ok {
				s.visit(classDef.Methods["initialize"])
				s.visit(classDef.Methods[member.Property])
			}
		}
	}
}

func staticConstructorReceiverClass(call *CallExpr) (string, bool) {
	member, ok := call.Callee.(*MemberExpr)
	if !ok || member.Property != "new" {
		return "", false
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// function scans everything a call may execute: parameter defaults run
// before the body when the caller omits the argument.
func (s *namespaceMutationScan) function(fn *ScriptFunction) {
	s.withFunctionContext(fn, nil, nil, func() {
		for _, param := range fn.Params {
			s.expression(param.DefaultVal)
		}
		s.statements(fn.Body)
	})
}

func (s *namespaceMutationScan) scanFunctionCall(
	fn *ScriptFunction,
	call *CallExpr,
	target staticCallable,
) {
	if fn == nil || fn.owner != s.checker.script {
		return
	}
	if call == nil {
		call = &CallExpr{}
	}
	if expanded, exact := s.checker.staticallyExpandedCall(call); exact {
		call = expanded
	}
	plan := s.checker.scriptCallBindingPlan(call, target)
	if !plan.bodyMayEnter && len(plan.defaultParams) == 0 {
		return
	}
	facts := s.checker.reachableCallParamFacts(call, target)
	lambdas := callableParamLambdaArguments(call, target, facts)
	if _, active := s.active[fn]; active {
		s.withFunctionContext(fn, facts, lambdas, func() {
			s.scanFunctionDefaults(fn, plan.defaultParams)
		})
		return
	}
	s.active[fn] = struct{}{}
	defer delete(s.active, fn)
	s.withFunctionContext(fn, facts, lambdas, func() {
		if !s.scanFunctionDefaults(fn, plan.defaultParams) {
			return
		}
		if plan.bodyMayEnter {
			s.statements(fn.Body)
		}
	})
}

func (s *namespaceMutationScan) scanFunctionDefaults(fn *ScriptFunction, indices []int) bool {
	if fn == nil {
		return true
	}
	active := s.activeDefaults[fn]
	if active == nil {
		active = make(map[int]struct{})
		s.activeDefaults[fn] = active
	}
	for _, paramIndex := range indices {
		if _, scanning := active[paramIndex]; scanning {
			continue
		}
		active[paramIndex] = struct{}{}
		completed := s.expression(fn.Params[paramIndex].DefaultVal)
		delete(active, paramIndex)
		if !completed {
			return false
		}
	}
	return true
}

func (s *namespaceMutationScan) withFunctionContext(
	fn *ScriptFunction,
	facts map[string]checkReachableParamFact,
	lambdas map[string][]Expression,
	walk func(),
) {
	restoreResolution := s.checker.withClassConstantProofResolution(fn, scriptFunctionBindings(fn))
	defer restoreResolution()
	previousFacts := s.checker.reachableParamFacts
	s.checker.reachableParamFacts = facts
	defer func() { s.checker.reachableParamFacts = previousFacts }()
	for _, param := range fn.Params {
		if param.Type != nil && param.Name != "" {
			s.checker.bindLocalTypeInCurrentFrame(param.Name, param.Type)
		}
		s.checker.applyReachableParamFact(param)
	}
	s.withCallableParamFacts(fn.Params, facts, lambdas, func() {
		s.withFunctionSelf(fn, func() {
			s.withNominalReceiverParams(fn.Params, false, walk)
		})
	})
}

func (s *namespaceMutationScan) withCallableParamFacts(
	params []Param,
	facts map[string]checkReachableParamFact,
	lambdas map[string][]Expression,
	walk func(),
) {
	previous := s.callableParams
	previousLambdas := s.callableLambdas
	s.callableParams = make(map[string][]*ScriptFunction, len(facts))
	for name, fact := range facts {
		if len(fact.callables) > 0 {
			s.callableParams[name] = fact.callables
		}
	}
	s.callableLambdas = make(map[string][]Expression, len(params)+len(lambdas))
	for _, param := range params {
		if param.Name != "" {
			s.callableLambdas[param.Name] = nil
		}
	}
	for name, values := range lambdas {
		s.callableLambdas[name] = values
	}
	defer func() {
		s.callableParams = previous
		s.callableLambdas = previousLambdas
	}()
	walk()
}

func (s *namespaceMutationScan) withCallableParamShadows(params []Param, walk func()) {
	previous := s.callableParams
	previousLambdas := s.callableLambdas
	shadowed := make(map[string]struct{}, len(params))
	for _, param := range params {
		if param.Name != "" {
			shadowed[param.Name] = struct{}{}
		}
	}
	s.callableParams = make(map[string][]*ScriptFunction, len(previous))
	for name, fns := range previous {
		if _, shadow := shadowed[name]; !shadow {
			s.callableParams[name] = fns
		}
	}
	s.callableLambdas = make(map[string][]Expression, len(previousLambdas))
	for name, lambdas := range previousLambdas {
		if _, shadow := shadowed[name]; !shadow {
			s.callableLambdas[name] = lambdas
		}
	}
	for name := range shadowed {
		s.callableLambdas[name] = nil
	}
	defer func() {
		s.callableParams = previous
		s.callableLambdas = previousLambdas
	}()
	walk()
}

func (s *namespaceMutationScan) scanLambdaBlock(block *BlockLiteral) {
	if block == nil {
		return
	}
	popScope := s.checker.pushBlockCheckScope(block)
	defer popScope()
	popNameScope := s.checker.pushBlockNameScope(block)
	defer popNameScope()
	for _, name := range block.ImplicitParams {
		s.checker.bindLocalTypeInCurrentFrame(name, nil)
	}
	s.withCallableParamShadows(block.Params, func() {
		s.withNominalReceiverParams(block.Params, true, func() {
			for _, param := range block.Params {
				if !s.expression(param.DefaultVal) {
					return
				}
				s.checker.recordParamBinding(param)
			}
			s.statements(block.Body)
		})
	})
}

func (s *namespaceMutationScan) withFunctionSelf(fn *ScriptFunction, walk func()) {
	previousClass := s.selfClass
	previousClassContext := s.selfClassContext
	s.selfClass = s.methodClasses[fn]
	_, s.selfClassContext = s.classMethodFns[fn]
	defer func() {
		s.selfClass = previousClass
		s.selfClassContext = previousClassContext
	}()
	walk()
}

func (s *namespaceMutationScan) withNominalReceiverParams(params []Param, inherit bool, walk func()) {
	previous := s.nominalReceivers
	receivers := make(map[string]*ClassDef, len(params))
	if inherit {
		for name, classDef := range previous {
			receivers[name] = classDef
		}
	}
	for _, param := range params {
		if param.Name == "" {
			continue
		}
		receivers[param.Name] = nil
		if param.Kind != ParamNormal && param.Kind != ParamKeyword {
			continue
		}
		if classDef := s.nominalReceiverClass(param.Type); classDef != nil {
			receivers[param.Name] = classDef
		}
	}
	s.nominalReceivers = receivers
	defer func() { s.nominalReceivers = previous }()
	walk()
}

func (s *namespaceMutationScan) nominalReceiverClass(ty *TypeExpr) *ClassDef {
	arms, ok := typeExprArms(ty, 0)
	if !ok || len(arms) == 0 {
		return nil
	}
	var resolved *ClassDef
	for _, arm := range arms {
		if arm.Kind == TypeNil {
			continue
		}
		if arm.Kind != TypeEnum {
			return nil
		}
		classDef, ok := s.classes[arm.Name]
		if !ok || classDef.IsModule || (resolved != nil && resolved != classDef) {
			return nil
		}
		resolved = classDef
	}
	return resolved
}

func (s *namespaceMutationScan) statements(statements []Statement) bool {
	for _, stmt := range statements {
		if !s.statement(stmt) || statementAlwaysExits(stmt) {
			return false
		}
	}
	return true
}

func (s *namespaceMutationScan) statementMayComplete(stmt Statement) bool {
	if s == nil || s.checker == nil {
		return true
	}
	return s.checker.statementMayCompleteForBinding(stmt)
}

func (s *namespaceMutationScan) statement(stmt Statement) bool {
	switch typed := stmt.(type) {
	case nil:
		return true
	case *AssignStmt:
		assignedValue := typed.Value
		setterReachable := true
		switch typed.Operator {
		case "":
			if !s.expression(typed.Value) || !s.assignmentTarget(typed.Target) {
				return false
			}
		case tokenOrAssign, tokenAndAssign:
			if !s.expression(typed.Target) {
				return false
			}
			truthy, known := s.checker.inferredConditionTruthiness(typed.Target)
			rhsReachable := !known ||
				(typed.Operator == tokenOrAssign && !truthy) ||
				(typed.Operator == tokenAndAssign && truthy)
			if !rhsReachable {
				setterReachable = false
				break
			}
			if !s.expression(typed.Value) {
				return !known
			}
		default:
			if !s.expression(typed.Target) || !s.expression(typed.Value) {
				return false
			}
			operatorValue := &BinaryExpr{
				Left:     typed.Target,
				Operator: typed.Operator,
				Right:    typed.Value,
				Position: typed.Pos(),
			}
			if !s.checker.binaryExpressionMayComplete(operatorValue) {
				return false
			}
			assignedValue = operatorValue
		}
		s.checker.withSuppressedWarnings(func() {
			s.checker.inferAssignStatementTypes("", typed)
		})
		if setterReachable {
			s.recordRuntimeNamespaceAssignment(typed.Target, assignedValue)
		}
		return s.statementMayComplete(typed)
	case *ExprStmt:
		return s.expression(typed.Expr)
	case *ReturnStmt:
		s.expression(typed.Value)
		return false
	case *RaiseStmt:
		if !s.expression(typed.Value) {
			return false
		}
		s.expression(typed.Message)
		return false
	case *BreakStmt:
		s.expression(typed.Value)
		return false
	case *NextStmt:
		s.expression(typed.Value)
		return false
	case *RetryStmt:
		return false
	case *IfStmt:
		return s.ifStatement(typed)
	case *ForStmt:
		if !s.expression(typed.Iterable) {
			return false
		}
		s.statements(typed.Body)
		return true
	case *WhileStmt:
		if !s.expression(typed.Condition) {
			return false
		}
		s.statements(typed.Body)
		return true
	case *UntilStmt:
		if !s.expression(typed.Condition) {
			return false
		}
		s.statements(typed.Body)
		return true
	case *TryStmt:
		bodyCompletes := s.statements(typed.Body)
		rescueCompletes := false
		selected, exact := s.checker.staticallySelectedRescue(typed.Body, typed.Rescues)
		if !statementsProvenNonRaising(typed.Body) && exact && selected >= 0 {
			clause := &typed.Rescues[selected]
			if len(clause.Body) > 0 {
				rescueCompletes = s.statements(clause.Body)
			}
		} else if !statementsProvenNonRaising(typed.Body) && !exact {
			for i := range typed.Rescues {
				body := typed.Rescues[i].Body
				if len(body) > 0 && s.statements(body) {
					rescueCompletes = true
				}
			}
		}
		if bodyCompletes {
			bodyCompletes = s.statements(typed.Else)
		}
		ensureCompletes := s.statements(typed.Ensure)
		return ensureCompletes && (bodyCompletes || rescueCompletes)
	case *FunctionStmt:
		// A nested definition's writes fire only when it is called, but a
		// missed invalidation is unsound while an extra one only widens, so
		// the walk stays conservative.
		s.withNominalReceiverParams(typed.Params, false, func() {
			for _, param := range typed.Params {
				s.expression(param.DefaultVal)
			}
			s.statements(typed.Body)
		})
		return true
	case *ClassStmt:
		return s.statements(typed.Body)
	}
	return true
}

func (c *scriptChecker) staticallyRaisedErrorKind(statements []Statement) (string, bool) {
	if len(statements) != 1 {
		return "", false
	}
	raise, ok := statements[0].(*RaiseStmt)
	if !ok {
		return "", false
	}
	if raise.Message == nil {
		if value, static := staticLiteralValue(raise.Value); static && value.Kind() == KindString {
			return runtimeErrorTypeBase, true
		}
		return "", false
	}
	if !staticRaiseErrorClass(raise) || !expressionProvenNonRaising(raise.Message) {
		return "", false
	}
	if message, static := staticLiteralValue(raise.Message); static && message.Kind() != KindString {
		return runtimeErrorTypeType, true
	}
	root := c.runtimeTypeRoot
	if root == nil {
		root = c.typeRoot
	}
	return raiseErrorTypeName(raise.Value, root)
}

func (c *scriptChecker) staticallyRaisedExpressionErrorKind(expr Expression) (string, bool) {
	call, ok := expr.(*CallExpr)
	if !ok || call == nil || callExpandsArguments(call) || call.BlockArg != nil || call.Block != nil {
		return "", false
	}
	if _, direct := call.Callee.(*Identifier); !direct {
		return "", false
	}
	for _, arg := range call.Args {
		if !expressionProvenNonRaising(arg) {
			return "", false
		}
	}
	for _, kwarg := range call.KwArgs {
		if !expressionProvenNonRaising(kwarg.Value) {
			return "", false
		}
	}
	target, resolved := c.resolveCallable(call)
	if !resolved || target.fn == nil || target.fn.owner != c.script {
		return "", false
	}
	plan := c.scriptCallBindingPlan(call, target)
	if !plan.bodyMayEnter {
		return "", false
	}
	for _, index := range plan.defaultParams {
		if index < 0 || index >= len(target.fn.Params) ||
			!expressionProvenNonRaising(target.fn.Params[index].DefaultVal) {
			return "", false
		}
	}
	return c.staticallyRaisedErrorKind(target.fn.Body)
}

func (c *scriptChecker) staticallySelectedRescue(
	body []Statement,
	rescues []RescueClause,
) (int, bool) {
	errorKind, exact := c.staticallyRaisedErrorKind(body)
	if !exact {
		return -1, false
	}
	for i := range rescues {
		if staticErrorKindMatchesRescue(errorKind, rescues[i].Ty) {
			return i, true
		}
	}
	return -1, true
}

func expressionProvenNonRaising(expr Expression) bool {
	switch expr.(type) {
	case nil, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral,
		*NilLiteral, *SymbolLiteral:
		return true
	default:
		return false
	}
}

func statementsProvenNonRaising(statements []Statement) bool {
	for _, stmt := range statements {
		exprStmt, ok := stmt.(*ExprStmt)
		if !ok || !expressionProvenNonRaising(exprStmt.Expr) {
			return false
		}
	}
	return true
}

func staticErrorKindMatchesRescue(errorKind string, rescueType *TypeExpr) bool {
	if rescueType == nil {
		return errorKind != runtimeErrorTypeLimit
	}
	return rescueTypeMatchesErrorKind(rescueType, errorKind)
}

func (s *namespaceMutationScan) assignmentTarget(target Expression) bool {
	switch typed := target.(type) {
	case nil, *Identifier, *IvarExpr, *ClassVarExpr:
		return true
	case *MemberExpr:
		return s.expression(typed.Object)
	case *IndexExpr:
		if !s.expression(typed.Object) {
			return false
		}
		for _, index := range typed.Indices {
			if !s.expression(index) {
				return false
			}
		}
		return true
	case *DestructureTarget:
		for _, element := range typed.Elements {
			if !s.assignmentTarget(element.Target) {
				return false
			}
		}
		return true
	default:
		return s.expression(target)
	}
}

func (s *namespaceMutationScan) ifStatement(stmt *IfStmt) bool {
	if stmt == nil || !s.expression(stmt.Condition) {
		return false
	}
	truthy, known := s.checker.inferredConditionTruthiness(stmt.Condition)
	mayComplete := false
	if !known || truthy {
		mayComplete = s.statements(stmt.Consequent)
		if known {
			return mayComplete
		}
	}
	if !known || !truthy {
		for _, branch := range stmt.ElseIf {
			if !s.expression(branch.Condition) {
				return mayComplete
			}
			branchTruthy, branchKnown := s.checker.inferredConditionTruthiness(branch.Condition)
			if !branchKnown || branchTruthy {
				mayComplete = s.statements(branch.Consequent) || mayComplete
				if branchKnown {
					return mayComplete
				}
			}
			if branchKnown && branchTruthy {
				return mayComplete
			}
		}
		mayComplete = s.statements(stmt.Alternate) || mayComplete
	}
	return mayComplete
}

func (s *namespaceMutationScan) recordRuntimeNamespaceAssignment(target, value Expression) {
	if destructure, ok := target.(*DestructureTarget); ok {
		values := destructureAssignmentExpressions(destructure, value)
		for i, element := range destructure.Elements {
			var elementValue Expression
			if i < len(values) {
				elementValue = values[i]
			}
			s.recordRuntimeNamespaceAssignment(element.Target, elementValue)
		}
		return
	}
	member, ok := target.(*MemberExpr)
	if !ok {
		return
	}
	var namespace string
	var classDef *ClassDef
	switch object := member.Object.(type) {
	case *Identifier:
		if object.Name == "self" {
			if s.selfClass == nil || !s.selfClassContext {
				return
			}
			classDef = s.selfClass
			namespace = s.selfClass.Name
		} else {
			classDef = s.classes[object.Name]
			namespace = object.Name
		}
	case *MemberExpr:
		ident, selfClass := object.Object.(*Identifier)
		if !selfClass || ident.Name != "self" || object.Property != "class" || s.selfClass == nil {
			return
		}
		classDef = s.selfClass
		namespace = s.selfClass.Name
	default:
		return
	}
	if classDef != nil {
		if setter := classDef.ClassMethods[member.Property+"="]; setter != nil {
			if setter.Private || setter.Protected &&
				(s.selfClass != classDef || !s.selfClassContext) {
				return
			}
			callee := *member
			callee.Property += "="
			call := &CallExpr{
				Callee:   &callee,
				Args:     []Expression{value},
				Position: member.Pos(),
			}
			s.scanFunctionCall(setter, call, staticCallable{
				name:       namespace + "." + callee.Property,
				fn:         setter,
				resolution: calleeMemberMethod,
			})
			return
		}
		if classDef.ClassMethods[member.Property] != nil {
			return
		}
	}
	s.out[namespace+"."+member.Property] = struct{}{}
}

func (s *namespaceMutationScan) expression(expr Expression) bool {
	return s.expressionWithAuto(expr, true)
}

func (s *namespaceMutationScan) expressionWithAuto(expr Expression, autoCall bool) bool {
	switch typed := expr.(type) {
	case nil:
		return true
	case *Identifier:
		if autoCall {
			s.functionReference(typed.Name)
			return s.checker.expressionMayCompleteForBinding(typed)
		}
		return true
	case *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral,
		*SymbolLiteral, *IvarExpr, *ClassVarExpr, *TypeLiteral:
		return true
	case *TryStmt, *IfStmt, *WhileStmt, *UntilStmt, *ForStmt:
		return s.statement(typed.(Statement))
	case *BlockLiteral:
		return true
	case *CallExpr:
		if !s.callCalleeExpression(typed) {
			return false
		}
		if staticNilSafeNavigationCall(typed) || s.checker.safeNavigationCallSkipsInferred(typed) {
			return true
		}
		argumentsAlwaysEvaluate := s.checker.safeNavigationArgumentsAlwaysEvaluateInferred(typed)
		argumentsMayBeSkipped := safeNavigationCallMaySkipArguments(typed) && !argumentsAlwaysEvaluate
		target, targetResolved := s.checker.resolveCallable(typed)
		dynamicCandidates := s.checker.captureDynamicCallCandidates(typed)
		deferForwardedTargets := callResolvesForwardedTargetAfterArguments(typed, target, targetResolved)
		var dynamicResolution checkDynamicCallResolution
		if !targetResolved && !deferForwardedTargets {
			dynamicResolution = s.checker.exactDynamicCallTargets(
				typed,
				target,
				false,
				dynamicCandidates,
			)
		}
		if s.checker.callCalleeLookupFails(
			typed,
			target,
			targetResolved,
			deferForwardedTargets,
			dynamicCandidates,
			dynamicResolution,
		) {
			return argumentsMayBeSkipped
		}
		positionalSplatSeen := false
		for i, arg := range typed.Args {
			expectation := expressionExpectation{}
			_, isSplat := arg.(*SplatArg)
			if targetResolved && !positionalSplatSeen && !isSplat {
				expectation = staticCallablePositionalArgumentExpectation(target, i)
			}
			if !s.callArgumentExpression(arg, expectation) ||
				!s.checker.positionalArgumentExpansionMaySucceed(arg) {
				return argumentsMayBeSkipped
			}
			positionalSplatSeen = positionalSplatSeen || isSplat
		}
		for _, kwarg := range typed.KwArgs {
			expectation := expressionExpectation{}
			if targetResolved && !kwarg.Splat {
				expectation = staticCallableKeywordArgumentExpectation(typed, target, kwarg.Name)
			}
			if !s.callArgumentExpression(kwarg.Value, expectation) ||
				!s.checker.keywordArgumentExpansionMaySucceed(kwarg) {
				return argumentsMayBeSkipped
			}
		}
		if !s.expressionWithAuto(typed.BlockArg, false) ||
			!s.checker.blockArgumentConversionMaySucceed(typed.BlockArg, s.checker.inferExpressionType(typed.BlockArg)) {
			return argumentsMayBeSkipped
		}
		if deferForwardedTargets {
			dynamicResolution = s.checker.exactDynamicCallTargets(
				typed,
				target,
				targetResolved,
				dynamicCandidates,
			)
		}
		s.callResolvedCallee(typed, target, targetResolved, dynamicResolution)
		if typed.Block != nil && s.resolvedCallBlockMayRun(
			typed,
			target,
			targetResolved,
			dynamicResolution,
		) {
			s.scanLambdaBlock(typed.Block)
		}
		return argumentsMayBeSkipped || s.checker.expressionMayCompleteForBinding(typed)
	case *SplatArg:
		return s.expression(typed.Value)
	case *UnaryExpr:
		return s.expression(typed.Right) && s.checker.unaryExpressionMayComplete(typed)
	case *BinaryExpr:
		if !s.expression(typed.Left) {
			return false
		}
		if binaryRightMayEvaluate(typed) && !s.checker.binaryRightUnreachable(typed) {
			if !s.expression(typed.Right) &&
				(binaryRightAlwaysEvaluates(typed) || s.checker.binaryRightAlwaysEvaluatesInferred(typed)) {
				return false
			}
		}
		return s.checker.expressionMayCompleteForBinding(typed)
	case *ConditionalExpr:
		if !s.expression(typed.Condition) {
			return false
		}
		truthy, known := s.checker.inferredConditionTruthiness(typed.Condition)
		if !known || truthy {
			s.expression(typed.Consequent)
		}
		if !known || !truthy {
			s.expression(typed.Alternate)
		}
		return s.checker.expressionMayCompleteForBinding(typed)
	case *IfExpr:
		if !s.expression(typed.Condition) {
			return false
		}
		truthy, known := s.checker.inferredConditionTruthiness(typed.Condition)
		if !known || truthy {
			s.expression(typed.Consequent)
		}
		falseReachable := !known || !truthy
		for _, branch := range typed.ElseIf {
			if !falseReachable || !s.expression(branch.Condition) {
				break
			}
			branchTruthy, branchKnown := s.checker.inferredConditionTruthiness(branch.Condition)
			if !branchKnown || branchTruthy {
				s.expression(branch.Result)
			}
			falseReachable = !branchKnown || !branchTruthy
		}
		if falseReachable {
			s.expression(typed.Alternate)
		}
		return s.checker.expressionMayCompleteForBinding(typed)
	case *RescueExpr:
		bodyCompletes := s.expression(typed.Body)
		if errorKind, exact := s.checker.staticallyRaisedExpressionErrorKind(typed.Body); exact &&
			!staticErrorKindMatchesRescue(errorKind, nil) {
			return bodyCompletes
		}
		s.expression(typed.Fallback)
		return s.checker.expressionMayCompleteForBinding(typed)
	case *RangeExpr:
		return s.expression(typed.Start) && s.expression(typed.End)
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if !s.expression(elem) {
				return false
			}
		}
		return true
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			if !s.expression(pair.Key) || !s.expression(pair.Value) {
				return false
			}
		}
		return true
	case *IndexExpr:
		if !s.expression(typed.Object) {
			return false
		}
		for _, index := range typed.Indices {
			if !s.expression(index) {
				return false
			}
		}
		call := &CallExpr{Callee: &MemberExpr{Object: typed.Object, Property: "[]", Position: typed.Pos()}, Args: typed.Indices, Position: typed.Pos()}
		s.callCallee(call)
		return s.checker.expressionMayCompleteForBinding(typed)
	case *MemberExpr:
		objectAutoCall := true
		if typed.Property == "call" && typeExprMayIncludeCallable(s.checker.inferExpressionType(typed.Object)) {
			objectAutoCall = false
		}
		if !s.expressionWithAuto(typed.Object, objectAutoCall) {
			return false
		}
		if autoCall {
			s.callCallee(&CallExpr{Callee: typed, Position: typed.Pos()})
			return s.checker.expressionMayCompleteForBinding(typed)
		}
		return true
	case *ScopeExpr:
		return s.expression(typed.Object)
	case *CaseExpr:
		if !s.expression(typed.Target) {
			return false
		}
		if result, known := s.checker.inferredCaseExpressionResult(typed); known {
			s.expression(result)
			return s.checker.expressionMayCompleteForBinding(typed)
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if !s.expression(value.Expr) ||
					!s.checker.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
					return false
				}
			}
			s.expression(clause.Result)
		}
		s.expression(typed.ElseExpr)
		return s.checker.expressionMayCompleteForBinding(typed)
	case *YieldExpr:
		for _, arg := range typed.Args {
			if !s.expression(arg) {
				return false
			}
		}
		return true
	case *InterpolatedString:
		return s.stringParts(typed.Parts)
	case *InterpolatedSymbol:
		return s.stringParts(typed.Parts)
	}
	return true
}

func (s *namespaceMutationScan) callArgumentExpression(
	expr Expression,
	expectation expressionExpectation,
) bool {
	autoCall := !expectation.includesCallable()
	if !autoCall {
		if callableExpr, bindable := s.checker.bareIdentifierCallableArgument(expr); bindable {
			if call, ok := callableExpr.(*CallExpr); ok {
				callableExpr = call.Callee
			}
			return s.expressionWithAuto(callableExpr, false)
		}
	}
	return s.expressionWithAuto(expr, autoCall)
}

func (s *namespaceMutationScan) callResolvedCallee(
	call *CallExpr,
	target staticCallable,
	resolved bool,
	dynamicResolution checkDynamicCallResolution,
) {
	if resolved {
		if target.fn != nil {
			s.scanFunctionCall(target.fn, call, target)
		}
		return
	}
	if dynamicResolution.exact {
		for _, candidate := range dynamicResolution.targets {
			s.scanFunctionCall(candidate.target.fn, candidate.call, candidate.target)
		}
		return
	}
	s.callCallee(call)
}

func (s *namespaceMutationScan) resolvedCallBlockMayRun(
	call *CallExpr,
	target staticCallable,
	resolved bool,
	dynamicResolution checkDynamicCallResolution,
) bool {
	if call == nil || call.Block == nil {
		return false
	}
	if resolved {
		if target.fn != nil {
			return s.functionCallBlockMayRun(call, target)
		}
		return s.checker.callMayEvaluateBlock(call)
	}
	if !dynamicResolution.exact {
		return true
	}
	for _, candidate := range dynamicResolution.targets {
		if s.functionCallBlockMayRun(candidate.call, candidate.target) {
			return true
		}
	}
	return false
}

func (s *namespaceMutationScan) functionCallBlockMayRun(
	call *CallExpr,
	target staticCallable,
) bool {
	return s.checker.scriptFunctionCallBlockMayRun(call, target)
}

func (s *namespaceMutationScan) callCalleeExpression(call *CallExpr) bool {
	if call == nil {
		return true
	}
	switch callee := call.Callee.(type) {
	case *Identifier:
		return true
	case *MemberExpr:
		objectAutoCall := true
		if callee.Property == "call" && typeExprMayIncludeCallable(s.checker.inferExpressionType(callee.Object)) {
			objectAutoCall = false
		}
		return s.expressionWithAuto(callee.Object, objectAutoCall)
	default:
		return s.expressionWithAuto(callee, false)
	}
}

func (s *namespaceMutationScan) callCallee(call *CallExpr) {
	if call == nil {
		return
	}
	if member, ok := call.Callee.(*MemberExpr); ok && member.Property == "call" {
		if ident, ok := member.Object.(*Identifier); ok {
			if _, bound := s.callableParams[ident.Name]; bound {
				s.functionReferenceWithCall(ident.Name, call)
				return
			}
			if _, bound := s.callableLambdas[ident.Name]; bound {
				s.functionReferenceWithCall(ident.Name, call)
				return
			}
		}
	}
	target, resolved := s.checker.resolveCallable(call)
	if resolved {
		if target.fn != nil {
			s.scanFunctionCall(target.fn, call, target)
		}
		return
	}
	candidates := s.checker.captureDynamicCallCandidates(call)
	resolution := s.checker.exactDynamicCallTargets(call, target, false, candidates)
	if resolution.exact {
		for _, candidate := range resolution.targets {
			s.scanFunctionCall(candidate.target.fn, candidate.call, candidate.target)
		}
		return
	}
	switch callee := call.Callee.(type) {
	case *Identifier:
		s.functionReferenceWithCall(callee.Name, call)
	case *MemberExpr:
		if ident, ok := callee.Object.(*Identifier); ok && ident.Name == "self" {
			s.selfCallReference(callee.Property, call)
			return
		}
		s.memberReference(callee)
	}
}

func (s *namespaceMutationScan) stringParts(parts []StringPart) bool {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			if !s.expression(exprPart.Expr) {
				return false
			}
		}
	}
	return true
}

func collectBindingTarget(target Expression, out map[string]struct{}) {
	switch typed := target.(type) {
	case *Identifier:
		out[typed.Name] = struct{}{}
	case *MemberExpr:
		if memberName, ok := runtimeNamespaceMemberName(typed); ok {
			out[memberName] = struct{}{}
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			collectBindingTarget(element.Target, out)
		}
	}
}

func runtimeNamespaceMemberName(target Expression) (string, bool) {
	member, ok := target.(*MemberExpr)
	if !ok {
		return "", false
	}
	obj, ok := member.Object.(*Identifier)
	if !ok {
		return "", false
	}
	return obj.Name + "." + member.Property, true
}

func (c *scriptChecker) add(function string, pos Position, format string, args ...any) {
	if c.orderIndependentOnly {
		return
	}
	c.addOrderIndependent(function, pos, format, args...)
}

// addOrderIndependent records a warning that holds no matter which function
// runs first or what state earlier calls established, so it survives
// order-independent-only mode. Undefined-name and literal block parameter
// warnings use it directly; every state-sensitive warning goes through add.
func (c *scriptChecker) addOrderIndependent(function string, pos Position, format string, args ...any) {
	c.warnings = append(c.warnings, CheckWarning{
		Function: function,
		Pos:      pos,
		Message:  fmt.Sprintf(format, args...),
	})
}
