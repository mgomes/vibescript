package runtime

import (
	"context"
	"strings"
	"testing"
)

// Every reading of the registered outputs has to be on one basis: their marginal
// over the reachable graph, which is what the memo holds and what
// estimateMemoryUsageBase folds into a snapshot. The fallback used to answer with
// their standalone footprint instead, which counts payloads the graph also holds,
// and a nested driver reaches that fallback on every construction because
// registering the inner output invalidates the memo. A charge built there recorded
// a start value inflated by every outer result aliasing its receiver, and since
// later readings came from the memo and were smaller, the growth between them read
// as zero and the driver's accumulation stopped being charged.
//
// Here the registered output holds exactly what the environment already holds, so
// the marginal is nothing and the standalone footprint is the whole string. The
// two bases are as far apart as they can be, and the fallback must report the
// former.
func TestRetainedOutputMarginalUsesOneBasis(t *testing.T) {
	t.Parallel()

	const payloadBytes = 512 * 1024
	shared := NewString(strings.Repeat("x", payloadBytes))

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)
	exec.root.Define("shared", shared)

	out := []Value{shared}
	exec.pushOutputWalkRoot(retainedValues(&out))
	defer func() { _ = exec.endOutputWalkRoot(nil) }()

	// No memo to answer from, which is the state a nested registration leaves
	// behind: pushing the root above bumped the topology version.
	if exec.baseWalkCache != nil {
		exec.baseWalkCache.valid = false
	}

	standalone := exec.outputWalkBytes(newMemoryEstimator())
	if standalone < payloadBytes {
		t.Fatalf("standalone footprint = %d, want at least the %d-byte payload; the shapes have drifted", standalone, payloadBytes)
	}

	got := exec.retainedOutputMarginalBytes()
	if got >= payloadBytes {
		t.Fatalf("retained output measured %d bytes against a graph that already holds the same %d-byte "+
			"payload; want its marginal (near zero, %d is the standalone footprint), or a snapshot and a "+
			"later reading are on different bases and their difference is meaningless",
			got, payloadBytes, standalone)
	}
}

// The bind charge's start value must be on that same basis, and it takes it from
// its own base walk rather than asking for it, so a nested construction cannot be
// answered by the standalone fallback.
func TestBlockBindChargeStartsOnTheMarginalBasis(t *testing.T) {
	t.Parallel()

	const payloadBytes = 512 * 1024
	shared := NewString(strings.Repeat("x", payloadBytes))

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)
	exec.root.Define("shared", shared)

	out := []Value{shared}
	exec.pushOutputWalkRoot(retainedValues(&out))
	defer func() { _ = exec.endOutputWalkRoot(nil) }()

	charge := newBlockBindCharge(exec, restBindingBlock(), NewNil(), nil, nil, NewNil())
	if charge == nil {
		t.Fatal("no bind charge built for a rest-binding block")
	}
	if charge.retainedAtStart >= payloadBytes {
		t.Fatalf("charge started at %d retained bytes for output that only aliases the graph; want its "+
			"marginal, so liveBaseline's growth is a difference of like quantities", charge.retainedAtStart)
	}
}

// restBindingBlock is the smallest block whose parameters make a bind charge
// necessary: one destructuring parameter that collects a named rest.
func restBindingBlock() *Block {
	pos := Position{Line: 1, Column: 1}
	target := &DestructureTarget{
		Position: pos,
		Elements: []DestructureElement{
			{Target: &Identifier{Name: "head", Position: pos}},
			{Target: &Identifier{Name: "tail", Position: pos}, Rest: true},
		},
	}
	return valueBlock(NewBlock([]Param{{Kind: ParamNormal, Target: target}}, nil, newEnv(nil)))
}

// A base walk that walks the registered outputs reports those visits in
// nodes(), which the accumulator callers charge to the step quota, and also
// added them to exec.outputWalkNodes for chargeRetainedOutputWalk to charge
// again when the callback returned. One traversal, two billers: a lookup
// callback that mutated and then built a multi-pair hash literal paid for the
// output-root portion twice.
//
// Taking the count is what transfers the billing, so this asserts the counter is
// empty afterwards rather than asserting a step total: whoever can see the whole
// traversal bills it, and nothing is left behind for a second biller.
func TestSessionNodesTakesOverTheOutputWalkBilling(t *testing.T) {
	t.Parallel()

	exec := &Execution{ctx: context.Background(), quota: 1 << 30, memoryQuota: 1 << 30}
	exec.root = newEnv(nil)

	// Distinct values the graph does not hold, so the output walk is the bulk of
	// the session's traversal and the overlap is unmistakable.
	out := make([]Value, 0, 200)
	for i := range 200 {
		out = append(out, NewArray([]Value{NewInt(int64(i)), NewString(strings.Repeat("y", 8))}))
	}
	exec.pushOutputWalkRoot(retainedValues(&out))
	defer func() { _ = exec.endOutputWalkRoot(nil) }()

	exec.outputWalkNodes = 0
	s := exec.beginBaseWalk()
	billed := s.nodes()
	s.close()

	if billed <= 0 {
		t.Fatalf("session reported %d nodes; the walk this pins is not happening", billed)
	}
	if exec.outputWalkNodes != 0 {
		t.Fatalf("a session billed %d nodes and left %d of them on outputWalkNodes for "+
			"chargeRetainedOutputWalk to bill a second time", billed, exec.outputWalkNodes)
	}
}
