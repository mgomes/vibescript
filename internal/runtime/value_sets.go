package runtime

import (
	"math"

	"github.com/mgomes/vibescript/vibes/value"
)

// setOpInitialCap bounds the capacity reserved up front by the array set
// helpers (union, difference, and uniq). The result and the membership set are
// at most as large as the inputs, but for heavily overlapping inputs they can
// be far smaller. Reserving the full input length would peak at roughly the same
// memory as the temporary slices these helpers were written to avoid, and that
// allocation escapes the post-call memory check. Capping the reservation and
// letting append and map growth take over keeps the peak proportional to the
// data actually retained.
const (
	setOpInitialCap    = 4096
	setOpCheckInterval = 64
)

// boundedSetCap caps a desired capacity at setOpInitialCap so a huge input
// length never drives an oversized up-front allocation.
func boundedSetCap(n int) int {
	if n > setOpInitialCap {
		return setOpInitialCap
	}
	return n
}

// estimatedScalarSetEntryBytes approximates one scalar-map entry: the key
// struct (whose string fields alias existing payloads), a bucket share, and
// map overhead.
const estimatedScalarSetEntryBytes = 160

// setOpScratch keeps a set operation's Go-local buffers — the result slice,
// the distinct-composite slice, and the scalar maps — reserved in loop
// scratch for the operation's lifetime. The buffers grow with the input yet
// live only in Go locals, invisible to the estimator's base walk, so
// without the reservation a later check (a metered probe's scratch
// validation, a periodic walk) would admit an allocation whose true peak
// includes them. Slice reservations are incremental at append-doubling
// granularity; everything is released when the operation returns. A nil
// exec or an unlimited quota makes every method a no-op.
type setOpScratch struct {
	exec *Execution
	// roots wrap the operation's input slices: they can be host-returned
	// values live only as builtin call roots, invisible to the base walk,
	// yet they coexist with every buffer this scratch reserves.
	roots        []Value
	held         int
	resultCap    int
	compositeCap int
	// scalarCap counts the scalar-map entries covered by the up-front
	// hinted-capacity reservation; entries beyond it reserve individually.
	scalarCap int
}

// newSetOpScratch builds the reservation tracker for a set operation over
// the receiver and the other operand slices. Taking them separately lets a
// high-arity caller spread its argument slice directly instead of
// materializing a combined source list, which would itself be an O(arity)
// Go-local buffer allocated before any reservation. The inputs are wrapped
// as extra roots for every validation; the wrappers alias the callers'
// backings, so an input that is also reachable from an execution root
// deduplicates rather than double-counting. The wrapper slice is itself a
// Go-local buffer that grows with the operation's arity, so its backing
// joins held scratch before it is materialized and is validated immediately:
// a caller may never reserve anything else (a high-arity union of empty
// arrays), leaving this the only check that sees the backing.
func newSetOpScratch(exec *Execution, lead []Value, rest ...[]Value) (setOpScratch, error) {
	s := setOpScratch{exec: exec}
	if exec == nil || exec.memoryQuota <= 0 {
		return s, nil
	}
	count := len(rest) + 1
	s.held += exec.reserveLoopScratch(valueSliceScratchBytes(count))
	s.roots = make([]Value, 0, count)
	s.roots = append(s.roots, NewArray(lead))
	for _, src := range rest {
		s.roots = append(s.roots, NewArray(src))
	}
	return s, exec.checkMemoryWith(s.roots...)
}

func (s *setOpScratch) reserve(extra int) error {
	if s.exec == nil || s.exec.memoryQuota <= 0 || extra <= 0 {
		return nil
	}
	s.held += s.exec.reserveLoopScratch(extra)
	return s.exec.checkMemoryWith(s.roots...)
}

// reserveResultCap reserves the result slice's initial backing.
func (s *setOpScratch) reserveResultCap(capacity int) error {
	if capacity <= s.resultCap {
		return nil
	}
	extra := valueSliceScratchBytes(capacity) - valueSliceScratchBytes(s.resultCap)
	s.resultCap = capacity
	return s.reserve(extra)
}

// reserveResultSlot reserves the growth an append at the given length
// realizes, once per doubling.
func (s *setOpScratch) reserveResultSlot(length int) error {
	return s.reserveResultCap(projectedAppendCap(length, s.resultCap))
}

