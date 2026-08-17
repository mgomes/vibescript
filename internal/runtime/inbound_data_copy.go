package runtime

import (
	"maps"
	"reflect"

	"github.com/mgomes/vibescript/vibes/value"
)

// inboundDataScanner decides whether one public Script.Call may deep-copy its
// host-provided values through the tight data-only copier instead of the full
// callFunctionRebinder walk. The rebinder does two jobs on inbound values:
// deep-copying mutable composites (the isolation boundary that keeps script
// mutators out of host memory) and re-attaching script-owned runtime values
// (functions, classes, enums, instances, blocks, builtins) to the current call
// root while deduplicating aliases. For a graph of plain data there is nothing
// to re-attach, so only the copy remains — but the copy must still preserve
// aliasing: two inbound references to one mutable composite must rebind to one
// shared clone so an in-place mutation through one alias stays visible through
// the other, exactly as the rebinder's seen-maps guarantee.
//
// Instead of tracking per-value alias state during the copy, the scanner
// verifies up front that the whole inbound set (positional args, keyword args,
// and globals) is data-only and alias-free: every
// composite wrapper — and every legacy hash entry map, which two distinct
// wrappers may intentionally share — appears exactly once. Then the copier
// needs no dedup state at all. Anything else (a runtime value anywhere, a
// typed-key hash, a hash carrying Ruby-style defaults, a repeated identity, a
// cycle) disables the fast path for the entire call and every value takes the
// unmodified slow path, so revocation of captured capability grants, alias
// dedup, and block re-rooting behave exactly as before.
type inboundDataScanner struct {
	seen map[uintptr]struct{}
	// rebinder, when set, extends repeat detection to composites the slow
	// rebind walk has already visited in this call. The deferred global scan
	// (see rebindGlobalValue) uses it so a global source aliasing an argument
	// — whether that argument was slow-path rebound or fast-copied with
	// registration — counts as a repeat and sends every global through the
	// slow path, which deduplicates against the registered clone.
	rebinder *callFunctionRebinder
}

// admit records one composite identity and reports whether it was new. A zero
// identity carries no dedupable state (a nil entry map) and is always
// admitted without tracking.
func (s *inboundDataScanner) admit(id uintptr) bool {
	if id == 0 {
		return true
	}
	if r := s.rebinder; r != nil {
		if _, ok := r.seenArrays[id]; ok {
			return false
		}
		if _, ok := r.seenHashes[id]; ok {
			return false
		}
		if _, ok := r.seenHashEntries[id]; ok {
			return false
		}
		if _, ok := r.seenMapPtrs[id]; ok {
			return false
		}
	}
	if s.seen == nil {
		s.seen = make(map[uintptr]struct{})
	}
	if _, dup := s.seen[id]; dup {
		return false
	}
	s.seen[id] = struct{}{}
	return true
}

