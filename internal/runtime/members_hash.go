package runtime

import (
	"fmt"
	"reflect"
	"slices"
)

// hashMemberNames mirrors the names dispatched by hashMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var hashMemberNames = []string{
	"size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_with_index", "each_key", "each_value", "to_a",
	"merge", "replace", "store", "delete", "clear", "delete_if", "keep_if", "slice", "except", "flatten", "select", "reject", "map", "map_with_index", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact",
	"inspect",
}

var hashBuiltinMembers = newTypedMemberTable(hashMemberNames, KindHash, KindObject)

// Most script hashes are small records/options; larger maps fall back to heap.
const smallHashKeyBufferSize = 8

func hashMember(obj Value, property string) (Value, error) {
	if _, mutates := hashInPlaceMutators[property]; mutates {
		if err := objectTagMutationError(obj, property); err != nil {
			return NewNil(), err
		}
	}
	if member, ok := hashBuiltinMembers.lookup(property, hashMemberBuiltin); ok {
		return member, nil
	}
	candidates := hashMemberSuggestionCandidates(obj.Hash())
	return NewNil(), fmt.Errorf("unknown hash method %s%s", property, didYouMean(property, candidates))
}

func hashMemberSuggestionCandidates(entries map[string]Value) []string {
	candidates := make([]string, 0, min(len(hashMemberNames)+len(entries), suggestMaxCandidates))
	for _, name := range hashMemberNames {
		if len(candidates) == suggestMaxCandidates {
			return candidates
		}
		candidates = append(candidates, name)
	}
	for key := range entries {
		if len(candidates) == suggestMaxCandidates {
			break
		}
		candidates = append(candidates, key)
	}
	return candidates
}

func hashMemberBuiltin(property string) (Value, error) {
	switch property {
	case "size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_with_index", "each_key", "each_value", "to_a":
		return hashMemberQuery(property)
	case "merge", "replace", "store", "delete", "clear", "delete_if", "keep_if", "slice", "except", "flatten", "select", "reject", "map", "map_with_index", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact":
		return hashMemberTransforms(property)
	case "inspect":
		return newInspectBuiltin("hash"), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

// formatMissingHashKey renders a requested key for "key not found" errors,
// mirroring Ruby's KeyError inspection: symbols render as :name and strings
// render quoted.
func formatMissingHashKey(key Value) string {
	switch key.Kind() {
	case KindSymbol:
		return ":" + key.String()
	default:
		return fmt.Sprintf("%q", key.String())
	}
}

func sortedHashEntryBufferBytes(entryCount int) int {
	if entryCount <= smallHashKeyBufferSize {
		return 0
	}
	return saturatingAdd(estimatedSliceBaseBytes, saturatingMul(entryCount, 2*estimatedValueBytes))
}

// hashFlattenBounded materializes Hash#flatten under sandbox accounting. Ruby's
// Hash#flatten first materializes the receiver's [key, value] pairs and then
// flattens that array to the requested depth; both phases previously ran as
// unmetered native loops, so a small receiver whose values expand into a huge
// flattened output allocated the whole result before the post-call quota check
// could reject it. The pairs pre-build follows hash.to_a's containment: a
// cheap step-budget bound, the entry/key scratch and the pairs backing charged
// before the sort or any pair allocation, and one step plus an accumulator
// charge per appended pair. The flatten phase then mirrors
// arrayFlattenBounded: the pairs slice stays live while the output accumulates
// on top of it, so its backing is folded into the baseline, every element
// examined charges a step (whose slow path runs the periodic memory and
// context checks), and the output backing's doubling is charged before append
// allocates it. Every leaf the output retains was already walked — values
// through the receiver in the baseline, keys and pair structures through the
// per-pair charges — so leaf payloads deduplicate to zero and slot growth is
// the only new memory the flatten phase has to meter.
func hashFlattenBounded(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, depth int) ([]Value, error) {
	count := receiver.HashLen()
	if err := exec.checkStepBudgetFor(count); err != nil {
		return nil, err
	}
	// Blockless build: both the pairs pre-build and the flatten phase charge
	// every allocation through the accumulator before performing it and never
	// re-enter script code, so the whole materialization runs as an
	// accumulator-metered section (see beginAccumulatorMeteredSection).
	defer exec.beginAccumulatorMeteredSection()()
	acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
	if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
		return nil, err
	}
	if err := acc.reserveSlots(count); err != nil {
		return nil, err
	}
	pairs := make([]Value, 0, count)
	appendPair := func(key, value Value) error {
		if err := exec.step(); err != nil {
			return err
		}
		pairs = append(pairs, NewArray([]Value{key, value}))
		return acc.add(pairs[len(pairs)-1], cap(pairs))
	}
	var entryBuf [smallHashKeyBufferSize]HashEntry
	for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
		if err := appendPair(entry.Key, entry.Value); err != nil {
			return nil, err
		}
	}
	// Fold the pairs backing into the baseline: the per-pair charges above
	// projected it through their slotCount argument, but from here the output
	// slice takes that argument over while the pairs stay live until the
	// flatten completes.
	if err := acc.reserveScratch(arraySlotBackingBytes(cap(pairs))); err != nil {
		return nil, err
	}
	if err := acc.reserveSlots(len(pairs)); err != nil {
		return nil, err
	}
	out := make([]Value, 0, len(pairs))
	state := &flattenState{
		arrays: make(map[sliceIdentity]struct{}),
		method: "hash.flatten",
		visit:  exec.step,
		appendLeaf: func(out []Value, v Value) ([]Value, error) {
			if len(out) == cap(out) {
				if err := acc.checkSlotArrays(projectedAppendCap(len(out), cap(out))); err != nil {
					return nil, err
				}
			}
			return append(out, v), nil
		},
	}
	return flattenValuesInto(out, pairs, depth, state)
}

// exclusionSetBytes returns the live heap footprint of a map[string]struct{} set
// holding count entries. Hash#except builds such a set of the candidate keys that
// appear in the receiver and holds it alongside the freshly copied output map, so
// its footprint must be charged before either allocation or a large set could
// allocate past the quota and vanish before the post-call check observed the peak
// (for example h.except(*h.keys), which excludes every key). The set's keys alias
// the receiver's own keys (already counted in the call-root usage), and its values
// are zero-size struct{} with no slot, so only the structural bytes are new: the
// map base plus one bucket and one distinct string header per entry.
func exclusionSetBytes(count int) int {
	if count <= 0 {
		return 0
	}
	return saturatingAdd(estimatedMapBaseBytes, saturatingMul(count, estimatedMapEntryBytes+estimatedStringHeaderBytes))
}

func deepTransformArrayBufferBytes(count int) int {
	return saturatingAdd(arraySlotBackingBytes(0), saturatingMul(count, estimatedValueBytes))
}

func deepTransformKeys(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return deepTransformKeysWithState(exec, receiver, receiver, args, kwargs, block, &deepTransformState{
		seenHashes: make(map[uintptr]struct{}),
		seenArrays: make(map[uintptr]struct{}),
	})
}

