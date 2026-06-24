package value

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// DefaultTimeParseLayouts is the ordered list of layouts attempted by
// ParseTimeString when no explicit layout is supplied.
var DefaultTimeParseLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	time.RFC1123Z,
	time.RFC1123,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006/01/02 15:04:05",
	"2006-01-02",
	"2006/01/02",
	"01/02/2006 15:04:05",
	"01/02/2006",
}

// ParseLocation parses a timezone specifier carried in a Value into a
// time.Location, returning (nil, nil) when val is nil.
func ParseLocation(val Value) (*time.Location, error) {
	switch val.Kind() {
	case KindString:
		return ParseLocationString(val.String())
	case KindNil:
		return nil, nil
	default:
		return nil, fmt.Errorf("invalid timezone spec")
	}
}

// ParseLocationString parses a timezone specifier string (named zone,
// fixed offset, or empty).
func ParseLocationString(spec string) (*time.Location, error) {
	if spec == "" {
		return nil, nil
	}
	switch strings.ToUpper(spec) {
	case "UTC", "GMT", "Z":
		return time.UTC, nil
	case "LOCAL":
		return time.Local, nil
	}
	if len(spec) == 6 && (spec[0] == '+' || spec[0] == '-') && spec[3] == ':' {
		sign := 1
		if spec[0] == '-' {
			sign = -1
		}
		hours, errH := strconv.Atoi(spec[1:3])
		mins, errM := strconv.Atoi(spec[4:])
		if errH != nil || errM != nil {
			return nil, fmt.Errorf("invalid timezone offset")
		}
		offset := sign * (hours*3600 + mins*60)
		return time.FixedZone(spec, offset), nil
	}
	loc, err := time.LoadLocation(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q", spec)
	}
	return loc, nil
}

// optionalDatePartReader returns a reader for the optional date/time parts of a
// Time constructor (month, day, hour, minute, second). It yields the integer
// value of the argument at idx, or fallback when that argument is absent or an
// explicit nil. Ruby's calendar constructors treat an explicit nil optional part
// the same as an omitted one, so a nil must resolve to the field's default rather
// than being coerced through Value.Int() to 0 (which time.Date would normalize
// backward into the previous month or year). The required year is read directly
// by the caller and never defaulted.
func optionalDatePartReader(args []Value) func(idx, fallback int) int {
	return func(idx, fallback int) int {
		if idx >= len(args) || args[idx].Kind() == KindNil {
			return fallback
		}
		return int(args[idx].Int())
	}
}

// TimeFromParts constructs a time.Time from a required year positional
// argument, with optional month/day/hour/minute/second and timezone arguments.
// Matching Ruby's Time.new, an omitted month or day defaults to 1 (January 1)
// and omitted time fields default to zero (midnight). An explicit nil in any of
// those positions is treated the same as omitting it, so Time.new(2024, nil)
// yields January 1 rather than normalizing month 0 into the prior year.
func TimeFromParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	if len(args) < 1 {
		return time.Time{}, fmt.Errorf("Time.new expects at least a year")
	}
	getOptional := optionalDatePartReader(args)

	year := int(args[0].Int())
	month := getOptional(1, 1)
	day := getOptional(2, 1)
	hour := getOptional(3, 0)
	min := getOptional(4, 0)
	sec := getOptional(5, 0)

	loc := defaultLoc
	if len(args) >= 7 {
		locVal := args[6]
		parsed, err := ParseLocation(locVal)
		if err != nil {
			return time.Time{}, err
		}
		if parsed != nil {
			loc = parsed
		}
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, loc), nil
}

// TimeFromCalendarParts constructs a time.Time from a required year positional
// argument, with optional month/day/hour/minute/second and a subsecond argument.
// Unlike TimeFromParts (which backs Time.new and reads the seventh argument as a
// timezone), this matches Ruby's Time.local/mktime/utc/gm where the seventh
// argument is microseconds-with-fraction and the location is fixed by the
// constructor. As with Ruby, an omitted month or day defaults to 1 (January 1)
// and omitted time fields default to zero (midnight). An explicit nil in any of
// those positions is treated the same as omitting it, so Time.utc(2024, nil)
// yields January 1 rather than normalizing month 0 into the prior year. A nil
// defaultLoc falls back to the local timezone.
func TimeFromCalendarParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	if len(args) < 1 {
		return time.Time{}, fmt.Errorf("Time constructor expects at least a year")
	}
	if len(args) > 7 {
		return time.Time{}, fmt.Errorf("Time constructor expects at most year, month, day, hour, minute, second, microsecond")
	}

	getOptional := optionalDatePartReader(args)

	year := int(args[0].Int())
	month := getOptional(1, 1)
	day := getOptional(2, 1)
	hour := getOptional(3, 0)
	min := getOptional(4, 0)
	sec := getOptional(5, 0)

	nanos := 0
	if len(args) >= 7 {
		ns, err := microsecondsArgNanos(args[6])
		if err != nil {
			return time.Time{}, err
		}
		nanos = ns
	}

	loc := defaultLoc
	if loc == nil {
		loc = time.Local
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, nanos, loc), nil
}

