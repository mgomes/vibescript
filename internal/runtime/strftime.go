package runtime

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// strftime renders t using a Ruby-compatible subset of strftime percent
// directives. It backs Time#strftime, giving Ruby formatting code a familiar
// API alongside Vibescript's Go-layout Time#format.
//
// A directive has the shape %<flags><width><colons><letter>, mirroring Ruby:
//
//	flags  any mix of - (omit padding), _ (pad with spaces), 0 (pad with
//	       zeros), ^ (uppercase the result), and # (toggle case: lowercase an
//	       all-uppercase result, otherwise uppercase it). Among the padding flags
//	       - sticks once seen, so it wins over a later _ or 0 (%-0d -> "2");
//	       otherwise the last of _ or 0 wins. The case flags are not last-wins:
//	       when # is present it toggles the value (%#^p -> "am"), while ^ -- on
//	       its own or inherited from a compound -- uppercases it.
//	width  an optional decimal minimum field width. Ruby honors a width on
//	       every numeric and name directive, not just %N, so %6Y -> "002024"
//	       and %^10B -> "   JANUARY".
//	colons  one to three leading colons, accepted only by %z to widen the offset
//	       punctuation (%:z -> +09:00, %::z -> +09:00:00, %:::z -> the compact
//	       form that drops trailing all-zero components, e.g. +09).
//
// Compound directives (%F, %T, %X, %R, %D, %x, %r, %c) expand a fixed
// sub-format. The ^ flag propagates into the expansion (%^c uppercases the
// nested names) while the # flag does not, matching Ruby. A width pads the whole
// expansion as one field (%12F -> "  2024-01-02", %012F -> "002024-01-02").
//
// Supported directives mirror Ruby's output for the common subset:
//
//	%Y  year; the default minimum of four counts magnitude digits only, so a BCE
//	    year keeps four digits after the sign (e.g. 2024, -0001)
//	%C  century (year floor-divided by 100), zero-padded to two digits; a BCE
//	    century floors toward negative infinity (year -1 -> "-1")
//	%y  year within century, zero-padded to two digits (00..99)
//	%m  month of year, zero-padded (01..12)
//	%d  day of month, zero-padded (01..31)
//	%e  day of month, blank-padded ( 1..31)
//	%j  day of year, zero-padded to three digits (001..366)
//	%H  hour of day, 24-hour clock, zero-padded (00..23)
//	%k  hour of day, 24-hour clock, blank-padded ( 0..23)
//	%I  hour of day, 12-hour clock, zero-padded (01..12)
//	%l  hour of day, 12-hour clock, blank-padded ( 1..12)
//	%M  minute of hour, zero-padded (00..59)
//	%S  second of minute, zero-padded (00..60)
//	%L  fractional seconds in milliseconds, three digits
//	%N  fractional seconds in nanoseconds, nine digits; %3N/%6N/%9N etc. pick
//	    the digit width (truncating or zero-padding to that many digits)
//	%p  meridian indicator, uppercase (AM/PM)
//	%P  meridian indicator, lowercase (am/pm)
//	%A  full weekday name in English (Sunday..Saturday)
//	%a  abbreviated weekday name in English (Sun..Sat)
//	%B  full month name in English (January..December)
//	%b  abbreviated month name in English (Jan..Dec); %h is an alias
//	%w  day of week, Sunday is 0 (0..6)
//	%u  day of week, Monday is 1 (1..7)
//	%s  seconds since the Unix epoch
//	%z  time zone offset from UTC (e.g. +0900); %:z inserts a colon (+09:00),
//	    %::z adds seconds (+09:00:00), and %:::z is the compact form that drops
//	    trailing all-zero components (+09, +05:30, +05:30:15)
//	%Z  time zone name (matching Time#zone)
//	%n  newline; %t  tab; %%  a literal percent sign
//	%F  shorthand for %Y-%m-%d
//	%T / %X  shorthand for %H:%M:%S
//	%R  shorthand for %H:%M
//	%D / %x  shorthand for %m/%d/%y
//	%r  shorthand for %I:%M:%S %p
//	%c  shorthand for %a %b %e %T %Y
//
// Unknown directives are emitted verbatim (e.g. "%Q" stays "%Q"), matching
// Ruby. A percent sequence that reaches the end of the format before its
// directive byte -- a bare trailing "%", or modifiers with no directive such as
// "%6" or "%:" -- is rejected as an invalid format. Ruby agrees for the bare
// "%" and "%6" cases but happens to pass "%:" through; Vibescript treats every
// modifier-without-directive uniformly as malformed rather than reproducing
// Ruby's per-modifier quirks for that degenerate input.
//
// One narrow divergence: the %z offset honors width with zero/default padding
// (%6z -> "+00530") but treats the _ space-padding flag as a no-op. Ruby's
// space-padded offset renders a quirky, lossy form (%_z -> " +530"); Vibescript
// keeps the offset intact rather than reproducing that degenerate behavior.
func strftime(t time.Time, format string) (string, error) {
	return strftimeCase(t, format, false)
}

