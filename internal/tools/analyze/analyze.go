// Package analyze provides static checks for compiled Vibescript
// programs. It walks the internal AST and reports issues such as
// unreachable statements following a terminator.
//
// The package is internal because it depends on the AST shape. The
// vibes command exposes the same behavior via "vibes analyze".
package analyze

import (
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
	case *ast.ReturnStmt, *ast.RaiseStmt:
		return true
	case *ast.IfStmt:
		return ifStatementTerminates(function, typed, warnings)
	case *ast.ForStmt:
		lintStatements(function, typed.Body, warnings)
		return false
	case *ast.WhileStmt:
		lintStatements(function, typed.Body, warnings)
		return false
	case *ast.UntilStmt:
		lintStatements(function, typed.Body, warnings)
		return false
	case *ast.TryStmt:
		bodyTerminated := lintStatements(function, typed.Body, warnings)
		elseTerminated := false
		if len(typed.Else) > 0 {
			elseTerminated = lintStatements(function, typed.Else, warnings)
		}
		rescueTerminated := false
		if len(typed.Rescue) > 0 {
			rescueTerminated = lintStatements(function, typed.Rescue, warnings)
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
		if len(typed.Rescue) == 0 {
			return normalTerminated
		}
		return normalTerminated && rescueTerminated
	default:
		return false
	}
}

func ifStatementTerminates(function string, stmt *ast.IfStmt, warnings *[]Warning) bool {
	consequentTerminated := lintStatements(function, stmt.Consequent, warnings)
	elseIfAllTerminated := true
	for _, elseIf := range stmt.ElseIf {
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
