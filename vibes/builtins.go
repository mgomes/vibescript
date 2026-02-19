package vibes

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"
)

const randomIDAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

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

func builtinUUID(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("uuid does not take arguments")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("uuid does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("uuid does not accept blocks")
	}
	raw, err := exec.engine.randomBytes(16)
	if err != nil {
		return NewNil(), err
	}

	// RFC 4122 v4: set version and variant bits.
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return NewString(formatUUIDv4(raw)), nil
}

func builtinRandomID(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("random_id does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("random_id does not accept blocks")
	}

	length := int64(16)
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("random_id expects at most one length argument")
	}
	if len(args) == 1 {
		n, err := valueToInt64(args[0])
		if err != nil {
			return NewNil(), fmt.Errorf("random_id length must be integer")
		}
		length = n
	}
	if length <= 0 {
		return NewNil(), fmt.Errorf("random_id length must be positive")
	}
	if length > 1024 {
		return NewNil(), fmt.Errorf("random_id length exceeds maximum 1024")
	}

	raw, err := exec.engine.randomBytes(int(length))
	if err != nil {
		return NewNil(), err
	}
	chars := make([]byte, len(raw))
	for i, b := range raw {
		chars[i] = randomIDAlphabet[int(b)%len(randomIDAlphabet)]
	}
	return NewString(string(chars)), nil
}

func formatUUIDv4(raw []byte) string {
	hexValue := hex.EncodeToString(raw)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32]
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

func builtinRegexMatch(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("Regex.match expects pattern and text")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("Regex.match does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("Regex.match does not accept blocks")
	}
	if args[0].Kind() != KindString || args[1].Kind() != KindString {
		return NewNil(), fmt.Errorf("Regex.match expects string pattern and text")
	}

	re, err := regexp.Compile(args[0].String())
	if err != nil {
		return NewNil(), fmt.Errorf("Regex.match invalid regex: %v", err)
	}
	match := re.FindString(args[1].String())
	if match == "" {
		return NewNil(), nil
	}
	return NewString(match), nil
}

func builtinRegexReplace(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return builtinRegexReplaceInternal(args, kwargs, block, false)
}

func builtinRegexReplaceAll(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return builtinRegexReplaceInternal(args, kwargs, block, true)
}

func builtinRegexReplaceInternal(args []Value, kwargs map[string]Value, block Value, replaceAll bool) (Value, error) {
	method := "Regex.replace"
	if replaceAll {
		method = "Regex.replace_all"
	}

	if len(args) != 3 {
		return NewNil(), fmt.Errorf("%s expects text, pattern, replacement", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not accept keyword arguments", method)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s does not accept blocks", method)
	}
	if args[0].Kind() != KindString || args[1].Kind() != KindString || args[2].Kind() != KindString {
		return NewNil(), fmt.Errorf("%s expects string text, pattern, replacement", method)
	}

	text := args[0].String()
	pattern := args[1].String()
	replacement := args[2].String()

	re, err := regexp.Compile(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("%s invalid regex: %v", method, err)
	}

	if replaceAll {
		return NewString(re.ReplaceAllString(text, replacement)), nil
	}

	loc := re.FindStringIndex(text)
	if loc == nil {
		return NewString(text), nil
	}
	segment := text[loc[0]:loc[1]]
	replaced := re.ReplaceAllString(segment, replacement)
	return NewString(text[:loc[0]] + replaced + text[loc[1]:]), nil
}
