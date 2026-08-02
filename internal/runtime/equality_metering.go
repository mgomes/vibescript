package runtime

// This file wires the value package's equality byte charge (see
// EqualityContext.SetCharge) to the execution's step quota, so comparisons
// that reach string payloads through arrays and hashes are bounded the same
// way a top-level `s == t` is (#1135).

// stringScanChargeFunc returns the execution's byte charge bound as a plain
// function, cached on the execution so the hot `==` path does not allocate a
// method value per comparison. A nil execution returns nil, which the
// equality context treats as unmetered.
func (exec *Execution) stringScanChargeFunc() func(int) error {
	if exec == nil {
		return nil
	}
	if exec.stringScanCharge == nil {
		exec.stringScanCharge = exec.chargeStringScan
	}
	return exec.stringScanCharge
}

// meteredEquality returns an EqualityContext that bills the string payloads a
// comparison reads at the string-scan rate and validates the walk's transient
// scratch against the memory quota. Loops that probe many candidates build
// one context, reuse it per element, and must surface Err before trusting a
// negative answer.
func (exec *Execution) meteredEquality() EqualityContext {
	var ctx EqualityContext
	ctx.SetCharge(exec.stringScanChargeFunc())
	ctx.SetScratchReserver(exec.equalityScratchValidatorFunc())
	return ctx
}

// hashKeySortScratchEntryBytes approximates one entry of a key-sorting
// scratch slice: a string header plus slice-slot overhead.
const hashKeySortScratchEntryBytes = 24

// equalityScratchValidatorFunc returns the cached scratch validator: it holds
// a reservation only for the duration of one memory check, because the
// walk's Go-local slices are invisible to the estimator and freed before any
// later full walk runs — the point is rejecting an allocation that would
// carry the transient footprint past the quota.
func (exec *Execution) equalityScratchValidatorFunc() func(int) error {
	if exec == nil {
		return nil
	}
	if exec.equalityScratchCheck == nil {
		exec.equalityScratchCheck = func(bytes int) error {
			if exec.memoryQuota <= 0 {
				return nil
			}
			delta := exec.reserveLoopScratch(bytes)
			err := exec.checkMemory()
			exec.releaseLoopScratch(delta)
			return err
		}
	}
	return exec.equalityScratchCheck
}

// equalValues is the one-shot metered comparison behind `==`, `!=`, and case
// equality: the boolean answer is only meaningful when the error is nil.
func (exec *Execution) equalValues(left, right Value) (bool, error) {
	ctx := exec.meteredEquality()
	eq := ctx.Equal(left, right)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return eq, nil
}
