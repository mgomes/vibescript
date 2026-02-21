package vibes

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func formatValueTypeExpr(val Value) string {
	state := valueTypeFormatState{
		seenArrays: make(map[uintptr]struct{}),
		seenHashes: make(map[uintptr]struct{}),
	}
	return state.format(val)
}

type valueTypeFormatState struct {
	seenArrays map[uintptr]struct{}
	seenHashes map[uintptr]struct{}
}

func (s *valueTypeFormatState) format(val Value) string {
	switch val.Kind() {
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
	case KindMoney:
		return "money"
	case KindDuration:
		return "duration"
	case KindTime:
		return "time"
	case KindSymbol:
		return "symbol"
	case KindRange:
		return "range"
	case KindFunction:
		return "function"
	case KindBuiltin:
		return "builtin"
	case KindBlock:
		return "block"
	case KindClass:
		return "class"
	case KindInstance:
		return "instance"
	case KindArray:
		return s.formatArray(val.Array())
	case KindHash, KindObject:
		return s.formatHash(val.Hash())
	default:
		return val.Kind().String()
	}
}

func (s *valueTypeFormatState) formatArray(values []Value) string {
	if len(values) == 0 {
		return "array<empty>"
	}

	id := reflect.ValueOf(values).Pointer()
	if id != 0 {
		if _, seen := s.seenArrays[id]; seen {
			return "array<...>"
		}
		s.seenArrays[id] = struct{}{}
		defer delete(s.seenArrays, id)
	}

	elementTypes := make(map[string]struct{}, len(values))
	for _, value := range values {
		elementTypes[s.format(value)] = struct{}{}
	}
	return "array<" + joinSortedTypes(elementTypes) + ">"
}

func (s *valueTypeFormatState) formatHash(values map[string]Value) string {
	if len(values) == 0 {
		return "{}"
	}

	id := reflect.ValueOf(values).Pointer()
	if id != 0 {
		if _, seen := s.seenHashes[id]; seen {
			return "{ ... }"
		}
		s.seenHashes[id] = struct{}{}
		defer delete(s.seenHashes, id)
	}

	if len(values) <= 6 {
		fields := make([]string, 0, len(values))
		for field := range values {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		parts := make([]string, len(fields))
		for i, field := range fields {
			parts[i] = fmt.Sprintf("%s: %s", field, s.format(values[field]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}

	valueTypes := make(map[string]struct{}, len(values))
	for _, value := range values {
		valueTypes[s.format(value)] = struct{}{}
	}
	return "hash<string, " + joinSortedTypes(valueTypes) + ">"
}

func joinSortedTypes(typeSet map[string]struct{}) string {
	if len(typeSet) == 0 {
		return "empty"
	}
	parts := make([]string, 0, len(typeSet))
	for typeName := range typeSet {
		parts = append(parts, typeName)
	}
	sort.Strings(parts)
	return strings.Join(parts, " | ")
}
