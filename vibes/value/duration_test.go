package value_test

import (
	"maps"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestDurationSecondsAndString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		seconds int64
		want    string
	}{
		{0, "0s"},
		{90, "90s"},
		{-30, "-30s"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			d := value.DurationFromSeconds(tc.seconds)
			if d.Seconds() != tc.seconds {
				t.Errorf("Seconds() = %d, want %d", d.Seconds(), tc.seconds)
			}
			if got := d.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDurationISO8601(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seconds int64
		want    string
	}{
		{"zero", 0, "PT0S"},
		{"seconds_only", 30, "PT30S"},
		{"minutes_and_seconds", 90, "PT1M30S"},
		{"hours_only", 3600, "PT1H"},
		{"days_only", 86400, "P1D"},
		{"week_renders_as_days", 7 * 86400, "P7D"},
		{"days_and_seconds", 86430, "P1DT30S"},
		{"all_parts", 90061, "P1DT1H1M1S"},
		{"negative", -90, "-PT1M30S"},
		{"negative_days", -86400, "-P1D"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := value.DurationFromSeconds(tc.seconds).ISO8601(); got != tc.want {
				t.Fatalf("ISO8601() for %ds = %q, want %q", tc.seconds, got, tc.want)
			}
		})
	}
}

func TestDurationParts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seconds int64
		want    map[string]int64
	}{
		{
			name:    "zero",
			seconds: 0,
			want:    map[string]int64{"days": 0, "hours": 0, "minutes": 0, "seconds": 0},
		},
		{
			name:    "all_parts",
			seconds: 90061,
			want:    map[string]int64{"days": 1, "hours": 1, "minutes": 1, "seconds": 1},
		},
		{
			name:    "negative_distributes_sign",
			seconds: -3661,
			want:    map[string]int64{"days": 0, "hours": -1, "minutes": -1, "seconds": -1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := value.DurationFromSeconds(tc.seconds).Parts()
			if !maps.Equal(got, tc.want) {
				t.Fatalf("Parts() for %ds = %v, want %v", tc.seconds, got, tc.want)
			}
		})
	}
}

func TestParseDurationString(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			input string
			want  int64
		}{
			{"90s", 90},
			{"1h30m", 5400},
			{"1.5h", 5400},
			{"-2m", -120},
			{"PT1H30M", 5400},
			{"PT1H2M3S", 3723},
			{"P1DT2H", 93600},
			{"P10D", 864000},
			{"P2W", 1209600},
			{"-P1D", -86400},
			{"-PT30S", -30},
			{"+PT10S", 10},
		}
		for _, tc := range tests {
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				got, err := value.ParseDurationString(tc.input)
				if err != nil {
					t.Fatalf("ParseDurationString(%q) error: %v", tc.input, err)
				}
				if got.Seconds() != tc.want {
					t.Fatalf("ParseDurationString(%q) = %ds, want %ds", tc.input, got.Seconds(), tc.want)
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name    string
			input   string
			wantErr string
		}{
			{"empty", "", "empty duration string"},
			{"sub_second_go_format", "500ms", "duration must be whole seconds"},
			{"fractional_seconds", "1.5s", "duration must be whole seconds"},
			{"bare_p", "P", "invalid duration format"},
			{"bare_pt", "PT", "invalid duration format"},
			{"garbage", "soon", "invalid duration format"},
			{"weeks_mixed_with_days", "P1W2D", "invalid mixed week duration"},
			{"weeks_mixed_with_time", "P1WT1H", "invalid mixed week duration"},
			{"week_without_count", "PW", "invalid week duration format"},
			{"week_not_at_end", "P2W3", "invalid week duration format"},
			{"week_count_overflow", "P99999999999999999999W", "invalid week duration"},
			{"unit_without_count", "PD", "invalid duration format"},
			{"count_without_unit", "P5", "invalid duration format"},
			{"day_count_overflow", "P99999999999999999999D", "invalid duration number"},
			{"hours_in_date_part", "P1H", "invalid duration format"},
			{"days_in_time_part", "PT5D", "invalid duration format"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := value.ParseDurationString(tc.input)
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("ParseDurationString(%q) error = %v, want %q", tc.input, err, tc.wantErr)
				}
			})
		}
	})
}

func TestNumericToSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		val     value.Value
		want    int64
		wantErr string
	}{
		{name: "int", val: value.NewInt(90), want: 90},
		{name: "float_truncates", val: value.NewFloat(90.9), want: 90},
		{name: "string", val: value.NewString("90"), wantErr: "duration expects numeric seconds"},
		{name: "nil", val: value.NewNil(), wantErr: "duration expects numeric seconds"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := value.NumericToSeconds(tc.val)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("NumericToSeconds error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NumericToSeconds error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NumericToSeconds = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDurationFromParts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                                 string
		weeks, days, hours, minutes, seconds int64
		want                                 int64
	}{
		{name: "zero", want: 0},
		{name: "all_parts", weeks: 1, days: 2, hours: 3, minutes: 4, seconds: 5, want: 788645},
		{name: "negative_parts_cancel", days: 1, hours: -24, want: 0},
		{name: "seconds_only", seconds: -30, want: -30},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := value.DurationFromParts(tc.weeks, tc.days, tc.hours, tc.minutes, tc.seconds)
			if got.Seconds() != tc.want {
				t.Fatalf("DurationFromParts = %ds, want %ds", got.Seconds(), tc.want)
			}
		})
	}
}

func TestSecondsDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		unit string
		val  int64
		want int64
	}{
		{name: "seconds", unit: "seconds", val: 90, want: 90},
		{name: "second", unit: "second", val: 1, want: 1},
		{name: "minutes", unit: "minutes", val: 2, want: 120},
		{name: "minute", unit: "minute", val: 1, want: 60},
		{name: "hours", unit: "hours", val: 2, want: 7200},
		{name: "hour", unit: "hour", val: 1, want: 3600},
		{name: "days", unit: "days", val: 2, want: 172800},
		{name: "day", unit: "day", val: 1, want: 86400},
		{name: "weeks", unit: "weeks", val: 2, want: 1209600},
		{name: "week", unit: "week", val: 1, want: 604800},
		{name: "unknown_unit_defaults_to_seconds", unit: "fortnights", val: 90, want: 90},
		{name: "negative_value", unit: "minutes", val: -5, want: -300},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := value.SecondsDuration(tc.val, tc.unit); got.Seconds() != tc.want {
				t.Fatalf("SecondsDuration(%d, %q) = %ds, want %ds", tc.val, tc.unit, got.Seconds(), tc.want)
			}
		})
	}
}
