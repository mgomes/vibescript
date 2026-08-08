package runtime

import (
	"context"
	"fmt"
	goruntime "runtime"
	"strings"
	"sync/atomic"
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
	// rejectable and eight of them are. The pattern truncates each to three
	// bytes, so the output is tiny: what the quota has to catch is the
	// conversions themselves, not the string they end up producing.
	src := bigToStringClass + `
def run()
  a = Big.new(1048576)
  format("%.3s%.3s%.3s%.3s%.3s%.3s%.3s%.3s", a, a, a, a, a, a, a, a)
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

// TestFormatChargesAConversionAgainstTheOutputItProduces pins that a conversion
// stays charged through the render, where it coexists with the string it
// produces.
//
// The checks inside the render measure the arguments, which hold the instance
// rather than what its to_s returned, so a megabyte conversion and the megabyte
// of output copied from it were never weighed together. Assigning the same
// value to a script variable first tripped the quota, which is the giveaway
// that the footprint was real and simply unseen.
func TestFormatChargesAConversionAgainstTheOutputItProduces(t *testing.T) {
	t.Parallel()

	src := bigToStringClass + `
def run()
  format("%s", Big.new(1048576))
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 3 << 19}, src)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatal("a conversion and the output copied from it must be weighed together")
	}
}

// TestFormatSeesEarlierConversionsInsideALaterToString pins that a conversion
// already in hand is visible to the memory checks that run inside whatever
// to_s runs next.
//
// The results live in a Go local that no walk reaches, so a later to_s
// allocating a large temporary was weighed against a baseline missing every
// conversion before it, and each one passed on its own.
func TestFormatSeesEarlierConversionsInsideALaterToString(t *testing.T) {
	t.Parallel()

	// The first argument converts to 1 MiB and is retained; the second builds a
	// 1 MiB temporary inside its own to_s. Neither fits beside the other under
	// this quota, and the temporary is what has to notice.
	src := `
class Kept
  def to_s
    "x" * 1048576
  end
end
class Temp
  def to_s
    big = "y" * 1048576
    big.byteslice(0, 1)
  end
end

def run()
  format("%.1s%.1s", Kept.new, Temp.new)
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 3 << 19}, src)
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil {
		t.Fatal("a temporary inside a later to_s must be weighed against the conversions already held")
	}
}

// TestFormatWeighsConversionsWithTheArgumentsTheyCameFrom pins that the check
// during conversion sees the arguments as well as what they converted to.
//
// The arguments are a builtin's Go locals, so a check on the execution alone
// does not reach them. Temporary instances could therefore fit the quota before
// dispatch while their conversions fit the conversion check separately, and a
// pattern that rejects returns before the render check that would have seen
// both.
func TestFormatWeighsConversionsWithTheArgumentsTheyCameFrom(t *testing.T) {
	t.Parallel()

	// Each instance holds 512 KiB and converts to a fresh 512 KiB, so the two
	// arguments are 1 MiB and their conversions another 1 MiB. At 2 MiB the
	// quota fits either side alone and not the two together, which is the only
	// window where the difference shows.
	src := `
class Both
  def initialize(n)
    @text = "x" * n
  end
  def to_s
    @text + "!"
  end
end

def run()
  format("", Both.new(524288), Both.new(524288))
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 2 << 20}, src)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("conversions must be weighed against the arguments they came from")
	}
	if strings.Contains(err.Error(), "unused") {
		t.Fatalf("rejected for the pattern rather than the memory it had built: %v", err)
	}
}

// heapProbeCapability reports the process heap, measured from inside the
// script so a value that is only reachable while the call is still running can
// be observed at all.
type heapProbeCapability struct{ heap *atomic.Int64 }

func (c heapProbeCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	heap := c.heap
	return map[string]Value{
		"probe": NewObject(map[string]Value{
			"heap": NewBuiltin("probe.heap", func(*Execution, Value, []Value, map[string]Value, Value) (Value, error) {
				var stats goruntime.MemStats
				goruntime.GC()
				goruntime.ReadMemStats(&stats)
				heap.Store(int64(stats.HeapAlloc))
				return NewNil(), nil
			}),
		}),
	}, nil
}

// TestFormatDoesNotRetainConversionsAfterTheyAreReleased pins that a released
// conversion stops being reachable, not merely uncounted.
//
// The retained set is a stack, and shortening a Go slice leaves the pointers in
// its backing array: the walk reads the visible length and stops counting the
// conversion, while the collector goes on holding it. That is the same
// retained-but-uncounted shape the registration exists to expose, and it is why
// the slots are cleared rather than just dropped.
//
// The heap is sampled from inside the call, because the stale slot is
// overwritten by the next conversion and the whole execution is gone by the
// time Call returns -- so nothing about it is observable from outside.
//
// Not parallel: it measures process-wide heap.
func TestFormatDoesNotRetainConversionsAfterTheyAreReleased(t *testing.T) {
	script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: 64 << 20},
		bigToStringClass+`
def run()
  format("%.1s", Big.new(8388608))
  probe.heap()
  0
end`)

	var during atomic.Int64
	var before goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	if _, err := script.Call(context.Background(), "run", nil,
		callOptionsWithCapabilities(heapProbeCapability{heap: &during})); err != nil {
		t.Fatalf("formatting failed: %v", err)
	}

	held := during.Load() - int64(before.HeapAlloc)
	// The 8 MiB conversion is dead once format returns: nothing in the script
	// holds it, so only a stale slot could.
	if limit := int64(4 << 20); held > limit {
		t.Fatalf("a released conversion still holds %.2f MiB, want under %.2f MiB",
			float64(held)/(1<<20), float64(limit)/(1<<20))
	}
}
