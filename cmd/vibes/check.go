package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mgomes/vibescript/vibes"
	"github.com/urfave/cli/v3"
)

type checkCommandConfig struct {
	modulePaths []string
	arguments   []string
}

// newCheckCommand builds `vibes check <script>`: it compiles the script and
// reports every statically checkable contract issue across the whole script —
// all functions, class methods, and top-level code — using the same semantic
// contract as `vibes run -check` (ADR-004: error on known contradictions,
// permit unknowns).
func newCheckCommand() *cli.Command {
	config := new(checkCommandConfig)
	stopAfterScript := 1
	return configureCLICommand(&cli.Command{
		Name:                      "check",
		Usage:                     "statically check a script without executing it",
		ArgsUsage:                 "<script>",
		StopOnNthArg:              &stopAfterScript,
		DisableSliceFlagSeparator: true,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:        "module-path",
				Usage:       "add a module search directory (repeatable)",
				TakesFile:   true,
				Destination: &config.modulePaths,
			},
		},
		Arguments: stringArguments("script", &config.arguments),
		Action: func(ctx context.Context, command *cli.Command) error {
			return checkAction(ctx, command, config)
		},
	})
}

func checkAction(_ context.Context, command *cli.Command, config *checkCommandConfig) error {
	if len(config.arguments) == 0 {
		return errors.New("vibes check: script path required")
	}
	if len(config.arguments) > 1 {
		return errors.New("vibes check: expected a single script path")
	}

	scriptPath, err := filepath.Abs(config.arguments[0])
	if err != nil {
		return fmt.Errorf("resolve script path: %w", err)
	}
	moduleDirs, err := computeModulePaths(filepath.Dir(scriptPath), config.modulePaths)
	if err != nil {
		return fmt.Errorf("compute module paths: %w", err)
	}
	engine, err := vibes.NewEngine(vibes.Config{
		ModulePaths:  moduleDirs,
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
		return fmt.Errorf("compile failed: %w", err)
	}

	warnings := script.CheckWarningsWithOptions(vibes.CallOptions{})
	if len(warnings) == 0 {
		if _, err := fmt.Fprintln(command.Writer, "No issues found"); err != nil {
			return fmt.Errorf("write check output: %w", err)
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
		source := warning.Source
		if source == "" {
			source = scriptPath
		}
		if warning.Function == "" {
			if _, err := fmt.Fprintf(command.Writer, "%s:%d:%d: %s\n", source, line, column, warning.Message); err != nil {
				return fmt.Errorf("write check output: %w", err)
			}
			continue
		}
		if _, err := fmt.Fprintf(command.Writer, "%s:%d:%d: %s (%s)\n", source, line, column, warning.Message, warning.Function); err != nil {
			return fmt.Errorf("write check output: %w", err)
		}
	}
	return fmt.Errorf("check failed with %d issue(s)", len(warnings))
}
