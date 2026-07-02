package parser

import "github.com/mgomes/vibescript/internal/ast"

type implicitBlockParamUsage struct {
	assigned map[string]struct{}
	numbered map[string]struct{}
	scoped   map[string]int
	it       bool
}

func inferImplicitBlockParams(body []ast.Statement, inferIt bool) []string {
	usage := implicitBlockParamUsage{
		assigned: map[string]struct{}{},
		numbered: map[string]struct{}{},
		scoped:   map[string]int{},
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
	if inferIt && usage.it {
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
		u.visitStatements(s.Body)
		for _, clause := range s.Rescues {
			if clause.Binding != "" {
				u.withScopedBinding(clause.Binding, func() {
					u.visitStatements(clause.Body)
				})
			} else {
				u.visitStatements(clause.Body)
			}
		}
		u.visitStatements(s.Else)
		u.visitStatements(s.Ensure)
	case *ast.BreakStmt:
		u.visitExpression(s.Value, false)
	case *ast.NextStmt:
		u.visitExpression(s.Value, false)
	case *ast.RetryStmt:
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
	case *ast.RescueModifierExpr:
		u.visitExpression(e.Body, false)
		u.visitExpression(e.Fallback, false)
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
	case *ast.ForStmt, *ast.WhileStmt, *ast.UntilStmt, *ast.TryStmt:
		u.visitStatement(e.(ast.Statement))
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
	if u.isScopedBinding(name) {
		return
	}
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
		if u.isScopedBinding(t.Name) {
			return
		}
		u.assigned[t.Name] = struct{}{}
	case *ast.DestructureTarget:
		for _, element := range t.Elements {
			u.recordAssignedTarget(element.Target)
		}
	case *ast.MemberExpr:
		u.visitExpression(t.Object, false)
	case *ast.IndexExpr:
		u.visitExpression(t.Object, false)
		for _, index := range t.Indices {
			u.visitExpression(index, false)
		}
	default:
		u.visitExpression(target, false)
	}
}

func (u *implicitBlockParamUsage) withScopedBinding(name string, visit func()) {
	u.scoped[name]++
	defer func() {
		u.scoped[name]--
		if u.scoped[name] == 0 {
			delete(u.scoped, name)
		}
	}()
	visit()
}

func (u *implicitBlockParamUsage) isScopedBinding(name string) bool {
	return u.scoped[name] > 0
}

func isNumberedBlockParamName(name string) bool {
	return len(name) == 2 && name[0] == '_' && name[1] >= '1' && name[1] <= '9'
}

func (p *parser) declareNumberedImplicitBlockParamCandidates() {
	for i := range 9 {
		p.declareLocal("_" + string(rune('1'+i)))
	}
}