func (s *setOpScratch) reserveCompositeSlot(length int) error {
	nextCap := projectedAppendCap(length, s.compositeCap)
	if nextCap <= s.compositeCap {
		return nil
	}
	extra := valueSliceScratchBytes(nextCap) - valueSliceScratchBytes(s.compositeCap)
	s.compositeCap = nextCap
	return s.reserve(extra)
}

// reserveScalarMapCap reserves the hinted scalar map's initial backing before
// it is allocated: make preallocates the whole bucket array for the hinted
// capacity, so a receiver near the quota must fail this validation instead of
// allocating the buckets first and being rejected 160 bytes at a time later.
func (s *setOpScratch) reserveScalarMapCap(capacity int) error {
	if capacity <= s.scalarCap {
		return nil
	}
	extra := (capacity - s.scalarCap) * estimatedScalarSetEntryBytes
	s.scalarCap = capacity
	return s.reserve(extra)
}

// reserveScalarEntry reserves one scalar-map entry past the hinted-capacity
// reservation; existing is the map's current entry count.
func (s *setOpScratch) reserveScalarEntry(existing int) error {
	if existing < s.scalarCap {
		return nil
	}
	return s.reserve(estimatedScalarSetEntryBytes)
}

func (s *setOpScratch) release() {
	if s.exec != nil {
		s.exec.releaseLoopScratch(s.held)
	}
	s.held = 0
}

type scalarValueSetKey struct {
	kind     ValueKind
	boolVal  bool
	intVal   int64
	floatVal float64
	floatNaN bool
	textVal  string
	moneyVal Money
	durVal   Duration
	rangeVal Range
}

func scalarValueKey(v Value) (scalarValueSetKey, bool) {
	key := scalarValueSetKey{kind: v.Kind()}
	switch v.Kind() {
	case KindNil:
	case KindBool:
		key.boolVal = v.Bool()
	case KindInt:
		if bi, ok := value.BigIntPayload(v); ok {
			// Big integers key by their hexadecimal text (linear in the
			// payload's words, unlike superlinear decimal); the canonical
			// invariant keeps that disjoint from compact keys (which leave
			// textVal empty), and the kind tag separates it from string/symbol
			// text. Runtime callers charge steps per key word before building.
			key.textVal = bi.Text(16)
		} else {
			key.intVal = v.Int()
		}
	case KindFloat:
		if f := v.Float(); math.IsNaN(f) {
			key.floatNaN = true
		} else {
			key.floatVal = f
		}
	case KindString, KindSymbol:
		key.textVal = v.String()
	case KindMoney:
		key.moneyVal = v.Money()
	case KindDuration:
		key.durVal = v.Duration()
	case KindRange:
		key.rangeVal = v.Range()
	default:
		return scalarValueSetKey{}, false
	}
	return key, true
}

// valueSet tracks membership of Values using value equality, collapsing
// duplicates as values are added. Scalar values are indexed in a map keyed by
// their content, while composite values (arrays, hashes, and other non-scalar
// kinds) fall back to a linear scan with Value.Equal. union and uniq build on it
// because they need duplicate collapsing; difference and subtract use the
// non-deduping membershipSet instead.
type valueSet struct {
	scalars   map[scalarValueSetKey]struct{}
	composite []Value
	// equality carries the optional byte charge for composite probes (see
	// bindMetering); its sticky error is surfaced via chargeErr.
	equality EqualityContext
	// scratch, when set, keeps the composite slice and scalar map reserved
	// in loop scratch as they grow; a reservation failure is sticky in
	// scratchErr and surfaced via chargeErr.
	scratch    *setOpScratch
	scratchErr error
}

// bindMetering makes the set's composite equality probes bill the string
// bytes they read and validate their sort scratch against the memory quota.
// Callers must consult chargeErr after operations that probe. A nil exec
// leaves the set unmetered.
func (s *valueSet) bindMetering(exec *Execution) {
	exec.bindEqualityMetering(&s.equality)
}

// chargeErr reports the first byte-charge or scratch-reservation failure an
// operation recorded, if any.
func (s *valueSet) chargeErr() error {
	if s.scratchErr != nil {
		return s.scratchErr
	}
	return s.equality.Err()
}

