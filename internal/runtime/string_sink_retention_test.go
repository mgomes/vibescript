package runtime

import (
	"context"
	goruntime "runtime"
	"strings"
	"testing"
)

// detachSubjectBytes is the receiver size the ordering test below builds its
// cases from. String#scan caps its subject at maxRegexInputBytes, so it sits
// comfortably under that while staying large enough that a second copy of it is
// unmistakable in an allocation count.
const detachSubjectBytes = 600 << 10

// allocatedByRejectedCall reports how many bytes the process allocated while
// running a call the memory quota must reject, and fails if the call succeeds.
//
// A detaching copy has to be weighed before it is made, not after: a check that
// runs once the copy exists reports the quota correctly but has already
// allocated past it. Requiring the rejection is what stops the opposite
// regression -- handing the undetached window back on the failure path would
// make the call succeed and reintroduce the retention these tests exist for.
//
// Callers are not parallel: TotalAlloc is process-wide.
func allocatedByRejectedCall(t *testing.T, quota int, source, arg string) int64 {
	t.Helper()

	script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: quota}, source)
	var before, after goruntime.MemStats
	goruntime.GC()
	goruntime.ReadMemStats(&before)
	got, err := script.Call(context.Background(), "run", []Value{NewString(arg)}, CallOptions{})
	goruntime.ReadMemStats(&after)
	if err == nil {
		t.Fatalf("the call must be rejected by the quota, got %s", got.Inspect())
	}
	return int64(after.TotalAlloc) - int64(before.TotalAlloc)
}

// TestDetachingCopiesAreReservedBeforeTheyAreMade pins that a member rejected by
// the memory quota has not already copied its result out of the receiver.
//
// String#scan and String#each_line built their copy first and let the next check
// -- the accumulator's per-element charge, or the block call -- discover the
// overrun afterwards, so a rejected call allocated 0.64 MiB, 0.60 MiB and 4.02
// MiB respectively past quotas of 900 KiB, 900 KiB and 6 MiB. lines, split and
// strip project or reserve their copies up front and are here as controls.
//
// Not parallel: it measures process-wide allocation.
func TestDetachingCopiesAreReservedBeforeTheyAreMade(t *testing.T) {
	subject := strings.Repeat("a", detachSubjectBytes)
	// Room for the receiver and its overhead, but not for a second copy of it.
	const quota = detachSubjectBytes + (detachSubjectBytes / 2)

	for _, tc := range []struct {
		name   string
		source string
		arg    string
	}{
		{"scan", `def run(s)
  s.scan("a+")
end`, "b" + subject},
		{"scan with a block", `def run(s)
  kept = []
  s.scan("a+") { |m| kept.push(m) }
  kept
end`, "b" + subject},
		{"each_line", `def run(s)
  kept = []
  s.each_line { |l| kept.push(l) }
  kept
end`, subject + "\nb"},
		{"lines (control)", `def run(s)
  s.lines
end`, subject + "\nb"},
		{"split (control)", `def run(s)
  s.split("|")
end`, subject + "|b"},
		{"strip (control)", `def run(s)
  s.strip
end`, " " + subject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocated := allocatedByRejectedCall(t, quota, tc.source, tc.arg)
			if limit := int64(detachSubjectBytes / 2); allocated > limit {
				t.Fatalf("a rejected call allocated %.2f MiB, want under %.2f MiB: the copy was made before it was weighed",
					float64(allocated)/(1<<20), float64(limit)/(1<<20))
			}
		})
	}
}

// whitespaceRetentionSeed is a padding-only seed, so `seed * 200` in
// retentionScript is a megabyte of whitespace that a strip reduces to whatever
// content is appended to it.
func whitespaceRetentionSeed() string {
	return strings.Repeat(" ", 5_000)
}

