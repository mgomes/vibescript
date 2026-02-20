package vibes

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	randomIDAlphabet       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	randomIDUnbiasedCutoff = byte((256 / len(randomIDAlphabet)) * len(randomIDAlphabet))
	maxJSONPayloadBytes    = 1 << 20
	maxRegexInputBytes     = 1 << 20
	maxRegexPatternSize    = 16 << 10
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
		if args[0].Kind() != KindInt {
			return NewNil(), fmt.Errorf("random_id length must be integer")
		}
		length = args[0].Int()
	}
	if length <= 0 {
		return NewNil(), fmt.Errorf("random_id length must be positive")
	}
	if length > 1024 {
		return NewNil(), fmt.Errorf("random_id length exceeds maximum 1024")
	}

	chars := make([]byte, 0, length)
	for int64(len(chars)) < length {
		needed := int(length) - len(chars)
		raw, err := exec.engine.randomBytes(needed)
		if err != nil {
			return NewNil(), err
		}
		for _, b := range raw {
			if b >= randomIDUnbiasedCutoff {
				continue
			}
			chars = append(chars, randomIDAlphabet[int(b)%len(randomIDAlphabet)])
			if int64(len(chars)) == length {
				break
			}
		}
	}
	return NewString(string(chars)), nil
}

func builtinToInt(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("to_int expects a single value argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("to_int does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("to_int does not accept blocks")
	}

	switch args[0].Kind() {
	case KindInt:
		return args[0], nil
	case KindFloat:
		f := args[0].Float()
		if math.Trunc(f) != f {
			return NewNil(), fmt.Errorf("to_int cannot convert non-integer float")
		}
		n, err := floatToInt64Checked(f, "to_int")
		if err != nil {
			return NewNil(), err
		}
		return NewInt(n), nil
	case KindString:
		s := strings.TrimSpace(args[0].String())
		if s == "" {
			return NewNil(), fmt.Errorf("to_int expects a numeric string")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return NewNil(), fmt.Errorf("to_int expects a base-10 integer string")
		}
		return NewInt(n), nil
	default:
		return NewNil(), fmt.Errorf("to_int expects int, float, or string")
	}
}

func builtinToFloat(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("to_float expects a single value argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("to_float does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("to_float does not accept blocks")
	}

	switch args[0].Kind() {
	case KindInt:
		return NewFloat(float64(args[0].Int())), nil
	case KindFloat:
		return args[0], nil
	case KindString:
		s := strings.TrimSpace(args[0].String())
		if s == "" {
			return NewNil(), fmt.Errorf("to_float expects a numeric string")
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return NewNil(), fmt.Errorf("to_float expects a numeric string")
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return NewNil(), fmt.Errorf("to_float expects a finite numeric string")
		}
		return NewFloat(f), nil
	default:
		return NewNil(), fmt.Errorf("to_float expects int, float, or string")
	}
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

	raw := args[0].String()
	if len(raw) > maxJSONPayloadBytes {
		return NewNil(), fmt.Errorf("JSON.parse input exceeds limit %d bytes", maxJSONPayloadBytes)
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
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
	if len(payload) > maxJSONPayloadBytes {
		return NewNil(), fmt.Errorf("JSON.stringify output exceeds limit %d bytes", maxJSONPayloadBytes)
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
	pattern := args[0].String()
	text := args[1].String()
	if len(pattern) > maxRegexPatternSize {
		return NewNil(), fmt.Errorf("Regex.match pattern exceeds limit %d bytes", maxRegexPatternSize)
	}
	if len(text) > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("Regex.match text exceeds limit %d bytes", maxRegexInputBytes)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("Regex.match invalid regex: %v", err)
	}
	indices := re.FindStringIndex(text)
	if indices == nil {
		return NewNil(), nil
	}
	return NewString(text[indices[0]:indices[1]]), nil
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
	if len(pattern) > maxRegexPatternSize {
		return NewNil(), fmt.Errorf("%s pattern exceeds limit %d bytes", method, maxRegexPatternSize)
	}
	if len(text) > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("%s text exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	if len(replacement) > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("%s replacement exceeds limit %d bytes", method, maxRegexInputBytes)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("%s invalid regex: %v", method, err)
	}

	if replaceAll {
		replaced, err := regexReplaceAllWithLimit(re, text, replacement, method)
		if err != nil {
			return NewNil(), err
		}
		return NewString(replaced), nil
	}

	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return NewString(text), nil
	}
	replaced := string(re.ExpandString(nil, replacement, text, loc))
	outputLen := len(text) - (loc[1] - loc[0]) + len(replaced)
	if outputLen > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	return NewString(text[:loc[0]] + replaced + text[loc[1]:]), nil
}

func regexReplaceAllWithLimit(re *regexp.Regexp, text string, replacement string, method string) (string, error) {
	out := make([]byte, 0, len(text))
	lastAppended := 0
	searchStart := 0
	for searchStart <= len(text) {
		loc, found := nextRegexReplaceAllSubmatchIndex(re, text, searchStart)
		if !found {
			break
		}

		segmentLen := loc[0] - lastAppended
		if len(out) > maxRegexInputBytes-segmentLen {
			return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
		}
		out = append(out, text[lastAppended:loc[0]]...)
		out = re.ExpandString(out, replacement, text, loc)
		if len(out) > maxRegexInputBytes {
			return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
		}
		lastAppended = loc[1]

		if loc[1] > searchStart {
			searchStart = loc[1]
			continue
		}
		if searchStart >= len(text) {
			break
		}
		_, size := utf8.DecodeRuneInString(text[searchStart:])
		if size == 0 {
			size = 1
		}
		searchStart += size
	}

	tailLen := len(text) - lastAppended
	if len(out) > maxRegexInputBytes-tailLen {
		return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	out = append(out, text[lastAppended:]...)
	return string(out), nil
}

func nextRegexReplaceAllSubmatchIndex(re *regexp.Regexp, text string, start int) ([]int, bool) {
	windowStart := start
	if windowStart > 0 {
		windowStart--
	}
	locs := re.FindAllStringSubmatchIndex(text[windowStart:], 2)
	for _, loc := range locs {
		absStart := loc[0] + windowStart
		if absStart < start {
			continue
		}
		abs := make([]int, len(loc))
		for i, index := range loc {
			if index < 0 {
				abs[i] = -1
				continue
			}
			abs[i] = index + windowStart
		}
		return abs, true
	}
	return nil, false
}
