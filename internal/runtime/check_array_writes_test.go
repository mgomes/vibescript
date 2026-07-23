package runtime

import (
	"context"
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
			name: "fill value",
			source: `
def f(items: array<int>)
  items.fill("bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value after the receiver escapes as an argument",
			source: `
def returns_string(items) -> string
  "bad"
end

def f(items: array<int>)
  items.fill(returns_string(items))
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill literal block result",
			source: `
def f(items: array<int>)
  items.fill() do
    "bad"
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill literal block next result",
			source: `
def f(items: array<int>)
  items.fill() do
    next "bad"
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill next result survives falling through ensure",
			source: `
def f(items: array<int>)
  items.fill() do
    begin
      next "bad"
    ensure
      1
    end
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill next result survives conditionally raising ensure",
			source: `
def f(items: array<int>, stop)
  items.fill() do
    begin
      next "bad"
    ensure
      if stop
        raise "stop"
      end
    end
  end
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "raising ensure leaves fill receiver bound intact",
			source: `
def f(items: array<int>)
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        raise "stop"
      end
    end
  rescue
    nil
  end
  items << true
end
`,
			warning: "write to items expected element int, got bool",
		},
		{
			name: "fill exact proc block result",
			source: `
def f(items: array<int>)
  callback = proc do |index|
    "bad"
  end
  items.fill(&callback)
  items
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible fill literal block result preserves the bound",
			source: `
def f(items: array<int>)
  items.fill() do
    1
  end
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible exact proc fill result preserves the bound",
			source: `
def f(items: array<int>)
  callback = proc do |index|
    1
  end
  items.fill(&callback)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "exact proc fill uses compatible invocation-time capture",
			source: `
def f(items: array<int>)
  value = "stale"
  callback = proc do |index|
    value
  end
  value = 1
  items.fill(&callback)
  items << true
end
`,
			warning: "write to items expected element int, got bool",
		},
		{
			name: "exact proc fill uses incompatible invocation-time capture",
			source: `
def f(items: array<int>)
  value = 1
  callback = proc do |index|
    value
  end
  value = "bad"
  items.fill(&callback)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "nested loop next is not the fill block result",
			source: `
def f(items: array<int>, repeat)
  items.fill() do
    while repeat
      next "not a fill value"
    end
    1
  end
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "nil block argument keeps the value form",
			source: `
def f(items: array<int>)
  items.fill("bad", &nil)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible fill preserves the bound",
			source: `
def f(items: array<int>)
  items.fill(1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with start",
			source: `
def f(items: array<int>)
  items.fill("bad", 0)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "minimum int literal remains a valid fill start",
			source: `
def f(items: array<int>)
  items.fill("bad", -9223372036854775808)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with start and length",
			source: `
def f(items: array<int>)
  items.fill("bad", 0, 1)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with exact range",
			source: `
def f(items: array<int>)
  items.fill("bad", 0..1)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with exact local range",
			source: `
def f(items: array<int>)
  window = 0..1
  items.fill("bad", window)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with exact beginless range",
			source: `
def f(items: array<int>)
  items.fill("bad", ..1)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with exact float range",
			source: `
def f(items: array<int>)
  items.fill("bad", 0.0..1.9)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "fill value with exact negative end range",
			source: `
def f(items: array<int>)
  items.fill("bad", 0..-1)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible exact beginless range preserves the bound",
			source: `
def f(items: array<int>)
  items.fill(1, ..1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible exact float range preserves the bound",
			source: `
def f(items: array<int>)
  items.fill(1, 0.0..1.9)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible exact negative end range preserves the bound",
			source: `
def f(items: array<int>)
  items.fill(1, 0..-1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "nullable element bound admits fill padding",
			source: `
def f(items: array<int?>)
  items.fill(1, 5, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int?, got string",
		},
		{
			name: "nullable element bound admits padding only fill",
			source: `
def f(items: array<int?>)
  items.fill("unused", 5, 0)
  items << "bad"
end
`,
			warning: "write to items expected element int?, got string",
		},
		{
			name: "nullable element bound admits range fill padding",
			source: `
def f(items: array<int?>)
  items.fill(1, 2..2)
  items << "bad"
end
`,
			warning: "write to items expected element int?, got string",
		},
		{
			name: "fill uses selector captured before a later argument rebind",
			source: `
def later() -> int
  yield
  1
end

def f(items: array<int>)
  start = 0
  items.fill("bad", start, later() do
    start = "invalid"
  end)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible non-padding fill selectors preserve the bound",
			source: `
def f(items: array<int>)
  items.fill(1, 2)
  items.fill(2, 0, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "compatible nil fill length preserves the bound",
			source: `
def f(items: array<int>)
  items.fill(1, 2, nil)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "exact splat supplies the fill value",
			source: `
def f(items: array<int>)
  items.fill(*["bad"])
end
`,
			warning: "write to items expected element int, got string",
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
			name: "empty literal splat preserves the bound",
			source: `
def f(items: array<int>)
  items.push(*[])
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "empty local splat with compatible argument preserves the bound",
			source: `
def f(items: array<int>)
  args = []
  items.push(*args, 1)
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
			name: "zero index insert preserves the bound",
			source: `
def f(items: array<int>)
  items.insert(0, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "negative index insert preserves the bound",
			source: `
def f(items: array<int>)
  items.insert(-1, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "exact local insert index preserves the bound",
			source: `
def f(items: array<int>)
  index = 0
  items.insert(index, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "non-padding index alternatives preserve the bound",
			source: `
def f(items: array<int>, flag: bool)
  index = flag ? 0 : -1
  items.insert(index, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "nullable element bound admits insert padding",
			source: `
def f(items: array<int?>)
  items.insert(5, 1)
  items << "bad"
end
`,
			warning: "write to items expected element int?, got string",
		},
		{
			name: "insert uses index captured before a later argument rebind",
			source: `
def later() -> int
  yield
  1
end

def f(items: array<int>)
  index = 0
  items.insert(index, later() do
    index = 5
  end)
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "non-padding splatted insert preserves the bound",
			source: `
def f(items: array<int>)
  items.insert(*[0, 1])
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "literal splat supplies the insert index",
			source: `
def f(items: array<int>)
  items.insert(*[0], "bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "literal splat supplies the complete insert call",
			source: `
def f(items: array<int>)
  items.insert(*[0, "bad"])
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "static local splat supplies the insert index",
			source: `
def f(items: array<int>)
  args = [0]
  items.insert(*args, "bad")
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "later argument rebind keeps the evaluated splat index",
			source: `
def later() -> string
  yield
  "bad"
end

def f(items: array<int>)
  args = [0]
  items.insert(*args, later() do
    args = ["x"]
  end)
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "index only literal splat preserves the bound",
			source: `
def f(items: array<int>)
  items.insert(*[0])
  items << "bad"
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
			name: "scalar local inside an array literal does not alias its root",
			source: `
def f(rows: array<array<int>>, child: array<int>, count: int, v)
  rows << [count]
  child << v
  rows << "bad"
end
`,
			warning: "write to rows expected element array<int>, got string",
		},
		{
			name: "container condition inside an array literal does not alias its root",
			source: `
def f(rows: array<array<int>>, flag: array<int>, v)
  rows << (flag ? [1] : [2])
  flag << v
  rows << "bad"
end
`,
			warning: "write to rows expected element array<int>, got string",
		},
		{
			name: "omitted optional shape field preserves the bound",
			source: `
def f(items: array<{ name: string, age?: int }>)
  items << { name: "Ada" }
  items << 1
end
`,
			warning: "write to items expected element { age?: int, name: string }, got int",
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
			name: "inert lambda value still checks the write",
			source: `
def f(items: array<int>)
  items[0] = ->() {
    items = ["s"]
    1
  }
end
`,
			warning: "write to items expected element int, got function",
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
			name: "harmless value block keeps the declared bound",
			source: `
def helper -> int
  yield
  1
end

def f(items: array<int>)
  items[0] = helper() do
    1
  end
  items << "bad"
end
`,
			warning: "write to items expected element int, got string",
		},
		{
			name: "rescue binding in the value block does not rebind the receiver",
			source: `
def helper -> int
  yield
  1
end

def f(items: array<int>)
  items[0] = helper() do
    begin
      raise "stop"
    rescue => items
      items
    end
    1
  end
  items << "bad"
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
			name: "unknown fill value stays silent and weakens",
			source: `
def f(items: array<int>, v)
  items.fill(v)
  items << "bad"
end
`,
		},
		{
			name: "dynamic fill selector stays gradual",
			source: `
def f(items: array<int>, start)
  items.fill("bad", start)
  items << "bad"
end
`,
		},
		{
			name: "dynamic fill block argument stays gradual",
			source: `
def f(items: array<int>, callback: function)
  items.fill(&callback)
end
`,
		},
		{
			name: "raising ensure suppresses fill next result",
			source: `
def f(items: array<int>)
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        raise "stop"
      end
    end
  rescue
    nil
  end
end
`,
		},
		{
			name: "exact raising ensure condition suppresses fill next result",
			source: `
def f(items: array<int>)
  stop = true
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        if stop
          raise "stop"
        end
      end
    end
  rescue
    nil
  end
end
`,
		},
		{
			name: "selector mutation of an evaluated fill value weakens the receiver",
			source: `
def selector(value, extra) -> int
  value << extra
  0
end

def f(items: array<array<int>>, value: array<int>, extra)
  items.fill(value, selector(value, extra))
  items << "bad"
end
`,
		},
		{
			name: "literal bignum fill start aborts before writes",
			source: `
def f(items: array<int>)
  items.fill("bad", 9223372036854775808)
end
`,
		},
		{
			name: "literal bignum fill length aborts before writes",
			source: `
def f(items: array<int>)
  items.fill("bad", 0, 9223372036854775808)
end
`,
		},
		{
			name: "negative literal bignum fill selector aborts before writes",
			source: `
def f(items: array<int>)
  items.fill("bad", -9223372036854775809)
end
`,
		},
		{
			name: "padding only fill window weakens without a value warning",
			source: `
def f(items: array<int>)
  items.fill("bad", 5, 0)
  items << "bad"
end
`,
		},
		{
			name: "compatible fill window that may pad weakens",
			source: `
def f(items: array<int>)
  items.fill(1, 5, 1)
  items << "bad"
end
`,
		},
		{
			name: "compatible fill range that may pad weakens",
			source: `
def f(items: array<int>)
  items.fill(1, 2..2)
  items << "bad"
end
`,
		},
		{
			name: "invalid literal splatted insert index never writes",
			source: `
def f(items: array<int>)
  items.insert(*["x"], "bad")
end
`,
		},
		{
			name: "later argument rebind cannot repair an evaluated invalid splat index",
			source: `
def later() -> string
  yield
  "bad"
end

def f(items: array<int>)
  args = ["x"]
  items.insert(*args, later() do
    args = [0]
  end)
end
`,
		},
		{
			name: "later argument rebind cannot change an evaluated empty splat",
			source: `
def later() -> int
  yield
  2
end

def f(items: array<int>)
  args = []
  items.push(*args, later() do
    args = ["bad"]
  end)
end
`,
		},
		{
			name: "empty literal splatted insert aborts",
			source: `
def f(items: array<int>)
  items.insert(*[])
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
			name: "positive insert that may pad weakens the bound",
			source: `
def f(items: array<int>)
  items.insert(5, 1)
  items << "bad"
end
`,
		},
		{
			name: "exact positive local insert may pad and weakens",
			source: `
def f(items: array<int>)
  index = 5
  items.insert(index, 1)
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
			name: "mutating a child retained inside an array literal weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<array<int>>>, v)
  child = [1]
  rows << [child]
  child << v
  for group in rows
    for row in group
      for value in row
        takes_string(value)
      end
    end
  end
end
`,
		},
		{
			name: "mutating a child retained by an assigned literal weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<array<int>>>, v)
  child = [1]
  box = [child]
  rows << box
  child << v
  for group in rows
    for row in group
      for value in row
        takes_string(value)
      end
    end
  end
end
`,
		},
		{
			name: "mutating a conditional child retained inside an array literal weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<array<int>>>, child: array<int>, choose, v)
  rows << [choose ? child : [1]]
  child << v
  for group in rows
    for row in group
      for value in row
        takes_string(value)
      end
    end
  end
end
`,
		},
		{
			name: "mutating a child retained inside a hash literal weakens the outer bound",
			source: `
def takes_string(value: string)
  value
end

def f(rows: array<array<{ child: array<int> }>>, v)
  child = [1]
  rows << [{ child: child }]
  child << v
  for group in rows
    for entry in group
      for value in entry[:child]
        takes_string(value)
      end
    end
  end
end
`,
		},
		{
			name: "container call retained inside an array literal weakens the outer bound",
			source: `
def retain(value: array<int>) -> array<int>
  value
end

def takes_string(value: string)
  value
end

def f(rows: array<array<array<int>>>, child: array<int>, v)
  rows << [retain(child)]
  child << v
  for group in rows
    for row in group
      for value in row
        takes_string(value)
      end
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
			name: "lambda passed to a call in the value can rebind the receiver",
			source: `
def helper(cb) -> string
  "s"
end

def f(items: array<int>)
  items[0] = helper(->() {
    items = ["s"]
    0
  })
end
`,
		},
		{
			name: "nested called lambda in the value block can rebind the receiver",
			source: `
def helper -> string
  yield
  "bad"
end

def f(items: array<int>)
  items[0] = helper() do
    (->() {
      items = ["s"]
    }).call()
  end
end
`,
		},
		{
			name: "unknown shovel value that escapes the receiver weakens",
			source: `
def f(items: array<int>, producer)
  items << producer.value(items)
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
			name: "provably non-array splat aborts the call",
			source: `
def f(items: array<int>)
  items.push(*1, "bad")
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
			name: "out of bounds exact index write stops later diagnostics",
			source: `
def run()
  names = [:first]
  names[2] = :third
  names << 1
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
			name: "optional written field weakens a required shape bound",
			source: `
def f(items: array<{ amount: int }>, raw: string)
  item = JSON.parse_as(raw, { amount?: int })
  items << item
  items << "bad"
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

func TestArrayMutatorSplatUsesEvaluationTimeValues(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def later()
  yield
  2
end

def run()
  args = []
  items = [1]
  items.push(*args, later() do
    args = ["bad"]
  end)
  [items, args]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindArray {
		t.Fatalf("run() kind = %v, want array", got.Kind())
	}
	result := got.Array()
	if len(result) != 2 {
		t.Fatalf("run() length = %d, want 2", len(result))
	}
	wantItems := NewArray([]Value{NewInt(1), NewInt(2)})
	if !result[0].Equal(wantItems) {
		t.Fatalf("run() items = %s, want %s", result[0].String(), wantItems.String())
	}
	wantArgs := NewArray([]Value{NewString("bad")})
	if !result[1].Equal(wantArgs) {
		t.Fatalf("run() args = %s, want %s", result[1].String(), wantArgs.String())
	}
}

func TestCheckArrayMutatorRetainedAliasesUseEvaluationTimeBindings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		source   string
		wantLine int
	}{
		{
			name: "push splat",
			source: `def later() -> int
  yield
  3
end

def f(rows: array<array<int> | int>)
  args = [[1]]
  rows.push(*args, later() do
    args = [[2]]
  end)
  args[0] << "new"
  rows << "bad"
end
`,
			wantLine: 12,
		},
		{
			name: "fill value",
			source: `def selector() -> int
  yield
  0
end

def f(rows: array<array<int>>)
  value = [1]
  rows.fill(value, selector() do
    value = [2]
  end)
  value << "new"
  rows << "bad"
end
`,
			wantLine: 12,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warnings := compileScriptDefault(t, tc.source).CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one incompatible write warning", warnings)
			}
			if warnings[0].Pos.Line != tc.wantLine ||
				!strings.Contains(warnings[0].Message, "write to rows expected element") ||
				!strings.Contains(warnings[0].Message, "got string") {
				t.Fatalf(
					"CheckWarnings() = %#v, want incompatible write warning on line %d",
					warnings,
					tc.wantLine,
				)
			}
		})
	}
}

func TestArrayMutatorRetainedAliasesUseEvaluationTimeBindings(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def push_later() -> int
  yield
  3
end

def fill_selector() -> int
  yield
  0
end

def run()
  push_rows = [[0]]
  push_args = [[1]]
  push_rows.push(*push_args, push_later() do
    push_args = [[2]]
  end)
  push_args[0] << "new"

  fill_rows = [[0]]
  fill_value = [1]
  fill_rows.fill(fill_value, fill_selector() do
    fill_value = [2]
  end)
  fill_value << "new"

  [push_rows, push_args, fill_rows, fill_value]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{
			NewArray([]Value{NewInt(0)}),
			NewArray([]Value{NewInt(1)}),
			NewInt(3),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(2), NewString("new")}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1)}),
		}),
		NewArray([]Value{NewInt(2), NewString("new")}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayMutatorExactSplatFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		source   string
		wantLine int
	}{
		{
			name: "diagnostic uses splat expression position",
			source: `def f(items: array<int>)
  args = ["bad"]
  items.push(*args)
end
`,
			wantLine: 3,
		},
		{
			name: "fill selectors keep their evaluation facts",
			source: `def zero
  0
end

def f(items: array<int>)
  args = ["bad", zero(), 1]
  items.fill(*args)
end
`,
			wantLine: 7,
		},
		{
			name: "direct fill selectors keep their evaluation facts",
			source: `def zero
  0
end

def f(items: array<int>)
  items.fill("bad", zero(), 1)
end
`,
			wantLine: 6,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warnings := compileScriptDefault(t, tc.source).CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one incompatible write warning", warnings)
			}
			if warnings[0].Pos.Line != tc.wantLine ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf(
					"CheckWarnings() = %#v, want incompatible write warning on line %d",
					warnings,
					tc.wantLine,
				)
			}
		})
	}
}

