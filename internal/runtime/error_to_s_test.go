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

// An attribute bag with no string to_s entry keeps its existing rendering, so
// match data is unaffected.
func TestAttributeBagWithoutStringEntryIsUnchanged(t *testing.T) {
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
	if got.String() != "<object>" {
		t.Fatalf("match data rendered as %q, want the unchanged <object>", got.String())
	}
}