// add inserts v into the set if absent and reports whether it was newly added.
// hint sizes the scalar map on first use; it is capped by boundedSetCap so a
// huge input length never drives an oversized map allocation, letting the map
// grow to the number of distinct scalars actually inserted. Composite values are
// deduplicated via a linear Value.Equal scan, so add is suited to the
// duplicate-collapsing helpers (union, uniq) but not to membership-only callers
// where that scan would make insertion quadratic.
func (s *valueSet) add(v Value, hint int) bool {
	added, _ := s.addCounted(v, hint)
	return added
}

// addCounted is add, additionally reporting how many equality probes the
// composite scan performed so a caller can charge the step quota for them.
//
// The count comes back from the scan rather than being predicted from the set's
// size for two reasons. The scan stops at the first match, so a duplicate that
// matches near the front costs one probe, not the whole set -- predicting a
// miss would overcharge a duplicate-heavy tail badly enough to exhaust the
// quota on work never done. And classifying a value as scalar means building
// its key, which for a big integer is a base-16 conversion of the whole
// payload; doing that here rather than in a separate predicate keeps it to the
// one conversion per element the caller already budgets for.
func (s *valueSet) addCounted(v Value, hint int) (bool, int) {
	if s.scratchErr != nil {
		return false, 0
	}
	if key, ok := scalarValueKey(v); ok {
		if s.scalars == nil {
			if s.scratch != nil {
				if err := s.scratch.reserveScalarMapCap(boundedSetCap(hint)); err != nil {
					s.scratchErr = err
					return false, 0
				}
			}
			s.scalars = make(map[scalarValueSetKey]struct{}, boundedSetCap(hint))
		}
		if _, found := s.scalars[key]; found {
			return false, 0
		}
		if s.scratch != nil {
			if err := s.scratch.reserveScalarEntry(len(s.scalars)); err != nil {
				s.scratchErr = err
				return false, 0
			}
		}
		s.scalars[key] = struct{}{}
		return true, 0
	}
	probes, found := indexOfEqualValue(s.composite, v, &s.equality)
	if found {
		return false, probes + 1
	}
	if s.scratch != nil {
		if err := s.scratch.reserveCompositeSlot(len(s.composite)); err != nil {
			s.scratchErr = err
			return false, probes
		}
	}
	s.composite = append(s.composite, v)
	return true, probes
}

// scalarSetEligible reports whether the scalar set indexes v's kind (see
// scalarValueKey), without building the key — the build itself performs the
// big-integer canonicalization the callers charge for.
func scalarSetEligible(v Value) bool {
	switch v.Kind() {
	case KindNil, KindBool, KindInt, KindFloat, KindString, KindSymbol, KindMoney, KindDuration, KindRange:
		return true
	default:
		return false
	}
}

// anyScalarSetKey reports whether values holds at least one element the
// scalar set indexes, using kind tests only. With no scalar on the lookup
// side, the other side's scalars are never hashed or canonicalized.
func anyScalarSetKey(values []Value) bool {
	for _, v := range values {
		if scalarSetEligible(v) {
			return true
		}
	}
	return false
}

// chargeScanSteps charges the step quota for n units of scanning work: the
// elements a pass will visit, or the equality probes a valueSet operation
// performed. Scanning nothing must cost nothing, and stepN charges one step
// even for a count of zero, which would make an empty receiver pay for a scan
// it never ran and would triple the per-element cost of deduplicating scalars.
//
// A probe charge necessarily follows the work, since only the completed scan
// knows its length. A single operation can therefore overshoot the quota before
// it fires, by at most the number of distinct composites already held -- one
// element's worth, which the element before it just paid nearly in full.
func (exec *Execution) chargeScanSteps(n int) error {
	// The set helpers accept a nil execution -- they are called directly from
	// tests and from paths with no quota to charge -- and bindEqualityMetering
	// and newSetOpScratch already tolerate one, so this does too.
	if exec == nil || n <= 0 {
		return nil
	}
	return exec.stepN(n)
}

