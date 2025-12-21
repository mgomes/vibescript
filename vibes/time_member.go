package vibes

import (
	"fmt"
	"time"
)

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
		}), nil
	case "eql?":
		return NewBuiltin("time.eql?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindTime {
				return NewNil(), fmt.Errorf("time.eql? expects a Time")
			}
			return NewBool(t.Equal(args[0].Time())), nil
		}), nil
	case "to_s":
		return NewString(t.Format(time.RFC3339Nano)), nil
	case "strftime":
		return NewBuiltin("time.strftime", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("strftime expects a format string")
			}
			return NewString(t.Format(args[0].String())), nil
		}), nil
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
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("round does not accept precision")
			}
			return NewTime(t.Round(time.Second)), nil
		}), nil
	case "ceil":
		return NewAutoBuiltin("time.ceil", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("ceil does not accept precision")
			}
			rounded := t.Round(time.Second)
			if rounded.Before(t) {
				rounded = rounded.Add(time.Second)
			}
			return NewTime(rounded), nil
		}), nil
	case "floor":
		return NewAutoBuiltin("time.floor", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("floor does not accept precision")
			}
			return NewTime(t.Truncate(time.Second)), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown time method %s", property)
	}
}
