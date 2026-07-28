package runtime

import (
	"bytes"
	"context"
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
