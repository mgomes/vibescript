package runtime

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

const enumToStringSource = `
enum Status
  Active
  Done
end
`

// Interpolation rendered an enum value fine, but the explicit conversion every
// other kind supports was missing, so an author who wrote .to_s got an unknown
// member -- and no suggestion, because neither name nor symbol is close enough
// for didYouMean to fire.
func TestEnumValueToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "to_s", expr: `Status::Active.to_s`, want: "Status::Active"},
		{name: "to_s with parentheses", expr: `Status::Active.to_s()`, want: "Status::Active"},
		{name: "string alias", expr: `Status::Active.string`, want: "Status::Active"},
		{name: "inspect", expr: `Status::Active.inspect`, want: "Status::Active"},
		{name: "another member", expr: `Status::Done.to_s`, want: "Status::Done"},
		// The point of the conversion is that it agrees with interpolation,
		// which is what already worked.
		{name: "agrees with interpolation", expr: `(Status::Active.to_s == "#{Status::Active}").to_s`, want: "true"},
		{name: "concatenates", expr: `"status=" + Status::Done.to_s`, want: "status=Status::Done"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumToStringSource+"\ndef run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// The existing members must keep working.
func TestEnumValueExistingMembersUnchanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "name", expr: `Status::Active.name`, want: "Active"},
		{name: "symbol", expr: `Status::Active.symbol.inspect`, want: ":active"},
		{name: "enum", expr: `"#{Status::Active.enum}"`, want: "<Enum Status>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumToStringSource+"\ndef run()\n  "+tc.expr+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.expr, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.expr, got.String(), tc.want)
			}
		})
	}
}

// An unknown member still reports, and now suggests to_s for the near misses
// that previously produced no suggestion at all.
func TestEnumValueUnknownMemberSuggests(t *testing.T) {
	t.Parallel()
	script := compileScript(t, enumToStringSource+"\ndef run()\n  Status::Active.to_str\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected an unknown enum member to be reported")
	}
	if !strings.Contains(err.Error(), "to_s") {
		t.Fatalf("error = %v, want it to suggest to_s", err)
	}
}

