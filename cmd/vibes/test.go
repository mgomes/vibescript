package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgomes/vibescript/vibes"
	"github.com/urfave/cli/v3"
)

// testFunctionPrefix marks the functions a *_test.vibe file exposes as tests.
const testFunctionPrefix = "test_"

func testCommand(args []string) error {
	return runStandaloneCommand(context.Background(), newTestCommand(), args)
}

func newTestCommand() *cli.Command {
	stopAfterPath := 1
	quotaFlags := newQuotaFlags()
	flags := make([]cli.Flag, 0, 2+len(quotaFlags))
	flags = append(flags,
		&cli.StringFlag{
			Name:  "run",
			Usage: "run only test functions matching this regular expression",
		},
		&cli.StringSliceFlag{
			Name:      "module-path",
			Usage:     "add a module search directory (repeatable)",
			TakesFile: true,
		},
	)
	flags = append(flags, quotaFlags...)
	return configureCLICommand(&cli.Command{
		Name:                      "test",
		Usage:                     "discover and run Vibescript tests",
		ArgsUsage:                 "[path...]",
		Flags:                     flags,
		StopOnNthArg:              &stopAfterPath,
		DisableSliceFlagSeparator: true,
		Action:                    testAction,
	})
}

func testAction(ctx context.Context, command *cli.Command) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	quota, err := resolveCommandQuota(command)
	if err != nil {
		return fmt.Errorf("vibes test: %w", err)
	}

	var filter *regexp.Regexp
	if command.String("run") != "" {
		compiled, err := regexp.Compile(command.String("run"))
		if err != nil {
			return fmt.Errorf("vibes test: invalid -run pattern: %w", err)
		}
		filter = compiled
	}

	roots := commandPositionalArgs(command)
	if len(roots) == 0 {
		roots = []string{"."}
	}
	files, err := discoverTestFiles(roots)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("vibes test: no *_test.vibe files found under %s", strings.Join(roots, ", "))
	}

	summary, err := runTestFiles(ctx, files, pathList(command.StringSlice("module-path")), quota, filter, os.Stdout)
	if err != nil {
		return fmt.Errorf("vibes test: %w", err)
	}
	fmt.Printf("%d test(s) across %d file(s): %d passed, %d failed\n",
		summary.passed+summary.failed, len(files), summary.passed, summary.failed)
	if summary.failed > 0 {
		return fmt.Errorf("vibes test: %d test(s) failed", summary.failed)
	}
	return nil
}

// discoverTestFiles expands the given paths into a sorted, deduplicated
// list of *_test.vibe files. Directories are walked recursively; files
// passed explicitly must already follow the naming convention.
func discoverTestFiles(roots []string) ([]string, error) {
	seen := make(map[string]struct{})
	var files []string
	add := func(path string) {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}

	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("vibes test: access %q: %w", root, err)
		}
		if !info.IsDir() {
			if !isTestFileName(root) {
				return nil, fmt.Errorf("vibes test: %q is not a *_test.vibe file", root)
			}
			add(filepath.Clean(root))
			continue
		}
		walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && isTestFileName(path) {
				add(filepath.Clean(path))
			}
			return nil
		})
		if walkErr != nil {
			return nil, fmt.Errorf("vibes test: walk %q: %w", root, walkErr)
		}
	}
	sort.Strings(files)
	return files, nil
}

func isTestFileName(path string) bool {
	return strings.HasSuffix(filepath.Base(path), "_test.vibe")
}

type testSummary struct {
	passed int
	failed int
}

func runTestFiles(ctx context.Context, files []string, modulePaths pathList, quota quotaConfig, filter *regexp.Regexp, out io.Writer) (testSummary, error) {
	var summary testSummary
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		fileSummary, err := runTestFile(ctx, file, modulePaths, quota, filter, out)
		if err != nil {
			return summary, err
		}
		summary.passed += fileSummary.passed
		summary.failed += fileSummary.failed
	}
	return summary, nil
}

func runTestFile(ctx context.Context, file string, modulePaths pathList, quota quotaConfig, filter *regexp.Regexp, out io.Writer) (testSummary, error) {
	var summary testSummary
	failTest := func(name string, err error) {
		summary.failed++
		fmt.Fprintf(out, "--- FAIL: %s :: %s\n%s\n", file, name, indentLines(err.Error(), "    "))
	}

	absPath, err := filepath.Abs(file)
	if err != nil {
		failTest("(resolve)", err)
		return summary, nil
	}
	moduleDirs, err := computeModulePaths(filepath.Dir(absPath), modulePaths)
	if err != nil {
		failTest("(module paths)", err)
		return summary, nil
	}
	// Wire the output helpers (puts/print/p/warn) to the test command's streams
	// so a *_test.vibe that prints does not fail on an unconfigured writer.
	cfg := vibes.Config{ModulePaths: moduleDirs, OutputWriter: out, ErrorWriter: os.Stderr}
	quota.applyTo(&cfg)
	engine, err := vibes.NewEngine(cfg)
	if err != nil {
		failTest("(engine)", err)
		return summary, nil
	}
	source, err := readScriptSource(engine, file)
	if err != nil {
		failTest("(read)", err)
		return summary, nil
	}
	script, err := engine.Compile(string(source))
	if err != nil {
		failTest("(compile)", err)
		return summary, nil
	}

	names := testFunctionNames(script, filter)
	if len(names) == 0 {
		fmt.Fprintf(out, "ok   %s (no test functions)\n", file)
		return summary, nil
	}

	for _, name := range names {
		if err := runTestFunction(ctx, script, name); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return summary, ctxErr
			}
			failTest(name, err)
			continue
		}
		summary.passed++
	}
	if summary.failed == 0 {
		fmt.Fprintf(out, "ok   %s (%d test(s))\n", file, len(names))
	}
	return summary, nil
}

// testFunctionNames returns the script's test_ functions in deterministic
// order, narrowed by the optional -run filter.
func testFunctionNames(script *vibes.Script, filter *regexp.Regexp) []string {
	var names []string
	for _, fn := range script.Functions() {
		if !strings.HasPrefix(fn.Name, testFunctionPrefix) {
			continue
		}
		if filter != nil && !filter.MatchString(fn.Name) {
			continue
		}
		names = append(names, fn.Name)
	}
	return names
}

func runTestFunction(ctx context.Context, script *vibes.Script, name string) error {
	fn, ok := script.Function(name)
	if !ok {
		return fmt.Errorf("function %s not found", name)
	}
	for _, param := range fn.Params {
		if (param.Kind == vibes.ParamNormal || param.Kind == vibes.ParamKeyword) && param.DefaultVal == nil {
			return errors.New("test functions must not require parameters")
		}
	}
	_, err := script.Call(ctx, name, nil, vibes.CallOptions{})
	return err
}

func indentLines(text, prefix string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