func TestArrayMutatorRepeatedSplatsKeepDiagnosticOrigins(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run(items: array<int>)
  args = ["bad"]
  items.push(
    *args,
    *args
  )
end
`)

	warnings := script.CheckWarningsForFunction("run")
	wantLines := []int{4, 5}
	if len(warnings) != len(wantLines) {
		t.Fatalf("CheckWarningsForFunction(run) = %#v, want two incompatible write warnings", warnings)
	}
	for i, warning := range warnings {
		if warning.Pos.Line != wantLines[i] ||
			!strings.Contains(warning.Message, "write to items expected element int, got string") {
			t.Errorf(
				"CheckWarningsForFunction(run)[%d] = %#v, want incompatible write warning on line %d",
				i,
				warning,
				wantLines[i],
			)
		}
	}

	got := callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewArray([]Value{NewInt(1)})},
		CallOptions{},
	)
	want := NewArray([]Value{NewInt(1), NewString("bad"), NewString("bad")})
	if !got.Equal(want) {
		t.Fatalf("run([1]) = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayMutatorExactSplatsKeepRetainedElementProvenance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call string
	}{
		{name: "push", call: "rows.push(*args)"},
		{name: "append", call: "rows.append(*args)"},
		{name: "prepend", call: "rows.prepend(*args)"},
		{name: "unshift", call: "rows.unshift(*args)"},
		{name: "fill", call: "rows.fill(*args)"},
		{name: "non-padding insert", call: "rows.insert(0, *args)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			requireNoCheckWarnings(t, compileScriptDefault(t, `
def takes_string(value: string)
  value
end

def f(rows: array<array<int>>, value)
  args = [[1]]
  `+tc.call+`
  args[0] << value
  for row in rows
    for item in row
      takes_string(item)
    end
  end
end
`))
		})
	}
}

func TestArrayMutatorExactSplatsRetainNestedAliases(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run()
  push_rows = [[0]]
  push_args = [[1]]
  push_rows.push(*push_args)
  push_args[0] << 2

  append_rows = [[0]]
  append_args = [[1]]
  append_rows.append(*append_args)
  append_args[0] << 2

  prepend_rows = [[0]]
  prepend_args = [[1]]
  prepend_rows.prepend(*prepend_args)
  prepend_args[0] << 2

  unshift_rows = [[0]]
  unshift_args = [[1]]
  unshift_rows.unshift(*unshift_args)
  unshift_args[0] << 2

  fill_rows = [[0]]
  fill_args = [[1]]
  fill_rows.fill(*fill_args)
  fill_args[0] << 2

  insert_rows = [[0]]
  insert_args = [[1]]
  insert_rows.insert(0, *insert_args)
  insert_args[0] << 2

  [push_rows, append_rows, prepend_rows, unshift_rows, fill_rows, insert_rows]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{
			NewArray([]Value{NewInt(0)}),
			NewArray([]Value{NewInt(1), NewInt(2)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(0)}),
			NewArray([]Value{NewInt(1), NewInt(2)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1), NewInt(2)}),
			NewArray([]Value{NewInt(0)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1), NewInt(2)}),
			NewArray([]Value{NewInt(0)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1), NewInt(2)}),
		}),
		NewArray([]Value{
			NewArray([]Value{NewInt(1), NewInt(2)}),
			NewArray([]Value{NewInt(0)}),
		}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestArrayInsertPaddingBoundaryMatchesRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def later()
  yield
  1
end

def insert_at(flag: bool)
  items = []
  index = flag ? 0 : -1
  items.insert(index, 1)
  items
end

def run()
  zero = []
  zero.insert(0, 1)
  negative = []
  negative.insert(-1, 1)
  positive = []
  positive.insert(1, 1)
  index = 0
  captured = []
  captured.insert(index, later() do
    index = 5
  end)
  [zero, negative, positive, captured, index, insert_at(true), insert_at(false)]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewNil(), NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
		NewInt(5),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayInsertStoredSplatSelectorAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  args = [flag ? 0 : -1]
  items.insert(*args, 1)
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 5", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "zero",
			flag: true,
			want: NewArray([]Value{NewInt(1), NewInt(0), NewString("bad")}),
		},
		{
			name: "negative",
			flag: false,
			want: NewArray([]Value{NewInt(0), NewInt(1), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(0)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([0], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

func TestArrayFillMutationMatchesCheckerModel(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run()
  items = [1, 2]
  alias_items = items
  returned = items.fill("bad")
  returned << "tail"
  empty = []
  empty.fill("bad")
  [items, alias_items, returned, empty]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	filled := NewArray([]Value{NewString("bad"), NewString("bad"), NewString("tail")})
	want := NewArray([]Value{
		filled,
		filled,
		filled,
		NewArray([]Value{}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestCheckArrayFillSelectorAlternativesMatchRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run(items: array<int>, flag: bool)
  start = flag ? 0 : -1
  items.fill(1, start, 1)
  items << "bad"
  items
end
`)

	warnings := script.CheckWarningsForFunction("run")
	if len(warnings) != 1 ||
		warnings[0].Pos.Line != 5 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 5", "run", warnings)
	}

	cases := []struct {
		name string
		flag bool
		want Value
	}{
		{
			name: "zero",
			flag: true,
			want: NewArray([]Value{NewInt(1), NewInt(0), NewString("bad")}),
		},
		{
			name: "negative",
			flag: false,
			want: NewArray([]Value{NewInt(0), NewInt(1), NewString("bad")}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(0), NewInt(0)}),
				NewBool(tc.flag),
			}, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("run([0, 0], %t) = %s, want %s", tc.flag, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayFillInvalidSelectorsPreserveReceiverThroughRescue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		call string
	}{
		{name: "non numeric start", call: `items.fill("unused", "bad")`},
		{name: "bignum start", call: `items.fill("unused", 9223372036854775808)`},
		{name: "bignum length", call: `items.fill("unused", 0, 9223372036854775808)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def run(items: array<int>)
  begin
    `+tc.call+`
  rescue
    nil
  end
  items << "bad"
  items
end
`)

			warnings := script.CheckWarningsForFunction("run")
			if len(warnings) != 1 ||
				warnings[0].Pos.Line != 8 ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want the incompatible append on line 8", "run", warnings)
			}

			got := callScript(t, context.Background(), script, "run", []Value{
				NewArray([]Value{NewInt(1)}),
			}, CallOptions{})
			want := NewArray([]Value{NewInt(1), NewString("bad")})
			if !got.Equal(want) {
				t.Errorf("run([1]) = %s, want %s", got.String(), want.String())
			}
		})
	}
}

