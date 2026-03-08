package vibes

import "testing"

func TestParserYieldWithoutParensDoesNotConsumeNextLineInAssignment(t *testing.T) {
	source := `def run
  result = yield
  elapsed = 1
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
	if len(fn.Body) != 2 {
		t.Fatalf("expected 2 body statements, got %d", len(fn.Body))
	}

	first, ok := fn.Body[0].(*AssignStmt)
	if !ok {
		t.Fatalf("expected first statement assignment, got %T", fn.Body[0])
	}
	yieldExpr, ok := first.Value.(*YieldExpr)
	if !ok {
		t.Fatalf("expected yield expression, got %T", first.Value)
	}
	if len(yieldExpr.Args) != 0 {
		t.Fatalf("expected zero-arg yield, got %d args", len(yieldExpr.Args))
	}

	second, ok := fn.Body[1].(*AssignStmt)
	if !ok {
		t.Fatalf("expected second statement assignment, got %T", fn.Body[1])
	}
	target, ok := second.Target.(*Identifier)
	if !ok || target.Name != "elapsed" {
		t.Fatalf("expected second assignment to elapsed, got %#v", second.Target)
	}
}

func TestParserYieldWithoutParensAcceptsInlineArgument(t *testing.T) {
	source := `def run
  result = yield value
end`

	p := newParser(source)
	program, errs := p.ParseProgram()
	if len(errs) > 0 {
		t.Fatalf("expected no parse errors, got %v", errs)
	}

	fn, ok := program.Statements[0].(*FunctionStmt)
	if !ok {
		t.Fatalf("expected function statement, got %T", program.Statements[0])
	}
	assign, ok := fn.Body[0].(*AssignStmt)
	if !ok {
		t.Fatalf("expected assignment, got %T", fn.Body[0])
	}
	yieldExpr, ok := assign.Value.(*YieldExpr)
	if !ok {
		t.Fatalf("expected yield expression, got %T", assign.Value)
	}
	if len(yieldExpr.Args) != 1 {
		t.Fatalf("expected one yield arg, got %d", len(yieldExpr.Args))
	}
	arg, ok := yieldExpr.Args[0].(*Identifier)
	if !ok || arg.Name != "value" {
		t.Fatalf("expected yield arg value, got %#v", yieldExpr.Args[0])
	}
}
