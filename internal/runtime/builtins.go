package runtime

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	randomIDAlphabet       = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	randomIDUnbiasedCutoff = byte((256 / len(randomIDAlphabet)) * len(randomIDAlphabet))
	maxRandomIDStallReads  = 8
	maxSleepDuration       = time.Duration(1<<63 - 1)
	maxSleepWholeSeconds   = int64(maxSleepDuration / time.Second)
	maxSleepRemainder      = maxSleepDuration % time.Second
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

	if !isNumericValue(centsVal) {
		return NewNil(), fmt.Errorf("money_cents expects integer cents")
	}
	cents, err := valueToInt64(centsVal)
	if err != nil {
		return NewNil(), fmt.Errorf("money_cents expects integer cents: %w", err)
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

func builtinRand(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("rand does not take keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("rand does not accept blocks")
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("rand expects at most one argument")
	}
	if len(args) == 0 {
		f, err := exec.randomFloat64()
		if err != nil {
			return NewNil(), err
		}
		return NewFloat(f), nil
	}
	switch arg := args[0]; arg.Kind() {
	case KindNil:
		f, err := exec.randomFloat64()
		if err != nil {
			return NewNil(), err
		}
		return NewFloat(f), nil
	case KindInt:
		limit := arg.Int()
		if limit <= 0 {
			return NewNil(), fmt.Errorf("rand integer bound must be positive")
		}
		n, err := exec.randomInt64n(uint64(limit))
		if err != nil {
			return NewNil(), err
		}
		return NewInt(int64(n)), nil
	case KindRange:
		return exec.randomRangeValue(arg.Range())
	default:
		return NewNil(), fmt.Errorf("rand expects an integer bound or integer range")
	}
}

func builtinSrand(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("srand does not take keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("srand does not accept blocks")
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("srand expects at most one seed")
	}

	previous := NewNil()
	if exec.randSeeded {
		previous = NewInt(exec.randSeed)
	}

	var seed int64
	if len(args) == 0 || args[0].Kind() == KindNil {
		raw, err := exec.randomUint64()
		if err != nil {
			return NewNil(), err
		}
		seed = int64(raw)
	} else if args[0].Kind() == KindInt {
		seed = args[0].Int()
	} else {
		return NewNil(), fmt.Errorf("srand seed must be integer or nil")
	}
	exec.randSource = rand.New(rand.NewSource(seed))
	exec.randSeed = seed
	exec.randSeeded = true
	return previous, nil
}

func (exec *Execution) randomFloat64() (float64, error) {
	if exec.randSource != nil {
		return exec.randSource.Float64(), nil
	}
	raw, err := exec.randomUint64()
	if err != nil {
		return 0, err
	}
	return float64(raw>>11) / (1 << 53), nil
}

func (exec *Execution) randomRangeValue(rng Range) (Value, error) {
	end := rng.End
	if rng.Exclusive {
		if end == math.MinInt64 {
			return NewNil(), fmt.Errorf("rand range is empty")
		}
		end--
	}
	if end < rng.Start {
		return NewNil(), fmt.Errorf("rand range is empty")
	}
	size := uint64(end) - uint64(rng.Start) + 1
	var offset uint64
	var err error
	if size == 0 {
		offset, err = exec.randomUint64ForRand()
	} else {
		offset, err = exec.randomInt64n(size)
	}
	if err != nil {
		return NewNil(), err
	}
	return NewInt(int64(uint64(rng.Start) + offset)), nil
}

func (exec *Execution) randomInt64n(limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, fmt.Errorf("random integer limit must be positive")
	}
	if exec.randSource != nil && limit <= uint64(math.MaxInt64) {
		return uint64(exec.randSource.Int63n(int64(limit))), nil
	}
	rejectAbove := ^uint64(0) - ((-limit) % limit)
	for {
		raw, err := exec.randomUint64ForRand()
		if err != nil {
			return 0, err
		}
		if raw <= rejectAbove {
			return raw % limit, nil
		}
	}
}

func (exec *Execution) randomUint64ForRand() (uint64, error) {
	if exec.randSource != nil {
		return exec.randSource.Uint64(), nil
	}
	return exec.randomUint64()
}

func (exec *Execution) randomUint64() (uint64, error) {
	raw, err := exec.engine.randomBytes(exec.Context(), 8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw), nil
}

func builtinFormat(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return formatStringBuiltin(exec, "format", receiver, args, kwargs, block)
}

