package runtime

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// stringMemberNames mirrors the names dispatched by stringMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var stringMemberNames = []string{
	"size", "length", "bytesize", "ord", "chr", "empty?", "clear", "concat", "replace", "start_with?", "end_with?", "include?", "casecmp", "casecmp?", "match", "scan", "index", "rindex", "slice",
	"strip", "strip!", "squish", "squish!", "lstrip", "lstrip!", "rstrip", "rstrip!", "chomp", "chomp!", "chop", "chop!", "delete_prefix", "delete_prefix!", "delete_suffix", "delete_suffix!", "upcase", "upcase!", "downcase", "downcase!", "capitalize", "capitalize!", "swapcase", "swapcase!", "reverse", "reverse!",
	"sub", "sub!", "gsub", "gsub!", "split", "partition", "rpartition", "chars", "lines", "template",
	"center", "ljust", "rjust",
}

var stringBuiltinMembers = newMemberTable(stringMemberNames)

func stringMember(str Value, property string) (Value, error) {
	if member, ok := stringBuiltinMembers.lookup(property, stringMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), fmt.Errorf("unknown string method %s%s", property, didYouMean(property, stringMemberNames))
}

func stringMemberBuiltin(property string) (Value, error) {
	switch property {
	case "size", "length", "bytesize", "ord", "chr", "empty?", "clear", "concat", "replace", "start_with?", "end_with?", "include?", "casecmp", "casecmp?", "match", "scan", "index", "rindex", "slice":
		return stringMemberQuery(property)
	case "strip", "strip!", "squish", "squish!", "lstrip", "lstrip!", "rstrip", "rstrip!", "chomp", "chomp!", "chop", "chop!", "delete_prefix", "delete_prefix!", "delete_suffix", "delete_suffix!", "upcase", "upcase!", "downcase", "downcase!", "capitalize", "capitalize!", "swapcase", "swapcase!", "reverse", "reverse!":
		return stringMemberTransforms(property)
	case "sub", "sub!", "gsub", "gsub!", "split", "partition", "rpartition", "chars", "lines", "template":
		return stringMemberTextOps(property)
	case "center", "ljust", "rjust":
		return stringMemberPadding(property)
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

// stringLines splits text into lines using "\n" as the record separator,
// retaining the trailing "\n" on each line as Ruby's String#lines does. A
// trailing newline does not produce a final empty line, and an empty string
// yields no lines. Carriage returns are preserved verbatim, so "a\r\nb" splits
// into "a\r\n" and "b".
func stringLines(text string) []string {
	if text == "" {
		return nil
	}
	var lines []string
	for {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:index+1])
		text = text[index+1:]
		if text == "" {
			break
		}
	}
	return lines
}

// stringPartition splits text around the first occurrence of sep, mirroring
// Ruby's String#partition. It returns the segment before the separator, the
// separator itself, and the segment after it. When the separator is absent the
// whole string is returned as the head with two empty trailing segments. An
// empty separator matches at the very start, yielding ("", "", text).
func stringPartition(text, sep string) (head, separator, tail string) {
	index := strings.Index(text, sep)
	if index < 0 {
		return text, "", ""
	}
	return text[:index], sep, text[index+len(sep):]
}

// stringRPartition splits text around the last occurrence of sep, mirroring
// Ruby's String#rpartition. When the separator is absent the whole string is
// returned as the tail with two empty leading segments. An empty separator
// matches at the very end, yielding (text, "", "").
func stringRPartition(text, sep string) (head, separator, tail string) {
	index := strings.LastIndex(text, sep)
	if index < 0 {
		return "", "", text
	}
	return text[:index], sep, text[index+len(sep):]
}

func chompDefault(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return text[:len(text)-2]
	}
	if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r") {
		return text[:len(text)-1]
	}
	return text
}

// chopDefault removes the trailing character from text, mirroring Ruby's
// String#chop. A "\r\n" pair is treated as a single record separator and both
// bytes are removed together. Otherwise one logical character (a full UTF-8
// rune) is removed rather than a single byte, so trailing multibyte characters
// are handled correctly. An empty string is returned unchanged.
func chopDefault(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return text[:len(text)-2]
	}
	if text == "" {
		return text
	}
	_, size := utf8.DecodeLastRuneInString(text)
	return text[:len(text)-size]
}

