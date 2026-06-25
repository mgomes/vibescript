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
// Supported directives mirror Ruby's output for the common subset:
//
//	%Y  year, zero-padded to at least four digits (e.g. 2024)
//	%C  century (year / 100), zero-padded to two digits
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
//	%z  time zone offset from UTC (e.g. +0900); %:z inserts a colon (+09:00)
//	    and %::z adds seconds (+09:00:00)
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
func strftime(t time.Time, format string) (string, error) {
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

		if out, recognized := renderStrftimeDirective(t, token); recognized {
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
// (e.g. "%6N", "%:z", "%%"), the optional numeric width preceding the directive
// byte (used by %N), the count of leading colon modifiers (used by %z), and the
// terminating directive byte itself.
type strftimeToken struct {
	source    string
	width     string
	colons    int
	directive byte
}

// scanStrftimeDirective reads the directive that begins at the percent sign at
// index start. It consumes an optional numeric width (for %N), then optional
// colon modifiers (for %z), then the directive byte. It returns ok=false when
// the percent sequence reaches the end of the format before a directive byte
// (e.g. a bare trailing "%" or "%6"), which Ruby rejects as an invalid format.
func scanStrftimeDirective(format string, start int) (strftimeToken, bool) {
	j := start + 1

	widthStart := j
	for j < len(format) && format[j] >= '0' && format[j] <= '9' {
		j++
	}
	width := format[widthStart:j]

	colons := 0
	for j < len(format) && format[j] == ':' {
		colons++
		j++
	}

	if j >= len(format) {
		return strftimeToken{}, false
	}

	return strftimeToken{
		source:    format[start : j+1],
		width:     width,
		colons:    colons,
		directive: format[j],
	}, true
}

// renderStrftimeDirective renders a parsed directive. recognized is false when
// the directive is not part of the supported subset (or carries modifiers that
// directive does not accept), signaling the caller to emit the source verbatim.
func renderStrftimeDirective(t time.Time, tok strftimeToken) (out string, recognized bool) {
	// Only %N reads a width and only %z reads colon modifiers. A modifier on any
	// other directive makes the whole sequence an unknown one Ruby passes through.
	if tok.width != "" && tok.directive != 'N' {
		return "", false
	}
	if tok.colons != 0 && tok.directive != 'z' {
		return "", false
	}

	switch tok.directive {
	case 'Y':
		return strftimePadYear(t.Year()), true
	case 'C':
		return fmt.Sprintf("%02d", t.Year()/100), true
	case 'y':
		return fmt.Sprintf("%02d", ((t.Year()%100)+100)%100), true
	case 'm':
		return fmt.Sprintf("%02d", int(t.Month())), true
	case 'd':
		return fmt.Sprintf("%02d", t.Day()), true
	case 'e':
		return fmt.Sprintf("%2d", t.Day()), true
	case 'j':
		return fmt.Sprintf("%03d", t.YearDay()), true
	case 'H':
		return fmt.Sprintf("%02d", t.Hour()), true
	case 'k':
		return fmt.Sprintf("%2d", t.Hour()), true
	case 'I':
		return fmt.Sprintf("%02d", hour12(t.Hour())), true
	case 'l':
		return fmt.Sprintf("%2d", hour12(t.Hour())), true
	case 'M':
		return fmt.Sprintf("%02d", t.Minute()), true
	case 'S':
		return fmt.Sprintf("%02d", t.Second()), true
	case 'L':
		return strftimeSubsec(t.Nanosecond(), 3), true
	case 'N':
		return strftimeSubsec(t.Nanosecond(), strftimeNanoWidth(tok.width)), true
	case 'p':
		return meridian(t.Hour(), true), true
	case 'P':
		return meridian(t.Hour(), false), true
	case 'A':
		return t.Weekday().String(), true
	case 'a':
		return t.Weekday().String()[:3], true
	case 'B':
		return t.Month().String(), true
	case 'b', 'h':
		return t.Month().String()[:3], true
	case 'w':
		return strconv.Itoa(int(t.Weekday())), true
	case 'u':
		return strconv.Itoa(isoWeekday(t.Weekday())), true
	case 's':
		return strconv.FormatInt(t.Unix(), 10), true
	case 'z':
		return strftimeOffset(t, tok.colons), true
	case 'Z':
		name, _ := t.Zone()
		return name, true
	case 'n':
		return "\n", true
	case 't':
		return "\t", true
	case '%':
		return "%", true
	case 'F':
		return mustStrftime(t, "%Y-%m-%d"), true
	case 'T', 'X':
		return mustStrftime(t, "%H:%M:%S"), true
	case 'R':
		return mustStrftime(t, "%H:%M"), true
	case 'D', 'x':
		return mustStrftime(t, "%m/%d/%y"), true
	case 'r':
		return mustStrftime(t, "%I:%M:%S %p"), true
	case 'c':
		return mustStrftime(t, "%a %b %e %T %Y"), true
	default:
		return "", false
	}
}

// strftimeNanoWidth resolves the %N digit width: the empty default is nine
// digits (nanoseconds). The width came from a bounded decimal run, so a parse
// failure is not expected; it falls back to the default if one ever overflows.
func strftimeNanoWidth(width string) int {
	if width == "" {
		return 9
	}
	parsed, err := strconv.Atoi(width)
	if err != nil {
		return 9
	}
	return parsed
}

// strftimePadYear renders a calendar year zero-padded to at least four digits,
// matching Ruby's %Y. Negative (BCE) years keep their sign ahead of the digits.
func strftimePadYear(year int) string {
	if year < 0 {
		return fmt.Sprintf("-%04d", -year)
	}
	return fmt.Sprintf("%04d", year)
}

// strftimeSubsec renders the fractional-second component to exactly width
// digits, truncating beyond nanosecond resolution and zero-padding past it.
// This backs %L (width 3) and %N (default width 9).
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
// %:z, and %::z.
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

// mustStrftime expands a compound directive's fixed sub-format. The sub-formats
// are literal and contain only supported single-byte directives, so strftime
// cannot return an error here; a non-nil error would indicate a programming
// mistake in a compound directive definition.
func mustStrftime(t time.Time, format string) string {
	out, err := strftime(t, format)
	if err != nil {
		panic(fmt.Sprintf("runtime: invalid compound strftime directive %q: %v", format, err))
	}
	return out
}