func builtinLoop(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("loop does not take arguments")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("loop does not take keyword arguments")
	}
	runner, err := newBlockCallRunner(exec, block, "loop", NewNil(), nil, kwargs)
	if err != nil {
		return NewNil(), err
	}

	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

	for {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		val, err := runner.call(nil)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				if breakVal, ok := loopBreakValue(err); ok {
					return breakVal, nil
				}
				return NewNil(), nil
			}
			if errors.Is(err, errLoopNext) {
				continue
			}
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), err
		}
	}
}

func builtinSprintf(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return formatStringBuiltin(exec, "sprintf", receiver, args, kwargs, block)
}

func formatStringBuiltin(exec *Execution, name string, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s does not accept blocks", name)
	}
	if len(args) == 0 {
		return NewNil(), fmt.Errorf("%s expects a format string", name)
	}
	if args[0].Kind() != KindString {
		return NewNil(), fmt.Errorf("%s expects a string format", name)
	}
	return exec.formatStringValues(args[0].String(), args[1:], receiver, args, kwargs, block)
}

func formatStringValues(pattern string, values []Value) (Value, error) {
	return formatStringValuesChecked(nil, pattern, values, NewNil(), nil, nil, NewNil())
}

func (exec *Execution) formatStringValues(pattern string, values []Value, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return formatStringValuesChecked(exec, pattern, values, receiver, args, kwargs, block)
}

func formatStringValuesChecked(exec *Execution, pattern string, values []Value, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	projected, err := projectedFormatStringBytes(pattern, values)
	if err != nil {
		return NewNil(), err
	}
	if exec != nil {
		if err := exec.checkProjectedStringBytesWithCallRoots(projected, receiver, args, kwargs, block); err != nil {
			return NewNil(), err
		}
	}
	fmtArgs := make([]any, len(values))
	for i, val := range values {
		fmtArgs[i] = formatStringArgument(val)
	}
	return NewString(fmt.Sprintf(pattern, fmtArgs...)), nil
}

func projectedFormatStringBytes(pattern string, values []Value) (int, error) {
	total := 0
	nextArg := 0
	for i := 0; i < len(pattern); {
		if pattern[i] != '%' {
			total = saturatingAdd(total, 1)
			i++
			continue
		}
		i++
		if i >= len(pattern) {
			total = saturatingAdd(total, 2)
			break
		}
		if pattern[i] == '%' {
			total = saturatingAdd(total, 1)
			i++
			continue
		}

		var explicitArg int
		var hasExplicitArg bool
		if idx, ok, next := parseFormatArgIndex(pattern, i); ok {
			explicitArg = idx
			hasExplicitArg = true
			i = next
		}
		for i < len(pattern) && strings.ContainsRune("#+-0 ", rune(pattern[i])) {
			i++
		}
		width, hasWidth, next, err := parseFormatCount(pattern, i, "width")
		if err != nil {
			return 0, err
		}
		i = next

		precision := 0
		hasPrecision := false
		if i < len(pattern) && pattern[i] == '.' {
			i++
			precision, hasPrecision, next, err = parseFormatCount(pattern, i, "precision")
			if err != nil {
				return 0, err
			}
			i = next
		}
		if idx, ok, next := parseFormatArgIndex(pattern, i); ok {
			explicitArg = idx
			hasExplicitArg = true
			i = next
		}
		if i >= len(pattern) {
			total = saturatingAdd(total, len(pattern))
			break
		}
		verb := pattern[i]
		i++

		argIndex := nextArg
		if hasExplicitArg {
			argIndex = explicitArg
		} else {
			nextArg++
		}
		field := projectedFormatFieldBytes(formatValueAt(values, argIndex), verb, hasPrecision, precision)
		if hasWidth && width > field {
			field = width
		}
		total = saturatingAdd(total, field)
		if total > maxFormatOutputBytes {
			return 0, fmt.Errorf("format output exceeds limit %d bytes", maxFormatOutputBytes)
		}
	}
	return total, nil
}

func parseFormatArgIndex(pattern string, i int) (int, bool, int) {
	if i >= len(pattern) || pattern[i] != '[' {
		return 0, false, i
	}
	j := i + 1
	start := j
	for j < len(pattern) && pattern[j] >= '0' && pattern[j] <= '9' {
		j++
	}
	if start == j || j >= len(pattern) || pattern[j] != ']' {
		return 0, false, i
	}
	n, err := strconv.Atoi(pattern[start:j])
	if err != nil || n <= 0 {
		return 0, false, i
	}
	return n - 1, true, j + 1
}

