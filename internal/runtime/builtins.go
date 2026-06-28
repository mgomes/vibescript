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
	low, high, ok := randomRangeInclusiveBounds(rng)
	if !ok {
		return NewNil(), fmt.Errorf("rand range is empty")
	}
	size := uint64(high) - uint64(low) + 1
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
	return NewInt(int64(uint64(low) + offset)), nil
}

func randomRangeInclusiveBounds(rng Range) (int64, int64, bool) {
	low, high := rng.Start, rng.End
	if low > high {
		low, high = high, low
		if rng.Exclusive {
			if low == math.MaxInt64 {
				return 0, 0, false
			}
			low++
		}
	} else if rng.Exclusive {
		if high == math.MinInt64 {
			return 0, 0, false
		}
		high--
	}
	if low > high {
		return 0, 0, false
	}
	return low, high, true
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
	prepared, err := prepareFormatString(exec, pattern, values)
	if err != nil {
		return NewNil(), err
	}
	if exec != nil {
		if err := exec.checkProjectedStringBytesAndScratchWithCallRoots(prepared.projectedBytes, prepared.scratchBytes, receiver, args, kwargs, block); err != nil {
			return NewNil(), err
		}
	}
	formatArgs, err := prepared.formatArgs()
	if err != nil {
		return NewNil(), err
	}
	return NewString(fmt.Sprintf(prepared.pattern, formatArgs...)), nil
}

type preparedFormatString struct {
	pattern        string
	args           []preparedFormatArgument
	projectedBytes int
	scratchBytes   int
}

func (p preparedFormatString) formatArgs() ([]any, error) {
	args := make([]any, 0, len(p.args))
	for _, arg := range p.args {
		formatted, err := arg.format()
		if err != nil {
			return nil, err
		}
		args = append(args, formatted)
	}
	return args, nil
}