// TestStripFamilyDoesNotRetainItsBacking pins that a stripped string stops
// holding the string it was trimmed from.
//
// strip, lstrip and rstrip return a window onto the receiver, so padding that
// dwarfs the content leaves the result tiny while it pins the whole receiver: a
// one-character strip of a megabyte of whitespace was charged one byte and held
// the megabyte, and 200 of them retained 192.2 MiB under an 8 MiB quota.
//
// Not parallel: it measures process-wide heap.
func TestStripFamilyDoesNotRetainItsBacking(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"strip", `(big + "x").strip`},
		{"strip!", `(big + "x").strip!`},
		{"lstrip", `(big + "x").lstrip`},
		{"lstrip!", `(big + "x").lstrip!`},
		{"rstrip", `("x" + big).rstrip`},
		{"rstrip!", `("x" + big).rstrip!`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), whitespaceRetentionSeed(), 200)
			assertUnderRetentionLimit(t, "stripped strings", held)
		})
	}
}

// TestAffixRemovalDoesNotRetainItsBacking pins that chomp, delete_prefix and
// delete_suffix stop holding the string they trimmed.
//
// Each removes an amount the caller chooses -- a whole separator, every
// trailing newline, an entire prefix or suffix -- so one call can leave a
// one-character result pinning a megabyte. Each shape below was charged one
// byte, held the megabyte, and retained 192.1 MiB across 200 of them under an
// 8 MiB quota.
//
// The argumentless chomp and chop are deliberately absent: they remove a fixed
// few bytes, so their result is never small enough to amplify anything (see
// chompDefault).
//
// Not parallel: it measures process-wide heap.
func TestAffixRemovalDoesNotRetainItsBacking(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed string
		expr string
	}{
		{"chomp separator", retentionSeed(), `("x" + big).chomp(big)`},
		{"chomp! separator", retentionSeed(), `("x" + big).chomp!(big)`},
		{"chomp newlines", newlineRetentionSeed(), `("x" + big).chomp("")`},
		{"chomp! newlines", newlineRetentionSeed(), `("x" + big).chomp!("")`},
		{"delete_prefix", retentionSeed(), `(big + "x").delete_prefix(big)`},
		{"delete_prefix!", retentionSeed(), `(big + "x").delete_prefix!(big)`},
		{"delete_suffix", retentionSeed(), `("x" + big).delete_suffix(big)`},
		{"delete_suffix!", retentionSeed(), `("x" + big).delete_suffix!(big)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), tc.seed, 200)
			assertUnderRetentionLimit(t, "trimmed strings", held)
		})
	}
}

// TestSplitPartsDoNotRetainTheirSubject pins that a kept split part stops
// holding the string it was cut from.
//
// Every part is a window onto the subject, so keeping one short part pinned the
// whole subject: each shape below was charged about one byte, held a megabyte,
// and retained 192.1 MiB across 200 of them under an 8 MiB quota. Both the
// separator and the whitespace forms are covered, and the limit argument as
// well, because each builds its parts in a different loop.
//
// Not parallel: it measures process-wide heap.
func TestSplitPartsDoNotRetainTheirSubject(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed string
		expr string
	}{
		{"separator", retentionSeed(), `(big + "|x").split("|")[1]`},
		{"separator with limit", retentionSeed(), `(big + "|x").split("|", -1)[1]`},
		{"whitespace", retentionSeed(), `(big + " x").split[1]`},
		{"whitespace with limit", retentionSeed(), `(big + " x").split(" ", -1)[1]`},
		{"empty separator", retentionSeed(), `(big + "x").split("", 2)[0]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), tc.seed, 200)
			assertUnderRetentionLimit(t, "split parts", held)
		})
	}
}

// TestSplitKeepsItsParts pins that detaching the parts did not change what
// String#split returns across the separator, whitespace, empty-separator and
// limit forms.
func TestSplitKeepsItsParts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.split("|"), s.split("|", 2), s.split("|", -1), s.split, s.split(" "),
   s.split(""), s.split("", 2), s.split(nil), s.split("zz"), s.split(s)]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("a漢|b| ")}, CallOptions{})
	if err != nil {
		t.Fatalf("split failed: %v", err)
	}
	want := `[["a漢", "b", " "], ["a漢", "b| "], ["a漢", "b", " "], ["a漢|b|"], ["a漢|b|"], ` +
		`["a", "漢", "|", "b", "|", " "], ["a", "漢|b| "], ["a漢|b|"], ["a漢|b| "], []]`
	if got.Inspect() != want {
		t.Fatalf("splits = %s, want %s", got.Inspect(), want)
	}
}

