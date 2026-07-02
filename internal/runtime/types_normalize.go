package runtime

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

const (
	maxNormalizeDepth          = 64
	normalizationCheckInterval = 64
)

type typeContext struct {
	owner    *Script
	env      *Env
	fallback *Env
	exec     *Execution
	depth    int
}

func normalizeValueForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	if err := ctx.checkSandbox(); err != nil {
		return NewNil(), err
	}
	if ty == nil {
		return val, nil
	}
	if ctx.depth == 0 {
		if err := validateTypeExprResolved(ty, ctx); err != nil {
			return NewNil(), err
		}
	}
	if ctx.depth >= maxNormalizeDepth {
		return NewNil(), guardLimitErrorf("type normalization exceeded maximum depth")
	}
	ctx.depth++
	if nullableNilCanBypassResolution(ty, val) {
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
		if isCallableValue(val) {
			return val, nil
		}
	case TypeArray:
		return normalizeArrayForType(val, ty, ctx)
	case TypeHash:
		return normalizeHashForType(val, ty, ctx)
	case TypeRange:
		if val.Kind() == KindRange {
			return val, nil
		}
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
		if ty.Nullable && val.Kind() == KindNil {
			if _, err := resolveEnumType(ty, ctx); err != nil {
				return NewNil(), err
			}
			return val, nil
		}
		return normalizeEnumForType(val, ty, ctx)
	case TypeUnknown:
		return NewNil(), fmt.Errorf("unknown type %s", ty.Name)
	}

	return NewNil(), &typeMismatchError{
		Expected: formatTypeExpr(ty),
		Actual:   formatValueTypeExpr(val),
	}
}

func (ctx typeContext) checkSandbox(extra ...Value) error {
	if ctx.exec == nil {
		return nil
	}
	if err := ctx.exec.checkContext(); err != nil {
		return err
	}
	if len(extra) > 0 {
		return ctx.exec.checkMemoryWith(extra...)
	}
	return nil
}

func (ctx typeContext) checkSandboxEvery(index int, extra ...Value) error {
	if index%normalizationCheckInterval != 0 {
		return nil
	}
	return ctx.checkSandbox(extra...)
}

func (ctx typeContext) reserveArraySlots(source Value, count int) error {
	if ctx.exec == nil {
		return nil
	}
	return newArrayBuildAccumulator(ctx.exec, source, nil, nil, NewNil()).reserveSlots(count)
}

func (ctx typeContext) reserveHashEntries(source Value, count int) error {
	if ctx.exec == nil {
		return nil
	}
	return ctx.exec.checkProjectedHashBytes(count, source, nil, nil, NewNil())
}

func (ctx typeContext) normalizedMap(source Value, entries map[string]Value) (map[string]Value, error) {
	if err := ctx.reserveHashEntries(source, len(entries)); err != nil {
		return nil, err
	}
	out := make(map[string]Value, len(entries))
	for key, item := range entries {
		out[key] = item
	}
	return out, nil
}

func nullableNilCanBypassResolution(ty *TypeExpr, val Value) bool {
	return ty.Nullable && val.Kind() == KindNil && ty.Kind != TypeUnknown && ty.Kind != TypeEnum
}

