package runtime

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

func checkValueType(val Value, ty *TypeExpr) error {
	if handled, matches := quickTypeCheck(val, ty); handled {
		if matches {
			return nil
		}
		return &typeMismatchError{
			Expected: formatTypeExpr(ty),
			Actual:   formatValueTypeExpr(val),
		}
	}
	matches, err := valueMatchesType(val, ty)
	if err != nil {
		return err
	}
	if matches {
		return nil
	}
	return &typeMismatchError{
		Expected: formatTypeExpr(ty),
		Actual:   formatValueTypeExpr(val),
	}
}

func quickTypeCheck(val Value, ty *TypeExpr) (bool, bool) {
	if ty == nil {
		return false, false
	}
	if ty.Nullable && val.Kind() == KindNil && ty.Kind != TypeUnknown {
		return true, true
	}

	switch ty.Kind {
	case TypeAny:
		return true, true
	case TypeInt:
		return true, val.Kind() == KindInt
	case TypeFloat:
		return true, val.Kind() == KindFloat
	case TypeNumber:
		return true, val.Kind() == KindInt || val.Kind() == KindFloat
	case TypeString:
		return true, val.Kind() == KindString
	case TypeBool:
		return true, val.Kind() == KindBool
	case TypeNil:
		return true, val.Kind() == KindNil
	case TypeDuration:
		return true, val.Kind() == KindDuration
	case TypeTime:
		return true, val.Kind() == KindTime
	case TypeMoney:
		return true, val.Kind() == KindMoney
	case TypeFunction:
		return true, isCallableValue(val)
	case TypeArray:
		if len(ty.TypeArgs) == 0 {
			return true, val.Kind() == KindArray
		}
		return false, false
	case TypeHash:
		if len(ty.TypeArgs) == 0 {
			return true, val.Kind() == KindHash || val.Kind() == KindObject
		}
		return false, false
	case TypeShape:
		if len(ty.Shape) == 0 {
			if val.Kind() != KindHash && val.Kind() != KindObject {
				return true, false
			}
			return true, len(val.Hash()) == 0
		}
		return false, false
	case TypeUnion:
		allHandled := true
		for _, option := range ty.Union {
			handled, matches := quickTypeCheck(val, option)
			if handled {
				if matches {
					return true, true
				}
				continue
			}
			allHandled = false
			break
		}
		if allHandled {
			return true, false
		}
		return false, false
	default:
		return false, false
	}
}

type typeMismatchError struct {
	Expected string
	Actual   string
}

func (e *typeMismatchError) Error() string {
	return fmt.Sprintf("expected %s, got %s", e.Expected, e.Actual)
}

func formatArgumentTypeMismatch(name string, err error) string {
	var mismatch *typeMismatchError
	if errors.As(err, &mismatch) {
		return fmt.Sprintf("argument %s expected %s, got %s", name, mismatch.Expected, mismatch.Actual)
	}
	return fmt.Sprintf("argument %s type check failed: %s", name, err.Error())
}

func formatReturnTypeMismatch(fnName string, err error) string {
	var mismatch *typeMismatchError
	if errors.As(err, &mismatch) {
		return fmt.Sprintf("return value for %s expected %s, got %s", fnName, mismatch.Expected, mismatch.Actual)
	}
	return fmt.Sprintf("return type check failed for %s: %s", fnName, err.Error())
}

// formatTypeExpr is kept as a thin alias to ast.FormatTypeExpr so the
// many runtime call sites continue to compile unchanged.
func formatTypeExpr(ty *TypeExpr) string { return ast.FormatTypeExpr(ty) }

type typeValidationVisit struct {
	valueKind ValueKind
	valueID   uintptr
	ty        *TypeExpr
}

type typeValidationState struct {
	active map[typeValidationVisit]struct{}
}

func valueMatchesType(val Value, ty *TypeExpr) (bool, error) {
	state := typeValidationState{
		active: make(map[typeValidationVisit]struct{}),
	}
	return state.matches(val, ty)
}

