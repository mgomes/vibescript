package vibes

import (
	"fmt"
	"reflect"
)

type typeContext struct {
	owner    *Script
	env      *Env
	fallback *Env
}

func normalizeValueForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	if ty == nil {
		return val, nil
	}
	if ty.Nullable && val.Kind() == KindNil {
		return val, nil
	}

	switch ty.Kind {
	case TypeAny:
		return val, nil
	case TypeInt:
		if val.Kind() == KindInt {
			return val, nil
		}
	case TypeFloat:
		if val.Kind() == KindFloat {
			return val, nil
		}
	case TypeNumber:
		if val.Kind() == KindInt || val.Kind() == KindFloat {
			return val, nil
		}
	case TypeString:
		if val.Kind() == KindString {
			return val, nil
		}
	case TypeBool:
		if val.Kind() == KindBool {
			return val, nil
		}
	case TypeNil:
		if val.Kind() == KindNil {
			return val, nil
		}
	case TypeDuration:
		if val.Kind() == KindDuration {
			return val, nil
		}
	case TypeTime:
		if val.Kind() == KindTime {
			return val, nil
		}
	case TypeMoney:
		if val.Kind() == KindMoney {
			return val, nil
		}
	case TypeFunction:
		if val.Kind() == KindFunction {
			return val, nil
		}
	case TypeArray:
		return normalizeArrayForType(val, ty, ctx)
	case TypeHash:
		return normalizeHashForType(val, ty, ctx)
	case TypeShape:
		return normalizeShapeForType(val, ty, ctx)
	case TypeUnion:
		for _, option := range ty.Union {
			normalized, err := normalizeValueForType(val, option, ctx)
			if err == nil {
				return normalized, nil
			}
			var mismatch *typeMismatchError
			if !errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), err
			}
		}
	case TypeEnum:
		return normalizeEnumForType(val, ty, ctx)
	case TypeUnknown:
		return NewNil(), fmt.Errorf("unknown type %s", ty.Name)
	}

	return NewNil(), &typeMismatchError{
		Expected: formatTypeExpr(ty),
		Actual:   formatValueTypeExpr(val),
	}
}

func normalizeArrayForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	if val.Kind() != KindArray {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
	}
	if len(ty.TypeArgs) == 0 {
		return val, nil
	}
	if len(ty.TypeArgs) != 1 {
		return NewNil(), fmt.Errorf("array type expects exactly 1 type argument")
	}

	items := val.Array()
	out := make([]Value, len(items))
	changed := false
	for i, item := range items {
		normalized, err := normalizeValueForType(item, ty.TypeArgs[0], ctx)
		if err != nil {
			var mismatch *typeMismatchError
			if errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
			}
			return NewNil(), err
		}
		out[i] = normalized
		if !sameNormalizedValue(normalized, item) {
			changed = true
		}
	}
	if !changed {
		return val, nil
	}
	return NewArray(out), nil
}

func normalizeHashForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	if val.Kind() != KindHash && val.Kind() != KindObject {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
	}
	if len(ty.TypeArgs) == 0 {
		return val, nil
	}
	if len(ty.TypeArgs) != 2 {
		return NewNil(), fmt.Errorf("hash type expects exactly 2 type arguments")
	}

	keyType := ty.TypeArgs[0]
	valueType := ty.TypeArgs[1]
	entries := val.Hash()
	out := make(map[string]Value, len(entries))
	changed := false

	if decided, keyMatches := typeAllowsStringHashKey(keyType); decided {
		if !keyMatches {
			return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
		}
	} else {
		for key := range entries {
			if _, err := normalizeValueForType(NewString(key), keyType, ctx); err != nil {
				var mismatch *typeMismatchError
				if errorAsTypeMismatch(err, &mismatch) {
					return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
				}
				return NewNil(), err
			}
		}
	}

	for key, item := range entries {
		normalized, err := normalizeValueForType(item, valueType, ctx)
		if err != nil {
			var mismatch *typeMismatchError
			if errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
			}
			return NewNil(), err
		}
		out[key] = normalized
		if !sameNormalizedValue(normalized, item) {
			changed = true
		}
	}

	if !changed {
		return val, nil
	}
	if val.Kind() == KindObject {
		return NewObject(out), nil
	}
	return NewHash(out), nil
}

func normalizeShapeForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	if val.Kind() != KindHash && val.Kind() != KindObject {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
	}
	entries := val.Hash()
	if len(entries) != len(ty.Shape) {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
	}

	out := make(map[string]Value, len(entries))
	changed := false
	for field, fieldType := range ty.Shape {
		fieldVal, ok := entries[field]
		if !ok {
			return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
		}
		normalized, err := normalizeValueForType(fieldVal, fieldType, ctx)
		if err != nil {
			var mismatch *typeMismatchError
			if errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
			}
			return NewNil(), err
		}
		out[field] = normalized
		if !sameNormalizedValue(normalized, fieldVal) {
			changed = true
		}
	}
	for field := range entries {
		if _, ok := ty.Shape[field]; !ok {
			return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
		}
	}

	if !changed {
		return val, nil
	}
	if val.Kind() == KindObject {
		return NewObject(out), nil
	}
	return NewHash(out), nil
}

func normalizeEnumForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	enumDef, err := resolveEnumType(ty, ctx)
	if err != nil {
		return NewNil(), err
	}

	switch val.Kind() {
	case KindEnumValue:
		if member := val.EnumValue(); member != nil && member.Enum == enumDef {
			return val, nil
		}
	case KindSymbol:
		if member, ok := enumDef.MembersByKey[val.String()]; ok {
			return NewEnumValue(member), nil
		}
	}

	return NewNil(), &typeMismatchError{
		Expected: formatTypeExpr(ty),
		Actual:   formatValueTypeExpr(val),
	}
}

func resolveEnumType(ty *TypeExpr, ctx typeContext) (*EnumDef, error) {
	if ty == nil {
		return nil, fmt.Errorf("unknown type")
	}
	if ty.Kind != TypeEnum {
		return nil, fmt.Errorf("unknown type %s", ty.Name)
	}
	if ctx.owner != nil && ctx.owner.enums != nil {
		if enumDef, ok := ctx.owner.enums[ty.Name]; ok {
			return enumDef, nil
		}
	}
	if enumDef, ok := lookupEnumInEnv(ctx.env, ty.Name); ok {
		return enumDef, nil
	}
	if ctx.fallback != ctx.env {
		if enumDef, ok := lookupEnumInEnv(ctx.fallback, ty.Name); ok {
			return enumDef, nil
		}
	}
	return nil, fmt.Errorf("unknown type %s", ty.Name)
}

func lookupEnumInEnv(env *Env, name string) (*EnumDef, bool) {
	if env == nil {
		return nil, false
	}
	val, ok := env.Get(name)
	if !ok || val.Kind() != KindEnum {
		return nil, false
	}
	return val.Enum(), true
}

func errorAsTypeMismatch(err error, target **typeMismatchError) bool {
	if err == nil {
		return false
	}
	mismatch, ok := err.(*typeMismatchError)
	if !ok {
		return false
	}
	*target = mismatch
	return true
}

func sameNormalizedValue(left Value, right Value) bool {
	if left.Kind() != right.Kind() {
		return false
	}

	switch left.Kind() {
	case KindArray:
		leftArr := left.Array()
		rightArr := right.Array()
		return len(leftArr) == len(rightArr) &&
			cap(leftArr) == cap(rightArr) &&
			reflect.ValueOf(leftArr).Pointer() == reflect.ValueOf(rightArr).Pointer()
	case KindHash, KindObject:
		return reflect.ValueOf(left.Hash()).Pointer() == reflect.ValueOf(right.Hash()).Pointer()
	default:
		return left.Equal(right)
	}
}