func prepareFormatString(exec *Execution, pattern string, values []Value) (preparedFormatString, error) {
	prepared := preparedFormatString{
		args: make([]preparedFormatArgument, 0, len(values)),
	}
	projection := formatProjection{exec: exec}
	var normalized strings.Builder
	normalized.Grow(min(len(pattern), maxFormatOutputBytes))
	total := 0
	nextArg := 0
	usedCursor := 0
	for i := 0; i < len(pattern); {
		if pattern[i] != '%' {
			var err error
			total, err = addProjectedFormatBytes(total, 1)
			if err != nil {
				return preparedFormatString{}, err
			}
			normalized.WriteByte(pattern[i])
			i++
			continue
		}
		directiveStart := i
		i++
		if i >= len(pattern) {
			var err error
			total, err = addProjectedFormatBytes(total, 2)
			if err != nil {
				return preparedFormatString{}, err
			}
			normalized.WriteString(pattern[directiveStart:])
			break
		}
		if pattern[i] == '%' {
			var err error
			total, err = addProjectedFormatBytes(total, 1)
			if err != nil {
				return preparedFormatString{}, err
			}
			normalized.WriteString("%%")
			i++
			continue
		}

		var explicitArg int
		var hasExplicitArg bool
		bodyAfterLeadingIndex := i
		if idx, ok, next := parseFormatArgIndex(pattern, i); ok {
			explicitArg = idx
			hasExplicitArg = true
			bodyAfterLeadingIndex = next
			i = next
		}
		flags := formatFlags{}
		for i < len(pattern) && strings.ContainsRune("#+-0 ", rune(pattern[i])) {
			flags.record(pattern[i])
			i++
		}
		width, hasWidth, next, err := parseFormatCount(pattern, i, "width")
		if err != nil {
			return preparedFormatString{}, err
		}
		i = next

		precision := 0
		hasPrecision := false
		if i < len(pattern) && pattern[i] == '.' {
			i++
			precision, hasPrecision, next, err = parseFormatCount(pattern, i, "precision")
			if err != nil {
				return preparedFormatString{}, err
			}
			i = next
		}
		bodyBeforeTrailingIndex := i
		bodyAfterTrailingIndex := i
		if idx, ok, next := parseFormatArgIndex(pattern, i); ok {
			explicitArg = idx
			hasExplicitArg = true
			bodyAfterTrailingIndex = next
			i = next
		}
		if i >= len(pattern) {
			var err error
			total, err = addProjectedFormatBytes(total, len(pattern))
			if err != nil {
				return preparedFormatString{}, err
			}
			normalized.WriteString(pattern[directiveStart:])
			break
		}
		verb := pattern[i]
		verbIndex := i
		i++

		argIndex := nextArg
		if hasExplicitArg {
			argIndex = explicitArg
			nextArg = explicitArg + 1
		} else {
			nextArg++
		}
		if nextArg > usedCursor {
			usedCursor = nextArg
		}
		if argIndex < 0 || argIndex >= len(values) {
			return preparedFormatString{}, fmt.Errorf("format references missing operand %d", argIndex+1)
		}
		arg, err := prepareFormatArgument(projection, values[argIndex], verb, hasPrecision, precision)
		if err != nil {
			return preparedFormatString{}, err
		}
		field, err := projectedFormatFieldBytes(projection, values[argIndex], verb, hasPrecision, precision, flags)
		if err != nil {
			return preparedFormatString{}, err
		}
		if hasWidth && width > field {
			field = width
		}
		nextTotal, addErr := addProjectedFormatBytes(total, field)
		if addErr != nil {
			return preparedFormatString{}, addErr
		}
		total = nextTotal
		normalized.WriteByte('%')
		normalized.WriteString(pattern[bodyAfterLeadingIndex:bodyBeforeTrailingIndex])
		if bodyAfterTrailingIndex > bodyBeforeTrailingIndex {
			normalized.WriteString(pattern[bodyAfterTrailingIndex:verbIndex])
		}
		normalized.WriteByte(verb)
		prepared.args = append(prepared.args, arg)
		prepared.scratchBytes = saturatingAdd(prepared.scratchBytes, arg.scratchBytes())
	}
	if usedCursor < len(values) {
		return preparedFormatString{}, fmt.Errorf("format has %d unused operand(s)", len(values)-usedCursor)
	}
	prepared.pattern = normalized.String()
	prepared.projectedBytes = total
	return prepared, nil
}

