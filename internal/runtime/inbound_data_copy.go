package runtime

import (
	"maps"
	"reflect"
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
// verifies up front that the whole inbound argument set (positional and
// keyword arguments) is data-only and alias-free: every
// composite wrapper — and every legacy hash entry map, which two distinct
// wrappers may intentionally share — appears exactly once. Then the copier
// needs no dedup state at all. Anything else (a runtime value anywhere, a
// typed-key hash, a hash carrying Ruby-style defaults, a repeated identity, a
// cycle) disables the fast path for the entire call and every value takes the
// unmodified slow path, so revocation of captured capability grants, alias
// dedup, and block re-rooting behave exactly as before.
type inboundDataScanner struct {
	seen map[uintptr]struct{}
}

// admit records one composite identity and reports whether it was new. A zero
// identity carries no dedupable state (a nil entry map) and is always
// admitted without tracking.
func (s *inboundDataScanner) admit(id uintptr) bool {
	if id == 0 {
		return true
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
		// Typed-key hashes and hashes carrying Ruby-style defaults rebind
		// through the slow path: defaults may hold procs and typed entries
		// carry Value keys, both outside the tight copier's contract.
		if hashHasTypedEntries(val) {
			return false
		}
		if !hashDefaultValue(val).IsNil() || !hashDefaultProc(val).IsNil() {
			return false
		}
		if !s.admit(hashIdentity(val)) {
			return false
		}
		// Track the entry map separately from the wrapper: two distinct
		// wrappers sharing one mutable entry map must dedup to one cloned map
		// (the rebinder's seenHashEntries contract), which the tight copier
		// cannot provide.
		entries, _ := hashStringMapIfMaterialized(val)
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
		entries := val.Hash()
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
// CallOptions.Globals are deliberately NOT part of this scan: they bind
// through the slow rebind walk before the arguments rebind, so a global
// aliasing an argument is already registered in the rebinder's seen-maps by
// the time the argument's fast-path check consults them. Lazy task-global
// sources always materialize through the slow rebind walk (see
// taskLazyGlobals.materialize), so they need no entry-time scan; their graphs
// cannot alias a task call's arguments or keywords because those are freshly
// built task clones.
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
		return NewHash(copyInboundDataEntries(val.Hash()))
	case KindObject:
		return NewObject(copyInboundDataEntries(val.Hash()))
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
