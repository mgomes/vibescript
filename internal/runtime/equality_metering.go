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

// Pricing walk scratch means estimating the reachable graph, and every check a
// builtin drives is uncached by construction (see beginBaseWalk), so pricing
// before each allocation made a compared one-entry hash pay a whole-graph walk
// to place 24 bytes of key slice: a membership probe over n small hashes ran n
// uncached walks while charging n steps, turning an O(n) scan into O(n²) host
// work. Scratch is therefore repriced once per granule instead.
//
// The granule cannot be a fixed byte count. Walk scratch is Go-local and never
// reserved, so nothing else observes it: whatever goes unpriced is invisible to
// the periodic check too. A constant sized against the default profile stops
// meaning anything once a host configures a smaller quota, and says nothing at
// all about an execution that has nearly exhausted a large one — a 48 KiB
// comparison would clear a 64 KiB granule with bytes of headroom left.
//
// So the granule is derived per check: at most a fixed fraction of the
// configured quota, shrinking further with the headroom left at the last
// estimate, so what may go unpriced is always small relative to what remains
// rather than to a default nobody kept.
const (
	// equalityScratchQuotaShift caps the granule at quota>>shift, bounding
	// unpriced scratch to a fraction of whatever the host configured.
	equalityScratchQuotaShift = 8
	// equalityScratchHeadroomShift shrinks the granule as headroom is spent,
	// so seven eighths of the remaining budget must be consumed by growth the
	// periodic check does see before unpriced scratch could matter.
	equalityScratchHeadroomShift = 3
	// equalityScratchMinGranule floors the headroom term. Without it an
	// execution parked just under its quota would price every comparison, and
	// a probe loop over small composites would be back to a whole-graph walk
	// per candidate — the cost this batching exists to remove, reachable on
	// purpose. It bounds worst-case unpriced scratch at the limit to a few
	// KiB, well inside the estimator's own modeling error, and it never
	// raises the granule above the quota-derived cap.
	equalityScratchMinGranule = 4 << 10
)

// equalityScratchGranule returns the walk scratch that may accumulate unpriced
// before the validator reprices. lastMemoryUsage is only as fresh as the last
// estimate, so the quota-derived cap is what bounds the granule when that
// figure is stale; the headroom term only ever tightens it.
func (exec *Execution) equalityScratchGranule() int {
	capBytes := exec.memoryQuota >> equalityScratchQuotaShift
	headroom := exec.memoryQuota - exec.lastMemoryUsage
	if headroom < 0 {
		headroom = 0
	}
	granule := headroom >> equalityScratchHeadroomShift
	if floor := min(equalityScratchMinGranule, capBytes); granule < floor {
		granule = floor
	}
	return min(granule, capBytes)
}

// equalityScratchValidatorFunc returns the cached scratch validator: it holds
// a reservation only for the duration of one memory check, because the
// walk's Go-local slices are invisible to the estimator and freed before any
// later full walk runs — the point is rejecting an allocation that would
// carry the transient footprint past the quota. The compared operands ride
// along as extra roots: they can be temporaries (a host capability's return,
// a set probe's stored element) that no execution root reaches, yet both
// graphs coexist with the scratch at its peak.
//
// bytes is the walk's cumulative held scratch. Zero marks a comparison
// boundary and retires the granule mark; within a walk the mark is a
// high-water figure and is deliberately not lowered when scratch is released,
// because no script code runs mid-walk, so a footprint already priced against
// this graph cannot have become unsafe before the walk ends.
func (exec *Execution) equalityScratchValidatorFunc() func(int, Value, Value) error {
	if exec == nil {
		return nil
	}
	if exec.equalityScratchCheck == nil {
		exec.equalityScratchCheck = func(bytes int, left, right Value) error {
			if exec.memoryQuota <= 0 {
				return nil
			}
			if bytes == 0 {
				// A comparison boundary: this walk holds no scratch yet, so
				// whatever the previous one priced is dead. The mark cannot be
				// retired by inference — a walk whose first reservation matches
				// the last total priced is indistinguishable from a
				// continuation of it — so the boundary is retired here and
				// only here.
				exec.equalityScratchPriced = 0
				return nil
			}
			if bytes-exec.equalityScratchPriced < exec.equalityScratchGranule() {
				return nil
			}
			exec.equalityScratchPriced = bytes
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
