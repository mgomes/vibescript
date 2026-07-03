package value

import "regexp"

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
func (r Regex) String() string {
	return "/" + r.Source + "/" + r.Flags
}