func parseFormatCount(pattern string, i int, label string) (int, bool, int, error) {
	if idx, ok, next := parseFormatArgIndex(pattern, i); ok {
		i = next
		_ = idx
	}
	if i < len(pattern) && pattern[i] == '*' {
		return 0, false, i, fmt.Errorf("format dynamic %s is not supported", label)
	}
	start := i
	for i < len(pattern) && pattern[i] >= '0' && pattern[i] <= '9' {
		i++
	}
	if start == i {
		return 0, false, i, nil
	}
	n, err := strconv.Atoi(pattern[start:i])
	if err != nil || n > maxFormatOutputBytes {
		return 0, false, i, fmt.Errorf("format %s exceeds limit %d bytes", label, maxFormatOutputBytes)
	}
	return n, true, i, nil
}

func formatValueAt(values []Value, index int) Value {
	if index < 0 || index >= len(values) {
		return NewString("%!MISSING")
	}
	return values[index]
}

func projectedFormatFieldBytes(val Value, verb byte, hasPrecision bool, precision int) int {
	base := projectedFormatArgumentBytes(val, verb, hasPrecision, precision)
	if hasPrecision {
		switch verb {
		case 'f', 'F', 'e', 'E', 'g', 'G':
			base = max(base, saturatingAdd(precision, 16))
		case 's':
			base = min(base, precision)
		case 'q':
			base = min(base, saturatingAdd(saturatingMul(2, precision), 2))
		}
	}
	return base
}

func projectedFormatArgumentBytes(val Value, verb byte, hasPrecision bool, precision int) int {
	switch verb {
	case 's':
		return formatArgumentStringBytes(val)
	case 'q':
		return saturatingAdd(saturatingMul(2, formatArgumentStringBytes(val)), 2)
	case 'x', 'X':
		if val.Kind() == KindString || val.Kind() == KindSymbol {
			return saturatingMul(2, len(val.String()))
		}
		return 64
	case 'd', 'b', 'o', 'O', 'U', 'c':
		return 64
	case 'f', 'F', 'e', 'E', 'g', 'G':
		if hasPrecision {
			return saturatingAdd(precision, 16)
		}
		return 64
	case 't':
		return 5
	default:
		return saturatingAdd(val.StringByteLen(), 32)
	}
}

func formatArgumentStringBytes(val Value) int {
	switch val.Kind() {
	case KindString, KindSymbol:
		return len(val.String())
	default:
		return val.StringByteLen()
	}
}

func formatStringArgument(val Value) any {
	switch val.Kind() {
	case KindInt:
		return val.Int()
	case KindFloat:
		return val.Float()
	case KindString, KindSymbol:
		return val.String()
	case KindBool:
		return val.Bool()
	case KindNil:
		return nil
	default:
		return val.String()
	}
}

func builtinSleep(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("sleep expects one duration argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("sleep does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("sleep does not accept blocks")
	}

	duration, err := valueToSleepDuration(args[0])
	if err != nil {
		return NewNil(), err
	}
	if duration <= 0 {
		if err := exec.checkContext(); err != nil {
			return NewNil(), err
		}
		return NewInt(0), nil
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return NewInt(int64(duration / time.Second)), nil
	case <-exec.Context().Done():
		return NewNil(), exec.Context().Err()
	}
}

func valueToSleepDuration(val Value) (time.Duration, error) {
	switch val.Kind() {
	case KindInt:
		seconds := val.Int()
		if seconds < 0 {
			return 0, fmt.Errorf("sleep duration must be non-negative")
		}
		if seconds > int64(maxSleepDuration/time.Second) {
			return 0, fmt.Errorf("sleep duration exceeds maximum")
		}
		return time.Duration(seconds) * time.Second, nil
	case KindFloat:
		seconds := val.Float()
		if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return 0, fmt.Errorf("sleep duration must be finite and non-negative")
		}
		return sleepDurationFromFloat(seconds)
	default:
		return 0, fmt.Errorf("sleep duration must be numeric")
	}
}

