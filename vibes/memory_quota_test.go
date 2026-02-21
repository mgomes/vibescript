package vibes

import (
	"context"
	"strings"
	"testing"
)

const quotaFixture = `
def run()
  items = []
  for i in 1..200
    items = items.push("abcdefghij")
  end
  items.size
end
`

const splitFixture = `
def run(input)
  input.split(",")
end
`

const classVarFixture = `
class Bucket
  @@items = {}

  def self.fill(count)
    for i in 1..count
      key = "k" + i
      @@items[key] = i
    end
    @@items["k1"]
  end
end

def run
  Bucket.fill(200)
end
`

func requireRunMemoryQuotaError(t *testing.T, script *Script, args []Value, opts CallOptions) {
	t.Helper()
	requireCallErrorContains(t, script, "run", args, opts, "memory quota exceeded")
}

func TestMemoryQuotaExceeded(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	}, quotaFixture)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestMemoryQuotaCountsClassVars(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 3072,
	}, classVarFixture)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestMemoryQuotaAllowsExecution(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 1 << 20,
	}, quotaFixture)

	var err error
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 200 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestMemoryQuotaExceededOnCompletion(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	}, splitFixture)

	input := strings.Repeat("a,", 4000)
	requireRunMemoryQuotaError(t, script, []Value{NewString(input)}, CallOptions{})
}

func TestMemoryQuotaExceededForEmptyBodyDefaultArg(t *testing.T) {
	cfg := Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	}

	largeCSV := strings.Repeat("abcdefghij,", 1500)
	source := `def run(payload = "` + largeCSV + `".split(","))
end`

	script := compileScriptWithConfig(t, cfg, source)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestMemoryQuotaExceededForBoundArguments(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	}, `def run(payload)
end`)

	parts := make([]Value, 2000)
	for i := range parts {
		parts[i] = NewString("abcdefghij")
	}
	largeArg := NewArray(parts)

	requireRunMemoryQuotaError(t, script, []Value{largeArg}, CallOptions{})
	requireRunMemoryQuotaError(t, script, nil, CallOptions{
		Keywords: map[string]Value{
			"payload": largeArg,
		},
	})
}

func TestMemoryQuotaCountsIndependentEmptySlices(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 4096,
	}, `def run
  items = []
  for i in 1..400
    items = items.push([])
  end
  items.size
end`)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestMemoryQuotaExceededWithWhileLoopAllocations(t *testing.T) {
	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 2048,
	}, `def run()
  items = []
  n = 0
  while n < 200
    items = items.push("abcdefghij")
    n = n + 1
  end
  items.size
end`)
	requireRunMemoryQuotaError(t, script, nil, CallOptions{})
}

func TestAssignmentPostCheckDoesNotDoubleCountAssignedValue(t *testing.T) {
	payload := strings.Repeat("abcdefghij", 300)

	stmt := &AssignStmt{
		Target:   &Identifier{Name: "x", position: Position{Line: 1, Column: 1}},
		Value:    &StringLiteral{Value: payload, position: Position{Line: 1, Column: 5}},
		position: Position{Line: 1, Column: 1},
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	if _, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv); err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage()
	probeExec.popEnv()
	extra := newMemoryEstimator().value(NewString(payload))
	quota := base + extra/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	if _, _, err := exec.evalStatements([]Statement{stmt}, env); err != nil {
		t.Fatalf("assignment should fit quota without double counting: %v", err)
	}

	val, ok := env.Get("x")
	if !ok {
		t.Fatalf("expected x to be assigned")
	}
	exec.pushEnv(env)
	err := exec.checkMemoryWith(val)
	exec.popEnv()
	if err != nil {
		t.Fatalf("aliased explicit extra-root check should not exceed quota: %v", err)
	}

	payloadCopy := string(append([]byte(nil), payload...))
	exec.pushEnv(env)
	err = exec.checkMemoryWith(NewString(payloadCopy))
	exec.popEnv()
	if err == nil {
		t.Fatalf("expected non-aliased extra-root check to exceed quota")
	}
}

func TestExpressionAliasPostCheckDoesNotDoubleCountString(t *testing.T) {
	payload := strings.Repeat("abcdefghij", 300)
	stmt := &ExprStmt{
		Expr:     &Identifier{Name: "payload", position: Position{Line: 1, Column: 1}},
		position: Position{Line: 1, Column: 1},
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	probeEnv.Define("payload", NewString(payload))
	if _, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv); err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage()
	probeExec.popEnv()
	extra := newMemoryEstimator().value(NewString(payload))
	quota := base + extra/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	env.Define("payload", NewString(payload))
	if _, _, err := exec.evalStatements([]Statement{stmt}, env); err != nil {
		t.Fatalf("aliased expression result should fit quota without payload double counting: %v", err)
	}
}

func TestTransientExpressionAllocationsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	stmt := &ExprStmt{
		Expr: &MemberExpr{
			Object: &CallExpr{
				Callee: &MemberExpr{
					Object:   &Identifier{Name: "input", position: pos},
					Property: "split",
					position: pos,
				},
				Args:     []Expression{&StringLiteral{Value: ",", position: pos}},
				position: pos,
			},
			Property: "size",
			position: pos,
		},
		position: pos,
	}

	input := strings.Repeat("a,", 1500)
	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	probeEnv.Define("input", NewString(input))
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	parts := strings.Split(input, ",")
	partValues := make([]Value, len(parts))
	for i, part := range parts {
		partValues[i] = NewString(part)
	}
	transient := newMemoryEstimator().value(NewArray(partValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	env.Define("input", NewString(input))
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for transient expression allocation")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestIndexedTransientAllocationsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	stmt := &ExprStmt{
		Expr: &IndexExpr{
			Object:   &ArrayLiteral{Elements: elements, position: pos},
			Index:    &IntegerLiteral{Value: 0, position: pos},
			position: pos,
		},
		position: pos,
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for indexed transient allocation")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestTransientMethodCallReceiverAllocationsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	stmt := &ExprStmt{
		Expr: &CallExpr{
			Callee: &MemberExpr{
				Object:   &ArrayLiteral{Elements: elements, position: pos},
				Property: "size",
				position: pos,
			},
			position: pos,
		},
		position: pos,
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for transient method-call receiver allocation")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestIfConditionTransientAllocationsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	stmt := &IfStmt{
		Condition: &ArrayLiteral{Elements: elements, position: pos},
		Consequent: []Statement{
			&ExprStmt{
				Expr:     &IntegerLiteral{Value: 1, position: pos},
				position: pos,
			},
		},
		position: pos,
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for if-condition transient allocation")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestAggregateBuiltinArgumentsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	payloadA := strings.Repeat("a", 3000)
	payloadB := strings.Repeat("b", 3000)

	stmt := &ExprStmt{
		Expr: &CallExpr{
			Callee: &Identifier{Name: "assert", position: pos},
			Args: []Expression{
				&StringLiteral{Value: payloadA, position: pos},
				&StringLiteral{Value: payloadB, position: pos},
			},
			position: pos,
		},
		position: pos,
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	probeEnv.Define("assert", NewBuiltin("assert", builtinAssert))
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	argA := newMemoryEstimator().value(NewString(payloadA))
	argB := newMemoryEstimator().value(NewString(payloadB))
	single := max(argB, argA)
	combined := argA + argB
	quota := base + single + (combined-single)/2
	if quota <= base+single {
		quota = base + single + 1
	}
	if quota >= base+combined {
		quota = base + combined - 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	env.Define("assert", NewBuiltin("assert", builtinAssert))
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for aggregate builtin arguments")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestTransientAssignmentValueIsCheckedBeforeAssign(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	assignStmt := &AssignStmt{
		Target: &IndexExpr{
			Object: &CallExpr{
				Callee:   &Identifier{Name: "mk", position: pos},
				position: pos,
			},
			Index:    &StringLiteral{Value: "x", position: pos},
			position: pos,
		},
		Value:    &ArrayLiteral{Elements: elements, position: pos},
		position: pos,
	}
	returnStmt := &ExprStmt{
		Expr:     &IntegerLiteral{Value: 1, position: pos},
		position: pos,
	}
	stmts := []Statement{assignStmt, returnStmt}

	mk := NewBuiltin("mk", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewHash(map[string]Value{}), nil
	})

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	probeEnv.Define("mk", mk)
	result, _, err := probeExec.evalStatements(stmts, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	env.Define("mk", mk)
	_, _, err = exec.evalStatements(stmts, env)
	if err == nil {
		t.Fatalf("expected memory quota error for transient assignment value")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestTransientUnaryOperandAllocationsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	stmt := &ExprStmt{
		Expr: &UnaryExpr{
			Operator: tokenBang,
			Right:    &ArrayLiteral{Elements: elements, position: pos},
			position: pos,
		},
		position: pos,
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for unary transient operand")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestTransientBinaryOperandsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	stmt := &ExprStmt{
		Expr: &BinaryExpr{
			Left:     &ArrayLiteral{Elements: elements, position: pos},
			Operator: tokenAnd,
			Right:    &BoolLiteral{Value: false, position: pos},
			position: pos,
		},
		position: pos,
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for binary transient operands")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestAssignmentTargetExpressionsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	elements := make([]Expression, 1200)
	for i := range elements {
		elements[i] = &StringLiteral{Value: "abcdefghij", position: pos}
	}

	assignStmt := &AssignStmt{
		Target: &IndexExpr{
			Object:   &ArrayLiteral{Elements: elements, position: pos},
			Index:    &IntegerLiteral{Value: 0, position: pos},
			position: pos,
		},
		Value:    &IntegerLiteral{Value: 1, position: pos},
		position: pos,
	}
	stmts := []Statement{
		assignStmt,
		&ExprStmt{
			Expr:     &IntegerLiteral{Value: 1, position: pos},
			position: pos,
		},
	}

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	result, _, err := probeExec.evalStatements(stmts, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	transientValues := make([]Value, len(elements))
	for i := range transientValues {
		transientValues[i] = NewString("abcdefghij")
	}
	transient := newMemoryEstimator().value(NewArray(transientValues))
	quota := base + transient/2
	if quota <= base {
		quota = base + 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err = exec.evalStatements(stmts, env)
	if err == nil {
		t.Fatalf("expected memory quota error for assignment target transient allocation")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}

func TestAggregateYieldArgumentsAreChecked(t *testing.T) {
	pos := Position{Line: 1, Column: 1}
	payloadA := strings.Repeat("a", 3000)
	payloadB := strings.Repeat("b", 3000)

	stmt := &ExprStmt{
		Expr: &YieldExpr{
			Args: []Expression{
				&StringLiteral{Value: payloadA, position: pos},
				&StringLiteral{Value: payloadB, position: pos},
			},
			position: pos,
		},
		position: pos,
	}

	blockVal := NewBlock(nil, nil, newEnv(nil))

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	probeEnv.Define("__block__", blockVal)
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	argA := newMemoryEstimator().value(NewString(payloadA))
	argB := newMemoryEstimator().value(NewString(payloadB))
	single := max(argB, argA)
	combined := argA + argB
	quota := base + single + (combined-single)/2
	if quota <= base+single {
		quota = base + single + 1
	}
	if quota >= base+combined {
		quota = base + combined - 1
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	env.Define("__block__", blockVal)
	_, _, err = exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for aggregate yield arguments")
	}
	requireErrorContains(t, err, "memory quota exceeded")
}
