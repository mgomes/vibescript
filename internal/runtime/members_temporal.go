package runtime

import (
	"fmt"
	"strings"
	"time"
)

// The *MemberNames lists below mirror the names dispatched by the member
// functions next to them and feed "did you mean" suggestions on the error
// path. Keep each list in sync with its switch;
// TestMemberSuggestionCandidatesResolve enforces that every listed name
// resolves. "strftime" is deliberately absent from timeMemberNames because
// it dispatches to an unsupported-method error.
var (
	durationMemberNames = []string{
		"seconds", "second", "minutes", "minute", "hours", "hour", "days", "day", "weeks", "week",
		"in_seconds", "in_minutes", "in_hours", "in_days", "in_weeks", "in_months", "in_years",
		"iso8601", "parts", "to_i", "to_s", "format", "eql?",
		"after", "since", "from_now", "ago", "before", "until",
	}
	timeMemberNames = []string{
		"year", "month", "mon", "mday", "day", "hour", "min", "sec", "usec", "tv_usec", "nsec", "tv_nsec", "subsec",
		"wday", "yday", "hash", "utc_offset", "gmt_offset", "gmtoff", "to_f", "to_i", "tv_sec", "to_r", "zone",
		"utc?", "gmt?", "dst?", "isdst",
		"sunday?", "monday?", "tuesday?", "wednesday?", "thursday?", "friday?", "saturday?",
		"<=>", "eql?", "to_s", "to_a", "iso8601", "xmlschema", "rfc3339", "httpdate", "rfc2822", "rfc822", "format",
		"getutc", "getgm", "getlocal", "utc", "gmtime", "localtime", "round", "ceil", "floor",
	}
)

// rejectTemporalKwargs reports an error when a temporal builtin receives any
// keyword arguments. None of the Duration/Time members accept keywords, so a
// non-empty kwargs map signals a misspelled or unsupported call (for example
// t.iso8601(ndigits: 3)) that should surface rather than be silently dropped.
func rejectTemporalKwargs(method string, kwargs map[string]Value) error {
	if len(kwargs) == 0 {
		return nil
	}
	return fmt.Errorf("%s does not accept keyword arguments", method)
}

func durationMember(d Duration, property string, pos Position) (Value, error) {
	switch property {
	case "seconds", "second":
		return NewInt(d.Seconds()), nil
	case "minutes", "minute":
		return NewInt(d.Seconds() / 60), nil
	case "hours", "hour":
		return NewInt(d.Seconds() / 3600), nil
	case "days", "day":
		return NewInt(d.Seconds() / 86400), nil
	case "weeks", "week":
		return NewInt(d.Seconds() / 604800), nil
	case "in_seconds":
		return NewFloat(float64(d.Seconds())), nil
	case "in_minutes":
		return NewFloat(float64(d.Seconds()) / 60), nil
	case "in_hours":
		return NewFloat(float64(d.Seconds()) / 3600), nil
	case "in_days":
		return NewFloat(float64(d.Seconds()) / 86400), nil
	case "in_weeks":
		return NewFloat(float64(d.Seconds()) / 604800), nil
	case "in_months":
		return NewFloat(float64(d.Seconds()) / (30 * 86400)), nil
	case "in_years":
		return NewFloat(float64(d.Seconds()) / (365 * 86400)), nil
	case "iso8601":
		return NewString(d.ISO8601()), nil
	case "parts":
		p := d.Parts()
		return NewHash(map[string]Value{
			"days":    NewInt(p["days"]),
			"hours":   NewInt(p["hours"]),
			"minutes": NewInt(p["minutes"]),
			"seconds": NewInt(p["seconds"]),
		}), nil
	case "to_i":
		return NewInt(d.Seconds()), nil
	case "to_s":
		return NewString(d.String()), nil
	case "format":
		return NewString(d.String()), nil
	case "eql?":
		return NewBuiltin("duration.eql?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callDurationEql(d, args, kwargs)
		}), nil
	case "after", "since", "from_now":
		return NewBuiltin("duration.after", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callDurationAfter(d, args, kwargs)
		}), nil
	case "ago", "before", "until":
		return NewBuiltin("duration.before", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callDurationBefore(d, args, kwargs)
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown duration method %s%s", property, didYouMean(property, durationMemberNames))
	}
}

func canCallDurationMemberDirect(property string) bool {
	switch property {
	case "eql?", "after", "since", "from_now", "ago", "before", "until":
		return true
	default:
		return false
	}
}