func stringIsASCII(text string) bool {
	for i := range len(text) {
		if text[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// asciiCaseCompare compares a and b byte-by-byte, folding only the ASCII
// letters A-Z down to a-z before each byte comparison. This mirrors Ruby's
// String#casecmp, whose comparison path applies an ASCII-only TOLOWER to each
// side while every other byte (punctuation and multibyte UTF-8 sequences alike)
// is compared ordinally. Folding downward is what keeps the result consistent
// with Ruby for the punctuation bytes between 'Z' and 'a' (such as '[', '\\',
// ']', '^', '_', and '`'): because uppercase letters fold to the 'a'-'z' range,
// those punctuation bytes sort below the folded letters, so e.g. "[".casecmp("A")
// is -1. Folding upward would invert that ordering. The result is normalized to
// -1, 0, or 1.
func asciiCaseCompare(a, b string) int {
	limit := min(len(a), len(b))
	for i := range limit {
		ca, cb := asciiLower(a[i]), asciiLower(b[i])
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func asciiLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// caseInsensitiveEqual reports whether a and b are equal under case folding,
// backing Ruby's String#casecmp?. When both operands are valid UTF-8 it uses
// Unicode simple case folding (matching the upcase/downcase surface), so
// full-fold cases like "ß" vs "SS" stay unequal. When either operand contains
// invalid UTF-8 it folds byte-wise over the ASCII letters instead, mirroring
// Ruby's binary-string path. The byte-wise fallback preserves byte identity:
// distinct invalid sequences such as "\xff" and "\xfe" remain unequal, whereas
// strings.EqualFold would decode both as utf8.RuneError and report them equal.
func caseInsensitiveEqual(a, b string) bool {
	if utf8.ValidString(a) && utf8.ValidString(b) {
		return strings.EqualFold(a, b)
	}
	return asciiCaseEqual(a, b)
}

// asciiCaseEqual reports whether a and b are equal after folding only the ASCII
// letters A-Z down to a-z, comparing every other byte ordinally. It is the
// equality counterpart of asciiCaseCompare and is used for operands that are
// not valid UTF-8.
func asciiCaseEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if asciiLower(a[i]) != asciiLower(b[i]) {
			return false
		}
	}
	return true
}

func stringRuneLen(text string) int {
	if stringIsASCII(text) {
		return len(text)
	}
	return utf8.RuneCountInString(text)
}

func stringByteIndexForRuneOffset(text string, offset int) (int, bool) {
	if offset < 0 {
		return 0, false
	}
	if stringIsASCII(text) {
		if offset > len(text) {
			return 0, false
		}
		return offset, true
	}
	runeIndex := 0
	for byteIndex := range text {
		if runeIndex == offset {
			return byteIndex, true
		}
		runeIndex++
	}
	if runeIndex == offset {
		return len(text), true
	}
	return 0, false
}

func stringRuneIndex(text, needle string, offset int) int {
	if offset < 0 {
		return -1
	}
	if stringIsASCII(text) && stringIsASCII(needle) {
		if offset > len(text) {
			return -1
		}
		if needle == "" {
			return offset
		}
		index := strings.Index(text[offset:], needle)
		if index < 0 {
			return -1
		}
		return offset + index
	}
	if !utf8.ValidString(text) || !utf8.ValidString(needle) {
		return stringRuneIndexFallback(text, needle, offset)
	}
	startByte, ok := stringByteIndexForRuneOffset(text, offset)
	if !ok {
		return -1
	}
	if needle == "" {
		return offset
	}
	index := strings.Index(text[startByte:], needle)
	if index < 0 {
		return -1
	}
	return offset + utf8.RuneCountInString(text[startByte:startByte+index])
}

func stringRuneIndexFallback(text, needle string, offset int) int {
	hayRunes := []rune(text)
	needleRunes := []rune(needle)
	if offset > len(hayRunes) {
		return -1
	}
	if len(needleRunes) == 0 {
		return offset
	}
	limit := len(hayRunes) - len(needleRunes)
	if limit < offset {
		return -1
	}
	for i := offset; i <= limit; i++ {
		if runesHavePrefix(hayRunes[i:], needleRunes) {
			return i
		}
	}
	return -1
}

func stringRuneRIndex(text, needle string, offset int) int {
	if offset < 0 {
		return -1
	}
	if stringIsASCII(text) && stringIsASCII(needle) {
		if offset > len(text) {
			offset = len(text)
		}
		if needle == "" {
			return offset
		}
		maxStart := len(text) - len(needle)
		if maxStart < 0 {
			return -1
		}
		start := min(offset, maxStart)
		return strings.LastIndex(text[:start+len(needle)], needle)
	}
	if !utf8.ValidString(text) || !utf8.ValidString(needle) {
		return stringRuneRIndexFallback(text, needle, offset)
	}
	textLen := stringRuneLen(text)
	if offset > textLen {
		offset = textLen
	}
	if needle == "" {
		return offset
	}
	needleLen := stringRuneLen(needle)
	if needleLen > textLen {
		return -1
	}
	start := min(offset, textLen-needleLen)
	endByte, ok := stringByteIndexForRuneOffset(text, start+needleLen)
	if !ok {
		return -1
	}
	index := strings.LastIndex(text[:endByte], needle)
	if index < 0 {
		return -1
	}
	return utf8.RuneCountInString(text[:index])
}

func stringRuneRIndexFallback(text, needle string, offset int) int {
	hayRunes := []rune(text)
	needleRunes := []rune(needle)
	if offset > len(hayRunes) {
		offset = len(hayRunes)
	}
	if len(needleRunes) == 0 {
		return offset
	}
	if len(needleRunes) > len(hayRunes) {
		return -1
	}
	start := min(offset, len(hayRunes)-len(needleRunes))
	for i := start; i >= 0; i-- {
		if runesHavePrefix(hayRunes[i:], needleRunes) {
			return i
		}
	}
	return -1
}

func runesHavePrefix(text, prefix []rune) bool {
	if len(prefix) > len(text) {
		return false
	}
	for i, r := range prefix {
		if text[i] != r {
			return false
		}
	}
	return true
}

func stringRuneSlice(text string, start, length int) (string, bool) {
	if start < 0 || length < 0 {
		return "", false
	}
	startByte, ok := stringByteIndexForRuneOffset(text, start)
	if !ok || startByte == len(text) {
		return "", false
	}
	endByte := startByte
	for range length {
		if endByte == len(text) {
			return normalizeInvalidUTF8(text[startByte:]), true
		}
		_, size := utf8.DecodeRuneInString(text[endByte:])
		endByte += size
	}
	return normalizeInvalidUTF8(text[startByte:endByte]), true
}

func normalizeInvalidUTF8(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	return string([]rune(text))
}

func stringCapitalize(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	for i := range len(runes) - 1 {
		runes[i+1] = unicode.ToLower(runes[i+1])
	}
	return string(runes)
}

func stringSwapCase(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			runes[i] = unicode.ToLower(r)
			continue
		}
		if unicode.IsLower(r) {
			runes[i] = unicode.ToUpper(r)
		}
	}
	return string(runes)
}

func stringReverse(text string) string {
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func stringRegexOption(method string, kwargs map[string]Value) (bool, error) {
	if len(kwargs) == 0 {
		return false, nil
	}
	regexVal, ok := kwargs["regex"]
	if !ok || len(kwargs) > 1 {
		return false, fmt.Errorf("string.%s supports only regex keyword", method)
	}
	if regexVal.Kind() != KindBool {
		return false, fmt.Errorf("string.%s regex keyword must be bool", method)
	}
	return regexVal.Bool(), nil
}

func validateRegexTextPattern(method, text, pattern string) error {
	if len(pattern) > maxRegexPatternSize {
		return fmt.Errorf("%s pattern exceeds limit %d bytes", method, maxRegexPatternSize)
	}
	if len(text) > maxRegexInputBytes {
		return fmt.Errorf("%s text exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	return nil
}

func validateRegexReplacement(method, replacement string) error {
	if len(replacement) > maxRegexInputBytes {
		return fmt.Errorf("%s replacement exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	return nil
}

func stringSub(method, text, pattern, replacement string, regex bool) (string, error) {
	if !regex {
		return strings.Replace(text, pattern, replacement, 1), nil
	}
	if err := validateRegexTextPattern(method, text, pattern); err != nil {
		return "", err
	}
	if err := validateRegexReplacement(method, replacement); err != nil {
		return "", err
	}
	re, err := compileCachedRegex(pattern)
	if err != nil {
		return "", fmt.Errorf("%s invalid regex: %w", method, err)
	}
	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return text, nil
	}
	replaced := re.ExpandString(nil, replacement, text, loc)
	outputLen := len(text) - (loc[1] - loc[0]) + len(replaced)
	if outputLen > maxRegexInputBytes {
		return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	return text[:loc[0]] + string(replaced) + text[loc[1]:], nil
}

func stringGSub(method, text, pattern, replacement string, regex bool) (string, error) {
	if !regex {
		return strings.ReplaceAll(text, pattern, replacement), nil
	}
	if err := validateRegexTextPattern(method, text, pattern); err != nil {
		return "", err
	}
	if err := validateRegexReplacement(method, replacement); err != nil {
		return "", err
	}
	re, err := compileCachedRegex(pattern)
	if err != nil {
		return "", fmt.Errorf("%s invalid regex: %w", method, err)
	}
	return regexReplaceAllWithLimit(re, text, replacement, method)
}

func stringBangResult(original, updated string) Value {
	if updated == original {
		return NewNil()
	}
	return NewString(updated)
}

func stringSquish(text string) string {
	if stringIsSquished(text) {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	pendingSpace := false
	fieldStart := -1
	for i, r := range text {
		if unicode.IsSpace(r) {
			if fieldStart >= 0 {
				if pendingSpace {
					b.WriteByte(' ')
				}
				b.WriteString(text[fieldStart:i])
				pendingSpace = true
				fieldStart = -1
			}
			continue
		}
		if fieldStart < 0 {
			fieldStart = i
		}
	}
	if fieldStart >= 0 {
		if pendingSpace {
			b.WriteByte(' ')
		}
		b.WriteString(text[fieldStart:])
	}
	return b.String()
}

func stringIsSquished(text string) bool {
	if text == "" {
		return true
	}
	sawText := false
	previousSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !sawText || previousSpace || r != ' ' {
				return false
			}
			previousSpace = true
			continue
		}
		sawText = true
		previousSpace = false
	}
	return !previousSpace
}

func stringTemplateOption(kwargs map[string]Value) (bool, error) {
	if len(kwargs) == 0 {
		return false, nil
	}
	value, ok := kwargs["strict"]
	if !ok || len(kwargs) != 1 {
		return false, fmt.Errorf("string.template supports only strict keyword")
	}
	if value.Kind() != KindBool {
		return false, fmt.Errorf("string.template strict keyword must be bool")
	}
	return value.Bool(), nil
}

func stringTemplateLookup(context Value, keyPath string) (Value, bool) {
	current := context
	for segment := range strings.SplitSeq(keyPath, ".") {
		if segment == "" {
			return NewNil(), false
		}
		if current.Kind() != KindHash && current.Kind() != KindObject {
			return NewNil(), false
		}
		next, ok := current.Hash()[segment]
		if !ok {
			return NewNil(), false
		}
		current = next
	}
	return current, true
}

func stringTemplateScalarValue(value Value, keyPath string) (string, error) {
	switch value.Kind() {
	case KindNil, KindBool, KindInt, KindFloat, KindString, KindSymbol, KindMoney, KindDuration, KindTime:
		return value.String(), nil
	case KindEnumValue:
		member := valueEnumValue(value)
		if member == nil {
			return "", fmt.Errorf("string.template placeholder %s value must be scalar", keyPath)
		}
		return member.Symbol, nil
	default:
		return "", fmt.Errorf("string.template placeholder %s value must be scalar", keyPath)
	}
}

func stringTemplate(text string, context Value, strict bool) (string, error) {
	var b strings.Builder
	rendered := false
	last := 0
	search := 0
	for search < len(text) {
		openRel := strings.Index(text[search:], "{{")
		if openRel < 0 {
			break
		}
		open := search + openRel
		keyPath, end, ok := parseTemplateAt(text, open)
		if !ok {
			search = open + 1
			continue
		}
		if !rendered {
			b.Grow(len(text))
			rendered = true
		}
		b.WriteString(text[last:open])
		placeholder := text[open:end]
		value, ok := stringTemplateLookup(context, keyPath)
		if !ok {
			if strict {
				return "", fmt.Errorf("string.template missing placeholder %s", keyPath)
			}
			b.WriteString(placeholder)
			last = end
			search = end
			continue
		}
		segment, err := stringTemplateScalarValue(value, keyPath)
		if err != nil {
			return "", err
		}
		b.WriteString(segment)
		last = end
		search = end
	}
	if !rendered {
		return text, nil
	}
	b.WriteString(text[last:])
	return b.String(), nil
}

func parseTemplateAt(text string, open int) (string, int, bool) {
	i := open + 2
	for i < len(text) && isTemplateSpace(text[i]) {
		i++
	}
	if i >= len(text) || !isTemplateKeyStart(text[i]) {
		return "", 0, false
	}
	keyStart := i
	i++
	for i < len(text) && isTemplateKeyRune(text[i]) {
		i++
	}
	keyEnd := i
	for i < len(text) && isTemplateSpace(text[i]) {
		i++
	}
	if i+1 >= len(text) || text[i] != '}' || text[i+1] != '}' {
		return "", 0, false
	}
	return text[keyStart:keyEnd], i + 2, true
}

func isTemplateSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	default:
		return false
	}
}

func isTemplateKeyStart(b byte) bool {
	return b == '_' || ('A' <= b && b <= 'Z') || ('a' <= b && b <= 'z')
}

func isTemplateKeyRune(b byte) bool {
	return isTemplateKeyStart(b) || ('0' <= b && b <= '9') || b == '.' || b == '-'
}

func stringMemberQuery(property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("string.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.size does not take arguments")
			}
			return NewInt(int64(stringRuneLen(receiver.String()))), nil
		}), nil
	case "length":
		return NewAutoBuiltin("string.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.length does not take arguments")
			}
			return NewInt(int64(stringRuneLen(receiver.String()))), nil
		}), nil
	case "bytesize":
		return NewAutoBuiltin("string.bytesize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.bytesize does not take arguments")
			}
			return NewInt(int64(len(receiver.String()))), nil
		}), nil
	case "ord":
		return NewAutoBuiltin("string.ord", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.ord does not take arguments")
			}
			r, size := utf8.DecodeRuneInString(receiver.String())
			if size == 0 {
				return NewNil(), fmt.Errorf("string.ord requires non-empty string")
			}
			return NewInt(int64(r)), nil
		}), nil
	case "chr":
		return NewAutoBuiltin("string.chr", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.chr does not take arguments")
			}
			r, size := utf8.DecodeRuneInString(receiver.String())
			if size == 0 {
				return NewNil(), nil
			}
			return NewString(string(r)), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("string.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.empty? does not take arguments")
			}
			return NewBool(len(receiver.String()) == 0), nil
		}), nil
	case "clear":
		return NewAutoBuiltin("string.clear", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.clear does not take arguments")
			}
			return NewString(""), nil
		}), nil
	case "concat":
		return NewAutoBuiltin("string.concat", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			var b strings.Builder
			b.WriteString(receiver.String())
			for _, arg := range args {
				if arg.Kind() != KindString {
					return NewNil(), fmt.Errorf("string.concat expects string arguments")
				}
				b.WriteString(arg.String())
			}
			return NewString(b.String()), nil
		}), nil
	case "replace":
		return NewAutoBuiltin("string.replace", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.replace expects exactly one replacement")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.replace replacement must be string")
			}
			return NewString(args[0].String()), nil
		}), nil
	case "start_with?":
		return NewAutoBuiltin("string.start_with?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("string.start_with? expects at least one prefix")
			}
			value := receiver.String()
			// Check candidates left to right and short-circuit on the first
			// match, like Ruby: a non-string is only rejected if it is reached
			// before any match.
			for _, arg := range args {
				if arg.Kind() != KindString {
					return NewNil(), fmt.Errorf("string.start_with? prefix must be string")
				}
				if strings.HasPrefix(value, arg.String()) {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "end_with?":
		return NewAutoBuiltin("string.end_with?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("string.end_with? expects at least one suffix")
			}
			value := receiver.String()
			// Check candidates left to right and short-circuit on the first
			// match, like Ruby: a non-string is only rejected if it is reached
			// before any match.
			for _, arg := range args {
				if arg.Kind() != KindString {
					return NewNil(), fmt.Errorf("string.end_with? suffix must be string")
				}
				if strings.HasSuffix(value, arg.String()) {
					return NewBool(true), nil
				}
			}
			return NewBool(false), nil
		}), nil
	case "include?":
		return NewAutoBuiltin("string.include?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.include? expects exactly one substring")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.include? substring must be string")
			}
			return NewBool(strings.Contains(receiver.String(), args[0].String())), nil
		}), nil
	case "casecmp":
		return NewAutoBuiltin("string.casecmp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.casecmp expects exactly one string")
			}
			if args[0].Kind() != KindString {
				return NewNil(), nil
			}
			return NewInt(int64(asciiCaseCompare(receiver.String(), args[0].String()))), nil
		}), nil
	case "casecmp?":
		return NewAutoBuiltin("string.casecmp?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.casecmp? expects exactly one string")
			}
			if args[0].Kind() != KindString {
				return NewNil(), nil
			}
			return NewBool(caseInsensitiveEqual(receiver.String(), args[0].String())), nil
		}), nil
	case "match":
		return NewAutoBuiltin("string.match", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.match does not accept keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.match expects exactly one pattern")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.match pattern must be string")
			}
			pattern := args[0].String()
			text := receiver.String()
			if err := validateRegexTextPattern("string.match", text, pattern); err != nil {
				return NewNil(), err
			}
			re, err := compileCachedRegex(pattern)
			if err != nil {
				return NewNil(), fmt.Errorf("string.match invalid regex: %w", err)
			}
			indices := re.FindStringSubmatchIndex(text)
			if indices == nil {
				return NewNil(), nil
			}
			values := make([]Value, len(indices)/2)
			for i := range values {
				start := indices[i*2]
				end := indices[i*2+1]
				if start < 0 || end < 0 {
					values[i] = NewNil()
					continue
				}
				values[i] = NewString(text[start:end])
			}
			return NewArray(values), nil
		}), nil
	case "scan":
		return NewAutoBuiltin("string.scan", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.scan does not accept keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.scan expects exactly one pattern")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.scan pattern must be string")
			}
			pattern := args[0].String()
			text := receiver.String()
			if err := validateRegexTextPattern("string.scan", text, pattern); err != nil {
				return NewNil(), err
			}
			re, err := compileCachedRegex(pattern)
			if err != nil {
				return NewNil(), fmt.Errorf("string.scan invalid regex: %w", err)
			}
			matches := re.FindAllString(text, -1)
			values := make([]Value, len(matches))
			for i, m := range matches {
				values[i] = NewString(m)
			}
			return NewArray(values), nil
		}), nil
	case "index":
		return NewAutoBuiltin("string.index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.index expects substring and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.index substring must be string")
			}
			offset := 0
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.index offset must be non-negative integer")
				}
				offset = i
			}
			index := stringRuneIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "rindex":
		return NewAutoBuiltin("string.rindex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.rindex expects substring and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.rindex substring must be string")
			}
			offset := stringRuneLen(receiver.String())
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.rindex offset must be non-negative integer")
				}
				offset = i
			}
			index := stringRuneRIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "slice":
		return NewAutoBuiltin("string.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.slice expects index and optional length")
			}
			start, err := valueToInt(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("string.slice index must be integer")
			}
			if len(args) == 1 {
				substr, ok := stringRuneSlice(receiver.String(), start, 1)
				if !ok {
					return NewNil(), nil
				}
				return NewString(substr), nil
			}
			length, err := valueToInt(args[1])
			if err != nil {
				return NewNil(), fmt.Errorf("string.slice length must be integer")
			}
			substr, ok := stringRuneSlice(receiver.String(), start, length)
			if !ok {
				return NewNil(), nil
			}
			return NewString(substr), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