// nanosPerSecond is the exclusive upper bound for a valid subsecond component,
// expressed in nanoseconds.
const nanosPerSecond = 1_000_000_000

// microsPerSecond is the exclusive upper bound for a valid microsecond
// argument.
const microsPerSecond = 1_000_000

// microsecondsArgNanos converts a Ruby-style microseconds-with-fraction argument
// into nanoseconds. Integers are whole microseconds; floats carry sub-microsecond
// precision down to the nanosecond. The result must stay within a single second
// ([0, 1_000_000_000) ns), matching Ruby's "subsecx out of range" rejection for
// boundary inputs such as 1_000_000 microseconds; without this guard time.Date
// would silently normalize an out-of-range component into an adjacent second.
// Non-numeric arguments are rejected.
func microsecondsArgNanos(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		// Validate the microsecond magnitude before scaling so an extreme
		// integer cannot overflow int64 and wrap into the valid range.
		usec := val.Int()
		if usec < 0 || usec >= microsPerSecond {
			return 0, microsecondRangeError()
		}
		return int(usec * 1000), nil
	case KindFloat:
		return floatMicrosecondsNanos(val.Float())
	case KindNil:
		// An explicit nil subsecond is treated as omitted (zero), matching
		// Ruby's Time.utc(..., nil).
		return 0, nil
	default:
		return 0, fmt.Errorf("Time constructor microsecond argument must be numeric")
	}
}

// floatMicrosecondsNanos converts a fractional microsecond value into whole
// nanoseconds the way Ruby does: it operates on the float's exact value (via a
// rational, so a second layer of float multiplication cannot perturb the
// decimal result) and truncates toward zero. The value must be a finite,
// non-negative quantity that stays within a single second.
func floatMicrosecondsNanos(usec float64) (int, error) {
	if math.IsNaN(usec) || math.IsInf(usec, 0) || usec < 0 {
		return 0, microsecondRangeError()
	}
	exact := new(big.Rat).SetFloat64(usec)
	if exact == nil {
		return 0, microsecondRangeError()
	}
	exact.Mul(exact, big.NewRat(1000, 1))
	nanos := new(big.Int).Quo(exact.Num(), exact.Denom()) // truncate toward zero
	if !nanos.IsInt64() {
		return 0, microsecondRangeError()
	}
	ns := nanos.Int64()
	if ns < 0 || ns >= nanosPerSecond {
		return 0, microsecondRangeError()
	}
	return int(ns), nil
}

func microsecondRangeError() error {
	return fmt.Errorf("Time constructor microsecond argument out of range (must be within one second)")
}

// TimeFromEpoch converts a numeric epoch value into a time.Time anchored
// to the supplied (or local) location.
func TimeFromEpoch(val Value, loc *time.Location) (time.Time, error) {
	return TimeFromEpochParts(val, nil, nil, loc)
}

// subsecUnitNanos maps the unit symbols accepted by Time.at's three-argument
// form to the number of nanoseconds each subsecond unit represents. Ruby spells
// these as the symbols :microsecond/:usec, :millisecond, and :nanosecond/:nsec.
var subsecUnitNanos = map[string]int64{
	"microsecond": 1_000,
	"usec":        1_000,
	"millisecond": 1_000_000,
	"nanosecond":  1,
	"nsec":        1,
}