// TestLinesDoNotRetainTheirSubject pins that a kept line stops holding the
// string it was cut from, both from the array String#lines returns and from the
// value String#each_line yields.
//
// Each line is a window onto the receiver, so keeping one short line pinned the
// whole receiver: 200 of them retained 192.1 MiB under an 8 MiB quota.
//
// Not parallel: it measures process-wide heap.
func TestLinesDoNotRetainTheirSubject(t *testing.T) {
	t.Run("lines", func(t *testing.T) {
		held := retainedHeapBytes(t, retentionScript(t, `(big + "\nx").lines[1]`), retentionSeed(), 200)
		assertUnderRetentionLimit(t, "lines", held)
	})

	t.Run("each_line", func(t *testing.T) {
		script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20},
			`def run(seed)
  kept = []
  i = 0
  while i < 200
    big = seed * 200 + "\nx"
    big.each_line do |line|
      if line.length == 1
        kept.push(line)
      end
    end
    i = i + 1
  end
  kept
end`)
		held := retainedHeapBytes(t, script, retentionSeed(), 200)
		assertUnderRetentionLimit(t, "yielded lines", held)
	})
}

// TestLinesKeepTheirCharacters pins that detaching the lines did not change
// what String#lines and String#each_line yield, including the trailing line
// ending each line keeps and a receiver with no ending at all.
func TestLinesKeepTheirCharacters(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  yielded = []
  s.each_line { |line| yielded.push(line) }
  [s.lines, yielded]
end`)
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"multiple lines", "a漢\nb\n", `[["a漢\n", "b\n"], ["a漢\n", "b\n"]]`},
		{"no trailing ending", "a\nb", `[["a\n", "b"], ["a\n", "b"]]`},
		{"no ending at all", "a漢b", `[["a漢b"], ["a漢b"]]`},
		{"blank lines", "\n\n", `[["\n", "\n"], ["\n", "\n"]]`},
		{"empty", "", `[[], []]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := script.Call(context.Background(), "run", []Value{NewString(tc.in)}, CallOptions{})
			if err != nil {
				t.Fatalf("lines failed: %v", err)
			}
			if got.Inspect() != tc.want {
				t.Fatalf("lines over %q = %s, want %s", tc.in, got.Inspect(), tc.want)
			}
		})
	}
}