func TestArrayFillSelectorSafetyBoundaryMatchesRuntime(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run()
  bare_start = []
  bare_start.fill(1, 2)
  safe_window = []
  safe_window.fill(1, 0, 1)
  negative_length = [1]
  negative_length.fill("bad", 0, -1)
  padding_only = []
  padding_only.fill(1, 2, 0)
  range_window = []
  range_window.fill(1, 0..1)
  range_padding = []
  range_padding.fill(1, 2..2)
  range_empty = []
  range_empty.fill(1, 0...0)
  beginless = [1, 2, 3]
  beginless.fill(4, ..1)
  float_range = [1, 2, 3]
  float_range.fill(5, 0.0..1.9)
  negative_end = [1, 2, 3]
  negative_end.fill(6, 0..-1)
  padding = [1]
  padding.fill(7, 3, 1)
  [
    bare_start,
    safe_window,
    negative_length,
    padding_only,
    range_window,
    range_padding,
    range_empty,
    beginless,
    float_range,
    negative_end,
    padding,
  ]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	want := NewArray([]Value{
		NewArray([]Value{}),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewInt(1)}),
		NewArray([]Value{NewNil(), NewNil()}),
		NewArray([]Value{NewInt(1), NewInt(1)}),
		NewArray([]Value{NewNil(), NewNil(), NewInt(1)}),
		NewArray([]Value{}),
		NewArray([]Value{NewInt(4), NewInt(4), NewInt(3)}),
		NewArray([]Value{NewInt(5), NewInt(5), NewInt(3)}),
		NewArray([]Value{NewInt(6), NewInt(6), NewInt(6)}),
		NewArray([]Value{NewInt(1), NewNil(), NewNil(), NewInt(7)}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestArrayFillValueAndBlockEvaluationMatchesCheckerModel(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def selector(value, extra) -> int
  value << extra
  0
end

def run()
  value = [1]
  selected = [[0]]
  selected.fill(value, selector(value, "bad"))
  blocked = [1, 2]
  blocked.fill() do
    "bad"
  end
  next_blocked = [1, 2]
  next_blocked.fill() do
    next "bad"
  end
  callback = proc do |index|
    "bad"
  end
  proc_blocked = [1, 2]
  proc_blocked.fill(&callback)
  nil_block = [1, 2]
  nil_block.fill("bad", &nil)
  [selected, value, blocked, next_blocked, proc_blocked, nil_block]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	mutatedValue := NewArray([]Value{NewInt(1), NewString("bad")})
	want := NewArray([]Value{
		NewArray([]Value{mutatedValue}),
		mutatedValue,
		NewArray([]Value{NewString("bad"), NewString("bad")}),
		NewArray([]Value{NewString("bad"), NewString("bad")}),
		NewArray([]Value{NewString("bad"), NewString("bad")}),
		NewArray([]Value{NewString("bad"), NewString("bad")}),
	})
	if !got.Equal(want) {
		t.Fatalf("run() = %s, want %s", got.String(), want.String())
	}
}

func TestArrayFillNextResultRespectsEnsureCompletion(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def always_raise()
  items = [1, 2]
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        raise "stop"
      end
    end
  rescue
    nil
  end
  items
end

def conditionally_raise(stop: bool)
  items = [1, 2]
  begin
    items.fill() do
      begin
        next "bad"
      ensure
        if stop
          raise "stop"
        end
      end
    end
  rescue
    nil
  end
  items
end
`)

	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{
			name: "raising ensure",
			fn:   "always_raise",
			want: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name: "conditional ensure raises",
			fn:   "conditionally_raise",
			args: []Value{NewBool(true)},
			want: NewArray([]Value{NewInt(1), NewInt(2)}),
		},
		{
			name: "conditional ensure falls through",
			fn:   "conditionally_raise",
			args: []Value{NewBool(false)},
			want: NewArray([]Value{NewString("bad"), NewString("bad")}),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := callScript(t, context.Background(), script, tc.fn, tc.args, CallOptions{})
			if !got.Equal(tc.want) {
				t.Errorf("%s(%v) = %s, want %s", tc.fn, tc.args, got.String(), tc.want.String())
			}
		})
	}
}

func TestCheckArrayFillNoOpSelectorsPreserveBound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		fillCall string
	}{
		{name: "negative length", fillCall: `items.fill("bad", 0, -1)`},
		{name: "zero length", fillCall: `items.fill("bad", 0, 0)`},
		{name: "empty range", fillCall: `items.fill("bad", 0...0)`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def f(items: array<int>)
  `+tc.fillCall+`
  items << "later"
end
`)

			warnings := script.CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one later write warning", warnings)
			}
			if warnings[0].Pos.Line != 4 ||
				!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
				t.Fatalf("CheckWarnings() = %#v, want the warning on the later write", warnings)
			}
		})
	}
}

