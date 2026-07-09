package main

import (
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/mgomes/vibescript/vibes"
)

// checkCommand implements `vibes check <script>`: it compiles the script and
// reports every statically checkable contract issue across the whole script —
// all functions, class methods, and top-level code — using the same semantic
// contract as `vibes run -check` (ADR-004: error on known contradictions,
// permit unknowns).
func checkCommand(args []string) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(new(flagErrorSink))
	var modulePaths pathList
	fs.Var(&modulePaths, "module-path", "add a module search directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return errors.New("vibes check: script path required")
	}
	if len(remaining) > 1 {
		return errors.New("vibes check: expected a single script path")
	}

	scriptPath, err := filepath.Abs(remaining[0])
	if err != nil {
		return fmt.Errorf("resolve script path: %w", err)
	}
	moduleDirs, err := computeModulePaths(filepath.Dir(scriptPath), modulePaths)
	if err != nil {
		return fmt.Errorf("compute module paths: %w", err)
	}
	engine, err := vibes.NewEngine(vibes.Config{ModulePaths: moduleDirs})
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
		if warning.Function == "" {
			fmt.Printf("%s:%d:%d: %s\n", scriptPath, line, column, warning.Message)
			continue
		}
		fmt.Printf("%s:%d:%d: %s (%s)\n", scriptPath, line, column, warning.Message, warning.Function)
	}
	return fmt.Errorf("check failed with %d issue(s)", len(warnings))
}
