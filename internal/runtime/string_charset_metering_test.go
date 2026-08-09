package runtime

import (
	"context"
	"strings"
	"testing"
)

// nonMatchingCharSet builds a character set of n distinct runes, none of which
// occurs in the receivers used below, so every lookup walks the whole entry
// list and the cost of the call is the product of receiver and set.
func nonMatchingCharSet(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteRune(rune(0x3000 + i))
	}
	return b.String()
}

func callCharSetOp(t *testing.T, quota int, expr string, receiver, set Value) error {
	t.Helper()
	script := compileScriptWithConfig(t, Config{StepQuota: quota, MemoryQuotaBytes: Unlimited},
		"def run(s, c)\n  "+expr+"\nend")
	_, err := script.Call(context.Background(), "run", []Value{receiver, set}, CallOptions{})
	return err
}

// minStepsForCharSetOp binary-searches the smallest step quota that lets one
// character-set call complete.
func minStepsForCharSetOp(t *testing.T, expr string, receiver, set Value) int {
	t.Helper()
	lo, hi := 1, 1<<21
	for lo < hi {
		mid := (lo + hi) / 2
		if err := callCharSetOp(t, mid, expr, receiver, set); err != nil {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if err := callCharSetOp(t, lo, expr, receiver, set); err != nil {
		t.Fatalf("%s never completed within %d steps: %v", expr, lo, err)
	}
	return lo
}

// A character-set call charges one step per receiver character but compares
// that character against every entry of the set, so its real cost is the
// product of the two arguments. Over a fixed 100 KB receiver, growing the set
// from 1 entry to 8192 took count from 1.1ms to 750ms and tr from 1.6ms to
// 5.9s while both calls charged exactly 100,000 steps either way (#26).
//
// Here the same 4096-character receiver runs against a 1-entry set and a
// 1024-entry set under one quota: the small set costs about 4.2k steps and the
// large one about 70k, so the quota admits the first and stops the second.
// Without the entry charge both cost the same 4.2k steps and the large set
// passes.
func TestStringCharSetProbesChargeStepQuota(t *testing.T) {
	t.Parallel()

	const quota = 16384
	receiver := NewString(strings.Repeat("z", 4096))
	small := NewString("q")
	large := NewString(nonMatchingCharSet(1024))

	for _, expr := range []string{`s.count(c)`, `s.delete(c)`, `s.tr(c, "q")`, `s.squeeze(c)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			if err := callCharSetOp(t, quota, expr, receiver, small); err != nil {
				t.Fatalf("%s over a one entry character set must fit in %d steps: %v", expr, quota, err)
			}
			err := callCharSetOp(t, quota, expr, receiver, large)
			if err == nil || !strings.Contains(err.Error(), "step quota exceeded") {
				t.Fatalf("%s over a 1024 entry character set must exhaust %d steps, got %v",
					expr, quota, err)
			}
		})
	}
}

// delete, tr and squeeze size their result in one pass and write it in a
// second, repeating every character-set comparison. Only the sizing pass
// charges steps for the receiver, so a second pass billed nothing would let
// half the comparisons run free (#26). Each of the three must therefore cost
// meaningfully more than count, which performs the same comparisons once.
func TestStringCharSetOutputPassChargesProbes(t *testing.T) {
	t.Parallel()

	receiver := NewString(strings.Repeat("z", 1024))
	set := NewString(nonMatchingCharSet(256))
	onePass := minStepsForCharSetOp(t, `s.count(c)`, receiver, set)

	for _, expr := range []string{`s.delete(c)`, `s.tr(c, "q")`, `s.squeeze(c)`} {
		t.Run(expr, func(t *testing.T) {
			t.Parallel()

			twoPass := minStepsForCharSetOp(t, expr, receiver, set)
			// Two passes over the same comparisons cost about twice one pass.
			// Require 1.5x so the bound tracks the second pass being billed at
			// all rather than the exact ratio.
			if twoPass*2 < onePass*3 {
				t.Errorf("%s cost %d steps and count cost %d over the same receiver and "+
					"character set; a method that compares every entry twice must cost "+
					"meaningfully more, so its output pass is unmetered", expr, twoPass, onePass)
			}
		})
	}
}

// The entry charge must not reach ordinary calls. A short set probed over a
// short receiver accumulates a handful of comparisons, far below the one step
// per stringScanBytesPerStep the residue settles at, so these calls cost what
// they always did and still return the right answers.
func TestStringCharSetOrdinaryCallsStayCheap(t *testing.T) {
	t.Parallel()

	const quota = 500
	script := compileScriptWithConfig(t, Config{StepQuota: quota}, `
def run()
  [
    "hello".delete("l"),
    "hello".count("lo"),
    "banana".tr("an", "AN"),
    "bookkeeper".squeeze,
    "hello   world".squeeze(" ")
  ]
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{
		NewString("heo"),
		NewInt(3),
		NewString("bANANA"),
		NewString("bokeper"),
		NewString("hello world"),
	})
}
