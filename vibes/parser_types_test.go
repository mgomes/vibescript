package vibes

import (
	"strings"
	"testing"
)

func TestParserTypeSyntaxCompositeForms(t *testing.T) {
	source := `def run(
  rows: array<int | string>,
  payload: { id: string, stats: { wins: int } }
) -> hash<string, { score: int | nil }>
  payload
end`

	p := newParser(source)
	program, errs := p.ParseProgram()
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}
	fn, ok := program.Statements[0].(*FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", program.Statements[0])
	}
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}

	rowsType := fn.Params[0].Type
	if rowsType == nil || rowsType.Kind != TypeArray || len(rowsType.TypeArgs) != 1 {
		t.Fatalf("expected array<T> param, got %#v", rowsType)
	}
	elemType := rowsType.TypeArgs[0]
	if elemType.Kind != TypeUnion || len(elemType.Union) != 2 {
		t.Fatalf("expected union element type, got %#v", elemType)
	}

	payloadType := fn.Params[1].Type
	if payloadType == nil || payloadType.Kind != TypeShape {
		t.Fatalf("expected shape payload type, got %#v", payloadType)
	}
	if _, ok := payloadType.Shape["id"]; !ok {
		t.Fatalf("expected shape field id")
	}
	statsType, ok := payloadType.Shape["stats"]
	if !ok || statsType.Kind != TypeShape {
		t.Fatalf("expected nested stats shape, got %#v", statsType)
	}
	winsType, ok := statsType.Shape["wins"]
	if !ok || winsType.Kind != TypeInt {
		t.Fatalf("expected stats.wins int type, got %#v", winsType)
	}

	if fn.ReturnTy == nil || fn.ReturnTy.Kind != TypeHash || len(fn.ReturnTy.TypeArgs) != 2 {
		t.Fatalf("expected hash<K,V> return type, got %#v", fn.ReturnTy)
	}
	valueType := fn.ReturnTy.TypeArgs[1]
	if valueType.Kind != TypeShape {
		t.Fatalf("expected shaped hash value type, got %#v", valueType)
	}
	scoreType, ok := valueType.Shape["score"]
	if !ok || scoreType.Kind != TypeUnion || len(scoreType.Union) != 2 {
		t.Fatalf("expected score union type, got %#v", scoreType)
	}
}

func TestParserTypeShapeRejectsDuplicateFields(t *testing.T) {
	source := `def run(payload: { id: string, id: int })
  payload
end`

	p := newParser(source)
	_, errs := p.ParseProgram()
	if len(errs) == 0 {
		t.Fatalf("expected parse errors")
	}
	if got := errs[0].Error(); !strings.Contains(got, "duplicate shape field id") {
		t.Fatalf("unexpected parse error: %s", got)
	}
}

func TestParserTypeSyntaxTypedBlockParameters(t *testing.T) {
	source := `def run(values)
  values.map do |value: int | string, label: string?|
    label
  end
end`

	p := newParser(source)
	program, errs := p.ParseProgram()
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}
	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}
	fn, ok := program.Statements[0].(*FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", program.Statements[0])
	}
	if len(fn.Body) != 1 {
		t.Fatalf("expected 1 body statement, got %d", len(fn.Body))
	}
	exprStmt, ok := fn.Body[0].(*ExprStmt)
	if !ok {
		t.Fatalf("expected expression statement, got %T", fn.Body[0])
	}
	call, ok := exprStmt.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("expected call expression, got %T", exprStmt.Expr)
	}
	if call.Block == nil {
		t.Fatalf("expected call block")
	}
	if len(call.Block.Params) != 2 {
		t.Fatalf("expected 2 block params, got %d", len(call.Block.Params))
	}

	first := call.Block.Params[0]
	if first.Name != "value" {
		t.Fatalf("expected first param name value, got %q", first.Name)
	}
	if first.Type == nil || first.Type.Kind != TypeUnion || len(first.Type.Union) != 2 {
		t.Fatalf("expected first param union type, got %#v", first.Type)
	}
	if first.Type.Union[0].Kind != TypeInt || first.Type.Union[1].Kind != TypeString {
		t.Fatalf("expected union int|string, got %#v", first.Type.Union)
	}

	second := call.Block.Params[1]
	if second.Name != "label" {
		t.Fatalf("expected second param name label, got %q", second.Name)
	}
	if second.Type == nil || second.Type.Kind != TypeString || !second.Type.Nullable {
		t.Fatalf("expected nullable string type, got %#v", second.Type)
	}
}

func TestParserTypeSyntaxRejectsGenericArgsOnScalars(t *testing.T) {
	source := `def run(value: int<string>)
  value
end`

	p := newParser(source)
	_, errs := p.ParseProgram()
	if len(errs) == 0 {
		t.Fatalf("expected parse errors")
	}
	if got := errs[0].Error(); !strings.Contains(got, "type int does not accept type arguments") {
		t.Fatalf("unexpected parse error: %s", got)
	}
}
