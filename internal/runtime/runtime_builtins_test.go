package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"
)

func TestDurationMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def duration_helpers()
      d = Duration.build(3600)
      {
        iso: d.iso8601,
        parts: d.parts,
        in_hours: d.in_hours,
        seconds: d.seconds,
        to_i: d.to_i,
        eql: d.eql?(Duration.parse("PT1H")),
        months: Duration.build(2592000).in_months
      }
    end

    def duration_after(base)
      60.seconds.after(base).to_s
    end

    def duration_ago(base)
      60.seconds.ago(base).to_s
    end

    def duration_parse_iso()
      Duration.parse("P1DT1H1M1S").to_i
    end

    def duration_parse_week()
      Duration.parse("P2W").to_i
    end

    def duration_parse_invalid()
      Duration.parse("P1DT1HXYZ")
    end

    def duration_parse_empty()
      Duration.parse("P")
    end

    def duration_parse_fractional()
      Duration.parse("1.5s")
    end

    def duration_add()
      (4.seconds + 2.hours).to_i
    end

    def duration_subtract()
      (2.hours - 4.seconds).to_i
    end

    def duration_multiply()
      (10.seconds * 3).to_i
    end

    def duration_multiply_left()
      (3 * 10.seconds).to_i
    end

    def duration_divide()
      (10.seconds / 2).to_i
    end

    def duration_divide_duration()
      10.seconds / 4.seconds
    end

    def duration_modulo()
      (10.seconds % 4.seconds).to_i
    end

    def duration_compare()
      [2.seconds < 3.seconds, 5.seconds == 5.seconds, 10.seconds > 3.seconds]
    end
    `)

	result := callFunc(t, script, "duration_helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	parts := result.Hash()
	if got, want := parts["iso"].String(), "PT1H"; got != want {
		t.Fatalf("iso8601 mismatch: got %s want %s", got, want)
	}
	if got, want := parts["to_i"], NewInt(3600); !got.Equal(want) {
		t.Fatalf("to_i mismatch: got %v want %v", got, want)
	}
	if got, want := parts["seconds"], NewInt(3600); !got.Equal(want) {
		t.Fatalf("seconds mismatch: got %v want %v", got, want)
	}
	if got := parts["in_hours"]; got.Kind() != KindFloat || got.Float() != 1 {
		t.Fatalf("in_hours mismatch: %v", got)
	}
	if got := parts["months"]; got.Kind() != KindFloat || got.Float() != 1 {
		t.Fatalf("in_months mismatch: %v", got)
	}
	if got := parts["eql"]; got.Kind() != KindBool || !got.Bool() {
		t.Fatalf("expected eql? to be true, got %v", got)
	}

	partsVal := parts["parts"]
	if partsVal.Kind() != KindHash {
		t.Fatalf("parts should be hash, got %v", partsVal.Kind())
	}
	partsMap := partsVal.Hash()
	if partsMap["hours"] != NewInt(1) || partsMap["minutes"] != NewInt(0) || partsMap["seconds"] != NewInt(0) {
		t.Fatalf("parts unexpected: %#v", partsMap)
	}

	base := NewString("2024-01-01T00:00:00Z")
	after := callFunc(t, script, "duration_after", []Value{base})
	if got := after.String(); got != "2024-01-01T00:01:00Z" {
		t.Fatalf("after mismatch: %s", got)
	}

	before := callFunc(t, script, "duration_ago", []Value{NewString("2024-01-01T00:01:00Z")})
	if got := before.String(); got != "2024-01-01T00:00:00Z" {
		t.Fatalf("ago mismatch: %s", got)
	}

	parsed := callFunc(t, script, "duration_parse_iso", nil)
	if !parsed.Equal(NewInt(90061)) {
		t.Fatalf("parse iso mismatch: got %v want 90061", parsed)
	}

	weeks := callFunc(t, script, "duration_parse_week", nil)
	if !weeks.Equal(NewInt(1209600)) {
		t.Fatalf("parse weeks mismatch: got %v", weeks)
	}

	_, err := script.Call(context.Background(), "duration_parse_invalid", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for invalid duration")
	}

	badOrder := compileScript(t, `
    def run()
      Duration.parse("PT1S30M")
    end
    `)
	_, err = badOrder.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for out-of-order duration")
	}

	empty := compileScript(t, `
    def run()
      Duration.parse("P")
    end
    `)
	_, err = empty.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for empty duration")
	}

	fractional := compileScript(t, `
    def run()
      Duration.parse("1.5s")
    end
    `)
	_, err = fractional.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error for fractional duration")
	}

	intCases := []struct {
		name string
		fn   string
		want int64
	}{
		{name: "duration_add", fn: "duration_add", want: 7204},
		{name: "duration_subtract", fn: "duration_subtract", want: 7196},
		{name: "duration_multiply", fn: "duration_multiply", want: 30},
		{name: "duration_multiply_left", fn: "duration_multiply_left", want: 30},
		{name: "duration_divide", fn: "duration_divide", want: 5},
		{name: "duration_modulo", fn: "duration_modulo", want: 2},
	}
	for _, tc := range intCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, tc.fn, nil)
			if !got.Equal(NewInt(tc.want)) {
				t.Fatalf("%s mismatch: %v", tc.fn, got)
			}
		})
	}

	divDur := callFunc(t, script, "duration_divide_duration", nil)
	if divDur.Kind() != KindFloat || divDur.Float() != 2.5 {
		t.Fatalf("duration divide duration mismatch: %v", divDur)
	}
	comp := callFunc(t, script, "duration_compare", nil)
	wantComp := arrayVal(boolVal(true), boolVal(true), boolVal(true))
	compareArrays(t, comp, wantComp.Array())
}

func TestTimeFormatUsesGoLayout(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      t = Time.utc(2000, 1, 1, 20, 15, 1)
      formatter = t.format
      {
        y2: t.format("06"),
        y4: t.format("2006"),
        date: t.format("2006-01-02"),
        time: t.format("15:04:05"),
        bound: formatter("2006-01-02")
      }
    end
    `)

	result := callFunc(t, script, "run", nil)
	want := hashVal(map[string]Value{
		"y2":    NewString("00"),
		"y4":    NewString("2000"),
		"date":  NewString("2000-01-01"),
		"time":  NewString("20:15:01"),
		"bound": NewString("2000-01-01"),
	})
	if result.Kind() != KindHash {
		t.Fatalf("unexpected format output: %#v", result)
	}
	got := result.Hash()
	for key, expected := range want.Hash() {
		if val, ok := got[key]; !ok || !val.Equal(expected) {
			t.Fatalf("unexpected format output %s: got %v want %v", key, val, expected)
		}
	}
}

