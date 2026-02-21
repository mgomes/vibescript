package vibes

import (
	"fmt"
	"reflect"
)

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

	if ty.Nullable && val.Kind() == KindNil {
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
		return val.Kind() == KindFunction, nil
	case TypeShape:
		if val.Kind() != KindHash && val.Kind() != KindObject {
			return false, nil
		}
		entries := val.Hash()
		if len(ty.Shape) == 0 {
			return len(entries) == 0, nil
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
