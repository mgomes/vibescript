package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
)

// Arrays compare lexicographically, as in Ruby: the first element pair that
// differs decides the result, and when every compared pair is equal the
// shorter array sorts first. Without this, an array of arrays could not be
// sorted at all -- which mattered more than it looks, because a pair is the
// language's own representation of a hash entry, so `hash.to_a.sort` was the
// documented route to an ordered hash and it failed.
//
// Only `<=>` and the ordering members (sort, min, max, and their _by forms)
// use this. The relational operators keep rejecting arrays, matching Ruby,
// where Array defines `<=>` but does not include Comparable, so `[1] < [2]`
// raises rather than answering.

// arrayComparePair identifies a pair of arrays being compared.
type arrayComparePair struct {
	leftPtr  uintptr
	rightPtr uintptr
	leftLen  int
	rightLen int
}

// arrayCompareState carries the sandbox hooks and the memo through a
// recursive comparison.
//
// Both exist for the same reason. Deleting a pair from the on-stack set as
// soon as it returned meant two sibling branches re-walked the same subtree,
// so comparing shared DAGs -- each level holding the previous child twice on
// both sides -- did 2^d work. Measured before the fix: 152ms, 551ms, and
// 2.1s at depths 20, 22, and 24, with the step quota never firing, because
// nothing in the walk charged a step. A script could monopolize the runtime
// from inside <=> regardless of its limits.
//
// Memoizing each completed pair collapses the shared subtrees to one
// comparison apiece, and charging a step per compared element makes the walk
// observable to the step quota and to cancellation.
type arrayCompareState struct {
	exec *Execution
	// onStack holds the pairs currently being compared, so a cyclic structure
	// terminates.
	onStack map[arrayComparePair]struct{}
	// done caches the outcome of pairs already compared, so a subtree shared
	// between siblings is walked once rather than once per path.
	done map[arrayComparePair]arrayCompareResult
	// cycleShortcuts counts how many times a comparison answered "equal"
	// because the pair was already on the stack. A result computed while such
	// a shortcut fired depends on the enclosing cycle and is not reusable
	// elsewhere, so only results whose subtree took none are memoized.
	cycleShortcuts int
	// evictionOrder is a ring of the memoized keys in insertion order, and
	// evictionNext is where the next key goes. Once the memo is full the entry
	// they name is dropped to make room.
	//
	// Insertion order is what matters. Entries are added as the recursion
	// unwinds, deepest first, and it is the shallower results a sibling branch
	// asks for next -- so keeping the newest is what keeps a shared DAG
	// linear. Simply refusing new entries once full kept the deepest ones
	// instead, and every level above the bound was recomputed on both
	// branches: a 300-deep DAG went exponential and tripped the step quota.
	evictionOrder []arrayComparePair
	evictionNext  int
	// memoReserved is the scratch held for the memo for the walk's duration.
	memoReserved int
	// memoTried records that the reservation has been attempted, so a walk
	// that could not afford the memo does not retry on every pair.
	memoTried bool
	// callRoots are the caller's Go-frame values, weighed alongside the
	// reservation when the memo is admitted.
	callRoots []Value
}

// arrayCompareMemoEntryBytes is the charge for one memoized pair.
//
// The key and result structs are 32 bytes each on 64-bit Go, but the map also
// carries control data, table and directory headers, and load-factor slack --
// and, because it grows rather than being preallocated, the old and new tables
// briefly coexist. Measured peak while filling to the bound: 77,400 bytes, or
// 302 per entry. The charge rounds that up so the reservation stays above the
// real footprint rather than under it.
const arrayCompareMemoEntryBytes = 320

// arrayCompareMemoRingEntryBytes is the charge for one eviction-ring slot: an
// arrayComparePair is 32 bytes on 64-bit Go. The ring is allocated at its full
// size alongside the memo so it never grows, and both are covered by the one
// reservation.
const arrayCompareMemoRingEntryBytes = 32

// arrayCompareMemoMaxEntries bounds the memo. It is generous enough for the
// sharing the memo exists to collapse -- a shared DAG has one distinct pair
// per level, so this covers nesting far deeper than any real structure --
// while capping the host memory a comparison can hold at about 80KB.
const arrayCompareMemoMaxEntries = 256