func callDurationMemberDirect(d Duration, property string, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	switch property {
	case "eql?":
		return callDurationEql(d, args, kwargs)
	case "after", "since", "from_now":
		return callDurationAfter(d, args, kwargs)
	case "ago", "before", "until":
		return callDurationBefore(d, args, kwargs)
	default:
		return NewNil(), fmt.Errorf("unknown duration method %s%s", property, didYouMean(property, durationMemberNames))
	}
}

func callDurationEql(d Duration, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("duration.eql?", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) != 1 || args[0].Kind() != KindDuration {
		return NewNil(), fmt.Errorf("duration.eql? expects a duration")
	}
	return NewBool(d.Seconds() == args[0].Duration().Seconds()), nil
}

func callDurationAfter(d Duration, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("duration.after", kwargs); err != nil {
		return NewNil(), err
	}
	start, err := durationTimeArg(args, true, "after")
	if err != nil {
		return NewNil(), err
	}
	result := start.Add(time.Duration(d.Seconds()) * time.Second).UTC()
	return NewTime(result), nil
}

func callDurationBefore(d Duration, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("duration.before", kwargs); err != nil {
		return NewNil(), err
	}
	start, err := durationTimeArg(args, true, "before")
	if err != nil {
		return NewNil(), err
	}
	result := start.Add(-time.Duration(d.Seconds()) * time.Second).UTC()
	return NewTime(result), nil
}

func durationTimeArg(args []Value, allowEmpty bool, name string) (time.Time, error) {
	if len(args) == 0 {
		if allowEmpty {
			return time.Now().UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%s expects a time argument", name)
	}
	if len(args) != 1 {
		return time.Time{}, fmt.Errorf("%s expects at most one time argument", name)
	}
	val := args[0]
	switch val.Kind() {
	case KindString:
		t, err := time.Parse(time.RFC3339, val.String())
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time: %w", err)
		}
		return t.UTC(), nil
	case KindTime:
		return val.Time(), nil
	default:
		return time.Time{}, fmt.Errorf("%s expects a Time or RFC3339 string", name)
	}
}

func timeMember(t time.Time, property string) (Value, error) {
	switch property {
	case "year":
		return NewInt(int64(t.Year())), nil
	case "month", "mon":
		return NewInt(int64(t.Month())), nil
	case "mday", "day":
		return NewInt(int64(t.Day())), nil
	case "hour":
		return NewInt(int64(t.Hour())), nil
	case "min":
		return NewInt(int64(t.Minute())), nil
	case "sec":
		return NewInt(int64(t.Second())), nil
	case "usec", "tv_usec":
		return NewInt(int64(t.Nanosecond() / 1000)), nil
	case "nsec", "tv_nsec":
		return NewInt(int64(t.Nanosecond())), nil
	case "subsec":
		return NewFloat(float64(t.Nanosecond()) / 1e9), nil
	case "wday":
		return NewInt(int64(t.Weekday())), nil
	case "yday":
		return NewInt(int64(t.YearDay())), nil
	case "hash":
		return NewInt(t.UnixNano()), nil
	case "utc_offset", "gmt_offset", "gmtoff":
		_, offset := t.Zone()
		return NewInt(int64(offset)), nil
	case "to_f":
		return NewFloat(float64(t.Unix()) + float64(t.Nanosecond())/1e9), nil
	case "to_i", "tv_sec":
		return NewInt(t.Unix()), nil
	case "to_r":
		return NewFloat(float64(t.Unix()) + float64(t.Nanosecond())/1e9), nil
	case "zone":
		name, _ := t.Zone()
		return NewString(name), nil
	case "utc?", "gmt?":
		return NewBool(t.Location() == time.UTC || t.Format("-0700") == "+0000"), nil
	case "dst?", "isdst":
		return NewBool(t.IsDST()), nil
	case "sunday?":
		return NewBool(t.Weekday() == time.Sunday), nil
	case "monday?":
		return NewBool(t.Weekday() == time.Monday), nil
	case "tuesday?":
		return NewBool(t.Weekday() == time.Tuesday), nil
	case "wednesday?":
		return NewBool(t.Weekday() == time.Wednesday), nil
	case "thursday?":
		return NewBool(t.Weekday() == time.Thursday), nil
	case "friday?":
		return NewBool(t.Weekday() == time.Friday), nil
	case "saturday?":
		return NewBool(t.Weekday() == time.Saturday), nil
	case "<=>":
		return NewBuiltin("time.cmp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeCompare(t, args, kwargs)
		}), nil
	case "eql?":
		return NewBuiltin("time.eql?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeEql(t, args, kwargs)
		}), nil
	case "to_s":
		return NewString(t.Format(time.RFC3339Nano)), nil
	case "to_a":
		return timeToArray(t), nil
	case "iso8601", "xmlschema", "rfc3339":
		return NewAutoBuiltin("time."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeISO8601("time."+property, t, args, kwargs)
		}), nil
	case "httpdate":
		return NewAutoBuiltin("time.httpdate", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeHTTPDate(t, args, kwargs)
		}), nil
	case "rfc2822", "rfc822":
		return NewAutoBuiltin("time."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeRFC2822("time."+property, t, args, kwargs)
		}), nil
	case "format":
		return NewBuiltin("time.format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeFormat(t, args, kwargs)
		}), nil
	case "strftime":
		return NewNil(), fmt.Errorf("strftime is not supported; use format with Go layouts instead")
	case "getutc", "getgm":
		return NewTime(t.UTC()), nil
	case "getlocal", "localtime":
		return NewAutoBuiltin("time."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeGetlocal(t, property, args, kwargs)
		}), nil
	case "utc", "gmtime":
		return NewTime(t.UTC()), nil
	case "round":
		return NewAutoBuiltin("time.round", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeRound(t, args, kwargs)
		}), nil
	case "ceil":
		return NewAutoBuiltin("time.ceil", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeCeil(t, args, kwargs)
		}), nil
	case "floor":
		return NewAutoBuiltin("time.floor", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeFloor(t, args, kwargs)
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown time method %s%s", property, didYouMean(property, timeMemberNames))
	}
}

