package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mgomes/vibescript/vibes"
)

type lintWarning struct {
	Function string
	Pos      vibes.Position
	Message  string
}

func analyzeCommand(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(new(flagErrorSink))
	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return errors.New("vibes analyze: script path required")
	}

	scriptPath, err := filepath.Abs(remaining[0])
	if err != nil {
		return fmt.Errorf("resolve script path: %w", err)
	}
	input, err := os.ReadFile(scriptPath)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(string(input))
	if err != nil {
		return fmt.Errorf("analysis compile failed: %w", err)
	}

	warnings := analyzeScriptWarnings(script)
	if len(warnings) == 0 {
		fmt.Println("No issues found")
		return nil
	}

	for _, warning := range warnings {
		line := warning.Pos.Line
		column := warning.Pos.Column
		if line <= 0 {
			line = 1
		}
		if column <= 0 {
			column = 1
		}
		fmt.Printf("%s:%d:%d: %s (%s)\n", scriptPath, line, column, warning.Message, warning.Function)
	}

	return fmt.Errorf("analysis found %d issue(s)", len(warnings))
}

func analyzeScriptWarnings(script *vibes.Script) []lintWarning {
	warnings := make([]lintWarning, 0)
	for _, fn := range script.Functions() {
		lintStatements(fn.Name, fn.Body, &warnings)
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

func lintStatements(function string, statements []vibes.Statement, warnings *[]lintWarning) bool {
	terminated := false
	for _, stmt := range statements {
		if terminated {
			*warnings = append(*warnings, lintWarning{
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

func statementTerminates(function string, stmt vibes.Statement, warnings *[]lintWarning) bool {
	switch typed := stmt.(type) {
	case *vibes.ReturnStmt, *vibes.RaiseStmt:
		return true
	case *vibes.IfStmt:
		return ifStatementTerminates(function, typed, warnings)
	case *vibes.ForStmt:
		lintStatements(function, typed.Body, warnings)
		return false
	case *vibes.WhileStmt:
		lintStatements(function, typed.Body, warnings)
		return false
	case *vibes.UntilStmt:
		lintStatements(function, typed.Body, warnings)
		return false
	case *vibes.TryStmt:
		bodyTerminated := lintStatements(function, typed.Body, warnings)
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
		if len(typed.Rescue) == 0 {
			return false
		}
		return bodyTerminated && rescueTerminated
	default:
		return false
	}
}

func ifStatementTerminates(function string, stmt *vibes.IfStmt, warnings *[]lintWarning) bool {
	consequentTerminated := lintStatements(function, stmt.Consequent, warnings)
	if len(stmt.Alternate) == 0 {
		return false
	}

	elseIfAllTerminated := true
	for _, elseIf := range stmt.ElseIf {
		if !ifStatementTerminates(function, elseIf, warnings) {
			elseIfAllTerminated = false
		}
	}
	alternateTerminated := lintStatements(function, stmt.Alternate, warnings)
	return consequentTerminated && elseIfAllTerminated && alternateTerminated
}
