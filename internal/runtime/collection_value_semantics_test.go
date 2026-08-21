package runtime

import (
	"context"
	"strings"
	"testing"
)

// Arrays and hashes are values (ADR-006 item 2). Binding or passing one
// produces another logical value, updating one binding or path cannot change a
// sibling, mutating operations rebind an addressable root, and the bang-named
// variants that duplicated a non-bang transformation are gone.

// collectionSemanticsPrelude carries the class and function declarations the
// table cases reach for, since a declaration cannot nest inside def run.
const collectionSemanticsPrelude = `class Cart
  def initialize()
    @items = [1]
  end
  def items()
    @items
  end
end

class Basket
  def initialize()
    @items = []
  end
  def add(x)
    @items.push(x)
    self
  end
  def size()
    @items.size
  end
end

def grow(list)
  list.push(2)
  list.size
end

def build()
  [1]
end

`

// TestCollectionValueSemanticsADRExample runs the example ADR-006 states as the
// resulting language shape, so the decision's own text is a test.
func TestCollectionValueSemanticsADRExample(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  first = [1, 2]
  second = first
  first[0] = 9
  first.inspect + " " + second.inspect
end
`)

	if got := callFunc(t, script, "run", nil).String(); got != "[9, 2] [1, 2]" {
		t.Fatalf("ADR example = %s, want [9, 2] [1, 2]", got)
	}
}

// TestCollectionMutationDoesNotCrossABinding covers the ways a second name can
// reach a collection: another local, a call argument, a return value, a hash
// entry, an element slot, and an instance variable read back through its
// accessor. A write through one of them is invisible to the other in every case.
func TestCollectionMutationDoesNotCrossABinding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "second local",
			body: `a = [1]
  b = a
  a.push(2)
  b.inspect`,
			want: "[1]",
		},
		{
			name: "call argument",
			body: `a = [1]
  grow(a)
  a.inspect`,
			want: "[1]",
		},
		{
			name: "returned value",
			body: `a = build
  b = a
  a.push(2)
  b.inspect`,
			want: "[1]",
		},
		{
			name: "hash entry",
			body: `a = [1]
  h = { list: a }
  a.push(2)
  h["list"].inspect`,
			want: "[1]",
		},
		{
			name: "sibling element slot",
			body: `inner = [1]
  outer = [inner, inner]
  outer[0].push(2)
  outer[1].inspect`,
			want: "[1]",
		},
		{
			name: "block parameter",
			body: `rows = [[1]]
  rows.each { |row| row.push(2) }
  rows.inspect`,
			want: "[[1]]",
		},
		{
			name: "instance accessor",
			body: `c = Cart.new
  c.items.push(2)
  c.items.inspect`,
			want: "[1]",
		},
		{
			name: "hash through a second local",
			body: `a = { x: 1 }
  b = a
  a["y"] = 2
  b.inspect`,
			want: "{x: 1}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, collectionSemanticsPrelude+"def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestCollectionMutationRebindsItsAddressableRoot is the other half of the
// contract: a write must still reach the local, instance variable, or nested
// path the source names, or value semantics would just be a way to lose updates.
func TestCollectionMutationRebindsItsAddressableRoot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "local",
			body: `a = [1]
  a.push(2)
  a.inspect`,
			want: "[1, 2]",
		},
		{
			name: "shovel through a local",
			body: `a = []
  3.times { |i| a << i }
  a.inspect`,
			want: "[0, 1, 2]",
		},
		{
			name: "element of a local",
			body: `outer = [[1]]
  outer[0].push(2)
  outer.inspect`,
			want: "[[1, 2]]",
		},
		{
			name: "nested index write",
			body: `data = { rows: [[1, 2]] }
  data["rows"][0][1] = 99
  data.inspect`,
			want: `{rows: [[1, 99]]}`,
		},
		{
			name: "hash entry of a local",
			body: `h = { list: [1] }
  h["list"].push(2)
  h.inspect`,
			want: `{list: [1, 2]}`,
		},
		{
			name: "instance variable",
			body: `Basket.new.add(1).add(2).size.to_s`,
			want: "2",
		},
		{
			name: "reassigned mutator result",
			body: `a = []
  10.times { |i| a = a.push(i) }
  a.size.to_s`,
			want: "10",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, collectionSemanticsPrelude+"def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestCollectionsCannotContainThemselves pins the acyclicity that falls out of
// value semantics: storing a container into itself stores the value it had, not
// a window onto the value it is about to have. Nothing in the runtime relies on
// arrays and hashes being able to cycle any more.
func TestCollectionsCannotContainThemselves(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"push self", "a = [1]\n  a.push(a)\n  a.inspect", "[1, [1]]"},
		{"shovel self", "a = [1]\n  a << a\n  a.inspect", "[1, [1]]"},
		{"index assign self", "a = [1]\n  a[0] = a\n  a.inspect", "[[1]]"},
		{"hash entry self", `h = { a: 1 }
  h["self"] = h
  h.inspect`, `{a: 1, self: {a: 1}}`},
		{"nested path self", "a = [[1]]\n  a[0].push(a)\n  a.inspect", "[[1, [[1]]]]"},
		{"hash store self", "h = { a: 1 }\n  h.store(\"self\", h)\n  h.inspect", "{a: 1, self: {a: 1}}"},
		{"hash replace ancestor", "a = { x: {} }\n  a.x.replace(a)\n  a.inspect", "{x: {x: {}}}"},
		{"hash replace child", "a = { x: { y: 1 } }\n  a.replace(a.x)\n  a.inspect", "{y: 1}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestCollectionEqualityIsContentEquality pins that == stays content equality
// and that equal? now answers the same question. Collections have no identity
// in the language, so there is nothing else equal? could report.
func TestCollectionEqualityIsContentEquality(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  parts = []
  parts.push(([1, 2] == [1, 2]).to_s)
  parts.push([1, 2].equal?([1, 2]).to_s)
  parts.push(({ a: 1 } == { a: 1 }).to_s)
  parts.push({ a: 1 }.equal?({ a: 1 }).to_s)
  parts.push([1, 2].equal?([1, 3]).to_s)
  a = [1]
  b = a
  a.push(2)
  parts.push(a.equal?(b).to_s)
  parts.join("|")
end
`)

	got := callFunc(t, script, "run", nil).String()
	want := "true|true|true|true|false|false"
	if got != want {
		t.Fatalf("collection equality = %s, want %s", got, want)
	}
}

