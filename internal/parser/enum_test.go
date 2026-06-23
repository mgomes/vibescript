package parser

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/mgomes/vibescript/internal/ast"
)

// astCmpOpts ignores source positions and the call-form marker so tests focus
// on structural shape. CallExpr.Parenthesized is a low-level syntactic detail
// consulted only by the runtime options-hash policy; tests that exercise that
// policy assert the field directly.
var astCmpOpts = cmp.Options{
	cmpopts.IgnoreFields(ast.Position{}, "Line", "Column"),
	cmpopts.IgnoreFields(ast.CallExpr{}, "Parenthesized"),
}

func TestParserEnumSyntax(t *testing.T) {
	t.Parallel()
	source := `enum Status
  Draft
  Published
end

def run(status: Status) -> Status
  Status::Draft
end`

	got, errs := parseSource(t, source)

	want := &ast.Program{
		Statements: []ast.Statement{
			&ast.EnumStmt{
				Name: "Status",
				Members: []ast.EnumMemberStmt{
					{Name: "Draft"},
					{Name: "Published"},
				},
			},
			&ast.FunctionStmt{
				Name: "run",
				Params: []ast.Param{
					{
						Name: "status",
						Type: &ast.TypeExpr{Name: "Status", Kind: ast.TypeEnum},
					},
				},
				ReturnTy: &ast.TypeExpr{Name: "Status", Kind: ast.TypeEnum},
				Body: []ast.Statement{
					&ast.ExprStmt{
						Expr: &ast.ScopeExpr{
							Object:   &ast.Identifier{Name: "Status"},
							Property: "Draft",
						},
					},
				},
			},
		},
	}

	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	if diff := cmp.Diff(want, got, astCmpOpts); diff != "" {
		t.Fatalf("program mismatch (-want +got):\n%s", diff)
	}
}

func TestParserNullableEnumTypeUsesCanonicalEnumName(t *testing.T) {
	t.Parallel()
	source := `enum Status
  Draft
end

def run(status: Status?) -> Status?
  status
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	fn, ok := got.Statements[1].(*ast.FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", got.Statements[1])
	}
	wantParamType := &ast.TypeExpr{Name: "Status", Kind: ast.TypeEnum, Nullable: true}
	if diff := cmp.Diff(wantParamType, fn.Params[0].Type, astCmpOpts); diff != "" {
		t.Fatalf("param type mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantParamType, fn.ReturnTy, astCmpOpts); diff != "" {
		t.Fatalf("return type mismatch (-want +got):\n%s", diff)
	}
}

func TestParserEnumErrorCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		source   string
		wantErrs []string
	}{
		{
			name: "nested_in_function",
			source: `def run()
  enum Status
    Draft
  end
end`,
			wantErrs: []string{"enum", "top level"},
		},
		{
			name: "duplicate_member",
			source: `enum Status
  Draft
  Draft
end`,
			wantErrs: []string{"duplicate enum member", "Draft"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("expected parse errors, got none")
			}
			got := errs[0].Error()
			for _, want := range tc.wantErrs {
				if !strings.Contains(got, want) {
					t.Errorf("parse error %q missing substring %q", got, want)
				}
			}
		})
	}
}
