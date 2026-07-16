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

func newAnalyzeCommand() *cli.Command {
	var arguments []string
	stopAfterScript := 1
	return configureCLICommand(&cli.Command{
		Name:         "analyze",
		Usage:        "analyze a script for lint issues",
		ArgsUsage:    "<script>",
		StopOnNthArg: &stopAfterScript,
		Arguments:    stringArguments("script", &arguments),
		Action: func(ctx context.Context, command *cli.Command) error {
			return analyzeAction(ctx, command, arguments)
		},
	})
}

func analyzeAction(_ context.Context, command *cli.Command, arguments []string) error {
	if len(arguments) == 0 {
		return errors.New("vibes analyze: script path required")
	}
	if len(arguments) > 1 {
		return errors.New("vibes analyze: expected a single script path")
	}

	scriptPath, err := filepath.Abs(arguments[0])
	if err != nil {
		return fmt.Errorf("resolve script path: %w", err)
	}
	engine, err := vibes.NewEngine(vibes.Config{
		OutputWriter: command.Writer,
		ErrorWriter:  command.ErrWriter,
	})
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}
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
		if _, err := fmt.Fprintln(command.Writer, "No issues found"); err != nil {
			return fmt.Errorf("write analysis output: %w", err)
		}
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
		if _, err := fmt.Fprintf(command.Writer, "%s:%d:%d: %s (%s)\n", scriptPath, line, column, warning.Message, warning.Function); err != nil {
			return fmt.Errorf("write analysis output: %w", err)
		}
	}

	return fmt.Errorf("analysis found %d issue(s)", len(warnings))
}