func TestEachDoesNotShareAnIgnoredBlockResult(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [1]
  [0].each { a }
  a
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatalf("each { a } left a shared, SoleRef() = false, want true")
	}
}

func TestMapDoesNotDoublePublishAFreshBlockResult(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  [1].map { [] }[0]
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatalf("[1].map { [] }[0] SoleRef() = false, want true")
	}
}

func TestCapabilityClonePublishesRepeatedChildren(t *testing.T) {
	t.Parallel()

	child := NewArray([]Value{NewInt(1)})
	cloned, err := cloneCapabilityDataOnlyValue("probe.result", NewArray([]Value{child, child}))
	if err != nil {
		t.Fatalf("cloneCapabilityDataOnlyValue(probe.result, [child, child]) error = %v", err)
	}
	items := cloned.Array()
	if len(items) != 2 {
		t.Fatalf("cloneCapabilityDataOnlyValue(...) len = %d, want 2", len(items))
	}
	if arrayIdentity(items[0]) != arrayIdentity(items[1]) {
		t.Fatalf("cloneCapabilityDataOnlyValue([child, child]) = distinct children, want one shared clone")
	}
	if items[0].SoleRef() {
		t.Fatal("cloneCapabilityDataOnlyValue([child, child]) left the child sole; mutating one slot would change the other")
	}
}

func TestCapabilityClonePublishesRepeatedObjectAttributes(t *testing.T) {
	t.Parallel()

	child := NewArray([]Value{NewInt(1)})
	cloned, err := cloneCapabilityDataOnlyValue("probe.result", NewObject(map[string]Value{"a": child, "b": child}))
	if err != nil {
		t.Fatalf("cloneCapabilityDataOnlyValue(probe.result, {a: child, b: child}) error = %v", err)
	}
	entries := cloned.Hash()
	if arrayIdentity(entries["a"]) != arrayIdentity(entries["b"]) {
		t.Fatalf("cloneCapabilityDataOnlyValue({a: child, b: child}) = distinct children, want one shared clone")
	}
	if entries["a"].SoleRef() {
		t.Fatal("cloneCapabilityDataOnlyValue({a: child, b: child}) left the child sole; mutating one attribute would change the other")
	}
}

// TestRemovedCollectionBangMembersStayRemoved fails if a bang variant that
// duplicates a non-bang transformation is ever registered again, and names the
// replacement in the message so a reintroduction is answered where it happens.
func TestRemovedCollectionBangMembersStayRemoved(t *testing.T) {
	t.Parallel()

	removed := []struct{ receiver, call, replacement string }{
		{"[3, 1, 2]", "sort!", "a = a.sort"},
		{"[1, 2]", "map! { |v| v }", "a = a.map { ... }"},
		{"[1, 2]", "reverse!", "a = a.reverse"},
		{"[1, 1]", "uniq!", "a = a.uniq"},
		{"[1, nil]", "compact!", "a = a.compact"},
		{"[1, 2]", "select! { |v| v > 1 }", "a.keep_if { ... }"},
		{"[1, 2]", "reject! { |v| v > 1 }", "a.delete_if { ... }"},
		{"{ a: 1 }", "merge!({ b: 2 })", "h = h.merge(other)"},
		{"{ a: 1 }", "update({ b: 2 })", "h = h.merge(other)"},
	}

	for _, tc := range removed {
		t.Run(strings.SplitN(tc.call, " ", 2)[0], func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  a = "+tc.receiver+"\n  a."+tc.call+"\nend\n")
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("%s is callable again; ADR-006 item 2 removed it in favor of %s", tc.call, tc.replacement)
			}
			if !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("%s failed with %v, want an unknown-member error", tc.call, err)
			}
		})
	}
}

// TestDestructuringRightSideHoldsASnapshot replaces the evaluation-order cases
// that pinned a destructuring right side reading a container a sibling target
// mutates. The right side is evaluated first and what it captured is a value, so
// a write through one target cannot reach it -- which is the same rule every
// other binding follows, now that it holds here too.
func TestDestructuringRightSideHoldsASnapshot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `class User
  property p: string

  def initialize()
    source = [[0], ["a", "b"]]
    source[1][0], (@p, ignored) = ["ok", source[1]]
    @rest = source[1]
  end

  def rest()
    @rest
  end
end

def run()
  u = User.new
  [u.p, u.rest.inspect]
end
`)

	got := callFunc(t, script, "run", nil).Array()
	if len(got) != 2 {
		t.Fatalf("run() returned %d values, want 2", len(got))
	}
	// @p took the first element of the value source[1] had when the right side
	// was built, not the "ok" the sibling target wrote afterwards.
	if got[0].String() != "a" {
		t.Fatalf("@p = %s, want a (the value the right side captured)", got[0].String())
	}
	// The sibling target still reached the path it addresses.
	if got[1].String() != `["ok", "b"]` {
		t.Fatalf("source[1] = %s, want [\"ok\", \"b\"]", got[1].String())
	}
}

