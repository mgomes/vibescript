package vibes

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Duration stores an integer number of seconds for now.
type Duration struct {
	seconds int64
}

func (d Duration) Seconds() int64 { return d.seconds }

func (d Duration) String() string {
	return fmt.Sprintf("%ds", d.seconds)
}

func (d Duration) iso8601() string {
	secs := d.seconds
	if secs == 0 {
		return "PT0S"
	}
	sign := ""
	if secs < 0 {
		sign = "-"
		secs = -secs
	}
	days := secs / 86400
	secs %= 86400
	hours := secs / 3600
	secs %= 3600
	minutes := secs / 60
	secs %= 60

	var b strings.Builder
	b.WriteString(sign)
	b.WriteString("P")
	if days > 0 {
		fmt.Fprintf(&b, "%dD", days)
	}
	if hours > 0 || minutes > 0 || secs > 0 {
		b.WriteString("T")
		if hours > 0 {
			fmt.Fprintf(&b, "%dH", hours)
		}
		if minutes > 0 {
			fmt.Fprintf(&b, "%dM", minutes)
		}
		if secs > 0 {
			fmt.Fprintf(&b, "%dS", secs)
		}
	}
	return b.String()
}

func (d Duration) parts() map[string]int64 {
	secs := d.seconds
	sign := int64(1)
	if secs < 0 {
		sign = -1
		secs = -secs
	}
	days := secs / 86400
	secs %= 86400
	hours := secs / 3600
	secs %= 3600
	minutes := secs / 60
	secs %= 60
	return map[string]int64{
		"days":    days * sign,
		"hours":   hours * sign,
		"minutes": minutes * sign,
		"seconds": secs * sign,
	}
}

func parseDurationString(input string) (Duration, error) {
	if input == "" {
		return Duration{}, fmt.Errorf("empty duration string")
	}

	if dur, err := time.ParseDuration(input); err == nil {
		return Duration{seconds: int64(dur.Seconds())}, nil
	}

	sign := int64(1)
	s := input
	if trimmed, ok := strings.CutPrefix(s, "-"); ok {
		sign = -1
		s = trimmed
	} else if trimmed, ok := strings.CutPrefix(s, "+"); ok {
		s = trimmed
	}

	if !strings.HasPrefix(s, "P") {
		return Duration{}, fmt.Errorf("invalid duration format")
	}
	s = strings.TrimPrefix(s, "P")

	if strings.ContainsRune(s, 'W') {
		if strings.ContainsRune(s, 'T') || strings.ContainsAny(s, "DHMS") {
			return Duration{}, fmt.Errorf("invalid mixed week duration")
		}
		if !strings.HasSuffix(s, "W") {
			return Duration{}, fmt.Errorf("invalid week duration format")
		}
		weeksStr := strings.TrimSuffix(s, "W")
		if weeksStr == "" {
			return Duration{}, fmt.Errorf("invalid week duration format")
		}
		weeks, err := strconv.ParseInt(weeksStr, 10, 64)
		if err != nil {
			return Duration{}, fmt.Errorf("invalid week duration")
		}
		return Duration{seconds: weeks * 7 * 86400 * sign}, nil
	}

	var days, hours, minutes, seconds int64
	var timePart, datePart string
	if idx := strings.IndexRune(s, 'T'); idx != -1 {
		datePart = s[:idx]
		timePart = s[idx+1:]
	} else {
		datePart = s
	}

	parseNumber := func(segment, suffix string) (int64, string, bool) {
		if segment == "" {
			return 0, segment, false
		}
		idx := strings.Index(segment, suffix)
		if idx == -1 {
			return 0, segment, false
		}
		numPart := segment[:idx]
		rest := segment[idx+len(suffix):]
		val, err := strconv.ParseInt(numPart, 10, 64)
		if err != nil {
			return 0, segment, false
		}
		return val, rest, true
	}

	for {
		val, rest, ok := parseNumber(datePart, "D")
		if !ok {
			break
		}
		days += val
		datePart = rest
	}

	for timePart != "" {
		if val, rest, ok := parseNumber(timePart, "H"); ok {
			hours += val
			timePart = rest
			continue
		}
		if val, rest, ok := parseNumber(timePart, "M"); ok {
			minutes += val
			timePart = rest
			continue
		}
		if val, rest, ok := parseNumber(timePart, "S"); ok {
			seconds += val
			timePart = rest
			continue
		}
		break
	}

	if datePart != "" || timePart != "" {
		return Duration{}, fmt.Errorf("invalid duration format")
	}

	total := days*86400 + hours*3600 + minutes*60 + seconds
	return Duration{seconds: total * sign}, nil
}

func numericToSeconds(val Value) (int64, error) {
	switch val.Kind() {
	case KindInt, KindFloat:
		return valueToInt64(val)
	default:
		return 0, fmt.Errorf("duration expects numeric seconds")
	}
}

func durationFromParts(weeks, days, hours, minutes, seconds int64) Duration {
	total := weeks*7*86400 + days*86400 + hours*3600 + minutes*60 + seconds
	return Duration{seconds: total}
}

func secondsDuration(value int64, unit string) Duration {
	factor := map[string]int64{
		"seconds": 1,
		"second":  1,
		"minutes": 60,
		"minute":  60,
		"hours":   3600,
		"hour":    3600,
		"weeks":   604800,
		"week":    604800,
		"days":    86400,
		"day":     86400,
	}[unit]
	if factor == 0 {
		factor = 1
	}
	return Duration{seconds: value * factor}
}
