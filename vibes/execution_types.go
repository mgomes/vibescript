package vibes

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

func checkValueType(val Value, ty *TypeExpr) error {
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

func formatTypeExpr(ty *TypeExpr) string {
	if ty == nil {
		return "unknown"
	}

	if ty.Kind == TypeUnion {
		if len(ty.Union) == 0 {
			return "unknown"
		}
		parts := make([]string, len(ty.Union))
		for i, option := range ty.Union {
			parts[i] = formatTypeExpr(option)
		}
		return strings.Join(parts, " | ")
	}

	var name string
	switch ty.Kind {
	case TypeAny:
		name = "any"
	case TypeInt:
		name = "int"
	case TypeFloat:
		name = "float"
	case TypeNumber:
		name = "number"
	case TypeString:
		name = "string"
	case TypeBool:
		name = "bool"
	case TypeNil:
		name = "nil"
	case TypeDuration:
		name = "duration"
	case TypeTime:
		name = "time"
	case TypeMoney:
		name = "money"
	case TypeArray:
		name = "array"
	case TypeHash:
		name = "hash"
	case TypeFunction:
		name = "function"
	case TypeShape:
		name = formatShapeType(ty)
	default:
		name = ty.Name
	}
	if name == "" {
		name = "unknown"
	}
	if len(ty.TypeArgs) > 0 {
		args := make([]string, len(ty.TypeArgs))
		for i, typeArg := range ty.TypeArgs {
			args[i] = formatTypeExpr(typeArg)
		}
		name = fmt.Sprintf("%s<%s>", name, strings.Join(args, ", "))
	}
	if ty.Nullable && !strings.HasSuffix(name, "?") {
		return name + "?"
	}
	return name
}

func formatShapeType(ty *TypeExpr) string {
	if ty == nil || len(ty.Shape) == 0 {
		return "{}"
	}
	fields := make([]string, 0, len(ty.Shape))
	for field := range ty.Shape {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	parts := make([]string, len(fields))
	for i, field := range fields {
		parts[i] = fmt.Sprintf("%s: %s", field, formatTypeExpr(ty.Shape[field]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}