// caseFlag captures the case transformation applied to a directive's rendered
// value. It collapses the ^ and # flags into a single decision so applyCase can
// share one switch across directives.
type caseFlag int

const (
	caseNone   caseFlag = iota // leave the rendered value unchanged
	caseUpper                  // ^ : uppercase the whole result
	caseToggle                 // # : lowercase all-uppercase results, else uppercase
)

// strftimeCase renders format. inheritedUpper carries the ^ flag down from a
// compound directive so it uppercases the nested name directives, matching
// Ruby's %^c -> "TUE JAN  2 ...". The # flag never propagates into a compound,
// so it has no inherited counterpart.
func strftimeCase(t time.Time, format string, inheritedUpper bool) (string, error) {
	var b strings.Builder
	b.Grow(len(format) + 16)

	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}

		token, ok := scanStrftimeDirective(format, i)
		if !ok {
			// A bare trailing percent has no directive, which Ruby rejects.
			return "", fmt.Errorf("time.strftime invalid format: %q", format)
		}

		if out, recognized := renderStrftimeDirective(t, token, inheritedUpper); recognized {
			b.WriteString(out)
		} else {
			// Unknown directive: emit the percent sequence verbatim like Ruby.
			b.WriteString(token.source)
		}
		i += len(token.source) - 1
	}

	return b.String(), nil
}

// strftimeToken captures one parsed percent directive: the full source slice
// (e.g. "%6N", "%:z", "%-^10B"), the parsed flags, the optional numeric width,
// the count of leading colon modifiers (used by %z), and the terminating
// directive byte itself.
type strftimeToken struct {
	source    string
	hasWidth  bool
	width     int
	colons    int
	directive byte

	noPad    bool // - flag: omit padding entirely
	spacePad bool // _ flag: pad with spaces
	zeroPad  bool // 0 flag: pad with zeros
	upper    bool // ^ flag: uppercase the result
	toggle   bool // # flag: lowercase all-uppercase results, else uppercase
}

// scanStrftimeDirective reads the directive that begins at the percent sign at
// index start. It consumes any flag bytes, an optional decimal width, then
// optional colon modifiers (for %z), then the directive byte. It returns
// ok=false when the percent sequence reaches the end of the format before a
// directive byte (e.g. a bare trailing "%" or "%6"), which Ruby rejects as an
// invalid format.
func scanStrftimeDirective(format string, start int) (strftimeToken, bool) {
	tok := strftimeToken{}
	j := start + 1

	for j < len(format) {
		switch format[j] {
		case '-':
			// Ruby treats - as no-padding whenever it appears in the flag set, so
			// it sticks even when a later _ or 0 padding flag follows (%-0d -> "2").
			tok.noPad = true
		case '_':
			tok.spacePad, tok.zeroPad = true, false
		case '0':
			tok.spacePad, tok.zeroPad = false, true
		case '^':
			tok.upper = true
		case '#':
			tok.toggle = true
		default:
			goto flagsDone
		}
		j++
	}
flagsDone:

	widthStart := j
	for j < len(format) && format[j] >= '0' && format[j] <= '9' {
		j++
	}
	if j > widthStart {
		// The run is a bounded decimal slice, but guard against overflow on a
		// pathological width so an extreme value falls back to no explicit width
		// rather than panicking.
		if w, err := strconv.Atoi(format[widthStart:j]); err == nil {
			tok.hasWidth, tok.width = true, w
		}
	}

	for j < len(format) && format[j] == ':' {
		tok.colons++
		j++
	}

	if j >= len(format) {
		return strftimeToken{}, false
	}

	tok.source = format[start : j+1]
	tok.directive = format[j]
	return tok, true
}