// TestConcatAccumulatorReuseStaysInvisible covers the one place two array
// wrappers can still share an element backing: the `x = x + [...]` accumulator
// appends into a hidden buffer across iterations, so a binding taken mid-loop
// keeps a wrapper whose backing the next iteration goes on filling.
//
// That reuse is a storage economy and must stay one. Every case here writes
// through the older binding -- growing it, shrinking it, and overwriting an
// element -- because those are the three ways a shared backing could show.
func TestConcatAccumulatorReuseStaysInvisible(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{
			name: "element write through the older binding",
			body: `a = []
  a = a + [1]
  a = a + [2]
  b = a
  a = a + [3]
  b[0] = 99
  b.inspect + " " + a.inspect`,
			want: "[99, 2] [1, 2, 3]",
		},
		{
			name: "growth through the older binding",
			body: `c = []
  c = c + [1]
  d = c
  c = c + [2]
  d.push(9)
  c.inspect + " " + d.inspect`,
			want: "[1, 2] [1, 9]",
		},
		{
			name: "shrink through the older binding",
			body: `e = []
  e = e + [1]
  e = e + [2]
  f = e
  e = e + [3]
  f.pop
  e.inspect + " " + f.inspect`,
			want: "[1, 2, 3] [1]",
		},
		{
			name: "element extracted before the parent regrows",
			body: `g = [[1]]
  inner = g[0]
  g = g + [[2]]
  inner.push(5)
  g.inspect + " " + inner.inspect`,
			want: "[[1], [2]] [1, 5]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestOneSourceArrayGivesEachInstanceItsOwn pins that constructing two objects
// from one array leaves each holding its own value, so a method that updates
// one instance's variable cannot reach the other's or the caller's.
func TestOneSourceArrayGivesEachInstanceItsOwn(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `class Box
  def initialize(v)
    @v = v
  end
  def v()
    @v
  end
  def add(x)
    @v.push(x)
    self
  end
end

def run()
  shared = [1]
  first = Box.new(shared)
  second = Box.new(shared)
  first.add(2)
  shared.inspect + " " + first.v.inspect + " " + second.v.inspect
end
`)

	if got := callFunc(t, script, "run", nil).String(); got != "[1] [1, 2] [1]" {
		t.Fatalf("run() = %s, want [1] [1, 2] [1]", got)
	}
}

// TestCompoundAssignmentIsolatesItsTarget covers the write form that reads,
// computes, and writes back. It prepares its target before the right side runs,
// which made it the one assignment path that reached its receiver without
// isolating it: `a[0] += 1` wrote through whatever wrapper the read found, and
// every other binding of a saw it.
func TestCompoundAssignmentIsolatesItsTarget(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{
			name: "index add",
			body: `a = [1, 2]
  b = a
  a[0] += 10
  a.inspect + " " + b.inspect`,
			want: "[11, 2] [1, 2]",
		},
		{
			name: "hash index add",
			body: `h = { n: 1 }
  g = h
  h["n"] += 10
  h.inspect + " " + g.inspect`,
			want: "{n: 11} {n: 1}",
		},
		{
			name: "member add",
			body: `m = { n: 1 }
  k = m
  m.n += 10
  m.inspect + " " + k.inspect`,
			want: "{n: 11} {n: 1}",
		},
		{
			name: "member or-assign",
			body: `o = { n: nil }
  p = o
  o.n ||= 5
  o.inspect + " " + p.inspect`,
			want: "{n: 5} {n: nil}",
		},
		{
			name: "index or-assign",
			body: `q = [nil]
  r = q
  q[0] ||= 7
  q.inspect + " " + r.inspect`,
			want: "[7] [nil]",
		},
		{
			name: "nested path add",
			body: `d = { rows: [[1, 2]] }
  e = d
  d["rows"][0][1] += 5
  d.inspect + " " + e.inspect`,
			want: "{rows: [[1, 7]]} {rows: [[1, 2]]}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestCompoundAssignmentReisolatesAfterTheRightSideRuns pins why the target is
// isolated twice. The right side is script code: it can bind the receiver
// somewhere new between the moment the target was prepared and the moment the
// write lands, and a wrapper that was exclusively held then need not still be.
func TestCompoundAssignmentReisolatesAfterTheRightSideRuns(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `class Holder
  def keep(x)
    @kept = x
    10
  end
  def kept()
    @kept
  end
end

def run()
  holder = Holder.new
  a = [1, 2]
  a[0] += holder.keep(a)
  a.inspect + " " + holder.kept.inspect
end
`)

	if got := callFunc(t, script, "run", nil).String(); got != "[11, 2] [1, 2]" {
		t.Fatalf("run() = %s, want [11, 2] [1, 2]", got)
	}
}

// TestCompoundAssignmentKeepsAReboundRootIntact pins the other half of the
// second pass: if the right side rebinds the root, the pending write belongs
// to the receiver that was already evaluated, not the replacement.
func TestCompoundAssignmentKeepsAReboundRootIntact(t *testing.T) {
	t.Parallel()

	// Assignment is a statement, so the right side rebinds through a method
	// rather than `a[0] += (a = [9]; 1)`. The path root is an instance
	// variable; isolateMutablePath treats it the same as a local.
	script := compileScriptDefault(t, `class Box
  def run()
    @a = [1, 2]
    @a[0] += replace()
    @a.inspect
  end
  def replace()
    @a = [9]
    1
  end
end

def run()
  Box.new.run
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[9]" {
		t.Fatalf("run() = %s, want [9]", got)
	}

	aliased := compileScriptDefault(t, `class Box
  def run()
    @a = [1, 2]
    @b = nil
    @a[0] += capture_and_replace()
    @a.inspect + " " + @b.inspect
  end
  def capture_and_replace()
    @b = @a
    @a = [9]
    1
  end
end

def run()
  Box.new.run
end
`)
	if got := callFunc(t, aliased, "run", nil).String(); got != "[9] [1, 2]" {
		t.Fatalf("aliased = %s, want [9] [1, 2]", got)
	}
}

func TestCompoundAssignmentKeepsAReboundIntermediateIntact(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `class Box
  def run()
    @a = [[1]]
    @a[0][0] += replace_child()
    @a.inspect
  end
  def replace_child()
    @a[0] = [9]
    1
  end
end

def run()
  Box.new.run
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[[9]]" {
		t.Fatalf("run() = %s, want [[9]]", got)
	}
}

// TestCompoundAssignmentStillReachesItsRoot is the other half: isolating must
// not cost the update. A loop that accumulates through one path has to land
// every time, or value semantics would just be a way to lose writes.
func TestCompoundAssignmentStillReachesItsRoot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  acc = { n: 0 }
  5.times { acc["n"] += 1 }
  acc.inspect
end
`)

	if got := callFunc(t, script, "run", nil).String(); got != "{n: 5}" {
		t.Fatalf("run() = %s, want {n: 5}", got)
	}
}

func TestIndexAssignmentDoesNotFollowAFilledMissingPath(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `class Box
  def run()
    @a = {}
    begin
      @a["x"][install()] = 1
    rescue
      return @a.inspect
    end
    @a.inspect
  end
  def install()
    @a["x"] = []
    0
  end
end

def run()
  Box.new.run
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "{x: []}" {
		t.Fatalf("@a[\"x\"][install()] = 1 = %s, want {x: []}", got)
	}
}

func TestIndexAssignmentDoesNotFollowAReboundScalarRoot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `class Box
  def run()
    @a = "x"
    begin
      @a[install()] = 1
    rescue
      return @a.inspect
    end
    @a.inspect
  end
  def install()
    @a = [9]
    0
  end
end

def run()
  Box.new.run
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[9]" {
		t.Fatalf("@a[install()] = 1 after @a = \"x\" = %s, want [9]", got)
	}
}

func TestFillSelfEmptyWindowDoesNotCloneTheReceiver(t *testing.T) {
	t.Parallel()
	skipNoCopyPin(t)

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 240 << 10, StepQuota: Unlimited}, `
    def run()
      a = []
      i = 0
      while i < 2000
        a << "xxxxxxxxxxxxxxxx"
        i += 1
      end
      a.fill(a, 0, 0)
      a.size
    end
    `)
	if got := callFunc(t, script, "run", nil).Int(); got != 2000 {
		t.Fatalf("a.fill(a, 0, 0) size = %d, want 2000", got)
	}
}

// skipNoCopyPin skips a test whose assertion is a cost pin -- a quota sized
// to catch one copy, or wrapper identity -- rather than a semantic one. The
// always-copy oracle changes every byte charged and every wrapper built, so
// these have no stable answer under it (see skipUnderCollectionCopyVerify).
func skipNoCopyPin(t *testing.T) {
	t.Helper()
	if skipUnderCollectionCopyVerify() {
		t.Skip("cost pin: quota and identity assertions have no stable answer under the always-copy oracle")
	}
}

func TestSharedClearDoesNotCopyDiscardedContents(t *testing.T) {
	t.Parallel()
	skipNoCopyPin(t)

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 240 << 10, StepQuota: Unlimited}, `
    def run()
      a = []
      i = 0
      while i < 2000
        a << "xxxxxxxxxxxxxxxx"
        i += 1
      end
      b = a
      a.clear
      a.inspect + " " + b.size.to_s
    end
    `)
	if got := callFunc(t, script, "run", nil).String(); got != "[] 2000" {
		t.Fatalf("a.clear with sibling b = %s, want [] 2000", got)
	}
}

func TestMutatorOnANonStorageMemberWritesATemporary(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"dup push", "out = a.dup.push(2)\n  a.inspect + \" \" + out.inspect", "[1] [1, 2]"},
		{"keys clear", "h = {a: 1}\n  out = h.keys.clear\n  h.inspect + \" \" + out.inspect", "{a: 1} []"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  a = [1]\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

func TestNestedWriteIsolatesASharedAncestor(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [[1]]
  b = a
  a[0].push(2)
  a.inspect + " " + b.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[[1, 2]] [[1]]" {
		t.Fatalf("a[0].push(2) with sibling b = %s, want [[1, 2]] [[1]]", got)
	}
}

// TestIndexedMissReadsAndContinuesOrdinarily pins that an indexed miss along
// a mutable path reads exactly as evaluating the expression would: object
// specials like MatchData captures resolve, the remaining hops still run --
// side effects included -- and the error identity matches ordinary
// evaluation.
func TestIndexedMissReadsAndContinuesOrdinarily(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"tail after mid-path miss still runs", "a = []\n  c = []\n  out = begin\n    a[9][c.push(1).size].push(2)\n  rescue => e\n    \"rescued\"\n  end\n  out.to_s + \" \" + c.size.to_s", "rescued 1"},
		{"match capture chains a mutator", "m = \"ab\".match(/(?<x>a)/)\n  out = m[:x].sub(\"a\", \"-\")\n  out", "-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestOrdinaryReceiverDoesNotInheritThePermission pins that a mutator whose
// receiver expression falls to ordinary evaluation cannot write in place
// through the enclosing mutator's record, even when the evaluated temporary
// happens to be the recorded wrapper.
func TestOrdinaryReceiverDoesNotInheritThePermission(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [1]
  a.push((true ? a : a).push(2))
  a.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[1, [1, 2]]" {
		t.Fatalf("conditional receiver = %s, want [1, [1, 2]]", got)
	}
}

// TestMutatorArgumentDoesNotDropTheOuterWrite pins that an inner mutator in
// an argument expression -- whose isolation may copy and rebind the very
// slot the outer mutator addressed -- still leaves the outer write landing
// on the same logical receiver: `a.push(a.pop)` reads back as the original
// contents. A script rebind still detaches the pending write (see
// TestEmptyFilterDoesNotOverwriteAReboundNestedSlot); only the isolation's
// own displacement forwards.
func TestMutatorArgumentDoesNotDropTheOuterWrite(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"sole root", "a = [1, 2]\n  a.push(a.pop)\n  a.inspect", "[1, 2]"},
		{"shared sibling", "a = [1, 2]\n  b = a\n  a.push(a.pop)\n  a.inspect + \" \" + b.inspect", "[1, 2] [1, 2]"},
		{"nested slot", "h = {x: [1, 2]}\n  h[:x].push(h[:x].shift)\n  h.inspect", "{x: [2, 1]}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestMutatorChainThroughAnAccessorTemporary pins the tail semantics after a
// path leaves collection storage: the first hop runs exactly as evaluating it
// alone would (`a.pop` pops), and everything after it works a temporary --
// writes through the chained value reach nothing a durable slot names.
func TestMutatorChainThroughAnAccessorTemporary(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"pop chain pops once", "a = [[1], [2]]\n  out = a.pop.push(9)\n  a.inspect + \" \" + out.inspect", "[[1]] [2, 9]"},
		{"nested mutator in own arguments", "a = [1]\n  a.push(a.itself.push(2))\n  a.inspect", "[1, [1, 2]]"},
		{"assign through accessor reaches nothing", "a = [[1]]\n  b = a\n  a.last[0] = 9\n  a.inspect + \" \" + b.inspect", "[[1]] [[1]]"},
		{"compound assign through pop temporary", "a = [[1], [2]]\n  a.pop[0] += 1\n  a.inspect", "[[1]]"},
		{"assign through dup temporary", "a = [1]\n  a.dup[0] = 9\n  a.inspect", "[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestAssignThroughACallFormAccessorReachesNothing pins that the call-form
// spelling of an accessor receiver behaves exactly like the member form
// (#1221, following #1219): the call's result is a temporary, so the write
// lands on a detached copy and reaches nothing a durable slot still names.
func TestAssignThroughACallFormAccessorReachesNothing(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"index assign through accessor call", "a = [[1]]\n  b = a\n  a.last()[0] = 9\n  a.inspect + \" \" + b.inspect", "[[1]] [[1]]"},
		{"call and member spellings agree", "a = [[1]]\n  b = a\n  a.last()[0] = 9\n  a.last[0] = 9\n  a.inspect + \" \" + b.inspect", "[[1]] [[1]]"},
		{"nested tail", "a = [[[1]]]\n  b = a\n  a.last()[0][0] = 9\n  a.inspect + \" \" + b.inspect", "[[[1]]] [[[1]]]"},
		{"member assign through call", "h = {inner: {x: 1}}\n  g = h\n  h.values()[0].x = 2\n  h.inspect + \" \" + g.inspect", "{inner: {x: 1}} {inner: {x: 1}}"},
		{"compound assign through call", "a = [[1]]\n  b = a\n  a.last()[0] += 8\n  a.inspect + \" \" + b.inspect", "[[1]] [[1]]"},
		{"getter call hands out the ivar", "c = Cart.new\n  c.items()[0] = 9\n  c.items.inspect", "[1]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, collectionSemanticsPrelude+"def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestCallRootedIndexAssignChargesTailExpressionSteps pins that a write
// through a call-rooted path still pays exec.step for every MemberExpr and
// IndexExpr hop after the call. The new route evaluates the call once and
// walks the tail in readPathTailOrdinarily; without a step per hop,
// factory().a.a.a.a[0] = 9 would succeed under a quota the equivalent
// ordinary read already exhausts.
func TestCallRootedIndexAssignChargesTailExpressionSteps(t *testing.T) {
	t.Parallel()

	const factory = `def factory()
  {a: {a: {a: {a: [1]}}}}
end
`
	readQuota := minSuccessfulStepQuota(t, factory+`def run()
  factory().a.a.a.a[0]
end
`)
	writeQuota := minSuccessfulStepQuota(t, factory+`def run()
  factory().a.a.a.a[0] = 9
end
`)
	if writeQuota < readQuota {
		t.Fatalf("call-rooted assign min step quota = %d, ordinary read of the same path needed %d",
			writeQuota, readQuota)
	}
}

// TestCallRootedAssignChecksIntermediateReceiverMemory pins that a
// parenless getter in a call-rooted tail still charges its oversized
// result before the next hop. factory().large.small[0] = 9 must not
// skip the huge hash large() returns, the way ordinary finishMemberExpr
// would have called checkMemoryValue on that receiver.
func TestCallRootedAssignChecksIntermediateReceiverMemory(t *testing.T) {
	t.Parallel()

	source := `class Box
  def large()
    a = []
    i = 0
    while i < 2000
      a.push("xxxxxxxxxxxxxxxx")
      i += 1
    end
    {small: [1], pad: a}
  end
end
def factory()
  Box.new
end
def run()
  factory().large.small[0] = 9
end
`
	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 64 << 10, StepQuota: Unlimited}, source)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("factory().large.small[0] = 9 under a quota that cannot hold large()'s hash must error")
	}
	requireErrorContains(t, err, "quota exceeded")
}

func minSuccessfulStepQuota(t *testing.T, source string) int {
	t.Helper()
	const maxQuota = 200
	for quota := 1; quota <= maxQuota; quota++ {
		script := compileScriptWithConfig(t, Config{StepQuota: quota, MemoryQuotaBytes: Unlimited}, source)
		_, err := script.Call(context.Background(), "run", nil, CallOptions{})
		if err == nil {
			return quota
		}
	}
	t.Fatalf("source never succeeded under step quotas 1..%d", maxQuota)
	return 0
}

// TestAssignThroughAGetterCallLeavesTheIvarIntact pins the issue's second
// reproduction (#1221) in every spelling: a getter reached as an explicit
// call (`geta()[0]`), a bare parenless name (`geta[0]`), or parenless
// through self (`self.geta[0]`) hands out the live ivar wrapper, and an
// assignment through it must not write that storage in place. The getter
// still runs exactly once per assignment, and a bare name that names no
// zero-argument call keeps its route: a required-argument method and an
// unknown member error exactly as reading the expression would.
func TestAssignThroughAGetterCallLeavesTheIvarIntact(t *testing.T) {
	t.Parallel()

	const prelude = `class Holder
  def initialize()
    @a = [1]
    @calls = 0
  end

  def geta()
    @calls += 1
    @a
  end

  def getb(x)
    @a
  end

  def poke_call()
    geta()[0] = 9
    @a.inspect + " " + @calls.to_s
  end

  def poke_parenless()
    geta[0] = 9
    @a.inspect + " " + @calls.to_s
  end

  def poke_self_parenless()
    self.geta[0] = 9
    @a.inspect + " " + @calls.to_s
  end

  def poke_compound_parenless()
    geta[0] += 8
    @a.inspect + " " + @calls.to_s
  end

  def poke_required_params()
    out = begin
      getb[0] = 9
    rescue => e
      e.message
    end
    out + " " + @a.inspect
  end

  def poke_unknown()
    begin
      nope[0] = 9
    rescue => e
      e.message
    end
  end
end

`
	cases := []struct{ name, body, want string }{
		{"explicit call", "Holder.new.poke_call()", "[1] 1"},
		{"bare parenless", "Holder.new.poke_parenless()", "[1] 1"},
		{"parenless through self", "Holder.new.poke_self_parenless()", "[1] 1"},
		{"compound parenless", "Holder.new.poke_compound_parenless()", "[1] 1"},
		{"required-argument method errors as a read would", "Holder.new.poke_required_params()", "missing argument x [1]"},
		{"unknown member keeps its error", "Holder.new.poke_unknown()", "unknown member nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, prelude+"def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestUnresolvedBareNameAssignKeepsItsRoute pins that claiming auto-invoked
// bare-name roots (#1221) leaves every other unresolved root exactly as it
// was: an undefined name keeps its error identity, and a class-constant root
// keeps the in-place class-storage write channel.
func TestUnresolvedBareNameAssignKeepsItsRoute(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, source, want string }{
		{
			name: "undefined variable keeps its error",
			source: `def run()
  begin
    nope[0] = 9
  rescue => e
    e.message
  end
end
`,
			want: "undefined variable nope",
		},
		{
			name: "class constant root writes class storage",
			source: `class Consts
  LIST = [1]

  def poke()
    LIST[0] = 9
    LIST.inspect
  end
end

def run()
  Consts.new.poke()
end
`,
			want: "[9]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.source)
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

// TestBuiltinNamespaceMemberAssignStaysInPlace pins the one sanctioned
// in-place member-assign channel: writing a member of an engine builtin
// object (`JSON.parse = ...`) must land where the same call reads it back,
// not on a detached copy.
func TestBuiltinNamespaceMemberAssignStaysInPlace(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  JSON.parse = "swapped"
  JSON.parse
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "swapped" {
		t.Fatalf("builtin namespace member assign = %s, want swapped", got)
	}
}

// TestMissingIndexedSlotStaysNilAndEvaluatesOnce pins that an indexed miss
// reads as nil exactly as the expression would -- the cached index is not
// re-evaluated, so a side-effecting selector runs once and cannot address a
// different slot on a second run.
func TestMissingIndexedSlotStaysNilAndEvaluatesOnce(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [[0], [1]]
  c = []
  out = begin
    a[8 / c.push(1).size].push(9)
  rescue
    "err"
  end
  out.to_s + " " + c.size.to_s + " " + a.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "err 1 [[0], [1]]" {
		t.Fatalf("missing indexed slot = %s, want err 1 [[0], [1]]", got)
	}
}

// TestNestedClearReturnsTheEmptiedValueWhenThePathVanishes pins the return
// contract of the clear-shaped mutators: when the mutator's own block
// invalidates the parent path, nothing is installed, but the mutator still
// returns the emptied collection rather than nil.
func TestNestedClearReturnsTheEmptiedValueWhenThePathVanishes(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"array parent cleared", "a = [[1, 2]]\n  out = a[0].delete_if { |x| a.clear; true }\n  out.inspect + \" \" + a.inspect", "[] []"},
		{"hash parent slot deleted", "h = {x: {y: 1}}\n  out = h[:x].delete_if { |k, v| h.delete(:x); true }\n  out.inspect + \" \" + h.inspect", "{} {}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

func TestNestedClearIsolatesASharedAncestor(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"array leaf", "a = [[1]]\n  b = a\n  a[0].clear\n  a.inspect + \" \" + b.inspect", "[[]] [[1]]"},
		{"hash leaf", "a = {x: {y: 1}}\n  b = a\n  a[:x].clear\n  a.inspect + \" \" + b.inspect", "{x: {}} {x: {y: 1}}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("nested clear with sibling = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestEmptyFilterDoesNotOverwriteAReboundNestedSlot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = { x: [1] }
  a[:x].delete_if { a[:x] = [9]; true }
  a[:x].inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[9]" {
		t.Fatalf("a[:x].delete_if rebound then emptied = %s, want [9]", got)
	}
}

func TestSharedHashClearDoesNotCopyDiscardedContents(t *testing.T) {
	t.Parallel()
	skipNoCopyPin(t)

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 400 << 10, StepQuota: Unlimited}, `
    def run()
      h = {}
      i = 0
      while i < 2000
        h[i.to_s] = "xxxxxxxxxxxxxxxx"
        i += 1
      end
      g = h
      h.clear
      h.inspect + " " + g.size.to_s
    end
    `)
	if got := callFunc(t, script, "run", nil).String(); got != "{} 2000" {
		t.Fatalf("h.clear with sibling g = %s, want {} 2000", got)
	}
}

func TestFillEmptyWindowDoesNotCopyASharedReceiver(t *testing.T) {
	t.Parallel()
	skipNoCopyPin(t)

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 240 << 10, StepQuota: Unlimited}, `
    def run()
      a = []
      i = 0
      while i < 2000
        a << "xxxxxxxxxxxxxxxx"
        i += 1
      end
      b = a
      a.fill(0, 0, 0)
      [a, b]
    end
    `)
	got := callFunc(t, script, "run", nil).Array()
	if arrayIdentity(got[0]) != arrayIdentity(got[1]) {
		t.Fatalf("a.fill(0, 0, 0) copied a shared receiver: identities %d and %d",
			arrayIdentity(got[0]), arrayIdentity(got[1]))
	}
}

func TestHashReplaceSelfDoesNotCopyASharedReceiver(t *testing.T) {
	t.Parallel()
	skipNoCopyPin(t)

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 400 << 10, StepQuota: Unlimited}, `
    def run()
      h = {}
      i = 0
      while i < 2000
        h[i.to_s] = "xxxxxxxxxxxxxxxx"
        i += 1
      end
      g = h
      h.replace(h)
      [h, g]
    end
    `)
	got := callFunc(t, script, "run", nil).Array()
	if hashIdentity(got[0]) != hashIdentity(got[1]) {
		t.Fatalf("h.replace(h) copied a shared receiver: identities %d and %d",
			hashIdentity(got[0]), hashIdentity(got[1]))
	}
}

func TestPushSelfRejectsWhenTheCloneWouldExceedQuota(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 240 << 10, StepQuota: Unlimited}, `
    def run()
      a = []
      i = 0
      while i < 2000
        a << "xxxxxxxxxxxxxxxx"
        i += 1
      end
      a.push(a)
    end
    `)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatal("a.push(a) under a quota that cannot hold the clone must error")
	}
	requireErrorContains(t, err, "quota exceeded")
}

func TestNestedWriteDoesNotMutateTaggedMatchData(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  m = "ab".match(/(a)(b)/)
  copy = m.captures
  begin
    m.captures.push("bad")
    return "mutated " + m.captures.inspect + " " + copy.inspect
  rescue => e
    return e.message + " " + m.captures.inspect + " " + copy.inspect
  end
end
`)
	got := callFunc(t, script, "run", nil).String()
	if !strings.Contains(got, "cannot modify match data") {
		t.Fatalf("m.captures.push = %s, want a match-data mutation error", got)
	}
	if !strings.Contains(got, `["a", "b"] ["a", "b"]`) {
		t.Fatalf("m.captures.push = %s, want both captures bindings to stay [\"a\", \"b\"]", got)
	}
}

