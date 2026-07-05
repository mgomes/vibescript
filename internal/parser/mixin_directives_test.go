package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserMixinDirectives(t *testing.T) {
	t.Parallel()
	source := `class Person
  include Named
  include A, B
  extend Support::Buildable
  include(Parenthesized)
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	class := got.Statements[0].(*ast.ClassStmt)

	var mixins []*ast.MixinDecl
	for _, member := range class.Members {
		if member.Mixin != nil {
			mixins = append(mixins, member.Mixin)
		}
	}
	if len(mixins) != 4 {
		t.Fatalf("mixin count = %d, want 4", len(mixins))
	}

	names := func(decl *ast.MixinDecl) string {
		parts := make([]string, 0, len(decl.Modules))
		for _, ref := range decl.Modules {
			parts = append(parts, ref.Name)
		}
		return strings.Join(parts, ",")
	}
	if mixins[0].Kind != ast.MixinInclude || names(mixins[0]) != "Named" {
		t.Fatalf("mixin 0 = %+v, want include Named", mixins[0])
	}
	if mixins[1].Kind != ast.MixinInclude || names(mixins[1]) != "A,B" {
		t.Fatalf("mixin 1 = %+v, want include A,B", mixins[1])
	}
	if mixins[2].Kind != ast.MixinExtend || names(mixins[2]) != "Support::Buildable" {
		t.Fatalf("mixin 2 = %+v, want extend Support::Buildable", mixins[2])
	}
	if mixins[3].Kind != ast.MixinInclude || names(mixins[3]) != "Parenthesized" {
		t.Fatalf("mixin 3 = %+v, want include(Parenthesized)", mixins[3])
	}
}

func TestParserMixinDirectiveErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "extend self",
			source: "class C\n  extend self\nend",
			want:   "extend self is not supported; define module functions with def self.name",
		},
		{
			name:   "bare include",
			source: "class C\n  include\nend",
			want:   "include expects a module name",
		},
		{
			name:   "lowercase module name",
			source: "class C\n  include named\nend",
			want:   "module name must start with an uppercase letter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, tc.source)
			if len(errs) == 0 {
				t.Fatalf("expected parse errors")
			}
			if !strings.Contains(errs[0].Error(), tc.want) {
				t.Fatalf("error = %v, want %q", errs[0], tc.want)
			}
		})
	}
}

func TestParserMixinWordsStayIdentifiersOutsideDirectives(t *testing.T) {
	t.Parallel()
	source := `class C
  include = 1
  extend = include + 1
end

def run(include, extend)
  include + extend
end`

	_, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
}
