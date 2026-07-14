package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mgomes/vibescript/internal/tools/analyze"
	"github.com/mgomes/vibescript/vibes"
	"github.com/urfave/cli/v3"
)

func analyzeCommand(args []string) error {
	return runStandaloneCommand(context.Background(), newAnalyzeCommand(), args)
}

func newAnalyzeCommand() *cli.Command {
	stopAfterScript := 1
	return configureCLICommand(&cli.Command{
		Name:         "analyze",
		Usage:        "analyze a script for lint issues",
		ArgsUsage:    "<script>",
		StopOnNthArg: &stopAfterScript,
		Action:       analyzeAction,
	})
}

func analyzeAction(_ context.Context, command *cli.Command) error {
	remaining := commandPositionalArgs(command)
	if len(remaining) == 0 {
		return errors.New("vibes analyze: script path required")
	}
	if len(remaining) > 1 {
		return errors.New("vibes analyze: expected a single script path")
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