func TestFlatMapDoesNotPublishANonArrayResultTwice(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  [1].flat_map { {} }[0]
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatalf("[1].flat_map { {} }[0] SoleRef() = false, want true")
	}
}

// TestMutatorNoOpDoesNotIsolateASharedReceiver checks that recording a
// mutator's path does not copy before the mutator decides whether it will
// write. a.pop(0) and a.push() on a shared receiver must leave both bindings
// on the same wrapper.
func TestEqualPredicateMetersCollectionWalks(t *testing.T) {
	t.Parallel()

	n := 8_000
	left := make([]Value, n)
	right := make([]Value, n)
	for i := range n {
		left[i] = NewString("x")
		right[i] = NewString("x")
	}
	script := compileScriptWithConfig(t, Config{StepQuota: 20, MemoryQuotaBytes: Unlimited}, `
    def run(a, b)
      a.equal?(b)
    end
    `)
	_, err := script.Call(context.Background(), "run", []Value{NewArray(left), NewArray(right)}, CallOptions{})
	if err == nil {
		t.Fatal("large_a.equal?(large_b) must consume the step quota")
	}
	requireErrorContains(t, err, "quota exceeded")
}

func TestToHDoesNotRetainTheReturnedPairWrapper(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  pair = ["k", 1]
  [0].to_h { pair }
  pair
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatalf("[0].to_h { pair } left pair shared, SoleRef() = false, want true")
	}
}