func TestTimeCalendarConstructorSubsecond(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def subsecond()
	      {
	        local_usec: Time.local(2024, 1, 2, 3, 4, 5, 123456).usec,
	        mktime_usec: Time.mktime(2024, 1, 2, 3, 4, 5, 123456).usec,
	        utc_usec: Time.utc(2024, 1, 2, 3, 4, 5, 123456).usec,
	        gm_usec: Time.gm(2024, 1, 2, 3, 4, 5, 123456).usec,
	        utc_nsec: Time.utc(2024, 1, 2, 3, 4, 5, 123456).nsec,
	        utc_offset: Time.utc(2024, 1, 2, 3, 4, 5, 123456).utc_offset,
	        gm_offset: Time.gm(2024, 1, 2, 3, 4, 5, 123456).utc_offset,
	        float_nsec: Time.utc(2024, 1, 2, 3, 4, 5, 123456.7).nsec,
	        nil_usec: Time.utc(2024, 1, 2, 3, 4, 5, nil).usec
	      }
	    end

	    def new_keeps_zone()
	      Time.new(2024, 1, 2, 3, 4, 5, "+02:30").utc_offset
	    end
	    `)

	result := callFunc(t, script, "subsecond", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	want := map[string]Value{
		"local_usec":  NewInt(123456),
		"mktime_usec": NewInt(123456),
		"utc_usec":    NewInt(123456),
		"gm_usec":     NewInt(123456),
		"utc_nsec":    NewInt(123456000),
		"utc_offset":  NewInt(0),
		"gm_offset":   NewInt(0),
		// Ruby truncates the float's exact value toward zero rather than
		// rounding: Time.utc(...,123456.7).nsec == 123456699.
		"float_nsec": NewInt(123456699),
		// An explicit nil subsecond is treated as omitted (zero), like Ruby.
		"nil_usec": NewInt(0),
	}
	got := result.Hash()
	for key, expected := range want {
		if val, ok := got[key]; !ok || !val.Equal(expected) {
			t.Fatalf("subsecond[%s] = %v, want %v", key, val, expected)
		}
	}

	zone := callFunc(t, script, "new_keeps_zone", nil)
	if zone.Kind() != KindInt || zone.Int() != 9000 {
		t.Fatalf("Time.new zone offset = %v, want 9000", zone)
	}
}

func TestTimeConstructorDefaultDateParts(t *testing.T) {
	t.Parallel()
	// Ruby defaults an omitted month/day to January 1 and omitted time fields to
	// midnight for every calendar constructor. We assert the constructed calendar
	// fields (year/month/day plus the start-of-day hour:minute:second) directly so
	// the test does not depend on the host timezone: Time.new/local/mktime anchor
	// at local midnight, whose UTC instant would otherwise shift with TZ.
	script := compileScript(t, `
	    def fields(t)
	      [t.year, t.month, t.day, t.hour, t.min, t.sec]
	    end

	    def constructors()
	      {
	        new_year: fields(Time.new(2024)),
	        new_year_month: fields(Time.new(2024, 2)),
	        new_year_month_day: fields(Time.new(2024, 2, 3)),
	        local_year: fields(Time.local(2024)),
	        mktime_year: fields(Time.mktime(2024)),
	        utc_year: fields(Time.utc(2024)),
	        utc_year_month: fields(Time.utc(2024, 2)),
	        gm_year: fields(Time.gm(2024)),
	        new_nil_month: fields(Time.new(2024, nil)),
	        utc_nil_month: fields(Time.utc(2024, nil)),
	        utc_nil_day: fields(Time.utc(2024, 2, nil)),
	        new_nil_time_fields: fields(Time.new(2024, 2, 3, nil, nil, nil)),
	      }
	    end

	    def utc_iso()
	      {
	        utc1: Time.utc(2024).iso8601,
	        utc2: Time.utc(2024, 2).iso8601,
	        gm1: Time.gm(2024).iso8601,
	      }
	    end
	    `)

	intArr := func(vals ...int64) Value {
		out := make([]Value, len(vals))
		for i, v := range vals {
			out[i] = NewInt(v)
		}
		return NewArray(out)
	}

	result := callFunc(t, script, "constructors", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	wantFields := map[string]Value{
		"new_year":            intArr(2024, 1, 1, 0, 0, 0),
		"new_year_month":      intArr(2024, 2, 1, 0, 0, 0),
		"new_year_month_day":  intArr(2024, 2, 3, 0, 0, 0),
		"local_year":          intArr(2024, 1, 1, 0, 0, 0),
		"mktime_year":         intArr(2024, 1, 1, 0, 0, 0),
		"utc_year":            intArr(2024, 1, 1, 0, 0, 0),
		"utc_year_month":      intArr(2024, 2, 1, 0, 0, 0),
		"gm_year":             intArr(2024, 1, 1, 0, 0, 0),
		"new_nil_month":       intArr(2024, 1, 1, 0, 0, 0),
		"utc_nil_month":       intArr(2024, 1, 1, 0, 0, 0),
		"utc_nil_day":         intArr(2024, 2, 1, 0, 0, 0),
		"new_nil_time_fields": intArr(2024, 2, 3, 0, 0, 0),
	}
	got := result.Hash()
	for key, expected := range wantFields {
		val, ok := got[key]
		if !ok {
			t.Fatalf("constructors missing key %s", key)
		}
		if !val.Equal(expected) {
			t.Fatalf("constructors[%s] = %v, want %v", key, val, expected)
		}
	}

	// The UTC/gm aliases anchor at a fixed zone, so their serialized instant is
	// independent of the host timezone and matches Ruby's iso8601 output.
	iso := callFunc(t, script, "utc_iso", nil)
	wantISO := map[string]Value{
		"utc1": NewString("2024-01-01T00:00:00Z"),
		"utc2": NewString("2024-02-01T00:00:00Z"),
		"gm1":  NewString("2024-01-01T00:00:00Z"),
	}
	gotISO := iso.Hash()
	for key, expected := range wantISO {
		if val, ok := gotISO[key]; !ok || !val.Equal(expected) {
			t.Fatalf("utc_iso[%s] = %v, want %v", key, val, expected)
		}
	}
}

func TestTimeCalendarConstructorArgRejection(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def string_subsec(method)
	      case method
	      when "local" then Time.local(2024, 1, 2, 3, 4, 5, "+02:30")
	      when "mktime" then Time.mktime(2024, 1, 2, 3, 4, 5, "+02:30")
	      when "utc" then Time.utc(2024, 1, 2, 3, 4, 5, "+02:30")
	      when "gm" then Time.gm(2024, 1, 2, 3, 4, 5, "+02:30")
	      end
	    end

	    def too_few()
	      Time.utc()
	    end

	    def too_many()
	      Time.utc(2024, 1, 2, 3, 4, 5, 6, 7)
	    end

	    def out_of_range(usec)
	      Time.utc(2024, 1, 2, 3, 4, 5, usec)
	    end

	    def nil_year(method)
	      case method
	      when "new" then Time.new(nil)
	      when "local" then Time.local(nil)
	      when "mktime" then Time.mktime(nil)
	      when "utc" then Time.utc(nil)
	      when "gm" then Time.gm(nil)
	      end
	    end
	    `)

	for _, method := range []string{"local", "mktime", "utc", "gm"} {
		requireCallErrorContains(t, script, "string_subsec", []Value{NewString(method)}, CallOptions{},
			"Time constructor microsecond argument must be numeric")
	}
	requireCallErrorContains(t, script, "too_few", nil, CallOptions{},
		"Time constructor expects at least a year")
	requireCallErrorContains(t, script, "too_many", nil, CallOptions{},
		"Time constructor expects at most year, month, day, hour, minute, second, microsecond")

	// Ruby never treats the required year as omittable: a nil year raises
	// (TypeError) rather than coercing to year 0, even with the new
	// one-argument forms.
	for _, method := range []string{"new", "local", "mktime", "utc", "gm"} {
		requireCallErrorContains(t, script, "nil_year", []Value{NewString(method)}, CallOptions{},
			"Time constructor year must be numeric, got nil")
	}

	// Ruby raises for a subsecond component that does not fit in one second
	// instead of rolling the timestamp into an adjacent second.
	rangeArgs := []Value{
		NewInt(1_000_000),
		NewInt(-1),
		NewFloat(1_000_000.0),
		NewFloat(-0.5),
	}
	for _, arg := range rangeArgs {
		requireCallErrorContains(t, script, "out_of_range", []Value{arg}, CallOptions{},
			"Time constructor microsecond argument out of range (must be within one second)")
	}
}

