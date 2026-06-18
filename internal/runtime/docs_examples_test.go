package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocsExampleSnippetsCompile(t *testing.T) {
	t.Parallel()
	docsExamplesDir := filepath.Join("..", "..", "docs", "examples")
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

func TestAutomatedRewardsExampleRewardThresholds(t *testing.T) {
	t.Parallel()
	snippet := readDocsExampleSnippet(t, "automated_rewards.md")
	script := compileScript(t, snippet)

	tests := []struct {
		name  string
		cents int64
		want  Value
	}{
		{name: "gold", cents: 50_000, want: NewString("gold")},
		{name: "silver", cents: 25_000, want: NewString("silver")},
		{name: "none", cents: 24_999, want: NewNil()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			moneyValue, err := newMoneyFromCents(tc.cents, "USD")
			if err != nil {
				t.Fatalf("newMoneyFromCents(%d, USD): %v", tc.cents, err)
			}
			got := callScript(t, t.Context(), script, "reward_for_total", []Value{NewMoney(moneyValue)}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Fatalf("reward_for_total(%d cents) = %s, want %s", tc.cents, got, tc.want)
			}
		})
	}
}

func readDocsExampleSnippet(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "examples", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	snippets := extractVibeCodeFences(string(data))
	if len(snippets) != 1 {
		t.Fatalf("%s has %d vibe snippets, want 1", name, len(snippets))
	}
	return snippets[0]
}

func extractVibeCodeFences(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	var snippets []string
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
