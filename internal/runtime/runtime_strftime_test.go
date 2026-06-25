package runtime

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// TestStrftimeDirectives exercises the Ruby-compatible directive subset across
// UTC, positive/negative fixed-offset, and subsecond receivers. The expected
// outputs were captured from MRI Ruby's Time#strftime so the table doubles as a
// parity record.
func TestStrftimeDirectives(t *testing.T) {
	t.Parallel()

	utc := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	afternoon := time.Date(2024, 7, 9, 15, 8, 6, 0, time.UTC)
	plus := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("+05:30", 5*3600+30*60))
	minus := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("-04:00", -4*3600))
	sub := time.Date(2024, 1, 2, 3, 4, 5, 123456789, time.UTC)
	sunday := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		tm     time.Time
		format string
		want   string
	}{
		// Date components.
		{name: "year", tm: utc, format: "%Y", want: "2024"},
		{name: "century", tm: utc, format: "%C", want: "20"},
		{name: "year in century", tm: utc, format: "%y", want: "24"},
		{name: "month", tm: utc, format: "%m", want: "01"},
		{name: "day zero padded", tm: utc, format: "%d", want: "02"},
		{name: "day blank padded", tm: utc, format: "%e", want: " 2"},
		{name: "day of year", tm: utc, format: "%j", want: "002"},
		{name: "day of year end", tm: time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC), format: "%j", want: "366"},

		// Time components.
		{name: "hour 24", tm: utc, format: "%H", want: "03"},
		{name: "hour 24 blank", tm: utc, format: "%k", want: " 3"},
		{name: "hour 24 pm", tm: afternoon, format: "%H", want: "15"},
		{name: "hour 12", tm: utc, format: "%I", want: "03"},
		{name: "hour 12 blank", tm: utc, format: "%l", want: " 3"},
		{name: "hour 12 pm", tm: afternoon, format: "%I", want: "03"},
		{name: "minute", tm: utc, format: "%M", want: "04"},
		{name: "second", tm: utc, format: "%S", want: "05"},
		{name: "meridian upper am", tm: utc, format: "%p", want: "AM"},
		{name: "meridian upper pm", tm: afternoon, format: "%p", want: "PM"},
		{name: "meridian lower am", tm: utc, format: "%P", want: "am"},
		{name: "meridian lower pm", tm: afternoon, format: "%P", want: "pm"},

		// Names.
		{name: "weekday full", tm: utc, format: "%A", want: "Tuesday"},
		{name: "weekday abbrev", tm: utc, format: "%a", want: "Tue"},
		{name: "month full", tm: utc, format: "%B", want: "January"},
		{name: "month abbrev", tm: utc, format: "%b", want: "Jan"},
		{name: "month abbrev h", tm: utc, format: "%h", want: "Jan"},

		// Weekday numbers.
		{name: "weekday number tuesday", tm: utc, format: "%w", want: "2"},
		{name: "weekday iso tuesday", tm: utc, format: "%u", want: "2"},
		{name: "weekday number sunday", tm: sunday, format: "%w", want: "0"},
		{name: "weekday iso sunday", tm: sunday, format: "%u", want: "7"},

		// Epoch and zone.
		{name: "epoch seconds", tm: utc, format: "%s", want: "1704164645"},
		{name: "offset utc", tm: utc, format: "%z", want: "+0000"},
		{name: "offset plus", tm: plus, format: "%z", want: "+0530"},
		{name: "offset plus colon", tm: plus, format: "%:z", want: "+05:30"},
		{name: "offset plus colon seconds", tm: plus, format: "%::z", want: "+05:30:00"},
		{name: "offset minus", tm: minus, format: "%z", want: "-0400"},
		{name: "offset minus colon", tm: minus, format: "%:z", want: "-04:00"},
		{name: "zone name utc", tm: utc, format: "%Z", want: "UTC"},
		// %Z mirrors Time#zone, which surfaces Go's offset zone name for
		// fixed-offset locations rather than Ruby's empty string.
		{name: "zone name offset", tm: plus, format: "%Z", want: "+05:30"},

		// Subsecond.
		{name: "milliseconds", tm: sub, format: "%L", want: "123"},
		{name: "nanoseconds default", tm: sub, format: "%N", want: "123456789"},
		{name: "subsec width 3", tm: sub, format: "%3N", want: "123"},
		{name: "subsec width 6", tm: sub, format: "%6N", want: "123456"},
		{name: "subsec width 9", tm: sub, format: "%9N", want: "123456789"},
		{name: "subsec width pads", tm: sub, format: "%12N", want: "123456789000"},
		{name: "subsec zero", tm: utc, format: "%N", want: "000000000"},

		// Literals and escapes.
		{name: "literal percent", tm: utc, format: "%%", want: "%"},
		{name: "newline", tm: utc, format: "%n", want: "\n"},
		{name: "tab", tm: utc, format: "%t", want: "\t"},
		{name: "surrounding literals", tm: utc, format: "[%Y]", want: "[2024]"},
		{name: "double percent in text", tm: utc, format: "100%% done", want: "100% done"},

		// Compound directives.
		{name: "iso date", tm: utc, format: "%F", want: "2024-01-02"},
		{name: "iso time", tm: utc, format: "%T", want: "03:04:05"},
		{name: "iso time alias", tm: utc, format: "%X", want: "03:04:05"},
		{name: "hour minute", tm: utc, format: "%R", want: "03:04"},
		{name: "us date", tm: utc, format: "%D", want: "01/02/24"},
		{name: "us date alias", tm: utc, format: "%x", want: "01/02/24"},
		{name: "twelve hour clock", tm: utc, format: "%r", want: "03:04:05 AM"},
		{name: "ctime", tm: utc, format: "%c", want: "Tue Jan  2 03:04:05 2024"},

		// Unknown directives pass through verbatim, matching Ruby.
		{name: "unknown directive", tm: utc, format: "%Q", want: "%Q"},
		{name: "unknown colon directive", tm: utc, format: "%:q", want: "%:q"},
		{name: "unknown double colon directive", tm: utc, format: "%::q", want: "%::q"},
		{name: "unknown directive with flags and width", tm: utc, format: "%-^6Q", want: "%-^6Q"},
		// Three or more colons on %z has no useful meaning and passes through.
		{name: "triple colon offset passes through", tm: plus, format: "%:::z", want: "%:::z"},

		// Combined real-world layout.
		{name: "combined layout", tm: plus, format: "%Y-%m-%d %H:%M:%S %z", want: "2024-01-02 03:04:05 +0530"},
		{name: "empty format", tm: utc, format: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := strftime(tc.tm, tc.format)
			if err != nil {
				t.Fatalf("strftime(%q) returned error: %v", tc.format, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("strftime(%q) mismatch (-want +got):\n%s", tc.format, diff)
			}
		})
	}
}