func TestTimeParseAndAliases(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def helpers()
	      default = Time.parse("2024-01-02T03:04:05Z")
	      default_nil_layout = Time.parse("2024-01-02T03:04:05Z", nil)
	      custom = Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "America/New_York")
	      {
	        default_to_s: default.to_s,
	        default_nil_layout_to_s: default_nil_layout.to_s,
	        default_iso: default.iso8601,
	        default_rfc3339: default.rfc3339,
	        custom_utc_offset: custom.utc_offset,
        custom_utc: custom.utc.to_s
      }
    end

    def parse_invalid()
      Time.parse("not-a-time")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if got["default_to_s"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_to_s mismatch: %q", got["default_to_s"].String())
	}
	if got["default_nil_layout_to_s"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_nil_layout_to_s mismatch: %q", got["default_nil_layout_to_s"].String())
	}
	if got["default_iso"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_iso mismatch: %q", got["default_iso"].String())
	}
	if got["default_rfc3339"].String() != "2024-01-02T03:04:05Z" {
		t.Fatalf("default_rfc3339 mismatch: %q", got["default_rfc3339"].String())
	}
	if got["custom_utc_offset"].Int() != -18000 {
		t.Fatalf("custom_utc_offset mismatch: %v", got["custom_utc_offset"])
	}
	if got["custom_utc"].String() != "2024-01-02T08:04:05Z" {
		t.Fatalf("custom_utc mismatch: %q", got["custom_utc"].String())
	}

	_, err := script.Call(context.Background(), "parse_invalid", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected parse error")
	}
	requireErrorContains(t, err, "could not parse time")
}

func TestTimeAtSubsecondConstructor(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def subsecond()
	      {
	        float_nsec: Time.at(0.123456).utc.nsec,
	        usec_positional: Time.at(0, 123456).utc.nsec,
	        usec_unit: Time.at(0, 123456, :microsecond).utc.nsec,
	        usec_alias: Time.at(0, 123456, :usec).utc.nsec,
	        msec_unit: Time.at(0, 123, :millisecond).utc.nsec,
	        nsec_unit: Time.at(0, 123456789, :nanosecond).utc.nsec,
	        nsec_alias: Time.at(0, 123456789, :nsec).utc.nsec,
	        zone_offset: Time.at(0, 123456, in: "+05:30").utc_offset,
	        zone_nsec: Time.at(0, 123456, in: "+05:30").nsec,
	        # Ruby floors a fractional subsecond offset, so a negative fractional
	        # value rounds toward negative infinity rather than toward zero:
	        # Time.at(0, -1.9, :nsec) floors -1.9 ns to -2 ns, leaving the
	        # instant at -1 s + 999999998 ns.
	        neg_float_nsec: Time.at(0, -1.9, :nsec).utc.nsec,
	        neg_float_nsec_sec: Time.at(0, -1.9, :nsec).utc.to_i,
	        # Time.at(0, -0.29, :usec) floors -289.99...98 ns to -290 ns.
	        neg_float_usec: Time.at(0, -0.29, :usec).utc.nsec,
	        # Time.at(0, -0.1, :nsec) floors -0.1 ns to -1 ns; truncation toward
	        # zero would leave the instant unchanged at 0 ns.
	        neg_subnano: Time.at(0, -0.1, :nsec).utc.nsec
	      }
	    end

	    def too_many()
	      Time.at(0, 1, :nsec, 2)
	    end

	    def unknown_kwarg()
	      Time.at(0, bogus: 1)
	    end

	    def bad_unit()
	      Time.at(0, 1, :picosecond)
	    end

	    def nil_subsec_with_unit()
	      Time.at(0, nil, :nsec)
	    end

	    def nil_subsec()
	      Time.at(0, nil)
	    end

	    def nil_unit()
	      Time.at(0, 500, nil)
	    end

	    def subsec_carries_into_seconds()
	      Time.at(5, 1_500_000).utc.to_i
	    end

	    def subsec_scaling_overflows()
	      Time.at(0, 9_223_372_036_854_776, :microsecond)
	    end

	    def float_subsec_overflows()
	      Time.at(0, 99999999999999999999.0, :nsec)
	    end
	    `)

	result := callFunc(t, script, "subsecond", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	want := map[string]Value{
		"float_nsec":      NewInt(123456000),
		"usec_positional": NewInt(123456000),
		"usec_unit":       NewInt(123456000),
		"usec_alias":      NewInt(123456000),
		"msec_unit":       NewInt(123000000),
		"nsec_unit":       NewInt(123456789),
		"nsec_alias":      NewInt(123456789),
		// in: composes with the subsecond forms: the offset is applied while
		// the subsecond component is preserved.
		"zone_offset": NewInt(19800),
		"zone_nsec":   NewInt(123456000),
		// Negative fractional subsecond offsets floor toward negative infinity,
		// matching Ruby's Time.at(0, -1.9, :nsec).nsec == 999999998.
		"neg_float_nsec":     NewInt(999999998),
		"neg_float_nsec_sec": NewInt(-1),
		"neg_float_usec":     NewInt(999999710),
		"neg_subnano":        NewInt(999999999),
	}
	got := result.Hash()
	for key, expected := range want {
		if val, ok := got[key]; !ok || !val.Equal(expected) {
			t.Fatalf("subsecond[%s] = %v, want %v", key, val, expected)
		}
	}

	requireCallErrorContains(t, script, "too_many", nil, CallOptions{},
		"Time.at expects seconds since epoch with optional subsecond value and unit")
	requireCallErrorContains(t, script, "unknown_kwarg", nil, CallOptions{},
		"Time.at unknown keyword argument bogus")
	requireCallErrorContains(t, script, "bad_unit", nil, CallOptions{},
		"unexpected unit: picosecond")
	// An explicitly-supplied nil subsecond is non-numeric and rejected, matching
	// Ruby's Time.at(0, nil) and Time.at(0, nil, :nsec) TypeError. Unlike the
	// calendar constructors (Time.utc/local), Time.at does not treat an explicit
	// nil subsecond as omitted.
	requireCallErrorContains(t, script, "nil_subsec_with_unit", nil, CallOptions{},
		"Time.at subsecond value must be numeric")
	requireCallErrorContains(t, script, "nil_subsec", nil, CallOptions{},
		"Time.at subsecond value must be numeric")
	// An explicit nil unit is an unrecognized unit, matching Ruby's
	// Time.at(0, 500, nil) ArgumentError ("unexpected unit: ").
	requireCallErrorContains(t, script, "nil_unit", nil, CallOptions{},
		"unexpected unit: ")

	// A subsecond value that exceeds one second still carries into the seconds,
	// matching Ruby, as long as the scaled nanosecond count fits in an int64.
	if carried := callFunc(t, script, "subsec_carries_into_seconds", nil); !carried.Equal(NewInt(6)) {
		t.Fatalf("subsec_carries_into_seconds = %v, want 6", carried)
	}

	// A subsecond magnitude whose scaled nanosecond count overflows int64 is
	// rejected rather than silently wrapped into a bogus instant.
	requireCallErrorContains(t, script, "subsec_scaling_overflows", nil, CallOptions{},
		"Time.at subsecond value out of range")
	requireCallErrorContains(t, script, "float_subsec_overflows", nil, CallOptions{},
		"Time.at subsecond value out of range")
}

func TestTimeSpaceshipComparison(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def compare_times()
      earlier = Time.utc(2024, 1, 2)
      same = Time.utc(2024, 1, 2)
      later = Time.utc(2024, 1, 3)
      [
        earlier <=> later,
        later <=> earlier,
        earlier <=> same,
        earlier.<=>(later),
        earlier.eql?(same),
        earlier < later,
        earlier <= same,
        later > earlier,
        same >= earlier
      ]
    end
    `)

	result := callFunc(t, script, "compare_times", nil)
	compareArrays(t, result, []Value{
		NewInt(-1),
		NewInt(1),
		NewInt(0),
		NewInt(-1),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
	})
}