// newArrayCompareState reserves the memo's whole footprint once, up front.
//
// Reserving per entry was the alternative and it was worse in three ways: the
// reservation had to be rolled back when an entry was dropped, a failure
// partway through unwinding raced the comparison's own error, and the quota
// check it needed walks the entire reachable heap, because these comparisons
// run inside builtin dispatch where the base-walk cache is disabled -- so an
// N-deep unwind did O(N^2) unmetered work after an O(N)-step descent.
//
// Charging once removes all three. If the reservation does not fit, the walk
// runs without a memo: correct, slower on shared structures, and still
// bounded by the step quota.
// newArrayCompareState builds the state for one comparison pass. callRoots are
// the values the caller holds live on its Go frame -- the receiver it is
// sorting, the operands being compared -- which the execution's own base walk
// cannot see. The memo is admitted against them so its reservation is weighed
// against the real live set rather than the roots alone.
func newArrayCompareState(exec *Execution, callRoots ...Value) *arrayCompareState {
	return &arrayCompareState{exec: exec, callRoots: callRoots}
}

// ensureMemo takes the reservation and allocates the map, once, the first time
// a pair of arrays is actually tracked.
//
// It is deliberately lazy. The ordering members compare scalars far more often
// than arrays, and reserving up front made every one of those comparisons take
// the reservation and run checkMemory -- a full reachable-graph walk, since
// builtin dispatch disables the base-walk cache -- turning linear extrema over
// a scalar array into quadratic work.
//
// The map grows rather than being preallocated at the bound. Presizing meant
// even `[1] <=> [2]` allocated the whole thing, so a script comparing small
// arrays produced hundreds of kilobytes of garbage per operation; the charge
// covers the growth peak instead.
func (state *arrayCompareState) ensureMemo() {
	if state.memoTried {
		return
	}
	state.memoTried = true
	if state.exec == nil {
		state.done = map[arrayComparePair]arrayCompareResult{}
		state.evictionOrder = make([]arrayComparePair, 0, arrayCompareMemoMaxEntries)
		return
	}
	reserved := state.exec.reserveLoopScratch(arrayCompareMemoMaxEntries * (arrayCompareMemoEntryBytes + arrayCompareMemoRingEntryBytes))
	if !state.exec.memoryFitsWith(state.callRoots...) {
		// Comparison proceeds without the memo; no room for it is a capacity
		// answer, not exhaustion, so no quota error is built here.
		state.exec.releaseLoopScratch(reserved)
		return
	}
	state.memoReserved = reserved
	state.done = map[arrayComparePair]arrayCompareResult{}
	// Allocated at full size so appending never doubles: a growing slice holds
	// the old and new backings at once, and the reservation covers one ring.
	state.evictionOrder = make([]arrayComparePair, 0, arrayCompareMemoMaxEntries)
}

// memoize records a completed pair, evicting the oldest entry once the memo is
// at its bound so the newest results -- the ones a sibling branch is about to
// ask for -- always survive.
func (state *arrayCompareState) memoize(pair arrayComparePair, result arrayCompareResult) {
	if state.done == nil {
		return
	}
	if _, already := state.done[pair]; already {
		state.done[pair] = result
		return
	}
	if len(state.evictionOrder) < arrayCompareMemoMaxEntries {
		state.evictionOrder = append(state.evictionOrder, pair)
	} else {
		delete(state.done, state.evictionOrder[state.evictionNext])
		state.evictionOrder[state.evictionNext] = pair
		state.evictionNext = (state.evictionNext + 1) % arrayCompareMemoMaxEntries
	}
	state.done[pair] = result
}

// resetMemo drops every cached result while keeping the map and its
// reservation, so a driver that must not carry results across a boundary
// still pays for the memo only once.
//
// min_by and max_by need this: their block runs between comparisons and can
// mutate an array it already returned as a key, which would leave an entry --
// keyed by backing address and length -- describing a value that no longer
// holds.
// withRoots replaces the live roots the memo is admitted against. A driver
// whose operands change between comparisons -- min_by and max_by, whose block
// produces a fresh key each time -- updates them so admission weighs what is
// actually live rather than only the receiver.
func (state *arrayCompareState) withRoots(roots ...Value) {
	if state == nil {
		return
	}
	state.callRoots = roots
}

