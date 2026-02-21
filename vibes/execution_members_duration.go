package vibes

import (
	"fmt"
	"time"
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
		return NewString(d.iso8601()), nil
	case "parts":
		p := d.parts()
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
			if len(args) != 1 || args[0].Kind() != KindDuration {
				return NewNil(), fmt.Errorf("duration.eql? expects a duration")
			}
			return NewBool(d.Seconds() == args[0].Duration().Seconds()), nil
		}), nil
	case "after", "since", "from_now":
		return NewBuiltin("duration.after", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			start, err := durationTimeArg(args, true, "after")
			if err != nil {
				return NewNil(), err
			}
			result := start.Add(time.Duration(d.Seconds()) * time.Second).UTC()
			return NewTime(result), nil
		}), nil
	case "ago", "before", "until":
		return NewBuiltin("duration.before", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			start, err := durationTimeArg(args, true, "before")
			if err != nil {
				return NewNil(), err
			}
			result := start.Add(-time.Duration(d.Seconds()) * time.Second).UTC()
			return NewTime(result), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown duration method %s", property)
	}
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
			return time.Time{}, fmt.Errorf("invalid time: %v", err)
		}
		return t.UTC(), nil
	case KindTime:
		return val.Time(), nil
	default:
		return time.Time{}, fmt.Errorf("%s expects a Time or RFC3339 string", name)
	}
}