// strftimeFieldKind classifies how a directive consumes width and padding.
type strftimeFieldKind int

const (
	fieldNumeric  strftimeFieldKind = iota // width is a minimum field width
	fieldYear                              // %Y: default width counts digits only
	fieldName                              // width pads, ^/# transform case
	fieldSubsec                            // width is the fractional digit count
	fieldLiteral                           // single literal byte, width pads
	fieldOffset                            // %z: width zero-pads the digits
	fieldCompound                          // expands a fixed sub-format
)

// renderStrftimeDirective renders a parsed directive. recognized is false when
// the directive is not part of the supported subset (or carries colon modifiers
// the directive does not accept), signaling the caller to emit the source
// verbatim.
func renderStrftimeDirective(t time.Time, tok strftimeToken, inheritedUpper bool) (out string, recognized bool) {
	// Only %z reads colon modifiers, and only one (%:z), two (%::z), or three
	// (%:::z) of them; any other directive carrying a colon, or %z with four or
	// more, is an unknown sequence emitted verbatim like Ruby.
	if tok.colons != 0 && (tok.directive != 'z' || tok.colons > 3) {
		return "", false
	}

	value, padChar, defaultWidth, kind, ok := strftimeField(t, tok)
	if !ok {
		return "", false
	}

	shift := directiveCase(tok, inheritedUpper)

	switch kind {
	case fieldCompound:
		// value holds the compound directive's sub-format. The case flag is
		// applied by expanding the sub-format with the propagated ^ flag rather
		// than transforming the rendered string, matching Ruby: %^c uppercases
		// the nested names while the # flag does not reach into a compound at all
		// (so it never propagates). The expanded result is then padded to the
		// requested width as a single field.
		expanded := mustStrftime(t, value, tok.upper || inheritedUpper)
		return padCompound(expanded, tok), true
	case fieldYear:
		// %Y's default minimum width counts magnitude digits only, so a BCE year
		// renders as "-0001" (sign plus four digits), while an explicit width is a
		// total field width counting the sign (%5Y of -1 -> "-0001", %4Y -> "-001").
		return applyCase(padYear(value, tok), shift), true
	case fieldSubsec:
		// %L/%N already encode width as digit count; only case applies.
		return applyCase(value, shift), true
	case fieldOffset:
		return padOffset(value, tok), true
	default:
		return applyCase(applyPad(value, padChar, defaultWidth, tok), shift), true
	}
}

// directiveCase resolves the case transformation for a single directive from its
// ^ (upper) and # (toggle) flags plus any ^ inherited from an enclosing compound.
// Ruby's combined-flag behavior is not last-wins: when # is present it toggles
// the value (%#^p -> "am"); otherwise ^ — whether on the directive or inherited
// from a compound — uppercases it.
func directiveCase(tok strftimeToken, inheritedUpper bool) caseFlag {
	switch {
	case tok.toggle:
		return caseToggle
	case tok.upper || inheritedUpper:
		return caseUpper
	default:
		return caseNone
	}
}

