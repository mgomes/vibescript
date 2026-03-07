package vibes

import (
	"strings"
	"testing"
)

func TestParserEnumSyntax(t *testing.T) {
	source := `enum Status
  Draft
  Published
end

def run(status: Status) -> Status
  Status::Draft
end`

	p := newParser(source)
	program, errs := p.ParseProgram()
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	if len(program.Statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(program.Statements))
	}

	enumStmt, ok := program.Statements[0].(*EnumStmt)
	if !ok {
		t.Fatalf("expected enum statement, got %T", program.Statements[0])
	}
	if enumStmt.Name != "Status" {
		t.Fatalf("expected enum Status, got %q", enumStmt.Name)
	}
	if len(enumStmt.Members) != 2 {
		t.Fatalf("expected 2 enum members, got %d", len(enumStmt.Members))
	}
	if enumStmt.Members[0].Name != "Draft" || enumStmt.Members[1].Name != "Published" {
		t.Fatalf("unexpected enum members: %#v", enumStmt.Members)
	}

	fn, ok := program.Statements[1].(*FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", program.Statements[1])
	}
	if len(fn.Params) != 1 || fn.Params[0].Type == nil {
		t.Fatalf("expected typed enum param, got %#v", fn.Params)
	}
	if fn.Params[0].Type.Kind != TypeEnum || fn.Params[0].Type.Name != "Status" {
		t.Fatalf("expected Status enum param, got %#v", fn.Params[0].Type)
	}
	if fn.ReturnTy == nil || fn.ReturnTy.Kind != TypeEnum || fn.ReturnTy.Name != "Status" {
		t.Fatalf("expected Status enum return type, got %#v", fn.ReturnTy)
	}
	if len(fn.Body) != 1 {
		t.Fatalf("expected 1 body statement, got %d", len(fn.Body))
	}
	exprStmt, ok := fn.Body[0].(*ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement, got %T", fn.Body[0])
	}
	scope, ok := exprStmt.Expr.(*ScopeExpr)
	if !ok {
		t.Fatalf("expected scope expression, got %T", exprStmt.Expr)
	}
	ident, ok := scope.Object.(*Identifier)
	if !ok || ident.Name != "Status" {
		t.Fatalf("expected Status identifier, got %#v", scope.Object)
	}
	if scope.Property != "Draft" {
		t.Fatalf("expected Draft member, got %q", scope.Property)
	}
}

func TestParserNullableEnumTypeUsesCanonicalEnumName(t *testing.T) {
	source := `enum Status
  Draft
end

def run(status: Status?) -> Status?
  status
end`

	p := newParser(source)
	program, errs := p.ParseProgram()
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	fn, ok := program.Statements[1].(*FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", program.Statements[1])
	}
	if len(fn.Params) != 1 || fn.Params[0].Type == nil {
		t.Fatalf("expected typed enum param, got %#v", fn.Params)
	}
	if fn.Params[0].Type.Kind != TypeEnum || fn.Params[0].Type.Name != "Status" || !fn.Params[0].Type.Nullable {
		t.Fatalf("expected nullable Status enum param, got %#v", fn.Params[0].Type)
	}
	if fn.ReturnTy == nil || fn.ReturnTy.Kind != TypeEnum || fn.ReturnTy.Name != "Status" || !fn.ReturnTy.Nullable {
		t.Fatalf("expected nullable Status enum return type, got %#v", fn.ReturnTy)
	}
}

func TestParserEnumRejectsNestedDeclarations(t *testing.T) {
	source := `def run()
  enum Status
    Draft
  end
end`

	p := newParser(source)
	_, errs := p.ParseProgram()
	if len(errs) == 0 {
		t.Fatalf("expected parse errors")
	}
	if got := errs[0].Error(); got == "" || !containsAll(got, "enum", "top level") {
		t.Fatalf("unexpected parse error: %s", got)
	}
}

func TestParserEnumRejectsDuplicateMembers(t *testing.T) {
	source := `enum Status
  Draft
  Draft
end`

	p := newParser(source)
	_, errs := p.ParseProgram()
	if len(errs) == 0 {
		t.Fatalf("expected parse errors")
	}
	if got := errs[0].Error(); got == "" || !containsAll(got, "duplicate enum member", "Draft") {
		t.Fatalf("unexpected parse error: %s", got)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
