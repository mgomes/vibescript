package value

import (
	"regexp"
	"strconv"
	"strings"
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
	for i := 0; i < len(source); i++ {
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
	for i := 0; i < len(source); i++ {
		if c := source[i]; c == '/' || c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}