// TestStrftimeFlagsAndWidth exercises Ruby's directive flags (- _ 0 ^ #) and the
// optional field width across numeric, name, fractional, offset, literal, and
// compound directives. Every expected value was captured from MRI Ruby's
// Time#strftime, so the table doubles as a parity record for the flag grammar.
func TestStrftimeFlagsAndWidth(t *testing.T) {
	t.Parallel()

	utc := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	afternoon := time.Date(2024, 7, 9, 15, 8, 6, 0, time.UTC)
	plus := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("+05:30", 5*3600+30*60))
	sub := time.Date(2024, 1, 2, 3, 4, 5, 123456000, time.UTC)
	noZone := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("", 5*3600+30*60))

	cases := []struct {
		name   string
		tm     time.Time
		format string
		want   string
	}{
		// - flag drops padding on numeric directives.
		{name: "minus day", tm: utc, format: "%-d", want: "2"},
		{name: "minus month", tm: utc, format: "%-m", want: "1"},
		{name: "minus hour", tm: utc, format: "%-H", want: "3"},
		{name: "minus second", tm: utc, format: "%-S", want: "5"},
		{name: "minus day of year", tm: utc, format: "%-j", want: "2"},
		{name: "minus year keeps natural width", tm: utc, format: "%-Y", want: "2024"},
		{name: "minus on blank padded day", tm: utc, format: "%-e", want: "2"},
		{name: "minus on blank padded hour", tm: utc, format: "%-k", want: "3"},

		// _ flag forces space padding on numeric directives.
		{name: "space day", tm: utc, format: "%_d", want: " 2"},
		{name: "space month", tm: utc, format: "%_m", want: " 1"},
		{name: "space hour 12", tm: afternoon, format: "%_I", want: " 3"},

		// 0 flag forces zero padding, even on normally blank-padded directives.
		{name: "zero day", tm: utc, format: "%0d", want: "02"},
		{name: "zero blank padded day", tm: utc, format: "%0e", want: "02"},
		{name: "zero blank padded hour", tm: utc, format: "%0k", want: "03"},
		{name: "zero blank padded hour 12", tm: utc, format: "%0l", want: "03"},

		// Width applies to every numeric directive, not just %N.
		{name: "width year", tm: utc, format: "%6Y", want: "002024"},
		{name: "width month", tm: utc, format: "%6m", want: "000001"},
		{name: "width day", tm: utc, format: "%6d", want: "000002"},
		{name: "width century", tm: utc, format: "%4C", want: "0020"},
		{name: "width year in century", tm: utc, format: "%4y", want: "0024"},
		{name: "width day of year", tm: utc, format: "%5j", want: "00002"},
		{name: "width epoch", tm: utc, format: "%15s", want: "000001704164645"},
		{name: "width weekday number", tm: utc, format: "%3w", want: "002"},
		{name: "explicit width with zero flag", tm: utc, format: "%03d", want: "002"},
		{name: "explicit width with zero flag wide", tm: utc, format: "%010d", want: "0000000002"},
		{name: "width smaller than natural keeps value", tm: utc, format: "%1Y", want: "2024"},
		{name: "width below default min shrinks field", tm: utc, format: "%1m", want: "1"},
		{name: "minus with width drops padding", tm: utc, format: "%-15s", want: "1704164645"},

		// Width with space padding on a numeric directive.
		{name: "space width day", tm: utc, format: "%_6d", want: "     2"},

		// Repeated padding flags: the last one wins.
		{name: "repeated minus", tm: utc, format: "%--d", want: "2"},
		{name: "repeated zero", tm: utc, format: "%00d", want: "02"},
		{name: "zero then minus", tm: utc, format: "%0-d", want: "2"},

		// ^ uppercases name directives.
		{name: "upper month full", tm: utc, format: "%^B", want: "JANUARY"},
		{name: "upper month abbrev", tm: utc, format: "%^b", want: "JAN"},
		{name: "upper weekday full", tm: utc, format: "%^A", want: "TUESDAY"},
		{name: "upper weekday abbrev", tm: utc, format: "%^a", want: "TUE"},
		{name: "upper meridian lower form", tm: utc, format: "%^P", want: "AM"},
		{name: "upper zone", tm: utc, format: "%^Z", want: "UTC"},

		// # toggles case: lowercase all-uppercase output, uppercase otherwise.
		{name: "toggle month full uppercases", tm: utc, format: "%#B", want: "JANUARY"},
		{name: "toggle weekday uppercases", tm: utc, format: "%#A", want: "TUESDAY"},
		{name: "toggle meridian upper lowercases", tm: utc, format: "%#p", want: "am"},
		{name: "toggle meridian lower uppercases", tm: utc, format: "%#P", want: "AM"},
		{name: "toggle zone lowercases", tm: utc, format: "%#Z", want: "utc"},

		// Case flags have no visible effect on numeric directives.
		{name: "upper day is numeric noop", tm: utc, format: "%^d", want: "02"},

		// Width pads name directives with spaces (or zeros with the 0 flag).
		{name: "width month name", tm: utc, format: "%10B", want: "   January"},
		{name: "width and upper month name", tm: utc, format: "%^10B", want: "   JANUARY"},
		{name: "minus then width on name drops padding", tm: utc, format: "%-10B", want: "January"},
		{name: "space width month name", tm: utc, format: "%_10B", want: "   January"},
		{name: "zero flag noop without width", tm: utc, format: "%0B", want: "January"},
		{name: "width on empty zone name not padded", tm: noZone, format: "%10Z", want: ""},

		// Meridian width padding.
		{name: "width meridian space pads", tm: utc, format: "%5p", want: "   AM"},
		{name: "width meridian zero pads", tm: utc, format: "%05p", want: "000AM"},
		{name: "minus meridian drops padding", tm: utc, format: "%-5p", want: "AM"},

		// Flag + width + case combined.
		{name: "upper zero width numeric", tm: utc, format: "%^010d", want: "0000000002"},

		// Fractional directives treat width as the digit count; flags are no-ops.
		{name: "milliseconds width six", tm: sub, format: "%6L", want: "123456"},
		{name: "milliseconds width one", tm: sub, format: "%1L", want: "1"},
		{name: "milliseconds width pads", tm: sub, format: "%10L", want: "1234560000"},
		{name: "nanoseconds minus noop", tm: sub, format: "%-N", want: "123456000"},
		{name: "nanoseconds width six space noop", tm: sub, format: "%_6N", want: "123456"},

		// Offsets honor width with zero padding; - _ 0 ^ # are no-ops.
		{name: "offset width", tm: plus, format: "%6z", want: "+00530"},
		{name: "offset width wider", tm: plus, format: "%8z", want: "+0000530"},
		{name: "offset zero flag width", tm: plus, format: "%06z", want: "+00530"},
		{name: "offset minus flag still pads", tm: plus, format: "%-6z", want: "+00530"},
		{name: "offset upper flag noop", tm: plus, format: "%^z", want: "+0530"},
		{name: "offset colon width", tm: plus, format: "%10:z", want: "+000005:30"},

		// Literal directives pad to the requested width.
		{name: "percent width space pads", tm: utc, format: "%5%", want: "    %"},
		{name: "percent width minus drops padding", tm: utc, format: "%-5%", want: "%"},

		// ^ propagates into compound directives; # does not (Ruby quirk).
		{name: "upper ctime", tm: utc, format: "%^c", want: "TUE JAN  2 03:04:05 2024"},
		{name: "toggle ctime is inert", tm: utc, format: "%#c", want: "Tue Jan  2 03:04:05 2024"},
		{name: "upper twelve hour clock", tm: utc, format: "%^r", want: "03:04:05 AM"},
		{name: "minus on compound is inert", tm: utc, format: "%-F", want: "2024-01-02"},

		// Width pads a compound directive's expansion as a single field: space
		// by default, zeros with the 0 flag, and - is ignored (like %z).
		{name: "width equals compound length", tm: utc, format: "%10F", want: "2024-01-02"},
		{name: "width pads compound with spaces", tm: utc, format: "%12F", want: "  2024-01-02"},
		{name: "width pads compound with zeros", tm: utc, format: "%012F", want: "002024-01-02"},
		{name: "minus does not suppress compound width", tm: utc, format: "%-12F", want: "  2024-01-02"},
		{name: "width pads time compound", tm: utc, format: "%12T", want: "    03:04:05"},
		{name: "upper width pads compound", tm: utc, format: "%^12F", want: "  2024-01-02"},

		// Ruby's lossy space-padded %z (%_z -> " +530") is not reproduced: the
		// underscore flag is a no-op on the offset, which Vibescript keeps intact.
		{name: "underscore offset keeps offset intact", tm: plus, format: "%_z", want: "+0530"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := strftime(tc.tm, tc.format)
			if err != nil {
				t.Fatalf("strftime(%q) returned error: %v", tc.format, err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("strftime(%q) mismatch (-want +got):\n%s", tc.format, diff)
			}
		})
	}
}

