package value_test

import (
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

	t.Run("too_few_args", func(t *testing.T) {
		t.Parallel()
		_, err := value.TimeFromParts(intArgs(2024, 6), time.UTC)
		want := "Time.new expects at least year, month, day"
		if err == nil || err.Error() != want {
			t.Fatalf("TimeFromParts error = %v, want %q", err, want)
		}
	})

	t.Run("date_only", func(t *testing.T) {
		t.Parallel()
		got, err := value.TimeFromParts(intArgs(2024, 6, 1), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		want := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Fatalf("TimeFromParts = %v, want %v", got, want)
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
}
