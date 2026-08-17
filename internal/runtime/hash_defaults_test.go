package runtime

import (
	"testing"
)

// Per-hash defaults are gone (ADR-006 item 3): a missing key reads as nil and
// `fetch` supplies the fallback per lookup, so `Hash.new` is a bare constructor.
func TestHashNewTakesNoDefault(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def bare()
  h = Hash.new
  h["a"] = 1
  h["a"].to_s + "|" + h["missing"].inspect + "|" + h.size.to_s
end

def with_value_default()
  Hash.new(0)
end

def missing_key_reads_nil()
  h = {}
  h[:missing].inspect
end

def fetch_supplies_the_fallback()
  h = {}
  h.fetch(:missing, 0).to_s + "|" + h.size.to_s
end
`)

	if got := callFunc(t, script, "bare", nil).String(); got != "1|nil|1" {
		t.Fatalf("Hash.new = %q, want %q", got, "1|nil|1")
	}
	if got := callFunc(t, script, "missing_key_reads_nil", nil).String(); got != "nil" {
		t.Fatalf("missing key = %q, want %q", got, "nil")
	}
	if got := callFunc(t, script, "fetch_supplies_the_fallback", nil).String(); got != "0|0" {
		t.Fatalf("fetch fallback = %q, want %q", got, "0|0")
	}
	requireCallErrorContains(t, script, "with_value_default", nil, CallOptions{},
		"Hash.new takes no default")
}

func TestHashDefaultMembersAreGone(t *testing.T) {
	t.Parallel()

	for _, member := range []string{"default", "default_proc"} {
		t.Run(member, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  {}."+member+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{},
				"unknown hash method "+member)
		})
	}
}