func stringMemberTextOps(property string) (Value, error) {
	switch property {
	case "sub":
		return NewAutoBuiltin("string.sub", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.sub expects pattern and replacement")
			}
			regex, err := stringRegexOption("sub", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub replacement must be string")
			}
			updated, err := stringSub("string.sub", receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), err
			}
			return NewString(updated), nil
		}), nil
	case "sub!":
		return NewAutoBuiltin("string.sub!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.sub! expects pattern and replacement")
			}
			regex, err := stringRegexOption("sub!", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub! pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub! replacement must be string")
			}
			updated, err := stringSub("string.sub!", receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), err
			}
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "gsub":
		return NewAutoBuiltin("string.gsub", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.gsub expects pattern and replacement")
			}
			regex, err := stringRegexOption("gsub", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub replacement must be string")
			}
			updated, err := stringGSub("string.gsub", receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), err
			}
			return NewString(updated), nil
		}), nil
	case "gsub!":
		return NewAutoBuiltin("string.gsub!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.gsub! expects pattern and replacement")
			}
			regex, err := stringRegexOption("gsub!", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub! pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub! replacement must be string")
			}
			updated, err := stringGSub("string.gsub!", receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), err
			}
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "split":
		return NewAutoBuiltin("string.split", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.split accepts at most one separator")
			}
			text := receiver.String()
			var parts []string
			if len(args) == 0 {
				parts = strings.Fields(text)
			} else {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("string.split separator must be string")
				}
				parts = strings.Split(text, args[0].String())
			}
			values := make([]Value, len(parts))
			for i, part := range parts {
				values[i] = NewString(part)
			}
			return NewArray(values), nil
		}), nil
	case "partition":
		return NewAutoBuiltin("string.partition", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.partition expects exactly one separator")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.partition separator must be string")
			}
			head, sep, tail := stringPartition(receiver.String(), args[0].String())
			return NewArray([]Value{NewString(head), NewString(sep), NewString(tail)}), nil
		}), nil
	case "rpartition":
		return NewAutoBuiltin("string.rpartition", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.rpartition expects exactly one separator")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.rpartition separator must be string")
			}
			head, sep, tail := stringRPartition(receiver.String(), args[0].String())
			return NewArray([]Value{NewString(head), NewString(sep), NewString(tail)}), nil
		}), nil
	case "chars":
		return NewAutoBuiltin("string.chars", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.chars does not take arguments")
			}
			text := receiver.String()
			values := make([]Value, 0, stringRuneLen(text))
			for _, r := range text {
				values = append(values, NewString(string(r)))
			}
			return NewArray(values), nil
		}), nil
	case "lines":
		return NewAutoBuiltin("string.lines", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.lines does not take arguments")
			}
			lines := stringLines(receiver.String())
			values := make([]Value, len(lines))
			for i, line := range lines {
				values[i] = NewString(line)
			}
			return NewArray(values), nil
		}), nil
	case "template":
		return NewAutoBuiltin("string.template", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.template expects exactly one context hash")
			}
			if args[0].Kind() != KindHash && args[0].Kind() != KindObject {
				return NewNil(), fmt.Errorf("string.template context must be hash")
			}
			strict, err := stringTemplateOption(kwargs)
			if err != nil {
				return NewNil(), err
			}
			rendered, err := stringTemplate(receiver.String(), args[0], strict)
			if err != nil {
				return NewNil(), err
			}
			return NewString(rendered), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