// TestScanMatchesDoNotRetainTheirSubject pins that a kept scan match stops
// holding the string it was found in.
//
// A match is a window onto the subject, so however little it matched it pinned
// the whole subject: 200 three-character matches of a megabyte retained 192.1
// MiB under an 8 MiB quota. The capture form is covered as well, since it builds
// its pieces in a separate loop, and so is the block form, whose yielded value
// a block can keep.
//
// Not parallel: it measures process-wide heap.
func TestScanMatchesDoNotRetainTheirSubject(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
	}{
		{"whole match", `(big + "zzz").scan("zzz")[0]`},
		{"capture", `(big + "zzz").scan("(z)z")[0][0]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			held := retainedHeapBytes(t, retentionScript(t, tc.expr), retentionSeed(), 200)
			assertUnderRetentionLimit(t, "scan matches", held)
		})
	}

	t.Run("block", func(t *testing.T) {
		script := compileScriptWithConfig(t, Config{StepQuota: 50_000_000, MemoryQuotaBytes: 8 << 20},
			`def run(seed)
  kept = []
  i = 0
  while i < 200
    big = seed * 200 + "zzz"
    big.scan("zzz") { |m| kept.push(m) }
    i = i + 1
  end
  kept
end`)
		held := retainedHeapBytes(t, script, retentionSeed(), 200)
		assertUnderRetentionLimit(t, "yielded scan matches", held)
	})
}

// TestScanKeepsItsMatches pins that detaching the matches did not change what
// String#scan returns, across the no-capture, capture and non-participating
// group shapes.
func TestScanKeepsItsMatches(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  yielded = []
  s.scan("(a)|(漢)") { |m| yielded.push(m) }
  [s.scan("漢"), s.scan("(a)(b)"), s.scan("(a)|(漢)"), s.scan("zz"), yielded]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("ab漢ab")}, CallOptions{})
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	want := `[["漢"], [["a", "b"], ["a", "b"]], [["a", nil], [nil, "漢"], ["a", nil]], [], ` +
		`[["a", nil], [nil, "漢"], ["a", nil]]]`
	if got.Inspect() != want {
		t.Fatalf("scans = %s, want %s", got.Inspect(), want)
	}
}

// newlineRetentionSeed is a newline-only seed, so `seed * 200` in
// retentionScript is a megabyte that chomp("") reduces to whatever content
// precedes it.
func newlineRetentionSeed() string {
	return strings.Repeat("\n", 5_000)
}

// TestAffixRemovalKeepsItsCharacters pins that detaching the result did not
// change what chomp, delete_prefix and delete_suffix yield, across the
// separator forms Ruby distinguishes and the affixes that do not match.
func TestAffixRemovalKeepsItsCharacters(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.chomp, s.chomp(""), s.chomp("b"), s.chomp(nil), s.chomp("zz"), s.chomp(s),
   s.chomp!, s.chomp!(""), s.chomp!("b"), s.chomp!(nil), s.chomp!("zz"), s.chomp!(s),
   s.delete_prefix("a"), s.delete_prefix("zz"), s.delete_prefix(s), s.delete_prefix(""),
   s.delete_suffix("b"), s.delete_suffix("zz"), s.delete_suffix(s), s.delete_suffix(""),
   s.delete_prefix!("a"), s.delete_prefix!("zz"), s.delete_suffix!("b"), s.delete_suffix!("zz")]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("a漢b")}, CallOptions{})
	if err != nil {
		t.Fatalf("affix removal failed: %v", err)
	}
	want := `["a漢b", "a漢b", "a漢", "a漢b", "a漢b", "", ` +
		`nil, nil, "a漢", nil, nil, "", ` +
		`"漢b", "a漢b", "", "a漢b", ` +
		`"a漢", "a漢b", "", "a漢b", ` +
		`"漢b", nil, "a漢", nil]`
	if got.Inspect() != want {
		t.Fatalf("affix removals = %s, want %s", got.Inspect(), want)
	}
}

// TestChompStripsEveryTrailingNewline pins the multi-newline behavior of
// chomp("") through the detaching path, which the single-line cases above
// cannot distinguish from removing one ending.
func TestChompStripsEveryTrailingNewline(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.chomp, s.chomp(""), s.chomp!, s.chomp!("")]
end`)
	got, err := script.Call(context.Background(), "run", []Value{NewString("a\r\n\r\n\n")}, CallOptions{})
	if err != nil {
		t.Fatalf("chomp failed: %v", err)
	}
	results := got.Array()
	for i, want := range []string{"a\r\n\r\n", "a", "a\r\n\r\n", "a"} {
		if results[i].String() != want {
			t.Fatalf("chomp result %d = %q, want %q", i, results[i].String(), want)
		}
	}
}

