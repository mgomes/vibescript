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
			name:   "auto-invoked function receiver",
			source: "def rows()\n  [1]\nend\nrows.push(2)\nputs \"done\"",
			want:   "push updates a temporary",
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
			name:   "empty splat still completes a zero-arg mutator",
			source: "[1].clear(*[])\nputs \"done\"",
			want:   "clear updates a temporary",
		},
		{
			name:   "discarded ternary mutators",
			source: "def f(flag)\n  flag ? [1].push(2) : [3].clear\n  nil\nend\nf(true)\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "statically selected ternary arm",
			source: "true ? [1].push(2) : [3].clear\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "short-circuit and result",
			source: "true && [1].push(2)\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "reachable case else mutator",
			source: "case 2\nwhen 1\n  0\nelse\n  [1].push(2)\nend\nputs \"done\"",
			want:   "push updates a temporary",
		},
		{
			name:   "union of array-of-array arms",
			source: "def f(rows: array<array<int>> | array<array<string>>)\n  rows.each { |row| row.clear }\n  nil\nend\nf([[1]])\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "union of hash value array arms",
			source: "def f(h: hash<string, array<int>> | hash<string, array<string>>)\n  h.each_value { |v| v.clear }\n  nil\nend\nf({a: [1]})\nputs \"done\"",
			want:   "mutating block parameter v",
		},
		{
			name:   "indexed union of nested arrays",
			source: "def f(rows: array<array<array<int>>> | array<array<array<string>>>)\n  rows.each { |row| row[0].clear }\n  nil\nend\nf([[[1]]])\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "delete_if block read is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.delete_if { |x| puts row; true } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "push block is ignored so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(1) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later ignored push block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].push(2) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a statically false if is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  if false\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a statically false ternary is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  false ? puts(row) : nil\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a statically skipped else is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  if true\n    nil\n  else\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read behind a short-circuit or is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); true || puts(row) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read behind a short-circuit and is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); false && puts(row) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "each_cons that yields still warns",
			source: "[1, 2].each_cons(2) { |row| row.push(1) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a statically false while is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  while false\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a statically true until is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  until true\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a proven non-raising rescue is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); 1 rescue puts(row) }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a statically skipped case else is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  case 1\n  when 1\n    nil\n  else\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a zero-yield iterator block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [].each { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in a zero-yield map block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [].map { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "same-call delete miss block does not observe the write",
			source: "rows = [[1], [2]]\nrows.each { |row| row.delete(1) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after a matched case candidate is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  case 1\n  when 1, puts(row)\n    nil\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after an aborting case candidate is not an observation",
			source: "def f(x)\n  rows = [[1], [2]]\n  rows.each do |row|\n    row.push(0)\n    case x\n    when 1, [1].clear(2)\n      nil\n    when 2\n      puts row\n    end\n  end\n  nil\nend\nf(1)\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after return is not an observation",
			source: "def f\n  rows = [[1], [2]]\n  rows.each do |row|\n    row.push(0)\n    return nil\n    puts row\n  end\nend\nf()\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after overwriting the parameter is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  row = []\n  puts row\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after a definite if overwrite is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  if true\n    row = []\n  end\n  puts row\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read in an unreachable try else is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n    raise \"x\"\n  rescue\n    nil\n  else\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later delete hit block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].delete(1) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later aborting mutator block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].delete_if(1) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later singleton sort block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].sort { |a, b| puts row; 0 } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later try-body overwrite is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); begin; row = []; ensure; nil; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later for-loop overwrite is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); for row in [1]; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after both if branches return is not an observation",
			source: "def f(flag)\n  rows = [[1], [2]]\n  rows.each do |row|\n    row.push(0)\n    if flag\n      return nil\n    else\n      return nil\n    end\n    puts row\n  end\nend\nf(true)\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later non-raising try rescue is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); begin; nil; rescue; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later raising try else is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); begin; raise \"x\"; rescue; nil; else; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later invalid iterator block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.each(1) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later invalid map iterator block is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.map(1) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "unseeded reduce on a singleton is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].reduce { puts row; 0 } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later fetch hit is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fetch(0) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later endless while is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); while true; nil; end; puts row }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "ensure after a body overwrite is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| begin; row.push(0); row = []; ensure; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "unreachable break does not make an endless while an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); while true; if false; break; end; end; puts row }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later aborting array element is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [[1].clear(2), row] }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "ensure after overwrite then raise is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); begin; row = []; raise \"x\"; rescue; nil; ensure; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "fetch_values without keys is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.fetch_values { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "fetch_values of a present key is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.fetch_values(:a) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later aborting binary left is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].clear(2) + row }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "hash fetch hit is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.fetch(:a) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "disjoint merge is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.merge({b: 2}) { puts row; 2 } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "empty fill span is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fill(0, 0) { puts row; 9 } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "expanding empty fill window writes a temporary",
			source: "[1].fill(9, 2, 0)\nputs \"done\"",
			want:   "fill updates a temporary",
		},
		{
			name:   "join ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].join { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "empty range fill is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fill(0...0) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "transpose ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [[1]].transpose { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "length ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].length { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "union ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].union { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "descending fill range is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1, 2].fill(1...0) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "raise before break does not make an endless while an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); while true; raise \"x\"; break; end; puts row }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "zip ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].zip { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "ensure after rescue overwrite is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); begin; raise \"x\"; rescue; row = []; ensure; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later aborting loop condition is not an observation",
			source: "first = true\nrows = [[1], [2]]\nrows.each { |row| row.push(0); while first || [[1].clear(2), row]; first = false; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "rescue binding is not an observation of the mutated parameter",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); begin; raise \"x\"; rescue => row; puts row; end }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later grep without a pattern is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].grep { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later aborting callee is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].clear(2).each { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later try overwrite is not observed in ensure",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  begin\n    row = []\n  ensure\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later mismatched try rescue is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  begin\n    raise TypeError, \"x\"\n  rescue ArgumentError\n    puts row\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later try that returns is not an observation",
			source: "def f\n  rows = [[1], [2]]\n  rows.each do |row|\n    row.push(0)\n    begin\n      return nil\n    ensure\n      nil\n    end\n    puts row\n  end\nend\nf()\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after a nested return is not an observation",
			source: "def f(flag)\n  rows = [[1], [2]]\n  rows.each do |row|\n    if flag\n      row.push(0)\n      return nil\n    end\n    puts row\n  end\nend\nf(true)\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later read after an aborting expression is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  [1].clear(2)\n  puts row\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "typed rescue that cannot match a later raise is not an observation",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n    raise TypeError, \"x\"\n  rescue ArgumentError\n    puts row\n  rescue TypeError\n    nil\n  end\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "union of shape collection fields",
			source: "def f(rows: array<{items: array<int>}> | array<{items: array<string>}>)\n  rows.each { |row| row.items.clear }\n  nil\nend\nf([{items: [1]}])\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "nested block parameter shadows later read",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  [1].each { |row| puts row }\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "fill block read is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.fill { |i| puts row; i } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "hash each rest then value",
			source: "h = {a: [1]}\nh.each { |(*, value)| value.push(2) }\nputs \"done\"",
			want:   "mutating block parameter value",
		},
		{
			name:   "block-param mutator nested in if",
			source: "def f(flag)\n  rows = [[1], [2]]\n  rows.each do |row|\n    if flag\n      row.push(0)\n    end\n  end\nend\nf(true)\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later overwrite is not a read",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  row = []\nend\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "later destructure overwrite is not a read",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  row, ignored = [[], nil]\nend\nputs \"done\"",
			want:   "mutating block parameter row",
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
		{
			name:   "destructured each parameter",
			source: "rows = [[[1], [2]], [[3], [4]]]\nrows.each { |(head, tail)| tail.push(1) }\nputs \"done\"",
			want:   "mutating block parameter tail",
		},
		{
			name:   "hash each collapsed pair destructure",
			source: "h = {a: [1]}\nh.each { |(k, v)| v.push(2) }\nputs \"done\"",
			want:   "mutating block parameter v",
		},
		{
			name:   "hash each_with_index pair destructure",
			source: "h = {a: [1]}\nh.each_with_index { |(k, v), i| v.push(2) }\nputs \"done\"",
			want:   "mutating block parameter v",
		},
		{
			name:   "nested hash pair value destructure",
			source: "h = {a: [[1], [2]]}\nh.each { |(k, (head, tail))| tail.push(3) }\nputs \"done\"",
			want:   "mutating block parameter tail",
		},
		{
			name: "builtin each after user each with conditional leaf",
			source: "class Widget\n  def each()\n    @saved = yield [1]\n    @saved\n  end\nend\n" +
				"def apply(x, flag)\n  x.each { |row| if flag; row.dup.push(0); else; row.dup.clear; end }\nend\n" +
				"apply(Widget.new, true)\napply([[1]], true)\nputs \"done\"",
			want: "push updates a temporary",
		},
		{
			name: "builtin each after a user each of the same body",
			source: "class Widget\n  def each()\n    @saved = yield [1]\n    @saved\n  end\nend\n" +
				"def apply(x)\n  x.each { |row| row.push(0) }\nend\n" +
				"apply(Widget.new)\napply([[1]])\nputs \"done\"",
			want: "mutating block parameter row",
		},
		{
			name:   "hash keys ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.keys { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "hash values ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.values { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "hash slice ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.slice { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "hash compact ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.compact { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "hash except ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.except { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "flatten ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].flatten { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "values_at ignores its block so it is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.values_at { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "raising if in try body is not an observation of the suffix",
			source: "rows = [[1], [2]]\nrows.each { |row| begin; if true; row.push(0); raise \"x\"; end; rescue; raise \"y\"; end; puts row }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "fill start past the end does not yield its block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fill(2) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "exclusive range past the end still writes a temporary",
			source: "[1].fill(9, 2...2)\nputs \"done\"",
			want:   "fill updates a temporary",
		},
		{
			name:   "normalized empty fill range is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fill(-1...0) { puts row } }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "unreachable break after raising if is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); while true; if true; raise \"x\"; end; break; end; puts row }\nputs \"done\"",
			want:   "mutating block parameter row",
		},
		{
			name:   "raising rescue after the mutation is not an observation",
			source: "rows = [[1], [2]]\nrows.each { |row| begin; raise \"x\"; rescue; row.push(0); raise \"y\"; end; puts row }\nputs \"done\"",
			want:   "mutating block parameter row",
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
			name:   "shape stored push is not a collection mutator",
			source: "{push: 1}.push(1)\nputs \"done\"",
		},
		{
			name:   "shape stored clear is not a hash mutator",
			source: "def f(s: {clear: int})\n  s.clear\n  nil\nend\nf({clear: 1})\nputs \"done\"",
		},
		{
			name:   "shape stored each is not a hash iterator",
			source: "def f(s: {each: int})\n  s.each { |row| row.push(0) }\n  nil\nend\nf({each: 1})\nputs \"done\"",
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
			name:   "unreachable short-circuit mutator",
			source: "true || [1].push(2)\nputs \"done\"",
		},
		{
			name:   "unreachable ternary alternate",
			source: "true ? 1 : [3].clear\nputs \"done\"",
		},
		{
			name:   "non-completing ternary arm is not reported",
			source: "def f(flag)\n  flag ? [1].clear(2) : nil\n  nil\nend\nf(true)\nputs \"done\"",
		},
		{
			name:   "non-completing pop arity is not reported",
			source: "def f(flag)\n  flag ? [1].pop(1, 2) : nil\n  nil\nend\nf(true)\nputs \"done\"",
		},
		{
			name:   "case when match that cannot complete",
			source: "def f(x)\n  case x\n  when 1\n    0\n  when [1].clear(2)\n    [2].push(3)\n  end\n  nil\nend\nf(1)\nputs \"done\"",
		},
		{
			name:   "unreachable rescue fallback",
			source: "1 rescue [1].push(2)\nputs \"done\"",
		},
		{
			name:   "unreachable case else",
			source: "case 1\nwhen 1\n  0\nelse\n  [1].push(2)\nend\nputs \"done\"",
		},
		{
			name:   "ensure observes the rebound parameter",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n  ensure\n    puts row\n  end\nend\nputs \"done\"",
		},
		{
			name:   "else observes the rebound parameter",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n  rescue\n    nil\n  else\n    puts row\n  end\nend\nputs \"done\"",
		},
		{
			name:   "while condition observes the rebound parameter",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  while row.length < 3\n    row.push(0)\n  end\nend\nputs \"done\"",
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
		{
			name:   "elsif condition that cannot complete",
			source: "def f(flag)\n  if flag\n    nil\n  elsif [1].clear(2)\n    [2].push(3)\n  end\n  nil\nend\nf(true)\nputs \"done\"",
		},
		{
			name:   "cycle zero never runs the block",
			source: "[[]].cycle(0) { |row| row.push(1) }\nputs \"done\"",
		},
		{
			name:   "cycle splatted zero never runs the block",
			source: "[[]].cycle(*[0]) { |row| row.push(1) }\nputs \"done\"",
		},
		{
			name:   "cycle negative never runs the block",
			source: "[[]].cycle(-1) { |row| row.push(1) }\nputs \"done\"",
		},
		{
			name:   "each_cons window larger than receiver never runs the block",
			source: "[[]].each_cons(2) { |row| row.push(1) }\nputs \"done\"",
		},
		{
			name:   "each_slice on an empty receiver never runs the block",
			source: "[].each_slice(2) { |row| row.push(1) }\nputs \"done\"",
		},
		{
			name:   "each on an empty receiver never runs the block",
			source: "[].each { |row| row.push(1) }\nputs \"done\"",
		},
		{
			name:   "delete miss block observes the rebound parameter",
			source: "rows = [[1], [2]]\nother = [1]\nrows.each { |row| row.push(0); other.delete(2) { puts row } }\nputs \"done\"",
		},
		{
			name:   "pop with a negative count cannot update",
			source: "[1].pop(-1)\nputs \"done\"",
		},
		{
			name:   "fill with too many selectors cannot update",
			source: "[1].fill(0, 0, 1, 2)\nputs \"done\"",
		},
		{
			name:   "insert with a non-integer index cannot update",
			source: "[1].insert(\"x\", 2)\nputs \"done\"",
		},
		{
			name:   "insert with an out-of-range negative index cannot update",
			source: "[1].insert(-3, 2)\nputs \"done\"",
		},
		{
			name:   "shift with a non-integer count cannot update",
			source: "[1].shift(\"x\")\nputs \"done\"",
		},
		{
			name:   "push with a keyword argument cannot update",
			source: "[1].push(value: 2)\nputs \"done\"",
		},
		{
			name:   "delete_if without a block cannot update",
			source: "[1].delete_if()\nputs \"done\"",
		},
		{
			name:   "keep_if without a block cannot update",
			source: "[1].keep_if()\nputs \"done\"",
		},
		{
			name:   "hash replace of a non-hash cannot update",
			source: "{ a: 1 }.replace([1])\nputs \"done\"",
		},
		{
			name: "user push that yields observes the rebound parameter",
			source: "class Widget\n  def push\n    yield\n  end\nend\n" +
				"widget = Widget.new\nrows = [[1], [2]]\n" +
				"rows.each { |row| row.push(0); widget.push { puts row } }\nputs \"done\"",
		},
		{
			name:   "later rescue after a raising body observes the rebound parameter",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n    raise \"x\"\n  rescue\n    puts row\n  end\nend\nputs \"done\"",
		},
		{
			name:   "compound assignment still observes the mutated binding",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); row ||= []; puts row }\nputs \"done\"",
		},
		{
			name:   "empty for loop still observes the mutated binding",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); for row in []; nil; end; puts row }\nputs \"done\"",
		},
		{
			name:   "empty for iterable variable still observes the mutated binding",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); values = []; for row in values; nil; end; puts row }\nputs \"done\"",
		},
		{
			name:   "empty splat iterator still observes in the block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.each(*[]) { puts row } }\nputs \"done\"",
		},
		{
			name:   "array each extra args still observe the rebound parameter",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].each(1) { puts row } }\nputs \"done\"",
		},
		{
			name:   "for loop over unknown iterable still observes in the body",
			source: "values = [1]\nrows = [[1], [2]]\nrows.each { |row| row.push(0); for x in values; puts row; end }\nputs \"done\"",
		},
		{
			name:   "seeded reduce still observes in the block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].reduce(0) { puts row; 0 } }\nputs \"done\"",
		},
		{
			name:   "seeded sum still observes in the block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].sum(0) { puts row; 0 } }\nputs \"done\"",
		},
		{
			name:   "find nil fallback still observes in the block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].find(nil) { puts row; false } }\nputs \"done\"",
		},
		{
			name:   "zero-count pop does not write a temporary",
			source: "[1].pop(0)\nputs \"done\"",
		},
		{
			name:   "zero-count shift does not write a temporary",
			source: "[1].shift(0)\nputs \"done\"",
		},
		{
			name:   "array map extra args still observe the rebound parameter",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].map(1) { puts row } }\nputs \"done\"",
		},
		{
			name:   "unseeded reduce over two elements still observes in the block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1, 2].reduce { puts row; 0 } }\nputs \"done\"",
		},
		{
			name:   "fetch miss fallback still observes in the block",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fetch(2) { puts row } }\nputs \"done\"",
		},
		{
			name:   "insert without values does not write a temporary",
			source: "[1].insert(0)\nputs \"done\"",
		},
		{
			name:   "empty pop does not write a temporary",
			source: "[].pop\nputs \"done\"",
		},
		{
			name:   "empty pop with count does not write a temporary",
			source: "[].pop(1)\nputs \"done\"",
		},
		{
			name:   "empty shift does not write a temporary",
			source: "[].shift\nputs \"done\"",
		},
		{
			name:   "hash merge conflict block still observes",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {a: 1}.merge({a: 2}) { puts row; 2 } }\nputs \"done\"",
		},
		{
			name:   "delete miss does not write a temporary",
			source: "[1].delete(2)\nputs \"done\"",
		},
		{
			name:   "delete of a range uses equality not case membership",
			source: "[1].delete(1..2)\nputs \"done\"",
		},
		{
			name:   "delete miss block of a range still observes",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].delete(1..2) { puts row } }\nputs \"done\"",
		},
		{
			name:   "empty delete miss block still observes",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [].delete(1) { puts row } }\nputs \"done\"",
		},
		{
			name:   "empty hash delete miss block still observes",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {}.delete(:a) { puts row } }\nputs \"done\"",
		},
		{
			name:   "nil fill length past the end does not write a temporary",
			source: "[1].fill(9, 2, nil)\nputs \"done\"",
		},
		{
			name:   "next still observes the loop condition",
			source: "rows = [[1], [2]]\nrows.each { |row| while row.length < 3; row.push(0); next; end }\nputs \"done\"",
		},
		{
			name:   "break still observes statements after the loop",
			source: "rows = [[1], [2]]\nrows.each { |row| while true; row.push(0); break; end; puts row }\nputs \"done\"",
		},
		{
			name:   "for next still observes the iterable",
			source: "rows = [[1], [2]]\nrows.each { |row| for i in row; row.push(0); next; end }\nputs \"done\"",
		},
		{
			name:   "raising prefix still observes in rescue",
			source: "def f(flag)\n  rows = [[1], [2]]\n  rows.each do |row|\n    begin\n      row.push(0)\n      if flag\n        raise \"x\"\n      end\n      row = []\n    rescue\n      puts row\n    end\n  end\nend\nf(true)\nputs \"done\"",
		},
		{
			name:   "fill with a start selector still observes in the block",
			source: "xs = [1]\nrows = [[1], [2]]\nrows.each { |row| row.push(0); xs.fill(0) { puts row; 9 } }\nputs \"done\"",
		},
		{
			name:   "sequential merge conflict still observes",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); {}.merge({a: 1}, {a: 2}) { puts row; 2 } }\nputs \"done\"",
		},
		{
			name:   "expanding fill on empty still observes",
			source: "xs = []\nrows = [[1], [2]]\nrows.each { |row| row.push(0); xs.fill(0, 2) { puts row; 9 } }\nputs xs",
		},
		{
			name:   "empty fill window does not write a temporary",
			source: "[1].fill(0, 0) { 9 }\nputs \"done\"",
		},
		{
			name:   "empty delete_if does not write a temporary",
			source: "[].delete_if { true }\nputs \"done\"",
		},
		{
			name:   "empty keep_if does not write a temporary",
			source: "[].keep_if { false }\nputs \"done\"",
		},
		{
			name:   "handled raise still observes after the try",
			source: "rows = [[1], [2]]\nrows.each { |row| begin; row.push(0); raise \"x\"; rescue; nil; end; puts row }\nputs \"done\"",
		},
		{
			name:   "raise before try overwrite still observes later",
			source: "def risky\n  raise \"x\"\nend\nrows = [[1], [2]]\nrows.each do |row|\n  row.push(0)\n  begin\n    risky()\n    row = []\n  rescue\n    nil\n  end\n  puts row\nend\nputs \"done\"",
		},
		{
			name:   "later rescue after a nested raising path observes the rebound parameter",
			source: "def f(flag)\n  rows = [[1], [2]]\n  rows.each do |row|\n    begin\n      if flag\n        row.push(0)\n        raise \"x\"\n      end\n    rescue\n      puts row\n    end\n  end\n  nil\nend\nf(true)\nputs \"done\"",
		},
		{
			name:   "unknown raise still observes a later matching rescue",
			source: "def risky\n  raise TypeError, \"x\"\nend\nrows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n    risky\n  rescue ArgumentError\n    nil\n  rescue TypeError\n    puts row\n  end\nend\nputs \"done\"",
		},
		{
			name:   "standard error rescue observes a type error raise",
			source: "rows = [[1], [2]]\nrows.each do |row|\n  begin\n    row.push(0)\n    raise TypeError, \"x\"\n  rescue StandardError\n    puts row\n  end\nend\nputs \"done\"",
		},
		{
			name:   "hash store with an unsupported key cannot update",
			source: "{a: 1}.store([1], 2)\nputs \"done\"",
		},
		{
			name:   "hash delete with an unsupported key cannot update",
			source: "{a: 1}.delete([1])\nputs \"done\"",
		},
		{
			name:   "fill without a value or block cannot update",
			source: "[1].fill()\nputs \"done\"",
		},
		{
			name:   "case later clause after aborting match",
			source: "def f(x)\n  case x\n  when 1, [1].clear(2)\n    nil\n  when 2\n    [2].push(3)\n  end\n  nil\nend\nf(1)\nputs \"done\"",
		},
		{
			name: "conditional uppercase local is addressable",
			source: "def f(flag)\n  if flag\n    ROWS = []\n  end\n  ROWS.push(2)\nend\n" +
				"f(true)\nputs \"done\"",
		},
		{
			name: "conditional local shadows auto-invoked function",
			source: "def rows()\n  [1]\nend\n" +
				"def f(flag)\n  if flag\n    rows = []\n  end\n  rows.push(2)\nend\n" +
				"f(true)\nputs \"done\"",
		},
		{
			name: "any-typed temporary may be a user push",
			source: "class Widget\n  def push(x)\n    @seen = x\n  end\nend\n" +
				"def pass(v: any) -> any\n  v\nend\n" +
				"pass(Widget.new).push(1)\nputs \"done\"",
		},
		{
			name:   "negative fill length still completes later reads",
			source: "rows = [[1], [2]]\nrows.each { |row| row.push(0); [1].fill(9, 2, -1); puts row }\nputs \"done\"",
		},
		{
			name:   "empty array clear does not write a temporary",
			source: "[].clear\nputs \"done\"",
		},
		{
			name:   "empty hash clear does not write a temporary",
			source: "{}.clear\nputs \"done\"",
		},
		{
			name:   "bare fill start past the end does not write a temporary",
			source: "[1].fill(9, 2)\nputs \"done\"",
		},
		{
			name:   "delete_if false retains every element",
			source: "[1].delete_if { false }\nputs \"done\"",
		},
		{
			name:   "keep_if true retains every element",
			source: "{a: 1}.keep_if { true }\nputs \"done\"",
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
			name:   "element-returning mutator binds the receiver",
			source: "[1, 2].pop\nputs \"done\"",
			want:   "pop updates a temporary; the update reaches nothing. Bind the receiver first, as in `xs = [...]; xs.pop`",
		},
		{
			name:   "hash store binds the receiver",
			source: "{a: 1}.store(:b, 2)\nputs \"done\"",
			want:   "store updates a temporary; the update reaches nothing. Bind the receiver first, as in `xs = {...}; xs.store(...)`",
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

// A CheckWarningsWithOptions / CheckWarningsForCall host global with an
// uppercase name remains an addressable Env binding; ROWS.push rebinds it
// so a later read observes the update, unlike a class-self constant.
func TestHostUppercaseArrayGlobalIsNotADiscardedMutator(t *testing.T) {
	t.Parallel()

	script := mutatorDiscardScript(t, "def run()\n  ROWS.push(1)\n  ROWS\nend\n")
	opts := CallOptions{Globals: map[string]Value{"ROWS": NewArray([]Value{NewInt(1)})}}
	for _, warning := range script.CheckWarningsWithOptions(opts) {
		if strings.Contains(warning.Message, "updates a temporary") ||
			strings.Contains(warning.Message, "mutating block parameter") {
			t.Fatalf("ROWS.push with host global: unexpected discarded-mutator diagnostic: %v", warning)
		}
	}
}

func TestZeroArgHostBuiltinReceiverIsTemporary(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{StepQuota: Unlimited, MemoryQuotaBytes: Unlimited, RecursionLimit: 10_000})
	engine.RegisterZeroArgBuiltin("rows", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewArray([]Value{NewInt(1)}), nil
	})
	script, err := engine.CompileSnippet("rows.push(2)\nputs \"done\"", "__main__")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	found := false
	for _, warning := range script.CheckWarnings() {
		if strings.Contains(warning.Message, "updates a temporary") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rows.push host builtin: no temporary warning in %v", script.CheckWarnings())
	}
}
