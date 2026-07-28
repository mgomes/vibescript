package runtime

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// `rescue => e` followed by `puts "failed: #{e}"` is the single most common
// error-reporting idiom there is, and it printed `failed: <object>`. The
// content was reachable through e.message and e.to_s, so this was a silent
// loss rather than an error: the line written precisely to explain a failure
// explained nothing.
func TestRescuedErrorRendersItsMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "interpolation", body: "begin\n    raise \"boom\"\n  rescue => e\n    \"#{e}\"\n  end", want: "boom"},
		{name: "agrees with to_s", body: "begin\n    raise \"boom\"\n  rescue => e\n    (\"#{e}\" == e.to_s).to_s\n  end", want: "true"},
		{name: "agrees with message", body: "begin\n    raise \"boom\"\n  rescue => e\n    (\"#{e}\" == e.message).to_s\n  end", want: "true"},
		{name: "inside a larger string", body: "begin\n    raise \"boom\"\n  rescue => e\n    \"failed: #{e}\"\n  end", want: "failed: boom"},
		// A runtime error, not just an explicit raise.
		{name: "runtime error", body: "begin\n    [1] + nil\n  rescue => e\n    \"#{e}\"\n  end", want: "unsupported addition operands"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, got.String(), tc.want)
			}
		})
	}
}

// puts and print render the string form, so they follow the same entry.
func TestPutsRendersRescuedErrorMessage(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	script := compileScriptWithConfig(t, Config{OutputWriter: &stdout}, `
    def run()
      begin
        raise "boom"
      rescue => e
        puts(e)
      end
    end
    `)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if stdout.String() != "boom\n" {
		t.Fatalf("puts e wrote %q, want %q", stdout.String(), "boom\n")
	}
}

// inspect is a separate rendering and must keep the error's full detail.
func TestRescuedErrorInspectKeepsDetail(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      begin
        raise "boom"
      rescue => e
        e.inspect
      end
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	for _, want := range []string{"message", "backtrace", "RuntimeError"} {
		if !strings.Contains(got.String(), want) {
			t.Fatalf("inspect lost %s: %s", want, got.String())
		}
	}
}

// Only a string to_s entry substitutes. Match data gained one when the
// internal key was removed, so it now renders its matched text -- which is
// also what Ruby's MatchData does -- while a bag carrying no such entry keeps
// the placeholder.
func TestAttributeBagRenderingFollowsItsStringEntry(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def run()
      m = "2026-07".match(/(\d+)-(\d+)/)
      "#{m}"
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "2026-07" {
		t.Fatalf("match data rendered as %q, want its matched text", got.String())
	}

	// A bag without the entry is untouched.
	if _, substituted := objectStringEntry(NewObject(map[string]Value{"a": NewInt(1)})); substituted {
		t.Fatalf("a bag with no to_s entry was substituted")
	}
	// A non-string entry does not substitute either.
	if _, substituted := objectStringEntry(NewObject(map[string]Value{"to_s": NewInt(1)})); substituted {
		t.Fatalf("a bag with a non-string to_s entry was substituted")
	}
}

// KindObject also carries ordinary host data, so a to_s entry alone does not
// mean the bag is declaring its string form. A host object holding a string
// field of that name would otherwise have its payload rendered in place of
// <object>, exposing it.
func TestOrdinaryHostObjectsKeepTheirRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entries map[string]Value
	}{
		{name: "bare to_s field", entries: map[string]Value{"to_s": NewString("payload")}},
		{name: "to_s with unrelated fields", entries: map[string]Value{"to_s": NewString("payload"), "id": NewInt(1)}},
		// Part of the error shape is not the error shape.
		{name: "partial error shape", entries: map[string]Value{"to_s": NewString("payload"), "message": NewString("m")}},
		// A non-string backtrace is not a backtrace.
		{name: "error shape with a wrong backtrace", entries: map[string]Value{
			"to_s": NewString("p"), "message": NewString("m"), "class": NewString("c"),
			"type": NewString("t"), "backtrace": NewString("not an array"),
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, substituted := objectStringEntry(NewObject(tc.entries)); substituted {
				t.Fatalf("%s: an ordinary host object had its payload rendered", tc.name)
			}
		})
	}
}

// The two bags that deliberately publish a string form still render it.
func TestDeliberateStringFormsStillRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "rescued error", body: "begin\n    raise \"boom\"\n  rescue => e\n    \"#{e}\"\n  end", want: "boom"},
		{name: "match data", body: "\"2026-07\".match(/(\\d+)-(\\d+)/).to_s", want: "2026-07"},
		{name: "interpolated match data", body: "m = \"2026-07\".match(/(\\d+)-(\\d+)/)\n  \"#{m}\"", want: "2026-07"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, got.String(), tc.want)
			}
		})
	}
}