func TestTimeToArray(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want []Value
	}{
		{
			name: "utc matches ruby field order",
			expr: `Time.utc(2024, 1, 2, 3, 4, 5).to_a`,
			want: []Value{
				NewInt(5), NewInt(4), NewInt(3), NewInt(2), NewInt(1), NewInt(2024),
				NewInt(2), NewInt(2), NewBool(false), NewString("UTC"),
			},
		},
		{
			name: "standard time reports zone and no dst",
			expr: `Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "America/New_York").to_a`,
			want: []Value{
				NewInt(5), NewInt(4), NewInt(3), NewInt(2), NewInt(1), NewInt(2024),
				NewInt(2), NewInt(2), NewBool(false), NewString("EST"),
			},
		},
		{
			name: "daylight time reports dst and adjusted yday",
			expr: `Time.parse("2024-07-02 03:04:05", "2006-01-02 15:04:05", in: "America/New_York").to_a`,
			want: []Value{
				NewInt(5), NewInt(4), NewInt(3), NewInt(2), NewInt(7), NewInt(2024),
				NewInt(2), NewInt(184), NewBool(true), NewString("EDT"),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

// TestTimeToArrayRejectsArguments documents that to_a is a plain accessor
// returning an Array, so supplying arguments tries to call that result like a
// function, matching the behavior of the other scalar Time accessors.
func TestTimeToArrayRejectsArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      Time.utc(2024, 1, 2).to_a(1)
    end
    `)
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "attempted to call non-callable value")
}

func TestTimeRoundPrecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "no argument rounds to seconds", expr: "t.round.to_s", want: "1970-01-01T00:00:00Z"},
		{name: "explicit zero rounds to seconds", expr: "t.round(0).to_s", want: "1970-01-01T00:00:00Z"},
		{name: "tenths precision", expr: "t.round(1).to_s", want: "1970-01-01T00:00:00.1Z"},
		{name: "millisecond precision", expr: "t.round(3).to_s", want: "1970-01-01T00:00:00.123Z"},
		{name: "microsecond precision", expr: "t.round(6).to_s", want: "1970-01-01T00:00:00.123456Z"},
		{name: "nanosecond precision is the cap", expr: "t.round(9).to_s", want: "1970-01-01T00:00:00.123456Z"},
		{name: "precision beyond nanosecond is unchanged", expr: "t.round(12).to_s", want: "1970-01-01T00:00:00.123456Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.parse("1970-01-01T00:00:00.123456Z")
		      `+tc.expr+`
		    end
		    `)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString || result.String() != tc.want {
				t.Fatalf("round result mismatch: got %v want %q", result, tc.want)
			}
		})
	}
}

func TestTimeRoundHalfwayRoundsAwayFromZero(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def run()
	      {
	        half_second: Time.parse("1970-01-01T00:00:00.5Z").round.to_s,
	        carry: Time.parse("1970-01-01T00:00:00.9999995Z").round(6).to_s,
	        round_up_tenths: Time.parse("1970-01-01T00:00:00.456Z").round(1).to_s
	      }
	    end
	    `)
	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	expect := map[string]string{
		"half_second":     "1970-01-01T00:00:01Z",
		"carry":           "1970-01-01T00:00:01Z",
		"round_up_tenths": "1970-01-01T00:00:00.5Z",
	}
	for key, want := range expect {
		if val, ok := got[key]; !ok || val.String() != want {
			t.Fatalf("round %s mismatch: got %v want %q", key, val, want)
		}
	}
}

func TestTimeRoundRejectsInvalidPrecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "negative precision", expr: "t.round(-1)", want: "time.round precision must be non-negative"},
		{name: "string precision", expr: `t.round("3")`, want: "time.round precision must be an Integer"},
		{name: "float precision", expr: "t.round(1.5)", want: "time.round precision must be an Integer"},
		{name: "too many arguments", expr: "t.round(0, 0)", want: "time.round expects at most one precision argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.parse("1970-01-01T00:00:00.123456Z")
		      `+tc.expr+`
		    end
		    `)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestTimeGetlocalAndLocaltimeOffsets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "getlocal positive offset",
			expr: `t.getlocal("+05:30").format("2006-01-02T15:04:05 -0700")`,
			want: "1970-01-01T05:30:00 +0530",
		},
		{
			name: "getlocal negative offset",
			expr: `t.getlocal("-04:00").format("2006-01-02T15:04:05 -0700")`,
			want: "1969-12-31T20:00:00 -0400",
		},
		{
			name: "localtime positive offset",
			expr: `t.localtime("+05:30").format("2006-01-02T15:04:05 -0700")`,
			want: "1970-01-01T05:30:00 +0530",
		},
		{
			name: "localtime negative offset",
			expr: `t.localtime("-04:00").format("2006-01-02T15:04:05 -0700")`,
			want: "1969-12-31T20:00:00 -0400",
		},
		{
			name: "named zone conversion",
			expr: `t.getlocal("America/New_York").format("2006-01-02T15:04:05 MST")`,
			want: "1969-12-31T19:00:00 EST",
		},
		{
			name: "utc string offset",
			expr: `t.localtime("UTC").to_s`,
			want: "1970-01-01T00:00:00Z",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.utc(1970, 1, 1, 0, 0, 0)
		      `+tc.expr+`
		    end
		    `)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString || result.String() != tc.want {
				t.Fatalf("conversion result mismatch: got %v want %q", result, tc.want)
			}
		})
	}
}

// TestTimeGetlocalDefaultsToHostLocal checks that the no-argument and nil
// forms convert to the host's local zone while preserving the instant, both
// as property-style access (auto-invoked) and as explicit calls.
func TestTimeGetlocalDefaultsToHostLocal(t *testing.T) {
	t.Parallel()
	instant := time.Unix(0, 0)
	_, wantOffset := instant.In(time.Local).Zone()
	cases := []struct {
		name string
		expr string
	}{
		{name: "getlocal property style", expr: "t.getlocal.utc_offset"},
		{name: "getlocal call style", expr: "t.getlocal().utc_offset"},
		{name: "getlocal nil argument", expr: "t.getlocal(nil).utc_offset"},
		{name: "localtime property style", expr: "t.localtime.utc_offset"},
		{name: "localtime call style", expr: "t.localtime().utc_offset"},
		{name: "localtime nil argument", expr: "t.localtime(nil).utc_offset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.utc(1970, 1, 1, 0, 0, 0)
		      [t.getlocal.to_i, `+tc.expr+`]
		    end
		    `)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, []Value{NewInt(0), NewInt(int64(wantOffset))})
		})
	}
}

func TestTimeGetlocalRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "integer offset", expr: "t.getlocal(123)", want: "invalid timezone spec"},
		{name: "unknown zone", expr: `t.getlocal("Not/AZone")`, want: `invalid timezone "Not/AZone"`},
		{name: "too many arguments", expr: `t.getlocal("+05:30", "+06:00")`, want: "getlocal expects at most one timezone offset argument"},
		{name: "localtime too many arguments", expr: `t.localtime("+05:30", "+06:00")`, want: "localtime expects at most one timezone offset argument"},
		{name: "keyword offset", expr: `t.getlocal(offset: "+05:30")`, want: "getlocal does not take keyword arguments"},
		{name: "localtime keyword", expr: `t.localtime(in: "UTC")`, want: "localtime does not take keyword arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.utc(1970, 1, 1, 0, 0, 0)
		      `+tc.expr+`
		    end
		    `)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

func TestTimeParseCommonLayouts(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def parse_common()
      {
        slash_date: Time.parse("2024/01/02", in: "UTC").to_s,
        slash_datetime: Time.parse("2024/01/02 03:04:05", in: "UTC").to_s,
        us_date: Time.parse("01/02/2024", in: "UTC").to_s,
        us_datetime: Time.parse("01/02/2024 03:04:05", in: "UTC").to_s,
        iso_no_zone: Time.parse("2024-01-02T03:04:05", in: "UTC").to_s,
        rfc1123: Time.parse("Tue, 02 Jan 2024 03:04:05 UTC").to_s
      }
    end
    `)

	result := callFunc(t, script, "parse_common", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	expect := map[string]string{
		"slash_date":     "2024-01-02T00:00:00Z",
		"slash_datetime": "2024-01-02T03:04:05Z",
		"us_date":        "2024-01-02T00:00:00Z",
		"us_datetime":    "2024-01-02T03:04:05Z",
		"iso_no_zone":    "2024-01-02T03:04:05Z",
		"rfc1123":        "2024-01-02T03:04:05Z",
	}
	for key, want := range expect {
		val, ok := got[key]
		if !ok {
			t.Fatalf("missing key %s", key)
		}
		if val.Kind() != KindString || val.String() != want {
			t.Fatalf("%s mismatch: got %v want %s", key, val, want)
		}
	}
}