// to_s takes no arguments, like every other scalar conversion.
func TestEnumValueToStringRejectsArguments(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{`Status::Active.to_s(1)`, `Status::Active.inspect(1)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, enumToStringSource+"\ndef run()\n  "+expr+"\nend")
			if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
				t.Fatalf("%s was accepted, want it rejected", expr)
			}
		})
	}
}

// The shared scalar helpers render before checking, so an identifier far
// larger than the memory quota -- reachable when MaxSourceBytes is configured
// higher -- allocated the whole Enum::Member text before any guard ran. The
// length follows from the two identifiers, so it can be projected first.
func TestEnumRenderingIsProjectedBeforeAllocating(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("N", 300000)
	script := compileScriptWithConfig(t, Config{StepQuota: Unlimited, MemoryQuotaBytes: 128 * 1024, MaxSourceBytes: 4 << 20},
		"enum "+long+"\n  Active\nend\ndef run()\n  "+long+"::Active.to_s\nend")
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatalf("a rendering far larger than the quota was produced without a guard")
	}
}

// A typo near the alias suggests it, which is the did-you-mean behavior the
// original change existed for.
func TestEnumValueSuggestsTheStringAlias(t *testing.T) {
	t.Parallel()
	script := compileScript(t, enumToStringSource+"\ndef run()\n  Status::Active.strng\nend")
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected an unknown enum member to be reported")
	}
	if !strings.Contains(err.Error(), `"string"`) {
		t.Fatalf("error = %v, want it to suggest string", err)
	}
}

// fmt.Sprintf holds a formatting buffer alongside the string it returns, so
// the real peak was roughly twice the text while the guard charged the text
// length alone. Growing a builder once to the exact size makes the returned
// string the only allocation, which is what the charge covers.
func TestEnumMemberTextAllocatesOnlyTheString(t *testing.T) {
	enum := &EnumDef{Name: strings.Repeat("E", 4096)}
	member := &EnumValueDef{Name: strings.Repeat("M", 4096), Enum: enum}

	allocs := testing.AllocsPerRun(100, func() {
		_ = enumMemberText(member)
	})
	if allocs > 1 {
		t.Fatalf("enumMemberText made %v allocations, want at most the returned string", allocs)
	}
}

// The guard charges the buffer the rendering grows, which an allocator rounds
// up to a size class. Charging the exact text length let a rendering that fit
// the quota only narrowly exceed it once rounded.
func TestEnumRenderingChargesTheRoundedBuffer(t *testing.T) {
	t.Parallel()

	for _, payload := range []int{1, 17, 100, 4095, 4096, 300000} {
		var grown strings.Builder
		grown.Grow(payload)
		charged := projectedBuilderCap(&strings.Builder{}, payload)
		if charged < grown.Cap() {
			t.Fatalf("payload %d: charged %d bytes, but growing the builder reserves %d", payload, charged, grown.Cap())
		}
	}
}

// Explicit conversion and interpolation render through the same helper, so
// they cannot drift apart.
func TestEnumRenderingIsTheSameThroughEveryPath(t *testing.T) {
	t.Parallel()

	script := compileScript(t, enumToStringSource+"\ndef run()\n  [Status::Active.to_s, Status::Active.string, Status::Active.inspect, \"#{Status::Active}\"].uniq.length\nend")
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "1" {
		t.Fatalf("enum renderings disagree across paths (%s distinct forms)", got.String())
	}
}

// Interpolation projects a value's rendered length before writing it, and
// answering that projection through Value.String allocates the very rendering
// the guard exists to decide about -- then allocates it a second time while
// the charged destination buffer is live. Both paths now answer from the two
// identifiers instead.
func TestEnumInterpolationDoesNotMaterializeTheText(t *testing.T) {
	enum := &EnumDef{Name: strings.Repeat("E", 4096)}
	member := &EnumValueDef{Name: strings.Repeat("M", 4096), Enum: enum}
	value := NewEnumValue(member)

	lenAllocs := testing.AllocsPerRun(100, func() {
		_, _ = value.StringByteLenBounded(func() error { return nil })
	})
	if lenAllocs > 0 {
		t.Fatalf("projecting the byte length made %v allocations, want none", lenAllocs)
	}

	// Width-qualified formatting projects rune lengths, so that path must not
	// materialize the rendering either.
	runeAllocs := testing.AllocsPerRun(100, func() {
		_, _ = value.StringRuneLenBounded(func() error { return nil })
	})
	if runeAllocs > 0 {
		t.Fatalf("projecting the rune length made %v allocations, want none", runeAllocs)
	}

	// Grown once, up front, and never reset: any allocation observed during
	// the run is a temporary rather than the destination's own growth.
	const runs = 100
	var sb strings.Builder
	sb.Grow(enumValueRenderingBytes(member) * (runs + 2))
	writeAllocs := testing.AllocsPerRun(runs, func() {
		value.WriteStringTo(&sb)
	})
	if writeAllocs > 0 {
		t.Fatalf("writing the text made %v allocations, want it streamed into the destination", writeAllocs)
	}
}

// The streamed write must produce exactly the bytes String would.
func TestStreamedEnumTextMatchesString(t *testing.T) {
	t.Parallel()

	enum := &EnumDef{Name: "Status"}
	member := &EnumValueDef{Name: "Active", Enum: enum}
	value := NewEnumValue(member)

	var sb strings.Builder
	value.WriteStringTo(&sb)
	if sb.String() != value.String() {
		t.Fatalf("streamed %q, String gives %q", sb.String(), value.String())
	}
	got, err := value.StringByteLenBounded(func() error { return nil })
	if err != nil {
		t.Fatalf("byte projection: %v", err)
	}
	if got != len(value.String()) {
		t.Fatalf("projected byte length %d, want %d", got, len(value.String()))
	}
	runes, err := value.StringRuneLenBounded(func() error { return nil })
	if err != nil {
		t.Fatalf("rune projection: %v", err)
	}
	if runes != utf8.RuneCountInString(value.String()) {
		t.Fatalf("projected rune length %d, want %d", runes, utf8.RuneCountInString(value.String()))
	}
}

// A precision-qualified format keeps only a few bytes, but the projection and
// the render both went through Value.String, materializing the whole member
// twice before the quota check saw an output of a few bytes. The hook is
// bounded now, so only the kept bytes are ever written.
func TestPrecisionFormattedEnumDoesNotMaterializeTheText(t *testing.T) {
	enum := &EnumDef{Name: strings.Repeat("E", 4096)}
	member := &EnumValueDef{Name: strings.Repeat("M", 4096), Enum: enum}
	value := NewEnumValue(member)

	projAllocs := testing.AllocsPerRun(100, func() {
		_, _, _ = value.StringByteLenBoundedUpTo(4, func() error { return nil })
	})
	if projAllocs > 0 {
		t.Fatalf("the capped projection made %v allocations, want none", projAllocs)
	}

	// A couple of small allocations are expected (a builder and the result);
	// what matters is that neither is the size of the member. Bytes, not
	// counts, is the property under test.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range 100 {
		if _, err := value.StringBounded(4); err == nil {
			t.Fatalf("a 4-byte render of a %d-byte member reported no truncation", len(value.String()))
		}
	}
	runtime.ReadMemStats(&after)

	perRender := (after.TotalAlloc - before.TotalAlloc) / 100
	if perRender > 1024 {
		t.Fatalf("a 4-byte bounded render allocated %d bytes, want it bounded by the limit rather than the %d-byte member", perRender, len(value.String()))
	}
}

// Bounded rendering must produce exactly the prefix String would, and report
// truncation the same way.
func TestBoundedEnumRenderingMatchesTheFullText(t *testing.T) {
	t.Parallel()

	enum := &EnumDef{Name: "Status"}
	member := &EnumValueDef{Name: "Active", Enum: enum}
	value := NewEnumValue(member)
	full := value.String()

	for limit := 1; limit <= len(full)+2; limit++ {
		got, err := value.StringBounded(limit)
		wantTruncated := limit < len(full)
		want := full
		if wantTruncated {
			want = full[:limit]
		}
		if got != want {
			t.Fatalf("StringBounded(%d) = %q, want %q", limit, got, want)
		}
		if (err != nil) != wantTruncated {
			t.Fatalf("StringBounded(%d) err = %v, want truncated = %v", limit, err, wantTruncated)
		}
		n, truncated, err := value.StringByteLenBoundedUpTo(limit, func() error { return nil })
		if err != nil {
			t.Fatalf("StringByteLenBoundedUpTo(%d): %v", limit, err)
		}
		if truncated != wantTruncated {
			t.Fatalf("StringByteLenBoundedUpTo(%d) truncated = %v, want %v", limit, truncated, wantTruncated)
		}
		if !truncated && n != len(full) {
			t.Fatalf("StringByteLenBoundedUpTo(%d) = %d, want %d", limit, n, len(full))
		}
	}
}

// The rendered text a script sees is unchanged by the bounded path.
func TestPrecisionFormattingMatchesTheStringForm(t *testing.T) {
	t.Parallel()

	script := compileScript(t, enumToStringSource+"\ndef run()\n  [format(\"%.1s\", Status::Active), format(\"%.100s\", Status::Active), format(\"%s\", Status::Active)].join(\"|\")\nend")
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "S|Status::Active|Status::Active" {
		t.Fatalf("got %q", got.String())
	}
}