// TestStrftimeRejectsMalformedFormat checks that percent sequences without a
// directive byte are rejected with an invalid-format error.
func TestStrftimeRejectsMalformedFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		format string
	}{
		{name: "trailing percent", format: "abc%"},
		{name: "lone percent", format: "%"},
		{name: "width without directive", format: "%6"},
		{name: "colon without directive", format: "%:"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := strftime(time.Unix(0, 0).UTC(), tc.format); err == nil {
				t.Fatalf("strftime(%q) expected an error, got nil", tc.format)
			}
		})
	}
}

// TestTimeStrftimeThroughRuntime verifies Time#strftime end to end through the
// interpreter, both as a direct call and as a bound formatter value, and that it
// agrees with Time#format for an equivalent simple layout.
func TestTimeStrftimeThroughRuntime(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      utc = Time.utc(2024, 1, 2, 3, 4, 5)
      offset = Time.parse("2024-01-02 03:04:05", "2006-01-02 15:04:05", in: "+05:30")
      formatter = utc.strftime
      {
        date:      utc.strftime("%Y-%m-%d"),
        time:      utc.strftime("%H:%M:%S"),
        zone:      offset.strftime("%Y-%m-%d %H:%M:%S %:z"),
        meridian:  utc.strftime("%I:%M %p"),
        escaped:   utc.strftime("%% literal"),
        unknown:   utc.strftime("%Q"),
        bound:     formatter("%Y/%m/%d"),
        matches_format: utc.strftime("%Y-%m-%d %H:%M:%S") == utc.format("2006-01-02 15:04:05")
      }
    end
    `)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	want := map[string]Value{
		"date":           NewString("2024-01-02"),
		"time":           NewString("03:04:05"),
		"zone":           NewString("2024-01-02 03:04:05 +05:30"),
		"meridian":       NewString("03:04 AM"),
		"escaped":        NewString("% literal"),
		"unknown":        NewString("%Q"),
		"bound":          NewString("2024/01/02"),
		"matches_format": NewBool(true),
	}
	got := result.Hash()
	for key, expected := range want {
		if val, ok := got[key]; !ok || !val.Equal(expected) {
			t.Errorf("strftime[%s] = %v, want %v", key, val, expected)
		}
	}
}

// TestTimeStrftimeRejectsBadArguments checks the runtime argument validation:
// strftime requires exactly one string positional argument, no keyword
// arguments, and rejects a malformed format string.
func TestTimeStrftimeRejectsBadArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "no arguments", expr: "t.strftime()", want: "time.strftime expects a format string"},
		{name: "non-string argument", expr: "t.strftime(5)", want: "time.strftime expects a format string"},
		{name: "too many arguments", expr: `t.strftime("%Y", "%m")`, want: "time.strftime expects a format string"},
		{name: "keyword argument", expr: `t.strftime(format: "%Y")`, want: "time.strftime does not accept keyword arguments"},
		{name: "malformed format", expr: `t.strftime("%Y%")`, want: "time.strftime invalid format"},
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
