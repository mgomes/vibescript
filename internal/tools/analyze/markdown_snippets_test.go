package analyze

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/internal/parser"
	"github.com/mgomes/vibescript/internal/runtime"
)

type markdownSnippet struct {
	Path               string
	Line               int
	Source             string
	Hash               string
	Check              bool
	ExpectedDiagnostic string
}

type markdownVibeFence struct {
	IsVibe             bool
	Check              bool
	ExpectedDiagnostic string
}

type markdownSnippetPolicyMode int

const (
	markdownSnippetKnownFailure markdownSnippetPolicyMode = iota
	markdownSnippetWrapped
)

type markdownSnippetPolicy struct {
	Path   string
	Line   int
	Hash   string
	Mode   markdownSnippetPolicyMode
	Reason string
}

var markdownSnippetPolicies []markdownSnippetPolicy

func TestMarkdownVibeSnippetsAreCovered(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", "..", ".."))
	snippets, err := collectMarkdownVibeSnippets(root)
	if err != nil {
		t.Fatalf("collectMarkdownVibeSnippets(%q): %v", root, err)
	}
	if len(snippets) == 0 {
		t.Fatal("expected at least one fenced vibe snippet in README.md or docs/**/*.md")
	}

	policies := markdownSnippetPolicyMap(t)
	seenPolicies := make(map[string]bool, len(policies))
	engine := runtime.MustNewEngine(runtime.Config{})
	var failures []string
	sawREADME := false
	sawReferenceDocs := false
	sawDocsExamples := false
	sawCheckedSnippet := false
	sawExpectedDiagnostic := false

	for _, snippet := range snippets {
		switch {
		case snippet.Path == "README.md":
			sawREADME = true
		case strings.HasPrefix(snippet.Path, "docs/examples/"):
			sawDocsExamples = true
		case strings.HasPrefix(snippet.Path, "docs/"):
			sawReferenceDocs = true
		}
		if snippet.Check {
			sawCheckedSnippet = true
		}
		if snippet.ExpectedDiagnostic != "" {
			sawExpectedDiagnostic = true
		}

		key := markdownSnippetPolicyKey(snippet.Path, snippet.Hash)
		policy, hasPolicy := policies[key]
		if hasPolicy {
			seenPolicies[key] = true
		}

		err := checkMarkdownSnippet(engine, snippet, policy, hasPolicy)
		if hasPolicy && policy.Mode == markdownSnippetKnownFailure {
			if err == nil {
				failures = append(failures, fmt.Sprintf("%s:%d known-failing snippet now passes; remove policy %s (%s)", snippet.Path, snippet.Line, snippet.Hash, policy.Reason))
			}
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s:%d snippet %s failed: %v", snippet.Path, snippet.Line, snippet.Hash, err))
		}
	}

	for key, policy := range policies {
		if !seenPolicies[key] {
			failures = append(failures, fmt.Sprintf("%s:%d policy %s no longer matches a fenced vibe snippet (%s)", policy.Path, policy.Line, policy.Hash, policy.Reason))
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Fatalf("markdown vibe snippet gate failed:\n%s", strings.Join(failures, "\n"))
	}
	if !sawREADME {
		t.Fatal("expected at least one fenced vibe snippet in README.md")
	}
	if !sawReferenceDocs {
		t.Fatal("expected at least one fenced vibe snippet in docs/**/*.md outside docs/examples")
	}
	if !sawDocsExamples {
		t.Fatal("expected at least one fenced vibe snippet in docs/examples")
	}
	if !sawCheckedSnippet {
		t.Fatal("expected at least one fenced vibe snippet marked for type checking")
	}
	if !sawExpectedDiagnostic {
		t.Fatal("expected at least one fenced vibe snippet with an expected type-check diagnostic")
	}
}

func TestMarkdownReferenceWrapperDoesNotHideAnalyzerWarnings(t *testing.T) {
	t.Parallel()

	engine := runtime.MustNewEngine(runtime.Config{})
	snippet := markdownSnippet{
		Path: "docs/reference.md",
		Line: 1,
		Source: `def run()
  return 1
  2
end
`,
	}
	err := checkMarkdownSnippet(engine, snippet, markdownSnippetPolicy{}, false)
	if err == nil {
		t.Fatal("checkMarkdownSnippet() error = nil, want analyzer warning")
	}
	if got := err.Error(); !strings.Contains(got, "analyze: unreachable statement") {
		t.Fatalf("checkMarkdownSnippet() error = %q, want analyzer warning", got)
	}
}

func TestMarkdownKnownFailurePolicySelfInvalidatesWhenOnlyTopLevelFails(t *testing.T) {
	t.Parallel()

	engine := runtime.MustNewEngine(runtime.Config{})
	snippet := markdownSnippet{
		Path:   "docs/reference.md",
		Line:   1,
		Source: "value = 1\n",
	}
	err := checkMarkdownSnippet(engine, snippet, markdownSnippetPolicy{Mode: markdownSnippetKnownFailure}, true)
	if err != nil {
		t.Fatalf("checkMarkdownSnippet() error = %v, want nil after reference wrapper", err)
	}
}

func TestMarkdownReferenceWrapperAnalyzesDeclarationsBeforeFallback(t *testing.T) {
	t.Parallel()

	engine := runtime.MustNewEngine(runtime.Config{})
	snippet := markdownSnippet{
		Path: "docs/reference.md",
		Line: 1,
		Source: `def clean_input(text)
  return text
  text
end

clean_input("  hello  ")
`,
	}
	err := checkMarkdownSnippet(engine, snippet, markdownSnippetPolicy{}, false)
	if err == nil {
		t.Fatal("checkMarkdownSnippet() error = nil, want analyzer warning from original function")
	}
	if got := err.Error(); !strings.Contains(got, "analyze: unreachable statement") || !strings.Contains(got, "clean_input") {
		t.Fatalf("checkMarkdownSnippet() error = %q, want analyzer warning from clean_input", got)
	}
}

func TestMarkdownReferenceWrapperKeepsDeclarationsTopLevel(t *testing.T) {
	t.Parallel()

	source := `def clean_input(text)
  text.strip
end

class Formatter
  def self.name
    "formatter"
  end
end

clean_input("  hello  ")
`
	wrapped := wrapMarkdownReferenceSnippet(source)
	if strings.Contains(wrapped, "  def clean_input") {
		t.Fatalf("wrapped snippet nested function declaration:\n%s", wrapped)
	}
	if strings.Contains(wrapped, "  class Formatter") {
		t.Fatalf("wrapped snippet nested class declaration:\n%s", wrapped)
	}
	if !strings.Contains(wrapped, "def __doc_snippet__()\n  clean_input") {
		t.Fatalf("wrapped snippet did not wrap executable statements only:\n%s", wrapped)
	}
}

func TestExtractMarkdownVibeSnippetsRejectsUnterminatedFence(t *testing.T) {
	t.Parallel()

	_, err := extractMarkdownVibeSnippets("docs/broken.md", "intro\n```vibe\nvalue = 1\n")
	if err == nil {
		t.Fatal("extractMarkdownVibeSnippets() error = nil, want unterminated fence error")
	}
	if got := err.Error(); !strings.Contains(got, "docs/broken.md:2 unterminated vibe code fence") {
		t.Fatalf("extractMarkdownVibeSnippets() error = %q, want unterminated fence location", got)
	}
}

func TestExtractMarkdownVibeSnippetsParsesCheckMarkers(t *testing.T) {
	t.Parallel()

	plain := "value = 1\n"
	checked := "value = 2\n"
	rejected := "value = \"bad\"\n"
	markdown := "```vibe\n" + strings.TrimSpace(plain) + "\n```\n" +
		"```vibe check\n" + strings.TrimSpace(checked) + "\n```\n" +
		"```vibe check-error=\"expected int, got string\"\n" + strings.TrimSpace(rejected) + "\n```\n"

	got, err := extractMarkdownVibeSnippets("docs/typing.md", markdown)
	if err != nil {
		t.Fatalf("extractMarkdownVibeSnippets() error = %v, want nil", err)
	}
	want := []markdownSnippet{
		{Path: "docs/typing.md", Line: 1, Source: plain, Hash: markdownSnippetHash(plain)},
		{Path: "docs/typing.md", Line: 4, Source: checked, Hash: markdownSnippetHash(checked), Check: true},
		{
			Path:               "docs/typing.md",
			Line:               7,
			Source:             rejected,
			Hash:               markdownSnippetHash(rejected),
			Check:              true,
			ExpectedDiagnostic: "expected int, got string",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("extractMarkdownVibeSnippets() mismatch (-want +got):\n%s", diff)
	}
}

func TestExtractMarkdownVibeSnippetsRejectsMalformedCheckMarker(t *testing.T) {
	t.Parallel()

	_, err := extractMarkdownVibeSnippets("docs/typing.md", "```vibe check-error=unquoted\nvalue = 1\n```\n")
	if err == nil {
		t.Fatal("extractMarkdownVibeSnippets() error = nil, want malformed check marker error")
	}
	if got := err.Error(); !strings.Contains(got, "docs/typing.md:1 invalid check-error marker") {
		t.Errorf("extractMarkdownVibeSnippets() error = %q, want malformed marker location", got)
	}
}

func TestMarkdownSnippetCheckExpectations(t *testing.T) {
	t.Parallel()

	engine := runtime.MustNewEngine(runtime.Config{})
	tests := []struct {
		name        string
		snippet     markdownSnippet
		wantErr     bool
		wantErrText string
	}{
		{
			name: "clean",
			snippet: markdownSnippet{
				Source: "def takes_int(value: int)\n  value\nend\n\ntakes_int(1)\n",
				Check:  true,
			},
		},
		{
			name: "expected diagnostic",
			snippet: markdownSnippet{
				Source:             "def takes_int(value: int)\n  value\nend\n\ntakes_int(\"bad\")\n",
				Check:              true,
				ExpectedDiagnostic: "argument value expected int, got string",
			},
		},
		{
			name: "unexpected diagnostic",
			snippet: markdownSnippet{
				Source: "def takes_int(value: int)\n  value\nend\n\ntakes_int(\"bad\")\n",
				Check:  true,
			},
			wantErr:     true,
			wantErrText: "unexpected diagnostic",
		},
		{
			name: "diagnostic mismatch",
			snippet: markdownSnippet{
				Source:             "def takes_int(value: int)\n  value\nend\n\ntakes_int(\"bad\")\n",
				Check:              true,
				ExpectedDiagnostic: "expected bool",
			},
			wantErr:     true,
			wantErrText: "want one containing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMarkdownSnippetTypeExpectation(engine, tt.snippet)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Errorf("checkMarkdownSnippetTypeExpectation(%q) error = %v, want error = %t", tt.snippet.Source, err, tt.wantErr)
				return
			}
			if err != nil && !strings.Contains(err.Error(), tt.wantErrText) {
				t.Errorf("checkMarkdownSnippetTypeExpectation(%q) error = %q, want substring %q", tt.snippet.Source, err, tt.wantErrText)
			}
		})
	}
}

