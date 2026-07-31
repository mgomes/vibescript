package value

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Regex is the payload of a KindRegex value: a compiled Ruby-style regex
// literal. Source is the pattern text between the slashes exactly as written
// (Go RE2 syntax), Flags holds the literal's flag letters in source order, and
// Compiled is the ready-to-match engine program the runtime compiled with the
// flags applied. A Regex is immutable once constructed, so values share it
// freely across clones and task boundaries.
type Regex struct {
	Source   string
	Flags    string
	Compiled *regexp.Regexp
}

// NewRegex returns a regex Value.
func NewRegex(r Regex) Value { return Value{kind: KindRegex, data: r} }

// Regex returns the regex payload of v. It panics when v is not a regex, like
// the other kind accessors.
func (v Value) Regex() Regex { return v.data.(Regex) }

// String renders the regex the way it is written in source: /pattern/flags.
// The source is escaped so wrapping it in delimiters yields a valid,
// round-trippable literal. Without this, a source built from a string
// (Regexp.new("a/b"), Regexp.new("\n")) would render /a/b/ or embed a raw
// newline, neither of which the lexer can re-parse.
func (r Regex) String() string {
	return "/" + escapeRegexLiteralSource(r.Source) + "/" + r.Flags
}

// StringLen reports the length String would return without building it.
//
// Sizing a regex by measuring String() performs the escaping and allocation
// being measured, which a quota cannot then prevent and a caller that renders
// afterwards pays for twice. This walks the source instead, mirroring
// escapeRegexLiteralSource case for case; TestRegexStringLenMatchesString holds
// the two together.
func (r Regex) StringLen() int {
	escaped := 0
	backslashes := 0
	for i := range len(r.Source) {
		c := r.Source[i]
		switch {
		case c == '/' && backslashes%2 == 0:
			escaped += len(`\/`)
		case c == '\a', c == '\t', c == '\n', c == '\v', c == '\f', c == '\r':
			escaped += 2
		case c < 0x20 || c == 0x7f:
			escaped += len(`\x{`) + len(strconv.FormatInt(int64(c), 16)) + len(`}`)
		default:
			escaped++
		}
		if c == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
	}
	return len("//") + escaped + len(r.Flags)
}

// escapeRegexLiteralSource returns source rendered so that wrapping it in `/`
// delimiters yields a valid, round-trippable regex literal. An unescaped forward
// slash is backslash-escaped (a slash after an odd run of backslashes is already
// escaped and left alone, so /a\/b/ is not double-escaped), and control
// characters — which the lexer rejects inside a literal — are rewritten as RE2
// escapes. RE2 treats the escaped forms identically, so matching is unchanged.
func escapeRegexLiteralSource(source string) string {
	if !regexSourceNeedsEscaping(source) {
		return source
	}
	var b strings.Builder
	b.Grow(len(source) + 8)
	backslashes := 0
	for i := range len(source) {
		c := source[i]
		switch {
		case c == '/' && backslashes%2 == 0:
			b.WriteString(`\/`)
		case c == '\a':
			b.WriteString(`\a`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\v':
			b.WriteString(`\v`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c == 0x7f:
			b.WriteString(`\x{`)
			b.WriteString(strconv.FormatInt(int64(c), 16))
			b.WriteString(`}`)
		default:
			b.WriteByte(c)
		}
		if c == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
	}
	return b.String()
}

// regexSourceNeedsEscaping reports whether source contains a byte that
// escapeRegexLiteralSource would rewrite: a forward slash or a control
// character. Patterns without any (the common case) render without allocating.
func regexSourceNeedsEscaping(source string) bool {
	for i := range len(source) {
		if c := source[i]; c == '/' || c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// StringRuneLen reports the rune count String would return without building it.
//
// Every escape the renderer emits is ASCII, so an escaped byte contributes as
// many runes as the escape has characters; a byte that passes through
// contributes as part of whatever rune it belongs to. Counting bytes will not do
// here, because a source may hold multibyte runes that survive unescaped.
func (r Regex) StringRuneLen() int {
	runes := 0
	backslashes := 0
	for i := 0; i < len(r.Source); i++ {
		c := r.Source[i]
		switch {
		case c == '/' && backslashes%2 == 0:
			runes += len(`\/`)
		case c == '\a', c == '\t', c == '\n', c == '\v', c == '\f', c == '\r':
			runes += 2
		case c < 0x20 || c == 0x7f:
			runes += len(`\x{`) + len(strconv.FormatInt(int64(c), 16)) + len(`}`)
		default:
			// Decode rather than test for a continuation byte. A stray one is
			// invalid UTF-8, which String preserves and RuneCountInString counts
			// as a single RuneError -- skipping it undercounted a source of them
			// by its whole length. DecodeRuneInString reports size 1 for that
			// case and the true width otherwise, so advancing by size counts
			// each rune exactly once.
			_, size := utf8.DecodeRuneInString(r.Source[i:])
			runes++
			i += size - 1
		}
		if c == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
	}
	return len("//") + runes + utf8.RuneCountInString(r.Flags)
}

// regexSourceStepBytes is the source bytes one sandbox step covers when a regex
// is sized or rendered. It matches the rate the runtime charges byte-oriented
// string work at; the constant is duplicated because this package cannot import
// the runtime, and TestRegexSourceStepBytesMatchesRuntime holds the two equal.
const regexSourceStepBytes = 64

// chargeRegexSourceSteps charges step once per regexSourceStepBytes of source,
// for the walk StringLen performs. Sizing a regex without rendering it still
// reads every source byte, and the step callback is the only accounting the
// projection walkers have -- mirroring chargeBigIntRenderSteps, which charges
// the same way for a base conversion.
func chargeRegexSourceSteps(v Value, step func() error) error {
	if v.kind != KindRegex {
		return nil
	}
	for range len(v.data.(Regex).Source) / regexSourceStepBytes {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// RegexSourceStepBytesForTest exposes regexSourceStepBytes so the runtime's test
// suite can hold it equal to its own byte-work rate. The constant is duplicated
// because this package cannot import the runtime.
func RegexSourceStepBytesForTest() int { return regexSourceStepBytes }
