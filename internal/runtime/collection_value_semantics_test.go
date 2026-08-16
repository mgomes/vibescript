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