func stringMemberPadding(property string) (Value, error) {
	switch property {
	case "center":
		return NewAutoBuiltin("string.center", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return stringPad(exec, "string.center", padCenter, receiver, args, kwargs)
		}), nil
	case "ljust":
		return NewAutoBuiltin("string.ljust", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return stringPad(exec, "string.ljust", padRight, receiver, args, kwargs)
		}), nil
	case "rjust":
		return NewAutoBuiltin("string.rjust", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return stringPad(exec, "string.rjust", padLeft, receiver, args, kwargs)
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

// padSide selects how padding runes are distributed around the receiver.
type padSide int

const (
	padRight padSide = iota
	padLeft
	padCenter
)

// stringPad implements the shared logic for center, ljust, and rjust. Width is
// measured in runes to mirror Ruby's character-oriented padding, and a width at
// or below the receiver length returns the receiver unchanged. Float widths are
// truncated toward zero like Ruby's to_int; a non-finite or out-of-range Float
// width is rejected outright rather than wrapping into an in-range int that
// would slip past the projected-size guard. The padding string defaults to a
// single space, must be non-empty, and is repeated then truncated by runes to
// fill the requested span. The projected byte length is checked against the
// memory quota before any buffer is allocated so an oversized width fails fast
// instead of materializing a huge string.
func stringPad(exec *Execution, method string, side padSide, receiver Value, args []Value, kwargs map[string]Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not accept keyword arguments", method)
	}
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s expects width and optional pad string", method)
	}
	width, err := valueToPadWidth(args[0])
	if err != nil {
		if errors.Is(err, errWidthOutOfRange) {
			return NewNil(), fmt.Errorf("%s width is out of range", method)
		}
		return NewNil(), fmt.Errorf("%s width must be integer", method)
	}
	pad := " "
	if len(args) == 2 {
		if args[1].Kind() != KindString {
			return NewNil(), fmt.Errorf("%s pad must be string", method)
		}
		pad = args[1].String()
	}
	if pad == "" {
		return NewNil(), fmt.Errorf("%s pad must not be empty", method)
	}

	text := receiver.String()
	srcRunes := stringRuneLen(text)
	if width <= srcRunes {
		return receiver, nil
	}

	totalPad := width - srcRunes
	leftPad, rightPad := 0, 0
	switch side {
	case padLeft:
		leftPad = totalPad
	case padRight:
		rightPad = totalPad
	case padCenter:
		leftPad = totalPad / 2
		rightPad = totalPad - leftPad
	}

	// Saturating arithmetic keeps the projected size from overflowing on a huge
	// width; the quota check below rejects anything that large regardless.
	projected := saturatingAdd(len(text), saturatingAdd(padRuneBytes(pad, leftPad), padRuneBytes(pad, rightPad)))
	if err := exec.checkProjectedStringBytes(projected); err != nil {
		return NewNil(), err
	}

	var b strings.Builder
	// Only preallocate when the projected size is exact; a saturated value means
	// the request overflowed int and would never fit in memory anyway.
	if projected < math.MaxInt {
		b.Grow(projected)
	}
	writePadRunes(&b, pad, leftPad)
	b.WriteString(text)
	writePadRunes(&b, pad, rightPad)
	return NewString(b.String()), nil
}

