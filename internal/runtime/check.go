package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// CheckWarning describes a statically checkable contract issue.
type CheckWarning struct {
	Function string
	Pos      Position
	Message  string
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
	if target.Function == "" {
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
	localNameUnions         []map[string]struct{}
	nameFactsCache          *checkNameFacts
	selfScopeFns            map[*ScriptFunction]struct{}
	orderIndependentOnly    bool
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
	popScope := c.pushScope(make(map[string]struct{}))
	defer popScope()

	for _, param := range fn.Params {
		c.collectRequiredModuleExportsFromExpression(param.DefaultVal)
		c.recordParamBinding(param)
	}
	c.collectRequiredModuleExportsFromStatements(fn.Body)
}

func (c *scriptChecker) collectRequiredModuleExportsFromStatements(statements []Statement) {
	for _, stmt := range statements {
		c.collectRequiredModuleExportsFromStatement(stmt)
		if statementAlwaysExits(stmt) {
			return
		}
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromStatement(stmt Statement) {
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
		c.recordBindingTarget(typed.Target)
	case *ExprStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Expr)
	case *LogicalStmt:
		c.collectRequiredModuleExportsFromStatement(typed.Left)
		leftState := c.snapshotModuleCollectionState()
		leftScopeState := c.snapshotScopeState()
		if logicalStatementRightMayEvaluate(typed) {
			c.collectRequiredModuleExportsFromStatement(typed.Right)
			if !logicalStatementRightAlwaysEvaluates(typed) {
				c.restoreModuleCollectionState(leftState)
				c.restoreScopeState(leftScopeState)
				c.recordLocalBindings([]Statement{typed})
			}
		}
	case *IfStmt:
		baseState := c.snapshotModuleCollectionState()
		baseScopeState := c.snapshotScopeState()
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		falseState := c.snapshotModuleCollectionState()
		falseScopeState := c.snapshotScopeState()
		fallthroughStates := make([]checkModuleCollectionState, 0, len(typed.ElseIf)+2)
		fallthroughScopeStates := make([]checkScopeState, 0, len(typed.ElseIf)+2)

		conditionTruthy, conditionKnown := staticExpressionTruthiness(typed.Condition)
		if !conditionKnown || conditionTruthy {
			c.collectRequiredModuleExportsFromStatements(typed.Consequent)
			if !blockAlwaysExits(typed.Consequent) {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
			if conditionKnown {
				c.mergeModuleCollectionStates(baseState, fallthroughStates)
				c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
				return
			}
		}
		for _, elseIf := range typed.ElseIf {
			c.restoreModuleCollectionState(falseState)
			c.restoreScopeState(falseScopeState)
			c.collectRequiredModuleExportsFromExpression(elseIf.Condition)
			falseState = c.snapshotModuleCollectionState()
			falseScopeState = c.snapshotScopeState()
			branchTruthy, branchKnown := staticExpressionTruthiness(elseIf.Condition)
			if !branchKnown || branchTruthy {
				c.collectRequiredModuleExportsFromStatements(elseIf.Consequent)
				if !blockAlwaysExits(elseIf.Consequent) {
					fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
					fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
				}
				if branchKnown {
					c.mergeModuleCollectionStates(baseState, fallthroughStates)
					c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
					return
				}
			}
		}
		c.restoreModuleCollectionState(falseState)
		c.restoreScopeState(falseScopeState)
		c.collectRequiredModuleExportsFromStatements(typed.Alternate)
		if len(typed.Alternate) == 0 || !blockAlwaysExits(typed.Alternate) {
			fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
			fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
		}
		c.mergeModuleCollectionStates(baseState, fallthroughStates)
		c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
	case *ForStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Iterable)
		c.recordBindingTarget(typed.Target)
		bodyState := c.snapshotModuleCollectionState()
		bodyScopeState := c.snapshotScopeState()
		c.collectRequiredModuleExportsFromStatements(typed.Body)
		c.restoreModuleCollectionState(bodyState)
		c.restoreScopeState(bodyScopeState)
		c.recordLocalBindings(typed.Body)
	case *WhileStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		bodyState := c.snapshotModuleCollectionState()
		bodyScopeState := c.snapshotScopeState()
		c.collectRequiredModuleExportsFromStatements(typed.Body)
		c.restoreModuleCollectionState(bodyState)
		c.restoreScopeState(bodyScopeState)
		c.recordLocalBindings(typed.Body)
	case *UntilStmt:
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		bodyState := c.snapshotModuleCollectionState()
		bodyScopeState := c.snapshotScopeState()
		c.collectRequiredModuleExportsFromStatements(typed.Body)
		c.restoreModuleCollectionState(bodyState)
		c.restoreScopeState(bodyScopeState)
		c.recordLocalBindings(typed.Body)
	case *TryStmt:
		baseState := c.snapshotModuleCollectionState()
		baseScopeState := c.snapshotScopeState()
		fallthroughStates := make([]checkModuleCollectionState, 0, 2)
		fallthroughScopeStates := make([]checkScopeState, 0, 2)

		c.collectRequiredModuleExportsFromStatements(typed.Body)
		if !blockAlwaysExits(typed.Body) {
			c.collectRequiredModuleExportsFromStatements(typed.Else)
			if len(typed.Else) == 0 || !blockAlwaysExits(typed.Else) {
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
			c.collectRequiredModuleExportsFromStatements(clause.Body)
			popScope()
			if !blockAlwaysExits(clause.Body) {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		c.mergeModuleCollectionStates(baseState, fallthroughStates)
		c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
		c.collectRequiredModuleExportsFromStatements(typed.Ensure)
	case *ClassStmt:
		c.collectRequiredModuleExportsFromClassBody(typed.Body)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromClassBody(body []Statement) {
	if len(body) == 0 {
		return
	}
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
		c.collectRequiredModuleExportsFromExpression(typed.Callee)
		if staticNilSafeNavigationCall(typed) {
			return
		}
		if safeNavigationCallMaySkipArguments(typed) {
			baseState := c.snapshotModuleCollectionState()
			c.collectRequiredModuleExportsFromCallArguments(typed)
			callState := c.snapshotModuleCollectionState()
			c.mergeModuleCollectionStates(baseState, []checkModuleCollectionState{baseState, callState})
		} else {
			c.collectRequiredModuleExportsFromCallArguments(typed)
		}
	case *MemberExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Object)
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
	case *UnaryExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Right)
	case *BinaryExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Left)
		if binaryRightAlwaysEvaluates(typed) {
			c.collectRequiredModuleExportsFromExpression(typed.Right)
		}
	case *ConditionalExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		if branch, ok := staticConditionalExpressionBranch(typed); ok {
			c.collectRequiredModuleExportsFromExpression(branch)
		} else {
			c.collectRequiredModuleExportsFromExpressionBranches(typed.Consequent, typed.Alternate)
		}
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
		baseState := c.snapshotModuleCollectionState()
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		falseState := c.snapshotModuleCollectionState()
		branchStates := make([]checkModuleCollectionState, 0, len(typed.ElseIf)+2)

		conditionTruthy, conditionKnown := staticExpressionTruthiness(typed.Condition)
		if !conditionKnown || conditionTruthy {
			c.collectRequiredModuleExportsFromExpression(typed.Consequent)
			branchStates = append(branchStates, c.snapshotModuleCollectionState())
			if conditionKnown {
				c.mergeModuleCollectionStates(baseState, branchStates)
				return
			}
		}
		for _, branch := range typed.ElseIf {
			c.restoreModuleCollectionState(falseState)
			c.collectRequiredModuleExportsFromExpression(branch.Condition)
			falseState = c.snapshotModuleCollectionState()
			branchTruthy, branchKnown := staticExpressionTruthiness(branch.Condition)
			if !branchKnown || branchTruthy {
				c.collectRequiredModuleExportsFromExpression(branch.Result)
				branchStates = append(branchStates, c.snapshotModuleCollectionState())
				if branchKnown {
					c.mergeModuleCollectionStates(baseState, branchStates)
					return
				}
			}
		}
		c.restoreModuleCollectionState(falseState)
		c.collectRequiredModuleExportsFromExpression(typed.Alternate)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
		c.mergeModuleCollectionStates(baseState, branchStates)
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

func (c *scriptChecker) collectRequiredModuleExportsFromCallArguments(call *CallExpr) {
	for _, arg := range call.Args {
		c.collectRequiredModuleExportsFromExpression(arg)
	}
	for _, kwarg := range call.KwArgs {
		c.collectRequiredModuleExportsFromExpression(kwarg.Value)
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromExpressionBranches(branches ...Expression) {
	baseState := c.snapshotModuleCollectionState()
	branchStates := make([]checkModuleCollectionState, 0, len(branches))
	for _, branch := range branches {
		c.restoreModuleCollectionState(baseState)
		c.collectRequiredModuleExportsFromExpression(branch)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
	}
	c.mergeModuleCollectionStates(baseState, branchStates)
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

func logicalStatementRightAlwaysEvaluates(stmt *LogicalStmt) bool {
	if stmt == nil || statementAlwaysExits(stmt.Left) {
		return false
	}
	val, ok := staticStatementValue(stmt.Left)
	if !ok {
		return false
	}
	switch stmt.Operator {
	case tokenWordAnd:
		return val.Truthy()
	case tokenWordOr:
		return !val.Truthy()
	default:
		return false
	}
}

func logicalStatementRightMayEvaluate(stmt *LogicalStmt) bool {
	if stmt == nil || statementAlwaysExits(stmt.Left) {
		return false
	}
	val, ok := staticStatementValue(stmt.Left)
	if !ok {
		return true
	}
	switch stmt.Operator {
	case tokenWordAnd:
		return val.Truthy()
	case tokenWordOr:
		return !val.Truthy()
	default:
		return true
	}
}

func staticStatementValue(stmt Statement) (Value, bool) {
	switch typed := stmt.(type) {
	case *ExprStmt:
		return staticLiteralValue(typed.Expr)
	case *AssignStmt:
		return staticLiteralValue(typed.Value)
	default:
		return NewNil(), false
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
	entry, err := c.script.engine.loadModule(moduleName, c.moduleCaller)
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
	if call == nil || len(call.Args) != 1 || call.Block != nil || c.requireCallShadowed() {
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
	c.moduleCaller = &caller
	c.scopes = nil
	defer func() {
		c.moduleCaller = previousCaller
		c.scopes = previousScopes
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
	for _, fn := range c.sortedScriptFunctions() {
		c.withFreshRuntimeTypeRootForCallable(fn, func() {
			c.checkFunction(fn.Name, fn)
		})
	}
	c.withFreshRuntimeTypeRoot(func() {
		c.checkRuntimeClassBodies(c.script.deferredClassBodies, false)
	})
	for _, classDef := range c.sortedClasses() {
		for _, method := range sortedCheckFunctions(classDef.Methods) {
			c.withFreshRuntimeTypeRootForCallable(method, func() {
				c.checkFunction(classDef.Name+"#"+method.Name, method)
			})
		}
		for _, method := range sortedCheckFunctions(classDef.ClassMethods) {
			c.withFreshRuntimeTypeRootForCallable(method, func() {
				c.checkFunction(classDef.Name+"."+method.Name, method)
			})
		}
	}
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
	popScope := c.pushScope(make(map[string]struct{}))
	defer popScope()
	popNameScope := c.pushFunctionNameScope(fn)
	defer popNameScope()

	var usedKw map[string]bool
	if len(kwargs) > 0 {
		usedKw = make(map[string]bool, len(kwargs))
	}
	argIdx := 0
	for _, param := range fn.Params {
		switch param.Kind {
		case ParamNormal:
			if argIdx < len(args) {
				c.checkArgumentValue(label, fn.Pos, args[argIdx], param.Type, fn.Name, param.Name)
				argIdx++
			} else if val, ok := kwargs[param.Name]; ok {
				c.checkArgumentValue(label, fn.Pos, val, param.Type, fn.Name, param.Name)
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else {
				c.checkParamDefault(label, param)
			}
		case ParamKeyword:
			if val, ok := kwargs[param.Name]; ok {
				c.checkArgumentValue(label, fn.Pos, val, param.Type, fn.Name, param.Name)
				if usedKw != nil {
					usedKw[param.Name] = true
				}
			} else {
				c.checkParamDefault(label, param)
			}
		case ParamRest:
			c.checkRestArgumentValues(label, fn.Pos, args[argIdx:], param.Type, fn.Name, param.Name)
			argIdx = len(args)
		case ParamKeywordRest:
			c.checkKeywordRestArgumentValues(label, fn.Pos, kwargs, usedKw, param.Type, fn.Name, param.Name)
			for _, name := range sortedValueKeywordNames(kwargs) {
				if usedKw != nil {
					usedKw[name] = true
				}
			}
		case ParamBlock:
			c.checkBlockArgumentValue(label, fn.Pos, nil, param.Type, fn.Name, param.Name)
		}
		c.recordParamBinding(param)
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
	for len(c.reachableFuncQueue) > 0 {
		next := c.reachableFuncQueue[0]
		c.reachableFuncQueue = c.reachableFuncQueue[1:]
		scopeState := c.snapshotScopeState()
		c.restoreRuntimeState(next.runtimeState)
		c.scopes = nil
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
		popScope := c.pushScope(make(map[string]struct{}))
		defer popScope()
		// Class bodies run with self bound to the class, so bare identifiers
		// can resolve through implicit self members the checker cannot see.
		previousSelf := c.selfScope
		c.selfScope = true
		defer func() { c.selfScope = previousSelf }()
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

type checkScopeState []map[string]struct{}

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
	if len(c.scopes) == 0 {
		return nil
	}
	state := make(checkScopeState, len(c.scopes))
	for i, scope := range c.scopes {
		state[i] = cloneCheckScope(scope)
	}
	return state
}

func (c *scriptChecker) restoreScopeState(state checkScopeState) {
	if len(state) == 0 {
		c.scopes = nil
		return
	}
	c.scopes = make([]map[string]struct{}, len(state))
	for i, scope := range state {
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
		if i >= len(states[0]) {
			continue
		}
		common := cloneCheckScope(states[0][i])
		for _, state := range states[1:] {
			if i >= len(state) {
				clear(common)
				break
			}
			for key := range common {
				if _, ok := state[i][key]; !ok {
					delete(common, key)
				}
			}
		}
		if i < len(base) {
			for key := range base[i] {
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
}

func (c *scriptChecker) checkStatements(function string, returnType *TypeExpr, statements []Statement) {
	for _, stmt := range statements {
		c.checkStatement(function, returnType, stmt)
		if statementAlwaysExits(stmt) {
			return
		}
	}
}

func (c *scriptChecker) checkStatement(function string, returnType *TypeExpr, stmt Statement) {
	switch typed := stmt.(type) {
	case nil:
		return
	case *ReturnStmt:
		if returnType != nil {
			c.checkReturnStatementType(function, returnType, typed)
		}
		c.checkExpression(function, typed.Value)
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
		c.recordRuntimeBindingTarget(typed.Target)
		c.recordBindingTarget(typed.Target)
	case *ExprStmt:
		c.checkExpression(function, typed.Expr)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Expr)
	case *ClassStmt:
		classDef := c.script.classes[typed.Name]
		if classDef != nil {
			c.checkRuntimeClassBody(classDef, false)
		}
	case *LogicalStmt:
		c.checkStatement(function, returnType, typed.Left)
		leftRuntimeState := c.snapshotRuntimeState()
		leftScopeState := c.snapshotScopeState()
		if logicalStatementRightMayEvaluate(typed) {
			c.checkStatement(function, returnType, typed.Right)
			if !logicalStatementRightAlwaysEvaluates(typed) {
				c.restoreRuntimeState(leftRuntimeState)
				c.restoreScopeState(leftScopeState)
				c.recordLocalBindings([]Statement{typed})
			}
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

		conditionTruthy, conditionKnown := staticExpressionTruthiness(typed.Condition)
		if !conditionKnown || conditionTruthy {
			c.collectRuntimeConditionOutcomeEffects(typed.Condition, true)
			c.checkStatements(function, returnType, typed.Consequent)
			if !blockAlwaysExits(typed.Consequent) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
			if conditionKnown {
				c.mergeRuntimeStates(baseRuntimeState, fallthroughRuntimeStates)
				c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
				return
			}
		}
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		c.collectRuntimeConditionOutcomeEffects(typed.Condition, false)
		falseRuntimeState := c.snapshotRuntimeState()
		falseScopeState := c.snapshotScopeState()
		for _, elseIf := range typed.ElseIf {
			c.restoreRuntimeState(falseRuntimeState)
			c.restoreScopeState(falseScopeState)
			c.checkExpression(function, elseIf.Condition)
			c.collectRuntimeRequireCallExportsFromExpression(elseIf.Condition)
			conditionRuntimeState = c.snapshotRuntimeState()
			conditionScopeState = c.snapshotScopeState()
			branchTruthy, branchKnown := staticExpressionTruthiness(elseIf.Condition)
			if !branchKnown || branchTruthy {
				c.collectRuntimeConditionOutcomeEffects(elseIf.Condition, true)
				c.checkStatements(function, returnType, elseIf.Consequent)
				if !blockAlwaysExits(elseIf.Consequent) {
					fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
					fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
				}
				if branchKnown {
					c.mergeRuntimeStates(baseRuntimeState, fallthroughRuntimeStates)
					c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
					return
				}
			}
			c.restoreRuntimeState(conditionRuntimeState)
			c.restoreScopeState(conditionScopeState)
			c.collectRuntimeConditionOutcomeEffects(elseIf.Condition, false)
			falseRuntimeState = c.snapshotRuntimeState()
			falseScopeState = c.snapshotScopeState()
		}
		c.restoreRuntimeState(falseRuntimeState)
		c.restoreScopeState(falseScopeState)
		c.checkStatements(function, returnType, typed.Alternate)
		if len(typed.Alternate) == 0 || !blockAlwaysExits(typed.Alternate) {
			fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
			fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
		}
		c.mergeRuntimeStates(baseRuntimeState, fallthroughRuntimeStates)
		c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
	case *ForStmt:
		c.checkExpression(function, typed.Iterable)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Iterable)
		c.recordBindingTarget(typed.Target)
		bodyRuntimeState := c.snapshotRuntimeState()
		bodyScopeState := c.snapshotScopeState()
		c.checkStatements(function, returnType, typed.Body)
		c.restoreRuntimeState(bodyRuntimeState)
		c.restoreScopeState(bodyScopeState)
		c.recordLocalBindings(typed.Body)
	case *WhileStmt:
		c.checkExpression(function, typed.Condition)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Condition)
		bodyRuntimeState := c.snapshotRuntimeState()
		bodyScopeState := c.snapshotScopeState()
		c.checkStatements(function, returnType, typed.Body)
		c.restoreRuntimeState(bodyRuntimeState)
		c.restoreScopeState(bodyScopeState)
		c.recordLocalBindings(typed.Body)
	case *UntilStmt:
		c.checkExpression(function, typed.Condition)
		c.collectRuntimeRequireCallExportsFromExpression(typed.Condition)
		bodyRuntimeState := c.snapshotRuntimeState()
		bodyScopeState := c.snapshotScopeState()
		c.checkStatements(function, returnType, typed.Body)
		c.restoreRuntimeState(bodyRuntimeState)
		c.restoreScopeState(bodyScopeState)
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
		deferredReturnChecks := make([]deferredReturnCheck, 0, 2)

		c.checkStatements(function, branchReturnType, typed.Body)
		if deferReturnType && blockMayReturn(typed.Body) {
			deferredReturnChecks = append(deferredReturnChecks, deferredReturnCheck{
				runtimeState: c.snapshotRuntimeState(),
				statements:   typed.Body,
			})
		}
		if !blockAlwaysExits(typed.Body) {
			c.checkStatements(function, branchReturnType, typed.Else)
			if deferReturnType && blockMayReturn(typed.Else) {
				deferredReturnChecks = append(deferredReturnChecks, deferredReturnCheck{
					runtimeState: c.snapshotRuntimeState(),
					statements:   typed.Else,
				})
			}
			if len(typed.Else) == 0 || !blockAlwaysExits(typed.Else) {
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
			c.checkStatements(function, branchReturnType, clause.Body)
			popScope()
			popEarlier()
			clauseLocals := map[string]struct{}{}
			collectLocalBindings(clause.Body, clauseLocals)
			delete(clauseLocals, clause.Binding)
			for name := range clauseLocals {
				earlierClauseLocals[name] = struct{}{}
			}
			if deferReturnType && blockMayReturn(clause.Body) {
				deferredReturnChecks = append(deferredReturnChecks, deferredReturnCheck{
					runtimeState: c.snapshotRuntimeState(),
					statements:   clause.Body,
				})
			}
			if !blockAlwaysExits(clause.Body) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		mergeRuntimeStates := fallthroughRuntimeStates
		mergeScopeStates := fallthroughScopeStates
		if deferReturnType && len(mergeRuntimeStates) == 0 && len(deferredReturnChecks) > 0 {
			mergeRuntimeStates = make([]checkRuntimeState, 0, len(deferredReturnChecks))
			mergeScopeStates = nil
			for _, check := range deferredReturnChecks {
				mergeRuntimeStates = append(mergeRuntimeStates, check.runtimeState)
			}
		}
		c.mergeRuntimeStates(baseRuntimeState, mergeRuntimeStates)
		c.mergeScopeStates(baseScopeState, mergeScopeStates)
		c.checkStatements(function, returnType, typed.Ensure)
		if deferReturnType {
			c.checkDeferredReturnsAfterEnsure(function, returnType, typed.Ensure, deferredReturnChecks)
		}
	}
}

type deferredReturnCheck struct {
	runtimeState checkRuntimeState
	statements   []Statement
}

func (c *scriptChecker) checkDeferredReturnsAfterEnsure(function string, returnType *TypeExpr, ensure []Statement, checks []deferredReturnCheck) {
	runtimeState := c.snapshotRuntimeState()
	scopeState := c.snapshotScopeState()
	defer func() {
		c.restoreRuntimeState(runtimeState)
		c.restoreScopeState(scopeState)
	}()

	for _, check := range checks {
		c.restoreRuntimeState(check.runtimeState)
		c.withSuppressedWarnings(func() {
			c.withRuntimeModuleCollection(func() {
				c.collectRequiredModuleExportsFromStatements(ensure)
			})
		})
		c.checkDeferredReturnTypes(function, returnType, check.statements)
	}
}

func (c *scriptChecker) collectRuntimeConditionOutcomeEffects(expr Expression, truthy bool) {
	switch typed := expr.(type) {
	case *BinaryExpr:
		switch typed.Operator {
		case tokenAnd:
			if truthy {
				c.collectRuntimeRequireCallExportsFromExpression(typed.Right)
				c.collectRuntimeConditionOutcomeEffects(typed.Left, true)
				c.collectRuntimeConditionOutcomeEffects(typed.Right, true)
			} else if binaryRightAlwaysEvaluates(typed) {
				c.collectRuntimeConditionOutcomeEffects(typed.Right, false)
			}
		case tokenOr:
			if !truthy {
				c.collectRuntimeRequireCallExportsFromExpression(typed.Right)
				c.collectRuntimeConditionOutcomeEffects(typed.Left, false)
				c.collectRuntimeConditionOutcomeEffects(typed.Right, false)
			} else if binaryRightAlwaysEvaluates(typed) {
				c.collectRuntimeConditionOutcomeEffects(typed.Right, true)
			}
		}
	}
}

func (c *scriptChecker) checkDeferredReturnTypes(function string, returnType *TypeExpr, statements []Statement) {
	if returnType == nil {
		return
	}
	for _, stmt := range statements {
		c.checkDeferredReturnTypeStatement(function, returnType, stmt)
		if statementAlwaysExits(stmt) {
			return
		}
	}
}

func (c *scriptChecker) checkDeferredReturnTypeStatement(function string, returnType *TypeExpr, stmt Statement) {
	switch typed := stmt.(type) {
	case nil:
		return
	case *ReturnStmt:
		c.checkReturnStatementType(function, returnType, typed)
	case *LogicalStmt:
		c.checkDeferredReturnTypeStatement(function, returnType, typed.Left)
		if logicalStatementRightMayEvaluate(typed) {
			c.checkDeferredReturnTypeStatement(function, returnType, typed.Right)
		}
	case *IfStmt:
		c.checkDeferredReturnTypes(function, returnType, typed.Consequent)
		for _, elseIf := range typed.ElseIf {
			c.checkDeferredReturnTypes(function, returnType, elseIf.Consequent)
		}
		c.checkDeferredReturnTypes(function, returnType, typed.Alternate)
	case *ForStmt:
		c.checkDeferredReturnTypes(function, returnType, typed.Body)
	case *WhileStmt:
		c.checkDeferredReturnTypes(function, returnType, typed.Body)
	case *UntilStmt:
		c.checkDeferredReturnTypes(function, returnType, typed.Body)
	case *TryStmt:
		c.checkDeferredReturnTypes(function, returnType, typed.Body)
		c.checkDeferredReturnTypes(function, returnType, typed.Else)
		for i := range typed.Rescues {
			c.checkDeferredReturnTypes(function, returnType, typed.Rescues[i].Body)
		}
		c.checkDeferredReturnTypes(function, returnType, typed.Ensure)
	}
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
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			c.checkExpressionWithAuto(function, elem, true)
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			c.checkExpressionWithAuto(function, pair.Key, true)
			c.checkExpressionWithAuto(function, pair.Value, true)
		}
	case *CallExpr:
		c.checkExpressionWithAuto(function, typed.Callee, false)
		if staticNilSafeNavigationCall(typed) {
			return
		}
		argumentsMayBeSkipped := safeNavigationCallMaySkipArguments(typed)
		var argumentState checkRuntimeState
		if argumentsMayBeSkipped {
			argumentState = c.snapshotRuntimeState()
		}
		c.collectRuntimeCallArgumentEffects(typed)
		c.checkCall(function, typed)
		for _, arg := range typed.Args {
			c.checkExpressionWithAuto(function, arg, true)
		}
		for _, kwarg := range typed.KwArgs {
			c.checkExpressionWithAuto(function, kwarg.Value, true)
		}
		if c.callMayEvaluateBlock(typed) {
			c.checkLiteralArrayBlockParamTypes(function, typed)
			c.checkBlockLiteral(function, typed.Block)
		}
		if argumentsMayBeSkipped {
			c.restoreRuntimeState(argumentState)
		}
	case *MemberExpr:
		c.checkExpressionWithAuto(function, typed.Object, true)
		if autoCall {
			c.checkMemberAutoCall(function, typed)
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
	case *UnaryExpr:
		c.checkExpressionWithAuto(function, typed.Right, true)
	case *BinaryExpr:
		c.checkExpressionWithAuto(function, typed.Left, true)
		if binaryRightMayEvaluate(typed) {
			state := c.snapshotRuntimeState()
			c.collectRuntimeRequireCallExportsFromExpression(typed.Left)
			c.checkExpressionWithAuto(function, typed.Right, true)
			c.restoreRuntimeState(state)
		}
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

		conditionTruthy, conditionKnown := staticExpressionTruthiness(typed.Condition)
		if !conditionKnown || conditionTruthy {
			c.collectRuntimeConditionOutcomeEffects(typed.Condition, true)
			c.checkExpressionWithAuto(function, typed.Consequent, true)
			branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
			branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
			if conditionKnown {
				c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
				c.mergeScopeStates(baseScopeState, branchScopeStates)
				return
			}
		}
		c.restoreRuntimeState(conditionRuntimeState)
		c.restoreScopeState(conditionScopeState)
		c.collectRuntimeConditionOutcomeEffects(typed.Condition, false)
		falseRuntimeState := c.snapshotRuntimeState()
		falseScopeState := c.snapshotScopeState()
		for _, branch := range typed.ElseIf {
			c.restoreRuntimeState(falseRuntimeState)
			c.restoreScopeState(falseScopeState)
			c.checkExpressionWithAuto(function, branch.Condition, true)
			c.collectRuntimeRequireCallExportsFromExpression(branch.Condition)
			conditionRuntimeState = c.snapshotRuntimeState()
			conditionScopeState = c.snapshotScopeState()
			branchTruthy, branchKnown := staticExpressionTruthiness(branch.Condition)
			if !branchKnown || branchTruthy {
				c.collectRuntimeConditionOutcomeEffects(branch.Condition, true)
				c.checkExpressionWithAuto(function, branch.Result, true)
				branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
				branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
				if branchKnown {
					c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
					c.mergeScopeStates(baseScopeState, branchScopeStates)
					return
				}
			}
			c.restoreRuntimeState(conditionRuntimeState)
			c.restoreScopeState(conditionScopeState)
			c.collectRuntimeConditionOutcomeEffects(branch.Condition, false)
			falseRuntimeState = c.snapshotRuntimeState()
			falseScopeState = c.snapshotScopeState()
		}
		c.restoreRuntimeState(falseRuntimeState)
		c.restoreScopeState(falseScopeState)
		c.checkExpressionWithAuto(function, typed.Alternate, true)
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
		c.mergeScopeStates(baseScopeState, branchScopeStates)
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

	conditionTruthy, conditionKnown := staticExpressionTruthiness(expr.Condition)
	if !conditionKnown || conditionTruthy {
		c.collectRuntimeConditionOutcomeEffects(expr.Condition, true)
		c.checkExpressionWithAuto(function, expr.Consequent, true)
		c.collectRuntimeRequireCallExportsFromExpression(expr.Consequent)
		branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
		branchScopeStates = append(branchScopeStates, c.snapshotScopeState())
		if conditionKnown {
			c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
			c.mergeScopeStates(baseScopeState, branchScopeStates)
			return
		}
	}

	c.restoreRuntimeState(conditionRuntimeState)
	c.restoreScopeState(conditionScopeState)
	c.collectRuntimeConditionOutcomeEffects(expr.Condition, false)
	c.checkExpressionWithAuto(function, expr.Alternate, true)
	c.collectRuntimeRequireCallExportsFromExpression(expr.Alternate)
	branchRuntimeStates = append(branchRuntimeStates, c.snapshotRuntimeState())
	branchScopeStates = append(branchScopeStates, c.snapshotScopeState())

	c.mergeRuntimeStates(baseRuntimeState, branchRuntimeStates)
	c.mergeScopeStates(baseScopeState, branchScopeStates)
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

func (c *scriptChecker) collectRuntimeCallArgumentEffects(call *CallExpr) {
	for _, arg := range call.Args {
		c.collectRuntimeRequireCallExportsFromExpression(arg)
	}
	for _, kwarg := range call.KwArgs {
		c.collectRuntimeRequireCallExportsFromExpression(kwarg.Value)
	}
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

	popScope := c.pushBlockCheckScope(block)
	defer popScope()
	popNameScope := c.pushBlockNameScope(block)
	defer popNameScope()

	for _, param := range block.Params {
		c.checkRuntimeTypeAnnotation(function, param.Type)
		c.checkDestructureTargetTypeAnnotations(function, param.Target)
		c.checkExpression(function, param.DefaultVal)
	}
	label := fmt.Sprintf("%s block at %d:%d", function, block.Pos().Line, block.Pos().Column)
	c.checkStatements(label, nil, block.Body)
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
	case *NextStmt:
		return expressionMayEscapeIteration(typed.Value)
	case *AssignStmt:
		return expressionMayEscapeIteration(typed.Target) || expressionMayEscapeIteration(typed.Value)
	case *ExprStmt:
		return expressionMayEscapeIteration(typed.Expr)
	case *LogicalStmt:
		return statementMayEscapeIteration(typed.Left) || statementMayEscapeIteration(typed.Right)
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
		return typed.Block != nil && expressionMayEscapeIteration(typed.Block)
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
	case *LogicalStmt:
		return c.statementMayEvaluateCallBlock(typed.Left, seen) ||
			(logicalStatementRightMayEvaluate(typed) && c.statementMayEvaluateCallBlock(typed.Right, seen))
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
		c.checkRuntimeExpressionAgainstType(function, typed.Expr, ty, "return value")
	case *AssignStmt:
		if expressionCanImplicitlyYieldNil(typed.Value) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
			return
		}
		c.checkRuntimeExpressionAgainstType(function, typed.Value, ty, "return value")
	case *LogicalStmt:
		c.checkImplicitFinalLogicalStatement(function, ty, typed)
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
	truthy, known := staticExpressionTruthiness(stmt.Condition)
	if !known || truthy {
		c.checkImplicitFinalBlock(function, ty, stmt.Consequent, stmt.Pos())
		if known {
			return
		}
	}
	for _, elseIf := range stmt.ElseIf {
		truthy, known = staticExpressionTruthiness(elseIf.Condition)
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

func (c *scriptChecker) checkImplicitFinalLogicalStatement(function string, ty *TypeExpr, stmt *LogicalStmt) {
	if stmt == nil {
		return
	}
	left, known := staticStatementValue(stmt.Left)
	switch stmt.Operator {
	case tokenWordAnd:
		if known {
			if left.Truthy() {
				c.checkImplicitFinalStatement(function, ty, stmt.Right)
			} else {
				c.checkImplicitFinalStatement(function, ty, stmt.Left)
			}
			return
		}
	case tokenWordOr:
		if known {
			if left.Truthy() {
				c.checkImplicitFinalStatement(function, ty, stmt.Left)
			} else {
				c.checkImplicitFinalStatement(function, ty, stmt.Right)
			}
			return
		}
	default:
		return
	}
	c.checkImplicitFinalStatement(function, ty, stmt.Left)
	c.checkImplicitFinalStatement(function, ty, stmt.Right)
}

func (c *scriptChecker) checkImplicitFinalBlock(function string, ty *TypeExpr, statements []Statement, pos Position) {
	if len(statements) == 0 {
		c.add(function, pos, "typed return %s can implicitly return nil", formatTypeExpr(ty))
		return
	}
	c.checkImplicitFinalStatement(function, ty, effectiveFinalStatement(statements))
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

// blockAlwaysExits reports whether a block always terminates, determined by its
// last reachable statement.
func blockAlwaysExits(statements []Statement) bool {
	if len(statements) == 0 {
		return false
	}
	return statementAlwaysExits(effectiveFinalStatement(statements))
}

func blockMayReturn(statements []Statement) bool {
	for _, stmt := range statements {
		if statementMayReturn(stmt) {
			return true
		}
		if statementAlwaysExits(stmt) {
			return false
		}
	}
	return false
}

func statementMayReturn(stmt Statement) bool {
	switch typed := stmt.(type) {
	case *ReturnStmt:
		return true
	case *LogicalStmt:
		return statementMayReturn(typed.Left) ||
			(logicalStatementRightMayEvaluate(typed) && statementMayReturn(typed.Right))
	case *IfStmt:
		if blockMayReturn(typed.Consequent) || blockMayReturn(typed.Alternate) {
			return true
		}
		for _, elseIf := range typed.ElseIf {
			if blockMayReturn(elseIf.Consequent) {
				return true
			}
		}
	case *ForStmt:
		return blockMayReturn(typed.Body)
	case *WhileStmt:
		return blockMayReturn(typed.Body)
	case *UntilStmt:
		return blockMayReturn(typed.Body)
	case *TryStmt:
		if blockMayReturn(typed.Ensure) {
			return true
		}
		if blockAlwaysExits(typed.Ensure) {
			return false
		}
		for i := range typed.Rescues {
			if blockMayReturn(typed.Rescues[i].Body) {
				return true
			}
		}
		return blockMayReturn(typed.Body) ||
			blockMayReturn(typed.Else)
	}
	return false
}

// effectiveFinalStatement returns the last statement that can actually run in a
// non-empty block. The first statement that always exits makes every later
// statement unreachable, so it becomes the terminal statement;
// otherwise the syntactic last statement is the block's result.
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

type staticCallSpec struct {
	minArgs         int
	maxArgs         int
	rejectKeywords  bool
	allowedKeywords map[string]struct{}
	rejectBlock     bool
	autoInvoke      bool
	usesBlock       bool
}

func (c *scriptChecker) checkCall(function string, call *CallExpr) {
	target, ok := c.resolveCallable(call)
	if !ok {
		return
	}
	if target.fn != nil {
		view := staticCallViewFor(call, target)
		c.checkCallShape(function, view, target.name, target.fn)
		c.checkCallArgumentTypes(function, view, target.name, target.fn)
		c.enqueueReachableFunction(target.name, target.fn)
		return
	}
	c.checkBuiltinCallShape(function, staticCallViewFor(call, target), target.name, target.spec)
}

func (c *scriptChecker) resolveCallable(call *CallExpr) (staticCallable, bool) {
	switch callee := call.Callee.(type) {
	case *Identifier:
		if c.identifierShadowed(callee.Name) {
			return staticCallable{}, false
		}
		if c.hostGlobalShadows(callee.Name) {
			return staticCallable{}, false
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
			return staticCallable{}, false
		}
		if spec, ok := staticBuiltinSpecs[callee.Name]; ok {
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
	if root == nil {
		return nil, false
	}
	val, ok := root.Get(name)
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

func (c *scriptChecker) resolveMemberCallable(member *MemberExpr) (staticCallable, bool) {
	if ident, ok := member.Object.(*Identifier); ok {
		if c.identifierShadowed(ident.Name) {
			return staticCallable{}, false
		}
		if c.hostGlobalShadows(ident.Name) {
			return staticCallable{}, false
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
		if c.hostBuiltinOverrides(ident.Name) {
			return staticCallable{}, false
		}
		if spec, ok := staticBuiltinSpecs[ident.Name+"."+member.Property]; ok {
			// A script that reassigns the namespace member (e.g. JSON.parse =
			// parse) dispatches through the assigned value at runtime, so the
			// builtin contract no longer applies.
			if c.namespaceMemberMutated(ident.Name, member.Property) {
				return staticCallable{}, false
			}
			return staticCallable{name: ident.Name + "." + member.Property, spec: spec}, true
		}
	}
	if className, ok := c.staticInstanceClass(member.Object); ok {
		if classDef, ok := c.script.classes[className]; ok {
			if fn, ok := classDef.Methods[member.Property]; ok {
				return staticCallable{name: className + "#" + member.Property, fn: fn, resolution: calleeMemberMethod}, true
			}
		}
	}
	if receiverKind, ok := staticBuiltinReceiverKind(member.Object); ok {
		if spec, ok := staticBuiltinSpecs[receiverKind+"."+member.Property]; ok {
			return staticCallable{name: receiverKind + "." + member.Property, spec: spec}, true
		}
	}
	if name, fn, ok := c.requiredModuleObjectFunction(member.Object, member.Property); ok {
		return staticCallable{name: name, fn: fn, resolution: calleeMemberValue}, true
	}
	return staticCallable{}, false
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
	entry, err := c.script.engine.loadModule(moduleName, c.moduleCaller)
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
	c.withRuntimeModuleCollection(func() {
		c.collectModuleExports(entry)
		c.bindRequireAlias(alias, exports)
	})
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

var staticBuiltinSpecs = map[string]staticCallSpec{
	"assert":            {minArgs: 1, maxArgs: -1},
	"money":             {minArgs: 1, maxArgs: 1},
	"money_cents":       {minArgs: 2, maxArgs: 2},
	"now":               {minArgs: 0, maxArgs: 0},
	"rand":              {minArgs: 0, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"srand":             {minArgs: 0, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"sleep":             {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"uuid":              {minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true},
	"random_id":         {minArgs: 0, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"JSON.parse":        {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"JSON.stringify":    {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"Regex.match":       {minArgs: 2, maxArgs: 2, rejectKeywords: true, rejectBlock: true},
	"Regex.replace":     {minArgs: 3, maxArgs: 3, rejectKeywords: true, rejectBlock: true},
	"Regex.replace_all": {minArgs: 3, maxArgs: 3, rejectKeywords: true, rejectBlock: true},
	"Time.parse":        {minArgs: 1, maxArgs: 2, allowedKeywords: keywordSet("in")},
	"array.at":          {minArgs: 1, maxArgs: 1, rejectKeywords: true, autoInvoke: true},
	"array.fetch":       {minArgs: 1, maxArgs: 2, autoInvoke: true, usesBlock: true},
	"array.slice":       {minArgs: 1, maxArgs: 2, rejectKeywords: true, autoInvoke: true},
	"string.slice":      {minArgs: 1, maxArgs: 2, autoInvoke: true},
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
}

type staticCallView struct {
	pos    Position
	args   []Expression
	kwargs []KeywordArg
	block  *BlockLiteral
}

func staticCallViewFor(call *CallExpr, target staticCallable) staticCallView {
	view := staticCallView{
		pos:    call.Pos(),
		args:   call.Args,
		kwargs: call.KwArgs,
		block:  call.Block,
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

func (c *scriptChecker) checkArgumentValue(function string, pos Position, val Value, ty *TypeExpr, callName, paramName string) {
	if ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
		return
	}
	if err := c.checkRuntimeStaticValueType(val, ty); err != nil {
		c.addArgumentValueWarning(function, pos, callName, paramName, err)
	}
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
	if !ok || ty == nil || !c.checkRuntimeTypeAnnotation(function, ty) {
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
	return func() {
		c.scopes = c.scopes[:len(c.scopes)-1]
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
		case *LogicalStmt:
			collectLocalBindings([]Statement{typed.Left}, out)
			collectLocalBindings([]Statement{typed.Right}, out)
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
