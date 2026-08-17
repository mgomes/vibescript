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

// TestMutatorNoOpDoesNotIsolateASharedReceiver checks that recording a
// mutator's path does not copy before the mutator decides whether it will
// write. a.pop(0) and a.push() on a shared receiver must leave both bindings
// on the same wrapper.
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
  h.delete("missing")
  h.delete_if { false }
  h.keep_if { true }
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
		{"symbol proc", "a.reduce(&:<<)"},
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
