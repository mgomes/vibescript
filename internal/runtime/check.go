package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
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
	return s.checkWarningsMode(opts, target, false)
}

func (s *Script) checkWarningsMode(opts CallOptions, target checkTarget, orderIndependentOnly bool) []CheckWarning {
	if s == nil {
		return nil
	}
	optionGlobals := checkOptionGlobals(s, opts)
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
	script                  *Script
	callOptions             CallOptions
	optionGlobals           map[string]Value
	optionGlobalsOverride   bool
	typeRoot                *Env
	runtimeTypeRoot         *Env
	hostGlobals             map[string]struct{}
	warnings                []CheckWarning
	scopes                  []map[string]struct{}
	localTypes              []checkTypeFrame
	typePoison              map[string]struct{}
	typeAliases             map[string]map[string]struct{}
	mutationRegionDepth     int
	speculativeInference    int
	callArgumentFacts       map[Expression]*TypeExpr
	deferredReturnSites     *[]deferredReturnSite
	implicitReturnLeaves    map[Statement]struct{}
	implicitReturnStates    map[Statement]checkStateSnapshot
	requiredModules         map[string]struct{}
	runtimeModules          map[string]struct{}
	runtimeNamespaceMembers map[string]struct{}
	moduleEntries           map[string]moduleEntry
	moduleExportValues      map[string]Value
	moduleCheckedFunctions  map[string]struct{}
	moduleCheckContext      string
	moduleCaller            *moduleContext
	moduleExportRoot        *Env
	runtimeTypeRootParent   *Env
	checkReachableCalls     bool
	checkedReachableFuncs   map[string]struct{}
	reachableFuncQueue      []reachableFunction
	selfScope               bool
	selfClass               *ClassDef
	selfClassContext        bool
	selfScopeFnClasses      map[*ScriptFunction]*ClassDef
	selfScopeClassFns       map[*ScriptFunction]struct{}
	localNameUnions         []map[string]struct{}
	liveLocalNames          []map[string]struct{}
	nameFactsCache          *checkNameFacts
	selfScopeFns            map[*ScriptFunction]struct{}
	orderIndependentOnly    bool
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
}