// A host object cannot claim to be a rescued error or match data by carrying
// the same fields. The fields these bags use are public, host-settable data,
// so recognizing them by shape let any bag with a matching set have its to_s
// payload rendered in place of the <object> form.
func TestHostObjectsCannotSpoofADeliberateStringForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		entries map[string]Value
	}{
		{name: "full rescued error shape", entries: map[string]Value{
			"to_s": NewString("payload"), "message": NewString("m"),
			"class": NewString("c"), "type": NewString("t"),
			"backtrace": NewArray([]Value{NewString("frame")}),
		}},
		{name: "full match data shape", entries: map[string]Value{
			"to_s": NewString("payload"), "captures": NewArray([]Value{NewString("a")}),
			"pre_match": NewString(""), "post_match": NewString(""),
		}},
		{name: "both shapes at once", entries: map[string]Value{
			"to_s": NewString("payload"), "message": NewString("m"),
			"class": NewString("c"), "type": NewString("t"),
			"backtrace": NewArray([]Value{}), "captures": NewArray([]Value{}),
			"pre_match": NewString(""), "post_match": NewString(""),
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, substituted := objectStringEntry(NewObject(tc.entries)); substituted {
				t.Fatalf("%s: a host object spoofed a deliberate string form and had its payload rendered", tc.name)
			}
		})
	}
}

// The tag rides in a word an object otherwise leaves unused, so it must not
// leak into anything script code can see.
func TestObjectTagIsInvisibleToScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "keys", body: `begin
    raise "boom"
  rescue => e
    e.keys.sort.join(",")
  end`, want: "backtrace,class,code_frame,message,to_s,type"},
		{name: "match data keys", body: `"ab".match(/a/).keys.sort.join(",")`, want: "begin,captures,end,named_captures,post_match,pre_match,to_s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s = %q, want %q", tc.name, got.String(), tc.want)
			}
		})
	}
}

// A tagged bag that script code rebuilds is an ordinary bag again: the tag
// vouches only for what the runtime built itself.
func TestRebuiltBagLosesItsTag(t *testing.T) {
	t.Parallel()

	tagged := NewTaggedObject(map[string]Value{"to_s": NewString("real")}, ObjectTagRescuedError)
	if _, substituted := objectStringEntry(tagged); !substituted {
		t.Fatalf("a tagged bag did not render its string form")
	}
	rebuilt := NewObject(tagged.Hash())
	if _, substituted := objectStringEntry(rebuilt); substituted {
		t.Fatalf("a rebuilt bag kept its tag")
	}
}

// The clones the runtime makes to isolate a value rebuild every KindObject,
// which dropped the tag. A match result returned by one Script.Call and passed
// into another therefore rendered as <object> instead of the matched text.
func TestObjectTagsSurviveInternalClones(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def make_match()
  "hello world".match(/w\w+/)
end
def make_error()
  begin
    raise "boom"
  rescue => e
    e
  end
end
def render(v)
  "#{v}"
end
def render_in_task(v)
  "#{v}"
end`)

	tests := []struct {
		name    string
		builder string
		want    string
	}{
		{name: "match data", builder: "make_match", want: "world"},
		{name: "rescued error", builder: "make_error", want: "boom"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			built, err := script.Call(context.Background(), tc.builder, nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.builder, err)
			}
			if built.ObjectTag() == ObjectTagNone {
				t.Fatalf("%s lost its tag crossing the call boundary", tc.name)
			}
			got, err := script.Call(context.Background(), "render", []Value{built}, CallOptions{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s rendered %q after a round trip, want %q", tc.name, got.String(), tc.want)
			}
		})
	}
}

// The tag is preserved only for the runtime's own containment clones. A bag
// script code rebuilds still goes through NewObject and loses it, which is
// what stops a host object from claiming the same treatment.
func TestScriptRebuiltBagsStillLoseTheirTag(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  m = "hello world".match(/w\w+/)
  rebuilt = m.merge({})
  "#{rebuilt}"
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() == "world" {
		t.Fatalf("a script-rebuilt bag kept its tag")
	}
}

// Task payloads go through deepCloneValue, a separate containment path from
// the global cloner, so a rescued error passed into a task rendered as
// <object> instead of its message.
func TestObjectTagsSurviveTaskPayloadCloning(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def work(e)
  "#{e}"
end
def run()
  err = begin
    raise "boom"
  rescue => e
    e
  end
  Tasks.run(max: 2) do |tasks|
    a = tasks.spawn(:work, err)
    a.value
  end
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "boom" {
		t.Fatalf("a rescued error rendered %q inside a task, want boom", got.String())
	}
}

// deepCloneValue backs both the runtime's task containment and the
// script-visible dup/clone. Only the containment copy stands for the same
// value; a bag script code duplicates becomes an ordinary object, so the tag
// cannot be laundered onto content the script then edits.
func TestScriptDuplicationDropsTheTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "rescued error dup", body: `begin
    raise "boom"
  rescue => e
    "#{e.dup}"
  end`},
		{name: "rescued error clone", body: `begin
    raise "boom"
  rescue => e
    "#{e.clone}"
  end`},
		{name: "match data dup", body: `"#{"hello world".match(/w\w+/).dup}"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != "<object>" {
				t.Fatalf("%s rendered %q, want <object>: a duplicate must not keep the tag", tc.name, got.String())
			}
		})
	}
}

