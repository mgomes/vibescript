package runtime

import (
	"strings"
	"testing"
)

// Element writes to a local whose fact is a declared array<T> — shovel
// appends, indexed assignment, and the in-place builtin mutators — are
// checked against T: a provably disjoint value is reported at the write, a
// provably compatible write preserves the declared fact, and everything else
// conservatively weakens it.

func TestCheckArrayWriteContradictions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "shovel to typed parameter",
			source: `
def f(items: array<int>)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "indexed assignment to typed parameter",
			source: `
def f(items: array<int>)
  items[0] = "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "push argument",
			source: `
def f(items: array<int>)
  items.push("bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "push later argument",
			source: `
def f(items: array<int>)
  items.push(1, "bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "push argument after the receiver escapes as an argument",
			source: `
def helper(a)
  0
end

def f(items: array<int>)
  items.push(helper(items), "bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "push argument after a receiver member access",
			source: `
def f(items: array<int>)
  items.push(items.length, "bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "shovel value that escapes the receiver",
			source: `
def returns_string(a) -> string
  "s"
end

def f(items: array<int>)
  items << returns_string(items)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "module element admits including class instances",
			source: `
module Nameable
end

class User
  include Nameable

  def initialize()
  end
end

def f(items: array<Nameable>, u: User)
  items << u
  items << 1
end
`,
			warning: "write to items expected element Nameable, got int",
		},
		{
			name: "indexed write whose value escapes the receiver",
			source: `
def returns_string(a) -> string
  "s"
end

def f(items: array<int>)
  items[0] = returns_string(items)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "index-only insert preserves the bound",
			source: `
def f(items: array<int>)
  items.insert(0)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "unshift argument",
			source: `
def f(items: array<int>)
  items.unshift("bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "prepend argument",
			source: `
def f(items: array<string>)
  items.prepend(1)
end
`,
			warning: "write to items expected element string, got int",
		},
		{
			name: "splatted literal element",
			source: `
def f(items: array<int>)
  items.push(*["bad"])
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible splat preserves the bound",
			source: `
def f(items: array<int>)
  items.push(*[1, 2])
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "insert element argument",
			source: `
def f(items: array<int>)
  items.insert(0, "bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "union element type rejects a disjoint value",
			source: `
def f(items: array<int | string>)
  items << true
end
`,
			warning: "write to items expected element int | string, got bool",
		},
		{
			name: "nested array element type",
			source: `
def f(rows: array<array<int>>)
  rows << "bad"
end
`,
			warning: "write to rows expected element array<int>, got string",
		},
		{
			name: "shape element with a contradicting field",
			source: `
def f(items: array<{ amount: int }>)
  items << { amount: "bad" }
end
`,
			warning: "write to items expected element { amount: int }, got { amount: string }",
		},
		{
			name: "compatible shape element write preserves the bound",
			source: `
def f(items: array<{ amount: int }>)
  items << { amount: 1 }
  items << "bad"
end
`,
			warning: "write to items expected element { amount: int }, got string",
		},
		{
			name: "appended child local keeps the bound until it mutates",
			source: `
def f(rows: array<array<int>>)
  child = [1]
  rows << child
  rows << "bad"
end
`,
			warning: "write to rows expected element array<int>, got string",
		},
		{
			name: "safe navigation mutator checks the nullable bound",
			source: `
def f(items: array<int>?)
  items&.push("bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "plain mutator call checks the nullable bound",
			source: `
def f(items: array<int>?)
  items.push("bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible safe navigation mutator preserves the nullable bound",
			source: `
def f(items: array<int>?)
  items&.push(1)
  items&.push("bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "pushed child local keeps the bound until it mutates",
			source: `
def f(rows: array<array<int>>)
  child = [1]
  rows.push(child)
  rows << "bad"
end
`,
			warning: "write to rows expected element array<int>, got string",
		},
		{
			name: "selector side effects follow receiver selection",
			source: `
def idx -> int
  yield
  0
end

def f(items: array<int>)
  items[idx() do
    items = ["s"]
    0
  end] = "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "chained shovel appends through the chain root",
			source: `
def f(items: array<int>)
  (items << 1) << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible chained shovels preserve the bound",
			source: `
def f(items: array<int>)
  (items << 1) << 2
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "local seeded by a declared return type",
			source: `
def build -> array<int>
  []
end

def f
  items = build()
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "write through an alias of the typed parameter",
			source: `
def f(items: array<int>)
  other = items
  other << "bad"
end
`,
			warning: "write to other expected element int, got string",
		},
		{
			name: "nullable receiver narrowed by a nil guard",
			source: `
def f(items: array<int>?)
  return if items.nil?
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible shovel preserves the fact for the next write",
			source: `
def f(items: array<int>)
  items << 1
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible indexed write preserves the fact",
			source: `
def f(items: array<int>)
  items[0] = 2
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible push preserves the fact",
			source: `
def f(items: array<int>)
  items.push(1, 2)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "assigned compatible shovel result carries the bound",
			source: `
def f(items: array<int>)
  x = (items << 1)
  x << "bad"
end
`,
			warning: "write to x expected element int, got string",
		},
		{
			name: "receiver warns after a compatible shovel was assigned",
			source: `
def f(items: array<int>)
  x = (items << 1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "write inside a conditional branch",
			source: `
def f(items: array<int>, flag)
  if flag
    items << "bad"
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "write inside a loop body",
			source: `
def f(items: array<int>, flag)
  while flag
    items << "bad"
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "write inside a block body",
			source: `
def f(items: array<int>)
  [1].each do |i|
    items << "bad"
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible loop appends keep the fact after the loop",
			source: `
def f(items: array<int>, flag)
  while flag
    items << 1
  end
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible block appends keep the fact after the block",
			source: `
def f(items: array<int>)
  [1].each do |i|
    items << 2
  end
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckArrayWritesStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "compatible writes stay silent",
			source: `
def f(items: array<int>)
  items << 1
  items[0] = 2
  items.push(3, 4)
  items.unshift(0)
  items.prepend(0)
  items.insert(1, 5)
end
`,
		},
		{
			name: "unknown values stay silent",
			source: `
def f(items: array<int>, v)
  items << v
  items[0] = v
  items.push(v)
end
`,
		},
		{
			name: "unknown receiver stays silent",
			source: `
def f(items)
  items << "bad"
  items[0] = "bad"
  items.push("bad")
end
`,
		},
		{
			name: "any-typed array stays silent",
			source: `
def f(items: array<any>)
  items << "s"
  items[0] = 1
  items.push(nil)
end
`,
		},
		{
			name: "bare array annotation stays silent",
			source: `
def f(items: array)
  items << "s"
  items[0] = 1
end
`,
		},
		{
			name: "nullable receiver without a guard stays silent",
			source: `
def f(items: array<int>?)
  items << "bad"
end
`,
		},
		{
			name: "witnessed literal carries no element bound",
			source: `
def f
  items = [1]
  items << "s"
  items[0] = "s"
end
`,
		},
		{
			name: "overlapping union value is not a contradiction",
			source: `
def f(items: array<int>, flag)
  v = flag ? 1 : "s"
  items << v
end
`,
		},
		{
			name: "unknown append weakens the fact for later writes",
			source: `
def f(items: array<int>, v)
  items << v
  items << "bad"
end
`,
		},
		{
			name: "unknown indexed write weakens the fact",
			source: `
def f(items: array<int>, v)
  items[0] = v
  items << "bad"
end
`,
		},
		{
			name: "compound indexed assignment weakens the fact",
			source: `
def f(items: array<int>)
  items[0] += 1
  items << "bad"
end
`,
		},
		{
			name: "receiver escaping into a call drops the bound",
			source: `
def helper(a)
  a
end

def f(items: array<int>)
  helper(items)
  items << "bad"
end
`,
		},
		{
			name: "shovel expression escaping as an argument drops the bound",
			source: `
def helper(a)
  a
end

def f(items: array<int>)
  helper(items << 1)
  items << "bad"
end
`,
		},
		{
			name: "consumed mutator result drops the bound",
			source: `
def f(items: array<int>)
  x = items.push(1)
  items << "bad"
end
`,
		},
		{
			name: "chained mutator calls drop the bound",
			source: `
def f(items: array<int>)
  items.push(1).push("bad")
  items << "bad"
end
`,
		},
		{
			name: "assigned shovel result aliases the receiver",
			source: `
def f(items: array<int>, v)
  x = (items << 1)
  items << v
  x << "bad"
end
`,
		},
		{
			name: "unknown write through the assigned alias weakens the receiver",
			source: `
def f(items: array<int>, v)
  x = (items << 1)
  x << v
  items << "bad"
end
`,
		},
		{
			name: "insert with values never preserves the bound",
			source: `
def f(items: array<int>)
  items.insert(5, 1)
  items << "bad"
end
`,
		},
		{
			name: "mutating a shovel-valued pushed child weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<int>>)
  child = [1]
  rows.push(child << 2)
  child << "bad"
  for row in rows
    for v in row
      takes_string(v)
    end
  end
end
`,
		},
		{
			name: "mutating a pushed child weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<int>>)
  child = [1]
  rows.push(child)
  child << "bad"
  for row in rows
    for v in row
      takes_string(v)
    end
  end
end
`,
		},
		{
			name: "mutating an appended child weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<int>>)
  child = [1]
  rows << child
  child << "bad"
  for row in rows
    for v in row
      takes_string(v)
    end
  end
end
`,
		},
		{
			name: "escaping an appended child weakens the outer bound",
			source: `
def helper(a)
  a
end

def takes_string(value: string)
  value
end

def f(rows: array<array<int>>)
  child = [1]
  rows << child
  helper(child)
  for row in rows
    for v in row
      takes_string(v)
    end
  end
end
`,
		},
		{
			name: "block in the assigned value can rebind the receiver",
			source: `
def helper -> string
  yield
  "s"
end

def f(items: array<int>)
  items[0] = helper() do
    items = ["s"]
    "s"
  end
end
`,
		},
		{
			name: "unknown shovel value that escapes the receiver weakens",
			source: `
def unknown_fn(a)
  a
end

def f(items: array<int>)
  items << unknown_fn(items)
  items << "bad"
end
`,
		},
		{
			name: "unknown splat weakens the bound",
			source: `
def f(items: array<int>, v)
  items.push(*v)
  items << "bad"
end
`,
		},
		{
			name: "possibly empty typed splat stays silent and weakens",
			source: `
def f(items: array<int>, more: array<string>)
  items.push(*more)
  items << "bad"
end
`,
		},
		{
			name: "splatted insert index stays gradual",
			source: `
def f(items: array<int>, v)
  items.insert(*v, "bad")
end
`,
		},
		{
			name: "insert with an invalid index never writes",
			source: `
def f(items: array<int>)
  items.insert("x", "bad")
end
`,
		},
		{
			name: "shape element with an unknown field value stays silent",
			source: `
def f(items: array<{ amount: int }>, v)
  items << { amount: v }
end
`,
		},
		{
			name: "unknown loop append weakens the fact after the loop",
			source: `
def f(items: array<int>, v, flag)
  while flag
    items << v
  end
  items << "bad"
end
`,
		},
		{
			name: "mutator call inside a loop stays conservative",
			source: `
def f(items: array<int>, flag)
  while flag
    items.push("bad")
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, tc.source))
		})
	}
}

func TestCheckArrayWriteContradictionKeepsWitness(t *testing.T) {
	t.Parallel()

	// The reported append really lands, so the written value is a witnessed
	// element afterwards: the corrupted array still satisfies a boundary the
	// witness does not contradict, and the write site stays the only report.
	script := compileScriptDefault(t, `
def strings(values: array<string>)
  values
end

def f(items: array<int>)
  items << "bad"
  strings(items)
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarnings() = %#v, want only the write contradiction", warnings)
	}
}