// reserveDeepTransformRetainedPayload reserves payloadBytes of loop scratch for
// what deep_transform_keys has retained so far, and checks that reservation
// against the call roots before the next block call runs.
//
// This figure must not be read as the bound on what this path retains. It comes
// from the build accumulator's running total, which sums each result's payload as
// that result is produced and is never re-derived, so it cannot see a retained
// value that grows afterwards -- and the block is arbitrary script code that can
// grow one. Measured: a block appending to a key it produced on an earlier call
// left this figure reading 931 bytes while the result hash held 7,000,000. That
// is four orders of magnitude, not a rounding error.
//
// The reservation is left in place because no bypass follows from the wrong
// figure today, not because the figure is right. Three shapes were built to turn
// the under-pricing into a quota escape, and each was caught by a charge that has
// nothing to do with this function:
//
//   - Grow a retained key, then drop the script's reference to it. The append is
//     charged while the array is still reachable from the block's scope, so the
//     peak is caught there. The admitting quota tracked the accumulation exactly,
//     7,010,520 bytes against 7,000,000 retained.
//   - Hand the result pre-built arrays and clear the pool afterwards. The pool
//     stays reachable for the whole loop, so every walk already sees the payload.
//   - Allocate the probe INSIDE a later block call, so that no charged allocation
//     observes the accumulation at full size. The result hash turns out to be
//     visible to the block's own checks regardless: dropping the pool reference
//     moved the admitting quota by -135,954 bytes, the wrong sign for a result
//     the checks cannot see.
//
// Every one of those covers is incidental to this code. A change to when an
// append is charged, or to how long a block scope keeps a value reachable, removes
// one of them without touching this function, and the under-pricing becomes a live
// bypass at that moment. Anyone changing either should treat this path as
// unprotected and derive the bound afresh rather than trusting this number.
//
// No behavioral test depends on this reservation: neutering it leaves the whole
// runtime suite green apart from the unit test of this helper's own mechanics.
func reserveDeepTransformRetainedPayload(exec *Execution, payloadBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) (int, error) {
	if payloadBytes <= 0 {
		return 0, nil
	}
	delta := exec.reserveLoopScratch(payloadBytes)
	if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
		exec.releaseLoopScratch(delta)
		return 0, err
	}
	return delta, nil
}

type deepTransformState struct {
	seenHashes map[uintptr]struct{}
	seenArrays map[uintptr]struct{}
	depth      int
}

func deepTransformKeysWithState(exec *Execution, value, receiver Value, args []Value, kwargs map[string]Value, block Value, state *deepTransformState) (Value, error) {
	if err := exec.step(); err != nil {
		return NewNil(), err
	}
	if state.depth >= maxJSONNestingDepth {
		return NewNil(), guardLimitErrorf("hash.deep_transform_keys nesting exceeds limit %d", maxJSONNestingDepth)
	}
	state.depth++
	defer func() { state.depth-- }()

	switch value.Kind() {
	case KindHash, KindObject:
		id := hashScanIdentity(value)
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}
		count := value.HashLen()
		scratchBytes := sortedHashEntryBufferBytes(count)
		delta := exec.reserveLoopScratch(hashTransformBufferBytes(count, scratchBytes))
		defer exec.releaseLoopScratch(delta)
		if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
		out := NewHashWithCapacity(count)
		var blockArg [1]Value
		var entryBuf [smallHashKeyBufferSize]HashEntry
		for _, entry := range value.HashEntriesInto(entryBuf[:]) {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			prefixDelta, err := reserveDeepTransformRetainedPayload(exec, acc.retainedPayloadBytes(), receiver, args, kwargs, block)
			if err != nil {
				return NewNil(), err
			}
			blockArg[0] = entry.Key
			nextKeyValue, err := exec.CallBlock(block, blockArg[:])
			if err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), err
			}
			if err := exec.checkContext(); err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), err
			}
			if err := exec.chargeValueKeySteps(nextKeyValue); err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), err
			}
			nextKey, err := hashKeyString(nextKeyValue)
			if err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), fmt.Errorf("hash.deep_transform_keys block returned an unsupported hash key: %w", err)
			}
			keyDelta, err := reserveDeepTransformRetainedPayload(exec, len(nextKey), receiver, args, kwargs, block)
			if err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), err
			}
			nextValue, err := deepTransformKeysWithState(exec, entry.Value, receiver, args, kwargs, block, state)
			exec.releaseLoopScratch(keyDelta)
			exec.releaseLoopScratch(prefixDelta)
			if err != nil {
				return NewNil(), err
			}
			if err := hashSet(out, nextKeyValue, nextValue); err != nil {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys block returned an unsupported hash key: %w", err)
			}
			if err := acc.addSynthesizedKey(nextKey); err != nil {
				return NewNil(), err
			}
			if err := acc.addBaselineDeduped(nextValue); err != nil {
				return NewNil(), err
			}
		}
		return out, nil
	case KindArray:
		items := value.Array()
		id := reflect.ValueOf(items).Pointer()
		if id != 0 {
			if _, seen := state.seenArrays[id]; seen {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
			}
			state.seenArrays[id] = struct{}{}
			defer delete(state.seenArrays, id)
		}
		delta := exec.reserveLoopScratch(deepTransformArrayBufferBytes(len(items)))
		defer exec.releaseLoopScratch(delta)
		if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
		out := make([]Value, 0, len(items))
		for _, item := range items {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			retainedDelta, err := reserveDeepTransformRetainedPayload(exec, acc.retainedPayloadBytes(), receiver, args, kwargs, block)
			if err != nil {
				return NewNil(), err
			}
			nextValue, err := deepTransformKeysWithState(exec, item, receiver, args, kwargs, block, state)
			exec.releaseLoopScratch(retainedDelta)
			if err != nil {
				return NewNil(), err
			}
			out = append(out, nextValue)
			// deep_transform_keys carries leaf values through unchanged; use the
			// baseline-deduped accumulator path rather than the conservative
			// block-result path.
			if err := acc.addToReservedBacking(nextValue); err != nil {
				return NewNil(), err
			}
		}
		return NewArray(out), nil
	default:
		return value, nil
	}
}

