package runtime

import (
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
	if s == nil {
		return nil
	}
	checker := scriptChecker{script: s}
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
	script   *Script
	warnings []CheckWarning
	scopes   []map[string]struct{}
}

func (c *scriptChecker) checkScript() {
	for _, fn := range c.sortedScriptFunctions() {
		c.checkFunction(fn.Name, fn)
	}
	for _, classDef := range c.sortedClasses() {
		c.checkStatements(classDef.Name+".<class body>", nil, classDef.Body)
		for _, method := range sortedCheckFunctions(classDef.Methods) {
			c.checkFunction(classDef.Name+"#"+method.Name, method)
		}
		for _, method := range sortedCheckFunctions(classDef.ClassMethods) {
			c.checkFunction(classDef.Name+"."+method.Name, method)
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
	popScope := c.pushFunctionScope(fn)
	defer popScope()

	for _, param := range fn.Params {
		if param.Type != nil {
			c.checkTypeAnnotation(label, param.Type)
			if param.DefaultVal != nil {
				c.checkExpressionAgainstType(label, param.DefaultVal, param.Type, fmt.Sprintf("default value for %s", param.Name))
			}
		}
		c.checkExpression(label, param.DefaultVal)
	}
	if fn.ReturnTy != nil {
		c.checkTypeAnnotation(label, fn.ReturnTy)
		c.checkImplicitReturn(label, fn.ReturnTy, fn.Body, fn.Pos)
	}
	c.checkStatements(label, fn.ReturnTy, fn.Body)
}

func (c *scriptChecker) checkStatements(function string, returnType *TypeExpr, statements []Statement) {
	for _, stmt := range statements {
		c.checkStatement(function, returnType, stmt)
	}
}

func (c *scriptChecker) checkStatement(function string, returnType *TypeExpr, stmt Statement) {
	switch typed := stmt.(type) {
	case nil:
		return
	case *ReturnStmt:
		if returnType != nil {
			if typed.Value == nil {
				c.checkNilAgainstType(function, typed.Pos(), returnType, "return value")
			} else {
				c.checkExpressionAgainstType(function, typed.Value, returnType, "return value")
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
	case *ExprStmt:
		c.checkExpression(function, typed.Expr)
	case *IfStmt:
		c.checkExpression(function, typed.Condition)
		c.checkStatements(function, returnType, typed.Consequent)
		for _, elseIf := range typed.ElseIf {
			c.checkExpression(function, elseIf.Condition)
			c.checkStatements(function, returnType, elseIf.Consequent)
		}
		c.checkStatements(function, returnType, typed.Alternate)
	case *ForStmt:
		c.checkExpression(function, typed.Iterable)
		c.checkStatements(function, returnType, typed.Body)
	case *WhileStmt:
		c.checkExpression(function, typed.Condition)
		c.checkStatements(function, returnType, typed.Body)
	case *UntilStmt:
		c.checkExpression(function, typed.Condition)
		c.checkStatements(function, returnType, typed.Body)
	case *TryStmt:
		c.checkStatements(function, returnType, typed.Body)
		c.checkStatements(function, returnType, typed.Rescue)
		c.checkStatements(function, returnType, typed.Else)
		c.checkStatements(function, returnType, typed.Ensure)
	}
}

func (c *scriptChecker) checkExpression(function string, expr Expression) {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			c.checkExpression(function, elem)
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			c.checkExpression(function, pair.Key)
			c.checkExpression(function, pair.Value)
		}
	case *CallExpr:
		c.checkCall(function, typed)
		c.checkExpression(function, typed.Callee)
		for _, arg := range typed.Args {
			c.checkExpression(function, arg)
		}
		for _, kwarg := range typed.KwArgs {
			c.checkExpression(function, kwarg.Value)
		}
		c.checkBlockLiteral(function, typed.Block)
	case *MemberExpr:
		c.checkExpression(function, typed.Object)
	case *ScopeExpr:
		c.checkExpression(function, typed.Object)
	case *IndexExpr:
		c.checkExpression(function, typed.Object)
		for _, index := range typed.Indices {
			c.checkExpression(function, index)
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			c.checkExpression(function, element.Target)
		}
	case *UnaryExpr:
		c.checkExpression(function, typed.Right)
	case *BinaryExpr:
		c.checkExpression(function, typed.Left)
		c.checkExpression(function, typed.Right)
	case *ConditionalExpr:
		c.checkExpression(function, typed.Condition)
		c.checkExpression(function, typed.Consequent)
		c.checkExpression(function, typed.Alternate)
	case *IfExpr:
		c.checkExpression(function, typed.Condition)
		c.checkExpression(function, typed.Consequent)
		for _, branch := range typed.ElseIf {
			c.checkExpression(function, branch.Condition)
			c.checkExpression(function, branch.Result)
		}
		c.checkExpression(function, typed.Alternate)
	case *RangeExpr:
		c.checkExpression(function, typed.Start)
		c.checkExpression(function, typed.End)
	case *CaseExpr:
		c.checkExpression(function, typed.Target)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				c.checkExpression(function, value.Expr)
			}
			c.checkExpression(function, clause.Result)
		}
		c.checkExpression(function, typed.ElseExpr)
	case *BlockLiteral:
		c.checkBlockLiteral(function, typed)
	case *YieldExpr:
		for _, arg := range typed.Args {
			c.checkExpression(function, arg)
		}
	case *InterpolatedString:
		c.checkStringParts(function, typed.Parts)
	case *InterpolatedSymbol:
		c.checkStringParts(function, typed.Parts)
	}
}

