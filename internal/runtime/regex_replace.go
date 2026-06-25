package runtime

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// errRegexOutputLimit reports that an expansion would push the result past the
// shared regex output-size guard. Callers wrap it with their method name so the
// surfaced message matches the rest of the regex output guards.
var errRegexOutputLimit = fmt.Errorf("output exceeds limit %d bytes", maxRegexInputBytes)

// rubyAppendReplacement expands a Ruby-style replacement template against a
// single match and appends the result to dst, mirroring the substitution rules
// of Ruby's String#sub and String#gsub. It is the Ruby counterpart to Go's
// regexp.Regexp.ExpandString, which uses the foreign "$1"/"${name}" syntax.
//
// Recognized escapes (every other character, including "$", is copied
// verbatim):
//
//	\0, \&    the entire match
//	\1 .. \9  the corresponding capture group (single digit only, like Ruby)
//	\`        the text preceding the match (pre-match)
//	\'        the text following the match (post-match)
//	\+        the last capture group that participated in the match
//	\k<name>  the named capture group "name"
//	\\        a literal backslash
//
// A backslash that introduces any other sequence (including a trailing
// backslash at the end of the template) is preserved literally together with
// the character it precedes, matching Ruby. Numbered or "\+" references to
// groups that did not participate in the match expand to the empty string,
// again as Ruby does. A "\k" that names a group the pattern does not define is
// an error, as is a "\k<" that is never closed, matching Ruby's
// IndexError/RuntimeError on the same inputs.
//
// Every append is bounded by maxRegexInputBytes before it runs, so a hostile
// template (for example many "\`"/"\'" escapes against a near-limit subject)
// fails with errRegexOutputLimit instead of transiently allocating past the
// guard.
//
// loc holds submatch byte indices in the form returned by
// FindStringSubmatchIndex: loc[0:2] are the whole match, and loc[2*i:2*i+2] are
// capture group i (negative when the group did not participate).
func rubyAppendReplacement(dst []byte, re *regexp.Regexp, template, src string, loc []int) ([]byte, error) {
	for i := 0; i < len(template); i++ {
		c := template[i]
		if c != '\\' {
			next, err := appendBounded(dst, template[i:i+1])
			if err != nil {
				return nil, err
			}
			dst = next
			continue
		}
		if i+1 >= len(template) {
			// Trailing backslash: Ruby keeps it literally.
			next, err := appendBounded(dst, "\\")
			if err != nil {
				return nil, err
			}
			dst = next
			break
		}
		next := template[i+1]
		switch {
		case next >= '0' && next <= '9':
			expanded, err := appendRubySubmatch(dst, src, loc, int(next-'0'))
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		case next == '&':
			expanded, err := appendRubySubmatch(dst, src, loc, 0)
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		case next == '`':
			expanded, err := appendBounded(dst, src[:loc[0]])
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		case next == '\'':
			expanded, err := appendBounded(dst, src[loc[1]:])
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		case next == '+':
			expanded, err := appendRubyLastGroup(dst, src, loc)
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		case next == '\\':
			expanded, err := appendBounded(dst, "\\")
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		case next == 'k' && i+2 < len(template) && template[i+2] == '<':
			expanded, nameEnd, err := appendRubyNamedGroup(dst, re, src, loc, template[i+3:])
			if err != nil {
				return nil, err
			}
			dst = expanded
			// Advance past "\k<", the name, and the closing ">". nameEnd is the
			// index of ">" within template[i+3:]; the loop's own i++ steps past
			// it, so add only up to and including the closing bracket.
			i += 3 + nameEnd
		default:
			// Unknown escape (including "\k" not followed by "<"): Ruby keeps the
			// backslash and the following character literally.
			expanded, err := appendBounded(dst, template[i:i+2])
			if err != nil {
				return nil, err
			}
			dst = expanded
			i++
		}
	}
	return dst, nil
}

// appendBounded appends s to dst but first verifies the result stays within
// maxRegexInputBytes, returning errRegexOutputLimit otherwise. It guards every
// expansion in rubyAppendReplacement so no single match can over-allocate past
// the shared regex output cap, even for escapes that copy large pre/post-match
// segments.
func appendBounded(dst []byte, s string) ([]byte, error) {
	if len(s) > maxRegexInputBytes-len(dst) {
		return nil, errRegexOutputLimit
	}
	return append(dst, s...), nil
}

// appendRubySubmatch appends submatch n (0 is the whole match) when it
// participated in the match; otherwise it appends nothing. An out-of-range or
// non-participating group expands to the empty string, as Ruby does. The append
// is bounded by the shared regex output guard.
func appendRubySubmatch(dst []byte, src string, loc []int, n int) ([]byte, error) {
	start := 2 * n
	if start+1 >= len(loc) {
		return dst, nil
	}
	lo, hi := loc[start], loc[start+1]
	if lo < 0 || hi < 0 {
		return dst, nil
	}
	return appendBounded(dst, src[lo:hi])
}

