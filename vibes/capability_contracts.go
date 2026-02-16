package vibes

import (
	"fmt"
	"reflect"
)

type capabilityContractScanner struct {
	seenArrays    map[sliceIdentity]struct{}
	seenMaps      map[uintptr]struct{}
	seenClasses   map[*ClassDef]struct{}
	seenInstances map[*Instance]struct{}
}

func newCapabilityContractScanner() *capabilityContractScanner {
	return &capabilityContractScanner{
		seenArrays:    make(map[sliceIdentity]struct{}),
		seenMaps:      make(map[uintptr]struct{}),
		seenClasses:   make(map[*ClassDef]struct{}),
		seenInstances: make(map[*Instance]struct{}),
	}
}

func validateCapabilityDataOnlyValue(label string, val Value) error {
	scanner := newCapabilityContractScanner()
	if scanner.containsCallable(val) {
		return fmt.Errorf("%s must be data-only", label)
	}
	return nil
}

func bindCapabilityContracts(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
) {
	if scope == nil || len(scope.contracts) == 0 {
		return
	}
	scanner := newCapabilityContractScanner()
	scanner.bindContracts(val, scope, target, scopes)
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

func (s *capabilityContractScanner) bindContracts(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
) {
	switch val.Kind() {
	case KindBuiltin:
		builtin := val.Builtin()
		if _, seen := scopes[builtin]; !seen {
			scopes[builtin] = scope
		}
		if contract, ok := scope.contracts[builtin.Name]; ok {
			if _, seen := target[builtin]; !seen {
				target[builtin] = contract
			}
		}
	case KindArray:
		values := val.Array()
		id := sliceIdentity{
			ptr: reflect.ValueOf(values).Pointer(),
			len: len(values),
			cap: cap(values),
		}
		if _, seen := s.seenArrays[id]; seen {
			return
		}
		s.seenArrays[id] = struct{}{}
		for _, item := range values {
			s.bindContracts(item, scope, target, scopes)
		}
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			s.bindContracts(item, scope, target, scopes)
		}
	case KindClass:
		classDef := val.Class()
		if classDef == nil {
			return
		}
		if _, seen := s.seenClasses[classDef]; seen {
			return
		}
		s.seenClasses[classDef] = struct{}{}
		for _, item := range classDef.ClassVars {
			s.bindContracts(item, scope, target, scopes)
		}
	case KindInstance:
		instance := val.Instance()
		if instance == nil {
			return
		}
		if _, seen := s.seenInstances[instance]; seen {
			return
		}
		s.seenInstances[instance] = struct{}{}
		for _, item := range instance.Ivars {
			s.bindContracts(item, scope, target, scopes)
		}
		if instance.Class != nil {
			s.bindContracts(NewClass(instance.Class), scope, target, scopes)
		}
	}
}