func canCallTimeMemberDirect(property string) bool {
	switch property {
	case "<=>", "eql?", "format", "iso8601", "xmlschema", "rfc3339", "httpdate", "rfc2822", "rfc822", "round", "ceil", "floor", "getlocal", "localtime":
		return true
	default:
		return false
	}
}

func callTimeMemberDirect(t time.Time, property string, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	switch property {
	case "<=>":
		return callTimeCompare(t, args, kwargs)
	case "eql?":
		return callTimeEql(t, args, kwargs)
	case "format":
		return callTimeFormat(t, args, kwargs)
	case "iso8601", "xmlschema", "rfc3339":
		return callTimeISO8601("time."+property, t, args, kwargs)
	case "httpdate":
		return callTimeHTTPDate(t, args, kwargs)
	case "rfc2822", "rfc822":
		return callTimeRFC2822("time."+property, t, args, kwargs)
	case "round":
		return callTimeRound(t, args, kwargs)
	case "ceil":
		return callTimeCeil(t, args, kwargs)
	case "floor":
		return callTimeFloor(t, args, kwargs)
	case "getlocal", "localtime":
		return callTimeGetlocal(t, property, args, kwargs)
	default:
		return NewNil(), fmt.Errorf("unknown time method %s%s", property, didYouMean(property, timeMemberNames))
	}
}

func callTimeCompare(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("time.<=>", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) != 1 || args[0].Kind() != KindTime {
		return NewNil(), fmt.Errorf("time comparison expects another Time")
	}
	other := args[0].Time()
	switch {
	case t.Before(other):
		return NewInt(-1), nil
	case t.After(other):
		return NewInt(1), nil
	default:
		return NewInt(0), nil
	}
}

func callTimeEql(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("time.eql?", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) != 1 || args[0].Kind() != KindTime {
		return NewNil(), fmt.Errorf("time.eql? expects a Time")
	}
	return NewBool(t.Equal(args[0].Time())), nil
}

// timeToArray builds the Ruby Time#to_a positional tuple:
// [sec, min, hour, mday, month, year, wday, yday, isdst, zone].
// Field values mirror the individual Time accessors so UTC, local, and
// offset receivers stay consistent across both forms.
func timeToArray(t time.Time) Value {
	name, _ := t.Zone()
	return NewArray([]Value{
		NewInt(int64(t.Second())),
		NewInt(int64(t.Minute())),
		NewInt(int64(t.Hour())),
		NewInt(int64(t.Day())),
		NewInt(int64(t.Month())),
		NewInt(int64(t.Year())),
		NewInt(int64(t.Weekday())),
		NewInt(int64(t.YearDay())),
		NewBool(t.IsDST()),
		NewString(name),
	})
}

func callTimeFormat(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("time.format", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) != 1 || args[0].Kind() != KindString {
		return NewNil(), fmt.Errorf("format expects a Go layout string")
	}
	return NewString(t.Format(args[0].String())), nil
}