func (c *scriptChecker) checkBlockLiteral(function string, block *BlockLiteral) {
	if block == nil {
		return
	}
	popScope := c.pushBlockScope(block)
	defer popScope()

	for _, param := range block.Params {
		c.checkTypeAnnotation(function, param.Type)
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

func (c *scriptChecker) checkTypeAnnotation(function string, ty *TypeExpr) bool {
	if ty == nil {
		return true
	}
	if err := validateTypeExprResolved(ty, typeContext{owner: c.script}); err != nil {
		c.add(function, typeExprPosition(ty), "%s", err)
		return false
	}
	return true
}

func (c *scriptChecker) checkExpressionAgainstType(function string, expr Expression, ty *TypeExpr, subject string) {
	val, ok := staticLiteralValue(expr)
	if !ok {
		return
	}
	c.checkValueAgainstType(function, expr.Pos(), val, ty, subject)
}

func (c *scriptChecker) checkNilAgainstType(function string, pos Position, ty *TypeExpr, subject string) {
	c.checkValueAgainstType(function, pos, NewNil(), ty, subject)
}

func (c *scriptChecker) checkValueAgainstType(function string, pos Position, val Value, ty *TypeExpr, subject string) {
	if ty == nil || !c.checkTypeAnnotation(function, ty) {
		return
	}
	if err := c.checkStaticValueType(val, ty); err != nil {
		var mismatch *typeMismatchError
		if errors.As(err, &mismatch) {
			c.add(function, pos, "%s expected %s, got %s", subject, mismatch.Expected, mismatch.Actual)
			return
		}
		c.add(function, pos, "%s type check failed: %s", subject, err)
	}
}

func (c *scriptChecker) checkStaticValueType(val Value, ty *TypeExpr) error {
	_, err := normalizeValueForType(val, ty, typeContext{owner: c.script})
	return err
}

func (c *scriptChecker) checkImplicitReturn(function string, ty *TypeExpr, statements []Statement, pos Position) {
	if !c.checkTypeAnnotation(function, ty) || typeAllowsNilReturn(ty) {
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
		c.checkExpressionAgainstType(function, typed.Expr, ty, "return value")
	case *AssignStmt:
		if expressionCanImplicitlyYieldNil(typed.Value) {
			c.add(function, typed.Pos(), "typed return %s can implicitly return nil", formatTypeExpr(ty))
			return
		}
		c.checkExpressionAgainstType(function, typed.Value, ty, "return value")
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

// statementAlwaysExits reports whether evaluating stmt always performs an
// explicit return or raise, mirroring the runtime's "returned"/error signals.
// It lets the implicit-return check tell when a begin/else's else branch is
// unreachable: evalTryStatement only runs the else branch when the body did not
// return and did not raise. The analysis is conservative—it returns true only
// when every path is known to exit—so a false result never suppresses a real
// warning.
func statementAlwaysExits(stmt Statement) bool {
	switch typed := stmt.(type) {
	case *ReturnStmt, *RaiseStmt:
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
	default:
		return false
	}
}

// blockAlwaysExits reports whether a block always returns or raises, determined
// by its last reachable statement.
func blockAlwaysExits(statements []Statement) bool {
	if len(statements) == 0 {
		return false
	}
	return statementAlwaysExits(effectiveFinalStatement(statements))
}

// effectiveFinalStatement returns the last statement that can actually run in a
// non-empty block. The first statement that always exits (return/raise) makes
// every later statement unreachable, so it becomes the terminal statement;
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
	c.checkBuiltinCallShape(function, call, target.name, target.spec)
}

func (c *scriptChecker) resolveCallable(call *CallExpr) (staticCallable, bool) {
	switch callee := call.Callee.(type) {
	case *Identifier:
		if c.identifierShadowed(callee.Name) {
			return staticCallable{}, false
		}
		if fn, ok := c.script.functions[callee.Name]; ok {
			return staticCallable{name: callee.Name, fn: fn, resolution: calleeDirect}, true
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

func (c *scriptChecker) resolveMemberCallable(member *MemberExpr) (staticCallable, bool) {
	if ident, ok := member.Object.(*Identifier); ok {
		if c.identifierShadowed(ident.Name) {
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
		if spec, ok := staticBuiltinSpecs[ident.Name+"."+member.Property]; ok {
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
	"assert":            {minArgs: 1, maxArgs: 2},
	"money":             {minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true},
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
	"array.at":          {minArgs: 1, maxArgs: 1, rejectKeywords: true},
	"array.fetch":       {minArgs: 1, maxArgs: 2},
	"array.slice":       {minArgs: 1, maxArgs: 2, rejectKeywords: true},
	"string.slice":      {minArgs: 1, maxArgs: 2},
}

func keywordSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

func (c *scriptChecker) checkBuiltinCallShape(function string, call *CallExpr, name string, spec staticCallSpec) {
	if len(call.Args) < spec.minArgs {
		c.add(function, call.Pos(), "call to %s has too few arguments: got %d, want at least %d", name, len(call.Args), spec.minArgs)
	}
	if spec.maxArgs >= 0 && len(call.Args) > spec.maxArgs {
		c.add(function, call.Pos(), "call to %s has too many arguments: got %d, want at most %d", name, len(call.Args), spec.maxArgs)
	}
	if spec.rejectKeywords && len(call.KwArgs) > 0 {
		c.add(function, call.Pos(), "call to %s does not accept keyword arguments", name)
	}
	if len(spec.allowedKeywords) > 0 {
		for _, kwarg := range call.KwArgs {
			if _, ok := spec.allowedKeywords[kwarg.Name]; !ok {
				c.add(function, kwarg.Value.Pos(), "call to %s has unexpected keyword argument %s", name, kwarg.Name)
			}
		}
	}
	if spec.rejectBlock && call.Block != nil {
		c.add(function, call.Block.Pos(), "call to %s does not accept a block", name)
	}
}

type staticCallView struct {
	pos    Position
	args   []Expression
	kwargs []KeywordArg
}

func staticCallViewFor(call *CallExpr, target staticCallable) staticCallView {
	view := staticCallView{
		pos:    call.Pos(),
		args:   call.Args,
		kwargs: call.KwArgs,
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
			}
		case ParamKeyword:
			if kwIndex := keywordIndex(call, param.Name); kwIndex >= 0 {
				c.checkArgumentExpression(function, call.kwargs[kwIndex].Value, param.Type, name, param.Name)
			}
		case ParamRest:
			c.checkRestArgumentExpressions(function, call.pos, call.args[argIdx:], param.Type, name, param.Name)
			argIdx = len(call.args)
		}
	}
}

func (c *scriptChecker) checkRestArgumentExpressions(function string, pos Position, args []Expression, ty *TypeExpr, callName, paramName string) {
	if ty == nil || !c.checkTypeAnnotation(function, ty) {
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
	if err := c.checkStaticValueType(NewArray(values), ty); err != nil {
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

func (c *scriptChecker) checkArgumentExpression(function string, expr Expression, ty *TypeExpr, callName, paramName string) {
	val, ok := staticLiteralValue(expr)
	if !ok || ty == nil || !c.checkTypeAnnotation(function, ty) {
		return
	}
	if err := c.checkStaticValueType(val, ty); err != nil {
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

func (c *scriptChecker) pushFunctionScope(fn *ScriptFunction) func() {
	scope := make(map[string]struct{})
	for _, param := range fn.Params {
		if param.Name != "" {
			scope[param.Name] = struct{}{}
		}
	}
	collectLocalBindings(fn.Body, scope)
	return c.pushScope(scope)
}

func (c *scriptChecker) pushBlockScope(block *BlockLiteral) func() {
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
	collectLocalBindings(block.Body, scope)
	return c.pushScope(scope)
}

func (c *scriptChecker) pushScope(scope map[string]struct{}) func() {
	c.scopes = append(c.scopes, scope)
	return func() {
		c.scopes = c.scopes[:len(c.scopes)-1]
	}
}

func (c *scriptChecker) identifierShadowed(name string) bool {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if _, ok := c.scopes[i][name]; ok {
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
			if typed.Iterator != "" {
				out[typed.Iterator] = struct{}{}
			}
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
	case *DestructureTarget:
		for _, element := range typed.Elements {
			collectBindingTarget(element.Target, out)
		}
	}
}

func (c *scriptChecker) add(function string, pos Position, format string, args ...any) {
	c.warnings = append(c.warnings, CheckWarning{
		Function: function,
		Pos:      pos,
		Message:  fmt.Sprintf(format, args...),
	})
}
