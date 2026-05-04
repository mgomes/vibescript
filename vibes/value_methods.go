package vibes

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// String returns the human-readable name of the ValueKind.
func (k ValueKind) String() string {
	switch k {
	case KindNil:
		return "nil"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindString:
		return "string"
	case KindArray:
		return "array"
	case KindHash:
		return "hash"
	case KindFunction:
		return "function"
	case KindBuiltin:
		return "builtin"
	case KindMoney:
		return "money"
	case KindDuration:
		return "duration"
	case KindTime:
		return "time"
	case KindSymbol:
		return "symbol"
	case KindObject:
		return "object"
	case KindRange:
		return "range"
	case KindBlock:
		return "block"
	case KindEnum:
		return "enum"
	case KindEnumValue:
		return "enum"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// String returns the string representation of v.
func (v Value) String() string {
	return v.stringWithState(&valueStringState{
		arrays: make(map[sliceIdentity]struct{}),
		maps:   make(map[uintptr]struct{}),
	})
}

type valueStringState struct {
	arrays map[sliceIdentity]struct{}
	maps   map[uintptr]struct{}
}

func (v Value) stringWithState(state *valueStringState) string {
	switch v.kind {
	case KindString:
		return v.data.(string)
	case KindNil:
		return ""
	case KindBool:
		if v.Bool() {
			return "true"
		}
		return "false"
	case KindInt:
		return fmt.Sprintf("%d", v.data.(int64))
	case KindFloat:
		return fmt.Sprintf("%g", v.data.(float64))
	case KindSymbol:
		return v.data.(string)
	case KindMoney:
		return v.data.(Money).String()
	case KindDuration:
		return v.data.(Duration).String()
	case KindTime:
		return v.data.(time.Time).Format(time.RFC3339Nano)
	case KindArray:
		elems := v.data.([]Value)
		id := sliceIdentity{
			ptr: reflect.ValueOf(elems).Pointer(),
			len: len(elems),
			cap: cap(elems),
		}
		if id.ptr != 0 {
			if _, seen := state.arrays[id]; seen {
				return "<cycle>"
			}
			state.arrays[id] = struct{}{}
			defer delete(state.arrays, id)
		}
		parts := make([]string, len(elems))
		for i, e := range elems {
			parts[i] = e.stringWithState(state)
		}
		return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
	case KindHash:
		entries := v.data.(map[string]Value)
		if len(entries) == 0 {
			return "{}"
		}
		ptr := reflect.ValueOf(entries).Pointer()
		if ptr != 0 {
			if _, seen := state.maps[ptr]; seen {
				return "<cycle>"
			}
			state.maps[ptr] = struct{}{}
			defer delete(state.maps, ptr)
		}
		parts := make([]string, 0, len(entries))
		for k, val := range entries {
			parts = append(parts, fmt.Sprintf("%s: %s", k, val.stringWithState(state)))
		}
		return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
	case KindRange:
		r := v.data.(Range)
		return fmt.Sprintf("%d..%d", r.Start, r.End)
	case KindEnum:
		enum := v.data.(*EnumDef)
		return fmt.Sprintf("<Enum %s>", enum.Name)
	case KindEnumValue:
		member := v.data.(*EnumValueDef)
		return fmt.Sprintf("%s::%s", member.Enum.Name, member.Name)
	case KindClass:
		cl := v.data.(*ClassDef)
		return fmt.Sprintf("<Class %s>", cl.Name)
	case KindInstance:
		inst := v.data.(*Instance)
		return fmt.Sprintf("<%s instance>", inst.Class.Name)
	default:
		return fmt.Sprintf("<%v>", v.kind)
	}
}

// Truthy reports whether v is considered true in a boolean context.
func (v Value) Truthy() bool {
	switch v.kind {
	case KindNil:
		return false
	case KindBool:
		return v.Bool()
	case KindInt:
		return v.data.(int64) != 0
	case KindFloat:
		return v.data.(float64) != 0
	case KindString:
		return v.data.(string) != ""
	case KindArray:
		return len(v.data.([]Value)) > 0
	case KindHash:
		return len(v.data.(map[string]Value)) > 0
	case KindEnum, KindEnumValue, KindClass, KindInstance:
		return true
	default:
		return true
	}
}

// Equal reports whether v and other hold the same kind and value.
func (v Value) Equal(other Value) bool {
	return valuesEqual(v, other, make(map[valueEqualityPair]struct{}))
}

type valueEqualityPair struct {
	kind     ValueKind
	leftPtr  uintptr
	rightPtr uintptr
	leftLen  int
	rightLen int
}

func valuesEqual(v Value, other Value, seen map[valueEqualityPair]struct{}) bool {
	if v.kind != other.kind {
		return false
	}
	switch v.kind {
	case KindNil:
		return true
	case KindBool:
		return v.Bool() == other.Bool()
	case KindInt:
		return v.data.(int64) == other.data.(int64)
	case KindFloat:
		return v.data.(float64) == other.data.(float64)
	case KindString, KindSymbol:
		return v.data.(string) == other.data.(string)
	case KindMoney:
		return v.data.(Money) == other.data.(Money)
	case KindDuration:
		return v.data.(Duration) == other.data.(Duration)
	case KindTime:
		return v.data.(time.Time).Equal(other.data.(time.Time))
	case KindRange:
		return v.data.(Range) == other.data.(Range)
	case KindArray:
		left := v.Array()
		right := other.Array()
		if len(left) != len(right) {
			return false
		}
		leftID := sliceIdentity{
			ptr: reflect.ValueOf(left).Pointer(),
			len: len(left),
			cap: cap(left),
		}
		rightID := sliceIdentity{
			ptr: reflect.ValueOf(right).Pointer(),
			len: len(right),
			cap: cap(right),
		}
		if leftID.ptr != 0 && leftID == rightID {
			return true
		}
		pair := valueEqualityPair{
			kind:     KindArray,
			leftPtr:  leftID.ptr,
			rightPtr: rightID.ptr,
			leftLen:  len(left),
			rightLen: len(right),
		}
		if pair.leftPtr != 0 || pair.rightPtr != 0 {
			if _, ok := seen[pair]; ok {
				return true
			}
			seen[pair] = struct{}{}
		}
		for i := range left {
			if !valuesEqual(left[i], right[i], seen) {
				return false
			}
		}
		return true
	case KindHash, KindObject:
		left := v.Hash()
		right := other.Hash()
		if len(left) != len(right) {
			return false
		}
		leftPtr := reflect.ValueOf(left).Pointer()
		rightPtr := reflect.ValueOf(right).Pointer()
		if leftPtr != 0 && leftPtr == rightPtr {
			return true
		}
		pair := valueEqualityPair{
			kind:     v.kind,
			leftPtr:  leftPtr,
			rightPtr: rightPtr,
			leftLen:  len(left),
			rightLen: len(right),
		}
		if pair.leftPtr != 0 || pair.rightPtr != 0 {
			if _, ok := seen[pair]; ok {
				return true
			}
			seen[pair] = struct{}{}
		}
		for key, leftValue := range left {
			rightValue, ok := right[key]
			if !ok {
				return false
			}
			if !valuesEqual(leftValue, rightValue, seen) {
				return false
			}
		}
		return true
	case KindFunction:
		return v.data.(*ScriptFunction) == other.data.(*ScriptFunction)
	case KindBuiltin:
		return v.data.(*Builtin) == other.data.(*Builtin)
	case KindBlock:
		return v.data.(*Block) == other.data.(*Block)
	case KindEnum:
		return enumDefsEqual(v.data.(*EnumDef), other.data.(*EnumDef))
	case KindEnumValue:
		return enumValueDefsEqual(v.data.(*EnumValueDef), other.data.(*EnumValueDef))
	case KindClass:
		return v.data.(*ClassDef) == other.data.(*ClassDef)
	case KindInstance:
		return v.data.(*Instance) == other.data.(*Instance)
	default:
		return false
	}
}

func enumDefsEqual(left *EnumDef, right *EnumDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	if left.Name != right.Name {
		return false
	}
	if left.owner == nil || right.owner == nil {
		return false
	}
	return left.owner == right.owner
}

func enumValueDefsEqual(left *EnumValueDef, right *EnumValueDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return left.Name == right.Name &&
		left.Symbol == right.Symbol &&
		left.Index == right.Index &&
		enumDefsEqual(left.Enum, right.Enum)
}