// scan reports whether val is a data-only, alias-free graph. Kinds are
// whitelisted so any future value kind fails closed onto the slow path.
func (s *inboundDataScanner) scan(val Value) bool {
	switch val.Kind() {
	case KindNil, KindBool, KindInt, KindFloat, KindString, KindMoney,
		KindDuration, KindTime, KindSymbol, KindRange, KindRegex:
		return true
	case KindArray:
		if !s.admit(arrayIdentity(val)) {
			return false
		}
		for _, item := range val.Array() {
			if !s.scan(item) {
				return false
			}
		}
		return true
	case KindHash:
		if !s.admit(hashIdentity(val)) {
			return false
		}
		// Track the entry map separately from the wrapper: two distinct
		// wrappers sharing one mutable entry map must dedup to one cloned map
		// (the rebinder's seenHashEntries contract), which the tight copier
		// cannot provide.
		entries := val.HashEntryMap()
		if entries == nil {
			return true
		}
		if !s.admit(reflect.ValueOf(entries).Pointer()) {
			return false
		}
		for _, item := range entries {
			if !s.scan(item) {
				return false
			}
		}
		return true
	case KindObject:
		entries := val.HashEntryMap()
		if entries == nil {
			return true
		}
		if !s.admit(reflect.ValueOf(entries).Pointer()) {
			return false
		}
		for _, item := range entries {
			if !s.scan(item) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// scanTopLevel scans one top-level inbound value. Enum definitions and enum
// members are leaves with no composite payload, so they cannot alias a data
// graph; they are admitted at the top level (where the slow path rebinds them
// by name without touching the rebinder's dedup state) but still poison a
// data graph when nested inside one, since a composite containing them needs
// the rebinder.
func (s *inboundDataScanner) scanTopLevel(val Value) bool {
	switch val.Kind() {
	case KindEnum, KindEnumValue:
		return true
	default:
		return s.scan(val)
	}
}

// scanInboundCallValues reports whether every positional and keyword argument
// entering one public Script.Call is a data-only, alias-free graph eligible
// for the tight copier. Both sets are scanned with one shared seen-set so
// aliasing between top-level values is detected as a repeat and disables the
// fast path, preserving the rebinder's shared-clone semantics.
//
// CallOptions.Globals are deliberately NOT part of this scan: composite
// globals bind lazily, so their sources are scanned only if one is actually
// read (see rebindGlobalValue), and cross-aliasing between a global source
// and an argument is caught there through the rebinder's seen-maps.
func scanInboundCallValues(args []Value, keywords map[string]Value) bool {
	var scanner inboundDataScanner
	for _, val := range args {
		if !scanner.scanTopLevel(val) {
			return false
		}
	}
	for _, val := range keywords {
		if !scanner.scanTopLevel(val) {
			return false
		}
	}
	return true
}

// copyInboundDataValue deep-copies a data-only graph admitted by
// inboundDataScanner. The scan proved the graph acyclic, alias-free,
// default-free, legacy-keyed, and free of runtime values, so the copy carries
// no dedup or rebinding state. It is still a full deep copy: script-side
// in-place mutators write into these clones, never into host memory.
func copyInboundDataValue(val Value) Value {
	switch val.Kind() {
	case KindArray:
		items := val.Array()
		cloned := make([]Value, len(items))
		copy(cloned, items)
		for i, item := range items {
			switch item.Kind() {
			case KindArray, KindHash, KindObject:
				cloned[i] = copyInboundDataValue(item)
			}
		}
		return NewArray(cloned)
	case KindHash:
		// The copy iterates the way its source does, which cloning the entry
		// map alone would not preserve.
		return value.NewHashWithTrustedOrder(copyInboundDataEntries(val.HashEntryMap()), val.HashKeyOrder())
	case KindObject:
		return retagClonedObject(val, copyInboundDataEntries(val.HashEntryMap()))
	default:
		return val
	}
}

// copyInboundDataEntries deep-copies one legacy entry map. A map whose values
// are all scalars clones its bucket structure wholesale (no per-key
// rehashing); a map holding nested composites falls back to a per-entry copy.
func copyInboundDataEntries(entries map[string]Value) map[string]Value {
	if entries == nil {
		return make(map[string]Value)
	}
	for _, item := range entries {
		switch item.Kind() {
		case KindArray, KindHash, KindObject:
			cloned := make(map[string]Value, len(entries))
			for key, entry := range entries {
				switch entry.Kind() {
				case KindArray, KindHash, KindObject:
					cloned[key] = copyInboundDataValue(entry)
				default:
					cloned[key] = entry
				}
			}
			return cloned
		}
	}
	return maps.Clone(entries)
}

// copyAndRegisterInboundValue is copyInboundDataValue plus registration of
// every source composite in the rebinder's seen-maps, exactly as the slow
// path would record it. It runs only when the call defers composite globals:
// a global source read later might alias an argument composite, and the
// deferred global scan plus the slow rebind walk resolve that alias through
// these registrations, deduplicating to the clones made here.
func (r *callFunctionRebinder) copyAndRegisterInboundValue(val Value) Value {
	switch val.Kind() {
	case KindArray:
		items := val.Array()
		cloned := make([]Value, len(items))
		copy(cloned, items)
		clonedVal := NewArray(cloned)
		if r.seenArrays == nil {
			r.seenArrays = make(map[uintptr]Value)
		}
		r.seenArrays[arrayIdentity(val)] = clonedVal
		for i, item := range items {
			switch item.Kind() {
			case KindArray, KindHash, KindObject:
				cloned[i] = r.copyAndRegisterInboundValue(item)
			}
		}
		return clonedVal
	case KindHash:
		entries := val.HashEntryMap()
		clonedEntries := r.copyAndRegisterInboundEntries(entries)
		clonedVal := value.NewHashWithTrustedOrder(clonedEntries, val.HashKeyOrder())
		if r.seenHashes == nil {
			r.seenHashes = make(map[uintptr]Value)
		}
		r.seenHashes[hashIdentity(val)] = clonedVal
		if entries != nil {
			if r.seenHashEntries == nil {
				r.seenHashEntries = make(map[uintptr]map[string]Value)
			}
			r.seenHashEntries[reflect.ValueOf(entries).Pointer()] = clonedEntries
		}
		return clonedVal
	case KindObject:
		entries := val.HashEntryMap()
		clonedEntries := r.copyAndRegisterInboundEntries(entries)
		if entries != nil {
			ptr := reflect.ValueOf(entries).Pointer()
			if r.seenMaps == nil {
				r.seenMaps = make(map[objectCloneKey]map[string]Value)
				r.seenMapPtrs = make(map[uintptr]struct{})
			}
			r.seenMaps[objectCloneKey{ptr: ptr, tag: val.ObjectTag()}] = clonedEntries
			r.seenMapPtrs[ptr] = struct{}{}
		}
		return retagClonedObject(val, clonedEntries)
	default:
		return val
	}
}

func (r *callFunctionRebinder) copyAndRegisterInboundEntries(entries map[string]Value) map[string]Value {
	if entries == nil {
		return make(map[string]Value)
	}
	for _, item := range entries {
		switch item.Kind() {
		case KindArray, KindHash, KindObject:
			cloned := make(map[string]Value, len(entries))
			for key, entry := range entries {
				switch entry.Kind() {
				case KindArray, KindHash, KindObject:
					cloned[key] = r.copyAndRegisterInboundValue(entry)
				default:
					cloned[key] = entry
				}
			}
			return cloned
		}
	}
	return maps.Clone(entries)
}