func checkMarkdownSnippet(engine *runtime.Engine, snippet markdownSnippet, policy markdownSnippetPolicy, hasPolicy bool) error {
	source := snippet.Source
	if hasPolicy && policy.Mode == markdownSnippetWrapped {
		source = wrapMarkdownReferenceSnippet(source)
	}
	if hasPolicy && policy.Mode == markdownSnippetKnownFailure && shouldWrapReferenceSnippet(snippet.Path) {
		_, err := compileAndAnalyzeMarkdownSnippet(engine, wrapMarkdownReferenceSnippet(source))
		return err
	}
	compileFailed, err := compileAndAnalyzeMarkdownSnippet(engine, source)
	if compileFailed && !hasPolicy && shouldWrapReferenceSnippet(snippet.Path) {
		if err := analyzeMarkdownSnippetDeclarations(source); err != nil {
			return err
		}
		_, err = compileAndAnalyzeMarkdownSnippet(engine, wrapMarkdownReferenceSnippet(source))
	}
	if err != nil || !snippet.Check {
		return err
	}
	return checkMarkdownSnippetTypeExpectation(engine, snippet)
}

func checkMarkdownSnippetTypeExpectation(engine *runtime.Engine, snippet markdownSnippet) error {
	script, err := engine.CompileSnippet(snippet.Source, "__markdown_snippet__")
	if err != nil {
		return fmt.Errorf("type-check compile: %w", err)
	}
	warnings := script.CheckWarnings()
	if snippet.ExpectedDiagnostic == "" {
		if len(warnings) > 0 {
			return fmt.Errorf("type-check: unexpected diagnostic %q", warnings[0].Message)
		}
		return nil
	}
	if len(warnings) == 0 {
		return fmt.Errorf("type-check diagnostics = none, want one containing %q", snippet.ExpectedDiagnostic)
	}
	messages := make([]string, len(warnings))
	matched := false
	for i, warning := range warnings {
		messages[i] = warning.Message
		if strings.Contains(warning.Message, snippet.ExpectedDiagnostic) {
			matched = true
		}
	}
	if len(warnings) != 1 || !matched {
		return fmt.Errorf("type-check diagnostics = %q, want one containing %q", messages, snippet.ExpectedDiagnostic)
	}
	return nil
}

