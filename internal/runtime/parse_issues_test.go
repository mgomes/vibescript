package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestParseIssuesFromSingleParseError(t *testing.T) {
	t.Parallel()
	engine := MustNewEngine(Config{})
	_, err := engine.Compile("def run()\n  x = [1,\nend\n")
	if err == nil {
		t.Fatal("expected compile error")
	}

	issues := ParseIssues(err)
	if len(issues) == 0 {
		t.Fatal("expected parse issues")
	}
	first := issues[0]
	if first.Pos.Line != 3 || first.Pos.Column != 1 {
		t.Fatalf("issues[0].Pos = %v, want 3:1", first.Pos)
	}
	if first.End.Line != 3 || first.End.Column != 4 {
		t.Fatalf("issues[0].End = %v, want 3:4 spanning the end keyword", first.End)
	}
	if first.Message != "unexpected token 'end'" {
		t.Fatalf("issues[0].Message = %q", first.Message)
	}
}

func TestParseIssuesPreserveSourceOrderAcrossCombinedErrors(t *testing.T) {
	t.Parallel()
	engine := MustNewEngine(Config{})
	_, err := engine.Compile("def 123()\n  1\nend\n")
	if err == nil {
		t.Fatal("expected compile error")
	}

	issues := ParseIssues(err)
	if len(issues) < 2 {
		t.Fatalf("expected multiple parse issues, got %d", len(issues))
	}
	for i := 1; i < len(issues); i++ {
		prev, cur := issues[i-1].Pos, issues[i].Pos
		if cur.Line < prev.Line || (cur.Line == prev.Line && cur.Column < prev.Column) {
			t.Fatalf("issues out of source order: %v before %v", prev, cur)
		}
	}
	if !strings.Contains(err.Error(), "\n\n") {
		t.Fatalf("combined error text lost its separator: %q", err.Error())
	}
	for _, issue := range issues {
		if !strings.Contains(err.Error(), issue.Message) {
			t.Fatalf("combined error text missing issue %q", issue.Message)
		}
	}
}

func TestParseIssuesReturnNilWithoutParsePositions(t *testing.T) {
	t.Parallel()
	if got := ParseIssues(nil); got != nil {
		t.Fatalf("ParseIssues(nil) = %v, want nil", got)
	}
	if got := ParseIssues(errors.New("boom")); got != nil {
		t.Fatalf("ParseIssues(plain error) = %v, want nil", got)
	}

	engine := MustNewEngine(Config{})
	_, err := engine.Compile("def run()\n  1\nend\n\ndef run()\n  2\nend\n")
	if err == nil {
		t.Fatal("expected duplicate-function compile error")
	}
	if got := ParseIssues(err); got != nil {
		t.Fatalf("ParseIssues(duplicate function) = %v, want nil", got)
	}
}
