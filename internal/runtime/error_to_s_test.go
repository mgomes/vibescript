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

	tagged := NewTaggedObject(map[string]Value{"to_s": NewString("real")}, ObjectTagRescuedError, "real")
	if _, substituted := objectStringEntry(tagged); !substituted {
		t.Fatalf("a tagged bag did not render its string form")
	}
	rebuilt := NewObject(tagged.Hash())
	if _, substituted := objectStringEntry(rebuilt); substituted {
		t.Fatalf("a rebuilt bag kept its tag")
	}
}

// The rendering a tagged bag publishes is fixed at construction, so it
// survives the host boundary intact: a match result or rescued error returned
// by one call and passed into another still renders. What the host cannot do
// is change it, which the mutation tests below cover.
func TestObjectTagsSurviveTheHostBoundary(t *testing.T) {
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
end`)

	tests := []struct {
		builder string
		want    string
	}{
		{builder: "make_match", want: "world"},
		{builder: "make_error", want: "boom"},
	}

	for _, tc := range tests {
		t.Run(tc.builder, func(t *testing.T) {
			t.Parallel()
			built, err := script.Call(context.Background(), tc.builder, nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.builder, err)
			}
			got, err := script.Call(context.Background(), "render", []Value{built}, CallOptions{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("%s rendered %q after a round trip, want %q", tc.builder, got.String(), tc.want)
			}
		})
	}
}

// A host holding the value can rewrite its entries -- Value.Hash() hands out
// the live map, and a builtin registered through Engine.RegisterBuiltin
// receives the value itself -- but the published rendering is fixed at
// construction and is never read back out of them.
func TestHostMutationCannotChangeThePublishedRendering(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("host_touch", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		args[0].Hash()["to_s"] = NewString("payload")
		args[0].Hash()["message"] = NewString("payload")
		return NewNil(), nil
	})
	script, err := engine.Compile("def run()\n  err = begin\n    raise \"boom\"\n  rescue => e\n    e\n  end\n  host_touch(err)\n  \"#{err}\"\nend")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "boom" {
		t.Fatalf("a host builtin rewrote the rendering to %q", got.String())
	}
}

// The same through Value.HashSet on a value the host received back.
func TestHostHashSetCannotChangeThePublishedRendering(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def make()
  begin
    raise "boom"
  rescue => e
    e
  end
end
def render(v)
  "#{v}"
end`)
	built, err := script.Call(context.Background(), "make", nil, CallOptions{})
	if err != nil {
		t.Fatalf("make: %v", err)
	}
	if err := built.HashSet(NewString("to_s"), NewString("payload")); err != nil {
		t.Fatalf("HashSet: %v", err)
	}
	got, err := script.Call(context.Background(), "render", []Value{built}, CallOptions{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got.String() != "boom" {
		t.Fatalf("a host HashSet rewrote the rendering to %q", got.String())
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

// deepCloneValue backs both the runtime's boundary containment and the
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
	tagged := NewTaggedObject(entries, ObjectTagRescuedError, "real")
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
	tagged := NewTaggedObject(entries, ObjectTagRescuedError, "real")
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
		{name: "delete", body: fmt.Sprintf(rescued, `e.delete(:to_s)`), want: "cannot modify a rescued error"},
		{name: "clear", body: fmt.Sprintf(rescued, `e.clear()`), want: "cannot modify a rescued error"},
		{name: "member assignment", body: fmt.Sprintf(rescued, `e.to_s = "payload"`), want: "cannot modify a rescued error"},
		{name: "match data replace", body: `"hello world".match(/w\w+/).replace({to_s: "payload"})`, want: "cannot modify match data"},
		{name: "match data member assignment", body: `m = "hello world".match(/w\w+/)
  m.to_s = "payload"`, want: "cannot modify match data"},
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

// A tagged bag is immutable, so it must not share entries with an untagged
// wrapper over the same map. Rebinding both into one call produced a shared
// clone, and a write through the untagged alias changed what the tagged one
// rendered -- bypassing the mutation guard entirely.
func TestTaggedWrappersDoNotShareEntriesWithUntaggedAliases(t *testing.T) {
	t.Parallel()

	newPair := func() (Value, Value) {
		entries := map[string]Value{
			"to_s": NewString("real"), "message": NewString("real"),
			"class": NewString("E"), "type": NewString("E"),
			"backtrace": NewArray([]Value{}),
		}
		return NewTaggedObject(entries, ObjectTagRescuedError, "real"), NewObject(entries)
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "tagged first", body: `def run(tagged, plain)
  plain[:to_s] = "payload"
  "#{tagged}"
end`},
		{name: "member assignment", body: `def run(tagged, plain)
  plain.to_s = "payload"
  "#{tagged}"
end`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tagged, plain := newPair()
			script := compileScript(t, tc.body)
			got, err := script.Call(context.Background(), "run", []Value{tagged, plain}, CallOptions{})
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.String() != "real" {
				t.Fatalf("%s: the tagged bag rendered %q after a write through an untagged alias", tc.name, got.String())
			}
		})
	}
}

// A cyclic entry map reachable through wrappers with different tags must
// terminate. Caching only the first wrapper left the other uncached, so
// rebinding recursed between them until the Go stack ran out.
func TestCyclicTaggedAndUntaggedWrappersTerminate(t *testing.T) {
	t.Parallel()

	build := func() Value {
		entries := map[string]Value{"to_s": NewString("real")}
		tagged := NewTaggedObject(entries, ObjectTagRescuedError, "real")
		// An untagged wrapper over the same map, reachable from inside it.
		entries["self"] = NewObject(entries)
		return tagged
	}

	t.Run("rebound as a call argument", func(t *testing.T) {
		t.Parallel()
		script := compileScript(t, "def run(v)\n  \"#{v}\"\nend")
		got, err := script.Call(context.Background(), "run", []Value{build()}, CallOptions{})
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		if got.String() != "real" {
			t.Fatalf("rendered %q, want real", got.String())
		}
	})

	t.Run("cloned for containment", func(t *testing.T) {
		t.Parallel()
		cloned := deepCloneValueForContainment(build())
		if cloned.ObjectTag() != ObjectTagRescuedError {
			t.Fatalf("the containment clone lost its tag")
		}
		self, ok := cloned.Hash()["self"]
		if !ok {
			t.Fatalf("the cycle entry did not survive cloning")
		}
		if self.ObjectTag() != ObjectTagNone {
			t.Fatalf("the untagged wrapper inside the cycle gained tag %v", self.ObjectTag())
		}
	})
}

// Capability boundaries copy values to isolate them, and the copy stands for
// the same value, so a bag the runtime built keeps its provenance across them.
func TestCapabilityBoundaryPreservesTags(t *testing.T) {
	t.Parallel()

	entries := map[string]Value{
		"to_s": NewString("boom"), "message": NewString("boom"),
		"class": NewString("RuntimeError"), "type": NewString("RuntimeError"),
		"backtrace": NewArray([]Value{}),
	}
	tagged := NewTaggedObject(entries, ObjectTagRescuedError, "boom")

	t.Run("arguments into the adapter", func(t *testing.T) {
		t.Parallel()
		payload := cloneHash(map[string]Value{"error": tagged})
		if got := payload["error"].ObjectTag(); got != ObjectTagRescuedError {
			t.Fatalf("a capability argument lost its tag (%v)", got)
		}
	})

	t.Run("results back from the adapter", func(t *testing.T) {
		t.Parallel()
		cloned, err := cloneCapabilityDataOnlyValue("probe.result", tagged)
		if err != nil {
			t.Fatalf("clone: %v", err)
		}
		if got := cloned.ObjectTag(); got != ObjectTagRescuedError {
			t.Fatalf("a capability result lost its tag (%v)", got)
		}
	})
}

// The tag governs a bag's rendering and whether it accepts writes, not its
// identity: collections are values, so two bags with the same entries are the
// same value however each was built. What the tag must keep doing is give the
// same answer inside a call as outside it, which containment cloning made hard
// when the tag was part of identity.
func TestObjectEqualityIgnoresTheTag(t *testing.T) {
	t.Parallel()

	entries := map[string]Value{"to_s": NewString("real")}
	tagged := NewTaggedObject(entries, ObjectTagRescuedError, "real")
	plain := NewObject(entries)

	if !tagged.Identical(plain) {
		t.Fatalf("bags with the same entries are the same value whatever their provenance")
	}

	// The answer must not change across the call boundary.
	script := compileScript(t, "def run(a, b)\n  a.equal?(b).inspect\nend")
	got, err := script.Call(context.Background(), "run", []Value{tagged, plain}, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "true" {
		t.Fatalf("equal? inside the script = %s, want true as it is outside", got.String())
	}
}

// A capability result can contain two wrappers over one entry map with
// different provenance -- a host receives a tagged bag, wraps its live Hash()
// with NewObject, and returns both. Caching the clone by entry map alone gave
// the plain wrapper the tag or stripped the tagged one's rendering, depending
// on which was cloned first.
func TestCapabilityResultKeepsPerWrapperTags(t *testing.T) {
	t.Parallel()

	entries := map[string]Value{"to_s": NewString("boom"), "message": NewString("boom")}

	tests := []struct {
		name  string
		order func(tagged, plain Value) []Value
		want  []ObjectTag
	}{
		{
			name:  "tagged first",
			order: func(tagged, plain Value) []Value { return []Value{tagged, plain} },
			want:  []ObjectTag{ObjectTagRescuedError, ObjectTagNone},
		},
		{
			name:  "plain first",
			order: func(tagged, plain Value) []Value { return []Value{plain, tagged} },
			want:  []ObjectTag{ObjectTagNone, ObjectTagRescuedError},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tagged := NewTaggedObject(entries, ObjectTagRescuedError, "boom")
			plain := NewObject(entries)
			cloned, err := cloneCapabilityDataOnlyValue("probe.result", NewArray(tc.order(tagged, plain)))
			if err != nil {
				t.Fatalf("clone: %v", err)
			}
			for i, item := range cloned.Array() {
				if item.ObjectTag() != tc.want[i] {
					t.Fatalf("%s: element %d cloned with tag %v, want %v", tc.name, i, item.ObjectTag(), tc.want[i])
				}
			}
		})
	}
}
