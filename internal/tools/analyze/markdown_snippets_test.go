package analyze

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/runtime"
)

type markdownSnippet struct {
	Path   string
	Line   int
	Source string
	Hash   string
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

var markdownSnippetPolicies = []markdownSnippetPolicy{
	{
		Path:   "README.md",
		Line:   20,
		Hash:   "7633934f4d14",
		Mode:   markdownSnippetKnownFailure,
		Reason: "#214 tracks trailing commas in hash literals",
	},
	{
		Path:   "docs/hashes.md",
		Line:   5,
		Hash:   "e2d2b177ca01",
		Mode:   markdownSnippetKnownFailure,
		Reason: "#214 tracks trailing commas in hash literals",
	},
	{
		Path:   "docs/language_reference.md",
		Line:   178,
		Hash:   "d274d3dbfc8a",
		Mode:   markdownSnippetKnownFailure,
		Reason: "#208 tracks function-level rescue and ensure syntax",
	},
}

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

	for _, snippet := range snippets {
		switch {
		case snippet.Path == "README.md":
			sawREADME = true
		case strings.HasPrefix(snippet.Path, "docs/examples/"):
			sawDocsExamples = true
		case strings.HasPrefix(snippet.Path, "docs/"):
			sawReferenceDocs = true
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

func checkMarkdownSnippet(engine *runtime.Engine, snippet markdownSnippet, policy markdownSnippetPolicy, hasPolicy bool) error {
	source := snippet.Source
	if hasPolicy && policy.Mode == markdownSnippetWrapped {
		source = wrapMarkdownSnippet(source)
	}
	if hasPolicy && policy.Mode == markdownSnippetKnownFailure && shouldWrapReferenceSnippet(snippet.Path) {
		_, err := compileAndAnalyzeMarkdownSnippet(engine, wrapMarkdownSnippet(source))
		return err
	}
	compileFailed, err := compileAndAnalyzeMarkdownSnippet(engine, source)
	if compileFailed && !hasPolicy && shouldWrapReferenceSnippet(snippet.Path) {
		_, err = compileAndAnalyzeMarkdownSnippet(engine, wrapMarkdownSnippet(source))
	}
	return err
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
		snippets = append(snippets, extractMarkdownVibeSnippets(rel, string(data))...)
	}
	return snippets, nil
}

func extractMarkdownVibeSnippets(path, markdown string) []markdownSnippet {
	lines := strings.Split(markdown, "\n")
	var snippets []markdownSnippet
	var current []string
	startLine := 0
	inVibeFence := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inVibeFence {
			if isVibeFenceStart(trimmed) {
				inVibeFence = true
				current = current[:0]
				startLine = i + 1
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			source := strings.TrimSpace(strings.Join(current, "\n"))
			if source != "" {
				source += "\n"
				snippets = append(snippets, markdownSnippet{
					Path:   path,
					Line:   startLine,
					Source: source,
					Hash:   markdownSnippetHash(source),
				})
			}
			inVibeFence = false
			continue
		}
		current = append(current, line)
	}

	return snippets
}

func isVibeFenceStart(line string) bool {
	if !strings.HasPrefix(line, "```") {
		return false
	}
	language := strings.TrimSpace(strings.TrimPrefix(line, "```"))
	return language == "vibe" || strings.HasPrefix(language, "vibe ")
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
		first := warnings[0]
		return false, fmt.Errorf("analyze: %s at %d:%d in %s", first.Message, first.Pos.Line, first.Pos.Column, first.Function)
	}
	return false, nil
}

func shouldWrapReferenceSnippet(path string) bool {
	return path != "README.md" && strings.HasPrefix(path, "docs/") && !strings.HasPrefix(path, "docs/examples/")
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