// TestStripFamilyKeepsItsCharacters pins that detaching the result did not
// change what the strip family yields, including the NUL byte Ruby's strip
// treats as whitespace, an all-whitespace receiver, a receiver with nothing to
// trim, and the nil the mutator forms return when they change nothing.
//
// The results are compared as values rather than through Inspect: the receivers
// here are made of control characters, and an escaped rendering would compare
// the escaping as much as the trimming.
func TestStripFamilyKeepsItsCharacters(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(s)
  [s.strip, s.lstrip, s.rstrip, s.strip!, s.lstrip!, s.rstrip!]
end`)
	// nil marks a mutator that must report "no change" rather than a string.
	const unchanged = "\x01no change\x01"
	for _, tc := range []struct {
		name string
		in   string
		want [6]string
	}{
		{
			"padded",
			" \t\na\x00b \r\n",
			[6]string{"a\x00b", "a\x00b \r\n", " \t\na\x00b", "a\x00b", "a\x00b \r\n", " \t\na\x00b"},
		},
		{"untrimmed", "a漢b", [6]string{"a漢b", "a漢b", "a漢b", unchanged, unchanged, unchanged}},
		{"all whitespace", " \t\n", [6]string{"", "", "", "", "", ""}},
		{"empty", "", [6]string{"", "", "", unchanged, unchanged, unchanged}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := script.Call(context.Background(), "run", []Value{NewString(tc.in)}, CallOptions{})
			if err != nil {
				t.Fatalf("strip family failed: %v", err)
			}
			results := got.Array()
			if len(results) != len(tc.want) {
				t.Fatalf("strip family over %q returned %d results, want %d", tc.in, len(results), len(tc.want))
			}
			for i, want := range tc.want {
				if want == unchanged {
					if !results[i].IsNil() {
						t.Fatalf("result %d over %q = %q, want nil", i, tc.in, results[i].String())
					}
					continue
				}
				if results[i].Kind() != KindString || results[i].String() != want {
					t.Fatalf("result %d over %q = %s, want %q", i, tc.in, results[i].Inspect(), want)
				}
			}
		})
	}
}

// lowestAllocOverRuns reports the smallest number of bytes any of several
// identical calls allocated, and asserts each one was accepted or rejected as
// expected. The minimum discards sporadic background allocation from other
// goroutines, which only ever adds.
//
// Callers are not parallel: TotalAlloc is process-wide.
func lowestAllocOverRuns(t *testing.T, quota int, source, arg string, wantAccepted bool) int64 {
	t.Helper()

	lowest := int64(1) << 62
	for range 5 {
		script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: quota}, source)
		var before, after goruntime.MemStats
		goruntime.GC()
		goruntime.ReadMemStats(&before)
		_, err := script.Call(context.Background(), "run", []Value{NewString(arg)}, CallOptions{})
		goruntime.ReadMemStats(&after)
		if accepted := err == nil; accepted != wantAccepted {
			t.Fatalf("quota %d: accepted = %v, want %v (err=%v)", quota, accepted, wantAccepted, err)
		}
		lowest = min(lowest, int64(after.TotalAlloc-before.TotalAlloc))
	}
	return lowest
}

// TestScanWeighsItsIndexTableWithItsOutput pins that the scan preflight counts
// everything the array form holds at once.
//
// The engine's index table stays live the whole time the elements are built from
// it, so the peak is roots + table + slots + every element's payload. The
// preflight weighed the output without the table while the accumulator's seed
// weighed the table without the output, and only the per-element check combined
// them -- after a match had been copied.
//
// The assertion is how much a rejected scan saves against an accepted one over
// the same subject, not how much it allocates. An absolute figure is not
// portable: the race detector adds about 7.3 MiB of its own bookkeeping to both,
// which swamped a threshold calibrated on an uninstrumented allocator and turned
// this test red in CI while the behavior was unchanged. The saving is the same
// quantity either way -- an accepted scan copies 5,000 small matches and one
// 700 KB match, a correctly rejected one copies none of them -- so it survives
// the instrument: 1.74 MiB against 1.51 MiB under -race, where copying before
// rejecting leaves only 0.43 MiB and 0.83 MiB.
//
// Not parallel: it measures process-wide allocation.
func TestScanWeighsItsIndexTableWithItsOutput(t *testing.T) {
	subject := strings.Repeat("b", 5_000) + strings.Repeat("a", 700_000)
	const source = `def run(s)
  s.scan("b|a+")
end`

	rejected := lowestAllocOverRuns(t, 1_800_000, source, subject, false)
	// The same scan under a quota that admits it, which necessarily copies every
	// match. Counting the table in the preflight must not double-charge against
	// the accumulator seed that also holds it, so this doubles as proof that a
	// quota this scan fit before still admits it.
	accepted := lowestAllocOverRuns(t, 1_900_000, source, subject, true)

	if saved := accepted - rejected; saved < 1100<<10 {
		t.Fatalf("a rejected scan saved only %.2f MiB against an accepted one (%.2f vs %.2f), want at least %.2f MiB: the matches were copied before the table and the output were weighed together",
			float64(saved)/(1<<20), float64(rejected)/(1<<20), float64(accepted)/(1<<20), float64(1100<<10)/(1<<20))
	}
}

// TestYieldedCopyIsChargedOnce pins that a block iterator's copy is not counted
// both as a reservation and as the value the block can reach.
//
// each_line and scan's block form held the reservation across runner.call, so
// while the block ran the quota saw the receiver, the actual copy, and a
// reservation for that same copy. A quota that genuinely fits the receiver and
// one yielded value rejected as though two were live: each_line needed
// 3,003,984 bytes for a receiver holding one 1 MB line and scan's block form
// 1,804,021 for one 600 KB match, against 2,003,967 and 1,204,005 now -- one
// copy less apiece.
func TestYieldedCopyIsChargedOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		quota  int
		source string
		arg    string
	}{
		{"each_line", 2_400_000, `def run(s)
  n = 0
  s.each_line { |l| n = n + l.length }
  n
end`, strings.Repeat("a", 1_000_000) + "\nb"},
		{"scan with a block", 1_500_000, `def run(s)
  n = 0
  s.scan("a+") { |m| n = n + m.length }
  n
end`, "b" + strings.Repeat("a", 600_000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: tc.quota}, tc.source)
			if _, err := script.Call(context.Background(), "run", []Value{NewString(tc.arg)}, CallOptions{}); err != nil {
				t.Fatalf("a receiver plus one yielded copy fits %d bytes, so this must not be rejected: %v", tc.quota, err)
			}
		})
	}
}

// TestEachLineCostsWhatLinesCosts pins that walking a string line by line prices
// the same peak as materializing its lines.
//
// Both hold the receiver and one line's copy at their most expensive moment --
// each_line because it yields one at a time, lines because a two-line receiver's
// array holds one short line beside one long one. Their minimum quotas differed
// by 1,000,194 bytes while each_line double-charged its copy, which is that copy
// exactly; they now agree to within a few hundred.
//
// Not parallel: it binary-searches quotas, which is slow enough to keep off the
// parallel set.
func TestEachLineCostsWhatLinesCosts(t *testing.T) {
	arg := strings.Repeat("a", 1_000_000) + "\nb"
	minimumQuota := func(source string) int {
		lo, hi := 1, 32<<20
		for lo < hi {
			mid := (lo + hi) / 2
			script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: mid}, source)
			if _, err := script.Call(context.Background(), "run", []Value{NewString(arg)}, CallOptions{}); err != nil {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}

	walked := minimumQuota(`def run(s)
  n = 0
  s.each_line { |l| n = n + l.length }
  n
end`)
	built := minimumQuota(`def run(s)
  s.lines.length
end`)

	// A generous margin: the gap this catches is a whole line copy, three orders
	// of magnitude above the structural difference between the two calls.
	if gap := walked - built; gap > 64<<10 {
		t.Fatalf("each_line needs %d bytes where lines needs %d, a gap of %d: the yielded copy is being charged twice",
			walked, built, gap)
	}
}

// TestScanResultBackingCostsWhatItCharges pins that a scan's result array costs
// what the preflight charged for it, across the size where the build used to
// switch from allocating to growing.
//
// The result was built at a capped capacity and grown by append, which
// overshoots: 257 matches reached a backing of 575 slots while the preflight
// charged the 257 it would use, leaving 10,176 bytes unpriced for acc.add to
// discover only after the last and largest match had been copied. Allocating the
// approved count instead makes the projection exact and lowers the peak.
//
// The assertion is the marginal cost of a match rather than a fixed quota, so it
// survives retuning of what a match costs: growing the backing made 50 extra
// matches cost 269 bytes each across this boundary against 93 when the same 50
// are added below it.
//
// Not parallel: it binary-searches quotas.
func TestScanResultBackingCostsWhatItCharges(t *testing.T) {
	minimumQuota := func(matches int) int {
		subject := strings.Repeat("ab ", matches)
		lo, hi := 1, 8<<20
		for lo < hi {
			mid := (lo + hi) / 2
			script := compileScriptWithConfig(t, Config{StepQuota: 500_000_000, MemoryQuotaBytes: mid},
				`def run(s)
  s.scan("[a-z]+")
end`)
			if _, err := script.Call(context.Background(), "run", []Value{NewString(subject)}, CallOptions{}); err != nil {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo
	}

	// 250 sits below the capacity the build used to start from and 300 above it,
	// so a strategy that grows charges the second for slots it will not fill.
	below := minimumQuota(250)
	above := minimumQuota(300)
	if perMatch := (above - below) / 50; perMatch > 150 {
		t.Fatalf("50 matches past the 256-match boundary cost %d bytes each (%d to %d), want at most 150: the result backing grows past what the preflight charges",
			perMatch, below, above)
	}
}