func TestJSONBuiltins(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def parse_payload()
      JSON.parse("{\"name\":\"alex\",\"score\":10,\"tags\":[\"x\",true,null],\"ratio\":1.5}")
    end

    def stringify_payload()
      payload = { name: "alex", score: 10, tags: ["x", true, nil], ratio: 1.5 }
      JSON.stringify(payload)
    end

    def parse_invalid()
      JSON.parse("{bad")
    end

    def stringify_unsupported()
      JSON.stringify({ fn: helper })
    end

    def helper(value)
      value
    end
    `)

	parsed := callFunc(t, script, "parse_payload", nil)
	if parsed.Kind() != KindHash {
		t.Fatalf("expected parsed payload hash, got %v", parsed.Kind())
	}
	obj := parsed.Hash()
	if !obj["name"].Equal(NewString("alex")) {
		t.Fatalf("name mismatch: %v", obj["name"])
	}
	if !obj["score"].Equal(NewInt(10)) {
		t.Fatalf("score mismatch: %v", obj["score"])
	}
	if obj["ratio"].Kind() != KindFloat || obj["ratio"].Float() != 1.5 {
		t.Fatalf("ratio mismatch: %v", obj["ratio"])
	}
	compareArrays(t, obj["tags"], []Value{NewString("x"), NewBool(true), NewNil()})

	stringified := callFunc(t, script, "stringify_payload", nil)
	if stringified.Kind() != KindString {
		t.Fatalf("expected JSON.stringify to return string, got %v", stringified.Kind())
	}
	if got := stringified.String(); got != `{"name":"alex","ratio":1.5,"score":10,"tags":["x",true,null]}` {
		t.Fatalf("stringify mismatch: %q", got)
	}

	requireCallErrorContains(t, script, "parse_invalid", nil, CallOptions{}, "JSON.parse invalid JSON")
	requireCallErrorContains(t, script, "stringify_unsupported", nil, CallOptions{}, "JSON.stringify unsupported value type function")
}

func TestJSONStringifyEscaping(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def stringify_value(value)
      JSON.stringify(value)
    end
    `)

	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{name: "html_sensitive", value: NewString("<>&"), want: `"\u003c\u003e\u0026"`},
		{name: "control_characters", value: NewString("line\n\t"), want: `"line\n\t"`},
		{name: "line_separators", value: NewString("\u2028\u2029"), want: `"\u2028\u2029"`},
		{name: "invalid_utf8", value: NewString("bad\xff"), want: `"bad\ufffd"`},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, "stringify_value", []Value{tc.value})
			if got.Kind() != KindString || got.String() != tc.want {
				t.Fatalf("JSON.stringify(%q) = %q, want %q", tc.value.String(), got.String(), tc.want)
			}
		})
	}
}

func TestJSONStringifyFloatFormattingMatchesJSON(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def stringify_value(value)
      JSON.stringify(value)
    end
    `)

	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{name: "million_fixed", value: NewFloat(1e6), want: `1000000`},
		{name: "micro_fixed", value: NewFloat(1e-6), want: `0.000001`},
		{name: "smaller_than_micro_exponent", value: NewFloat(1e-7), want: `1e-7`},
		{name: "large_fixed", value: NewFloat(1e20), want: `100000000000000000000`},
		{name: "larger_exponent", value: NewFloat(1e21), want: `1e+21`},
		{name: "negative_smaller_than_micro_exponent", value: NewFloat(-1e-7), want: `-1e-7`},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callFunc(t, script, "stringify_value", []Value{tc.value})
			if got.Kind() != KindString || got.String() != tc.want {
				t.Fatalf("JSON.stringify(%s) = %q, want %q", tc.value.String(), got.String(), tc.want)
			}
		})
	}
}

func TestJSONParseEscapesAndNumbers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def parse_payload()
      JSON.parse("{\"newline\":\"line\\n\",\"emoji\":\"\\ud83d\\ude00\",\"bad\":\"\\ud800\",\"int\":9223372036854775808,\"float\":1e2}")
    end
    `)

	parsed := callFunc(t, script, "parse_payload", nil)
	if parsed.Kind() != KindHash {
		t.Fatalf("parse_payload() = %s, want hash", parsed.Kind())
	}
	obj := parsed.Hash()
	if got, want := obj["newline"], NewString("line\n"); !got.Equal(want) {
		t.Fatalf("newline = %q, want %q", got.String(), want.String())
	}
	if got, want := obj["emoji"], NewString("😀"); !got.Equal(want) {
		t.Fatalf("emoji = %q, want %q", got.String(), want.String())
	}
	if got, want := obj["bad"], NewString(string(utf8.RuneError)); !got.Equal(want) {
		t.Fatalf("bad surrogate = %q, want %q", got.String(), want.String())
	}
	if got, want := obj["int"], NewFloat(9223372036854775808); !got.Equal(want) {
		t.Fatalf("overflow integer = %s, want %s", got, want)
	}
	if got, want := obj["float"], NewFloat(100); !got.Equal(want) {
		t.Fatalf("float = %s, want %s", got, want)
	}
}

func TestJSONRejectsExcessiveNesting(t *testing.T) {
	t.Parallel()

	tooDeepJSON := strings.Repeat("[", maxJSONNestingDepth+1) + "0" + strings.Repeat("]", maxJSONNestingDepth+1)
	_, err := builtinJSONParse(nil, NewNil(), []Value{NewString(tooDeepJSON)}, nil, NewNil())
	if err == nil || !strings.Contains(err.Error(), "exceeded max depth") {
		t.Fatalf("JSON.parse(deep array) error = %v, want exceeded max depth", err)
	}

	value := NewInt(0)
	for range maxJSONNestingDepth + 1 {
		value = NewArray([]Value{value})
	}
	_, err = builtinJSONStringify(nil, NewNil(), []Value{value}, nil, NewNil())
	if err == nil || !strings.Contains(err.Error(), "exceeded max depth") {
		t.Fatalf("JSON.stringify(deep array) error = %v, want exceeded max depth", err)
	}
}

