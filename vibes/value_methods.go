package vibes

import (
	"fmt"
	"strings"
	"time"
)

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
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

func (v Value) String() string {
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
		parts := make([]string, len(elems))
		for i, e := range elems {
			parts[i] = e.String()
		}
		return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
	case KindHash:
		entries := v.data.(map[string]Value)
		if len(entries) == 0 {
			return "{}"
		}
		parts := make([]string, 0, len(entries))
		for k, val := range entries {
			parts = append(parts, fmt.Sprintf("%s: %s", k, val.String()))
		}
		return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
	case KindRange:
		r := v.data.(Range)
		return fmt.Sprintf("%d..%d", r.Start, r.End)
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
	case KindClass, KindInstance:
		return true
	default:
		return true
	}
}

func (v Value) Equal(other Value) bool {
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
	case KindClass:
		return v.data.(*ClassDef) == other.data.(*ClassDef)
	case KindInstance:
		return v.data.(*Instance) == other.data.(*Instance)
	default:
		return v.data == other.data
	}
}
