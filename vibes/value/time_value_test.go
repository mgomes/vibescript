package value_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestParseLocationString(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name       string
			spec       string
			wantNil    bool
			wantOffset int
			wantLoc    *time.Location
		}{
			{name: "empty_means_unset", spec: "", wantNil: true},
			{name: "utc", spec: "UTC", wantLoc: time.UTC},
			{name: "gmt_case_insensitive", spec: "gmt", wantLoc: time.UTC},
			{name: "z", spec: "Z", wantLoc: time.UTC},
			{name: "local", spec: "local", wantLoc: time.Local},
			{name: "positive_offset", spec: "+05:30", wantOffset: 19800},
			{name: "negative_offset", spec: "-08:00", wantOffset: -28800},
			{name: "named_zone", spec: "America/New_York"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				loc, err := value.ParseLocationString(tc.spec)
				if err != nil {
					t.Fatalf("ParseLocationString(%q) error: %v", tc.spec, err)
				}
				if tc.wantNil {
					if loc != nil {
						t.Fatalf("ParseLocationString(%q) = %v, want nil", tc.spec, loc)
					}
					return
				}
				if loc == nil {
					t.Fatalf("ParseLocationString(%q) = nil location", tc.spec)
				}
				if tc.wantLoc != nil && loc != tc.wantLoc {
					t.Fatalf("ParseLocationString(%q) = %v, want %v", tc.spec, loc, tc.wantLoc)
				}
				if tc.wantOffset != 0 {
					_, offset := time.Date(2024, 6, 1, 0, 0, 0, 0, loc).Zone()
					if offset != tc.wantOffset {
						t.Fatalf("offset for %q = %d, want %d", tc.spec, offset, tc.wantOffset)
					}
				}
				if tc.spec == "America/New_York" && loc.String() != "America/New_York" {
					t.Fatalf("named zone = %q, want America/New_York", loc.String())
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name    string
			spec    string
			wantErr string
		}{
			{name: "bad_offset_digits", spec: "+0a:00", wantErr: "invalid timezone offset"},
			{name: "unknown_zone", spec: "Mars/Olympus", wantErr: `invalid timezone "Mars/Olympus"`},
			{name: "short_offset", spec: "+5:30", wantErr: `invalid timezone "+5:30"`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := value.ParseLocationString(tc.spec)
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("ParseLocationString(%q) error = %v, want %q", tc.spec, err, tc.wantErr)
				}
			})
		}
	})
}

func TestParseLocation(t *testing.T) {
	t.Parallel()

	t.Run("nil_value_means_unset", func(t *testing.T) {
		t.Parallel()
		loc, err := value.ParseLocation(value.NewNil())
		if err != nil || loc != nil {
			t.Fatalf("ParseLocation(nil) = %v, %v; want nil, nil", loc, err)
		}
	})

	t.Run("string_value", func(t *testing.T) {
		t.Parallel()
		loc, err := value.ParseLocation(value.NewString("UTC"))
		if err != nil || loc != time.UTC {
			t.Fatalf("ParseLocation(UTC) = %v, %v; want UTC, nil", loc, err)
		}
	})

	t.Run("non_string_value", func(t *testing.T) {
		t.Parallel()
		_, err := value.ParseLocation(value.NewInt(3))
		if err == nil || err.Error() != "invalid timezone spec" {
			t.Fatalf("ParseLocation(int) error = %v, want %q", err, "invalid timezone spec")
		}
	})
}