// callTimeISO8601 renders an RFC3339/ISO8601 timestamp. With no argument it
// emits whole-second precision; a non-negative ndigits argument appends that
// many fractional-second digits, truncating toward zero like Ruby's
// Time#iso8601(ndigits).
func callTimeISO8601(method string, t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	ndigits, err := timeISO8601Precision(method, args, kwargs)
	if err != nil {
		return NewNil(), err
	}
	return NewString(formatTimeISO8601(t, ndigits)), nil
}

// timeISO8601Precision resolves the optional ndigits argument (defaulting to 0)
// into a fractional-second digit count, rejecting keyword arguments as well as
// non-integer or negative precision so misuse surfaces a clear error rather
// than silently coercing or dropping the requested precision.
func timeISO8601Precision(method string, args []Value, kwargs map[string]Value) (int, error) {
	if err := rejectTemporalKwargs(method, kwargs); err != nil {
		return 0, err
	}
	if len(args) == 0 {
		return 0, nil
	}
	if len(args) > 1 {
		return 0, fmt.Errorf("%s expects at most one precision argument", method)
	}
	arg := args[0]
	if arg.Kind() != KindInt {
		return 0, fmt.Errorf("%s precision must be an Integer", method)
	}
	ndigits := arg.Int()
	if ndigits < 0 {
		return 0, fmt.Errorf("%s precision must be non-negative", method)
	}
	if ndigits > maxISO8601FractionDigits {
		return 0, fmt.Errorf("%s precision exceeds maximum %d digits", method, maxISO8601FractionDigits)
	}
	return int(ndigits), nil
}

// formatTimeISO8601 formats t as RFC3339 with ndigits fractional-second digits.
// Zero digits omit the fractional part entirely; digits beyond nanosecond
// resolution are zero-padded since the underlying clock cannot represent them.
func formatTimeISO8601(t time.Time, ndigits int) string {
	if ndigits <= 0 {
		return t.Format(time.RFC3339)
	}
	digits := ndigits
	if digits > maxTimePrecisionDigits {
		digits = maxTimePrecisionDigits
	}
	out := t.Format("2006-01-02T15:04:05." + strings.Repeat("0", digits) + "Z07:00")
	if ndigits <= maxTimePrecisionDigits {
		return out
	}
	// The fractional block sits immediately after the seconds' decimal point;
	// pad the sub-nanosecond positions with zeros before the trailing offset.
	insertAt := strings.IndexByte(out, '.') + 1 + maxTimePrecisionDigits
	return out[:insertAt] + strings.Repeat("0", ndigits-maxTimePrecisionDigits) + out[insertAt:]
}

// httpDateLayout is the IMF-fixdate form mandated by RFC 7231 for the
// HTTP-date production: a fixed-width English weekday and month with a literal
// GMT zone. callTimeHTTPDate always renders in UTC, matching Ruby's
// Time#httpdate.
const httpDateLayout = "Mon, 02 Jan 2006 15:04:05 GMT"

// rfc2822Layout is the RFC 2822 mail-date form Ruby emits for Time#rfc2822 and
// its Time#rfc822 alias: a fixed-width English weekday and month with a numeric
// zone offset. The zone is rendered separately so genuine UTC times can use the
// "-0000" form Ruby reserves for timestamps without real zone information.
const rfc2822Layout = "Mon, 02 Jan 2006 15:04:05"

// callTimeHTTPDate renders the receiver as an HTTP-date (IMF-fixdate), always
// in GMT/UTC like Ruby's Time#httpdate. The method takes no arguments, so any
// positional or keyword argument is rejected. Sub-second precision is dropped
// because the HTTP-date grammar has whole-second resolution.
func callTimeHTTPDate(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("time.httpdate", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("time.httpdate does not accept arguments")
	}
	return NewString(t.UTC().Format(httpDateLayout)), nil
}

// callTimeRFC2822 renders the receiver as an RFC 2822 mail date, preserving the
// receiver's zone offset like Ruby's Time#rfc2822 and Time#rfc822. Genuine UTC
// receivers use the "-0000" zone Ruby reserves for timestamps without real zone
// information, while explicit zero offsets and other zones render their signed
// numeric offset. The method takes no arguments, so any positional or keyword
// argument is rejected. Sub-second precision is dropped because the RFC 2822
// date grammar has whole-second resolution.
func callTimeRFC2822(method string, t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs(method, kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) != 0 {
		return NewNil(), fmt.Errorf("%s does not accept arguments", method)
	}
	zone := t.Format("-0700")
	if isUTCZone(t) {
		zone = "-0000"
	}
	return NewString(t.Format(rfc2822Layout) + " " + zone), nil
}

