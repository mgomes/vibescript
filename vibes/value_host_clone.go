package vibes

import (
	"reflect"

	"github.com/mgomes/vibescript/vibes/value"
)

type hostValueCloneState struct {
	arrays    map[sliceIdentity]Value
	maps      map[uintptr]map[string]Value
	instances map[*Instance]Value
	classes   map[*ClassDef]*ClassDef
	envs      map[*Env]*Env
}

type hostValueScanState struct {
	arrays map[sliceIdentity]struct{}
	maps   map[uintptr]struct{}
}

func valueNeedsHostClone(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindClass, KindInstance, KindEnum, KindEnumValue, KindBlock, KindBuiltin:
		return true
	case KindArray, KindHash, KindObject:
		return compositeValueNeedsHostClone(val)
	default:
		return false
	}
}

func compositeValueNeedsHostClone(val Value) bool {
	switch val.Kind() {
	case KindArray:
		for _, item := range val.Array() {
			if itemDirectlyNeedsHostClone(item) {
				return true
			}
			if itemCanContainHostClone(item) {
				return valueNeedsHostCloneWithFreshState(val)
			}
		}
		return false
	case KindHash, KindObject:
		entries := val.Hash()
		if len(entries) == 0 {
			return false
		}
		for _, item := range entries {
			if itemDirectlyNeedsHostClone(item) {
				return true
			}
			if itemCanContainHostClone(item) {
				return valueNeedsHostCloneWithFreshState(val)
			}
		}
		return false
	default:
		return valueNeedsHostClone(val)
	}
}

func valueNeedsHostCloneWithFreshState(val Value) bool {
	state := hostValueScanState{
		arrays: make(map[sliceIdentity]struct{}),
		maps:   make(map[uintptr]struct{}),
	}
	return valueNeedsHostCloneWithState(val, state)
}

func itemDirectlyNeedsHostClone(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindClass, KindInstance, KindEnum, KindEnumValue, KindBlock, KindBuiltin:
		return true
	default:
		return false
	}
}

func itemCanContainHostClone(val Value) bool {
	switch val.Kind() {
	case KindArray, KindHash, KindObject:
		return true
	default:
		return false
	}
}

func valueNeedsHostCloneWithState(val Value, state hostValueScanState) bool {
	switch val.Kind() {
	case KindFunction, KindClass, KindInstance, KindEnum, KindEnumValue, KindBlock, KindBuiltin:
		return true
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(items).Pointer(),
			Len: len(items),
			Cap: cap(items),
		}
		if id.Ptr != 0 {
			if _, ok := state.arrays[id]; ok {
				return false
			}
			state.arrays[id] = struct{}{}
		}
		for _, item := range items {
			if valueNeedsHostCloneWithState(item, state) {
				return true
			}
		}
		return false
	case KindHash, KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, ok := state.maps[ptr]; ok {
				return false
			}
			state.maps[ptr] = struct{}{}
		}
		for _, item := range entries {
			if valueNeedsHostCloneWithState(item, state) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func cloneValueForHost(val Value) Value {
	state := hostValueCloneState{
		arrays:    make(map[sliceIdentity]Value),
		maps:      make(map[uintptr]map[string]Value),
		instances: make(map[*Instance]Value),
		classes:   make(map[*ClassDef]*ClassDef),
		envs:      make(map[*Env]*Env),
	}
	return cloneValueForHostWithState(val, state)
}

func cloneValueForHostWithState(val Value, state hostValueCloneState) Value {
	switch val.Kind() {
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(items).Pointer(),
			Len: len(items),
			Cap: cap(items),
		}
		if id.Ptr != 0 {
			if clone, ok := state.arrays[id]; ok {
				return clone
			}
		}
		clonedItems := make([]Value, len(items))
		cloned := NewArray(clonedItems)
		if id.Ptr != 0 {
			state.arrays[id] = cloned
		}
		for i, item := range items {
			clonedItems[i] = cloneValueForHostWithState(item, state)
		}
		return cloned
	case KindHash:
		return cloneHostMapValue(val, state, NewHash)
	case KindObject:
		return cloneHostMapValue(val, state, NewObject)
	case KindFunction:
		return NewFunction(cloneFunctionForHostWithState(valueFunction(val), state))
	case KindClass:
		return NewClass(cloneClassForHostWithState(valueClass(val), state))
	case KindInstance:
		inst := valueInstance(val)
		if inst == nil {
			return val
		}
		if clone, ok := state.instances[inst]; ok {
			return clone
		}
		clonedClass := inst.Class
		if inst.Class != nil {
			clonedClass = cloneClassForHostWithState(inst.Class, state)
		}
		clonedIvars := make(map[string]Value, len(inst.Ivars))
		cloned := NewInstance(&Instance{Class: clonedClass, Ivars: clonedIvars})
		state.instances[inst] = cloned
		for name, ivar := range inst.Ivars {
			clonedIvars[name] = cloneValueForHostWithState(ivar, state)
		}
		return cloned
	case KindEnum:
		enumDef := valueEnum(val)
		return NewEnum(cloneEnumDef(enumDef, enumOwner(enumDef)))
	case KindEnumValue:
		member := valueEnumValue(val)
		if member == nil || member.Enum == nil {
			return val
		}
		enumClone := cloneEnumDef(member.Enum, enumOwner(member.Enum))
		if memberClone, ok := enumClone.Members[member.Name]; ok {
			return NewEnumValue(memberClone)
		}
		if memberClone, ok := enumClone.MembersByKey[member.Symbol]; ok {
			return NewEnumValue(memberClone)
		}
		return val
	case KindBlock:
		block := valueBlock(val)
		if block == nil {
			return val
		}
		clone := *block
		clone.Params = cloneParams(block.Params)
		clone.Body = cloneStatements(block.Body)
		clone.Env = cloneEnvForHost(block.Env, state)
		return value.NewValue(KindBlock, &clone)
	case KindBuiltin:
		return cloneBuiltinValue(val)
	default:
		return val
	}
}

