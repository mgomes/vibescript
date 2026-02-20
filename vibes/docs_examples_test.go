package vibes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsExampleSnippetsCompile(t *testing.T) {
	docsExamplesDir := filepath.Join("..", "docs", "examples")
	entries, err := os.ReadDir(docsExamplesDir)
	if err != nil {
		t.Fatalf("read docs/examples: %v", err)
	}

	engine := MustNewEngine(Config{})
	totalSnippets := 0

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(docsExamplesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		snippets := extractVibeCodeFences(string(data))
		for idx, snippet := range snippets {
			if _, err := engine.Compile(snippet); err != nil {
				t.Fatalf("%s snippet #%d compile failed: %v\n%s", entry.Name(), idx+1, err, snippet)
			}
			totalSnippets++
		}
	}

	if totalSnippets == 0 {
		t.Fatalf("expected at least one ```vibe fenced snippet in docs/examples")
	}
}

func extractVibeCodeFences(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	snippets := make([]string, 0)
	var current []string
	inVibeFence := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inVibeFence {
			if strings.HasPrefix(trimmed, "```vibe") {
				inVibeFence = true
				current = current[:0]
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			snippet := strings.TrimSpace(strings.Join(current, "\n"))
			if snippet != "" {
				snippets = append(snippets, snippet+"\n")
			}
			inVibeFence = false
			continue
		}
		current = append(current, line)
	}

	return snippets
}