// containsCounted reports whether the set holds a value equal to v, along with
// the equality probes the composite scan performed. See addCounted for why the
// count is measured rather than predicted.
func (s *valueSet) containsCounted(v Value) (bool, int) {
	if s.scalars == nil {
		if scalarSetEligible(v) {
			return false, 0
		}
	} else if key, ok := scalarValueKey(v); ok {
		_, found := s.scalars[key]
		return found, 0
	}
	probes, found := indexOfEqualValue(s.composite, v, &s.equality)
	if found {
		return true, probes + 1
	}
	return false, probes
}

// membershipSet answers contains queries with value equality but, unlike
// valueSet, never deduplicates on insertion. Scalars are indexed in a map for
// O(1) membership; composites are collected and scanned with Value.Equal only
// when contains is asked about one. difference and subtract use it because they
// only need to know whether the removal side holds a value, never how many
// times. Skipping the scan on insertion is what keeps building the removal side
// linear in the argument length even when the arguments are full of distinct
// composites.
//
// It collects the composites rather than retaining the argument slices they came
// from. Retaining the slices costs no memory, but it puts every scalar beside
// them in the path of a composite query: the scan then walks the whole argument
// to reach the few composites in it, and rejects the rest on a kind mismatch
// that compares nothing. A difference of 400 composites against 8,000 scalars
// took 47.3ms that way against 1.8ms for the same work here, and equality
// metering cannot price it, because charging for bytes compared charges nothing
// for a comparison that never begins.
//
// Collecting them costs one Value per composite, which is memory the scratch
// reservation now prices -- it did not exist when the slices were first retained
// to avoid exactly that allocation.
type membershipSet struct {
	scalars map[scalarValueSetKey]struct{}
	// equality carries the optional byte charge for composite probes; see
	// valueSet.bindMetering.
	equality EqualityContext
	// scratch, when set, keeps the scalar map reserved in loop scratch as
	// it grows; a reservation failure is sticky in scratchErr.
	scratch    *setOpScratch
	scratchErr error
	// composite holds the composite values from every source, in insertion
	// order and with duplicates kept; contains scans it.
	composite []Value
}

// containsCounted reports whether the set holds a value equal to v, along with
// the equality probes the composite scan performed. The count is measured
// rather than predicted, and callers charge it: a composite is matched by
// scanning every composite the removal side holds, so a per-element step alone
// would let a receiver of c composites cost c*n unmetered comparisons against n
// of them. uniq already charges its own scan for the same reason.
func (s *membershipSet) containsCounted(v Value) (bool, int) {
	if s.scalars == nil {
		// No scalar was ever added, so a scalar probe cannot match; building
		// its key anyway would run the big-integer canonicalization for
		// nothing.
		if scalarSetEligible(v) {
			return false, 0
		}
	} else if key, ok := scalarValueKey(v); ok {
		_, found := s.scalars[key]
		return found, 0
	}
	probes, found := indexOfEqualValue(s.composite, v, &s.equality)
	return found, probes
}

// addSource records every value in values for later membership tests. hint sizes
// the scalar map on first use, capped by boundedSetCap. Scalars are deduplicated
// by the map key for free; composites are appended without being scanned
// against what is already there, so insertion stays linear in len(values)
// however many distinct composites it holds.
func (s *membershipSet) addSource(values []Value, hint int) {
	for _, v := range values {
		if s.scratchErr != nil {
			return
		}
		key, ok := scalarValueKey(v)
		if !ok {
			if s.scratch != nil {
				if err := s.scratch.reserveCompositeSlot(len(s.composite)); err != nil {
					s.scratchErr = err
					return
				}
			}
			s.composite = append(s.composite, v)
			continue
		}
		if s.scalars == nil {
			if s.scratch != nil {
				if err := s.scratch.reserveScalarMapCap(boundedSetCap(hint)); err != nil {
					s.scratchErr = err
					return
				}
			}
			s.scalars = make(map[scalarValueSetKey]struct{}, boundedSetCap(hint))
		}
		if _, found := s.scalars[key]; found {
			continue
		}
		if s.scratch != nil {
			if err := s.scratch.reserveScalarEntry(len(s.scalars)); err != nil {
				s.scratchErr = err
				return
			}
		}
		s.scalars[key] = struct{}{}
	}
}

