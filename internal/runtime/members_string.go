package runtime

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// stringMemberNames mirrors the names dispatched by stringMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var stringMemberNames = []string{
	"size", "length", "bytesize", "ord", "chr", "hex", "oct", "empty?", "clear", "concat", "prepend", "insert", "replace", "start_with?", "end_with?", "include?", "casecmp", "casecmp?", "match", "match?", "scan", "index", "rindex", "slice",
	"strip", "strip!", "squish", "squish!", "lstrip", "lstrip!", "rstrip", "rstrip!", "chomp", "chomp!", "chop", "chop!", "delete_prefix", "delete_prefix!", "delete_suffix", "delete_suffix!", "upcase", "upcase!", "downcase", "downcase!", "capitalize", "capitalize!", "swapcase", "swapcase!", "reverse", "reverse!",
	"sub", "sub!", "gsub", "gsub!", "split", "partition", "rpartition", "chars", "lines", "bytes", "each_char", "each_line", "each_byte", "template",
	"center", "ljust", "rjust",
	"to_sym", "intern",
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
	case "size", "length", "bytesize", "ord", "chr", "hex", "oct", "empty?", "clear", "concat", "prepend", "insert", "replace", "start_with?", "end_with?", "include?", "casecmp", "casecmp?", "match", "match?", "scan", "index", "rindex", "slice":
		return stringMemberQuery(property)
	case "strip", "strip!", "squish", "squish!", "lstrip", "lstrip!", "rstrip", "rstrip!", "chomp", "chomp!", "chop", "chop!", "delete_prefix", "delete_prefix!", "delete_suffix", "delete_suffix!", "upcase", "upcase!", "downcase", "downcase!", "capitalize", "capitalize!", "swapcase", "swapcase!", "reverse", "reverse!":
		return stringMemberTransforms(property)
	case "sub", "sub!", "gsub", "gsub!", "split", "partition", "rpartition", "chars", "lines", "bytes", "each_char", "each_line", "each_byte", "template":
		return stringMemberTextOps(property)
	case "center", "ljust", "rjust":
		return stringMemberPadding(property)
	case "to_sym", "intern":
		return stringMemberConversions(property)
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

// stringMemberConversions builds the string-to-symbol conversion members.
// Ruby's String#to_sym and its alias String#intern both return the symbol
// whose name is the receiver, so any string contents (including empty) yield a
// symbol verbatim without further validation.
func stringMemberConversions(property string) (Value, error) {
	switch property {
	case "to_sym", "intern":
		name := "string." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("%s does not take arguments", name)
			}
			return NewSymbol(receiver.String()), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

// forEachLine invokes yield for each line in text using "\n" as the record
// separator, retaining the trailing "\n" on each line as Ruby's String#lines
// does. A trailing newline does not produce a final empty line, and an empty
// string yields nothing. Carriage returns are preserved verbatim, so "a\r\nb"
// yields "a\r\n" then "b". Lines are located one at a time via IndexByte so
// callers can stream without materializing every line, and an error returned by
// yield stops the scan immediately.
func forEachLine(text string, yield func(line string) error) error {
	for text != "" {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			return yield(text)
		}
		if err := yield(text[:index+1]); err != nil {
			return err
		}
		text = text[index+1:]
	}
	return nil
}

// stringLines splits text into lines following the same rules as forEachLine,
// matching Ruby's String#lines.
func stringLines(text string) []string {
	var lines []string
	_ = forEachLine(text, func(line string) error {
		lines = append(lines, line)
		return nil
	})
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

// isRubyASCIISpace reports whether b is one of the six ASCII whitespace bytes
// Ruby's ISSPACE macro recognizes: space, horizontal tab, newline, vertical
// tab, form feed, and carriage return. Ruby uses this classification for the
// default no-separator String#split, so wider Unicode whitespace such as NBSP
// (U+00A0) or the em space (U+2003) is intentionally excluded.
func isRubyASCIISpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	default:
		return false
	}
}