// padRuneBytes reports how many bytes count runes drawn from pad occupy. The
// pad string is conceptually repeated and then truncated to count runes, so
// full repeats contribute their whole byte length and the remainder contributes
// a rune-aligned prefix. The byte total saturates at math.MaxInt so an
// oversized count cannot overflow the projected-size check.
func padRuneBytes(pad string, count int) int {
	if count <= 0 {
		return 0
	}
	padRunes := stringRuneLen(pad)
	full := count / padRunes
	remainder := count % padRunes
	return saturatingAdd(saturatingMul(full, len(pad)), padPrefixBytes(pad, remainder))
}

// saturatingAdd returns a+b clamped to math.MaxInt instead of overflowing. Both
// operands are non-negative byte counts.
func saturatingAdd(a, b int) int {
	if a > math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

// saturatingMul returns a*b clamped to math.MaxInt instead of overflowing. Both
// operands are non-negative byte counts.
func saturatingMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt/b {
		return math.MaxInt
	}
	return a * b
}

// padPrefixBytes returns the byte length of the first runes of pad.
func padPrefixBytes(pad string, runes int) int {
	if runes <= 0 {
		return 0
	}
	seen := 0
	for i := range pad {
		if seen == runes {
			return i
		}
		seen++
	}
	return len(pad)
}

