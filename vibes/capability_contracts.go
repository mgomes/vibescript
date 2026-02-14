package vibes

import (
	"fmt"
	"reflect"
)

type capabilityContractScanner struct {
	seenArrays map[sliceIdentity]struct{}
	seenMaps   map[uintptr]struct{}
}

func newCapabilityContractScanner() *capabilityContractScanner {
	return &capabilityContractScanner{
		seenArrays: make(map[sliceIdentity]struct{}),
		seenMaps:   make(map[uintptr]struct{}),
	}
}

func validateCapabilityDataOnlyValue(label string, val Value) error {
	scanner := newCapabilityContractScanner()
	if scanner.containsCallable(val) {
		return fmt.Errorf("%s must be data-only", label)
	}
	return nil
}

func (s *capabilityContractScanner) containsCallable(val Value) bool {
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
		for _, item := range values {
			if s.containsCallable(item) {
				return true
			}
		}
		return false
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
