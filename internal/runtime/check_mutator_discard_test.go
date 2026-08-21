package runtime

import (
	"strings"
	"testing"
)

func mutatorDiscardScript(t *testing.T, source string) *Script {
	t.Helper()
	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	script, err := engine.CompileSnippet(source, "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return script
}

func mutatorDiscardWarnings(t *testing.T, source string) []CheckWarning {
	t.Helper()
	var matched []CheckWarning
	for _, warning := range mutatorDiscardScript(t, source).CheckWarnings() {
		if strings.Contains(warning.Message, "updates a temporary") ||
			strings.Contains(warning.Message, "mutating block parameter") {
			matched = append(matched, warning)
		}
	}
	return matched
}

// A statement-position mutator whose receiver provably names no slot -- a
// temporary, or a per-iteration block binding nothing reads again -- updates
// nothing the script can see, so the checker has to say so.
func TestDiscardedMutatorOnTemporaryIsReported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "dup receiver",
			source: "a = [1, 2]\na.dup.push(3)\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "call result receiver",
			source: "def f()\n  [1]\nend\nf().clear\nputs \"done\"",
			want:   "clear updates a temporary",
		},
		{
			name:   "slice receiver",
			source: "xs = [1, 2, 3]\nxs[0..1].push(9)\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "array literal receiver",
			source: "[1, 2].push(3)\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			// mutablePathFor rejects constant roots, so the write lands on
			// a detached copy in static and instance methods alike.
			name:   "class constant in a static method",
			source: "class C\n  ROWS = [1]\n  def self.grow()\n    ROWS.push(2)\n    nil\n  end\nend\nC.grow()\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "class constant in an instance method",
			source: "class D\n  ROWS = [1]\n  def grow()\n    ROWS.push(2)\n    nil\n  end\nend\nD.new.grow()\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "each block parameter",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "each block parameter with unrelated later statement",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  puts \"step\"\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "each_with_index block parameter",
			source: "rows = [[1], [2]]\nrows.each_with_index { |row, i| row.push(i) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "block parameter behind an index hop",
			source: "rows = [[[1]], [[2]]]\nrows.each { |row| row[0].push(9) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "each_value block parameter",
			source: "h = {a: [1]}\nh.each_value { |v| v.push(2) }\nputs \"done\"",
			want:   "mutating block parameter v",
		},
		{
			name:   "hash each value parameter",
			source: "h = {a: [1]}\nh.each { |k, v| v.push(2) }\nputs \"done\"",
			want:   "mutating block parameter v",
		},
		{
			name:   "hash each collapsed pair",
			source: "h = {a: [1]}\nh.each { |pair| pair.push(2) }\nputs \"done\"",
			want:   "mutating block parameter pair",
		},
		{
			name:   "hash each_with_index pair",
			source: "h = {a: [1]}\nh.each_with_index { |pair, i| pair.push(i) }\nputs \"done\"",
			want:   "mutating block parameter pair",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := mutatorDiscardWarnings(t, tc.source)
			found := false
			for _, warning := range warnings {
				if strings.Contains(warning.Message, tc.want) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: no discarded-mutator diagnostic containing %q in %v",
					tc.name, tc.want, warnings)
			}
		})
	}
}

