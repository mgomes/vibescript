package vibes

import (
	"fmt"
	"time"

	"github.com/google/go-cmp/cmp"
)

type valueSnapshot struct {
	Kind string
	Data any
}

type moneySnapshot struct {
	Cents    int64
	Currency string
}

type enumSnapshot struct {
	Identity string
	Name     string
	Order    []string
}

type enumValueSnapshot struct {
	Identity string
	Enum     string
	Name     string
	Symbol   string
	Index    int
}

type classSnapshot struct {
	Identity  string
	Name      string
	ClassVars map[string]valueSnapshot
}

type instanceSnapshot struct {
	Identity string
	Class    string
	Ivars    map[string]valueSnapshot
}

type functionSnapshot struct {
	Identity string
	Name     string
}

type builtinSnapshot struct {
	Identity   string
	Name       string
	AutoInvoke bool
}

type blockSnapshot struct {
	Identity string
	Params   []string
}

func valueDiff(want, got Value) string {
	return cmp.Diff(snapshotValue(want), snapshotValue(got))
}

func valuesDiff(want, got []Value) string {
	return cmp.Diff(snapshotValues(want), snapshotValues(got))
}

func valueMapDiff(want, got map[string]Value) string {
	return cmp.Diff(snapshotValueMap(want), snapshotValueMap(got))
}

func snapshotValue(val Value) valueSnapshot {
	snapshot := valueSnapshot{Kind: val.Kind().String()}

	switch val.Kind() {
	case KindNil:
	case KindBool:
		snapshot.Data = val.Bool()
	case KindInt:
		snapshot.Data = val.Int()
	case KindFloat:
		snapshot.Data = val.Float()
	case KindString, KindSymbol:
		snapshot.Data = val.String()
	case KindArray:
		snapshot.Data = snapshotValues(val.Array())
	case KindHash, KindObject:
		snapshot.Data = snapshotValueMap(val.Hash())
	case KindMoney:
		money := val.Money()
		snapshot.Data = moneySnapshot{
			Cents:    money.cents,
			Currency: money.currency,
		}
	case KindDuration:
		snapshot.Data = val.Duration().Seconds()
	case KindTime:
		snapshot.Data = val.Time().UTC().Format(time.RFC3339Nano)
	case KindRange:
		snapshot.Data = val.Range()
	case KindFunction:
		fn := val.Function()
		if fn != nil {
			snapshot.Data = functionSnapshot{
				Identity: fmt.Sprintf("%p", fn),
				Name:     fn.Name,
			}
		}
	case KindBuiltin:
		builtin := val.Builtin()
		if builtin != nil {
			snapshot.Data = builtinSnapshot{
				Identity:   fmt.Sprintf("%p", builtin),
				Name:       builtin.Name,
				AutoInvoke: builtin.AutoInvoke,
			}
		}
	case KindBlock:
		block := val.Block()
		if block != nil {
			params := make([]string, len(block.Params))
			for i, param := range block.Params {
				params[i] = param.Name
			}
			snapshot.Data = blockSnapshot{
				Identity: fmt.Sprintf("%p", block),
				Params:   params,
			}
		}
	case KindEnum:
		enumDef := val.Enum()
		if enumDef != nil {
			snapshot.Data = enumSnapshot{
				Identity: fmt.Sprintf("%p", enumDef),
				Name:     enumDef.Name,
				Order:    append([]string(nil), enumDef.Order...),
			}
		}
	case KindEnumValue:
		enumValue := val.EnumValue()
		if enumValue != nil {
			enumName := ""
			if enumValue.Enum != nil {
				enumName = enumValue.Enum.Name
			}
			snapshot.Data = enumValueSnapshot{
				Identity: fmt.Sprintf("%p", enumValue),
				Enum:     enumName,
				Name:     enumValue.Name,
				Symbol:   enumValue.Symbol,
				Index:    enumValue.Index,
			}
		}
	case KindClass:
		classDef := val.Class()
		if classDef != nil {
			snapshot.Data = classSnapshot{
				Identity:  fmt.Sprintf("%p", classDef),
				Name:      classDef.Name,
				ClassVars: snapshotValueMap(classDef.ClassVars),
			}
		}
	case KindInstance:
		inst := val.Instance()
		if inst != nil {
			className := ""
			if inst.Class != nil {
				className = inst.Class.Name
			}
			snapshot.Data = instanceSnapshot{
				Identity: fmt.Sprintf("%p", inst),
				Class:    className,
				Ivars:    snapshotValueMap(inst.Ivars),
			}
		}
	}

	return snapshot
}

func snapshotValues(values []Value) []valueSnapshot {
	out := make([]valueSnapshot, len(values))
	for i, val := range values {
		out[i] = snapshotValue(val)
	}
	return out
}

func snapshotValueMap(values map[string]Value) map[string]valueSnapshot {
	out := make(map[string]valueSnapshot, len(values))
	for key, val := range values {
		out[key] = snapshotValue(val)
	}
	return out
}
