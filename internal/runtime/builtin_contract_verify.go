package runtime

import (
	"fmt"
	"unsafe"

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
// walk per verified dispatch. It has two limitations, and they are independent,
// so knowing the first does not cover the second:
//
//   - Coverage. It proves a declaration only along the paths something actually
//     exercises. A builtin with a rarely taken mutating branch that no test
//     reaches passes it.
//   - Resolution, even on a path it does cover. The comparison is over a byte
//     total and a digest of the reachable identities, so a mutation that changes
//     neither is invisible to it. Overwriting one element with a different value
//     of the same kind and size is the ordinary example. That particular case
//     leaves the memo's answer correct, which is why the check is drawn where it
//     is -- but the boundary is a property of what the memo records, not a
//     guarantee that every write is seen.
//
// Together they bound the risk of a wrong declaration. Neither removes the need
// to have read the body, which is why the runtime declares a set small enough to
// have been read rather than a set observed to pass.
var builtinContractVerify = false

// builtinGraphFingerprint is what the verifier compares across a call. Total
// bytes catches a write that changes how much the graph holds, which is what
// the memory quota is accounted in.
//
// Bytes alone are not enough, and neither is a count alongside them. A builtin
// can raw-replace a reachable container with a different container holding the
// same estimated bytes: the total matches, the number of identities matches,
// and the memo is left pointing at an identity the graph no longer contains, so
// a later value reusing that freed backing deduplicates against it and is
// charged nothing. identities therefore folds the identity values themselves,
// not how many there are.
type builtinGraphFingerprint struct {
	bytes      int
	identities uint64
	epoch      uint64
}

// mixIdentity folds one identity into an order-independent accumulator. The
// combining step is XOR so the walk order cannot change the result, and each
// key is passed through a bit mixer first, salted per seen-set, so that two
// different sets holding the same pointer do not cancel each other out and a
// swap between sets is still visible.
func mixIdentity(acc, salt, key uint64) uint64 {
	h := key + salt*0x9e3779b97f4a7c15
	h ^= h >> 30
	h *= 0xbf58476d1ce4e5b9
	h ^= h >> 27
	h *= 0x94d049bb133111eb
	h ^= h >> 31
	return acc ^ h
}

// identityDigest folds every identity the estimator committed into one value.
// Any container entering or leaving the reachable graph changes it, including a
// one-for-one replacement that leaves both the byte total and the identity
// count untouched.
func (est *memoryEstimator) identityDigest() uint64 {
	var acc uint64
	for i := range est.seenEnvInlineLen {
		acc = mixIdentity(acc, 1, uint64(uintptr(unsafe.Pointer(est.seenEnvInline[i]))))
	}
	for env := range est.seenEnvs {
		acc = mixIdentity(acc, 1, uint64(uintptr(unsafe.Pointer(env))))
	}
	for ptr := range est.seenMaps {
		acc = mixIdentity(acc, 2, uint64(ptr))
	}
	for ptr := range est.seenHashData {
		acc = mixIdentity(acc, 3, uint64(ptr))
	}
	for ptr := range est.seenObjectData {
		acc = mixIdentity(acc, 4, uint64(ptr))
	}
	for ptr := range est.seenSlices {
		acc = mixIdentity(acc, 5, uint64(ptr))
	}
	for id := range est.seenStrings {
		acc = mixIdentity(acc, 6, uint64(id.ptr)^(uint64(id.len)<<32))
	}
	for class := range est.seenClasses {
		acc = mixIdentity(acc, 7, uint64(uintptr(unsafe.Pointer(class))))
	}
	for inst := range est.seenInstances {
		acc = mixIdentity(acc, 8, uint64(uintptr(unsafe.Pointer(inst))))
	}
	for block := range est.seenBlocks {
		acc = mixIdentity(acc, 9, uint64(uintptr(unsafe.Pointer(block))))
	}
	for builtin := range est.seenBuiltins {
		acc = mixIdentity(acc, 10, uint64(uintptr(unsafe.Pointer(builtin))))
	}
	return acc
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
			"without advancing the mutation epoch: bytes %d -> %d, identity digest "+
			"%#x -> %#x. An unobserved write behind that declaration leaves an "+
			"execution's memory accounting short and lets it allocate past its quota",
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
		identities: est.identityDigest(),
		epoch:      value.MutationEpoch(),
	}
}