// strftimeField resolves a directive to its rendered value, default pad
// character, default minimum width, and field kind. For a fieldCompound
// directive, value is the sub-format the caller expands rather than a rendered
// string. ok is false for unknown directives.
func strftimeField(t time.Time, tok strftimeToken) (value string, padChar byte, defaultWidth int, kind strftimeFieldKind, ok bool) {
	switch tok.directive {
	case 'Y':
		return strftimeYear(t.Year()), '0', 4, fieldYear, true
	case 'C':
		return strconv.Itoa(floorDiv(t.Year(), 100)), '0', 2, fieldNumeric, true
	case 'y':
		return strconv.Itoa(((t.Year() % 100) + 100) % 100), '0', 2, fieldNumeric, true
	case 'm':
		return strconv.Itoa(int(t.Month())), '0', 2, fieldNumeric, true
	case 'd':
		return strconv.Itoa(t.Day()), '0', 2, fieldNumeric, true
	case 'e':
		return strconv.Itoa(t.Day()), ' ', 2, fieldNumeric, true
	case 'j':
		return strconv.Itoa(t.YearDay()), '0', 3, fieldNumeric, true
	case 'H':
		return strconv.Itoa(t.Hour()), '0', 2, fieldNumeric, true
	case 'k':
		return strconv.Itoa(t.Hour()), ' ', 2, fieldNumeric, true
	case 'I':
		return strconv.Itoa(hour12(t.Hour())), '0', 2, fieldNumeric, true
	case 'l':
		return strconv.Itoa(hour12(t.Hour())), ' ', 2, fieldNumeric, true
	case 'M':
		return strconv.Itoa(t.Minute()), '0', 2, fieldNumeric, true
	case 'S':
		return strconv.Itoa(t.Second()), '0', 2, fieldNumeric, true
	case 'w':
		return strconv.Itoa(int(t.Weekday())), '0', 1, fieldNumeric, true
	case 'u':
		return strconv.Itoa(isoWeekday(t.Weekday())), '0', 1, fieldNumeric, true
	case 's':
		return strconv.FormatInt(t.Unix(), 10), '0', 1, fieldNumeric, true
	case 'L':
		return strftimeSubsec(t.Nanosecond(), strftimeSubsecWidth(tok, 3)), 0, 0, fieldSubsec, true
	case 'N':
		return strftimeSubsec(t.Nanosecond(), strftimeSubsecWidth(tok, 9)), 0, 0, fieldSubsec, true
	case 'p':
		return meridian(t.Hour(), true), ' ', 0, fieldName, true
	case 'P':
		return meridian(t.Hour(), false), ' ', 0, fieldName, true
	case 'A':
		return t.Weekday().String(), ' ', 0, fieldName, true
	case 'a':
		return t.Weekday().String()[:3], ' ', 0, fieldName, true
	case 'B':
		return t.Month().String(), ' ', 0, fieldName, true
	case 'b', 'h':
		return t.Month().String()[:3], ' ', 0, fieldName, true
	case 'z':
		return strftimeOffset(t, tok.colons), '0', 0, fieldOffset, true
	case 'Z':
		name, _ := t.Zone()
		return name, ' ', 0, fieldName, true
	case 'n':
		return "\n", ' ', 1, fieldLiteral, true
	case 't':
		return "\t", ' ', 1, fieldLiteral, true
	case '%':
		return "%", ' ', 1, fieldLiteral, true
	case 'F':
		return "%Y-%m-%d", 0, 0, fieldCompound, true
	case 'T', 'X':
		return "%H:%M:%S", 0, 0, fieldCompound, true
	case 'R':
		return "%H:%M", 0, 0, fieldCompound, true
	case 'D', 'x':
		return "%m/%d/%y", 0, 0, fieldCompound, true
	case 'r':
		return "%I:%M:%S %p", 0, 0, fieldCompound, true
	case 'c':
		return "%a %b %e %T %Y", 0, 0, fieldCompound, true
	default:
		return "", 0, 0, fieldNumeric, false
	}
}