func cloneFunctionForHostWithState(fn *ScriptFunction, state hostValueCloneState) *ScriptFunction {
	if fn == nil {
		return nil
	}
	clone := *fn
	clone.Params = cloneParams(fn.Params)
	clone.ReturnTy = cloneTypeExpr(fn.ReturnTy)
	clone.Body = cloneStatements(fn.Body)
	clone.Env = cloneEnvForHost(fn.Env, state)
	return &clone
}

func cloneClassForHostWithState(classDef *ClassDef, state hostValueCloneState) *ClassDef {
	if classDef == nil {
		return nil
	}
	if clone, ok := state.classes[classDef]; ok {
		return clone
	}
	classClone := &ClassDef{
		Name:         classDef.Name,
		Methods:      make(map[string]*ScriptFunction, len(classDef.Methods)),
		ClassMethods: make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
		ClassVars:    make(map[string]Value, len(classDef.ClassVars)),
		Body:         cloneStatements(classDef.Body),
		owner:        classDef.owner,
	}
	state.classes[classDef] = classClone
	for name, val := range classDef.ClassVars {
		classClone.ClassVars[name] = cloneValueForHostWithState(val, state)
	}
	for methodName, method := range classDef.Methods {
		classClone.Methods[methodName] = cloneFunctionForHostWithState(method, state)
	}
	for methodName, method := range classDef.ClassMethods {
		classClone.ClassMethods[methodName] = cloneFunctionForHostWithState(method, state)
	}
	return classClone
}

func cloneEnvForHost(env *Env, state hostValueCloneState) *Env {
	if env == nil {
		return nil
	}
	if clone, ok := state.envs[env]; ok {
		return clone
	}
	clone := newEnvWithCapacity(nil, len(env.values))
	state.envs[env] = clone
	clone.parent = cloneEnvForHost(env.parent, state)
	for name, val := range env.values {
		clone.values[name] = cloneValueForHostWithState(val, state)
	}
	return clone
}

func cloneHostMapValue(val Value, state hostValueCloneState, construct func(map[string]Value) Value) Value {
	entries := val.Hash()
	ptr := reflect.ValueOf(entries).Pointer()
	if ptr != 0 {
		if clone, ok := state.maps[ptr]; ok {
			return construct(clone)
		}
	}
	clonedEntries := make(map[string]Value, len(entries))
	if ptr != 0 {
		state.maps[ptr] = clonedEntries
	}
	for key, item := range entries {
		clonedEntries[key] = cloneValueForHostWithState(item, state)
	}
	return construct(clonedEntries)
}

func enumOwner(enumDef *EnumDef) *Script {
	if enumDef == nil {
		return nil
	}
	return enumDef.owner
}
