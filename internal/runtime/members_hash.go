package runtime

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
)

// hashMemberNames mirrors the names dispatched by hashMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var hashMemberNames = []string{
	"size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_key", "each_value", "to_a", "default", "default_proc",
	"merge", "update", "merge!", "replace", "store", "slice", "except", "flatten", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact",
	"inspect",
}

var hashBuiltinMembers = newMemberTable(hashMemberNames)

// Most script hashes are small records/options; larger maps fall back to heap.
const smallHashKeyBufferSize = 8

func hashMember(obj Value, property string) (Value, error) {
	if member, ok := hashBuiltinMembers.lookup(property, hashMemberBuiltin); ok {
		return member, nil
	}
	candidates := slices.AppendSeq(slices.Clone(hashMemberNames), maps.Keys(obj.Hash()))
	return NewNil(), fmt.Errorf("unknown hash method %s%s", property, didYouMean(property, candidates))
}

func hashMemberBuiltin(property string) (Value, error) {
	switch property {
	case "size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_key", "each_value", "to_a", "default", "default_proc":
		return hashMemberQuery(property)
	case "merge", "update", "merge!", "replace", "store", "slice", "except", "flatten", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact":
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
// proc error with the index expression's position for a precise diagnostic.
func (exec *Execution) hashMissingKeyDefault(receiver, key Value, pos Position) (Value, error) {
	result, err := exec.hashDefaultForKey(receiver, key)
	if err != nil {
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
	sort.Strings(keys)
	return keys
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

func deepTransformKeys(exec *Execution, value, block Value) (Value, error) {
	return deepTransformKeysWithState(exec, value, block, &deepTransformState{
		seenHashes: make(map[uintptr]struct{}),
		seenArrays: make(map[uintptr]struct{}),
	})
}

type deepTransformState struct {
	seenHashes map[uintptr]struct{}
	seenArrays map[uintptr]struct{}
}

func deepTransformKeysWithState(exec *Execution, value, block Value, state *deepTransformState) (Value, error) {
	switch value.Kind() {
	case KindHash, KindObject:
		entries := value.Hash()
		id := reflect.ValueOf(entries).Pointer()
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not support cyclic structures")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}
		out := make(map[string]Value, len(entries))
		var blockArg [1]Value
		var keyBuf [smallHashKeyBufferSize]string
		for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
			blockArg[0] = NewSymbol(key)
			nextKeyValue, err := exec.CallBlock(block, blockArg[:])
			if err != nil {
				return NewNil(), err
			}
			nextKey, err := valueToHashKey(nextKeyValue)
			if err != nil {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys block must return symbol or string")
			}
			nextValue, err := deepTransformKeysWithState(exec, entries[key], block, state)
			if err != nil {
				return NewNil(), err
			}
			out[nextKey] = nextValue
		}
		return NewHash(out), nil
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
		out := make([]Value, len(items))
		for i, item := range items {
			nextValue, err := deepTransformKeysWithState(exec, item, block, state)
			if err != nil {
				return NewNil(), err
			}
			out[i] = nextValue
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
			return NewInt(int64(len(receiver.Hash()))), nil
		}), nil
	case "length":
		return NewAutoBuiltin("hash.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.length does not take arguments")
			}
			return NewInt(int64(len(receiver.Hash()))), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("hash.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.empty? does not take arguments")
			}
			return NewBool(len(receiver.Hash()) == 0), nil
		}), nil
	case "key?", "has_key?", "member?", "include?":
		name := property
		return NewAutoBuiltin("hash."+name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("hash.%s expects exactly one key", name)
			}
			// Ruby's membership predicates accept any object as the candidate
			// key and report false when it is absent. Vibescript only stores
			// symbol/string keys, so an unsupported candidate type can never be
			// present and is reported as a non-member rather than a type error.
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewBool(false), nil
			}
			_, ok := receiver.Hash()[key]
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
			// Vibescript mirrors this with Value.Equal so deep collection and
			// scalar equality match Ruby's hash value membership semantics.
			for _, stored := range receiver.Hash() {
				if stored.Equal(args[0]) {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "keys":
		return NewAutoBuiltin("hash.keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.keys does not take arguments")
			}
			entries := receiver.Hash()
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			values := make([]Value, len(keys))
			for i, k := range keys {
				values[i] = NewSymbol(k)
			}
			return NewArray(values), nil
		}), nil
	case "values":
		return NewAutoBuiltin("hash.values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.values does not take arguments")
			}
			entries := receiver.Hash()
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			values := make([]Value, len(keys))
			for i, k := range keys {
				values[i] = entries[k]
			}
			return NewArray(values), nil
		}), nil
	case "values_at":
		return NewAutoBuiltin("hash.values_at", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			entries := receiver.Hash()
			out := make([]Value, len(args))
			for i, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.values_at keys must be symbol or string")
				}
				if value, ok := entries[key]; ok {
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
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("hash.fetch expects key and optional default")
			}
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.fetch key must be symbol or string")
			}
			if value, ok := receiver.Hash()[key]; ok {
				return value, nil
			}
			if len(args) == 2 {
				return args[1], nil
			}
			return NewNil(), nil
		}), nil
	case "fetch_values":
		return NewAutoBuiltin("hash.fetch_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			entries := receiver.Hash()
			out := make([]Value, len(args))
			for i, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.fetch_values keys must be symbol or string")
				}
				if value, ok := entries[key]; ok {
					out[i] = value
					continue
				}
				if valueBlock(block) == nil {
					return NewNil(), fmt.Errorf("hash.fetch_values key not found: %s", formatMissingHashKey(arg))
				}
				blockArg := [1]Value{arg}
				value, err := exec.CallBlock(block, blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				out[i] = value
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
			runner, err := newBlockCallRunner(exec, block, "hash.each")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// each builds no output map, but it materializes a sorted key list to
			// walk entries deterministically. Reserve that scratch buffer against the
			// quota for the walk's whole lifetime so a large receiver cannot escape the
			// memory quota and so every memory check inside the block body counts the
			// live scratch; the walk projection charges no output map this iterator
			// never creates. Reserving first means the projection adds no separate
			// scratch bytes.
			delta := exec.reserveLoopScratch(sortedKeyBufferBytes(len(entries)))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
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
				blockArgs[0] = NewSymbol(key)
				blockArgs[1] = entries[key]
				if _, err := runner.call(blockArgs[:]); err != nil {
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
			runner, err := newBlockCallRunner(exec, block, "hash.each_key")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// Reserve the sorted key scratch buffer for the walk's lifetime; each_key
			// builds no output map but walks a materialized key list that stays live
			// while the block body runs, so reserving it keeps every memory check
			// inside the body aware of the scratch. Reserving first means the walk
			// projection adds no separate scratch bytes.
			delta := exec.reserveLoopScratch(sortedKeyBufferBytes(len(entries)))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
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
			}
			return receiver, nil
		}), nil
	case "each_value":
		return NewAutoBuiltin("hash.each_value", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.each_value does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.each_value")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// Reserve the sorted key scratch buffer for the walk's lifetime; each_value
			// builds no output map but walks a materialized key list that stays live
			// while the block body runs, so reserving it keeps every memory check
			// inside the body aware of the scratch. Reserving first means the walk
			// projection adds no separate scratch bytes.
			delta := exec.reserveLoopScratch(sortedKeyBufferBytes(len(entries)))
			defer exec.releaseLoopScratch(delta)
			if err := exec.checkProjectedHashWalkBytes(receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
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
			acc := newArrayBuildAccumulator(exec, receiver, args, kwargs, block)
			scratch := sortedKeyBufferBytes(len(entries))
			if err := acc.reserveScratch(scratch); err != nil {
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

func hashMemberTransforms(property string) (Value, error) {
	switch property {
	case "merge", "update", "merge!":
		// update and merge! are Ruby aliases of merge. Ruby mutates the receiver
		// in place and returns it; Vibescript's method-based hash helpers are
		// immutable-style, so all three return a new merged hash and leave the
		// receiver unchanged. Index assignment (hash[key] = value) remains the
		// way to mutate in place.
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
			base := receiver.Hash()
			// A block only resolves conflicts, which require at least one argument
			// hash. With zero arguments the merge short-circuits to a plain copy
			// below and never runs the block or sorts the base, so the conflict
			// block's base scratch buffer is never allocated. Gate useBlock on having
			// arguments so the projection does not charge that phantom scratch and
			// reject a large receiver whose copy fits but whose unused base scratch
			// would not.
			useBlock := valueBlock(block) != nil && len(args) > 0
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
				r, err := newBlockCallRunner(exec, block, "hash."+name)
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
				// never drop below the live footprint.
				acc = newHashBuildAccumulator(exec, receiver, args, kwargs, block)
				// The output map is preallocated with make(map, len(base)) but grows as
				// non-conflicting argument keys are inserted, reaching the exact distinct
				// union at peak. Reserve that union (projectedEntries -- the same bound the
				// up-front projection charged) so a large early conflict result is checked
				// against the whole grown backing, not just the len(base) slots filled so
				// far; reserving only len(base) would under-count the backing live once the
				// non-conflict additions have grown the map and let backing + an early
				// conflict result slip past the quota until a later check. The exact union
				// (not the loose len(base)+sum(arg lens) bound) is reserved here so an
				// overlapping merge whose true union fits is never rejected by phantom
				// slots the result map never allocates.
				if err := acc.reserveBacking(projectedEntries); err != nil {
					return NewNil(), err
				}
				// The sorted key scratch buffer stays live the whole build, coexisting
				// with the output map at peak, so reserve it in the accumulator's
				// running budget -- the same bytes the projection above charged.
				if err := acc.reserveScratch(scratchBytes); err != nil {
					return NewNil(), err
				}
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
					out[key] = resolved
					if err := acc.add(resolved); err != nil {
						return NewNil(), err
					}
				}
			}
			// Ruby's Hash#merge copies the receiver's default onto the result.
			return newHashPreservingDefault(receiver, out), nil
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
			// argument's entries, mutating in place. Vibescript's hash helpers are
			// immutable-style, so replace returns a fresh hash holding a copy of
			// the replacement's entries and leaves the receiver unchanged.
			replacement := args[0].Hash()
			// Preflight the copied map before reserving it so a large replacement
			// cannot allocate past the quota ahead of the statement-level check.
			if err := exec.checkProjectedHashBytes(len(replacement), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(replacement))
			// The output map is order-independent, so iterate the replacement
			// directly rather than materializing a sorted key list. A sorted
			// walk would heap a len(replacement) []string scratch buffer that
			// the scratch-free memory preflight above does not charge, letting
			// it escape the quota; iterating in place keeps accounting exact and
			// mirrors compact and slice. The range loop still charges a step per
			// copied entry so a large replacement participates in the step quota
			// and honors cancellation, matching every other O(n) hash transform.
			for key, val := range replacement {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				out[key] = val
			}
			return NewHash(out), nil
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
			entries := receiver.Hash()
			var keyBuf [smallHashKeyBufferSize]string
			keys := sortedHashKeysInto(entries, keyBuf[:])
			pairs := make([]Value, len(keys))
			for i, key := range keys {
				pairs[i] = NewArray([]Value{NewSymbol(key), entries[key]})
			}
			out, err := flattenValues(pairs, depth, "hash.flatten")
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
			key, err := valueToHashKey(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("hash.store key must be symbol or string")
			}
			// Vibescript's method-based hash helpers are immutable-style: store
			// returns a new hash with the key assigned rather than mutating the
			// receiver, matching merge and the array collection helpers.
			base := receiver.Hash()
			// Preflight the copied map before reserving it so storing into a large
			// hash cannot allocate past the quota ahead of the statement-level
			// check. Storing an existing key replaces its value, so the result keeps
			// len(base) entries; only a new key grows the map to len(base)+1.
			// Sizing the projection by the existing-key case avoids rejecting an
			// in-place-style update that fits a quota tuned to the receiver's size.
			projected := len(base)
			if _, exists := base[key]; !exists {
				projected = saturatingAdd(projected, 1)
			}
			if err := exec.checkProjectedHashBytes(projected, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, projected)
			// Copy the receiver entry by entry rather than with maps.Copy so a
			// store into a large hash charges a step per copied entry and honors
			// cancellation, matching replace, compact, and slice. The output map
			// is order-independent, so no sorted key list is needed.
			for k, v := range base {
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				out[k] = v
			}
			out[key] = args[1]
			return NewHash(out), nil
		}), nil
	case "slice":
		// AutoBuiltin so a parenless `hash.slice` invokes with zero arguments
		// and returns an empty hash, matching Ruby where the call has no
		// parentheses distinction. Explicit `slice(...)` calls still pass
		// their candidate keys through the normal call path.
		return NewAutoBuiltin("hash.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
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
			runner, err := newBlockCallRunner(exec, block, "hash.select")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// Preflight the largest map select could keep plus the sorted key scratch
			// buffer before reserving either; the block may keep every entry, so
			// project the full input. The scratch list of all keys is live alongside
			// the output map, so both are charged together here.
			if err := exec.checkProjectedHashTransformBytes(len(entries), sortedKeyBufferBytes(len(entries)), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
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
			runner, err := newBlockCallRunner(exec, block, "hash.reject")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// Preflight the largest map reject could keep plus the sorted key scratch
			// buffer before reserving either; the block may keep every entry, so
			// project the full input. The scratch list of all keys is live alongside
			// the output map, so both are charged together here.
			if err := exec.checkProjectedHashTransformBytes(len(entries), sortedKeyBufferBytes(len(entries)), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
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
				if !exclude.Truthy() {
					out[key] = entries[key]
				}
			}
			return NewHash(out), nil
		}), nil
	case "transform_keys":
		return NewAutoBuiltin("hash.transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_keys does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_keys")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// Preflight the output map's structural slots before reserving it;
			// transform_keys produces at most one entry per input key. The block can
			// return a fresh key string per entry, and those synthesized keys live
			// only in the Go-local out map until the builtin returns, so the
			// structural projection cannot bound them. Charge each synthesized key
			// incrementally through a build accumulator via addSynthesizedKey: only
			// the block-returned key is fresh, so only it goes through the results-only
			// estimator; the value stays a receiver value already counted in the call
			// roots, so charging it through the estimator would record its backing as
			// seen and risk dedup'ing a later block result to nothing. Counting is
			// conservative: a block that collapses several input keys onto one output
			// key is charged once per write rather than dedup'd to a single entry, an
			// over-count that keeps the bound sound (the running total only grows). The
			// sorted key scratch buffer is charged alongside the structural projection
			// here and reserved in the accumulator so it stays charged for the whole
			// build.
			scratch := sortedKeyBufferBytes(len(entries))
			if err := exec.checkProjectedHashTransformBytes(len(entries), scratch, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			// The output map is preallocated with make(map, len(entries)), so its full
			// backing is live before the first block runs; reserve it so a large early
			// synthesized key is checked against the whole backing, not just the slots
			// filled so far.
			if err := acc.reserveBacking(len(entries)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveScratch(scratch); err != nil {
				return NewNil(), err
			}
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
				blockArg[0] = NewSymbol(key)
				nextKey, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				resolved, err := valueToHashKey(nextKey)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.transform_keys block must return symbol or string")
				}
				out[resolved] = entries[key]
				if err := acc.addSynthesizedKey(resolved); err != nil {
					return NewNil(), err
				}
			}
			return NewHash(out), nil
		}), nil
	case "deep_transform_keys":
		return NewAutoBuiltin("hash.deep_transform_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.deep_transform_keys does not take arguments")
			}
			if err := ensureBlock(block, "hash.deep_transform_keys"); err != nil {
				return NewNil(), err
			}
			return deepTransformKeys(exec, receiver, block)
		}), nil
	case "remap_keys":
		return NewBuiltin("hash.remap_keys", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || (args[0].Kind() != KindHash && args[0].Kind() != KindObject) {
				return NewNil(), fmt.Errorf("hash.remap_keys expects a key mapping hash")
			}
			entries := receiver.Hash()
			mapping := args[0].Hash()
			// Preflight the output map plus the sorted key scratch buffer before
			// reserving either; remap_keys produces one entry per input key (renamed
			// or kept), so project the full input.
			if err := exec.checkProjectedHashTransformBytes(len(entries), sortedKeyBufferBytes(len(entries)), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(entries))
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				// Charge a step per remapped key so remapping a large hash
				// participates in the step quota and honors cancellation.
				if err := exec.step(); err != nil {
					return NewNil(), err
				}
				value := entries[key]
				if mapped, ok := mapping[key]; ok {
					nextKey, err := valueToHashKey(mapped)
					if err != nil {
						return NewNil(), fmt.Errorf("hash.remap_keys mapping values must be symbol or string")
					}
					out[nextKey] = value
					continue
				}
				out[key] = value
			}
			return NewHash(out), nil
		}), nil
	case "transform_values":
		return NewAutoBuiltin("hash.transform_values", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.transform_values does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "hash.transform_values")
			if err != nil {
				return NewNil(), err
			}
			entries := receiver.Hash()
			// Preflight the output map's structural slots before reserving it;
			// transform_values keeps every key. The block can return a fresh heap
			// value per entry, and those results live only in the Go-local out map
			// until the builtin returns, so the structural projection cannot bound
			// them. Charge each result incrementally through a build accumulator whose
			// results-only estimator counts each block result's full footprint as it is
			// produced, so accumulated payloads count toward the quota during the loop,
			// not only at the post-call check. Counting is conservative: a block that
			// returns a value unchanged and shared with the receiver is counted again
			// rather than dedup'd against the baseline, an over-count that keeps the
			// bound sound even when a block mutates a receiver-owned container in place
			// and returns it. The sorted key scratch buffer is charged alongside the
			// structural projection here and reserved in the accumulator so it stays
			// charged for the whole build.
			scratch := sortedKeyBufferBytes(len(entries))
			if err := exec.checkProjectedHashTransformBytes(len(entries), scratch, receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			acc := newHashBuildAccumulator(exec, receiver, args, kwargs, block)
			// The output map is preallocated with make(map, len(entries)), so its full
			// backing is live before the first block runs; reserve it so a large early
			// block result is checked against the whole backing, not just the slots
			// filled so far.
			if err := acc.reserveBacking(len(entries)); err != nil {
				return NewNil(), err
			}
			if err := acc.reserveScratch(scratch); err != nil {
				return NewNil(), err
			}
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
