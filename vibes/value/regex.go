package value

import (
	"regexp"
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
// Any forward slash in the source that is not already escaped is escaped so the
// result is a valid, round-trippable literal. Without this, a source built from
// a string (Regexp.new("a/b"), Regexp.union("a/b")) would render /a/b/, which
// re-parses as /a/ followed by a stray flag rather than the original pattern.
func (r Regex) String() string {
	return "/" + escapeRegexDelimiters(r.Source) + "/" + r.Flags
}

// escapeRegexDelimiters backslash-escapes every unescaped forward slash in
// source. A slash preceded by an odd number of backslashes is already escaped
// and is left untouched, so literals that already carry \/ (such as /a\/b/) are
// not double-escaped. Escaping a slash never changes what the pattern matches:
// RE2 treats \/ and / identically.
func escapeRegexDelimiters(source string) string {
	if !strings.ContainsRune(source, '/') {
		return source
	}
	var b strings.Builder
	b.Grow(len(source) + strings.Count(source, "/"))
	backslashes := 0
	for i := 0; i < len(source); i++ {
		c := source[i]
		if c == '/' && backslashes%2 == 0 {
			b.WriteByte('\\')
		}
		if c == '\\' {
			backslashes++
		} else {
			backslashes = 0
		}
		b.WriteByte(c)
	}
	return b.String()
}