func sleepDurationFromFloat(seconds float64) (time.Duration, error) {
	whole, fractional := math.Modf(seconds)
	if whole > float64(maxSleepWholeSeconds) {
		return 0, fmt.Errorf("sleep duration exceeds maximum")
	}
	fractionalNanos := fractional * float64(time.Second)
	if whole == float64(maxSleepWholeSeconds) && fractionalNanos > float64(maxSleepRemainder) {
		return 0, fmt.Errorf("sleep duration exceeds maximum")
	}
	return time.Duration(int64(whole))*time.Second + time.Duration(fractionalNanos), nil
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
	raw, err := exec.engine.randomBytes(exec.Context(), 16)
	if err != nil {
		return NewNil(), err
	}

	// RFC 9562 v7: unix timestamp milliseconds + random bits.
	nowMillis := uint64(time.Now().UTC().UnixMilli())
	raw[0] = byte(nowMillis >> 40)
	raw[1] = byte(nowMillis >> 32)
	raw[2] = byte(nowMillis >> 24)
	raw[3] = byte(nowMillis >> 16)
	raw[4] = byte(nowMillis >> 8)
	raw[5] = byte(nowMillis)
	raw[6] = (raw[6] & 0x0f) | 0x70
	raw[8] = (raw[8] & 0x3f) | 0x80
	return NewString(formatUUID(raw)), nil
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
	stalledReads := 0
	for int64(len(chars)) < length {
		needed := int(length) - len(chars)
		raw, err := exec.engine.randomBytes(exec.Context(), needed)
		if err != nil {
			return NewNil(), err
		}
		acceptedThisRead := 0
		for _, b := range raw {
			if b >= randomIDUnbiasedCutoff {
				continue
			}
			chars = append(chars, randomIDAlphabet[int(b)%len(randomIDAlphabet)])
			acceptedThisRead++
			if int64(len(chars)) == length {
				break
			}
		}
		if acceptedThisRead == 0 {
			stalledReads++
			if stalledReads > maxRandomIDStallReads {
				return NewNil(), fmt.Errorf("random_id entropy source rejected too many bytes")
			}
			continue
		}
		stalledReads = 0
	}
	return NewString(string(chars)), nil
}

func formatUUID(raw []byte) string {
	hexValue := hex.EncodeToString(raw)
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32]
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

	parser := jsonValueParser{raw: raw}
	value, err := parser.parse()
	if err != nil {
		var invalidNumber jsonInvalidNumberError
		if errors.As(err, &invalidNumber) {
			return NewNil(), err
		}
		return NewNil(), fmt.Errorf("JSON.parse invalid JSON: %w", err)
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
	payload, err := appendJSONValue(make([]byte, 0, 256), args[0], state)
	if err != nil {
		return NewNil(), err
	}
	if len(payload) > maxJSONPayloadBytes {
		return NewNil(), fmt.Errorf("JSON.stringify output exceeds limit %d bytes", maxJSONPayloadBytes)
	}
	return NewString(string(payload)), nil
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

	re, err := compileCachedRegex(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("Regex.match invalid regex: %w", err)
	}
	indices := re.FindStringIndex(text)
	if indices == nil {
		return NewNil(), nil
	}
	return NewString(text[indices[0]:indices[1]]), nil
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

	re, err := compileCachedRegex(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("%s invalid regex: %w", method, err)
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

func regexReplaceAllWithLimit(re *regexp.Regexp, text, replacement, method string) (string, error) {
	out := make([]byte, 0, len(text))
	lastAppended := 0
	searchStart := 0
	lastMatchEnd := -1
	for searchStart <= len(text) {
		loc, found := nextRegexReplaceAllSubmatchIndex(re, text, searchStart)
		if !found {
			break
		}
		if loc[0] == loc[1] && loc[0] == lastMatchEnd {
			if loc[0] >= len(text) {
				break
			}
			_, size := utf8.DecodeRuneInString(text[loc[0]:])
			if size == 0 {
				size = 1
			}
			searchStart = loc[0] + size
			continue
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
		lastMatchEnd = loc[1]

		if loc[1] > loc[0] {
			searchStart = loc[1]
			continue
		}
		if loc[1] >= len(text) {
			break
		}
		_, size := utf8.DecodeRuneInString(text[loc[1]:])
		if size == 0 {
			size = 1
		}
		searchStart = loc[1] + size
	}

	tailLen := len(text) - lastAppended
	if len(out) > maxRegexInputBytes-tailLen {
		return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	out = append(out, text[lastAppended:]...)
	return string(out), nil
}

func nextRegexReplaceAllSubmatchIndex(re *regexp.Regexp, text string, start int) ([]int, bool) {
	loc := re.FindStringSubmatchIndex(text[start:])
	if loc == nil {
		return nil, false
	}
	direct := offsetRegexSubmatchIndexInPlace(loc, start)
	if start == 0 || direct[0] > start {
		return direct, true
	}

	windowStart := start - 1
	locs := re.FindAllStringSubmatchIndex(text[windowStart:], 2)
	if len(locs) == 0 {
		return nil, false
	}

	first := offsetRegexSubmatchIndexInPlace(locs[0], windowStart)
	if first[0] >= start {
		return first, true
	}
	if first[1] > start {
		return direct, true
	}
	if len(locs) < 2 {
		return nil, false
	}
	second := offsetRegexSubmatchIndexInPlace(locs[1], windowStart)
	if second[0] >= start {
		return second, true
	}
	return nil, false
}

func offsetRegexSubmatchIndexInPlace(loc []int, offset int) []int {
	if offset == 0 {
		return loc
	}
	for i := range loc {
		if loc[i] < 0 {
			continue
		}
		loc[i] += offset
	}
	return loc
}
