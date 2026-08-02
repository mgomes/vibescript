package runtime

import (
	"cmp"
	"fmt"
	"math"
	"reflect"
	"slices"
)

// hashMemberNames mirrors the names dispatched by hashMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var hashMemberNames = []string{
	"size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_with_index", "each_key", "each_value", "to_a", "default", "default_proc",
	"merge", "update", "merge!", "replace", "store", "delete", "clear", "delete_if", "keep_if", "slice", "except", "flatten", "select", "reject", "map", "map_with_index", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact",
	"inspect",
}

var hashBuiltinMembers = newMemberTable(hashMemberNames)

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

func anyTypedHash(values []Value) bool {
	return slices.ContainsFunc(values, hashHasTypedEntries)
}

func hashMemberBuiltin(property string) (Value, error) {
	switch property {
	case "size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_with_index", "each_key", "each_value", "to_a", "default", "default_proc":
		return hashMemberQuery(property)
	case "merge", "update", "merge!", "replace", "store", "delete", "clear", "delete_if", "keep_if", "slice", "except", "flatten", "select", "reject", "map", "map_with_index", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact":
		return hashMemberTransforms(property)
	case "inspect":
		return newInspectBuiltin("hash"), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

// newHashPreservingDefault wraps out in a hash that carries the same default
// metadata as receiver. Ruby's Hash#merge family copies the receiver's default
// value and default proc onto the merged hash, so the immutable-style merge here
// does the same. A receiver without a default produces a plain hash.
func newHashPreservingDefault(receiver Value, out map[string]Value) Value {
	defaultValue := hashDefaultValue(receiver)
	defaultProc := hashDefaultProc(receiver)
	if defaultValue.IsNil() && defaultProc.IsNil() {
		return NewHash(out)
	}
	return NewHashWithDefault(out, defaultValue, defaultProc)
}

// hashDefaultForKey resolves a hash's Ruby-style default for a missing key. A
// configured default proc takes precedence and is invoked with (hash, key) --
// the receiver passes through unchanged so the proc can store into it via
// hash[key] = ..., and key keeps its original symbol/string value. A default
// proc never auto-inserts: only the proc body's own assignment, if any, mutates
// the hash. With no proc, the default value is returned without inserting (Ruby
// returns the same default object for every missing key). With neither, the
// result is nil. It backs both missing-key [] access and Hash#default(key).
func (exec *Execution) hashDefaultForKey(receiver, key Value) (Value, error) {
	if proc := hashDefaultProc(receiver); !proc.IsNil() {
		return exec.CallBlock(proc, []Value{receiver, key})
	}
	return hashDefaultValue(receiver), nil
}

// hashMissingKeyDefault resolves a missing-key [] access, wrapping any default
// proc error with the index expression's position for a precise diagnostic. A
// non-local return from the proc is not an error: it passes through intact so
// the method that created the proc can consume it; every real proc error is
// still re-anchored at the [] expression.
func (exec *Execution) hashMissingKeyDefault(receiver, key Value, pos Position) (Value, error) {
	result, err := exec.hashDefaultForKey(receiver, key)
	if err != nil {
		if isNonLocalReturnSignal(err) {
			return NewNil(), err
		}
		return NewNil(), exec.errorAt(pos, "%s", err.Error())
	}
	return result, nil
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

func sortedHashKeysInto(entries map[string]Value, buf []string) []string {
	keys := buf[:0]
	if cap(keys) < len(entries) {
		keys = make([]string, 0, len(entries))
	}
	for key := range entries {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// orderedTypedHashEntriesInto materializes receiver's typed entries into buf in
// Ruby-style insertion order. The copy snapshots the entries so a user block
// that mutates the receiver mid-iteration cannot skew the walk.
func orderedTypedHashEntriesInto(receiver Value, buf []HashEntry) []HashEntry {
	return receiver.HashEntriesInto(buf)
}

// hashEntryKeysAreStable reports whether every key in entries canonicalizes to
// the same lookup identity no matter when it is canonicalized.
//
// It gates the key-preserving drivers' deferred build. Populating the result
// after the block loop re-canonicalizes each key at build time, so a key whose
// value can change mid-iteration would be stored under its final identity rather
// than the one it had when its entry was processed -- two entries could collapse
// onto one. Of the kinds NewHashLookupKey accepts (nil, bool, int, float, string,
// symbol, range, array) only an array is mutable in place, so entries without an
// array key are immune and may defer. A receiver with an array key falls back to
// inserting during the loop, preserving today's identities exactly.
func hashEntryKeysAreStable(entries []HashEntry) bool {
	for i := range entries {
		if entries[i].Key.Kind() == KindArray {
			return false
		}
	}
	return true
}

// deterministicHashEntriesInto returns receiver's entries in the order a copy
// should preserve: a typed hash keeps its recorded insertion order; a bare
// host-provided map carries no insertion record, so its entries contribute in
// sorted key order. Copying a bare source through HashEntries() would instead
// persist its arbitrary Go-map traversal as the fresh hash's insertion order,
// making a documented sorted map nondeterministic after store/delete/merge.
func deterministicHashEntriesInto(receiver Value, buf []HashEntry) []HashEntry {
	if hashHasTypedEntries(receiver) {
		return receiver.HashEntriesInto(buf)
	}
	// Sort the materialized entries in place rather than building a separate
	// sorted key list, so this allocates no more than the HashEntries() copy the
	// callers already account for (a bare hash's sorted scratch would otherwise
	// fall outside their memory-quota projections).
	m := receiver.Hash()
	entries := buf[:0]
	if cap(entries) < len(m) {
		entries = make([]HashEntry, 0, len(m))
	}
	for key, val := range m {
		entries = append(entries, HashEntry{Key: NewString(key), Value: val})
	}
	slices.SortFunc(entries, func(a, b HashEntry) int {
		return cmp.Compare(a.Key.String(), b.Key.String())
	})
	return entries
}

// sortedKeyBufferBytes returns the heap bytes sortedHashKeysInto allocates to
// hold a sorted key list for keyCount keys. A count that fits the inline stack
// buffer reuses it and allocates nothing; a larger count heaps a fresh []string
// of one header per key plus the slice base. The key strings alias the map's
// own keys (already resident), so only the scratch slice's backing is new and
// the payload bytes are not counted here. Hash transforms charge this against
// the quota before sorting so the scratch list itself cannot escape the sandbox
// on a large receiver.
func sortedKeyBufferBytes(keyCount int) int {
	if keyCount <= smallHashKeyBufferSize {
		return 0
	}
	return saturatingAdd(estimatedSliceBaseBytes, saturatingMul(keyCount, estimatedStringHeaderBytes))
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
	typed := hashHasTypedEntries(receiver)
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
	scratch := sortedKeyBufferBytes(count)
	if typed {
		scratch = sortedHashEntryBufferBytes(count)
	}
	if err := acc.reserveScratch(scratch); err != nil {
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
	if typed {
		var entryBuf [smallHashKeyBufferSize]HashEntry
		for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
			if err := appendPair(entry.Key, entry.Value); err != nil {
				return nil, err
			}
		}
	} else {
		entries := receiver.Hash()
		var keyBuf [smallHashKeyBufferSize]string
		for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
			if err := appendPair(NewSymbol(key), entries[key]); err != nil {
				return nil, err
			}
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

func typedExclusionSetBytes(count int) int {
	if count <= 0 {
		return 0
	}
	return saturatingAdd(estimatedMapBaseBytes, saturatingMul(count, estimatedMapEntryBytes+estimatedHashLookupKeyBytes))
}

// typedHashEntryMapBytes returns the structural bytes a typed hash retains for
// count entries: the entry map's base and per-entry bucket, lookup key, and
// entry pair, plus the insertion-order backing (one lookup-key slot per entry
// behind a slice base) HashSet grows alongside the map.
func typedHashEntryMapBytes(count int) int {
	if count < 0 {
		return 0
	}
	if count == 0 {
		return estimatedMapBaseBytes
	}
	perEntry := estimatedMapEntryBytes + 2*estimatedHashLookupKeyBytes + estimatedHashEntryBytes
	return saturatingAdd(estimatedMapBaseBytes+estimatedSliceBaseBytes, saturatingMul(count, perEntry))
}

func typedHashTransformBufferBytes(outputEntries, scratchBytes int) int {
	bytes := estimatedValueBytes + estimatedHashDataBytes
	bytes = saturatingAdd(bytes, typedHashEntryMapBytes(outputEntries))
	return saturatingAdd(bytes, scratchBytes)
}

func legacyTransformKeysBufferBytes(outputEntries, scratchBytes int) int {
	if outputEntries == 0 {
		return hashTransformBufferBytes(0, scratchBytes)
	}
	return typedHashTransformBufferBytes(outputEntries, scratchBytes)
}

func (exec *Execution) checkProjectedTypedHashBytes(count int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	return exec.checkProjectedTypedHashTransformBytes(count, 0, receiver, args, kwargs, block)
}

func (exec *Execution) checkProjectedTypedHashTransformBytes(outputEntries, scratchBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) error {
	if exec.memoryQuota <= 0 {
		return nil
	}

	used := exec.hashCallRootBytes(receiver, args, kwargs, block)
	used = saturatingAdd(used, typedHashTransformBufferBytes(outputEntries, scratchBytes))
	if used > exec.memoryQuota {
		return fmt.Errorf("%w (%d bytes)", errMemoryQuotaExceeded, exec.memoryQuota)
	}
	return nil
}

func (exec *Execution) maxProjectedTypedHashEntries(scratchBytes int, receiver Value, args []Value, kwargs map[string]Value, block Value) int {
	if exec.memoryQuota <= 0 {
		return math.MaxInt
	}
	used := exec.hashCallRootBytes(receiver, args, kwargs, block)
	used = saturatingAdd(used, scratchBytes)
	used = saturatingAdd(used, estimatedValueBytes+estimatedHashDataBytes+estimatedMapBaseBytes)
	if used >= exec.memoryQuota {
		return 0
	}
	if saturatingAdd(used, estimatedSliceBaseBytes) > exec.memoryQuota {
		return 0
	}
	perEntry := estimatedMapEntryBytes + 2*estimatedHashLookupKeyBytes + estimatedHashEntryBytes
	return (exec.memoryQuota - used - estimatedSliceBaseBytes) / perEntry
}

func newTypedResultHash(capacity int) Value {
	return NewTypedHash(capacity)
}

func newTypedHashPreservingDefault(receiver Value, capacity int) Value {
	out := NewTypedHash(capacity)
	out.SetHashDefaults(hashDefaultValue(receiver), hashDefaultProc(receiver))
	return out
}

func deepTransformArrayBufferBytes(count int) int {
	return saturatingAdd(estimatedValueBytes+estimatedSliceBaseBytes, saturatingMul(count, estimatedValueBytes))
}

func deepTransformKeys(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return deepTransformKeysWithState(exec, receiver, receiver, args, kwargs, block, &deepTransformState{
		seenHashes: make(map[uintptr]struct{}),
		seenArrays: make(map[uintptr]struct{}),
	})
}

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
		if hashHasTypedEntries(value) {
			id := hashIdentity(value)
			if id != 0 {
				if _, seen := state.seenHashes[id]; seen {
					return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
				}
				state.seenHashes[id] = struct{}{}
				defer delete(state.seenHashes, id)
			}
			count := value.HashLen()
			scratchBytes := sortedHashEntryBufferBytes(count)
			delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(count, scratchBytes))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			out := newTypedResultHash(count)
			var blockArg [1]Value
			var entryBuf [smallHashKeyBufferSize]HashEntry
			for _, entry := range orderedTypedHashEntriesInto(value, entryBuf[:]) {
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
				lookupKey, err := hashLookupKey(nextKeyValue)
				if err != nil {
					exec.releaseLoopScratch(prefixDelta)
					return NewNil(), fmt.Errorf("hash.deep_transform_keys block returned unsupported hash key: %w", err)
				}
				nextKey := hashDisplayKey(nextKeyValue)
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
					return NewNil(), fmt.Errorf("hash.deep_transform_keys block returned unsupported hash key: %w", err)
				}
				if err := acc.addTypedSynthesizedKey(nextKeyValue, nextKey, lookupKey); err != nil {
					return NewNil(), err
				}
				if err := acc.addBaselineDeduped(nextValue); err != nil {
					return NewNil(), err
				}
			}
			return out, nil
		}
		entries := value.Hash()
		id := reflect.ValueOf(entries).Pointer()
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}
		scratchBytes := sortedKeyBufferBytes(len(entries))
		delta := exec.reserveLoopScratch(hashTransformBufferBytes(len(entries), scratchBytes))
		defer exec.releaseLoopScratch(delta)
		if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
		out := NewHash(make(map[string]Value, len(entries)))
		var blockArg [1]Value
		var keyBuf [smallHashKeyBufferSize]string
		for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			prefixDelta, err := reserveDeepTransformRetainedPayload(exec, acc.retainedPayloadBytes(), receiver, args, kwargs, block)
			if err != nil {
				return NewNil(), err
			}
			blockArg[0] = NewSymbol(key)
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
			nextKey, err := valueToHashKey(nextKeyValue)
			if err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), fmt.Errorf("hash.deep_transform_keys block returned unsupported hash key: %w", err)
			}
			keyDelta, err := reserveDeepTransformRetainedPayload(exec, len(nextKey), receiver, args, kwargs, block)
			if err != nil {
				exec.releaseLoopScratch(prefixDelta)
				return NewNil(), err
			}
			nextValue, err := deepTransformKeysWithState(exec, entries[key], receiver, args, kwargs, block, state)
			exec.releaseLoopScratch(keyDelta)
			exec.releaseLoopScratch(prefixDelta)
			if err != nil {
				return NewNil(), err
			}
			if err := hashSet(out, nextKeyValue, nextValue); err != nil {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys block returned unsupported hash key: %w", err)
			}
			if err := acc.addSynthesizedKey(nextKey); err != nil {
				return NewNil(), err
			}
			// The value is a recursive transform result, not an arbitrary block
			// result: charge fresh containers while deduplicating unchanged leaves
			// already held by the receiver.
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
				return NewBool(false), nil
			}
			return NewBool(ok), nil
		}), nil
	case "default":
		return NewAutoBuiltin("hash.default", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.default does not accept keyword arguments")
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("hash.default expects at most one key")
			}
			// Ruby's Hash#default with no argument returns the configured default
			// value, never invoking the default proc (so a proc-only hash reports
			// nil). Given a key, it resolves the default the same way a missing-key
			// [] access would: a default proc is invoked with (hash, key) -- which
			// may store -- and otherwise the default value is returned.
			if len(args) == 0 {
				return hashDefaultValue(receiver), nil
			}
			return exec.hashDefaultForKey(receiver, args[0])
		}), nil
	case "default_proc":
		return NewAutoBuiltin("hash.default_proc", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.default_proc does not take arguments")
			}
			// Returns the default proc (a block value) or nil, mirroring Ruby's
			// Hash#default_proc.
			return hashDefaultProc(receiver), nil
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
			if hashHasTypedEntries(receiver) {
				for _, entry := range receiver.HashEntries() {
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
			}
			// Deterministic traversal: with randomized map iteration a quota
			// that covers one metered comparison but not two alternated
			// between a result and an error on identical inputs. Sorting
			// reads the keys, so their bytes are billed first.
			entries := receiver.Hash()
			// The key slice is a transient the estimator never sees;
			// validate it against the memory quota before allocating, like
			// the equality sort helper does.
			if err := exec.equalityScratchValidatorFunc()(len(entries) * hashKeySortScratchEntryBytes); err != nil {
				return NewNil(), err
			}
			keyBytes := 0
			keys := make([]string, 0, len(entries))
			for k := range entries {
				keys = append(keys, k)
				keyBytes += len(k)
			}
			if err := exec.chargeStringScan(keyBytes); err != nil {
				return NewNil(), err
			}
			// The sort rereads common prefixes past the linear charge above;
			// measure the exact bytes each comparison reads and bill them
			// after, as the equality sorter does.
			sortRead := 0
			slices.SortFunc(keys, func(a, b string) int {
				n := min(len(a), len(b))
				i := 0
				for i < n && a[i] == b[i] {
					i++
				}
				read := i + 1
				if read > n {
					read = n
				}
				if sortRead += read; sortRead < 0 {
					sortRead = math.MaxInt / 2
				}
				if i < n {
					if a[i] < b[i] {
						return -1
					}
					return 1
				}
				return len(a) - len(b)
			})
			if err := exec.chargeStringScan(sortRead); err != nil {
				return NewNil(), err
			}
			for _, k := range keys {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if equality.Equal(entries[k], args[0]) {
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
			if hashHasTypedEntries(receiver) {
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
				entries := orderedTypedHashEntriesInto(receiver, entryBuf[:])
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
			}
			entries := receiver.Hash()
			if err := exec.checkStepBudgetFor(len(entries)); err != nil {
				return NewNil(), err
			}
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedKeyBufferBytes(len(entries))); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(len(entries)); err != nil {
				return NewNil(), err
			}
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			values := make([]Value, 0, len(keys))
			for _, k := range keys {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				values = append(values, NewSymbol(k))
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
			if hashHasTypedEntries(receiver) {
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
				entries := orderedTypedHashEntriesInto(receiver, entryBuf[:])
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
			}
			entries := receiver.Hash()
			if err := exec.checkStepBudgetFor(len(entries)); err != nil {
				return NewNil(), err
			}
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedKeyBufferBytes(len(entries))); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveSlots(len(entries)); err != nil {
				return NewNil(), err
			}
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			values := make([]Value, 0, len(keys))
			for _, k := range keys {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				values = append(values, entries[k])
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
			out := make([]Value, len(args))
			for i, arg := range args {
				if err := exec.chargeValueKeySteps(arg); err != nil {
					return NewNil(), err
				}
				value, ok, err := hashGet(receiver, arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.values_at key is unsupported hash key: %w", err)
				}
				if ok {
					out[i] = value
					continue
				}
				// A missing key is a [] access: consult the hash's Ruby-style
				// default (a default value, or a default proc invoked with the
				// hash and key, which may store) rather than filling nil, matching
				// MRI's Hash#values_at.
				resolved, err := exec.hashDefaultForKey(receiver, arg)
				if err != nil {
					return NewNil(), err
				}
				out[i] = resolved
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
				return NewNil(), fmt.Errorf("hash.fetch key is unsupported hash key: %w", err)
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
		return NewAutoBuiltin("hash.fetch_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			out := make([]Value, len(args))
			for i, arg := range args {
				if err := exec.chargeValueKeySteps(arg); err != nil {
					return NewNil(), err
				}
				value, ok, err := hashGet(receiver, arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.fetch_values key is unsupported hash key: %w", err)
				}
				if ok {
					out[i] = value
					continue
				}
				if valueBlock(block) == nil {
					return NewNil(), fmt.Errorf("hash.fetch_values key not found: %s", formatMissingHashKey(arg))
				}
				blockArg := [1]Value{arg}
				blockValue, err := exec.CallBlock(block, blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				out[i] = blockValue
			}
			return NewArray(out), nil
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
			if hashHasTypedEntries(receiver) {
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
				for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
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
			}
			entries := receiver.Hash()
			// Match Ruby: a block declaring a single positional parameter receives
			// each entry as a two-element [key, value] pair, while a block with two
			// or more parameters auto-splats into key and value (extra parameters get
			// nil). blockWantsCollapsedPair inspects the block's arity to pick the
			// shape.
			collapsePair := blockWantsCollapsedPair(valueBlock(block))
			// each builds no output map, but it materializes a sorted key list to walk
			// entries deterministically. Reserve that scratch for the walk's lifetime so
			// every block-body check sees it. Collapsed-pair walks also allocate one
			// transient [key, value] pair per entry. Keep the largest pair reserved only
			// when the body cannot account for the actual bound pair: either the block
			// collects a rest while binding, or the receiver is not already reachable
			// from the live baseline. Otherwise preflight the largest pair once and let
			// body checks count the real bound pair, avoiding a false extra pair.
			scratch := sortedKeyBufferBytes(len(entries))
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
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// The loop below writes only builtin-local state (nothing; each yields without building a result), which is
			// unreachable from any root until this builtin returns, so the region's
			// vouch holds: everything reachable it touches goes through the block, whose
			// own writes bump or are re-walked fresh. Without it a host-built (untyped)
			// receiver falls to the builtin bypass and re-walks the whole hash on every
			// check, which is quadratic in the entry count.
			defer exec.beginBlockIterationRegion().end()
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call only charges a step
				// per statement it runs, so a blockless body would otherwise iterate
				// the whole hash uncharged.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if collapsePair {
					// The yielded value is always a two-element array. Rest-collecting
					// destructuring blocks keep the max pair reserved in the runner's bind
					// baseline; plain pair bindings rely on the bound pair the body checks
					// already see when the receiver is reachable.
					pair := NewArray([]Value{NewSymbol(key), entries[key]})
					if _, err := runner.call([]Value{pair}); err != nil {
						return NewNil(), err
					}
					if err := exec.checkContext(); err != nil {
						return NewNil(), err
					}
					continue
				}
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
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
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				delta := exec.reserveLoopScratch(sortedHashEntryBufferBytes(count))
				defer exec.releaseLoopScratch(delta)
				if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				var blockArgs [2]Value
				var entryBuf [smallHashKeyBufferSize]HashEntry
				for i, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
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
			}
			entries := receiver.Hash()
			// each_with_index walks a materialized sorted key list to yield entries
			// deterministically. Reserve that scratch buffer against the quota for the
			// walk's whole lifetime; the iterator returns the receiver and builds no
			// derived map, so it is charged the receiver baseline plus the live scratch
			// rather than a phantom output map. Yielding the [key, value] pair builds a
			// fresh two-element array per entry, which the per-pair memory check below
			// charges as the body runs.
			delta := exec.reserveLoopScratch(sortedKeyBufferBytes(len(entries)))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for i, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation, matching each.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// Ruby's Hash#each_with_index yields the [key, value] pair as the first
				// block parameter and the index as the second.
				pair := NewArray([]Value{NewSymbol(key), entries[key]})
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
			if hashHasTypedEntries(receiver) {
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
				for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
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
			}
			entries := receiver.Hash()
			// Reserve the sorted key scratch buffer for the walk's lifetime; each_key
			// builds no output map but walks a materialized key list that stays live
			// while the block body runs, so reserving it keeps every memory check
			// inside the body aware of the scratch. Reserving first means the walk
			// projection adds no separate scratch bytes. each_key binds the key
			// directly, so it allocates no per-entry pair. Reserve it BEFORE building
			// the runner so the runner's bind-charge baseline snapshot already includes
			// the scratch; otherwise a rest-collecting block (|(head, *tail)|) over an
			// ephemeral receiver could charge its fresh tail backing against a baseline
			// that omits the scratch while the body checks miss the Go-stack receiver.
			delta := exec.reserveLoopScratch(sortedKeyBufferBytes(len(entries)))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.each_key", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// The loop below writes only builtin-local state (nothing; each_key yields without building a result), which is
			// unreachable from any root until this builtin returns, so the region's
			// vouch holds: everything reachable it touches goes through the block, whose
			// own writes bump or are re-walked fresh. Without it a host-built (untyped)
			// receiver falls to the builtin bypass and re-walks the whole hash on every
			// check, which is quadratic in the entry count.
			defer exec.beginBlockIterationRegion().end()
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per key so an empty block still consumes the step
				// quota and observes cancellation rather than relying on runner.call.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = NewSymbol(key)
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
			if hashHasTypedEntries(receiver) {
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
				for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
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
			}
			entries := receiver.Hash()
			// Reserve the sorted key scratch buffer for the walk's lifetime; each_value
			// builds no output map but walks a materialized key list that stays live
			// while the block body runs, so reserving it keeps every memory check
			// inside the body aware of the scratch. Reserving first means the walk
			// projection adds no separate scratch bytes. each_value binds the value
			// directly, so it allocates no per-entry pair. Reserve it BEFORE building
			// the runner so the runner's bind-charge baseline snapshot already includes
			// the scratch; otherwise a rest-collecting block (|(head, *tail)|) over an
			// ephemeral receiver could charge its fresh tail backing against a baseline
			// that omits the scratch while the body checks miss the Go-stack receiver.
			delta := exec.reserveLoopScratch(sortedKeyBufferBytes(len(entries)))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.each_value", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// The loop below writes only builtin-local state (nothing; each_value yields without building a result), which is
			// unreachable from any root until this builtin returns, so the region's
			// vouch holds: everything reachable it touches goes through the block, whose
			// own writes bump or are re-walked fresh. Without it a host-built (untyped)
			// receiver falls to the builtin bypass and re-walks the whole hash on every
			// check, which is quadratic in the entry count.
			defer exec.beginBlockIterationRegion().end()
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per value so an empty block still consumes the step
				// quota and observes cancellation rather than relying on runner.call.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = entries[key]
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
			if hashHasTypedEntries(receiver) {
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
				entries := orderedTypedHashEntriesInto(receiver, entryBuf[:])
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
			}
			entries := receiver.Hash()
			// Materialize the [key, value] pairs in Vibescript's deterministic
			// (sorted-key) iteration order, matching keys/values/each. Keys
			// reconstruct as symbols, as everywhere a hash key surfaces as a value.
			//
			// The pairs alias the receiver's values, but the output slice, the one
			// pair array per entry, and the sorted-key scratch buffer are all fresh
			// allocations the post-call result check would only observe after the
			// whole structure was built. A receiver that fits the quota can still
			// have a [key, value] materialization that does not, so charge the build
			// incrementally through an array accumulator (seeded with the receiver, so
			// aliased values dedup against it) and step per entry. This bounds the
			// peak against MemoryQuotaBytes and honors a small StepQuota or a canceled
			// context mid-loop, matching the neighboring hash walks rather than
			// allocating everything before the runtime can reject it.
			//
			// Abort before materializing and sorting the keys when the context is
			// already canceled or the remaining step quota cannot cover one step per
			// entry. The per-pair loop charges a step per entry and observes
			// cancellation, but the sortedHashKeysInto sort is O(n log n), so without
			// this cheap up-front check a large hash could spend that work even when the
			// loop is guaranteed to exhaust the step quota partway. Bounding on
			// len(entries) (not just one remaining step) keeps a sandboxed caller from
			// spending O(n log n) CPU on a projection that cannot fit the remaining
			// steps; the per-pair step still enforces the quota, so this never rejects a
			// build the loop would accept.
			if err := exec.checkStepBudgetFor(len(entries)); err != nil {
				return NewNil(), err
			}
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			scratch := sortedKeyBufferBytes(len(entries))
			if err := acc.reserveScratch(scratch); err != nil {
				return NewNil(), err
			}
			// The output length is known before the keys are sorted. Reserve the full
			// slot backing before sortedHashKeysInto so a build that cannot fit the
			// result slice does not spend the sort or allocate the key scratch first.
			if err := acc.reserveSlots(len(entries)); err != nil {
				return NewNil(), err
			}
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			pairs := make([]Value, 0, len(keys))
			for _, key := range keys {
				// Charge a step per pair so materializing a large hash participates in
				// the step quota and observes cancellation before the result is fully
				// assembled.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				pairs = append(pairs, NewArray([]Value{NewSymbol(key), entries[key]}))
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

// looseMergedKeyUpperBound returns a non-allocating upper bound on the number of
// keys a merge of base and args could hold: the receiver's keys plus every
// argument's length, summed without subtracting overlaps. It never under-counts
// the real union, so when checkProjectedHashBytes accepts this bound the merge is
// guaranteed to fit and the caller can skip the exact (allocating) union count.
func looseMergedKeyUpperBound(base map[string]Value, args []Value) int {
	count := len(base)
	for _, arg := range args {
		count = saturatingAdd(count, len(arg.Hash()))
	}
	return count
}

// mergeSortScratchBytes returns the peak heap footprint of merge's sorted scratch
// buffer. The per-argument buffer is reused across arguments, so it only ever
// sizes to the largest single argument. The receiver base is copied in map order
// without sorting, so it contributes no scratch. The result feeds the merge
// projection so the scratch list cannot allocate past the quota even when the
// merged union itself is small (a huge argument that fully overlaps the base).
func mergeSortScratchBytes(args []Value) int {
	maxArg := 0
	for _, arg := range args {
		if n := len(arg.Hash()); n > maxArg {
			maxArg = n
		}
	}
	// The merge sorts each argument's keys (reusing one buffer sized to the
	// largest argument) so conflict block side effects are deterministic. The
	// receiver base is copied in map order without a sorted buffer, so it adds no
	// scratch of its own.
	return sortedKeyBufferBytes(maxArg)
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

// typedMergedKeyCount returns the number of distinct keys produced by merging a
// typed receiver with args. Keys are compared by their canonical hash key, so
// symbol, string, and other typed keys dedup exactly as the result map will --
// the loose receiver+sum(arg lens) bound over-counts every overlapping key. It
// caps its tracking set at limit (the quota's entry budget) so a doomed merge
// cannot allocate a large set before being rejected, mirroring mergedKeyCount
// for the legacy string-keyed path.
func typedMergedKeyCount(exec *Execution, receiver Value, args []Value, limit int) (int, error) {
	count := receiver.HashLen()
	if count > limit {
		return count, nil
	}
	seen := make(map[string]struct{}, count)
	for _, entry := range receiver.HashEntries() {
		if err := exec.step(); err != nil {
			return count, err
		}
		// The exact-union preflight canonicalizes every key; charge before
		// the copy, like every other canonicalization site.
		if err := exec.chargeValueKeySteps(entry.Key); err != nil {
			return count, err
		}
		key, err := canonicalHashKey(entry.Key)
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
			key, err := canonicalHashKey(entry.Key)
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
	if hashHasTypedEntries(receiver) {
		count := receiver.HashLen()
		delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(count, sortedHashEntryBufferBytes(count)))
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
		for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
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
		for _, key := range dropped {
			if _, _, err := hashDeleteKey(receiver, key); err != nil {
				return NewNil(), err
			}
		}
		return receiver, nil
	}
	entries := receiver.Hash()
	delta := exec.reserveLoopScratch(hashTransformBufferBytes(len(entries), sortedKeyBufferBytes(len(entries))))
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
	var keyBuf [smallHashKeyBufferSize]string
	for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		blockArgs[0] = NewSymbol(key)
		blockArgs[1] = entries[key]
		include, err := runner.call(blockArgs[:])
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkContext(); err != nil {
			return NewNil(), err
		}
		if include.Truthy() != keepTruthy {
			dropped = append(dropped, NewSymbol(key))
		}
	}
	for _, key := range dropped {
		if _, _, err := hashDeleteKey(receiver, key); err != nil {
			return NewNil(), err
		}
	}
	return receiver, nil
}

// hashMergeInPlace implements Ruby's Hash#merge! / Hash#update: it folds each
// argument's entries into the receiver in place, resolving key conflicts
// through the optional block (invoked with key, old value, new value), and
// returns the receiver. Argument entries land in insertion order (bare host
// maps contribute in sorted key order), so new keys append to the receiver's
// recorded order exactly as index assignment would.
func hashMergeInPlace(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value, name string) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("hash.%s does not accept keyword arguments", name)
	}
	for i, arg := range args {
		if arg.Kind() != KindHash && arg.Kind() != KindObject {
			return NewNil(), fmt.Errorf("hash.%s argument %d must be a hash", name, i+1)
		}
	}
	if len(args) == 0 {
		return receiver, nil
	}
	added := 0
	maxArgLen := 0
	for _, arg := range args {
		argLen := arg.HashLen()
		added = saturatingAdd(added, argLen)
		if argLen > maxArgLen {
			maxArgLen = argLen
		}
	}
	// The first typed write to a legacy receiver also materializes typed maps
	// for its existing entries (promotion), so charge those alongside the new
	// keys.
	if !hashHasTypedEntries(receiver) {
		added = saturatingAdd(added, receiver.HashLen())
	}
	// Charge the worst-case growth (every argument entry landing in a fresh
	// typed receiver slot) plus the sorted-entry scratch the deterministic walk
	// may allocate, before the receiver grows.
	if err := exec.checkProjectedTypedHashTransformBytes(added, sortedHashEntryBufferBytes(maxArgLen), receiver, args, kwargs, block); err != nil {
		return NewNil(), err
	}
	var runner *blockCallRunner
	if valueBlock(block) != nil {
		r, err := newBlockCallRunner(exec, block, "hash."+name, receiver, args, kwargs)
		if err != nil {
			return NewNil(), err
		}
		runner = r
	}
	var entryBuf [smallHashKeyBufferSize]HashEntry
	var blockArgs [3]Value
	for _, arg := range args {
		// The snapshot buffer also makes h.merge!(h) safe: the walk reads the
		// copied entries while the writes land in the receiver.
		for _, entry := range deterministicHashEntriesInto(arg, entryBuf[:]) {
			if err := exec.step(); err != nil {
				return NewNil(), err
			}
			if err := exec.chargeValueKeySteps(entry.Key); err != nil {
				return NewNil(), err
			}
			val := entry.Value
			if runner != nil {
				oldValue, conflict, err := hashGet(receiver, entry.Key)
				if err != nil {
					return NewNil(), err
				}
				if conflict {
					blockArgs[0] = entry.Key
					blockArgs[1] = oldValue
					blockArgs[2] = val
					merged, err := runner.call(blockArgs[:])
					if err != nil {
						return NewNil(), err
					}
					val = merged
				}
			}
			if err := hashSet(receiver, entry.Key, val); err != nil {
				return NewNil(), err
			}
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
			if hashHasTypedEntries(receiver) || anyTypedHash(args) {
				looseEntries := receiver.HashLen()
				maxArgLen := 0
				for _, arg := range args {
					argLen := arg.HashLen()
					looseEntries = saturatingAdd(looseEntries, argLen)
					if argLen > maxArgLen {
						maxArgLen = argLen
					}
				}
				scratchBytes := sortedHashEntryBufferBytes(maxArgLen)
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
					limit := exec.maxProjectedTypedHashEntries(scratchBytes, receiver, args, kwargs, block)
					projected, err := typedMergedKeyCount(exec, receiver, args, limit)
					if err != nil {
						return NewNil(), err
					}
					projectedEntries = projected
				default:
					// No block reservation lingers, so the loose bound is only an
					// up-front admission check. Try it first (non-allocating); only
					// when it exceeds the quota does overlap matter, so compute the
					// exact union (capped at the entry budget) before rejecting.
					if exec.checkProjectedTypedHashTransformBytes(looseEntries, scratchBytes, receiver, args, kwargs, block) != nil {
						limit := exec.maxProjectedTypedHashEntries(scratchBytes, receiver, args, kwargs, block)
						projected, err := typedMergedKeyCount(exec, receiver, args, limit)
						if err != nil {
							return NewNil(), err
						}
						projectedEntries = projected
					}
				}
				if err := exec.checkProjectedTypedHashTransformBytes(projectedEntries, scratchBytes, receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				out := newTypedHashPreservingDefault(receiver, projectedEntries)
				var runner *blockCallRunner
				var acc *hashBuildAccumulator
				if useBlock {
					delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(projectedEntries, scratchBytes))
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
				for _, entry := range deterministicHashEntriesInto(receiver, entryBuf[:]) {
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
					for _, entry := range deterministicHashEntriesInto(arg, entryBuf[:]) {
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
			}
			base := receiver.Hash()
			// Preflight the map this merge could materialize before allocating it.
			// The merge also materializes a sorted key scratch buffer sized to the
			// largest single argument (reused across arguments). Charge that scratch
			// in the projection so a merge whose union fits but whose largest input
			// dwarfs the quota cannot allocate the key list past the sandbox limit.
			// The receiver base is copied in map order, so it needs no scratch.
			scratchBytes := mergeSortScratchBytes(args)
			// projectedEntries records the output-map entry count the projection
			// charged, so the block accumulator below can reserve the identical
			// backing. The output map grows from len(base) up to the distinct union
			// as non-conflicting argument keys are inserted, so its peak backing is
			// the union -- not len(base).
			var projectedEntries int
			switch {
			case useBlock && exec.memoryQuota > 0:
				// The block accumulator reserves projectedEntries as the output map's
				// backing, and the real output grows to exactly the distinct union.
				// The loose upper bound (len(base)+sum(arg lens)) over-counts every
				// overlapping key, so reserving it would hold phantom slots the result
				// never allocates and let acc.add falsely reject a merge whose true
				// union plus block results fit the quota (h.merge(h) { ... }). Compute
				// the exact union up front so the projection and the reservation agree
				// on the backing the map will actually hold. mergedKeyCount caps its
				// deduplication set at the quota's entry budget (limit) so a doomed
				// merge cannot allocate a large tracking table before being rejected.
				// Only the memory-bounded block path takes this exact pre-walk: with no
				// memory quota nothing is reserved, so the cheap loose bound below is
				// used and the merge loop charges steps once, as before.
				limit := exec.maxProjectedHashEntries(scratchBytes, receiver, args, kwargs, block)
				projected, err := mergedKeyCount(exec, base, args, limit)
				if err != nil {
					return NewNil(), err
				}
				projectedEntries = projected
			default:
				// Reached without a block (no accumulator lingers to over-reserve) or
				// with no memory quota (nothing is reserved or checked), so
				// projectedEntries is only a cheap up-front admission bound. Two phases
				// keep the check itself within the quota it enforces: first try the
				// non-allocating loose upper bound (the receiver's keys plus every
				// argument's length, overlaps included). If even that fits the merge is
				// guaranteed to fit and no tracking set is needed. Only when the loose
				// bound exceeds the quota does the exact union matter, because overlap
				// (h.merge(h), repeated defaults) could still bring the real output
				// within the limit; mergedKeyCount then caps its deduplication set at the
				// quota's entry budget so a doomed merge cannot allocate a large tracking
				// table before being rejected. (With no memory quota the loose check
				// passes immediately and the exact union is never walked.)
				projectedEntries = looseMergedKeyUpperBound(base, args)
				if exec.checkProjectedHashTransformBytes(projectedEntries, scratchBytes, receiver, args, kwargs, block) != nil {
					limit := exec.maxProjectedHashEntries(scratchBytes, receiver, args, kwargs, block)
					projected, err := mergedKeyCount(exec, base, args, limit)
					if err != nil {
						return NewNil(), err
					}
					projectedEntries = projected
				}
			}
			if err := exec.checkProjectedHashTransformBytes(projectedEntries, scratchBytes, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(base))
			var runner *blockCallRunner
			var acc *hashBuildAccumulator
			if useBlock {
				// With a block, Ruby resolves conflicts by yielding
				// (key, old_value, new_value) and storing the block result; keys
				// present on only one side are copied without invoking the block.
				// Conflicting keys are visited in sorted order so block side
				// effects are deterministic, mirroring the other hash helpers.
				//
				// Reserve the output map and the sorted-key scratch for the build's
				// whole lifetime BEFORE building the runner, so the runner's bind-charge
				// baseline already includes them. The output map is preallocated with
				// make(map, len(base)) but grows as non-conflicting argument keys are
				// inserted, reaching the exact distinct union at peak, so reserve that
				// union (projectedEntries -- the same bound the up-front projection
				// charged). The exact union (not the loose len(base)+sum(arg lens) bound)
				// is reserved here so an overlapping merge whose true union fits is never
				// rejected by phantom slots the result map never allocates. Folding the
				// backing and scratch into the live baseline through reserveLoopScratch
				// (rather than only into the accumulator's running budget) means a
				// conflict block that destructures new_value into a rest-collecting
				// parameter (|k, o, (head, *tail)|) charges its fresh tail backing against
				// a baseline that already accounts for the output map and scratch, closing
				// the gap where receiver+out+scratch and receiver+tail each fit the quota
				// while the real peak receiver+out+scratch+tail exceeds it.
				delta := exec.reserveLoopScratch(hashTransformBufferBytes(projectedEntries, scratchBytes))
				defer exec.releaseLoopScratch(delta)
				// The argument hashes being merged in (args) live on the Go call stack
				// for the whole conflict loop, so pass them as the runner's positional
				// call roots: a conflict block that destructures a new_value into a
				// rest-collecting parameter copies part of an argument into a fresh
				// backing, and that copy must be charged against a baseline that already
				// counts the arguments it was copied from.
				r, err := newBlockCallRunner(exec, block, "hash."+name, receiver, args, kwargs)
				if err != nil {
					return NewNil(), err
				}
				runner = r
				// The conflict block can return a fresh heap value per collision,
				// and those results live only in the Go-local out map until merge
				// returns, so neither the structural projection above nor the call
				// roots can bound them. Charge each conflict result incrementally
				// through a build accumulator whose results-only estimator measures
				// the result's full footprint as it is produced. Only conflict
				// results pass through the accumulator: base and non-conflict
				// argument values are receiver/argument values already counted in
				// the call roots (acc.base) and the projection, so seeding them into
				// the estimator would mark their backings as seen and let a later
				// conflict block that mutates and returns one of them be dedup'd to
				// nothing -- an under-count that escapes the quota. Counting is
				// conservative: a key folded through many colliding arguments is
				// charged once per conflict write rather than dedup'd to a single
				// entry, so the bound stays sound even when a block mutates a
				// receiver-owned container in place and returns it (see the
				// accumulator's doc comment); the running total only grows, so it can
				// never drop below the live footprint. The output map and scratch are
				// already held against the quota by the reserveLoopScratch above (which
				// the accumulator's baseline reads through estimateMemoryUsageBase), so
				// the accumulator charges only the per-entry payloads beyond those slots.
				acc = newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			}
			// Copy the receiver entry by entry rather than with maps.Copy so a
			// merge over a large base charges a step per copied entry and honors
			// cancellation, matching the additions loop below and the other hash
			// transforms. The output map is order-independent. The base entries are
			// receiver values: their payloads are already counted in the call roots
			// (acc.base) and their output map slots in the up-front projection, so
			// they are never charged through the accumulator. Only block-returned
			// conflict results -- fresh payloads invisible to both -- go through
			// acc.add, which keeps the results-only estimator unseeded by receiver
			// backings so a conflict block that mutates and returns a base value is
			// charged at full size rather than dedup'd to nothing.
			for key, val := range base {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				out[key] = val
			}
			if len(args) == 0 {
				// Ruby's Hash#merge with no arguments returns a copy of self,
				// carrying the receiver's default metadata.
				return newHashPreservingDefault(receiver, out), nil
			}
			// Multiple hashes are applied left to right, so later arguments win
			// on conflicts, matching Ruby's Hash#merge(*others). The conflict
			// block sees the value accumulated so far as old_value, so a key
			// repeated across arguments folds through the block on each step.
			var blockArgs [3]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, arg := range args {
				addition := arg.Hash()
				for _, key := range sortedHashKeysInto(addition, keyBuf[:]) {
					// Charge a step per merged key so a large merge participates in
					// the step quota and honors cancellation; the block conflict
					// path also steps through runner.call below.
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					oldValue, conflict := out[key]
					if !conflict || !useBlock {
						// A non-conflict addition stores an argument value directly.
						// Its payload is already counted in the call roots (acc.base)
						// and its output map slot in the up-front projection, so it is
						// never charged through the results-only estimator -- seeding
						// the estimator with an argument backing would let a later
						// conflict block that mutates and returns that same value be
						// dedup'd to nothing and under-count its fresh payload.
						out[key] = addition[key]
						continue
					}
					blockArgs[0] = NewSymbol(key)
					blockArgs[1] = oldValue
					blockArgs[2] = addition[key]
					resolved, err := runner.call(blockArgs[:])
					if err != nil {
						return NewNil(), err
					}
					if err := exec.checkContext(); err != nil {
						return NewNil(), err
					}
					out[key] = resolved
					if err := acc.add(resolved); err != nil {
						return NewNil(), err
					}
				}
			}
			// Ruby's Hash#merge copies the receiver's default onto the result.
			return newHashPreservingDefault(receiver, out), nil
		}), nil
	case "update", "merge!":
		// Ruby's Hash#update / Hash#merge! fold the argument hashes into the
		// receiver in place (resolving conflicts through the optional block)
		// and return the receiver. AutoBuiltin so a parenless `hash.merge!`
		// invokes with zero arguments and is a no-op returning the receiver.
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return hashMergeInPlace(exec, receiver, args, kwargs, block, name)
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
			if err := exec.checkProjectedTypedHashTransformBytes(count, sortedHashEntryBufferBytes(count), receiver, args, kwargs, block); err != nil {
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
			entries := deterministicHashEntriesInto(args[0], entryBuf[:])
			hashClearEntries(receiver)
			// Pre-size the typed storage and order backing to the adopted entry
			// count so the rebuilt receiver holds exactly the slots the
			// projection charged, with no append-growth overshoot.
			receiver.ReserveTypedHashOrder(len(entries))
			for _, entry := range entries {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if err := hashSet(receiver, entry.Key, entry.Value); err != nil {
					return NewNil(), err
				}
			}
			// Adopt the replacement's default metadata, matching Ruby's
			// Hash#replace (initialize_copy) which copies the default too. An
			// object argument carries no defaults, so the receiver's are cleared.
			receiver.SetHashDefaults(hashDefaultValue(args[0]), hashDefaultProc(args[0]))
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
			if _, err := canonicalHashKey(args[0]); err != nil {
				return NewNil(), fmt.Errorf("hash.store key is unsupported hash key: %w", err)
			}
			// Ruby's Hash#store is index assignment: it writes the entry into
			// the receiver in place and returns the stored value. HashSet keeps
			// an existing key at its recorded position, preserving Ruby's
			// position-preserving store. Charge the potential growth before the
			// receiver takes it: one typed slot, plus the typed maps the first
			// write to a legacy receiver materializes during promotion.
			projected := 1
			if !hashHasTypedEntries(receiver) {
				projected = saturatingAdd(receiver.HashLen(), 1)
			}
			if err := exec.checkProjectedTypedHashBytes(projected, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			if err := hashSet(receiver, args[0], args[1]); err != nil {
				return NewNil(), fmt.Errorf("hash.store key is unsupported hash key: %w", err)
			}
			return args[1], nil
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
			if _, err := canonicalHashKey(args[0]); err != nil {
				return NewNil(), fmt.Errorf("hash.delete key is unsupported hash key: %w", err)
			}
			// Ruby's Hash#delete removes the entry from the receiver in place
			// and returns the removed value. The removal keeps the surviving
			// entries in their recorded insertion order and allocates nothing.
			removed, existed, err := hashDeleteKey(receiver, args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.delete key is unsupported hash key: %w", err)
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
			// Ruby's Hash#clear empties the receiver in place, keeps its default
			// metadata, and returns the receiver.
			hashClearEntries(receiver)
			return receiver, nil
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
			if hashHasTypedEntries(receiver) {
				projected := min(len(args), receiver.HashLen())
				if err := exec.checkProjectedTypedHashBytes(projected, receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				out := newTypedResultHash(projected)
				for _, arg := range args {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					if err := exec.chargeValueKeySteps(arg); err != nil {
						return NewNil(), err
					}
					value, ok, err := hashGet(receiver, arg)
					if err != nil || !ok {
						continue
					}
					if err := hashSet(out, arg, value); err != nil {
						return NewNil(), err
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Preflight the map slice could materialize before reserving it. The
			// output holds at most one entry per requested key and never more than
			// the receiver has, so the worst case is min(len(args), len(entries)) --
			// missing and duplicate candidate keys collapse. Reserving the backing
			// map at len(args) would let a huge candidate-key list allocate past the
			// quota even when the receiver (and result) is tiny, before the
			// statement-level check could observe it.
			projected := min(len(args), len(entries))
			if err := exec.checkProjectedHashBytes(projected, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, projected)
			for _, arg := range args {
				// Charge a step per requested key so slicing with many candidate
				// keys participates in the step quota and honors cancellation.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// Vibescript hash keys are only symbols or strings, so an
				// unsupported argument can never match an entry. Ruby's
				// Hash#slice omits candidate keys that are absent, so we
				// treat those arguments as misses rather than raising.
				key, err := valueToHashKey(arg)
				if err != nil {
					continue
				}
				if value, ok := entries[key]; ok {
					out[key] = value
				}
			}
			return NewHash(out), nil
		}), nil
	case "except":
		// AutoBuiltin so a parenless `hash.except` invokes with zero arguments
		// and returns a copy of the receiver, matching Ruby where the call has
		// no parentheses distinction. Explicit `except(...)` calls still pass
		// their excluded keys through the normal call path.
		return NewAutoBuiltin("hash.except", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				exclusionEntries := min(len(args), count)
				if err := exec.checkProjectedTypedHashTransformBytes(count, typedExclusionSetBytes(exclusionEntries), receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				retainedKeyPayloadDelta := 0
				defer func() {
					if retainedKeyPayloadDelta > 0 {
						exec.releaseLoopScratch(retainedKeyPayloadDelta)
					}
				}()
				var excluded map[HashLookupKey]struct{}
				for _, arg := range args {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					if err := exec.chargeValueKeySteps(arg); err != nil {
						return NewNil(), err
					}
					if _, ok, err := hashGet(receiver, arg); err != nil || !ok {
						continue
					}
					key, err := hashLookupKey(arg)
					if err != nil {
						continue
					}
					if excluded == nil {
						excluded = make(map[HashLookupKey]struct{})
					}
					if _, exists := excluded[key]; !exists {
						delta := exec.reserveLoopScratch(key.ExtraPayloadBytes())
						retainedKeyPayloadDelta = saturatingAdd(retainedKeyPayloadDelta, delta)
						if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
							return NewNil(), err
						}
					}
					excluded[key] = struct{}{}
				}
				if retainedKeyPayloadDelta > 0 {
					if err := exec.checkProjectedTypedHashTransformBytes(count, typedExclusionSetBytes(exclusionEntries), receiver, args, kwargs, block); err != nil {
						return NewNil(), err
					}
				}
				out := newTypedResultHash(count)
				for _, entry := range receiver.HashEntries() {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					key, err := hashLookupKey(entry.Key)
					if err != nil {
						return NewNil(), err
					}
					if _, skip := excluded[key]; skip {
						continue
					}
					if err := hashSet(out, entry.Key, entry.Value); err != nil {
						return NewNil(), err
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Preflight the largest map except could materialize before reserving
			// anything. Excluded keys absent from the receiver leave the full input
			// in place, so the worst case is a copy of every entry. Checking this
			// first means a tiny receiver paired with a huge candidate-key list
			// fails fast on the output bound rather than after allocating and
			// scanning a set proportional to the argument count.
			//
			// The exclusion set is live alongside the output copy at peak: it holds
			// the candidate keys present in the receiver (at most one per receiver
			// entry, and never more than the argument count), so charge its footprint
			// here too. Without it h.except(*h.keys) over a large receiver could
			// allocate the full set plus the full output past a receiver+output quota,
			// with the set gone before the post-call check could observe the peak.
			exclusionEntries := min(len(args), len(entries))
			if err := exec.checkProjectedHashTransformBytes(len(entries), exclusionSetBytes(exclusionEntries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// Build the exclusion set from candidate keys that actually appear in
			// the receiver. Only present keys affect the result, so the set is
			// bounded by the receiver's size, never the argument count: a huge
			// candidate list against a tiny receiver cannot grow a set past the
			// output the projection already admitted. A step per candidate keeps
			// the scan CPU-bounded and observing cancellation before the copy loop.
			var excluded map[string]struct{}
			for _, arg := range args {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// Vibescript hash keys are only symbols or strings, so an
				// unsupported argument can never match an entry. Ruby's
				// Hash#except ignores keys that are not present, so we treat
				// those arguments as misses rather than raising.
				key, err := valueToHashKey(arg)
				if err != nil {
					continue
				}
				if _, present := entries[key]; !present {
					continue
				}
				if excluded == nil {
					excluded = make(map[string]struct{})
				}
				excluded[key] = struct{}{}
			}
			out := make(map[string]Value, len(entries))
			for key, value := range entries {
				// Charge a step per surviving-candidate entry so excepting a large
				// hash participates in the step quota and honors cancellation.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if _, skip := excluded[key]; skip {
					continue
				}
				out[key] = value
			}
			return NewHash(out), nil
		}), nil
	case "select":
		return NewAutoBuiltin("hash.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.select does not take arguments")
			}
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(count, sortedHashEntryBufferBytes(count)))
				defer exec.releaseLoopScratch(delta)
				runner, err := newBlockCallRunner(exec, block, "hash.select", receiver, nil, kwargs)
				if err != nil {
					return NewNil(), err
				}
				defer exec.beginBlockIterationRegion().end()
				if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				out := newTypedResultHash(count)
				var blockArgs [2]Value
				var entryBuf [smallHashKeyBufferSize]HashEntry
				// Compact the kept entries to the front of the buffer and populate the
				// result hash after the loop; see hash.transform_values for why the
				// in-loop hashSet had to go. The write index never runs ahead of the
				// read index, so the compaction is safe in place and adds no
				// allocation.
				ordered := orderedTypedHashEntriesInto(receiver, entryBuf[:])
				deferBuild := hashEntryKeysAreStable(ordered)
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
						if !deferBuild {
							if err := hashSet(out, ordered[i].Key, ordered[i].Value); err != nil {
								return NewNil(), err
							}
							continue
						}
						ordered[kept] = ordered[i]
						kept++
					}
				}
				if deferBuild {
					for _, entry := range ordered[:kept] {
						if err := hashSet(out, entry.Key, entry.Value); err != nil {
							return NewNil(), err
						}
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Reserve the output map and sorted-key scratch for the walk's whole
			// lifetime BEFORE building the runner, so the runner's bind-charge baseline
			// already includes them. The block may keep every entry, so project the
			// full input. Reserving first (rather than only preflighting the buffers
			// separately) folds them into the live baseline every in-body check, the
			// preflight, and the per-call bind charge all see -- so a rest-collecting
			// destructure block (|k, (head, *tail)|) charges its fresh tail backing
			// against a baseline that already accounts for the output map and scratch,
			// closing the gap where receiver+out+scratch and receiver+tail each fit the
			// quota while the real peak receiver+out+scratch+tail exceeds it.
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(len(entries), sortedKeyBufferBytes(len(entries))))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.select", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// The loop below writes only builtin-local state (the out map), which is
			// unreachable from any root until this builtin returns, so the region's
			// vouch holds: everything reachable it touches goes through the block, whose
			// own writes bump or are re-walked fresh. Without it a host-built (untyped)
			// receiver falls to the builtin bypass and re-walks the whole hash on every
			// check, which is quadratic in the entry count.
			defer exec.beginBlockIterationRegion().end()
			out := make(map[string]Value, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call charges no step for a
				// blockless body, so without this an empty select would scan the whole
				// hash uncharged.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
				include, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				if include.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
		}), nil
	case "reject":
		return NewAutoBuiltin("hash.reject", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.reject does not take arguments")
			}
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(count, sortedHashEntryBufferBytes(count)))
				defer exec.releaseLoopScratch(delta)
				runner, err := newBlockCallRunner(exec, block, "hash.reject", receiver, nil, kwargs)
				if err != nil {
					return NewNil(), err
				}
				defer exec.beginBlockIterationRegion().end()
				if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				out := newTypedResultHash(count)
				var blockArgs [2]Value
				var entryBuf [smallHashKeyBufferSize]HashEntry
				// Compact the kept entries to the front of the buffer and populate the
				// result hash after the loop; see hash.transform_values for why the
				// in-loop hashSet had to go. The write index never runs ahead of the
				// read index, so the compaction is safe in place and adds no
				// allocation.
				ordered := orderedTypedHashEntriesInto(receiver, entryBuf[:])
				deferBuild := hashEntryKeysAreStable(ordered)
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
						if !deferBuild {
							if err := hashSet(out, ordered[i].Key, ordered[i].Value); err != nil {
								return NewNil(), err
							}
							continue
						}
						ordered[kept] = ordered[i]
						kept++
					}
				}
				if deferBuild {
					for _, entry := range ordered[:kept] {
						if err := hashSet(out, entry.Key, entry.Value); err != nil {
							return NewNil(), err
						}
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Reserve the output map and sorted-key scratch for the walk's whole
			// lifetime BEFORE building the runner, so the runner's bind-charge baseline
			// already includes them. The block may keep every entry, so project the
			// full input. Reserving first (rather than only preflighting the buffers
			// separately) folds them into the live baseline every in-body check, the
			// preflight, and the per-call bind charge all see -- so a rest-collecting
			// destructure block (|k, (head, *tail)|) charges its fresh tail backing
			// against a baseline that already accounts for the output map and scratch,
			// closing the gap where receiver+out+scratch and receiver+tail each fit the
			// quota while the real peak receiver+out+scratch+tail exceeds it.
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(len(entries), sortedKeyBufferBytes(len(entries))))
			defer exec.releaseLoopScratch(delta)
			runner, err := newBlockCallRunner(exec, block, "hash.reject", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			// The loop below writes only builtin-local state (the out map), which is
			// unreachable from any root until this builtin returns, so the region's
			// vouch holds: everything reachable it touches goes through the block, whose
			// own writes bump or are re-walked fresh. Without it a host-built (untyped)
			// receiver falls to the builtin bypass and re-walks the whole hash on every
			// check, which is quadratic in the entry count.
			defer exec.beginBlockIterationRegion().end()
			out := make(map[string]Value, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call charges no step for a
				// blockless body, so without this an empty reject would scan the whole
				// hash uncharged.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
				exclude, err := runner.call(blockArgs[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				if !exclude.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
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
			retained.reserve(arraySlotBackingBytes(hashEntryCount(receiver)))

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
			if hashHasTypedEntries(receiver) {
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
				for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
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
						val, err = runner.call(blockArg[:])
					} else {
						blockArgs[0] = entry.Key
						blockArgs[1] = entry.Value
						val, err = runner.call(blockArgs[:])
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
				return NewArray(out), nil
			}
			entries := receiver.Hash()
			// map keeps an arbitrary block result per entry, so charge the
			// growing result incrementally rather than only after the call: a block
			// returning an individually quota-sized value per entry could otherwise pile
			// up past the quota before the post-call check ran. The accumulator's
			// baseline includes the live receiver and block (held on the Go stack during
			// the call), and reserveScratch folds in the sorted key list that stays live
			// for the whole build so it is charged alongside the accumulating result.
			if err := acc.reserveScratch(sortedKeyBufferBytes(len(entries))); err != nil {
				return NewNil(), err
			}
			// Reject the build before reserving the backing slice when its len(entries)
			// slots would already overflow the quota on top of the baseline and the
			// sorted key scratch just reserved. map keeps one result per entry, so the
			// backing reaches len(entries) Value slots regardless of the block; make
			// would otherwise reserve all of them as a Go-local slice (invisible to the
			// quota) before the first acc.add projected cap(out), letting a large hash
			// transiently allocate a full result backing that should have been rejected
			// up front, mirroring array.map.
			if err := acc.reserveSlots(len(entries)); err != nil {
				return NewNil(), err
			}
			out := make([]Value, 0, len(entries))
			// Reserve the preallocated backing before the first block call; see
			// the typed branch above.
			retained.reserve(acc.accumulatedBytes(cap(out)))
			var blockArg [1]Value
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call charges no step for a
				// blockless body.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// Entries follow the deterministic sorted key order every
				// block-based hash iterator uses.
				var (
					val Value
					err error
				)
				if collapsePair {
					pair := NewArray([]Value{NewSymbol(key), entries[key]})
					// Charge the fresh pair against the quota before yielding: it is
					// live alongside the accumulating result for the block's duration,
					// so without this a block whose result fits but whose live pair does
					// not could run past the sandbox memory limit. Mirrors
					// hash.each_with_index, which charges its yielded pair the same way.
					// cap(out) is the result backing before this iteration's append, so
					// the peak charges the pair on top of the result built so far.
					if err = acc.checkTransient(pair, cap(out)); err != nil {
						return NewNil(), err
					}
					blockArg[0] = pair
					val, err = runner.call(blockArg[:])
				} else {
					// A block taking key and value separately needs no pair, so none
					// is built and none is charged.
					blockArgs[0] = NewSymbol(key)
					blockArgs[1] = entries[key]
					val, err = runner.call(blockArgs[:])
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
			return NewArray(out), nil
		}), nil
	case "map_with_index":
		return NewAutoBuiltin("hash.map_with_index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.map_with_index does not take arguments")
			}
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("hash.map_with_index does not take keyword arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.map_with_index", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			defer exec.beginBlockIterationRegion().end()
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
				if err := acc.reserveScratch(sortedHashEntryBufferBytes(count)); err != nil {
					return NewNil(), err
				}
				if err := acc.reserveSlots(count); err != nil {
					return NewNil(), err
				}
				out := make([]Value, 0, count)
				var blockArgs [2]Value
				var entryBuf [smallHashKeyBufferSize]HashEntry
				for i, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					pair := NewArray([]Value{entry.Key, entry.Value})
					if err := acc.checkTransient(pair, cap(out)); err != nil {
						return NewNil(), err
					}
					blockArgs[0] = pair
					blockArgs[1] = NewInt(int64(i))
					val, err := runner.call(blockArgs[:])
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
				}
				return NewArray(out), nil
			}
			entries := receiver.Hash()
			// map_with_index keeps an arbitrary block result per entry, so charge the
			// growing result incrementally rather than only after the call: a block
			// returning an individually quota-sized value per entry could otherwise pile
			// up past the quota before the post-call check ran. The accumulator's
			// baseline includes the live receiver and block (held on the Go stack during
			// the call), and reserveScratch folds in the sorted key list that stays live
			// for the whole build so it is charged alongside the accumulating result.
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			if err := acc.reserveScratch(sortedKeyBufferBytes(len(entries))); err != nil {
				return NewNil(), err
			}
			// Reject the build before reserving the backing slice when its len(entries)
			// slots would already overflow the quota on top of the baseline and the
			// sorted key scratch just reserved. map keeps one result per entry, so the
			// backing reaches len(entries) Value slots regardless of the block; make
			// would otherwise reserve all of them as a Go-local slice (invisible to the
			// quota) before the first acc.add projected cap(out), letting a large hash
			// transiently allocate a full result backing that should have been rejected
			// up front, mirroring array.map_with_index.
			if err := acc.reserveSlots(len(entries)); err != nil {
				return NewNil(), err
			}
			out := make([]Value, 0, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for i, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call charges no step for a
				// blockless body.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				// Ruby yields the [key, value] pair as the first block parameter and the
				// index as the second; the index follows the deterministic sorted key
				// order Vibescript uses for every block-based hash iterator.
				pair := NewArray([]Value{NewSymbol(key), entries[key]})
				// Charge the fresh pair against the quota before yielding: it is live
				// alongside the accumulating result for the block's duration, so without
				// this a block whose result fits but whose live pair does not could run
				// past the sandbox memory limit. Mirrors hash.each_with_index, which
				// charges its yielded pair the same way. cap(out) is the result backing
				// before this iteration's append, so the peak charges the pair on top of
				// the result built so far.
				if err := acc.checkTransient(pair, cap(out)); err != nil {
					return NewNil(), err
				}
				blockArgs[0] = pair
				blockArgs[1] = NewInt(int64(i))
				val, err := runner.call(blockArgs[:])
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
			}
			return NewArray(out), nil
		}), nil
	case "transform_keys":
		return NewAutoBuiltin("hash.transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_keys does not take arguments")
			}
			if err := ensureBlock(block, "hash.transform_keys"); err != nil {
				return NewNil(), err
			}
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				scratch := sortedHashEntryBufferBytes(count)
				delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(count, scratch))
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
				out := newTypedResultHash(count)
				var blockArg [1]Value
				var entryBuf [smallHashKeyBufferSize]HashEntry
				// Defer the insertions past the last block call so the block region's
				// memoized prefix survives the loop; see hash.transform_values. Unlike
				// the key-preserving drivers this cannot decide up front whether
				// deferring is safe, because the block produces the keys as it goes: an
				// array key can be mutated in place after its entry is processed, and a
				// deferred insert would then canonicalize it in its final state. So the
				// buffered entries are flushed and the loop falls back to inserting
				// inline the moment the block yields one. Flushing in order keeps both
				// the result's insertion order and its last-writer-wins collisions
				// identical to inserting throughout.
				ordered := orderedTypedHashEntriesInto(receiver, entryBuf[:])
				deferBuild := true
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
					lookupKey, err := hashLookupKey(nextKey)
					if err != nil {
						return NewNil(), fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
					}
					resolved := hashDisplayKey(nextKey)
					if deferBuild && nextKey.Kind() == KindArray {
						for _, entry := range ordered[:buffered] {
							if err := hashSet(out, entry.Key, entry.Value); err != nil {
								return NewNil(), fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
							}
						}
						deferBuild = false
					}
					if deferBuild {
						// buffered never runs ahead of i, so this reuses the already
						// reserved entry buffer without disturbing an unread entry.
						ordered[buffered] = HashEntry{Key: nextKey, Value: ordered[i].Value}
						buffered++
					} else if err := hashSet(out, nextKey, ordered[i].Value); err != nil {
						return NewNil(), fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
					}
					if err := acc.addTypedSynthesizedKey(nextKey, resolved, lookupKey); err != nil {
						return NewNil(), err
					}
				}
				if deferBuild {
					for _, entry := range ordered[:buffered] {
						if err := hashSet(out, entry.Key, entry.Value); err != nil {
							return NewNil(), fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
						}
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Reserve the output map and sorted-key scratch for the build's whole
			// lifetime BEFORE building the runner, so the runner's bind-charge baseline
			// already includes them. transform_keys produces at most one entry per input
			// key, and its make(map, len(entries)) backing is live before the first block
			// runs, so reserve the full input. Folding the backing and scratch into the
			// live baseline through reserveLoopScratch (rather than only into the
			// accumulator's running budget) means a rest-collecting destructure block
			// charges its fresh backing against a baseline that already accounts for the
			// output map and scratch, closing the gap where receiver+out+scratch and
			// receiver+rest each fit the quota while the real peak exceeds it.
			scratch := sortedKeyBufferBytes(len(entries))
			delta := exec.reserveLoopScratch(legacyTransformKeysBufferBytes(len(entries), scratch))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_keys", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			// With the insertions deferred below, the loop writes only builtin-local
			// state, so the region's vouch holds and a host-built receiver stops
			// falling to the builtin bypass. This branch was left region-less when the
			// other legacy hash drivers gained one, because a region cannot help while
			// the loop bumps the mutation epoch on every insertion.
			defer exec.beginBlockIterationRegion().end()
			// The block can return a fresh typed key per entry, and those synthesized
			// keys live only in the Go-local out map until the builtin returns, so the
			// structural reservation above cannot bound their payloads. Charge each
			// synthesized key incrementally through the typed-key accumulator: the value
			// stays a receiver value already counted in the call roots, so charging it
			// through the estimator would record its backing as seen and risk dedup'ing a
			// later block result to nothing. Counting is conservative: a block that
			// collapses several input keys onto one output key is charged once per write
			// rather than dedup'd to a single entry, an over-count that keeps the bound
			// sound. The output maps and scratch are already held against the quota by the
			// reserveLoopScratch above, so the accumulator charges only per-entry payloads
			// beyond those slots.
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			out := NewHash(make(map[string]Value, len(entries)))
			out.ReserveHashOrder(len(entries))
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			var producedBuf [smallHashKeyBufferSize]HashEntry
			sorted := sortedHashKeysInto(entries, keyBuf[:])
			// Defer the insertions past the last block call so the region's memoized
			// prefix survives the loop, and fall back the moment the block yields an
			// array key -- the only mutable key kind, whose identity a deferred insert
			// would canonicalize in its final state rather than the state it had when
			// its entry was processed. This mirrors the typed branch; the difference is
			// that there is no entry buffer to reuse here, so the produced keys get
			// their own buffer, holding each key with the value its entry contributed
			// at the moment the block ran.
			// The buffer is reserved and allocated on first use rather than up front,
			// because a block that yields an array key falls back on its very first
			// entry and never buffers anything. Charging for storage that run never
			// allocates is the false-rejection direction: it fails scripts that
			// previously fit their quota.
			produced := producedBuf[:0]
			producedDelta := 0
			defer func() { exec.releaseLoopScratch(producedDelta) }()
			reserveProduced := func() error {
				if cap(produced) >= len(sorted) {
					return nil
				}
				delta := exec.reserveLoopScratch(sortedHashEntryBufferBytes(len(sorted)))
				producedDelta += delta
				if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
					return err
				}
				// The accumulator snapshotted its baseline before this reservation
				// existed, so fold the buffer in or its per-entry checks would weigh
				// synthesized key payloads against a baseline that omits it.
				if err := acc.reserveScratch(delta); err != nil {
					return err
				}
				grown := make([]HashEntry, len(produced), len(sorted))
				copy(grown, produced)
				produced = grown
				return nil
			}
			flush := func() error {
				for _, entry := range produced {
					if err := hashSet(out, entry.Key, entry.Value); err != nil {
						return fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
					}
				}
				produced = produced[:0]
				return nil
			}
			deferBuild := true
			for _, key := range sorted {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call charges no step for a
				// blockless body.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = NewSymbol(key)
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
				lookupKey, err := hashLookupKey(nextKey)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
				}
				resolved := hashDisplayKey(nextKey)
				if deferBuild && nextKey.Kind() == KindArray {
					// produced holds exactly sorted[:i] at this point, so flushing in
					// order reproduces the insertion sequence an inline build made.
					if err := flush(); err != nil {
						return NewNil(), err
					}
					deferBuild = false
				}
				if deferBuild {
					if err := reserveProduced(); err != nil {
						return NewNil(), err
					}
					// The value is captured here, not at flush time: a block that
					// mutates or deletes an already-processed entry of the receiver
					// must not change the value this entry contributes, which is what
					// inserting inline gave and what the typed branch gets from its
					// snapshotted entries.
					produced = append(produced, HashEntry{Key: nextKey, Value: entries[key]})
				} else if err := hashSet(out, nextKey, entries[key]); err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block returned unsupported hash key: %w", err)
				}
				if err := acc.addTypedSynthesizedKey(nextKey, resolved, lookupKey); err != nil {
					return NewNil(), err
				}
			}
			if deferBuild {
				if err := flush(); err != nil {
					return NewNil(), err
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
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				if err := exec.checkProjectedTypedHashTransformBytes(count, sortedHashEntryBufferBytes(count), receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				out := newTypedResultHash(count)
				var entryBuf [smallHashKeyBufferSize]HashEntry
				for _, entry := range orderedTypedHashEntriesInto(receiver, entryBuf[:]) {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					if mapped, ok, err := hashGet(args[0], entry.Key); err != nil {
						return NewNil(), fmt.Errorf("hash.remap_keys mapping key is unsupported hash key: %w", err)
					} else if ok {
						if err := exec.chargeValueKeySteps(mapped); err != nil {
							return NewNil(), err
						}
						if _, err := valueToHashKey(mapped); err != nil {
							return NewNil(), fmt.Errorf("hash.remap_keys mapping value is unsupported hash key: %w", err)
						}
						if err := hashSet(out, mapped, entry.Value); err != nil {
							return NewNil(), fmt.Errorf("hash.remap_keys mapping value is unsupported hash key: %w", err)
						}
						continue
					}
					if err := hashSet(out, entry.Key, entry.Value); err != nil {
						return NewNil(), err
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			mapping := args[0].Hash()
			// Preflight the output map plus the sorted key scratch buffer before
			// reserving either; remap_keys produces one entry per input key (renamed
			// or kept), so project the full input.
			if err := exec.checkProjectedHashTransformBytes(len(entries), sortedKeyBufferBytes(len(entries)), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := NewHash(make(map[string]Value, len(entries)))
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per remapped key so remapping a large hash
				// participates in the step quota and honors cancellation.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				value := entries[key]
				if mapped, ok := mapping[key]; ok {
					if err := exec.chargeValueKeySteps(mapped); err != nil {
						return NewNil(), err
					}
					if _, err := valueToHashKey(mapped); err != nil {
						return NewNil(), fmt.Errorf("hash.remap_keys mapping value is unsupported hash key: %w", err)
					}
					if err := hashSet(out, mapped, value); err != nil {
						return NewNil(), fmt.Errorf("hash.remap_keys mapping value is unsupported hash key: %w", err)
					}
					continue
				}
				setClonedHashEntry(out, NewString(key), value)
			}
			return out, nil
		}), nil
	case "transform_values":
		return NewAutoBuiltin("hash.transform_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_values does not take arguments")
			}
			if err := ensureBlock(block, "hash.transform_values"); err != nil {
				return NewNil(), err
			}
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				scratch := sortedHashEntryBufferBytes(count)
				delta := exec.reserveLoopScratch(typedHashTransformBufferBytes(count, scratch))
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
				out := newTypedResultHash(count)
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
				ordered := orderedTypedHashEntriesInto(receiver, entryBuf[:])
				deferBuild := hashEntryKeysAreStable(ordered)
				for i := range ordered {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					blockArg[0] = ordered[i].Value
					nextValue, err := runner.call(blockArg[:])
					if err != nil {
						return NewNil(), err
					}
					if err := exec.checkContext(); err != nil {
						return NewNil(), err
					}
					ordered[i].Value = nextValue
					if !deferBuild {
						if err := hashSet(out, ordered[i].Key, nextValue); err != nil {
							return NewNil(), err
						}
					}
					if err := acc.add(nextValue); err != nil {
						return NewNil(), err
					}
				}
				if deferBuild {
					for _, entry := range ordered {
						if err := hashSet(out, entry.Key, entry.Value); err != nil {
							return NewNil(), err
						}
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Reserve the output map and sorted-key scratch for the build's whole
			// lifetime BEFORE building the runner, so the runner's bind-charge baseline
			// already includes them. transform_values keeps every key, and its
			// make(map, len(entries)) backing is live before the first block runs, so
			// reserve the full input. Folding the backing and scratch into the live
			// baseline through reserveLoopScratch (rather than only into the accumulator's
			// running budget) means a rest-collecting destructure block charges its fresh
			// backing against a baseline that already accounts for the output map and
			// scratch, closing the gap where receiver+out+scratch and receiver+rest each
			// fit the quota while the real peak exceeds it.
			scratch := sortedKeyBufferBytes(len(entries))
			delta := exec.reserveLoopScratch(hashTransformBufferBytes(len(entries), scratch))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkReservedLoopScratch(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_values", receiver, nil, kwargs)
			if err != nil {
				return NewNil(), err
			}
			// The loop below writes only builtin-local state (the out map), which is
			// unreachable from any root until this builtin returns, so the region's
			// vouch holds: everything reachable it touches goes through the block, whose
			// own writes bump or are re-walked fresh. Without it a host-built (untyped)
			// receiver falls to the builtin bypass and re-walks the whole hash on every
			// check, which is quadratic in the entry count.
			defer exec.beginBlockIterationRegion().end()
			// The block can return a fresh heap value per entry, and those results live
			// only in the Go-local out map until the builtin returns, so the structural
			// reservation above cannot bound them. Charge each result incrementally
			// through a build accumulator whose results-only estimator counts each block
			// result's full footprint as it is produced, so accumulated payloads count
			// toward the quota during the loop, not only at the post-call check. Counting
			// is conservative: a block that returns a value unchanged and shared with the
			// receiver is counted again rather than dedup'd against the baseline, an
			// over-count that keeps the bound sound even when a block mutates a
			// receiver-owned container in place and returns it. The output map and scratch
			// are already held against the quota by the reserveLoopScratch above (which
			// the accumulator's baseline reads through estimateMemoryUsageBase), so the
			// accumulator charges only the per-entry payloads beyond those slots.
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			out := make(map[string]Value, len(entries))
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per entry so an empty block still consumes the step
				// quota and observes cancellation; runner.call charges no step for a
				// blockless body.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				blockArg[0] = entries[key]
				nextValue, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				if err := exec.checkContext(); err != nil {
					return NewNil(), err
				}
				out[key] = nextValue
				if err := acc.add(nextValue); err != nil {
					return NewNil(), err
				}
			}
			return NewHash(out), nil
		}), nil
	case "compact":
		return NewAutoBuiltin("hash.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.compact does not take arguments")
			}
			if hashHasTypedEntries(receiver) {
				count := receiver.HashLen()
				if err := exec.checkProjectedTypedHashBytes(count, receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
				out := newTypedResultHash(count)
				for _, entry := range receiver.HashEntries() {
					if err := exec.step(); err != nil {
						return NewNil(), err
					}
					if entry.Value.Kind() != KindNil {
						if err := hashSet(out, entry.Key, entry.Value); err != nil {
							return NewNil(), err
						}
					}
				}
				return out, nil
			}
			entries := receiver.Hash()
			// Preflight the largest map compact could keep before reserving it; a
			// hash with no nil values keeps every entry, so project the full input.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(entries))
			for k, v := range entries {
				// Charge a step per inspected entry so compacting a large hash
				// participates in the step quota and honors cancellation.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				if v.Kind() != KindNil {
					out[k] = v
				}
			}
			return NewHash(out), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

// hashEntryCount reports how many entries a hash receiver holds, for either
// storage shape.
func hashEntryCount(receiver Value) int {
	if hashHasTypedEntries(receiver) {
		return receiver.HashLen()
	}
	return len(receiver.Hash())
}