func TestTimeFromParts(t *testing.T) {
	t.Parallel()

	intArgs := func(vals ...int64) []value.Value {
		out := make([]value.Value, len(vals))
		for i, v := range vals {
			out[i] = value.NewInt(v)
		}
		return out
	}

	t.Run("no_args", func(t *testing.T) {
		t.Parallel()
		_, err := value.TimeFromParts(nil, time.UTC)
		want := "Time.new expects at least a year"
		if err == nil || err.Error() != want {
			t.Fatalf("TimeFromParts error = %v, want %q", err, want)
		}
	})

	// Ruby's Time.new defaults an omitted month/day to January 1 and omitted
	// time fields to midnight, so the year-, year+month-, and date-only forms
	// all anchor at the start of the relevant period.
	t.Run("default_date_parts", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			args []value.Value
			want time.Time
		}{
			{name: "year_only", args: intArgs(2024), want: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{name: "year_month", args: intArgs(2024, 2), want: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
			{name: "year_month_day", args: intArgs(2024, 6, 3), want: time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := value.TimeFromParts(tc.args, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Equal(tc.want) || got.Location() != time.UTC {
					t.Fatalf("TimeFromParts(%s) = %v, want %v", tc.name, got, tc.want)
				}
			})
		}
	})

	// Ruby treats an explicit nil optional part identically to omitting it, so
	// Time.new(2024, nil) is January 1 -- not month 0, which time.Date would
	// normalize backward into December 2023.
	t.Run("explicit_nil_optional_parts_use_defaults", func(t *testing.T) {
		t.Parallel()
		nilArg := value.NewNil()
		tests := []struct {
			name string
			args []value.Value
			want time.Time
		}{
			{name: "nil_month", args: []value.Value{value.NewInt(2024), nilArg}, want: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{name: "nil_day", args: []value.Value{value.NewInt(2024), value.NewInt(2), nilArg}, want: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
			{name: "nil_hour", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), nilArg}, want: time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC)},
			{name: "nil_minute", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), value.NewInt(4), nilArg}, want: time.Date(2024, 2, 3, 4, 0, 0, 0, time.UTC)},
			{name: "nil_second", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), value.NewInt(4), value.NewInt(5), nilArg}, want: time.Date(2024, 2, 3, 4, 5, 0, 0, time.UTC)},
			{name: "all_optional_nil", args: []value.Value{value.NewInt(2024), nilArg, nilArg, nilArg, nilArg, nilArg}, want: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := value.TimeFromParts(tc.args, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Equal(tc.want) || got.Location() != time.UTC {
					t.Fatalf("TimeFromParts(%s) = %v, want %v", tc.name, got, tc.want)
				}
			})
		}
	})

	t.Run("full_date_time", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromParts(intArgs(2024, 6, 1, 12, 30, 45), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2024, 6, 1, 12, 30, 45, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("TimeFromParts = %v, want %v", got, want)
		}
	})

	t.Run("seventh_arg_overrides_location", func(t *testing.T) {
		t.Parallel()
		args := append(intArgs(2024, 6, 1, 12, 0, 0), value.NewString("+02:00"))
		got, err := value.TimeFromParts(args, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("TimeFromParts with +02:00 = %v, want instant %v", got, want)
		}
		if _, offset := got.Zone(); offset != 2*3600 {
			t.Fatalf("zone offset = %d, want 7200", offset)
		}
	})

	t.Run("seventh_arg_invalid_zone", func(t *testing.T) {
		t.Parallel()
		args := append(intArgs(2024, 6, 1, 0, 0, 0), value.NewString("Mars/Olympus"))
		_, err := value.TimeFromParts(args, time.UTC)
		if err == nil || err.Error() != `invalid timezone "Mars/Olympus"` {
			t.Fatalf("TimeFromParts error = %v, want invalid timezone", err)
		}
	})

	t.Run("nil_default_location_uses_local", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromParts(intArgs(2024, 6, 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Location() != time.Local {
			t.Fatalf("Location() = %v, want time.Local", got.Location())
		}
	})
}