// splitOnASCIIWhitespace splits text on runs of ASCII whitespace, mirroring
// Ruby's default (AWK-style) String#split. Leading and trailing whitespace is
// discarded and consecutive whitespace collapses, so " a  b " yields ["a",
// "b"] and a blank string yields no fields. Only the bytes recognized by
// isRubyASCIISpace separate fields; wider Unicode whitespace is preserved
// inside the surrounding field rather than acting as a delimiter, matching Ruby
// instead of Go's strings.Fields Unicode whitespace table.
func splitOnASCIIWhitespace(text string) []string {
	var fields []string
	start := -1
	for i := range len(text) {
		if isRubyASCIISpace(text[i]) {
			if start >= 0 {
				fields = append(fields, text[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		fields = append(fields, text[start:])
	}
	return fields
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

// stringEffectiveOffset normalizes a rune offset that may be negative the way
// Ruby's String#index and String#rindex do: a negative offset counts back from
// the end of the string, so -1 refers to the last rune. The second return value
// is false when the resulting offset falls before the start of the string, which
// callers translate into a nil result.
func stringEffectiveOffset(text string, offset int) (int, bool) {
	if offset >= 0 {
		return offset, true
	}
	effective := stringRuneLen(text) + offset
	if effective < 0 {
		return 0, false
	}
	return effective, true
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

// stringRuneSlice extracts at most length runes starting at the rune offset
// start, matching Ruby's String#slice(start, length). A negative start counts
// back from the end of the string. It returns ok=false when length is negative
// or when start lands outside the string; a start exactly equal to the rune
// length is in range and yields an empty string (Ruby's "abc".slice(3, n) =>
// ""). The length is clamped to the remaining runes, so an oversized length
// returns the suffix from start rather than overrunning.
func stringRuneSlice(text string, start, length int) (string, bool) {
	if length < 0 {
		return "", false
	}
	if start < 0 {
		start += stringRuneLen(text)
		if start < 0 {
			return "", false
		}
	}
	startByte, ok := stringByteIndexForRuneOffset(text, start)
	if !ok {
		return "", false
	}
	endByte := startByte
	for range length {
		if endByte == len(text) {
			break
		}
		_, size := utf8.DecodeRuneInString(text[endByte:])
		endByte += size
	}
	return normalizeInvalidUTF8(text[startByte:endByte]), true
}

// stringSlice implements String#slice. It mirrors Ruby's extraction semantics
// across the four argument shapes Vibescript can represent: a single integer
// index (single character, negative counts from the end), an integer start with
// a length, an integer range, and a substring. Out-of-range selectors yield nil
// rather than raising, matching Ruby. Regexp selectors are intentionally not
// handled because Vibescript has no regexp value type yet (tracked separately).
func stringSlice(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("string.slice expects an index, range, or substring with optional length")
	}
	text := receiver.String()
	if len(args) == 2 {
		start, err := valueToInt(args[0])
		if err != nil {
			return NewNil(), fmt.Errorf("string.slice index must be integer")
		}
		length, err := valueToInt(args[1])
		if err != nil {
			return NewNil(), fmt.Errorf("string.slice length must be integer")
		}
		substr, ok := stringRuneSlice(text, start, length)
		if !ok {
			return NewNil(), nil
		}
		return NewString(substr), nil
	}
	switch arg := args[0]; arg.Kind() {
	case KindRange:
		substr, ok := stringRuneRangeSlice(text, arg.Range())
		if !ok {
			return NewNil(), nil
		}
		return NewString(substr), nil
	case KindString:
		if strings.Contains(text, arg.String()) {
			return NewString(arg.String()), nil
		}
		return NewNil(), nil
	default:
		index, err := valueToInt(arg)
		if err != nil {
			return NewNil(), fmt.Errorf("string.slice index must be an integer, range, or substring")
		}
		return stringSliceCharAt(text, index), nil
	}
}

// stringSliceCharAt returns the single-character slice for String#slice(index).
// Unlike the (start, length) form, an index equal to the rune length is out of
// range and yields nil (Ruby's "abc".slice(3) => nil while "abc".slice(3, 1) =>
// ""). A negative index counts back from the end.
func stringSliceCharAt(text string, index int) Value {
	if index < 0 {
		index += stringRuneLen(text)
		if index < 0 {
			return NewNil()
		}
	}
	if index >= stringRuneLen(text) {
		return NewNil()
	}
	substr, ok := stringRuneSlice(text, index, 1)
	if !ok {
		return NewNil()
	}
	return NewString(substr)
}

// stringInsertByteOffset maps a Ruby String#insert character index to a byte
// offset in text, returning ok=false when the index is out of range. A
// non-negative index inserts before the character at that position, so the
// valid range is 0..runeLen (a value equal to runeLen appends). A negative
// index inserts after the character it selects, so -1 appends and the valid
// range is -(runeLen+1)..-1; the effective offset is runeLen + index + 1.
func stringInsertByteOffset(text string, index int) (int, bool) {
	if index < 0 {
		index += stringRuneLen(text) + 1
		if index < 0 {
			return 0, false
		}
	}
	return stringByteIndexForRuneOffset(text, index)
}

// stringRuneRangeSlice extracts the runes selected by a range, matching Ruby's
// String#slice(range). Negative bounds count back from the end. A begin bound
// before the start of the string (after normalization) or past its length
// returns ok=false (nil); a begin exactly at the length yields an empty string.
// The end bound is clamped to the string length, and an end before begin yields
// an empty string.
func stringRuneRangeSlice(text string, rng Range) (string, bool) {
	length := int64(stringRuneLen(text))
	begin := rng.Start
	if begin < 0 {
		begin += length
	}
	if begin < 0 || begin > length {
		return "", false
	}
	end := rng.End
	if end < 0 {
		end += length
	}
	if !rng.Exclusive {
		// An inclusive range's exclusive end is one past End; guard the increment so
		// End == math.MaxInt64 cannot wrap to a negative no-op window.
		if end == math.MaxInt64 {
			end = length
		} else {
			end++
		}
	}
	if end > length {
		end = length
	}
	if end < begin {
		end = begin
	}
	substr, ok := stringRuneSlice(text, int(begin), int(end-begin))
	if !ok {
		return "", false
	}
	return substr, true
}

func normalizeInvalidUTF8(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	return string([]rune(text))
}

// caseMode selects how the case-mapping helpers (upcase, downcase, capitalize,
// swapcase) transform their input. It mirrors Ruby's optional case-mapping
// arguments: the default applies full Unicode mapping, :ascii restricts mapping
// to ASCII letters, and :fold applies Unicode case folding (downcase only).
type caseMode int

const (
	caseModeDefault caseMode = iota
	caseModeASCII
	caseModeFold
)

// parseCaseMode interprets the optional symbol argument shared by upcase,
// downcase, capitalize, and swapcase. Ruby accepts at most one mode here (the
// remaining locale options such as :turkic are out of scope), so more than one
// argument or an argument that is not a recognized symbol is an error. The
// allowFold flag is true only for downcase, matching Ruby's rule that :fold is
// "only allowed for downcasing".
func parseCaseMode(method string, args []Value, allowFold bool) (caseMode, error) {
	if len(args) == 0 {
		return caseModeDefault, nil
	}
	if len(args) > 1 {
		return caseModeDefault, fmt.Errorf("string.%s accepts at most one case-mapping option", method)
	}
	arg := args[0]
	if arg.Kind() != KindSymbol {
		return caseModeDefault, fmt.Errorf("string.%s option must be a symbol", method)
	}
	switch arg.String() {
	case "ascii":
		return caseModeASCII, nil
	case "fold":
		if !allowFold {
			return caseModeDefault, fmt.Errorf("string.%s does not support the :fold option", method)
		}
		return caseModeFold, nil
	default:
		return caseModeDefault, fmt.Errorf("string.%s does not support the :%s option", method, arg.String())
	}
}

// stringUpcase converts text to uppercase. The default mode applies full Unicode
// case mapping (so "ß" becomes "SS" and the "ﬁ" ligature becomes "FI"); the
// :ascii mode and the invalid-UTF-8 fallback restrict mapping to ASCII letters,
// matching Ruby's binary-string behavior.
func stringUpcase(text string, mode caseMode) string {
	if mode == caseModeASCII || !utf8.ValidString(text) {
		return asciiUpcase(text)
	}
	return unicodeUpcase(text)
}

// stringDowncase converts text to lowercase. The default mode applies full
// Unicode case mapping, :fold applies Unicode case folding (e.g. "ß" becomes
// "ss"), and :ascii or invalid UTF-8 restrict mapping to ASCII letters.
func stringDowncase(text string, mode caseMode) string {
	switch {
	case mode == caseModeASCII || !utf8.ValidString(text):
		return asciiDowncase(text)
	case mode == caseModeFold:
		return cases.Fold().String(text)
	default:
		return unicodeDowncase(text)
	}
}

// unicodeUpcase applies full Unicode uppercase mapping. A fresh Caser is built
// per call because cases.Caser is not safe for concurrent use, and scripts may
// run member methods from several goroutines via the task system.
func unicodeUpcase(text string) string {
	return cases.Upper(language.Und).String(text)
}

// unicodeDowncase applies full Unicode lowercase mapping without the Greek
// final-sigma rule. Ruby's default downcase keeps a medial sigma everywhere
// ("ΟΔΟΣ".downcase is "οδοσ", not "οδος"), so final-sigma handling is disabled.
func unicodeDowncase(text string) string {
	return cases.Lower(language.Und, cases.HandleFinalSigma(false)).String(text)
}

// unicodeTitleFirst titlecases a single leading grapheme using full Unicode
// mapping. Ruby's capitalize uses the titlecase mapping for the first character
// (so the "ǆ" digraph becomes "ǅ" rather than "Ǆ"), which differs from a plain
// uppercase. NoLower keeps the call from also lowercasing trailing runes; the
// caller is expected to pass only the first character.
func unicodeTitleFirst(text string) string {
	return cases.Title(language.Und, cases.NoLower).String(text)
}

func asciiUpcase(text string) string {
	out := make([]byte, len(text))
	for i := range len(text) {
		out[i] = asciiUpper(text[i])
	}
	return string(out)
}

func asciiDowncase(text string) string {
	out := make([]byte, len(text))
	for i := range len(text) {
		out[i] = asciiLower(text[i])
	}
	return string(out)
}

func asciiUpper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - ('a' - 'A')
	}
	return b
}

func stringCapitalize(text string, mode caseMode) string {
	if text == "" {
		return ""
	}
	if mode == caseModeASCII || !utf8.ValidString(text) {
		return asciiCapitalize(text)
	}
	r, size := utf8.DecodeRuneInString(text)
	return unicodeTitleFirst(string(r)) + unicodeDowncase(text[size:])
}

// asciiCapitalize uppercases the first byte and lowercases the rest, touching
// only ASCII letters. Non-ASCII bytes (including the leading rune of a UTF-8
// sequence) are left unchanged, matching Ruby's capitalize(:ascii).
func asciiCapitalize(text string) string {
	out := make([]byte, len(text))
	out[0] = asciiUpper(text[0])
	for i := 1; i < len(text); i++ {
		out[i] = asciiLower(text[i])
	}
	return string(out)
}

func stringSwapCase(text string, mode caseMode) string {
	if mode == caseModeASCII || !utf8.ValidString(text) {
		return asciiSwapCase(text)
	}
	var b strings.Builder
	b.Grow(len(text))
	for _, r := range text {
		switch {
		case isUppercaseLike(r):
			b.WriteString(unicodeDowncase(string(r)))
		case isLowercaseLike(r):
			b.WriteString(unicodeUpcase(string(r)))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isUppercaseLike reports whether a rune should be lowercased by swapcase. It
// matches uppercase and titlecase letters (Lu/Lt) as well as cased symbols that
// live outside the letter categories yet carry a distinct lowercase mapping,
// such as circled Latin capitals ("Ⓐ") and uppercase Roman numerals ("Ⅰ"),
// which the Is{Upper,Title} predicates miss.
//
// Titlecase digraphs (e.g. "ǅ") are downcased to a single rune ("ǆ"). Ruby
// instead toggles each underlying letter component ("ǅ" -> "dŽ"); reproducing
// that would require hand-encoding Unicode's full case-mapping table (the Greek
// titlecase letters expand the iota subscript to a standalone capital iota), so
// this deliberately diverges from Ruby for those rare codepoints in favor of a
// clean lowercase.
func isUppercaseLike(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsTitle(r) || unicode.ToLower(r) != r
}

// isLowercaseLike reports whether a rune should be uppercased by swapcase. It
// matches lowercase letters (Ll), including those whose single-rune uppercase is
// identical but whose full Unicode mapping expands ("ß" -> "SS"), as well as
// cased symbols outside the letter categories with a distinct uppercase mapping,
// such as circled Latin small letters ("ⓐ") and lowercase Roman numerals
// ("ⅰ"). Uppercase-like runes are excluded by the caller checking
// isUppercaseLike first.
func isLowercaseLike(r rune) bool {
	return unicode.IsLower(r) || unicode.ToUpper(r) != r
}

// asciiSwapCase toggles the case of ASCII letters only, leaving every other byte
// (including multibyte UTF-8 sequences) unchanged. It backs Ruby's
// swapcase(:ascii) and the invalid-UTF-8 fallback for swapcase.
func asciiSwapCase(text string) string {
	out := []byte(text)
	for i, c := range out {
		switch {
		case c >= 'A' && c <= 'Z':
			out[i] = c + ('a' - 'A')
		case c >= 'a' && c <= 'z':
			out[i] = c - ('a' - 'A')
		}
	}
	return string(out)
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

// regexMatchFromRuneOffset reports whether pattern has a match in text that
// starts at or after the given rune offset, mirroring Ruby's
// Regexp#match?(str, pos). The offset is a rune (codepoint) position; a match
// may begin anywhere from that position onward. Anchors such as \A, ^, and \b
// keep the full-string context: rather than searching a detached suffix (which
// would let \A or \b match at the slice boundary), the search begins one rune
// before the offset so the engine still sees the real character preceding every
// candidate start. Because Go's RE2 has no lookbehind, that single preceding
// rune is the only left context any anchor can observe, so the wrapper stays a
// fixed size regardless of the offset: it never embeds the subject prefix into
// the pattern, keeping the compiled regex small and within the pattern-size
// guard. An offset past the end of text yields no match rather than an error,
// matching Ruby; an invalid pattern is still reported regardless of the offset,
// since the offset only decides the match result, never whether a bad regex is
// accepted. The pattern is compiled with the same guards and cache as
// String#match.
func regexMatchFromRuneOffset(method, text, pattern string, offset int) (bool, error) {
	return regexMatchFromRuneOffsetWithCache(compiledRegexps, method, text, pattern, offset)
}

// regexMatchFromRuneOffsetWithCache implements regexMatchFromRuneOffset against
// an explicit regex cache so tests can assert that the offset wrapper never
// stores an oversized, prefix-bearing pattern.
func regexMatchFromRuneOffsetWithCache(cache *regexCache, method, text, pattern string, offset int) (bool, error) {
	// Compile (and validate) the user pattern first so an invalid regex is always
	// reported, even when the offset lands past the end of the string. The offset
	// must only decide the match result, never whether a bad pattern is accepted.
	re, err := cache.compile(pattern)
	if err != nil {
		return false, fmt.Errorf("%s invalid regex: %w", method, err)
	}
	if offset == 0 {
		return re.MatchString(text), nil
	}
	byteOffset, ok := stringByteIndexForRuneOffset(text, offset)
	if !ok {
		// The offset lands past the final rune, so no match can begin there.
		return false, nil
	}
	// Search a view that begins one rune before the offset. The leading [\s\S]
	// consumes that real preceding rune so \b, \B, and ^ evaluate against it,
	// while \A correctly fails (the view does not start at the absolute string
	// start). The lazy [\s\S]*? then advances to the first candidate start at or
	// after the offset. The wrapper is independent of the prefix length, so it
	// stays small even for offsets deep into a megabyte subject.
	_, ctxSize := utf8.DecodeLastRuneInString(text[:byteOffset])
	ctxStart := byteOffset - ctxSize
	wrapped := `\A[\s\S][\s\S]*?(?:` + pattern + `)`
	re, err = cache.compile(wrapped)
	if err != nil {
		return false, fmt.Errorf("%s invalid regex: %w", method, err)
	}
	return re.MatchString(text[ctxStart:]), nil
}

// regexSubmatchFromRuneOffset returns the submatch indices of the leftmost match
// of pattern in text that starts at or after the given rune offset, mirroring
// Ruby's String#match(str, pos). The result is a flat slice of byte index pairs
// in text laid out exactly like regexp.Regexp.FindStringSubmatchIndex: element 0
// is the whole match (Ruby's group 0), and each subsequent pair is a capture
// group, with -1/-1 for groups that did not participate. A nil result means no
// match begins at or after the offset, which callers translate into Ruby's nil.
//
// The offset is a rune (codepoint) position. As with regexMatchFromRuneOffset,
// the search begins one rune before the offset so anchors such as ^, \b, and \B
// still observe the real preceding character, while \A correctly fails because
// the searched view does not start at the absolute beginning of text. The
// wrapper groups the user pattern in a capturing group so its boundaries and the
// capture indices can be recovered without embedding the subject prefix, keeping
// the compiled pattern small regardless of the offset.
func regexSubmatchFromRuneOffset(method, text, pattern string, offset int) ([]int, error) {
	return regexSubmatchFromRuneOffsetWithCache(compiledRegexps, method, text, pattern, offset)
}

// regexSubmatchFromRuneOffsetWithCache implements regexSubmatchFromRuneOffset
// against an explicit regex cache so tests can assert that the offset wrapper
// never stores an oversized, prefix-bearing pattern.
func regexSubmatchFromRuneOffsetWithCache(cache *regexCache, method, text, pattern string, offset int) ([]int, error) {
	// Compile (and validate) the user pattern first so an invalid regex is always
	// reported, even when the offset lands past the end of the string. The offset
	// must only decide the match result, never whether a bad pattern is accepted.
	re, err := cache.compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("%s invalid regex: %w", method, err)
	}
	if offset == 0 {
		return re.FindStringSubmatchIndex(text), nil
	}
	byteOffset, ok := stringByteIndexForRuneOffset(text, offset)
	if !ok {
		// The offset lands past the final rune, so no match can begin there.
		return nil, nil
	}
	// Search a view that begins one rune before the offset, capturing the user
	// pattern so its real boundaries survive the leading-context skip. The leading
	// [\s\S] consumes the real preceding rune so \b, \B, and ^ evaluate against it,
	// while \A correctly fails (the view does not start at the absolute string
	// start). The lazy [\s\S]*? then advances to the first candidate start at or
	// after the offset. The wrapper is independent of the prefix length, so it
	// stays small even for offsets deep into a megabyte subject.
	_, ctxSize := utf8.DecodeLastRuneInString(text[:byteOffset])
	ctxStart := byteOffset - ctxSize
	wrapped := `\A[\s\S][\s\S]*?(` + pattern + `)`
	wrappedRe, err := cache.compile(wrapped)
	if err != nil {
		return nil, fmt.Errorf("%s invalid regex: %w", method, err)
	}
	indices := wrappedRe.FindStringSubmatchIndex(text[ctxStart:])
	if indices == nil {
		return nil, nil
	}
	// Drop the wrapper's whole-match pair (the leading context plus the user
	// match) and re-base the remaining pairs onto text. The wrapper's group 1 is
	// the user's whole match (Ruby's group 0); each later pair is a user capture.
	userIndices := indices[2:]
	rebased := make([]int, len(userIndices))
	for i, idx := range userIndices {
		if idx < 0 {
			rebased[i] = idx
			continue
		}
		rebased[i] = idx + ctxStart
	}
	return rebased, nil
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
	return rubyRegexSub(re, text, replacement, method)
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
	return rubyRegexGSub(re, text, replacement, method)
}

func stringBangResult(original, updated string) Value {
	if updated == original {
		return NewNil()
	}
	return NewString(updated)
}

// isRubyStripSpace reports whether b is one of the ASCII whitespace bytes that
// Ruby's strip family removes from either edge: the NUL byte, horizontal tab,
// newline, vertical tab, form feed, carriage return, and space. Ruby's String
// docs define this same set for strip, lstrip, and rstrip alike. Unlike Go's
// unicode.IsSpace it never matches multibyte Unicode spaces (NBSP, Ogham space
// mark, em space, BOM, ...), which Ruby intentionally preserves.
func isRubyStripSpace(b byte) bool {
	switch b {
	case 0x00, '\t', '\n', '\v', '\f', '\r', ' ':
		return true
	default:
		return false
	}
}

// rubyLstrip trims leading Ruby strip-family whitespace (including NUL) from
// text.
func rubyLstrip(text string) string {
	start := 0
	for start < len(text) && isRubyStripSpace(text[start]) {
		start++
	}
	return text[start:]
}

// rubyRstrip trims trailing Ruby strip-family whitespace (including NUL) from
// text.
func rubyRstrip(text string) string {
	end := len(text)
	for end > 0 && isRubyStripSpace(text[end-1]) {
		end--
	}
	return text[:end]
}

// rubyStrip trims Ruby strip-family whitespace (including NUL) from both ends of
// text.
func rubyStrip(text string) string {
	return rubyLstrip(rubyRstrip(text))
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

// errInumOverflow signals that a leniently parsed integer magnitude does not
// fit in the int64 range. Ruby promotes such values to an arbitrary-precision
// Bignum, but Vibescript only has int64, so the runtime reports overflow the way
// the other integer operations do (see Integer#abs, Integer#succ).
var errInumOverflow = errors.New("integer out of range")

// inumDigit returns the numeric value of a base digit byte and whether it is a
// valid digit for the given base. Letters are case-insensitive, so 'a'/'A' both
// map to 10.
func inumDigit(b byte, base int) (int, bool) {
	var d int
	switch {
	case '0' <= b && b <= '9':
		d = int(b - '0')
	case 'a' <= b && b <= 'z':
		d = int(b-'a') + 10
	case 'A' <= b && b <= 'Z':
		d = int(b-'A') + 10
	default:
		return 0, false
	}
	if d >= base {
		return 0, false
	}
	return d, true
}

// parseRubyInum implements Ruby's lenient String#hex / String#oct conversion.
// It skips leading whitespace, accepts a single optional sign, consumes a
// base prefix (0x/0b/0o/0d, case-insensitive) when detectBase is set, honors a
// 0x/0X prefix for the fixed hexadecimal base otherwise, allows single
// underscores between digits as separators, and stops at the first byte that is
// not a valid digit. A string with no leading digit yields 0, mirroring Ruby's
// badcheck=false behavior. The magnitude is accumulated in int64; a value that
// would exceed the int64 range returns errInumOverflow because Vibescript has no
// Bignum to promote to.
func parseRubyInum(text string, defaultBase int, detectBase bool) (int64, error) {
	i := 0
	// Skip leading whitespace using Ruby's ISSPACE classification, matching
	// rb_str_to_inum.
	for i < len(text) && isRubyASCIISpace(text[i]) {
		i++
	}

	negative := false
	if i < len(text) && (text[i] == '+' || text[i] == '-') {
		negative = text[i] == '-'
		i++
	}

	base := defaultBase
	if i+1 < len(text) && text[i] == '0' {
		switch text[i+1] {
		case 'x', 'X':
			if base == 16 || detectBase {
				base = 16
				i += 2
			}
		case 'b', 'B':
			if detectBase {
				base = 2
				i += 2
			}
		case 'o', 'O':
			if detectBase {
				base = 8
				i += 2
			}
		case 'd', 'D':
			if detectBase {
				base = 10
				i += 2
			}
		}
	}

	var magnitude uint64
	parsedDigit := false
	lastWasUnderscore := false
	for i < len(text) {
		b := text[i]
		if b == '_' {
			// Underscores are separators only between two digits, so a leading,
			// trailing, or doubled underscore terminates the run like Ruby does.
			if !parsedDigit || lastWasUnderscore {
				break
			}
			lastWasUnderscore = true
			i++
			continue
		}
		d, ok := inumDigit(b, base)
		if !ok {
			break
		}
		// Detect overflow before accumulating: magnitude*base+d must fit in
		// uint64. The wraparound idiom (next < magnitude) is unsound for
		// multiplication because magnitude*base can wrap to a value still
		// >= magnitude, so check each factor exactly instead.
		if magnitude > (math.MaxUint64-uint64(d))/uint64(base) {
			return 0, errInumOverflow
		}
		magnitude = magnitude*uint64(base) + uint64(d)
		parsedDigit = true
		lastWasUnderscore = false
		i++
	}

	if !parsedDigit {
		return 0, nil
	}
	if negative {
		// MinInt64 is -(1<<63), so the negative magnitude may reach 1<<63 exactly.
		if magnitude > uint64(math.MaxInt64)+1 {
			return 0, errInumOverflow
		}
		return -int64(magnitude), nil
	}
	if magnitude > uint64(math.MaxInt64) {
		return 0, errInumOverflow
	}
	return int64(magnitude), nil
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
				return NewString(""), nil
			}
			return NewString(string(r)), nil
		}), nil
	case "hex":
		return NewAutoBuiltin("string.hex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.hex does not take arguments")
			}
			n, err := parseRubyInum(receiver.String(), 16, false)
			if err != nil {
				return NewNil(), fmt.Errorf("string.hex %w", err)
			}
			return NewInt(n), nil
		}), nil
	case "oct":
		return NewAutoBuiltin("string.oct", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.oct does not take arguments")
			}
			n, err := parseRubyInum(receiver.String(), 8, true)
			if err != nil {
				return NewNil(), fmt.Errorf("string.oct %w", err)
			}
			return NewInt(n), nil
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
	case "prepend":
		return NewAutoBuiltin("string.prepend", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			var b strings.Builder
			for _, arg := range args {
				if arg.Kind() != KindString {
					return NewNil(), fmt.Errorf("string.prepend expects string arguments")
				}
				b.WriteString(arg.String())
			}
			b.WriteString(receiver.String())
			return NewString(b.String()), nil
		}), nil
	case "insert":
		return NewAutoBuiltin("string.insert", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.insert expects an index and a string")
			}
			index, err := valueToInt(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("string.insert index must be integer")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.insert value must be string")
			}
			text := receiver.String()
			byteAt, ok := stringInsertByteOffset(text, index)
			if !ok {
				return NewNil(), fmt.Errorf("string.insert index %d out of string", index)
			}
			var b strings.Builder
			b.WriteString(text[:byteAt])
			b.WriteString(args[1].String())
			b.WriteString(text[byteAt:])
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
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.match expects a pattern and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.match pattern must be string")
			}
			pattern := args[0].String()
			text := receiver.String()
			if err := validateRegexTextPattern("string.match", text, pattern); err != nil {
				return NewNil(), err
			}
			// Ruby counts a negative offset back from the end of the string; an
			// offset that falls before the start yields nil. The regex is still
			// compiled in that branch so an invalid pattern is rejected regardless
			// of the offset, mirroring the in-range path: the offset only decides
			// the match result, never whether a bad regex is accepted.
			//
			// Unlike String#match?, a positive offset that runs past the end is
			// clamped to the length rather than rejected: Ruby still starts the
			// search at the end, so a zero-width-capable pattern matches the empty
			// string there while a pattern that needs a character returns nil. The
			// regex engine decides the outcome from the clamped end position.
			offset := 0
			if len(args) == 2 {
				raw, err := valueToInt(args[1])
				if err != nil {
					return NewNil(), fmt.Errorf("string.match offset must be integer")
				}
				effective, ok := stringEffectiveOffset(text, raw)
				if !ok {
					if _, compileErr := compileCachedRegex(pattern); compileErr != nil {
						return NewNil(), fmt.Errorf("string.match invalid regex: %w", compileErr)
					}
					return NewNil(), nil
				}
				if runeLen := stringRuneLen(text); effective > runeLen {
					effective = runeLen
				}
				offset = effective
			}
			indices, err := regexSubmatchFromRuneOffset("string.match", text, pattern, offset)
			if err != nil {
				return NewNil(), err
			}
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
	case "match?":
		return NewAutoBuiltin("string.match?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.match? does not accept keyword arguments")
			}
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.match? expects a pattern and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.match? pattern must be string")
			}
			offset := 0
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.match? offset must be non-negative integer")
				}
				offset = i
			}
			pattern := args[0].String()
			text := receiver.String()
			if err := validateRegexTextPattern("string.match?", text, pattern); err != nil {
				return NewNil(), err
			}
			matched, err := regexMatchFromRuneOffset("string.match?", text, pattern, offset)
			if err != nil {
				return NewNil(), err
			}
			return NewBool(matched), nil
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
				if err != nil {
					return NewNil(), fmt.Errorf("string.index offset must be integer")
				}
				offset = i
			}
			effective, ok := stringEffectiveOffset(receiver.String(), offset)
			if !ok {
				return NewNil(), nil
			}
			index := stringRuneIndex(receiver.String(), args[0].String(), effective)
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
				if err != nil {
					return NewNil(), fmt.Errorf("string.rindex offset must be integer")
				}
				effective, ok := stringEffectiveOffset(receiver.String(), i)
				if !ok {
					return NewNil(), nil
				}
				offset = effective
			}
			index := stringRuneRIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "slice":
		return NewAutoBuiltin("string.slice", stringSlice), nil
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
			// An explicit nil separator behaves like the no-argument form,
			// splitting on runs of ASCII whitespace, matching Ruby's
			// String#split(nil).
			if len(args) == 0 || args[0].IsNil() {
				parts = splitOnASCIIWhitespace(text)
			} else {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("string.split separator must be string or nil")
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
	case "bytes":
		return NewAutoBuiltin("string.bytes", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.bytes does not take arguments")
			}
			text := receiver.String()
			// Reject the allocation up front so a string that fits the memory
			// quota cannot reserve a result array of one Value per byte that
			// does not. make([]Value, len(text)) would reserve the entire
			// backing array before the post-call check could observe it.
			if err := exec.checkProjectedIntArrayBytes(len(text)); err != nil {
				return NewNil(), err
			}
			values := make([]Value, len(text))
			for i := range len(text) {
				values[i] = NewInt(int64(text[i]))
			}
			return NewArray(values), nil
		}), nil
	case "each_char":
		return NewAutoBuiltin("string.each_char", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.each_char does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "string.each_char")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			for _, r := range receiver.String() {
				blockArg[0] = NewString(string(r))
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_byte":
		return NewAutoBuiltin("string.each_byte", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.each_byte does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "string.each_byte")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			text := receiver.String()
			for i := range len(text) {
				blockArg[0] = NewInt(int64(text[i]))
				if _, err := runner.call(blockArg[:]); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "each_line":
		return NewAutoBuiltin("string.each_line", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 || len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.each_line does not take arguments")
			}
			runner, err := newBlockCallRunner(exec, block, "string.each_line")
			if err != nil {
				return NewNil(), err
			}
			var blockArg [1]Value
			if err := forEachLine(receiver.String(), func(line string) error {
				blockArg[0] = NewString(line)
				_, err := runner.call(blockArg[:])
				return err
			}); err != nil {
				return NewNil(), err
			}
			return receiver, nil
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
			return NewString(rubyStrip(receiver.String())), nil
		}), nil
	case "strip!":
		return NewAutoBuiltin("string.strip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip! does not take arguments")
			}
			updated := rubyStrip(receiver.String())
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
			return NewString(rubyLstrip(receiver.String())), nil
		}), nil
	case "lstrip!":
		return NewAutoBuiltin("string.lstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip! does not take arguments")
			}
			updated := rubyLstrip(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "rstrip":
		return NewAutoBuiltin("string.rstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip does not take arguments")
			}
			return NewString(rubyRstrip(receiver.String())), nil
		}), nil
	case "rstrip!":
		return NewAutoBuiltin("string.rstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip! does not take arguments")
			}
			updated := rubyRstrip(receiver.String())
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
			if args[0].Kind() == KindNil {
				// Ruby treats a nil separator as "do not chomp" and returns
				// the receiver unchanged.
				return NewString(text), nil
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
			if args[0].Kind() == KindNil {
				// Ruby treats a nil separator as "do not chomp"; since no
				// change occurs, the mutator form returns nil.
				return NewNil(), nil
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
			mode, err := parseCaseMode("upcase", args, false)
			if err != nil {
				return NewNil(), err
			}
			return NewString(stringUpcase(receiver.String(), mode)), nil
		}), nil
	case "upcase!":
		return NewAutoBuiltin("string.upcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("upcase!", args, false)
			if err != nil {
				return NewNil(), err
			}
			original := receiver.String()
			return stringBangResult(original, stringUpcase(original, mode)), nil
		}), nil
	case "downcase":
		return NewAutoBuiltin("string.downcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("downcase", args, true)
			if err != nil {
				return NewNil(), err
			}
			return NewString(stringDowncase(receiver.String(), mode)), nil
		}), nil
	case "downcase!":
		return NewAutoBuiltin("string.downcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("downcase!", args, true)
			if err != nil {
				return NewNil(), err
			}
			original := receiver.String()
			return stringBangResult(original, stringDowncase(original, mode)), nil
		}), nil
	case "capitalize":
		return NewAutoBuiltin("string.capitalize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("capitalize", args, false)
			if err != nil {
				return NewNil(), err
			}
			return NewString(stringCapitalize(receiver.String(), mode)), nil
		}), nil
	case "capitalize!":
		return NewAutoBuiltin("string.capitalize!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("capitalize!", args, false)
			if err != nil {
				return NewNil(), err
			}
			original := receiver.String()
			return stringBangResult(original, stringCapitalize(original, mode)), nil
		}), nil
	case "swapcase":
		return NewAutoBuiltin("string.swapcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("swapcase", args, false)
			if err != nil {
				return NewNil(), err
			}
			return NewString(stringSwapCase(receiver.String(), mode)), nil
		}), nil
	case "swapcase!":
		return NewAutoBuiltin("string.swapcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			mode, err := parseCaseMode("swapcase!", args, false)
			if err != nil {
				return NewNil(), err
			}
			original := receiver.String()
			return stringBangResult(original, stringSwapCase(original, mode)), nil
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