func (state *arrayCompareState) resetMemo() {
	if state == nil || state.done == nil {
		return
	}
	clear(state.done)
	state.evictionOrder = state.evictionOrder[:0]
	state.evictionNext = 0
}

// release returns the memo's reservation once the comparison that built it is
// finished with it.
func (state *arrayCompareState) release() {
	if state == nil || state.exec == nil {
		return
	}
	state.exec.releaseLoopScratch(state.memoReserved)
	state.memoReserved = 0
}

// arrayCompareResult is a completed comparison, memoized.
type arrayCompareResult struct {
	order   int
	ordered bool
	err     error
}

// step charges the execution for one compared element and honors
// cancellation. A nil execution (host-side comparison outside a script) is
// unmetered, as it is elsewhere.
func (state *arrayCompareState) step() error {
	if state == nil || state.exec == nil {
		return nil
	}
	return state.exec.step()
}

// chargeStringOrder bills the bytes an ordered string pair can read: the
// shorter operand, at the string-scan rate, matching the operator-level
// charge for a top-level `s <=> t`. Ordering reads the common prefix whatever
// the lengths, so unlike equality there is no length-mismatch exemption. A
// nil state or execution (host-side comparison) is unmetered, as step is.
func (state *arrayCompareState) chargeStringOrder(left, right string) error {
	if state == nil || state.exec == nil {
		return nil
	}
	return state.exec.chargeStringScan(min(len(left), len(right)))
}

// compareArrayOrder compares two arrays lexicographically.
//
// ordered is false when some element pair is unordered (a NaN), which makes
// the whole comparison unordered exactly as a bare NaN comparison is. A
// non-nil error means an element pair cannot be ordered at all; callers treat
// it as they treat any incomparable pair, so `[1,2] <=> [1,"a"]` yields nil
// while `sort` reports that the values are not comparable.
//
// A pair of arrays already being compared counts as equal, which terminates
// on cyclic structures and matches Ruby, where comparing two distinct
// self-referential arrays answers 0 rather than exhausting the stack.
func compareArrayOrder(left, right []Value, state *arrayCompareState) (order int, ordered bool, err error) {
	if state == nil {
		state = &arrayCompareState{}
	}
	leftPtr, rightPtr := sliceAddress(left), sliceAddress(right)
	if leftPtr != 0 && leftPtr == rightPtr && len(left) == len(right) {
		return 0, true, nil
	}
	pair := arrayComparePair{leftPtr: leftPtr, rightPtr: rightPtr, leftLen: len(left), rightLen: len(right)}
	tracked := pair.leftPtr != 0 || pair.rightPtr != 0
	if tracked {
		state.ensureMemo()
		if cached, ok := state.done[pair]; ok {
			return cached.order, cached.ordered, cached.err
		}
		if state.onStack == nil {
			state.onStack = map[arrayComparePair]struct{}{}
		}
		if _, onStack := state.onStack[pair]; onStack {
			// A pair already being compared counts as equal, which terminates
			// on cyclic structures and matches Ruby, where two distinct
			// self-referential arrays answer 0.
			state.cycleShortcuts++
			return 0, true, nil
		}
		state.onStack[pair] = struct{}{}
		shortcutsBefore := state.cycleShortcuts
		defer func() {
			delete(state.onStack, pair)
			// Memoize only a result whose whole subtree was computed without
			// the equal-on-cycle shortcut. One that used it depends on the
			// enclosing cycle and would be wrong to reuse elsewhere.
			if state.cycleShortcuts != shortcutsBefore {
				return
			}
			state.memoize(pair, arrayCompareResult{order: order, ordered: ordered, err: err})
		}()
	}
	for i := range min(len(left), len(right)) {
		// One step per compared element, so the walk is bounded by the step
		// quota and observes cancellation.
		if stepErr := state.step(); stepErr != nil {
			return 0, false, stepErr
		}
		order, ordered, err = compareOrderForSort(left[i], right[i], state)
		if err != nil || !ordered {
			return 0, false, err
		}
		if order != 0 {
			return order, true, nil
		}
	}
	// Every compared element was equal, so the shorter array sorts first.
	switch {
	case len(left) < len(right):
		return -1, true, nil
	case len(left) > len(right):
		return 1, true, nil
	default:
		return 0, true, nil
	}
}

