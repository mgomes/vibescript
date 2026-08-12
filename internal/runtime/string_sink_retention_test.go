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