func TestTimeFromCalendarParts(t *testing.T) {
	t.Parallel()

	intArgs := func(vals ...int64) []value.Value {
		out := make([]value.Value, len(vals))
		for i, v := range vals {
			out[i] = value.NewInt(v)
		}
		return out
	}

	t.Run("no_args", func(t *testing.T) {
		t.Parallel()
		_, err := value.TimeFromCalendarParts(nil, time.UTC)
		want := "Time constructor expects at least a year"
		if err == nil || err.Error() != want {
			t.Fatalf("TimeFromCalendarParts error = %v, want %q", err, want)
		}
	})

	t.Run("too_many_args", func(t *testing.T) {
		t.Parallel()
		_, err := value.TimeFromCalendarParts(intArgs(2024, 6, 1, 0, 0, 0, 0, 0), time.UTC)
		want := "Time constructor expects at most year, month, day, hour, minute, second, microsecond"
		if err == nil || err.Error() != want {
			t.Fatalf("TimeFromCalendarParts error = %v, want %q", err, want)
		}
	})

	// Ruby's Time.utc/gm/local/mktime default an omitted month/day to January 1
	// and omitted time fields to midnight, fixing the location by constructor.
	t.Run("default_date_parts_keep_location", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name string
			args []value.Value
			want time.Time
		}{
			{name: "year_only", args: intArgs(2024), want: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{name: "year_month", args: intArgs(2024, 2), want: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
			{name: "year_month_day", args: intArgs(2024, 6, 1), want: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := value.TimeFromCalendarParts(tc.args, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Equal(tc.want) || got.Location() != time.UTC {
					t.Fatalf("TimeFromCalendarParts(%s) = %v, want %v", tc.name, got, tc.want)
				}
			})
		}
	})

	// Ruby treats an explicit nil optional part identically to omitting it, so
	// Time.utc(2024, nil) is January 1 -- not month 0, which time.Date would
	// normalize backward into December 2023.
	t.Run("explicit_nil_optional_parts_use_defaults", func(t *testing.T) {
		t.Parallel()
		nilArg := value.NewNil()
		tests := []struct {
			name string
			args []value.Value
			want time.Time
		}{
			{name: "nil_month", args: []value.Value{value.NewInt(2024), nilArg}, want: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
			{name: "nil_day", args: []value.Value{value.NewInt(2024), value.NewInt(2), nilArg}, want: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
			{name: "nil_hour", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), nilArg}, want: time.Date(2024, 2, 3, 0, 0, 0, 0, time.UTC)},
			{name: "nil_minute", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), value.NewInt(4), nilArg}, want: time.Date(2024, 2, 3, 4, 0, 0, 0, time.UTC)},
			{name: "nil_second", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), value.NewInt(4), value.NewInt(5), nilArg}, want: time.Date(2024, 2, 3, 4, 5, 0, 0, time.UTC)},
			{name: "nil_microsecond", args: []value.Value{value.NewInt(2024), value.NewInt(2), value.NewInt(3), value.NewInt(4), value.NewInt(5), value.NewInt(6), nilArg}, want: time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)},
			{name: "all_optional_nil", args: []value.Value{value.NewInt(2024), nilArg, nilArg, nilArg, nilArg, nilArg, nilArg}, want: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := value.TimeFromCalendarParts(tc.args, time.UTC)
				if err != nil {
					t.Fatal(err)
				}
				if !got.Equal(tc.want) || got.Location() != time.UTC {
					t.Fatalf("TimeFromCalendarParts(%s) = %v, want %v", tc.name, got, tc.want)
				}
			})
		}
	})

	t.Run("integer_microseconds", func(t *testing.T) {
		t.Parallel()
		args := append(intArgs(2024, 1, 2, 3, 4, 5), value.NewInt(123456))
		got, err := value.TimeFromCalendarParts(args, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got.Nanosecond() != 123_456_000 {
			t.Fatalf("Nanosecond() = %d, want 123456000", got.Nanosecond())
		}
	})

	t.Run("float_microseconds_carry_subusec", func(t *testing.T) {
		t.Parallel()
		// Ruby truncates the exact value of the float toward zero rather than
		// rounding: Time.utc(...,123456.7).nsec == 123456699, not 123456700.
		args := append(intArgs(2024, 1, 2, 3, 4, 5), value.NewFloat(123456.7))
		got, err := value.TimeFromCalendarParts(args, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got.Nanosecond() != 123_456_699 {
			t.Fatalf("Nanosecond() = %d, want 123456699", got.Nanosecond())
		}
	})

	t.Run("float_microseconds_truncate_toward_zero", func(t *testing.T) {
		t.Parallel()
		// Each case mirrors observed Ruby output (Time.utc(...,usec).nsec). The
		// fractional nanoseconds are floored, and the result is decided by the
		// float's exact value rather than a second float multiplication.
		tests := []struct {
			name string
			usec float64
			want int
		}{
			{name: "half_usec", usec: 1.5, want: 1500},
			{name: "just_under_usec", usec: 1.4999, want: 1499},
			{name: "subnanosecond_floors", usec: 0.9999999, want: 999},
			{name: "exact_value_009_floors_to_8", usec: 0.009, want: 8},
			{name: "exact_value_011_floors_to_10", usec: 0.011, want: 10},
			{name: "exact_value_1001_floors_to_1000", usec: 1.001, want: 1000},
			{name: "max_fraction_stays_in_range", usec: 999999.9999, want: 999_999_999},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				args := append(intArgs(2024, 1, 2, 3, 4, 5), value.NewFloat(tc.usec))
				got, err := value.TimeFromCalendarParts(args, time.UTC)
				if err != nil {
					t.Fatalf("TimeFromCalendarParts(usec=%v) error: %v", tc.usec, err)
				}
				if got.Nanosecond() != tc.want {
					t.Fatalf("Nanosecond() for usec=%v = %d, want %d", tc.usec, got.Nanosecond(), tc.want)
				}
			})
		}
	})

	t.Run("microsecond_out_of_range_rejected", func(t *testing.T) {
		t.Parallel()
		// Ruby raises "subsecx out of range" for these boundary inputs instead
		// of silently rolling the value into an adjacent second.
		wantErr := "Time constructor microsecond argument out of range (must be within one second)"
		tests := []struct {
			name string
			arg  value.Value
		}{
			{name: "int_one_full_second", arg: value.NewInt(1_000_000)},
			{name: "int_above_one_second", arg: value.NewInt(2_000_000)},
			{name: "int_negative", arg: value.NewInt(-1)},
			// 9e18 fits in int64 but 9e18*1000 overflows it; the guard must
			// reject by magnitude before scaling rather than wrapping.
			{name: "int_extreme_no_overflow", arg: value.NewInt(9_000_000_000_000_000_000)},
			{name: "float_one_full_second", arg: value.NewFloat(1_000_000.0)},
			{name: "float_just_above_one_second", arg: value.NewFloat(1_000_000.0001)},
			{name: "float_negative", arg: value.NewFloat(-0.5)},
			{name: "float_tiny_negative", arg: value.NewFloat(-0.0001)},
			{name: "float_infinity", arg: value.NewFloat(math.Inf(1))},
			{name: "float_nan", arg: value.NewFloat(math.NaN())},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				args := append(intArgs(2024, 1, 2, 3, 4, 5), tc.arg)
				_, err := value.TimeFromCalendarParts(args, time.UTC)
				if err == nil || err.Error() != wantErr {
					t.Fatalf("TimeFromCalendarParts(%v) error = %v, want %q", tc.arg, err, wantErr)
				}
			})
		}
	})

	t.Run("microsecond_max_valid_int", func(t *testing.T) {
		t.Parallel()
		args := append(intArgs(2024, 1, 2, 3, 4, 5), value.NewInt(999_999))
		got, err := value.TimeFromCalendarParts(args, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got.Nanosecond() != 999_999_000 || got.Second() != 5 {
			t.Fatalf("got nsec=%d sec=%d, want nsec=999999000 sec=5", got.Nanosecond(), got.Second())
		}
	})

	t.Run("seventh_arg_is_not_a_timezone", func(t *testing.T) {
		t.Parallel()
		args := append(intArgs(2024, 1, 2, 3, 4, 5), value.NewString("+02:30"))
		_, err := value.TimeFromCalendarParts(args, time.UTC)
		want := "Time constructor microsecond argument must be numeric"
		if err == nil || err.Error() != want {
			t.Fatalf("TimeFromCalendarParts error = %v, want %q", err, want)
		}
	})

	t.Run("nil_default_location_uses_local", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromCalendarParts(intArgs(2024, 6, 1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Location() != time.Local {
			t.Fatalf("Location() = %v, want time.Local", got.Location())
		}
	})
}

func TestTimeFromEpoch(t *testing.T) {
	t.Parallel()

	t.Run("int_seconds", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromEpoch(value.NewInt(1_700_000_000), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got.Unix() != 1_700_000_000 || got.Location() != time.UTC {
			t.Fatalf("TimeFromEpoch = %v (%v), want unix 1700000000 in UTC", got, got.Location())
		}
	})

	t.Run("float_carries_fraction", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromEpoch(value.NewFloat(1_700_000_000.5), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		if got.Unix() != 1_700_000_000 || got.Nanosecond() != 500_000_000 {
			t.Fatalf("TimeFromEpoch = %v, want .5s fraction", got)
		}
	})

	t.Run("nil_location_uses_local", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromEpoch(value.NewInt(0), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Location() != time.Local {
			t.Fatalf("Location() = %v, want time.Local", got.Location())
		}
	})

	t.Run("non_numeric", func(t *testing.T) {
		t.Parallel()
		_, err := value.TimeFromEpoch(value.NewString("soon"), time.UTC)
		if err == nil || err.Error() != "Time.at expects numeric seconds" {
			t.Fatalf("TimeFromEpoch error = %v, want %q", err, "Time.at expects numeric seconds")
		}
	})
}

func TestTimeFromEpochParts(t *testing.T) {
	t.Parallel()

	subsec := func(v value.Value) *value.Value { return &v }
	unitSym := func(name string) *value.Value {
		v := value.NewSymbol(name)
		return &v
	}
	nilArg := func() *value.Value {
		v := value.NewNil()
		return &v
	}

	cases := []struct {
		name        string
		sec         value.Value
		subsec      *value.Value
		unit        *value.Value
		wantUnix    int64
		wantNanos   int
		wantErrText string
	}{
		{
			name:      "float seconds carry fraction",
			sec:       value.NewFloat(0.123456),
			subsec:    nil,
			unit:      nil,
			wantUnix:  0,
			wantNanos: 123456000,
		},
		{
			name:      "microsecond positional defaults to usec",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(123456)),
			unit:      nil,
			wantUnix:  0,
			wantNanos: 123456000,
		},
		{
			name:      "explicit usec unit",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(123456)),
			unit:      unitSym("usec"),
			wantUnix:  0,
			wantNanos: 123456000,
		},
		{
			name:      "microsecond unit alias",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(123456)),
			unit:      unitSym("microsecond"),
			wantUnix:  0,
			wantNanos: 123456000,
		},
		{
			name:      "millisecond unit",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(123)),
			unit:      unitSym("millisecond"),
			wantUnix:  0,
			wantNanos: 123000000,
		},
		{
			name:      "nanosecond positional",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(123456789)),
			unit:      unitSym("nsec"),
			wantUnix:  0,
			wantNanos: 123456789,
		},
		{
			name:      "nanosecond unit alias",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(123456789)),
			unit:      unitSym("nanosecond"),
			wantUnix:  0,
			wantNanos: 123456789,
		},
		{
			name:      "subsecond overflows into seconds",
			sec:       value.NewInt(5),
			subsec:    subsec(value.NewInt(1_500_000)),
			unit:      nil,
			wantUnix:  6,
			wantNanos: 500000000,
		},
		{
			name:      "negative subsecond borrows from seconds",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewInt(-123456)),
			unit:      nil,
			wantUnix:  -1,
			wantNanos: 876544000,
		},
		{
			// Ruby floors a fractional subsecond offset; for a positive value
			// flooring and truncation coincide, so Time.at(0, 1.9, :nsec) keeps
			// 1 ns.
			name:      "positive float subsecond floors to whole nanosecond",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(1.9)),
			unit:      unitSym("nsec"),
			wantUnix:  0,
			wantNanos: 1,
		},
		{
			// Ruby keeps the rational offset and floors the resulting instant,
			// so a negative fractional offset rounds toward negative infinity:
			// Time.at(0, -1.9, :nsec) floors -1.9 ns to -2 ns, leaving
			// sec=-1, nsec=999999998. Truncating toward zero would land one
			// nanosecond too late (nsec 999999999).
			name:      "negative float nanosecond floors toward negative infinity",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(-1.9)),
			unit:      unitSym("nsec"),
			wantUnix:  -1,
			wantNanos: 999999998,
		},
		{
			// The float -0.1 has no representation that lands on zero, so
			// flooring -0.1 ns yields -1 ns (nsec 999999999). Truncating toward
			// zero would leave the instant unchanged at 0 ns -- the regression
			// this guards against.
			name:      "negative subnanosecond floors to one nanosecond earlier",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(-0.1)),
			unit:      unitSym("nsec"),
			wantUnix:  -1,
			wantNanos: 999999999,
		},
		{
			// The float -0.29 is -0.28999...998, so scaled by 1000 ns/usec the
			// exact value is -289.99...98 ns. Ruby floors this to -290 ns,
			// leaving nsec 999999710. Truncating toward zero (or rounding the
			// float product back to -290.0) would give the wrong result.
			name:      "negative float microsecond floors exact value",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(-0.29)),
			unit:      unitSym("usec"),
			wantUnix:  -1,
			wantNanos: 999999710,
		},
		{
			// Time.at(0, -1.5, :usec) == -1500 ns exactly (no sub-nanosecond
			// fraction), so flooring and truncation coincide: sec=-1,
			// nsec=999998500.
			name:      "negative float microsecond exact half",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(-1.5)),
			unit:      unitSym("usec"),
			wantUnix:  -1,
			wantNanos: 999998500,
		},
		{
			name:        "non numeric seconds",
			sec:         value.NewString("soon"),
			subsec:      nil,
			unit:        nil,
			wantErrText: "Time.at expects numeric seconds",
		},
		{
			name:        "non numeric subsecond",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewString("1")),
			unit:        nil,
			wantErrText: "Time.at subsecond value must be numeric",
		},
		{
			// An explicitly-supplied nil subsecond is non-numeric and must be
			// rejected, matching Ruby's Time.at(0, nil) TypeError. Unlike the
			// calendar constructors (Time.utc/local) which treat an explicit nil
			// subsecond as omitted, Time.at surfaces it as an error.
			name:        "explicit nil subsecond rejected",
			sec:         value.NewInt(0),
			subsec:      nilArg(),
			unit:        nil,
			wantErrText: "Time.at subsecond value must be numeric",
		},
		{
			// Even paired with a valid unit, an explicit nil subsecond is still
			// non-numeric and rejected, matching Ruby's Time.at(0, nil, :usec).
			name:        "explicit nil subsecond rejected with unit",
			sec:         value.NewInt(0),
			subsec:      nilArg(),
			unit:        unitSym("usec"),
			wantErrText: "Time.at subsecond value must be numeric",
		},
		{
			// An explicit nil unit is an unrecognized unit, matching Ruby's
			// Time.at(0, 500, nil) ArgumentError ("unexpected unit: ").
			name:        "explicit nil unit rejected",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewInt(500)),
			unit:        nilArg(),
			wantErrText: "unexpected unit: ",
		},
		{
			name:        "unknown unit symbol",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewInt(1)),
			unit:        unitSym("bogus"),
			wantErrText: "unexpected unit: bogus",
		},
		{
			// A non-symbol unit (here a string) is an unrecognized unit, matching
			// Ruby's Time.at(0, 500, "usec") ArgumentError ("unexpected unit: usec").
			name:        "non symbol unit",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewInt(1)),
			unit:        subsec(value.NewString("nsec")),
			wantErrText: "unexpected unit: nsec",
		},
		{
			name:        "unit without subsecond",
			sec:         value.NewInt(0),
			subsec:      nil,
			unit:        unitSym("nsec"),
			wantErrText: "Time.at expects a subsecond value before a unit",
		},
		{
			name:        "integer subsecond scaling overflows int64",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewInt(9_223_372_036_854_776)),
			unit:        unitSym("microsecond"),
			wantErrText: "Time.at subsecond value out of range",
		},
		{
			name:        "negative integer subsecond scaling overflows int64",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewInt(-9_223_372_036_854_776)),
			unit:        unitSym("microsecond"),
			wantErrText: "Time.at subsecond value out of range",
		},
		{
			name:        "subsecond addition overflows int64",
			sec:         value.NewFloat(0.5),
			subsec:      subsec(value.NewInt(math.MaxInt64)),
			unit:        unitSym("nsec"),
			wantErrText: "Time.at subsecond value out of range",
		},
		{
			name:        "float subsecond magnitude out of int64 range",
			sec:         value.NewInt(0),
			subsec:      subsec(value.NewFloat(1e20)),
			unit:        unitSym("nsec"),
			wantErrText: "Time.at subsecond value out of range",
		},
		{
			// The float 0.29 is 0.28999...998, so the exact value scaled by
			// 1000 ns/usec truncates to 289, not the 290 that float64
			// multiplication would round up to. Ruby's Time.at(0, 0.29, :usec)
			// yields 289 ns, so representation error must not flip the result.
			name:      "float microsecond truncates exact value not rounded product",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(0.29)),
			unit:      unitSym("usec"),
			wantUnix:  0,
			wantNanos: 289,
		},
		{
			// Matches Ruby's Time.at(0, 1.005, :millisecond) == 1004999 ns:
			// the float 1.005 is just below 1.005, so scaling by 1e6 ns/ms and
			// truncating yields 1004999 rather than 1005000.
			name:      "float millisecond truncates exact value not rounded product",
			sec:       value.NewInt(0),
			subsec:    subsec(value.NewFloat(1.005)),
			unit:      unitSym("millisecond"),
			wantUnix:  0,
			wantNanos: 1004999,
		},
		{
			// A whole-second subsecond carry that pushes the epoch seconds past
			// math.MaxInt64 must raise rather than let time.Unix normalize the
			// overflow into a wrapped (negative) instant. Time.Time cannot
			// represent Ruby's arbitrary-precision result of MaxInt64 + 1 second.
			name:        "subsecond carry overflows epoch seconds",
			sec:         value.NewInt(math.MaxInt64),
			subsec:      subsec(value.NewInt(1_000_000)),
			unit:        nil,
			wantErrText: "Time.at subsecond value out of range",
		},
		{
			// The mirror image at the lower bound: a borrow that pushes the
			// epoch seconds below math.MinInt64 must raise rather than wrap.
			name:        "subsecond borrow underflows epoch seconds",
			sec:         value.NewInt(math.MinInt64),
			subsec:      subsec(value.NewInt(-1_000_000)),
			unit:        nil,
			wantErrText: "Time.at subsecond value out of range",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := value.TimeFromEpochParts(tc.sec, tc.subsec, tc.unit, time.UTC)
			if tc.wantErrText != "" {
				if err == nil || err.Error() != tc.wantErrText {
					t.Fatalf("TimeFromEpochParts error = %v, want %q", err, tc.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Unix() != tc.wantUnix || got.Nanosecond() != tc.wantNanos {
				t.Fatalf("TimeFromEpochParts = unix %d nanos %d, want unix %d nanos %d",
					got.Unix(), got.Nanosecond(), tc.wantUnix, tc.wantNanos)
			}
			if got.Location() != time.UTC {
				t.Fatalf("Location() = %v, want UTC", got.Location())
			}
		})
	}

	t.Run("nil_location_uses_local", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromEpochParts(value.NewInt(0), subsec(value.NewInt(1)), unitSym("nsec"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Location() != time.Local {
			t.Fatalf("Location() = %v, want time.Local", got.Location())
		}
	})
}

func TestParseTimeString(t *testing.T) {
	t.Parallel()

	t.Run("explicit_layout", func(t *testing.T) {
		t.Parallel()
		got, err := value.ParseTimeString("2024-06-01 12:30:45", "2006-01-02 15:04:05", true, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2024, 6, 1, 12, 30, 45, 0, time.UTC)
		if !got.Equal(want) {
			t.Fatalf("ParseTimeString = %v, want %v", got, want)
		}
	})

	t.Run("explicit_layout_nil_location_parses_local", func(t *testing.T) {
		t.Parallel()
		got, err := value.ParseTimeString("2024-06-01 12:30:45", "2006-01-02 15:04:05", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2024, 6, 1, 12, 30, 45, 0, time.Local)
		if !got.Equal(want) || got.Location() != time.Local {
			t.Fatalf("ParseTimeString = %v (%v), want %v in Local", got, got.Location(), want)
		}
	})

	t.Run("default_layout_nil_location_keeps_parsed_zone", func(t *testing.T) {
		t.Parallel()
		got, err := value.ParseTimeString("2024-06-01T12:00:00+02:00", "", false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, offset := got.Zone(); offset != 2*3600 {
			t.Fatalf("zone offset = %d, want 7200", offset)
		}
	})

	t.Run("explicit_layout_mismatch", func(t *testing.T) {
		t.Parallel()
		_, err := value.ParseTimeString("June 1st", "2006-01-02", true, time.UTC)
		if err == nil || !strings.HasPrefix(err.Error(), "Time.parse could not parse time:") {
			t.Fatalf("ParseTimeString error = %v, want parse failure with cause", err)
		}
	})

	t.Run("default_layouts", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name  string
			input string
			want  time.Time
		}{
			{
				name:  "rfc3339",
				input: "2024-06-01T12:30:45Z",
				want:  time.Date(2024, 6, 1, 12, 30, 45, 0, time.UTC),
			},
			{
				name:  "date_only",
				input: "2024-06-01",
				want:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			{
				name:  "us_date",
				input: "06/01/2024",
				want:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			},
			{
				name:  "space_separated",
				input: "2024-06-01 12:30:45",
				want:  time.Date(2024, 6, 1, 12, 30, 45, 0, time.UTC),
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := value.ParseTimeString(tc.input, "", false, time.UTC)
				if err != nil {
					t.Fatalf("ParseTimeString(%q) error: %v", tc.input, err)
				}
				if !got.Equal(tc.want) {
					t.Fatalf("ParseTimeString(%q) = %v, want %v", tc.input, got, tc.want)
				}
			})
		}
	})

	t.Run("location_conversion", func(t *testing.T) {
		t.Parallel()
		loc, err := value.ParseLocationString("+02:00")
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.ParseTimeString("2024-06-01T12:00:00Z", "", false, loc)
		if err != nil {
			t.Fatal(err)
		}
		if _, offset := got.Zone(); offset != 2*3600 {
			t.Fatalf("zone offset = %d, want 7200", offset)
		}
		if !got.Equal(time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("instant changed during conversion: %v", got)
		}
	})

	t.Run("unparseable", func(t *testing.T) {
		t.Parallel()
		_, err := value.ParseTimeString("not a time", "", false, time.UTC)
		if err == nil || err.Error() != "Time.parse could not parse time" {
			t.Fatalf("ParseTimeString error = %v, want %q", err, "Time.parse could not parse time")
		}
	})

	// Go's parser normalizes a trailing negative-zero offset to a nameless
	// zero-offset zone, dropping the "-" Ruby reads as the RFC 2822 unknown-zone
	// marker. ParseTimeString re-anchors such inputs to the canonical negative-zero
	// zone (name "-00:00") so serializers can distinguish them from a genuine
	// "+00:00" offset, while "Z"/"+00:00" inputs keep their nameless UTC zone and
	// an explicit location override suppresses the inference.
	t.Run("negative_zero_offset_zone", func(t *testing.T) {
		t.Parallel()
		canonical, err := value.ParseLocationString("-00:00")
		if err != nil {
			t.Fatal(err)
		}
		// Only the negative-zero inputs must be re-anchored to the canonical
		// "-00:00" zone (so rfc2822 emits the unknown-zone marker). For other
		// zero-offset inputs we deliberately leave the parsed location alone;
		// Go names a zero-offset zone based on the host TZ ("", "Local", or
		// "UTC"), so asserting an exact name here would be environment-dependent.
		// We therefore only assert whether the zone is the canonical negative
		// zero, which is the behavior under test.
		tests := []struct {
			name          string
			input         string
			wantCanonical bool
		}{
			{name: "rfc3339_negative_zero", input: "2024-01-02T03:04:05-00:00", wantCanonical: true},
			{name: "rfc1123z_negative_zero", input: "Tue, 02 Jan 2024 03:04:05 -0000", wantCanonical: true},
			{name: "rfc3339_positive_zero", input: "2024-01-02T03:04:05+00:00", wantCanonical: false},
			{name: "rfc3339_zulu", input: "2024-01-02T03:04:05Z", wantCanonical: false},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := value.ParseTimeString(tc.input, "", false, nil)
				if err != nil {
					t.Fatalf("ParseTimeString(%q) error: %v", tc.input, err)
				}
				if _, offset := got.Zone(); offset != 0 {
					t.Fatalf("ParseTimeString(%q) offset = %d, want 0", tc.input, offset)
				}
				gotCanonical := got.Location().String() == canonical.String()
				if gotCanonical != tc.wantCanonical {
					t.Fatalf("ParseTimeString(%q): canonical negative-zero zone = %v, want %v (zone name %q)",
						tc.input, gotCanonical, tc.wantCanonical, got.Location().String())
				}
			})
		}
	})

	// An explicit `in:` location override must win over the negative-zero
	// inference, converting the parsed instant into the requested zone.
	t.Run("negative_zero_offset_with_location_override", func(t *testing.T) {
		t.Parallel()
		loc, err := value.ParseLocationString("+05:30")
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.ParseTimeString("2024-01-02T03:04:05-00:00", "", false, loc)
		if err != nil {
			t.Fatal(err)
		}
		if _, offset := got.Zone(); offset != 5*3600+30*60 {
			t.Fatalf("zone offset = %d, want %d", offset, 5*3600+30*60)
		}
		if !got.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
			t.Fatalf("instant changed during conversion: %v", got)
		}
	})
}