func markdownSnippetPolicyMap(t *testing.T) map[string]markdownSnippetPolicy {
	t.Helper()
	out := make(map[string]markdownSnippetPolicy, len(markdownSnippetPolicies))
	for _, policy := range markdownSnippetPolicies {
		if policy.Reason == "" {
			t.Fatalf("markdown snippet policy for %s:%d missing reason", policy.Path, policy.Line)
		}
		key := markdownSnippetPolicyKey(policy.Path, policy.Hash)
		if _, exists := out[key]; exists {
			t.Fatalf("duplicate markdown snippet policy for %s:%s", policy.Path, policy.Hash)
		}
		out[key] = policy
	}
	return out
}

func markdownSnippetPolicyKey(path, hash string) string {
	return path + "\x00" + hash
}

func collectMarkdownVibeSnippets(root string) ([]markdownSnippet, error) {
	paths := []string{filepath.Join(root, "README.md")}
	docsRoot := filepath.Join(root, "docs")
	if err := filepath.WalkDir(docsRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".md" {
			return nil
		}
		paths = append(paths, path)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(paths)

	var snippets []markdownSnippet
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		found, err := extractMarkdownVibeSnippets(rel, string(data))
		if err != nil {
			return nil, err
		}
		snippets = append(snippets, found...)
	}
	return snippets, nil
}

