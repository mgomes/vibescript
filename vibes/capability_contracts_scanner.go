package vibes

import (
	"reflect"
	"slices"
)

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

func (s *capabilityContractScanner) bindContracts(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
) {
	switch val.Kind() {
	case KindBuiltin:
		builtin := val.Builtin()
		if _, skip := s.excluded[builtin]; skip {
			return
		}
		ownerScope, seen := scopes[builtin]
		if !seen {
			scopes[builtin] = scope
			ownerScope = scope
		}
		if ownerScope != scope {
			return
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

func (s *capabilityContractScanner) collectBuiltins(val Value, out map[*Builtin]struct{}) {
	switch val.Kind() {
	case KindBuiltin:
		out[val.Builtin()] = struct{}{}
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
			s.collectBuiltins(item, out)
		}
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			s.collectBuiltins(item, out)
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
			s.collectBuiltins(item, out)
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
			s.collectBuiltins(item, out)
		}
		if instance.Class != nil {
			s.collectBuiltins(NewClass(instance.Class), out)
		}
	}
}
