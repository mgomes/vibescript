package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mgomes/vibescript/vibes"
)

// testFunctionPrefix marks the functions a *_test.vibe file exposes as tests.
const testFunctionPrefix = "test_"

func testCommand(args []string) error {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(new(flagErrorSink))
	runFilter := flags.String("run", "", "run only test functions matching this regular expression")
	var modulePaths pathList
	flags.Var(&modulePaths, "module-path", "add a module search directory (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	var filter *regexp.Regexp
	if *runFilter != "" {
		compiled, err := regexp.Compile(*runFilter)
		if err != nil {
			return fmt.Errorf("vibes test: invalid -run pattern: %w", err)
		}
		filter = compiled
	}

	roots := flags.Args()
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

	summary := runTestFiles(context.Background(), files, modulePaths, filter, os.Stdout)
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

func runTestFiles(ctx context.Context, files []string, modulePaths pathList, filter *regexp.Regexp, out io.Writer) testSummary {
	var summary testSummary
	for _, file := range files {
		fileSummary := runTestFile(ctx, file, modulePaths, filter, out)
		summary.passed += fileSummary.passed
		summary.failed += fileSummary.failed
	}
	return summary
}

func runTestFile(ctx context.Context, file string, modulePaths pathList, filter *regexp.Regexp, out io.Writer) testSummary {
	var summary testSummary
	failTest := func(name string, err error) {
		summary.failed++
		fmt.Fprintf(out, "--- FAIL: %s :: %s\n%s\n", file, name, indentLines(err.Error(), "    "))
	}

	source, err := os.ReadFile(file)
	if err != nil {
		failTest("(read)", err)
		return summary
	}
	absPath, err := filepath.Abs(file)
	if err != nil {
		failTest("(resolve)", err)
		return summary
	}
	moduleDirs, err := computeModulePaths(filepath.Dir(absPath), modulePaths)
	if err != nil {
		failTest("(module paths)", err)
		return summary
	}
	engine, err := vibes.NewEngine(vibes.Config{ModulePaths: moduleDirs})
	if err != nil {
		failTest("(engine)", err)
		return summary
	}
	script, err := engine.Compile(string(source))
	if err != nil {
		failTest("(compile)", err)
		return summary
	}

	names := testFunctionNames(script, filter)
	if len(names) == 0 {
		fmt.Fprintf(out, "ok   %s (no test functions)\n", file)
		return summary
	}

	for _, name := range names {
		if err := runTestFunction(ctx, script, name); err != nil {
			failTest(name, err)
			continue
		}
		summary.passed++
	}
	if summary.failed == 0 {
		fmt.Fprintf(out, "ok   %s (%d test(s))\n", file, len(names))
	}
	return summary
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
		if param.Kind == vibes.ParamNormal && param.DefaultVal == nil {
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
