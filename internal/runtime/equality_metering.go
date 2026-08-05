package runtime

// This file wires the value package's equality byte charge (see
// EqualityContext.SetCharge) to the execution's step quota, so comparisons
// that reach string payloads through arrays and hashes are bounded the same
// way a top-level `s == t` is (#1135).

// stringScanChargeFunc returns the execution's equality byte charge bound as
// a plain function, cached on the execution so the hot `==` path does not
// allocate a method value per comparison. A nil execution returns nil, which
// the equality context treats as unmetered.
func (exec *Execution) stringScanChargeFunc() func(int) error {
	if exec == nil {
		return nil
	}
	if exec.stringScanCharge == nil {
		exec.stringScanCharge = exec.chargeEqualityScanBytes
	}
	return exec.stringScanCharge
}

// chargeEqualityScanBytes bills equality-walk bytes at the string-scan rate,
// carrying the sub-step remainder on the execution. Rounding each invocation
// down let a probe loop flush a sub-step tail per candidate and never bill
// the aggregate — a set operation could compare hundreds of distinct small
// composites in quadratic time for zero steps. Carrying the remainder settles
// whole steps as tails accumulate, so the unbilled residue stays under one
// step per execution rather than one per probe.
func (exec *Execution) chargeEqualityScanBytes(n int) error {
	if n <= 0 {
		return nil
	}
	exec.equalityScanResidue += n
	steps := exec.equalityScanResidue / stringScanBytesPerStep
	if steps <= 0 {
		return nil
	}
	exec.equalityScanResidue -= steps * stringScanBytesPerStep
	return exec.stepN(steps)
}

// bindEqualityMetering installs the execution's byte charge, scratch
// validator, and allocator rounder on ctx, so equality contexts embedded in
// longer-lived structures (the set helpers) meter exactly like
// meteredEquality's. The rounder makes rendered display keys reserve the
// capacity the allocator realizes for the pregrown builder, not the
// projected length; see projectedBuilderCap for why the size-class gap
// matters.
func (exec *Execution) bindEqualityMetering(ctx *EqualityContext) {
	ctx.SetCharge(exec.stringScanChargeFunc())
	ctx.SetScratchReserver(exec.equalityScratchValidatorFunc())
	ctx.SetScratchAllocRounder(roundedAllocSize)
}

// meteredEquality returns an EqualityContext that bills the string payloads a
// comparison reads at the string-scan rate and validates the walk's transient
// scratch against the memory quota. Loops that probe many candidates build
// one context, reuse it per element, and must surface Err before trusting a
// negative answer.
func (exec *Execution) meteredEquality() EqualityContext {
	var ctx EqualityContext
	exec.bindEqualityMetering(&ctx)
	return ctx
}

// hashKeySortScratchEntryBytes approximates one entry of a key-sorting
// scratch slice: a string header plus slice-slot overhead.
const hashKeySortScratchEntryBytes = 24

// equalityScratchValidatorFunc returns the cached scratch validator: it holds
// a reservation only for the duration of one memory check, because the
// walk's Go-local slices are invisible to the estimator and freed before any
// later full walk runs — the point is rejecting an allocation that would
// carry the transient footprint past the quota. The compared operands ride
// along as extra roots: they can be temporaries (a host capability's return,
// a set probe's stored element) that no execution root reaches, yet both
// graphs coexist with the scratch at its peak.
func (exec *Execution) equalityScratchValidatorFunc() func(int, Value, Value) error {
	if exec == nil {
		return nil
	}
	if exec.equalityScratchCheck == nil {
		exec.equalityScratchCheck = func(bytes int, left, right Value) error {
			if exec.memoryQuota <= 0 {
				return nil
			}
			delta := exec.reserveLoopScratch(bytes)
			err := exec.checkMemoryWith(left, right)
			exec.releaseLoopScratch(delta)
			return err
		}
	}
	return exec.equalityScratchCheck
}

// equalValues is the one-shot metered comparison behind `==`, `!=`, and case
// equality: the boolean answer is only meaningful when the error is nil.
// The context is pooled on the execution: the walk's traversal closures leak
// the context's state, so a stack-scoped context would heap-allocate on every
// comparison. Taking the context out of the pool for the walk keeps any
// re-entrant comparison on a fresh context, and a context that surfaced an
// error is dropped rather than repooled because its charge failure is sticky.
func (exec *Execution) equalValues(left, right Value) (bool, error) {
	var ctx *EqualityContext
	if exec != nil && exec.equalityCtx != nil {
		ctx = exec.equalityCtx
		exec.equalityCtx = nil
	} else {
		// A nil execution (the static checker's speculative comparisons)
		// compares unmetered on a throwaway context.
		fresh := exec.meteredEquality()
		ctx = &fresh
	}
	eq := ctx.Equal(left, right)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if exec != nil {
		exec.equalityCtx = ctx
	}
	return eq, nil
}
