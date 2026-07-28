package runtime

import (
	"context"
	"testing"
)

// Named captures compiled and matched but were never surfaced: m[:name] read
// nil and named_captures did not exist, so the readable way to write any
// non-trivial extraction was unavailable and every pattern had to be read back
// positionally.
func TestMatchDataNamedCaptures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "symbol index", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[:k].inspect`, want: `"x"`},
		{name: "string index", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)["v"].inspect`, want: `"1"`},
		{name: "named_captures member", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/).named_captures.size.to_s`, want: "2"},
		{name: "named_captures by string", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/).named_captures["k"].inspect`, want: `"x"`},
		{name: "named_captures by symbol", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/).named_captures[:v].inspect`, want: `"1"`},
		// An unnamed pattern answers with an empty hash, as in Ruby, rather
		// than reporting an unknown member.
		{name: "pattern with no named groups", expr: `"2026-07".match(/(\d+)-(\d+)/).named_captures.size.to_s`, want: "0"},
		// Only the named groups appear when a pattern mixes both.
		{name: "mixed named and positional", expr: `"a1".match(/(?<letter>[a-z])(\d)/).named_captures.size.to_s`, want: "1"},
		{name: "mixed reads the named group", expr: `"a1".match(/(?<letter>[a-z])(\d)/)[:letter].inspect`, want: `"a"`},
		// A name the pattern does not define reads nil rather than raising,
		// consistent with reading a missing entry off this hash-shaped result.
		{name: "undefined name", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[:nope].inspect`, want: "nil"},
		// The readable spelling of a real extraction.
		{name: "date extraction", expr: `d = "2026-07-27".match(/(?<y>\d{4})-(?<mo>\d{2})-(?<dy>\d{2})/)` + "\n  " + `"#{d[:y]}/#{d[:mo]}/#{d[:dy]}"`, want: "2026/07/27"},
		// Regexp#match builds the same result as String#match.
		{name: "through Regexp#match", expr: `/(?<a>\w+)/.match("hello")[:a].inspect`, want: `"hello"`},
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

// Positional access and the existing entries must keep working: a named index
// must not shadow the match result's own shape.
func TestMatchDataNamedAccessPreservesExistingShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "whole match by index", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[0].inspect`, want: `"x=1"`},
		{name: "capture by index", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[1].inspect`, want: `"x"`},
		{name: "second capture by index", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[2].inspect`, want: `"1"`},
		{name: "captures entry", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[:captures].inspect`, want: `["x", "1"]`},
		{name: "pre_match entry", expr: `"ax=1".match(/(?<k>\w)=(?<v>\d)/)[:pre_match].inspect`, want: `"a"`},
		{name: "post_match entry", expr: `"x=1b".match(/(?<k>\w)=(?<v>\d)/)[:post_match].inspect`, want: `"b"`},
		{name: "captures member", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/).captures.inspect`, want: `["x", "1"]`},
		{name: "begin member", expr: `"ax=1".match(/(?<k>\w)=(?<v>\d)/).begin(0).to_s`, want: "1"},
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

// A group named after an existing entry must not hide that entry, since the
// match result doubles as a hash and scripts read its shape by name.
func TestNamedGroupDoesNotShadowMatchDataEntry(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      "ab".match(/(?<captures>a)(?<pre_match>b)/)[:captures].inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != `["a", "b"]` {
		t.Fatalf("[:captures] = %s, want the captures entry to win", got.String())
	}
}

// The offset form of String#match rewrites the pattern internally, so its
// group numbering has to keep lining up with the names.
func TestNamedCapturesSurviveMatchOffset(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      "x=1 y=2".match(/(?<k>\w)=(?<v>\d)/, 3)[:k].inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != `"y"` {
		t.Fatalf("offset match named capture = %s, want \"y\"", got.String())
	}
}

// A pattern may reuse a group name, and only one of those groups participates
// in any given match. Assigning unconditionally let a later non-participating
// duplicate overwrite an earlier match with nil, so /(?<x>a)|(?<x>b)/ against
// "ab" reported nil rather than "a". Ruby keeps the last group that actually
// participated.
func TestDuplicateNamedCapturesKeepTheParticipatingGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string
		want string
	}{
		{name: "alternation first branch", expr: `"ab".match(/(?<x>a)|(?<x>b)/)[:x].inspect`, want: `"a"`},
		{name: "alternation via named_captures", expr: `"ab".match(/(?<x>a)|(?<x>b)/).named_captures[:x].inspect`, want: `"a"`},
		{name: "alternation second branch", expr: `"b".match(/(?<x>a)|(?<x>b)/)[:x].inspect`, want: `"b"`},
		{name: "absent leading optional duplicate", expr: `"b".match(/(?<x>a)?(?<x>b)/)[:x].inspect`, want: `"b"`},
		// A genuinely unmatched distinct group is still nil.
		{name: "unmatched distinct group", expr: `"b".match(/(?<x>b)(?<y>c)?/)[:y].inspect`, want: "nil"},
		{name: "distinct names unaffected", expr: `"x=1".match(/(?<k>\w)=(?<v>\d)/)[:v].inspect`, want: `"1"`},
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
