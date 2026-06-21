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

// TimeFromParts constructs a time.Time from year/month/day positional
// arguments, with optional hour/minute/second and timezone arguments.
func TimeFromParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	if len(args) < 3 {
		return time.Time{}, fmt.Errorf("Time.new expects at least year, month, day")
	}
	getInt := func(idx int) int {
		if idx >= len(args) {
			return 0
		}
		return int(args[idx].Int())
	}

	year := getInt(0)
	month := getInt(1)
	day := getInt(2)
	hour := getInt(3)
	min := getInt(4)
	sec := getInt(5)

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

// TimeFromCalendarParts constructs a time.Time from year/month/day positional
// arguments, with optional hour/minute/second and a subsecond argument. Unlike
// TimeFromParts (which backs Time.new and reads the seventh argument as a
// timezone), this matches Ruby's Time.local/mktime/utc/gm where the seventh
// argument is microseconds-with-fraction and the location is fixed by the
// constructor. A nil defaultLoc falls back to the local timezone.
func TimeFromCalendarParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	if len(args) < 3 {
		return time.Time{}, fmt.Errorf("Time constructor expects at least year, month, day")
	}
	if len(args) > 7 {
		return time.Time{}, fmt.Errorf("Time constructor expects at most year, month, day, hour, minute, second, microsecond")
	}

	getInt := func(idx int) int {
		if idx >= len(args) {
			return 0
		}
		return int(args[idx].Int())
	}

	year := getInt(0)
	month := getInt(1)
	day := getInt(2)
	hour := getInt(3)
	min := getInt(4)
	sec := getInt(5)

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
	var seconds int64
	var nanos int64
	switch val.Kind() {
	case KindInt:
		seconds = val.Int()
	case KindFloat:
		f := val.Float()
		seconds = int64(f)
		nanos = int64((f - float64(seconds)) * 1e9)
	default:
		return time.Time{}, fmt.Errorf("Time.at expects numeric seconds")
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Unix(seconds, nanos).In(loc), nil
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
		if loc != nil {
			return parsed.In(loc), nil
		}
		return parsed, nil
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
			if loc != nil {
				return parsed.In(loc), nil
			}
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("Time.parse could not parse time")
}
