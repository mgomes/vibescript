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
	"size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_key", "each_value",
	"merge", "update", "merge!", "replace", "store", "slice", "except", "flatten", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact",
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
	case "size", "length", "empty?", "key?", "has_key?", "member?", "include?", "value?", "has_value?", "keys", "values", "values_at", "fetch", "fetch_values", "dig", "each", "each_key", "each_value":
		return hashMemberQuery(property)
	case "merge", "update", "merge!", "replace", "store", "slice", "except", "flatten", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact":
		return hashMemberTransforms(property)
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
				} else {
					out[i] = NewNil()
				}
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
			current := receiver
			for _, arg := range args {
				key, err := valueToHashKey(arg)
				if err != nil {
					return NewNil(), fmt.Errorf("hash.dig path keys must be symbol or string")
				}
				if current.Kind() != KindHash && current.Kind() != KindObject {
					return NewNil(), nil
				}
				next, ok := current.Hash()[key]
				if !ok {
					return NewNil(), nil
				}
				current = next
			}
			return current, nil
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
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
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
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
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
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArg[0] = entries[key]
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
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

// mergedKeyCount returns the number of distinct keys a merge of base and args
// would hold, stopping early once the running total passes limit. The merged
// hash is the union of the receiver's keys and every argument's keys, so
// overlapping keys (h.merge(h), or the same defaults applied repeatedly) collapse
// to one entry. Counting the union lets the projected memory check size the real
// output map instead of summing every input length, which would over-count an
// overlapping merge and reject one that fits the quota.
//
// limit is the largest output the quota can admit (maxProjectedHashEntries). A
// single argument needs no cross-argument deduplication, so its union is counted
// against base alone with no tracking set. Multiple arguments require a seen set
// to collapse keys repeated across arguments, but the set is bounded by limit:
// once the distinct-key total exceeds limit the merge is certain to be rejected,
// so counting stops and returns limit+1 rather than growing a tracking table
// sized to the over-quota result. All inputs walked here are already resident in
// memory.
func mergedKeyCount(base map[string]Value, args []Value, limit int) int {
	count := len(base)
	if count > limit {
		return count
	}
	if len(args) <= 1 {
		// One argument (or none): every argument key is distinct on its own, so
		// the union is base plus the argument keys absent from base, countable
		// without a tracking set.
		for _, arg := range args {
			for key := range arg.Hash() {
				if _, inBase := base[key]; inBase {
					continue
				}
				count++
				if count > limit {
					return count
				}
			}
		}
		return count
	}
	var seen map[string]struct{}
	for _, arg := range args {
		for key := range arg.Hash() {
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
				return count
			}
		}
	}
	return count
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
			// Preflight the map this merge could materialize before allocating it.
			// Two phases keep the check itself within the quota it enforces:
			//
			// First try a non-allocating upper bound (the receiver's keys plus
			// every argument's length, overlaps included). If even that loose
			// bound fits, the merge is guaranteed to fit and no tracking set is
			// needed to count the exact union.
			//
			// Only when the loose bound exceeds the quota does the exact union
			// count matter, because overlap (h.merge(h), repeated defaults) could
			// still bring the real output within the limit. The exact count is
			// then taken with mergedKeyCount, which caps its deduplication set at
			// the quota's entry budget so a doomed merge cannot allocate a large
			// tracking table before being rejected here.
			upperBound := looseMergedKeyUpperBound(base, args)
			if exec.checkProjectedHashBytes(upperBound, receiver, args, kwargs, block) != nil {
				limit := exec.maxProjectedHashEntries(receiver, args, kwargs, block)
				projected := mergedKeyCount(base, args, limit)
				if err := exec.checkProjectedHashBytes(projected, receiver, args, kwargs, block); err != nil {
					return NewNil(), err
				}
			}
			out := make(map[string]Value, len(base))
			maps.Copy(out, base)
			if len(args) == 0 {
				// Ruby's Hash#merge with no arguments returns a copy of self.
				return NewHash(out), nil
			}
			useBlock := valueBlock(block) != nil
			var runner *blockCallRunner
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
				}
			}
			return NewHash(out), nil
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
			maps.Copy(out, replacement)
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
			// Preflight the copied map (plus the stored entry) before reserving it
			// so storing into a large hash cannot allocate past the quota ahead of
			// the statement-level check.
			if err := exec.checkProjectedHashBytes(saturatingAdd(len(base), 1), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(base)+1)
			maps.Copy(out, base)
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
			out := make(map[string]Value, len(args))
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
			excluded := make(map[string]struct{}, len(args))
			for _, arg := range args {
				// Vibescript hash keys are only symbols or strings, so an
				// unsupported argument can never match an entry. Ruby's
				// Hash#except ignores keys that are not present, so we treat
				// those arguments as misses rather than raising.
				key, err := valueToHashKey(arg)
				if err != nil {
					continue
				}
				excluded[key] = struct{}{}
			}
			entries := receiver.Hash()
			// Preflight the largest map except could materialize before reserving
			// it. Excluded keys absent from the receiver leave the full input in
			// place, so the worst case is a copy of every entry.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
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
			// Preflight the largest map select could keep before reserving it; the
			// block may keep every entry, so project the full input. Per-entry
			// step accounting and the periodic memory check come from runner.call.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
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
			// Preflight the largest map reject could keep before reserving it; the
			// block may keep every entry, so project the full input. Per-entry
			// step accounting and the periodic memory check come from runner.call.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(entries))
			var blockArgs [2]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
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
			// Preflight the output map before reserving it; transform_keys produces
			// at most one entry per input key. Per-entry step accounting and the
			// periodic memory check come from runner.call.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(entries))
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
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
			// Preflight the output map before reserving it; remap_keys produces one
			// entry per input key (renamed or kept), so project the full input.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
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
			// Preflight the output map before reserving it; transform_values keeps
			// every key. Per-entry step accounting and the periodic memory check
			// come from runner.call.
			if err := exec.checkProjectedHashBytes(len(entries), receiver, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			out := make(map[string]Value, len(entries))
			var blockArg [1]Value
			var keyBuf [smallHashKeyBufferSize]string
			for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
				blockArg[0] = entries[key]
				nextValue, err := runner.call(blockArg[:])
				if err != nil {
					return NewNil(), err
				}
				out[key] = nextValue
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
