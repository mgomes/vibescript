package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/mgomes/vibescript/internal/tools/analyze"
	"github.com/mgomes/vibescript/vibes"
)

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
	engine := vibes.MustNewEngine(vibes.Config{})
	input, err := readScriptSource(engine, scriptPath)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}

	script, err := engine.CompileSnippet(string(input), scriptEntrypointFunction)
	if err != nil {
		return fmt.Errorf("analysis compile failed: %w", err)
	}

	warnings := analyze.Script(script)
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
