package runtime

import (
	"fmt"
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
		"<=>", "eql?", "to_s", "iso8601", "rfc3339", "format",
		"getutc", "getgm", "getlocal", "utc", "gmtime", "localtime", "round", "ceil", "floor",
	}
)

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
			return callDurationEql(d, args)
		}), nil
	case "after", "since", "from_now":
		return NewBuiltin("duration.after", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callDurationAfter(d, args)
		}), nil
	case "ago", "before", "until":
		return NewBuiltin("duration.before", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callDurationBefore(d, args)
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
		return callDurationEql(d, args)
	case "after", "since", "from_now":
		return callDurationAfter(d, args)
	case "ago", "before", "until":
		return callDurationBefore(d, args)
	default:
		return NewNil(), fmt.Errorf("unknown duration method %s%s", property, didYouMean(property, durationMemberNames))
	}
}

func callDurationEql(d Duration, args []Value) (Value, error) {
	if len(args) != 1 || args[0].Kind() != KindDuration {
		return NewNil(), fmt.Errorf("duration.eql? expects a duration")
	}
	return NewBool(d.Seconds() == args[0].Duration().Seconds()), nil
}

func callDurationAfter(d Duration, args []Value) (Value, error) {
	start, err := durationTimeArg(args, true, "after")
	if err != nil {
		return NewNil(), err
	}
	result := start.Add(time.Duration(d.Seconds()) * time.Second).UTC()
	return NewTime(result), nil
}

func callDurationBefore(d Duration, args []Value) (Value, error) {
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
			return callTimeCompare(t, args)
		}), nil
	case "eql?":
		return NewBuiltin("time.eql?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeEql(t, args)
		}), nil
	case "to_s":
		return NewString(t.Format(time.RFC3339Nano)), nil
	case "iso8601", "rfc3339":
		return NewString(t.Format(time.RFC3339)), nil
	case "format":
		return NewBuiltin("time.format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeFormat(t, args)
		}), nil
	case "strftime":
		return NewNil(), fmt.Errorf("strftime is not supported; use format with Go layouts instead")
	case "getutc", "getgm":
		return NewTime(t.UTC()), nil
	case "getlocal":
		return NewTime(t.In(time.Local)), nil
	case "utc", "gmtime":
		return NewTime(t.UTC()), nil
	case "localtime":
		return NewTime(t.In(time.Local)), nil
	case "round":
		return NewAutoBuiltin("time.round", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeRound(t, args)
		}), nil
	case "ceil":
		return NewAutoBuiltin("time.ceil", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeCeil(t, args)
		}), nil
	case "floor":
		return NewAutoBuiltin("time.floor", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return callTimeFloor(t, args)
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown time method %s%s", property, didYouMean(property, timeMemberNames))
	}
}

func canCallTimeMemberDirect(property string) bool {
	switch property {
	case "<=>", "eql?", "format", "round", "ceil", "floor":
		return true
	default:
		return false
	}
}

func callTimeMemberDirect(t time.Time, property string, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	switch property {
	case "<=>":
		return callTimeCompare(t, args)
	case "eql?":
		return callTimeEql(t, args)
	case "format":
		return callTimeFormat(t, args)
	case "round":
		return callTimeRound(t, args)
	case "ceil":
		return callTimeCeil(t, args)
	case "floor":
		return callTimeFloor(t, args)
	default:
		return NewNil(), fmt.Errorf("unknown time method %s%s", property, didYouMean(property, timeMemberNames))
	}
}

func callTimeCompare(t time.Time, args []Value) (Value, error) {
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

func callTimeEql(t time.Time, args []Value) (Value, error) {
	if len(args) != 1 || args[0].Kind() != KindTime {
		return NewNil(), fmt.Errorf("time.eql? expects a Time")
	}
	return NewBool(t.Equal(args[0].Time())), nil
}

func callTimeFormat(t time.Time, args []Value) (Value, error) {
	if len(args) != 1 || args[0].Kind() != KindString {
		return NewNil(), fmt.Errorf("format expects a Go layout string")
	}
	return NewString(t.Format(args[0].String())), nil
}

// maxTimePrecisionDigits is the most fractional-second digits Time can
// represent, matching the nanosecond resolution of the underlying clock.
const maxTimePrecisionDigits = 9

func callTimeRound(t time.Time, args []Value) (Value, error) {
	unit, err := timeRoundingUnit("time.round", args)
	if err != nil {
		return NewNil(), err
	}
	return NewTime(t.Round(unit)), nil
}

func callTimeCeil(t time.Time, args []Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("ceil does not accept precision")
	}
	rounded := t.Round(time.Second)
	if rounded.Before(t) {
		rounded = rounded.Add(time.Second)
	}
	return NewTime(rounded), nil
}

func callTimeFloor(t time.Time, args []Value) (Value, error) {
	if len(args) > 0 {
		return NewNil(), fmt.Errorf("floor does not accept precision")
	}
	return NewTime(t.Truncate(time.Second)), nil
}

// timeRoundingUnit resolves the optional Ruby-style precision argument
// (ndigits, defaulting to 0) into the duration unit to round toward. With no
// argument or 0 it rounds to whole seconds; positive ndigits round to that
// many fractional-second digits, capped at nanosecond resolution.
func timeRoundingUnit(method string, args []Value) (time.Duration, error) {
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
