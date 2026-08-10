package runtime

import (
	"context"
	"strings"
	"testing"
)

// callerHoldCapability drives a block while holding an argument in its own Go
// frame that it never passes on, which is what a host adapter walking a
// collection itself does.
type callerHoldCapability struct{}

func (callerHoldCapability) Bind(CapabilityBinding) (map[string]Value, error) {
	return map[string]Value{
		"drv": NewObject(map[string]Value{
			"hold": NewBuiltin("drv.hold", func(exec *Execution, _ Value, args []Value, _ map[string]Value, block Value) (Value, error) {
				// args[0] is live here for the whole callback and is not passed.
				_, err := exec.CallBlock(block, []Value{NewInt(1)})
				_ = args
				return NewNil(), err
			}),
		}),
	}, nil
}

// minAdmittingQuota returns the smallest memory quota that lets src complete.
func minAdmittingQuota(t *testing.T, src string, seed Value, caps bool) int {
	t.Helper()

	lo, hi := 1<<10, 64<<20
	for lo < hi {
		mid := (lo + hi) / 2
		script := compileScriptWithConfig(t, Config{StepQuota: 900_000_000, MemoryQuotaBytes: mid}, src)
		opts := CallOptions{}
		if caps {
			opts.Capabilities = []CapabilityAdapter{callerHoldCapability{}}
		}
		if _, err := script.Call(context.Background(), "run", []Value{seed}, opts); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// TestCallBlockChargesWhatTheCallerHolds pins that a value a builtin keeps
// while a block it drives runs is counted alongside what the block allocates.
//
// Such a value sits on the driver's Go stack, which no walk reaches. Its bytes
// were counted when the caller evaluated it and again once the call returned,
// but never at the same time as the body's own allocation, so the peak of the
// two together escaped: a 2,000,000-byte default the block supersedes moved the
// smallest admitting quota by nothing at all.
//
// Every shape here holds an expression temporary rather than a named local, so
// the driver's own hold is the only path to it; a value the script still names
// is reachable from the environment and would be charged either way.
func TestCallBlockChargesWhatTheCallerHolds(t *testing.T) {
	t.Parallel()

	// 100 bytes repeated 20,000 times, on both sides of every case.
	seed := NewString(strings.Repeat("abcdefghij", 10))
	const payload = 2_000_000

	cases := []struct {
		name       string
		holds      string
		passesOnly string
		caps       bool
	}{
		{
			name:       "array.fetch ignored default",
			holds:      "def run(s)\n  a = [1]\n  a.fetch(9, s * 20_000) { |i| s * 20_000 }\nend",
			passesOnly: "def run(s)\n  a = [1]\n  a.fetch(9) { |i| s * 20_000 }\nend",
		},
		{
			name:       "hash.fetch ignored default",
			holds:      "def run(s)\n  h = { a: 1 }\n  h.fetch(:zz, s * 20_000) { |k| s * 20_000 }\nend",
			passesOnly: "def run(s)\n  h = { a: 1 }\n  h.fetch(:zz) { |k| s * 20_000 }\nend",
		},
		{
			name:       "host adapter argument",
			holds:      "def run(s)\n  drv.hold(s * 20_000) { |x| s * 20_000 }\nend",
			passesOnly: "def run(s)\n  drv.hold(0) { |x| s * 20_000 }\nend",
			caps:       true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			holds := minAdmittingQuota(t, tc.holds, seed, tc.caps)
			passesOnly := minAdmittingQuota(t, tc.passesOnly, seed, tc.caps)
			if grew := holds - passesOnly; grew < payload*3/4 {
				t.Fatalf("holding %d bytes across the callback moved the smallest admitting "+
					"quota from %d to %d, a change of %d; the caller's value and the body's "+
					"must be live to the quota at the same time", payload, passesOnly, holds, grew)
			}
		})
	}
}

// TestCallBlockDoesNotChargeWhatTheCallerPassed pins the other side: a driver
// whose argument is the block's argument must be charged for it once.
//
// `proc.call` passes on everything it holds, so it never had the gap above.
// Charging the caller's roots without excluding what it passed billed its
// argument twice -- once bound into the block's scope, once as a root -- which
// is the failure a test for the undercharge alone would not have seen.
func TestCallBlockDoesNotChargeWhatTheCallerPassed(t *testing.T) {
	t.Parallel()

	seed := NewString(strings.Repeat("abcdefghij", 10))
	const payload = 2_000_000

	holds := minAdmittingQuota(t, "def run(s)\n  blk = ->(x) { s * 20_000 }\n  blk.call(s * 20_000)\nend", seed, false)
	passesOnly := minAdmittingQuota(t, "def run(s)\n  blk = ->(x) { s * 20_000 }\n  blk.call(0)\nend", seed, false)

	grew := holds - passesOnly
	if grew < payload*3/4 {
		t.Fatalf("the argument moved the quota by %d, want about %d: it is live alongside the "+
			"body and must be charged", grew, payload)
	}
	if grew > payload*3/2 {
		t.Fatalf("the argument moved the quota by %d, want about %d: it is charged once as the "+
			"block's binding and must not be charged again as a root", grew, payload)
	}
}
