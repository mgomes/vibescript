package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// bigToStringClass is a class whose to_s returns a string of the requested
// size. Each result fits a quota that the accumulation of several does not,
// which is the shape the conversion loop has to catch.
const bigToStringClass = `
class Big
  def initialize(n)
    @n = n
  end
  def to_s
    "x" * @n
  end
end
`

// TestFormatChargesConversionsAsTheyAccumulate pins that format reserves the
// strings a script's to_s returns while it is still converting.
//
// format converts every argument before it looks at the pattern, so a to_s runs
// once per operand whether or not the pattern uses it. The results land in a Go
// local slice the estimator cannot reach: each one passed its own check with the
// earlier ones invisible, so many individually quota-sized strings piled up
// unseen (#4).
func TestFormatChargesConversionsAsTheyAccumulate(t *testing.T) {
	t.Parallel()

	// Each conversion is 1 MiB against a 6 MiB quota, so no single one is
	// rejectable and eight of them are.
	src := bigToStringClass + `
def run()
  a = Big.new(1048576)
  format("%s %s %s %s %s %s %s %s", a, a, a, a, a, a, a, a)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 6 << 20}, src)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatal("eight megabyte conversions must not accumulate past the quota")
	}
}

// TestFormatChargesConversionsThePatternNeverUses pins the same thing for
// operands the pattern discards. A pattern that fails validation returns its
// error before any output check runs, so the conversions were the only thing
// that had happened and nothing had counted them.
func TestFormatChargesConversionsThePatternNeverUses(t *testing.T) {
	t.Parallel()

	src := bigToStringClass + `
def run()
  a = Big.new(1048576)
  format("", a, a, a, a, a, a, a, a)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 6 << 20}, src)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("conversions for unused operands must be counted before the pattern is rejected")
	}
	// The unused-operand error means the conversions ran and were not charged;
	// the memory error means they were.
	if strings.Contains(err.Error(), "unused") {
		t.Fatalf("rejected for the pattern rather than the memory it had already built: %v", err)
	}
}

// TestFormatLeavesFittingConversionsAlone pins that the reservation does not
// reject work that fits, so the charge cannot be "refuse everything".
func TestFormatLeavesFittingConversionsAlone(t *testing.T) {
	t.Parallel()

	const size = 64 << 10
	src := bigToStringClass + fmt.Sprintf(`
def run()
  a = Big.new(%d)
  format("%%s|%%s", a, a).bytesize
end`, size)
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20}, src)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("two conversions well under the quota must format: %v", err)
	}
	if want := int64(2*size + 1); got.Int() != want {
		t.Fatalf("formatted length = %d, want %d", got.Int(), want)
	}
}

// aliasToStringClass returns an instance field from to_s, which is the common
// shape: the string handed back is one the argument already holds.
const aliasToStringClass = `
class Alias
  def initialize(n)
    @text = "x" * n
  end
  def to_s
    @text
  end
end
`

// TestFormatDoesNotChargeConversionsTheArgumentsAlreadyHold pins that a
// conversion is charged for what it adds, not for what it hands back.
//
// A to_s that returns an instance field produces no new memory, and several
// arguments can return the same backing. Charging each by its length billed
// memory the arguments were already counted for, which rejects a call whose
// real footprint fits.
func TestFormatDoesNotChargeConversionsTheArgumentsAlreadyHold(t *testing.T) {
	t.Parallel()

	const size = 200 << 10
	src := aliasToStringClass + fmt.Sprintf(`
def run()
  a = Alias.new(%d)
  format("%%s %%s %%s %%s", a, a, a, a).bytesize
end`, size)
	// One 200 KiB payload, aliased four times. The output is charged either
	// way; what the quota catches is the extra 600 KiB of aliases billed as
	// though each conversion were fresh.
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 1300 << 10}, src)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("aliased conversions must not be charged as fresh memory: %v", err)
	}
	if want := int64(4*size + 3); got.Int() != want {
		t.Fatalf("formatted length = %d, want %d", got.Int(), want)
	}
}