func addProjectedFormatBytes(total, bytes int) (int, error) {
	total = saturatingAdd(total, bytes)
	if total > maxFormatOutputBytes {
		return 0, fmt.Errorf("format output exceeds limit %d bytes", maxFormatOutputBytes)
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

type formatFlags struct {
	alternate bool
	plus      bool
	space     bool
}

func (f *formatFlags) record(flag byte) {
	switch flag {
	case '#':
		f.alternate = true
	case '+':
		f.plus = true
	case ' ':
		f.space = true
	}
}

type formatProjection struct {
	exec *Execution
}

func (p formatProjection) stringBytes(val Value) (int, error) {
	switch val.Kind() {
	case KindString, KindSymbol:
		return len(val.String()), nil
	default:
		if p.exec != nil {
			return val.StringByteLenBounded(p.exec.step)
		}
		return val.StringByteLen(), nil
	}
}

func (p formatProjection) stringBytesUpTo(val Value, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	switch val.Kind() {
	case KindString, KindSymbol:
		return min(len(val.String()), limit), nil
	default:
		if p.exec != nil {
			n, truncated, err := val.StringByteLenBoundedUpTo(limit, p.exec.step)
			if err != nil {
				return 0, err
			}
			if truncated {
				return limit, nil
			}
			return n, nil
		}
		return min(val.StringByteLen(), limit), nil
	}
}

func (p formatProjection) stringPrecisionBytes(val Value, precision int) (int, error) {
	if precision <= 0 {
		return 0, nil
	}
	switch val.Kind() {
	case KindString, KindSymbol:
		return formatStringPrecisionBytes(val.String(), precision), nil
	default:
		return p.stringBytesUpTo(val, saturatingMul(utf8.UTFMax, precision))
	}
}

func formatStringPrecisionBytes(s string, precision int) int {
	if precision <= 0 {
		return 0
	}
	runes := 0
	for i := range s {
		if runes == precision {
			return i
		}
		runes++
	}
	return len(s)
}

func projectedFormatFieldBytes(projection formatProjection, val Value, verb byte, hasPrecision bool, precision int, flags formatFlags) (int, error) {
	if hasPrecision {
		switch verb {
		case 's':
			return projection.stringPrecisionBytes(val, precision)
		case 'q':
			selectedBytes, err := projection.stringPrecisionBytes(val, precision)
			if err != nil {
				return 0, err
			}
			return projectedQuotedStringBytes(selectedBytes), nil
		}
	}
	base, err := projectedFormatArgumentBytes(projection, val, verb, hasPrecision, precision, flags)
	if err != nil {
		return 0, err
	}
	if hasPrecision {
		switch verb {
		case 'f', 'F', 'e', 'E', 'g', 'G':
			base = max(base, saturatingAdd(precision, 16))
		}
	}
	return base, nil
}

func projectedFormatArgumentBytes(projection formatProjection, val Value, verb byte, hasPrecision bool, precision int, flags formatFlags) (int, error) {
	switch verb {
	case 's':
		return projection.stringBytes(val)
	case 'q':
		n, err := projection.stringBytes(val)
		if err != nil {
			return 0, err
		}
		return projectedQuotedStringBytes(n), nil
	case 'x', 'X':
		if val.Kind() == KindString || val.Kind() == KindSymbol {
			bytesPerInput := 2
			if flags.space {
				bytesPerInput++
			}
			field := saturatingMul(bytesPerInput, len(val.String()))
			if flags.alternate {
				field = saturatingAdd(field, 2)
				if flags.space {
					field = saturatingAdd(field, saturatingMul(2, len(val.String())))
				}
			}
			return field, nil
		}
		if val.Kind() == KindInt {
			return projectedIntegerFormatBytes(val, verb, hasPrecision, precision, flags)
		}
		if val.Kind() == KindFloat && hasPrecision {
			return saturatingAdd(precision, 32), nil
		}
		return 64, nil
	case 'd', 'b', 'o', 'O', 'U':
		return projectedIntegerFormatBytes(val, verb, hasPrecision, precision, flags)
	case 'c':
		return 64, nil
	case 'f', 'F', 'e', 'E', 'g', 'G':
		if verb == 'f' || verb == 'F' {
			return projectedFixedFloatFormatBytes(val, hasPrecision, precision, flags), nil
		}
		if hasPrecision {
			return saturatingAdd(precision, 16), nil
		}
		return 64, nil
	case 't':
		return 5, nil
	case 'v':
		if flags.alternate {
			n, err := projection.stringBytes(val)
			if err != nil {
				return 0, err
			}
			return projectedQuotedStringBytes(n), nil
		}
		n, err := projection.stringBytes(val)
		if err != nil {
			return 0, err
		}
		return saturatingAdd(n, 32), nil
	default:
		n, err := projection.stringBytes(val)
		if err != nil {
			return 0, err
		}
		return saturatingAdd(n, 32), nil
	}
}

func projectedIntegerFormatBytes(val Value, verb byte, hasPrecision bool, precision int, flags formatFlags) (int, error) {
	n, err := valueToInt64(val)
	if err != nil {
		return 0, err
	}
	if verb == 'U' {
		digits := max(4, unsignedIntegerDigitBytes(uint64(n), 16))
		if hasPrecision {
			digits = max(digits, precision)
		}
		field := saturatingAdd(2, digits)
		if flags.alternate {
			field = saturatingAdd(field, 16)
		}
		return field, nil
	}

	base := 10
	prefix := 0
	switch verb {
	case 'b':
		base = 2
		if flags.alternate {
			prefix = 2
		}
	case 'o':
		base = 8
		if flags.alternate {
			prefix = 1
		}
	case 'O':
		base = 8
		if flags.alternate {
			prefix = 2
		}
	case 'x', 'X':
		base = 16
		if flags.alternate {
			prefix = 2
		}
	}
	digits := signedIntegerDigitBytes(n, base)
	if hasPrecision {
		digits = max(digits, precision)
	}
	return saturatingAdd(saturatingAdd(projectedNumericSignBytes(n < 0, flags), prefix), digits), nil
}

func projectedFixedFloatFormatBytes(val Value, hasPrecision bool, precision int, flags formatFlags) int {
	sign := projectedNumericSignBytes(formatFloatIsNegative(val), flags)
	if val.Kind() == KindFloat && (math.IsInf(val.Float(), 0) || math.IsNaN(val.Float())) {
		return saturatingAdd(sign, 3)
	}
	integerDigits := projectedFixedFloatIntegerDigits(val)
	fractionDigits := 6
	if hasPrecision {
		fractionDigits = precision
	}
	decimal := 0
	if fractionDigits > 0 || flags.alternate {
		decimal = 1
	}
	return saturatingAdd(saturatingAdd(sign, integerDigits), saturatingAdd(decimal, fractionDigits))
}

func projectedFixedFloatIntegerDigits(val Value) int {
	switch val.Kind() {
	case KindInt:
		return signedIntegerDigitBytes(val.Int(), 10)
	case KindFloat:
		f := math.Abs(val.Float())
		if f < 1 {
			return 1
		}
		formatted := strconv.FormatFloat(f, 'e', -1, 64)
		exponentStart := strings.LastIndexByte(formatted, 'e')
		if exponentStart < 0 {
			return 309
		}
		exponent, err := strconv.Atoi(formatted[exponentStart+1:])
		if err != nil || exponent < 0 {
			return 309
		}
		return exponent + 1
	default:
		return 1
	}
}

func signedIntegerDigitBytes(n int64, base int) int {
	return unsignedIntegerDigitBytes(absInt64AsUint64(n), base)
}

func unsignedIntegerDigitBytes(n uint64, base int) int {
	if n == 0 {
		return 1
	}
	digits := 0
	for n > 0 {
		digits++
		n /= uint64(base)
	}
	return digits
}

func absInt64AsUint64(n int64) uint64 {
	if n >= 0 {
		return uint64(n)
	}
	return uint64(-(n + 1)) + 1
}

func projectedNumericSignBytes(negative bool, flags formatFlags) int {
	if negative || flags.plus || flags.space {
		return 1
	}
	return 0
}

func formatFloatIsNegative(val Value) bool {
	switch val.Kind() {
	case KindInt:
		return val.Int() < 0
	case KindFloat:
		return math.Signbit(val.Float())
	default:
		return false
	}
}

func projectedQuotedStringBytes(inputBytes int) int {
	return saturatingAdd(saturatingMul(4, inputBytes), 2)
}

type preparedFormatArgument struct {
	value        Value
	verb         byte
	stringRender formatArgumentStringRender
}

type formatArgumentStringRender struct {
	enabled        bool
	limit          int
	allowTruncated bool
}

func prepareFormatArgument(projection formatProjection, val Value, verb byte, hasPrecision bool, precision int) (preparedFormatArgument, error) {
	arg := preparedFormatArgument{value: val, verb: verb}
	switch verb {
	case 's', 'q':
		render, err := prepareFormatStringRender(projection, val, hasPrecision, precision)
		if err != nil {
			return preparedFormatArgument{}, err
		}
		arg.stringRender = render
	case 'x', 'X':
		switch val.Kind() {
		case KindString, KindSymbol, KindInt, KindFloat:
		default:
			return preparedFormatArgument{}, fmt.Errorf("format %%%c expects string or numeric operand", verb)
		}
	case 'd', 'b', 'o', 'O', 'U', 'c':
		if _, err := valueToInt64(val); err != nil {
			return preparedFormatArgument{}, fmt.Errorf("format %%%c expects integer operand", verb)
		}
	case 'f', 'F', 'e', 'E', 'g', 'G':
		switch val.Kind() {
		case KindInt, KindFloat:
		default:
			return preparedFormatArgument{}, fmt.Errorf("format %%%c expects numeric operand", verb)
		}
	case 't':
		if val.Kind() != KindBool {
			return preparedFormatArgument{}, fmt.Errorf("format %%t expects bool operand")
		}
	default:
		if formatArgumentNeedsRenderedString(val) {
			render, err := prepareFormatStringRender(projection, val, false, 0)
			if err != nil {
				return preparedFormatArgument{}, err
			}
			arg.stringRender = render
		}
	}
	return arg, nil
}

func prepareFormatStringRender(projection formatProjection, val Value, hasPrecision bool, precision int) (formatArgumentStringRender, error) {
	if formatArgumentHasDirectString(val) {
		return formatArgumentStringRender{}, nil
	}
	allowTruncated := false
	var limit int
	if hasPrecision {
		limit = saturatingMul(utf8.UTFMax, precision)
		allowTruncated = true
	} else {
		var err error
		limit, err = projection.stringBytes(val)
		if err != nil {
			return formatArgumentStringRender{}, err
		}
	}
	return formatArgumentStringRender{
		enabled:        true,
		limit:          limit,
		allowTruncated: allowTruncated,
	}, nil
}

func (a preparedFormatArgument) scratchBytes() int {
	if !a.stringRender.enabled {
		return 0
	}
	return a.stringRender.limit
}

func (a preparedFormatArgument) format() (any, error) {
	switch a.verb {
	case 's', 'q':
		if a.stringRender.enabled {
			return a.renderString()
		}
		return a.value.String(), nil
	case 'x', 'X':
		switch a.value.Kind() {
		case KindString, KindSymbol:
			return a.value.String(), nil
		case KindInt:
			return a.value.Int(), nil
		case KindFloat:
			return a.value.Float(), nil
		}
	case 'd', 'b', 'o', 'O', 'U', 'c':
		return valueToInt64(a.value)
	case 'f', 'F', 'e', 'E', 'g', 'G':
		switch a.value.Kind() {
		case KindInt:
			return float64(a.value.Int()), nil
		case KindFloat:
			return a.value.Float(), nil
		}
	case 't':
		return a.value.Bool(), nil
	default:
		if a.stringRender.enabled {
			return a.renderString()
		}
		return formatStringArgument(a.value), nil
	}
	return nil, fmt.Errorf("format %%%c received incompatible operand", a.verb)
}

func (a preparedFormatArgument) renderString() (string, error) {
	if a.stringRender.limit == 0 && a.stringRender.allowTruncated {
		return "", nil
	}
	if a.stringRender.limit <= 0 {
		return a.value.String(), nil
	}
	rendered, err := a.value.StringBounded(a.stringRender.limit)
	if err == nil {
		return rendered, nil
	}
	if a.stringRender.allowTruncated && errors.Is(err, errStringRenderTruncated) {
		return rendered, nil
	}
	return "", err
}

func formatArgumentHasDirectString(val Value) bool {
	switch val.Kind() {
	case KindString, KindSymbol:
		return true
	default:
		return false
	}
}

func formatArgumentNeedsRenderedString(val Value) bool {
	switch val.Kind() {
	case KindInt, KindFloat, KindString, KindSymbol, KindBool, KindNil:
		return false
	default:
		return true
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