// appendRubyLastGroup appends the highest-numbered capture group that
// participated in the match, matching Ruby's "\+" replacement escape. With no
// participating group it appends nothing. The append is bounded by the shared
// regex output guard.
func appendRubyLastGroup(dst []byte, src string, loc []int) ([]byte, error) {
	for n := len(loc)/2 - 1; n >= 1; n-- {
		lo, hi := loc[2*n], loc[2*n+1]
		if lo >= 0 && hi >= 0 {
			return appendBounded(dst, src[lo:hi])
		}
	}
	return dst, nil
}

// appendRubyNamedGroup expands "\k<name>" given the template text immediately
// following "\k<" (rest). It returns the appended buffer and the index of the
// closing ">" within rest so the caller can advance past the reference. An
// unterminated name or a name the pattern never defines is an error, matching
// Ruby's RuntimeError and IndexError on the same templates.
//
// When the pattern reuses a name (for example "(?<x>a)(?<x>b)" or
// "(?<x>foo)|(?<x>bar)"), Ruby resolves the reference to the *last*
// participating occurrence, matching MatchData[:name]: "(?<x>a)(?<x>b)" over
// "ab" expands to "b", and "(?<x>a)(?<x>b)?(?<x>c)" over "ac" expands to "c".
// When the name exists but no occurrence participated, Ruby expands to the
// empty string. An undefined name is an error.
func appendRubyNamedGroup(dst []byte, re *regexp.Regexp, src string, loc []int, rest string) ([]byte, int, error) {
	end := strings.IndexByte(rest, '>')
	if end < 0 {
		return nil, 0, fmt.Errorf("invalid group name reference format")
	}
	name := rest[:end]
	defined := false
	lastParticipating := -1
	for idx, candidate := range re.SubexpNames() {
		if candidate == "" || candidate != name {
			continue
		}
		defined = true
		if 2*idx+1 < len(loc) && loc[2*idx] >= 0 && loc[2*idx+1] >= 0 {
			lastParticipating = idx
		}
	}
	if !defined {
		return nil, 0, fmt.Errorf("undefined group name reference: %s", name)
	}
	if lastParticipating < 0 {
		// The name exists but no occurrence participated; Ruby expands to empty.
		return dst, end, nil
	}
	expanded, err := appendRubySubmatch(dst, src, loc, lastParticipating)
	if err != nil {
		return nil, 0, err
	}
	return expanded, end, nil
}

// rubyRegexSub replaces the first match of re in src using the Ruby-style
// replacement template, enforcing the shared regex output-size guard. It is the
// Ruby-semantics counterpart of the first-match path that previously relied on
// Go's ExpandString.
func rubyRegexSub(re *regexp.Regexp, src, template, method string) (string, error) {
	loc := re.FindStringSubmatchIndex(src)
	if loc == nil {
		return src, nil
	}
	replaced, err := rubyAppendReplacement(nil, re, template, src, loc)
	if err != nil {
		return "", fmt.Errorf("%s %w", method, err)
	}
	outputLen := len(src) - (loc[1] - loc[0]) + len(replaced)
	if outputLen > maxRegexInputBytes {
		return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	return src[:loc[0]] + string(replaced) + src[loc[1]:], nil
}

// rubyRegexGSub replaces every match of re in src using the Ruby-style
// replacement template. Match iteration mirrors regexReplaceAllWithLimit
// (empty-match advancement and the output-size guard); only the per-match
// expansion differs, using Ruby substitution rules instead of Go's.
func rubyRegexGSub(re *regexp.Regexp, src, template, method string) (string, error) {
	out := make([]byte, 0, len(src))
	lastAppended := 0
	searchStart := 0
	lastMatchEnd := -1
	for searchStart <= len(src) {
		loc, found := nextRegexReplaceAllSubmatchIndex(re, src, searchStart)
		if !found {
			break
		}
		if loc[0] == loc[1] && loc[0] == lastMatchEnd {
			if loc[0] >= len(src) {
				break
			}
			_, size := utf8.DecodeRuneInString(src[loc[0]:])
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
		out = append(out, src[lastAppended:loc[0]]...)
		expanded, err := rubyAppendReplacement(out, re, template, src, loc)
		if err != nil {
			return "", fmt.Errorf("%s %w", method, err)
		}
		out = expanded
		if len(out) > maxRegexInputBytes {
			return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
		}
		lastAppended = loc[1]
		lastMatchEnd = loc[1]

		if loc[1] > loc[0] {
			searchStart = loc[1]
			continue
		}
		if loc[1] >= len(src) {
			break
		}
		_, size := utf8.DecodeRuneInString(src[loc[1]:])
		if size == 0 {
			size = 1
		}
		searchStart = loc[1] + size
	}

	tailLen := len(src) - lastAppended
	if len(out) > maxRegexInputBytes-tailLen {
		return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	out = append(out, src[lastAppended:]...)
	return string(out), nil
}