func TestMutatorNoOpDoesNotIsolateASharedReceiver(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [1, 2]
  b = a
  h = { a: 1 }
  g = h
  a.pop(0)
  a.shift(0)
  a.push()
  a.insert(0)
  a.delete(99)
  a.delete_if { false }
  a.keep_if { true }
  a.fill(0, 0, 0)
  h.delete("missing")
  h.delete_if { false }
  h.keep_if { true }
  h.replace(h)
  [a, b, h, g]
end
`)
	got := callFunc(t, script, "run", nil).Array()
	if arrayIdentity(got[0]) != arrayIdentity(got[1]) {
		t.Fatalf("no-op mutators copied shared array: identities %d and %d",
			arrayIdentity(got[0]), arrayIdentity(got[1]))
	}
	if hashIdentity(got[2]) != hashIdentity(got[3]) {
		t.Fatalf("hash.delete miss copied shared hash: identities %d and %d",
			hashIdentity(got[2]), hashIdentity(got[3]))
	}
}

func TestSendPushUpdatesTheAddressableReceiver(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [1]
  b = a
  a.send(:push, 2)
  a.inspect + " " + b.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[1, 2] [1]" {
		t.Fatalf("a.send(:push, 2) = %s, want [1, 2] [1]", got)
	}

	nested := compileScriptDefault(t, `def run()
  a = [1]
  b = a
  a.send(:send, :push, 2)
  a.inspect + " " + b.inspect
end
`)
	if got := callFunc(t, nested, "run", nil).String(); got != "[1, 2] [1]" {
		t.Fatalf("a.send(:send, :push, 2) = %s, want [1, 2] [1]", got)
	}
}

func TestReduceShovelDoesNotMutateTheSourceElement(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, call string }{
		{"string op", `a.reduce("<<")`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  a = [[], 1]\n  out = "+tc.call+"\n  a.inspect + \" \" + out.inspect\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != "[[], 1] [1]" {
				t.Fatalf("%s = %s, want [[], 1] [1]", tc.call, got)
			}
		})
	}
}

func TestHostBuiltinPublishesARetainedArgument(t *testing.T) {
	t.Parallel()

	var kept Value
	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("save", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		kept = args[0]
		return NewNil(), nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  a = []
  save(a)
  a.push(1)
  a.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[1]" {
		t.Fatalf("save(a); a.push(1) = %s, want [1]", got)
	}
	if got := kept.Inspect(); got != "[]" {
		t.Fatalf("host-retained save(a) after a.push(1) = %s, want []", got)
	}
}

func TestHostBuiltinIsolatesASharedArgument(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("host_mutate", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		if err := args[0].HashSet(NewString("x"), NewInt(9)); err != nil {
			return NewNil(), err
		}
		return args[0], nil
	})
	script := compileScriptWithEngine(t, engine, `def run()
  a = { x: 1 }
  b = a
  host_mutate(a)
  a.inspect + " " + b.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "{x: 1} {x: 1}" {
		t.Fatalf("host_mutate(a) with sibling b = %s, want {x: 1} {x: 1}", got)
	}
}

