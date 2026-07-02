// Package analyze provides static checks for compiled Vibescript
// programs. It walks the internal AST and reports issues such as
// unreachable statements following a terminator.
//
// The package is internal because it depends on the AST shape. The
// vibes command exposes the same behavior via "vibes analyze".
package analyze

import (
	"fmt"
	"sort"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes/source"
)

// Warning describes a single issue surfaced by the analyzer.
type Warning struct {
	Function string
	Pos      source.Position
	Message  string
}

// Script returns warnings collected from the given compiled script.
// The returned slice is sorted by position then function name for
// deterministic output.
func Script(script *runtime.Script) []Warning {
	var warnings []Warning
	for _, fn := range script.Functions() {
		lintStatements(fn.Name, fn.Body, &warnings)
	}
	for _, classDef := range script.Classes() {
		lintStatements(classDef.Name+".<class body>", classDef.Body, &warnings)
		for _, method := range sortedFunctionsByName(classDef.Methods) {
			lintStatements(classDef.Name+"#"+method.Name, method.Body, &warnings)
		}
		for _, method := range sortedFunctionsByName(classDef.ClassMethods) {
			lintStatements(classDef.Name+"."+method.Name, method.Body, &warnings)
		}
	}

	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Pos.Line != warnings[j].Pos.Line {
			return warnings[i].Pos.Line < warnings[j].Pos.Line
		}
		if warnings[i].Pos.Column != warnings[j].Pos.Column {
			return warnings[i].Pos.Column < warnings[j].Pos.Column
		}
		return warnings[i].Function < warnings[j].Function
	})

	return warnings
}

func sortedFunctionsByName(functions map[string]*runtime.ScriptFunction) []*runtime.ScriptFunction {
	names := make([]string, 0, len(functions))
	for name := range functions {
		names = append(names, name)
	}
	sort.Strings(names)
	sorted := make([]*runtime.ScriptFunction, 0, len(names))
	for _, name := range names {
		sorted = append(sorted, functions[name])
	}
	return sorted
}

func lintStatements(function string, statements []ast.Statement, warnings *[]Warning) bool {
	terminated := false
	for _, stmt := range statements {
		if terminated {
			*warnings = append(*warnings, Warning{
				Function: function,
				Pos:      stmt.Pos(),
				Message:  "unreachable statement",
			})
			continue
		}
		if statementTerminates(function, stmt, warnings) {
			terminated = true
		}
	}
	return terminated
}

func statementTerminates(function string, stmt ast.Statement, warnings *[]Warning) bool {
	switch typed := stmt.(type) {
	case *ast.ReturnStmt:
		lintExpression(function, typed.Value, warnings)
		return true
	case *ast.RaiseStmt:
		lintExpression(function, typed.Value, warnings)
		return true
	case *ast.AssignStmt:
		lintExpression(function, typed.Target, warnings)
		lintExpression(function, typed.Value, warnings)
		return false
	case *ast.ExprStmt:
		lintExpression(function, typed.Expr, warnings)
		return false
	case *ast.IfStmt:
		return ifStatementTerminates(function, typed, warnings)
	case *ast.ForStmt:
		lintExpression(function, typed.Iterable, warnings)
		lintStatements(function, typed.Body, warnings)
		return false
	case *ast.WhileStmt:
		lintExpression(function, typed.Condition, warnings)
		lintStatements(function, typed.Body, warnings)
		return false
	case *ast.UntilStmt:
		lintExpression(function, typed.Condition, warnings)
		lintStatements(function, typed.Body, warnings)
		return false
	case *ast.TryStmt:
		bodyTerminated := lintStatements(function, typed.Body, warnings)
		elseTerminated := false
		if len(typed.Else) > 0 {
			elseTerminated = lintStatements(function, typed.Else, warnings)
		}
		rescuesTerminate := len(typed.Rescues) > 0
		for _, clause := range typed.Rescues {
			if !lintStatements(function, clause.Body, warnings) {
				rescuesTerminate = false
			}
		}
		ensureTerminated := false
		if len(typed.Ensure) > 0 {
			ensureTerminated = lintStatements(function, typed.Ensure, warnings)
		}
		if ensureTerminated {
			return true
		}
		normalTerminated := bodyTerminated
		if len(typed.Else) > 0 {
			normalTerminated = bodyTerminated || elseTerminated
		}
		if len(typed.Rescues) == 0 {
			return normalTerminated
		}
		return normalTerminated && rescuesTerminate
	default:
		return false
	}
}

