package runtime

import (
	"context"
	"testing"
)

func TestDirectFastPathErrorsRemainRescuable(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run
  begin
    Time.parse("not-a-time")
  rescue
    "rescued"
  end
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if !got.Equal(NewString("rescued")) {
		t.Fatalf("run() = %s, want rescued", got)
	}
}

func TestDirectFastPathInvalidArityEvaluatesArgumentsBeforeRescue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call string
	}{
		{name: "string length", call: `"a".length(Probe.mark())`},
		{name: "string bytesize", call: `"a".bytesize(Probe.mark())`},
		{name: "string index", call: `"abc".index("a", 0, Probe.mark())`},
		{name: "string rindex", call: `"abc".rindex("a", 0, Probe.mark())`},
		{name: "string slice", call: `"abc".slice(0, 1, Probe.mark())`},
		{name: "regex replace all", call: `Regex.replace_all("a", "a", "b", Probe.mark())`},
		{name: "time format", call: `Time.parse("2024-01-02T03:04:05Z").format("2006", Probe.mark())`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `
class Probe
  @@value = 0

  def self.mark
    @@value = 1
  end

  def self.value
    @@value
  end
end

def run
  begin
    `+tc.call+`
  rescue
    nil
  end
  Probe.value
end
`)

			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if !got.Equal(NewInt(1)) {
				t.Fatalf("run() after %s = %s, want 1", tc.call, got)
			}
		})
	}
}
