package parser

import "github.com/mgomes/vibescript/internal/ast"

type implicitBlockParamUsage struct {
	assigned map[string]struct{}
	numbered map[string]struct{}
	it       bool
}

func inferImplicitBlockParams(body []ast.Statement) []string {
	usage := implicitBlockParamUsage{
		assigned: map[string]struct{}{},
		numbered: map[string]struct{}{},
	}
	usage.visitStatements(body)

	params := []string{}
	for i := range 9 {
		name := "_" + string(rune('1'+i))
		if _, used := usage.numbered[name]; !used {
			continue
		}
		if _, assigned := usage.assigned[name]; assigned {
			continue
		}
		params = append(params, name)
	}
	if usage.it {
		if _, assigned := usage.assigned["it"]; !assigned {
			params = append(params, "it")
		}
	}
	return params
}

func (u *implicitBlockParamUsage) visitStatements(stmts []ast.Statement) {
	for _, stmt := range stmts {
		u.visitStatement(stmt)
	}
}

func (u *implicitBlockParamUsage) visitStatement(stmt ast.Statement) {
	switch s := stmt.(type) {
	case *ast.FunctionStmt, *ast.ClassStmt, *ast.EnumStmt:
		return
	case *ast.ReturnStmt:
		u.visitExpression(s.Value, false)
	case *ast.RaiseStmt:
		u.visitExpression(s.Value, false)
	case *ast.AssignStmt:
		u.recordAssignedTarget(s.Target)
		u.visitExpression(s.Value, false)
	case *ast.ExprStmt:
		u.visitExpression(s.Expr, false)
	case *ast.IfStmt:
		u.visitExpression(s.Condition, false)
		u.visitStatements(s.Consequent)
		for _, branch := range s.ElseIf {
			u.visitStatement(branch)
		}
		u.visitStatements(s.Alternate)
	case *ast.ForStmt:
		u.assigned[s.Iterator] = struct{}{}
		u.visitExpression(s.Iterable, false)
		u.visitStatements(s.Body)
	case *ast.WhileStmt:
		u.visitExpression(s.Condition, false)
		u.visitStatements(s.Body)
	case *ast.UntilStmt:
		u.visitExpression(s.Condition, false)
		u.visitStatements(s.Body)
	case *ast.TryStmt:
		if s.RescueBinding != "" {
			u.assigned[s.RescueBinding] = struct{}{}
		}
		u.visitStatements(s.Body)
		u.visitStatements(s.Rescue)
		u.visitStatements(s.Else)
		u.visitStatements(s.Ensure)
	case *ast.BreakStmt:
		u.visitExpression(s.Value, false)
	case *ast.NextStmt:
		return
	}
}

func (u *implicitBlockParamUsage) visitExpression(expr ast.Expression, callCallee bool) {
	switch e := expr.(type) {
	case nil:
		return
	case *ast.Identifier:
		u.recordIdentifier(e.Name, callCallee)
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.StringLiteral, *ast.BoolLiteral,
		*ast.NilLiteral, *ast.SymbolLiteral, *ast.IvarExpr, *ast.ClassVarExpr:
		return
	case *ast.ArrayLiteral:
		for _, element := range e.Elements {
			u.visitExpression(element, false)
		}
	case *ast.HashLiteral:
		for _, pair := range e.Pairs {
			u.visitExpression(pair.Key, false)
			u.visitExpression(pair.Value, false)
		}
	case *ast.CallExpr:
		u.visitExpression(e.Callee, true)
		for _, arg := range e.Args {
			u.visitExpression(arg, false)
		}
		for _, arg := range e.KwArgs {
			u.visitExpression(arg.Value, false)
		}
	case *ast.MemberExpr:
		u.visitExpression(e.Object, false)
	case *ast.ScopeExpr:
		u.visitExpression(e.Object, false)
	case *ast.IndexExpr:
		u.visitExpression(e.Object, false)
		for _, index := range e.Indices {
			u.visitExpression(index, false)
		}
	case *ast.DestructureTarget:
		return
	case *ast.UnaryExpr:
		u.visitExpression(e.Right, false)
	case *ast.BinaryExpr:
		u.visitExpression(e.Left, false)
		u.visitExpression(e.Right, false)
	case *ast.ConditionalExpr:
		u.visitExpression(e.Condition, false)
		u.visitExpression(e.Consequent, false)
		u.visitExpression(e.Alternate, false)
	case *ast.IfExpr:
		u.visitExpression(e.Condition, false)
		u.visitExpression(e.Consequent, false)
		for _, branch := range e.ElseIf {
			u.visitExpression(branch.Condition, false)
			u.visitExpression(branch.Result, false)
		}
		u.visitExpression(e.Alternate, false)
	case *ast.RangeExpr:
		u.visitExpression(e.Start, false)
		u.visitExpression(e.End, false)
	case *ast.CaseExpr:
		u.visitExpression(e.Target, false)
		for _, clause := range e.Clauses {
			for _, value := range clause.Values {
				u.visitExpression(value.Expr, false)
			}
			u.visitExpression(clause.Result, false)
		}
		u.visitExpression(e.ElseExpr, false)
	case *ast.BlockLiteral:
		return
	case *ast.YieldExpr:
		for _, arg := range e.Args {
			u.visitExpression(arg, false)
		}
	case *ast.InterpolatedString:
		u.visitStringParts(e.Parts)
	case *ast.InterpolatedSymbol:
		u.visitStringParts(e.Parts)
	}
}

func (u *implicitBlockParamUsage) visitStringParts(parts []ast.StringPart) {
	for _, part := range parts {
		expr, ok := part.(ast.StringExpr)
		if !ok {
			continue
		}
		u.visitExpression(expr.Expr, false)
	}
}

func (u *implicitBlockParamUsage) recordIdentifier(name string, callCallee bool) {
	if isNumberedBlockParamName(name) {
		u.numbered[name] = struct{}{}
		return
	}
	if name == "it" && !callCallee {
		u.it = true
	}
}

func (u *implicitBlockParamUsage) recordAssignedTarget(target ast.Expression) {
	switch t := target.(type) {
	case nil:
		return
	case *ast.Identifier:
		u.assigned[t.Name] = struct{}{}
	case *ast.DestructureTarget:
		for _, element := range t.Elements {
			u.recordAssignedTarget(element.Target)
		}
	}
}

func isNumberedBlockParamName(name string) bool {
	return len(name) == 2 && name[0] == '_' && name[1] >= '1' && name[1] <= '9'
}