// applyPad pads value to its field width using the flag/width modifiers. The
// minus flag omits padding, the underscore and zero flags override the pad
// character, and an explicit width overrides the directive's default minimum.
// Padding is never applied when it would have to truncate a value wider than the
// field, and a sign on a zero-padded numeric stays ahead of the digits.
func applyPad(value string, defaultPad byte, defaultWidth int, tok strftimeToken) string {
	// An empty value (only %Z on an unnamed zone) is never padded; Ruby keeps it
	// empty rather than emitting a run of pad characters.
	if tok.noPad || value == "" {
		return value
	}

	width := defaultWidth
	if tok.hasWidth {
		width = tok.width
	}
	if len(value) >= width {
		return value
	}

	pad := defaultPad
	switch {
	case tok.spacePad:
		pad = ' '
	case tok.zeroPad:
		pad = '0'
	}

	if pad == '0' && len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		return string(value[0]) + strings.Repeat("0", width-len(value)) + value[1:]
	}
	return strings.Repeat(string(pad), width-len(value)) + value
}

// padOffset pads a %z offset. Ruby zero-pads the digit run after the sign to the
// requested width and treats the -, _, and 0 flags as no-ops for the offset (so
// even %-6z still zero-pads). Case flags do not affect an offset either.
func padOffset(value string, tok strftimeToken) string {
	if !tok.hasWidth || len(value) >= tok.width {
		return value
	}
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		return string(value[0]) + strings.Repeat("0", tok.width-len(value)) + value[1:]
	}
	return strings.Repeat("0", tok.width-len(value)) + value
}

// padYear pads a %Y value, reproducing Ruby's split width semantics: an explicit
// width is a total field width that counts the sign, while the default minimum of
// four counts only the magnitude digits, so a BCE year keeps four digits after
// the sign (%Y of -1 -> "-0001"). The zero pad (default or 0 flag) goes after the
// sign; the space pad (_ flag) goes before the whole value; the - flag drops
// padding entirely.
func padYear(value string, tok strftimeToken) string {
	if tok.noPad {
		return value
	}

	sign, digits := "", value
	if len(value) > 0 && (value[0] == '+' || value[0] == '-') {
		sign, digits = value[:1], value[1:]
	}

	if tok.hasWidth {
		if len(value) >= tok.width {
			return value
		}
		if tok.spacePad {
			return strings.Repeat(" ", tok.width-len(value)) + value
		}
		return sign + strings.Repeat("0", tok.width-len(value)) + digits
	}

	const minDigits = 4
	if len(digits) >= minDigits {
		return value
	}
	if tok.spacePad {
		return strings.Repeat(" ", minDigits-len(digits)) + value
	}
	return sign + strings.Repeat("0", minDigits-len(digits)) + digits
}

// padCompound pads an expanded compound directive (e.g. %F, %T) to its width.
// Ruby pads the whole expansion as one field with spaces, or zeros under the 0
// flag, and ignores the - and _ flags here (so %-12F still space-pads), the same
// way it treats the %z offset.
func padCompound(value string, tok strftimeToken) string {
	if !tok.hasWidth || len(value) >= tok.width {
		return value
	}
	pad := byte(' ')
	if tok.zeroPad {
		pad = '0'
	}
	return strings.Repeat(string(pad), tok.width-len(value)) + value
}

// applyCase applies the ^/# case transformation to a rendered value. caseUpper
// uppercases everything; caseToggle reproduces Ruby's # flag, which lowercases a
// value whose cased letters are already all uppercase (e.g. %#p "AM" -> "am",
// %#Z "UTC" -> "utc") and uppercases everything else (e.g. %#B "January" ->
// "JANUARY").
func applyCase(value string, shift caseFlag) string {
	switch shift {
	case caseUpper:
		return strings.ToUpper(value)
	case caseToggle:
		if isAllUpper(value) {
			return strings.ToLower(value)
		}
		return strings.ToUpper(value)
	default:
		return value
	}
}

// isAllUpper reports whether value contains at least one cased letter and every
// cased letter is uppercase, the condition Ruby's # flag uses to decide whether
// to lowercase rather than uppercase a directive's output.
func isAllUpper(value string) bool {
	sawCased := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= 'A' && r <= 'Z':
			sawCased = true
		}
	}
	return sawCased
}