// TimeFromEpochParts converts Ruby-style Time.at arguments into a time.Time
// anchored to the supplied (or local) location.
//
// The seconds argument may be an integer or float. The optional subsec argument
// adds a subsecond offset whose unit defaults to microseconds and may be
// overridden by the optional unit symbol (:microsecond/:usec, :millisecond, or
// :nanosecond/:nsec). Pass a nil pointer for subsec and/or unit when they are
// absent. A non-nil pointer to a nil Value represents a subsecond or unit that
// was explicitly supplied as nil, which Ruby rejects (Time.at does not treat an
// explicit nil subsecond as omitted the way the calendar constructors do).
//
// The result is backed by time.Time, which has nanosecond resolution, so
// fractional nanoseconds (for example a float subsecond value) are truncated
// toward zero rather than retained as Ruby's arbitrary-precision rationals do.
func TimeFromEpochParts(secVal Value, subsecVal, unitVal *Value, loc *time.Location) (time.Time, error) {
	seconds, nanos, err := epochSecondsToParts(secVal)
	if err != nil {
		return time.Time{}, err
	}

	if subsecVal != nil {
		unitNanos := int64(1_000)
		if unitVal != nil {
			if unitVal.Kind() != KindSymbol {
				return time.Time{}, fmt.Errorf("unexpected unit: %s", unitVal.String())
			}
			factor, ok := subsecUnitNanos[unitVal.String()]
			if !ok {
				return time.Time{}, fmt.Errorf("unexpected unit: %s", unitVal.String())
			}
			unitNanos = factor
		}

		subNanos, err := subsecToNanos(*subsecVal, unitNanos)
		if err != nil {
			return time.Time{}, err
		}
		total, ok := addInt64Checked(nanos, subNanos)
		if !ok {
			return time.Time{}, subsecondOverflowError()
		}
		nanos = total
	} else if unitVal != nil {
		return time.Time{}, fmt.Errorf("Time.at expects a subsecond value before a unit")
	}

	seconds, nanos, err = normalizeUnixParts(seconds, nanos)
	if err != nil {
		return time.Time{}, err
	}

	if loc == nil {
		loc = time.Local
	}
	return time.Unix(seconds, nanos).In(loc), nil
}

// normalizeUnixParts carries whole seconds out of the nanosecond component so
// nanos lands in [0, 1_000_000_000) before reaching time.Unix. time.Unix accepts
// an out-of-range nanos and normalizes it itself, but that normalization wraps
// silently when the carry overflows the int64 seconds -- for example
// Time.at(math.MaxInt64, 1_000_000) carries one whole second and would wrap the
// seconds to math.MinInt64. Performing the carry here with checked addition
// surfaces that overflow as an error instead.
func normalizeUnixParts(seconds, nanos int64) (int64, int64, error) {
	carry := nanos / nanosPerSecond
	nanos -= carry * nanosPerSecond
	if nanos < 0 {
		// Borrow a second so the nanosecond remainder is non-negative, matching
		// time.Unix's normalization.
		nanos += nanosPerSecond
		carry--
	}
	seconds, ok := addInt64Checked(seconds, carry)
	if !ok {
		return 0, 0, subsecondOverflowError()
	}
	return seconds, nanos, nil
}

// epochSecondsToParts decomposes the seconds argument of Time.at into whole
// seconds and a nanosecond remainder. Float seconds carry their fractional part
// into the nanosecond component.
func epochSecondsToParts(val Value) (seconds, nanos int64, err error) {
	switch val.Kind() {
	case KindInt:
		return val.Int(), 0, nil
	case KindFloat:
		f := val.Float()
		// Reject non-finite epochs: int64() of Infinity/NaN is
		// implementation-specific and would silently create a bogus time.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, 0, fmt.Errorf("Time.at expects a finite numeric epoch")
		}
		whole := int64(f)
		return whole, int64((f - float64(whole)) * 1e9), nil
	default:
		return 0, 0, fmt.Errorf("Time.at expects numeric seconds")
	}
}