// The original still renders, so dropping the tag on duplication does not
// weaken the tagged value itself.
func TestDuplicationDoesNotAffectTheOriginal(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run()
  begin
    raise "boom"
  rescue => e
    copy = e.dup
    "#{e}"
  end
end`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "boom" {
		t.Fatalf("the original rendered %q after being duplicated, want boom", got.String())
	}
}

// The clone cache is keyed by the entry map, but the tag belongs to the
// wrapper. A host that rebuilds a tagged bag with NewObject(tagged.Hash())
// produces two wrappers sharing one map with different tags; returning the
// cached clone as it stands gave the second wrapper the first one's
// provenance, in whichever order they were cloned.
func TestSharedEntryMapsKeepPerWrapperTags(t *testing.T) {
	t.Parallel()

	entries := map[string]Value{
		"to_s": NewString("real"), "message": NewString("real"),
		"class": NewString("E"), "type": NewString("E"),
		"backtrace": NewArray([]Value{}),
	}
	tagged := NewTaggedObject(entries, ObjectTagRescuedError)
	// Same map, no tag: what a host rebuild produces.
	plain := NewObject(entries)

	tests := []struct {
		name  string
		input []Value
	}{
		{name: "tagged first", input: []Value{tagged, plain}},
		{name: "plain first", input: []Value{plain, tagged}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cloned := deepCloneValueForContainment(NewArray(tc.input))
			got := cloned.Array()
			for i, want := range tc.input {
				if got[i].ObjectTag() != want.ObjectTag() {
					t.Fatalf("%s: element %d cloned with tag %v, want %v", tc.name, i, got[i].ObjectTag(), want.ObjectTag())
				}
			}
		})
	}
}

// Script-visible duplication drops the tag whichever wrapper is reached first.
func TestSharedEntryMapsAllLoseTagsOnScriptDuplication(t *testing.T) {
	t.Parallel()

	entries := map[string]Value{"to_s": NewString("real")}
	tagged := NewTaggedObject(entries, ObjectTagRescuedError)
	cloned := deepCloneValue(NewArray([]Value{tagged, NewObject(entries)}))
	for i, elem := range cloned.Array() {
		if elem.ObjectTag() != ObjectTagNone {
			t.Fatalf("element %d kept tag %v through script duplication", i, elem.ObjectTag())
		}
	}
}

// The tag rides in Value.scalar, which every copy carries, so an in-place
// write to the backing map cannot clear it: after e.replace({to_s: "payload"})
// the bag still claimed to be a rescued error and rendered the payload as its
// message. The tag has to keep meaning "the runtime built these entries", so
// the entries cannot be changed.
func TestTaggedBagsRejectInPlaceMutation(t *testing.T) {
	t.Parallel()

	const rescued = `begin
    raise "boom"
  rescue => e
    %s
  end`

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "replace", body: fmt.Sprintf(rescued, `e.replace({to_s: "payload"})`), want: "cannot modify a rescued error"},
		{name: "index assignment", body: fmt.Sprintf(rescued, `e[:to_s] = "payload"`), want: "cannot modify a rescued error"},
		{name: "store", body: fmt.Sprintf(rescued, `e.store(:to_s, "payload")`), want: "cannot modify a rescued error"},
		{name: "merge!", body: fmt.Sprintf(rescued, `e.merge!({to_s: "payload"})`), want: "cannot modify a rescued error"},
		{name: "delete", body: fmt.Sprintf(rescued, `e.delete(:to_s)`), want: "cannot modify a rescued error"},
		{name: "clear", body: fmt.Sprintf(rescued, `e.clear()`), want: "cannot modify a rescued error"},
		{name: "match data replace", body: `"hello world".match(/w\w+/).replace({to_s: "payload"})`, want: "cannot modify match data"},
		{name: "match data index assignment", body: `m = "hello world".match(/w\w+/)
  m[:to_s] = "payload"`, want: "cannot modify match data"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s: a tagged bag was mutated in place", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: error = %v, want it to mention %q", tc.name, err, tc.want)
			}
		})
	}
}

// Reading a tagged bag is unaffected, and the non-mutating transforms still
// work by returning a new hash -- which, being rebuilt, is untagged.
func TestTaggedBagsStillSupportReadsAndCopies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		body string
		want string
	}{
		{body: `begin
    raise "boom"
  rescue => e
    e[:message]
  end`, want: "boom"},
		{body: `begin
    raise "boom"
  rescue => e
    e.keys.length.to_s
  end`, want: "6"},
		// merge returns a new hash rather than mutating, so it is allowed --
		// and the copy is no longer the tagged bag.
		{body: `begin
    raise "boom"
  rescue => e
    ("#{e.merge({extra: 1})}" == "boom").inspect
  end`, want: "false"},
		{body: `"hello world".match(/w\w+/)[:captures].length.to_s`, want: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.body, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.body+"\nend")
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("got %q, want %q", got.String(), tc.want)
			}
		})
	}
}