func TestHostBuiltinIsolatesANestedArgument(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("host_mutate_nested", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		child := args[0].Hash()["child"]
		if err := child.HashSet(NewString("x"), NewInt(9)); err != nil {
			return NewNil(), err
		}
		return args[0], nil
	})

	t.Run("shared child", func(t *testing.T) {
		t.Parallel()
		script := compileScriptWithEngine(t, engine, `def run()
  child = { x: 1 }
  a = { child: child }
  host_mutate_nested(a)
  child.inspect + " " + a[:child].inspect
end
`)
		if got := callFunc(t, script, "run", nil).String(); got != "{x: 1} {x: 1}" {
			t.Fatalf("host_mutate_nested(a) with sibling child = %s, want {x: 1} {x: 1}", got)
		}
	})

	t.Run("shared parent", func(t *testing.T) {
		t.Parallel()
		script := compileScriptWithEngine(t, engine, `def run()
  child = { x: 1 }
  a = { child: child }
  b = a
  host_mutate_nested(a)
  child.inspect + " " + a[:child].inspect + " " + b[:child].inspect
end
`)
		if got := callFunc(t, script, "run", nil).String(); got != "{x: 1} {x: 1} {x: 1}" {
			t.Fatalf("host_mutate_nested(a) with sibling b = %s, want {x: 1} {x: 1} {x: 1}", got)
		}
	})

	// Inverted deliberately for #1210: an argument named by a script slot
	// crosses the boundary as an independent value even when its graph is
	// exclusively held, so the host's write is invisible; the sanctioned
	// channel for a host mutation is the return value.
	t.Run("exclusive nested", func(t *testing.T) {
		t.Parallel()
		script := compileScriptWithEngine(t, engine, `def run()
  a = { child: { x: 1 } }
  host_mutate_nested(a)
  a[:child].inspect
end
`)
		if got := callFunc(t, script, "run", nil).String(); got != "{x: 1}" {
			t.Fatalf("host_mutate_nested(a) exclusive child = %s, want {x: 1}", got)
		}
	})
}

