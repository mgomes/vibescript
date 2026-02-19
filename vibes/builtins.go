package vibes

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
)

func builtinAssert(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) == 0 {
		return NewNil(), fmt.Errorf("assert requires a condition argument")
	}
	cond := args[0]
	if cond.Truthy() {
		return NewNil(), nil
	}
	message := "assertion failed"
	if len(args) > 1 {
		message = args[1].String()
	} else if msg, ok := kwargs["message"]; ok {
		message = msg.String()
	}
	return NewNil(), newAssertionFailureError(message)
}

func builtinMoney(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("money expects a single string literal")
	}
	lit := args[0]
	if lit.Kind() != KindString {
		return NewNil(), fmt.Errorf("money expects a string literal")
	}
	parsed, err := parseMoneyLiteral(lit.String())
	if err != nil {
		return NewNil(), err
	}
	return NewMoney(parsed), nil
}

func builtinMoneyCents(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("money_cents expects cents and currency")
	}
	centsVal := args[0]
	currencyVal := args[1]

	cents, err := valueToInt64(centsVal)
	if err != nil {
		return NewNil(), fmt.Errorf("money_cents expects integer cents")
	}
	if currencyVal.Kind() != KindString {
		return NewNil(), fmt.Errorf("money_cents expects currency string")
	}

	money, err := newMoneyFromCents(cents, currencyVal.String())
	if err != nil {
		return NewNil(), err
	}
	return NewMoney(money), nil
}

func builtinNow(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("now does not take arguments")
	}
	return NewString(time.Now().UTC().Format(time.RFC3339)), nil
}

type jsonStringifyState struct {
	seenArrays map[uintptr]struct{}
	seenHashes map[uintptr]struct{}
}

func builtinJSONParse(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 || args[0].Kind() != KindString {
		return NewNil(), fmt.Errorf("JSON.parse expects a single JSON string argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("JSON.parse does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("JSON.parse does not accept blocks")
	}

	decoder := json.NewDecoder(strings.NewReader(args[0].String()))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return NewNil(), fmt.Errorf("JSON.parse invalid JSON: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return NewNil(), fmt.Errorf("JSON.parse invalid JSON: trailing data")
	}

	value, err := jsonValueToVibeValue(decoded)
	if err != nil {
		return NewNil(), err
	}
	return value, nil
}

func builtinJSONStringify(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("JSON.stringify expects a single value argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("JSON.stringify does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("JSON.stringify does not accept blocks")
	}

	state := &jsonStringifyState{
		seenArrays: map[uintptr]struct{}{},
		seenHashes: map[uintptr]struct{}{},
	}
	encoded, err := vibeValueToJSONValue(args[0], state)
	if err != nil {
		return NewNil(), err
	}

	payload, err := json.Marshal(encoded)
	if err != nil {
		return NewNil(), fmt.Errorf("JSON.stringify failed: %v", err)
	}
	return NewString(string(payload)), nil
}

func jsonValueToVibeValue(val any) (Value, error) {
	switch v := val.(type) {
	case nil:
		return NewNil(), nil
	case bool:
		return NewBool(v), nil
	case string:
		return NewString(v), nil
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return NewInt(i), nil
		}
		f, err := v.Float64()
		if err != nil {
			return NewNil(), fmt.Errorf("JSON.parse invalid number %q", v.String())
		}
		return NewFloat(f), nil
	case float64:
		return NewFloat(v), nil
	case []any:
		arr := make([]Value, len(v))
		for i, item := range v {
			converted, err := jsonValueToVibeValue(item)
			if err != nil {
				return NewNil(), err
			}
			arr[i] = converted
		}
		return NewArray(arr), nil
	case map[string]any:
		obj := make(map[string]Value, len(v))
		for key, item := range v {
			converted, err := jsonValueToVibeValue(item)
			if err != nil {
				return NewNil(), err
			}
			obj[key] = converted
		}
		return NewHash(obj), nil
	default:
		return NewNil(), fmt.Errorf("JSON.parse unsupported value type %T", val)
	}
}

func vibeValueToJSONValue(val Value, state *jsonStringifyState) (any, error) {
	switch val.Kind() {
	case KindNil:
		return nil, nil
	case KindBool:
		return val.Bool(), nil
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		return val.Float(), nil
	case KindString, KindSymbol:
		return val.String(), nil
	case KindArray:
		arr := val.Array()
		id := reflect.ValueOf(arr).Pointer()
		if id != 0 {
			if _, seen := state.seenArrays[id]; seen {
				return nil, fmt.Errorf("JSON.stringify does not support cyclic arrays")
			}
			state.seenArrays[id] = struct{}{}
			defer delete(state.seenArrays, id)
		}

		out := make([]any, len(arr))
		for i, item := range arr {
			converted, err := vibeValueToJSONValue(item, state)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	case KindHash, KindObject:
		hash := val.Hash()
		id := reflect.ValueOf(hash).Pointer()
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return nil, fmt.Errorf("JSON.stringify does not support cyclic objects")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}

		out := make(map[string]any, len(hash))
		for key, item := range hash {
			converted, err := vibeValueToJSONValue(item, state)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil
	default:
		return nil, fmt.Errorf("JSON.stringify unsupported value type %s", val.Kind())
	}
}