func isNormalizationLimitError(err error) bool {
	return classifyRuntimeErrorType(err) == runtimeErrorTypeLimit
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
	var out []Value
	for i, item := range items {
		if err := ctx.checkSandboxEvery(i); err != nil {
			return NewNil(), err
		}
		normalized, err := normalizeValueForType(item, ty.TypeArgs[0], ctx)
		if err != nil {
			var mismatch *typeMismatchError
			if errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
			}
			return NewNil(), err
		}
		if !sameNormalizedValue(normalized, item) {
			if out == nil {
				if err := ctx.reserveArraySlots(val, len(items)); err != nil {
					return NewNil(), err
				}
				out = make([]Value, len(items))
				copy(out, items[:i])
			}
		}
		if out != nil {
			out[i] = normalized
		}
	}
	if out == nil {
		return val, nil
	}
	result := NewArray(out)
	if err := ctx.checkSandbox(result); err != nil {
		return NewNil(), err
	}
	return result, nil
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
	var out map[string]Value

	if decided, keyMatches := typeAllowsStringHashKey(keyType); decided {
		if !keyMatches {
			return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
		}
	} else {
		i := 0
		for key := range entries {
			if err := ctx.checkSandboxEvery(i); err != nil {
				return NewNil(), err
			}
			i++
			if _, err := normalizeValueForType(NewString(key), keyType, ctx); err != nil {
				var mismatch *typeMismatchError
				if errorAsTypeMismatch(err, &mismatch) {
					return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
				}
				return NewNil(), err
			}
		}
	}

	i := 0
	for key, item := range entries {
		if err := ctx.checkSandboxEvery(i); err != nil {
			return NewNil(), err
		}
		i++
		normalized, err := normalizeValueForType(item, valueType, ctx)
		if err != nil {
			var mismatch *typeMismatchError
			if errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
			}
			return NewNil(), err
		}
		if !sameNormalizedValue(normalized, item) {
			if out == nil {
				var err error
				out, err = ctx.normalizedMap(val, entries)
				if err != nil {
					return NewNil(), err
				}
			}
		}
		if out != nil {
			out[key] = normalized
		}
	}

	// A Ruby-style hash default is consulted on missing-key lookup, so it is part
	// of the hash's value type: an empty Hash.new("oops") would otherwise satisfy
	// hash<string, int> while result[:missing] yields a string. Validate (and
	// carry through) the default value, and reject default procs whose result the
	// type checker cannot inspect.
	defaultProc := hashDefaultProc(val)
	if !defaultProc.IsNil() {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val) + " with a default proc"}
	}
	defaultValue := hashDefaultValue(val)
	normalizedDefault := defaultValue
	if !defaultValue.IsNil() {
		converted, err := normalizeValueForType(defaultValue, valueType, ctx)
		if err != nil {
			var mismatch *typeMismatchError
			if errorAsTypeMismatch(err, &mismatch) {
				return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
			}
			return NewNil(), err
		}
		normalizedDefault = converted
		if !sameNormalizedValue(normalizedDefault, defaultValue) {
			if out == nil {
				var err error
				out, err = ctx.normalizedMap(val, entries)
				if err != nil {
					return NewNil(), err
				}
			}
		}
	}

	if out == nil {
		return val, nil
	}
	if val.Kind() == KindObject {
		result := NewObject(out)
		if err := ctx.checkSandbox(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}
	if defaultValue.IsNil() {
		result := NewHash(out)
		if err := ctx.checkSandbox(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}
	result := NewHashWithDefault(out, normalizedDefault, NewNil())
	if err := ctx.checkSandbox(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func normalizeShapeForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	if val.Kind() != KindHash && val.Kind() != KindObject {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
	}
	entries := val.Hash()
	if len(entries) != len(ty.Shape) {
		return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
	}

	var out map[string]Value
	i := 0
	for field, fieldType := range ty.Shape {
		if err := ctx.checkSandboxEvery(i); err != nil {
			return NewNil(), err
		}
		i++
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
		if !sameNormalizedValue(normalized, fieldVal) {
			if out == nil {
				var err error
				out, err = ctx.normalizedMap(val, entries)
				if err != nil {
					return NewNil(), err
				}
			}
		}
		if out != nil {
			out[field] = normalized
		}
	}
	for field := range entries {
		if _, ok := ty.Shape[field]; !ok {
			return NewNil(), &typeMismatchError{Expected: formatTypeExpr(ty), Actual: formatValueTypeExpr(val)}
		}
	}

	if out == nil {
		return val, nil
	}
	if val.Kind() == KindObject {
		result := NewObject(out)
		if err := ctx.checkSandbox(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}
	result := NewHash(out)
	if err := ctx.checkSandbox(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func normalizeEnumForType(val Value, ty *TypeExpr, ctx typeContext) (Value, error) {
	enumDef, err := resolveEnumType(ty, ctx)
	if err != nil {
		return NewNil(), err
	}

	switch val.Kind() {
	case KindEnumValue:
		if member := valueEnumValue(val); member != nil && member.Enum == enumDef {
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
	enumDef, ok, err := lookupEnumInEnv(ctx.env, ty.Name)
	if err != nil {
		return nil, err
	}
	if ok {
		return enumDef, nil
	}
	if ctx.fallback != ctx.env {
		enumDef, ok, err := lookupEnumInEnv(ctx.fallback, ty.Name)
		if err != nil {
			return nil, err
		}
		if ok {
			return enumDef, nil
		}
	}
	enumDef, ok, err = lookupEnumDef(ctx.owner, ty.Name)
	if err != nil {
		return nil, err
	}
	if ok {
		return enumDef, nil
	}
	return nil, fmt.Errorf("unknown type %s", ty.Name)
}

func validateTypeExprResolved(ty *TypeExpr, ctx typeContext) error {
	if ty == nil {
		return nil
	}

	switch ty.Kind {
	case TypeUnknown:
		return fmt.Errorf("unknown type %s", ty.Name)
	case TypeEnum:
		if _, err := resolveEnumType(ty, ctx); err != nil {
			return err
		}
	}

	for _, arg := range ty.TypeArgs {
		if err := validateTypeExprResolved(arg, ctx); err != nil {
			return err
		}
	}
	for _, option := range ty.Union {
		if err := validateTypeExprResolved(option, ctx); err != nil {
			return err
		}
	}
	for _, field := range ty.Shape {
		if err := validateTypeExprResolved(field, ctx); err != nil {
			return err
		}
	}
	return nil
}

func lookupEnumDef(owner *Script, name string) (*EnumDef, bool, error) {
	if owner == nil || len(owner.enums) == 0 {
		return nil, false, nil
	}
	if enumDef, ok := owner.enums[name]; ok {
		return enumDef, true, nil
	}
	var match *EnumDef
	matches := make([]string, 0, 2)
	for enumName, enumDef := range owner.enums {
		if !strings.EqualFold(enumName, name) {
			continue
		}
		matches = append(matches, enumName)
		if match == nil {
			match = enumDef
			continue
		}
		if match != enumDef {
			return nil, false, ambiguousEnumTypeError(name, matches)
		}
	}
	if match != nil {
		return match, true, nil
	}
	return nil, false, nil
}

func lookupEnumInEnv(env *Env, name string) (*EnumDef, bool, error) {
	for scope := env; scope != nil; scope = scope.parent {
		if enumDef, ok, err := lookupEnumInScope(scope, name); err != nil {
			return nil, false, err
		} else if ok {
			return enumDef, true, nil
		}
	}
	return nil, false, nil
}

// lookupEnumInScope considers a scope's dynamic and static bindings as
// one namespace: an exact name wins outright (a name lives in only one
// of the two maps), while case-insensitive matches accumulate across
// both maps so a collision between, say, a script-defined static enum
// and a host-supplied dynamic one still reports ambiguity.
func lookupEnumInScope(scope *Env, name string) (*EnumDef, bool, error) {
	if val, ok := scope.getOwn(name); ok && val.Kind() == KindEnum {
		return valueEnum(val), true, nil
	}

	var match *EnumDef
	matches := make([]string, 0, 2)
	var scanErr error
	scan := func(key string, val Value) {
		if scanErr != nil || key == name || !strings.EqualFold(key, name) || val.Kind() != KindEnum {
			return
		}
		matches = append(matches, key)
		if match == nil {
			match = valueEnum(val)
			return
		}
		if match != valueEnum(val) {
			scanErr = ambiguousEnumTypeError(name, matches)
		}
	}
	scope.rangeDynamicBindings(scan)
	for key, val := range scope.statics {
		scan(key, val)
	}
	if scanErr != nil {
		return nil, false, scanErr
	}
	if match != nil {
		return match, true, nil
	}
	return nil, false, nil
}

func ambiguousEnumTypeError(name string, matches []string) error {
	sort.Strings(matches)
	return fmt.Errorf("ambiguous enum type %s matches %s", name, strings.Join(matches, ", "))
}

func errorAsTypeMismatch(err error, target **typeMismatchError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

func sameNormalizedValue(left, right Value) bool {
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