func uniqueValues(values []Value) []Value {
	unique, _ := uniqueValuesMetered(values, nil, nil, nil)
	return unique
}

// uniqueValuesMetered deduplicates values, calling check periodically so a
// long run stays cancellable and charging for the work each add is about to do.
// Both callbacks are optional.
//
// A scalar canonicalizes into a map and costs O(1), but a composite is matched
// by scanning every distinct composite already seen, so n distinct composites
// cost n(n-1)/2 equality probes. Charging one step per element covers only the
// scalar case; without charging the scan, an array of composites that fits the
// memory quota could spend billions of comparisons inside the step budget.
// charge receives the number of probes the next add will perform.
// meterExec, when non-nil, makes composite probes bill the string bytes they
// read and validate their sort scratch, so nested payloads are bounded like
// the probe count is (#1135).
func uniqueValuesMetered(values []Value, check func() error, charge func(int) error, meterExec *Execution) ([]Value, error) {
	var seen valueSet
	seen.bindMetering(meterExec)
	scratch, err := newSetOpScratch(meterExec, values)
	defer scratch.release()
	if err != nil {
		return nil, err
	}
	seen.scratch = &scratch
	initial := boundedSetCap(len(values))
	if err := scratch.reserveResultCap(initial); err != nil {
		return nil, err
	}
	unique := make([]Value, 0, initial)
	for i, item := range values {
		if check != nil && i%setOpCheckInterval == 0 {
			if err := check(); err != nil {
				return nil, err
			}
		}
		added, probes := seen.addCounted(item, len(values))
		if err := seen.chargeErr(); err != nil {
			return nil, err
		}
		if charge != nil {
			if err := charge(probes); err != nil {
				return nil, err
			}
		}
		if added {
			if err := scratch.reserveResultSlot(len(unique)); err != nil {
				return nil, err
			}
			unique = append(unique, item)
		}
	}
	return unique, nil
}

// unionArrayValues returns the receiver concatenated with every array in others,
// duplicates removed while preserving first-seen order, mirroring Ruby's
// Array#union(*others). The receiver's own duplicates are collapsed too, so the
// result is always free of repeats. The unique result is built directly while
// iterating the inputs, so no intermediate concatenated slice is materialized.
func unionArrayValues(exec *Execution, left []Value, others [][]Value) ([]Value, error) {
	total := len(left)
	for _, other := range others {
		total += len(other)
	}
	var seen valueSet
	seen.bindMetering(exec)
	scratch, err := newSetOpScratch(exec, left, others...)
	defer scratch.release()
	if err != nil {
		return nil, err
	}
	seen.scratch = &scratch
	initial := boundedSetCap(total)
	if err := scratch.reserveResultCap(initial); err != nil {
		return nil, err
	}
	unique := make([]Value, 0, initial)
	appendUnique := func(item Value) error {
		if err := scratch.reserveResultSlot(len(unique)); err != nil {
			return err
		}
		unique = append(unique, item)
		return nil
	}
	for _, item := range left {
		added := seen.add(item, total)
		if err := seen.chargeErr(); err != nil {
			return nil, err
		}
		if added {
			if err := appendUnique(item); err != nil {
				return nil, err
			}
		}
	}
	for _, other := range others {
		for _, item := range other {
			added := seen.add(item, total)
			if err := seen.chargeErr(); err != nil {
				return nil, err
			}
			if added {
				if err := appendUnique(item); err != nil {
					return nil, err
				}
			}
		}
	}
	return unique, nil
}

