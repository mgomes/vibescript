package runtime

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
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
      {
        y2: t.format("06"),
        y4: t.format("2006"),
        date: t.format("2006-01-02"),
        time: t.format("15:04:05")
      }
    end
    `)

	result := callFunc(t, script, "run", nil)
	want := hashVal(map[string]Value{
		"y2":   NewString("00"),
		"y4":   NewString("2000"),
		"date": NewString("2000-01-01"),
		"time": NewString("20:15:01"),
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
	if !out["down_i_dot"].Equal(NewString("i")) {
		t.Fatalf("down_i_dot mismatch: %v", out["down_i_dot"])
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
	script := compileScript(t, `
    def current()
      now()
    end
    `)

	result := callFunc(t, script, "current", nil)
	if result.Kind() != KindString {
		t.Fatalf("expected string, got %v", result.Kind())
	}
	if _, err := time.Parse(time.RFC3339, result.String()); err != nil {
		t.Fatalf("now() output not RFC3339: %v", err)
	}
}