func TestKeywordSplatMemoryCheckDoesNotPublishValues(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  h = { x: [] }
  assert(true, **h)
  h[:x]
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatal("h[:x] after assert(true, **h) SoleRef() = false, want true")
	}
}

func TestSplatMemoryCheckDoesNotPublishReceiverElements(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [[1]]
  assert(*a)
  a[0]
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatal("a[0] after assert(*a) SoleRef() = false, want true")
	}
}

func TestSetOpScratchDoesNotPublishReceiverElements(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 64 << 10, StepQuota: Unlimited}, `def run()
  a = [[]]
  a.difference([[]])
  a[0]
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatal("a[0] after a.difference([[]]) SoleRef() = false, want true")
	}
}

func TestFetchValuesPublishesPresentEntries(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  h = { a: [] }
  vals = h.fetch_values(:a)
  vals[0].push(1)
  h[:a].inspect + " " + vals[0].inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[] [1]" {
		t.Fatalf("h.fetch_values(:a) then vals[0].push(1) = %s, want [] [1]", got)
	}
}

func TestGrepPublishesUntransformedMatches(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  source = [[]]
  out = source.grep_v(1)
  out[0].push(1)
  source[0].inspect + " " + out[0].inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[] [1]" {
		t.Fatalf("source.grep_v(1) then out[0].push(1) = %s, want [] [1]", got)
	}
}

func TestArrayRangeMutatorWritesATemporary(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body, want string }{
		{"push", "out = a[0..1].push(9)\n  a.inspect + \" \" + out.inspect", "[1, 2, 3] [1, 2, 9]"},
		{"shovel", "out = a[0..1] << 9\n  a.inspect + \" \" + out.inspect", "[1, 2, 3] [1, 2, 9]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  a = [1, 2, 3]\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("a[0..1] %s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}

func TestJSONParseDoesNotShareASoleObjectElement(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  JSON.parse("[{}]")[0]
end
`)
	got := callFunc(t, script, "run", nil)
	if !got.SoleRef() {
		t.Fatal("JSON.parse(\"[{}]\")[0] SoleRef() = false, want true")
	}
}

