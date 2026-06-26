package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserParallelAssignmentTargets(t *testing.T) {
	t.Parallel()

	source := `def run
  a, b = pair
  first, *rest, last = values
  x, (y, z) = nested
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
	}

	wantBody := []ast.Statement{
		&ast.AssignStmt{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "a"}},
				{Target: &ast.Identifier{Name: "b"}},
			}},
			Value: &ast.Identifier{Name: "pair"},
		},
		&ast.AssignStmt{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "first"}},
				{Target: &ast.Identifier{Name: "rest"}, Rest: true},
				{Target: &ast.Identifier{Name: "last"}},
			}},
			Value: &ast.Identifier{Name: "values"},
		},
		&ast.AssignStmt{
			Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "x"}},
				{Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
					{Target: &ast.Identifier{Name: "y"}},
					{Target: &ast.Identifier{Name: "z"}},
				}}},
			}},
			Value: &ast.Identifier{Name: "nested"},
		},
	}

	if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
		t.Fatalf("function body mismatch (-want +got):\n%s", diff)
	}
}

func TestParserParallelAssignmentAnonymousRestTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		assignment string
		want       *ast.DestructureTarget
	}{
		{
			name:       "trailing",
			assignment: "first, * = values",
			want: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "first"}},
				{Rest: true},
			}},
		},
		{
			name:       "leading",
			assignment: "*, last = values",
			want: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Rest: true},
				{Target: &ast.Identifier{Name: "last"}},
			}},
		},
		{
			name:       "middle",
			assignment: "first, *, last = values",
			want: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "first"}},
				{Rest: true},
				{Target: &ast.Identifier{Name: "last"}},
			}},
		},
		{
			name:       "nested",
			assignment: "a, (b, *), c = nested",
			want: &ast.DestructureTarget{Elements: []ast.DestructureElement{
				{Target: &ast.Identifier{Name: "a"}},
				{Target: &ast.DestructureTarget{Elements: []ast.DestructureElement{
					{Target: &ast.Identifier{Name: "b"}},
					{Rest: true},
				}}},
				{Target: &ast.Identifier{Name: "c"}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  " + tt.assignment + "\nend"
			got, errs := parseSource(t, source)
			if len(errs) > 0 {
				t.Fatalf("parseSource(%q) errors = %v, want none", source, errs)
			}

			value := tt.assignment[strings.LastIndex(tt.assignment, "= ")+2:]
			wantBody := []ast.Statement{
				&ast.AssignStmt{Target: tt.want, Value: &ast.Identifier{Name: value}},
			}
			if diff := cmp.Diff(wantBody, parsedFunctionBody(t, got), astCmpOpts); diff != "" {
				t.Fatalf("function body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParserParallelAssignmentRejectsDuplicateRestTarget(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"named then named":     "a, *b, *c = values",
		"named then anonymous": "a, *b, * = values",
		"anonymous then named": "a, *, *c = values",
		"anonymous twice":      "a, *, * = values",
	}

	for name, assignment := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			source := "def run\n  " + assignment + "\nend"
			_, errs := parseSource(t, source)
			if len(errs) == 0 {
				t.Fatalf("parseSource(%q) errors = nil, want duplicate rest error", source)
			}
			if got, want := errs[0].Error(), "duplicate rest assignment target"; !strings.Contains(got, want) {
				t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
			}
		})
	}
}

func TestParserParallelAssignmentRequiresAssignment(t *testing.T) {
	t.Parallel()

	source := `def run
  a, b
end`

	_, errs := parseSource(t, source)
	if len(errs) == 0 {
		t.Fatalf("parseSource(%q) errors = nil, want missing assignment error", source)
	}
	if got, want := errs[0].Error(), "parallel assignment targets require '='"; !strings.Contains(got, want) {
		t.Fatalf("parseSource(%q) error = %q, want substring %q", source, got, want)
	}
}
