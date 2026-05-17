package runtime

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
	requireCallErrorIs(t, script, "run", args, opts, errMemoryQuotaExceeded)
}

// buildLargeStringArrayLiteral builds an ArrayLiteral AST node of `count`
// identical string elements. It mirrors the transient allocation pattern used
// across the OOM tests below.
func buildLargeStringArrayLiteral(count int, element string, pos Position) *ArrayLiteral {
	elements := make([]Expression, count)
	for i := range elements {
		elements[i] = &StringLiteral{Value: element, Position: pos}
	}
	return &ArrayLiteral{Elements: elements, Position: pos}
}

// estimateLargeStringArray returns the byte cost of the runtime Value that
// matches an AST built by buildLargeStringArrayLiteral with the same params.
func estimateLargeStringArray(count int, element string) int {
	values := make([]Value, count)
	for i := range values {
		values[i] = NewString(element)
	}
	return newMemoryEstimator().value(NewArray(values))
}

func TestMemoryQuotaScriptOOM(t *testing.T) {
	t.Parallel()

	largeCSV := strings.Repeat("abcdefghij,", 1500)
	emptyBodyDefaultArgSource := `def run(payload = "` + largeCSV + `".split(","))
end`

	boundArgsParts := make([]Value, 2000)
	for i := range boundArgsParts {
		boundArgsParts[i] = NewString("abcdefghij")
	}
	boundLargeArg := NewArray(boundArgsParts)

	highAllocRows := make([]Value, 120)
	for i := range highAllocRows {
		values := make([]Value, 8)
		for j := range values {
			values[j] = NewInt(int64(i + j))
		}
		highAllocRows[i] = NewHash(map[string]Value{
			"id":     NewString("row"),
			"values": NewArray(values),
		})
	}

	tests := []struct {
		name   string
		cfg    Config
		source string
		args   []Value
		opts   CallOptions
	}{
		{
			name:   "string_push_loop",
			cfg:    Config{StepQuota: 20000, MemoryQuotaBytes: 2048},
			source: quotaFixture,
		},
		{
			name:   "class_vars_counted",
			cfg:    Config{StepQuota: 20000, MemoryQuotaBytes: 3072},
			source: classVarFixture,
		},
		{
			name:   "split_result_on_completion",
			cfg:    Config{StepQuota: 20000, MemoryQuotaBytes: 2048},
			source: splitFixture,
			args:   []Value{NewString(strings.Repeat("a,", 4000))},
		},
		{
			name:   "empty_body_default_arg_evaluated",
			cfg:    Config{StepQuota: 20000, MemoryQuotaBytes: 2048},
			source: emptyBodyDefaultArgSource,
		},
		{
			name: "positional_bound_argument",
			cfg:  Config{StepQuota: 20000, MemoryQuotaBytes: 2048},
			source: `def run(payload)
end`,
			args: []Value{boundLargeArg},
		},
		{
			name: "keyword_bound_argument",
			cfg:  Config{StepQuota: 20000, MemoryQuotaBytes: 2048},
			source: `def run(payload)
end`,
			opts: CallOptions{Keywords: map[string]Value{"payload": boundLargeArg}},
		},
		{
			name: "independent_empty_slices_counted",
			cfg:  Config{StepQuota: 20000, MemoryQuotaBytes: 4096},
			source: `def run
  items = []
  for i in 1..400
    items = items.push([])
  end
  items.size
end`,
		},
		{
			name: "while_loop_allocations",
			cfg:  Config{StepQuota: 20000, MemoryQuotaBytes: 2048},
			source: `def run()
  items = []
  n = 0
  while n < 200
    items = items.push("abcdefghij")
    n = n + 1
  end
  items.size
end`,
		},
		{
			name: "high_allocation_typed_call_pattern",
			cfg:  Config{StepQuota: 500000, MemoryQuotaBytes: 8 * 1024},
			source: `def run(rows: array<{ id: string, values: array<int> }>) -> int
  total = 0
  rows.each do |row: { id: string, values: array<int> }|
    row[:values].each do |value: int|
      total = total + value
    end
  end
  total
end`,
			args: []Value{NewArray(highAllocRows)},
		},
		{
			name: "capability_workflow_pattern",
			cfg:  Config{StepQuota: 500000, MemoryQuotaBytes: 2 * 1024},
			source: `def run(n)
  total = 0
  for i in 1..n
    player_id = ctx[:player_id]
    row = db.find("Player", player_id)
    events.publish("scores.seen", { player_id: row[:id], score: row[:score] })
    total = total + row[:score]
  end
  total
end`,
			args: []Value{NewInt(120)},
			opts: CallOptions{
				Capabilities: []CapabilityAdapter{
					MustNewDBCapability("db", highAllocPatternDB{}),
					MustNewEventsCapability("events", highAllocPatternEvents{}),
					MustNewContextCapability("ctx", highAllocPatternContext),
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, tc.cfg, tc.source)
			requireRunMemoryQuotaError(t, script, tc.args, tc.opts)
		})
	}
}