func ifStatementTerminates(function string, stmt *ast.IfStmt, warnings *[]Warning) bool {
	lintExpression(function, stmt.Condition, warnings)
	consequentTerminated := lintStatements(function, stmt.Consequent, warnings)
	elseIfAllTerminated := true
	for _, elseIf := range stmt.ElseIf {
		lintExpression(function, elseIf.Condition, warnings)
		if !lintStatements(function, elseIf.Consequent, warnings) {
			elseIfAllTerminated = false
		}
	}
	if len(stmt.Alternate) == 0 {
		return false
	}
	alternateTerminated := lintStatements(function, stmt.Alternate, warnings)
	return consequentTerminated && elseIfAllTerminated && alternateTerminated
}

func lintExpression(function string, expr ast.Expression, warnings *[]Warning) {
	switch typed := expr.(type) {
	case nil:
		return
	case *ast.ArrayLiteral:
		for _, elem := range typed.Elements {
			lintExpression(function, elem, warnings)
		}
	case *ast.HashLiteral:
		for _, pair := range typed.Pairs {
			lintExpression(function, pair.Key, warnings)
			lintExpression(function, pair.Value, warnings)
		}
	case *ast.CallExpr:
		lintExpression(function, typed.Callee, warnings)
		for _, arg := range typed.Args {
			lintExpression(function, arg, warnings)
		}
		for _, kwarg := range typed.KwArgs {
			lintExpression(function, kwarg.Value, warnings)
		}
		lintBlockLiteral(function, typed.Block, warnings)
	case *ast.MemberExpr:
		lintExpression(function, typed.Object, warnings)
	case *ast.ScopeExpr:
		lintExpression(function, typed.Object, warnings)
	case *ast.IndexExpr:
		lintExpression(function, typed.Object, warnings)
		for _, index := range typed.Indices {
			lintExpression(function, index, warnings)
		}
	case *ast.DestructureTarget:
		for _, element := range typed.Elements {
			lintExpression(function, element.Target, warnings)
		}
	case *ast.UnaryExpr:
		lintExpression(function, typed.Right, warnings)
	case *ast.BinaryExpr:
		lintExpression(function, typed.Left, warnings)
		lintExpression(function, typed.Right, warnings)
	case *ast.ConditionalExpr:
		lintExpression(function, typed.Condition, warnings)
		lintExpression(function, typed.Consequent, warnings)
		lintExpression(function, typed.Alternate, warnings)
	case *ast.RescueModifierExpr:
		lintExpression(function, typed.Body, warnings)
		lintExpression(function, typed.Fallback, warnings)
	case *ast.IfExpr:
		lintExpression(function, typed.Condition, warnings)
		lintExpression(function, typed.Consequent, warnings)
		for _, branch := range typed.ElseIf {
			lintExpression(function, branch.Condition, warnings)
			lintExpression(function, branch.Result, warnings)
		}
		lintExpression(function, typed.Alternate, warnings)
	case *ast.RangeExpr:
		lintExpression(function, typed.Start, warnings)
		lintExpression(function, typed.End, warnings)
	case *ast.CaseExpr:
		lintExpression(function, typed.Target, warnings)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				lintExpression(function, value.Expr, warnings)
			}
			lintExpression(function, clause.Result, warnings)
		}
		lintExpression(function, typed.ElseExpr, warnings)
	case *ast.BlockLiteral:
		lintBlockLiteral(function, typed, warnings)
	case *ast.YieldExpr:
		for _, arg := range typed.Args {
			lintExpression(function, arg, warnings)
		}
	case *ast.InterpolatedString:
		lintStringParts(function, typed.Parts, warnings)
	case *ast.InterpolatedSymbol:
		lintStringParts(function, typed.Parts, warnings)
	case *ast.ForStmt, *ast.WhileStmt, *ast.UntilStmt, *ast.TryStmt:
		statementTerminates(function, typed.(ast.Statement), warnings)
	}
}

func lintStringParts(function string, parts []ast.StringPart, warnings *[]Warning) {
	for _, part := range parts {
		if exprPart, ok := part.(ast.StringExpr); ok {
			lintExpression(function, exprPart.Expr, warnings)
		}
	}
}

func lintBlockLiteral(function string, block *ast.BlockLiteral, warnings *[]Warning) {
	if block == nil {
		return
	}
	scope := fmt.Sprintf("%s block at %d:%d", function, block.Pos().Line, block.Pos().Column)
	lintStatements(scope, block.Body, warnings)
}
