package vibes

import "reflect"

type capabilityCycleScanner struct {
	visitingArrays map[sliceIdentity]struct{}
	visitingMaps   map[uintptr]struct{}
	seenArrays     map[sliceIdentity]struct{}
	seenMaps       map[uintptr]struct{}
}

func newCapabilityCycleScanner() *capabilityCycleScanner {
	return &capabilityCycleScanner{
		visitingArrays: make(map[sliceIdentity]struct{}),
		visitingMaps:   make(map[uintptr]struct{}),
		seenArrays:     make(map[sliceIdentity]struct{}),
		seenMaps:       make(map[uintptr]struct{}),
	}
}

func (s *capabilityCycleScanner) containsCycle(val Value) bool {
	switch val.Kind() {
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
		if _, visiting := s.visitingArrays[id]; visiting {
			return true
		}
		s.visitingArrays[id] = struct{}{}
		for _, item := range values {
			if s.containsCycle(item) {
				return true
			}
		}
		delete(s.visitingArrays, id)
		s.seenArrays[id] = struct{}{}
		return false
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return true
		}
		s.visitingMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCycle(item) {
				return true
			}
		}
		delete(s.visitingMaps, ptr)
		s.seenMaps[ptr] = struct{}{}
		return false
	default:
		return false
	}
}
