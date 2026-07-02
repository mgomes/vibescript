package ast

import (
	"fmt"
	"sort"
	"strings"
)

// ResolveType maps a textual type name (with optional trailing "?" to
// mark nullability) to its TypeKind. It returns TypeUnknown when the
// name does not match a built-in type.
func ResolveType(name string) (TypeKind, bool) {
	nullable := false
	if strings.HasSuffix(name, "?") {
		nullable = true
		name = strings.TrimSuffix(name, "?")
	}
	switch strings.ToLower(name) {
	case "any":
		return TypeAny, nullable
	case "int":
		return TypeInt, nullable
	case "float":
		return TypeFloat, nullable
	case "number":
		return TypeNumber, nullable
	case "string":
		return TypeString, nullable
	case "bool":
		return TypeBool, nullable
	case "nil":
		return TypeNil, nullable
	case "duration":
		return TypeDuration, nullable
	case "time":
		return TypeTime, nullable
	case "money":
		return TypeMoney, nullable
	case "array":
		return TypeArray, nullable
	case "hash", "object":
		return TypeHash, nullable
	case "range":
		return TypeRange, nullable
	case "function":
		return TypeFunction, nullable
	}
	return TypeUnknown, nullable
}

// FormatTypeExpr returns a stable textual representation of a TypeExpr
// suitable for use in error messages.
func FormatTypeExpr(ty *TypeExpr) string {
	if ty == nil {
		return "unknown"
	}

	if ty.Kind == TypeUnion {
		if len(ty.Union) == 0 {
			return "unknown"
		}
		parts := make([]string, len(ty.Union))
		for i, option := range ty.Union {
			parts[i] = FormatTypeExpr(option)
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
		name = formatHashTypeName(ty)
	case TypeRange:
		name = "range"
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
			args[i] = FormatTypeExpr(typeArg)
		}
		name = fmt.Sprintf("%s<%s>", name, strings.Join(args, ", "))
	}
	if ty.Nullable && !strings.HasSuffix(name, "?") {
		return name + "?"
	}
	return name
}

func formatHashTypeName(ty *TypeExpr) string {
	base := strings.TrimSuffix(strings.ToLower(ty.Name), "?")
	if base == "object" {
		return "object"
	}
	return "hash"
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
		parts[i] = fmt.Sprintf("%s: %s", field, FormatTypeExpr(ty.Shape[field]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// FormatParamTarget returns the parameter's binding target in source form.
func FormatParamTarget(param Param) string {
	label := param.Name
	if param.Target != nil {
		label = FormatDestructureTarget(param.Target)
	}
	switch param.Kind {
	case ParamKeyword:
		label += ":"
	case ParamRest:
		label = "*" + label
	case ParamKeywordRest:
		label = "**" + label
	case ParamBlock:
		label = "&" + label
	}
	return label
}

// FormatDestructureTarget returns a destructuring binding target in source form.
func FormatDestructureTarget(target Expression) string {
	switch t := target.(type) {
	case *Identifier:
		return t.Name
	case *DestructureTarget:
		parts := make([]string, len(t.Elements))
		for i, element := range t.Elements {
			parts[i] = FormatDestructureTarget(element.Target)
			if element.Rest {
				parts[i] = "*" + parts[i]
			}
			if element.Type != nil {
				parts[i] += ": " + FormatTypeExpr(element.Type)
			}
		}
		return "(" + strings.Join(parts, ", ") + ")"
	default:
		return ""
	}
}
