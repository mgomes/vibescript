package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
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
	return dedupeCheckWarnings(checker.warnings)
}

type scriptChecker struct {
	script                     *Script
	callOptions                CallOptions
	optionGlobals              map[string]Value
	optionGlobalsOverride      bool
	typeRoot                   *Env
	runtimeTypeRoot            *Env
	hostGlobals                map[string]struct{}
	warnings                   []CheckWarning
	scopes                     []map[string]struct{}
	localTypes                 []checkTypeFrame
	localClassValues           []checkClassValueFrame
	evaluatedIfClassFacts      map[*IfExpr][]string
	typePoison                 map[string]struct{}
	staticValuePoison          map[string]struct{}
	staticValueDependents      map[string]map[string]checkBindingEdge
	valueAliases               map[string]map[string]checkBindingEdge
	typeAliases                map[string]map[string]checkBindingEdge
	containerIdentityAliases   map[string]map[string]checkBindingEdge
	containerSelections        map[string]checkContainerSelection
	degradedContainerBindings  map[string]struct{}
	mutationRegionDepth        int
	speculativeInference       int
	oneShotIvarRefinementDepth int
	expressionStatementRoot    Expression
	callArgumentFacts          map[Expression]*TypeExpr
	callArgumentClassValues    map[Expression][]string
	callArgumentCallables      map[Expression][]*ScriptFunction
	callArgumentSelfBindings   map[Expression]checkCallableSelfBinding
	callArgumentStaticValues   map[Expression][]Expression
	callArgumentStaticChoices  map[Expression]checkStaticChoiceFact
	callArgumentSplatSources   map[Expression]checkCallSplatSource
	callArrayReceiverLength    checkArrayReceiverLength
	shapeFieldSources          map[*TypeExpr]map[string]checkValueSource
	evaluatedBlockValues       map[Expression][]capturedBlockLiteralValue
	evaluatedHashDefaults      map[Expression][]directCoreHashDefaultCapture
	evaluatedDestructureFacts  map[Expression]capturedDestructureValueFact
	destructureProjectionFacts map[Expression]capturedDestructureValueFact
	variadicParamStaticValues  map[string][]Expression
	localBindingGenerations    map[string]uint64
	reachableParamFacts        map[string]checkReachableParamFact
	reachableBindingPlan       *scriptCallBindingPlan
	reachableBlockKnownAbsent  bool
	pendingBindingParams       map[string]struct{}
	deferredReturnSites        *[]deferredReturnSite
	constructorReturnExitSites *[]checkStateSnapshot
	exceptionExitSites         *[]checkStateSnapshot
	expressionExitSites        *[]checkStateSnapshot
	nonLocalReturnExitSites    *[]checkStateSnapshot
	ensureExitSites            *[]checkStateSnapshot
	retryExitSites             *[]checkStateSnapshot
	implicitReturnLeaves       map[Statement]struct{}
	implicitReturnStates       map[Statement]checkStateSnapshot
	returnAnalyses             map[returnSummaryCacheKey]functionReturnAnalysis
	summaryInProgress          map[returnSummaryCacheKey]struct{}
	bindingCompletionProbes    map[Expression]struct{}
	blockLiteralBindingDepth   int
	returnCollector            *returnSummaryCollector
	blockResultCollector       *returnSummaryCollector
	blockLocalReturnCollector  *returnSummaryCollector
	blockLocalBreakCollector   *returnSummaryCollector
	summaryYieldCollector      *returnSummaryCollector
	summaryYieldBlock          *BlockLiteral
	summaryYieldsActive        bool
	summaryBlockAvailable      bool
	pinnedExpressionFacts      map[Expression]*TypeExpr
	pinnedExpressionSources    map[Expression]checkValueSourceCapture
	pinnedInstanceOrigins      map[Expression]checkInstanceOriginsCapture
	constructorInstanceFacts   map[Expression]checkInstanceClassFact
	constructorIvarFacts       map[Expression]map[string]*TypeExpr
	widenedIvarFacts           map[string]struct{}
	assignmentReceiverCapture  *checkAssignmentReceiverCapture
	requiredModules            map[string]struct{}
	runtimeModules             map[string]struct{}
	runtimeNamespaceMembers    map[string]struct{}
	opaqueClassConstants       bool
	classConstantContext       checkClassConstantEffects
	classConstantCaptures      []checkClassConstantEffects
	loopExitEffects            *checkLoopExitEffects
	moduleEntries              map[string]moduleEntry
	moduleExportValues         map[string]Value
	moduleCheckedFunctions     map[string]struct{}
	moduleCheckContext         string
	moduleCaller               *moduleContext
	moduleExportRoot           *Env
	runtimeTypeRootParent      *Env
	checkReachableCalls        bool
	checkedReachableFuncs      map[string]struct{}
	reachableFuncQueue         []reachableFunction
	selfScope                  bool
	selfClass                  *ClassDef
	selfClassContext           bool
	selfScopeFnClasses         map[*ScriptFunction]*ClassDef
	selfScopeClassFns          map[*ScriptFunction]struct{}
	localNameUnions            []map[string]struct{}
	liveLocalNames             []map[string]struct{}
	localCallBypassScopes      []map[string]int
	nameFactsCache             *checkNameFacts
	selfScopeFns               map[*ScriptFunction]struct{}
	orderIndependentOnly       bool
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
	// expressionReturnsNonLocally reports that an exact retained proc call
	// failed to produce a value only because it returned from the enclosing
	// function. Expression wrappers propagate the marker to the statement
	// boundary instead of misclassifying that exit as a rescueable failure.
	expressionReturnsNonLocally bool
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
	label            string
	fn               *ScriptFunction
	runtimeState     checkRuntimeState
	paramFacts       map[string]checkReachableParamFact
	bindingPlan      *scriptCallBindingPlan
	blockKnownAbsent bool
}

type checkReachableParamFact struct {
	typeExpr              *TypeExpr
	classNames            []string
	callables             []*ScriptFunction
	callableIdentityExact bool
	selfCallables         []*ScriptFunction
	selfCallablesCaptured bool
	selfCallableAmbiguous bool
	staticVals            []Expression
	instanceOrigins       []Expression
	containerIdentity     string
	usesDefault           bool
}

// Synthetic reachable facts correlate queued constructor, instance-method,
// and getter checks without exposing checker-only state as script parameters.
const (
	reachableConstructorOriginFact = "\x00constructor-origin"
	reachableInstanceOriginFact    = "\x00instance-origin"
	reachableRescuedInstanceFact   = "\x00rescued-instance"
	reachableGetterOriginFact      = "\x00getter-origin"
)

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

type checkAssignmentReceiverCapture struct {
	target            Expression
	candidates        checkDynamicCallCandidates
	receiverType      *TypeExpr
	staticValues      []Expression
	staticValuesExact bool
	rootName          string
	rootGeneration    uint64
	captured          bool
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

type checkCallSplatSource struct {
	identity     []capturedContainerRoot
	alternatives []Expression
	evaluation   Expression
}

type checkCallableSelfBinding struct {
	functions []*ScriptFunction
	ambiguous bool
}

type checkStaticChoiceFact struct {
	source  checkCallSplatSource
	indices []int
}

type checkArrayReceiverCapture struct {
	name        string
	generation  uint64
	alternative Expression
	length      int
	literal     bool
	exact       bool
}

type checkArrayReceiverLength struct {
	length int
	exact  bool
}

func (c *scriptChecker) callStaticValueAlternatives(expr Expression) ([]Expression, bool) {
	if values, captured := c.callArgumentStaticValues[expr]; captured {
		return append([]Expression(nil), values...), len(values) > 0
	}
	return c.staticValueExpressionAlternatives(expr)
}

func literalAlternativeValues(alternatives []Expression, exact bool) ([]Value, bool) {
	if !exact || len(alternatives) == 0 {
		return nil, false
	}
	values := make([]Value, 0, len(alternatives))
	for _, alternative := range alternatives {
		value, literal := staticLiteralValue(alternative)
		if !literal {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func (c *scriptChecker) staticLiteralValueAlternatives(expr Expression) ([]Value, bool) {
	return literalAlternativeValues(c.staticValueExpressionAlternatives(expr))
}

func (c *scriptChecker) evaluatedStaticLiteralValueAlternatives(expr Expression) ([]Value, bool) {
	return literalAlternativeValues(c.evaluatedStaticValueExpressionAlternatives(expr))
}

func (c *scriptChecker) callStaticLiteralValueAlternatives(expr Expression) ([]Value, bool) {
	return literalAlternativeValues(c.callStaticValueAlternatives(expr))
}

type scriptCallBindingPlan struct {
	defaultParams   []int
	boundParamCount int
	bindingStarts   bool
	exactBindings   bool
	bodyMayEnter    bool
}

type scriptParamBindingInput struct {
	usesDefault bool
	mayBind     bool
}

type defaultBindingFact struct {
	values   []Value
	inferred *TypeExpr
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
		c.inferAssignStatementTypes("", stmt, nil, nil)
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
		if c.exactIterableProvablyEmpty(typed.Iterable) {
			c.recordBindingTarget(typed.Target)
			c.degradeLocalTypesForBindings(nil, typed.Target)
			c.recordLocalBindings(typed.Body)
			break
		}
		if c.isolatedCollectInference {
			elemType := c.forTargetElementType(typed)
			c.recordLiveStatementNames(typed.Body)
			c.degradeLocalTypesForBindings(typed.Body, typed.Target)
			c.bindForTargetType(typed, elemType)
			c.widenRepeatedRegionIvarFacts(typed.Body)
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
				c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
				c.widenRepeatedLoopIvarFacts(typed.Condition, typed.Body)
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
				c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
				c.widenRepeatedLoopIvarFacts(typed.Condition, typed.Body)
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
				c.poisonEscapedCallValue(member.Object, true)
			}
			for _, arg := range typed.Args {
				c.poisonEscapedCallValue(arg, true)
			}
			for _, kwarg := range typed.KwArgs {
				c.poisonEscapedCallValue(kwarg.Value, true)
			}
		}
	case *MemberExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
		if c.isolatedCollectInference && !c.memberDispatchPreservesReceiverFacts(typed) {
			c.poisonEscapedCallValue(typed.Object, true)
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
		if !c.expressionMayCompleteForBinding(typed.Start) ||
			!c.rangeEndpointConversionMaySucceed(typed.Start) {
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

func (c *scriptChecker) callCalleeExpressionMayCompleteForBinding(call *CallExpr) bool {
	if call == nil {
		return true
	}
	switch callee := call.Callee.(type) {
	case *Identifier:
		return true
	case *MemberExpr:
		expectation := expressionExpectation{}
		if callee.Property == "call" && typeExprMayIncludeCallable(c.inferExpressionType(callee.Object)) {
			expectation = typeExpressionExpectation(checkTypeFunction)
		}
		return c.expressionMayCompleteForBindingWithExpectation(callee.Object, expectation)
	default:
		return c.expressionMayCompleteForBindingWithExpectation(
			callee,
			typeExpressionExpectation(checkTypeFunction),
		)
	}
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
	c.checkSpacedParenMemberCalls()
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
	c.seedInstanceIvarFacts(fn)

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
	returnType := fn.ReturnTy
	if fn.Accessor == functionAccessorGetter && !c.checkReachableCalls {
		returnType = nil
	}
	c.checkStatements(label, returnType, fn.Body)
	if returnType != nil {
		c.checkImplicitReturn(label, returnType, fn.Body, fn.Pos)
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

// enqueueReachableFunction covers runtime-generated calls without a CallExpr,
// such as host entry and auto-invocation paths; those calls always pass nil as
// their block. Spelled call expressions use enqueueReachableFunctionForCall.
func (c *scriptChecker) enqueueReachableFunction(label string, fn *ScriptFunction) {
	c.enqueueReachableFunctionWithContext(label, fn, nil, true)
}

func (c *scriptChecker) enqueueReachableFunctionForCall(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	call *CallExpr,
) {
	c.enqueueReachableFunctionWithContext(
		label,
		fn,
		paramFacts,
		c.callBlockKnownAbsent(call),
	)
}

func (c *scriptChecker) callBlockKnownAbsent(call *CallExpr) bool {
	if call == nil || call.Block != nil {
		return false
	}
	return call.BlockArg == nil ||
		typeExprIsNilOnly(c.inferExpressionType(call.BlockArg))
}

func (c *scriptChecker) enqueueReachableFunctionWithContext(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	blockKnownAbsent bool,
) {
	if !c.checkReachableCalls || fn == nil || fn.owner != c.script {
		return
	}
	if !c.markReachableFunctionCheckedWithContext(fn, paramFacts, blockKnownAbsent) {
		return
	}
	c.reachableFuncQueue = append(c.reachableFuncQueue, reachableFunction{
		label:            label,
		fn:               fn,
		runtimeState:     c.snapshotRuntimeState(),
		paramFacts:       cloneReachableParamFacts(paramFacts),
		blockKnownAbsent: blockKnownAbsent,
	})
}

func (c *scriptChecker) enqueueReachableFunctionBinding(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	plan scriptCallBindingPlan,
) {
	c.enqueueReachableFunctionBindingWithContext(label, fn, paramFacts, plan, true)
}

func (c *scriptChecker) enqueueReachableFunctionBindingForCall(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	plan scriptCallBindingPlan,
	call *CallExpr,
) {
	c.enqueueReachableFunctionBindingWithContext(
		label,
		fn,
		paramFacts,
		plan,
		c.callBlockKnownAbsent(call),
	)
}

func (c *scriptChecker) enqueueReachableFunctionBindingWithContext(
	label string,
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	plan scriptCallBindingPlan,
	blockKnownAbsent bool,
) {
	if !c.checkReachableCalls || fn == nil || fn.owner != c.script {
		return
	}
	key := c.reachableFunctionCheckContextKey(fn, paramFacts, blockKnownAbsent) + "\x00binding:" +
		strconv.FormatBool(plan.bindingStarts) + ":" +
		strconv.FormatBool(plan.exactBindings) + ":" +
		strconv.FormatBool(plan.bodyMayEnter) + ":" +
		strconv.Itoa(plan.boundParamCount) + ":" + fmt.Sprint(plan.defaultParams)
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
		label:            label,
		fn:               fn,
		runtimeState:     c.snapshotRuntimeState(),
		paramFacts:       cloneReachableParamFacts(paramFacts),
		bindingPlan:      &planCopy,
		blockKnownAbsent: blockKnownAbsent,
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
		bindingStarts = plan.bindingStarts
		// A bare implicit-self call has no captured argument facts, so its
		// binding plan must carry exact omitted-default execution into the body.
		if plan.bindingStarts {
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
	return c.markReachableFunctionCheckedWithContext(fn, paramFacts, false)
}

func (c *scriptChecker) markReachableFunctionCheckedWithContext(
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	blockKnownAbsent bool,
) bool {
	if fn == nil {
		return false
	}
	if c.checkedReachableFuncs == nil {
		c.checkedReachableFuncs = make(map[string]struct{})
	}
	key := c.reachableFunctionCheckContextKey(fn, paramFacts, blockKnownAbsent)
	if _, ok := c.checkedReachableFuncs[key]; ok {
		return false
	}
	c.checkedReachableFuncs[key] = struct{}{}
	return true
}

func (c *scriptChecker) reachableFunctionCheckKey(
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
) string {
	return c.reachableFunctionCheckContextKey(fn, paramFacts, false)
}

func (c *scriptChecker) reachableFunctionCheckContextKey(
	fn *ScriptFunction,
	paramFacts map[string]checkReachableParamFact,
	blockKnownAbsent bool,
) string {
	return fmt.Sprintf(
		"%p\x00%s\x00%s\x00block-absent:%t",
		fn,
		c.runtimeCheckContextKey(),
		reachableParamFactsKey(paramFacts),
		blockKnownAbsent,
	)
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
		fact.selfCallables = append([]*ScriptFunction(nil), fact.selfCallables...)
		fact.staticVals = append([]Expression(nil), fact.staticVals...)
		fact.instanceOrigins = append([]Expression(nil), fact.instanceOrigins...)
		clone[name] = fact
	}
	return clone
}

func (c *scriptChecker) reachableCallInstanceFacts(
	call *CallExpr,
	target staticCallable,
	facts map[string]checkReachableParamFact,
) map[string]checkReachableParamFact {
	return c.reachableCallInstanceFactsWithConstructorOrigin(call, target, facts, call)
}

func (c *scriptChecker) reachableCallInstanceFactsWithConstructorOrigin(
	call *CallExpr,
	target staticCallable,
	facts map[string]checkReachableParamFact,
	constructorOrigin Expression,
) map[string]checkReachableParamFact {
	if call == nil || target.fn == nil {
		return facts
	}
	add := func(name string, origins []Expression) {
		origins = normalizeCheckExpressionIdentities(origins)
		if len(origins) == 0 {
			return
		}
		if facts == nil {
			facts = make(map[string]checkReachableParamFact)
		} else {
			facts = cloneReachableParamFacts(facts)
		}
		facts[name] = checkReachableParamFact{
			staticVals: origins,
		}
	}
	if target.constructor {
		add(reachableConstructorOriginFact, []Expression{constructorOrigin})
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return facts
	}
	origins, exact := c.instanceValueOrigins(member.Object)
	if exact {
		if target.fn.Accessor == functionAccessorGetter {
			add(reachableGetterOriginFact, origins)
		} else if !target.constructor {
			add(reachableInstanceOriginFact, origins)
			if c.expressionExitSites != nil || c.exceptionExitSites != nil {
				add(reachableRescuedInstanceFact, origins)
			}
		}
	}
	return facts
}

func (c *scriptChecker) recordUninitializedConstructorIvarFacts(
	origin Expression,
	className string,
) {
	if origin == nil || className == "" {
		return
	}
	classDef := c.script.classes[className]
	if classDef == nil {
		return
	}
	facts := make(map[string]*TypeExpr)
	for _, method := range classDef.Methods {
		if method.Accessor == functionAccessorNone || method.AccessorName == "" {
			continue
		}
		if _, ty := propertyContract(classDef, method.AccessorName); ty == nil {
			continue
		}
		facts[method.AccessorName] = checkTypeNil
	}
	if c.constructorIvarFacts == nil {
		c.constructorIvarFacts = make(map[Expression]map[string]*TypeExpr)
	}
	c.constructorIvarFacts[origin] = facts
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
		key.WriteString(typeFactKey(fact.typeExpr))
		key.WriteByte(':')
		key.WriteString(strings.Join(fact.classNames, ","))
		key.WriteByte(':')
		for _, fn := range fact.callables {
			fmt.Fprintf(&key, "%p,", fn)
		}
		key.WriteByte(':')
		key.WriteString(strconv.FormatBool(fact.callableIdentityExact))
		key.WriteByte(':')
		for _, fn := range fact.selfCallables {
			fmt.Fprintf(&key, "%p,", fn)
		}
		key.WriteByte(':')
		key.WriteString(strconv.FormatBool(fact.selfCallablesCaptured))
		key.WriteByte(':')
		key.WriteString(strconv.FormatBool(fact.selfCallableAmbiguous))
		key.WriteByte(':')
		for _, value := range fact.staticVals {
			fmt.Fprintf(&key, "%T:%p,", value, value)
		}
		key.WriteByte(':')
		for _, origin := range fact.instanceOrigins {
			fmt.Fprintf(&key, "%T:%p,", origin, origin)
		}
		key.WriteByte(':')
		key.WriteString(fact.containerIdentity)
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
		nextIndex := 0
		// Helpers can reveal additional contexts for a constructor origin
		// after a dependent getter is already queued. Drain the non-getter
		// graph first so every reachable constructor state has been joined.
		for i, queued := range c.reachableFuncQueue {
			if queued.fn.Accessor != functionAccessorGetter {
				nextIndex = i
				break
			}
		}
		next := c.reachableFuncQueue[nextIndex]
		c.reachableFuncQueue = append(
			c.reachableFuncQueue[:nextIndex],
			c.reachableFuncQueue[nextIndex+1:]...,
		)
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
		previousBlockKnownAbsent := c.reachableBlockKnownAbsent
		c.reachableParamFacts = next.paramFacts
		c.reachableBindingPlan = next.bindingPlan
		c.reachableBlockKnownAbsent = next.blockKnownAbsent
		c.checkFunction(next.label, next.fn)
		c.reachableParamFacts = previousParamFacts
		c.reachableBindingPlan = previousBindingPlan
		c.reachableBlockKnownAbsent = previousBlockKnownAbsent
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
	effects   checkClassConstantEffects
	seen      bool
	breakSeen bool
	nextSeen  bool
}

type checkBindingEdge struct {
	fromGeneration uint64
	toGeneration   uint64
}

type checkContainerSelection struct {
	key        string
	generation uint64
}

type checkModuleCollectionState struct {
	root    *Env
	modules map[string]struct{}
}

type checkScopeState struct {
	defined            []map[string]struct{}
	types              []checkTypeFrame
	classValues        []checkClassValueFrame
	containerAlias     checkNameRelations
	containerIdentity  checkNameRelations
	staticDependents   checkNameRelations
	valueAlias         checkNameRelations
	containerSelection map[string]checkContainerSelection
	degradedContainers map[string]struct{}
}

type checkNameRelations map[string]map[string]struct{}

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

type checkLoopFlow struct {
	effects                checkClassConstantEffects
	bodyFallsThrough       bool
	breakSeen              bool
	nextSeen               bool
	nonLocalReturnExitSeen bool
}

func (c *scriptChecker) checkLoopStatements(
	function string,
	returnType *TypeExpr,
	statements []Statement,
) checkLoopFlow {
	previous := c.loopExitEffects
	previousNonLocalReturnExitSites := c.nonLocalReturnExitSites
	previousBlockResultCollector := c.blockResultCollector
	previousBlockLocalBreakCollector := c.blockLocalBreakCollector
	var exits checkLoopExitEffects
	var nonLocalReturnExitSites []checkStateSnapshot
	c.loopExitEffects = &exits
	c.nonLocalReturnExitSites = &nonLocalReturnExitSites
	// A nested loop consumes both next and break before either can become the
	// surrounding block or lambda's result.
	c.blockResultCollector = nil
	c.blockLocalBreakCollector = nil
	defer func() {
		c.loopExitEffects = previous
		c.nonLocalReturnExitSites = previousNonLocalReturnExitSites
		c.blockResultCollector = previousBlockResultCollector
		c.blockLocalBreakCollector = previousBlockLocalBreakCollector
	}()
	bodyFallsThrough := c.checkStatements(function, returnType, statements)
	if bodyFallsThrough {
		mergeCheckClassConstantEffects(&exits.effects, c.currentClassConstantEffects())
	}
	if previousNonLocalReturnExitSites != nil {
		*previousNonLocalReturnExitSites = append(
			*previousNonLocalReturnExitSites,
			nonLocalReturnExitSites...,
		)
	}
	return checkLoopFlow{
		effects:                exits.effects,
		bodyFallsThrough:       bodyFallsThrough,
		breakSeen:              exits.breakSeen,
		nextSeen:               exits.nextSeen,
		nonLocalReturnExitSeen: len(nonLocalReturnExitSites) > 0,
	}
}

func (c *scriptChecker) captureLoopExitClassConstantEffects(breaks bool) {
	if c.loopExitEffects == nil {
		return
	}
	c.loopExitEffects.seen = true
	if breaks {
		c.loopExitEffects.breakSeen = true
	} else {
		c.loopExitEffects.nextSeen = true
	}
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
		types:              c.snapshotLocalTypes(),
		classValues:        c.snapshotLocalClassValues(),
		containerAlias:     c.snapshotBindingRelations(c.typeAliases),
		containerIdentity:  c.snapshotContainerIdentityRelations(),
		staticDependents:   c.snapshotBindingRelations(c.staticValueDependents),
		valueAlias:         c.snapshotBindingRelations(c.valueAliases),
		containerSelection: c.snapshotContainerSelections(),
		degradedContainers: cloneCheckStringSet(c.degradedContainerBindings),
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
	} else {
		c.scopes = make([]map[string]struct{}, len(state.defined))
		for i, scope := range state.defined {
			c.scopes[i] = cloneCheckScope(scope)
		}
	}
	c.restoreContainerAliasRelations(state.containerAlias)
	c.restoreStaticValueDependencyRelations(state.staticDependents)
	c.restoreValueAliasRelations(state.valueAlias)
	c.restoreContainerIdentityRelations(state.containerIdentity)
	c.restoreContainerSelections(state.containerSelection)
	c.degradedContainerBindings = cloneCheckStringSet(state.degradedContainers)
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
	c.mergeScopeBindingRelations(states)
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
		c.seedInstanceIvarFacts(fn)

		previousConstructorReturnExitSites := c.constructorReturnExitSites
		var constructorReturnExitSites []checkStateSnapshot
		if fn.Name == "initialize" {
			if _, captured := c.reachableParamFacts[reachableConstructorOriginFact]; captured {
				c.constructorReturnExitSites = &constructorReturnExitSites
			}
		} else if _, captured := c.reachableParamFacts[reachableInstanceOriginFact]; captured {
			c.constructorReturnExitSites = &constructorReturnExitSites
		}
		defer func() {
			c.constructorReturnExitSites = previousConstructorReturnExitSites
		}()

		previousInstanceExceptionExitSites := c.exceptionExitSites
		var instanceExceptionExitSites []checkStateSnapshot
		if _, captured := c.reachableParamFacts[reachableRescuedInstanceFact]; captured {
			c.exceptionExitSites = &instanceExceptionExitSites
		}
		defer func() {
			c.exceptionExitSites = previousInstanceExceptionExitSites
		}()

		c.linkReachableParamAliases(fn.Params)
		for i, param := range fn.Params {
			expectation := bindingDefaultExpectation(param)
			defaultRuns := c.reachableParamDefaultRuns(param)
			_, defaultExecutionExact := c.reachableParamFacts[param.Name]
			if c.reachableBindingPlan != nil {
				defaultRuns = slices.Contains(c.reachableBindingPlan.defaultParams, i)
				defaultExecutionExact = c.reachableBindingPlan.exactBindings
			}
			if defaultRuns {
				if !defaultExecutionExact {
					c.oneShotIvarRefinementDepth++
				}
				c.checkExpressionWithExpectation(
					label,
					param.DefaultVal,
					expectation,
				)
				if !defaultExecutionExact {
					c.oneShotIvarRefinementDepth--
				}
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
			c.checkIvarParamBinding(label, fn, param)
			if c.reachableBindingPlan != nil && i >= c.reachableBindingPlan.boundParamCount {
				return
			}
			c.recordParamBinding(param)
			c.applyReachableParamFact(param)
		}
		if c.reachableBindingPlan != nil && !c.reachableBindingPlan.bodyMayEnter {
			return
		}
		returnType := fn.ReturnTy
		if fn.Accessor == functionAccessorGetter && !c.checkReachableCalls {
			returnType = nil
		}
		bodyFallsThrough := c.checkStatements(label, returnType, fn.Body)
		if returnType != nil {
			c.checkImplicitReturn(label, returnType, fn.Body, fn.Pos)
		}
		c.captureReachableConstructorIvarFacts(fn, bodyFallsThrough, constructorReturnExitSites)
		c.captureReachableInstanceMethodIvarFacts(fn, bodyFallsThrough, constructorReturnExitSites)
		c.captureRescuedInstanceMethodIvarFacts(
			fn,
			instanceExceptionExitSites,
			bodyFallsThrough || len(constructorReturnExitSites) > 0,
		)
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
		if origins, exact := c.instanceValueOrigins(param.DefaultVal); exact {
			fact.instanceOrigins = origins
		} else if classNames, exact := c.classValueExpressionNames(param.DefaultVal); exact {
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
	if len(fact.instanceOrigins) > 0 {
		c.bindLocalExactValueFact(param.Name, checkLocalValueFact{
			instanceOrigins: fact.instanceOrigins,
		})
	} else if len(fact.classNames) > 0 {
		c.bindLocalClassValues(param.Name, fact.classNames)
	} else if len(fact.callables) > 0 {
		c.bindLocalCallableValues(param.Name, fact.callables)
	} else if len(fact.staticVals) > 0 {
		c.bindLocalStaticValues(param.Name, fact.staticVals)
	}
	if fact.usesDefault && param.DefaultVal != nil {
		// Defaults run after earlier parameters are bound. A container-valued
		// default such as b = a therefore retains the same runtime object and
		// must participate in later mutation invalidation.
		c.linkContainerAssignmentAlias(param.Name, param.DefaultVal, fact.typeExpr)
	}
}

func (c *scriptChecker) linkReachableParamAliases(params []Param) {
	identities := make(map[string]string)
	for _, param := range params {
		if param.Name == "" {
			continue
		}
		fact, ok := c.reachableParamFacts[param.Name]
		if !ok || fact.containerIdentity == "" || fact.usesDefault {
			continue
		}
		other, linked := identities[fact.containerIdentity]
		if !linked {
			identities[fact.containerIdentity] = param.Name
			continue
		}
		c.linkContainerIdentityAlias(other, param.Name)
		c.linkStaticValueAlias(other, param.Name)
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
	if c.expressionReturnsNonLocally {
		c.expressionReturnsNonLocally = false
	} else {
		if c.expressionExitSites != nil {
			c.captureExpressionExitState()
		} else {
			c.captureExceptionExitState()
		}
		c.captureEnsureExitState()
	}
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
				c.recordReturnSummaryResult(c.returnCollector, nil, checkTypeNil)
			} else {
				c.recordReturnSummaryResult(
					c.returnCollector,
					typed.Value,
					c.inferExpressionType(typed.Value),
				)
			}
		}
		if c.blockLocalReturnCollector != nil {
			result := checkTypeNil
			if typed.Value != nil {
				result = c.inferExpressionType(typed.Value)
			}
			c.blockLocalReturnCollector.record(result)
		}
		if c.constructorReturnExitSites != nil &&
			c.deferredReturnSites == nil &&
			c.blockLocalReturnCollector == nil {
			c.captureFailureExitState(c.constructorReturnExitSites)
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
		if c.blockLocalBreakCollector != nil {
			result := checkTypeNil
			if typed.Value != nil {
				result = c.inferExpressionType(typed.Value)
			}
			c.blockLocalBreakCollector.record(result)
		}
		c.captureLoopExitClassConstantEffects(true)
		c.captureEnsureExitState()
	case *NextStmt:
		if !c.checkExpression(function, typed.Value) {
			c.recordNonCompletingExpression()
			return
		}
		if c.blockResultCollector != nil {
			result := checkTypeNil
			if typed.Value != nil {
				result = c.inferExpressionType(typed.Value)
			}
			c.blockResultCollector.record(result)
		}
		c.captureLoopExitClassConstantEffects(false)
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
		previousEvaluatedFacts := c.evaluatedDestructureFacts
		c.evaluatedDestructureFacts = make(map[Expression]capturedDestructureValueFact)
		defer func() { c.evaluatedDestructureFacts = previousEvaluatedFacts }()
		// Compound and logical writes select their receiver before the getter,
		// selectors, and right side run. Capture the declared container fact at
		// that point so later effects cannot erase the bound the eventual write
		// targets. Plain assignment evaluates its value first; the capture stays
		// valid unless an inline block can actually rebind the local.
		var assignmentReceiverFact *TypeExpr
		var assignmentReceiverName string
		var logicalTargetFact *logicalAssignmentTargetFact
		var receiver Expression
		switch target := typed.Target.(type) {
		case *IndexExpr:
			receiver = target.Object
		case *MemberExpr:
			receiver = target.Object
		}
		if ident, ok := receiver.(*Identifier); ok {
			assignmentReceiverFact = c.localTypeFor(ident.Name)
			assignmentReceiverName = ident.Name
		}
		if assignmentReceiverFact == nil {
			assignmentReceiverName = ""
		}
		targetMayWrite := true
		inferWrite := true
		switch typed.Operator {
		case "":
			// Plain assignment evaluates its value before the target receiver and
			// selectors, and it dispatches only the setter (never [] or a getter).
			expectation := c.assignmentValueExpectation(typed.Target, typed.Value)
			valueCompleted := c.withAssignmentLocalCallBypass(
				typed.Target,
				typed.Value,
				func() bool {
					return c.checkExpressionWithExpectation(
						function,
						typed.Value,
						expectation,
					)
				},
			)
			if !valueCompleted {
				c.recordNonCompletingExpression()
				return
			}
			c.captureEvaluatedDestructureFactWithExpectation(typed.Value, expectation)
			// Plain assignment evaluates its value first. An inline block in
			// that value can rebind the receiver local before the target
			// resolves; ordinary escapes cannot rebind a caller local, so the
			// condition-time bound remains useful for the write diagnosis.
			if assignmentReceiverFact != nil &&
				expressionMayRunBlockLiteralAssigning(typed.Value, assignmentReceiverName) {
				assignmentReceiverFact = nil
				c.poisonLocalType(assignmentReceiverName)
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
			if ivar, ok := typed.Target.(*IvarExpr); ok &&
				c.ivarAssignmentMayComplete(ivar, typed.Value) &&
				!c.ivarWriteProvablyCompletes(ivar.Name, typed.Value) {
				c.captureNonCompletingExpressionArm()
			}
			if destructure, ok := typed.Target.(*DestructureTarget); ok {
				if !c.replayDestructureAssignment(function, destructure, typed.Value) {
					c.recordNonCompletingExpression()
					return
				}
				c.captureImplicitReturnState(typed)
				return
			}
			if !c.checkPlainAssignmentTarget(function, typed.Target, typed.Value) {
				if _, ivar := typed.Target.(*IvarExpr); ivar {
					c.inferAssignStatementTypes(function, typed, assignmentReceiverFact, nil)
				}
				c.recordNonCompletingExpression()
				return
			}
		case tokenOrAssign, tokenAndAssign:
			targetCompleted, setterReceiver := c.withAssignmentReceiverCapture(
				typed.Target,
				func() bool { return c.checkLogicalAssignmentTarget(function, typed.Target) },
			)
			if !targetCompleted {
				c.recordNonCompletingExpression()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Target)
			truthy, known := c.logicalAssignmentTargetTruthiness(
				typed.Target,
				assignmentReceiverFact,
			)
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
			logicalTargetFact = &logicalAssignmentTargetFact{
				rhsReachable: rhsReachable,
				known:        known,
			}
			switch target := typed.Target.(type) {
			case *Identifier:
				logicalTargetFact.current = c.localTypeFor(target.Name)
				if typed.Operator == tokenOrAssign && !known {
					logicalTargetFact.priorAliasTransfer = c.captureContainerAliasTransfer(target)
				}
			case *IvarExpr:
				logicalTargetFact.current = c.localTypeFor(ivarFactKey(target.Name))
			}
			if rhsReachable {
				runtimeState := c.snapshotRuntimeState()
				scopeState := c.snapshotScopeState()
				if ivar, ok := typed.Target.(*IvarExpr); ok && !known {
					// Walk the RHS under the arm that actually evaluates it. A
					// failure snapshot must retain that selected pre-RHS fact,
					// while RHS side effects remain free to replace it.
					c.narrowLogicalIvarFact(ivar.Name, typed.Operator == tokenAndAssign)
				}
				opaqueDispatch := false
				var indexSetterType *TypeExpr
				switch target := typed.Target.(type) {
				case *IndexExpr:
					indexSetterType = setterReceiver.receiverType
					opaqueDispatch = c.instanceDispatchHasOpaqueClassConstantEffects(
						indexSetterType,
						"[]=",
					)
				case *MemberExpr:
					opaqueDispatch = c.memberSetterHasOpaqueClassConstantEffects(target)
				}
				expectation := c.assignmentValueExpectation(typed.Target, typed.Value)
				rhsCompleted := c.withAssignmentLocalCallBypass(
					typed.Target,
					typed.Value,
					func() bool {
						return c.checkExpressionWithExpectation(
							function,
							typed.Value,
							expectation,
						)
					},
				)
				c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
				if !rhsCompleted {
					if rhsAlwaysEvaluates {
						c.recordNonCompletingExpression()
						return
					}
					c.captureNonCompletingExpressionArm()
					targetMayWrite = false
					inferWrite = false
					c.restoreRuntimeState(runtimeState)
					c.restoreScopeState(scopeState)
					if ivar, ok := typed.Target.(*IvarExpr); ok {
						c.narrowLogicalIvarFact(ivar.Name, typed.Operator == tokenOrAssign)
					}
					break
				}
				c.captureEvaluatedDestructureFactWithExpectation(typed.Value, expectation)
				if indexSetterType != nil {
					c.enqueueReachableInstanceDispatch(indexSetterType, "[]=")
				}
				setterCompletes := c.checkAssignmentSetterDispatch(
					function,
					typed.Target,
					typed.Value,
					setterReceiver,
				)
				if opaqueDispatch {
					c.markOpaqueClassConstants()
				}
				if !setterCompletes {
					if _, ivar := typed.Target.(*IvarExpr); ivar {
						c.inferAssignStatementTypes(function, typed, nil, logicalTargetFact)
					}
					if rhsAlwaysEvaluates {
						c.recordNonCompletingExpression()
						return
					}
					c.captureNonCompletingExpressionArm()
					targetMayWrite = false
					inferWrite = false
					c.restoreRuntimeState(runtimeState)
					c.restoreScopeState(scopeState)
					if ivar, ok := typed.Target.(*IvarExpr); ok {
						c.narrowLogicalIvarFact(ivar.Name, typed.Operator == tokenOrAssign)
					}
					break
				}
				if ivar, ok := typed.Target.(*IvarExpr); ok &&
					!c.ivarWriteProvablyCompletes(ivar.Name, typed.Value) {
					c.captureNonCompletingExpressionArm()
				}
				if !rhsAlwaysEvaluates {
					if ivar, ok := typed.Target.(*IvarExpr); ok {
						c.commitLogicalIvarWritingArm(
							function,
							typed,
							ivar,
							logicalTargetFact.current,
						)
						inferWrite = false
					}
					evaluatedScopeState := c.snapshotScopeState()
					c.restoreRuntimeStatePreservingClassConstantEffects(runtimeState)
					c.restoreScopeState(scopeState)
					if ivar, ok := typed.Target.(*IvarExpr); ok {
						c.narrowLogicalIvarFact(ivar.Name, typed.Operator == tokenOrAssign)
					}
					skippedScopeState := c.snapshotScopeState()
					c.mergeScopeStates(
						scopeState,
						[]checkScopeState{skippedScopeState, evaluatedScopeState},
					)
				}
			}
		default:
			targetCompleted, setterReceiver := c.withAssignmentReceiverCapture(
				typed.Target,
				func() bool { return c.checkExpression(function, typed.Target) },
			)
			if !targetCompleted {
				c.recordNonCompletingExpression()
				return
			}
			c.collectRuntimeRequireCallExportsFromExpression(typed.Target)
			operatorType := c.inferExpressionType(typed.Target)
			c.pinExpressionFact(typed.Target, operatorType)
			opaqueOperator := c.binaryDispatchHasOpaqueClassConstantEffects(operatorType, typed.Operator)
			opaqueSetter := false
			var indexSetterType *TypeExpr
			switch target := typed.Target.(type) {
			case *IndexExpr:
				indexSetterType = setterReceiver.receiverType
				opaqueSetter = c.instanceDispatchHasOpaqueClassConstantEffects(
					indexSetterType,
					"[]=",
				)
			case *MemberExpr:
				opaqueSetter = c.memberSetterHasOpaqueClassConstantEffects(target)
			}
			valueCompleted := c.withAssignmentLocalCallBypass(
				typed.Target,
				typed.Value,
				func() bool {
					return c.checkExpression(function, typed.Value)
				},
			)
			if !valueCompleted {
				c.recordNonCompletingExpression()
				return
			}
			c.pinExpressionFact(typed.Value, c.inferExpressionType(typed.Value))
			c.captureEvaluatedDestructureFactOnce(typed.Value)
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
			operatorCompletes := true
			operatorProvablyCompletes := false
			c.withEvaluatedDestructureArgumentFacts([]Expression{typed.Value}, func() {
				dispatch := c.binaryScriptDispatch(operatorValue, operatorType)
				if dispatch.mayReject() {
					c.captureNonCompletingExpressionArm()
				}
				if dispatch.mayRunScript() {
					c.widenRegionIvarFacts(c.scriptDispatchIvarEffects(dispatch))
					c.captureNonCompletingExpressionArm()
				}
				operatorCompletes = c.binaryExpressionMayCompleteWithReceiver(
					operatorValue,
					operatorType,
				)
				operatorProvablyCompletes = c.binaryExpressionProvablyCompletes(operatorValue)
				if operatorCompletes {
					c.captureEvaluatedDestructureFact(operatorValue)
				}
			})
			if !operatorCompletes {
				if _, ivar := typed.Target.(*IvarExpr); !ivar {
					c.inferAssignStatementTypes(function, typed, nil, nil)
				}
				c.recordNonCompletingExpression()
				return
			}
			c.pinExpressionFact(operatorValue, c.inferExpressionType(operatorValue))
			if indexSetterType != nil {
				c.enqueueReachableInstanceDispatch(indexSetterType, "[]=")
			}
			setterCompletes := c.checkAssignmentSetterDispatch(
				function,
				typed.Target,
				operatorValue,
				setterReceiver,
			)
			if opaqueSetter {
				c.markOpaqueClassConstants()
			}
			if !setterCompletes {
				c.recordNonCompletingExpression()
				return
			}
			if ivar, ok := typed.Target.(*IvarExpr); ok &&
				(!operatorProvablyCompletes ||
					!c.ivarWriteProvablyCompletes(ivar.Name, operatorValue)) {
				c.captureNonCompletingExpressionArm()
			}
		}
		if inferWrite {
			c.inferAssignStatementTypes(function, typed, assignmentReceiverFact, logicalTargetFact)
		}
		if targetMayWrite {
			c.recordRuntimeBindingTarget(typed.Target)
		}
		c.recordBindingTarget(typed.Target)
		c.captureImplicitReturnState(typed)
	case *ExprStmt:
		// A statement-level expression discards its value, so a mutator
		// call's returned receiver cannot escape through it; array mutator
		// fact preservation keys on this root.
		previousRoot := c.expressionStatementRoot
		c.expressionStatementRoot = typed.Expr
		completed := c.checkExpression(function, typed.Expr)
		c.expressionStatementRoot = previousRoot
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
				c.captureNonCompletingExpressionArm()
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
		if c.exactIterableProvablyEmpty(typed.Iterable) {
			c.recordBindingTarget(typed.Target)
			c.degradeLocalTypesForBindings(nil, typed.Target)
			c.recordLocalBindings(typed.Body)
			return
		}
		elemType := c.forTargetElementType(typed)
		c.recordLiveStatementNames(typed.Body)
		c.degradeLocalTypesForBindings(typed.Body, typed.Target)
		c.recordBindingTarget(typed.Target)
		c.bindForTargetType(typed, elemType)
		c.widenRepeatedRegionIvarFacts(typed.Body)
		bodyRuntimeState := c.snapshotRuntimeState()
		bodyScopeState := c.snapshotScopeState()
		c.mutationRegionDepth++
		loopFlow := c.checkLoopStatements(function, returnType, typed.Body)
		c.mutationRegionDepth--
		bodyExitScopeState := c.snapshotScopeState()
		c.restoreRuntimeState(bodyRuntimeState)
		c.applyClassConstantEffects(loopFlow.effects)
		c.restoreScopeState(bodyScopeState)
		c.mergeScopeBindingRelations([]checkScopeState{bodyScopeState, bodyExitScopeState})
		c.degradeLocalTypesForBindings(nil, typed.Target)
		c.recordLocalBindings(typed.Body)
		if c.exactIterableProvablyNonEmpty(typed.Iterable) &&
			loopFlow.nonLocalReturnExitSeen &&
			!loopFlow.bodyFallsThrough &&
			!loopFlow.breakSeen &&
			!loopFlow.nextSeen {
			c.stmtNoFallthroughInferred = true
		}
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
			c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
			c.widenRepeatedLoopIvarFacts(typed.Condition, typed.Body)
			bodyRuntimeState := c.snapshotRuntimeState()
			bodyScopeState := c.snapshotScopeState()
			c.applyLoopEntryTypeRefinements(conditionScopeState.types, conditionRefinedScopeState.types)
			var bodyExitScopeState checkScopeState
			bodyWalked := false
			var loopFlow checkLoopFlow
			if c.collectRuntimeConditionOutcomeEffects(typed.Condition, true) {
				c.mutationRegionDepth++
				loopFlow = c.checkLoopStatements(function, returnType, typed.Body)
				c.mutationRegionDepth--
				bodyExitScopeState = c.snapshotScopeState()
				bodyWalked = true
				c.restoreRuntimeState(bodyRuntimeState)
				c.applyClassConstantEffects(loopFlow.effects)
			} else {
				c.restoreRuntimeState(bodyRuntimeState)
			}
			c.restoreScopeState(bodyScopeState)
			if bodyWalked {
				c.mergeScopeBindingRelations([]checkScopeState{bodyScopeState, bodyExitScopeState})
				if truthy, known := staticExpressionTruthiness(typed.Condition); known &&
					truthy && loopFlow.nonLocalReturnExitSeen &&
					!loopFlow.breakSeen {
					c.stmtNoFallthroughInferred = true
				}
			}
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
			c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
			c.widenRepeatedLoopIvarFacts(typed.Condition, typed.Body)
			bodyRuntimeState := c.snapshotRuntimeState()
			bodyScopeState := c.snapshotScopeState()
			c.applyLoopEntryTypeRefinements(conditionScopeState.types, conditionRefinedScopeState.types)
			var bodyExitScopeState checkScopeState
			bodyWalked := false
			var loopFlow checkLoopFlow
			if c.collectRuntimeConditionOutcomeEffects(typed.Condition, false) {
				c.mutationRegionDepth++
				loopFlow = c.checkLoopStatements(function, returnType, typed.Body)
				c.mutationRegionDepth--
				bodyExitScopeState = c.snapshotScopeState()
				bodyWalked = true
				c.restoreRuntimeState(bodyRuntimeState)
				c.applyClassConstantEffects(loopFlow.effects)
			} else {
				c.restoreRuntimeState(bodyRuntimeState)
			}
			c.restoreScopeState(bodyScopeState)
			if bodyWalked {
				c.mergeScopeBindingRelations([]checkScopeState{bodyScopeState, bodyExitScopeState})
				if truthy, known := staticExpressionTruthiness(typed.Condition); known &&
					!truthy && loopFlow.nonLocalReturnExitSeen &&
					!loopFlow.breakSeen {
					c.stmtNoFallthroughInferred = true
				}
			}
		}
		c.recordLocalBindings(typed.Body)
	case *TryStmt:
		selectedRescue, rescueSelectionExact := c.staticallySelectedRescue(typed.Body, typed.Rescues)
		rescueBodiesReachable := !statementsProvenNonRaising(typed.Body)
		ensureAlwaysExits := blockAlwaysExits(typed.Ensure)
		previousBlockResultCollector := c.blockResultCollector
		var protectedBlockResultCollector *returnSummaryCollector
		if len(typed.Ensure) > 0 && previousBlockResultCollector != nil {
			protectedBlockResultCollector = &returnSummaryCollector{}
			c.blockResultCollector = protectedBlockResultCollector
		}
		previousBlockLocalReturnCollector := c.blockLocalReturnCollector
		var protectedBlockLocalReturnCollector *returnSummaryCollector
		if len(typed.Ensure) > 0 && previousBlockLocalReturnCollector != nil {
			if previousBlockLocalReturnCollector == previousBlockResultCollector {
				protectedBlockLocalReturnCollector = protectedBlockResultCollector
			} else {
				protectedBlockLocalReturnCollector = &returnSummaryCollector{}
			}
			c.blockLocalReturnCollector = protectedBlockLocalReturnCollector
		}
		previousBlockLocalBreakCollector := c.blockLocalBreakCollector
		var protectedBlockLocalBreakCollector *returnSummaryCollector
		if len(typed.Ensure) > 0 && previousBlockLocalBreakCollector != nil {
			switch previousBlockLocalBreakCollector {
			case previousBlockResultCollector:
				protectedBlockLocalBreakCollector = protectedBlockResultCollector
			case previousBlockLocalReturnCollector:
				protectedBlockLocalBreakCollector = protectedBlockLocalReturnCollector
			default:
				protectedBlockLocalBreakCollector = &returnSummaryCollector{}
			}
			c.blockLocalBreakCollector = protectedBlockLocalBreakCollector
		}
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
		transformExceptionExitsThroughEnsure := len(typed.Ensure) > 0 && previousExceptionExitSites != nil
		var bodyExceptionExitSites []checkStateSnapshot
		if len(typed.Rescues) > 0 || transformExceptionExitsThroughEnsure {
			c.exceptionExitSites = &bodyExceptionExitSites
		}
		var pendingExceptionExitSites []checkStateSnapshot
		exceptionExitTarget := previousExceptionExitSites
		if transformExceptionExitsThroughEnsure {
			exceptionExitTarget = &pendingExceptionExitSites
		}
		previousExpressionExitSites := c.expressionExitSites
		transformExpressionExitsThroughEnsure := len(typed.Ensure) > 0 && previousExpressionExitSites != nil
		localizeExpressionExits := len(typed.Rescues) > 0 && previousExpressionExitSites != nil ||
			transformExpressionExitsThroughEnsure
		var bodyExpressionExitSites []checkStateSnapshot
		if localizeExpressionExits {
			c.expressionExitSites = &bodyExpressionExitSites
		}
		var pendingExpressionExitSites []checkStateSnapshot
		expressionExitTarget := previousExpressionExitSites
		if transformExpressionExitsThroughEnsure {
			expressionExitTarget = &pendingExpressionExitSites
		}
		previousNonLocalReturnExitSites := c.nonLocalReturnExitSites
		localizeNonLocalReturns := len(typed.Rescues) > 0 ||
			len(typed.Ensure) > 0 && previousNonLocalReturnExitSites != nil
		var bodyNonLocalReturnExitSites []checkStateSnapshot
		if localizeNonLocalReturns {
			c.nonLocalReturnExitSites = &bodyNonLocalReturnExitSites
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
		if len(typed.Rescues) > 0 || transformExceptionExitsThroughEnsure {
			c.exceptionExitSites = exceptionExitTarget
		}
		if localizeExpressionExits {
			c.expressionExitSites = expressionExitTarget
		}
		if localizeNonLocalReturns {
			c.nonLocalReturnExitSites = previousNonLocalReturnExitSites
		}
		bodyHasFailureExit := len(bodyExceptionExitSites) > 0 ||
			len(bodyExpressionExitSites) > 0
		if !bodyFallsThrough && !bodyHasFailureExit &&
			len(bodyNonLocalReturnExitSites) > 0 {
			rescueBodiesReachable = false
		}
		if len(typed.Ensure) == 0 && previousNonLocalReturnExitSites != nil {
			*previousNonLocalReturnExitSites = append(
				*previousNonLocalReturnExitSites,
				bodyNonLocalReturnExitSites...,
			)
		}
		if tryBodyExceptionsMayEscape(typed.Rescues, selectedRescue, rescueSelectionExact) {
			if exceptionExitTarget != nil {
				*exceptionExitTarget = append(*exceptionExitTarget, bodyExceptionExitSites...)
			}
			if expressionExitTarget != nil {
				*expressionExitTarget = append(*expressionExitTarget, bodyExpressionExitSites...)
			}
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
			bodyFailureExitSites := make([]checkStateSnapshot, 0,
				len(bodyExceptionExitSites)+len(bodyExpressionExitSites))
			bodyFailureExitSites = append(bodyFailureExitSites, bodyExceptionExitSites...)
			bodyFailureExitSites = append(bodyFailureExitSites, bodyExpressionExitSites...)
			if len(bodyFailureExitSites) == 0 {
				c.restoreRuntimeState(baseRuntimeState)
				c.applyClassConstantEffects(bodyEffects)
				c.restoreScopeState(baseScopeState)
			} else {
				runtimeStates := make([]checkRuntimeState, 0, len(bodyFailureExitSites))
				scopeStates := make([]checkScopeState, 0, len(bodyFailureExitSites))
				for _, site := range bodyFailureExitSites {
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
		if protectedBlockResultCollector != nil {
			c.blockResultCollector = previousBlockResultCollector
		}
		if protectedBlockLocalReturnCollector != nil {
			c.blockLocalReturnCollector = previousBlockLocalReturnCollector
		}
		if protectedBlockLocalBreakCollector != nil {
			c.blockLocalBreakCollector = previousBlockLocalBreakCollector
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
		for _, site := range pendingExceptionExitSites {
			mergeRuntimeStates = append(mergeRuntimeStates, site.runtimeState)
			mergeScopeStates = append(mergeScopeStates, site.scopeState)
		}
		for _, site := range pendingExpressionExitSites {
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
			if transformExceptionExitsThroughEnsure {
				c.exceptionExitSites = previousExceptionExitSites
			}
			if transformExpressionExitsThroughEnsure {
				c.expressionExitSites = previousExpressionExitSites
			}
			c.nonLocalReturnExitSites = previousNonLocalReturnExitSites
			previousContext := cloneCheckClassConstantEffects(c.classConstantContext)
			mergeCheckClassConstantEffects(&c.classConstantContext, ensureEffects)
			ensureFallsThrough = c.checkStatements(function, returnType, typed.Ensure)
			c.classConstantContext = previousContext
			if transformExceptionExitsThroughEnsure && ensureFallsThrough &&
				len(pendingExceptionExitSites) > 0 {
				c.captureExceptionExitState()
			}
			if transformExpressionExitsThroughEnsure && ensureFallsThrough &&
				len(pendingExpressionExitSites) > 0 {
				c.captureExpressionExitState()
			}
			if ensureFallsThrough && previousNonLocalReturnExitSites != nil &&
				len(bodyNonLocalReturnExitSites) > 0 {
				c.captureFailureExitState(previousNonLocalReturnExitSites)
			}
			if ensureFallsThrough && protectedLoopExitEffects != nil && protectedLoopExitEffects.seen {
				mergeCheckClassConstantEffects(
					&protectedLoopExitEffects.effects,
					c.currentClassConstantEffects(),
				)
				previousLoopExitEffects.seen = true
				previousLoopExitEffects.breakSeen = previousLoopExitEffects.breakSeen ||
					protectedLoopExitEffects.breakSeen
				previousLoopExitEffects.nextSeen = previousLoopExitEffects.nextSeen ||
					protectedLoopExitEffects.nextSeen
				mergeCheckClassConstantEffects(
					&previousLoopExitEffects.effects,
					protectedLoopExitEffects.effects,
				)
			}
		}
		if ensureFallsThrough {
			previousBlockResultCollector.mergeResultArms(protectedBlockResultCollector)
			if protectedBlockLocalReturnCollector != protectedBlockResultCollector {
				previousBlockLocalReturnCollector.mergeResultArms(protectedBlockLocalReturnCollector)
			}
			if protectedBlockLocalBreakCollector != protectedBlockResultCollector &&
				protectedBlockLocalBreakCollector != protectedBlockLocalReturnCollector {
				previousBlockLocalBreakCollector.mergeResultArms(protectedBlockLocalBreakCollector)
			}
		}
		// An ensure the walk proves always exits replaces every deferred
		// body return, even when the proof is inferred rather than
		// syntactic, so those arms must not widen the summary.
		if c.constructorReturnExitSites != nil &&
			armCapture &&
			previousSites == nil &&
			ensureFallsThrough &&
			len(deferredSites) > 0 {
			c.captureFailureExitState(c.constructorReturnExitSites)
		}
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
	runtimeState      checkRuntimeState
	scopeState        checkScopeState
	typePoison        map[string]struct{}
	staticValuePoison map[string]struct{}
}

func (c *scriptChecker) captureExceptionExitState() {
	c.captureFailureExitState(c.exceptionExitSites)
}

func (c *scriptChecker) captureExpressionExitState() {
	c.captureFailureExitState(c.expressionExitSites)
}

func (c *scriptChecker) captureNonLocalReturnExitState() {
	c.captureFailureExitState(c.nonLocalReturnExitSites)
	c.captureEnsureExitState()
	if c.deferredReturnSites == nil {
		return
	}
	*c.deferredReturnSites = append(*c.deferredReturnSites, deferredReturnSite{
		runtimeState: c.snapshotRuntimeState(),
		scopeState:   c.snapshotScopeState(),
	})
}

func (c *scriptChecker) captureFailureExitState(sites *[]checkStateSnapshot) {
	if sites == nil {
		return
	}
	*sites = append(*sites, checkStateSnapshot{
		runtimeState:      c.snapshotRuntimeState(),
		scopeState:        c.snapshotScopeState(),
		typePoison:        cloneCheckStringSet(c.typePoison),
		staticValuePoison: cloneCheckStringSet(c.staticValuePoison),
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

// captureNonCompletingExpressionArm preserves a failed arm before its parent
// expression restores another arm. An inline rescue consumes the failure;
// otherwise it propagates to the surrounding rescue and ensure.
func (c *scriptChecker) captureNonCompletingExpressionArm() {
	if c.expressionReturnsNonLocally {
		c.expressionReturnsNonLocally = false
		return
	}
	if c.expressionExitSites != nil {
		c.captureExpressionExitState()
		return
	}
	c.captureExceptionExitState()
	c.captureEnsureExitState()
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
		if site.stmt == nil {
			continue
		}
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

func (c *scriptChecker) checkLogicalAssignmentTarget(
	function string,
	target Expression,
) bool {
	if _, local := target.(*Identifier); local {
		return c.checkExpressionWithAuto(function, target, false)
	}
	return c.checkExpression(function, target)
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
		if callableExpr, bindable := bareIdentifierCallableValue(expr); bindable {
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
			c.captureEvaluatedDestructureFact(element)
			c.pinExpressionValueSource(element)
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
			c.pinExpressionFact(pair.Key, c.inferExpressionType(pair.Key))
			valueExpectation := expressionExpectation{}
			if key, ok := staticLiteralValue(pair.Key); ok {
				valueExpectation = typeExpressionExpectation(hashLiteralValueType(expectation.ty, key))
			}
			if !c.checkExpressionWithExpectation(function, pair.Value, valueExpectation) {
				return false
			}
			c.pinExpressionFact(
				pair.Value,
				c.inferExpressionTypeWithExpectation(pair.Value, valueExpectation),
			)
			c.pinExpressionValueSource(pair.Value)
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
	if !completed && !c.expressionReturnsNonLocally && c.expressionExitSites != nil {
		c.captureExpressionExitState()
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
			if !c.pureCallArgument(typed) {
				if dispatch, exact := c.implicitSelfIdentifierDispatch(typed); exact {
					if dispatch.mayRunScript() {
						c.widenRegionIvarFacts(c.scriptDispatchIvarEffects(dispatch))
					}
				} else {
					c.widenUnsetInstanceIvarFacts()
				}
			}
			return c.autoInvokedIdentifierMayComplete(typed)
		}
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if !c.checkExpressionWithAuto(function, elem, true) {
				return false
			}
			c.captureEvaluatedDestructureFact(elem)
			c.pinExpressionValueSource(elem)
		}
	case *HashLiteral:
		// A dual-reading braced group evaluates as a shape unless one of its
		// type names is shadowed, so its identifier values are type spellings
		// rather than variable reads and must not warn as undefined.
		if typed.ShapeType != nil && !c.hashShapeStaticallyShadowed(typed) {
			return true
		}
		for _, pair := range typed.Pairs {
			if !c.checkExpressionWithAuto(function, pair.Key, true) {
				return false
			}
			c.pinExpressionFact(pair.Key, c.inferExpressionType(pair.Key))
			if !c.checkExpressionWithAuto(function, pair.Value, true) {
				return false
			}
			c.pinExpressionFact(pair.Value, c.inferExpressionType(pair.Value))
			c.pinExpressionValueSource(pair.Value)
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
		delete(c.evaluatedBlockValues, typed)
		delete(c.evaluatedHashDefaults, typed)
		// The receiver's nil-ness resolves from the facts at its evaluation
		// point, before member dispatch poisons the receiver's own facts.
		callSkipsInferred := c.safeNavigationCallSkipsInferred(typed)
		argumentsAlwaysEvaluate := c.safeNavigationArgumentsAlwaysEvaluateInferred(typed)
		var invokedLambda *BlockLiteral
		if member, ok := typed.Callee.(*MemberExpr); ok && member.Property == "call" {
			invokedLambda = c.resolveImmediateLambdaBlock(member.Object)
		}
		preTarget, preTargetResolved := c.resolveCallable(typed)
		preBlockCapturingBuiltin := c.callTargetsBlockCapturingBuiltin(
			typed,
			preTarget,
			preTargetResolved,
		)
		if member, ok := typed.Callee.(*MemberExpr); ok && preBlockCapturingBuiltin {
			// Proc and Hash are namespace values here, not zero-argument
			// callables. Evaluating the member callee must not auto-invoke
			// their identifiers before the constructor is classified.
			if !c.checkExpressionWithAuto(function, member.Object, false) {
				return false
			}
			c.captureAssignmentReceiver(member)
		} else if !c.checkExpressionWithAuto(function, typed.Callee, false) {
			return false
		}
		if staticNilSafeNavigationCall(typed) || callSkipsInferred {
			return true
		}
		var evaluatedStoredBlocks []capturedBlockLiteralValue
		evaluatedStoredBlocksExact := false
		if member, ok := typed.Callee.(*MemberExpr); ok && member.Property == "call" {
			evaluatedStoredBlocks, evaluatedStoredBlocksExact = c.capturedBlockLiteralValueAlternatives(member.Object)
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
		// The exact current-self effect path can resolve calls that the
		// general call checker deliberately leaves dynamic. Recover that
		// target only for evaluation-time callable identity; argument
		// checking below continues to use the general resolution.
		argumentTarget := target
		argumentTargetResolved := targetResolved
		if !argumentTargetResolved {
			switch callee := typed.Callee.(type) {
			case *Identifier:
				if dispatch, exact := c.implicitSelfCallDispatch(typed); exact &&
					len(dispatch.targets) == 1 {
					argumentTarget = dispatch.targets[0].target
					argumentTargetResolved = true
				}
			case *MemberExpr:
				argumentTarget, argumentTargetResolved = c.explicitSelfMemberCallable(callee)
			}
		}
		blockCapturingBuiltin := c.callTargetsBlockCapturingBuiltin(
			typed,
			target,
			targetResolved,
		)
		// A member callee's receiver evaluates before any argument runs, so
		// the fact the container mutator checks its writes against is
		// captured now: an argument that escapes or reads the same local
		// must not erase the bound the receiver was evaluated under.
		var receiverFact *TypeExpr
		var receiverLengthCapture checkArrayReceiverCapture
		if member, ok := typed.Callee.(*MemberExpr); ok {
			if ident, ok := member.Object.(*Identifier); ok {
				receiverFact = c.localTypeFor(ident.Name)
			} else {
				receiverFact = c.inferExpressionType(member.Object)
			}
			receiverLengthCapture = c.captureArrayReceiverLength(member.Object)
		}
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
		if evaluatedStoredBlocksExact {
			opaqueCallEffects = false
		}
		// Arguments evaluate left to right before the call dispatches, so
		// each argument's inferred type and retained callable identity are
		// captured at its own evaluation point: a mutating earlier argument
		// (h.delete(:name)) poisons its container's facts for later arguments,
		// while a mutating later argument cannot erase or replace the facts
		// an earlier argument was evaluated under. checkCall and the effect
		// scanner consume the captured facts afterwards.
		argumentFacts := make(map[Expression]*TypeExpr, len(typed.Args)+len(typed.KwArgs))
		argumentClassValues := make(map[Expression][]string, len(typed.Args)+len(typed.KwArgs))
		argumentCallables := make(map[Expression][]*ScriptFunction, len(typed.Args)+len(typed.KwArgs))
		argumentSelfBindings := make(
			map[Expression]checkCallableSelfBinding,
			len(typed.Args)+len(typed.KwArgs),
		)
		argumentStaticValues := make(map[Expression][]Expression, len(typed.Args)+len(typed.KwArgs))
		argumentStaticChoices := make(map[Expression]checkStaticChoiceFact)
		argumentSplatSources := make(map[Expression]checkCallSplatSource)
		argumentRetainedAliases := make(map[Expression]checkRetainedContainerCapture, len(typed.Args))
		argumentSplatOrigins := make(map[Expression][]*SplatArg)
		var argumentSelfScan *namespaceMutationScan
		captureArgumentFacts := func(
			expr Expression,
			expectation expressionExpectation,
			autoCall bool,
			retainsCallable bool,
		) {
			retainedValue := expr
			if splat, ok := expr.(*SplatArg); ok {
				argumentFacts[expr] = c.inferExpressionType(splat.Value)
				retainedValue = splat.Value
			} else {
				argumentFacts[expr] = c.inferExpressionTypeWithExpectation(expr, expectation)
			}
			c.pinExpressionValueSource(retainedValue)
			c.pinExpressionInstanceOrigins(retainedValue)
			identityAutoCall := autoCall && !retainsCallable
			retainedFact := argumentFacts[expr]
			argumentRetainedAliases[expr] = c.captureRetainedContainerAliases(
				retainedValue,
				retainedFact,
			)
			identitySource := expr
			if !identityAutoCall {
				if callableExpr, bindable := bareIdentifierCallableValue(expr); bindable {
					identitySource = callableExpr
				}
			}
			identityExpr, autoInvoked := c.evaluatedIdentityExpression(identitySource, identityAutoCall)
			classNames, classExact := c.classValueExpressionNames(identityExpr)
			if autoInvoked {
				classNames, classExact = c.dispatchClassValueExpressionNames(identityExpr)
			}
			if classExact {
				argumentClassValues[expr] = classNames
			}
			callableIdentityExpr := identityExpr
			if retainsCallable {
				callableIdentityExpr = c.evaluatedCallableIdentityExpression(identitySource)
			}
			if fns, ok := c.callableExpressionFunctions(callableIdentityExpr); ok {
				argumentCallables[expr] = fns
			}
			if retainsCallable {
				if argumentSelfScan == nil {
					argumentSelfScan = c.newNamespaceMutationScan()
				}
				if binding, exact := argumentSelfScan.exactCallableSelfBinding(callableIdentityExpr); exact {
					argumentSelfBindings[expr] = binding
				}
			}
			staticExpr := expr
			if splat, ok := expr.(*SplatArg); ok {
				staticExpr = splat.Value
			}
			values, staticExact := c.staticValueExpressionAlternatives(staticExpr)
			if retainsCallable {
				values, staticExact = c.evaluatedStaticValueExpressionAlternatives(staticExpr)
			}
			if staticExact {
				argumentStaticValues[staticExpr] = append([]Expression(nil), values...)
				if choice, correlated := c.staticValueChoiceForExpression(staticExpr); correlated {
					argumentStaticChoices[staticExpr] = cloneCheckStaticChoiceFact(choice)
				}
				if splat, isSplat := expr.(*SplatArg); isSplat && len(values) == 1 {
					if array, isArray := values[0].(*ArrayLiteral); isArray {
						for _, element := range array.Elements {
							if _, captured := argumentFacts[element]; !captured {
								argumentFacts[element] = c.inferExpressionType(element)
							}
							if elementValues, exact := c.staticValueExpressionAlternatives(element); exact {
								argumentStaticValues[element] = append([]Expression(nil), elementValues...)
								if choice, correlated := c.staticValueChoiceForExpression(element); correlated {
									argumentStaticChoices[element] = cloneCheckStaticChoiceFact(choice)
								}
							}
							argumentSplatOrigins[element] = append(argumentSplatOrigins[element], splat)
						}
					}
				}
			}
		}
		captureSplatSource := func(expr Expression) {
			ident, directLocal := expr.(*Identifier)
			values, exact := argumentStaticValues[expr]
			if !directLocal || !exact || len(values) == 0 {
				return
			}
			argumentSplatSources[expr] = c.checkCallSplatSourceForLocal(ident.Name, values)
		}
		positionalSplatSeen := false
		argumentEvaluationFailed := false
		for i, arg := range typed.Args {
			expectation := expressionExpectation{}
			_, isSplat := arg.(*SplatArg)
			if evaluatedStoredBlocksExact && !positionalSplatSeen && !isSplat {
				expectation = retainedBlockPositionalArgumentExpectation(
					evaluatedStoredBlocks,
					i,
					len(typed.Args),
				)
			} else if invokedLambda != nil && !positionalSplatSeen && !isSplat {
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
						captureArgumentFacts(elem, expressionExpectation{}, true, false)
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
			identityExpectation := expectation
			if argumentTargetResolved && !positionalSplatSeen && !isSplat {
				identityExpectation = staticCallablePositionalArgumentExpectation(argumentTarget, i)
			}
			captureArgumentFacts(
				arg,
				expectation,
				!expectation.includesCallable(),
				identityExpectation.includesCallable(),
			)
			if splat, expanded := arg.(*SplatArg); expanded {
				captureSplatSource(splat.Value)
			}
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
			if evaluatedStoredBlocksExact && !kwarg.Splat {
				expectation = retainedBlockKeywordArgumentExpectation(
					evaluatedStoredBlocks,
					kwarg.Name,
				)
			} else if targetResolved && !kwarg.Splat {
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
						captureArgumentFacts(pair.Value, expressionExpectation{}, true, false)
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
			identityExpectation := expectation
			if argumentTargetResolved && !kwarg.Splat {
				identityExpectation = staticCallableKeywordArgumentExpectation(
					typed,
					argumentTarget,
					kwarg.Name,
				)
			}
			captureArgumentFacts(
				kwarg.Value,
				expectation,
				!expectation.includesCallable(),
				identityExpectation.includesCallable(),
			)
			if kwarg.Splat {
				captureSplatSource(kwarg.Value)
			}
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
					true,
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
		previousSelfBindings := c.callArgumentSelfBindings
		previousStaticValues := c.callArgumentStaticValues
		previousStaticChoices := c.callArgumentStaticChoices
		previousSplatSources := c.callArgumentSplatSources
		previousReceiverLength := c.callArrayReceiverLength
		c.callArgumentFacts = argumentFacts
		c.callArgumentClassValues = argumentClassValues
		c.callArgumentCallables = argumentCallables
		c.callArgumentSelfBindings = argumentSelfBindings
		c.callArgumentStaticValues = argumentStaticValues
		c.callArgumentStaticChoices = argumentStaticChoices
		c.callArgumentSplatSources = argumentSplatSources
		receiverLength := c.currentArrayReceiverLength(receiverLengthCapture)
		c.callArrayReceiverLength = receiverLength
		c.pinForwardedConstructorInstanceFact(typed, dynamicCandidates)
		if deferForwardedTargets {
			dynamicResolution = c.exactDynamicCallTargets(typed, target, targetResolved, dynamicCandidates)
		}
		callMayEnter := !argumentEvaluationFailed
		checkedCall := typed
		if expanded, exact := c.staticallyExpandedCall(typed); exact {
			checkedCall = expanded
		}
		if !argumentEvaluationFailed {
			c.captureEvaluatedRetainedConstructor(
				typed,
				target,
				blockCapturingBuiltin,
			)
		}
		arrayMutatorMayComplete := true
		arrayMutatorProperty := ""
		if member, ok := typed.Callee.(*MemberExpr); ok {
			receiver := nonNilMutatorReceiverFact(receiverFact)
			if receiver != nil && typeExprArmsAll(receiver, func(arm *TypeExpr) bool {
				return arm.Kind == TypeArray
			}) {
				if _, modeled := arrayMutatorBuiltinProperty("array." + member.Property); modeled {
					arrayMutatorProperty = member.Property
					arrayMutatorMayComplete = c.arrayMutatorCallMayComplete(typed, member.Property)
				}
			}
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
			targetMayEnter = targetMayEnter &&
				c.specialBuiltinCallMayComplete(checkedCall, target.name, receiverFact)
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
		if targetMayEnter &&
			c.containerMutatorCallProvablyAborts(checkedCall, receiverFact, argumentFacts) {
			targetMayEnter = false
			callMayComplete = false
		}
		targetMayEnter = targetMayEnter && arrayMutatorMayComplete
		callMayComplete = callMayComplete && arrayMutatorMayComplete
		storedBlockCallExact := targetMayEnter && invokedLambda == nil &&
			evaluatedStoredBlocksExact
		var storedBlockEntries []blockLiteralCallEntryOutcome
		storedBlockMayReturnNonLocally := false
		storedBlockMayFail := false
		if storedBlockCallExact {
			storedBlockEntries = make(
				[]blockLiteralCallEntryOutcome,
				len(evaluatedStoredBlocks),
			)
			storedBlockMayEnter := false
			storedBlockMayReject := false
			storedBlockMayComplete := false
			for i, block := range evaluatedStoredBlocks {
				entry := c.capturedBlockLiteralCallEntry(block, typed)
				flow := c.blockLiteralBodyCompletionFlow(block.block, block.strict)
				storedBlockEntries[i] = entry
				storedBlockMayEnter = storedBlockMayEnter || entry.mayEnter
				storedBlockMayReject = storedBlockMayReject || entry.mayReject
				storedBlockMayComplete = storedBlockMayComplete ||
					entry.mayEnter &&
						(flow.fallsThrough || flow.completes)
				storedBlockMayReturnNonLocally = storedBlockMayReturnNonLocally ||
					entry.mayEnter && flow.returnsNonLocally
				storedBlockMayFail = storedBlockMayFail || entry.mayEnter && flow.fails
			}
			if storedBlockMayReject {
				c.captureNonCompletingExpressionArm()
			}
			targetMayEnter = storedBlockMayEnter
			callMayComplete = storedBlockMayComplete
		}
		exactLocalReturnBlockCall := invokedLambda != nil
		if storedBlockCallExact {
			exactLocalReturnBlockCall = true
			for i, block := range evaluatedStoredBlocks {
				if storedBlockEntries[i].mayEnter && !block.strict {
					exactLocalReturnBlockCall = false
					break
				}
			}
		}
		immediateLambdaEntry := c.immediateLambdaCallEntry(invokedLambda, typed)
		if targetMayEnter && invokedLambda != nil {
			if immediateLambdaEntry.mayReject {
				c.captureNonCompletingExpressionArm()
			}
			targetMayEnter = immediateLambdaEntry.mayEnter
			if !targetMayEnter {
				callMayComplete = false
			} else if !c.immediateLambdaBodyMayCompleteForBinding(invokedLambda) {
				callMayComplete = false
			}
		}
		opaqueCallEffectsMayRun := targetMayEnter && opaqueCallEffects
		// Binding can stop before the body after earlier parameter defaults
		// have run, so carry only that evaluated prefix into the exception path.
		if callMayEnter && targetResolved && target.fn != nil &&
			!c.scriptCallDefaultPrefixClassConstantEffectsProvenAbsent(checkedCall, target) {
			opaqueCallEffectsMayRun = true
		} else if callMayEnter && !targetResolved && dynamicResolution.exact &&
			c.exactDynamicCallDefaultPrefixHasOpaqueClassConstantEffects(dynamicResolution) {
			opaqueCallEffectsMayRun = true
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
		if targetMayEnter && c.returnCollector != nil && !targetResolved &&
			!exactLocalReturnBlockCall && c.callMayDispatchDynamicValue(typed) {
			c.recordReturnSummaryResult(c.returnCollector, nil, nil)
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
		if opaqueCallEffectsMayRun {
			c.markOpaqueClassConstants()
		}
		arrayFillBlockCall := arrayMutatorProperty == "fill" ||
			targetResolved && target.name == "array.fill"
		arrayFillBlockMayRun := !arrayFillBlockCall || c.arrayFillBlockMayEvaluate(typed)
		arrayFillBlockBodyMayRun := arrayFillBlockMayRun
		var arrayFillBlockValues []checkBlockLiteralValue
		arrayFillBlockValuesExact := false
		if arrayFillBlockCall && arrayFillBlockMayRun {
			invocation := &checkBlockInvocation{
				arguments: []*TypeExpr{checkTypeInt},
			}
			switch {
			case typed.Block != nil:
				invocation.strictArity = typed.Block.Lambda
				arrayFillBlockBodyMayRun = c.blockLiteralInvocationMayEnter(typed.Block, invocation)
			case typed.BlockArg != nil:
				if blocks, _, exact := c.blockLiteralValueChoices(typed.BlockArg); exact {
					arrayFillBlockValuesExact = true
					for _, block := range blocks {
						invocation.strictArity = block.lambda
						if c.blockLiteralInvocationMayEnter(block.block, invocation) {
							arrayFillBlockValues = append(arrayFillBlockValues, block)
						}
					}
					arrayFillBlockBodyMayRun = len(arrayFillBlockValues) > 0
				}
			}
		}
		callBlockMayRun := targetMayEnter && invokedLambda == nil &&
			arrayFillBlockMayRun && c.callMayInvokeSuppliedBlock(typed)
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
		arrayMutatorIgnoresBlock := arrayMutatorProperty != "" && arrayMutatorProperty != "fill"
		if arrayMutatorIgnoresBlock {
			callBlockMayRun = false
		}
		var blockResult checkBlockResult
		immediateLambdaEnters := targetMayEnter && invokedLambda != nil
		oneShotBlockEnters := immediateLambdaEnters ||
			targetMayEnter && storedBlockCallExact
		var oneShotBlockBaseScopeState checkScopeState
		if oneShotBlockEnters {
			oneShotBlockBaseScopeState = c.snapshotScopeState()
		}
		if immediateLambdaEnters {
			c.applyLambdaBlockNamespaceMutations(invokedLambda)
			c.checkInvokedLambdaSummaryYields(function, invokedLambda)
			c.widenRepeatedRegionBlockIvarFacts(invokedLambda)
			c.captureNonCompletingExpressionArm()
		}
		if targetMayEnter && storedBlockCallExact {
			for i, block := range evaluatedStoredBlocks {
				if storedBlockEntries[i].mayEnter {
					if block.strict {
						c.checkInvokedLambdaSummaryYields(function, block.block)
					}
					if c.applyLambdaBlockNamespaceMutations(block.block) {
						c.markOpaqueClassConstants()
					}
					c.widenRepeatedRegionBlockIvarFacts(block.block)
				}
			}
			if storedBlockMayReturnNonLocally {
				c.captureNonLocalReturnExitState()
			}
			if storedBlockMayFail {
				c.captureNonCompletingExpressionArm()
			}
		}
		if callMayComplete && oneShotBlockEnters {
			// A safe call refines only its entered non-nil arm here. The
			// argumentsMayBeSkipped join below adds the untouched nil arm.
			if immediateLambdaEnters {
				c.refineOneShotBlockIvarFacts(
					oneShotBlockBaseScopeState,
					[]capturedBlockLiteralValue{{
						block:  invokedLambda,
						strict: true,
					}},
					[]blockLiteralCallEntryOutcome{immediateLambdaEntry},
				)
			} else {
				c.refineOneShotBlockIvarFacts(
					oneShotBlockBaseScopeState,
					evaluatedStoredBlocks,
					storedBlockEntries,
				)
			}
		}
		// Exact script targets carry callable arguments through their parameter
		// facts, so their body scan applies a lambda only at an actual `.call`.
		// Opaque dynamic and builtin targets may invoke any escaping lambda.
		escapingLambdaMayRun := targetMayEnter && !blockCapturingBuiltin &&
			(targetResolved && target.fn == nil || !targetResolved && !dynamicResolution.exact)
		if escapingLambdaMayRun && (invokedLambda == nil || immediateLambdaEnters) {
			if arrayMutatorProperty == "" {
				for _, arg := range typed.Args {
					c.applyLambdaLiteralNamespaceMutations(arg)
					c.checkLambdaLiteralSummaryYields(function, arg)
				}
				for _, kwarg := range typed.KwArgs {
					c.applyLambdaLiteralNamespaceMutations(kwarg.Value)
					c.checkLambdaLiteralSummaryYields(function, kwarg.Value)
				}
			}
			if !arrayFillBlockCall && !arrayMutatorIgnoresBlock {
				c.applyLambdaLiteralNamespaceMutations(typed.BlockArg)
				c.checkLambdaLiteralSummaryYields(function, typed.BlockArg)
			}
		} else if callBlockMayRun {
			c.applyLambdaLiteralNamespaceMutations(typed.BlockArg)
			c.checkLambdaLiteralSummaryYields(function, typed.BlockArg)
		}
		if arrayFillBlockCall && arrayFillBlockBodyMayRun && typed.BlockArg != nil {
			if arrayFillBlockValuesExact {
				for _, block := range arrayFillBlockValues {
					c.applyLambdaBlockNamespaceMutations(block.block)
				}
			} else {
				c.applyLambdaLiteralNamespaceMutations(typed.BlockArg)
			}
			c.checkLambdaLiteralSummaryYields(function, typed.BlockArg)
			c.applyCallableNamespaceMutations(argumentCallables[typed.BlockArg])
		} else if callBlockMayRun {
			c.applyCallableNamespaceMutations(argumentCallables[typed.BlockArg])
		}
		if typed.Block != nil && (callBlockMayRun || blockCapturingBuiltin) {
			if callBlockMayRun {
				c.checkLiteralArrayBlockParamTypes(function, typed)
			}
			// The lambda builtin converts its literal block to local return
			// semantics, so those returns cannot unwind the enclosing
			// function.
			localReturns := typed.Block.Lambda || c.callTargetsCoreLambda(typed, target, targetResolved)
			if blockCapturingBuiltin {
				c.checkCapturedBlockLiteral(
					function,
					typed.Block,
					localReturns,
				)
			} else if arrayFillBlockCall && !arrayFillBlockBodyMayRun {
				for _, param := range typed.Block.Params {
					c.checkRuntimeTypeAnnotation(function, param.Type)
					c.checkDestructureTargetTypeAnnotations(function, param.Target)
				}
				blockResult = checkBlockResult{exact: true}
			} else {
				blockResult = c.checkBlockLiteral(function, typed.Block, localReturns)
			}
		} else if targetMayEnter && typed.BlockArg != nil && arrayFillBlockMayRun {
			blocks, _, exact := c.blockLiteralValueChoices(typed.BlockArg)
			if exact && arrayFillBlockValuesExact {
				blocks = arrayFillBlockValues
			}
			if exact && len(blocks) == 0 {
				blockResult = checkBlockResult{exact: true}
			} else if exact {
				blockResult = c.blockLiteralValuesResult(function, blocks)
			}
		}
		if callMayComplete && arrayFillBlockCall &&
			blockResult.exact && !blockResult.mayComplete {
			callMayComplete = c.arrayFillCallMayCompleteWithoutInvokingBlock(typed)
		}
		c.callArgumentFacts = previousFacts
		c.callArgumentClassValues = previousClassValues
		c.callArgumentCallables = previousCallables
		c.callArgumentSelfBindings = previousSelfBindings
		c.callArgumentStaticValues = previousStaticValues
		c.callArgumentStaticChoices = previousStaticChoices
		c.callArgumentSplatSources = previousSplatSources
		c.callArrayReceiverLength = previousReceiverLength
		if storedBlockMayReturnNonLocally && !callMayComplete {
			c.expressionReturnsNonLocally = true
		}
		if argumentsMayBeSkipped {
			if !callMayComplete {
				// The failed non-nil arm still reaches rescue and ensure with any
				// argument or default effects that ran before the failure. Only the
				// nil short-circuit contributes state after the call itself.
				c.captureNonCompletingExpressionArm()
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
		_, memberCall := typed.Callee.(*MemberExpr)
		// Containers pass by reference, so a callee may mutate an argument
		// in place; the caller's structural facts stop holding. Dispatch
		// happens after the arguments evaluate, so the receiver's facts
		// stop holding here too, not during the callee walk. A dispatch
		// proven pure by its registered member contract preserves the
		// receiver's facts for outer inference and condition-outcome
		// narrowing, a modeled builtin container mutator checks its writes
		// and may preserve a still-compatible declared fact, and everything
		// else keeps poisoning.
		if targetMayEnter {
			forwardedBlockMayRun := typed.BlockArg != nil && c.callMayInvokeSuppliedBlock(typed)
			if !blockCapturingBuiltin && invokedLambda == nil && !storedBlockCallExact &&
				(!memberCall || c.memberCallMayWriteUnknownIvar(typed) || forwardedBlockMayRun) {
				preciseImplicitSelfDispatch := false
				if !memberCall {
					c.callArgumentFacts = argumentFacts
					c.callArgumentClassValues = argumentClassValues
					c.callArgumentCallables = argumentCallables
					c.callArgumentSelfBindings = argumentSelfBindings
					c.callArgumentStaticValues = argumentStaticValues
					c.callArgumentStaticChoices = argumentStaticChoices
					c.callArgumentSplatSources = argumentSplatSources
					c.callArrayReceiverLength = receiverLength
					if dispatch, exact := c.implicitSelfCallDispatch(typed); exact {
						preciseImplicitSelfDispatch = true
						if dispatch.mayRunScript() {
							c.widenRegionIvarFacts(c.scriptDispatchIvarEffects(dispatch))
						}
					}
					c.callArgumentFacts = previousFacts
					c.callArgumentClassValues = previousClassValues
					c.callArgumentCallables = previousCallables
					c.callArgumentSelfBindings = previousSelfBindings
					c.callArgumentStaticValues = previousStaticValues
					c.callArgumentStaticChoices = previousStaticChoices
					c.callArgumentSplatSources = previousSplatSources
					c.callArrayReceiverLength = previousReceiverLength
				}
				if !preciseImplicitSelfDispatch {
					c.widenUnsetInstanceIvarFacts()
				}
			}
			shovelEscapeMayMutate := callMayComplete ||
				!c.nonCompletingScriptCallLeavesParametersUnused(checkedCall, target, targetResolved)
			exactScriptMutatedArguments := c.applyExactScriptArrayArgumentMutations(
				checkedCall,
				target,
				targetResolved,
			)
			c.applyKeywordSplatDeleteFact(typed)
			mutatorArgsModeled := false
			if member, ok := typed.Callee.(*MemberExpr); ok && !c.memberCallPreservesReceiverFacts(typed) {
				preserved, modeled, mayWrite := c.applyContainerMutatorCallFacts(
					function,
					typed,
					checkedCall,
					member,
					argumentFacts,
					argumentStaticValues,
					argumentStaticChoices,
					argumentRetainedAliases,
					argumentSplatOrigins,
					argumentSplatSources,
					blockResult,
					receiverFact,
					receiverLength,
				)
				mutatorArgsModeled = modeled || arrayMutatorRetainsArgumentsWithoutCalling(
					typed,
					member.Property,
					receiverFact,
				)
				if preserved {
					if mayWrite {
						if ident, ok := member.Object.(*Identifier); ok {
							c.clearLocalStaticValueAliases(ident.Name)
						}
					}
				} else if modeled && mayWrite && Expression(typed) == c.expressionStatementRoot {
					if ident, ok := member.Object.(*Identifier); ok {
						c.poisonElementWriteFacts(ident.Name)
					} else {
						c.poisonEscapedCallValue(member.Object, shovelEscapeMayMutate)
					}
				} else {
					c.poisonEscapedCallValue(member.Object, shovelEscapeMayMutate)
				}
			}
			if blockArgEvaluated {
				if member, ok := typed.BlockArg.(*MemberExpr); ok {
					c.poisonEscapedCallValue(member.Object, shovelEscapeMayMutate)
				}
			}
			for arg := range argumentFacts {
				// A modeled builtin mutator only reads and retains its
				// arguments. Retention is tracked through container write
				// aliases, so generic escape poison would undo the compatible
				// fact the mutator just preserved.
				if mutatorArgsModeled {
					continue
				}
				if _, modeled := exactScriptMutatedArguments[arg]; modeled {
					continue
				}
				c.poisonEscapedCallValue(arg, shovelEscapeMayMutate)
			}
			for _, kwarg := range typed.KwArgs {
				if mutatorArgsModeled {
					continue
				}
				if _, evaluated := argumentFacts[kwarg.Value]; evaluated {
					c.poisonEscapedCallValue(kwarg.Value, shovelEscapeMayMutate)
				}
			}
		}
		if (!callMayEnter || !callMayComplete) && !argumentsMayBeSkipped {
			return false
		}
	case *MemberExpr:
		if autoCall && typed.Property == "call" {
			blocks, exact := c.capturedBlockLiteralValueAlternatives(typed.Object)
			hasBlock := false
			for _, block := range blocks {
				if block.block != nil {
					hasBlock = true
					break
				}
			}
			if exact && hasBlock {
				call := &CallExpr{
					Callee:             typed,
					KeywordOptionsHash: true,
					Safe:               typed.Safe,
					Position:           typed.Pos(),
				}
				completed := c.checkExpressionWithAuto(function, call, true)
				if fact, pinned := c.pinnedExpressionFacts[call]; pinned {
					c.pinExpressionFact(typed, fact)
				}
				if fact, pinned := c.constructorInstanceFacts[call]; pinned {
					c.constructorInstanceFacts[typed] = fact
				}
				return completed
			}
		}
		var invokedLambda *BlockLiteral
		if autoCall && typed.Property == "call" {
			invokedLambda = c.resolveImmediateLambdaBlock(typed.Object)
		}
		blockCapturingBuiltin := false
		if autoCall && typed.Property == "new" {
			call := &CallExpr{Callee: typed, Position: typed.Pos()}
			target, resolved := c.resolveCallable(call)
			blockCapturingBuiltin = c.callTargetsBlockCapturingBuiltin(call, target, resolved)
		}
		objectAutoCall := !blockCapturingBuiltin
		if typed.Property == "call" && typeExprMayIncludeCallable(c.inferExpressionType(typed.Object)) {
			objectAutoCall = false
		}
		if !c.checkExpressionWithAuto(function, typed.Object, objectAutoCall) {
			return false
		}
		c.captureAssignmentReceiver(typed)
		if autoCall {
			if typed.Property == "new" {
				classes, exact := c.constructorInstanceClassNames(typed.Object, "")
				c.pinConstructorInstanceFact(typed, classes, exact)
				if exact && len(classes) == 1 {
					classDef := c.script.classes[classes[0]]
					if classDef != nil && classDef.Methods["initialize"] == nil {
						c.recordUninitializedConstructorIvarFacts(typed, classes[0])
					}
				}
				if exact && len(classes) == 0 {
					// Parenless `.new` also resolves before any outer dispatch.
					// Plain modules fail here and cannot contribute opaque effects.
					return false
				}
			}
			immediateLambdaEnters := lambdaLiteralArity(invokedLambda) == 0
			var oneShotBlockBaseScopeState checkScopeState
			if immediateLambdaEnters {
				oneShotBlockBaseScopeState = c.snapshotScopeState()
				c.applyLambdaBlockNamespaceMutations(invokedLambda)
				c.checkInvokedLambdaSummaryYields(function, invokedLambda)
				c.widenRepeatedRegionBlockIvarFacts(invokedLambda)
				c.captureNonCompletingExpressionArm()
			}
			dispatchRuntimeState := c.snapshotRuntimeState()
			dispatchScopeState := c.snapshotScopeState()
			target, resolved, invoked, completed := c.checkMemberAutoCall(function, typed)
			if !completed {
				if typed.Safe && !c.safeNavigationReceiverKnownNonNil(typed.Object) {
					c.captureNonCompletingExpressionArm()
					c.restoreRuntimeState(dispatchRuntimeState)
					c.restoreScopeState(dispatchScopeState)
					c.pinExpressionFact(typed, checkTypeNil)
					return true
				}
				return false
			}
			if immediateLambdaEnters {
				c.refineOneShotBlockIvarFacts(
					oneShotBlockBaseScopeState,
					[]capturedBlockLiteralValue{{
						block:  invokedLambda,
						strict: true,
					}},
					[]blockLiteralCallEntryOutcome{{mayEnter: true}},
				)
			}
			dispatchPreservesFacts := c.memberDispatchPreservesReceiverFacts(typed)
			if !blockCapturingBuiltin &&
				invokedLambda == nil &&
				(invoked || !resolved) &&
				c.memberDispatchEffect(typed) == effectUnknown {
				c.widenUnsetInstanceIvarFacts()
			}
			if resolved && invoked && target.fn == nil && target.spec.resultType != nil {
				// The target is selected from the receiver fact before
				// dispatch effects can weaken that fact. Preserve its
				// invariant result for outer inference even when the sole
				// completing arm is nil and exact shape arms raise.
				c.pinExpressionFact(
					typed,
					c.safeNavigationMemberResultFact(typed, target.spec.resultType),
				)
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
			if !dispatchPreservesFacts {
				c.poisonEscapedCallValue(typed.Object, true)
			}
		}
	case *ScopeExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
	case *IndexExpr:
		previousEvaluatedFacts := c.evaluatedDestructureFacts
		if previousEvaluatedFacts == nil {
			c.evaluatedDestructureFacts = make(map[Expression]capturedDestructureValueFact)
			defer func() { c.evaluatedDestructureFacts = previousEvaluatedFacts }()
		}
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
		c.captureEvaluatedDestructureFactOnce(typed.Object)
		hashDefaults, hashDefaultsExact := c.captureDirectCoreHashDefaults(typed.Object)
		hashDefaultAliases := c.directCoreHashDefaultReceiverAliasNames(typed.Object)
		c.captureAssignmentReceiver(typed)
		dispatchType := c.instanceDispatchReceiverType(
			typed.Object,
			c.inferExpressionType(typed.Object),
		)
		opaqueDispatch := c.indexReadHasOpaqueClassConstantEffects(typed.Object)
		for _, index := range typed.Indices {
			if !c.checkExpressionWithAuto(function, index, true) {
				return false
			}
			c.pinExpressionFact(index, c.inferExpressionType(index))
			c.captureEvaluatedDestructureFactOnce(index)
		}
		var dispatch instanceScriptDispatchSelection
		var effects regionIvarEffects
		var defaultMayRun bool
		var completed bool
		c.withEvaluatedDestructureArgumentFacts(typed.Indices, func() {
			dispatch = c.indexScriptDispatch(typed, dispatchType)
			if dispatch.mayReject() {
				c.captureNonCompletingExpressionArm()
			}
			effects = c.scriptDispatchIvarEffects(dispatch)
			hashDefaults, hashDefaultsExact = c.validateEvaluatedDirectCoreHashDefaults(
				hashDefaults,
				hashDefaultsExact,
				hashDefaultAliases,
			)
			defaultEffects, mayRun, defaultMayReject := c.indexReadIvarEffects(typed, dispatchType, hashDefaults)
			c.applyDirectCoreHashDefaultNamespaceMutations(
				typed,
				dispatchType,
				hashDefaults,
			)
			defaultMayRun = mayRun
			mergeRegionIvarEffects(&effects, defaultEffects)
			completed = c.indexExpressionMayCompleteWithReceiverAndDefaults(
				typed,
				dispatchType,
				hashDefaults,
				hashDefaultsExact,
			)
			if defaultMayRun {
				// A default callback receives the Hash itself and may retain or
				// populate it before returning or raising. Later reads therefore
				// cannot reuse the fresh-empty provenance observed by this read.
				c.poisonDirectCoreHashDefaultReceiverAliases(hashDefaultAliases)
			}
			if defaultMayReject {
				c.captureNonCompletingExpressionArm()
			}
		})
		c.enqueueReachableInstanceDispatch(dispatchType, "[]")
		if opaqueDispatch {
			c.markOpaqueClassConstants()
		}
		if dispatch.mayRunScript() || defaultMayRun {
			c.widenRegionIvarFacts(effects)
			c.captureNonCompletingExpressionArm()
		}
		c.withEvaluatedDestructureArgumentFacts(
			append([]Expression{typed.Object}, typed.Indices...),
			func() { c.captureEvaluatedDestructureFact(typed) },
		)
		return completed
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
		previousEvaluatedFacts := c.evaluatedDestructureFacts
		if previousEvaluatedFacts == nil {
			c.evaluatedDestructureFacts = make(map[Expression]capturedDestructureValueFact)
			defer func() { c.evaluatedDestructureFacts = previousEvaluatedFacts }()
		}
		if !c.checkExpressionWithAuto(function, typed.Left, true) {
			return false
		}
		// A shovel receiver evaluates before its right operand, so the fact
		// the append is checked against is captured now: a right side that
		// escapes the same local must not erase the bound the receiver was
		// evaluated under.
		var shovelReceiverFact *TypeExpr
		if typed.Operator == tokenShovel {
			if ident, ok := unwrapShovelChain(typed.Left).(*Identifier); ok {
				shovelReceiverFact = c.localTypeFor(ident.Name)
			}
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
				if rightCompleted {
					c.pinExpressionFact(typed.Right, c.inferExpressionType(typed.Right))
					c.captureEvaluatedDestructureFactOnce(typed.Right)
				}
			}
			if !rightReachable {
				c.restoreRuntimeState(state)
				c.restoreScopeState(scopeState)
			} else if !rightCompleted {
				if rightAlwaysRuns {
					return false
				}
				c.captureNonCompletingExpressionArm()
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
		completed := true
		c.withEvaluatedDestructureArgumentFacts([]Expression{typed.Right}, func() {
			dispatch := c.binaryScriptDispatch(typed, dispatchType)
			if dispatch.mayReject() {
				c.captureNonCompletingExpressionArm()
			}
			c.checkBinaryOperandTypes(function, typed)
			c.checkShovelElementWrite(function, typed, shovelReceiverFact)
			c.applyShovelMutationFacts(typed)
			c.enqueueReachableInstanceDispatch(
				dispatchType,
				binaryDispatchMethodNames(typed.Operator)...,
			)
			if opaqueDispatch {
				c.markOpaqueClassConstants()
			}
			if dispatch.mayRunScript() {
				c.widenRegionIvarFacts(c.scriptDispatchIvarEffects(dispatch))
				c.captureNonCompletingExpressionArm()
			}
			completed = c.binaryExpressionMayCompleteWithReceiver(typed, dispatchType)
		})
		if !completed {
			return false
		}
	case *ConditionalExpr:
		return c.checkConditionalExpression(function, typed, expressionExpectation{})
	case *RescueExpr:
		return c.checkRescueExpression(function, typed, autoCall)
	case *IfExpr:
		return c.checkIfExpression(function, typed, expressionExpectation{})
	case *RangeExpr:
		if !c.checkExpressionWithAuto(function, typed.Start, true) ||
			!c.rangeEndpointConversionMaySucceed(typed.Start) ||
			!c.checkExpressionWithAuto(function, typed.End, true) ||
			!c.rangeEndpointConversionMaySucceed(typed.End) {
			return false
		}
	case *CaseExpr:
		return c.checkCaseExpression(function, typed, expressionExpectation{})
	case *BlockLiteral:
		// A standalone block literal is a stabby lambda; its body checks like a
		// call block's. Plain call blocks are checked from the CallExpr case.
		if typed.Lambda {
			c.checkBlockLiteral(function, typed, true)
		}
		return true
	case *YieldExpr:
		if c.yieldBlockKnownAbsent() {
			return false
		}
		for _, arg := range typed.Args {
			if !c.checkExpressionWithAuto(function, arg, true) {
				return false
			}
			c.poisonEscapedCallValue(arg, true)
		}
		c.markOpaqueClassConstants()
		// The caller-supplied block may return non-locally instead of
		// letting the summarized function produce its later result.
		if c.summaryYieldsActive {
			c.recordReturnSummaryResult(c.summaryYieldCollector, nil, nil)
		}
		c.widenUnsetInstanceIvarFacts()
	case *InterpolatedString:
		return c.checkStringParts(function, typed.Parts)
	case *InterpolatedSymbol:
		return c.checkStringParts(function, typed.Parts)
	}
	return true
}

// yieldBlockKnownAbsent distinguishes an exact blockless call from a pristine
// function walk, where a caller may still supply a block. Return summaries
// carry their own call shape and take precedence over the enclosing reachable
// check while their synthetic body walk is active.
func (c *scriptChecker) yieldBlockKnownAbsent() bool {
	if c.returnCollector != nil {
		return !c.summaryBlockAvailable
	}
	return c.reachableBlockKnownAbsent
}

// callHasOpaqueClassConstantEffects reports calls whose implementation is not
// proven unable to install a class constant. The verdict is captured when the
// callee evaluates because later arguments can change the receiver's facts.
func (c *scriptChecker) callHasOpaqueClassConstantEffects(call *CallExpr, target staticCallable, resolved bool) bool {
	if call == nil {
		return false
	}
	blockMayRun := !c.callTargetsBlockCapturingBuiltin(call, target, resolved) &&
		(call.BlockArg != nil || c.resolvedCallMayEvaluateBlock(call, target, resolved))
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

func (c *scriptChecker) exactDynamicCallDefaultPrefixHasOpaqueClassConstantEffects(
	resolution checkDynamicCallResolution,
) bool {
	for _, candidate := range resolution.targets {
		if candidate.bindingStarts &&
			!c.scriptCallDefaultPrefixClassConstantEffectsProvenAbsent(
				candidate.call,
				candidate.target,
			) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) exactDynamicCallHasOpaqueClassConstantEffects(
	resolution checkDynamicCallResolution,
) bool {
	for _, candidate := range resolution.targets {
		if candidate.mayEnter {
			if !c.scriptCallClassConstantEffectsProvenAbsent(candidate.call, candidate.target) {
				return true
			}
			continue
		}
		if candidate.bindingStarts &&
			!c.scriptCallDefaultPrefixClassConstantEffectsProvenAbsent(
				candidate.call,
				candidate.target,
			) {
			return true
		}
	}
	return false
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

func (c *scriptChecker) evaluatedCallableIdentityExpression(expr Expression) Expression {
	switch typed := expr.(type) {
	case *ConditionalExpr:
		if truthy, known := c.inferredConditionTruthiness(typed.Condition); known {
			if truthy {
				return c.evaluatedCallableIdentityExpression(typed.Consequent)
			}
			return c.evaluatedCallableIdentityExpression(typed.Alternate)
		}
	case *IfExpr:
		if branch, known := c.inferredIfExpressionBranch(typed); known {
			return c.evaluatedCallableIdentityExpression(branch)
		}
	case *CaseExpr:
		if result, known := c.inferredCaseExpressionResult(typed); known {
			return c.evaluatedCallableIdentityExpression(result)
		}
	case *BinaryExpr:
		if typed.Operator != tokenAnd && typed.Operator != tokenOr {
			break
		}
		if truthy, known := c.inferredConditionTruthiness(typed.Left); known {
			if truthy == (typed.Operator == tokenAnd) {
				return c.evaluatedCallableIdentityExpression(typed.Right)
			}
			return c.evaluatedCallableIdentityExpression(typed.Left)
		}
	}
	return expr
}

func (c *scriptChecker) callableExpressionFunctionsSeen(
	expr Expression,
	seen map[*ScriptFunction]struct{},
) ([]*ScriptFunction, bool) {
	if fact, captured := c.destructureProjectionFacts[expr]; captured &&
		fact.factKind == destructureCallableFact && len(fact.callables) > 0 {
		return append([]*ScriptFunction(nil), fact.callables...), true
	}
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
		make(map[scriptClassConstantEffectKey]struct{}),
	)
}

type scriptClassConstantEffectKey struct {
	fn   *ScriptFunction
	plan string
}

func scriptClassConstantEffectPlanKey(defaultParams []int, body bool) string {
	var key strings.Builder
	if body {
		key.WriteByte('b')
	} else {
		key.WriteByte('d')
	}
	for _, index := range defaultParams {
		key.WriteByte(':')
		key.WriteString(strconv.Itoa(index))
	}
	return key.String()
}

func (c *scriptChecker) scriptCallClassConstantEffectsProvenAbsentSeen(
	call *CallExpr,
	target staticCallable,
	seen map[scriptClassConstantEffectKey]struct{},
) bool {
	fn := target.fn
	if fn == nil || fn.owner != c.script {
		return false
	}

	defaultParams := make([]int, 0, len(fn.Params))
	collapseOptionsHash := call != nil && staticCallCollapsesOptionsHash(call, target)
	for i, param := range fn.Params {
		if param.DefaultVal != nil &&
			(call == nil || callMayEvaluateParamDefault(call, fn, i, collapseOptionsHash)) {
			defaultParams = append(defaultParams, i)
		}
	}
	return c.scriptFunctionClassConstantEffectsProvenAbsentForPlan(
		fn,
		defaultParams,
		true,
		seen,
	)
}

func (c *scriptChecker) scriptFunctionClassConstantEffectsProvenAbsentForPlan(
	fn *ScriptFunction,
	defaultParams []int,
	body bool,
	seen map[scriptClassConstantEffectKey]struct{},
) bool {
	key := scriptClassConstantEffectKey{
		fn:   fn,
		plan: scriptClassConstantEffectPlanKey(defaultParams, body),
	}
	if _, recursive := seen[key]; recursive {
		// Re-entering the same evaluated defaults/body is a proof cycle. Any
		// concrete write before or after that edge is still scanned by its
		// first visit; treating the edge itself as clean reaches the fixed point.
		return true
	}
	seen[key] = struct{}{}
	defer delete(seen, key)

	bindings := scriptFunctionBindings(fn)
	restoreResolution := c.withClassConstantProofResolution(fn, bindings)
	defer restoreResolution()

	for _, index := range defaultParams {
		if index < 0 || index >= len(fn.Params) {
			return false
		}
		param := fn.Params[index]
		if !c.scriptExpressionClassConstantEffectsProvenAbsent(param.DefaultVal, bindings, seen) {
			return false
		}
	}
	if !body {
		return true
	}
	for _, stmt := range fn.Body {
		var expressions []Expression
		switch typed := stmt.(type) {
		case *ExprStmt:
			expressions = []Expression{typed.Expr}
		case *ReturnStmt:
			expressions = []Expression{typed.Value}
		case *RaiseStmt:
			if staticRaiseErrorClass(typed) {
				expressions = []Expression{typed.Message}
			} else {
				expressions = []Expression{typed.Value, typed.Message}
			}
		default:
			return false
		}
		for _, expr := range expressions {
			if !c.scriptExpressionClassConstantEffectsProvenAbsent(expr, bindings, seen) {
				return false
			}
		}
	}
	return true
}

func (c *scriptChecker) scriptCallDefaultPrefixClassConstantEffectsProvenAbsent(
	call *CallExpr,
	target staticCallable,
) bool {
	fn := target.fn
	if call == nil || fn == nil || fn.owner != c.script {
		return false
	}
	plan := c.scriptCallBindingPlan(call, target)
	if plan.bodyMayEnter || len(plan.defaultParams) == 0 {
		return true
	}
	return c.scriptFunctionClassConstantEffectsProvenAbsentForPlan(
		fn,
		plan.defaultParams,
		false,
		make(map[scriptClassConstantEffectKey]struct{}),
	)
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
	seen map[scriptClassConstantEffectKey]struct{},
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

func binaryDispatchCall(expr *BinaryExpr, method string) *CallExpr {
	if expr == nil {
		return nil
	}
	return &CallExpr{
		Callee: &MemberExpr{
			Object:   expr.Left,
			Property: method,
			Position: expr.Pos(),
		},
		Args:     []Expression{expr.Right},
		Position: expr.Pos(),
	}
}

type instanceScriptDispatchTarget struct {
	call          *CallExpr
	target        staticCallable
	classDef      *ClassDef
	classMethod   bool
	bindingStarts bool
	mayEnter      bool
	mayReject     bool
}

type instanceScriptDispatchSelection struct {
	targets            []instanceScriptDispatchTarget
	fallbackArms       []*TypeExpr
	instanceFallbacks  int
	pureFallbacks      int
	rejectingFallbacks int
	unknown            bool
}

func (s instanceScriptDispatchSelection) mayRunScript() bool {
	if s.unknown {
		return true
	}
	for _, target := range s.targets {
		if target.bindingStarts {
			return true
		}
	}
	return false
}

func (s instanceScriptDispatchSelection) mayReject() bool {
	if s.unknown || s.rejectingFallbacks > 0 {
		return true
	}
	for _, target := range s.targets {
		if target.mayReject {
			return true
		}
	}
	return false
}

func (c *scriptChecker) instanceDispatchReceiverType(
	receiver Expression,
	receiverType *TypeExpr,
) *TypeExpr {
	if c.expressionIsCurrentInstanceSelf(receiver) {
		return &TypeExpr{Kind: TypeEnum, Name: c.selfClass.Name}
	}
	return receiverType
}

// instanceScriptDispatch selects the one user-defined method each receiver
// arm would use. The method list is ordered so != stops at a defined != and
// only falls back to == when != is absent, matching evalInstanceOperator.
func (c *scriptChecker) instanceScriptDispatch(
	receiver Expression,
	receiverType *TypeExpr,
	args []Expression,
	methods ...string,
) instanceScriptDispatchSelection {
	var selection instanceScriptDispatchSelection
	if len(methods) == 0 {
		return selection
	}
	currentSelfReceiver := c.expressionIsCurrentInstanceSelf(receiver)
	receiverType = c.instanceDispatchReceiverType(receiver, receiverType)
	arms, exact := typeExprArms(receiverType, 0)
	if !exact || len(arms) == 0 {
		selection.unknown = true
		return selection
	}
	resolve := c.checkNamedTypeResolver()
	for _, arm := range arms {
		if arm.Kind != TypeEnum {
			selection.fallbackArms = append(selection.fallbackArms, arm)
			continue
		}
		match, ok := resolve(arm)
		if !ok {
			selection.unknown = true
			continue
		}
		if match.enum != nil {
			selection.instanceFallbacks++
			continue
		}
		classDef := match.class
		if classDef == nil {
			selection.unknown = true
			continue
		}
		if currentSelfReceiver {
			classDef = c.selfClass
		}
		if classDef.IsModule {
			selection.unknown = true
			continue
		}
		var method string
		var fn *ScriptFunction
		for _, candidate := range methods {
			if candidateFn := classDef.Methods[candidate]; candidateFn != nil {
				method = candidate
				fn = candidateFn
				break
			}
		}
		if fn == nil {
			selection.instanceFallbacks++
			continue
		}
		call := &CallExpr{
			Callee: &MemberExpr{
				Object:   receiver,
				Property: method,
				Position: receiver.Pos(),
			},
			Args:     append([]Expression(nil), args...),
			Position: receiver.Pos(),
		}
		target := staticCallable{
			name:       classDef.Name + "#" + method,
			fn:         fn,
			resolution: calleeMemberMethod,
		}
		visible := c.dynamicCallTargetVisible(classDef, false, fn, false)
		plan := c.scriptCallBindingPlan(call, target)
		mayEnter := visible && plan.bodyMayEnter
		selection.targets = append(selection.targets, instanceScriptDispatchTarget{
			call:          call,
			target:        target,
			classDef:      classDef,
			bindingStarts: visible && plan.bindingStarts,
			mayEnter:      mayEnter,
			mayReject:     !visible || !c.scriptCallBodyMustEnter(call, target),
		})
	}
	return selection
}

func (c *scriptChecker) binaryScriptDispatch(
	expr *BinaryExpr,
	receiverType *TypeExpr,
) instanceScriptDispatchSelection {
	if expr == nil {
		return instanceScriptDispatchSelection{}
	}
	selection := c.instanceScriptDispatch(
		expr.Left,
		receiverType,
		[]Expression{expr.Right},
		binaryDispatchMethodNames(expr.Operator)...,
	)
	if expr.Operator == tokenEQ || expr.Operator == tokenNotEQ {
		selection.pureFallbacks += selection.instanceFallbacks
	} else {
		selection.rejectingFallbacks += selection.instanceFallbacks
	}
	return selection
}

func (c *scriptChecker) indexScriptDispatch(
	expr *IndexExpr,
	receiverType *TypeExpr,
) instanceScriptDispatchSelection {
	if expr == nil {
		return instanceScriptDispatchSelection{}
	}
	selection := c.instanceScriptDispatch(
		expr.Object,
		receiverType,
		expr.Indices,
		"[]",
	)
	selection.rejectingFallbacks += selection.instanceFallbacks
	return selection
}

func (c *scriptChecker) assignmentSetterScriptDispatch(
	target Expression,
	value Expression,
	receiver checkAssignmentReceiverCapture,
) instanceScriptDispatchSelection {
	call := assignmentSetterCall(target, value)
	if call == nil {
		return instanceScriptDispatchSelection{}
	}
	switch typed := target.(type) {
	case *IndexExpr:
		selection := c.instanceScriptDispatch(
			typed.Object,
			receiver.receiverType,
			call.Args,
			"[]=",
		)
		selection.rejectingFallbacks += selection.instanceFallbacks
		for _, arm := range selection.fallbackArms {
			if !c.builtinIndexSetterArmMustComplete(typed, arm, receiver) {
				selection.rejectingFallbacks++
			}
		}
		return selection
	case *MemberExpr:
		receivers, exact := c.assignmentMemberReceiversFromCandidates(receiver.candidates)
		if (!exact || len(receivers) == 0) && c.expressionIsCurrentSelf(typed.Object) {
			receivers = []assignmentMemberReceiver{{
				class:       c.selfClass,
				classMethod: c.selfClassContext,
			}}
			exact = true
		}
		if !exact {
			return c.memberSetterFallbackSelection(receiver.receiverType)
		}
		var selection instanceScriptDispatchSelection
		if len(receivers) == 0 {
			return c.memberSetterFallbackSelection(receiver.receiverType)
		}
		if receiver.candidates.instancesExact && receiver.candidates.instancesMayNil {
			selection.rejectingFallbacks++
		}
		setter := typed.Property + "="
		for _, receiver := range receivers {
			methods := receiver.class.Methods
			separator := "#"
			if receiver.classMethod {
				methods = receiver.class.ClassMethods
				separator = "."
			}
			fn := methods[setter]
			if fn == nil {
				if methods[typed.Property] == nil {
					selection.pureFallbacks++
				} else {
					selection.rejectingFallbacks++
				}
				continue
			}
			staticTarget := staticCallable{
				name:       receiver.class.Name + separator + setter,
				fn:         fn,
				resolution: calleeMemberMethod,
			}
			visible := c.dynamicCallTargetVisible(
				receiver.class,
				receiver.classMethod,
				fn,
				false,
			)
			plan := c.scriptCallBindingPlan(call, staticTarget)
			selection.targets = append(selection.targets, instanceScriptDispatchTarget{
				call:          call,
				target:        staticTarget,
				classDef:      receiver.class,
				classMethod:   receiver.classMethod,
				bindingStarts: visible && plan.bindingStarts,
				mayEnter:      visible && plan.bodyMayEnter,
				mayReject:     !visible || !c.scriptCallBodyMustEnter(call, staticTarget),
			})
		}
		return selection
	default:
		return instanceScriptDispatchSelection{}
	}
}

func (c *scriptChecker) memberSetterFallbackSelection(
	receiverType *TypeExpr,
) instanceScriptDispatchSelection {
	var selection instanceScriptDispatchSelection
	arms, exact := typeExprArms(receiverType, 0)
	if !exact || len(arms) == 0 {
		selection.unknown = true
		return selection
	}
	for _, arm := range arms {
		switch arm.Kind {
		case TypeHash, TypeShape:
			selection.pureFallbacks++
		case TypeAny, TypeUnknown, TypeEnum:
			selection.unknown = true
		default:
			selection.rejectingFallbacks++
		}
	}
	return selection
}

func expressionIsSelf(expr Expression) bool {
	ident, ok := expr.(*Identifier)
	return ok && ident.Name == "self"
}

func (c *scriptChecker) expressionIsCurrentSelf(expr Expression) bool {
	return c.selfClass != nil && expressionIsSelf(expr)
}

func (c *scriptChecker) expressionIsCurrentInstanceSelf(expr Expression) bool {
	return !c.selfClassContext && c.expressionIsCurrentSelf(expr)
}

func (c *scriptChecker) scriptDispatchRunsOnCurrentSelf(
	selected instanceScriptDispatchTarget,
) bool {
	if !sameScriptClass(c.selfClass, selected.classDef) ||
		c.selfClassContext != selected.classMethod {
		return false
	}
	member, ok := selected.call.Callee.(*MemberExpr)
	return ok && c.expressionIsCurrentSelf(member.Object)
}

func sameScriptClass(left, right *ClassDef) bool {
	return left != nil && right != nil &&
		left.owner == right.owner && left.Name == right.Name
}

func (c *scriptChecker) implicitSelfIdentifierDispatch(
	ident *Identifier,
) (instanceScriptDispatchSelection, bool) {
	if ident == nil {
		return instanceScriptDispatchSelection{}, false
	}
	return c.implicitSelfCallDispatch(&CallExpr{
		Callee:   ident,
		Position: ident.Pos(),
	})
}

func (c *scriptChecker) implicitSelfCallDispatch(
	call *CallExpr,
) (instanceScriptDispatchSelection, bool) {
	if call == nil || c.selfClass == nil {
		return instanceScriptDispatchSelection{}, false
	}
	ident, ok := call.Callee.(*Identifier)
	if !ok || ident.Name == blockGivenName ||
		c.identifierCallShadowed(call, ident.Name) || c.hostGlobalShadows(ident.Name) {
		return instanceScriptDispatchSelection{}, false
	}
	if c.script.functions[ident.Name] != nil {
		return instanceScriptDispatchSelection{}, false
	}
	if _, ok := c.typeRootFunction(ident.Name); ok {
		return instanceScriptDispatchSelection{}, false
	}
	if c.typeRootHasBinding(ident.Name) || c.hostBuiltinOverrides(ident.Name) {
		return instanceScriptDispatchSelection{}, false
	}
	if _, ok := c.defaultBuiltinCallSpec(ident.Name); ok {
		return instanceScriptDispatchSelection{}, false
	}
	fn := c.implicitSelfFunction(ident.Name)
	if fn == nil {
		return instanceScriptDispatchSelection{}, false
	}
	separator := "#"
	if c.selfClassContext {
		separator = "."
	}
	effectiveCall := *call
	effectiveCall.Callee = &MemberExpr{
		Object:   &Identifier{Name: "self", Position: ident.Pos()},
		Property: ident.Name,
		Position: ident.Pos(),
	}
	call = &effectiveCall
	target := staticCallable{
		name:       c.selfClass.Name + separator + ident.Name,
		fn:         fn,
		resolution: calleeMemberMethod,
	}
	plan := c.scriptCallBindingPlan(call, target)
	return instanceScriptDispatchSelection{
		targets: []instanceScriptDispatchTarget{{
			call:          call,
			target:        target,
			classDef:      c.selfClass,
			classMethod:   c.selfClassContext,
			bindingStarts: plan.bindingStarts,
			mayEnter:      plan.bodyMayEnter,
			mayReject:     !c.scriptCallBodyMustEnter(call, target),
		}},
	}, true
}

func (c *scriptChecker) withAssignmentLocalCallBypass(
	target Expression,
	value Expression,
	walk func() bool,
) bool {
	names := assignmentLocalCallBypassNames(target, value)
	if len(names) == 0 {
		return walk()
	}
	scopes := make(map[string]int, len(names))
	for name := range names {
		scope := len(c.scopes) - 1
		for ; scope >= 0; scope-- {
			if _, bound := c.scopes[scope][name]; bound {
				break
			}
		}
		if scope < 0 {
			scope = len(c.scopes) - 1
		}
		scopes[name] = scope
	}
	c.localCallBypassScopes = append(c.localCallBypassScopes, scopes)
	defer func() {
		c.localCallBypassScopes = c.localCallBypassScopes[:len(c.localCallBypassScopes)-1]
	}()
	return walk()
}

func (c *scriptChecker) identifierCallShadowed(call *CallExpr, name string) bool {
	if !callUsesBypassableIdentifierResolution(call) ||
		len(c.localCallBypassScopes) == 0 {
		return c.identifierShadowed(name)
	}
	skipped := make(map[int]struct{})
	for _, frame := range c.localCallBypassScopes {
		if scope, bypassed := frame[name]; bypassed {
			skipped[scope] = struct{}{}
		}
	}
	for scope := len(c.scopes) - 1; scope >= 0; scope-- {
		if _, bypassed := skipped[scope]; bypassed {
			continue
		}
		if _, bound := c.scopes[scope][name]; bound {
			return true
		}
	}
	return false
}

// scriptDispatchIvarEffects reports only effects that can reach the caller's
// current self. Parameter defaults may run after binding starts even when a
// later default prevents the method body from starting. A method running on a
// distinct receiver has a different self; it affects the caller only by
// invoking a captured callable argument.
func (c *scriptChecker) scriptDispatchIvarEffects(
	selection instanceScriptDispatchSelection,
) regionIvarEffects {
	var effects regionIvarEffects
	if selection.unknown {
		effects.unknown = true
		return effects
	}
	for _, selected := range selection.targets {
		if !selected.bindingStarts {
			continue
		}
		currentSelf := c.scriptDispatchRunsOnCurrentSelf(selected)
		sameInstanceClass := !currentSelf && !selected.classMethod &&
			!c.selfClassContext && sameScriptClass(selected.classDef, c.selfClass)
		if sameInstanceClass && selected.mayEnter {
			effects.unknown = true
			continue
		}
		scan := c.scriptFunctionEffectScan(selected.call, selected.target)
		if scan == nil {
			effects.unknown = true
			continue
		}
		if currentSelf || sameInstanceClass {
			mergeScriptFunctionDirectIvarEffects(&effects, scan, selected.target.fn)
		}
		if scan.invokedUnknownCallable {
			effects.unknown = true
			continue
		}
		for fn := range scan.invokedSelfFunctions {
			mergeScriptFunctionDirectIvarEffects(&effects, scan, fn)
		}
		var callerLambdas map[*BlockLiteral]struct{}
		if !selected.mayEnter && !currentSelf && !sameInstanceClass {
			callerLambdas = callerLambdaArgumentBlocks(selected.call)
		}
		for block := range scan.invokedLambdas {
			if callerLambdas != nil {
				if _, callerOwned := callerLambdas[block]; !callerOwned {
					continue
				}
			}
			c.collectRepeatedRegionIvarEffectsFromBlock(block, &effects)
		}
	}
	return effects
}

func mergeScriptFunctionDirectIvarEffects(
	effects *regionIvarEffects,
	scan *namespaceMutationScan,
	fn *ScriptFunction,
) {
	if effects == nil || scan == nil || fn == nil {
		return
	}
	if _, unknown := scan.unknownDirectIvarEffects[fn]; unknown {
		effects.unknown = true
	}
	for name := range scan.directIvarWrites[fn] {
		if effects.writes == nil {
			effects.writes = make(map[string]struct{})
		}
		effects.writes[name] = struct{}{}
	}
}

func (c *scriptChecker) binaryExpressionMayComplete(expr *BinaryExpr) bool {
	var receiverType *TypeExpr
	if expr != nil {
		receiverType = c.inferExpressionType(expr.Left)
	}
	return c.binaryExpressionMayCompleteWithReceiver(expr, receiverType)
}

func (c *scriptChecker) binaryExpressionMayCompleteWithReceiver(
	expr *BinaryExpr,
	receiverType *TypeExpr,
) bool {
	if expr == nil || expr.Operator == tokenAnd || expr.Operator == tokenOr {
		return true
	}
	if c.binaryExpressionProvablyAborts(expr) {
		return false
	}
	methods := binaryDispatchMethodNames(expr.Operator)
	if len(methods) == 0 {
		return true
	}
	selection := c.binaryScriptDispatch(expr, receiverType)
	if selection.unknown {
		return true
	}
	for _, selected := range selection.targets {
		if selected.mayEnter && c.scriptFunctionCallMayComplete(
			selected.call,
			selected.target,
		) {
			return true
		}
	}
	if selection.instanceFallbacks > 0 &&
		(expr.Operator == tokenEQ || expr.Operator == tokenNotEQ) {
		return true
	}
	rightType := c.inferExpressionType(expr.Right)
	for _, arm := range selection.fallbackArms {
		if !c.binaryOperationOutcome(expr.Operator, arm, rightType).invalid {
			return true
		}
	}
	return false
}

func (c *scriptChecker) binaryExpressionProvablyAborts(expr *BinaryExpr) bool {
	if expr == nil || expr.Operator != tokenSlash && expr.Operator != tokenPercent {
		return false
	}
	right, static := staticLiteralValue(expr.Right)
	if !static || right.Kind() != KindInt || !intValueIsZero(right) {
		return false
	}
	leftKind, known := staticOperandKind(c.inferExpressionType(expr.Left))
	return known && leftKind == TypeInt
}

func (c *scriptChecker) binaryExpressionProvablyCompletes(expr *BinaryExpr) bool {
	if expr == nil || expr.Operator == tokenAnd || expr.Operator == tokenOr {
		return true
	}
	switch expr.Operator {
	case tokenPlus, tokenMinus, tokenAsterisk:
	default:
		return false
	}
	leftKind, leftOK := staticOperandKind(c.inferExpressionType(expr.Left))
	rightKind, rightOK := staticOperandKind(c.inferExpressionType(expr.Right))
	if !leftOK || !rightOK {
		return false
	}
	for _, left := range expandNumericKinds(leftKind) {
		for _, right := range expandNumericKinds(rightKind) {
			if left != TypeInt && left != TypeFloat || right != TypeInt && right != TypeFloat {
				return false
			}
			if _, valid := binaryScalarOutcome(expr.Operator, left, right); !valid {
				return false
			}
		}
	}
	return true
}

func (c *scriptChecker) indexExpressionMayComplete(expr *IndexExpr) bool {
	var receiverType *TypeExpr
	if expr != nil {
		receiverType = c.inferExpressionType(expr.Object)
	}
	return c.indexExpressionMayCompleteWithReceiver(expr, receiverType)
}

func (c *scriptChecker) indexExpressionMayCompleteWithReceiver(
	expr *IndexExpr,
	receiverType *TypeExpr,
) bool {
	if expr == nil {
		return true
	}
	defaults, exact := c.captureDirectCoreHashDefaults(expr.Object)
	return c.indexExpressionMayCompleteWithReceiverAndDefaults(
		expr,
		receiverType,
		defaults,
		exact,
	)
}

func (c *scriptChecker) indexExpressionMayCompleteWithReceiverAndDefaults(
	expr *IndexExpr,
	receiverType *TypeExpr,
	defaults []directCoreHashDefaultCapture,
	defaultsExact bool,
) bool {
	if expr == nil {
		return true
	}
	if defaultsExact {
		return c.directCoreHashDefaultMayComplete(expr, receiverType, defaults)
	}
	if c.indexedHashOperationProvablyAbortsWithReceiver(expr, receiverType) {
		return false
	}
	selection := c.indexScriptDispatch(expr, receiverType)
	if selection.unknown {
		return true
	}
	for _, selected := range selection.targets {
		if selected.mayEnter && c.scriptFunctionCallMayComplete(
			selected.call,
			selected.target,
		) {
			return true
		}
	}
	for _, arm := range selection.fallbackArms {
		if c.builtinIndexArmMayComplete(expr, arm) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) builtinIndexArmMayComplete(expr *IndexExpr, arm *TypeExpr) bool {
	if expr == nil || arm == nil {
		return true
	}
	switch arm.Kind {
	case TypeAny, TypeUnknown:
		return true
	case TypeArray, TypeString:
		switch len(expr.Indices) {
		case 1:
			allowed := unionTypeExprs(checkTypeNumber, checkTypeRange)
			return c.callArgumentMayBindType(expr.Indices[0], allowed)
		case 2:
			return c.callArgumentMayBindType(expr.Indices[0], checkTypeNumber) &&
				c.callArgumentMayBindType(expr.Indices[1], checkTypeNumber)
		default:
			return false
		}
	case TypeHash, TypeShape:
		if len(expr.Indices) != 1 {
			return false
		}
		keyType := c.inferExpressionType(expr.Indices[0])
		return keyType == nil || !typeExprProvablyUnstorableKey(keyType)
	default:
		return false
	}
}

func (c *scriptChecker) builtinIndexSetterArmMayComplete(
	target *IndexExpr,
	arm *TypeExpr,
	receiver checkAssignmentReceiverCapture,
) bool {
	if target == nil || arm == nil {
		return true
	}
	switch arm.Kind {
	case TypeAny, TypeUnknown:
		return true
	case TypeArray:
		return len(target.Indices) == 1 &&
			!c.exactArrayIndexWriteOutOfBoundsWithReceiver(target, receiver) &&
			c.callArgumentMayBindType(target.Indices[0], checkTypeNumber)
	case TypeHash, TypeShape:
		if len(target.Indices) != 1 {
			return false
		}
		keyType := c.inferExpressionType(target.Indices[0])
		return keyType == nil || !typeExprProvablyUnstorableKey(keyType)
	default:
		return false
	}
}

func (c *scriptChecker) builtinIndexSetterArmMustComplete(
	target *IndexExpr,
	arm *TypeExpr,
	receiver checkAssignmentReceiverCapture,
) bool {
	if target == nil || arm == nil {
		return false
	}
	switch arm.Kind {
	case TypeArray:
		return len(target.Indices) == 1 &&
			c.exactArrayIndexWriteProvablyInBoundsWithReceiver(target, receiver) &&
			c.callArgumentMustBindType(target.Indices[0], checkTypeNumber)
	case TypeHash, TypeShape:
		if len(target.Indices) != 1 {
			return false
		}
		if _, static := staticLiteralHashKey(target.Indices[0]); static {
			return true
		}
		keyType := c.inferExpressionType(target.Indices[0])
		return keyType != nil && typeExprProvablyStorableHashKey(keyType)
	default:
		return false
	}
}

func typeExprProvablyStorableHashKey(ty *TypeExpr) bool {
	return typeExprArmsAll(ty, func(arm *TypeExpr) bool {
		if _, shapeValue := shapeValuePayload(arm); shapeValue {
			return false
		}
		switch arm.Kind {
		case TypeNil, TypeBool, TypeInt, TypeString, TypeSymbol, TypeRange:
			return true
		default:
			return false
		}
	})
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

func (c *scriptChecker) checkPlainAssignmentTarget(
	function string,
	target Expression,
	value Expression,
) bool {
	switch typed := target.(type) {
	case nil, *Identifier, *ClassVarExpr:
		return true
	case *IvarExpr:
		return c.ivarAssignmentMayComplete(typed, value)
	case *MemberExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Object)
		receiver, _ := c.assignmentReceiverSnapshot(typed)
		opaqueEffects := c.memberSetterHasOpaqueClassConstantEffects(typed)
		completed := c.checkAssignmentSetterDispatch(function, typed, value, receiver)
		if opaqueEffects {
			c.markOpaqueClassConstants()
		}
		return completed
	case *IndexExpr:
		if !c.checkExpressionWithAuto(function, typed.Object, true) {
			return false
		}
		c.collectRuntimeRequireCallExportsFromExpression(typed.Object)
		c.captureEvaluatedDestructureFact(typed.Object)
		receiver, _ := c.assignmentReceiverSnapshot(typed)
		dispatchType := receiver.receiverType
		opaqueDispatch := c.instanceDispatchHasOpaqueClassConstantEffects(
			dispatchType,
			"[]=",
		)
		for _, index := range typed.Indices {
			if !c.checkExpressionWithAuto(function, index, true) {
				return false
			}
			c.collectRuntimeRequireCallExportsFromExpression(index)
			c.captureEvaluatedDestructureFact(index)
			c.pinExpressionFact(index, c.inferExpressionType(index))
		}
		c.enqueueReachableInstanceDispatch(dispatchType, "[]=")
		completed := c.checkAssignmentSetterDispatch(function, typed, value, receiver)
		if opaqueDispatch {
			c.markOpaqueClassConstants()
		}
		return completed
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

func (c *scriptChecker) replayDestructureAssignment(
	function string,
	target *DestructureTarget,
	value Expression,
) bool {
	facts := c.captureDestructureValueFacts(target, value)
	return c.replayCapturedDestructureAssignment(function, facts)
}

func (c *scriptChecker) replayCapturedDestructureAssignment(
	function string,
	facts []capturedDestructureValueFact,
) bool {
	for _, fact := range facts {
		if fact.target == nil {
			continue
		}
		if _, nested := fact.target.(*DestructureTarget); nested {
			if !c.replayCapturedDestructureAssignment(
				function,
				c.expandCapturedNestedDestructureFact(fact),
			) {
				return false
			}
			continue
		}
		fact = c.refreshCapturedDestructureContainerFact(fact)
		if _, ident := fact.target.(*Identifier); ident {
			c.bindCapturedDestructureValueFact(fact)
			c.recordRuntimeBindingTarget(fact.target)
			c.recordBindingTarget(fact.target)
			continue
		}

		leafValue := fact.value
		if leafValue == nil {
			leafValue = &Identifier{
				Name:     "\x00destructure-value",
				Position: fact.target.Pos(),
			}
		}
		c.pinExpressionFact(leafValue, fact.assigned)
		var indexedReceiverFact *TypeExpr
		if index, ok := fact.target.(*IndexExpr); ok {
			if ident, direct := index.Object.(*Identifier); direct {
				indexedReceiverFact = c.localTypeFor(ident.Name)
			}
		}
		leaf := &AssignStmt{
			Target:   fact.target,
			Value:    leafValue,
			Position: fact.target.Pos(),
		}
		completed := true
		c.withCapturedDestructureArgumentFact(leafValue, fact, func() {
			if ivar, ok := fact.target.(*IvarExpr); ok &&
				c.ivarAssignmentMayComplete(ivar, leafValue) &&
				!c.ivarWriteProvablyCompletes(ivar.Name, leafValue) {
				c.captureNonCompletingExpressionArm()
			}
			completed = c.checkPlainAssignmentTarget(function, fact.target, leafValue)
		})
		if !completed {
			if _, ivar := fact.target.(*IvarExpr); ivar {
				c.withCapturedDestructureArgumentFact(leafValue, fact, func() {
					c.inferAssignStatementTypes(function, leaf, indexedReceiverFact, nil)
				})
			}
			return false
		}
		c.withCapturedDestructureArgumentFact(leafValue, fact, func() {
			c.inferAssignStatementTypes(function, leaf, indexedReceiverFact, nil)
		})
		c.recordRuntimeBindingTarget(fact.target)
		c.recordBindingTarget(fact.target)
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
	captured ...checkDynamicCallCandidates,
) ([]assignmentMemberReceiver, bool) {
	if member == nil {
		return nil, false
	}
	var candidates checkDynamicCallCandidates
	if len(captured) > 0 {
		candidates = captured[0]
	} else {
		candidates = c.captureDynamicCallCandidates(assignmentSetterCall(member, nil))
	}
	return c.assignmentMemberReceiversFromCandidates(candidates)
}

func (c *scriptChecker) assignmentMemberReceiversFromCandidates(
	candidates checkDynamicCallCandidates,
) ([]assignmentMemberReceiver, bool) {
	if candidates.instancesExact {
		receivers := make([]assignmentMemberReceiver, 0, len(candidates.instanceClasses))
		for _, className := range candidates.instanceClasses {
			classDef := c.script.classes[className]
			if classDef == nil {
				return nil, false
			}
			receivers = append(receivers, assignmentMemberReceiver{class: classDef})
		}
		return receivers, true
	}
	if !candidates.classValuesExact {
		return nil, false
	}
	receivers := make([]assignmentMemberReceiver, 0, len(candidates.classValues))
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
	return receivers, true
}

func (c *scriptChecker) assignmentValueExpectation(
	target Expression,
	value Expression,
) expressionExpectation {
	if !memberAssignmentValueCanUseExpectation(value) {
		return expressionExpectation{}
	}
	if ivar, ok := target.(*IvarExpr); ok {
		if c.selfClass == nil || c.selfClassContext {
			return expressionExpectation{}
		}
		_, ty := propertyContract(c.selfClass, ivar.Name)
		return typeExpressionExpectation(ty)
	}
	member, ok := target.(*MemberExpr)
	if !ok || !c.memberAssignmentReceiverCanUseExpectation(member.Object) {
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

func (c *scriptChecker) memberAssignmentReceiverCanUseExpectation(expr Expression) bool {
	if !memberAssignmentReceiverSyntaxCanUseExpectation(expr) {
		return false
	}
	switch typed := expr.(type) {
	case *CallExpr:
		target, resolved := c.resolveCallable(typed)
		return resolved && (target.constructorClass != "" ||
			target.fn != nil && target.fn.ReturnTy != nil)
	case *MemberExpr:
		if !c.memberAssignmentReceiverCanUseExpectation(typed.Object) {
			return false
		}
		target, resolved := c.resolveMemberCallable(typed)
		return resolved && (target.constructorClass != "" ||
			target.fn != nil && target.fn.ReturnTy != nil)
	case *Identifier:
		if c.identifierShadowed(typed.Name) {
			return true
		}
		if fn := c.script.functions[typed.Name]; fn != nil && len(fn.Params) == 0 {
			return fn.ReturnTy != nil
		}
		if fn, ok := c.typeRootFunction(typed.Name); ok && len(fn.Params) == 0 {
			return fn.ReturnTy != nil
		}
		if fn := c.implicitSelfFunction(typed.Name); fn != nil {
			return fn.ReturnTy != nil
		}
	}
	return true
}

func memberAssignmentReceiverSyntaxCanUseExpectation(expr Expression) bool {
	return staticMemberAssignmentReceiverCanUseExpectation(expr) ||
		boundMemberAssignmentReceiverCanUseExpectation(expr)
}

func staticMemberAssignmentReceiverCanUseExpectation(expr Expression) bool {
	switch typed := expr.(type) {
	case *Identifier, *IvarExpr, *ClassVarExpr, *CallExpr:
		return true
	case *MemberExpr:
		return staticMemberAssignmentReceiverCanUseExpectation(typed.Object)
	default:
		return false
	}
}

func boundMemberAssignmentReceiverCanUseExpectation(expr Expression) bool {
	switch typed := expr.(type) {
	case *Identifier, *IvarExpr, *ClassVarExpr:
		return true
	case *IndexExpr:
		if len(typed.Indices) != 1 ||
			!boundMemberAssignmentReceiverCanUseExpectation(typed.Object) {
			return false
		}
		switch typed.Indices[0].(type) {
		case *IntegerLiteral, *StringLiteral, *SymbolLiteral:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func (c *scriptChecker) assignmentSetterMayComplete(
	target, value Expression,
	captured ...checkDynamicCallCandidates,
) bool {
	switch typed := target.(type) {
	case *IvarExpr:
		return c.ivarAssignmentMayComplete(typed, value)
	case *MemberExpr, *IndexExpr:
	default:
		return true
	}
	receiver, _ := c.assignmentReceiverSnapshot(target)
	if len(captured) > 0 {
		receiver.candidates = captured[0]
		receiver.captured = true
	}
	return c.assignmentSetterMayCompleteWithReceiver(target, value, receiver)
}

func (c *scriptChecker) assignmentSetterMayCompleteWithReceiver(
	target, value Expression,
	receiver checkAssignmentReceiverCapture,
) bool {
	switch typed := target.(type) {
	case *IvarExpr:
		return c.ivarAssignmentMayComplete(typed, value)
	case *MemberExpr, *IndexExpr:
	default:
		return true
	}
	if indexed, ok := target.(*IndexExpr); ok {
		if c.exactArrayIndexWriteOutOfBoundsWithReceiver(indexed, receiver) ||
			c.indexedHashOperationProvablyAbortsWithReceiver(indexed, receiver.receiverType) {
			return false
		}
	}
	selection := c.assignmentSetterScriptDispatch(target, value, receiver)
	if selection.unknown || selection.pureFallbacks > 0 {
		return true
	}
	for _, selected := range selection.targets {
		if selected.mayEnter && c.scriptFunctionCallMayComplete(
			selected.call,
			selected.target,
		) {
			return true
		}
	}
	if indexed, ok := target.(*IndexExpr); ok {
		for _, arm := range selection.fallbackArms {
			if c.builtinIndexSetterArmMayComplete(indexed, arm, receiver) {
				return true
			}
		}
	}
	return false
}

func (c *scriptChecker) applyAssignmentSetterIvarEffects(
	selection instanceScriptDispatchSelection,
) {
	if selection.mayReject() {
		c.captureNonCompletingExpressionArm()
	}
	if !selection.mayRunScript() {
		return
	}
	c.widenRegionIvarFacts(c.scriptDispatchIvarEffects(selection))
	c.captureNonCompletingExpressionArm()
}

func (c *scriptChecker) checkAssignmentSetterDispatch(
	function string,
	target Expression,
	value Expression,
	receiver checkAssignmentReceiverCapture,
) bool {
	completed := true
	c.withEvaluatedAssignmentSetterArgumentFacts(target, value, func() {
		selection := c.assignmentSetterScriptDispatch(target, value, receiver)
		if _, member := target.(*MemberExpr); member {
			c.enqueueReachableMemberSetterCalls(selection)
		}
		c.checkGeneratedAssignmentSetterArgument(function, selection)
		c.applyAssignmentSetterIvarEffects(selection)
		completed = c.assignmentSetterMayCompleteWithReceiver(target, value, receiver)
		c.applyAssignmentNamespaceMutations(target, value, receiver.candidates)
	})
	return completed
}

// enqueueReachableMemberSetterCalls preserves the generated assignment call's
// evaluated argument facts and exact default-binding plan.
func (c *scriptChecker) enqueueReachableMemberSetterCalls(
	selection instanceScriptDispatchSelection,
) {
	for _, selected := range selection.targets {
		if !selected.bindingStarts || selected.call == nil || selected.target.fn == nil {
			continue
		}
		plan := c.scriptCallBindingPlan(selected.call, selected.target)
		facts := c.reachableCallParamFacts(selected.call, selected.target)
		c.enqueueReachableFunctionBindingForCall(
			selected.target.name,
			selected.target.fn,
			facts,
			plan,
			selected.call,
		)
	}
}

func (c *scriptChecker) checkGeneratedAssignmentSetterArgument(
	function string,
	selection instanceScriptDispatchSelection,
) {
	if selection.unknown || selection.instanceFallbacks > 0 ||
		selection.pureFallbacks > 0 || selection.rejectingFallbacks > 0 ||
		len(selection.fallbackArms) > 0 || len(selection.targets) != 1 {
		return
	}
	selected := selection.targets[0]
	if !selected.bindingStarts || selected.target.fn == nil ||
		selected.target.fn.Accessor != functionAccessorSetter {
		return
	}
	view := staticCallViewFor(selected.call, selected.target)
	c.checkCallArgumentTypes(
		function,
		view,
		selected.target.name,
		selected.target.fn,
	)
}

func (c *scriptChecker) indexedHashOperationProvablyAbortsWithReceiver(
	target *IndexExpr,
	receiver *TypeExpr,
) bool {
	if target == nil {
		return false
	}
	if !typeExprArmsAll(receiver, func(arm *TypeExpr) bool {
		return arm.Kind == TypeHash || arm.Kind == TypeShape || arm.Kind == TypeNil
	}) {
		return false
	}
	if len(target.Indices) != 1 {
		return true
	}
	key := target.Indices[0]
	keyType, captured := c.callArgumentFacts[key]
	if !captured {
		keyType = c.inferExpressionType(key)
	}
	return keyType != nil && typeExprProvablyUnstorableKey(keyType)
}

func (c *scriptChecker) exactArrayIndexWriteOutOfBoundsWithReceiver(
	target *IndexExpr,
	receiver checkAssignmentReceiverCapture,
) bool {
	if target == nil || len(target.Indices) != 1 {
		return false
	}
	indexValue, static := staticLiteralValue(target.Indices[0])
	if !static || indexValue.Kind() != KindInt {
		return false
	}
	if !receiver.staticValuesExact || len(receiver.staticValues) == 0 {
		return false
	}
	index := indexValue.Int()
	for _, value := range receiver.staticValues {
		array, ok := value.(*ArrayLiteral)
		if !ok {
			return false
		}
		length := int64(len(array.Elements))
		normalized := index
		if normalized < 0 {
			normalized += length
		}
		if normalized >= 0 && normalized < length {
			return false
		}
	}
	return true
}

func (c *scriptChecker) exactArrayIndexWriteProvablyInBoundsWithReceiver(
	target *IndexExpr,
	receiver checkAssignmentReceiverCapture,
) bool {
	if target == nil || len(target.Indices) != 1 {
		return false
	}
	indexValue, static := staticLiteralValue(target.Indices[0])
	if !static || indexValue.Kind() != KindInt ||
		!receiver.staticValuesExact || len(receiver.staticValues) == 0 {
		return false
	}
	index := indexValue.Int()
	for _, value := range receiver.staticValues {
		array, ok := value.(*ArrayLiteral)
		if !ok {
			return false
		}
		length := int64(len(array.Elements))
		normalized := index
		if normalized < 0 {
			normalized += length
		}
		if normalized < 0 || normalized >= length {
			return false
		}
	}
	return true
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

func retainedBlockPositionalArgumentExpectation(
	blocks []capturedBlockLiteralValue,
	index int,
	count int,
) expressionExpectation {
	expectations := make([]expressionExpectation, 0, len(blocks))
	for _, block := range blocks {
		if block.block == nil {
			expectations = append(expectations, expressionExpectation{})
			continue
		}
		expectations = append(
			expectations,
			blockArgumentExpectation(block.block.Params, index, count),
		)
	}
	return mergeExpressionExpectations(expectations)
}

func retainedBlockKeywordArgumentExpectation(
	blocks []capturedBlockLiteralValue,
	name string,
) expressionExpectation {
	expectations := make([]expressionExpectation, 0, len(blocks))
	for _, block := range blocks {
		if block.block == nil {
			expectations = append(expectations, expressionExpectation{})
			continue
		}
		expectations = append(
			expectations,
			typeExpressionExpectation(keywordArgumentExpectedType(block.block.Params, name)),
		)
	}
	return mergeExpressionExpectations(expectations)
}

func mergeExpressionExpectations(expectations []expressionExpectation) expressionExpectation {
	merged := expressionExpectation{}
	types := make([]*TypeExpr, 0, len(expectations))
	hasCallableType := false
	hasArrayElement := false
	for _, expectation := range expectations {
		if expectation.ty != nil {
			types = append(types, expectation.ty)
			hasCallableType = hasCallableType ||
				typeExprIncludesCallable(expectation.ty)
		}
		if _, ok := expectation.arrayElementExpectation(); ok {
			hasArrayElement = true
		}
	}
	if len(types) > 0 {
		merged.ty = unionTypeExprs(types...)
		if merged.ty == nil && hasCallableType {
			// An oversized union still has to retain the one property that
			// changes evaluation: any callable arm suppresses bare auto-call.
			merged.ty = checkTypeFunction
		}
	}
	if !hasArrayElement {
		return merged
	}
	merged.arrayElement = func(index, count int) expressionExpectation {
		elements := make([]expressionExpectation, 0, len(expectations))
		for _, expectation := range expectations {
			elementExpectation, ok := expectation.arrayElementExpectation()
			if !ok {
				elements = append(elements, expressionExpectation{})
				continue
			}
			elements = append(elements, elementExpectation(index, count))
		}
		return mergeExpressionExpectations(elements)
	}
	return merged
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
		} else {
			c.captureNonCompletingExpressionArm()
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
	} else {
		c.captureNonCompletingExpressionArm()
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
) (expressionCompleted bool) {
	captureClassBranch := c.beginIfClassBranchCapture(expr)
	var selectedClassBranch Expression
	selectedClassBranchKnown := false
	defer func() {
		c.finishIfClassBranchCapture(
			expr,
			selectedClassBranch,
			selectedClassBranchKnown,
			expressionCompleted,
		)
	}()

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
	captureElseIfBranch := captureClassBranch && conditionKnown && !conditionTruthy
	if captureClassBranch && conditionKnown && conditionTruthy {
		selectedClassBranch = expr.Consequent
		selectedClassBranchKnown = true
	}
	trueReachable := !conditionKnown || conditionTruthy
	if trueReachable {
		trueReachable = c.collectRuntimeConditionOutcomeEffects(expr.Condition, true)
	}
	if trueReachable {
		completed := c.checkExpressionWithExpectation(function, expr.Consequent, expectation)
		if completed {
			branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		} else {
			c.captureNonCompletingExpressionArm()
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
		captureThisElseIf := captureElseIfBranch
		captureThisElseIfTruthy := false
		if captureThisElseIf {
			captureThisElseIfTruthy, captureThisElseIf = c.stableIfClassConditionTruthiness(branch.Condition)
			if !captureThisElseIf {
				captureElseIfBranch = false
			}
		}
		if !c.checkExpressionWithAuto(function, branch.Condition, true) {
			c.captureNonCompletingExpressionArm()
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
		if captureThisElseIf {
			switch {
			case !branchKnown || branchTruthy != captureThisElseIfTruthy:
				captureElseIfBranch = false
			case captureThisElseIfTruthy:
				selectedClassBranch = branch.Result
				selectedClassBranchKnown = true
				captureElseIfBranch = false
			}
		}
		trueReachable = !branchKnown || branchTruthy
		if trueReachable {
			trueReachable = c.collectRuntimeConditionOutcomeEffects(branch.Condition, true)
		}
		if trueReachable {
			completed := c.checkExpressionWithExpectation(function, branch.Result, expectation)
			if completed {
				branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
				branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
			} else {
				c.captureNonCompletingExpressionArm()
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
	if captureElseIfBranch {
		selectedClassBranch = expr.Alternate
		selectedClassBranchKnown = true
	}
	c.restoreRuntimeState(falseRuntimeState)
	c.restoreScopeState(falseScopeState)
	if c.checkExpressionWithExpectation(function, expr.Alternate, expectation) {
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	} else {
		c.captureNonCompletingExpressionArm()
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
	baseStaticValuePoison := cloneCheckStringSet(c.staticValuePoison)
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
	bodyStaticValuePoison := cloneCheckStringSet(c.staticValuePoison)
	bodyReturnsNonLocally := c.expressionReturnsNonLocally
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
			if _, alreadyPoisoned := baseStaticValuePoison[name]; !alreadyPoisoned {
				delete(c.staticValuePoison, name)
				c.bindLocalStaticValues(name, fact.staticVals)
			}
		}
	}
	if len(expressionExitSites) > 0 {
		// The fallback runs on a captured failure arm. Check it without the
		// body's nonlocal-return marker so nested rescue expressions can report
		// a marker of their own.
		c.expressionReturnsNonLocally = false
	}
	fallbackCompleted := c.checkExpressionWithAuto(function, expr.Fallback, autoCall)
	fallbackReturnsNonLocally := c.expressionReturnsNonLocally
	c.collectRuntimeRequireCallExportsFromExpression(expr.Fallback)
	fallbackRuntimeState := c.snapshotRuntimeState()
	fallbackScopeState := c.snapshotScopeState()
	if !fallbackCompleted {
		c.captureNonCompletingExpressionArm()
	}
	c.typePoison = unionCheckStringSet(bodyTypePoison, c.typePoison)
	c.staticValuePoison = unionCheckStringSet(bodyStaticValuePoison, c.staticValuePoison)
	hasCompletingValueArm := bodyCompleted && !bodyReturnsNonLocally ||
		fallbackCompleted && !fallbackReturnsNonLocally
	c.expressionReturnsNonLocally = !hasCompletingValueArm &&
		(bodyReturnsNonLocally || fallbackReturnsNonLocally)

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
				c.captureNonCompletingExpressionArm()
				if len(branchRuntimeStates) == 0 {
					return false
				}
				c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
				c.mergeScopeStates(baseScopeState, branchScopeStates)
				return true
			}
			c.collectRuntimeRequireCallExportsFromExpression(value.Expr)
			if !c.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
				c.captureNonCompletingExpressionArm()
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
			} else {
				c.captureNonCompletingExpressionArm()
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
	} else {
		c.captureNonCompletingExpressionArm()
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
	return ok && staticNilSafeNavigationMember(member)
}

func staticNilSafeNavigationMember(member *MemberExpr) bool {
	if member == nil || !member.Safe {
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
	if c.hashLikeDataMemberLookupProvablyFails(member) {
		return staticCallable{}, false, false, false
	}
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
		previousSelfBindings := c.callArgumentSelfBindings
		previousStaticValues := c.callArgumentStaticValues
		previousStaticChoices := c.callArgumentStaticChoices
		previousSplatSources := c.callArgumentSplatSources
		previousReceiverLength := c.callArrayReceiverLength
		c.callArgumentFacts = map[Expression]*TypeExpr{}
		c.callArgumentClassValues = map[Expression][]string{}
		c.callArgumentCallables = map[Expression][]*ScriptFunction{}
		c.callArgumentSelfBindings = map[Expression]checkCallableSelfBinding{}
		c.callArgumentStaticValues = map[Expression][]Expression{}
		c.callArgumentStaticChoices = map[Expression]checkStaticChoiceFact{}
		c.callArgumentSplatSources = map[Expression]checkCallSplatSource{}
		c.callArrayReceiverLength = checkArrayReceiverLength{}
		bodyMayEnter := c.refineDynamicCallTargetEntry(resolution.targets)
		if resolution.exact && c.exactDynamicCallHasOpaqueClassConstantEffects(resolution) {
			c.markOpaqueClassConstants()
		}
		c.checkDynamicCallTargets(function, resolution.targets, resolution.diagnoseTargets)
		c.applyDynamicCallNamespaceMutations(call, resolution.targets)
		c.callArgumentFacts = previousFacts
		c.callArgumentClassValues = previousClassValues
		c.callArgumentCallables = previousCallables
		c.callArgumentSelfBindings = previousSelfBindings
		c.callArgumentStaticValues = previousStaticValues
		c.callArgumentStaticChoices = previousStaticChoices
		c.callArgumentSplatSources = previousSplatSources
		c.callArrayReceiverLength = previousReceiverLength
		if !resolution.exact {
			c.markOpaqueClassConstants()
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
			// A bare member auto-call has no captured argument facts, so its
			// binding plan must carry exact omitted-default execution into
			// the reachable body check.
			if plan.bindingStarts {
				facts := c.reachableCallInstanceFactsWithConstructorOrigin(
					call,
					target,
					nil,
					member,
				)
				c.enqueueReachableFunctionBindingForCall(
					target.name,
					target.fn,
					facts,
					plan,
					call,
				)
			}
			if plan.bodyMayEnter {
				if !c.scriptCallClassConstantEffectsProvenAbsent(call, target) {
					c.markOpaqueClassConstants()
				}
			} else if !c.scriptCallDefaultPrefixClassConstantEffectsProvenAbsent(call, target) {
				c.markOpaqueClassConstants()
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

// checkBlockResult distinguishes an unknown value from a block proven not to
// complete normally.
type checkBlockResult struct {
	fact        *TypeExpr
	exact       bool
	mayComplete bool
}

// checkBlockLiteral walks a block or lambda body. localReturns marks blocks
// whose returns stay inside the block itself (stabby lambdas and the lambda
// builtin's literal block); a plain block's return unwinds the enclosing
// function instead.
func (c *scriptChecker) checkBlockLiteral(
	function string,
	block *BlockLiteral,
	localReturns bool,
) checkBlockResult {
	return c.checkBlockLiteralWithIvarWidening(
		function,
		block,
		localReturns,
		true,
		!localReturns,
	)
}

func (c *scriptChecker) checkBlockLiteralWithIvarWidening(
	function string,
	block *BlockLiteral,
	localReturns bool,
	widenIvars bool,
	preserveNamespaceWrites bool,
) checkBlockResult {
	if block == nil {
		return checkBlockResult{}
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
		if preserveNamespaceWrites {
			c.preserveRuntimeNamespaceMembers(walkMembers)
		}
	}()

	// Block and lambda returns are not checked against the enclosing
	// function's annotation today, so an active begin/ensure deferral must
	// not capture them either (a lambda return is local to the lambda).
	previousSites := c.deferredReturnSites
	previousExceptionExitSites := c.exceptionExitSites
	previousNonLocalReturnExitSites := c.nonLocalReturnExitSites
	previousEnsureExitSites := c.ensureExitSites
	previousExpressionReturnsNonLocally := c.expressionReturnsNonLocally
	c.deferredReturnSites = nil
	c.exceptionExitSites = nil
	c.nonLocalReturnExitSites = nil
	c.ensureExitSites = nil
	c.expressionReturnsNonLocally = false
	defer func() {
		c.deferredReturnSites = previousSites
		c.exceptionExitSites = previousExceptionExitSites
		c.nonLocalReturnExitSites = previousNonLocalReturnExitSites
		c.ensureExitSites = previousEnsureExitSites
		c.expressionReturnsNonLocally = previousExpressionReturnsNonLocally
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
			c.recordReturnSummaryResult(previousCollector, nil, nil)
		}
	}()

	// A call block may run zero or many times, so outer locals its body
	// assigns lose their facts before the walk. A lambda literal only creates
	// a callable; its body has not run yet, so creation preserves captured
	// outer facts. The walk's own bindings are rolled back in either case.
	var blockEntryScopeState checkScopeState
	var blockEntryTypePoison map[string]struct{}
	var blockEntryStaticValuePoison map[string]struct{}
	var blockEntryDegradedBindings map[string]struct{}
	if !block.Lambda {
		c.degradeBlockBodyBindingsWithIvarWidening(block, widenIvars)
		blockEntryScopeState = c.snapshotScopeState()
		blockEntryTypePoison = cloneCheckStringSet(c.typePoison)
		blockEntryStaticValuePoison = cloneCheckStringSet(c.staticValuePoison)
		blockEntryDegradedBindings = cloneCheckStringSet(c.degradedContainerBindings)
	}
	typesState := c.snapshotLocalTypes()
	defer c.restoreLocalTypes(typesState)
	classValuesState := c.snapshotLocalClassValues()
	defer c.restoreLocalClassValues(classValuesState)

	popScope := c.pushBlockCheckScope(block)
	defer popScope()
	blockBoundNames := cloneCheckScope(c.scopes[len(c.scopes)-1])
	if !block.Lambda {
		c.typePoison = restoreCheckStringSetNames(c.typePoison, nil, blockBoundNames)
		c.staticValuePoison = restoreCheckStringSetNames(c.staticValuePoison, nil, blockBoundNames)
		c.degradedContainerBindings = restoreCheckStringSetNames(
			c.degradedContainerBindings,
			nil,
			blockBoundNames,
		)
		blockWalkScopeState := c.snapshotScopeState()
		removeScopeBindingRelationsForNames(&blockWalkScopeState, blockBoundNames)
		c.restoreScopeState(blockWalkScopeState)
	}
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
	previousBlockResultCollector := c.blockResultCollector
	previousBlockLocalReturnCollector := c.blockLocalReturnCollector
	previousBlockLocalBreakCollector := c.blockLocalBreakCollector
	blockResultCollector := &returnSummaryCollector{}
	c.blockResultCollector = blockResultCollector
	// A plain nested block owns its breaks, while a lambda converts a direct
	// break into its local return value just like an explicit return.
	c.blockLocalBreakCollector = nil
	if localReturns {
		c.blockLocalReturnCollector = blockResultCollector
		c.blockLocalBreakCollector = blockResultCollector
	}
	defer func() {
		c.blockResultCollector = previousBlockResultCollector
		c.blockLocalReturnCollector = previousBlockLocalReturnCollector
		c.blockLocalBreakCollector = previousBlockLocalBreakCollector
	}()
	c.mutationRegionDepth++
	fallsThrough := c.checkStatements(label, nil, block.Body)
	c.mutationRegionDepth--
	if !block.Lambda {
		bodyExitScopeState := c.snapshotScopeState()
		c.typePoison = restoreCheckStringSetNames(
			c.typePoison,
			blockEntryTypePoison,
			blockBoundNames,
		)
		c.staticValuePoison = restoreCheckStringSetNames(
			c.staticValuePoison,
			blockEntryStaticValuePoison,
			blockBoundNames,
		)
		c.degradedContainerBindings = restoreCheckStringSetNames(
			c.degradedContainerBindings,
			blockEntryDegradedBindings,
			blockBoundNames,
		)
		restoreScopeBindingRelationsForNames(
			&bodyExitScopeState,
			blockEntryScopeState,
			blockBoundNames,
		)
		c.mergeScopeBindingRelations([]checkScopeState{blockEntryScopeState, bodyExitScopeState})
	}
	if fallsThrough {
		blockResultCollector.record(c.blockImplicitResultFact(block.Body))
	}
	if blockResultCollector.unknown {
		return checkBlockResult{mayComplete: true}
	}
	if len(blockResultCollector.arms) == 0 {
		return checkBlockResult{exact: true}
	}
	return checkBlockResult{
		fact:        unionTypeExprs(blockResultCollector.arms...),
		exact:       true,
		mayComplete: true,
	}
}

func (c *scriptChecker) blockImplicitResultFact(statements []Statement) *TypeExpr {
	collector := &returnSummaryCollector{}
	previousCollector := c.returnCollector
	previousStates := c.implicitReturnStates
	c.returnCollector = collector
	c.implicitReturnStates = nil
	c.collectImplicitResultFacts(statements)
	c.returnCollector = previousCollector
	c.implicitReturnStates = previousStates
	if collector.unknown || len(collector.arms) == 0 {
		return nil
	}
	return unionTypeExprs(collector.arms...)
}

func (c *scriptChecker) blockLiteralValuesResult(
	function string,
	blocks []checkBlockLiteralValue,
) checkBlockResult {
	if len(blocks) == 0 {
		return checkBlockResult{}
	}
	results := make([]*TypeExpr, 0, len(blocks))
	for _, blockValue := range blocks {
		if blockValue.lambda && lambdaLiteralArity(blockValue.block) != 1 {
			continue
		}
		var result checkBlockResult
		c.withSuppressedWarnings(func() {
			result = c.checkBlockLiteral(function, blockValue.block, blockValue.lambda)
		})
		if !result.exact {
			return checkBlockResult{mayComplete: true}
		}
		if !result.mayComplete {
			continue
		}
		results = append(results, result.fact)
	}
	if len(results) == 0 {
		return checkBlockResult{exact: true}
	}
	return checkBlockResult{
		fact:        unionTypeExprs(results...),
		exact:       true,
		mayComplete: true,
	}
}

// checkCapturedBlockLiteral validates a constructor's retained block without
// treating its body as evaluated. Local, namespace, and ivar effects are
// applied only at invocation sites; non-local returns still inform summaries.
func (c *scriptChecker) checkCapturedBlockLiteral(
	function string,
	block *BlockLiteral,
	localReturns bool,
) {
	restoreInference := c.withClonedLocalInferenceScope()
	defer restoreInference()
	// This is an out-of-order validation walk, not an evaluation of the
	// retained body. Nested constructor identities are valid only within this
	// walk; the body must resolve them again under the namespace in effect
	// when the retained block is actually invoked.
	c.evaluatedBlockValues = nil
	c.evaluatedHashDefaults = nil
	c.checkBlockLiteralWithIvarWidening(
		function,
		block,
		localReturns,
		false,
		false,
	)
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
	if c.callTargetsBlockCapturingBuiltin(call, target, resolved) {
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
	if target.name == "array.fill" {
		return c.arrayFillBlockMayEvaluate(call)
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
	if c.callTargetsBlockCapturingBuiltin(call, target, true) {
		return false
	}
	if target.fn != nil {
		return c.functionMayEvaluateCallBlock(call, target, seen)
	}
	if target.name == "array.fetch" {
		return staticArrayFetchBlockMayEvaluate(call)
	}
	return target.spec.usesBlock
}

func (c *scriptChecker) callMayInvokeSuppliedBlock(call *CallExpr) bool {
	return c.callMayInvokeSuppliedBlockWithSeen(call, nil)
}

func (c *scriptChecker) callMayInvokeSuppliedBlockWithSeen(
	call *CallExpr,
	seen map[*ScriptFunction]struct{},
) bool {
	target, resolved := c.resolveCallable(call)
	var dynamicResolution checkDynamicCallResolution
	if !resolved {
		dynamicResolution = c.exactDynamicCallTargets(
			call,
			target,
			false,
			c.captureDynamicCallCandidates(call),
		)
	}
	return c.resolvedCallMayInvokeSuppliedBlockWithSeen(
		call,
		target,
		resolved,
		dynamicResolution,
		seen,
	)
}

func (c *scriptChecker) resolvedCallMayInvokeSuppliedBlockWithSeen(
	call *CallExpr,
	target staticCallable,
	resolved bool,
	dynamicResolution checkDynamicCallResolution,
	seen map[*ScriptFunction]struct{},
) bool {
	if call == nil || c.callBlockKnownAbsent(call) ||
		staticNilSafeNavigationCall(call) {
		return false
	}
	if c.callTargetsBlockCapturingBuiltin(call, target, resolved) {
		return false
	}
	if !resolved {
		if dynamicResolution.lookupFails {
			return false
		}
		if !dynamicResolution.exact {
			return !staticallyNonCallableCallee(call.Callee)
		}
		if dynamicResolution.nonScriptMayComplete {
			return true
		}
		for _, candidate := range dynamicResolution.targets {
			if candidate.bindingStarts && c.functionMayEvaluateCallBlock(
				candidate.call,
				candidate.target,
				seen,
			) {
				return true
			}
		}
		return false
	}
	if target.fn != nil {
		return c.functionMayEvaluateCallBlock(call, target, seen)
	}
	if target.name == "array.fetch" {
		return staticArrayFetchBlockMayEvaluate(call)
	}
	if target.name == "array.fill" {
		return c.arrayFillBlockMayEvaluate(call)
	}
	return target.spec.usesBlock
}

func staticArrayFetchBlockMayEvaluate(call *CallExpr) bool {
	if call == nil || (call.Block == nil && call.BlockArg == nil) {
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
	facts := c.reachableCallParamFacts(call, target)
	return c.functionBindingMayEvaluateCallBlock(fn, plan, facts, seen)
}

func (c *scriptChecker) scriptFunctionCallBlockMayRun(call *CallExpr, target staticCallable) bool {
	return c.functionMayEvaluateCallBlock(call, target, nil)
}

func (c *scriptChecker) functionBindingMayEvaluateCallBlock(
	fn *ScriptFunction,
	plan scriptCallBindingPlan,
	facts map[string]checkReachableParamFact,
	seen map[*ScriptFunction]struct{},
) bool {
	if fn == nil || !plan.bindingStarts {
		return false
	}
	restoreResolution := c.withClassConstantProofResolution(fn, nil)
	defer restoreResolution()
	previousScopes := c.scopes
	restoreInference := c.withIsolatedLocalInference()
	c.scopes = nil
	popScope := c.pushScope(make(map[string]struct{}))
	defer func() {
		popScope()
		restoreInference()
		c.scopes = previousScopes
	}()
	previousFacts := c.reachableParamFacts
	c.reachableParamFacts = facts
	defer func() { c.reachableParamFacts = previousFacts }()
	previousPending := c.pendingBindingParams
	c.pendingBindingParams = functionParamBindingNames(fn)
	defer func() { c.pendingBindingParams = previousPending }()

	c.seedInstanceIvarFacts(fn)
	c.linkReachableParamAliases(fn.Params)
	defaults := make(map[int]struct{}, len(plan.defaultParams))
	for _, index := range plan.defaultParams {
		defaults[index] = struct{}{}
	}
	for i, param := range fn.Params {
		if _, runs := defaults[i]; runs &&
			c.defaultExpressionMayEvaluateCallBlock(param, seen) {
			return true
		}
		if i >= plan.boundParamCount {
			return false
		}
		c.withSuppressedWarnings(func() {
			c.checkIvarParamBinding("", fn, param)
		})
		c.recordParamBinding(param)
		c.applyReachableParamFact(param)
		removeFunctionParamBindingNames(c.pendingBindingParams, param)
	}
	return plan.bodyMayEnter && c.statementsMayEvaluateCallBlock(fn.Body, seen)
}

func (c *scriptChecker) defaultExpressionMayEvaluateCallBlock(
	param Param,
	seen map[*ScriptFunction]struct{},
) bool {
	expr := param.DefaultVal
	if bindingDefaultExpectation(param).includesCallable() {
		if _, bindable := c.bareMemberArgumentCallableFact(expr); bindable {
			return false
		}
		if _, bindable := bareIdentifierCallableValue(expr); bindable {
			return false
		}
	}
	return c.expressionMayEvaluateCallBlock(expr, seen)
}

func (c *scriptChecker) statementsMayEvaluateCallBlock(statements []Statement, seen map[*ScriptFunction]struct{}) bool {
	for _, stmt := range statements {
		if c.statementMayEvaluateCallBlock(stmt, seen) {
			return true
		}
		if statementAlwaysExits(stmt) || !c.statementMayCompleteForBinding(stmt) {
			return false
		}
		c.replayCallBlockStatementFacts(stmt)
	}
	return false
}

// replayCallBlockStatementFacts keeps exact callable dispatch synchronized
// with assignments already passed by the ordered block-evaluation scan.
// Direct assignments can reuse normal inference. Regions with multiple or
// repeated paths must instead discard every fact they might replace; keeping
// the state from whichever branch happened to be scanned last can otherwise
// prove incorrectly that a later call will ignore its supplied block.
func (c *scriptChecker) replayCallBlockStatementFacts(stmt Statement) {
	switch typed := stmt.(type) {
	case *AssignStmt:
		c.recordBindingTarget(typed.Target)
		switch typed.Target.(type) {
		case *Identifier, *DestructureTarget:
			c.withSuppressedWarnings(func() {
				c.inferAssignStatementTypes("", typed, nil, nil)
			})
		}
	case *IfStmt:
		if !c.ifStatementHasExactBranch(typed) {
			c.degradeLocalTypesForBindings([]Statement{typed})
		}
		c.recordLocalBindings([]Statement{typed})
	case *ForStmt:
		if !staticIterableProvenEmpty(typed.Iterable) &&
			!staticIterableProvenSingle(typed.Iterable) &&
			loopBodyMayReachBackedge(typed.Body) {
			c.degradeLocalTypesForBindings(typed.Body, typed.Target)
		}
		c.recordLocalBindings([]Statement{typed})
	case *WhileStmt:
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if !known || truthy {
			c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
		}
		c.recordLocalBindings([]Statement{typed})
	case *UntilStmt:
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if !known || !truthy {
			c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
		}
		c.recordLocalBindings([]Statement{typed})
	case *TryStmt:
		c.degradeLocalTypesForBindings([]Statement{typed})
		c.recordLocalBindings([]Statement{typed})
	}
}

func (c *scriptChecker) ifStatementHasExactBranch(stmt *IfStmt) bool {
	if stmt == nil {
		return true
	}
	truthy, known := c.inferredConditionTruthiness(stmt.Condition)
	if !known || truthy {
		return known
	}
	for _, branch := range stmt.ElseIf {
		truthy, known = c.inferredConditionTruthiness(branch.Condition)
		if !known || truthy {
			return known
		}
	}
	return true
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
	case *TryStmt:
		return c.tryStatementMayCompleteForBinding(typed)
	case *ReturnStmt, *RaiseStmt, *BreakStmt, *NextStmt, *RetryStmt:
		return false
	default:
		return true
	}
}

func (c *scriptChecker) tryStatementMayCompleteForBinding(stmt *TryStmt) bool {
	if stmt == nil {
		return true
	}
	if !c.statementsMayCompleteForBinding(stmt.Ensure) {
		return false
	}
	if c.statementsMayCompleteForBinding(stmt.Body) &&
		c.statementsMayCompleteForBinding(stmt.Else) {
		return true
	}
	if statementsProvenNonRaising(stmt.Body) {
		return false
	}
	selected, exact := c.staticallySelectedRescue(stmt.Body, stmt.Rescues)
	if exact {
		if selected < 0 || len(stmt.Rescues[selected].Body) == 0 {
			return false
		}
		return c.statementsMayCompleteForBinding(stmt.Rescues[selected].Body)
	}
	for i := range stmt.Rescues {
		if len(stmt.Rescues[i].Body) > 0 &&
			c.statementsMayCompleteForBinding(stmt.Rescues[i].Body) {
			return true
		}
	}
	return false
}

func (c *scriptChecker) statementsMayCompleteForBinding(statements []Statement) bool {
	for _, stmt := range statements {
		if statementAlwaysExits(stmt) || !c.statementMayCompleteForBinding(stmt) {
			return false
		}
	}
	return true
}

type lambdaBindingCompletionFlow struct {
	fallsThrough bool
	completes    bool
	fails        bool
	breaks       bool
	continues    bool
}

// immediateLambdaBodyMayCompleteForBinding keeps strict-lambda control local
// without discarding the exact noncompletion proofs used during call binding.
func (c *scriptChecker) immediateLambdaBodyMayCompleteForBinding(
	block *BlockLiteral,
) bool {
	if block == nil {
		return false
	}
	flow := c.immediateLambdaStatementsCompletionFlow(block.Body, 0)
	return flow.fallsThrough || flow.completes
}

func (c *scriptChecker) immediateLambdaStatementsCompletionFlow(
	statements []Statement,
	loopDepth int,
) lambdaBindingCompletionFlow {
	flow := lambdaBindingCompletionFlow{fallsThrough: true}
	for _, stmt := range statements {
		if !flow.fallsThrough {
			break
		}
		current := c.immediateLambdaStatementCompletionFlow(stmt, loopDepth)
		flow.completes = flow.completes || current.completes
		flow.fails = flow.fails || current.fails
		flow.breaks = flow.breaks || current.breaks
		flow.continues = flow.continues || current.continues
		flow.fallsThrough = current.fallsThrough
	}
	return flow
}

func (c *scriptChecker) immediateLambdaStatementCompletionFlow(
	stmt Statement,
	loopDepth int,
) lambdaBindingCompletionFlow {
	switch typed := stmt.(type) {
	case nil:
		return lambdaBindingCompletionFlow{fallsThrough: true}
	case *ReturnStmt:
		return c.immediateLambdaControlCompletionFlow(typed.Value, true, false, false)
	case *BreakStmt:
		return c.immediateLambdaControlCompletionFlow(
			typed.Value,
			loopDepth == 0,
			loopDepth > 0,
			false,
		)
	case *NextStmt:
		return c.immediateLambdaControlCompletionFlow(
			typed.Value,
			loopDepth == 0,
			false,
			loopDepth > 0,
		)
	case *RaiseStmt, *RetryStmt:
		return lambdaBindingCompletionFlow{fails: true}
	case *IfStmt:
		return c.immediateLambdaIfCompletionFlow(typed, loopDepth)
	case *ForStmt:
		return c.immediateLambdaForCompletionFlow(typed, loopDepth)
	case *WhileStmt:
		return c.immediateLambdaWhileCompletionFlow(typed, loopDepth)
	case *UntilStmt:
		return c.immediateLambdaUntilCompletionFlow(typed, loopDepth)
	case *TryStmt:
		return c.immediateLambdaTryCompletionFlow(typed, loopDepth)
	case *ExprStmt:
		return lambdaBindingCompletionFlow{
			fallsThrough: c.expressionMayCompleteForBinding(typed.Expr),
			fails:        !expressionProvenNonRaising(typed.Expr),
		}
	default:
		return lambdaBindingCompletionFlow{
			fallsThrough: c.statementMayCompleteForBinding(stmt),
			fails:        true,
		}
	}
}

func (c *scriptChecker) immediateLambdaControlCompletionFlow(
	expr Expression,
	completes bool,
	breaks bool,
	continues bool,
) lambdaBindingCompletionFlow {
	flow := lambdaBindingCompletionFlow{
		fails: !expressionProvenNonRaising(expr),
	}
	if c.expressionMayCompleteForBinding(expr) {
		flow.completes = completes
		flow.breaks = breaks
		flow.continues = continues
	}
	return flow
}

func (c *scriptChecker) immediateLambdaIfCompletionFlow(
	stmt *IfStmt,
	loopDepth int,
) lambdaBindingCompletionFlow {
	if stmt == nil {
		return lambdaBindingCompletionFlow{fallsThrough: true}
	}
	var flow lambdaBindingCompletionFlow
	merge := func(branch lambdaBindingCompletionFlow) {
		flow.fallsThrough = flow.fallsThrough || branch.fallsThrough
		flow.completes = flow.completes || branch.completes
		flow.fails = flow.fails || branch.fails
		flow.breaks = flow.breaks || branch.breaks
		flow.continues = flow.continues || branch.continues
	}

	flow.fails = !expressionProvenNonRaising(stmt.Condition)
	if !c.expressionMayCompleteForBinding(stmt.Condition) {
		return flow
	}
	truthy, known := c.inferredConditionTruthiness(stmt.Condition)
	if !known || truthy {
		merge(c.immediateLambdaStatementsCompletionFlow(
			stmt.Consequent,
			loopDepth,
		))
	}
	if known && truthy {
		return flow
	}
	for _, branch := range stmt.ElseIf {
		flow.fails = flow.fails || !expressionProvenNonRaising(branch.Condition)
		if !c.expressionMayCompleteForBinding(branch.Condition) {
			return flow
		}
		truthy, known = c.inferredConditionTruthiness(branch.Condition)
		if !known || truthy {
			merge(c.immediateLambdaStatementsCompletionFlow(
				branch.Consequent,
				loopDepth,
			))
		}
		if known && truthy {
			return flow
		}
	}
	merge(c.immediateLambdaStatementsCompletionFlow(stmt.Alternate, loopDepth))
	return flow
}

func (c *scriptChecker) immediateLambdaForCompletionFlow(
	stmt *ForStmt,
	loopDepth int,
) lambdaBindingCompletionFlow {
	if stmt == nil {
		return lambdaBindingCompletionFlow{fallsThrough: true}
	}
	flow := lambdaBindingCompletionFlow{
		fails: !expressionProvenNonRaising(stmt.Iterable),
	}
	if !c.expressionMayCompleteForBinding(stmt.Iterable) {
		return flow
	}
	flow.fallsThrough = true
	if !staticIterableProvenEmpty(stmt.Iterable) {
		body := c.immediateLambdaStatementsCompletionFlow(stmt.Body, loopDepth+1)
		flow.completes = body.completes
		flow.fails = flow.fails || body.fails
		if value, exact := staticLiteralValue(stmt.Iterable); exact &&
			value.Kind() == KindArray && len(value.Array()) > 0 {
			flow.fallsThrough = body.fallsThrough || body.breaks || body.continues
		}
	}
	return flow
}

func (c *scriptChecker) immediateLambdaWhileCompletionFlow(
	stmt *WhileStmt,
	loopDepth int,
) lambdaBindingCompletionFlow {
	if stmt == nil {
		return lambdaBindingCompletionFlow{fallsThrough: true}
	}
	flow := lambdaBindingCompletionFlow{
		fails: !expressionProvenNonRaising(stmt.Condition),
	}
	if !c.expressionMayCompleteForBinding(stmt.Condition) {
		return flow
	}
	truthy, known := c.inferredConditionTruthiness(stmt.Condition)
	if !known || truthy {
		body := c.immediateLambdaStatementsCompletionFlow(stmt.Body, loopDepth+1)
		flow.completes = body.completes
		flow.fails = flow.fails || body.fails
		staticTruthy, staticKnown := staticExpressionTruthiness(stmt.Condition)
		flow.fallsThrough = !staticKnown || !staticTruthy || body.breaks
	} else {
		flow.fallsThrough = true
	}
	return flow
}

func (c *scriptChecker) immediateLambdaUntilCompletionFlow(
	stmt *UntilStmt,
	loopDepth int,
) lambdaBindingCompletionFlow {
	if stmt == nil {
		return lambdaBindingCompletionFlow{fallsThrough: true}
	}
	flow := lambdaBindingCompletionFlow{
		fails: !expressionProvenNonRaising(stmt.Condition),
	}
	if !c.expressionMayCompleteForBinding(stmt.Condition) {
		return flow
	}
	truthy, known := c.inferredConditionTruthiness(stmt.Condition)
	if !known || !truthy {
		body := c.immediateLambdaStatementsCompletionFlow(stmt.Body, loopDepth+1)
		flow.completes = body.completes
		flow.fails = flow.fails || body.fails
		staticTruthy, staticKnown := staticExpressionTruthiness(stmt.Condition)
		flow.fallsThrough = !staticKnown || staticTruthy || body.breaks
	} else {
		flow.fallsThrough = true
	}
	return flow
}

func (c *scriptChecker) immediateLambdaTryCompletionFlow(
	stmt *TryStmt,
	loopDepth int,
) lambdaBindingCompletionFlow {
	if stmt == nil {
		return lambdaBindingCompletionFlow{fallsThrough: true}
	}
	bodyFlow := c.immediateLambdaStatementsCompletionFlow(stmt.Body, loopDepth)
	protectedFlow := lambdaBindingCompletionFlow{
		completes: bodyFlow.completes,
		breaks:    bodyFlow.breaks,
		continues: bodyFlow.continues,
	}
	if bodyFlow.fallsThrough {
		elseFlow := c.immediateLambdaStatementsCompletionFlow(stmt.Else, loopDepth)
		protectedFlow.fallsThrough = elseFlow.fallsThrough
		protectedFlow.completes = protectedFlow.completes || elseFlow.completes
		protectedFlow.fails = protectedFlow.fails || elseFlow.fails
		protectedFlow.breaks = protectedFlow.breaks || elseFlow.breaks
		protectedFlow.continues = protectedFlow.continues || elseFlow.continues
	}
	mergeRescue := func(body []Statement) {
		if len(body) == 0 {
			return
		}
		flow := c.immediateLambdaStatementsCompletionFlow(body, loopDepth)
		protectedFlow.fallsThrough = protectedFlow.fallsThrough || flow.fallsThrough
		protectedFlow.completes = protectedFlow.completes || flow.completes
		protectedFlow.fails = protectedFlow.fails || flow.fails
		protectedFlow.breaks = protectedFlow.breaks || flow.breaks
		protectedFlow.continues = protectedFlow.continues || flow.continues
	}
	if bodyFlow.fails {
		selected, exact := c.staticallySelectedRescue(stmt.Body, stmt.Rescues)
		if exact {
			if selected >= 0 && len(stmt.Rescues[selected].Body) > 0 {
				mergeRescue(stmt.Rescues[selected].Body)
			} else {
				protectedFlow.fails = true
			}
		} else {
			protectedFlow.fails = true
			for i := range stmt.Rescues {
				mergeRescue(stmt.Rescues[i].Body)
			}
		}
	}
	ensureFlow := c.immediateLambdaStatementsCompletionFlow(stmt.Ensure, loopDepth)
	return lambdaBindingCompletionFlow{
		fallsThrough: protectedFlow.fallsThrough && ensureFlow.fallsThrough,
		completes: ensureFlow.completes ||
			protectedFlow.completes && ensureFlow.fallsThrough,
		fails: ensureFlow.fails ||
			protectedFlow.fails && ensureFlow.fallsThrough,
		breaks: ensureFlow.breaks ||
			protectedFlow.breaks && ensureFlow.fallsThrough,
		continues: ensureFlow.continues ||
			protectedFlow.continues && ensureFlow.fallsThrough,
	}
}

type blockLiteralCompletionFlow struct {
	fallsThrough      bool
	completes         bool
	returnsNonLocally bool
	fails             bool
}

func (c *scriptChecker) blockLiteralBodyMayComplete(
	block *BlockLiteral,
	strict bool,
) bool {
	flow := c.blockLiteralBodyCompletionFlow(block, strict)
	return flow.fallsThrough || flow.completes
}

func (c *scriptChecker) blockLiteralBodyCompletionFlow(
	block *BlockLiteral,
	strict bool,
) blockLiteralCompletionFlow {
	if block == nil {
		return blockLiteralCompletionFlow{}
	}
	return c.blockLiteralStatementsCompletionFlow(
		block.Body,
		strict || block.Lambda,
	)
}

func (c *scriptChecker) blockLiteralStatementsCompletionFlow(
	statements []Statement,
	localControl bool,
) blockLiteralCompletionFlow {
	flow := blockLiteralCompletionFlow{fallsThrough: true}
	for _, stmt := range statements {
		if !flow.fallsThrough {
			break
		}
		current := c.blockLiteralStatementCompletionFlow(stmt, localControl)
		flow.completes = flow.completes || current.completes
		flow.returnsNonLocally = flow.returnsNonLocally || current.returnsNonLocally
		flow.fails = flow.fails || current.fails
		flow.fallsThrough = current.fallsThrough
	}
	return flow
}

func (c *scriptChecker) blockLiteralStatementCompletionFlow(
	stmt Statement,
	localControl bool,
) blockLiteralCompletionFlow {
	switch typed := stmt.(type) {
	case nil:
		return blockLiteralCompletionFlow{fallsThrough: true}
	case *NextStmt:
		return c.blockLiteralControlExpressionCompletionFlow(typed.Value, true, false)
	case *ReturnStmt:
		return c.blockLiteralControlExpressionCompletionFlow(
			typed.Value,
			localControl,
			!localControl,
		)
	case *BreakStmt:
		flow := c.blockLiteralControlExpressionCompletionFlow(typed.Value, localControl, false)
		if !localControl {
			flow.fails = true
		}
		return flow
	case *RaiseStmt, *RetryStmt:
		return blockLiteralCompletionFlow{fails: true}
	case *IfStmt:
		return c.blockLiteralIfCompletionFlow(typed, localControl)
	case *TryStmt:
		return c.blockLiteralTryCompletionFlow(typed, localControl)
	case *ExprStmt:
		return c.blockLiteralExpressionCompletionFlow(typed.Expr)
	default:
		return blockLiteralCompletionFlow{
			fallsThrough: true,
			fails:        true,
		}
	}
}

func (c *scriptChecker) blockLiteralControlExpressionCompletionFlow(
	expr Expression,
	completes bool,
	returnsNonLocally bool,
) blockLiteralCompletionFlow {
	flow := blockLiteralCompletionFlow{
		fails: !expressionProvenNonRaising(expr),
	}
	if !c.expressionMayCompleteForBinding(expr) {
		return flow
	}
	flow.completes = completes
	flow.returnsNonLocally = returnsNonLocally
	return flow
}

func (c *scriptChecker) blockLiteralExpressionCompletionFlow(expr Expression) blockLiteralCompletionFlow {
	return blockLiteralCompletionFlow{
		fallsThrough: true,
		fails:        !expressionProvenNonRaising(expr),
	}
}

func (c *scriptChecker) blockLiteralIfCompletionFlow(
	stmt *IfStmt,
	localControl bool,
) blockLiteralCompletionFlow {
	if stmt == nil {
		return blockLiteralCompletionFlow{}
	}
	var flow blockLiteralCompletionFlow
	merge := func(branch blockLiteralCompletionFlow) {
		flow.fallsThrough = flow.fallsThrough || branch.fallsThrough
		flow.completes = flow.completes || branch.completes
		flow.returnsNonLocally = flow.returnsNonLocally || branch.returnsNonLocally
		flow.fails = flow.fails || branch.fails
	}

	if !expressionProvenNonRaising(stmt.Condition) {
		flow.fails = true
	}
	truthy, known := staticExpressionTruthiness(stmt.Condition)
	if !known || truthy {
		merge(c.blockLiteralStatementsCompletionFlow(
			stmt.Consequent,
			localControl,
		))
	}
	if known && truthy {
		return flow
	}
	for _, branch := range stmt.ElseIf {
		truthy, known = staticExpressionTruthiness(branch.Condition)
		if !known || truthy {
			merge(c.blockLiteralStatementsCompletionFlow(
				branch.Consequent,
				localControl,
			))
		}
		if known && truthy {
			return flow
		}
	}
	merge(c.blockLiteralStatementsCompletionFlow(stmt.Alternate, localControl))
	return flow
}

func (c *scriptChecker) blockLiteralTryCompletionFlow(
	stmt *TryStmt,
	localControl bool,
) blockLiteralCompletionFlow {
	if stmt == nil {
		return blockLiteralCompletionFlow{fallsThrough: true}
	}
	bodyFlow := c.blockLiteralStatementsCompletionFlow(stmt.Body, localControl)
	var protectedFlow blockLiteralCompletionFlow
	protectedFlow.completes = bodyFlow.completes
	protectedFlow.returnsNonLocally = bodyFlow.returnsNonLocally
	if bodyFlow.fallsThrough {
		elseFlow := c.blockLiteralStatementsCompletionFlow(stmt.Else, localControl)
		protectedFlow.fallsThrough = elseFlow.fallsThrough
		protectedFlow.completes = protectedFlow.completes || elseFlow.completes
		protectedFlow.returnsNonLocally = protectedFlow.returnsNonLocally || elseFlow.returnsNonLocally
		protectedFlow.fails = protectedFlow.fails || elseFlow.fails
	}
	mergeRescue := func(body []Statement) {
		if len(body) == 0 {
			return
		}
		flow := c.blockLiteralStatementsCompletionFlow(body, localControl)
		protectedFlow.fallsThrough = protectedFlow.fallsThrough || flow.fallsThrough
		protectedFlow.completes = protectedFlow.completes || flow.completes
		protectedFlow.returnsNonLocally = protectedFlow.returnsNonLocally || flow.returnsNonLocally
		protectedFlow.fails = protectedFlow.fails || flow.fails
	}
	if bodyFlow.fails {
		selected, exact := c.staticallySelectedRescue(stmt.Body, stmt.Rescues)
		if exact {
			if selected >= 0 && len(stmt.Rescues[selected].Body) > 0 {
				mergeRescue(stmt.Rescues[selected].Body)
			} else {
				protectedFlow.fails = true
			}
		} else {
			protectedFlow.fails = true
			for i := range stmt.Rescues {
				mergeRescue(stmt.Rescues[i].Body)
			}
		}
	}
	ensureFlow := c.blockLiteralStatementsCompletionFlow(stmt.Ensure, localControl)
	return blockLiteralCompletionFlow{
		fallsThrough: protectedFlow.fallsThrough && ensureFlow.fallsThrough,
		completes: ensureFlow.completes ||
			protectedFlow.completes && ensureFlow.fallsThrough,
		returnsNonLocally: ensureFlow.returnsNonLocally ||
			protectedFlow.returnsNonLocally && ensureFlow.fallsThrough,
		fails: ensureFlow.fails ||
			protectedFlow.fails && ensureFlow.fallsThrough,
	}
}

func (c *scriptChecker) plainAssignmentTargetMayCompleteForBinding(
	target, value Expression,
) bool {
	switch typed := target.(type) {
	case nil, *Identifier, *ClassVarExpr:
		return true
	case *IvarExpr:
		return c.ivarAssignmentMayComplete(typed, value)
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

func (c *scriptChecker) ivarAssignmentMayComplete(target *IvarExpr, value Expression) bool {
	if target == nil || c.selfClass == nil || c.selfClassContext {
		return true
	}
	_, ty := propertyContract(c.selfClass, target.Name)
	return c.callArgumentMayBindType(value, ty)
}

func (c *scriptChecker) applyAssignmentNamespaceMutations(
	target, value Expression,
	captured ...checkDynamicCallCandidates,
) {
	switch target.(type) {
	case *MemberExpr, *IndexExpr:
	default:
		return
	}
	scan := c.newNamespaceMutationScan()
	scan.recordRuntimeNamespaceAssignment(target, value, captured...)
	for member := range scan.out {
		c.recordRuntimeNamespaceMember(member)
	}
}

func (c *scriptChecker) statementMayEvaluateCallBlock(stmt Statement, seen map[*ScriptFunction]struct{}) bool {
	switch typed := stmt.(type) {
	case nil, *EnumStmt:
		return false
	case *ReturnStmt:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *RaiseStmt:
		if !staticRaiseErrorClass(typed) {
			if c.expressionMayEvaluateCallBlock(typed.Value, seen) {
				return true
			}
			if !c.expressionMayCompleteForBinding(typed.Value) {
				return false
			}
		}
		return c.expressionMayEvaluateCallBlock(typed.Message, seen)
	case *BreakStmt:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *NextStmt:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *AssignStmt:
		// Assignment targets are locals before their values evaluate. Keep a
		// same-named global function from winning callable resolution during
		// this statement or any statement that follows it.
		c.recordBindingTarget(typed.Target)
		expectation := c.assignmentValueExpectation(typed.Target, typed.Value)
		if typed.Operator == "" {
			if c.expressionMayEvaluateCallBlock(typed.Value, seen) {
				return true
			}
			if !c.expressionMayCompleteForBindingWithExpectation(typed.Value, expectation) {
				return false
			}
			return c.expressionMayEvaluateCallBlock(typed.Target, seen)
		}
		if c.expressionMayEvaluateCallBlock(typed.Target, seen) {
			return true
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
				return false
			}
		}
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *ExprStmt:
		return c.expressionMayEvaluateCallBlock(typed.Expr, seen)
	case *IfStmt:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if known && truthy {
			return c.statementsMayEvaluateCallBlock(typed.Consequent, seen)
		}
		if !known {
			falsePathState := c.snapshotScopeState()
			if c.statementsMayEvaluateCallBlock(typed.Consequent, seen) {
				return true
			}
			c.restoreScopeState(falsePathState)
		}
		for _, branch := range typed.ElseIf {
			if c.expressionMayEvaluateCallBlock(branch.Condition, seen) {
				return true
			}
			if !c.expressionMayCompleteForBinding(branch.Condition) {
				return false
			}
			truthy, known = c.inferredConditionTruthiness(branch.Condition)
			if known && truthy {
				return c.statementsMayEvaluateCallBlock(branch.Consequent, seen)
			}
			if known {
				continue
			}
			falsePathState := c.snapshotScopeState()
			if c.statementsMayEvaluateCallBlock(branch.Consequent, seen) {
				return true
			}
			c.restoreScopeState(falsePathState)
		}
		return c.statementsMayEvaluateCallBlock(typed.Alternate, seen)
	case *ForStmt:
		if c.expressionMayEvaluateCallBlock(typed.Iterable, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Iterable) ||
			staticIterableProvenEmpty(typed.Iterable) {
			return false
		}
		if staticIterableProvenSingle(typed.Iterable) ||
			!loopBodyMayReachBackedge(typed.Body) {
			c.degradeLocalTypesForBindings(nil, typed.Target)
			c.recordBindingTarget(typed.Target)
		} else {
			c.degradeLocalTypesForBindings(typed.Body, typed.Target)
			c.recordLocalBindings([]Statement{typed})
		}
		return c.expressionMayEvaluateCallBlock(typed.Target, seen) ||
			c.statementsMayEvaluateCallBlock(typed.Body, seen)
	case *WhileStmt:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if known && !truthy {
			return false
		}
		if loopBodyMayReachBackedge(typed.Body) {
			c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
			c.recordLocalBindings([]Statement{typed})
			if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
				return true
			}
		}
		return c.statementsMayEvaluateCallBlock(typed.Body, seen)
	case *UntilStmt:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if known && truthy {
			return false
		}
		if loopBodyMayReachBackedge(typed.Body) {
			c.degradeLocalTypesForRepeatedLoop(typed.Condition, typed.Body)
			c.recordLocalBindings([]Statement{typed})
			if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
				return true
			}
		}
		return c.statementsMayEvaluateCallBlock(typed.Body, seen)
	case *TryStmt:
		entryState := c.snapshotScopeState()
		if c.statementsMayEvaluateCallBlock(typed.Body, seen) {
			return true
		}
		bodyState := c.snapshotScopeState()
		if c.statementsMayCompleteForBinding(typed.Body) &&
			c.statementsMayEvaluateCallBlock(typed.Else, seen) {
			return true
		}
		c.restoreScopeState(entryState)
		c.degradeLocalTypesForBindings([]Statement{typed})
		failureEntryState := c.snapshotScopeState()
		if !statementsProvenNonRaising(typed.Body) {
			selected, exact := c.staticallySelectedRescue(typed.Body, typed.Rescues)
			for i := range typed.Rescues {
				if exact && i != selected {
					continue
				}
				c.restoreScopeStateWithObservedBindings(failureEntryState, bodyState)
				clause := &typed.Rescues[i]
				popScope := c.pushRescueScope(clause)
				if clause.Binding != "" {
					c.bindLocalTypeInCurrentFrame(clause.Binding, nil)
				}
				mayEvaluate := c.statementsMayEvaluateCallBlock(clause.Body, seen)
				popScope()
				if mayEvaluate {
					return true
				}
			}
		}
		c.restoreScopeState(failureEntryState)
		c.recordLocalBindings([]Statement{typed})
		return c.statementsMayEvaluateCallBlock(typed.Ensure, seen)
	case *ClassStmt:
		return c.isolatedStatementsMayEvaluateCallBlock(typed.Body, seen)
	default:
		return false
	}
}

func (c *scriptChecker) isolatedStatementsMayEvaluateCallBlock(
	statements []Statement,
	seen map[*ScriptFunction]struct{},
) bool {
	previousScopes := c.scopes
	restoreInference := c.withIsolatedLocalInference()
	c.scopes = nil
	popScope := c.pushScope(make(map[string]struct{}))
	defer func() {
		popScope()
		restoreInference()
		c.scopes = previousScopes
	}()
	return c.statementsMayEvaluateCallBlock(statements, seen)
}

func (c *scriptChecker) restoreScopeStateWithObservedBindings(
	base checkScopeState,
	observed checkScopeState,
) {
	c.restoreScopeState(base)
	for i, bindings := range observed.defined {
		if i >= len(c.scopes) {
			break
		}
		if c.scopes[i] == nil && len(bindings) > 0 {
			c.scopes[i] = make(map[string]struct{}, len(bindings))
		}
		for name := range bindings {
			c.scopes[i][name] = struct{}{}
		}
	}
}

func (c *scriptChecker) expressionsMayEvaluateCallBlockInOrder(
	expressions []Expression,
	seen map[*ScriptFunction]struct{},
) (mayEvaluateBlock, completes bool) {
	for _, expr := range expressions {
		if c.expressionMayEvaluateCallBlock(expr, seen) {
			return true, false
		}
		if !c.expressionMayCompleteForBinding(expr) {
			return false, false
		}
	}
	return false, true
}

func (c *scriptChecker) expressionMayEvaluateCallBlock(expr Expression, seen map[*ScriptFunction]struct{}) bool {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return false
	case *ArrayLiteral:
		mayEvaluate, _ := c.expressionsMayEvaluateCallBlockInOrder(typed.Elements, seen)
		return mayEvaluate
	case *HashLiteral:
		if typed.ShapeType != nil && !c.hashShapeStaticallyShadowed(typed) {
			return false
		}
		for _, pair := range typed.Pairs {
			mayEvaluate, completes := c.expressionsMayEvaluateCallBlockInOrder(
				[]Expression{pair.Key, pair.Value},
				seen,
			)
			if mayEvaluate {
				return true
			}
			if !completes {
				return false
			}
		}
		return false
	case *CallExpr:
		if c.expressionMayEvaluateCallBlock(typed.Callee, seen) {
			return true
		}
		if !c.callCalleeExpressionMayCompleteForBinding(typed) {
			return false
		}
		if staticNilSafeNavigationCall(typed) || c.safeNavigationCallSkipsInferred(typed) {
			return false
		}
		target, resolved := c.resolveCallable(typed)
		dynamicCandidates := c.captureDynamicCallCandidates(typed)
		deferForwardedTargets := callResolvesForwardedTargetAfterArguments(
			typed,
			target,
			resolved,
		)
		var dynamicResolution checkDynamicCallResolution
		if !deferForwardedTargets {
			dynamicResolution = c.exactDynamicCallTargets(
				typed,
				target,
				resolved,
				dynamicCandidates,
			)
		}
		if c.callCalleeLookupFails(
			typed,
			target,
			resolved,
			deferForwardedTargets,
			dynamicCandidates,
			dynamicResolution,
		) {
			return false
		}
		positionalSplatSeen := false
		for i, arg := range typed.Args {
			if c.expressionMayEvaluateCallBlock(arg, seen) {
				return true
			}
			expectation := expressionExpectation{}
			_, splat := arg.(*SplatArg)
			if resolved && !positionalSplatSeen && !splat {
				expectation = staticCallablePositionalArgumentExpectation(target, i)
			}
			if !c.expressionMayCompleteForBindingWithExpectation(arg, expectation) ||
				!c.positionalArgumentExpansionMaySucceed(arg) {
				return false
			}
			positionalSplatSeen = positionalSplatSeen || splat
		}
		for _, kwarg := range typed.KwArgs {
			if c.expressionMayEvaluateCallBlock(kwarg.Value, seen) {
				return true
			}
			expectation := expressionExpectation{}
			if resolved && !kwarg.Splat {
				expectation = staticCallableKeywordArgumentExpectation(typed, target, kwarg.Name)
			}
			if !c.expressionMayCompleteForBindingWithExpectation(kwarg.Value, expectation) ||
				!c.keywordArgumentExpansionMaySucceed(kwarg) {
				return false
			}
		}
		if typed.BlockArg != nil {
			if c.expressionMayEvaluateCallBlock(typed.BlockArg, seen) {
				return true
			}
			if !c.expressionMayCompleteForBindingWithExpectation(
				typed.BlockArg,
				typeExpressionExpectation(checkTypeFunction),
			) || !c.blockArgumentConversionMaySucceed(
				typed.BlockArg,
				c.inferExpressionTypeWithExpectation(
					typed.BlockArg,
					typeExpressionExpectation(checkTypeFunction),
				),
			) {
				return false
			}
		}
		if deferForwardedTargets {
			dynamicResolution = c.exactDynamicCallTargets(
				typed,
				target,
				resolved,
				dynamicCandidates,
			)
		}
		if typed.BlockArg != nil {
			if c.resolvedCallMayInvokeSuppliedBlockWithSeen(
				typed,
				target,
				resolved,
				dynamicResolution,
				seen,
			) {
				return true
			}
		}
		return c.resolvedCallMayInvokeSuppliedBlockWithSeen(
			typed,
			target,
			resolved,
			dynamicResolution,
			seen,
		) &&
			c.blockLiteralMayEvaluateCallBlock(typed.Block, seen)
	case *MemberExpr:
		return c.expressionMayEvaluateCallBlock(typed.Object, seen)
	case *ScopeExpr:
		return c.expressionMayEvaluateCallBlock(typed.Object, seen)
	case *IndexExpr:
		expressions := make([]Expression, 0, len(typed.Indices)+1)
		expressions = append(expressions, typed.Object)
		expressions = append(expressions, typed.Indices...)
		mayEvaluate, _ := c.expressionsMayEvaluateCallBlockInOrder(expressions, seen)
		return mayEvaluate
	case *DestructureTarget:
		targets := make([]Expression, 0, len(typed.Elements))
		for _, element := range typed.Elements {
			targets = append(targets, element.Target)
		}
		mayEvaluate, _ := c.expressionsMayEvaluateCallBlockInOrder(targets, seen)
		return mayEvaluate
	case *SplatArg:
		return c.expressionMayEvaluateCallBlock(typed.Value, seen)
	case *UnaryExpr:
		return c.expressionMayEvaluateCallBlock(typed.Right, seen)
	case *BinaryExpr:
		if c.expressionMayEvaluateCallBlock(typed.Left, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Left) ||
			!binaryRightMayEvaluate(typed) || c.binaryRightUnreachable(typed) {
			return false
		}
		return c.expressionMayEvaluateCallBlock(typed.Right, seen)
	case *ConditionalExpr:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if known {
			branch := typed.Alternate
			if truthy {
				branch = typed.Consequent
			}
			return c.expressionMayEvaluateCallBlock(branch, seen)
		}
		return c.expressionMayEvaluateCallBlock(typed.Consequent, seen) ||
			c.expressionMayEvaluateCallBlock(typed.Alternate, seen)
	case *RescueExpr:
		if c.expressionMayEvaluateCallBlock(typed.Body, seen) {
			return true
		}
		if expressionProvenNonRaising(typed.Body) {
			return false
		}
		if errorKind, exact := c.staticallyRaisedExpressionErrorKind(typed.Body); exact &&
			!staticErrorKindMatchesRescue(errorKind, nil) {
			return false
		}
		return c.expressionMayEvaluateCallBlock(typed.Fallback, seen)
	case *IfExpr:
		if c.expressionMayEvaluateCallBlock(typed.Condition, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if (!known || truthy) && c.expressionMayEvaluateCallBlock(typed.Consequent, seen) {
			return true
		}
		if known && truthy {
			return false
		}
		for _, branch := range typed.ElseIf {
			if c.expressionMayEvaluateCallBlock(branch.Condition, seen) {
				return true
			}
			if !c.expressionMayCompleteForBinding(branch.Condition) {
				return false
			}
			branchTruthy, branchKnown := c.inferredConditionTruthiness(branch.Condition)
			if (!branchKnown || branchTruthy) &&
				c.expressionMayEvaluateCallBlock(branch.Result, seen) {
				return true
			}
			if branchKnown && branchTruthy {
				return false
			}
		}
		return c.expressionMayEvaluateCallBlock(typed.Alternate, seen)
	case *RangeExpr:
		if c.expressionMayEvaluateCallBlock(typed.Start, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Start) ||
			!c.rangeEndpointConversionMaySucceed(typed.Start) {
			return false
		}
		return c.expressionMayEvaluateCallBlock(typed.End, seen)
	case *CaseExpr:
		if c.expressionMayEvaluateCallBlock(typed.Target, seen) {
			return true
		}
		if !c.expressionMayCompleteForBinding(typed.Target) {
			return false
		}
		if result, known := c.inferredCaseExpressionResult(typed); known {
			return c.expressionMayEvaluateCallBlock(result, seen)
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if c.expressionMayEvaluateCallBlock(value.Expr, seen) {
					return true
				}
				if !c.expressionMayCompleteForBinding(value.Expr) ||
					!c.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
					return false
				}
			}
			if c.expressionMayEvaluateCallBlock(clause.Result, seen) {
				return true
			}
		}
		return c.expressionMayEvaluateCallBlock(typed.ElseExpr, seen)
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
	// Literal call blocks close over the surrounding locals but may run zero
	// or many times. Degrade captured bindings up front, then keep the walk's
	// final iteration state from escaping as a definite fact. Block-bound
	// parameters live in a nested scope and never overwrite same-named outer
	// locals.
	c.degradeBlockBodyBindings(block)
	entryState := c.snapshotScopeState()
	popScope := c.pushBlockCheckScope(block)
	popNameScope := c.pushBlockNameScope(block)
	defer func() {
		popNameScope()
		popScope()
		c.restoreScopeState(entryState)
	}()
	for _, name := range block.ImplicitParams {
		c.bindLocalTypeInCurrentFrame(name, nil)
	}
	for _, param := range block.Params {
		if c.expressionMayEvaluateCallBlock(param.DefaultVal, seen) {
			return true
		}
		c.recordParamBinding(param)
	}
	return c.statementsMayEvaluateCallBlock(block.Body, seen)
}

func (c *scriptChecker) stringPartsMayEvaluateCallBlock(parts []StringPart, seen map[*ScriptFunction]struct{}) bool {
	expressions := make([]Expression, 0, len(parts))
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			expressions = append(expressions, exprPart.Expr)
		}
	}
	mayEvaluate, _ := c.expressionsMayEvaluateCallBlockInOrder(expressions, seen)
	return mayEvaluate
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
	c.checkRuntimeExpressionAgainstTypeWithExpectation(
		function,
		expr,
		ty,
		subject,
		expressionExpectation{},
	)
}

func (c *scriptChecker) checkRuntimeExpressionAgainstTypeWithExpectation(
	function string,
	expr Expression,
	ty *TypeExpr,
	subject string,
	expectation expressionExpectation,
) {
	if value, literal := staticLiteralValue(expr); literal {
		c.checkRuntimeValueAgainstType(function, expr.Pos(), value, ty, subject)
		return
	}
	warningsBeforeInference := len(c.warnings)
	c.checkInferredExpressionAgainstTypeWithExpectation(function, expr, ty, subject, expectation)
	if len(c.warnings) > warningsBeforeInference {
		return
	}
	values, exact := c.evaluatedStaticLiteralValueAlternatives(expr)
	if !exact {
		return
	}
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	if err := c.staticValuesBoundaryMismatch(values, ty); err != nil {
		c.addValueTypeWarning(function, expr.Pos(), subject, err)
	}
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

// staticValuesTypeMismatch returns nil when at least one exact alternative
// passes the runtime normalizer. A checker diagnostic represents a guaranteed
// rejection, so mixed compatible and incompatible alternatives stay gradual.
func (c *scriptChecker) staticValuesTypeMismatch(values []Value, ty *TypeExpr) error {
	var firstErr error
	for _, value := range values {
		err := c.checkRuntimeStaticValueType(value, ty)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// staticValuesBoundaryMismatch returns the first exact alternative that a
// typed boundary would reject. Every known alternative must normalize even
// when their shared inferred kind remains gradual, as symbols do for enums.
func (c *scriptChecker) staticValuesBoundaryMismatch(values []Value, ty *TypeExpr) error {
	for _, value := range values {
		if err := c.checkRuntimeStaticValueType(value, ty); err != nil {
			return err
		}
	}
	return nil
}

func (c *scriptChecker) staticValuesMustNormalizeType(values []Value, ty *TypeExpr) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if c.checkRuntimeStaticValueType(value, ty) != nil {
			return false
		}
	}
	return true
}

func (c *scriptChecker) checkImplicitReturn(function string, ty *TypeExpr, statements []Statement, pos Position) {
	if !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	c.checkImplicitFinalBlock(function, ty, statements, pos)
}

func (c *scriptChecker) checkImplicitFinalStatement(function string, ty *TypeExpr, stmt Statement) {
	switch typed := stmt.(type) {
	case *ReturnStmt, *RaiseStmt:
		return
	case *ExprStmt:
		warningsBefore := len(c.warnings)
		c.checkImplicitLeafAgainstType(function, typed, typed.Expr, ty)
		if len(c.warnings) == warningsBefore &&
			expressionCanImplicitlyYieldNil(typed.Expr) &&
			!typeAllowsNilReturn(ty) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
		}
	case *AssignStmt:
		result := typed.Value
		if typed.Operator != "" {
			result = typed.Target
		}
		warningsBefore := len(c.warnings)
		c.checkImplicitLeafAgainstType(function, typed, result, ty)
		if len(c.warnings) == warningsBefore &&
			expressionCanImplicitlyYieldNil(result) &&
			!typeAllowsNilReturn(ty) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
		}
	case *IfStmt:
		c.checkImplicitFinalIfStatement(function, ty, typed)
	case *ForStmt, *WhileStmt, *UntilStmt:
		if !typeAllowsNilReturn(ty) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
		}
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
		if !typeAllowsNilReturn(ty) {
			c.add(function, Position{}, "typed return %s can implicitly return nil", formatTypeExpr(ty))
		}
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
		if !typeAllowsNilReturn(ty) {
			c.add(function, stmt.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
		}
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
		if !typeAllowsNilReturn(ty) {
			c.add(function, pos, "typed return %s can implicitly return nil", formatTypeExpr(ty))
		}
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
	if target.constructor && target.fn == nil {
		c.recordUninitializedConstructorIvarFacts(call, target.constructorClass)
	}
	if target.fn == nil && (target.name == "send" || target.name == "public_send") {
		c.checkDynamicCallTargets(function, dynamicTargets, diagnoseDynamicTargets)
	}
	if callExpandsArguments(call) {
		// Splat expansion makes the argument shape dynamic: the runtime
		// validates the expanded call with the same binding errors a literal
		// spelling would raise, so static shape checks step aside.
		if target.fn != nil {
			facts := c.reachableCallInstanceFacts(call, target, nil)
			c.enqueueReachableFunctionForCall(target.name, target.fn, facts, call)
		}
		return
	}
	if target.fn != nil {
		view := staticCallViewFor(call, target)
		c.checkCallShape(function, view, target.name, target.fn)
		c.checkCallArgumentTypes(function, view, target.name, target.fn)
		plan := c.scriptCallBindingPlan(call, target)
		facts := c.reachableCallParamFacts(call, target)
		facts = c.reachableCallInstanceFacts(call, target, facts)
		c.checkScriptCallContextualDefaults(
			target.name,
			target.fn,
			plan,
			facts,
		)
		if plan.bodyMayEnter {
			c.enqueueReachableFunctionForCall(
				target.name,
				target.fn,
				facts,
				call,
			)
		} else if plan.bindingStarts {
			c.enqueueReachableFunctionBindingForCall(
				target.name,
				target.fn,
				facts,
				plan,
				call,
			)
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
		facts = c.reachableCallInstanceFacts(candidate.call, candidate.target, facts)
		if candidate.mayEnter {
			c.enqueueReachableFunctionForCall(
				candidate.target.name,
				candidate.target.fn,
				facts,
				candidate.call,
			)
		} else if candidate.bindingStarts {
			c.enqueueReachableFunctionBindingForCall(
				candidate.target.name,
				candidate.target.fn,
				facts,
				c.scriptCallBindingPlan(candidate.call, candidate.target),
				candidate.call,
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

func assignmentSetterCall(target, value Expression) *CallExpr {
	switch typed := target.(type) {
	case *MemberExpr:
		setter := *typed
		setter.Property += "="
		return &CallExpr{
			Callee:   &setter,
			Args:     []Expression{value},
			Position: typed.Pos(),
		}
	case *IndexExpr:
		args := append([]Expression(nil), typed.Indices...)
		args = append(args, value)
		return &CallExpr{
			Callee: &MemberExpr{
				Object:   typed.Object,
				Property: "[]=",
				Position: typed.Pos(),
			},
			Args:     args,
			Position: typed.Pos(),
		}
	default:
		return nil
	}
}

func (c *scriptChecker) withAssignmentReceiverCapture(
	target Expression,
	walk func() bool,
) (bool, checkAssignmentReceiverCapture) {
	previous := c.assignmentReceiverCapture
	capture := &checkAssignmentReceiverCapture{target: target}
	c.assignmentReceiverCapture = capture
	defer func() { c.assignmentReceiverCapture = previous }()
	completed := walk()
	return completed, *capture
}

func (c *scriptChecker) captureAssignmentReceiver(target Expression) {
	capture := c.assignmentReceiverCapture
	if capture == nil || capture.captured || capture.target != target {
		return
	}
	snapshot, ok := c.assignmentReceiverSnapshot(target)
	if !ok {
		return
	}
	snapshot.target = capture.target
	*capture = snapshot
}

func (c *scriptChecker) assignmentReceiverSnapshot(
	target Expression,
) (checkAssignmentReceiverCapture, bool) {
	call := assignmentSetterCall(target, nil)
	if call == nil {
		return checkAssignmentReceiverCapture{}, false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok {
		return checkAssignmentReceiverCapture{}, false
	}
	staticValues, staticValuesExact := c.staticValueExpressionAlternatives(member.Object)
	rootName, _ := rootIdentifierName(member.Object)
	return checkAssignmentReceiverCapture{
		target:            target,
		candidates:        c.captureDynamicCallCandidates(call),
		receiverType:      c.inferExpressionType(member.Object),
		staticValues:      staticValues,
		staticValuesExact: staticValuesExact,
		rootName:          rootName,
		rootGeneration:    c.localBindingGenerations[rootName],
		captured:          true,
	}, true
}

func (c *scriptChecker) assignmentReceiverRootCurrent(
	receiver checkAssignmentReceiverCapture,
) bool {
	// A logical or compound RHS can rebind the target's root after the
	// receiver was selected. Inference must not invalidate that new value.
	return !receiver.captured ||
		receiver.rootName == "" ||
		c.localBindingGenerations[receiver.rootName] == receiver.rootGeneration
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
	if c.staticNilCallCalleeLookupFails(call) {
		return true
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

func (c *scriptChecker) staticNilCallCalleeLookupFails(call *CallExpr) bool {
	if call == nil {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || member.Safe || !typeExprIsNilOnly(c.inferExpressionType(member.Object)) {
		return false
	}
	if isUniversalMember(member.Property) {
		return false
	}
	switch member.Property {
	case "inspect", "to_s", "string":
		return false
	default:
		return true
	}
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
			if callableExpr, bindable := bareIdentifierCallableValue(expr); bindable {
				identitySource = callableExpr
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
	containerIdentities := make(map[string]string)
	containerIdentitySequence := 0
	argumentContainerIdentity := func(expr Expression) string {
		ident, direct := expr.(*Identifier)
		if !direct {
			if !c.pureCallArgument(expr) {
				clear(containerIdentities)
			}
			return ""
		}
		captured := c.callArgumentFacts[expr]
		local := c.localTypeFor(ident.Name)
		if local == nil || !typeExprHasContainerArm(local) ||
			typeExprMayIncludeCallable(local) || captured == nil || !typeExprHasContainerArm(captured) {
			if !c.pureCallArgument(expr) {
				clear(containerIdentities)
			}
			return ""
		}
		identityNames := c.containerIdentityNames(ident.Name)
		for name := range identityNames {
			if identity := containerIdentities[name]; identity != "" {
				containerIdentities[ident.Name] = identity
				return identity
			}
		}
		containerIdentitySequence++
		identity := strconv.Itoa(containerIdentitySequence)
		for name := range identityNames {
			containerIdentities[name] = identity
		}
		return identity
	}
	for i, arg := range view.args {
		containerIdentity := argumentContainerIdentity(arg)
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
		selfBinding, selfCaptured := c.callArgumentSelfBindings[arg]
		_, staticValuesCaptured := c.callArgumentStaticValues[arg]
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
		instanceOrigins, instanceExact := c.evaluatedInstanceValueOrigins(arg)
		callableIdentityExact := callablesCaptured || selfCaptured || staticValuesCaptured
		if fact != nil || len(classNames) > 0 || len(callables) > 0 || callableIdentityExact ||
			staticExact || instanceExact || containerIdentity != "" {
			facts[param.Name] = checkReachableParamFact{
				typeExpr:              fact,
				classNames:            append([]string(nil), classNames...),
				callables:             append([]*ScriptFunction(nil), callables...),
				callableIdentityExact: callableIdentityExact,
				selfCallables:         append([]*ScriptFunction(nil), selfBinding.functions...),
				selfCallablesCaptured: selfCaptured,
				selfCallableAmbiguous: selfBinding.ambiguous,
				staticVals:            append([]Expression(nil), staticVals...),
				instanceOrigins:       append([]Expression(nil), instanceOrigins...),
				containerIdentity:     containerIdentity,
			}
		}
	}
	for _, kwarg := range view.kwargs {
		containerIdentity := argumentContainerIdentity(kwarg.Value)
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
			selfBinding, selfCaptured := c.callArgumentSelfBindings[kwarg.Value]
			_, staticValuesCaptured := c.callArgumentStaticValues[kwarg.Value]
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
			instanceOrigins, instanceExact := c.evaluatedInstanceValueOrigins(kwarg.Value)
			callableIdentityExact := callablesCaptured || selfCaptured || staticValuesCaptured
			if fact != nil || len(classNames) > 0 || len(callables) > 0 || callableIdentityExact ||
				staticExact || instanceExact || containerIdentity != "" {
				facts[param.Name] = checkReachableParamFact{
					typeExpr:              fact,
					classNames:            append([]string(nil), classNames...),
					callables:             append([]*ScriptFunction(nil), callables...),
					callableIdentityExact: callableIdentityExact,
					selfCallables:         append([]*ScriptFunction(nil), selfBinding.functions...),
					selfCallablesCaptured: selfCaptured,
					selfCallableAmbiguous: selfBinding.ambiguous,
					staticVals:            append([]Expression(nil), staticVals...),
					instanceOrigins:       append([]Expression(nil), instanceOrigins...),
					containerIdentity:     containerIdentity,
				}
			}
			break
		}
	}
	c.bindVariadicReachableParamFacts(view, fn, facts)
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

// evaluatedCallArgumentStaticAlternatives freezes an evaluated argument's
// exact literal, class, or callable identity for aggregate parameter bindings.
func (c *scriptChecker) evaluatedCallArgumentStaticAlternatives(
	expr Expression,
) ([]Expression, bool) {
	if values, captured := c.callArgumentStaticValues[expr]; captured &&
		len(values) > 0 {
		return append([]Expression(nil), values...), true
	}
	fact := capturedDestructureValueFact{
		value:     expr,
		assigned:  c.callArgumentFacts[expr],
		known:     true,
		evaluated: true,
	}
	if classNames, captured := c.callArgumentClassValues[expr]; captured &&
		len(classNames) > 0 {
		fact.classNames = append([]string(nil), classNames...)
		fact.factKind = destructureClassFact
	} else if callables, captured := c.callArgumentCallables[expr]; captured &&
		len(callables) > 0 {
		fact.callables = append([]*ScriptFunction(nil), callables...)
		fact.factKind = destructureCallableFact
	} else {
		return nil, false
	}
	projection := c.newDestructureProjection(fact, expr.Pos())
	values := []Expression{projection}
	if c.callArgumentStaticValues == nil {
		c.callArgumentStaticValues = make(map[Expression][]Expression)
	}
	c.callArgumentStaticValues[expr] = values
	return append([]Expression(nil), values...), true
}

// internVariadicParamStaticValues gives synthesized aggregates a durable
// identity so recursive call and return-summary cache keys converge.
func (c *scriptChecker) internVariadicParamStaticValues(
	fn *ScriptFunction,
	param Param,
	values []Expression,
) []Expression {
	if fn == nil || param.Name == "" || len(values) == 0 {
		return values
	}
	var key strings.Builder
	fmt.Fprintf(&key, "%p:%d:%s:", fn, len(param.Name), param.Name)
	for _, value := range values {
		c.writeVariadicStaticValueKey(&key, value)
		key.WriteByte(',')
	}
	cacheKey := key.String()
	if cached, ok := c.variadicParamStaticValues[cacheKey]; ok {
		return append([]Expression(nil), cached...)
	}
	if c.variadicParamStaticValues == nil {
		c.variadicParamStaticValues = make(map[string][]Expression)
	}
	c.variadicParamStaticValues[cacheKey] = append([]Expression(nil), values...)
	return values
}

func (c *scriptChecker) writeVariadicStaticValueKey(
	key *strings.Builder,
	expr Expression,
) {
	if identity, static := staticLiteralHashIdentity(expr); static {
		fmt.Fprintf(key, "literal:%d:%s", len(identity), identity)
		return
	}
	if fact, projected := c.destructureProjectionFacts[expr]; projected {
		fmt.Fprintf(key, "projection:%d:%s:", fact.factKind, typeFactKey(fact.assigned))
		for _, className := range fact.classNames {
			fmt.Fprintf(key, "%d:%s,", len(className), className)
		}
		key.WriteByte(':')
		for _, callable := range fact.callables {
			fmt.Fprintf(key, "%p,", callable)
		}
		return
	}
	switch typed := expr.(type) {
	case *ArrayLiteral:
		key.WriteString("array[")
		for _, element := range typed.Elements {
			c.writeVariadicStaticValueKey(key, element)
			key.WriteByte(';')
		}
		key.WriteByte(']')
	case *HashLiteral:
		key.WriteString("hash:")
		key.WriteString(typeFactKey(typed.ShapeType))
		key.WriteByte('{')
		for _, pair := range typed.Pairs {
			c.writeVariadicStaticValueKey(key, pair.Key)
			key.WriteByte('=')
			c.writeVariadicStaticValueKey(key, pair.Value)
			key.WriteByte(';')
		}
		key.WriteByte('}')
	default:
		fmt.Fprintf(key, "%T:%p", expr, expr)
	}
}

// bindVariadicReachableParamFacts mirrors the runtime's rest parameter binding
// so indexing the aggregate can recover exact evaluated argument identities.
func (c *scriptChecker) bindVariadicReachableParamFacts(
	view staticCallView,
	fn *ScriptFunction,
	facts map[string]checkReachableParamFact,
) {
	if fn == nil {
		return
	}
	const maxAlternatives = 32
	argIndex := 0
	usedKeywords := make(map[string]struct{}, len(view.kwargs))
	lastKeyword := make(map[string]int, len(view.kwargs))
	for i, kwarg := range view.kwargs {
		lastKeyword[kwarg.Name] = i
	}
	for _, param := range fn.Params {
		switch param.Kind {
		case ParamNormal:
			if argIndex < len(view.args) {
				argIndex++
			} else if keywordIndex(view, param.Name) >= 0 {
				usedKeywords[param.Name] = struct{}{}
			}
		case ParamKeyword:
			if keywordIndex(view, param.Name) >= 0 {
				usedKeywords[param.Name] = struct{}{}
			}
		case ParamRest:
			alternatives := []*ArrayLiteral{{Position: view.pos}}
			exact := true
			for _, arg := range view.args[argIndex:] {
				values, ok := c.evaluatedCallArgumentStaticAlternatives(arg)
				if !ok || len(alternatives) > maxAlternatives/len(values) {
					exact = false
					break
				}
				next := make([]*ArrayLiteral, 0, len(alternatives)*len(values))
				for _, prefix := range alternatives {
					for _, value := range values {
						next = append(next, &ArrayLiteral{
							Elements: append(
								append([]Expression(nil), prefix.Elements...),
								value,
							),
							Position: view.pos,
						})
					}
				}
				alternatives = next
			}
			if exact && param.Name != "" {
				staticVals := make([]Expression, len(alternatives))
				for i, alternative := range alternatives {
					staticVals[i] = alternative
				}
				staticVals = c.internVariadicParamStaticValues(fn, param, staticVals)
				facts[param.Name] = checkReachableParamFact{
					typeExpr:   checkTypeArray,
					staticVals: staticVals,
				}
			}
			argIndex = len(view.args)
		case ParamKeywordRest:
			alternatives := []*HashLiteral{{Position: view.pos}}
			exact := true
			for i, kwarg := range view.kwargs {
				if _, used := usedKeywords[kwarg.Name]; used ||
					kwarg.Splat || lastKeyword[kwarg.Name] != i {
					continue
				}
				values, ok := c.evaluatedCallArgumentStaticAlternatives(kwarg.Value)
				if !ok || len(alternatives) > maxAlternatives/len(values) {
					exact = false
					break
				}
				next := make([]*HashLiteral, 0, len(alternatives)*len(values))
				for _, prefix := range alternatives {
					for _, value := range values {
						next = append(next, &HashLiteral{
							Pairs: append(
								append([]HashPair(nil), prefix.Pairs...),
								HashPair{
									Key: &StringLiteral{
										Value:    kwarg.Name,
										Position: kwarg.Value.Pos(),
									},
									Value: value,
								},
							),
							Position: view.pos,
						})
					}
				}
				alternatives = next
			}
			if exact && param.Name != "" {
				staticVals := make([]Expression, len(alternatives))
				for i, alternative := range alternatives {
					staticVals[i] = alternative
				}
				staticVals = c.internVariadicParamStaticValues(fn, param, staticVals)
				facts[param.Name] = checkReachableParamFact{
					typeExpr:   checkTypeHash,
					staticVals: staticVals,
				}
			}
			for _, kwarg := range view.kwargs {
				usedKeywords[kwarg.Name] = struct{}{}
			}
		}
	}
}

func callableParamLambdaArguments(
	call *CallExpr,
	target staticCallable,
	facts map[string]checkReachableParamFact,
) map[string][]Expression {
	if call == nil || target.fn == nil {
		return nil
	}
	arguments := callableParamArgumentExpressions(call, target, facts, true, true)
	lambdas := make(map[string][]Expression)
	for name, expressions := range arguments {
		for _, expression := range expressions {
			if lambdaLiteralBlock(expression) != nil {
				lambdas[name] = append(lambdas[name], expression)
			}
		}
	}
	if call.Block != nil {
		for _, param := range target.fn.Params {
			if param.Kind == ParamBlock && param.Name != "" {
				lambdas[param.Name] = append(lambdas[param.Name], call.Block)
				break
			}
		}
	}
	if len(lambdas) == 0 {
		return nil
	}
	return lambdas
}

func callableParamArgumentExpressions(
	call *CallExpr,
	target staticCallable,
	facts map[string]checkReachableParamFact,
	includeDefaults bool,
	preferCapturedIdentity bool,
) map[string][]Expression {
	if call == nil || target.fn == nil || callExpandsArguments(call) {
		return nil
	}
	view := staticCallViewFor(call, target)
	arguments := make(map[string][]Expression)
	appendExpressions := func(name string, expressions ...Expression) {
		for _, expression := range expressions {
			if expression != nil {
				arguments[name] = append(arguments[name], expression)
			}
		}
	}
	positionallyBound := make(map[string]struct{})
	for i, arg := range view.args {
		param, ok := positionalCallableParam(target.fn.Params, i)
		if !ok || param.Name == "" || param.Kind == ParamRest {
			continue
		}
		positionallyBound[param.Name] = struct{}{}
		fact, captured := facts[param.Name]
		if !preferCapturedIdentity || !captured || !fact.callableIdentityExact {
			appendExpressions(param.Name, arg)
		}
		if captured {
			appendExpressions(param.Name, fact.staticVals...)
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
		fact, captured := facts[kwarg.Name]
		if !preferCapturedIdentity || !captured || !fact.callableIdentityExact {
			appendExpressions(kwarg.Name, kwarg.Value)
		}
		if captured {
			appendExpressions(kwarg.Name, fact.staticVals...)
		}
	}
	if includeDefaults {
		for _, param := range target.fn.Params {
			fact, ok := facts[param.Name]
			if !ok || !fact.usesDefault {
				continue
			}
			appendExpressions(param.Name, param.DefaultVal)
		}
	}
	if len(arguments) == 0 {
		return nil
	}
	return arguments
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
	if rawType != nil && c.boundaryTypeRejected(rawType, checkTypeString) {
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
	arms, ok := boundaryTypeExprArms(inferred, 0)
	if !ok || len(arms) == 0 {
		return
	}
	knownMismatch := false
	for _, arm := range arms {
		if _, isShape := shapeValuePayload(arm); isShape {
			continue
		}
		if arm.Kind == TypeAny || arm.Kind == TypeUnknown {
			continue
		}
		knownMismatch = true
		break
	}
	if !knownMismatch {
		return
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
	arms, ok := boundaryTypeExprArms(inferred, 0)
	if !ok || len(arms) == 0 {
		return
	}
	knownMismatch := false
	for _, arm := range arms {
		if _, shapeValue := shapeValuePayload(arm); shapeValue {
			knownMismatch = true
			continue
		}
		if arm.Kind == TypeAny || arm.Kind == TypeUnknown {
			continue
		}
		if typeArmProvablyNotClass(arm) {
			knownMismatch = true
		}
	}
	if !knownMismatch {
		return
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

func (c *scriptChecker) rangeEndpointConversionMaySucceed(expr Expression) bool {
	if expr == nil {
		return true
	}
	if value, literal := staticLiteralValue(expr); literal {
		_, err := valueToInt64(value)
		return err == nil
	}
	if value, literal := staticBigIntegerLiteralValue(expr); literal {
		return value.IsInt64()
	}
	if rangeEndpointIsBigIntegerLiteral(expr) {
		return false
	}
	inferred := c.inferExpressionType(expr)
	return inferred == nil ||
		!typeExprsDisjoint(inferred, checkTypeNumber, c.checkNamedTypeResolver())
}

func staticBigIntegerLiteralValue(expr Expression) (*big.Int, bool) {
	switch typed := expr.(type) {
	case *IntegerLiteral:
		if typed.Big == nil {
			return nil, false
		}
		return new(big.Int).Set(typed.Big), true
	case *UnaryExpr:
		value, literal := staticBigIntegerLiteralValue(typed.Right)
		if !literal {
			return nil, false
		}
		switch typed.Operator {
		case tokenMinus:
			return new(big.Int).Neg(value), true
		case tokenPlus:
			return value, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func rangeEndpointIsBigIntegerLiteral(expr Expression) bool {
	switch typed := expr.(type) {
	case *IntegerLiteral:
		return typed.Big != nil
	case *UnaryExpr:
		return rangeEndpointIsBigIntegerLiteral(typed.Right)
	default:
		return false
	}
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
		return scriptCallBindingPlan{
			defaultParams:   defaults,
			boundParamCount: len(target.fn.Params),
			bindingStarts:   true,
			bodyMayEnter:    true,
		}
	}
	if !scriptCallBodyMayEnter(call, target) {
		return scriptCallBindingPlan{}
	}

	view := staticCallViewFor(call, target)
	usedKeywords := make(map[string]struct{}, len(view.kwargs))
	argIndex := 0
	inputs := make([]scriptParamBindingInput, len(target.fn.Params))
	for i := range inputs {
		inputs[i].mayBind = true
	}
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
				inputs[i].usesDefault = true
				continue
			}
			if !c.callArgumentMayBindType(value, param.Type) {
				inputs[i].mayBind = false
			}
			if ty := c.ivarParamContract(target.fn, param); ty != nil &&
				!c.ivarParamBindingFactMayStore(
					c.ivarParamArgumentBindingFact(value, param),
					param,
					ty,
				) {
				inputs[i].mayBind = false
			}
		case ParamKeyword:
			if kwIndex := keywordIndex(view, param.Name); kwIndex >= 0 {
				usedKeywords[param.Name] = struct{}{}
				value := view.kwargs[kwIndex].Value
				if !c.callArgumentMayBindType(value, param.Type) {
					inputs[i].mayBind = false
				}
				if ty := c.ivarParamContract(target.fn, param); ty != nil &&
					!c.ivarParamBindingFactMayStore(
						c.ivarParamArgumentBindingFact(value, param),
						param,
						ty,
					) {
					inputs[i].mayBind = false
				}
				continue
			}
			inputs[i].usesDefault = true
		case ParamRest:
			if !c.callRestArgumentsMayBindType(view.args[argIndex:], param.Type) {
				inputs[i].mayBind = false
			}
			argIndex = len(view.args)
		case ParamKeywordRest:
			if !c.callKeywordRestArgumentsMayBindType(view.kwargs, usedKeywords, param.Type) {
				inputs[i].mayBind = false
			}
			for _, kwarg := range view.kwargs {
				usedKeywords[kwarg.Name] = struct{}{}
			}
		case ParamBlock:
			if !c.callBlockMayBindType(call, param.Type) {
				inputs[i].mayBind = false
			}
		}
	}
	facts := c.reachableCallParamFacts(call, target)
	return c.scriptCallBindingPlanInContext(target.fn, inputs, facts)
}

// scriptCallBodyMustEnter is the universal counterpart to
// scriptCallBindingPlan(...).bodyMayEnter. It proves that argument shape,
// defaults, and runtime type normalization cannot reject the selected call
// before its body starts.
func (c *scriptChecker) scriptCallBodyMustEnter(
	call *CallExpr,
	target staticCallable,
) bool {
	if call == nil || target.fn == nil || callExpandsArguments(call) ||
		!scriptCallBodyMayEnter(call, target) {
		return false
	}
	view := staticCallViewFor(call, target)
	usedKeywords := make(map[string]struct{}, len(view.kwargs))
	argIndex := 0
	for _, param := range target.fn.Params {
		switch param.Kind {
		case ParamNormal:
			var value Expression
			if argIndex < len(view.args) {
				value = view.args[argIndex]
				argIndex++
			} else if index := keywordIndex(view, param.Name); index >= 0 {
				value = view.kwargs[index].Value
				usedKeywords[param.Name] = struct{}{}
			} else {
				if param.DefaultVal == nil ||
					!expressionProvenNonRaising(param.DefaultVal) {
					return false
				}
				fact := c.defaultExpressionBindingFact(param)
				if !c.defaultBindingFactMustBindType(fact, param.Type) {
					return false
				}
				if ty := c.ivarParamContract(target.fn, param); ty != nil &&
					!c.ivarParamBindingFactMustStore(fact, param, ty) {
					return false
				}
				continue
			}
			if !c.callArgumentMustBindType(value, param.Type) {
				return false
			}
			if ty := c.ivarParamContract(target.fn, param); ty != nil &&
				!c.ivarParamBindingFactMustStore(
					c.ivarParamArgumentBindingFact(value, param),
					param,
					ty,
				) {
				return false
			}
		case ParamKeyword:
			index := keywordIndex(view, param.Name)
			if index < 0 {
				if param.DefaultVal == nil ||
					!expressionProvenNonRaising(param.DefaultVal) {
					return false
				}
				fact := c.defaultExpressionBindingFact(param)
				if !c.defaultBindingFactMustBindType(fact, param.Type) {
					return false
				}
				if ty := c.ivarParamContract(target.fn, param); ty != nil &&
					!c.ivarParamBindingFactMustStore(fact, param, ty) {
					return false
				}
				continue
			}
			value := view.kwargs[index].Value
			usedKeywords[param.Name] = struct{}{}
			if !c.callArgumentMustBindType(value, param.Type) {
				return false
			}
			if ty := c.ivarParamContract(target.fn, param); ty != nil &&
				!c.ivarParamBindingFactMustStore(
					c.ivarParamArgumentBindingFact(value, param),
					param,
					ty,
				) {
				return false
			}
		case ParamRest:
			if param.Type != nil {
				rest := &ArrayLiteral{
					Elements: append([]Expression(nil), view.args[argIndex:]...),
					Position: call.Pos(),
				}
				if !c.callArgumentMustBindType(rest, param.Type) {
					return false
				}
			}
			argIndex = len(view.args)
		case ParamKeywordRest:
			if param.Type != nil {
				rest := &HashLiteral{Position: call.Pos()}
				for _, kwarg := range view.kwargs {
					if _, used := usedKeywords[kwarg.Name]; used {
						continue
					}
					rest.Pairs = append(rest.Pairs, HashPair{
						Key: &SymbolLiteral{
							Name:     kwarg.Name,
							Position: kwarg.Value.Pos(),
						},
						Value: kwarg.Value,
					})
				}
				if !c.callArgumentMustBindType(rest, param.Type) {
					return false
				}
			}
		case ParamBlock:
			var value Expression = &NilLiteral{}
			if call.BlockArg != nil {
				value = call.BlockArg
			} else if call.Block != nil {
				if param.Type != nil && !typeExprMayIncludeCallable(param.Type) {
					return false
				}
				continue
			}
			if !c.callArgumentMustBindType(value, param.Type) {
				return false
			}
		}
	}
	return true
}

func (c *scriptChecker) scriptCallBindingPlanInContext(
	fn *ScriptFunction,
	inputs []scriptParamBindingInput,
	facts map[string]checkReachableParamFact,
) (plan scriptCallBindingPlan) {
	if fn == nil || len(inputs) != len(fn.Params) {
		return plan
	}
	plan.bindingStarts = true
	plan.exactBindings = true
	restoreResolution := c.withClassConstantProofResolution(fn, nil)
	defer restoreResolution()
	previousScopes := c.scopes
	restoreInference := c.withIsolatedLocalInference()
	c.scopes = nil
	popScope := c.pushScope(make(map[string]struct{}))
	defer func() {
		popScope()
		restoreInference()
		c.scopes = previousScopes
	}()
	previousFacts := c.reachableParamFacts
	c.reachableParamFacts = facts
	defer func() { c.reachableParamFacts = previousFacts }()
	previousPending := c.pendingBindingParams
	c.pendingBindingParams = functionParamBindingNames(fn)
	defer func() { c.pendingBindingParams = previousPending }()

	c.seedInstanceIvarFacts(fn)
	c.linkReachableParamAliases(fn.Params)
	for i, param := range fn.Params {
		input := inputs[i]
		if input.usesDefault {
			plan.defaultParams = append(plan.defaultParams, i)
			if !c.defaultExpressionMayCompleteForBinding(param) {
				return plan
			}
			fact := c.defaultExpressionBindingFact(param)
			if !c.defaultBindingFactMayBindType(fact, param.Type) {
				return plan
			}
			if ty := c.ivarParamContract(fn, param); ty != nil &&
				!c.ivarParamBindingFactMayStore(fact, param, ty) {
				return plan
			}
		} else if !input.mayBind {
			return plan
		}
		c.withSuppressedWarnings(func() {
			c.checkIvarParamBinding("", fn, param)
		})
		c.recordParamBinding(param)
		c.applyReachableParamFact(param)
		removeFunctionParamBindingNames(c.pendingBindingParams, param)
		plan.boundParamCount = i + 1
	}
	plan.bodyMayEnter = true
	return plan
}

// checkScriptCallContextualDefaults diagnoses exact default values that only
// become known after earlier call arguments bind. Definition-time checking
// already covers defaults that are exact without call context.
func (c *scriptChecker) checkScriptCallContextualDefaults(
	function string,
	fn *ScriptFunction,
	plan scriptCallBindingPlan,
	facts map[string]checkReachableParamFact,
) {
	if fn == nil || !plan.bindingStarts || len(plan.defaultParams) == 0 {
		return
	}
	restoreResolution := c.withClassConstantProofResolution(fn, nil)
	defer restoreResolution()
	previousScopes := c.scopes
	restoreInference := c.withIsolatedLocalInference()
	c.scopes = nil
	popScope := c.pushScope(make(map[string]struct{}))
	defer func() {
		popScope()
		restoreInference()
		c.scopes = previousScopes
	}()
	previousFacts := c.reachableParamFacts
	c.reachableParamFacts = facts
	defer func() { c.reachableParamFacts = previousFacts }()
	previousPending := c.pendingBindingParams
	c.pendingBindingParams = functionParamBindingNames(fn)
	defer func() { c.pendingBindingParams = previousPending }()

	defaults := make(map[int]struct{}, len(plan.defaultParams))
	for _, index := range plan.defaultParams {
		defaults[index] = struct{}{}
	}
	pristineExact := make(map[int]bool, len(defaults))
	for index := range defaults {
		_, pristineExact[index] = c.staticLiteralValueAlternatives(
			fn.Params[index].DefaultVal,
		)
	}

	c.seedInstanceIvarFacts(fn)
	c.linkReachableParamAliases(fn.Params)
	for i, param := range fn.Params {
		_, defaultRuns := defaults[i]
		if i >= plan.boundParamCount && !defaultRuns {
			return
		}
		if defaultRuns {
			if !c.defaultExpressionMayCompleteForBinding(param) {
				return
			}
			fact := c.defaultExpressionBindingFact(param)
			if !pristineExact[i] && len(fact.values) > 0 {
				if param.Type != nil &&
					validateTypeExprResolved(param.Type, c.runtimeTypeContext()) == nil {
					if err := c.staticValuesBoundaryMismatch(fact.values, param.Type); err != nil {
						c.addValueTypeWarning(
							function,
							param.DefaultVal.Pos(),
							"default value for "+param.Name,
							err,
						)
					}
				}
				if ty := c.ivarParamContract(fn, param); ty != nil {
					if err := c.ivarParamBindingFactMismatch(fact, param, ty); err != nil {
						c.addValueTypeWarning(
							function,
							param.DefaultVal.Pos(),
							"default value for @"+param.Name,
							err,
						)
					}
				}
			}
			if !c.defaultBindingFactMayBindType(fact, param.Type) {
				return
			}
			if ty := c.ivarParamContract(fn, param); ty != nil &&
				!c.ivarParamBindingFactMayStore(fact, param, ty) {
				return
			}
		}
		if i >= plan.boundParamCount {
			return
		}
		c.withSuppressedWarnings(func() {
			c.checkIvarParamBinding("", fn, param)
		})
		c.recordParamBinding(param)
		c.applyReachableParamFact(param)
		removeFunctionParamBindingNames(c.pendingBindingParams, param)
	}
}

func functionParamBindingNames(fn *ScriptFunction) map[string]struct{} {
	if fn == nil {
		return nil
	}
	names := make(map[string]struct{}, len(fn.Params))
	for _, param := range fn.Params {
		if param.Name != "" {
			names[param.Name] = struct{}{}
		}
		collectBindingTarget(param.Target, names)
	}
	return names
}

func removeFunctionParamBindingNames(names map[string]struct{}, param Param) {
	delete(names, param.Name)
	bound := make(map[string]struct{})
	collectBindingTarget(param.Target, bound)
	for name := range bound {
		delete(names, name)
	}
}

func bindingDefaultExpectation(param Param) expressionExpectation {
	if param.Kind == ParamKeyword {
		return typeExpressionExpectation(param.Type)
	}
	return positionalArgumentExpectation(param)
}

func (c *scriptChecker) defaultExpressionMayCompleteForBinding(param Param) bool {
	return c.expressionMayCompleteForBindingWithExpectation(
		param.DefaultVal,
		bindingDefaultExpectation(param),
	)
}

func (c *scriptChecker) expressionMayCompleteForBindingWithAuto(
	expr Expression,
	autoCall bool,
) bool {
	if autoCall {
		return c.expressionMayCompleteForBinding(expr)
	}
	if _, bindable := c.bareMemberArgumentCallableFact(expr); bindable {
		return c.expressionMayCompleteForBinding(expr.(*MemberExpr).Object)
	}
	if identity, bindable := bareIdentifierCallableValue(expr); bindable {
		ident := identity.(*Identifier)
		if _, pending := c.pendingBindingParams[ident.Name]; pending &&
			!c.staticNameShadowed(ident.Name) {
			return false
		}
		return true
	}
	switch typed := expr.(type) {
	case *RescueExpr:
		bodyCompletes := c.expressionMayCompleteForBindingWithAuto(typed.Body, false)
		if errorKind, exact := c.staticallyRaisedExpressionErrorKind(typed.Body); exact &&
			!staticErrorKindMatchesRescue(errorKind, nil) {
			return bodyCompletes
		}
		return bodyCompletes || c.expressionMayCompleteForBindingWithAuto(typed.Fallback, false)
	case *TypeLiteral:
		return !c.typeLiteralStaticallyShadowed(typed) ||
			c.expressionMayCompleteForBindingWithAuto(typed.Fallback, false)
	default:
		return c.expressionMayCompleteForBinding(expr)
	}
}

func (c *scriptChecker) expressionMayCompleteForBindingWithExpectation(
	expr Expression,
	expectation expressionExpectation,
) bool {
	if expectation.empty() {
		return c.expressionMayCompleteForBinding(expr)
	}
	if expectation.includesCallable() {
		if _, bindable := c.bareMemberArgumentCallableFact(expr); bindable {
			return c.expressionMayCompleteForBinding(expr.(*MemberExpr).Object)
		}
		if identity, bindable := bareIdentifierCallableValue(expr); bindable {
			ident := identity.(*Identifier)
			if _, pending := c.pendingBindingParams[ident.Name]; pending &&
				!c.staticNameShadowed(ident.Name) {
				return false
			}
			return true
		}
	}
	switch typed := expr.(type) {
	case *ConditionalExpr:
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if known {
			branch := typed.Alternate
			if truthy {
				branch = typed.Consequent
			}
			return c.expressionMayCompleteForBindingWithExpectation(branch, expectation)
		}
		return c.expressionMayCompleteForBindingWithExpectation(typed.Consequent, expectation) ||
			c.expressionMayCompleteForBindingWithExpectation(typed.Alternate, expectation)
	case *IfExpr:
		if !c.expressionMayCompleteForBinding(typed.Condition) {
			return false
		}
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if (!known || truthy) &&
			c.expressionMayCompleteForBindingWithExpectation(typed.Consequent, expectation) {
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
			if (!branchKnown || branchTruthy) &&
				c.expressionMayCompleteForBindingWithExpectation(branch.Result, expectation) {
				return true
			}
			if branchKnown && branchTruthy {
				return false
			}
		}
		return c.expressionMayCompleteForBindingWithExpectation(typed.Alternate, expectation)
	case *CaseExpr:
		if !c.expressionMayCompleteForBinding(typed.Target) {
			return false
		}
		if result, known := c.inferredCaseExpressionResult(typed); known {
			return c.expressionMayCompleteForBindingWithExpectation(result, expectation)
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if !c.expressionMayCompleteForBinding(value.Expr) ||
					!c.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
					return false
				}
				if c.expressionMayCompleteForBindingWithExpectation(clause.Result, expectation) {
					return true
				}
			}
		}
		return c.expressionMayCompleteForBindingWithExpectation(typed.ElseExpr, expectation)
	case *RescueExpr:
		return c.expressionMayCompleteForBindingWithAuto(
			typed,
			!expectation.includesCallable(),
		)
	case *ArrayLiteral:
		elementExpectation, ok := expectation.arrayElementExpectation()
		if !ok {
			break
		}
		for i, element := range typed.Elements {
			if !c.expressionMayCompleteForBindingWithExpectation(
				element,
				elementExpectation(i, len(typed.Elements)),
			) {
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
			if !c.expressionMayCompleteForBinding(pair.Key) {
				return false
			}
			valueExpectation := expressionExpectation{}
			if key, ok := staticLiteralValue(pair.Key); ok {
				valueExpectation = typeExpressionExpectation(hashLiteralValueType(expectation.ty, key))
			}
			if !c.expressionMayCompleteForBindingWithExpectation(pair.Value, valueExpectation) {
				return false
			}
		}
		return true
	}
	return c.expressionMayCompleteForBinding(expr)
}

func (c *scriptChecker) callArgumentMayBindType(expr Expression, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	inferred, captured := c.callArgumentFacts[expr]
	if !captured {
		inferred = c.inferExpressionTypeWithExpectation(expr, typeExpressionExpectation(ty))
	}
	if inferred != nil && typeExprsDisjoint(inferred, ty, c.checkNamedTypeResolver()) {
		return false
	}
	if values, exact := c.callStaticLiteralValueAlternatives(expr); exact {
		return c.staticValuesTypeMismatch(values, ty) == nil
	}
	return true
}

// callArgumentMustBindType reports whether every value represented by expr is
// accepted by ty. It complements callArgumentMayBindType: dispatch-effect
// tracking needs both the body-entering arm and the pre-body rejection arm.
func (c *scriptChecker) callArgumentMustBindType(expr Expression, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	if values, exact := c.callStaticLiteralValueAlternatives(expr); exact {
		return c.staticValuesMustNormalizeType(values, ty)
	}
	inferred, captured := c.callArgumentFacts[expr]
	if !captured {
		inferred = c.inferExpressionTypeWithExpectation(
			expr,
			typeExpressionExpectation(ty),
		)
	}
	return inferred != nil &&
		typeExprSatisfies(inferred, ty, c.checkNamedTypeResolver())
}

func (c *scriptChecker) defaultExpressionBindingFact(param Param) defaultBindingFact {
	fact := defaultBindingFact{
		inferred: c.inferExpressionTypeWithExpectation(
			param.DefaultVal,
			bindingDefaultExpectation(param),
		),
	}
	if values, exact := c.staticLiteralValueAlternatives(param.DefaultVal); exact {
		fact.values = values
	}
	return fact
}

func (c *scriptChecker) defaultBindingFactMayBindType(fact defaultBindingFact, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	if fact.inferred != nil &&
		typeExprsDisjoint(fact.inferred, ty, c.checkNamedTypeResolver()) {
		return false
	}
	if len(fact.values) > 0 {
		return c.staticValuesTypeMismatch(fact.values, ty) == nil
	}
	return fact.inferred == nil ||
		!typeExprsDisjoint(fact.inferred, ty, c.checkNamedTypeResolver())
}

func (c *scriptChecker) defaultBindingFactMustBindType(fact defaultBindingFact, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, c.runtimeTypeContext()); err != nil {
		return false
	}
	if len(fact.values) > 0 {
		return c.staticValuesMustNormalizeType(fact.values, ty)
	}
	return fact.inferred != nil &&
		typeExprSatisfies(fact.inferred, ty, c.checkNamedTypeResolver())
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
	case *YieldExpr:
		if c.yieldBlockKnownAbsent() {
			return false
		}
		for _, arg := range typed.Args {
			if !c.expressionMayCompleteForBinding(arg) {
				return false
			}
		}
		return true
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
		if member, ok := typed.Callee.(*MemberExpr); ok && member.Property == "call" {
			if block := c.resolveImmediateLambdaBlock(member.Object); block != nil {
				if !c.immediateLambdaCallEntry(block, typed).mayEnter ||
					!c.immediateLambdaBodyMayCompleteForBinding(block) {
					return false
				}
			}
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
		if _, pending := c.pendingBindingParams[typed.Name]; pending &&
			!c.staticNameShadowed(typed.Name) {
			return false
		}
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
		if c.hashLikeDataMemberLookupProvablyFails(typed) {
			return false
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
		truthy, known := c.inferredConditionTruthiness(typed.Condition)
		if known {
			branch := typed.Alternate
			if truthy {
				branch = typed.Consequent
			}
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
			c.rangeEndpointConversionMaySucceed(typed.Start) &&
			c.expressionMayCompleteForBinding(typed.End) &&
			c.rangeEndpointConversionMaySucceed(typed.End)
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

func (c *scriptChecker) explicitSelfMemberCallable(
	member *MemberExpr,
) (staticCallable, bool) {
	if member == nil || !c.expressionIsCurrentSelf(member.Object) {
		return staticCallable{}, false
	}
	methods := c.selfClass.Methods
	separator := "#"
	if c.selfClassContext {
		methods = c.selfClass.ClassMethods
		separator = "."
	}
	fn := methods[member.Property]
	if fn == nil {
		return staticCallable{}, false
	}
	return staticCallable{
		name:       c.selfClass.Name + separator + member.Property,
		fn:         fn,
		resolution: calleeMemberMethod,
	}, true
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
		if target, resolved := c.factReceiverNilOnlyMemberCallable(member); resolved {
			return target, true
		}
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

// factReceiverNilOnlyMemberCallable resolves nil's typed contract when every
// non-nil receiver arm is an exact shape that provably lacks the member.
// Those shape paths raise before producing a value, so they do not make the
// sole completing scalar dispatch's invariant result gradual.
func (c *scriptChecker) factReceiverNilOnlyMemberCallable(member *MemberExpr) (staticCallable, bool) {
	if member == nil || member.Safe ||
		memberKindOwns("hash", member.Property) ||
		isUniversalMember(member.Property) {
		return staticCallable{}, false
	}
	arms, ok := typeExprArms(c.inferExpressionType(member.Object), 0)
	if !ok || len(arms) == 0 {
		return staticCallable{}, false
	}
	sawNil, sawShapeMiss := false, false
	for _, arm := range arms {
		switch arm.Kind {
		case TypeNil:
			sawNil = true
		case TypeShape:
			if arm.Open {
				return staticCallable{}, false
			}
			if _, present := arm.Shape[member.Property]; present {
				return staticCallable{}, false
			}
			sawShapeMiss = true
		default:
			return staticCallable{}, false
		}
	}
	if !sawNil || !sawShapeMiss {
		return staticCallable{}, false
	}
	spec, ok := staticMemberSpecs["nil."+member.Property]
	if !ok {
		return staticCallable{}, false
	}
	return staticCallable{name: "nil." + member.Property, spec: spec}, true
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
func (c *scriptChecker) specialBuiltinCallMayComplete(
	call *CallExpr,
	name string,
	receiverFacts ...*TypeExpr,
) bool {
	if property, mutator := arrayMutatorBuiltinProperty(name); mutator {
		return c.arrayMutatorCallMayComplete(call, property)
	}
	switch name {
	case "JSON.parse_as":
		return c.parseAsCallMayComplete(call)
	case isTypeMemberName:
		return c.isTypeCallMayComplete(call)
	case "hash.store", "hash.merge!", "hash.update", "hash.replace":
		if call == nil {
			return true
		}
		var receiverFact *TypeExpr
		member, direct := call.Callee.(*MemberExpr)
		if !direct {
			return true
		}
		if _, direct = member.Object.(*Identifier); !direct {
			return true
		}
		if len(receiverFacts) != 0 {
			receiverFact = receiverFacts[0]
		}
		if receiverFact == nil {
			receiverFact = c.inferExpressionType(member.Object)
		}
		return !c.hashMutatorCallProvablyAborts(
			call,
			name,
			receiverFact,
			c.callArgumentFacts,
		)
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
				c.checkIvarParamArgument(function, call.args[argIdx], fn, param, name)
				argIdx++
				continue
			}
			if kwIndex := keywordIndex(call, param.Name); kwIndex >= 0 {
				c.checkIvarParamArgument(function, call.kwargs[kwIndex].Value, fn, param, name)
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			}
		case ParamKeyword:
			if kwIndex := keywordIndex(call, param.Name); kwIndex >= 0 {
				c.checkIvarParamArgument(function, call.kwargs[kwIndex].Value, fn, param, name)
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
			// checking the collected array against a union contract, or each
			// argument against a plain array's element contract.
			if ty.Kind == TypeUnion {
				inferred := c.inferredRestArgumentType(args, restElementBoundaryType(ty))
				if c.restLiteralRejectedByUnion(args, ty) ||
					(inferred != nil && c.boundaryTypeRejected(inferred, ty)) {
					warningPos := pos
					if len(args) > 0 {
						warningPos = args[0].Pos()
					}
					c.add(function, warningPos, "call to %s argument %s expected %s, got %s",
						callName, paramName, formatTypeExpr(ty), formatTypeExpr(inferred))
				}
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

func restElementBoundaryType(ty *TypeExpr) *TypeExpr {
	if ty == nil {
		return nil
	}
	var elements []*TypeExpr
	var collect func(*TypeExpr)
	collect = func(candidate *TypeExpr) {
		if candidate == nil {
			return
		}
		switch candidate.Kind {
		case TypeArray:
			if len(candidate.TypeArgs) == 1 {
				elements = append(elements, candidate.TypeArgs[0])
			}
		case TypeUnion:
			for _, arm := range candidate.Union {
				collect(arm)
			}
		}
	}
	collect(ty)
	return unionTypeExprs(elements...)
}

func (c *scriptChecker) inferredRestArgumentType(args []Expression, expected *TypeExpr) *TypeExpr {
	elements := make([]*TypeExpr, 0, len(args))
	seenSources := make(map[checkValueSource]struct{})
	sawUnknown := false
	for _, arg := range args {
		inferred := c.inferredRestExpressionType(arg, expected)
		if inferred == nil {
			sawUnknown = true
			continue
		}
		if source, sourced := c.evaluatedValueSourceForExpression(arg); sourced {
			if _, duplicate := seenSources[source]; duplicate {
				continue
			}
			seenSources[source] = struct{}{}
		}
		elements = append(elements, inferred)
	}
	if len(elements) == 0 {
		return &TypeExpr{Kind: TypeArray}
	}
	marker := literalElementsMarker
	if sawUnknown {
		marker = literalPartialElementsMarker
		if len(elements) == 1 {
			marker = literalPartialAlternativeElementsMarker
		}
	} else if len(elements) == 1 {
		marker = literalAlternativeElementsMarker
	}
	return &TypeExpr{
		Kind:     TypeArray,
		Name:     marker,
		TypeArgs: []*TypeExpr{unionTypeExprs(elements...)},
	}
}

func (c *scriptChecker) inferredRestExpressionType(arg Expression, expected *TypeExpr) *TypeExpr {
	if value, literal := staticLiteralValue(arg); literal {
		return typeFactForValue(value)
	}
	if captured, ok := c.callArgumentFacts[arg]; ok {
		return captured
	}
	if expected != nil {
		return c.inferExpressionTypeWithExpectation(arg, typeExpressionExpectation(expected))
	}
	return c.inferExpressionType(arg)
}

// restLiteralRejectedByUnion preserves concrete literal normalization before
// the mixed rest list degrades to inferred aggregate types.
func (c *scriptChecker) restLiteralRejectedByUnion(args []Expression, ty *TypeExpr) bool {
	arms, exact := boundaryTypeExprArms(ty, 0)
	if !exact {
		return false
	}
	for _, arg := range args {
		values, valuesExact := c.callStaticLiteralValueAlternatives(arg)
		if !valuesExact {
			continue
		}
		for _, value := range values {
			sawArray := false
			accepted := false
			for _, arm := range arms {
				if arm.Kind == TypeAny || arm.Kind == TypeUnknown {
					accepted = true
					break
				}
				if arm.Kind != TypeArray {
					continue
				}
				sawArray = true
				if len(arm.TypeArgs) == 0 {
					accepted = true
					break
				}
				if len(arm.TypeArgs) == 1 &&
					c.checkRuntimeStaticValueType(value, arm.TypeArgs[0]) == nil {
					accepted = true
					break
				}
			}
			if sawArray && !accepted {
				return true
			}
		}
	}
	return false
}

func (c *scriptChecker) keywordRestLiteralRejectedByUnion(kwargs []KeywordArg, usedKw map[string]bool, ty *TypeExpr) bool {
	arms, exact := boundaryTypeExprArms(ty, 0)
	if !exact {
		return false
	}
	for _, kwarg := range kwargs {
		if usedKw != nil && usedKw[kwarg.Name] {
			continue
		}
		values, valuesExact := c.callStaticLiteralValueAlternatives(kwarg.Value)
		if !valuesExact {
			continue
		}
		for _, value := range values {
			sawContainer := false
			accepted := false
			for _, arm := range arms {
				if arm.Kind == TypeAny || arm.Kind == TypeUnknown {
					accepted = true
					break
				}
				switch arm.Kind {
				case TypeHash:
					sawContainer = true
					if len(arm.TypeArgs) == 0 ||
						(len(arm.TypeArgs) == 2 &&
							c.checkRuntimeStaticValueType(NewString(kwarg.Name), arm.TypeArgs[0]) == nil &&
							c.checkRuntimeStaticValueType(value, arm.TypeArgs[1]) == nil) {
						accepted = true
					}
				case TypeShape:
					sawContainer = true
					field, known := arm.Shape[kwarg.Name]
					if (!known && arm.Open) ||
						(known && c.checkRuntimeStaticValueType(value, field) == nil) {
						accepted = true
					}
				}
				if accepted {
					break
				}
			}
			if sawContainer && !accepted {
				return true
			}
		}
	}
	return false
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
				inferred := c.inferredKeywordRestArgumentType(last)
				if c.keywordRestLiteralRejectedByUnion(kwargs, usedKw, ty) ||
					c.boundaryTypeRejected(inferred, ty) {
					c.add(function, kwarg.Value.Pos(), "call to %s argument %s expected %s, got %s",
						callName, paramName, formatTypeExpr(ty), formatTypeExpr(inferred))
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

func (c *scriptChecker) inferredKeywordRestArgumentType(values map[string]Expression) *TypeExpr {
	fields := make(map[string]*TypeExpr, len(values))
	sources := make(map[string]checkValueSource, len(values))
	for name, expr := range values {
		inferred, captured := c.callArgumentFacts[expr]
		if !captured {
			inferred = c.inferExpressionType(expr)
		}
		if inferred == nil {
			inferred = &TypeExpr{Kind: TypeUnknown}
		}
		fields[name] = inferred
		if source, ok := c.evaluatedValueSourceForExpression(expr); ok {
			sources[name] = source
		}
	}
	shape := &TypeExpr{
		Kind:  TypeShape,
		Name:  shapeKeysStringMarker,
		Shape: fields,
	}
	c.recordShapeFieldSources(shape, sources)
	return shape
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
	if value, literal := staticLiteralValue(expr); literal {
		if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
			return
		}
		if err := c.checkRuntimeStaticValueType(value, ty); err != nil {
			c.addArgumentValueWarning(function, expr.Pos(), callName, paramName, err)
		}
		return
	}
	warningsBeforeInference := len(c.warnings)
	c.checkInferredArgument(function, expr, ty, callName, paramName)
	if len(c.warnings) > warningsBeforeInference {
		return
	}
	values, exact := c.callStaticLiteralValueAlternatives(expr)
	if !exact {
		return
	}
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	if err := c.staticValuesBoundaryMismatch(values, ty); err != nil {
		c.addArgumentValueWarning(function, expr.Pos(), callName, paramName, err)
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
		rng := Range{
			Exclusive: typed.Exclusive,
			Beginless: typed.Start == nil,
			Endless:   typed.End == nil,
		}
		if typed.Start != nil {
			start, ok := staticLiteralRangeEndpoint(typed.Start)
			if !ok {
				return NewNil(), false
			}
			rng.Start = start
		}
		if typed.End != nil {
			end, ok := staticLiteralRangeEndpoint(typed.End)
			if !ok {
				return NewNil(), false
			}
			rng.End = end
		}
		return NewRange(rng), true
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

func staticIterableProvenEmpty(expr Expression) bool {
	array, ok := expr.(*ArrayLiteral)
	return ok && len(array.Elements) == 0
}

func staticIterableProvenSingle(expr Expression) bool {
	array, ok := expr.(*ArrayLiteral)
	return ok && len(array.Elements) == 1
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
	if !ok || val.Kind() != KindInt && val.Kind() != KindFloat {
		return 0, false
	}
	endpoint, err := valueToInt64(val)
	return endpoint, err == nil
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
	return c.staticClassArgument(ident)
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
		if classDef, ok := c.staticClassArgument(obj); ok {
			return classDef.Name + "." + member.Property, true
		}
		if c.identifierShadowed(obj.Name) || c.hostGlobalShadows(obj.Name) {
			return "", false
		}
		return obj.Name + "." + member.Property, true
	case *ScopeExpr:
		if classDef, ok := c.staticClassArgument(obj); ok {
			return classDef.Name + "." + member.Property, true
		}
		return "", false
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

// callTargetsBlockCapturingBuiltin recognizes core constructors that retain a
// supplied block without entering it. The resolved target and builtin function
// identity both matter: script bindings and host replacements may use the same
// spelling while executing the block immediately.
func (c *scriptChecker) callTargetsBlockCapturingBuiltin(
	call *CallExpr,
	target staticCallable,
	resolved bool,
) bool {
	if call == nil || !resolved || target.fn != nil || target.constructor {
		return false
	}
	switch callee := call.Callee.(type) {
	case *Identifier:
		if target.name != callee.Name {
			return false
		}
		switch callee.Name {
		case "lambda":
			return c.coreLambdaBinding()
		case "proc":
			return c.coreBuiltinBinding("proc", builtinProc)
		}
	case *MemberExpr:
		object, ok := callee.Object.(*Identifier)
		if !ok || callee.Property != "new" ||
			target.name != object.Name+"."+callee.Property {
			return false
		}
		switch object.Name {
		case "Proc":
			return c.coreNamespaceBuiltinBinding("Proc", "new", builtinProc)
		case "Hash":
			return c.coreNamespaceBuiltinBinding("Hash", "new", builtinHashNew)
		}
	}
	return false
}

// coreLambdaBinding reports whether a bare lambda call resolves to the
// language's lambda constructor. Call-option globals normally shadow core
// builtins, but a host may pass back the cloned lambda value returned by
// Engine.Builtins; its function identity preserves the same local-return
// semantics. Checking the function itself avoids treating a mutated snapshot
// as core.
func (c *scriptChecker) coreLambdaBinding() bool {
	return c.coreBuiltinBinding("lambda", builtinLambda)
}

func (c *scriptChecker) coreBuiltinBinding(name string, fn BuiltinFunc) bool {
	if c.hostGlobalShadows(name) &&
		(c.optionGlobalsOverride || c.optionGlobalSeeded(name)) {
		return builtinValueUsesFunction(c.optionGlobals[name], fn)
	}
	return !c.hostBuiltinOverrides(name)
}

func (c *scriptChecker) coreNamespaceBuiltinBinding(
	namespace string,
	member string,
	fn BuiltinFunc,
) bool {
	if c.hostGlobalShadows(namespace) &&
		(c.optionGlobalsOverride || c.optionGlobalSeeded(namespace)) {
		object := c.optionGlobals[namespace]
		if object.Kind() != KindObject {
			return false
		}
		value, ok := object.Hash()[member]
		return ok && builtinValueUsesFunction(value, fn)
	}
	return !c.hostBuiltinOverrides(namespace) &&
		!c.namespaceMemberMutated(namespace, member)
}

func builtinValueUsesFunction(value Value, fn BuiltinFunc) bool {
	builtin := valueBuiltin(value)
	return builtin != nil && builtin.Fn != nil &&
		reflect.ValueOf(builtin.Fn).Pointer() == reflect.ValueOf(fn).Pointer()
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

type blockLiteralCallEntryOutcome struct {
	mayEnter  bool
	mayReject bool
}

// blockLiteralBindingOutcome separates a reachable binding arm from one that
// is guaranteed to bind, so callers can retain both body and rejection paths.
type blockLiteralBindingOutcome struct {
	mayBind  bool
	mustBind bool
}

// immediateLambdaCallEntry separates the arm that enters a direct lambda body
// from arms rejected by block.call before entry. The runtime rejects keywords
// and a non-nil supplied block, then enforces exact lambda arity and parameter
// types.
func (c *scriptChecker) immediateLambdaCallEntry(
	block *BlockLiteral,
	call *CallExpr,
) blockLiteralCallEntryOutcome {
	if block == nil || call == nil {
		return blockLiteralCallEntryOutcome{}
	}
	if call.Block != nil {
		return blockLiteralCallEntryOutcome{mayReject: true}
	}

	outcome := blockLiteralCallEntryOutcome{}
	if call.BlockArg != nil {
		blockType, captured := c.callArgumentFacts[call.BlockArg]
		if !captured {
			blockType = c.inferExpressionTypeWithExpectation(
				call.BlockArg,
				typeExpressionExpectation(checkTypeFunction),
			)
		}
		if typeExprNeverNil(blockType) {
			return blockLiteralCallEntryOutcome{mayReject: true}
		}
		outcome.mayReject = !typeExprIsNilOnly(blockType) ||
			!c.blockArgumentConversionMustSucceed(call.BlockArg, blockType)
	}

	for _, kwarg := range call.KwArgs {
		if !c.keywordArgumentMayExpandEmpty(kwarg) {
			return blockLiteralCallEntryOutcome{mayReject: true}
		}
		if !c.keywordArgumentMustExpandEmpty(kwarg) {
			outcome.mayReject = true
		}
	}

	binding := c.immediateLambdaPositionalBindingOutcome(block, call)
	if !binding.mayBind {
		return blockLiteralCallEntryOutcome{mayReject: true}
	}
	outcome.mayEnter = true
	if !binding.mustBind {
		outcome.mayReject = true
	}
	return outcome
}

func (c *scriptChecker) immediateLambdaPositionalBindingOutcome(
	block *BlockLiteral,
	call *CallExpr,
) blockLiteralBindingOutcome {
	arity := lambdaLiteralArity(block)
	if arity < 0 {
		return blockLiteralBindingOutcome{}
	}
	if variants, exact, correlated := c.exactPositionalArgumentVariants(call, 32); exact && correlated {
		alternatives := make([]blockLiteralBindingOutcome, 0, len(variants))
		for _, arguments := range variants {
			alternatives = append(
				alternatives,
				c.blockLiteralBindingOutcome(block, arguments, true, nil),
			)
		}
		return mergeBlockLiteralBindingAlternatives(alternatives)
	}
	reachable := make([]bool, arity+1)
	reachable[0] = true

	advance := func(current []bool, argument Expression) []bool {
		next := make([]bool, arity+1)
		for position, possible := range current {
			if !possible || position == arity {
				continue
			}
			if c.lambdaLiteralParamBindingOutcome(block, position, argument).mayBind {
				next[position+1] = true
			}
		}
		return next
	}

	for _, argument := range call.Args {
		splat, dynamic := argument.(*SplatArg)
		if !dynamic {
			reachable = advance(reachable, argument)
			continue
		}
		if alternatives, exact := c.callStaticValueAlternatives(splat.Value); exact {
			next := make([]bool, arity+1)
			for _, alternative := range alternatives {
				array, ok := alternative.(*ArrayLiteral)
				if !ok {
					continue
				}
				candidate := append([]bool(nil), reachable...)
				for _, element := range array.Elements {
					candidate = advance(candidate, element)
				}
				for position, possible := range candidate {
					next[position] = next[position] || possible
				}
			}
			reachable = next
			continue
		}
		if !c.positionalArgumentExpansionMaySucceed(argument) {
			return blockLiteralBindingOutcome{}
		}
		elementType := c.positionalSplatElementType(splat)
		next := make([]bool, arity+1)
		for start, possible := range reachable {
			if !possible {
				continue
			}
			next[start] = true
			for end := start; end < arity; end++ {
				if !c.lambdaLiteralParamTypeBindingOutcome(
					block,
					end,
					elementType,
				).mayBind {
					break
				}
				next[end+1] = true
			}
		}
		reachable = next
	}
	outcome := blockLiteralBindingOutcome{mayBind: reachable[arity]}
	variants, exact, _ := c.exactPositionalArgumentVariants(call, 32)
	if !exact || len(variants) == 0 {
		return outcome
	}
	outcome.mustBind = true
	for _, arguments := range variants {
		if len(arguments) != lambdaLiteralArity(block) {
			outcome.mustBind = false
			break
		}
		for i, argument := range arguments {
			if !c.lambdaLiteralParamBindingOutcome(block, i, argument).mustBind {
				outcome.mustBind = false
				break
			}
		}
		if !outcome.mustBind {
			break
		}
	}
	return outcome
}

func (c *scriptChecker) exactPositionalArgumentVariants(
	call *CallExpr,
	limit int,
) ([][]Expression, bool, bool) {
	if call == nil || limit <= 0 {
		return nil, false, false
	}

	sourceGroups := make([]int, len(call.Args))
	for i := range sourceGroups {
		sourceGroups[i] = -1
	}
	// Repeated reads of one unchanged direct local expand the same runtime
	// array. Matching the ordered alternative nodes keeps branch identity
	// exact without relating aliases or independently rebound locals.
	var sources []checkCallSplatSource
	correlated := false
	for i, argument := range call.Args {
		splat, expanded := argument.(*SplatArg)
		if !expanded {
			continue
		}
		source, captured := c.callArgumentSplatSources[splat.Value]
		if !captured {
			continue
		}
		for group, existing := range sources {
			if sameCheckCallSplatSource(source, existing) {
				sourceGroups[i] = group
				correlated = true
				break
			}
		}
		if sourceGroups[i] < 0 {
			sourceGroups[i] = len(sources)
			sources = append(sources, source)
		}
	}

	type positionalArgumentVariant struct {
		arguments []Expression
		choices   []int
	}
	choices := make([]int, len(sources))
	for i := range choices {
		choices[i] = -1
	}
	variants := []positionalArgumentVariant{{choices: choices}}
	for argumentIndex, argument := range call.Args {
		splat, expanded := argument.(*SplatArg)
		if !expanded {
			for i := range variants {
				variants[i].arguments = append(variants[i].arguments, argument)
			}
			continue
		}
		alternatives, exact := c.callStaticValueAlternatives(splat.Value)
		if !exact || len(alternatives) == 0 {
			return nil, false, false
		}
		group := sourceGroups[argumentIndex]
		next := make([]positionalArgumentVariant, 0, len(variants)*len(alternatives))
		for _, prefix := range variants {
			firstAlternative := 0
			lastAlternative := len(alternatives)
			if group >= 0 && prefix.choices[group] >= 0 {
				firstAlternative = prefix.choices[group]
				lastAlternative = firstAlternative + 1
			}
			for offset := range lastAlternative - firstAlternative {
				alternativeIndex := firstAlternative + offset
				alternative := alternatives[alternativeIndex]
				array, ok := alternative.(*ArrayLiteral)
				if !ok {
					return nil, false, false
				}
				candidate := positionalArgumentVariant{
					arguments: append([]Expression(nil), prefix.arguments...),
					choices:   append([]int(nil), prefix.choices...),
				}
				candidate.arguments = append(candidate.arguments, array.Elements...)
				if group >= 0 && candidate.choices[group] < 0 {
					candidate.choices[group] = alternativeIndex
				}
				next = append(next, candidate)
				if len(next) > limit {
					return nil, false, false
				}
			}
		}
		variants = next
	}
	arguments := make([][]Expression, len(variants))
	for i, variant := range variants {
		arguments[i] = variant.arguments
	}
	return arguments, true, correlated
}

func (c *scriptChecker) positionalSplatElementType(splat *SplatArg) *TypeExpr {
	if splat == nil {
		return nil
	}
	inferred, captured := c.callArgumentFacts[splat]
	if !captured {
		inferred = c.inferExpressionType(splat.Value)
	}
	return splattedElementBound(inferred)
}

func (c *scriptChecker) blockArgumentConversionMustSucceed(
	expr Expression,
	inferred *TypeExpr,
) bool {
	if value, literal := staticLiteralValue(expr); literal {
		switch value.Kind() {
		case KindNil, KindSymbol, KindFunction, KindBuiltin, KindBlock:
			return true
		default:
			return false
		}
	}
	if inferred == nil {
		return false
	}
	allowed := unionTypeExprs(checkTypeNil, checkTypeSymbol, checkTypeFunction)
	return typeExprSatisfies(inferred, allowed, c.checkNamedTypeResolver())
}

func (c *scriptChecker) keywordArgumentMayExpandEmpty(kwarg KeywordArg) bool {
	if !kwarg.Splat {
		return false
	}
	values, exact := c.callArgumentStaticValues[kwarg.Value]
	if !exact {
		values, exact = c.staticValueExpressionAlternatives(kwarg.Value)
	}
	if !exact {
		return true
	}
	for _, value := range values {
		hash, ok := value.(*HashLiteral)
		if ok && (hash.ShapeType == nil || c.hashShapeStaticallyShadowed(hash)) &&
			len(hash.Pairs) == 0 {
			return true
		}
	}
	return false
}

func (c *scriptChecker) keywordArgumentMustExpandEmpty(kwarg KeywordArg) bool {
	if !kwarg.Splat {
		return false
	}
	values, exact := c.callArgumentStaticValues[kwarg.Value]
	if !exact {
		values, exact = c.staticValueExpressionAlternatives(kwarg.Value)
	}
	if !exact || len(values) == 0 {
		return false
	}
	for _, value := range values {
		hash, ok := value.(*HashLiteral)
		if !ok || hash.ShapeType != nil && !c.hashShapeStaticallyShadowed(hash) ||
			len(hash.Pairs) != 0 {
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

func callerLambdaArgumentBlocks(call *CallExpr) map[*BlockLiteral]struct{} {
	if call == nil {
		return nil
	}
	blocks := make(map[*BlockLiteral]struct{})
	record := func(expr Expression) {
		if block := lambdaLiteralBlock(expr); block != nil {
			blocks[block] = struct{}{}
		}
	}
	for _, arg := range call.Args {
		record(arg)
	}
	for _, kwarg := range call.KwArgs {
		record(kwarg.Value)
	}
	return blocks
}

func (c *scriptChecker) exactLambdaExpressionAlternatives(
	expr Expression,
) ([]Expression, bool) {
	values, exact := c.callStaticValueAlternatives(expr)
	if !exact || len(values) == 0 {
		return nil, false
	}
	lambdas := make([]Expression, 0, len(values))
	seen := make(map[*BlockLiteral]struct{}, len(values))
	for _, value := range values {
		block := lambdaLiteralBlock(value)
		if block == nil {
			return nil, false
		}
		if _, duplicate := seen[block]; duplicate {
			continue
		}
		seen[block] = struct{}{}
		lambdas = append(lambdas, value)
	}
	return lambdas, len(lambdas) > 0
}

func (c *scriptChecker) callableBlockLiteralValues(
	expr Expression,
) ([]checkBlockLiteralValue, bool) {
	switch typed := expr.(type) {
	case *Identifier:
		return c.localBlockLiteralValuesFor(typed.Name)
	case *BlockLiteral:
		if typed.Lambda {
			return []checkBlockLiteralValue{{block: typed, lambda: true}}, true
		}
	case *ConditionalExpr:
		if branch, known := staticConditionalExpressionBranch(typed); known {
			return c.callableBlockLiteralValues(branch)
		}
		left, leftExact := c.callableBlockLiteralValues(typed.Consequent)
		right, rightExact := c.callableBlockLiteralValues(typed.Alternate)
		if !leftExact || !rightExact || len(left) == 0 || len(right) == 0 {
			return nil, false
		}
		return normalizeCheckBlockLiterals(append(left, right...)), true
	case *CallExpr:
		if typed.Block == nil || typed.BlockArg != nil ||
			len(typed.Args) != 0 || len(typed.KwArgs) != 0 {
			return nil, false
		}
		target, resolved := c.resolveCallable(typed)
		if !resolved || target.fn != nil {
			return nil, false
		}
		switch target.name {
		case "proc", "Proc.new":
			return []checkBlockLiteralValue{{block: typed.Block}}, true
		case "lambda":
			if c.callTargetsCoreLambda(typed, target, resolved) {
				return []checkBlockLiteralValue{{block: typed.Block, lambda: true}}, true
			}
		}
	}
	return nil, false
}

// blockLiteralValueChoices keeps exact non-nil literal blocks from a
// conditional that may also produce nil. Ordinary callable resolution stays
// gradual for that local; Array#fill uses the non-nil subset only after its
// separate nil/value and block-form outcomes have both been modeled.
func (c *scriptChecker) blockLiteralValueChoices(
	expr Expression,
) ([]checkBlockLiteralValue, bool, bool) {
	if blocks, exact := c.callableBlockLiteralValues(expr); exact {
		return blocks, false, true
	}
	switch typed := expr.(type) {
	case *Identifier:
		blocks, exact := c.localArrayFillBlockLiteralValuesFor(typed.Name)
		if !exact {
			return nil, false, false
		}
		fact, _ := c.localValueFactFor(typed.Name)
		return blocks, fact.blockChoiceMayNil, true
	case *ConditionalExpr:
		if branch, known := staticConditionalExpressionBranch(typed); known {
			return c.blockLiteralValueChoices(branch)
		}
		branchValues := func(branch Expression) ([]checkBlockLiteralValue, bool, bool) {
			if value, exact := staticLiteralValue(branch); exact && value.Kind() == KindNil {
				return nil, true, true
			}
			return c.blockLiteralValueChoices(branch)
		}
		left, leftMayNil, leftExact := branchValues(typed.Consequent)
		right, rightMayNil, rightExact := branchValues(typed.Alternate)
		if !leftExact || !rightExact || len(left)+len(right) == 0 {
			return nil, false, false
		}
		return normalizeCheckBlockLiterals(append(left, right...)),
			leftMayNil || rightMayNil,
			true
	}
	return nil, false, false
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
func (c *scriptChecker) applyLambdaBlockNamespaceMutations(block *BlockLiteral) bool {
	if block == nil {
		return false
	}
	scan := c.newNamespaceMutationScan()
	scan.scanLambdaBlock(block)
	for member := range scan.out {
		c.recordRuntimeNamespaceMember(member)
	}
	return scan.invokedUnknownCallable
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

func (c *scriptChecker) withCapturedDestructureArgumentFact(
	expr Expression,
	fact capturedDestructureValueFact,
	walk func(),
) {
	c.withCapturedDestructureArgumentFacts(map[Expression]capturedDestructureValueFact{
		expr: fact,
	}, walk)
}

func (c *scriptChecker) withEvaluatedDestructureArgumentFacts(expressions []Expression, walk func()) {
	facts := make(map[Expression]capturedDestructureValueFact, len(expressions))
	for _, expr := range expressions {
		if fact, captured := c.evaluatedDestructureFacts[expr]; captured {
			facts[expr] = fact
		}
	}
	c.withCapturedDestructureArgumentFacts(facts, walk)
}

func (c *scriptChecker) withEvaluatedAssignmentSetterArgumentFacts(
	target Expression,
	value Expression,
	walk func(),
) {
	expressions := []Expression{value}
	if indexed, ok := target.(*IndexExpr); ok {
		expressions = append(append([]Expression(nil), indexed.Indices...), value)
	}
	c.withEvaluatedDestructureArgumentFacts(expressions, walk)
}

func (c *scriptChecker) withCapturedDestructureArgumentFacts(
	facts map[Expression]capturedDestructureValueFact,
	walk func(),
) {
	previousFacts := c.callArgumentFacts
	previousClassValues := c.callArgumentClassValues
	previousCallables := c.callArgumentCallables
	previousSelfBindings := c.callArgumentSelfBindings
	previousStaticValues := c.callArgumentStaticValues
	previousStaticChoices := c.callArgumentStaticChoices
	c.callArgumentFacts = make(map[Expression]*TypeExpr, len(previousFacts)+len(facts))
	c.callArgumentClassValues = make(map[Expression][]string, len(previousClassValues)+len(facts))
	c.callArgumentCallables = make(map[Expression][]*ScriptFunction, len(previousCallables)+len(facts))
	c.callArgumentSelfBindings = make(
		map[Expression]checkCallableSelfBinding,
		len(previousSelfBindings),
	)
	c.callArgumentStaticValues = make(map[Expression][]Expression, len(previousStaticValues)+len(facts))
	c.callArgumentStaticChoices = make(
		map[Expression]checkStaticChoiceFact,
		len(previousStaticChoices),
	)
	for expr, fact := range previousFacts {
		c.callArgumentFacts[expr] = fact
	}
	for expr, classNames := range previousClassValues {
		c.callArgumentClassValues[expr] = classNames
	}
	for expr, callables := range previousCallables {
		c.callArgumentCallables[expr] = callables
	}
	for expr, binding := range previousSelfBindings {
		c.callArgumentSelfBindings[expr] = binding
	}
	for expr, values := range previousStaticValues {
		c.callArgumentStaticValues[expr] = values
	}
	for expr, choice := range previousStaticChoices {
		c.callArgumentStaticChoices[expr] = cloneCheckStaticChoiceFact(choice)
	}
	for expr, fact := range facts {
		c.callArgumentFacts[expr] = fact.assigned
		c.callArgumentClassValues[expr] = append([]string(nil), fact.classNames...)
		c.callArgumentCallables[expr] = append([]*ScriptFunction(nil), fact.callables...)
		c.callArgumentStaticValues[expr] = append([]Expression(nil), fact.staticVals...)
		if capturedDestructureStaticChoiceExact(fact) {
			c.callArgumentStaticChoices[expr] = cloneCheckStaticChoiceFact(fact.staticChoice)
		}
	}
	defer func() {
		c.callArgumentFacts = previousFacts
		c.callArgumentClassValues = previousClassValues
		c.callArgumentCallables = previousCallables
		c.callArgumentSelfBindings = previousSelfBindings
		c.callArgumentStaticValues = previousStaticValues
		c.callArgumentStaticChoices = previousStaticChoices
	}()
	walk()
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
				clauseBindings := make(map[string]struct{})
				collectLocalBindings(clause.Body, clauseBindings)
				delete(clauseBindings, clause.Binding)
				for name := range clauseBindings {
					out[name] = struct{}{}
				}
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

type callableSelfBindings struct {
	functions map[string][]*ScriptFunction
	ambiguous map[string]struct{}
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
	// The effect receiver stays fixed while selfClass follows nested callees.
	effectSelfClass        *ClassDef
	effectSelfClassContext bool
	// A nil class records a parameter that shadows a class name without
	// proving one exact instance dispatch.
	nominalReceivers         map[string]*ClassDef
	callableParams           map[string][]*ScriptFunction
	callableLambdas          map[string][]Expression
	callableSelfParams       map[string][]*ScriptFunction
	ambiguousSelfCallables   map[string]struct{}
	unboundParams            map[string]struct{}
	invokedLambdas           map[*BlockLiteral]struct{}
	invokedSelfFunctions     map[*ScriptFunction]struct{}
	invokedUnknownCallable   bool
	directIvarWrites         map[*ScriptFunction]map[string]struct{}
	unknownDirectIvarEffects map[*ScriptFunction]struct{}
	currentFunction          *ScriptFunction
	failureScopes            [][]checkScopeState
}

func (c *scriptChecker) newNamespaceMutationScan() *namespaceMutationScan {
	c.prepareSelfScopeFunctions()
	scan := &namespaceMutationScan{
		checker:                  c,
		out:                      make(map[string]struct{}),
		functions:                c.script.functions,
		classes:                  c.script.classes,
		active:                   make(map[*ScriptFunction]struct{}),
		activeDefaults:           make(map[*ScriptFunction]map[int]struct{}),
		methodClasses:            c.selfScopeFnClasses,
		classMethodFns:           c.selfScopeClassFns,
		selfClass:                c.selfClass,
		selfClassContext:         c.selfClassContext,
		effectSelfClass:          c.selfClass,
		effectSelfClassContext:   c.selfClassContext,
		invokedLambdas:           make(map[*BlockLiteral]struct{}),
		invokedSelfFunctions:     make(map[*ScriptFunction]struct{}),
		directIvarWrites:         make(map[*ScriptFunction]map[string]struct{}),
		unknownDirectIvarEffects: make(map[*ScriptFunction]struct{}),
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

func (s *namespaceMutationScan) recordDirectIvarWrite(name string) {
	if s.currentFunction == nil || name == "" {
		return
	}
	writes := s.directIvarWrites[s.currentFunction]
	if writes == nil {
		writes = make(map[string]struct{})
		s.directIvarWrites[s.currentFunction] = writes
	}
	writes[name] = struct{}{}
}

func (s *namespaceMutationScan) recordDirectIvarTarget(target Expression) {
	switch typed := target.(type) {
	case *IvarExpr:
		s.recordDirectIvarWrite(typed.Name)
	case *DestructureTarget:
		for _, element := range typed.Elements {
			s.recordDirectIvarTarget(element.Target)
		}
	}
}

func (s *namespaceMutationScan) markUnknownDirectIvarEffects() {
	if s.currentFunction != nil {
		s.unknownDirectIvarEffects[s.currentFunction] = struct{}{}
	}
}

func (s *namespaceMutationScan) withSuspendedDirectIvarAttribution(walk func()) {
	previous := s.currentFunction
	s.currentFunction = nil
	defer func() { s.currentFunction = previous }()
	walk()
}

func (s *namespaceMutationScan) callableParamSelfBindings(
	arguments map[string][]Expression,
) callableSelfBindings {
	var bindings callableSelfBindings
	for name, expressions := range arguments {
		for _, expression := range expressions {
			if ident, ok := expression.(*Identifier); ok {
				if _, ambiguous := s.ambiguousSelfCallables[ident.Name]; ambiguous {
					if bindings.ambiguous == nil {
						bindings.ambiguous = make(map[string]struct{})
					}
					bindings.ambiguous[name] = struct{}{}
				}
			}
			if functions, exact := s.currentSelfCallableFunctions(expression); exact {
				if bindings.functions == nil {
					bindings.functions = make(map[string][]*ScriptFunction)
				}
				bindings.functions[name] = append(bindings.functions[name], functions...)
				continue
			}
			functions, exact := s.checker.callableExpressionFunctions(expression)
			if !exact {
				continue
			}
			for _, fn := range functions {
				if s.functionMayRunOnEffectSelf(fn) {
					if bindings.ambiguous == nil {
						bindings.ambiguous = make(map[string]struct{})
					}
					bindings.ambiguous[name] = struct{}{}
					break
				}
			}
		}
	}
	for name, functions := range bindings.functions {
		bindings.functions[name] = normalizeCheckCallables(functions)
	}
	return bindings
}

func capturedCallableParamSelfBindings(
	bindings callableSelfBindings,
	facts map[string]checkReachableParamFact,
) callableSelfBindings {
	// Evaluation-time facts replace, rather than union with, the raw
	// expression scan: a later argument may have rebound the expression's
	// source local before dispatch starts.
	for name, fact := range facts {
		if !fact.selfCallablesCaptured {
			continue
		}
		if len(fact.selfCallables) == 0 {
			delete(bindings.functions, name)
		} else {
			if bindings.functions == nil {
				bindings.functions = make(map[string][]*ScriptFunction)
			}
			bindings.functions[name] = normalizeCheckCallables(
				append([]*ScriptFunction(nil), fact.selfCallables...),
			)
		}
		if !fact.selfCallableAmbiguous {
			delete(bindings.ambiguous, name)
			continue
		}
		if bindings.ambiguous == nil {
			bindings.ambiguous = make(map[string]struct{})
		}
		bindings.ambiguous[name] = struct{}{}
	}
	return bindings
}

func (s *namespaceMutationScan) currentSelfCallableFunctions(
	expr Expression,
) ([]*ScriptFunction, bool) {
	if ident, ok := expr.(*Identifier); ok {
		if functions, bound := s.callableSelfParams[ident.Name]; bound {
			return append([]*ScriptFunction(nil), functions...), len(functions) > 0
		}
	}
	if call, ok := expr.(*CallExpr); ok {
		if identity, bare := bareIdentifierCallableValue(call); bare {
			return s.currentSelfCallableFunctions(identity)
		}
	}
	if s.effectSelfClass == nil || s.effectSelfClassContext ||
		!sameScriptClass(s.selfClass, s.effectSelfClass) || s.selfClassContext {
		return nil, false
	}
	switch typed := expr.(type) {
	case *Identifier:
		functions, exact := s.checker.callableExpressionFunctions(typed)
		fn := s.selfClass.Methods[typed.Name]
		if !exact || fn == nil || fn.Accessor == functionAccessorGetter ||
			len(functions) == 0 {
			return nil, false
		}
		for _, candidate := range functions {
			if candidate != fn {
				return nil, false
			}
		}
		return functions, true
	case *MemberExpr:
		if !expressionIsSelf(typed.Object) {
			return nil, false
		}
		fn := s.selfClass.Methods[typed.Property]
		if fn == nil || fn.Accessor == functionAccessorGetter {
			return nil, false
		}
		return []*ScriptFunction{fn}, true
	default:
		return nil, false
	}
}

func (s *namespaceMutationScan) exactCallableSelfBinding(
	expr Expression,
) (checkCallableSelfBinding, bool) {
	if functions, exact := s.currentSelfCallableFunctions(expr); exact {
		return checkCallableSelfBinding{
			functions: append([]*ScriptFunction(nil), functions...),
		}, true
	}
	functions, exact := s.checker.callableExpressionFunctions(expr)
	if !exact || len(functions) == 0 {
		if lambdaLiteralBlock(expr) != nil {
			return checkCallableSelfBinding{}, true
		}
		return checkCallableSelfBinding{}, false
	}
	for _, fn := range functions {
		if s.functionMayRunOnEffectSelf(fn) {
			return checkCallableSelfBinding{ambiguous: true}, true
		}
	}
	return checkCallableSelfBinding{}, true
}

func (s *namespaceMutationScan) functionMayRunOnEffectSelf(fn *ScriptFunction) bool {
	if fn == nil || s.effectSelfClass == nil || s.effectSelfClassContext {
		return false
	}
	classDef := s.methodClasses[fn]
	_, classMethod := s.classMethodFns[fn]
	return sameScriptClass(classDef, s.effectSelfClass) && !classMethod
}

// functionReference unions in the writes of an owned top-level function or
// implicit-self method the scanned body mentions. Any mention counts — callee,
// bare auto-invoke, or escaping value — since a stored function can run later;
// a shadowed name only over-invalidates, which is the sound direction.
func (s *namespaceMutationScan) functionReference(name string) {
	if fns, bound := s.callableParams[name]; bound {
		if _, ambiguous := s.ambiguousSelfCallables[name]; ambiguous {
			s.invokedUnknownCallable = true
		}
		for _, fn := range fns {
			if len(fn.Params) == 0 {
				if functionInSet(fn, s.callableSelfParams[name]) {
					s.invokedSelfFunctions[fn] = struct{}{}
				}
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
			s.selfReference(name)
		}
	}
}

func (s *namespaceMutationScan) functionReferenceWithCall(name string, call *CallExpr) {
	if s.scanExactLambdaCall(
		&Identifier{Name: name, Position: call.Pos()},
		call,
	) {
		return
	}
	if fns, bound := s.callableParams[name]; bound {
		if _, ambiguous := s.ambiguousSelfCallables[name]; ambiguous {
			s.invokedUnknownCallable = true
		}
		for _, fn := range fns {
			if functionInSet(fn, s.callableSelfParams[name]) {
				s.invokedSelfFunctions[fn] = struct{}{}
			}
			s.scanFunctionCall(fn, call, staticCallable{name: fn.Name + ".call", fn: fn})
		}
		return
	}
	if lambdas, bound := s.callableLambdas[name]; bound {
		resolved := false
		for _, lambda := range lambdas {
			if block := callableArgumentBlock(lambda); block != nil &&
				s.checker.immediateLambdaCallEntry(block, call).mayEnter {
				resolved = true
				s.invokedLambdas[block] = struct{}{}
				s.scanLambdaBlock(block)
			}
		}
		if !resolved && typeExprMayIncludeCallable(
			s.checker.inferExpressionType(&Identifier{Name: name}),
		) {
			s.invokedUnknownCallable = true
		}
		return
	}
	if fn, ok := s.functions[name]; ok {
		s.scanFunctionCall(fn, call, staticCallable{name: name, fn: fn})
		return
	}
	s.selfCallReference(name, call)
}

func callableArgumentBlock(expr Expression) *BlockLiteral {
	if block, ok := expr.(*BlockLiteral); ok {
		return block
	}
	return lambdaLiteralBlock(expr)
}

func (s *namespaceMutationScan) scanExactLambdaCall(
	expr Expression,
	call *CallExpr,
) bool {
	lambdas, exact := s.checker.exactLambdaExpressionAlternatives(expr)
	if !exact {
		return false
	}
	for _, lambda := range lambdas {
		block := lambdaLiteralBlock(lambda)
		if block == nil || !s.checker.immediateLambdaCallEntry(block, call).mayEnter {
			continue
		}
		s.invokedLambdas[block] = struct{}{}
		s.scanLambdaBlock(block)
	}
	return true
}

func functionInSet(fn *ScriptFunction, functions []*ScriptFunction) bool {
	for _, candidate := range functions {
		if candidate == fn {
			return true
		}
	}
	return false
}

func (s *namespaceMutationScan) selfReference(name string) {
	s.selfCallReference(name, &CallExpr{})
}

func (s *namespaceMutationScan) selfCallReference(name string, call *CallExpr) {
	if s.selfClass == nil {
		return
	}
	s.markUnknownDirectIvarEffects()
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
	defaults := make([]int, 0, len(fn.Params))
	for i, param := range fn.Params {
		if param.DefaultVal != nil {
			defaults = append(defaults, i)
		}
	}
	s.withFunctionContext(fn, nil, nil, callableSelfBindings{}, func() {
		if !s.scanFunctionBindings(
			fn,
			defaults,
			len(fn.Params),
			nil,
			nil,
			callableSelfBindings{},
			false,
		) {
			return
		}
		if fn.Accessor == functionAccessorSetter {
			s.recordDirectIvarWrite(fn.AccessorName)
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
	if !plan.bindingStarts {
		return
	}
	facts := s.checker.reachableCallParamFacts(call, target)
	lambdas := callableParamLambdaArguments(call, target, facts)
	selfBindings := s.callableParamSelfBindings(
		callableParamArgumentExpressions(call, target, facts, false, false),
	)
	selfBindings = capturedCallableParamSelfBindings(selfBindings, facts)
	if _, active := s.active[fn]; active {
		s.withFunctionContext(fn, facts, lambdas, selfBindings, func() {
			s.scanFunctionBindings(
				fn,
				plan.defaultParams,
				plan.boundParamCount,
				facts,
				lambdas,
				selfBindings,
				plan.exactBindings,
			)
		})
		return
	}
	s.active[fn] = struct{}{}
	defer delete(s.active, fn)
	s.withFunctionContext(fn, facts, lambdas, selfBindings, func() {
		if !s.scanFunctionBindings(
			fn,
			plan.defaultParams,
			plan.boundParamCount,
			facts,
			lambdas,
			selfBindings,
			plan.exactBindings,
		) {
			return
		}
		if plan.bodyMayEnter {
			if fn.Accessor == functionAccessorSetter {
				s.recordDirectIvarWrite(fn.AccessorName)
			}
			s.statements(fn.Body)
		}
	})
}

func (s *namespaceMutationScan) scanFunctionBindings(
	fn *ScriptFunction,
	defaultParams []int,
	boundParamCount int,
	facts map[string]checkReachableParamFact,
	lambdas map[string][]Expression,
	selfBindings callableSelfBindings,
	gateDefaults bool,
) bool {
	defaults := make(map[int]struct{}, len(defaultParams))
	for _, index := range defaultParams {
		defaults[index] = struct{}{}
	}
	for i, param := range fn.Params {
		_, defaultPossible := defaults[i]
		if defaultPossible {
			beforeDefault := s.checker.snapshotScopeState()
			completed := s.scanFunctionDefaults(fn, []int{i})
			if gateDefaults {
				if !completed || !s.functionParamDefaultMayBind(fn, param) {
					return false
				}
			} else if completed {
				afterDefault := s.checker.snapshotScopeState()
				s.checker.mergeScopeStates(
					beforeDefault,
					[]checkScopeState{beforeDefault, afterDefault},
				)
			} else {
				s.checker.restoreScopeState(beforeDefault)
			}
		}
		if i >= boundParamCount {
			return false
		}
		s.checker.withSuppressedWarnings(func() {
			s.checker.checkIvarParamBinding("", fn, param)
		})
		s.checker.recordParamBinding(param)
		s.checker.applyReachableParamFact(param)
		s.markFunctionParamBound(param)
		s.bindCallableParamFact(param, facts, lambdas, selfBindings, defaultPossible)
		s.bindNominalReceiverParam(param)
		if param.IsIvar && s.methodClasses[fn] != nil {
			if _, classMethod := s.classMethodFns[fn]; !classMethod {
				s.recordDirectIvarWrite(param.Name)
			}
		}
	}
	return true
}

func (s *namespaceMutationScan) functionParamDefaultMayBind(fn *ScriptFunction, param Param) bool {
	fact := s.checker.defaultExpressionBindingFact(param)
	if !s.checker.defaultBindingFactMayBindType(fact, param.Type) {
		return false
	}
	ty := s.checker.ivarParamContract(fn, param)
	return ty == nil ||
		s.checker.ivarParamBindingFactMayStore(fact, param, ty)
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
		param := fn.Params[paramIndex]
		completed := s.callArgumentExpression(
			param.DefaultVal,
			bindingDefaultExpectation(param),
		)
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
	selfBindings callableSelfBindings,
	walk func(),
) {
	// A called function runs in an isolated local scope. The caller-side
	// expression walk already captures the state before and after dispatch;
	// snapshots taken while walking the callee would otherwise be merged as
	// though its unrelated local frame belonged to the caller's rescue.
	previousFunction := s.currentFunction
	s.currentFunction = fn
	defer func() { s.currentFunction = previousFunction }()
	previousFailureScopes := s.failureScopes
	s.failureScopes = nil
	defer func() { s.failureScopes = previousFailureScopes }()
	restoreResolution := s.checker.withClassConstantProofResolution(fn, nil)
	defer restoreResolution()
	previousScopes := s.checker.scopes
	restoreInference := s.checker.withIsolatedLocalInference()
	s.checker.scopes = nil
	popScope := s.checker.pushScope(make(map[string]struct{}))
	defer func() {
		popScope()
		restoreInference()
		s.checker.scopes = previousScopes
	}()
	previousFacts := s.checker.reachableParamFacts
	s.checker.reachableParamFacts = facts
	defer func() { s.checker.reachableParamFacts = previousFacts }()
	previousUnbound := s.unboundParams
	previousPending := s.checker.pendingBindingParams
	s.unboundParams = functionParamBindingNames(fn)
	s.checker.pendingBindingParams = s.unboundParams
	defer func() {
		s.unboundParams = previousUnbound
		s.checker.pendingBindingParams = previousPending
	}()
	s.withFunctionSelf(fn, func() {
		s.checker.seedInstanceIvarFacts(fn)
		s.checker.linkReachableParamAliases(fn.Params)
		s.withCallableParamFacts(fn.Params, facts, lambdas, selfBindings, func() {
			s.withNominalReceiverParamShadows(fn.Params, walk)
		})
	})
}

func (s *namespaceMutationScan) withCallableParamFacts(
	params []Param,
	facts map[string]checkReachableParamFact,
	lambdas map[string][]Expression,
	selfBindings callableSelfBindings,
	walk func(),
) {
	previous := s.callableParams
	previousLambdas := s.callableLambdas
	previousSelf := s.callableSelfParams
	previousAmbiguous := s.ambiguousSelfCallables
	s.callableParams = make(map[string][]*ScriptFunction, len(facts))
	s.callableLambdas = make(map[string][]Expression, len(params)+len(lambdas))
	s.callableSelfParams = make(map[string][]*ScriptFunction, len(selfBindings.functions))
	s.ambiguousSelfCallables = make(map[string]struct{}, len(selfBindings.ambiguous))
	defer func() {
		s.callableParams = previous
		s.callableLambdas = previousLambdas
		s.callableSelfParams = previousSelf
		s.ambiguousSelfCallables = previousAmbiguous
	}()
	walk()
}

func (s *namespaceMutationScan) bindCallableParamFact(
	param Param,
	facts map[string]checkReachableParamFact,
	lambdas map[string][]Expression,
	selfBindings callableSelfBindings,
	defaultPossible bool,
) {
	if param.Name == "" {
		return
	}
	delete(s.callableParams, param.Name)
	delete(s.callableSelfParams, param.Name)
	delete(s.ambiguousSelfCallables, param.Name)
	var candidates []*ScriptFunction
	if fns, exact := s.checker.localCallableValuesFor(param.Name); exact && len(fns) > 0 {
		candidates = append(candidates, fns...)
	}
	if fact, ok := facts[param.Name]; ok && len(fact.callables) > 0 {
		candidates = append(candidates, fact.callables...)
	}
	if defaultPossible {
		if fns, exact := s.checker.callableExpressionFunctions(param.DefaultVal); exact {
			candidates = append(candidates, fns...)
		}
	}
	candidates = append(candidates, selfBindings.functions[param.Name]...)
	if len(candidates) > 0 {
		seen := make(map[*ScriptFunction]struct{}, len(candidates))
		unique := candidates[:0]
		for _, fn := range candidates {
			if fn == nil {
				continue
			}
			if _, exists := seen[fn]; exists {
				continue
			}
			seen[fn] = struct{}{}
			unique = append(unique, fn)
		}
		if len(unique) > 0 {
			s.callableParams[param.Name] = unique
		}
	}
	selfFunctions := append([]*ScriptFunction(nil), selfBindings.functions[param.Name]...)
	defaultAmbiguous := false
	if defaultPossible {
		defaultBindings := s.callableParamSelfBindings(map[string][]Expression{
			param.Name: {param.DefaultVal},
		})
		selfFunctions = append(selfFunctions, defaultBindings.functions[param.Name]...)
		_, defaultAmbiguous = defaultBindings.ambiguous[param.Name]
	}
	if len(selfFunctions) > 0 {
		s.callableSelfParams[param.Name] = normalizeCheckCallables(selfFunctions)
	}
	if _, ambiguous := selfBindings.ambiguous[param.Name]; ambiguous || defaultAmbiguous {
		s.ambiguousSelfCallables[param.Name] = struct{}{}
	}
	boundLambdas := append([]Expression(nil), lambdas[param.Name]...)
	if defaultPossible && lambdaLiteralBlock(param.DefaultVal) != nil {
		boundLambdas = append(boundLambdas, param.DefaultVal)
	}
	s.callableLambdas[param.Name] = boundLambdas
}

func (s *namespaceMutationScan) markFunctionParamBound(param Param) {
	removeFunctionParamBindingNames(s.unboundParams, param)
}

func (s *namespaceMutationScan) withCallableParamShadows(params []Param, walk func()) {
	previous := s.callableParams
	previousLambdas := s.callableLambdas
	previousSelf := s.callableSelfParams
	previousAmbiguous := s.ambiguousSelfCallables
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
	s.callableSelfParams = make(map[string][]*ScriptFunction, len(previousSelf))
	for name, functions := range previousSelf {
		if _, shadow := shadowed[name]; !shadow {
			s.callableSelfParams[name] = functions
		}
	}
	s.ambiguousSelfCallables = make(map[string]struct{}, len(previousAmbiguous))
	for name := range previousAmbiguous {
		if _, shadow := shadowed[name]; !shadow {
			s.ambiguousSelfCallables[name] = struct{}{}
		}
	}
	defer func() {
		s.callableParams = previous
		s.callableLambdas = previousLambdas
		s.callableSelfParams = previousSelf
		s.ambiguousSelfCallables = previousAmbiguous
	}()
	walk()
}

func (s *namespaceMutationScan) scanLambdaBlock(block *BlockLiteral) {
	if block == nil {
		return
	}
	previousFunction := s.currentFunction
	s.currentFunction = nil
	defer func() { s.currentFunction = previousFunction }()
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

func (s *namespaceMutationScan) withNominalReceiverParamShadows(params []Param, walk func()) {
	previous := s.nominalReceivers
	s.nominalReceivers = make(map[string]*ClassDef, len(params))
	defer func() { s.nominalReceivers = previous }()
	walk()
}

func (s *namespaceMutationScan) bindNominalReceiverParam(param Param) {
	if param.Name == "" {
		return
	}
	s.nominalReceivers[param.Name] = nil
	if param.Kind != ParamNormal && param.Kind != ParamKeyword {
		return
	}
	if classDef := s.nominalReceiverClass(param.Type); classDef != nil {
		s.nominalReceivers[param.Name] = classDef
	}
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

func (s *namespaceMutationScan) captureFailureScope() {
	if len(s.failureScopes) == 0 {
		return
	}
	state := s.checker.snapshotScopeState()
	for i := range s.failureScopes {
		s.failureScopes[i] = append(s.failureScopes[i], state)
	}
}

func (s *namespaceMutationScan) failureScopeCheckpoint() []int {
	checkpoint := make([]int, len(s.failureScopes))
	for i := range s.failureScopes {
		checkpoint[i] = len(s.failureScopes[i])
	}
	return checkpoint
}

func (s *namespaceMutationScan) restoreFailureScopeCheckpoint(checkpoint []int) {
	for i, length := range checkpoint {
		if i >= len(s.failureScopes) || length > len(s.failureScopes[i]) {
			continue
		}
		s.failureScopes[i] = s.failureScopes[i][:length]
	}
}

func (s *namespaceMutationScan) collectFailureScopes(walk func() bool) (bool, []checkScopeState) {
	s.failureScopes = append(s.failureScopes, nil)
	index := len(s.failureScopes) - 1
	completed := walk()
	states := s.failureScopes[index]
	s.failureScopes = s.failureScopes[:index]
	return completed, states
}

func (s *namespaceMutationScan) statement(stmt Statement) bool {
	failureCheckpoint := s.failureScopeCheckpoint()
	captureBoundary := true
	s.captureFailureScope()
	defer func() {
		if captureBoundary {
			s.captureFailureScope()
		}
	}()
	switch typed := stmt.(type) {
	case nil:
		return true
	case *AssignStmt:
		destructure, preciseDestructure := typed.Target.(*DestructureTarget)
		preciseDestructure = preciseDestructure && typed.Operator == ""
		plainIvar, preciseIvar := typed.Target.(*IvarExpr)
		preciseIvar = preciseIvar && typed.Operator == ""
		if preciseDestructure || preciseIvar {
			captureBoundary = false
			s.restoreFailureScopeCheckpoint(failureCheckpoint)
		}
		previousEvaluatedFacts := s.checker.evaluatedDestructureFacts
		s.checker.evaluatedDestructureFacts = make(map[Expression]capturedDestructureValueFact)
		defer func() { s.checker.evaluatedDestructureFacts = previousEvaluatedFacts }()
		assignedValue := typed.Value
		setterReachable := true
		var logicalTargetFact *logicalAssignmentTargetFact
		var setterReceiver checkAssignmentReceiverCapture
		var logicalBaseScopeState checkScopeState
		logicalUnknown := false
		switch typed.Operator {
		case "":
			rhsFailureCheckpoint := s.failureScopeCheckpoint()
			expectation := s.checker.assignmentValueExpectation(typed.Target, typed.Value)
			if !s.callArgumentExpression(typed.Value, expectation) {
				return false
			}
			if (preciseDestructure || preciseIvar) && expressionProvenNonRaising(typed.Value) {
				s.restoreFailureScopeCheckpoint(rhsFailureCheckpoint)
			}
			s.checker.captureEvaluatedDestructureFactWithExpectation(typed.Value, expectation)
			if preciseDestructure {
				return s.replayDestructureAssignment(destructure, typed.Value)
			}
			if preciseIvar {
				setterCompletes := s.checker.assignmentSetterMayComplete(plainIvar, typed.Value)
				if !s.checker.ivarWriteProvablyCompletes(plainIvar.Name, typed.Value) {
					s.captureFailureScope()
				}
				s.checker.withSuppressedWarnings(func() {
					s.checker.inferAssignStatementTypes("", typed, nil, nil)
				})
				if setterCompletes {
					s.recordDirectIvarWrite(plainIvar.Name)
				}
				return setterCompletes
			}
			var targetCompleted bool
			targetCompleted, setterReceiver = s.checker.withAssignmentReceiverCapture(
				typed.Target,
				func() bool { return s.assignmentTarget(typed.Target) },
			)
			if !targetCompleted {
				return false
			}
		case tokenOrAssign, tokenAndAssign:
			var assignmentReceiverFact *TypeExpr
			if member, ok := typed.Target.(*MemberExpr); ok {
				if ident, ok := member.Object.(*Identifier); ok {
					assignmentReceiverFact = s.checker.localTypeFor(ident.Name)
				}
			}
			var targetCompleted bool
			targetCompleted, setterReceiver = s.checker.withAssignmentReceiverCapture(
				typed.Target,
				func() bool {
					if _, ident := typed.Target.(*Identifier); ident {
						return s.expressionWithAuto(typed.Target, false)
					}
					return s.expression(typed.Target)
				},
			)
			if !targetCompleted {
				return false
			}
			truthy, known := false, false
			if member, ok := typed.Target.(*MemberExpr); ok && assignmentReceiverFact != nil {
				truthy, known = s.checker.hashLikeMemberGetterTruthiness(
					member,
					assignmentReceiverFact,
				)
			}
			if !known {
				truthy, known = s.logicalAssignmentTargetTruthiness(typed.Target)
			}
			rhsReachable := !known ||
				(typed.Operator == tokenOrAssign && !truthy) ||
				(typed.Operator == tokenAndAssign && truthy)
			logicalTargetFact = &logicalAssignmentTargetFact{
				rhsReachable: rhsReachable,
				known:        known,
			}
			switch target := typed.Target.(type) {
			case *Identifier:
				logicalTargetFact.current = s.checker.localTypeFor(target.Name)
				if typed.Operator == tokenOrAssign && !known {
					logicalTargetFact.priorAliasTransfer = s.checker.captureContainerAliasTransfer(target)
				}
			case *IvarExpr:
				logicalTargetFact.current = s.checker.localTypeFor(ivarFactKey(target.Name))
			}
			if ivar, ok := typed.Target.(*IvarExpr); ok {
				// Ivar reads themselves cannot fail. Replace the generic
				// statement boundary with the two runtime logical arms so rescue
				// receives only failures from the writing arm and ordinary flow
				// receives only successful-write or skipped states.
				captureBoundary = false
				s.restoreFailureScopeCheckpoint(failureCheckpoint)
				baseScopeState := s.checker.snapshotScopeState()
				if !rhsReachable {
					return true
				}
				if !known {
					s.checker.narrowLogicalIvarFact(
						ivar.Name,
						typed.Operator == tokenAndAssign,
					)
				}
				rhsFailureCheckpoint := s.failureScopeCheckpoint()
				expectation := s.checker.assignmentValueExpectation(typed.Target, typed.Value)
				if !s.callArgumentExpression(typed.Value, expectation) {
					if !known {
						s.checker.restoreScopeState(baseScopeState)
						s.checker.narrowLogicalIvarFact(
							ivar.Name,
							typed.Operator == tokenOrAssign,
						)
					}
					return !known
				}
				if expressionProvenNonRaising(typed.Value) {
					s.restoreFailureScopeCheckpoint(rhsFailureCheckpoint)
				}
				setterCompletes := s.checker.assignmentSetterMayComplete(typed.Target, typed.Value)
				if !s.checker.ivarWriteProvablyCompletes(ivar.Name, typed.Value) {
					s.captureFailureScope()
				}
				if !setterCompletes {
					if !known {
						s.checker.restoreScopeState(baseScopeState)
						s.checker.narrowLogicalIvarFact(
							ivar.Name,
							typed.Operator == tokenOrAssign,
						)
						return true
					}
					return false
				}
				s.recordDirectIvarWrite(ivar.Name)
				s.checker.withSuppressedWarnings(func() {
					s.checker.commitLogicalIvarWritingArm(
						"",
						typed,
						ivar,
						logicalTargetFact.current,
					)
				})
				if !known {
					writtenScopeState := s.checker.snapshotScopeState()
					s.checker.restoreScopeState(baseScopeState)
					s.checker.narrowLogicalIvarFact(
						ivar.Name,
						typed.Operator == tokenOrAssign,
					)
					skippedScopeState := s.checker.snapshotScopeState()
					s.checker.mergeScopeStates(
						baseScopeState,
						[]checkScopeState{skippedScopeState, writtenScopeState},
					)
				}
				return true
			}
			if !rhsReachable {
				setterReachable = false
				break
			}
			if !known {
				logicalBaseScopeState = s.checker.snapshotScopeState()
				logicalUnknown = true
			}
			expectation := s.checker.assignmentValueExpectation(typed.Target, typed.Value)
			if !s.callArgumentExpression(typed.Value, expectation) {
				if logicalUnknown {
					s.checker.restoreScopeState(logicalBaseScopeState)
					return true
				}
				return false
			}
			s.checker.captureEvaluatedDestructureFactWithExpectation(typed.Value, expectation)
		default:
			var targetCompleted bool
			targetCompleted, setterReceiver = s.checker.withAssignmentReceiverCapture(
				typed.Target,
				func() bool { return s.expression(typed.Target) },
			)
			if !targetCompleted {
				return false
			}
			compoundIvar, preciseCompoundIvar := typed.Target.(*IvarExpr)
			if preciseCompoundIvar {
				captureBoundary = false
				s.restoreFailureScopeCheckpoint(failureCheckpoint)
			}
			operatorType := s.checker.inferExpressionType(typed.Target)
			s.checker.pinExpressionFact(typed.Target, operatorType)
			rhsFailureCheckpoint := s.failureScopeCheckpoint()
			if !s.expression(typed.Value) {
				return false
			}
			s.checker.captureEvaluatedDestructureFact(typed.Value)
			if preciseCompoundIvar && expressionProvenNonRaising(typed.Value) {
				s.restoreFailureScopeCheckpoint(rhsFailureCheckpoint)
			}
			operatorValue := &BinaryExpr{
				Left:     typed.Target,
				Operator: typed.Operator,
				Right:    typed.Value,
				Position: typed.Pos(),
			}
			for _, method := range binaryDispatchMethodNames(operatorValue.Operator) {
				s.callCallee(binaryDispatchCall(operatorValue, method))
			}
			if preciseCompoundIvar && !s.checker.binaryExpressionProvablyCompletes(operatorValue) {
				s.captureFailureScope()
			}
			if !s.checker.binaryExpressionMayComplete(operatorValue) {
				return false
			}
			s.checker.captureEvaluatedDestructureFact(operatorValue)
			if preciseCompoundIvar {
				setterCompletes := s.checker.assignmentSetterMayComplete(compoundIvar, operatorValue)
				if !s.checker.ivarWriteProvablyCompletes(compoundIvar.Name, operatorValue) {
					s.captureFailureScope()
				}
				s.checker.withSuppressedWarnings(func() {
					s.checker.inferAssignStatementTypes("", typed, nil, nil)
				})
				if setterCompletes {
					s.recordDirectIvarWrite(compoundIvar.Name)
				}
				return setterCompletes
			}
			assignedValue = operatorValue
		}
		_, memberAssignment := typed.Target.(*MemberExpr)
		_, indexAssignment := typed.Target.(*IndexExpr)
		setterAssignment := memberAssignment || indexAssignment
		setterAssignmentCompletes := true
		if setterReachable && setterAssignment {
			setterAssignmentCompletes = s.assignmentSetter(
				typed.Target,
				assignedValue,
				setterReceiver,
			)
			if logicalUnknown && !setterAssignmentCompletes {
				s.checker.restoreScopeState(logicalBaseScopeState)
				return true
			}
		}
		if !setterAssignment || s.checker.assignmentReceiverRootCurrent(setterReceiver) {
			s.checker.withSuppressedWarnings(func() {
				s.checker.inferAssignStatementTypes("", typed, nil, logicalTargetFact)
			})
		}
		if setterReachable && typed.Operator == "" {
			s.recordCallableAlias(typed.Target, assignedValue)
		}
		if setterReachable && !setterAssignment {
			s.recordRuntimeNamespaceAssignment(typed.Target, assignedValue)
		}
		if logicalUnknown {
			writtenScopeState := s.checker.snapshotScopeState()
			s.checker.restoreScopeState(logicalBaseScopeState)
			s.checker.mergeScopeStates(
				logicalBaseScopeState,
				[]checkScopeState{logicalBaseScopeState, writtenScopeState},
			)
		}
		if setterAssignment {
			return setterAssignmentCompletes
		}
		return true
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
		baseScopeState := s.checker.snapshotScopeState()
		bodyCompletes, bodyFailureStates := s.collectFailureScopes(func() bool {
			return s.statements(typed.Body)
		})
		bodyScopeState := s.checker.snapshotScopeState()
		rescueCompletes := false
		rescueReachable := len(bodyFailureStates) > 0 && !statementsProvenNonRaising(typed.Body)
		selected, exact := s.checker.staticallySelectedRescue(typed.Body, typed.Rescues)
		failureScopeState := bodyScopeState
		if rescueReachable && !exact {
			// Unknown failures can occur between any two evaluated subexpressions,
			// including after a partial destructuring assignment. Merge every
			// captured prefix so rescue and ensure dispatch never assumes that a
			// later rebind already happened.
			s.checker.mergeScopeStates(baseScopeState, bodyFailureStates)
			failureScopeState = s.checker.snapshotScopeState()
			s.checker.restoreScopeState(bodyScopeState)
		}
		ensureScopeStates := make([]checkScopeState, 0, len(typed.Rescues)+2)
		if rescueReachable && (!exact || selected < 0) {
			// The body may exit before its final statement. Its scanned state is
			// a conservative input for ensure alongside every handler outcome.
			ensureScopeStates = append(ensureScopeStates, failureScopeState)
		}
		if bodyCompletes {
			bodyCompletes = s.statements(typed.Else)
			ensureScopeStates = append(ensureScopeStates, s.checker.snapshotScopeState())
		}
		if rescueReachable && exact && selected >= 0 {
			s.checker.restoreScopeState(bodyScopeState)
			clause := &typed.Rescues[selected]
			if len(clause.Body) > 0 {
				rescueCompletes = s.statements(clause.Body)
				ensureScopeStates = append(ensureScopeStates, s.checker.snapshotScopeState())
			} else {
				ensureScopeStates = append(ensureScopeStates, bodyScopeState)
			}
		} else if rescueReachable && !exact {
			for i := range typed.Rescues {
				s.checker.restoreScopeState(failureScopeState)
				body := typed.Rescues[i].Body
				if len(body) > 0 {
					if s.statements(body) {
						rescueCompletes = true
					}
					ensureScopeStates = append(ensureScopeStates, s.checker.snapshotScopeState())
				}
			}
		}
		s.checker.mergeScopeStates(baseScopeState, ensureScopeStates)
		ensureCompletes := s.statements(typed.Ensure)
		return ensureCompletes && (bodyCompletes || rescueCompletes)
	case *FunctionStmt:
		// A nested definition's writes fire only when it is called, but a
		// missed invalidation is unsound while an extra one only widens, so
		// the walk stays conservative.
		s.withSuspendedDirectIvarAttribution(func() {
			s.withNominalReceiverParams(typed.Params, false, func() {
				for _, param := range typed.Params {
					s.expression(param.DefaultVal)
				}
				s.statements(typed.Body)
			})
		})
		return true
	case *ClassStmt:
		completed := true
		s.withSuspendedDirectIvarAttribution(func() {
			completed = s.statements(typed.Body)
		})
		return completed
	}
	return true
}

func (s *namespaceMutationScan) recordCallableAlias(target, value Expression) {
	targetIdent, ok := target.(*Identifier)
	if !ok || targetIdent.Name == "" {
		return
	}
	sourceIdent, ok := value.(*Identifier)
	if !ok || sourceIdent.Name == "" {
		return
	}
	if functions, bound := s.callableParams[sourceIdent.Name]; bound {
		s.callableParams[targetIdent.Name] = normalizeCheckCallables(append(
			s.callableParams[targetIdent.Name],
			functions...,
		))
	}
	if lambdas, bound := s.callableLambdas[sourceIdent.Name]; bound {
		s.callableLambdas[targetIdent.Name] = append(
			s.callableLambdas[targetIdent.Name],
			lambdas...,
		)
	}
	if functions, bound := s.callableSelfParams[sourceIdent.Name]; bound {
		s.callableSelfParams[targetIdent.Name] = normalizeCheckCallables(append(
			s.callableSelfParams[targetIdent.Name],
			functions...,
		))
	}
	if _, ambiguous := s.ambiguousSelfCallables[sourceIdent.Name]; ambiguous {
		s.ambiguousSelfCallables[targetIdent.Name] = struct{}{}
	}
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
	if !c.unshadowedStaticRaiseErrorClass(raise) ||
		!expressionProvenNonRaising(raise.Message) {
		return "", false
	}
	message, static := staticLiteralValue(raise.Message)
	if !static {
		return "", false
	}
	if message.Kind() != KindString {
		return runtimeErrorTypeType, true
	}
	root := c.runtimeTypeRoot
	if root == nil {
		root = c.typeRoot
	}
	return raiseErrorTypeName(raise.Value, root)
}

func (c *scriptChecker) unshadowedStaticRaiseErrorClass(stmt *RaiseStmt) bool {
	if !staticRaiseErrorClass(stmt) {
		return false
	}
	ident, ok := stmt.Value.(*Identifier)
	return ok && !c.staticNameShadowed(ident.Name)
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

func tryBodyExceptionsMayEscape(rescues []RescueClause, selected int, exact bool) bool {
	if len(rescues) == 0 {
		return true
	}
	if exact {
		return selected < 0 || len(rescues[selected].Body) == 0
	}
	for i := range rescues {
		clause := &rescues[i]
		if len(clause.Body) == 0 {
			return true
		}
		if clause.Ty == nil {
			return false
		}
	}
	return true
}

func expressionProvenNonRaising(expr Expression) bool {
	switch typed := expr.(type) {
	case nil, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral,
		*NilLiteral, *SymbolLiteral:
		return true
	case *ArrayLiteral:
		for _, element := range typed.Elements {
			if _, splat := element.(*SplatArg); splat || !expressionProvenNonRaising(element) {
				return false
			}
		}
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
		if !s.expression(typed.Object) {
			return false
		}
		s.checker.captureAssignmentReceiver(typed)
		return true
	case *IndexExpr:
		if !s.expression(typed.Object) {
			return false
		}
		s.checker.captureEvaluatedDestructureFact(typed.Object)
		s.checker.captureAssignmentReceiver(typed)
		for _, index := range typed.Indices {
			if !s.expression(index) {
				return false
			}
			s.checker.captureEvaluatedDestructureFact(index)
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

func (s *namespaceMutationScan) replayDestructureAssignment(
	target *DestructureTarget,
	value Expression,
) bool {
	facts := s.checker.captureDestructureValueFacts(target, value)
	return s.replayCapturedDestructureAssignment(facts)
}

func (s *namespaceMutationScan) replayCapturedDestructureAssignment(
	facts []capturedDestructureValueFact,
) bool {
	for _, fact := range facts {
		if fact.target == nil {
			continue
		}
		if _, nested := fact.target.(*DestructureTarget); nested {
			if !s.replayCapturedDestructureAssignment(
				s.checker.expandCapturedNestedDestructureFact(fact),
			) {
				return false
			}
			continue
		}
		fact = s.checker.refreshCapturedDestructureContainerFact(fact)
		if _, ident := fact.target.(*Identifier); ident {
			s.checker.bindCapturedDestructureValueFact(fact)
			continue
		}
		leafValue := fact.value
		if leafValue == nil {
			leafValue = &Identifier{
				Name:     "\x00destructure-value",
				Position: fact.target.Pos(),
			}
		}
		s.checker.pinExpressionFact(leafValue, fact.assigned)
		var indexedReceiverFact *TypeExpr
		if index, ok := fact.target.(*IndexExpr); ok {
			if ident, direct := index.Object.(*Identifier); direct {
				indexedReceiverFact = s.checker.localTypeFor(ident.Name)
			}
		}
		leaf := &AssignStmt{
			Target:   fact.target,
			Value:    leafValue,
			Position: fact.target.Pos(),
		}
		targetCompleted, setterReceiver := s.checker.withAssignmentReceiverCapture(
			fact.target,
			func() bool { return s.assignmentTarget(fact.target) },
		)
		if !targetCompleted {
			return false
		}
		argumentFacts := map[Expression]capturedDestructureValueFact{leafValue: fact}
		if index, ok := fact.target.(*IndexExpr); ok {
			for _, selector := range index.Indices {
				if selectorFact, captured := s.checker.evaluatedDestructureFacts[selector]; captured {
					argumentFacts[selector] = selectorFact
				}
			}
		}
		completed := true
		s.checker.withCapturedDestructureArgumentFacts(argumentFacts, func() {
			ivar, ivarTarget := fact.target.(*IvarExpr)
			if !ivarTarget || !s.checker.ivarWriteProvablyCompletes(ivar.Name, leafValue) {
				s.captureFailureScope()
			}
			completed = s.assignmentSetter(fact.target, leafValue, setterReceiver)
			if s.checker.assignmentReceiverRootCurrent(setterReceiver) {
				s.checker.withSuppressedWarnings(func() {
					s.checker.inferAssignStatementTypes("", leaf, indexedReceiverFact, nil)
				})
			}
		})
		if !completed {
			return false
		}
		s.recordDirectIvarTarget(fact.target)
	}
	return true
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

func (s *namespaceMutationScan) recordRuntimeNamespaceAssignment(
	target, value Expression,
	captured ...checkDynamicCallCandidates,
) {
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
	if index, ok := target.(*IndexExpr); ok {
		call := assignmentSetterCall(index, value)
		if len(captured) > 0 {
			resolution := s.checker.exactDynamicCallTargets(
				call,
				staticCallable{},
				false,
				captured[0],
			)
			if resolution.exact {
				for _, candidate := range resolution.targets {
					s.scanResolvedFunctionCall(candidate.call, candidate.target)
				}
			}
			return
		}
		s.callCallee(call)
		return
	}
	member, ok := target.(*MemberExpr)
	if !ok {
		return
	}
	if s.recordResolvedAssignmentMember(member, value, captured...) {
		return
	}
	switch object := member.Object.(type) {
	case *Identifier:
		if object.Name == "self" ||
			s.checker.identifierShadowed(object.Name) ||
			s.checker.hostGlobalShadows(object.Name) {
			return
		}
		s.out[object.Name+"."+member.Property] = struct{}{}
	case *MemberExpr:
		ident, selfClass := object.Object.(*Identifier)
		if !selfClass || ident.Name != "self" || object.Property != "class" || s.selfClass == nil {
			return
		}
		s.out[s.selfClass.Name+"."+member.Property] = struct{}{}
	}
}

func (s *namespaceMutationScan) assignmentSetter(
	target, value Expression,
	receiver checkAssignmentReceiverCapture,
) bool {
	completed := true
	s.checker.withEvaluatedAssignmentSetterArgumentFacts(target, value, func() {
		if receiver.captured {
			if index, ok := target.(*IndexExpr); ok {
				s.capturedIndexSetter(index, value, receiver)
			} else {
				s.recordRuntimeNamespaceAssignment(target, value, receiver.candidates)
			}
			completed = s.checker.assignmentSetterMayCompleteWithReceiver(
				target,
				value,
				receiver,
			)
			return
		}
		s.recordRuntimeNamespaceAssignment(target, value)
		completed = s.checker.assignmentSetterMayComplete(target, value)
	})
	return completed
}

func (s *namespaceMutationScan) capturedIndexSetter(
	target *IndexExpr,
	value Expression,
	receiver checkAssignmentReceiverCapture,
) {
	selection := s.checker.assignmentSetterScriptDispatch(target, value, receiver)
	if selection.unknown {
		if s.checker.expressionIsCurrentInstanceSelf(target.Object) {
			s.markUnknownDirectIvarEffects()
		}
		s.callCallee(assignmentSetterCall(target, value))
		return
	}
	for _, selected := range selection.targets {
		if selected.bindingStarts {
			s.scanResolvedFunctionCall(selected.call, selected.target)
		}
	}
}

func (s *namespaceMutationScan) capturedIndexGetter(
	target *IndexExpr,
	receiver checkAssignmentReceiverCapture,
) bool {
	hashDefaults, hashDefaultsExact := s.checker.captureDirectCoreHashDefaults(target.Object)
	call := &CallExpr{
		Callee: &MemberExpr{
			Object:   target.Object,
			Property: "[]",
			Position: target.Pos(),
		},
		Args:     target.Indices,
		Position: target.Pos(),
	}
	selection := s.checker.indexScriptDispatch(target, receiver.receiverType)
	if selection.unknown {
		if member, ok := call.Callee.(*MemberExpr); ok {
			s.memberReference(member)
		}
	} else {
		for _, selected := range selection.targets {
			if selected.bindingStarts {
				s.scanResolvedFunctionCall(selected.call, selected.target)
			}
		}
	}
	if hashDefaultsExact {
		effects, mayRun, _ := s.checker.indexReadIvarEffects(
			target,
			receiver.receiverType,
			hashDefaults,
		)
		if mayRun {
			if effects.unknown {
				s.markUnknownDirectIvarEffects()
			}
			for name := range effects.writes {
				s.recordDirectIvarWrite(name)
			}
		}
	}
	return s.checker.indexExpressionMayCompleteWithReceiverAndDefaults(
		target,
		receiver.receiverType,
		hashDefaults,
		hashDefaultsExact,
	)
}

func (s *namespaceMutationScan) logicalAssignmentTargetTruthiness(
	target Expression,
) (bool, bool) {
	if fact, captured := s.checker.evaluatedDestructureFacts[target]; captured {
		valueFact := checkLocalValueFact{
			classNames: fact.classNames,
			callables:  fact.callables,
			staticVals: fact.staticVals,
		}
		if truthy, known := localValueFactTruthiness(valueFact, fact.known); known {
			return truthy, true
		}
		if typeExprDefinitelyTruthy(fact.assigned) {
			return true, true
		}
		if typeExprDefinitelyFalsey(fact.assigned) {
			return false, true
		}
	}
	ident, ok := target.(*Identifier)
	if !ok {
		return s.checker.inferredConditionTruthiness(target)
	}
	if !s.checker.localTypeTracked(ident.Name) {
		if !isConstantIdentifier(ident.Name) {
			return false, true
		}
		return s.checker.inferredConditionTruthiness(target)
	}
	if fact, exact := s.checker.localValueFactFor(ident.Name); exact {
		if truthy, known := localValueFactTruthiness(fact, true); known {
			return truthy, true
		}
	}
	ty := s.checker.localTypeFor(ident.Name)
	if typeExprDefinitelyTruthy(ty) {
		return true, true
	}
	if typeExprDefinitelyFalsey(ty) {
		return false, true
	}
	return false, false
}

func (s *namespaceMutationScan) recordResolvedAssignmentMember(
	member *MemberExpr,
	value Expression,
	captured ...checkDynamicCallCandidates,
) bool {
	if member != nil && expressionIsSelf(member.Object) {
		s.markUnknownDirectIvarEffects()
	}
	receivers, exact := s.checker.exactAssignmentMemberReceivers(member, captured...)
	if !exact {
		switch object := member.Object.(type) {
		case *Identifier:
			if object.Name == "self" && s.selfClass != nil {
				receivers = []assignmentMemberReceiver{{
					class:       s.selfClass,
					classMethod: s.selfClassContext,
				}}
				exact = true
			}
		case *MemberExpr:
			ident, selfClass := object.Object.(*Identifier)
			if selfClass && ident.Name == "self" && object.Property == "class" && s.selfClass != nil {
				receivers = []assignmentMemberReceiver{{
					class:       s.selfClass,
					classMethod: true,
				}}
				exact = true
			}
		}
	}
	if !exact {
		return false
	}
	for _, receiver := range receivers {
		classDef := receiver.class
		if classDef == nil {
			continue
		}
		if !receiver.classMethod && sameScriptClass(classDef, s.effectSelfClass) {
			s.markUnknownDirectIvarEffects()
		}
		methods := classDef.Methods
		separator := "#"
		if receiver.classMethod {
			methods = classDef.ClassMethods
			separator = "."
		}
		setterName := member.Property + "="
		setter := methods[setterName]
		if setter == nil {
			if methods[member.Property] == nil && receiver.classMethod {
				s.out[classDef.Name+"."+member.Property] = struct{}{}
			}
			continue
		}
		if setter.Private || setter.Protected &&
			(s.selfClass != classDef || s.selfClassContext != receiver.classMethod) {
			continue
		}
		callee := *member
		callee.Property = setterName
		call := &CallExpr{
			Callee:   &callee,
			Args:     []Expression{value},
			Position: member.Pos(),
		}
		s.scanFunctionCall(setter, call, staticCallable{
			name:       classDef.Name + separator + setterName,
			fn:         setter,
			resolution: calleeMemberMethod,
		})
	}
	return true
}

func (s *namespaceMutationScan) expression(expr Expression) bool {
	return s.expressionWithAuto(expr, true)
}

func (s *namespaceMutationScan) expressionWithAuto(expr Expression, autoCall bool) bool {
	s.captureFailureScope()
	defer s.captureFailureScope()
	switch typed := expr.(type) {
	case nil:
		return true
	case *Identifier:
		if _, pending := s.unboundParams[typed.Name]; pending &&
			!s.checker.staticNameShadowed(typed.Name) {
			return false
		}
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
		for _, method := range binaryDispatchMethodNames(typed.Operator) {
			s.callCallee(binaryDispatchCall(typed, method))
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
		bodyCompletes := s.expressionWithAuto(typed.Body, autoCall)
		if errorKind, exact := s.checker.staticallyRaisedExpressionErrorKind(typed.Body); exact &&
			!staticErrorKindMatchesRescue(errorKind, nil) {
			return bodyCompletes
		}
		s.expressionWithAuto(typed.Fallback, autoCall)
		return s.checker.expressionMayCompleteForBindingWithAuto(typed, autoCall)
	case *RangeExpr:
		return s.expression(typed.Start) &&
			s.checker.rangeEndpointConversionMaySucceed(typed.Start) &&
			s.expression(typed.End) &&
			s.checker.rangeEndpointConversionMaySucceed(typed.End)
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			if !s.expression(elem) {
				return false
			}
			s.checker.captureEvaluatedDestructureFact(elem)
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
		previousEvaluatedFacts := s.checker.evaluatedDestructureFacts
		if previousEvaluatedFacts == nil {
			s.checker.evaluatedDestructureFacts = make(map[Expression]capturedDestructureValueFact)
			defer func() {
				s.checker.evaluatedDestructureFacts = previousEvaluatedFacts
			}()
		}
		if !s.expression(typed.Object) {
			return false
		}
		s.checker.captureEvaluatedDestructureFactOnce(typed.Object)
		getterReceiver, _ := s.checker.assignmentReceiverSnapshot(typed)
		s.checker.captureAssignmentReceiver(typed)
		for _, index := range typed.Indices {
			if !s.expression(index) {
				return false
			}
			s.checker.captureEvaluatedDestructureFactOnce(index)
		}
		argumentFacts := make(
			map[Expression]capturedDestructureValueFact,
			len(typed.Indices)+1,
		)
		for _, expression := range append([]Expression{typed.Object}, typed.Indices...) {
			if fact, captured := s.checker.evaluatedDestructureFacts[expression]; captured {
				argumentFacts[expression] = fact
			}
		}
		if getterReceiver.staticValuesExact && len(getterReceiver.staticValues) > 0 {
			argumentFacts[typed.Object] = capturedDestructureValueFact{
				value:      typed.Object,
				assigned:   getterReceiver.receiverType,
				known:      true,
				evaluated:  true,
				staticVals: append([]Expression(nil), getterReceiver.staticValues...),
				factKind:   destructureStaticFact,
			}
		}
		completed := true
		s.checker.withCapturedDestructureArgumentFacts(argumentFacts, func() {
			completed = s.capturedIndexGetter(typed, getterReceiver)
			if completed {
				s.checker.captureEvaluatedDestructureFact(typed)
			}
		})
		return completed
	case *MemberExpr:
		objectAutoCall := true
		if typed.Property == "call" && typeExprMayIncludeCallable(s.checker.inferExpressionType(typed.Object)) {
			objectAutoCall = false
		}
		if !s.expressionWithAuto(typed.Object, objectAutoCall) {
			return false
		}
		s.checker.captureAssignmentReceiver(typed)
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
	if expectation.empty() {
		return s.expression(expr)
	}
	if expectation.includesCallable() {
		if _, bindable := s.checker.bareMemberArgumentCallableFact(expr); bindable {
			return s.expressionWithAuto(expr, false)
		}
		if callableExpr, bindable := bareIdentifierCallableValue(expr); bindable {
			return s.expressionWithAuto(callableExpr, false)
		}
	}
	switch typed := expr.(type) {
	case *ConditionalExpr:
		if !s.expression(typed.Condition) {
			return false
		}
		truthy, known := s.checker.inferredConditionTruthiness(typed.Condition)
		if !known || truthy {
			s.callArgumentExpression(typed.Consequent, expectation)
		}
		if !known || !truthy {
			s.callArgumentExpression(typed.Alternate, expectation)
		}
		return s.checker.expressionMayCompleteForBindingWithExpectation(expr, expectation)
	case *IfExpr:
		if !s.expression(typed.Condition) {
			return false
		}
		truthy, known := s.checker.inferredConditionTruthiness(typed.Condition)
		if !known || truthy {
			s.callArgumentExpression(typed.Consequent, expectation)
		}
		falseReachable := !known || !truthy
		for _, branch := range typed.ElseIf {
			if !falseReachable || !s.expression(branch.Condition) {
				break
			}
			branchTruthy, branchKnown := s.checker.inferredConditionTruthiness(branch.Condition)
			if !branchKnown || branchTruthy {
				s.callArgumentExpression(branch.Result, expectation)
			}
			falseReachable = !branchKnown || !branchTruthy
		}
		if falseReachable {
			s.callArgumentExpression(typed.Alternate, expectation)
		}
		return s.checker.expressionMayCompleteForBindingWithExpectation(expr, expectation)
	case *CaseExpr:
		if !s.expression(typed.Target) {
			return false
		}
		if result, known := s.checker.inferredCaseExpressionResult(typed); known {
			s.callArgumentExpression(result, expectation)
			return s.checker.expressionMayCompleteForBindingWithExpectation(expr, expectation)
		}
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				if !s.expression(value.Expr) ||
					!s.checker.caseWhenSplatExpansionMaySucceed(value.Expr, value.Splat) {
					return false
				}
			}
			s.callArgumentExpression(clause.Result, expectation)
		}
		s.callArgumentExpression(typed.ElseExpr, expectation)
		return s.checker.expressionMayCompleteForBindingWithExpectation(expr, expectation)
	case *RescueExpr:
		autoCall := !expectation.includesCallable()
		s.expressionWithAuto(typed.Body, autoCall)
		if errorKind, exact := s.checker.staticallyRaisedExpressionErrorKind(typed.Body); exact &&
			!staticErrorKindMatchesRescue(errorKind, nil) {
			return s.checker.expressionMayCompleteForBindingWithAuto(expr, autoCall)
		}
		s.expressionWithAuto(typed.Fallback, autoCall)
		return s.checker.expressionMayCompleteForBindingWithAuto(expr, autoCall)
	case *ArrayLiteral:
		elementExpectation, ok := expectation.arrayElementExpectation()
		if !ok {
			break
		}
		for i, element := range typed.Elements {
			if !s.callArgumentExpression(element, elementExpectation(i, len(typed.Elements))) {
				return false
			}
			s.checker.captureEvaluatedDestructureFact(element)
		}
		return true
	case *HashLiteral:
		if typed.ShapeType != nil && !s.checker.hashShapeStaticallyShadowed(typed) {
			return true
		}
		if !hashLiteralTypeHasValueSlots(expectation.ty) {
			break
		}
		for _, pair := range typed.Pairs {
			if !s.expression(pair.Key) {
				return false
			}
			valueExpectation := expressionExpectation{}
			if key, ok := staticLiteralValue(pair.Key); ok {
				valueExpectation = typeExpressionExpectation(hashLiteralValueType(expectation.ty, key))
			}
			if !s.callArgumentExpression(pair.Value, valueExpectation) {
				return false
			}
		}
		return true
	}
	return s.expressionWithAuto(expr, !expectation.includesCallable())
}

func (s *namespaceMutationScan) callResolvedCallee(
	call *CallExpr,
	target staticCallable,
	resolved bool,
	dynamicResolution checkDynamicCallResolution,
) {
	if s.callableParamCall(call) {
		return
	}
	if s.explicitSelfCall(call) {
		return
	}
	if resolved {
		if target.fn != nil {
			s.scanResolvedFunctionCall(call, target)
		}
		return
	}
	if dynamicResolution.exact {
		for _, candidate := range dynamicResolution.targets {
			s.scanResolvedFunctionCall(candidate.call, candidate.target)
		}
		return
	}
	s.callCallee(call)
}

func (s *namespaceMutationScan) scanResolvedFunctionCall(
	call *CallExpr,
	target staticCallable,
) {
	if s.resolvedMethodMayRunOnEffectSelf(call, target) {
		s.markUnknownDirectIvarEffects()
	}
	s.scanFunctionCall(target.fn, call, target)
}

func (s *namespaceMutationScan) resolvedMethodMayRunOnEffectSelf(
	call *CallExpr,
	target staticCallable,
) bool {
	if call == nil || target.fn == nil || target.constructor ||
		s.currentFunction == nil || s.effectSelfClass == nil ||
		s.effectSelfClassContext {
		return false
	}
	if target.resolution != calleeMemberMethod &&
		target.resolution != calleeForwardedMethod {
		return false
	}
	if _, memberCall := call.Callee.(*MemberExpr); !memberCall {
		return false
	}
	if s.functionMayRunOnEffectSelf(target.fn) {
		return true
	}
	return target.name == s.effectSelfClass.Name+"#"+target.fn.Name
}

func (s *namespaceMutationScan) explicitSelfCall(call *CallExpr) bool {
	if call == nil {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || !expressionIsSelf(member.Object) {
		return false
	}
	s.selfCallReference(member.Property, call)
	return true
}

func (s *namespaceMutationScan) callableParamCall(call *CallExpr) bool {
	if call == nil {
		return false
	}
	member, ok := call.Callee.(*MemberExpr)
	if !ok || member.Property != "call" {
		return false
	}
	ident, ok := member.Object.(*Identifier)
	if !ok {
		return false
	}
	if _, bound := s.callableParams[ident.Name]; bound {
		s.functionReferenceWithCall(ident.Name, call)
		return true
	}
	if _, bound := s.callableLambdas[ident.Name]; bound {
		s.functionReferenceWithCall(ident.Name, call)
		return true
	}
	return false
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
		if s.scanExactLambdaCall(member.Object, call) {
			return
		}
	}
	if s.callableParamCall(call) {
		return
	}
	if s.explicitSelfCall(call) {
		return
	}
	target, resolved := s.checker.resolveCallable(call)
	if resolved {
		if target.fn != nil {
			s.scanResolvedFunctionCall(call, target)
		}
		return
	}
	candidates := s.checker.captureDynamicCallCandidates(call)
	resolution := s.checker.exactDynamicCallTargets(call, target, false, candidates)
	if resolution.exact {
		for _, candidate := range resolution.targets {
			s.scanResolvedFunctionCall(candidate.call, candidate.target)
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
		if callee.Property == "call" &&
			typeExprMayIncludeCallable(s.checker.inferExpressionType(callee.Object)) {
			s.invokedUnknownCallable = true
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
