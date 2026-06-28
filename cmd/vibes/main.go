package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	vibesruntime "github.com/mgomes/vibescript/internal/runtime"
	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
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
	case "fmt":
		return fmtCommand(args[2:])
	case "analyze":
		return analyzeCommand(args[2:])
	case "test":
		return testCommand(args[2:])
	case "lsp":
		return runLSP()
	case "repl":
		return runREPL()
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
	checkOnly := fs.Bool("check", false, "compile and validate static contracts without executing")
	snippet := fs.String("e", "", "evaluate an inline snippet instead of a script file")
	watch := fs.Bool("watch", false, "re-run whenever the script or its modules change")
	var modulePaths pathList
	fs.Var(&modulePaths, "module-path", "add a module search directory (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	functionSet := flagWasSet(fs, "function")
	if flagWasSet(fs, "e") {
		switch {
		case *watch:
			return errors.New("vibes run: -e cannot be combined with -watch")
		case functionSet:
			return errors.New("vibes run: -e cannot be combined with -function")
		case len(fs.Args()) > 0:
			return errors.New("vibes run: -e does not accept positional arguments")
		}
		return evalSnippet(context.Background(), *snippet, modulePaths, *checkOnly, os.Stdout)
	}

	remaining := fs.Args()
	if len(remaining) == 0 {
		return errors.New("vibes run: script path required")
	}
	absScriptPath, err := filepath.Abs(remaining[0])
	if err != nil {
		return fmt.Errorf("resolve script path: %w", err)
	}
	moduleDirs, err := computeModulePaths(filepath.Dir(absScriptPath), modulePaths)
	if err != nil {
		return fmt.Errorf("compute module paths: %w", err)
	}
	inv := runInvocation{
		scriptPath:  absScriptPath,
		function:    *function,
		functionSet: functionSet,
		checkOnly:   *checkOnly,
		moduleDirs:  moduleDirs,
		callArgs:    stringArgs(remaining[1:]),
	}

	if *watch {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return watchScript(ctx, inv, defaultWatchInterval, os.Stdout, os.Stderr)
	}
	return executeScript(context.Background(), inv, os.Stdout)
}

// runInvocation captures everything needed to execute a script file once,
// so single runs and watch-mode re-runs share one code path.
type runInvocation struct {
	scriptPath  string
	function    string
	functionSet bool
	checkOnly   bool
	moduleDirs  []string
	callArgs    []value.Value
}

func executeScript(ctx context.Context, inv runInvocation, out io.Writer) error {
	engine, err := vibes.NewEngine(vibes.Config{ModulePaths: inv.moduleDirs})
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}
	input, err := readScriptSource(engine, inv.scriptPath)
	if err != nil {
		return fmt.Errorf("read script: %w", err)
	}
	script, err := engine.CompileSnippet(string(input), scriptEntrypointFunction)
	if err != nil {
		return fmt.Errorf("compile failed: %w", err)
	}
	if inv.checkOnly {
		return checkCompiledScript(script)
	}
	function := inv.function
	if !inv.functionSet && scriptEntrypointHasBody(script) {
		function = scriptEntrypointFunction
	}
	result, err := script.Call(ctx, function, inv.callArgs, vibes.CallOptions{})
	if err != nil {
		return fmt.Errorf("execution failed: %w", err)
	}
	return printResult(out, result)
}

// maxResultRenderBytes caps how large a result rendering the CLI will
// materialize. The runtime call returns before the result is formatted, so the
// rendering path has no step or memory quota to charge against; without a cap a
// script that returns a huge nested array or hash could make the CLI allocate
// the whole formatted string in host memory. The cap matches the 1 MiB stdlib
// output guards (see internal/runtime/limits.go); keep README and
// docs/tooling.md in sync when changing it.
const maxResultRenderBytes = 1 << 20

// printResult writes a non-nil result to out using a bounded rendering so a
// large composite cannot allocate the formatted string without bound. When the
// rendering trips the byte cap it reports an error instead of printing
// truncated output, so the CLI never silently drops part of a result.
func printResult(out io.Writer, result value.Value) error {
	if result.IsNil() {
		return nil
	}
	rendered, err := result.StringBounded(maxResultRenderBytes)
	if err != nil {
		if errors.Is(err, value.ErrStringRenderTruncated) {
			return fmt.Errorf("result rendering exceeds %d bytes; reduce the returned value or stream it from the script", maxResultRenderBytes)
		}
		return fmt.Errorf("render result: %w", err)
	}
	fmt.Fprintln(out, rendered)
	return nil
}

const scriptEntrypointFunction = "<script>"