func hashMemberQuery(property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("hash.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.size does not take arguments")
			}
			return NewInt(int64(receiver.HashLen())), nil
		}), nil
	case "length":
		return NewAutoBuiltin("hash.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.length does not take arguments")
			}
			return NewInt(int64(receiver.HashLen())), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("hash.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.empty? does not take arguments")
			}
			return NewBool(receiver.HashLen() == 0), nil
		}), nil
	case "key?", "has_key?", "member?", "include?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one key", name)
			}
			if err := exec.chargeValueKeySteps(args[0]); err != nil {
				return NewNil(), err
			}
			_, ok, err := hashGet(receiver, args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.%s key is an unsupported hash key: %w", name, err)
			}
			return NewBool(ok), nil
		}), nil
	case "value?", "has_value?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one value", name)
			}
			// Ruby compares the candidate against each stored value with ==.
			// Vibescript mirrors this with metered equality so deep collection
			// and scalar equality match Ruby's hash value membership semantics
			// while each probe charges a step and its string bytes — the scan
			// used to be free, so the quota never bounded it (#1135).
			equality := exec.meteredEquality()
			count := receiver.HashLen()
			delta := exec.reserveLoopScratch(sortedHashEntryBufferBytes(count))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkMemory(); err != nil {
				return NewNil(), err
			}
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if equality.Equal(entry.Value, args[0]) {
					return NewBool(true), nil
				}
				if err := equality.Err(); err != nil {
					return NewNil(), err
				}
			}
			return NewBool(false), nil
		}), nil
	case "keys":
		return NewAutoBuiltin("hash.keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.keys does not take arguments")
			}
			count := receiver.HashLen()
			if err := exec.checkStepBudgetFor(count); err != nil {
				return NewNil(), err
			}
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(count); err != nil {
				return NewNil(), err
			}
			var entryBuf [smallHashKeyBufferSize]HashEntry
			entries := receiver.HashEntriesInto(entryBuf[:])
			values := make([]Value, 0, len(entries))
			for _, entry := range entries {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				values = append(values, entry.Key)
				if err := acc.add(values[len(values)-1], cap(values)); err != nil {
					return NewNil(), err
				}
			}
			return NewArray(values), nil
		}), nil
	case "values":
		return NewAutoBuiltin("hash.values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.values does not take arguments")
			}
			count := receiver.HashLen()
			if err := exec.checkStepBudgetFor(count); err != nil {
				return NewNil(), err
			}
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(count); err != nil {
				return NewNil(), err
			}
			var entryBuf [smallHashKeyBufferSize]HashEntry
			entries := receiver.HashEntriesInto(entryBuf[:])
			values := make([]Value, 0, len(entries))
			for _, entry := range entries {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				values = append(values, entry.Value)
				if err := acc.add(values[len(values)-1], cap(values)); err != nil {
					return NewNil(), err
				}
			}
			return NewArray(values), nil
		}), nil
	case "values_at":
		return NewAutoBuiltin("hash.values_at", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.values_at does not accept keyword arguments")
			}
			// The result slots alias receiver values, so the backing is the only
			// fresh allocation; a missing key contributes nil, as in Ruby for a
			// hash without a default.
			backing := exec.reserveLoopScratch(arraySlotBackingBytes(len(args)))
			defer exec.releaseLoopScratch(backing)
			if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make([]Value, len(args))
			for i, arg := range args {
				if err := exec.chargeValueKeySteps(arg); err != nil {
					return NewNil(), err
				}
				value, ok, err := hashGet(receiver, arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.values_at key is an unsupported hash key: %w", err)
				}
				if ok {
					out[i] = value
				}
			}
			return NewArray(out), nil
		}), nil
	case "fetch":
		return NewAutoBuiltin("hash.fetch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			hasBlock := valueBlock(block) != nil
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("hash.fetch expects key and optional default")
			}
			if err := exec.chargeValueKeySteps(args[0]); err != nil {
				return NewNil(), err
			}
			value, ok, err := hashGet(receiver, args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.fetch key is an unsupported hash key: %w", err)
			}
			if ok {
				return value, nil
			}
			// A block supersedes a default value argument, matching Ruby's
			// Hash#fetch: when both are supplied the block is invoked on a
			// miss and the default argument is ignored.
			if hasBlock {
				blockArg := [1]Value{args[0]}
				return exec.CallBlock(block, blockArg[:])
			}
			if len(args) == 2 {
				return args[1], nil
			}
			return NewNil(), fmt.Errorf("hash.fetch key not found: %s", formatMissingHashKey(args[0]))
		}), nil
	case "fetch_values":
		return NewAutoBuiltin("hash.fetch_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (result Value, err error) {
			// The block runs once per missing key and every result stays in a
			// Go-local slice the checks inside the next block call cannot reach,
			// so a block returning an individually permitted value passed its own
			// check each time while the slice still held all the earlier ones.
			// Reserving the slots and registering the slice as a walk root puts
			// the whole retained output in every one of those checks, re-derived
			// as the block leaves it (see memory_output.go).
			backing := exec.reserveLoopScratch(arraySlotBackingBytes(len(args)))
			defer exec.releaseLoopScratch(backing)
			// The root walks the slots filled so far, not the whole preallocated
			// slice: a lookup with many keys would otherwise pay for its entire
			// output on the very first miss, before it has produced anything. The
			// backing itself is reserved above, where its size is known.
			out := make([]Value, len(args))
			var produced []Value
			exec.pushOutputWalkRoot(retainedValuesWithReceiver(receiver, &produced))
			// Settled on the way out rather than only after a successful block
			// call, so a block that mutates state and then raises pays what one
			// that returns pays, and leaves nothing for a later lookup to be
			// billed for (see memory_output.go).
			defer func() { err = exec.endOutputWalkRoot(err) }()
			// The block is driven through a runner inside a block-iteration region
			// so the base walk stays memoized across misses; see hash.values_at for
			// the measurement.
			//
			// The runner is built on the first miss rather than before the loop,
			// the way values_at builds its default proc's. Building one constructs
			// a blockBindCharge, whose baseline walks the whole reachable graph and
			// every registered output root, and a lookup whose keys are all present
			// never calls the block at all -- which is the ordinary use of
			// fetch_values, so the cost landed on the common path. Over a
			// 20,000-element reachable graph an all-present lookup cost 120,081
			// estimator visits with a rest-binding block against 100,064 with a
			// plain one, and an argumentless call cost 100,040 against 80,029; the
			// two spellings cost the same again now, as they do on master.
			hasBlock := valueBlock(block) != nil
			var runner *blockCallRunner
			defer exec.beginBlockIterationRegion().end()
			for i, arg := range args {
				if err := exec.chargeValueKeySteps(arg); err != nil {
					return NewNil(), err
				}
				value, ok, err := hashGet(receiver, arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.fetch_values key is an unsupported hash key: %w", err)
				}
				if ok {
					// Charged as retained output even though it aliases the
					// receiver: a later block call can delete the entry or clear
					// the hash, and then nothing reachable holds the payload while
					// this slot still does. The estimator deduplicates it against
					// the receiver for as long as the alias lasts.
					out[i] = value
					produced = out[:i+1]
					exec.addRetainedOutput(value)
					continue
				}
				if !hasBlock {
					return NewNil(), fmt.Errorf("hash.fetch_values key not found: %s", formatMissingHashKey(arg))
				}
				if runner == nil {
					built, buildErr := newBlockCallRunner(exec, block, "hash.fetch_values", receiver, nil, kwargs)
					if buildErr != nil {
						return NewNil(), buildErr
					}
					// A block that grows a reachable container leaves this
					// charge's baseline measuring a graph that no longer exists,
					// and a later key destructured with a named rest is weighed
					// against it (see refreshChargeOnMutation).
					built.refreshChargeOnMutation()
					runner = built
				}
				blockArg := [1]Value{arg}
				// The key is charged as a per-call root, not left to the runner's
				// one-time baseline: it lives only in the builtin's Go argument
				// slice, and a block destructuring it with a named rest copies a
				// window sized to it. Weighing that window against a baseline the
				// key is missing from let an ephemeral array key's copy be
				// allocated before anything accounted for the key itself.
				blockValue, err := runner.callRetainedWithChargedRoots(blockArg[:], arg)
				if err != nil {
					return NewNil(), err
				}
				out[i] = blockValue
				produced = out[:i+1]
				exec.addRetainedOutput(blockValue)
			}
			return adoptArray(out), nil
		}), nil
	case "dig":
		return NewAutoBuiltin("hash.dig", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("hash.dig expects at least one key")
			}
			return exec.digPath("hash.dig", receiver, args)
		}), nil
	case "each":
		return NewAutoBuiltin("hash.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each does not take arguments")
			}
			if err := ensureBlock(block, "hash.each"); err != nil {
				return NewNil(), err
			}
			count := receiver.HashLen()
			collapsePair := blockWantsCollapsedPair(valueBlock(block))
			scratch := sortedHashEntryBufferBytes(count)
			reservePair := collapsePair && (blockBindsRest(valueBlock(block)) || !exec.valueReachableFromLiveBase(receiver, block))
			if reservePair {
				scratch = saturatingAdd(scratch, exec.maxCollapsedPairBytes(receiver))
			}
			delta := exec.reserveLoopScratch(scratch)
			defer exec.releaseLoopScratch(delta)
			if collapsePair && !reservePair {
				if err := exec.checkCollapsedPairBytesWithLiveBase(receiver, block); err != nil {
					return NewNil(), err
				}
			}
			runner, err := newBlockCallRunner(exec, block, "hash.each", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			var blockArgs [2]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if collapsePair {
					pair := NewArray([]Value{entry.Key, entry.Value})
					if _, err := runner.call([]Value{pair}); err != nil {
						return NewNil(), err
					}
					if err := exec.checkContext(); err != nil {
						return NewNil(), err
					}
					continue
				}
				blockArgs[0] = entry.Key
				blockArgs[1] = entry.Value
				if _, err := runner.call(blockArgs[:]); err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_with_index":
		return NewAutoBuiltin("hash.each_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.each_with_index does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.each_with_index", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			count := receiver.HashLen()
			delta := exec.reserveLoopScratch(sortedHashEntryBufferBytes(count))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			var blockArgs [2]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for i, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				pair := NewArray([]Value{entry.Key, entry.Value})
				if err := exec.checkMemoryValue(pair); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = pair
				blockArgs[1] = NewInt(int64(i))
				if _, err := runner.call(blockArgs[:]); err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_key":
		return NewAutoBuiltin("hash.each_key", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_key does not take arguments")
			}
			if err := ensureBlock(block, "hash.each_key"); err != nil {
				return NewNil(), err
			}
			count := receiver.HashLen()
			delta := exec.reserveLoopScratch(sortedHashEntryBufferBytes(count))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.each_key", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = entry.Key
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_value":
		return NewAutoBuiltin("hash.each_value", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_value does not take arguments")
			}
			if err := ensureBlock(block, "hash.each_value"); err != nil {
				return NewNil(), err
			}
			count := receiver.HashLen()
			delta := exec.reserveLoopScratch(sortedHashEntryBufferBytes(count))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.each_value", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = entry.Value
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "to_a":
		return NewAutoBuiltin("hash.to_a", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.to_a does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.to_a does not take keyword arguments")
			}
			// Blockless build: both branches below charge every allocation
			// through the accumulator before performing it and never re-enter
			// script code, so the materialization runs as an
			// accumulator-metered section (see beginAccumulatorMeteredSection).
			defer exec.beginAccumulatorMeteredSection()()
			count := receiver.HashLen()
			if err := exec.checkStepBudgetFor(count); err != nil {
				return NewNil(), err
			}
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(count); err != nil {
				return NewNil(), err
			}
			var entryBuf [smallHashKeyBufferSize]HashEntry
			entries := receiver.HashEntriesInto(entryBuf[:])
			pairs := make([]Value, 0, len(entries))
			for _, entry := range entries {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				pairs = append(pairs, NewArray([]Value{entry.Key, entry.Value}))
				if err := acc.add(pairs[len(pairs)-1], cap(pairs)); err != nil {
					return NewNil(), err
				}
			}
			return NewArray(pairs), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

// mergedKeyCount returns the number of distinct keys a merge of base and args
// would hold, stopping early once the running total passes limit. The merged
// hash is the union of the receiver's keys and every argument's keys, so
// overlapping keys (h.merge(h), or the same defaults applied repeatedly) collapse
// to one entry. Counting the union lets the projected memory check size the real
// output map instead of summing every input length, which would over-count an
// overlapping merge and reject one that fits the quota.
//
// limit is the largest output the quota can admit once the merge's scratch
// budget is reserved (maxProjectedHashEntries, passed the same scratchBytes the
// final projection charges). A single argument needs no cross-argument
// deduplication, so its union is counted against base alone with no tracking set.
// Multiple arguments require a seen set to collapse keys repeated across
// arguments, but the set is bounded by limit: once the distinct-key total exceeds
// limit the merge is certain to be rejected, so counting stops and returns
// limit+1 rather than growing a tracking table sized to the over-quota result.
// Because limit already accounts for the scratch bytes, the seen set never grows
// past what the projection's real byte budget permits.
//
// Every key examined charges a step via exec.step, so the union count itself is
// CPU-bounded by the step quota and observes cancellation. Without this a large
// overlapping merge under a tight memory quota (h.merge(h)) could scan O(n) keys
// here, between the loose projection failing and the exact projection running,
// while the step quota was already exhausted or the context already canceled.
// When step returns an error the walk stops and propagates it: the merge is
// abandoned for the same quota or cancellation reason that would have stopped the
// merge loop itself. All inputs walked here are already resident in memory, so the
// step charge guards CPU, not allocation.
func mergedKeyCount(exec *Execution, base map[string]Value, args []Value, limit int) (int, error) {
	count := len(base)
	if count > limit {
		return count, nil
	}
	if len(args) <= 1 {
		// One argument (or none): every argument key is distinct on its own, so
		// the union is base plus the argument keys absent from base, countable
		// without a tracking set.
		for _, arg := range args {
			for key := range arg.Hash() {
				if err := exec.step(); err != nil {
					return count, err
				}
				// The base probe hashes the whole key text.
				if err := exec.chargeStringScan(len(key)); err != nil {
					return count, err
				}
				if _, inBase := base[key]; inBase {
					continue
				}
				count++
				if count > limit {
					return count, nil
				}
			}
		}
		return count, nil
	}
	var seen map[string]struct{}
	for _, arg := range args {
		for key := range arg.Hash() {
			if err := exec.step(); err != nil {
				return count, err
			}
			if err := exec.chargeStringScan(len(key)); err != nil {
				return count, err
			}
			if _, inBase := base[key]; inBase {
				continue
			}
			if seen == nil {
				seen = make(map[string]struct{})
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			count++
			if count > limit {
				// The merge already exceeds the quota's entry budget, so it will
				// be rejected regardless of further keys. Stop before the tracking
				// set grows past the admissible result size.
				return count, nil
			}
		}
	}
	return count, nil
}

// mergedKeyCount returns the number of distinct keys produced by merging a
// receiver with args, so the projection does not over-count every overlapping
// key the way the loose receiver+sum(arg lens) bound does. It caps its tracking
// set at limit (the quota's entry budget) so a doomed merge cannot allocate a
// large set before being rejected.
func mergedHashKeyCount(exec *Execution, receiver Value, args []Value, limit int) (int, error) {
	count := receiver.HashLen()
	if count > limit {
		return count, nil
	}
	seen := make(map[string]struct{}, count)
	for _, entry := range receiver.HashEntries() {
		if err := exec.step(); err != nil {
			return count, err
		}
		// The exact-union preflight reads every key; charge before the copy,
		// like every other key site.
		if err := exec.chargeValueKeySteps(entry.Key); err != nil {
			return count, err
		}
		key, err := hashKeyString(entry.Key)
		if err != nil {
			return count, err
		}
		seen[key] = struct{}{}
	}
	for _, arg := range args {
		for _, entry := range arg.HashEntries() {
			if err := exec.step(); err != nil {
				return count, err
			}
			if err := exec.chargeValueKeySteps(entry.Key); err != nil {
				return count, err
			}
			key, err := hashKeyString(entry.Key)
			if err != nil {
				return count, err
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			count++
			if count > limit {
				// Already past the quota's entry budget; stop before the tracking
				// set grows beyond the admissible result size.
				return count, nil
			}
		}
	}
	return count, nil
}

// hashFilterByBlock implements Ruby's Hash#delete_if and Hash#keep_if: the
// block visits a snapshot of the entries, the entries it condemns are removed
// from the receiver in place only after the walk completes (so the block never
// observes a half-filtered receiver), and the receiver is returned. Surviving
// entries keep their recorded insertion order.
func hashFilterByBlock(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, method string, keepTruthy bool) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("%s does not take arguments", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", method)
	}
	count := receiver.HashLen()
	delta := exec.reserveLoopScratch(hashTransformBufferBytes(count, sortedHashEntryBufferBytes(count)))
	defer exec.releaseLoopScratch(delta)
	runner, err := newBlockCallRunner(exec, block, method, receiver, nil, kwargs)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}
	var dropped []Value
	var blockArgs [2]Value
	var entryBuf [smallHashKeyBufferSize]HashEntry
	for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		blockArgs[0] = entry.Key
		blockArgs[1] = entry.Value
		include, err := runner.call(blockArgs[:])
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkContext(); err != nil {
			return NewNil(), err
		}
		if include.Truthy() != keepTruthy {
			dropped = append(dropped, entry.Key)
		}
	}
	// Writability is checked here rather than before the loop: the block is
	// script code and can bind the receiver somewhere new while it runs.
	receiver, err = exec.writableCollection(receiver)
	if err != nil {
		return NewNil(), err
	}
	for _, key := range dropped {
		if _, _, err := hashDeleteKey(receiver, key); err != nil {
			return NewNil(), err
		}
	}
	return receiver, nil
}

func hashMemberTransforms(property string) (Value, error) {
	switch property {
	case "merge":
		// merge is non-mutating in Ruby too: it returns a new merged hash and
		// leaves the receiver unchanged. The mutating aliases update/merge!
		// fold into the receiver in place; see hashMergeInPlace below.
		name := property
		// AutoBuiltin so a parenless `hash.merge` invokes with zero arguments and
		// returns a copy of the receiver, matching Ruby where the call has no
		// parentheses distinction. Ruby's no-argument Hash#merge returns a copy of
		// self, which the len(args) == 0 branch below handles for both the bare and
		// explicit `merge()` forms. Explicit `merge(...)` calls still pass their
		// hash arguments through the normal call path.
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// Reject keyword arguments rather than silently dropping them. Ruby
			// folds trailing keywords into an implicit hash argument, but
			// Vibescript's native hash helpers only consume positional hashes, so
			// keywords must be passed explicitly (e.g. merge({ b: 2 })).
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.%s does not accept keyword arguments", name)
			}
			for i, arg := range args {
				if arg.Kind() != KindHash && arg.Kind() != KindObject {
					return NewNil(), fmt.Errorf("hash.%s argument %d must be a hash", name, i+1)
				}
			}
			// A block only resolves conflicts, which require at least one argument
			// hash. With zero arguments the merge short-circuits to a plain copy
			// below and never runs the block or sorts the base, so the conflict
			// block's base scratch buffer is never allocated. Gate useBlock on having
			// arguments so the projection does not charge that phantom scratch and
			// reject a large receiver whose copy fits but whose unused base scratch
			// would not.
			useBlock := valueBlock(block) != nil && len(args) > 0
			looseEntries := receiver.HashLen()
			maxArgLen := 0
			for _, arg := range args {
				argLen := arg.HashLen()
				looseEntries = saturatingAdd(looseEntries, argLen)
				if argLen > maxArgLen {
					maxArgLen = argLen
				}
			}
			scratchBytes := sortedHashEntryBufferBytes(max(receiver.HashLen(), maxArgLen))
			// looseEntries counts every overlapping key separately. Like the
			// legacy path, fall back to the exact distinct union when that bound
			// matters so an overlapping merge (e.g. h.merge(h), or an argument that
			// only updates existing keys) whose real output fits is not rejected by
			// phantom slots, and the reserved backing matches the map the build
			// actually holds.
			projectedEntries := looseEntries
			switch {
			case useBlock && exec.memoryQuota > 0:
				// The block accumulator reserves projectedEntries below, so the
				// loose bound's phantom slots would falsely reject a merge whose
				// true union plus block results fit. Compute the exact union up
				// front so the projection and the reservation agree.
				limit := exec.maxProjectedHashEntries(scratchBytes, receiver, args, kwargs, block)
				projected, err := mergedHashKeyCount(exec, receiver, args, limit)
				if err != nil {
					return NewNil(), err
				}
				projectedEntries = projected
			default:
				// No block reservation lingers, so the loose bound is only an
				// up-front admission check. Try it first (non-allocating); only
				// when it exceeds the quota does overlap matter, so compute the
				// exact union (capped at the entry budget) before rejecting.
				if !exec.projectedHashTransformFits(looseEntries, scratchBytes, receiver, args, kwargs, block) {
					limit := exec.maxProjectedHashEntries(scratchBytes, receiver, args, kwargs, block)
					projected, err := mergedHashKeyCount(exec, receiver, args, limit)
					if err != nil {
						return NewNil(), err
					}
					projectedEntries = projected
				}
			}
			if err := exec.checkProjectedHashTransformBytes(projectedEntries, scratchBytes, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHashWithCapacity(projectedEntries)
			var runner *blockCallRunner
			var acc *hashBuildAccumulator
			if useBlock {
				delta := exec.reserveLoopScratch(hashTransformBufferBytes(projectedEntries, scratchBytes))
				defer exec.releaseLoopScratch(delta)
				r, err := newBlockCallRunner(exec, block, "hash."+name, receiver, args, kwargs)
				if err != nil {
					return NewNil(), err
				}
				runner = r
				acc = newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			}
			// A bare receiver merged under the typed path (an argument carries
			// typed entries) has no insertion record, so copy it in sorted key
			// order to keep the merged result deterministic.
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// The copy canonicalizes each receiver key into the output
				// hash; charge it exactly as the argument-entry loop does.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if err := hashSet(out, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			if len(args) == 0 {
				return out, nil
			}
			var blockArgs [3]Value
			for _, arg := range args {
				// A bare argument has no insertion record, so walk it in sorted
				// key order too; inserting it in raw Go-map order would leave the
				// merged result nondeterministic.
				for _, entry := range arg.HashEntriesInto(entryBuf[:]) {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					if err := exec.chargeValueKeySteps(entry.Key); err != nil {
						return NewNil(), err
					}
					oldValue, conflict, err := hashGet(out, entry.Key)
					if err != nil {
						return NewNil(), err
					}
					if !conflict || !useBlock {
						if err := hashSet(out, entry.Key, entry.Value); err != nil {
							return NewNil(), err
						}
						continue
					}
					blockArgs[0] = entry.Key
					blockArgs[1] = oldValue
					blockArgs[2] = entry.Value
					resolved, err := runner.call(blockArgs[:])
					if err != nil {
						return NewNil(), err
					}
					if err := exec.checkContext(); err != nil {
						return NewNil(), err
					}
					if err := hashSet(out, entry.Key, resolved); err != nil {
						return NewNil(), err
					}
					if err := acc.add(resolved); err != nil {
						return NewNil(), err
					}
				}
			}
			return out, nil
		}), nil
	case "replace":
		return NewBuiltin("hash.replace", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// Reject keyword arguments rather than silently dropping them; the
			// replacement hash must be passed positionally (e.g. replace({ b: 2 })).
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.replace does not accept keyword arguments")
			}
			if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
				return NewNil(), fmt.Errorf("hash.replace expects a single hash argument")
			}
			// Ruby's Hash#replace discards the receiver's contents and adopts the
			// argument's entries (and default) in place, returning the receiver.
			// Preflight the adopted entries before the receiver grows, plus the
			// sorted-entry scratch a bare replacement's deterministic walk may
			// allocate.
			count := args[0].HashLen()
			if err := exec.checkProjectedHashTransformBytes(count, sortedHashEntryBufferBytes(count), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// Poll the step/cancellation budget before the first mutation so an
			// already-aborted execution never wipes the receiver.
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			// Snapshot the replacement's entries before clearing so h.replace(h)
			// is a harmless no-op rather than wiping the entries it is about to
			// copy.
			var entryBuf [smallHashKeyBufferSize]HashEntry
			entries := args[0].HashEntriesInto(entryBuf[:])
			receiver, err := exec.writableCollection(receiver)
			if err != nil {
				return NewNil(), err
			}
			hashClearEntries(receiver)
			// Pre-size the entry map and order backing to the adopted entry
			// count so the rebuilt receiver holds exactly the slots the
			// projection charged, with no append-growth overshoot.
			receiver.ReserveHashCapacity(len(entries))
			for _, entry := range entries {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// Adopting an entry hashes its key; bill the payload before
				// the walk.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if err := hashSet(receiver, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "flatten":
		return NewAutoBuiltin("hash.flatten", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.flatten does not accept keyword arguments")
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("hash.flatten accepts at most one depth argument")
			}
			// Ruby's Hash#flatten builds the [[key, value], ...] pairs and then
			// flattens that array to the given depth (default 1, so the pairs are
			// spread into a flat [key, value, ...] list). A depth of 0 keeps the
			// pairs nested, and a negative depth flattens completely. valueToInt
			// truncates a Float depth, matching Ruby.
			depth := 1
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil {
					return NewNil(), fmt.Errorf("hash.flatten depth must be integer")
				}
				depth = n
			}
			out, err := hashFlattenBounded(exec, receiver, args, kwargs, block, depth)
			if err != nil {
				return NewNil(), err
			}
			return NewArray(out), nil
		}), nil
	case "store":
		return NewBuiltin("hash.store", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.store does not accept keyword arguments")
			}
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("hash.store expects a key and a value")
			}
			if err := exec.chargeValueKeySteps(args[0]); err != nil {
				return NewNil(), err
			}
			if _, err := hashKeyString(args[0]); err != nil {
				return NewNil(), fmt.Errorf("hash.store key is an unsupported hash key: %w", err)
			}
			// Ruby's Hash#store is index assignment: it writes the entry into
			// the receiver in place and returns the stored value. HashSet keeps
			// an existing key at its recorded position, preserving Ruby's
			// position-preserving store. Charge the one slot the receiver may
			// grow by before it takes it.
			if err := exec.checkProjectedHashBytes(1, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			stored, err := exec.detachStoredCollection(args[1])
			if err != nil {
				return NewNil(), err
			}
			receiver, err = exec.writableCollection(receiver)
			if err != nil {
				return NewNil(), err
			}
			if err := hashSet(receiver, args[0], stored); err != nil {
				return NewNil(), fmt.Errorf("hash.store key is an unsupported hash key: %w", err)
			}
			return stored, nil
		}), nil
	case "delete":
		return NewBuiltin("hash.delete", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.delete does not accept keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.delete expects a key")
			}
			if err := exec.chargeValueKeySteps(args[0]); err != nil {
				return NewNil(), err
			}
			if _, err := hashKeyString(args[0]); err != nil {
				return NewNil(), fmt.Errorf("hash.delete key is an unsupported hash key: %w", err)
			}
			// Ruby's Hash#delete removes the entry from the receiver in place
			// and returns the removed value. The removal keeps the surviving
			// entries in their recorded insertion order and allocates nothing.
			receiver, err := exec.writableCollection(receiver)
			if err != nil {
				return NewNil(), err
			}
			removed, existed, err := hashDeleteKey(receiver, args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.delete key is an unsupported hash key: %w", err)
			}
			if existed {
				return removed, nil
			}
			// On a miss Ruby returns nil, or the result of a block invoked with
			// the requested key, matching `h.delete(key) { |k| default }`. The
			// receiver is left untouched.
			if valueBlock(block) != nil {
				return exec.CallBlock(block, []Value{args[0]})
			}
			return NewNil(), nil
		}), nil
	case "clear":
		return NewAutoBuiltin("hash.clear", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.clear does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.clear does not take keyword arguments")
			}
			if valueBlock(block) != nil {
				return NewNil(), fmt.Errorf("hash.clear does not accept a block")
			}
			// Ruby's Hash#clear empties the receiver and returns it.
			return exec.writeHashClear(receiver)
		}), nil
	case "delete_if", "keep_if":
		name := "hash." + property
		keepTruthy := property == "keep_if"
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return hashFilterByBlock(exec, receiver, args, kwargs, block, name, keepTruthy)
		}), nil
	case "slice":
		// AutoBuiltin so a parenless `hash.slice` invokes with zero arguments
		// and returns an empty hash, matching Ruby where the call has no
		// parentheses distinction. Explicit `slice(...)` calls still pass
		// their candidate keys through the normal call path.
		return NewAutoBuiltin("hash.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			projected := min(len(args), receiver.HashLen())
			if err := exec.checkProjectedHashBytes(projected, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHashWithCapacity(projected)
			for _, arg := range args {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if err := exec.chargeValueKeySteps(arg); err != nil {
					return NewNil(), err
				}
				value, ok, err := hashGet(receiver, arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.slice key is an unsupported hash key: %w", err)
				}
				if !ok {
					continue
				}
				if err := hashSet(out, arg, value); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}), nil
	case "except":
		// AutoBuiltin so a parenless `hash.except` invokes with zero arguments
		// and returns a copy of the receiver, matching Ruby where the call has
		// no parentheses distinction. Explicit `except(...)` calls still pass
		// their excluded keys through the normal call path.
		return NewAutoBuiltin("hash.except", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			count := receiver.HashLen()
			exclusionEntries := min(len(args), count)
			scratch := saturatingAdd(exclusionSetBytes(exclusionEntries), sortedHashEntryBufferBytes(count))
			if err := exec.checkProjectedHashTransformBytes(count, scratch, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			var excluded map[string]struct{}
			for _, arg := range args {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if err := exec.chargeValueKeySteps(arg); err != nil {
					return NewNil(), err
				}
				key, err := hashKeyString(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.except key is an unsupported hash key: %w", err)
				}
				if _, ok, _ := hashGet(receiver, arg); !ok {
					continue
				}
				if excluded == nil {
					excluded = make(map[string]struct{}, exclusionEntries)
				}
				excluded[key] = struct{}{}
			}
			out := NewHashWithCapacity(count)
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// The exclusion probe and the retained copy both hash the
				// receiver key; bill its payload before the first probe.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if _, skip := excluded[entry.Key.String()]; skip {
					continue
				}
				if err := hashSet(out, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}), nil
	case "select":
		return NewAutoBuiltin("hash.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.select does not take arguments")
			}
			count := receiver.HashLen()
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(count, sortedHashEntryBufferBytes(count)))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.select", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHashWithCapacity(count)
			var blockArgs [2]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			// Compact the kept entries to the front of the buffer and populate the
			// result hash after the loop; see hash.transform_values for why the
			// in-loop hashSet had to go. The write index never runs ahead of the
			// read index, so the compaction is safe in place and adds no
			// allocation.
			ordered := receiver.HashEntriesInto(entryBuf[:])
			kept := 0
			for i := range ordered {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = ordered[i].Key
				blockArgs[1] = ordered[i].Value
				include, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				if include.Truthy() {
					ordered[kept] = ordered[i]
					kept++
				}
			}
			for _, entry := range ordered[:kept] {
				// Copying a kept entry hashes its key; bill the payload
				// before the write.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if err := hashSet(out, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}), nil
	case "reject":
		return NewAutoBuiltin("hash.reject", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.reject does not take arguments")
			}
			count := receiver.HashLen()
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(count, sortedHashEntryBufferBytes(count)))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.reject", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHashWithCapacity(count)
			var blockArgs [2]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			// Compact the kept entries to the front of the buffer and populate the
			// result hash after the loop; see hash.transform_values for why the
			// in-loop hashSet had to go. The write index never runs ahead of the
			// read index, so the compaction is safe in place and adds no
			// allocation.
			ordered := receiver.HashEntriesInto(entryBuf[:])
			kept := 0
			for i := range ordered {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = ordered[i].Key
				blockArgs[1] = ordered[i].Value
				exclude, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				if !exclude.Truthy() {
					ordered[kept] = ordered[i]
					kept++
				}
			}
			for _, entry := range ordered[:kept] {
				// Copying a kept entry hashes its key; bill the payload
				// before the write.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if err := hashSet(out, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}), nil
	case "map":
		return NewAutoBuiltin("hash.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.map does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.map does not take keyword arguments")
			}
			// The accumulator is built first, so its baseline snapshots
			// exec.reservedScratchBytes before the reservation below. That
			// counter is part of the baseline, so reserving first would put
			// the result backing in the baseline and then add it again through
			// reserveSlots and every cap(out) projection, rejecting a build
			// whose receiver, scratch, and one backing actually fit.
			//
			// map keeps an arbitrary block result per entry, so the growing
			// result is charged incrementally rather than only after the call:
			// a block returning an individually quota-sized value per entry
			// could otherwise pile up past the quota before the post-call
			// check ran. The baseline includes the live receiver and block,
			// held on the Go stack during the call.
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)

			// The reservation is what makes the backing visible to checks that
			// run inside the block body, which cannot see a Go-local slice. It
			// is raised before the runner is constructed because the runner
			// snapshots its bind baseline once, at construction.
			retained := newRetainedOutputScratch(exec)
			defer retained.release()
			retained.reserve(arraySlotBackingBytes(receiver.HashLen()))

			runner, err := newBlockCallRunner(exec, block, "hash.map", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			// Ruby picks the yield shape from the block's positional arity: one
			// parameter receives the [key, value] pair, two or more auto-splat
			// into key and value. Numbered implicit parameters count toward
			// that arity, so `{ _2 }` is an arity-2 block -- yielding the pair
			// unconditionally bound `_1` to the whole pair and left `_2` nil.
			collapsePair := blockWantsCollapsedPair(valueBlock(block))
			count := receiver.HashLen()
			if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(count); err != nil {
				return NewNil(), err
			}
			out := make([]Value, 0, count)
			// Reserve the preallocated backing before the first block call,
			// not after it returns: the backing is live from the make above,
			// so a block allocating its large temporary on the very first
			// entry would otherwise be measured without it.
			retained.reserve(acc.accumulatedBytes(cap(out)))
			var blockArg [1]Value
			var blockArgs [2]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				var (
					val Value
					err error
				)
				if collapsePair {
					pair := NewArray([]Value{entry.Key, entry.Value})
					if err = acc.checkTransient(pair, cap(out)); err != nil {
						return NewNil(), err
					}
					blockArg[0] = pair
					val, err = runner.callRetained(blockArg[:])
				} else {
					blockArgs[0] = entry.Key
					blockArgs[1] = entry.Value
					val, err = runner.callRetained(blockArgs[:])
				}
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				out = append(out, val)
				if err := acc.addConservative(val, cap(out)); err != nil {
					return NewNil(), err
				}
				retained.reserve(acc.accumulatedBytes(cap(out)))
			}
			return adoptArray(out), nil
		}), nil
	case "map_with_index":
		return NewAutoBuiltin("hash.map_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.map_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.map_with_index does not take keyword arguments")
			}
			// The results accumulate in a Go-local slice the checks inside later
			// block calls cannot reach, so a block allocating a large temporary was
			// measured against a graph missing everything the loop had already kept,
			// and the two passed separately though they coexist. Reserving the
			// running total as releasable scratch puts it in front of those checks.
			// Each reservation is O(1): the accumulator already tracks the total, so
			// nothing is re-walked.
			retained := newRetainedOutputScratch(exec)
			defer retained.release()
			runner, err := newBlockCallRunner(exec, block, "hash.map_with_index", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			count := receiver.HashLen()
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(count); err != nil {
				return NewNil(), err
			}
			out := make([]Value, 0, count)
			// Reserve the preallocated backing before the first block call, not
			// after it returns: it is live from the make above, so a block
			// allocating a large temporary on the very first entry would
			// otherwise be measured without it.
			retained.reserve(acc.accumulatedBytes(cap(out)))
			var blockArgs [2]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for i, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				pair := NewArray([]Value{entry.Key, entry.Value})
				if err := acc.checkTransient(pair, cap(out)); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = pair
				blockArgs[1] = NewInt(int64(i))
				val, err := runner.callRetained(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				out = append(out, val)
				if err := acc.addConservative(val, cap(out)); err != nil {
					return NewNil(), err
				}
				retained.reserve(acc.accumulatedBytes(cap(out)))
			}
			return adoptArray(out), nil
		}), nil
	case "transform_keys":
		return NewAutoBuiltin("hash.transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_keys does not take arguments")
			}
			if err := ensureBlock(block, "hash.transform_keys"); err != nil {
				return NewNil(), err
			}
			count := receiver.HashLen()
			scratch := sortedHashEntryBufferBytes(count)
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(count, scratch))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_keys", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			out := NewHashWithCapacity(count)
			var blockArg [1]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			// Defer the insertions past the last block call so the block region's
			// memoized prefix survives the loop; see hash.transform_values.
			// Inserting in order keeps both the result's insertion order and its
			// last-writer-wins collisions identical to inserting throughout.
			ordered := receiver.HashEntriesInto(entryBuf[:])
			buffered := 0
			for i := range ordered {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = ordered[i].Key
				nextKey, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				if err := exec.chargeValueKeySteps(nextKey); err != nil {
					return NewNil(), err
				}
				// Validation stays inline so an unsupported key still fails at the
				// same point in the iteration it always did.
				resolved, err := hashKeyString(nextKey)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block returned an unsupported hash key: %w", err)
				}
				// buffered never runs ahead of i, so this reuses the already
				// reserved entry buffer without disturbing an unread entry.
				ordered[buffered] = HashEntry{Key: nextKey, Value: ordered[i].Value}
				buffered++
				if err := acc.addSynthesizedKey(resolved); err != nil {
					return NewNil(), err
				}
			}
			for _, entry := range ordered[:buffered] {
				if err := hashSet(out, entry.Key, entry.Value); err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block returned an unsupported hash key: %w", err)
				}
			}
			return out, nil
		}), nil
	case "deep_transform_keys":
		return NewAutoBuiltin("hash.deep_transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not take arguments")
			}
			if err := ensureBlock(block, "hash.deep_transform_keys"); err != nil {
				return NewNil(), err
			}
			return deepTransformKeys(exec, receiver, args, kwargs, block)
		}), nil
	case "remap_keys":
		return NewBuiltin("hash.remap_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
				return NewNil(), fmt.Errorf("hash.remap_keys expects a key mapping hash")
			}
			count := receiver.HashLen()
			if err := exec.checkProjectedHashTransformBytes(count, sortedHashEntryBufferBytes(count), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHashWithCapacity(count)
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// The mapping lookup and the fallback copy both hash the
				// original key; bill its payload before the first probe.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if mapped, ok, err := hashGet(args[0], entry.Key); err != nil {
					return NewNil(), fmt.Errorf("hash.remap_keys mapping key is an unsupported hash key: %w", err)
				} else if ok {
					if err := exec.chargeValueKeySteps(mapped); err != nil {
						return NewNil(), err
					}
					if err := hashSet(out, mapped, entry.Value); err != nil {
						return NewNil(), fmt.Errorf("hash.remap_keys mapping value is an unsupported hash key: %w", err)
					}
					continue
				}
				if err := hashSet(out, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}), nil
	case "transform_values":
		return NewAutoBuiltin("hash.transform_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_values does not take arguments")
			}
			// The transformed values accumulate in Go locals the checks inside later
			// block calls cannot reach, so a block allocating a large temporary was
			// measured against a graph missing everything the loop had already
			// transformed, and the two passed separately though they coexist.
			// Reserving the running total as releasable scratch puts it in front of
			// those checks. Only the payloads are reserved here; the output map and
			// its scratch are already held by the reserveLoopScratch in each branch.
			retained := newRetainedOutputScratch(exec)
			defer retained.release()
			if err := ensureBlock(block, "hash.transform_values"); err != nil {
				return NewNil(), err
			}
			count := receiver.HashLen()
			scratch := sortedHashEntryBufferBytes(count)
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(count, scratch))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_values", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			out := NewHashWithCapacity(count)
			var blockArg [1]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			// Collect the transformed values first and populate the result hash
			// after the loop. hashSet goes through the value package, which bumps
			// the mutation epoch on every insertion; doing that between block calls
			// invalidated the block region's memoized prefix each iteration and
			// re-walked the whole receiver, which is quadratic in the entry count.
			// The bumps still happen, just after the last block call, where no
			// further block-body check depends on the memo -- the same shape
			// array.group_by already uses.
			//
			// The transformed value is written back into the entry buffer rather
			// than a second slice: HashEntriesInto copies entries by value, so this
			// cannot disturb the receiver, and the buffer's bytes are already held
			// by the reserveLoopScratch above. So this adds no allocation and needs
			// no accounting change.
			ordered := receiver.HashEntriesInto(entryBuf[:])
			for i := range ordered {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = ordered[i].Value
				nextValue, err := runner.callRetained(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				ordered[i].Value = nextValue
				if err := acc.add(nextValue); err != nil {
					return NewNil(), err
				}
				retained.reserve(acc.retainedPayloadBytes())
			}
			for _, entry := range ordered {
				// Copying the entry hashes its key; bill the payload before
				// the write.
				if err := exec.chargeValueKeySteps(entry.Key); err != nil {
					return NewNil(), err
				}
				if err := out.HashSetOwned(entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}), nil
	case "compact":
		return NewAutoBuiltin("hash.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.compact does not take arguments")
			}
			count := receiver.HashLen()
			if err := exec.checkProjectedHashTransformBytes(count, sortedHashEntryBufferBytes(count), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHashWithCapacity(count)
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range receiver.HashEntriesInto(entryBuf[:]) {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if entry.Value.Kind() != KindNil {
					// Copying a kept entry hashes its key; bill the payload
					// before the write.
					if err := exec.chargeValueKeySteps(entry.Key); err != nil {
						return NewNil(), err
					}
					if err := hashSet(out, entry.Key, entry.Value); err != nil {
						return NewNil(), err
					}
				}
			}
			return out, nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

// sortQuotaAbort sentinels the panic that stops a metered key sort once its
// quota charge fails: slices.SortFunc has no error path, and finishing the
// sort after the quota fired would be O(n log n) post-quota comparisons whose
// order the caller discards with the error anyway.
type sortQuotaAbort struct{}

// abortableKeySort sorts keys with cmp until cmp reports false, then abandons
// the sort immediately, leaving keys in unspecified order.
func abortableKeySort(keys []string, cmp func(a, b string) (int, bool)) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(sortQuotaAbort); !ok {
				panic(r)
			}
		}
	}()
	slices.SortFunc(keys, func(a, b string) int {
		c, ok := cmp(a, b)
		if !ok {
			panic(sortQuotaAbort{})
		}
		return c
	})
}