// isUTCZone reports whether t is anchored to a canonical UTC zone, which is the
// condition under which Ruby's Time#rfc2822 emits the "-0000" unknown-zone
// marker instead of a signed numeric offset. A zone qualifies when its offset
// is zero and it is either the time.UTC singleton or a zone explicitly named
// "UTC". Explicit numeric zero offsets (named "-00:00"/"+00:00", as produced by
// Time.new or getlocal) and named zones such as "GMT" do not qualify: Ruby
// renders those as "+0000", matching its rule that "-0000" is reserved for
// timestamps created in UTC mode rather than any zero-offset zone. Inspecting
// the zone name rather than only the location pointer keeps the decision robust
// against UTC zones that are not the time.UTC singleton.
func isUTCZone(t time.Time) bool {
	if t.Location() == time.UTC {
		return true
	}
	name, offset := t.Zone()
	return offset == 0 && name == "UTC"
}

// callTimeGetlocal implements Ruby's non-mutating Time#getlocal and
// Time#localtime. With no argument it converts the receiver to the host's
// local zone; with a timezone-offset argument (e.g. "+05:30" or "-04:00") it
// converts to that fixed-offset zone using the shared location parser. The
// underlying instant is preserved, only the displayed zone changes. localtime
// is reconciled with Vibescript's immutable value model by returning a new
// Time rather than mutating the receiver, matching getlocal.
func callTimeGetlocal(t time.Time, method string, args []Value, kwargs map[string]Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not take keyword arguments; pass the offset positionally", method)
	}
	if len(args) == 0 {
		return NewTime(t.In(time.Local)), nil
	}
	if len(args) > 1 {
		return NewNil(), fmt.Errorf("%s expects at most one timezone offset argument", method)
	}
	loc, err := parseLocation(args[0])
	if err != nil {
		return NewNil(), err
	}
	if loc == nil {
		loc = time.Local
	}
	return NewTime(t.In(loc)), nil
}

// maxTimePrecisionDigits is the most fractional-second digits Time can
// represent, matching the nanosecond resolution of the underlying clock.
const maxTimePrecisionDigits = 9

// maxISO8601FractionDigits bounds the fractional-second digits Time#iso8601 will
// render. Digits beyond nanosecond resolution are pure zero padding, so the
// limit blocks pathologically large precision values from forcing huge string
// allocations while staying well above any realistic precision request.
const maxISO8601FractionDigits = 100

func callTimeRound(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	unit, err := timeRoundingUnit("time.round", args, kwargs)
	if err != nil {
		return NewNil(), err
	}
	return NewTime(t.Round(unit)), nil
}

func callTimeCeil(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("time.ceil", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("ceil does not accept precision")
	}
	rounded := t.Round(time.Second)
	if rounded.Before(t) {
		rounded = rounded.Add(time.Second)
	}
	return NewTime(rounded), nil
}

func callTimeFloor(t time.Time, args []Value, kwargs map[string]Value) (Value, error) {
	if err := rejectTemporalKwargs("time.floor", kwargs); err != nil {
		return NewNil(), err
	}
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("floor does not accept precision")
	}
	return NewTime(t.Truncate(time.Second)), nil
}

// timeRoundingUnit resolves the optional Ruby-style precision argument
// (ndigits, defaulting to 0) into the duration unit to round toward. With no
// argument or 0 it rounds to whole seconds; positive ndigits round to that
// many fractional-second digits, capped at nanosecond resolution. Keyword
// arguments are rejected since the method only accepts a positional ndigits.
func timeRoundingUnit(method string, args []Value, kwargs map[string]Value) (time.Duration, error) {
	if err := rejectTemporalKwargs(method, kwargs); err != nil {
		return 0, err
	}
	if len(args) == 0 {
		return time.Second, nil
	}
	if len(args) > 1 {
		return 0, fmt.Errorf("%s expects at most one precision argument", method)
	}
	arg := args[0]
	if arg.Kind() != KindInt {
		return 0, fmt.Errorf("%s precision must be an Integer", method)
	}
	ndigits := arg.Int()
	if ndigits < 0 {
		return 0, fmt.Errorf("%s precision must be non-negative", method)
	}
	if ndigits >= maxTimePrecisionDigits {
		return time.Nanosecond, nil
	}
	unit := time.Second
	for range ndigits {
		unit /= 10
	}
	return unit, nil
}