func TestMemoryQuotaAllowsExecution(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{
		StepQuota:        20000,
		MemoryQuotaBytes: 1 << 20,
	}, quotaFixture)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind() != KindInt || result.Int() != 200 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

// transientOOMCase exercises the common pattern for transient-allocation OOM
// detection: build statements + env, probe to measure baseline, set a quota
// just above the baseline but below baseline+transient, then verify the same
// statements OOM under the tighter quota.
type transientOOMCase struct {
	name string
	// buildStmts returns the statements to execute and an env setup function.
	buildStmts func() (stmts []Statement, setupEnv func(*Env))
	// transientBytes returns the estimated extra bytes attributable to the
	// transient allocation under test.
	transientBytes func() int
	// passResultToProbe controls whether the probe's result Value is passed
	// to estimateMemoryUsage when computing the baseline.
	passResultToProbe bool
}

func runTransientOOMCase(t *testing.T, tc transientOOMCase) {
	t.Helper()

	stmts, setupEnv := tc.buildStmts()

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	if setupEnv != nil {
		setupEnv(probeEnv)
	}
	result, _, err := probeExec.evalStatements(stmts, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	var base int
	if tc.passResultToProbe {
		base = probeExec.estimateMemoryUsage(result)
	} else {
		base = probeExec.estimateMemoryUsage()
	}
	probeExec.popEnv()

	transient := tc.transientBytes()
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
	if setupEnv != nil {
		setupEnv(env)
	}
	if _, _, err := exec.evalStatements(stmts, env); err == nil {
		t.Fatalf("expected memory quota error for transient allocation")
	} else {
		requireErrorIs(t, err, errMemoryQuotaExceeded)
	}
}

func TestMemoryQuotaTransientAllocations(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	const transientCount = 1200
	const transientElement = "abcdefghij"
	largeArray := func() *ArrayLiteral {
		return buildLargeStringArrayLiteral(transientCount, transientElement, pos)
	}
	transientBytes := func() int {
		return estimateLargeStringArray(transientCount, transientElement)
	}

	tests := []transientOOMCase{
		{
			name: "split_method_call",
			buildStmts: func() ([]Statement, func(*Env)) {
				stmt := &ExprStmt{
					Expr: &MemberExpr{
						Object: &CallExpr{
							Callee: &MemberExpr{
								Object:   &Identifier{Name: "input", Position: pos},
								Property: "split",
								Position: pos,
							},
							Args:     []Expression{&StringLiteral{Value: ",", Position: pos}},
							Position: pos,
						},
						Property: "size",
						Position: pos,
					},
					Position: pos,
				}
				input := strings.Repeat("a,", 1500)
				return []Statement{stmt}, func(env *Env) {
					env.Define("input", NewString(input))
				}
			},
			transientBytes: func() int {
				parts := strings.Split(strings.Repeat("a,", 1500), ",")
				partValues := make([]Value, len(parts))
				for i, part := range parts {
					partValues[i] = NewString(part)
				}
				return newMemoryEstimator().value(NewArray(partValues))
			},
			passResultToProbe: true,
		},
		{
			name: "indexed_array_literal",
			buildStmts: func() ([]Statement, func(*Env)) {
				stmt := &ExprStmt{
					Expr: &IndexExpr{
						Object:   largeArray(),
						Index:    &IntegerLiteral{Value: 0, Position: pos},
						Position: pos,
					},
					Position: pos,
				}
				return []Statement{stmt}, nil
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
		{
			name: "method_call_receiver",
			buildStmts: func() ([]Statement, func(*Env)) {
				stmt := &ExprStmt{
					Expr: &CallExpr{
						Callee: &MemberExpr{
							Object:   largeArray(),
							Property: "size",
							Position: pos,
						},
						Position: pos,
					},
					Position: pos,
				}
				return []Statement{stmt}, nil
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
		{
			name: "if_condition",
			buildStmts: func() ([]Statement, func(*Env)) {
				stmt := &IfStmt{
					Condition: largeArray(),
					Consequent: []Statement{
						&ExprStmt{
							Expr:     &IntegerLiteral{Value: 1, Position: pos},
							Position: pos,
						},
					},
					Position: pos,
				}
				return []Statement{stmt}, nil
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
		{
			name: "unary_operand",
			buildStmts: func() ([]Statement, func(*Env)) {
				stmt := &ExprStmt{
					Expr: &UnaryExpr{
						Operator: tokenBang,
						Right:    largeArray(),
						Position: pos,
					},
					Position: pos,
				}
				return []Statement{stmt}, nil
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
		{
			name: "binary_operand",
			buildStmts: func() ([]Statement, func(*Env)) {
				stmt := &ExprStmt{
					Expr: &BinaryExpr{
						Left:     largeArray(),
						Operator: tokenAnd,
						Right:    &BoolLiteral{Value: false, Position: pos},
						Position: pos,
					},
					Position: pos,
				}
				return []Statement{stmt}, nil
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
		{
			name: "assignment_target_expression",
			buildStmts: func() ([]Statement, func(*Env)) {
				assignStmt := &AssignStmt{
					Target: &IndexExpr{
						Object:   largeArray(),
						Index:    &IntegerLiteral{Value: 0, Position: pos},
						Position: pos,
					},
					Value:    &IntegerLiteral{Value: 1, Position: pos},
					Position: pos,
				}
				return []Statement{
					assignStmt,
					&ExprStmt{
						Expr:     &IntegerLiteral{Value: 1, Position: pos},
						Position: pos,
					},
				}, nil
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
		{
			name: "assignment_value_pre_assign",
			buildStmts: func() ([]Statement, func(*Env)) {
				mk := NewBuiltin("mk", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					return NewHash(map[string]Value{}), nil
				})
				assignStmt := &AssignStmt{
					Target: &IndexExpr{
						Object: &CallExpr{
							Callee:   &Identifier{Name: "mk", Position: pos},
							Position: pos,
						},
						Index:    &StringLiteral{Value: "x", Position: pos},
						Position: pos,
					},
					Value:    largeArray(),
					Position: pos,
				}
				returnStmt := &ExprStmt{
					Expr:     &IntegerLiteral{Value: 1, Position: pos},
					Position: pos,
				}
				return []Statement{assignStmt, returnStmt}, func(env *Env) {
					env.Define("mk", mk)
				}
			},
			transientBytes:    transientBytes,
			passResultToProbe: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runTransientOOMCase(t, tc)
		})
	}
}

// TestMemoryQuotaTransientMethodCallLookupError covers the
// failed-method-lookup path on a transient receiver. The receiver allocation
// is large but the called method does not exist, so the runtime should fail
// fast on memory accounting before producing a missing-method error.
func TestMemoryQuotaTransientMethodCallLookupError(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	stmt := &ExprStmt{
		Expr: &CallExpr{
			Callee: &MemberExpr{
				Object:   buildLargeStringArrayLiteral(1200, "abcdefghij", pos),
				Property: "missing",
				Position: pos,
			},
			Position: pos,
		},
		Position: pos,
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   1,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	_, _, err := exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for transient method-call lookup receiver")
	}
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// aggregateOOMCase verifies that the sum of several large arguments to a
// callable trips the quota even when a single argument fits.
type aggregateOOMCase struct {
	name     string
	payloadA string
	payloadB string
	makeStmt func(payloads [2]Expression, pos Position) Statement
	setupEnv func(*Env)
}

func runAggregateOOMCase(t *testing.T, tc aggregateOOMCase) {
	t.Helper()
	pos := Position{Line: 1, Column: 1}
	payloads := [2]Expression{
		&StringLiteral{Value: tc.payloadA, Position: pos},
		&StringLiteral{Value: tc.payloadB, Position: pos},
	}
	stmt := tc.makeStmt(payloads, pos)

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	if tc.setupEnv != nil {
		tc.setupEnv(probeEnv)
	}
	result, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv)
	if err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage(result)
	probeExec.popEnv()

	argA := newMemoryEstimator().value(NewString(tc.payloadA))
	argB := newMemoryEstimator().value(NewString(tc.payloadB))
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
	if tc.setupEnv != nil {
		tc.setupEnv(env)
	}
	if _, _, err := exec.evalStatements([]Statement{stmt}, env); err == nil {
		t.Fatalf("expected memory quota error for aggregate arguments")
	} else {
		requireErrorIs(t, err, errMemoryQuotaExceeded)
	}
}

func TestMemoryQuotaAggregateArguments(t *testing.T) {
	t.Parallel()

	tests := []aggregateOOMCase{
		{
			name:     "builtin_call",
			payloadA: strings.Repeat("a", 3000),
			payloadB: strings.Repeat("b", 3000),
			makeStmt: func(payloads [2]Expression, pos Position) Statement {
				return &ExprStmt{
					Expr: &CallExpr{
						Callee:   &Identifier{Name: "assert", Position: pos},
						Args:     []Expression{payloads[0], payloads[1]},
						Position: pos,
					},
					Position: pos,
				}
			},
			setupEnv: func(env *Env) {
				env.Define("assert", NewBuiltin("assert", builtinAssert))
			},
		},
		{
			name:     "yield",
			payloadA: strings.Repeat("a", 3000),
			payloadB: strings.Repeat("b", 3000),
			makeStmt: func(payloads [2]Expression, pos Position) Statement {
				return &ExprStmt{
					Expr: &YieldExpr{
						Args:     []Expression{payloads[0], payloads[1]},
						Position: pos,
					},
					Position: pos,
				}
			},
			setupEnv: func(env *Env) {
				env.Define("__block__", NewBlock(nil, nil, newEnv(nil)))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runAggregateOOMCase(t, tc)
		})
	}
}

func TestMemoryQuotaCallArgumentsFailFastBeforeLaterSideEffects(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	payload := strings.Repeat("a", 5000)
	tickCount := 0

	stmt := &ExprStmt{
		Expr: &CallExpr{
			Callee: &Identifier{Name: "noop", Position: pos},
			Args: []Expression{
				&StringLiteral{Value: payload, Position: pos},
				&CallExpr{
					Callee:   &Identifier{Name: "tick", Position: pos},
					Position: pos,
				},
			},
			Position: pos,
		},
		Position: pos,
	}

	exec := &Execution{
		quota:         10000,
		memoryQuota:   2048,
		moduleLoading: make(map[string]bool),
	}
	env := newEnv(nil)
	env.Define("noop", NewBuiltin("noop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewNil(), nil
	}))
	env.Define("tick", NewBuiltin("tick", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		tickCount++
		return NewInt(1), nil
	}))

	_, _, err := exec.evalStatements([]Statement{stmt}, env)
	if err == nil {
		t.Fatalf("expected memory quota error for oversized first argument")
	}
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if tickCount != 0 {
		t.Fatalf("expected later argument side effects to be skipped, got %d", tickCount)
	}
}

// doubleCountCase covers the post-check accounting branch: assigning or
// aliasing a value must not double-count its bytes against the quota.
type doubleCountCase struct {
	name      string
	payload   string
	buildStmt func(payload string, pos Position) Statement
	setupEnv  func(env *Env, payload string)
	// postChecks runs implementation-specific assertions after the main run.
	postChecks func(t *testing.T, exec *Execution, env *Env, payload string, quota int)
}

func runDoubleCountCase(t *testing.T, tc doubleCountCase) {
	t.Helper()
	pos := Position{Line: 1, Column: 1}
	stmt := tc.buildStmt(tc.payload, pos)

	probeExec := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
	}
	probeEnv := newEnv(nil)
	if tc.setupEnv != nil {
		tc.setupEnv(probeEnv, tc.payload)
	}
	if _, _, err := probeExec.evalStatements([]Statement{stmt}, probeEnv); err != nil {
		t.Fatalf("probe execution failed: %v", err)
	}

	probeExec.pushEnv(probeEnv)
	base := probeExec.estimateMemoryUsage()
	probeExec.popEnv()
	extra := newMemoryEstimator().value(NewString(tc.payload))
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
	if tc.setupEnv != nil {
		tc.setupEnv(env, tc.payload)
	}
	if _, _, err := exec.evalStatements([]Statement{stmt}, env); err != nil {
		t.Fatalf("alias post-check should fit quota without double counting: %v", err)
	}

	if tc.postChecks != nil {
		tc.postChecks(t, exec, env, tc.payload, quota)
	}
}

func TestMemoryQuotaDoubleCounting(t *testing.T) {
	t.Parallel()

	payload := strings.Repeat("abcdefghij", 300)

	tests := []doubleCountCase{
		{
			name:    "assignment_aliased_value",
			payload: payload,
			buildStmt: func(payload string, pos Position) Statement {
				return &AssignStmt{
					Target:   &Identifier{Name: "x", Position: pos},
					Value:    &StringLiteral{Value: payload, Position: pos},
					Position: pos,
				}
			},
			postChecks: func(t *testing.T, exec *Execution, env *Env, payload string, quota int) {
				t.Helper()
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
			},
		},
		{
			name:    "expression_alias_string",
			payload: payload,
			buildStmt: func(payload string, pos Position) Statement {
				return &ExprStmt{
					Expr:     &Identifier{Name: "payload", Position: pos},
					Position: pos,
				}
			},
			setupEnv: func(env *Env, payload string) {
				env.Define("payload", NewString(payload))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runDoubleCountCase(t, tc)
		})
	}
}

func TestMemoryQuotaCountsCapabilityScopeKnownBuiltins(t *testing.T) {
	t.Parallel()

	scopeWithKnown := &capabilityContractScope{
		knownBuiltins: make(map[*Builtin]struct{}),
	}
	for range 400 {
		scopeWithKnown.knownBuiltins[valueBuiltin(NewBuiltin("cap.dynamic", builtinAssert))] = struct{}{}
	}
	scopeWithoutKnown := &capabilityContractScope{
		knownBuiltins: make(map[*Builtin]struct{}),
	}

	withKnown := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
		capabilityContractScopes: map[*Builtin]*capabilityContractScope{
			valueBuiltin(NewBuiltin("cap.call", builtinAssert)): scopeWithKnown,
		},
	}
	withoutKnown := &Execution{
		quota:         10000,
		memoryQuota:   0,
		moduleLoading: make(map[string]bool),
		capabilityContractScopes: map[*Builtin]*capabilityContractScope{
			valueBuiltin(NewBuiltin("cap.call", builtinAssert)): scopeWithoutKnown,
		},
	}

	withKnownBytes := withKnown.estimateMemoryUsage()
	withoutKnownBytes := withoutKnown.estimateMemoryUsage()
	if withKnownBytes <= withoutKnownBytes {
		t.Fatalf("expected known builtin cache to increase memory estimate (%d <= %d)", withKnownBytes, withoutKnownBytes)
	}

	quota := withoutKnownBytes + (withKnownBytes-withoutKnownBytes)/2
	if quota <= withoutKnownBytes {
		quota = withoutKnownBytes + 1
	}
	if quota >= withKnownBytes {
		quota = withKnownBytes - 1
	}

	enforced := &Execution{
		quota:         10000,
		memoryQuota:   quota,
		moduleLoading: make(map[string]bool),
		capabilityContractScopes: map[*Builtin]*capabilityContractScope{
			valueBuiltin(NewBuiltin("cap.call", builtinAssert)): scopeWithKnown,
		},
	}
	err := enforced.checkMemory()
	if err == nil {
		t.Fatalf("expected memory quota error when known builtin cache grows")
	}
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

type highAllocPatternDB struct{}

func (highAllocPatternDB) Find(ctx context.Context, req DBFindRequest) (Value, error) {
	return NewHash(map[string]Value{
		"id":    req.ID,
		"score": NewInt(1),
	}), nil
}

func (highAllocPatternDB) Query(ctx context.Context, req DBQueryRequest) (Value, error) {
	return NewArray(nil), nil
}

func (highAllocPatternDB) Update(ctx context.Context, req DBUpdateRequest) (Value, error) {
	return NewNil(), nil
}

func (highAllocPatternDB) Sum(ctx context.Context, req DBSumRequest) (Value, error) {
	return NewInt(0), nil
}

func (highAllocPatternDB) Each(ctx context.Context, req DBEachRequest) ([]Value, error) {
	return nil, nil
}

type highAllocPatternEvents struct{}

func (highAllocPatternEvents) Publish(ctx context.Context, req EventPublishRequest) (Value, error) {
	return NewHash(map[string]Value{
		"ok": NewBool(true),
	}), nil
}

func highAllocPatternContext(context.Context) (Value, error) {
	return NewHash(map[string]Value{
		"player_id": NewString("player-1"),
	}), nil
}
