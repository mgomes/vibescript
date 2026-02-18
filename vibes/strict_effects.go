package vibes

import (
	"fmt"
	"reflect"
	"slices"
)

type sliceIdentity struct {
	ptr uintptr
	len int
	cap int
}

type strictGlobalsScanner struct {
	seenArrays map[sliceIdentity]struct{}
	seenMaps   map[uintptr]struct{}
}

func validateStrictGlobals(globals map[string]Value) error {
	if len(globals) == 0 {
		return nil
	}
	scanner := strictGlobalsScanner{
		seenArrays: make(map[sliceIdentity]struct{}),
		seenMaps:   make(map[uintptr]struct{}),
	}
	for name, val := range globals {
		if scanner.containsCallable(val) {
			return fmt.Errorf("strict effects: global %s must be data-only; use CallOptions.Capabilities for side effects", name)
		}
	}
	return nil
}

func (s *strictGlobalsScanner) containsCallable(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin, KindBlock, KindClass, KindInstance:
		return true
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			ptr: reflect.ValueOf(values).Pointer(),
			len: len(values),
			cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		s.seenArrays[id] = struct{}{}
		return slices.ContainsFunc(values, s.containsCallable)
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCallable(item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}