func TestCheckArrayFillNegativeLengthPrecedesUnknownStart(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def f(items: array<int>, start)
  items.fill("unused", start, -1)
  items << "later"
end
`)

	warnings := script.CheckWarnings()
	if len(warnings) != 1 {
		t.Fatalf("CheckWarnings() = %#v, want one later write warning", warnings)
	}
	if warnings[0].Pos.Line != 4 ||
		!strings.Contains(warnings[0].Message, "write to items expected element int, got string") {
		t.Fatalf("CheckWarnings() = %#v, want the warning on the later write", warnings)
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

func TestCheckArrayWritesKeepForwardedValuesAndDeclaredBoundsConsistent(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes_int(value: int)
  value
end

class UpdatedReceiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

class DynamicReceiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

class Strict
  def check(value)
    takes_int(value)
  end
end

class Producer
  def consume(values)
    values[0] = :third
  end
end

def exact_index(names: array<symbol>)
  names[0] = :third
  UpdatedReceiver.new.send(*names, "bad")
end

def exact_alias(names: array<symbol>)
  copy = names
  names[0] = :third
  UpdatedReceiver.new.send(*copy, "bad")
end

def retain_index_bound(names: array<symbol>)
  names[0] = :third
  names << 1
end

def retain_alias_bound(names: array<symbol>)
  copy = names
  names[0] = :third
  copy << 1
end

def rebound_alias(names: array<symbol>)
  copy = names
  copy = Strict
  names << :third
  copy.new.check("bad")
end

def dynamic_index(names: array<symbol>, index: int)
  names[index] = :third
  DynamicReceiver.new.send(*names, "bad")
end

def short_circuit_index(names: array<symbol>)
  names[0] ||= :third
  UpdatedReceiver.new.send(*names, "bad")
end

def retain_dynamic_index_bound(names: array<symbol>, index: int)
  names[index] = :third
  names << 1
end

def prepend_name(names: array<symbol>)
  names.prepend(:third)
  DynamicReceiver.new.send(*names, "bad")
end

def escaped_shovel(names: array<symbol>)
  Producer.new.consume(names << :extra)
  DynamicReceiver.new.send(*names, "bad")
end

def mutate_name_in_loop(names: array<symbol>, flag: bool)
  while flag
    names.prepend(:third)
    break
  end
  DynamicReceiver.new.send(*names, "bad")
end

def shovel_name_in_loop(names: array<symbol>, flag: bool)
  while flag
    names << :third
    break
  end
  DynamicReceiver.new.send(*names, "bad")
end

def retain_shovel_loop_bound(names: array<symbol>, flag: bool)
  while flag
    names << :third
    break
  end
  names << 1
end

def run(index: int, flag: bool)
  exact_index([:first])
  exact_alias([:first])
  retain_index_bound([:first])
  retain_alias_bound([:first])
  dynamic_index([:first], index)
  short_circuit_index([:first])
  retain_dynamic_index_bound([:first], index)
  prepend_name([:first])
  escaped_shovel([:first])
  mutate_name_in_loop([:first], flag)
  shovel_name_in_loop([:first], flag)
  retain_shovel_loop_bound([:first], flag)
  rebound_alias([:first])
end`)

	gotWarnings := script.CheckWarningsForFunction("run")
	warnings := strings.Join(checkWarningMessages(gotWarnings), "\n")
	if got := strings.Count(warnings, "call to takes_int argument value expected int, got string"); got != 3 {
		t.Fatalf("forwarded diagnostics = %d in %#v, want 3 exact targets", got, gotWarnings)
	}
	if got := strings.Count(warnings, "write to names expected element symbol, got int"); got != 3 {
		t.Fatalf("receiver write diagnostics = %d in %q, want 3 retained bounds", got, warnings)
	}
	if got := strings.Count(warnings, "write to copy expected element symbol, got int"); got != 1 {
		t.Fatalf("alias write diagnostics = %d in %q, want 1 retained alias bound", got, warnings)
	}
}

