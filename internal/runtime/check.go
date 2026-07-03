package runtime

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	if s == nil {
		return nil
	}
	optionGlobals := checkOptionGlobals(s, opts)
	checker := scriptChecker{
		script:          s,
		callOptions:     opts,
		optionGlobals:   optionGlobals,
		typeRoot:        checkTypeRoot(s, optionGlobals),
		runtimeTypeRoot: checkTypeRoot(s, optionGlobals),
		hostGlobals:     checkHostGlobals(optionGlobals),
	}
	checker.moduleExportRoot = checker.typeRoot
	checker.checkScript()
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
	typeRoot                *Env
	runtimeTypeRoot         *Env
	hostGlobals             map[string]struct{}
	warnings                []CheckWarning
	scopes                  []map[string]struct{}
	requiredModules         map[string]struct{}
	runtimeModules          map[string]struct{}
	runtimeNamespaceMembers map[string]struct{}
	moduleEntries           map[string]moduleEntry
	moduleCaller            *moduleContext
	moduleExportRoot        *Env
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
	if script == nil {
		return nil
	}
	root := newEnvWithCapacity(nil, len(script.functions)+len(script.classes)+len(script.enums)+len(globals))
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

		c.collectRequiredModuleExportsFromStatements(typed.Consequent)
		if !blockAlwaysExits(typed.Consequent) {
			fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
			fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
		}
		for _, elseIf := range typed.ElseIf {
			c.restoreModuleCollectionState(falseState)
			c.restoreScopeState(falseScopeState)
			c.collectRequiredModuleExportsFromExpression(elseIf.Condition)
			falseState = c.snapshotModuleCollectionState()
			falseScopeState = c.snapshotScopeState()
			c.collectRequiredModuleExportsFromStatements(elseIf.Consequent)
			if !blockAlwaysExits(elseIf.Consequent) {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
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
		if len(typed.Rescue) > 0 {
			c.restoreModuleCollectionState(baseState)
			c.restoreScopeState(baseScopeState)
			popScope := c.pushRescueScope(typed)
			c.collectRequiredModuleExportsFromStatements(typed.Rescue)
			popScope()
			if !blockAlwaysExits(typed.Rescue) {
				fallthroughStates = append(fallthroughStates, c.snapshotModuleCollectionState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		c.mergeModuleCollectionStates(baseState, fallthroughStates)
		c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
		c.collectRequiredModuleExportsFromStatements(typed.Ensure)
	}
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
		for _, arg := range typed.Args {
			c.collectRequiredModuleExportsFromExpression(arg)
		}
		for _, kwarg := range typed.KwArgs {
			c.collectRequiredModuleExportsFromExpression(kwarg.Value)
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
		c.collectRequiredModuleExportsFromExpressionBranches(typed.Consequent, typed.Alternate)
	case *IfExpr:
		baseState := c.snapshotModuleCollectionState()
		c.collectRequiredModuleExportsFromExpression(typed.Condition)
		falseState := c.snapshotModuleCollectionState()
		branchStates := make([]checkModuleCollectionState, 0, len(typed.ElseIf)+2)

		c.collectRequiredModuleExportsFromExpression(typed.Consequent)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
		for _, branch := range typed.ElseIf {
			c.restoreModuleCollectionState(falseState)
			c.collectRequiredModuleExportsFromExpression(branch.Condition)
			falseState = c.snapshotModuleCollectionState()
			c.collectRequiredModuleExportsFromExpression(branch.Result)
			branchStates = append(branchStates, c.snapshotModuleCollectionState())
		}
		c.restoreModuleCollectionState(falseState)
		c.collectRequiredModuleExportsFromExpression(typed.Alternate)
		branchStates = append(branchStates, c.snapshotModuleCollectionState())
		c.mergeModuleCollectionStates(baseState, branchStates)
	case *RangeExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Start)
		c.collectRequiredModuleExportsFromExpression(typed.End)
	case *CaseExpr:
		c.collectRequiredModuleExportsFromExpression(typed.Target)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				c.collectRequiredModuleExportsFromExpression(value.Expr)
			}
			c.collectRequiredModuleExportsFromExpression(clause.Result)
		}
		c.collectRequiredModuleExportsFromExpression(typed.ElseExpr)
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
	if len(call.Args) == 0 || c.requireCallShadowed() {
		return
	}
	callee, ok := call.Callee.(*Identifier)
	if !ok || callee.Name != "require" {
		return
	}
	moduleName, ok := staticRequireModuleName(call.Args[0])
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
	c.collectModuleExports(entry)
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
	c.collectRequiredModuleExportsFromModuleEntrypoint(entry)
	root := c.moduleExportRoot
	if root == nil {
		root = c.typeRoot
	}
	for name, enumDef := range cloneEnumsForCall(entry.script.enums) {
		if _, exists := root.Get(name); exists {
			continue
		}
		root.Define(name, NewEnum(enumDef))
	}
	for name, fn := range entry.script.functions {
		if name == moduleEntrypointFunction || !shouldExportModuleFunction(fn) {
			continue
		}
		if _, exists := root.Get(name); exists {
			continue
		}
		root.DefineStatic(name, NewFunction(fn))
	}
}

func (c *scriptChecker) collectRequiredModuleExportsFromModuleEntrypoint(entry moduleEntry) {
	fn := entry.script.functions[moduleEntrypointFunction]
	if fn == nil {
		return
	}
	caller := moduleContextForEntry(entry)
	previousCaller := c.moduleCaller
	previousScopes := c.scopes
	c.moduleCaller = &caller
	c.scopes = nil
	defer func() {
		c.moduleCaller = previousCaller
		c.scopes = previousScopes
	}()

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
		c.checkRuntimeClassBodies(nil, false)
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
	c.runtimeTypeRoot = checkTypeRoot(c.script, c.optionGlobals)
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
	c.warnings = nil
	defer func() {
		c.warnings = previousWarnings
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
	if len(common) != 0 {
		c.withRuntimeModuleCollection(func() {
			for key := range common {
				entry, ok := c.moduleEntries[key]
				if !ok {
					continue
				}
				c.collectModuleExports(entry)
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
			if typed.Value == nil {
				c.checkRuntimeNilAgainstType(function, typed.Pos(), returnType, "return value")
			} else {
				c.checkRuntimeExpressionAgainstType(function, typed.Value, returnType, "return value")
			}
		}
		c.checkExpression(function, typed.Value)
	case *RaiseStmt:
		c.checkExpression(function, typed.Value)
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
		falseRuntimeState := c.snapshotRuntimeState()
		falseScopeState := c.snapshotScopeState()
		fallthroughRuntimeStates := make([]checkRuntimeState, 0, len(typed.ElseIf)+2)
		fallthroughScopeStates := make([]checkScopeState, 0, len(typed.ElseIf)+2)

		c.checkStatements(function, returnType, typed.Consequent)
		if !blockAlwaysExits(typed.Consequent) {
			fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
			fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
		}
		for _, elseIf := range typed.ElseIf {
			c.restoreRuntimeState(falseRuntimeState)
			c.restoreScopeState(falseScopeState)
			c.checkExpression(function, elseIf.Condition)
			c.collectRuntimeRequireCallExportsFromExpression(elseIf.Condition)
			falseRuntimeState = c.snapshotRuntimeState()
			falseScopeState = c.snapshotScopeState()
			c.checkStatements(function, returnType, elseIf.Consequent)
			if !blockAlwaysExits(elseIf.Consequent) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
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
		branchReturnType := returnType
		if blockAlwaysExits(typed.Ensure) {
			branchReturnType = nil
		}
		baseRuntimeState := c.snapshotRuntimeState()
		baseScopeState := c.snapshotScopeState()
		fallthroughRuntimeStates := make([]checkRuntimeState, 0, 2)
		fallthroughScopeStates := make([]checkScopeState, 0, 2)

		c.checkStatements(function, branchReturnType, typed.Body)
		if !blockAlwaysExits(typed.Body) {
			c.checkStatements(function, branchReturnType, typed.Else)
			if len(typed.Else) == 0 || !blockAlwaysExits(typed.Else) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		if len(typed.Rescue) > 0 {
			c.restoreRuntimeState(baseRuntimeState)
			c.restoreScopeState(baseScopeState)
			popScope := c.pushRescueScope(typed)
			c.checkStatements(function, branchReturnType, typed.Rescue)
			popScope()
			if !blockAlwaysExits(typed.Rescue) {
				fallthroughRuntimeStates = append(fallthroughRuntimeStates, c.snapshotRuntimeState())
				fallthroughScopeStates = append(fallthroughScopeStates, c.snapshotScopeState())
			}
		}
		c.mergeRuntimeStates(baseRuntimeState, fallthroughRuntimeStates)
		c.mergeScopeStates(baseScopeState, fallthroughScopeStates)
		c.checkStatements(function, returnType, typed.Ensure)
	}
}

func (c *scriptChecker) checkExpression(function string, expr Expression) {
	c.checkExpressionWithAuto(function, expr, true)
}

func (c *scriptChecker) checkExpressionWithAuto(function string, expr Expression, autoCall bool) {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return
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
		c.collectRuntimeCallArgumentEffects(typed)
		c.checkCall(function, typed)
		for _, arg := range typed.Args {
			c.checkExpressionWithAuto(function, arg, true)
		}
		for _, kwarg := range typed.KwArgs {
			c.checkExpressionWithAuto(function, kwarg.Value, true)
		}
		c.checkBlockLiteral(function, typed.Block)
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
			c.checkExpressionWithAuto(function, typed.Right, true)
		}
	case *ConditionalExpr:
		c.checkExpressionWithAuto(function, typed.Condition, true)
		c.checkExpressionWithAuto(function, typed.Consequent, true)
		c.checkExpressionWithAuto(function, typed.Alternate, true)
	case *IfExpr:
		c.checkExpressionWithAuto(function, typed.Condition, true)
		c.checkExpressionWithAuto(function, typed.Consequent, true)
		for _, branch := range typed.ElseIf {
			c.checkExpressionWithAuto(function, branch.Condition, true)
			c.checkExpressionWithAuto(function, branch.Result, true)
		}
		c.checkExpressionWithAuto(function, typed.Alternate, true)
	case *RangeExpr:
		c.checkExpressionWithAuto(function, typed.Start, true)
		c.checkExpressionWithAuto(function, typed.End, true)
	case *CaseExpr:
		c.checkExpressionWithAuto(function, typed.Target, true)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				c.checkExpressionWithAuto(function, value.Expr, true)
			}
			c.checkExpressionWithAuto(function, clause.Result, true)
		}
		c.checkExpressionWithAuto(function, typed.ElseExpr, true)
	case *BlockLiteral:
		c.checkBlockLiteral(function, typed)
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

func (c *scriptChecker) checkMemberAutoCall(function string, member *MemberExpr) {
	target, ok := c.resolveMemberCallable(member)
	if !ok {
		return
	}
	view := staticCallView{pos: member.Pos()}
	if target.fn != nil {
		c.checkCallShape(function, view, target.name, target.fn)
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

	for _, param := range block.Params {
		c.checkRuntimeTypeAnnotation(function, param.Type)
		c.checkExpression(function, param.DefaultVal)
	}
	label := fmt.Sprintf("%s block at %d:%d", function, block.Pos().Line, block.Pos().Column)
	c.checkStatements(label, nil, block.Body)
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
	case *IfStmt:
		if len(typed.Alternate) == 0 {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
			return
		}
		c.checkImplicitFinalBlock(function, ty, typed.Consequent, typed.Pos())
		for _, elseIf := range typed.ElseIf {
			c.checkImplicitFinalBlock(function, ty, elseIf.Consequent, elseIf.Pos())
		}
		c.checkImplicitFinalBlock(function, ty, typed.Alternate, typed.Pos())
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
		if len(typed.Rescue) > 0 {
			c.checkImplicitFinalBlock(function, ty, typed.Rescue, typed.RescuePosition)
		}
	}
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
		if len(typed.Alternate) == 0 {
			return false
		}
		if !blockAlwaysExits(typed.Consequent) {
			return false
		}
		for _, elseIf := range typed.ElseIf {
			if !blockAlwaysExits(elseIf.Consequent) {
				return false
			}
		}
		return blockAlwaysExits(typed.Alternate)
	case *TryStmt:
		if blockAlwaysExits(typed.Ensure) {
			return true
		}
		if len(typed.Rescue) > 0 && !blockAlwaysExits(typed.Rescue) {
			return false
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
		if typed.Alternate == nil {
			return true
		}
		if expressionCanImplicitlyYieldNil(typed.Consequent) || expressionCanImplicitlyYieldNil(typed.Alternate) {
			return true
		}
		for _, branch := range typed.ElseIf {
			if expressionCanImplicitlyYieldNil(branch.Result) {
				return true
			}
		}
	case *ConditionalExpr:
		return expressionCanImplicitlyYieldNil(typed.Consequent) ||
			expressionCanImplicitlyYieldNil(typed.Alternate)
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

func (c *scriptChecker) typeRootHasBinding(name string) bool {
	if c.typeRoot == nil {
		return false
	}
	_, ok := c.typeRoot.Get(name)
	return ok
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
	return staticCallable{}, false
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
	"uuid":              {minArgs: 0, maxArgs: 0, rejectKeywords: true},
	"random_id":         {minArgs: 0, maxArgs: 1, rejectKeywords: true},
	"JSON.parse":        {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"JSON.stringify":    {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
	"Regex.match":       {minArgs: 2, maxArgs: 2, rejectKeywords: true, rejectBlock: true},
	"Regex.replace":     {minArgs: 3, maxArgs: 3, rejectKeywords: true, rejectBlock: true},
	"Regex.replace_all": {minArgs: 3, maxArgs: 3, rejectKeywords: true, rejectBlock: true},
	"Time.parse":        {minArgs: 1, maxArgs: 2, allowedKeywords: keywordSet("in")},
	"array.at":          {minArgs: 1, maxArgs: 1, rejectKeywords: true, autoInvoke: true},
	"array.fetch":       {minArgs: 1, maxArgs: 2, autoInvoke: true},
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
	return functionCanReceiveOptionsHash(target.fn, len(view.args), staticKeywordNames(view.kwargs))
}

func staticKeywordNames(kwargs []KeywordArg) map[string]Value {
	out := make(map[string]Value, len(kwargs))
	for _, kwarg := range kwargs {
		out[kwarg.Name] = NewNil()
	}
	return out
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
		}
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

func (c *scriptChecker) pushRescueScope(stmt *TryStmt) func() {
	if stmt == nil || stmt.RescueBinding == "" {
		return func() {}
	}
	return c.pushScope(map[string]struct{}{stmt.RescueBinding: {}})
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
			if typed.RescueBinding != "" {
				out[typed.RescueBinding] = struct{}{}
			}
			collectLocalBindings(typed.Rescue, out)
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
	c.warnings = append(c.warnings, CheckWarning{
		Function: function,
		Pos:      pos,
		Message:  fmt.Sprintf(format, args...),
	})
}