// subsecToNanos converts a subsecond value expressed in units of unitNanos
// nanoseconds into a nanosecond count. Float subsecond values are floored to
// whole nanoseconds, matching the way Ruby exposes a fractional subsecond
// offset (see below).
//
// Unlike Ruby's Time.at, which carries an arbitrarily large subsecond argument
// into the seconds via arbitrary-precision arithmetic, the result here is bound
// by time.Time's int64 nanosecond resolution. A subsecond magnitude whose scaled
// nanosecond count would not fit in an int64 is rejected rather than silently
// wrapped into a bogus instant.
func subsecToNanos(val Value, unitNanos int64) (int64, error) {
	switch val.Kind() {
	case KindInt:
		nanos, ok := mulInt64Checked(val.Int(), unitNanos)
		if !ok {
			return 0, subsecondOverflowError()
		}
		return nanos, nil
	case KindFloat:
		f := val.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("Time.at expects a finite subsecond value")
		}
		// Scale and floor against the float's exact binary value via a
		// rational. Multiplying in float64 can round the product before
		// integer conversion -- e.g. the float 0.29 is 0.28999...998, but
		// 0.29 * 1000.0 rounds back to exactly 290.0 -- which would flip the
		// nanosecond count. The exact rational keeps representation error from
		// deciding the result.
		//
		// Ruby keeps the rational offset and floors the resulting instant when
		// exposing nanoseconds, so a negative fractional offset rounds toward
		// negative infinity rather than toward zero: Time.at(0, -0.29, :usec)
		// floors -289.99...98 ns to -290 ns (nsec 999999710), and
		// Time.at(0, 0.29, :usec) floors 289.99...98 ns to 289 ns. Because the
		// whole-second and integer-nanosecond epoch parts are added afterward
		// as exact integers, flooring this rational is equivalent to flooring
		// the combined instant. Use Div (floored, Euclidean) rather than Quo
		// (truncated toward zero); unitNanos is always positive.
		exact := new(big.Rat).SetFloat64(f)
		if exact == nil {
			return 0, fmt.Errorf("Time.at expects a finite subsecond value")
		}
		exact.Mul(exact, new(big.Rat).SetInt64(unitNanos))
		nanos := new(big.Int).Div(exact.Num(), exact.Denom()) // floor toward negative infinity
		if !nanos.IsInt64() {
			return 0, subsecondOverflowError()
		}
		return nanos.Int64(), nil
	default:
		return 0, fmt.Errorf("Time.at subsecond value must be numeric")
	}
}

// subsecondOverflowError is returned when a Time.at subsecond argument is too
// large (in magnitude) to express within time.Time's int64 nanosecond range.
func subsecondOverflowError() error {
	return fmt.Errorf("Time.at subsecond value out of range")
}

// ParseTimeString parses a time string, optionally using a caller-supplied
// layout. When hasLayout is false the default layouts are tried in order.
func ParseTimeString(input, layout string, hasLayout bool, loc *time.Location) (time.Time, error) {
	parseLoc := time.Local
	if loc != nil {
		parseLoc = loc
	}

	if hasLayout {
		parsed, err := time.ParseInLocation(layout, input, parseLoc)
		if err != nil {
			return time.Time{}, fmt.Errorf("Time.parse could not parse time: %w", err)
		}
		return resolveParsedTime(parsed, input, loc), nil
	}

	for _, candidate := range DefaultTimeParseLayouts {
		var (
			parsed time.Time
			err    error
		)
		switch candidate {
		case time.RFC3339, time.RFC3339Nano:
			parsed, err = time.Parse(candidate, input)
		default:
			parsed, err = time.ParseInLocation(candidate, input, parseLoc)
		}
		if err == nil {
			return resolveParsedTime(parsed, input, loc), nil
		}
	}

	return time.Time{}, fmt.Errorf("Time.parse could not parse time")
}

// resolveParsedTime applies the caller's requested location override and, when
// none is given, preserves a negative-zero zone read from the input. Go's
// time.Parse normalizes a trailing "-00:00"/"-0000" offset to a nameless
// zero-offset zone, dropping the leading "-" that Ruby reads as the RFC 2822
// unknown-zone marker. When the input carries that token, the result is anchored
// to the canonical FixedZone("-00:00", 0) ParseLocationString produces so
// serializers like Time#rfc2822 emit "-0000" rather than "+0000". An explicit
// `in:` location override takes precedence and suppresses this inference.
func resolveParsedTime(parsed time.Time, input string, loc *time.Location) time.Time {
	if loc != nil {
		return parsed.In(loc)
	}
	if _, offset := parsed.Zone(); offset == 0 && hasNegativeZeroOffsetToken(input) {
		return parsed.In(negativeZeroZone)
	}
	return parsed
}

// negativeZeroZone is the canonical zero-offset location whose name preserves
// the negative sign, matching ParseLocationString("-00:00"). Its name lets the
// RFC 2822 serializer recognize the unknown-zone marker.
var negativeZeroZone = time.FixedZone("-00:00", 0)

// hasNegativeZeroOffsetToken reports whether input ends with an RFC 3339
// "-00:00" or RFC 1123Z "-0000" zone offset: a minus sign followed by all-zero
// hour and minute digits. A trailing "Z" or "+00:00" is the genuine-UTC case
// and is intentionally excluded.
func hasNegativeZeroOffsetToken(input string) bool {
	switch {
	case strings.HasSuffix(input, "-00:00"):
		return true
	case strings.HasSuffix(input, "-0000"):
		return true
	default:
		return false
	}
}