func TestRegexBuiltins(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def helpers()
	      {
	        match_hit: Regex.match("ID-[0-9]+", "ID-12 ID-34"),
	        match_miss: Regex.match("Z+", "ID-12"),
	        match_empty: Regex.match("^", "ID-12"),
	        replace_one: Regex.replace("ID-12 ID-34", "ID-[0-9]+", "X"),
	        replace_all: Regex.replace_all("ID-12 ID-34", "ID-[0-9]+", "X"),
	        replace_all_adjacent: Regex.replace_all("aaaa", "aa", "X"),
	        replace_all_anchor: Regex.replace_all("abc", "^", "X"),
	        replace_all_boundary: Regex.replace_all("ab", "\\b", "X"),
	        replace_all_abutting_empty: Regex.replace_all("aa", "aa|", "X"),
	        replace_capture: Regex.replace("ID-12 ID-34", "ID-([0-9]+)", "X-$1"),
	        replace_boundary: Regex.replace("ab", "\\Bb", "X")
	      }
    end

    def invalid_regex()
      Regex.match("[", "abc")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	out := result.Hash()
	if !out["match_hit"].Equal(NewString("ID-12")) {
		t.Fatalf("match_hit mismatch: %v", out["match_hit"])
	}
	if out["match_miss"].Kind() != KindNil {
		t.Fatalf("expected match_miss nil, got %v", out["match_miss"])
	}
	if !out["match_empty"].Equal(NewString("")) {
		t.Fatalf("match_empty mismatch: %#v", out["match_empty"])
	}

	stringCases := map[string]string{
		"replace_one":                "X ID-34",
		"replace_all":                "X X",
		"replace_all_adjacent":       "XX",
		"replace_all_anchor":         "Xabc",
		"replace_all_boundary":       "XabX",
		"replace_all_abutting_empty": "X",
		"replace_capture":            "X-12 ID-34",
		"replace_boundary":           "aX",
	}
	for key, want := range stringCases {
		if !out[key].Equal(NewString(want)) {
			t.Fatalf("%s mismatch: %v", key, out[key])
		}
	}

	requireCallErrorContains(t, script, "invalid_regex", nil, CallOptions{}, "Regex.match invalid regex")
}

func TestJSONAndRegexMalformedInputs(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def bad_json_trailing()
      JSON.parse("{\"a\":1}{\"b\":2}")
    end

    def bad_json_syntax()
      JSON.parse("{\"a\":")
    end

    def bad_json_number()
      JSON.parse("1e10000")
    end

    def bad_regex_replace()
      Regex.replace("abc", "[", "x")
    end

    def bad_regex_replace_all()
      Regex.replace_all("abc", "[", "x")
    end
    `)

	cases := []struct {
		name string
		fn   string
		want string
	}{
		{name: "json_trailing", fn: "bad_json_trailing", want: "JSON.parse invalid JSON: trailing data"},
		{name: "json_syntax", fn: "bad_json_syntax", want: "JSON.parse invalid JSON"},
		{name: "json_number", fn: "bad_json_number", want: "JSON.parse invalid number"},
		{name: "regex_replace", fn: "bad_regex_replace", want: "Regex.replace invalid regex"},
		{name: "regex_replace_all", fn: "bad_regex_replace_all", want: "Regex.replace_all invalid regex"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestJSONAndRegexSizeGuards(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4 << 20}, `
    def parse_raw(raw)
      JSON.parse(raw)
    end

    def stringify_value(value)
      JSON.stringify(value)
    end

    def regex_match_guard(pattern, text)
      Regex.match(pattern, text)
    end

    def regex_replace_all_guard(text, pattern, replacement)
      Regex.replace_all(text, pattern, replacement)
    end
    `)

	largeJSON := `{"data":"` + strings.Repeat("x", maxJSONPayloadBytes) + `"}`
	requireCallErrorContains(t, script, "parse_raw", []Value{NewString(largeJSON)}, CallOptions{}, "JSON.parse input exceeds limit")

	largeValue := NewHash(map[string]Value{
		"data": NewString(strings.Repeat("x", maxJSONPayloadBytes)),
	})
	requireCallErrorContains(t, script, "stringify_value", []Value{largeValue}, CallOptions{}, "JSON.stringify output exceeds limit")

	largePattern := strings.Repeat("a", maxRegexPatternSize+1)
	requireCallErrorContains(t, script, "regex_match_guard", []Value{NewString(largePattern), NewString("aaa")}, CallOptions{}, "Regex.match pattern exceeds limit")

	largeText := strings.Repeat("a", maxRegexInputBytes+1)
	requireCallErrorContains(t, script, "regex_match_guard", []Value{NewString("a+"), NewString(largeText)}, CallOptions{}, "Regex.match text exceeds limit")

	hugeReplacement := strings.Repeat("x", maxRegexInputBytes/2)
	requireCallErrorContains(t, script, "regex_replace_all_guard", []Value{NewString("abc"), NewString(""), NewString(hugeReplacement)}, CallOptions{}, "Regex.replace_all output exceeds limit")

	largeRun := strings.Repeat("a", maxRegexInputBytes-1024)
	replaced, err := script.Call(
		context.Background(),
		"regex_replace_all_guard",
		[]Value{NewString(largeRun), NewString("(a)[a]*"), NewString("$1$1")},
		CallOptions{},
	)
	if err != nil {
		t.Fatalf("expected large capture replacement to succeed, got %v", err)
	}
	if replaced.Kind() != KindString || replaced.String() != "aa" {
		t.Fatalf("expected capture replacement to produce \"aa\", got %v", replaced)
	}
}

func TestLocaleSensitiveOperationsDeterministic(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def locale_ops()
      {
        up_i: "i".upcase,
        down_i_dot: "İ".downcase,
        sorted_words: ["ä", "z", "a"].sort,
        sorted_case: ["b", "A", "a"].sort
      }
    end
    `)

	result := callFunc(t, script, "locale_ops", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	out := result.Hash()
	if !out["up_i"].Equal(NewString("I")) {
		t.Fatalf("up_i mismatch: %v", out["up_i"])
	}
	// Full Unicode case mapping lowercases the dotted capital I (U+0130) to
	// "i" plus a combining dot above (U+0307), matching Ruby. The result is
	// the same regardless of host locale, which is what this test guards.
	if !out["down_i_dot"].Equal(NewString("i̇")) {
		t.Fatalf("down_i_dot mismatch: %q", out["down_i_dot"].String())
	}
	compareArrays(t, out["sorted_words"], []Value{NewString("a"), NewString("z"), NewString("ä")})
	compareArrays(t, out["sorted_case"], []Value{NewString("A"), NewString("a"), NewString("b")})
}

func TestRandomIdentifierBuiltins(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{
		RandomReader: bytes.NewReader(bytes.Repeat([]byte{0xAB}, 128)),
	}, `
    def values()
      {
        uuid: uuid(),
        id8: random_id(8),
        id_default: random_id()
      }
    end

	    def bad_length_type()
	      random_id("x")
	    end

	    def bad_length_float()
	      random_id(8.9)
	    end

	    def bad_length_value()
	      random_id(0)
	    end

    def bad_uuid_args()
      uuid(1)
    end
    `)

	result, err := script.Call(context.Background(), "values", nil, CallOptions{})
	if err != nil {
		t.Fatalf("values call failed: %v", err)
	}
	if result.Kind() != KindHash {
		t.Fatalf("expected hash result, got %v", result.Kind())
	}
	out := result.Hash()
	uuidValue := out["uuid"]
	if uuidValue.Kind() != KindString {
		t.Fatalf("uuid should be string, got %v", uuidValue)
	}
	uuidText := uuidValue.String()
	if len(uuidText) != 36 {
		t.Fatalf("uuid length mismatch: %q", uuidText)
	}
	if uuidText[8] != '-' || uuidText[13] != '-' || uuidText[18] != '-' || uuidText[23] != '-' {
		t.Fatalf("uuid separator mismatch: %q", uuidText)
	}
	if uuidText[14] != '7' {
		t.Fatalf("uuid version mismatch: %q", uuidText)
	}
	if !strings.HasSuffix(uuidText, "-7bab-abab-abababababab") {
		t.Fatalf("uuid random suffix mismatch: %q", uuidText)
	}
	if !out["id8"].Equal(NewString("VVVVVVVV")) {
		t.Fatalf("id8 mismatch: %v", out["id8"])
	}
	if got := out["id_default"]; got.Kind() != KindString || got.String() != "VVVVVVVVVVVVVVVV" {
		t.Fatalf("id_default mismatch: %v", got)
	}

	errorCases := []struct {
		name string
		fn   string
		want string
	}{
		{name: "bad_length_type", fn: "bad_length_type", want: "random_id length must be integer"},
		{name: "bad_length_float", fn: "bad_length_float", want: "random_id length must be integer"},
		{name: "bad_length_value", fn: "bad_length_value", want: "random_id length must be positive"},
		{name: "bad_uuid_args", fn: "bad_uuid_args", want: "uuid does not take arguments"},
	}
	for _, tc := range errorCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

