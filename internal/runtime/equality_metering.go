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
// comparison reads at the string-scan rate. Loops that probe many candidates
// build one context, reuse it per element, and must surface Err before
// trusting a negative answer.
func (exec *Execution) meteredEquality() EqualityContext {
	var ctx EqualityContext
	ctx.SetCharge(exec.stringScanChargeFunc())
	return ctx
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