func scriptEntrypointHasBody(script *vibes.Script) bool {
	fn, ok := script.Function(scriptEntrypointFunction)
	return ok && len(fn.Body) > 0
}

// evalSnippetFunction is the synthetic function that executes -e snippets.
// Snippet compilation keeps declarations at top level and moves executable
// top-level statements into this entrypoint.
const evalSnippetFunction = "__eval__"

var evalSnippetSourceMap = snippetSourceMap{
	syntheticFunction: evalSnippetFunction,
	displayFunction:   "<snippet>",
}

func evalSnippet(ctx context.Context, snippet string, modulePaths []string, checkOnly bool, out io.Writer) error {
	if strings.TrimSpace(snippet) == "" {
		return errors.New("vibes run: -e requires a non-empty snippet")
	}
	workDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	moduleDirs, err := computeModulePaths(workDir, modulePaths)
	if err != nil {
		return fmt.Errorf("compute module paths: %w", err)
	}
	engine, err := vibes.NewEngine(vibes.Config{ModulePaths: moduleDirs})
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}
	script, err := engine.CompileSnippet(snippet, evalSnippetFunction)
	if err != nil {
		return fmt.Errorf("compile failed: %w", remapSnippetCompileError(err, snippet, evalSnippetSourceMap))
	}
	if checkOnly {
		return checkCompiledScript(script)
	}
	result, err := script.Call(ctx, evalSnippetFunction, nil, vibes.CallOptions{})
	if err != nil {
		return fmt.Errorf("execution failed: %w", remapSnippetRuntimeError(err, snippet, evalSnippetSourceMap))
	}
	return printResult(out, result)
}

func checkCompiledScript(script *vibes.Script) error {
	warnings := script.CheckWarnings()
	if len(warnings) == 0 {
		return nil
	}
	return formatCheckWarnings(warnings)
}

func formatCheckWarnings(warnings []vibesruntime.CheckWarning) error {
	if len(warnings) == 1 {
		return fmt.Errorf("check failed: %s", formatCheckWarning(warnings[0]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "check failed with %d issue(s):", len(warnings))
	for _, warning := range warnings {
		fmt.Fprintf(&b, "\n  %s", formatCheckWarning(warning))
	}
	return errors.New(b.String())
}

func formatCheckWarning(warning vibesruntime.CheckWarning) string {
	line := warning.Pos.Line
	column := warning.Pos.Column
	if line <= 0 {
		line = 1
	}
	if column <= 0 {
		column = 1
	}
	if warning.Function == "" {
		return fmt.Sprintf("%d:%d: %s", line, column, warning.Message)
	}
	return fmt.Sprintf("%d:%d: %s (%s)", line, column, warning.Message, warning.Function)
}

func stringArgs(raw []string) []value.Value {
	out := make([]value.Value, len(raw))
	for i, arg := range raw {
		out[i] = value.NewString(arg)
	}
	return out
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func usageError() error {
	printUsage()
	return errors.New("invalid command")
}

func printUsage() {
	prog := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [flags] [args...]\n\n", prog)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  run <script>    Execute a script file")
	fmt.Fprintln(os.Stderr, "  fmt <path>      Canonical formatting for .vibe files")
	fmt.Fprintln(os.Stderr, "  analyze <script> Analyze a script for lint issues")
	fmt.Fprintln(os.Stderr, "  test [path...]  Run *_test.vibe files (-run <regexp> to filter)")
	fmt.Fprintln(os.Stderr, "  lsp             Start language server (stdio)")
	fmt.Fprintln(os.Stderr, "  repl            Start interactive REPL")
	fmt.Fprintln(os.Stderr, "  help            Show this help message")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run flags:")
	fmt.Fprintln(os.Stderr, "  -function string")
	fmt.Fprintln(os.Stderr, "    function to invoke after compilation (default \"run\")")
	fmt.Fprintln(os.Stderr, "  -check")
	fmt.Fprintln(os.Stderr, "    compile and validate static contracts without executing")
	fmt.Fprintln(os.Stderr, "  -e <snippet>")
	fmt.Fprintln(os.Stderr, "    evaluate an inline snippet instead of a script file")
	fmt.Fprintln(os.Stderr, "  -watch")
	fmt.Fprintln(os.Stderr, "    re-run whenever the script or its modules change")
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

func computeModulePaths(baseDir string, extras []string) ([]string, error) {
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
	if err := addPath("script directory", baseDir); err != nil {
		return nil, err
	}
	for _, extra := range extras {
		if err := addPath("module path", extra); err != nil {
			return nil, err
		}
	}
	return dirs, nil
}