// differenceArrayValues returns the elements of left that do not appear in any
// of the others, mirroring Ruby's Array#difference(*others). Unlike union it
// preserves the receiver's own duplicates: only elements equal to something in
// the others are dropped. The removal side indexes the others' scalars in a map
// and retains references to their composite-bearing slices, so no flattened copy
// of the arguments is materialized and the extra memory stays proportional to
// the number of argument slices rather than their total length.
func differenceArrayValues(exec *Execution, left []Value, others [][]Value) ([]Value, error) {
	if len(others) == 0 {
		out := make([]Value, len(left))
		copy(out, left)
		return out, nil
	}
	removalTotal := 0
	for _, other := range others {
		removalTotal += len(other)
	}
	var removal membershipSet
	exec.bindEqualityMetering(&removal.equality)
	scratch, err := newSetOpScratch(exec, left, others...)
	defer scratch.release()
	if err != nil {
		return nil, err
	}
	removal.scratch = &scratch
	for _, other := range others {
		removal.addSource(other, removalTotal)
		if removal.scratchErr != nil {
			return nil, removal.scratchErr
		}
	}
	initial := boundedSetCap(len(left))
	if err := scratch.reserveResultCap(initial); err != nil {
		return nil, err
	}
	out := make([]Value, 0, initial)
	for _, item := range left {
		hit, probes := removal.containsCounted(item)
		if err := removal.equality.Err(); err != nil {
			return nil, err
		}
		if err := exec.chargeScanSteps(probes); err != nil {
			return nil, err
		}
		keep := !hit
		if keep {
			if err := scratch.reserveResultSlot(len(out)); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// intersectArrayValues returns the elements of left that also appear in right,
// duplicates removed while preserving the left array's first-seen order,
// mirroring Ruby's Array#&. The right array seeds a membership set so each left
// element is a constant-time scalar lookup (or an equality scan for composite
// elements), and a second set tracks which results were already emitted so the
// output never repeats a value.
func intersectArrayValues(exec *Execution, left, right []Value) ([]Value, error) {
	var inRight membershipSet
	exec.bindEqualityMetering(&inRight.equality)
	scratch, err := newSetOpScratch(exec, left, right)
	defer scratch.release()
	if err != nil {
		return nil, err
	}
	inRight.scratch = &scratch
	inRight.addSource(right, len(right))
	if inRight.scratchErr != nil {
		return nil, inRight.scratchErr
	}
	var emitted valueSet
	emitted.bindMetering(exec)
	emitted.scratch = &scratch
	initial := boundedSetCap(min(len(left), len(right)))
	if err := scratch.reserveResultCap(initial); err != nil {
		return nil, err
	}
	out := make([]Value, 0, initial)
	for _, item := range left {
		hit, probes := inRight.containsCounted(item)
		if err := inRight.equality.Err(); err != nil {
			return nil, err
		}
		if err := exec.chargeScanSteps(probes); err != nil {
			return nil, err
		}
		if !hit {
			continue
		}
		added := emitted.add(item, len(left))
		if err := emitted.chargeErr(); err != nil {
			return nil, err
		}
		if added {
			if err := scratch.reserveResultSlot(len(out)); err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}

func subtractArrayValues(exec *Execution, left, right []Value) ([]Value, error) {
	var removal membershipSet
	exec.bindEqualityMetering(&removal.equality)
	scratch, err := newSetOpScratch(exec, left, right)
	defer scratch.release()
	if err != nil {
		return nil, err
	}
	removal.scratch = &scratch
	removal.addSource(right, len(right))
	if removal.scratchErr != nil {
		return nil, removal.scratchErr
	}
	initial := boundedSetCap(len(left))
	if err := scratch.reserveResultCap(initial); err != nil {
		return nil, err
	}
	out := make([]Value, 0, initial)
	for _, item := range left {
		hit, probes := removal.containsCounted(item)
		if err := removal.equality.Err(); err != nil {
			return nil, err
		}
		if err := exec.chargeScanSteps(probes); err != nil {
			return nil, err
		}
		if hit {
			continue
		}
		if err := scratch.reserveResultSlot(len(out)); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// indexOfEqualValue finds target by value equality, reporting the index it
// matched at. On a miss it reports len(values). Either way the result is the
// number of equality probes performed, which is what the step quota charges:
// only the scan knows where it stopped, and a match near the front costs far
// less than the full set.
// A nil equality context compares unmetered with a local scratch.
func indexOfEqualValue(values []Value, target Value, equality *EqualityContext) (int, bool) {
	if equality == nil {
		equality = &EqualityContext{}
	}
	for i, candidate := range values {
		if equality.Equal(target, candidate) {
			return i, true
		}
		if equality.Err() != nil {
			// A sticky charge failure makes every later probe answer false
			// in O(1), and the caller surfaces the error, so scanning the
			// rest of the source is pure post-quota work.
			return i + 1, false
		}
	}
	return len(values), false
}
