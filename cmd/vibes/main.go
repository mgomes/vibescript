package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vibescript/vibes"
)

func main() {
	if err := runCLI(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCLI(args []string) error {
	if len(args) < 2 {
		return usageError()
	}
	switch args[1] {
	case "run":
		return runCommand(args[2:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return usageError()
	}
}

func runCommand(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(new(flagErrorSink))
	function := fs.String("function", "run", "function to invoke after compilation")
	checkOnly := fs.Bool("check", false, "only compile the script without executing")
	var modulePaths pathList
	fs.Var(&modulePaths, "module-path", "add a module search directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return errors.New("vibes run: script path required")
	}
	scriptPath := remaining[0]
	absScriptPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return fmt.Errorf("resolve script path: %w", err)
	}
	input, err := os.ReadFile(absScriptPath)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}
	moduleDirs, err := computeModulePaths(absScriptPath, modulePaths)
	if err != nil {
		return err
	}
	engine := vibes.NewEngine(vibes.Config{ModulePaths: moduleDirs})
	script, err := engine.Compile(string(input))
	if err != nil {
		return fmt.Errorf("compile failed: %w", err)
	}
	if *checkOnly {
		return nil
	}
	argsValues := make([]vibes.Value, len(remaining)-1)
	for i, raw := range remaining[1:] {
		argsValues[i] = vibes.NewString(raw)
	}
	result, err := script.Call(context.Background(), *function, argsValues, vibes.CallOptions{})
	if err != nil {
		return fmt.Errorf("execution failed: %w", err)
	}
	if !result.IsNil() {
		fmt.Println(result.String())
	}
	return nil
}

func usageError() error {
	printUsage()
	return errors.New("invalid command")
}

func printUsage() {
	prog := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, "Usage: %s run [flags] <script> [args...]\n", prog)
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -function string")
	fmt.Fprintln(os.Stderr, "    function to invoke after compilation (default \"run\")")
	fmt.Fprintln(os.Stderr, "  -check")
	fmt.Fprintln(os.Stderr, "    only compile the script without executing")
	fmt.Fprintln(os.Stderr, "  -module-path <dir>")
	fmt.Fprintln(os.Stderr, "    add a directory to module search paths (repeatable)")
}

type flagErrorSink struct{}

func (flagErrorSink) Write(p []byte) (int, error) {
	return len(p), nil
}

type pathList []string

func (l *pathList) String() string {
	return strings.Join(*l, string(os.PathListSeparator))
}

func (l *pathList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

func computeModulePaths(scriptPath string, extras []string) ([]string, error) {
	scriptDir := filepath.Dir(scriptPath)
	seen := make(map[string]struct{})
	var dirs []string
	addPath := func(label, p string) error {
		abs, err := filepath.Abs(p)
		if err != nil {
			return fmt.Errorf("resolve %s %q: %w", label, p, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("access %s %q: %w", label, abs, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s %q is not a directory", label, abs)
		}
		if _, ok := seen[abs]; ok {
			return nil
		}
		seen[abs] = struct{}{}
		dirs = append(dirs, abs)
		return nil
	}
	if err := addPath("script directory", scriptDir); err != nil {
		return nil, err
	}
	for _, extra := range extras {
		if err := addPath("module path", extra); err != nil {
			return nil, err
		}
	}
	return dirs, nil
}
