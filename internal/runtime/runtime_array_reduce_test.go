package runtime

import (
	"context"
	"errors"
	"testing"
)

// TestArrayReduceSymbolShorthand exercises Ruby's Array#reduce operation form,
// where a symbol or string names the operation sent to the accumulator. The
// expected values mirror Ruby 2.6 behavior captured in issue #634.
func TestArrayReduceSymbolShorthand(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "operator symbol concat",
			source: `def run(); ["a", "b", "c"].reduce(:concat); end`,
			want:   NewString("abc"),
		},
		{
			name:   "string operator plus",
			source: `def run(); [1, 2, 3].reduce("+"); end`,
			want:   NewInt(6),
		},
		{
			name:   "string operator times",
			source: `def run(); [2, 3, 4].reduce("*"); end`,
			want:   NewInt(24),
		},
		{
			name:   "string operator minus",
			source: `def run(); [10, 1, 2].reduce("-"); end`,
			want:   NewInt(7),
		},
		{
			name:   "initial plus operator",
			source: `def run(); [1, 2, 3].reduce(10, "+"); end`,
			want:   NewInt(16),
		},
		{
			name:   "initial concat symbol",
			source: `def run(); ["b", "c"].reduce("a", :concat); end`,
			want:   NewString("abc"),
		},
		{
			name:   "single element returns element",
			source: `def run(); [42].reduce("+"); end`,
			want:   NewInt(42),
		},
		{
			name:   "empty array no initial yields nil",
			source: `def run(); [].reduce("+"); end`,
			want:   NewNil(),
		},
		{
			name:   "empty array with initial yields initial",
			source: `def run(); [].reduce(99, "+"); end`,
			want:   NewInt(99),
		},
		{
			name:   "method name symbol dispatches to array member",
			source: `def run(); [[1], [2], [3]].reduce(:union); end`,
			want:   NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name:   "string operator shovel appends each element",
			source: `def run(); [[1], 2, 3].reduce("<<"); end`,
			want:   NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
		},
		{
			name:   "string operator intersection",
			source: `def run(); [[1, 2, 3], [2, 3], [2, 3, 4]].reduce("&"); end`,
			want:   NewArray([]Value{NewInt(2), NewInt(3)}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			got := callFunc(t, script, "run", nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("reduce result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayReduceBlockForm confirms the explicit block form still works and
// that a block always takes precedence over a lone symbol argument: the symbol
// becomes the initial accumulator value, matching Ruby's
// `[1].reduce(:seed) { |a, b| "#{a}-#{b}" }`.
func TestArrayReduceBlockForm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name:   "block without initial",
			source: `def run(); [1, 2, 3].reduce do |acc, n| acc + n end; end`,
			want:   NewInt(6),
		},
		{
			name:   "block with initial",
			source: `def run(); [1, 2, 3].reduce(100) do |acc, n| acc + n end; end`,
			want:   NewInt(106),
		},
		{
			name:   "block makes symbol argument the seed",
			source: `def run(); [1].reduce(:seed) do |acc, n| "#{acc}-#{n}" end; end`,
			want:   NewString("seed-1"),
		},
		{
			name:   "empty array with block and no initial yields nil",
			source: `def run(); [].reduce do |acc, n| acc + n end; end`,
			want:   NewNil(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			got := callFunc(t, script, "run", nil)
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("reduce result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestArrayReduceOperationSendsViaRuntime verifies the operator form routes
// through the runtime so symbols constructed directly (which operator-symbol
// literals will produce once they lex) fold through the same path as string
// operation names.
func TestArrayReduceOperationSendsViaRuntime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		receiver  Value
		operation Value
		want      Value
	}{
		{
			name:      "plus operator symbol",
			receiver:  NewArray([]Value{NewInt(1), NewInt(2), NewInt(3)}),
			operation: NewSymbol("+"),
			want:      NewInt(6),
		},
		{
			name:      "times operator symbol",
			receiver:  NewArray([]Value{NewInt(2), NewInt(3), NewInt(4)}),
			operation: NewSymbol("*"),
			want:      NewInt(24),
		},
		{
			name:      "power operator symbol",
			receiver:  NewArray([]Value{NewInt(2), NewInt(3), NewInt(2)}),
			operation: NewSymbol("**"),
			want:      NewInt(64),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `def run(values, op); values.reduce(op); end`)
			got := callFunc(t, script, "run", []Value{tc.receiver, tc.operation})
			if diff := valueDiff(tc.want, got); diff != "" {
				t.Fatalf("reduce result mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArrayReduceErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "no block no operation",
			source: `def run(); [1, 2, 3].reduce; end`,
			want:   "array.reduce requires a block or an operation",
		},
		{
			name:   "lone non-operation argument without block",
			source: `def run(); [1, 2, 3].reduce(10); end`,
			want:   "array.reduce operation must be a symbol or string",
		},
		{
			name:   "two-argument operation must be symbol or string",
			source: `def run(); [1, 2, 3].reduce(1, 2); end`,
			want:   "array.reduce operation must be a symbol or string",
		},
		{
			name:   "too many arguments",
			source: `def run(); [1, 2, 3].reduce(1, 2, 3); end`,
			want:   "array.reduce accepts at most an initial value and an operation",
		},
		{
			name:   "keyword arguments rejected",
			source: `def run(); [1, 2, 3].reduce(seed: 1); end`,
			want:   "array.reduce does not take keyword arguments",
		},
		{
			name:   "unknown operation surfaces dispatch error",
			source: `def run(); [1, 2, 3].reduce(:nope); end`,
			want:   `array.reduce cannot apply "nope"`,
		},
		{
			name:   "incompatible operands surface arithmetic error",
			source: `def run(); [1, "a"].reduce("-"); end`,
			want:   "subtraction",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestArrayReduceOperationRejectsPrivateMethods confirms the symbol/string
// operation form resolves methods with public-only dispatch, matching its
// documented `accumulator.public_send(operation, item)` contract. Even when the
// accumulator is the current self inside an instance method, a private method
// must not be reachable through reduce, while a public method still folds.
func TestArrayReduceOperationRejectsPrivateMethods(t *testing.T) {
	t.Parallel()

	const source = `
class Folder
  private def secret(other)
    self
  end

  def public_combine(other)
    self
  end

  def fold_private(other)
    [self, other].reduce(:secret)
  end

  def fold_public(other)
    [self, other].reduce(:public_combine)
  end
end

def fold_private
  a = Folder.new
  a.fold_private(Folder.new)
end

def fold_public
  a = Folder.new
  a.fold_public(Folder.new)
end
`

	script := compileScript(t, source)

	// reduce(:secret) on self must be rejected with the same privacy error a
	// member-style call would raise, even though the accumulator is self.
	requireCallErrorContains(t, script, "fold_private", nil, CallOptions{}, "private method secret")

	// A public method still folds through the operation form.
	got := callFunc(t, script, "fold_public", nil)
	if got.Kind() != KindInstance {
		t.Fatalf("fold_public via reduce(:public_combine) = %v, want an instance", got.Kind())
	}
}

// TestArrayReduceOperationParticipatesInStepQuota confirms the operation form
// charges a step per element so a tight step quota trips on a large receiver.
func TestArrayReduceOperationParticipatesInStepQuota(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{StepQuota: 40}, `def run(values); values.reduce("+"); end`)
	requireCallRuntimeErrorType(t, script, "run", []Value{largeIntArray(1000)}, CallOptions{}, runtimeErrorTypeLimit)
}

// TestArrayReduceOperationHonorsCancellation confirms the operation form polls
// context cancellation through the per-element step.
func TestArrayReduceOperationHonorsCancellation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run(); [3, 1, 2].reduce("+"); end`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("reduce under canceled context = %v, want context.Canceled", err)
	}
}

// emptyAccRestReduceBlock builds an EMPTY-body |(head, *tail), item| block: a block
// whose first positional parameter destructures the accumulator with a rest target
// and whose second binds the element. The empty body runs no statements, so the
// body's own per-statement memory checks never observe the fresh tail backing the
// rest collects -- the case the per-call bind charge must cover on its own.
func emptyAccRestReduceBlock() Value {
	pos := Position{Line: 1, Column: 1}
	accTarget := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	return NewBlock([]Param{
		{Kind: ParamNormal, Target: accTarget},
		{Kind: ParamNormal, Target: &Identifier{Name: "item", Position: pos}},
	}, nil, newEnv(nil))
}

// reduceAccRestChargeBytes returns the bytes the per-call bind charge attributes to a
// single reduce call whose |(head, *tail), item| block destructures the accumulator
// acc and the element item. It mirrors blockBindCharge exactly: charge the
// accumulator as a per-call Go-frame root, seed the call's arguments (so payloads the
// rest backing shares with acc deduplicate), then charge the genuinely fresh tail
// backing of acc[1:]. The returned (full, tailOnly) pair lets tests size a quota
// between the old accounting (tailOnly, which omitted the accumulator) and the new
// one (full), proving the accumulator's footprint is what the charge now closes.
func reduceAccRestChargeBytes(acc, item Value) (full, tailOnly int) {
	tail := NewArray(slicesClone(acc.Array()[1:]))

	withAcc := newMemoryEstimator()
	accBytes := withAcc.value(acc)
	withAcc.value(item)
	full = accBytes + withAcc.value(tail)

	tailOnlyEst := newMemoryEstimator()
	tailOnlyEst.value(acc)
	tailOnlyEst.value(item)
	tailOnly = tailOnlyEst.value(tail)
	return full, tailOnly
}

// TestArrayReduceEmptyBodyAccRestTripsMemoryQuota is the regression for the reduce
// accumulator gap on PR #808. reduce(big) do |(head, *tail), item| ... end folds
// from a large initial accumulator that lives only in arrayReduce's Go frame, never
// in any env the body's checks walk. With an EMPTY block body, the body never
// observes the fresh tail the rest target copies, and the accumulator is not in the
// runner's one-time baseline (which holds the receiver, not the seed). Charging only
// the tail against that baseline let a quota fit (receiver + tail) while the real
// peak (receiver + accumulator + tail) escaped. The per-call charge now counts the
// accumulator, so a quota admitting the receiver and tail but NOT the accumulator
// rejects the fold.
func TestArrayReduceEmptyBodyAccRestTripsMemoryQuota(t *testing.T) {
	t.Parallel()

	// A single-element receiver makes reduce run exactly one block call whose
	// accumulator is the large seed; the empty body then returns nil, so no later
	// call carries a large accumulator. The seed lives only on the Go call stack.
	const seedLen = 200_000
	seed := arrayValue(seedLen)
	receiver := NewArray([]Value{NewInt(1)})
	block := emptyAccRestReduceBlock()
	if !blockCanReuseEnv(valueBlock(block)) {
		t.Fatal("test setup expects a reusable-env block so the empty-body rebind path is exercised")
	}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	baseline := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	full, tailOnly := reduceAccRestChargeBytes(seed, NewInt(1))
	const headroom = 16 * 1024
	quota := baseline + tailOnly + headroom

	// Sanity: the accumulator's footprint genuinely exceeds the headroom the quota
	// leaves above the tail-only accounting, so its omission -- not an incidentally
	// tight quota -- is what would let the fold escape.
	if full <= tailOnly+headroom {
		t.Fatalf("test setup expects the accumulator charge (full %d) to exceed tail-only (%d) plus headroom (%d)", full, tailOnly, headroom)
	}

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	_, err := arrayReduce(exec, receiver, []Value{seed}, nil, block)
	requireErrorIs(t, err, errMemoryQuotaExceeded)
}

// TestArrayReduceEmptyBodyAccRestFitsWhenAccFitsQuota pins the other side of the
// charge: it must not over-reject a fold whose accumulator and fresh tail genuinely
// fit. A quota generously above the receiver, the accumulator, and the tail copy
// admits the empty-body fold and returns the empty block's nil result, proving the
// charge bounds only the live peak rather than rejecting every rest-collecting
// reduce block.
func TestArrayReduceEmptyBodyAccRestFitsWhenAccFitsQuota(t *testing.T) {
	t.Parallel()

	const seedLen = 50_000
	seed := arrayValue(seedLen)
	receiver := NewArray([]Value{NewInt(1)})
	block := emptyAccRestReduceBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	baseline := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	full, _ := reduceAccRestChargeBytes(seed, NewInt(1))
	quota := baseline + full + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := arrayReduce(exec, receiver, []Value{seed}, nil, block)
	if err != nil {
		t.Fatalf("reduce over an accumulator-rest block whose tail fits the quota = %v, want success", err)
	}
	if got.Kind() != KindNil {
		t.Fatalf("empty-body reduce returned %v, want nil", got.Kind())
	}
}

// TestArrayReduceNoSeedAccRestNotDoubleCharged is the direct regression for the
// Codex finding on PR #808: a no-initial reduce makes the accumulator the receiver's
// first element, so charging it as a per-call root must not count it a SECOND time
// on top of the receiver. With a large first element and a |(head, *tail), item|
// block, a quota that fits the actual peak (the receiver plus the fresh tail copy)
// must admit the fold; the buggy charge measured the accumulator with a fresh
// estimator and rejected the fold as if a second copy of the first element existed.
// Registering the accumulator as an active root deduplicates it against the receiver,
// so the first element is charged exactly once.
func TestArrayReduceNoSeedAccRestNotDoubleCharged(t *testing.T) {
	t.Parallel()

	// Two elements so the fold runs exactly one block call whose accumulator is the
	// large first element (no seed, so acc = arr[0]). The fresh tail the rest collects
	// is arr[0][1:], whose payload aliases the receiver and so adds only its backing
	// slots. The empty body returns nil, ending the fold after one call.
	const firstLen = 200_000
	first := arrayValue(firstLen)
	receiver := NewArray([]Value{first, NewInt(2)})
	block := emptyAccRestReduceBlock()

	probe := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 0}
	// The true peak is the receiver (which already holds the first element) plus the
	// fresh tail copy. The accumulator IS the first element, already inside the
	// receiver, so it contributes nothing beyond the receiver.
	roots := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	tail := NewArray(slicesClone(first.Array()[1:]))
	est := newMemoryEstimator()
	est.value(receiver)
	tailCharge := est.value(tail)
	// A quota sized to the real peak plus modest headroom must admit the fold. A
	// phantom second copy of the 200k-element first element would dwarf this headroom,
	// so success proves the accumulator is not charged twice.
	quota := roots + tailCharge + 64*1024

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: quota}
	got, err := arrayReduce(exec, receiver, nil, nil, block)
	if err != nil {
		t.Fatalf("no-seed reduce whose accumulator is the receiver's first element = %v, want success (the accumulator must not be charged twice)", err)
	}
	if got.Kind() != KindNil {
		t.Fatalf("empty-body reduce returned %v, want nil", got.Kind())
	}

	// Guard that the headroom genuinely cannot hold a second copy of the first
	// element, so the success above reflects single-charging rather than slack. The
	// buggy charge measured the accumulator with a fresh estimator (no receiver
	// dedup), which is the full footprint here.
	doubleChargeProbe := newMemoryEstimator()
	secondCopy := doubleChargeProbe.value(first)
	if secondCopy <= 64*1024 {
		t.Fatalf("test setup expects a second copy of the first element (%d bytes) to dwarf the headroom (%d)", secondCopy, 64*1024)
	}
}
