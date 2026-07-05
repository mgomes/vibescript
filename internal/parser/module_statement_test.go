package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserModuleDeclaration(t *testing.T) {
	t.Parallel()
	source := `module Billing
  LIMIT = 5

  def self.code
    "ok"
  end

  def shared_helper
    1
  end

  module Rates
    BASE = 2
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	module, ok := got.Statements[0].(*ast.ClassStmt)
	if !ok {
		t.Fatalf("statement 0 = %T, want *ast.ClassStmt", got.Statements[0])
	}
	if !module.IsModule || module.Name != "Billing" {
		t.Fatalf("module = %+v, want IsModule Billing", module)
	}
	if len(module.ClassMethods) != 1 || module.ClassMethods[0].Name != "code" {
		t.Fatalf("class methods = %+v, want [code]", module.ClassMethods)
	}
	if len(module.Methods) != 1 || module.Methods[0].Name != "shared_helper" {
		t.Fatalf("methods = %+v, want [shared_helper]", module.Methods)
	}
	if len(module.Body) != 1 {
		t.Fatalf("body statements = %d, want 1 (the constant assignment)", len(module.Body))
	}
	if len(module.Modules) != 1 || module.Modules[0].Name != "Rates" || !module.Modules[0].IsModule {
		t.Fatalf("nested modules = %+v, want [Rates]", module.Modules)
	}
}

func TestParserModuleDeclarationPlacementErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "inside class body",
			source: "class Person\n  module Naming\n  end\nend",
			want:   "module declarations are only supported at the top level and nested in module bodies",
		},
		{
			name:   "inside function body",
			source: "def run\n  module Billing\n  end\nend",
			want:   "module declarations are only supported at the top level and nested in module bodies",
		},
		{
			name:   "lowercase name",
			source: "module billing\nend",
			want:   "module name must start with an uppercase letter",
		},
		{
			name:   "class inside module body",
			source: "module Outer\n  class Inner\n  end\nend",
			want:   "class declarations are not supported in module bodies",
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

func TestParserModuleStaysIdentifierOutsideDeclarations(t *testing.T) {
	t.Parallel()
	source := `def run
  module = 5
  helper(module)
  module.size
end`

	_, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
}
