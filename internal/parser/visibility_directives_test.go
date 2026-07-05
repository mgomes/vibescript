package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

func TestParserVisibilitySectionDirectives(t *testing.T) {
	t.Parallel()
	source := `class Secret
  private
  def hidden
    1
  end
  public
  def shown
    2
  end
  protected
  def guarded
    3
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	class, ok := got.Statements[0].(*ast.ClassStmt)
	if !ok {
		t.Fatalf("statement 0 = %T, want *ast.ClassStmt", got.Statements[0])
	}

	wantMembers := []struct {
		level  string
		fnName string
	}{
		{level: ast.VisibilityPrivate},
		{fnName: "hidden"},
		{level: ast.VisibilityPublic},
		{fnName: "shown"},
		{level: ast.VisibilityProtected},
		{fnName: "guarded"},
	}
	if len(class.Members) != len(wantMembers) {
		t.Fatalf("member count = %d, want %d", len(class.Members), len(wantMembers))
	}
	for i, want := range wantMembers {
		member := class.Members[i]
		if want.level != "" {
			if member.Visibility == nil || member.Visibility.Level != want.level || len(member.Visibility.Names) != 0 {
				t.Fatalf("member %d = %+v, want section directive %s", i, member, want.level)
			}
			continue
		}
		if member.Function == nil || member.Function.Name != want.fnName {
			t.Fatalf("member %d = %+v, want function %s", i, member, want.fnName)
		}
		if member.Function.Visibility != "" {
			t.Fatalf("member %d function visibility = %q, want inherited (empty)", i, member.Function.Visibility)
		}
	}
}

func TestParserVisibilitySymbolDirectives(t *testing.T) {
	t.Parallel()
	source := `class Secret
  def hidden
    1
  end
  private :hidden, :other
  protected :guarded
  public :shown
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	class := got.Statements[0].(*ast.ClassStmt)

	var decls []*ast.VisibilityDecl
	for _, member := range class.Members {
		if member.Visibility != nil {
			decls = append(decls, member.Visibility)
		}
	}
	if len(decls) != 3 {
		t.Fatalf("visibility directive count = %d, want 3", len(decls))
	}
	if decls[0].Level != ast.VisibilityPrivate || strings.Join(decls[0].Names, ",") != "hidden,other" {
		t.Fatalf("directive 0 = %+v, want private hidden,other", decls[0])
	}
	if decls[1].Level != ast.VisibilityProtected || strings.Join(decls[1].Names, ",") != "guarded" {
		t.Fatalf("directive 1 = %+v, want protected guarded", decls[1])
	}
	if decls[2].Level != ast.VisibilityPublic || strings.Join(decls[2].Names, ",") != "shown" {
		t.Fatalf("directive 2 = %+v, want public shown", decls[2])
	}
}

func TestParserVisibilityInlineModifiers(t *testing.T) {
	t.Parallel()
	source := `class Secret
  private
  public def shown
    1
  end
  def still_hidden
    2
  end
  protected def guarded
    3
  end
  private property token: string
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	class := got.Statements[0].(*ast.ClassStmt)

	byName := map[string]string{}
	for _, fn := range class.Methods {
		byName[fn.Name] = fn.Visibility
	}
	if byName["shown"] != ast.VisibilityPublic {
		t.Fatalf("shown visibility = %q, want public", byName["shown"])
	}
	if byName["still_hidden"] != "" {
		t.Fatalf("still_hidden visibility = %q, want inherited (empty)", byName["still_hidden"])
	}
	if byName["guarded"] != ast.VisibilityProtected {
		t.Fatalf("guarded visibility = %q, want protected", byName["guarded"])
	}
	if len(class.Properties) != 1 || class.Properties[0].Visibility != ast.VisibilityPrivate {
		t.Fatalf("properties = %+v, want one private property", class.Properties)
	}
}

func TestParserVisibilityDirectiveRejectsOtherArguments(t *testing.T) {
	t.Parallel()
	_, errs := parseSource(t, `class Secret
  private 42
end`)
	if len(errs) == 0 {
		t.Fatalf("expected parse error for private 42")
	}
	if !strings.Contains(errs[0].Error(), "private expects a method definition, symbol method names, or no argument") {
		t.Fatalf("error = %v, want private directive diagnostic", errs[0])
	}
}

func TestParserVisibilityLocalSuppressesSectionDirective(t *testing.T) {
	t.Parallel()
	source := `class Config
  protected = 5
  protected
  def shown
    1
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	class := got.Statements[0].(*ast.ClassStmt)
	for _, member := range class.Members {
		if member.Visibility != nil {
			t.Fatalf("bare protected after a protected local parsed as a %s directive, want identifier expression", member.Visibility.Level)
		}
	}
	if len(class.Body) != 2 {
		t.Fatalf("class body statement count = %d, want assignment and identifier read", len(class.Body))
	}
	if len(class.Methods) != 1 || class.Methods[0].Visibility != "" {
		t.Fatalf("methods = %+v, want one method with inherited (empty) visibility", class.Methods)
	}
}

func TestParserVisibilitySectionDirectiveWithoutLocalStaysDirective(t *testing.T) {
	t.Parallel()
	source := `class Config
  protected
  def guarded
    1
  end
end`

	got, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	class := got.Statements[0].(*ast.ClassStmt)
	if len(class.Members) == 0 || class.Members[0].Visibility == nil || class.Members[0].Visibility.Level != ast.VisibilityProtected {
		t.Fatalf("members = %+v, want leading protected section directive", class.Members)
	}
}

func TestParserVisibilityWordsStayIdentifiersOutsideDirectives(t *testing.T) {
	t.Parallel()
	source := `class Config
  public = 1
  x = protected
end

def uses(public)
  public
end`

	_, errs := parseSource(t, source)
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
}