// compareSpaceshipOrder orders two values for the <=> operator: arrays
// compare lexicographically, and every other operand pair keeps exactly the
// ordering it has today.
//
// Operand coverage follows Ruby per kind rather than the element coverage
// below: symbols order under both <=> and the relational operators (Symbol
// includes Comparable), nil orders under <=> only, and booleans order under
// neither even though sort accepts them.
func compareSpaceshipOrder(exec *Execution, left, right Value) (order int, ordered bool, err error) {
	if left.Kind() == KindArray && right.Kind() == KindArray {
		state := newArrayCompareState(exec, left, right)
		defer state.release()
		return compareArrayOrder(left.Array(), right.Array(), state)
	}
	// nil answers <=> against itself but has no relational operators, so this
	// case belongs to the spaceship alone: Ruby's `nil <=> nil` is 0 while
	// `nil < nil` raises. Booleans get neither, there as here.
	if left.Kind() == KindNil && right.Kind() == KindNil {
		return 0, true, nil
	}
	return compareValueOrder(left, right)
}

// compareOrderForSort orders two values for the ordering members and for
// elements nested inside a compared array. It is compareValueOrder widened
// with the kinds that
// order but that the relational operators do not accept -- arrays, nil,
// booleans, and symbols -- and it is deliberately not what `<` and friends
// call, so those keep rejecting the same operands they reject today.
//
// Symbols and nil matter here beyond arrays themselves: a hash entry is a
// [symbol, value] pair, so hash.to_a.sort orders symbol-headed pairs, which
// is the case that made array comparison worth having.
func compareOrderForSort(left, right Value, state *arrayCompareState) (order int, ordered bool, err error) {
	switch {
	case left.Kind() == KindArray && right.Kind() == KindArray:
		return compareArrayOrder(left.Array(), right.Array(), state)
	case left.Kind() == KindNil && right.Kind() == KindNil:
		return 0, true, nil
	case left.Kind() == KindBool && right.Kind() == KindBool:
		switch {
		case !left.Bool() && right.Bool():
			return -1, true, nil
		case left.Bool() && !right.Bool():
			return 1, true, nil
		default:
			return 0, true, nil
		}
	case left.Kind() == KindSymbol && right.Kind() == KindSymbol:
		if err := state.chargeStringOrder(left.String(), right.String()); err != nil {
			return 0, false, err
		}
		return compareOrderedStrings(left.String(), right.String()), true, nil
	default:
		// String pairs are the one scalar case whose cost scales with the
		// payload; charging here makes the step quota bound comparisons that
		// reach big strings through an aggregate (#1135). The other scalars
		// fall through to compareValueOrder's constant-time arms unmetered.
		if left.Kind() == KindString && right.Kind() == KindString {
			if err := state.chargeStringOrder(left.String(), right.String()); err != nil {
				return 0, false, err
			}
		}
		return compareValueOrder(left, right)
	}
}

// compareOrderedStrings reports the relative order of two strings as -1, 0,
// or 1.
// compareOrderedStrings orders two strings in a single pass. Comparing with <
// and then > scans their common prefix twice, so the operand charge -- which
// bills the shorter one once -- covered half the work. Shared by symbol
// ordering and array element ordering, so both are one pass.
func compareOrderedStrings(left, right string) int {
	return strings.Compare(left, right)
}

// sliceAddress returns the backing-array address of a slice, or 0 when the
// slice has none, so two arrays can be recognized as the same object.
func sliceAddress(values []Value) uintptr {
	if values == nil {
		return 0
	}
	return reflect.ValueOf(values).Pointer()
}

// sortComparisonError classifies an error from a metered element comparison.
//
// The ordering members replace a comparison failure with their own "values are
// not comparable" message, which is right for genuine incomparability and
// wrong for everything else: now that the comparison charges steps and
// observes cancellation, that replacement would relabel a quota exhaustion as
// an ordinary runtime error and hide a canceled context entirely. Sandbox and
// context errors pass through unchanged so they keep their classification and
// stay outside rescue's reach.
func sortComparisonError(err error, incomparable string) error {
	if err == nil {
		return nil
	}
	if classifyRuntimeErrorType(err) == runtimeErrorTypeLimit || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return errors.New(incomparable)
}