func extractMarkdownVibeSnippets(path, markdown string) ([]markdownSnippet, error) {
	lines := strings.Split(markdown, "\n")
	var snippets []markdownSnippet
	var current []string
	startLine := 0
	inVibeFence := false
	checkSnippet := false
	expectedDiagnostic := ""

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inVibeFence {
			fence, err := parseMarkdownVibeFence(trimmed)
			if err != nil {
				return nil, fmt.Errorf("%s:%d %w", path, i+1, err)
			}
			if fence.IsVibe {
				inVibeFence = true
				current = current[:0]
				startLine = i + 1
				checkSnippet = fence.Check
				expectedDiagnostic = fence.ExpectedDiagnostic
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			source := strings.TrimSpace(strings.Join(current, "\n"))
			if source != "" {
				source += "\n"
				snippets = append(snippets, markdownSnippet{
					Path:               path,
					Line:               startLine,
					Source:             source,
					Hash:               markdownSnippetHash(source),
					Check:              checkSnippet,
					ExpectedDiagnostic: expectedDiagnostic,
				})
			}
			inVibeFence = false
			continue
		}
		current = append(current, line)
	}

	if inVibeFence {
		return nil, fmt.Errorf("%s:%d unterminated vibe code fence", path, startLine)
	}
	return snippets, nil
}

func parseMarkdownVibeFence(line string) (markdownVibeFence, error) {
	if !strings.HasPrefix(line, "```") {
		return markdownVibeFence{}, nil
	}
	language := strings.TrimSpace(strings.TrimPrefix(line, "```"))
	if language != "vibe" && !strings.HasPrefix(language, "vibe ") {
		return markdownVibeFence{}, nil
	}
	fence := markdownVibeFence{IsVibe: true}
	marker := strings.TrimSpace(strings.TrimPrefix(language, "vibe"))
	if marker == "" {
		return fence, nil
	}
	if marker == "check" {
		fence.Check = true
		return fence, nil
	}
	if !strings.HasPrefix(marker, "check-error") {
		return fence, nil
	}
	if !strings.HasPrefix(marker, "check-error=") {
		return markdownVibeFence{}, fmt.Errorf("invalid check-error marker %q", marker)
	}
	expected, err := strconv.Unquote(strings.TrimPrefix(marker, "check-error="))
	if err != nil || strings.TrimSpace(expected) == "" {
		return markdownVibeFence{}, fmt.Errorf("invalid check-error marker %q", marker)
	}
	fence.Check = true
	fence.ExpectedDiagnostic = expected
	return fence, nil
}

func markdownSnippetHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])[:12]
}

func compileAndAnalyzeMarkdownSnippet(engine *runtime.Engine, source string) (bool, error) {
	script, err := engine.Compile(source)
	if err != nil {
		return true, fmt.Errorf("compile: %w", err)
	}
	if warnings := Script(script); len(warnings) > 0 {
		return false, markdownSnippetWarningError(warnings[0])
	}
	return false, nil
}

func analyzeMarkdownSnippetDeclarations(source string) error {
	program, parseErrors := parser.Parse(source)
	if len(parseErrors) > 0 {
		return nil
	}
	warnings := analyzeMarkdownSnippetProgramDeclarations(program)
	if len(warnings) == 0 {
		return nil
	}
	return markdownSnippetWarningError(warnings[0])
}

func analyzeMarkdownSnippetProgramDeclarations(program *ast.Program) []Warning {
	if program == nil {
		return nil
	}

	var warnings []Warning
	for _, stmt := range program.Statements {
		switch typed := stmt.(type) {
		case *ast.FunctionStmt:
			lintStatements(typed.Name, typed.Body, &warnings)
		case *ast.ClassStmt:
			for _, method := range typed.Methods {
				lintStatements(typed.Name+"#"+method.Name, method.Body, &warnings)
			}
			for _, method := range typed.ClassMethods {
				lintStatements(typed.Name+"."+method.Name, method.Body, &warnings)
			}
		}
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

func markdownSnippetWarningError(warning Warning) error {
	return fmt.Errorf("analyze: %s at %d:%d in %s", warning.Message, warning.Pos.Line, warning.Pos.Column, warning.Function)
}

func shouldWrapReferenceSnippet(path string) bool {
	return path != "README.md" && strings.HasPrefix(path, "docs/") && !strings.HasPrefix(path, "docs/examples/")
}

func wrapMarkdownReferenceSnippet(source string) string {
	program, parseErrors := parser.Parse(source)
	if len(parseErrors) > 0 || program == nil {
		return wrapMarkdownSnippet(source)
	}

	declarations, body := splitMarkdownSnippetTopLevelSource(source, program)
	if len(body) == 0 {
		return source
	}
	wrapped := wrapMarkdownSnippet(strings.Join(body, "\n\n") + "\n")
	if len(declarations) == 0 {
		return wrapped
	}
	return strings.Join(declarations, "\n\n") + "\n\n" + wrapped
}

func splitMarkdownSnippetTopLevelSource(source string, program *ast.Program) ([]string, []string) {
	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")
	declarations := make([]string, 0)
	body := make([]string, 0)
	for i, stmt := range program.Statements {
		start := stmt.Pos().Line - 1
		if start < 0 || start >= len(lines) {
			continue
		}
		end := len(lines)
		if i+1 < len(program.Statements) {
			end = program.Statements[i+1].Pos().Line - 1
		}
		if end < start {
			end = start
		}
		chunk := strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		if isMarkdownSnippetDeclaration(stmt) {
			declarations = append(declarations, chunk)
			continue
		}
		body = append(body, chunk)
	}
	return declarations, body
}

func isMarkdownSnippetDeclaration(stmt ast.Statement) bool {
	switch stmt.(type) {
	case *ast.FunctionStmt, *ast.ClassStmt, *ast.EnumStmt:
		return true
	default:
		return false
	}
}

func wrapMarkdownSnippet(source string) string {
	return "def __doc_snippet__()\n" + indentMarkdownSnippet(source) + "end\n"
}

func indentMarkdownSnippet(source string) string {
	lines := strings.Split(strings.TrimRight(source, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n") + "\n"
}