func TestFillPublishesEachRepeatedSlot(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  a = [0, 0]
  a.fill([])
  a[0].push(1)
  a.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[[1], []]" {
		t.Fatalf("a.fill([]) then a[0].push(1) = %s, want [[1], []]", got)
	}
}

func TestDupPublishesRepeatedChildren(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  child = [1]
  copy = [child, child].dup
  copy[0].push(2)
  copy.inspect
end
`)
	if got := callFunc(t, script, "run", nil).String(); got != "[[1, 2], [1]]" {
		t.Fatalf("[child, child].dup then copy[0].push(2) = %s, want [[1, 2], [1]]", got)
	}
}

func TestDupPublishesRepeatedObjectAttributes(t *testing.T) {
	t.Parallel()

	child := NewArray([]Value{NewInt(1)})
	obj := NewObject(map[string]Value{"a": child, "b": child})
	script := compileScriptDefault(t, `def run(obj)
  copy = obj.dup
  copy.a.push(2)
  copy.a.inspect + " " + copy.b.inspect
end
`)
	if got := callFunc(t, script, "run", []Value{obj}).String(); got != "[1, 2] [1]" {
		t.Fatalf("obj.dup then copy.a.push(2) = %s, want [1, 2] [1]", got)
	}
}

// TestWritesIsolateAfterScriptCodeRuns closes the class the compound-assignment
// finding opened. A write is licensed by an isolation check, and between that
// check and the write itself script code can run and bind the receiver somewhere
// new -- so the license goes stale. Every way script code can get in between is
// covered here.
//
// The two failure modes are different and both matter. A write that finds a
// published receiver and merely copies loses the update, because the copy is
// installed nowhere; a write that does not look again at all leaks to the
// sibling binding. Each case asserts both halves: the update landed, and the
// binding taken mid-flight holds what it was given.
func TestWritesIsolateAfterScriptCodeRuns(t *testing.T) {
	t.Parallel()

	const prelude = `class Capture
  def keep(x)
    @kept = x
    0
  end
  def kept()
    @kept
  end
end

`

	cases := []struct{ name, body, want string }{
		{
			name: "mutator argument expression",
			body: `c = Capture.new
  a = [1]
  a.push(c.keep(a))
  a.inspect + " " + c.kept.inspect`,
			want: "[1, 0] [1]",
		},
		{
			name: "shovel right operand",
			body: `c = Capture.new
  a = [1]
  a << c.keep(a)
  a.inspect + " " + c.kept.inspect`,
			want: "[1, 0] [1]",
		},
		{
			name: "array filter block",
			body: `c = Capture.new
  a = [1, 2, 3]
  a.delete_if { |x| c.keep(a); x == 2 }
  a.inspect + " " + c.kept.inspect`,
			want: "[1, 3] [1, 2, 3]",
		},
		{
			name: "array fill block",
			body: `c = Capture.new
  a = [1, 2]
  a.fill { |i| c.keep(a); 9 }
  a.inspect + " " + c.kept.inspect`,
			want: "[9, 9] [1, 2]",
		},
		{
			name: "hash filter block",
			body: `c = Capture.new
  h = { x: 1, y: 2 }
  h.delete_if { |k, v| c.keep(h); v == 2 }
  h.inspect + " " + c.kept.inspect`,
			want: "{x: 1} {x: 1, y: 2}",
		},
		{
			name: "index selector expression",
			body: `c = Capture.new
  a = [1, 2]
  a[c.keep(a)] = 9
  a.inspect + " " + c.kept.inspect`,
			want: "[9, 2] [1, 2]",
		},
		{
			name: "assigned value expression",
			body: `c = Capture.new
  a = [1, 2]
  a[0] = c.keep(a)
  a.inspect + " " + c.kept.inspect`,
			want: "[0, 2] [1, 2]",
		},
		{
			name: "member assigned value expression",
			body: `c = Capture.new
  h = { n: 1 }
  h.n = c.keep(h)
  h.inspect + " " + c.kept.inspect`,
			want: "{n: 0} {n: 1}",
		},
		{
			name: "compound assignment right side",
			body: `c = Capture.new
  a = [1, 2]
  a[0] += c.keep(a) + 10
  a.inspect + " " + c.kept.inspect`,
			want: "[11, 2] [1, 2]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, prelude+"def run()\n  "+tc.body+"\nend\n")
			if got := callFunc(t, script, "run", nil).String(); got != tc.want {
				t.Fatalf("%s = %s, want %s", tc.name, got, tc.want)
			}
		})
	}
}