// Expression position stays legitimate temporary mutation, addressable
// receivers keep working updates, non-mutating members are out of scope, and
// user functions that yield keep their own block contracts.
func TestLegitimateMutationsAreNotReported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name:   "argument position",
			source: "def f(v)\n  v\nend\nlist = [1]\nf(list.push(2))\nputs \"done\"",
		},
		{
			name:   "assignment right side",
			source: "list = [1]\ny = list.push(2)\nputs y",
		},
		{
			name:   "explicit return value",
			source: "def g(list)\n  return list.push(1)\nend\ny = g([2])\nputs y",
		},
		{
			name:   "implicit return value",
			source: "def h(list)\n  list.dup.push(1)\nend\ny = h([2])\nputs y",
		},
		{
			name:   "condition position",
			source: "list = [1]\nif list.push(2)\n  puts \"grew\"\nend",
		},
		{
			name:   "addressable local",
			source: "a = [1]\na.push(2)\nputs a",
		},
		{
			name:   "addressable instance variable",
			source: "class C\n  def initialize()\n    @rows = [1]\n  end\n  def wipe()\n    @rows.clear\n  end\nend\nc = C.new\nc.wipe()\nputs \"done\"",
		},
		{
			name:   "hash entry path",
			source: "h = {k: [1]}\nh[:k].push(2)\nputs h",
		},
		{
			name:   "nested container path",
			source: "data = {rows: [[1]]}\ndata[:rows][0].push(2)\nputs data",
		},
		{
			name:   "stored-member hop on a hash-like receiver",
			source: "def get_cfg()\n  {rows: [1]}\nend\ncfg = get_cfg()\ncfg.rows.push(2)\nputs cfg",
		},
		{
			name:   "non-mutating member on a temporary",
			source: "a = [2, 1]\na.dup.sort\nputs \"done\"",
		},
		{
			name:   "block reads the parameter after the mutation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  puts row\nend\nputs \"done\"",
		},
		{
			name:   "map block result carries the mutation",
			source: "rows = [[1], [2]]\nout = rows.map { |row| row.push(0) }\nputs out",
		},
		{
			name:   "yield-driven block",
			source: "def each_pair()\n  yield [1]\n  yield [2]\nend\neach_pair { |row| row.push(0) }\nputs \"done\"",
		},
		{
			name:   "string receiver answers the name non-mutatingly",
			source: "def f()\n  \"abc\"\nend\nf().delete(\"a\")\nputs \"done\"",
		},
		{
			// An outer parameter mutated inside a nested block interleaves
			// with that block's iterations, so the check stays silent.
			name:   "outer block parameter inside a nested block",
			source: "rows = [[1]]\nrows.each do |row|\n  [1].each do |x|\n    row.push(x)\n  end\nend\nputs \"done\"",
		},
		{
			name:   "reduce accumulator carries the block result",
			source: "total = [1, 2].reduce([]) { |acc, x| acc.push(x) }\nputs total",
		},
		{
			// An uppercase FUNCTION-LOCAL binding is addressable -- only
			// constant reads detach -- so the constant arm must not fire.
			name:   "uppercase local binding",
			source: "def f()\n  B = [1]\n  B.push(2)\n  puts B\nend\nf()\nputs \"done\"",
		},
		{
			name:   "class reference receiver",
			source: "class K\nend\ndef f()\n  K.push(1)\n  nil\nend\nf()\nputs \"done\"",
		},
		{
			// A user method named each that consumes the block result is
			// not the builtin iterator, so the block body's mutation is
			// observed and must stay silent.
			name: "user each consumes the block result",
			source: "class C\n  def each()\n    @saved = yield [1]\n    @saved\n  end\nend\n" +
				"C.new.each { |row| row.push(1) }\nputs \"done\"",
		},
		{
			// An unannotated block parameter yielded from a class instance
			// may dispatch a user method named push; unknown and named
			// receivers are not proven collection mutators.
			name: "user push on each-yielded instance",
			source: "class Widget\n  def push(x)\n    @seen = x\n  end\nend\n" +
				"[Widget.new].each { |w| w.push(1) }\nputs \"done\"",
		},
		{
			// A union that includes a named instance may dispatch Widget#push,
			// so the temporary arm must not claim the update reaches nothing.
			name: "union of array and instance",
			source: "class Widget\n  def push(x)\n    @seen = x\n  end\nend\n" +
				"def f(flag)\n  (flag ? [] : Widget.new).push(1)\nend\n" +
				"f(true)\nputs \"done\"",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if warnings := mutatorDiscardWarnings(t, tc.source); len(warnings) > 0 {
				t.Fatalf("%s: unexpected discarded-mutator diagnostic: %v", tc.name, warnings)
			}
		})
	}
}

// The message has to teach the fix, not just point at the line: the
// temporary arm spells the assignment that keeps the result, and the block
// arm names the parameter and the two working idioms.
func TestDiscardedMutatorMessagesTeachTheFix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "temporary arm spells the assignment",
			source: "a = [1, 2]\na.dup.push(3)\nputs \"done\"",
			want:   "push updates a temporary; the update reaches nothing. Assign the result, as in `x = a.dup.push(...)`",
		},
		{
			name:   "bare member call keeps its own spelling",
			source: "def f()\n  [1]\nend\nf().clear\nputs \"done\"",
			want:   "clear updates a temporary; the update reaches nothing. Assign the result, as in `x = f().clear`",
		},
		{
			name:   "slice receiver keeps the range spelling",
			source: "xs = [1, 2, 3]\nxs[0..1].push(9)\nputs \"done\"",
			want:   "push updates a temporary; the update reaches nothing. Assign the result, as in `x = xs[0..1].push(...)`",
		},
		{
			// The advice must keep the call's block, or following it would
			// run a different computation.
			name:   "block-taking call keeps its block in the example",
			source: "xs = [1, 2]\nxs.dup.fill do\n  0\nend\nputs \"done\"",
			want:   "fill updates a temporary; the update reaches nothing. Assign the result, as in `x = xs.dup.fill { ... }`",
		},
		{
			name:   "constant receiver spells the assignment",
			source: "class C\n  ROWS = [1]\n  def self.grow()\n    ROWS.push(2)\n    nil\n  end\nend\nC.grow()\nputs \"done\"",
			want:   "push updates a temporary; the update reaches nothing. Assign the result, as in `x = ROWS.push(...)`",
		},
		{
			name:   "block arm names the parameter and the idioms",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0) }\nputs \"done\"",
			want:   "mutating block parameter row does not update the collection it was yielded from; build the result with map, or index the original",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := mutatorDiscardWarnings(t, tc.source)
			for _, warning := range warnings {
				if warning.Message == tc.want {
					return
				}
			}
			t.Fatalf("%s: no diagnostic with message %q in %v", tc.name, tc.want, warnings)
		})
	}
}
