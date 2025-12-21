package vibes

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parseLocation(val Value) (*time.Location, error) {
	switch val.Kind() {
	case KindString:
		return parseLocationString(val.String())
	case KindNil:
		return nil, nil
	default:
		return nil, fmt.Errorf("invalid timezone spec")
	}
}

func parseLocationString(spec string) (*time.Location, error) {
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

func timeFromParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	if len(args) < 3 {
		return time.Time{}, fmt.Errorf("Time.new expects at least year, month, day") //nolint:staticcheck // class.method reference
	}
	getInt := func(idx int) (int, error) {
		if idx >= len(args) {
			return 0, nil
		}
		return int(args[idx].Int()), nil
	}

	year, _ := getInt(0)
	month, _ := getInt(1)
	day, _ := getInt(2)
	hour, _ := getInt(3)
	min, _ := getInt(4)
	sec, _ := getInt(5)

	loc := defaultLoc
	if len(args) >= 7 {
		locVal := args[6]
		parsed, err := parseLocation(locVal)
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

func timeFromEpoch(val Value, loc *time.Location) (time.Time, error) {
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
		return time.Time{}, fmt.Errorf("Time.at expects numeric seconds") //nolint:staticcheck // class.method reference
	}
	if loc == nil {
		loc = time.Local
	}
	return time.Unix(seconds, nanos).In(loc), nil
}