// writePadRunes appends count runes drawn from pad to b, repeating pad and
// truncating the final repeat to a rune boundary.
func writePadRunes(b *strings.Builder, pad string, count int) {
	if count <= 0 {
		return
	}
	padRunes := stringRuneLen(pad)
	full := count / padRunes
	for range full {
		b.WriteString(pad)
	}
	remainder := count % padRunes
	if remainder == 0 {
		return
	}
	b.WriteString(pad[:padPrefixBytes(pad, remainder)])
}

func stringMemberTransforms(property string) (Value, error) {
	switch property {
	case "strip":
		return NewAutoBuiltin("string.strip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip does not take arguments")
			}
			return NewString(strings.TrimSpace(receiver.String())), nil
		}), nil
	case "strip!":
		return NewAutoBuiltin("string.strip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip! does not take arguments")
			}
			updated := strings.TrimSpace(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "squish":
		return NewAutoBuiltin("string.squish", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.squish does not take arguments")
			}
			return NewString(stringSquish(receiver.String())), nil
		}), nil
	case "squish!":
		return NewAutoBuiltin("string.squish!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.squish! does not take arguments")
			}
			updated := stringSquish(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "lstrip":
		return NewAutoBuiltin("string.lstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip does not take arguments")
			}
			return NewString(strings.TrimLeftFunc(receiver.String(), unicode.IsSpace)), nil
		}), nil
	case "lstrip!":
		return NewAutoBuiltin("string.lstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip! does not take arguments")
			}
			updated := strings.TrimLeftFunc(receiver.String(), unicode.IsSpace)
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "rstrip":
		return NewAutoBuiltin("string.rstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip does not take arguments")
			}
			return NewString(strings.TrimRightFunc(receiver.String(), unicode.IsSpace)), nil
		}), nil
	case "rstrip!":
		return NewAutoBuiltin("string.rstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip! does not take arguments")
			}
			updated := strings.TrimRightFunc(receiver.String(), unicode.IsSpace)
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "chomp":
		return NewAutoBuiltin("string.chomp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.chomp accepts at most one separator")
			}
			text := receiver.String()
			if len(args) == 0 {
				return NewString(chompDefault(text)), nil
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.chomp separator must be string")
			}
			sep := args[0].String()
			if sep == "" {
				return NewString(strings.TrimRight(text, "\r\n")), nil
			}
			if strings.HasSuffix(text, sep) {
				return NewString(text[:len(text)-len(sep)]), nil
			}
			return NewString(text), nil
		}), nil
	case "chomp!":
		return NewAutoBuiltin("string.chomp!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.chomp! accepts at most one separator")
			}
			original := receiver.String()
			if len(args) == 0 {
				return stringBangResult(original, chompDefault(original)), nil
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.chomp! separator must be string")
			}
			sep := args[0].String()
			if sep == "" {
				return stringBangResult(original, strings.TrimRight(original, "\r\n")), nil
			}
			if strings.HasSuffix(original, sep) {
				return stringBangResult(original, original[:len(original)-len(sep)]), nil
			}
			return NewNil(), nil
		}), nil
	case "chop":
		return NewAutoBuiltin("string.chop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.chop does not take arguments")
			}
			return NewString(chopDefault(receiver.String())), nil
		}), nil
	case "chop!":
		return NewAutoBuiltin("string.chop!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.chop! does not take arguments")
			}
			original := receiver.String()
			return stringBangResult(original, chopDefault(original)), nil
		}), nil
	case "delete_prefix":
		return NewAutoBuiltin("string.delete_prefix", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_prefix expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_prefix prefix must be string")
			}
			return NewString(strings.TrimPrefix(receiver.String(), args[0].String())), nil
		}), nil
	case "delete_prefix!":
		return NewAutoBuiltin("string.delete_prefix!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_prefix! expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_prefix! prefix must be string")
			}
			updated := strings.TrimPrefix(receiver.String(), args[0].String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "delete_suffix":
		return NewAutoBuiltin("string.delete_suffix", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_suffix expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_suffix suffix must be string")
			}
			return NewString(strings.TrimSuffix(receiver.String(), args[0].String())), nil
		}), nil
	case "delete_suffix!":
		return NewAutoBuiltin("string.delete_suffix!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_suffix! expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_suffix! suffix must be string")
			}
			updated := strings.TrimSuffix(receiver.String(), args[0].String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "upcase":
		return NewAutoBuiltin("string.upcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase does not take arguments")
			}
			return NewString(strings.ToUpper(receiver.String())), nil
		}), nil
	case "upcase!":
		return NewAutoBuiltin("string.upcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase! does not take arguments")
			}
			updated := strings.ToUpper(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "downcase":
		return NewAutoBuiltin("string.downcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase does not take arguments")
			}
			return NewString(strings.ToLower(receiver.String())), nil
		}), nil
	case "downcase!":
		return NewAutoBuiltin("string.downcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase! does not take arguments")
			}
			updated := strings.ToLower(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "capitalize":
		return NewAutoBuiltin("string.capitalize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.capitalize does not take arguments")
			}
			return NewString(stringCapitalize(receiver.String())), nil
		}), nil
	case "capitalize!":
		return NewAutoBuiltin("string.capitalize!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.capitalize! does not take arguments")
			}
			updated := stringCapitalize(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "swapcase":
		return NewAutoBuiltin("string.swapcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.swapcase does not take arguments")
			}
			return NewString(stringSwapCase(receiver.String())), nil
		}), nil
	case "swapcase!":
		return NewAutoBuiltin("string.swapcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.swapcase! does not take arguments")
			}
			updated := stringSwapCase(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "reverse":
		return NewAutoBuiltin("string.reverse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.reverse does not take arguments")
			}
			return NewString(stringReverse(receiver.String())), nil
		}), nil
	case "reverse!":
		return NewAutoBuiltin("string.reverse!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.reverse! does not take arguments")
			}
			updated := stringReverse(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}