func TestCheckArrayWritesInvalidateOnlyDependentForwardedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		want         string
		wantFunction string
	}{
		{
			name: "retained child stays exact when its parent changes",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def third(value)
    takes_int(value)
  end
end

def retained_child(items: array<array<symbol>>, child: array<symbol>)
  items[0] = child
  Receiver.new.send(*child, "bad")
end

def run()
  retained_child([[:first]], [:third])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "nested parent write updates a projected child alias",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def nested_write(items: array<array<symbol>>)
  child = items[0]
  items[0][0] = :third
  Receiver.new.send(*child, "bad")
end

def run()
  nested_write([[:first]])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "replacing a parent element preserves the detached child",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def replace_parent(items: array<array<symbol>>)
  child = items[0]
  items[0] = [:third]
  Receiver.new.send(*child, "bad")
end

def run()
  replace_parent([[:first]])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#first",
		},
		{
			name: "destructured aliases share exact mutations",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def destructured(names: array<symbol>)
  copy, ignored = [names, 0]
  names[0] = :third
  Receiver.new.send(*copy, "bad")
end

def run()
  destructured([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "destructuring captures every value before rebinding targets",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def simultaneous(names: array<symbol>)
  names, copy = [[:third], names]
  Receiver.new.send(*copy, "bad")
end

def run()
  simultaneous([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#first",
		},
		{
			name: "duplicate destructure targets keep the last value",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def duplicate_target()
  names, names = [[:first], [:third]]
  Receiver.new.send(*names, "bad")
end

def run()
  duplicate_target()
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "shared call arguments keep alias identity",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def mutate_first(a: array<symbol>, b: array<symbol>)
  a[0] = :third
  Receiver.new.send(*b, "bad")
end

def run()
  names = [:first]
  mutate_first(names, names)
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "logical assignment keeps selected alias identity",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def logical_alias(names: array<symbol>)
  copy = nil
  copy ||= names
  names[0] = :third
  Receiver.new.send(*copy, "bad")
end

def run()
  logical_alias([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#third",
		},
		{
			name: "no-op insert keeps the exact forwarded name",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end
end

def no_op_mutator(names)
  names.push()
  names.insert(0)
  Receiver.new.send(*names, "bad")
end

def run()
  no_op_mutator([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Receiver#first",
		},
		{
			name: "rebound alias keeps its unrelated class identity",
			source: `def takes_int(value: int)
  value
end

class Strict
  def check(value)
    takes_int(value)
  end
end

def rebound_alias(names: array<symbol>)
  copy = names
  copy = Strict
  names << :third
  copy.new.check("bad")
end

def run()
  rebound_alias([:first])
end`,
			want:         "call to takes_int argument value expected int, got string",
			wantFunction: "Strict#check",
		},
		{
			name: "escaped shovel does not retain a stale forwarded name",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

class Producer
  def consume(values)
    values[0] = :third
  end
end

def escaped_shovel(names: array<symbol>)
  Producer.new.consume(names << :extra)
  Receiver.new.send(*names, "bad")
end

def run()
  escaped_shovel([:first])
end`,
		},
		{
			name: "parenless member mutation clears a shovel receiver value",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def parenless_mutation()
  names = [:first]
  (names << :third).shift
  Receiver.new.send(*names, "bad")
end

def run()
  parenless_mutation()
end`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := compileScript(t, tc.source).CheckWarningsForFunction("run")
			got := strings.Join(checkWarningMessages(warnings), "\n")
			if tc.want == "" {
				if got != "" {
					t.Fatalf("CheckWarningsForFunction(%q) = %q, want none", "run", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, tc.want)
			}
			if len(warnings) != 1 || warnings[0].Function != tc.wantFunction {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want one warning in %q", "run", warnings, tc.wantFunction)
			}
		})
	}
}

func TestCheckArrayWriteDirectAliasTransfersRelations(t *testing.T) {
	t.Parallel()

	checker := &scriptChecker{
		scopes: []map[string]struct{}{{
			"base":   {},
			"child":  {},
			"copy":   {},
			"parent": {},
			"source": {},
		}},
		localTypes: []checkTypeFrame{{
			"base":   checkTypeArray,
			"child":  checkTypeArray,
			"copy":   nil,
			"parent": checkTypeArray,
			"source": checkTypeArray,
		}},
		localClassValues: []checkClassValueFrame{nil},
	}
	checker.linkContainerIdentityAlias("source", "base")
	checker.linkStaticValueAlias("source", "base")
	checker.linkContainerAlias("base", "child")
	checker.linkStaticValueDependency("child", "base")
	checker.linkContainerAlias("base", "parent")
	checker.linkStaticValueDependency("base", "parent")

	transfer := checker.captureContainerAliasTransfer(&Identifier{Name: "source"})
	checker.advanceLocalBindingGeneration("copy")
	checker.bindLocalType("copy", checkTypeArray)
	checker.applyContainerAliasTransfer("copy", transfer)
	checker.advanceLocalBindingGeneration("source")

	tests := []struct {
		name      string
		relations map[string]map[string]checkBindingEdge
		from      string
		to        string
	}{
		{
			name:      "definite identity",
			relations: checker.containerIdentityAliases,
			from:      "copy",
			to:        "base",
		},
		{
			name:      "may alias reachability",
			relations: checker.typeAliases,
			from:      "copy",
			to:        "child",
		},
		{
			name:      "incoming static dependency",
			relations: checker.staticValueDependents,
			from:      "child",
			to:        "copy",
		},
		{
			name:      "outgoing static dependency",
			relations: checker.staticValueDependents,
			from:      "copy",
			to:        "parent",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			edge, exists := tc.relations[tc.from][tc.to]
			if !exists || !checker.bindingEdgeCurrent(tc.from, tc.to, edge) {
				t.Errorf("transferred relation %q -> %q = %#v, %t, want current", tc.from, tc.to, edge, exists)
			}
		})
	}
}

func TestCheckArrayWriteLogicalAssignmentBindingGeneration(t *testing.T) {
	t.Parallel()

	nullableIntArray := &TypeExpr{
		Kind:     TypeArray,
		Nullable: true,
		TypeArgs: []*TypeExpr{checkTypeInt},
	}
	tests := []struct {
		name         string
		operator     TokenType
		current      *TypeExpr
		fact         *logicalAssignmentTargetFact
		wantAdvance  bool
		wantAlias    bool
		wantIdentity bool
		wantStatic   bool
	}{
		{
			name:     "known truthy or assignment preserves identity",
			operator: tokenOrAssign,
			current:  checkTypeArray,
			fact: &logicalAssignmentTargetFact{
				current: checkTypeArray,
				known:   true,
			},
			wantAlias:    true,
			wantIdentity: true,
			wantStatic:   true,
		},
		{
			name:     "known truthy and assignment replaces identity",
			operator: tokenAndAssign,
			current:  checkTypeArray,
			fact: &logicalAssignmentTargetFact{
				current:      checkTypeArray,
				rhsReachable: true,
				known:        true,
			},
			wantAdvance: true,
		},
		{
			name:     "unknown or assignment retains a possible alias",
			operator: tokenOrAssign,
			current:  nullableIntArray,
			fact: &logicalAssignmentTargetFact{
				current:      nullableIntArray,
				rhsReachable: true,
			},
			wantAdvance: true,
			wantAlias:   true,
			wantStatic:  true,
		},
		{
			name:     "unknown and assignment replaces a possible container",
			operator: tokenAndAssign,
			current:  nullableIntArray,
			fact: &logicalAssignmentTargetFact{
				current:      nullableIntArray,
				rhsReachable: true,
			},
			wantAdvance: true,
		},
		{
			name:         "collection fallback preserves known truthy identity",
			operator:     tokenOrAssign,
			current:      checkTypeArray,
			wantAlias:    true,
			wantIdentity: true,
			wantStatic:   true,
		},
		{
			name:        "collection fallback replaces known truthy identity",
			operator:    tokenAndAssign,
			current:     checkTypeArray,
			wantAdvance: true,
		},
		{
			name:        "collection fallback retains an unknown possible alias",
			operator:    tokenOrAssign,
			current:     nullableIntArray,
			wantAdvance: true,
			wantAlias:   true,
			wantStatic:  true,
		},
		{
			name:        "collection fallback and assignment replaces an unknown container",
			operator:    tokenAndAssign,
			current:     nullableIntArray,
			wantAdvance: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checker := &scriptChecker{
				scopes: []map[string]struct{}{{
					"copy":  {},
					"items": {},
				}},
				localTypes: []checkTypeFrame{{
					"copy":  checkTypeArray,
					"items": tc.current,
				}},
				localClassValues: []checkClassValueFrame{nil},
			}
			checker.linkContainerIdentityAlias("items", "copy")
			checker.linkStaticValueAlias("items", "copy")
			stmt := &AssignStmt{
				Target:   &Identifier{Name: "items"},
				Operator: tc.operator,
				Value: &ArrayLiteral{
					Elements: []Expression{&IntegerLiteral{Value: 1}},
				},
			}

			fact := tc.fact
			if fact != nil {
				captured := *fact
				if tc.operator == tokenOrAssign && !captured.known {
					captured.priorAliasTransfer = checker.captureContainerAliasTransfer(stmt.Target)
				}
				fact = &captured
			}
			checker.inferAssignStatementTypes("", stmt, nil, fact)

			if got := checker.localBindingGenerations["items"]; (got != 0) != tc.wantAdvance {
				t.Errorf("binding generation = %d, want advance %t", got, tc.wantAdvance)
			}
			aliasEdge, aliasExists := checker.typeAliases["items"]["copy"]
			aliasCurrent := aliasExists && checker.bindingEdgeCurrent("items", "copy", aliasEdge)
			if aliasCurrent != tc.wantAlias {
				t.Errorf("possible alias current = %t, want %t", aliasCurrent, tc.wantAlias)
			}
			identityEdge, identityExists := checker.containerIdentityAliases["items"]["copy"]
			identityCurrent := identityExists && checker.bindingEdgeCurrent("items", "copy", identityEdge)
			if identityCurrent != tc.wantIdentity {
				t.Errorf("identity alias current = %t, want %t", identityCurrent, tc.wantIdentity)
			}
			forwardEdge, forwardExists := checker.staticValueDependents["items"]["copy"]
			forwardCurrent := forwardExists && checker.bindingEdgeCurrent("items", "copy", forwardEdge)
			reverseEdge, reverseExists := checker.staticValueDependents["copy"]["items"]
			reverseCurrent := reverseExists && checker.bindingEdgeCurrent("copy", "items", reverseEdge)
			if forwardCurrent != tc.wantStatic || reverseCurrent != tc.wantStatic {
				t.Errorf("static dependencies current = (%t, %t), want (%t, %t)",
					forwardCurrent, reverseCurrent, tc.wantStatic, tc.wantStatic)
			}
		})
	}
}

func TestCheckArrayWriteRegressionMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []string
		reject []string
	}{
		{
			name: "closed shape extras preserve an open bound",
			source: `def f(items: array<{ id: int, ... }>)
  items.push({ id: 1, name: "ok" })
  items.push({ id: "bad" })
end

def run()
  f([])
end`,
			want: []string{"write to items expected element { id: int, ... }, got { id: string }"},
		},
		{
			name: "open shape weakens a closed bound",
			source: `def f(items: array<{ id: int }>, raw: string)
  item = JSON.parse_as(raw, { id: int, ... })
  items.push(item)
  items.push({ id: "bad" })
end

def run(raw: string)
  f([], raw)
end`,
		},
		{
			name: "open shape may hide an omitted optional field",
			source: `def f(items: array<{ id: int, tag?: string, ... }>, raw: string)
  item = JSON.parse_as(raw, { id: int, ... })
  items.push(item)
  items.push({ id: "bad" })
end

def run(raw: string)
  f([], raw)
end`,
		},
		{
			name: "matching open shapes preserve the bound",
			source: `def f(items: array<{ id: int, ... }>, raw: string)
  item = JSON.parse_as(raw, { id: int, ... })
  items.push(item)
  items.push({ id: "bad" })
end

def run(raw: string)
  f([], raw)
end`,
			want: []string{"write to items expected element { id: int, ... }, got { id: string }"},
		},
		{
			name: "destructure keeps a known prefix before a missing value",
			source: `def takes_int(value: int)
  value
end

class Strict
  def check(value)
    takes_int(value)
  end
end

def run()
  klass, missing = [Strict]
  klass.new.check("bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure binds a missing value to nil",
			source: `def takes_int(value: int)
  value
end

def run()
  first, missing = [1]
  takes_int(missing)
end`,
			want: []string{"call to takes_int argument value expected int, got nil"},
		},
		{
			name: "destructure rest keeps an evaluated value before rebind",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def run()
  names = [:first]
  *rest, ignored = [names, -> { names = [:third] }.call()]
  Receiver.new.send(*rest[0], "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure snapshots an earlier value before a later rebind",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def run()
  names = [:first]
  copy, ignored = [names, -> { names = [:third] }.call()]
  Receiver.new.send(*copy, "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure advances an earlier alias through a later write",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def run()
  names = []
  copy, ignored = [names, names << :third]
  Receiver.new.send(*copy, "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "array-valued identifier destructure stays gradual",
			source: `def takes_symbol(value: symbol)
  value
end

def run()
  values = [:first, :second]
  first, second = values
  takes_symbol(first)
  takes_symbol(second)
end`,
		},
		{
			name: "destructure checks an indexed leaf write",
			source: `def probe(items: array<int>)
  items[0], ignored = ["bad", 0]
end

def run()
  probe([1])
end`,
			want: []string{"write to items expected element int, got string"},
		},
		{
			name: "destructure applies leaf writes from left to right",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    value
  end

  def third(value)
    takes_int(value)
  end
end

def run()
  names = [:first]
  names[0], copy = [:third, names]
  Receiver.new.send(*copy, "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure rebinds a receiver before a later indexed write",
			source: `def takes_int(value: int)
  value
end

def run()
  values = []
  values, values[0] = [[1], 2]
  takes_int("bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure rebind preserves a captured nonexact bound",
			source: `def probe(names: array<symbol>)
  names, copy = [[:third], names]
  copy << 1
end

def run()
  probe([:first])
end`,
			want: []string{"write to copy expected element symbol, got int"},
		},
		{
			name: "destructure stops after a rebind makes a later index invalid",
			source: `def takes_int(value: int)
  value
end

def run()
  values = [1, 2]
  values, values[1] = [[], 2]
  takes_int("bad")
end`,
		},
		{
			name: "destructure rescue observes an earlier completed leaf",
			source: `def takes_string(value: string)
  value
end

def run()
  value = "old"
  values = []
  begin
    value, values[0] = [1, 2]
  rescue
    takes_string(value)
  end
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "destructure stops at an incompatible typed ivar write",
			source: `def takes_int(value: int)
  value
end

class Counter
  property label: string

  def probe()
    @label, ignored = [1, 0]
    takes_int("bad")
  end
end

def run()
  Counter.new.probe()
end`,
		},
		{
			name: "destructure rescue observes a binding before a failing typed ivar write",
			source: `def takes_string(value: string)
  value
end

class Counter
  property label: string

  def probe()
    value = "old"
    begin
      value, @label = [1, 0]
    rescue
      takes_string(value)
    end
  end
end

def run()
  Counter.new.probe()
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "rescue keeps static poison from a completing body path",
			source: `def crash()
  raise "stop"
end

def maybe(flag: bool, values)
  flag ? values.push("changed") : crash()
end

def takes_string(value: string)
  value
end

def run(flag: bool)
  values = [1]
  begin
    maybe(flag, values)
  rescue
    nil
  end
  takes_string(values[0])
end`,
		},
		{
			name: "failure summaries discard exact values poisoned on one exit",
			source: `def crash()
  raise "stop"
end

def maybe(flag: bool, values: array<int | string>)
  flag ? [values.push("changed"), crash()][1] : crash()
end

def takes_string(value: string)
  value
end

def run(flag: bool)
  values = [1]
  maybe(flag, values) rescue takes_string(values[0])
end`,
		},
		{
			name: "failure summaries retain exact values on every clean exit",
			source: `def crash()
  raise "stop"
end

def maybe(flag: bool, values: array<int | string>)
  flag ? crash() : crash()
end

def takes_string(value: string)
  value
end

def run(flag: bool)
  values = [1]
  maybe(flag, values) rescue takes_string(values[0])
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "nonexact destructure aliases retain mutation identity",
			source: `def build() -> array<int>
  [1]
end

def takes_string(value: string)
  value
end

def run()
  names = build()
  copy, ignored = [names, 0]
  names.map! { "ok" }
  takes_string(copy[0])
end`,
		},
		{
			name: "logical assignment uses its pre-RHS decision",
			source: `def takes_string(value: string)
  value
end

def run()
  copy = nil
  copy ||= (-> { copy = "temporary"; true }.call() ? 1 : 1)
  takes_string(copy)
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "skipped logical assignment preserves an existing alias",
			source: `def takes_string(value: string)
  value
end

def run()
  items = [1]
  copy = items
  items ||= []
  copy.map! { "ok" }
  takes_string(items[0])
end`,
		},
		{
			name: "selected logical assignment rebinds an existing alias",
			source: `def takes_int(value: int)
  value
end

def run()
  original = [1]
  selected = original
  selected &&= ["replacement"]
  original.map! { 2 }
  takes_int(selected[0])
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "known skipped logical index keeps its bound",
			source: `def f(names: array<symbol>)
  names[0] ||= :third
  names << 1
end

def run()
  f([:first])
end`,
			want: []string{"write to names expected element symbol, got int"},
		},
		{
			name: "known selected logical index checks its write",
			source: `def f(names: array<symbol>)
  names[0] &&= 1
end

def run()
  f([:first])
end`,
			want: []string{"write to names expected element symbol, got int"},
		},
		{
			name: "known selected logical index updates the exact value",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def third(value)
    takes_int(value)
  end
end

def run()
  names = [nil]
  names[0] ||= :third
  Receiver.new.send(*names, "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "conditional exact aliases retain the unmodified path",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def run(flag: bool)
  original = [:first]
  selected = flag ? original : [:first]
  selected[0] = :third
  Receiver.new.send(*original, "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "anti-correlated exact aliases retain both unmodified paths",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def other(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def run(flag: bool)
  x = [:first]
  y = [:other]
  a = flag ? x : y
  b = flag ? y : x
  b[0] = :third
  Receiver.new.send(*a, "bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "correlated exact aliases advance together",
			source: `def takes_int(value: int)
  value
end

def probe(flag: bool)
  x = [1]
  y = [2]
  a = flag ? x : y
  b = flag ? x : y
  b[0] = "bad"
  takes_int(a[0])
end

def run(flag: bool)
  probe(flag)
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "exact child writes advance a containing parent",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

def probe(items: array<array<symbol>>)
  child = items[0]
  child[0] = :third
  Receiver.new.send(*items[0], "bad")
end

def run()
  probe([[:first]])
end`,
		},
		{
			name: "nonexact logical assignment retains alias identity",
			source: `def build() -> array<int>
  [1]
end

def takes_string(value: string)
  value
end

def probe(names: array<int>)
  copy = nil
  copy ||= names
  names.map! { "ok" }
  takes_string(copy[0])
end

def run()
  probe(build())
end`,
		},
		{
			name: "nonexact shared arguments retain identity",
			source: `def build() -> array<int>
  [1]
end

def takes_string(value: string)
  value
end

def mutate(a: array<int>, b: array<int>)
  a.map! { "ok" }
  takes_string(b[0])
end

def run()
  names = build()
  mutate(names, names)
end`,
		},
		{
			name: "return summaries retain shared parameter identity",
			source: `def build() -> array<int>
  [1]
end

def takes_string(value: string)
  value
end

def mutate_and_read(a: array<int>, b: array<int>)
  a.map! { "ok" }
  b[0]
end

def run()
  names = build()
  takes_string(mutate_and_read(names, names))
end`,
		},
		{
			name: "defaults observe shared parameter identity",
			source: `def build() -> array<int>
  [1]
end

def takes_string(value: string)
  value
end

def mutate(values)
  values.map! { "ok" }
  0
end

def inspect_pair(a: array<int>, middle: int = mutate(a), b: array<int>)
  takes_string(b[0])
end

def run()
  names = build()
  inspect_pair(a: names, b: names)
end`,
		},
		{
			name: "historical caller aliases do not imply current identity",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

class Producer
  def consume(values)
    values[0] = :third
  end
end

def inspect_pair(a: array<symbol>, b: array<symbol>)
  Producer.new.consume(a)
  Receiver.new.send(*b, "bad")
end

def run()
  first = [:third]
  second = first
  second = [:first]
  inspect_pair(first, second)
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "intervening argument effects break repeated-name identity",
			source: `def takes_int(value: int)
  value
end

class Receiver
  def first(value)
    takes_int(value)
  end

  def third(value)
    value
  end
end

class Producer
  def consume(values)
    values[0] = :third
  end
end

def inspect_pair(a: array<symbol>, ignored, b: array<symbol>)
  Producer.new.consume(a)
  Receiver.new.send(*b, "bad")
end

def run()
  first = [:third]
  second = [:first]
  inspect_pair(first, -> { first = second }.call(), first)
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "repeated auto calls do not imply result identity",
			source: `def takes_string(value: string)
  value
end

def maker() -> array<int>
  [1]
end

def inspect_pair(a: array<int>, b: array<int>)
  a.map! { "ok" }
  takes_string(b[0])
end

def run()
  inspect_pair(maker, maker)
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "container defaults retain earlier parameter identity",
			source: `def takes_string(value: string)
  value
end

def inspect_pair(a: array<int>, b: array<int> = a)
  a.map! { "ok" }
  takes_string(b[0])
end

def run()
  inspect_pair([1])
end`,
		},
		{
			name: "bare member auto calls do not alias their receiver",
			source: `def takes_int(value: int)
  value
end

class Factory
  def maker() -> array<string>
    ["ok"]
  end

  def check(value)
    takes_int(value)
  end
end

def run()
  factory = Factory.new
  copy = factory.maker
  copy << "more"
  factory.check("bad")
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "appending to a parent preserves a retained child",
			source: `def takes_string(value: string)
  value
end

def probe(rows: array<array<int>>, child: array<int>)
  rows << [2]
  takes_string(child[0])
end

def run()
  child = [1]
  probe([child], child)
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "push retains a child for later nested writes",
			source: `def takes_string(value: string)
  value
end

def run()
  rows = []
  child = [1]
  rows.push(child)
  rows[0][0] = "fixed"
  takes_string(child[0])
end`,
		},
		{
			name: "incompatible parent append preserves a retained child bound",
			source: `def mutate(rows: array<array<int>>, child: array<int>)
  rows << child
  rows << "bad"
  child << "also bad"
end

def run()
  mutate([], [])
end`,
			want: []string{
				"write to rows expected element array<int>, got string",
				"write to child expected element int, got string",
			},
		},
		{
			name: "mutating an already poisoned alias weakens its peer",
			source: `def mutate(a: array<number>, b: array<int>)
  a << 1.5
  b.push("bad")
  a << "also bad"
end

def run()
  values = [1]
  mutate(values, values)
end`,
		},
		{
			name: "rebound historical alias does not weaken its new peer",
			source: `def takes_string(value: string)
  value
end

def probe(a: array<number>, b: array<int>)
  selected = a
  selected = b
  a << 1.5
  takes_string(b[0])
end

def run()
  probe([1], [2])
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "direct alias survives intermediary rebind",
			source: `def takes_string(value: string)
  value
end

def mutate(values: array<int | string>)
  values[0] = "ok"
end

def run(b: array<int>, c: array<int>)
  y = b
  x = y
  y = c
  mutate(x)
  takes_string(b[0])
end`,
		},
		{
			name: "direct alias retains intermediary may aliases",
			source: `def takes_string(value: string)
  value
end

def mutate(values: array<int | string>)
  values[0] = "ok"
end

def run(a: array<int>, b: array<int>, c: array<int>, flag: bool)
  y = flag ? a : b
  x = y
  y = c
  mutate(x)
  takes_string(a[0])
end`,
		},
		{
			name: "sibling may alias does not leak mutation",
			source: `def takes_string(value: string)
  value
end

def mutate(values: array<int | string>)
  values[0] = "ok"
end

def run(flag: bool)
  a = [1]
  x = [2]
  if flag
    a = x
  else
    mutate(x)
  end
  takes_string(a[0])
end`,
			want: []string{"call to takes_string argument value expected string, got int"},
		},
		{
			name: "branch only correlated selection remains gradual",
			source: `def takes_int(value: int)
  value
end

def run(flag: bool, choose: bool)
  a = [1]
  b = [2]
  x = a
  y = b
  if flag
    x = choose ? a : b
    y = choose ? a : b
  end
  x[0] = "ok"
  takes_int(y[0])
end`,
		},
		{
			name: "conditional rebind retains a possible prior alias",
			source: `def takes_string(value: string)
  value
end

def probe(a: array<int>, b: array<int | string>, replacement: array<int>, flag: bool)
  if flag
    a = replacement
  end
  b[0] = "ok"
  a << "also ok"
  takes_string(a[0])
end

def run(values: array<int>, replacement: array<int>, flag: bool)
  probe(values, values, replacement, flag)
end`,
		},
		{
			name: "branch local identity is not definite after the join",
			source: `def takes_int(value: int)
  value
end

def run(flag: bool)
  a = [1]
  b = [2]
  selected = a
  if flag
    selected = b
  end
  selected[0] = "ok"
  takes_int(b[0])
end`,
		},
		{
			name: "branch join retains aliases from every path",
			source: `def takes_string(value: string)
  value
end

def mutate(values: array<int | string>)
  values[0] = "ok"
end

def run(
  a: array<int>,
  b: array<int>,
  c: array<int>,
  choose: bool,
  flag: bool
)
  selected = choose ? a : c
  if flag
    selected = b
  end
  mutate(selected)
  takes_string(a[0])
end`,
		},
		{
			name: "parent writes preserve conditionally retained children",
			source: `def mutate(
  rows: array<array<int>>,
  child: array<int>,
  other: array<int>,
  flag: bool
)
  rows[0] = flag ? child : other
  rows << [3]
  child << "bad"
end

def run(flag: bool)
  mutate([[0]], [1], [2], flag)
end`,
			want: []string{"write to child expected element int, got string"},
		},
		{
			name: "shared parameters with divergent index bounds weaken together",
			source: `def takes_float(value: float)
  value
end

def mutate(a: array<number>, b: array<int>)
  a[0] = 1.5
  takes_float(b[0])
end

def run()
  values = [1]
  mutate(values, values)
end`,
		},
		{
			name: "shared parameters with divergent shovel bounds weaken together",
			source: `def takes_float(value: float)
  value
end

def mutate(a: array<number>, b: array<int>)
  a << 1.5
  takes_float(b[1])
end

def run()
  values = [1]
  mutate(values, values)
end`,
		},
		{
			name: "shared parameters with divergent push bounds weaken together",
			source: `def takes_float(value: float)
  value
end

def mutate(a: array<number>, b: array<int>)
  a.push(1.5)
  takes_float(b[1])
end

def run()
  values = [1]
  mutate(values, values)
end`,
		},
		{
			name: "mutating a contained alias weakens its parent bound",
			source: `def takes_float(value: float)
  value
end

def mutate(rows: array<array<int>>, child: array<number>)
  child << 1.5
  takes_float(rows[0][1])
end

def run()
  rows = [[1]]
  mutate(rows, rows[0])
end`,
		},
		{
			name: "custom push may mutate its argument",
			source: `def strings(values: array<string>)
  values
end

class Custom
  def push(values)
    values[0] = "fixed"
  end
end

def run()
  values = [1]
  Custom.new.push(values)
  strings(values)
end`,
		},
		{
			name: "unknown callback clears a shovel witness",
			source: `def takes_int(value: int)
  value
end

def run(callback: function)
  values = [1]
  callback(values << "bad")
  takes_int(values[0])
end`,
		},
		{
			name: "unknown logical index checks its possible write",
			source: `def mutate(items: array<symbol?>)
  items[0] ||= 1
end

def run(items: array<symbol?>)
  mutate(items)
end`,
			want: []string{"write to items expected element symbol?, got int"},
		},
		{
			name: "skipped logical and assignment does not create an invalid keyword key",
			source: `def replacement(value)
  1
end

def install(**options)
  JSON.stringify = replacement
end

def takes_int(value: int)
  value
end

def run()
  options = {}
  options[2] &&= "bad"
  install(**options)
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "failed destructure index prevents a later namespace write",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run()
  values = []
  begin
    values[0], JSON.stringify = [1, replacement]
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "failing destructure setter retains effects before the raise",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Bomb
  def self.value=(value)
    JSON.stringify = replacement
    raise "stop"
  end
end

def install()
  Bomb.value, ignored = [1, 0]
end

def run()
  begin
    install()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure rescue state does not replace ensure receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

def install()
  klass = WriterA
  begin
    kept, ignored = [1, 0]
  rescue
    klass = WriterB
  ensure
    klass.install()
  end
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "unknown failure preserves the pre-body ensure receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

def maybe(flag: bool)
  if flag
    raise "stop"
  end
end

def install(flag: bool)
  klass = WriterA
  begin
    maybe(flag)
    klass = WriterB
  rescue
    nil
  ensure
    klass.install()
  end
end

def run(flag: bool)
  install(flag)
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "unknown failure preserves an intermediate ensure receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    nil
  end
end

class WriterB
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterC
  def self.install()
    nil
  end
end

def install(callback: function)
  klass = WriterA
  begin
    klass = WriterB
    callback.call()
    klass = WriterC
  rescue
    nil
  ensure
    klass.install()
  end
end

def run(callback: function)
  install(callback)
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "exact rescue state replaces ensure receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

def install()
  klass = WriterA
  begin
    raise "stop"
  rescue
    klass = WriterB
  ensure
    klass.install()
  end
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "namespace scan uses the evaluated destructure value",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

class Holder
  def self.value=(klass)
    klass.install()
  end
end

def install()
  klass = WriterA
  Holder.value, ignored = [klass, -> { klass = WriterB }.call()]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure rest retains evaluated class values",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

class Holder
  def self.value=(classes)
    classes[0].install()
  end
end

def install()
  klass = WriterA
  ignored, *Holder.value, last = [0, klass, -> { klass = WriterB }.call()]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure rest class values survive delayed use",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

class Holder
  def self.value=(classes)
    classes[0].install()
  end
end

def install()
  klass = WriterA
  ignored, *classes, last = [0, klass, 0]
  klass = WriterB
  Holder.value = classes
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure rest projections survive exact writes",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

class Holder
  def self.value=(classes)
    classes[0].install()
  end
end

def install()
  klass = WriterA
  ignored, *classes, last = [0, klass, 0, 0]
  klass = WriterB
  classes[1] = 1
  Holder.value = classes
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "nested destructure rest retains evaluated class values",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class Holder
  def self.value=(groups)
    groups[0][0].install()
  end
end

def install()
  ignored, *groups, last = [0, [WriterA], 0]
  Holder.value = groups
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure rest callables survive delayed use",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def writer(value)
  JSON.stringify = replacement
end

def noop(value)
  nil
end

class Holder
  def self.value=(callbacks)
    callbacks[0].call(1)
  end
end

def install()
  callback = writer
  ignored, *callbacks, last = [0, callback, -> { callback = noop; 0 }.call()]
  Holder.value = callbacks
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure rest projections survive branch joins",
			source: `def writer(value)
  value
end

def takes_function(value: function)
  value
end

def run(flag: bool)
  callback = writer
  ignored, *callbacks, last = [0, callback, 0]
  if flag
    callbacks = [nil]
  end
  takes_function(callbacks[0])
end`,
		},
		{
			name: "destructure rest invalidates retained nonexact children",
			source: `def takes_int(value: int)
  value
end

class WriterA
  def check()
    raise "stop"
  end
end

class WriterB
  def check()
    nil
  end
end

def mutate(names: array<WriterA>)
  ignored, *groups, last = [0, names, 0]
  names.map! { WriterB.new }
  groups[0][0].check()
  takes_int("bad")
end

def run()
  mutate([WriterA.new])
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure index setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install(box: Box)
  box[0], ignored = [1, 0]
end

def run()
  install(Box.new)
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "scalar destructure RHS retains its evaluated class value",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class Holder
  def self.value=(klass)
    klass.install()
  end
end

def install()
  Holder.value, ignored = WriterA
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure index setter retains evaluated selectors",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

class Box
  def []=(klass, ignored, value)
    klass.install()
  end
end

def install()
  box = Box.new
  klass = WriterA
  box[klass, -> { klass = WriterB }.call()], ignored = [1, 0]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "destructure index setter retains its evaluated receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def []=(index, value)
    JSON.stringify = replacement
  end
end

class BoxB
  def []=(index, value)
    nil
  end
end

def install()
  box = BoxA.new
  box[-> { box = BoxB.new; 0 }.call()], ignored = [1, 0]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want:   []string{"reassignment of box expected BoxA, got BoxB"},
			reject: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "destructure member target resolves an evaluated class alias",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def self.value=(value)
    JSON.stringify = replacement
  end
end

def install()
  klass = Holder
  klass.value, ignored = [1, 0]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "typed ivar callable assignment does not invoke its value",
			source: `def integer_encoder(value)
  1
end

def replacement()
  JSON.stringify = integer_encoder
  nil
end

def takes_int(value: int)
  value
end

class Holder
  property callback: function

  def install()
    @callback = replacement
  end
end

def run()
  Holder.new.install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "typed ivar logical assignment does not invoke its value",
			source: `def integer_encoder(value)
  1
end

def replacement()
  JSON.stringify = integer_encoder
  nil
end

def takes_int(value: int)
  value
end

class Holder
  property callback: function

  def install()
    @callback ||= replacement
  end
end

def run()
  Holder.new.install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "compound member setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    0
  end

  def value=(value)
    nil
  end
end

class BoxB
  def value()
    0
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

def install()
  box = BoxA.new
  box.value += -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "compound member setter does not follow an RHS rebind",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    0
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

class BoxB
  def value()
    0
  end

  def value=(value)
    nil
  end
end

def install()
  box = BoxA.new
  box.value += -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want:   []string{"reassignment of box expected BoxA, got BoxB"},
			reject: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "logical member setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    nil
  end

  def value=(value)
    nil
  end
end

class BoxB
  def value()
    nil
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

def install()
  box = BoxA.new
  box.value ||= -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "logical member setter does not follow an RHS rebind",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    nil
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

class BoxB
  def value()
    nil
  end

  def value=(value)
    nil
  end
end

def install()
  box = BoxA.new
  box.value ||= -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want:   []string{"reassignment of box expected BoxA, got BoxB"},
			reject: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "compound index setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def [](index)
    0
  end

  def []=(index, value)
    nil
  end
end

class BoxB
  def [](index)
    0
  end

  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install()
  box = BoxA.new
  box[0] += -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "raising compound setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    0
  end

  def value=(value)
    raise "stop"
  end
end

class BoxB
  def value()
    0
  end

  def value=(value)
    nil
  end
end

def install()
  box = BoxA.new
  box.value += -> { box = BoxB.new; 1 }.call()
  JSON.stringify = replacement
end

def run()
  begin
    install()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "class-valued constant setter resolves its semantic class",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Target
  def self.value=(value)
    nil
  end
end

class Actual
  def self.value=(value)
    JSON.stringify = replacement
  end
end

class Outer
  Target = Actual

  def self.install()
    Target.value = 1
  end
end

def run()
  Outer.install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "class-valued constant setter ignores its syntactic tail",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Target
  def self.value=(value)
    JSON.stringify = replacement
  end
end

class Actual
  def self.value=(value)
    nil
  end
end

class Outer
  Target = Actual

  def self.install()
    Target.value = 1
  end
end

def run()
  Outer.install()
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "scoped class-valued constant setter resolves its semantic class",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Target
  def self.value=(value)
    nil
  end
end

class Actual
  def self.value=(value)
    JSON.stringify = replacement
  end
end

class Outer
  Target = Actual
end

def install()
  Outer::Target.value = 1
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "local instance setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    JSON.stringify = replacement
  end
end

def install()
  holder = Holder.new
  holder.value = 1
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "raising local instance setter stops later namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    raise "stop"
  end
end

def install()
  holder = Holder.new
  holder.value = 1
  JSON.stringify = replacement
end

def run()
  begin
    install()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "local index setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install()
  box = Box.new
  box[0] = 1
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "local destructure index setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install()
  box = Box.new
  box[0], ignored = [1, 0]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "raising local index setter stops later namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    raise "stop"
  end
end

def install()
  box = Box.new
  box[0] = 1
  JSON.stringify = replacement
end

def run()
  begin
    install()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "typed parameter setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    JSON.stringify = replacement
  end
end

def install(holder: Holder)
  holder.value = 1
end

def run()
  install(Holder.new)
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "self setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    JSON.stringify = replacement
  end

  def install()
    self.value = 1
  end
end

def run()
  Holder.new.install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "builtin namespace parameter shadow does not record a global write",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  property stringify
end

def install(JSON)
  JSON.stringify = replacement
end

def run()
  install(Holder.new)
  takes_int(JSON.stringify({}))
end`,
			want: []string{"call to takes_int argument value expected int, got string"},
		},
		{
			name: "namespace scans do not alias same-named caller locals",
			source: `def build() -> array<int>
  [1]
end

def takes_string(value: string)
  value
end

def noop(a, b)
  0
end

def run()
  a = build()
  b = build()
  shared = build()
  noop(shared, shared)
  a.map! { "ok" }
  takes_string(b[0])
end`,
			want: []string{"call to takes_string argument value expected string, got int | nil"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := compileScript(t, tc.source).CheckWarningsForFunction("run")
			got := strings.Join(checkWarningMessages(warnings), "\n")
			if len(tc.want) == 0 {
				if got != "" {
					t.Fatalf("CheckWarningsForFunction(%q) = %q, want none", "run", got)
				}
				return
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(got, reject) {
					t.Fatalf("CheckWarningsForFunction(%q) = %q, reject substring %q", "run", got, reject)
				}
			}
		})
	}
}

func TestCheckAssignmentNamespaceEffectsInWholeScript(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantWarning bool
	}{
		{
			name: "ordinary member assignment control",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class Holder
  def self.value=(klass)
    klass.install()
  end
end

def run()
  Holder.value = WriterA
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "logical member assignment",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def self.value()
    nil
  end

  def self.value=(value)
    JSON.stringify = replacement
  end
end

def run()
  Holder.value ||= 1
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "skipped logical member assignment",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def self.value()
    1
  end

  def self.value=(value)
    JSON.stringify = replacement
  end
end

def run()
  Holder.value ||= 2
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "parameter shadows a class setter",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def self.value=(value)
    JSON.stringify = replacement
  end
end

def run(Holder)
  Holder.value = 1
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "typed ivar callable assignment does not invoke its value",
			source: `def integer_encoder(value)
  1
end

def replacement()
  JSON.stringify = integer_encoder
  nil
end

def takes_int(value: int)
  value
end

class Holder
  property callback: function

  def install()
    @callback = replacement
  end
end

def run()
  Holder.new.install()
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "typed ivar logical assignment does not invoke its value",
			source: `def integer_encoder(value)
  1
end

def replacement()
  JSON.stringify = integer_encoder
  nil
end

def takes_int(value: int)
  value
end

class Holder
  property callback: function

  def install()
    @callback ||= replacement
  end
end

def run()
  Holder.new.install()
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "compound member setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    0
  end

  def value=(value)
    nil
  end
end

class BoxB
  def value()
    0
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

def install()
  box = BoxA.new
  box.value += -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "compound member setter does not follow an RHS rebind",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    0
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

class BoxB
  def value()
    0
  end

  def value=(value)
    nil
  end
end

def install()
  box = BoxA.new
  box.value += -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "logical member setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    nil
  end

  def value=(value)
    nil
  end
end

class BoxB
  def value()
    nil
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

def install()
  box = BoxA.new
  box.value ||= -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "logical member setter does not follow an RHS rebind",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    nil
  end

  def value=(value)
    JSON.stringify = replacement
  end
end

class BoxB
  def value()
    nil
  end

  def value=(value)
    nil
  end
end

def install()
  box = BoxA.new
  box.value ||= -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "compound index setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def [](index)
    0
  end

  def []=(index, value)
    nil
  end
end

class BoxB
  def [](index)
    0
  end

  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install()
  box = BoxA.new
  box[0] += -> { box = BoxB.new; 1 }.call()
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "raising compound setter retains its pre-RHS receiver",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class BoxA
  def value()
    0
  end

  def value=(value)
    raise "stop"
  end
end

class BoxB
  def value()
    0
  end

  def value=(value)
    nil
  end
end

def install()
  box = BoxA.new
  box.value += -> { box = BoxB.new; 1 }.call()
  JSON.stringify = replacement
end

def run()
  begin
    install()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "class-valued constant setter resolves its semantic class",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Target
  def self.value=(value)
    nil
  end
end

class Actual
  def self.value=(value)
    JSON.stringify = replacement
  end
end

class Outer
  Target = Actual

  def self.install()
    Target.value = 1
  end
end

def run()
  Outer.install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "class-valued constant setter ignores its syntactic tail",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Target
  def self.value=(value)
    JSON.stringify = replacement
  end
end

class Actual
  def self.value=(value)
    nil
  end
end

class Outer
  Target = Actual

  def self.install()
    Target.value = 1
  end
end

def run()
  Outer.install()
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "scoped class-valued constant setter resolves its semantic class",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Target
  def self.value=(value)
    nil
  end
end

class Actual
  def self.value=(value)
    JSON.stringify = replacement
  end
end

class Outer
  Target = Actual
end

def install()
  Outer::Target.value = 1
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "local instance setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    JSON.stringify = replacement
  end
end

def install()
  holder = Holder.new
  holder.value = 1
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "typed parameter setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    JSON.stringify = replacement
  end
end

def install(holder: Holder)
  holder.value = 1
end

def run()
  install(Holder.new)
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "raising typed parameter setter stops later namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    raise "stop"
  end
end

def install(holder: Holder)
  holder.value = 1
  JSON.stringify = replacement
end

def run()
  begin
    install(Holder.new)
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "local index setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install()
  box = Box.new
  box[0] = 1
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "local destructure index setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    JSON.stringify = replacement
  end
end

def install()
  box = Box.new
  box[0], ignored = [1, 0]
end

def run()
  install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "raising typed index setter stops later namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def []=(index, value)
    raise "stop"
  end
end

def install(box: Box)
  box[0] = 1
  JSON.stringify = replacement
end

def run()
  begin
    install(Box.new)
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "self setter contributes namespace effects",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  def value=(value)
    JSON.stringify = replacement
  end

  def install()
    self.value = 1
  end
end

def run()
  Holder.new.install()
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "builtin namespace parameter shadow does not record a global write",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Holder
  property stringify
end

def install(JSON)
  JSON.stringify = replacement
end

def run()
  install(Holder.new)
  takes_int(JSON.stringify({}))
end`,
			wantWarning: true,
		},
		{
			name: "compound index assignment",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Box
  def [](index)
    0
  end

  def []=(index, value)
    JSON.stringify = replacement
  end
end

def run()
  box = Box.new
  box[0] += 1
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "scalar value",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class Holder
  def self.value=(klass)
    klass.install()
  end
end

def run()
  Holder.value, ignored = WriterA
  takes_int(JSON.stringify({}))
end`,
		},
		{
			name: "evaluated selector",
			source: `def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class WriterA
  def self.install()
    JSON.stringify = replacement
  end
end

class WriterB
  def self.install()
    nil
  end
end

class Box
  def []=(klass, ignored, value)
    klass.install()
  end
end

def run()
  box = Box.new
  klass = WriterA
  box[klass, -> { klass = WriterB }.call()], ignored = [1, 0]
  takes_int(JSON.stringify({}))
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := compileScript(t, tc.source).CheckWarnings()
			got := strings.Join(checkWarningMessages(warnings), "\n")
			staleWarning := strings.Contains(got, "call to takes_int argument value expected int, got string")
			if tc.wantWarning && !staleWarning {
				t.Fatalf("CheckWarnings() = %q, want unmodified namespace warning", got)
			}
			if !tc.wantWarning && staleWarning {
				t.Fatalf("CheckWarnings() = %q, reject stale namespace warning", got)
			}
		})
	}
}
