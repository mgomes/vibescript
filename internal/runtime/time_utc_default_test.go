package runtime

import (
	"context"
	"testing"
	"time"
)

// Script results used to depend on the host's TZ: the same script and the same
// data produced a different calendar day depending on where it ran. Scripts run
// in a sandbox whose quotas, capabilities, and memory bound all exist to make
// execution predictable, and TZ is an ambient input no capability gates.
//
// These run the same source under several host zones and require one answer.
func TestZonelessTimeResultsDoNotDependOnHostZone(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "zoneless date parses as UTC", expr: `Time.parse("2026-07-27").iso8601`, want: "2026-07-27T00:00:00Z"},
		{name: "zoneless datetime parses as UTC", expr: `Time.parse("2026-07-27 14:30:45").iso8601`, want: "2026-07-27T14:30:45Z"},
		{name: "zoneless parse with a layout", expr: `Time.parse("2026-07-27", "2006-01-02").iso8601`, want: "2026-07-27T00:00:00Z"},
		{name: "the calendar day does not shift", expr: `Time.parse("2026-07-27").day.to_s`, want: "27"},
		{name: "the hour does not shift", expr: `Time.parse("2026-07-27 23:30:00").hour.to_s`, want: "23"},
		{name: "an explicit Z is unchanged", expr: `Time.parse("2026-07-27T14:30:45Z").iso8601`, want: "2026-07-27T14:30:45Z"},
		{name: "an explicit offset is preserved", expr: `Time.parse("2026-07-27T14:30:45+05:30").iso8601`, want: "2026-07-27T14:30:45+05:30"},
		{name: "utc_offset of a zoneless parse", expr: `Time.parse("2026-07-27").utc_offset.to_s`, want: "0"},
	}

	for _, zone := range []string{"UTC", "America/New_York", "Asia/Tokyo", "Pacific/Kiritimati"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Skipf("zone %s unavailable: %v", zone, err)
			}
			restore := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = restore })

			for _, tc := range tests {
				script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
				got, err := script.Call(context.Background(), "run", nil, CallOptions{})
				if err != nil {
					t.Fatalf("%s in %s: %v", tc.name, zone, err)
				}
				if got.String() != tc.want {
					t.Fatalf("%s in %s = %s, want %s", tc.name, zone, got.String(), tc.want)
				}
			}
		})
	}
}

// The now builtin is documented as a UTC timestamp and always was one, so
// Time.now returning host-local meant one concept had two answers that could
// differ by a calendar day.
func TestTimeNowAgreesWithNowBuiltin(t *testing.T) {
	for _, zone := range []string{"UTC", "America/New_York", "Asia/Tokyo"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Skipf("zone %s unavailable: %v", zone, err)
			}
			restore := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = restore })

			script := compileScript(t, `
            def offset()
              Time.now.utc_offset
            end
            def zone_suffix()
              Time.now.iso8601.end_with?("Z")
            end
            `)
			offset, err := script.Call(context.Background(), "offset", nil, CallOptions{})
			if err != nil {
				t.Fatalf("offset in %s: %v", zone, err)
			}
			if offset.String() != "0" {
				t.Fatalf("Time.now utc_offset in %s = %s, want 0", zone, offset.String())
			}
			suffix, err := script.Call(context.Background(), "zone_suffix", nil, CallOptions{})
			if err != nil {
				t.Fatalf("zone_suffix in %s: %v", zone, err)
			}
			if suffix.String() != "true" {
				t.Fatalf("Time.now in %s did not render as UTC", zone)
			}
		})
	}
}

// A script that wants a zone still asks for one, so the default does not
// remove the capability -- it only stops it from being ambient.
func TestExplicitZoneStillOverridesTheDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "Time.now with in", expr: `Time.now(in: "Asia/Tokyo").utc_offset.to_s`, want: "32400"},
		{name: "Time.parse with in", expr: `Time.parse("2026-07-27", in: "Asia/Tokyo").iso8601`, want: "2026-07-27T00:00:00+09:00"},
		{name: "Time.parse with a layout and in", expr: `Time.parse("2026-07-27", "2006-01-02", in: "Asia/Tokyo").iso8601`, want: "2026-07-27T00:00:00+09:00"},
		{name: "negative offset zone", expr: `Time.parse("2026-07-27", in: "America/New_York").utc_offset.to_s`, want: "-14400"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %s, want %s", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// An input naming a zone is not zoneless, and resolving an abbreviation needs
// a zone database -- the host's is the only one there is. Forcing UTC made Go
// fabricate the abbreviation at offset zero, so an RFC1123 timestamp carrying
// EDT silently shifted by four hours.
func TestZoneAbbreviationsResolveAgainstTheHostZone(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("zone unavailable: %v", err)
	}
	restore := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = restore })

	script := compileScript(t, `
    def run()
      Time.parse("Mon, 27 Jul 2026 14:30:45 EDT").utc_offset.to_s
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "-14400" {
		t.Fatalf("EDT resolved to offset %s, want -14400", got.String())
	}
}

// The zoneless default is unaffected: a timestamp naming no zone stays UTC
// whatever the host is, which is what #1063 was about.
func TestZonelessInputStaysUTCAlongsideZoneAbbreviations(t *testing.T) {
	for _, zone := range []string{"America/New_York", "Asia/Tokyo"} {
		t.Run(zone, func(t *testing.T) {
			loc, err := time.LoadLocation(zone)
			if err != nil {
				t.Skipf("zone unavailable: %v", err)
			}
			restore := time.Local
			time.Local = loc
			t.Cleanup(func() { time.Local = restore })

			script := compileScript(t, `
            def run()
              "#{Time.parse("2026-07-27 14:30:45").iso8601}|#{Time.parse("2026-07-27").iso8601}|#{Time.parse("Mon, 27 Jul 2026 14:30:45 -0400").utc_offset}"
            end
            `)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			want := "2026-07-27T14:30:45Z|2026-07-27T00:00:00Z|-14400"
			if got.String() != want {
				t.Fatalf("in %s = %s, want %s", zone, got.String(), want)
			}
		})
	}
}