func TestRandomIdentifierBuiltinsRandomSourceFailure(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{RandomReader: bytes.NewReader([]byte{1, 2, 3})}, `
    def run()
      uuid()
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "random source failed")
}

func TestRandomIdentifierBuiltinsUsesUnbiasedSampling(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{RandomReader: bytes.NewReader([]byte{248, 1})}, `
    def run()
      random_id(1)
    end
    `)

	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !got.Equal(NewString("b")) {
		t.Fatalf("expected unbiased sample to produce b, got %v", got)
	}
}

func TestRandomIdentifierBuiltinsRejectsStalledEntropy(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{RandomReader: bytes.NewReader(bytes.Repeat([]byte{0xFF}, 1024))}, `
    def run()
      random_id(4)
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "random_id entropy source rejected too many bytes")
}

func TestDurationHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def minutes()
      (90.seconds).minutes
    end

    def hours()
      (7200.seconds).hours
    end

    def format()
      (2.hours).format
    end
    `)

	minutes := callFunc(t, script, "minutes", nil)
	if !minutes.Equal(NewInt(1)) {
		t.Fatalf("minutes mismatch: %#v", minutes)
	}
	hours := callFunc(t, script, "hours", nil)
	if !hours.Equal(NewInt(2)) {
		t.Fatalf("hours mismatch: %#v", hours)
	}
	formatted := callFunc(t, script, "format", nil)
	if formatted.Kind() != KindString || formatted.String() != "7200s" {
		t.Fatalf("format mismatch: %#v", formatted)
	}
}

func TestNowBuiltin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		script := compileScript(t, `
	    def current()
	      {
	        now: now(),
	        time_now: Time.now(in: "UTC").to_s,
	        from_now: 90.seconds.from_now().to_s,
	        ago: 90.seconds.ago().to_s
	      }
	    end
	    `)

		result := callFunc(t, script, "current", nil)
		if result.Kind() != KindHash {
			t.Fatalf("expected hash, got %v", result.Kind())
		}
		got := result.Hash()
		want := map[string]string{
			"now":      "2000-01-01T00:00:00Z",
			"time_now": "2000-01-01T00:00:00Z",
			"from_now": "2000-01-01T00:01:30Z",
			"ago":      "1999-12-31T23:58:30Z",
		}
		for key, wantValue := range want {
			val, ok := got[key]
			if !ok {
				t.Fatalf("current() missing %s", key)
			}
			if val.Kind() != KindString || val.String() != wantValue {
				t.Fatalf("current()[%s] = %v, want %s", key, val, wantValue)
			}
		}
	})
}

func TestTimeISO8601Precision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "no argument keeps whole seconds", expr: "t.iso8601", want: "1970-01-01T00:00:00Z"},
		{name: "no argument with parentheses", expr: "t.iso8601()", want: "1970-01-01T00:00:00Z"},
		{name: "explicit zero keeps whole seconds", expr: "t.iso8601(0)", want: "1970-01-01T00:00:00Z"},
		{name: "millisecond precision", expr: "t.iso8601(3)", want: "1970-01-01T00:00:00.123Z"},
		{name: "microsecond precision", expr: "t.iso8601(6)", want: "1970-01-01T00:00:00.123456Z"},
		{name: "fractional digits truncate toward zero", expr: "t.iso8601(5)", want: "1970-01-01T00:00:00.12345Z"},
		{name: "nanosecond precision", expr: "t.iso8601(9)", want: "1970-01-01T00:00:00.123456000Z"},
		{name: "sub-nanosecond digits zero pad", expr: "t.iso8601(12)", want: "1970-01-01T00:00:00.123456000000Z"},
		{name: "rfc3339 mirrors iso8601 precision", expr: "t.rfc3339(3)", want: "1970-01-01T00:00:00.123Z"},
		{name: "rfc3339 without argument", expr: "t.rfc3339", want: "1970-01-01T00:00:00Z"},
		{name: "xmlschema aliases iso8601", expr: "t.xmlschema", want: "1970-01-01T00:00:00Z"},
		{name: "xmlschema with parentheses", expr: "t.xmlschema()", want: "1970-01-01T00:00:00Z"},
		{name: "xmlschema mirrors iso8601 precision", expr: "t.xmlschema(6)", want: "1970-01-01T00:00:00.123456Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.parse("1970-01-01T00:00:00.123456Z")
		      `+tc.expr+`
		    end
		    `)
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString || result.String() != tc.want {
				t.Fatalf("iso8601 result mismatch: got %v want %q", result, tc.want)
			}
		})
	}
}

func TestTimeISO8601PreservesOffset(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
	    def run()
	      t = Time.parse("2020-03-04 05:06:07", "2006-01-02 15:04:05", in: "America/New_York")
	      {
	        iso0: t.iso8601,
	        iso3: t.iso8601(3)
	      }
	    end
	    `)
	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	expect := map[string]string{
		"iso0": "2020-03-04T05:06:07-05:00",
		"iso3": "2020-03-04T05:06:07.000-05:00",
	}
	for key, want := range expect {
		if val, ok := got[key]; !ok || val.String() != want {
			t.Fatalf("iso8601 %s mismatch: got %v want %q", key, val, want)
		}
	}
}

func TestTimeISO8601RejectsInvalidPrecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "negative precision", expr: "t.iso8601(-1)", want: "time.iso8601 precision must be non-negative"},
		{name: "string precision", expr: `t.iso8601("3")`, want: "time.iso8601 precision must be an Integer"},
		{name: "float precision", expr: "t.iso8601(1.5)", want: "time.iso8601 precision must be an Integer"},
		{name: "too many arguments", expr: "t.iso8601(0, 0)", want: "time.iso8601 expects at most one precision argument"},
		{name: "precision beyond maximum", expr: "t.iso8601(101)", want: "time.iso8601 precision exceeds maximum 100 digits"},
		{name: "rfc3339 negative precision", expr: "t.rfc3339(-1)", want: "time.rfc3339 precision must be non-negative"},
		{name: "xmlschema string precision", expr: `t.xmlschema("3")`, want: "time.xmlschema precision must be an Integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.parse("1970-01-01T00:00:00.123456Z")
		      `+tc.expr+`
		    end
		    `)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}