func checkOptionGlobals(script *Script, opts CallOptions) map[string]Value {
	if len(opts.Capabilities) == 0 && len(opts.Globals) == 0 {
		return nil
	}
	globals := make(map[string]Value, len(opts.Globals)+len(opts.Capabilities)*2)
	if script != nil {
		binding := CapabilityBinding{Context: context.Background(), Engine: script.engine}
		for _, adapter := range opts.Capabilities {
			if adapter == nil {
				continue
			}
			bound, err := adapter.Bind(binding)
			if err != nil {
				continue
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
		return nil
	}
	return globals
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
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			c.collectRequiredModuleExportsFromExpression(pair.Key)
			c.collectRequiredModuleExportsFromExpression(pair.Value)
		}
	case *CallExpr:
		c.collectRequireCallExports(typed)
		callSkipsInferred := c.safeNavigationCallSkipsInferred(typed)
		argumentsAlwaysEvaluate := c.safeNavigationArgumentsAlwaysEvaluateInferred(typed)
		if member, ok := typed.Callee.(*MemberExpr); ok {
			// The callee's receiver walks directly so its facts survive
			// until dispatch, mirroring the real walk's poison ordering.
			c.collectRequiredModuleExportsFromExpression(member.Object)
		} else {
			c.collectRequiredModuleExportsFromExpression(typed.Callee)
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
		if c.isolatedCollectInference {
			if typed.Block != nil {
				c.degradeBlockBodyBindings(typed.Block)
			}
			if member, ok := typed.Callee.(*MemberExpr); ok && !c.knownPureUniversalPredicateCall(typed) {
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
		if c.isolatedCollectInference && !c.knownPureUniversalPredicateMember(typed) {
			c.poisonEscapedIdentifier(typed.Object)
		}
	case *ScopeExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
	case *IndexExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
		for _, index := range typed.Indices {
			c.collectRequiredModuleExportsFromExpression(index)
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
		c.collectRequiredModuleExportsFromExpression(typed.End)
	case *CaseExpr:
		c.collectRequiredModuleExportsFromCaseExpression(typed)
	case *BlockLiteral:
		return
	case *YieldExpr:
		for _, arg := range typed.Args {
			c.collectRequiredModuleExportsFromExpression(arg)
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
	}
	for _, kwarg := range call.KwArgs {
		c.collectRequiredModuleExportsFromExpression(kwarg.Value)
	}
	if call.BlockArg != nil {
		c.collectRequiredModuleExportsFromExpression(call.BlockArg)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromCaseExpression(expr *CaseExpr) {
	baseState := c.snapshotModuleCollectionState()
	c.collectRequiredModuleExportsFromExpression(expr.Target)
	fallthroughState := c.snapshotModuleCollectionState()
	branchStates := make([]checkModuleCollectionState, 0, len(expr.Clauses)+1)

	for _, clause := range expr.Clauses {
		for _, value := range clause.Values {
			c.restoreModuleCollectionState(fallthroughState)
			c.collectRequiredModuleExportsFromExpression(value.Expr)
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
	c.moduleCaller = &caller
	c.scopes = nil
	c.localTypes = nil
	defer func() {
		c.moduleCaller = previousCaller
		c.scopes = previousScopes
		c.localTypes = previousLocalTypes
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
				c.refineAnnotatedParamFact(param, c.inferExpressionType(param.DefaultVal))
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
	c.checkExpression(function, param.DefaultVal)
	c.collectRuntimeRequireCallExportsFromExpression(param.DefaultVal)
	if param.Type == nil {
		return
	}
	c.checkRuntimeTypeAnnotation(function, param.Type)
	if param.DefaultVal != nil {
		c.checkRuntimeExpressionAgainstType(function, param.DefaultVal, param.Type, fmt.Sprintf("default value for %s", param.Name))
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
	if !c.checkReachableCalls || fn == nil || fn.owner != c.script {
		return
	}
	if !c.markReachableFunctionChecked(fn) {
		return
	}
	c.reachableFuncQueue = append(c.reachableFuncQueue, reachableFunction{
		label:        label,
		fn:           fn,
		runtimeState: c.snapshotRuntimeState(),
	})
}

// enqueueReachableIdentifierCall covers bare auto-calls: a top-level `run`
// dispatches like run(), so the callee checks under the call-time runtime
// root exactly as a spelled-out call does.
func (c *scriptChecker) enqueueReachableIdentifierCall(ident *Identifier) {
	if !c.checkReachableCalls || ident == nil {
		return
	}
	if c.identifierShadowed(ident.Name) || c.hostGlobalShadows(ident.Name) {
		return
	}
	if fn, ok := c.script.functions[ident.Name]; ok {
		c.enqueueReachableFunction(ident.Name, fn)
	}
}

func (c *scriptChecker) markReachableFunctionChecked(fn *ScriptFunction) bool {
	if fn == nil {
		return false
	}
	if c.checkedReachableFuncs == nil {
		c.checkedReachableFuncs = make(map[string]struct{})
	}
	key := c.reachableFunctionCheckKey(fn)
	if _, ok := c.checkedReachableFuncs[key]; ok {
		return false
	}
	c.checkedReachableFuncs[key] = struct{}{}
	return true
}

func (c *scriptChecker) reachableFunctionCheckKey(fn *ScriptFunction) string {
	root := c.runtimeTypeRoot
	if root == nil {
		root = c.typeRoot
	}
	return fmt.Sprintf("%p\x00%s", fn, moduleCheckContextKey(root))
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
		c.checkFunction(next.label, next.fn)
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
	c.runtimeTypeRoot = checkTypeRootWithParentAndGlobals(c.script, c.optionGlobals, cloneCheckRoot(c.runtimeTypeRootParent), c.optionGlobalsOverride)
	c.runtimeModules = nil
	c.runtimeNamespaceMembers = nil
	defer func() {
		c.runtimeTypeRoot = previousRoot
		c.runtimeModules = previousModules
		c.runtimeNamespaceMembers = previousNamespaceMembers
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
	root             *Env
	modules          map[string]struct{}
	namespaceMembers map[string]struct{}
}

type checkModuleCollectionState struct {
	root    *Env
	modules map[string]struct{}
}

type checkScopeState struct {
	defined []map[string]struct{}
	types   []checkTypeFrame
}

func (c *scriptChecker) snapshotRuntimeState() checkRuntimeState {
	state := checkRuntimeState{
		modules:          cloneCheckModuleSet(c.runtimeModules),
		namespaceMembers: cloneCheckStringSet(c.runtimeNamespaceMembers),
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

func (c *scriptChecker) mergeRuntimeStates(base checkRuntimeState, states []checkRuntimeState) {
	c.restoreRuntimeState(base)
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

	commonMembers := commonRuntimeNamespaceMembers(base, states)
	if len(commonMembers) == 0 {
		return
	}
	if c.runtimeNamespaceMembers == nil {
		c.runtimeNamespaceMembers = make(map[string]struct{}, len(commonMembers))
	}
	for member := range commonMembers {
		c.runtimeNamespaceMembers[member] = struct{}{}
	}
}

func commonRuntimeNamespaceMembers(base checkRuntimeState, states []checkRuntimeState) map[string]struct{} {
	common := cloneCheckStringSet(states[0].namespaceMembers)
	for _, state := range states[1:] {
		for key := range common {
			if _, ok := state.namespaceMembers[key]; !ok {
				delete(common, key)
			}
		}
	}
	for key := range base.namespaceMembers {
		delete(common, key)
	}
	return common
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
	state := checkScopeState{types: c.snapshotLocalTypes()}
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

		for _, param := range fn.Params {
			c.checkExpression(label, param.DefaultVal)
			c.collectRuntimeRequireCallExportsFromExpression(param.DefaultVal)
			if param.Type != nil {
				c.checkRuntimeTypeAnnotation(label, param.Type)
				if param.DefaultVal != nil {
					c.checkRuntimeExpressionAgainstType(label, param.DefaultVal, param.Type, fmt.Sprintf("default value for %s", param.Name))
				}
			}
			c.recordParamBinding(param)
		}
		c.checkStatements(label, fn.ReturnTy, fn.Body)
		if fn.ReturnTy != nil {
			c.checkImplicitReturn(label, fn.ReturnTy, fn.Body, fn.Pos)
		}
	})
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
		c.checkExpression(function, typed.Value)
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
		if !staticRaiseErrorClass(typed) {
			c.checkExpression(function, typed.Value)
		}
		c.checkExpression(function, typed.Message)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Message)
	case *BreakStmt:
		c.checkExpression(function, typed.Value)
	case *AssignStmt:
		c.checkExpression(function, typed.Target)
		c.checkExpression(function, typed.Value)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Value)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Target)
		c.inferAssignStatementTypes(function, typed)
		c.recordRuntimeBindingTarget(typed.Target)
		c.recordBindingTarget(typed.Target)
		c.captureImplicitReturnState(typed)
	case *ExprStmt:
		c.checkExpression(function, typed.Expr)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Expr)
		c.captureImplicitReturnState(typed)
	case *ClassStmt:
		classDef := c.script.classes[typed.Name]
		if classDef != nil {
			c.checkRuntimeClassBody(classDef, false)
		}
	case *IfStmt:
		baseRuntimeState := c.snapshotRuntimeState()
		baseScopeState := c.snapshotScopeState()
		c.checkExpression(function, typed.Condition)
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
			c.checkExpression(function, elseIf.Condition)
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
		c.checkExpression(function, typed.Iterable)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Iterable)
		elemType := c.forTargetElementType(typed)
		c.recordLiveStatementNames(typed.Body)
		c.degradeLocalTypesForBindings(typed.Body, typed.Target)
		c.recordBindingTarget(typed.Target)
		c.bindForTargetType(typed, elemType)
		bodyRuntimeState := c.snapshotRuntimeState()
		bodyScopeState := c.snapshotScopeState()
		c.mutationRegionDepth++
		c.checkStatements(function, returnType, typed.Body)
		c.mutationRegionDepth--
		c.restoreRuntimeState(bodyRuntimeState)
		c.restoreScopeState(bodyScopeState)
		c.degradeLocalTypesForBindings(nil, typed.Target)
		c.recordLocalBindings(typed.Body)
	case *WhileStmt:
		// The condition's first evaluation sees pre-loop facts, so it is
		// checked before body-assigned locals degrade to unknown. Prove the
		// body outcome reachable against those facts before degradation, then
		// reapply it to the conservative loop-body state for the actual walk.
		c.checkExpression(function, typed.Condition)
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
				c.checkStatements(function, returnType, typed.Body)
				c.mutationRegionDepth--
			}
			c.restoreRuntimeState(bodyRuntimeState)
			c.restoreScopeState(bodyScopeState)
		}
		c.recordLocalBindings(typed.Body)
	case *UntilStmt:
		c.checkExpression(function, typed.Condition)
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
				c.checkStatements(function, returnType, typed.Body)
				c.mutationRegionDepth--
			}
			c.restoreRuntimeState(bodyRuntimeState)
			c.restoreScopeState(bodyScopeState)
		}
		c.recordLocalBindings(typed.Body)
	case *TryStmt:
		deferReturnType := returnType != nil && len(typed.Ensure) > 0 && !blockAlwaysExits(typed.Ensure)
		branchReturnType := returnType
		if deferReturnType || blockAlwaysExits(typed.Ensure) {
			branchReturnType = nil
		}
		baseRuntimeState := c.snapshotRuntimeState()
		baseScopeState := c.snapshotScopeState()
		fallthroughRuntimeStates := make([]checkRuntimeState, 0, 2)
		fallthroughScopeStates := make([]checkScopeState, 0, 2)
		// Every non-exiting ensure collects the returns escaping through it,
		// whether or not this level owns the deferred annotation check: the
		// ensure walk merges their states, and the sites hand up to any
		// enclosing ensure the same returns continue through.
		armCapture := len(typed.Ensure) > 0 && !blockAlwaysExits(typed.Ensure)
		var deferredSites []deferredReturnSite
		var previousSites *[]deferredReturnSite
		if armCapture {
			previousSites = c.deferredReturnSites
			c.deferredReturnSites = &deferredSites
		}

		if c.checkStatements(function, branchReturnType, typed.Body) {
			if c.checkStatements(function, branchReturnType, typed.Else) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		// When handler i runs, every earlier clause was skipped and predeclared
		// its body locals as surrounding-scope nils, so each clause is checked
		// with the accumulated locals of the clauses before it in scope.
		earlierClauseLocals := map[string]struct{}{}
		for i := range typed.Rescues {
			clause := &typed.Rescues[i]
			// An empty clause never falls through (the matched error propagates
			// after ensure), so it must not merge the base state into the paths
			// that reach the code after the block.
			if len(clause.Body) == 0 {
				continue
			}
			c.restoreRuntimeState(baseRuntimeState)
			c.restoreScopeState(baseScopeState)
			popEarlier := func() {}
			if len(earlierClauseLocals) > 0 {
				scope := make(map[string]struct{}, len(earlierClauseLocals))
				for name := range earlierClauseLocals {
					scope[name] = struct{}{}
				}
				popEarlier = c.pushScope(scope)
			}
			popScope := c.pushRescueScope(clause)
			clauseFallsThrough := c.checkStatements(function, branchReturnType, clause.Body)
			popScope()
			popEarlier()
			clauseLocals := map[string]struct{}{}
			collectLocalBindings(clause.Body, clauseLocals)
			delete(clauseLocals, clause.Binding)
			for name := range clauseLocals {
				earlierClauseLocals[name] = struct{}{}
			}
			if clauseFallsThrough {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		if armCapture {
			c.deferredReturnSites = previousSites
		}
		mergeRuntimeStates := fallthroughRuntimeStates
		mergeScopeStates := fallthroughScopeStates
		// The ensure block runs on every path into it — fallthrough or
		// deferred return — so the return sites' states join the merge the
		// ensure walk (and the code after the block) sees.
		for _, site := range deferredSites {
			mergeRuntimeStates = append(mergeRuntimeStates, site.runtimeState)
			mergeScopeStates = append(mergeScopeStates, site.scopeState)
		}
		c.mergeRuntimeStates(baseRuntimeState, mergeRuntimeStates)
		c.mergeScopeStates(baseScopeState, mergeScopeStates)
		c.checkStatements(function, returnType, typed.Ensure)
		if deferReturnType {
			c.checkDeferredReturnSitesAfterEnsure(function, returnType, typed.Ensure, deferredSites)
		}
		if armCapture && previousSites != nil {
			*previousSites = append(*previousSites, deferredSites...)
		}
		// No fallthrough path means the code after the block is
		// unreachable: deferred returns exit through the ensure.
		c.stmtNoFallthroughInferred = len(fallthroughRuntimeStates) == 0
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
			len(typed.Args) == 0 && len(typed.KwArgs) == 0 &&
			typed.Block == nil && typed.BlockArg == nil {
			return c.narrowNilPredicateMember(member, truthy)
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

func (c *scriptChecker) checkExpression(function string, expr Expression) {
	c.checkExpressionWithAuto(function, expr, true)
}

func (c *scriptChecker) checkExpressionWithAuto(function string, expr Expression, autoCall bool) {
	switch typed := expr.(type) {
	case nil, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return
	case *Identifier:
		c.checkIdentifierResolved(function, typed)
		if autoCall {
			c.enqueueReachableIdentifierCall(typed)
		}
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			c.checkExpressionWithAuto(function, elem, true)
		}
	case *HashLiteral:
		// A dual-reading braced group evaluates as a shape unless one of its
		// type names is shadowed, so its identifier values are type spellings
		// rather than variable reads and must not warn as undefined.
		if typed.ShapeType != nil && !c.hashShapeStaticallyShadowed(typed) {
			return
		}
		for _, pair := range typed.Pairs {
			c.checkExpressionWithAuto(function, pair.Key, true)
			c.checkExpressionWithAuto(function, pair.Value, true)
		}
	case *CallExpr:
		// The receiver's nil-ness resolves from the facts at its evaluation
		// point, before member dispatch poisons the receiver's own facts.
		callSkipsInferred := c.safeNavigationCallSkipsInferred(typed)
		argumentsAlwaysEvaluate := c.safeNavigationArgumentsAlwaysEvaluateInferred(typed)
		c.checkExpressionWithAuto(function, typed.Callee, false)
		if staticNilSafeNavigationCall(typed) || callSkipsInferred {
			return
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
		// Arguments evaluate left to right before the call dispatches, so
		// each argument's inferred type is captured at its own evaluation
		// point: a mutating earlier argument (h.delete(:name)) poisons its
		// container's facts for later arguments, while a mutating later
		// argument cannot erase the facts an earlier argument was evaluated
		// under. checkCall consumes the captured facts afterwards.
		argumentFacts := make(map[Expression]*TypeExpr, len(typed.Args)+len(typed.KwArgs))
		for _, arg := range typed.Args {
			c.checkExpressionWithAuto(function, arg, true)
			// The argument's value and effects materialize once its own
			// evaluation completes, before the next argument runs: a shovel
			// append lands in the facts, and a require binds its exports
			// for the arguments after it, never the ones before.
			c.collectRuntimeRequireCallExportsFromExpression(arg)
			argumentFacts[arg] = c.inferExpressionType(arg)
		}
		for _, kwarg := range typed.KwArgs {
			c.checkExpressionWithAuto(function, kwarg.Value, true)
			c.collectRuntimeRequireCallExportsFromExpression(kwarg.Value)
			argumentFacts[kwarg.Value] = c.inferExpressionType(kwarg.Value)
		}
		if typed.BlockArg != nil {
			c.checkExpressionWithAuto(function, typed.BlockArg, false)
			// A block argument evaluates with the other arguments, before
			// dispatch, so its require effects are live for the call checks.
			c.collectRuntimeRequireCallExportsFromExpression(typed.BlockArg)
		}
		previousFacts := c.callArgumentFacts
		c.callArgumentFacts = argumentFacts
		c.checkCallResolved(function, typed, target, targetResolved)
		c.callArgumentFacts = previousFacts
		if c.callMayEvaluateBlock(typed) {
			c.checkLiteralArrayBlockParamTypes(function, typed)
			c.checkBlockLiteral(function, typed.Block)
		}
		if argumentsMayBeSkipped {
			c.restoreRuntimeState(argumentState)
			// A nil receiver skips the arguments entirely, so type facts the
			// argument walk established (a shovel append, for example) hold
			// on only one of the two paths and must merge as a branch join.
			evaluatedScopeState := c.snapshotScopeState()
			c.mergeScopeStates(argumentScopeState, []checkScopeState{argumentScopeState, evaluatedScopeState})
		}
		// Containers pass by reference, so a callee may mutate an argument
		// in place; the caller's structural facts stop holding. Dispatch
		// happens after the arguments evaluate, so the receiver's facts
		// stop holding here too, not during the callee walk. Proven universal
		// predicates are pure, so their receiver facts must
		// survive for outer inference and condition-outcome narrowing.
		if member, ok := typed.Callee.(*MemberExpr); ok && !c.knownPureUniversalPredicateCall(typed) {
			c.poisonEscapedIdentifier(member.Object)
		}
		if member, ok := typed.BlockArg.(*MemberExpr); ok {
			c.poisonEscapedIdentifier(member.Object)
		}
		for _, arg := range typed.Args {
			c.poisonEscapedIdentifier(arg)
		}
		for _, kwarg := range typed.KwArgs {
			c.poisonEscapedIdentifier(kwarg.Value)
		}
	case *MemberExpr:
		c.checkExpressionWithAuto(function, typed.Object, true)
		if autoCall {
			c.checkMemberAutoCall(function, typed)
			// Member dispatch on a container may mutate it in place (push,
			// delete, ...), so the receiver's structural facts stop
			// holding. A call callee poisons after its arguments instead:
			// they evaluate before dispatch and still see the facts. Proven
			// universal predicates are pure and preserve the receiver
			// fact that outer inference or narrowing consumes next.
			if !c.knownPureUniversalPredicateMember(typed) {
				c.poisonEscapedIdentifier(typed.Object)
			}
		}
	case *ScopeExpr:
		c.checkExpressionWithAuto(function, typed.Object, true)
	case *IndexExpr:
		c.checkExpressionWithAuto(function, typed.Object, true)
		for _, index := range typed.Indices {
			c.checkExpressionWithAuto(function, index, true)
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			c.checkExpressionWithAuto(function, element.Target, true)
		}
	case *SplatArg:
		c.checkExpressionWithAuto(function, typed.Value, true)
	case *UnaryExpr:
		c.checkExpressionWithAuto(function, typed.Right, true)
		c.checkUnaryOperandTypes(function, typed)
	case *BinaryExpr:
		c.checkExpressionWithAuto(function, typed.Left, true)
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
			if rightReachable {
				c.checkExpressionWithAuto(function, typed.Right, true)
			}
			if !rightReachable {
				c.restoreRuntimeState(state)
				c.restoreScopeState(scopeState)
			} else if !rightAlwaysRuns {
				// A short-circuited right operand may not run, so its
				// runtime effects roll back and its type facts (a shovel
				// append, for example) merge as a branch join instead of
				// surviving unconditionally. A right side that provably
				// always runs keeps both — including exports from a
				// guaranteed require.
				c.restoreRuntimeState(state)
				evaluatedScopeState := c.snapshotScopeState()
				c.mergeScopeStates(scopeState, []checkScopeState{scopeState, evaluatedScopeState})
			}
		}
		c.checkBinaryOperandTypes(function, typed)
		c.applyShovelMutationFacts(typed)
	case *ConditionalExpr:
		c.checkConditionalExpression(function, typed)
	case *RescueExpr:
		c.checkRescueExpression(function, typed, autoCall)
	case *IfExpr:
		baseRuntimeState := c.snapshotRuntimeState()
		baseScopeState := c.snapshotScopeState()
		c.checkExpressionWithAuto(function, typed.Condition, true)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Condition)
		conditionRuntimeState := c.snapshotRuntimeState()
		conditionScopeState := c.snapshotScopeState()
		branchRuntimeStates := make([]checkRuntimeState, 0, len(typed.ElseIf)+2)
		branchScopeStates := make([]checkScopeState, 0, len(typed.ElseIf)+2)
		finish := func() {
			c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
			c.mergeScopeStates(baseScopeState, branchScopeStates)
		}

		conditionTruthy, conditionKnown := c.inferredConditionTruthiness(typed.Condition)
		trueReachable := !conditionKnown || conditionTruthy
		if trueReachable {
			trueReachable = c.collectRuntimeConditionOutcomeEffects(typed.Condition, true)
		}
		if trueReachable {
			c.checkExpressionWithAuto(function, typed.Consequent, true)
			branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		}
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		falseReachable := !conditionKnown || !conditionTruthy
		if falseReachable {
			falseReachable = c.collectRuntimeConditionOutcomeEffects(typed.Condition, false)
		}
		if !falseReachable {
			finish()
			return
		}
		falseRuntimeState := c.snapshotRuntimeState()
		falseScopeState := c.snapshotScopeState()
		for _, branch := range typed.ElseIf {
			c.restoreRuntimeState(falseRuntimeState)
			c.restoreScopeState(falseScopeState)
			c.checkExpressionWithAuto(function, branch.Condition, true)
			c.collectRuntimeRequireCallExportsFromExpression(branch.Condition)
			conditionRuntimeState = c.snapshotRuntimeState()
			conditionScopeState = c.snapshotScopeState()
			branchTruthy, branchKnown := c.inferredConditionTruthiness(branch.Condition)
			trueReachable = !branchKnown || branchTruthy
			if trueReachable {
				trueReachable = c.collectRuntimeConditionOutcomeEffects(branch.Condition, true)
			}
			if trueReachable {
				c.checkExpressionWithAuto(function, branch.Result, true)
				branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
				branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
			}
			c.restoreRuntimeState(conditionRuntimeState)
			c.restoreScopeState(conditionScopeState)
			falseReachable = !branchKnown || !branchTruthy
			if falseReachable {
				falseReachable = c.collectRuntimeConditionOutcomeEffects(branch.Condition, false)
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
		c.checkExpressionWithAuto(function, typed.Alternate, true)
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		finish()
	case *RangeExpr:
		c.checkExpressionWithAuto(function, typed.Start, true)
		c.checkExpressionWithAuto(function, typed.End, true)
	case *CaseExpr:
		c.checkCaseExpression(function, typed)
	case *BlockLiteral:
		// A standalone block literal is a stabby lambda; its body checks like a
		// call block's. Plain call blocks are checked from the CallExpr case.
		if typed.Lambda {
			c.checkBlockLiteral(function, typed)
		}
		return
	case *YieldExpr:
		for _, arg := range typed.Args {
			c.checkExpressionWithAuto(function, arg, true)
			c.poisonEscapedIdentifier(arg)
		}
	case *InterpolatedString:
		c.checkStringParts(function, typed.Parts)
	case *InterpolatedSymbol:
		c.checkStringParts(function, typed.Parts)
	}
}

func (c *scriptChecker) checkConditionalExpression(function string, expr *ConditionalExpr) {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	c.checkExpressionWithAuto(function, expr.Condition, true)
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
		c.checkExpressionWithAuto(function, expr.Consequent, true)
		c.collectRuntimeRequireCallExportsFromExpression(expr.Consequent)
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
	}

	c.restoreRuntimeState(conditionRuntimeState)
	c.restoreScopeState(conditionScopeState)
	falseReachable := !conditionKnown || !conditionTruthy
	if falseReachable {
		falseReachable = c.collectRuntimeConditionOutcomeEffects(expr.Condition, false)
	}
	if !falseReachable {
		finish()
		return
	}
	c.checkExpressionWithAuto(function, expr.Alternate, true)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Alternate)
	branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
	branchScopeStates = append(branchScopeStates, c.snapshotScopeState())

	finish()
}

func (c *scriptChecker) checkRescueExpression(function string, expr *RescueExpr, autoCall bool) {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	c.checkExpressionWithAuto(function, expr.Body, autoCall)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Body)
	bodyRuntimeState := c.snapshotRuntimeState()
	bodyScopeState := c.snapshotScopeState()

	c.restoreRuntimeState(baseRuntimeState)
	c.restoreScopeState(baseScopeState)
	c.checkExpressionWithAuto(function, expr.Fallback, autoCall)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Fallback)
	fallbackRuntimeState := c.snapshotRuntimeState()
	fallbackScopeState := c.snapshotScopeState()

	c.mergeRuntimeStates(baseRuntimeState, []checkRuntimeState{bodyRuntimeState, fallbackRuntimeState})
	c.mergeScopeStates(baseScopeState, []checkScopeState{bodyScopeState, fallbackScopeState})
}

func (c *scriptChecker) checkCaseExpression(function string, expr *CaseExpr) {
	baseRuntimeState := c.snapshotRuntimeState()
	baseScopeState := c.snapshotScopeState()
	c.checkExpressionWithAuto(function, expr.Target, true)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Target)
	fallthroughRuntimeState := c.snapshotRuntimeState()
	fallthroughScopeState := c.snapshotScopeState()
	branchRuntimeStates := make([]checkRuntimeState, 0, len(expr.Clauses)+1)
	branchScopeStates := make([]checkScopeState, 0, len(expr.Clauses)+1)

	for _, clause := range expr.Clauses {
		for _, value := range clause.Values {
			c.restoreRuntimeState(fallthroughRuntimeState)
			c.restoreScopeState(fallthroughScopeState)
			c.checkExpressionWithAuto(function, value.Expr, true)
			c.collectRuntimeRequireCallExportsFromExpression(value.Expr)
			matchRuntimeState := c.snapshotRuntimeState()
			matchScopeState := c.snapshotScopeState()

			c.checkExpressionWithAuto(function, clause.Result, true)
			c.collectRuntimeRequireCallExportsFromExpression(clause.Result)
			branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
			fallthroughRuntimeState = matchRuntimeState
			fallthroughScopeState = matchScopeState
		}
	}

	c.restoreRuntimeState(fallthroughRuntimeState)
	c.restoreScopeState(fallthroughScopeState)
	c.checkExpressionWithAuto(function, expr.ElseExpr, true)
	c.collectRuntimeRequireCallExportsFromExpression(expr.ElseExpr)
	branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
	branchScopeStates = append(branchScopeStates, c.snapshotScopeState())

	c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
	c.mergeScopeStates(baseScopeState, branchScopeStates)
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

func (c *scriptChecker) checkMemberAutoCall(function string, member *MemberExpr) {
	target, ok := c.resolveMemberCallable(member)
	if !ok {
		return
	}
	view := staticCallView{pos: member.Pos()}
	if target.fn != nil {
		if target.resolution != calleeMemberValue || target.constructor || len(target.fn.Params) == 0 {
			c.checkCallShape(function, view, target.name, target.fn)
		}
		// A bare member read dispatches like a call, so the callee checks
		// under the call-time runtime root.
		c.enqueueReachableFunction(target.name, target.fn)
		return
	}
	if target.spec.autoInvoke {
		c.checkBuiltinCallShape(function, view, target.name, target.spec)
	}
}

func (c *scriptChecker) checkBlockLiteral(function string, block *BlockLiteral) {
	if block == nil {
		return
	}
	runtimeState := c.snapshotRuntimeState()
	defer c.restoreRuntimeState(runtimeState)

	// Block and lambda returns are not checked against the enclosing
	// function's annotation today, so an active begin/ensure deferral must
	// not capture them either (a lambda return is local to the lambda).
	previousSites := c.deferredReturnSites
	c.deferredReturnSites = nil
	defer func() { c.deferredReturnSites = previousSites }()

	// A block may run zero or many times, so outer locals its body assigns
	// lose their facts before the walk, and the walk's own bindings are
	// rolled back afterwards (the degraded state is the correct post-call
	// truth for those locals). Names the block binds itself (parameters,
	// implicit parameters) shadow the outer locals and never write through.
	c.degradeBlockBodyBindings(block)
	typesState := c.snapshotLocalTypes()
	defer c.restoreLocalTypes(typesState)

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
		c.checkExpression(function, param.DefaultVal)
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
		return c.functionMayEvaluateCallBlock(target.fn, seen)
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

func (c *scriptChecker) functionMayEvaluateCallBlock(fn *ScriptFunction, seen map[*ScriptFunction]struct{}) bool {
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

	return c.statementsMayEvaluateCallBlock(fn.Body, seen)
}

func (c *scriptChecker) statementsMayEvaluateCallBlock(statements []Statement, seen map[*ScriptFunction]struct{}) bool {
	for _, stmt := range statements {
		if c.statementMayEvaluateCallBlock(stmt, seen) {
			return true
		}
		if statementAlwaysExits(stmt) {
			return false
		}
	}
	return false
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

func (c *scriptChecker) checkStringParts(function string, parts []StringPart) {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			c.checkExpression(function, exprPart.Expr)
		}
	}
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
		if expressionCanImplicitlyYieldNil(typed.Value) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
			return
		}
		c.checkImplicitLeafAgainstType(function, typed, typed.Value, ty)
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
		for i := range typed.Rescues {
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
	case *ReturnStmt, *RaiseStmt, *BreakStmt, *NextStmt:
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

func (c *scriptChecker) checkCallResolved(function string, call *CallExpr, target staticCallable, ok bool) {
	if !ok {
		return
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
		c.enqueueReachableFunction(target.name, target.fn)
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
}

// checkParseAsShapeArgument reports a JSON.parse_as call whose second
// argument is provably not a shape value — a scalar, a data hash, or any
// other fully known non-shape type — since the runtime always rejects it.
func (c *scriptChecker) checkParseAsShapeArgument(function string, call *CallExpr) {
	if len(call.Args) != 2 || callExpandsArguments(call) {
		return
	}
	raw := call.Args[0]
	rawType, rawCaptured := c.callArgumentFacts[raw]
	if !rawCaptured {
		rawType = c.inferExpressionType(raw)
	}
	if rawType != nil && typeExprsDisjoint(rawType, checkTypeString, c.checkNamedTypeResolver()) {
		c.add(function, raw.Pos(), "call to JSON.parse_as expects a JSON string as its first argument, got %s", formatTypeExpr(rawType))
	}
	arg := call.Args[1]
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
	c.add(function, arg.Pos(), "call to JSON.parse_as expects a shape literal as its second argument, got %s", formatTypeExpr(inferred))
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

func (c *scriptChecker) resolveCallable(call *CallExpr) (staticCallable, bool) {
	switch callee := call.Callee.(type) {
	case *Identifier:
		if c.identifierShadowed(callee.Name) {
			return staticCallable{}, false
		}
		if c.hostGlobalShadows(callee.Name) {
			return c.hostGlobalCallable(callee.Name)
		}
		if fn, ok := c.script.functions[callee.Name]; ok {
			return staticCallable{name: callee.Name, fn: fn, resolution: calleeDirect}, true
		}
		if fn, ok := c.typeRootFunction(callee.Name); ok {
			return staticCallable{name: callee.Name, fn: fn, resolution: calleeDirect}, true
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
	return staticCallable{}, false
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
	// A receiver whose fact pins the dispatch kind resolves member contracts
	// before the identifier paths below: typed locals are scope bindings, so
	// they would otherwise bail at the shadowing guard.
	if target, ok := c.factReceiverMemberCallable(member); ok {
		return target, true
	}
	if ident, ok := member.Object.(*Identifier); ok {
		if c.identifierShadowed(ident.Name) {
			return staticCallable{}, false
		}
		if c.hostGlobalShadows(ident.Name) {
			return c.hostGlobalMemberCallable(ident.Name, member.Property)
		}
		if member.Property == "call" {
			if fn, ok := c.typeRootFunctionValue(ident.Name); ok {
				return staticCallable{name: ident.Name + ".call", fn: fn, resolution: calleeDirect}, true
			}
		}
		if classDef, ok := c.script.classes[ident.Name]; ok {
			if member.Property == "new" {
				if initFn, ok := classDef.Methods["initialize"]; ok {
					return staticCallable{
						name:        ident.Name + ".new",
						fn:          initFn,
						resolution:  calleeMemberValue,
						constructor: true,
					}, true
				}
				return staticCallable{name: ident.Name + ".new", spec: staticCallSpec{minArgs: 0, maxArgs: 0}}, true
			}
			if fn, ok := classDef.ClassMethods[member.Property]; ok {
				return staticCallable{name: ident.Name + "." + member.Property, fn: fn, resolution: calleeMemberMethod}, true
			}
		}
		if fn, ok := c.typeRootObjectFunction(ident.Name, member.Property); ok {
			return staticCallable{name: ident.Name + "." + member.Property, fn: fn, resolution: calleeMemberValue}, true
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

// autoInvokedBuiltinResultFact reports the invariant result type of a bare
// builtin identifier that auto-invokes at runtime (`t = uuid`). The guard
// chain mirrors resolveCallable: any shadowing binding, script function, or
// host override dispatches elsewhere, so no builtin fact applies.
func (c *scriptChecker) autoInvokedBuiltinResultFact(name string) *TypeExpr {
	if c.identifierShadowed(name) || c.hostGlobalShadows(name) {
		return nil
	}
	if _, ok := c.script.functions[name]; ok {
		return nil
	}
	if _, ok := c.typeRootFunction(name); ok {
		return nil
	}
	if c.typeRootHasBinding(name) {
		return nil
	}
	if c.hostBuiltinOverrides(name) {
		return nil
	}
	spec, ok := c.defaultBuiltinCallSpec(name)
	if !ok || !spec.autoInvoke {
		return nil
	}
	return spec.resultType
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
	// A local or parameter that shadows the class name dispatches through the
	// runtime value, not the static class, so the chained call must not be
	// validated against the class. This mirrors the direct receiver path in
	// resolveMemberCallable.
	if c.identifierShadowed(ident.Name) {
		return "", false
	}
	if c.hostGlobalShadows(ident.Name) {
		return "", false
	}
	if _, ok := c.script.classes[ident.Name]; !ok {
		return "", false
	}
	return ident.Name, true
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

var staticMemberSpecs = map[string]staticCallSpec{
	"array.at":     {minArgs: 1, maxArgs: 1, rejectKeywords: true, autoInvoke: true},
	"array.fetch":  {minArgs: 1, maxArgs: 2, autoInvoke: true, usesBlock: true},
	"array.slice":  {minArgs: 1, maxArgs: 2, rejectKeywords: true, autoInvoke: true},
	"string.slice": {minArgs: 1, maxArgs: 2, autoInvoke: true},

	// Callable scalar conversions are nullary auto-invoked builtins with
	// invariant results (members_scalar.go, members_symbol.go,
	// members_string.go, members_numeric.go, members_temporal.go).
	"nil.to_s":        scalarMemberSpec(checkTypeString),
	"nil.string":      scalarMemberSpec(checkTypeString),
	"bool.to_s":       scalarMemberSpec(checkTypeString),
	"bool.string":     scalarMemberSpec(checkTypeString),
	"symbol.id2name":  scalarMemberSpec(checkTypeString),
	"symbol.to_s":     scalarMemberSpec(checkTypeString),
	"symbol.string":   scalarMemberSpec(checkTypeString),
	"symbol.to_sym":   scalarMemberSpec(checkTypeSymbol),
	"string.to_i":     scalarMemberSpec(checkTypeInt),
	"string.to_f":     scalarMemberSpec(checkTypeFloat),
	"string.to_s":     scalarMemberSpec(checkTypeString),
	"string.string":   scalarMemberSpec(checkTypeString),
	"string.to_sym":   scalarMemberSpec(checkTypeSymbol),
	"string.intern":   scalarMemberSpec(checkTypeSymbol),
	"int.to_i":        scalarMemberSpec(checkTypeInt),
	"int.to_f":        scalarMemberSpec(checkTypeFloat),
	"int.to_s":        scalarMemberSpec(checkTypeString),
	"int.string":      scalarMemberSpec(checkTypeString),
	"float.to_i":      scalarMemberSpec(checkTypeInt),
	"float.to_f":      scalarMemberSpec(checkTypeFloat),
	"float.to_s":      scalarMemberSpec(checkTypeString),
	"float.string":    scalarMemberSpec(checkTypeString),
	"money.to_s":      scalarMemberSpec(checkTypeString),
	"money.string":    scalarMemberSpec(checkTypeString),
	"duration.to_s":   scalarMemberSpec(checkTypeString),
	"duration.string": scalarMemberSpec(checkTypeString),
	// Temporal eql? methods own dispatch ahead of the universal fallback.
	// They reject keywords but intentionally ignore a supplied block.
	"duration.eql?": {minArgs: 1, maxArgs: 1, rejectKeywords: true, resultType: checkTypeBool},
	"time.to_s":     scalarMemberSpec(checkTypeString),
	"time.string":   scalarMemberSpec(checkTypeString),
	"time.eql?":     {minArgs: 1, maxArgs: 1, rejectKeywords: true, resultType: checkTypeBool},
	// range.to_a ignores a block at runtime, so it cannot use the stricter
	// scalarMemberSpec contract shared by the other conversion builtins.
	"range.to_a": {minArgs: 0, maxArgs: 0, rejectKeywords: true, autoInvoke: true, resultType: checkTypeIntArray},
}

// scalarMemberSpec is the contract shared by the nullary scalar conversion
// members: no arguments, no keywords, no block, auto-invoked on a bare read.
func scalarMemberSpec(result *TypeExpr) staticCallSpec {
	return staticCallSpec{minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: result}
}

// staticMemberValueTypes records conversion-style temporal members that the
// runtime exposes as direct values rather than builtins. They contribute a
// result fact to a bare member read, but deliberately stay outside
// staticMemberSpecs: `d.to_i()` attempts to call the returned int at runtime,
// so it must not resolve as a conversion callable.
var staticMemberValueTypes = map[string]*TypeExpr{
	"duration.to_i": checkTypeInt,
	"time.to_i":     checkTypeInt,
	"time.tv_sec":   checkTypeInt,
	"time.to_f":     checkTypeFloat,
	"time.to_r":     checkTypeFloat,
	"time.to_a":     checkTypeArray,
}

// universalMemberSpecs are the Object-level predicates with fixed boolean
// results (members_universal.go). They apply only when every known receiver
// arm dispatches them through the runtime universal fallback — no class
// instances, whose user methods take precedence.
var universalMemberSpecs = map[string]staticCallSpec{
	"nil?":         {minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
	"frozen?":      {minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
	"eql?":         {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, resultType: checkTypeBool},
	"equal?":       {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, resultType: checkTypeBool},
	"respond_to?":  {minArgs: 1, maxArgs: 2, rejectKeywords: true, rejectBlock: true, autoInvoke: true, paramTypes: []*TypeExpr{checkTypeMethodName, checkTypeBool}, resultType: checkTypeBool},
	"is_a?":        {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
	"kind_of?":     {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
	"instance_of?": {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
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
	if !staticCallCollapsesOptionsHash(call, target, view) {
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

func staticCallCollapsesOptionsHash(call *CallExpr, target staticCallable, view staticCallView) bool {
	if !call.KeywordOptionsHash || len(call.KwArgs) == 0 || target.fn == nil {
		return false
	}
	if call.Parenthesized && !target.constructor && target.resolution == calleeMemberMethod {
		return false
	}
	return functionCanReceiveOptionsHash(target.fn, len(view.args), func(name string) bool {
		for _, kwarg := range view.kwargs {
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
		for _, kwarg := range call.kwargs {
			if !usedKw[kwarg.Name] {
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
						c.add(function, rest.Value.Pos(), "call to %s argument %s expected %s, got keyword %s",
							callName, paramName, formatTypeExpr(ty), rest.Name)
						continue
					}
					c.checkInferredArgument(function, rest.Value, fieldType, callName, paramName)
				}
				// Exact shapes require every field, so an absent keyword is a
				// known normalization failure.
				missingPos := warningPos
				if missingPos == (Position{}) {
					missingPos = pos
				}
				fields := make([]string, 0, len(ty.Shape))
				for field := range ty.Shape {
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
	for i, kwarg := range call.kwargs {
		if kwarg.Name == name {
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
	c.liveLocalNames = append(c.liveLocalNames, nil)
	return func() {
		c.scopes = c.scopes[:len(c.scopes)-1]
		c.localTypes = c.localTypes[:len(c.localTypes)-1]
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
	memberName, ok := runtimeNamespaceMemberName(target)
	if !ok {
		return
	}
	if c.runtimeNamespaceMembers == nil {
		c.runtimeNamespaceMembers = make(map[string]struct{})
	}
	c.runtimeNamespaceMembers[memberName] = struct{}{}
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