// strftimeSubsecWidth resolves the fractional-second digit count for %L and %N.
// An explicit width selects the digit count; otherwise the directive's default
// applies (3 for %L, 9 for %N). The minus flag, which suppresses padding
// elsewhere, has no meaning for fractional digits and is ignored as in Ruby.
func strftimeSubsecWidth(tok strftimeToken, def int) int {
	if tok.hasWidth {
		return tok.width
	}
	return def
}

// strftimeYear renders a calendar year. Negative (BCE) years keep their sign
// ahead of the digits; the four-digit minimum is applied by the padding step.
func strftimeYear(year int) string {
	if year < 0 {
		return "-" + strconv.Itoa(-year)
	}
	return strconv.Itoa(year)
}

// floorDiv returns the floored quotient of a/b, rounding toward negative
// infinity rather than toward zero like Go's / operator. %C uses it so a BCE
// century floors to Ruby's value (year -1 -> century -1, not Go's 0).
func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// strftimeSubsec renders the fractional-second component to exactly width
// digits, truncating beyond nanosecond resolution and zero-padding past it.
// This backs %L (default width 3) and %N (default width 9).
func strftimeSubsec(nanos, width int) string {
	if width <= 0 {
		return ""
	}
	full := fmt.Sprintf("%09d", nanos)
	if width <= len(full) {
		return full[:width]
	}
	return full + strings.Repeat("0", width-len(full))
}

// strftimeOffset renders the UTC offset for %z. colons selects the punctuation:
// 0 yields +HHMM, 1 yields +HH:MM, and 2 yields +HH:MM:SS, matching Ruby's %z,
// %:z, and %::z. colons == 3 renders Ruby's %:::z compact form, which drops the
// trailing all-zero components: +05:30:15 stays +05:30:15, +05:30:00 collapses
// to +05:30, and +00:00:00 collapses to +00.
func strftimeOffset(t time.Time, colons int) string {
	_, offset := t.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	h := offset / 3600
	m := (offset % 3600) / 60
	s := offset % 60
	switch colons {
	case 1:
		return fmt.Sprintf("%s%02d:%02d", sign, h, m)
	case 2:
		return fmt.Sprintf("%s%02d:%02d:%02d", sign, h, m, s)
	case 3:
		switch {
		case s != 0:
			return fmt.Sprintf("%s%02d:%02d:%02d", sign, h, m, s)
		case m != 0:
			return fmt.Sprintf("%s%02d:%02d", sign, h, m)
		default:
			return fmt.Sprintf("%s%02d", sign, h)
		}
	default:
		return fmt.Sprintf("%s%02d%02d", sign, h, m)
	}
}

// hour12 maps a 24-hour clock hour to its 12-hour clock equivalent (1..12).
func hour12(hour int) int {
	h := hour % 12
	if h == 0 {
		return 12
	}
	return h
}

// meridian returns the AM/PM indicator for hour. upper picks the uppercase form
// (%p) versus the lowercase form (%P).
func meridian(hour int, upper bool) string {
	if hour < 12 {
		if upper {
			return "AM"
		}
		return "am"
	}
	if upper {
		return "PM"
	}
	return "pm"
}

// isoWeekday maps Go's Weekday (Sunday=0) to the ISO numbering used by %u where
// Monday is 1 and Sunday is 7.
func isoWeekday(wd time.Weekday) int {
	if wd == time.Sunday {
		return 7
	}
	return int(wd)
}

// mustStrftime expands a compound directive's fixed sub-format, propagating the
// ^ flag (inheritedUpper) the compound directive carried. The sub-formats are
// literal and contain only supported single-byte directives, so strftime cannot
// return an error here; a non-nil error would indicate a programming mistake in
// a compound directive definition.
func mustStrftime(t time.Time, format string, inheritedUpper bool) string {
	out, err := strftimeCase(t, format, inheritedUpper)
	if err != nil {
		panic(fmt.Sprintf("runtime: invalid compound strftime directive %q: %v", format, err))
	}
	return out
}