func (s *typeValidationState) matches(val Value, ty *TypeExpr) (bool, error) {
	if visit, ok := typeValidationVisitFor(val, ty); ok {
		if _, seen := s.active[visit]; seen {
			// Recursive value/type pair already being validated higher in the stack.
			return true, nil
		}
		s.active[visit] = struct{}{}
		defer delete(s.active, visit)
	}

	if ty.Nullable && val.Kind() == KindNil && ty.Kind != TypeUnknown {
		return true, nil
	}
	switch ty.Kind {
	case TypeAny:
		return true, nil
	case TypeInt:
		return val.Kind() == KindInt, nil
	case TypeFloat:
		return val.Kind() == KindFloat, nil
	case TypeNumber:
		return val.Kind() == KindInt || val.Kind() == KindFloat, nil
	case TypeString:
		return val.Kind() == KindString, nil
	case TypeBool:
		return val.Kind() == KindBool, nil
	case TypeNil:
		return val.Kind() == KindNil, nil
	case TypeDuration:
		return val.Kind() == KindDuration, nil
	case TypeTime:
		return val.Kind() == KindTime, nil
	case TypeMoney:
		return val.Kind() == KindMoney, nil
	case TypeArray:
		if val.Kind() != KindArray {
			return false, nil
		}
		if len(ty.TypeArgs) == 0 {
			return true, nil
		}
		if len(ty.TypeArgs) != 1 {
			return false, fmt.Errorf("array type expects exactly 1 type argument")
		}
		elemType := ty.TypeArgs[0]
		for _, elem := range val.Array() {
			matches, err := s.matches(elem, elemType)
			if err != nil {
				return false, err
			}
			if !matches {
				return false, nil
			}
		}
		return true, nil
	case TypeHash:
		if val.Kind() != KindHash && val.Kind() != KindObject {
			return false, nil
		}
		if len(ty.TypeArgs) == 0 {
			return true, nil
		}
		if len(ty.TypeArgs) != 2 {
			return false, fmt.Errorf("hash type expects exactly 2 type arguments")
		}
		keyType := ty.TypeArgs[0]
		valueType := ty.TypeArgs[1]
		if decided, keyMatches := typeAllowsStringHashKey(keyType); decided {
			if !keyMatches {
				return false, nil
			}
			for _, value := range val.Hash() {
				valueMatches, err := s.matches(value, valueType)
				if err != nil {
					return false, err
				}
				if !valueMatches {
					return false, nil
				}
			}
			return true, nil
		}
		for key, value := range val.Hash() {
			keyMatches, err := s.matches(NewString(key), keyType)
			if err != nil {
				return false, err
			}
			if !keyMatches {
				return false, nil
			}
			valueMatches, err := s.matches(value, valueType)
			if err != nil {
				return false, err
			}
			if !valueMatches {
				return false, nil
			}
		}
		return true, nil
	case TypeFunction:
		return isCallableValue(val), nil
	case TypeEnum:
		member := valueEnumValue(val)
		return member != nil && member.Enum != nil && member.Enum.Name == ty.Name, nil
	case TypeShape:
		if val.Kind() != KindHash && val.Kind() != KindObject {
			return false, nil
		}
		entries := val.Hash()
		if len(entries) != len(ty.Shape) {
			return false, nil
		}
		if len(ty.Shape) == 0 {
			return true, nil
		}
		for field, fieldType := range ty.Shape {
			fieldVal, ok := entries[field]
			if !ok {
				return false, nil
			}
			matches, err := s.matches(fieldVal, fieldType)
			if err != nil {
				return false, err
			}
			if !matches {
				return false, nil
			}
		}
		for field := range entries {
			if _, ok := ty.Shape[field]; !ok {
				return false, nil
			}
		}
		return true, nil
	case TypeUnion:
		for _, option := range ty.Union {
			matches, err := s.matches(val, option)
			if err != nil {
				return false, err
			}
			if matches {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("unknown type %s", ty.Name)
	}
}

func isCallableValue(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin, KindBlock:
		return true
	default:
		return false
	}
}

func typeValidationVisitFor(val Value, ty *TypeExpr) (typeValidationVisit, bool) {
	if ty == nil {
		return typeValidationVisit{}, false
	}

	var valueID uintptr
	switch val.Kind() {
	case KindArray:
		valueID = reflect.ValueOf(val.Array()).Pointer()
	case KindHash, KindObject:
		valueID = reflect.ValueOf(val.Hash()).Pointer()
	default:
		return typeValidationVisit{}, false
	}
	if valueID == 0 {
		return typeValidationVisit{}, false
	}

	return typeValidationVisit{
		valueKind: val.Kind(),
		valueID:   valueID,
		ty:        ty,
	}, true
}

func typeAllowsStringHashKey(ty *TypeExpr) (bool, bool) {
	if ty == nil {
		return false, false
	}

	switch ty.Kind {
	case TypeUnknown, TypeEnum:
		// Unknown and enum key types must flow through full matching so callers
		// preserve unknown-type/resolution errors instead of silently treating
		// them as mismatches.
		return false, false
	case TypeAny, TypeString:
		return true, true
	case TypeUnion:
		anyMatches := false
		for _, option := range ty.Union {
			decided, matches := typeAllowsStringHashKey(option)
			if !decided {
				return false, false
			}
			if matches {
				anyMatches = true
			}
		}
		return true, anyMatches
	default:
		if ty.Nullable {
			clone := *ty
			clone.Nullable = false
			return typeAllowsStringHashKey(&clone)
		}
		return true, false
	}
}

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
	case KindEnum:
		if enumDef := valueEnum(val); enumDef != nil {
			return "enum " + enumDef.Name
		}
		return "enum"
	case KindEnumValue:
		if member := valueEnumValue(val); member != nil && member.Enum != nil {
			return member.Enum.Name
		}
		return "enum"
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
