package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/value"
)

// A builtin's non-mutating declaration is a promise about Go code the runtime
// did not write and cannot inspect, so nothing about it can be proven by
// reading the flag. This is the instrument that makes a false one announce
// itself: with verification on, every dispatch of a builtin that declares
// non-mutation walks the execution's reachable graph before and after the call
// and requires it to be unchanged.
//
// The comparison is conditioned on the mutation epoch, which is what makes it
// precise rather than merely conservative. A builtin that drives a script block
// is not promising anything about the block, and a script write inside that
// block advances the epoch on its own; when the epoch moved, the graph was
// allowed to change and the check does not apply. When the epoch did not move
// and the graph did, the difference is exactly the unobserved write the promise
// denies -- a raw slice or map write the epoch could not see -- which is the
// failure the declaration exists to rule out.
//
// It is a test instrument, off in production: it costs a full reachable-graph
// walk per verified dispatch. Its limitation is the usual one for a dynamic
// check, and is worth stating plainly: it proves a declaration only along the
// paths something actually exercises. A builtin with a rarely taken mutating
// branch that no test reaches passes it. It bounds the risk of a wrong
// declaration; it does not remove the need to have read the body.
var builtinContractVerify = false

// builtinGraphFingerprint is what the verifier compares across a call. Total
// bytes catches a write that changes how much the graph holds, which is what
// the memory quota is accounted in. The identity count catches a write that
// swaps one container for another of the same size, which leaves the byte total
// alone but changes what a later walk deduplicates against.
type builtinGraphFingerprint struct {
	bytes      int
	identities int
	epoch      uint64
}

// contractVerification is the handle a verified dispatch holds across the call.
// The zero value means verification is off or the builtin declared nothing, and
// its check is a no-op, so the ordinary dispatch path stays free.
type contractVerification struct {
	active bool
	before builtinGraphFingerprint
}

// beginContractVerification snapshots the reachable graph before a builtin that
// declares non-mutation runs. It must be called after dispatch has done any
// epoch bump of its own, so the recorded epoch reflects only what the callee
// goes on to do.
func (exec *Execution) beginContractVerification(builtin *Builtin) contractVerification {
	if !builtinContractVerify || !builtin.declaredNonMutating() {
		return contractVerification{}
	}
	return contractVerification{active: true, before: exec.builtinGraphFingerprint()}
}

// check re-walks the graph after the call and panics if a builtin that promised
// not to mutate changed it without the epoch moving. It panics rather than
// returning an error because a false declaration is a defect in the embedding
// program, not a script-visible condition a script could rescue.
func (v contractVerification) check(exec *Execution, builtin *Builtin) {
	if !v.active {
		return
	}
	after := exec.builtinGraphFingerprint()
	if after.epoch != v.before.epoch {
		// Something advanced the epoch during the call -- script code the
		// builtin drove, most likely -- so the graph was free to change and
		// every memo covering it has already been invalidated.
		return
	}
	if after.bytes == v.before.bytes && after.identities == v.before.identities {
		return
	}
	panic(fmt.Sprintf(
		"vibescript: builtin %q declares non-mutating but changed the reachable graph "+
			"without advancing the mutation epoch: bytes %d -> %d, identities %d -> %d. "+
			"An unobserved write behind that declaration leaves an execution's memory "+
			"accounting short and lets it allocate past its quota",
		builtin.Name, v.before.bytes, after.bytes, v.before.identities, after.identities))
}

// builtinGraphFingerprint walks the execution's whole reachable graph on a
// throwaway estimator, so the measurement neither reads nor disturbs the
// committed seen-state the memo is built on.
func (exec *Execution) builtinGraphFingerprint() builtinGraphFingerprint {
	est := newMemoryEstimator()
	bytes := exec.estimateGraphBase(est, nil)
	return builtinGraphFingerprint{
		bytes:      bytes,
		identities: est.identityCount(),
		epoch:      value.MutationEpoch(),
	}
}