// TestTimeHTTPDateAndRFC2822 checks the Ruby-aligned HTTP-date and RFC 2822
// helpers across UTC, explicit-zero, and offset receivers, including the
// "-0000" zone Ruby reserves for genuine UTC and the GMT conversion httpdate
// always performs. Each want value was captured from MRI's `require "time"`.
func TestTimeHTTPDateAndRFC2822(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "httpdate utc", expr: `Time.utc(2024, 1, 2, 3, 4, 5).httpdate`, want: "Tue, 02 Jan 2024 03:04:05 GMT"},
		{name: "httpdate with parentheses", expr: `Time.utc(2024, 1, 2, 3, 4, 5).httpdate()`, want: "Tue, 02 Jan 2024 03:04:05 GMT"},
		{name: "httpdate drops subseconds", expr: `Time.utc(2024, 1, 2, 3, 4, 5, 123456).httpdate`, want: "Tue, 02 Jan 2024 03:04:05 GMT"},
		{
			name: "httpdate converts offset to gmt",
			expr: `Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "+05:30").httpdate`,
			want: "Mon, 01 Jan 2024 21:34:05 GMT",
		},
		{name: "rfc2822 utc uses minus zero zone", expr: `Time.utc(2024, 1, 2, 3, 4, 5).rfc2822`, want: "Tue, 02 Jan 2024 03:04:05 -0000"},
		{name: "rfc822 aliases rfc2822", expr: `Time.utc(2024, 1, 2, 3, 4, 5).rfc822`, want: "Tue, 02 Jan 2024 03:04:05 -0000"},
		{name: "rfc2822 drops subseconds", expr: `Time.utc(2024, 1, 2, 3, 4, 5, 123456).rfc2822`, want: "Tue, 02 Jan 2024 03:04:05 -0000"},
		{
			name: "rfc2822 explicit zero offset uses plus zero zone",
			expr: `Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "+00:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 +0000",
		},
		{
			name: "rfc2822 preserves offset",
			expr: `Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "+05:30").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 +0530",
		},
		{
			name: "rfc2822 negative offset",
			expr: `Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "-05:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0500",
		},
		{
			// Ruby treats an explicit negative-zero "-00:00" offset as the RFC
			// 2822 unknown-zone marker and renders "-0000", distinct from the
			// positive "+00:00" zone. Captured from MRI Time.new(..., "-00:00").
			name: "rfc2822 explicit negative zero offset uses minus zero zone",
			expr: `Time.new(2024, 1, 2, 3, 4, 5, "-00:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0000",
		},
		{
			name: "rfc2822 explicit positive zero offset uses plus zero zone",
			expr: `Time.new(2024, 1, 2, 3, 4, 5, "+00:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 +0000",
		},
		{
			// getlocal("-00:00") shifts a receiver into the negative-zero zone,
			// which Ruby renders with the "-0000" unknown-zone marker.
			name: "rfc2822 getlocal negative zero offset uses minus zero zone",
			expr: `Time.utc(2024, 1, 2, 3, 4, 5).getlocal("-00:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0000",
		},
		{
			// A named UTC zone is genuine UTC, so it keeps the "-0000" marker
			// even though its location is not the time.UTC singleton.
			name: "rfc2822 named utc zone uses minus zero zone",
			expr: `Time.new(2024, 1, 2, 3, 4, 5, "UTC").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0000",
		},
		{
			// Time.parse of an RFC 3339 input ending in "-00:00" carries the
			// negative-zero unknown-zone marker. Go's parser drops the leading
			// "-", so ParseTimeString re-anchors it to the canonical negative-zero
			// zone. Captured from MRI Time.parse(...).rfc2822.
			name: "rfc2822 parsed rfc3339 negative zero offset uses minus zero zone",
			expr: `Time.parse("2024-01-02T03:04:05-00:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0000",
		},
		{
			// An RFC 1123Z "-0000" input is likewise the unknown-zone marker.
			name: "rfc2822 parsed rfc1123z negative zero offset uses minus zero zone",
			expr: `Time.parse("Tue, 02 Jan 2024 03:04:05 -0000").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0000",
		},
		{
			// A parsed "+00:00" offset is a genuine zero offset, not the
			// unknown-zone marker, so it renders "+0000".
			name: "rfc2822 parsed positive zero offset uses plus zero zone",
			expr: `Time.parse("2024-01-02T03:04:05+00:00").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 +0000",
		},
		{
			// A trailing "Z" is genuine UTC and keeps the "-0000" marker.
			name: "rfc2822 parsed zulu uses minus zero zone",
			expr: `Time.parse("2024-01-02T03:04:05Z").rfc2822`,
			want: "Tue, 02 Jan 2024 03:04:05 -0000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString || result.String() != tc.want {
				t.Fatalf("result mismatch: got %v want %q", result, tc.want)
			}
		})
	}
}

// TestIsUTCModeMatchesUTCSingletonAndNegativeZero guards the RFC 2822 zone
// classifier. On UTC-environment hosts time.Local reports the zone name "UTC"
// with a zero offset, yet Time.local/Time.now/Time.at receivers are still local
// times that Ruby renders as "+0000". UTC mode (which earns the "-0000" marker)
// is the canonical time.UTC singleton (Time.utc/getutc and the "UTC"/"GMT"/"Z"
// specs) plus the negative-zero offset zone "-00:00", whose name preserves the
// sign Ruby reads as the RFC 2822 unknown-zone marker. The positive "+00:00"
// zone stays "+0000".
func TestIsUTCModeMatchesUTCSingletonAndNegativeZero(t *testing.T) {
	t.Parallel()
	base := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name string
		loc  *time.Location
		want bool
	}{
		{name: "utc singleton", loc: time.UTC, want: true},
		// A distinct location that mimics a UTC container's time.Local: named
		// "UTC" with a zero offset but not the singleton.
		{name: "zero offset zone named UTC", loc: time.FixedZone("UTC", 0), want: false},
		{name: "explicit positive zero offset", loc: time.FixedZone("+00:00", 0), want: false},
		// The negative-zero offset "-00:00" parses to a zero-offset zone whose
		// name begins with "-"; Ruby treats it as RFC 2822 UTC mode.
		{name: "explicit negative zero offset", loc: time.FixedZone("-00:00", 0), want: true},
		{name: "named gmt zone", loc: time.FixedZone("GMT", 0), want: false},
		{name: "positive offset", loc: time.FixedZone("+05:30", 5*3600+30*60), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isUTCMode(base.In(tc.loc)); got != tc.want {
				t.Fatalf("isUTCMode(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestTimeRFC2822LocalUTCHostUsesPlusZero exercises the full Time.local/Time.now
// path on a simulated UTC-environment host, where the host's local zone is named
// "UTC" with a zero offset. Ruby treats these as local times and emits "+0000",
// not the "-0000" UTC-mode marker. Overriding time.Local makes this test
// non-parallel; it restores the original value before returning.
func TestTimeRFC2822LocalUTCHostUsesPlusZero(t *testing.T) {
	original := time.Local
	t.Cleanup(func() { time.Local = original })
	// Simulate a UTC container: time.Local is a zero-offset zone named "UTC"
	// that is not the time.UTC singleton.
	time.Local = time.FixedZone("UTC", 0)

	cases := []struct {
		name string
		expr string
	}{
		{name: "Time.local", expr: `Time.local(2024, 1, 2, 3, 4, 5).rfc2822`},
		{name: "Time.mktime", expr: `Time.mktime(2024, 1, 2, 3, 4, 5).rfc2822`},
		{name: "getlocal default", expr: `Time.utc(2024, 1, 2, 3, 4, 5).getlocal.rfc2822`},
	}
	const want = "Tue, 02 Jan 2024 03:04:05 +0000"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			result := callFunc(t, script, "run", nil)
			if result.Kind() != KindString || result.String() != want {
				t.Fatalf("result mismatch: got %v want %q", result, want)
			}
		})
	}
}

// TestTimeHTTPDateAndRFC2822RejectArguments documents that the argument-free
// mail/HTTP date helpers reject any positional or keyword argument rather than
// silently ignoring it.
func TestTimeHTTPDateAndRFC2822RejectArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "httpdate positional", expr: "t.httpdate(0)", want: "time.httpdate does not accept arguments"},
		{name: "httpdate keyword", expr: "t.httpdate(in: \"UTC\")", want: "time.httpdate does not accept keyword arguments"},
		{name: "rfc2822 positional", expr: "t.rfc2822(0)", want: "time.rfc2822 does not accept arguments"},
		{name: "rfc822 positional", expr: "t.rfc822(0)", want: "time.rfc822 does not accept arguments"},
		{name: "rfc2822 keyword", expr: "t.rfc2822(in: \"UTC\")", want: "time.rfc2822 does not accept keyword arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
		    def run()
		      t = Time.utc(2024, 1, 2, 3, 4, 5)
		      `+tc.expr+`
		    end
		    `)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}
