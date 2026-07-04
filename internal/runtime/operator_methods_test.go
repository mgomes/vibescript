package runtime

import (
	"context"
	"testing"
)

// TestOperatorMethodDefinitions pins Ruby's operator method protocol: a class
// defines +, -, *, /, %, **, <<, &, <, <=, >, >=, or <=> as an instance method
// and operator syntax dispatches to it on the left operand.
func TestOperatorMethodDefinitions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Vec
  def initialize(x)
    @x = x
  end
  def x
    @x
  end
  def +(other)
    Vec.new(x + other.x)
  end
  def -(other)
    Vec.new(x - other.x)
  end
  def *(factor)
    Vec.new(x * factor)
  end
  def <(other)
    x < other.x
  end
  def <=>(other)
    x <=> other.x
  end
end

class Bag
  def initialize
    @items = []
  end
  def <<(item)
    @items = @items + [item]
    self
  end
  def items
    @items
  end
end

def arithmetic
  a = Vec.new(3)
  b = Vec.new(4)
  [(a + b).x, (a - b).x, (a * 5).x]
end

def comparisons
  a = Vec.new(3)
  b = Vec.new(4)
  [a < b, (a <=> b), (b <=> a)]
end

def shovel_chain
  bag = Bag.new
  bag << 1 << 2 << 3
  bag.items
end`)

	ctx := context.Background()
	arithmetic := callScript(t, ctx, script, "arithmetic", nil, CallOptions{})
	compareArrays(t, arithmetic, []Value{NewInt(7), NewInt(-1), NewInt(15)})
	comparisons := callScript(t, ctx, script, "comparisons", nil, CallOptions{})
	compareArrays(t, comparisons, []Value{NewBool(true), NewInt(-1), NewInt(1)})
	shovel := callScript(t, ctx, script, "shovel_chain", nil, CallOptions{})
	compareArrays(t, shovel, []Value{NewInt(1), NewInt(2), NewInt(3)})
}

// TestOperatorMethodEquality pins == and != dispatch: a user == defines value
// equality for both operators (!= negates it when no explicit != exists), and
// instances without either keep built-in identity semantics.
func TestOperatorMethodEquality(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Point
  def initialize(x)
    @x = x
  end
  def x
    @x
  end
  def ==(other)
    x == other.x
  end
end

class Odd
  def !=(other)
    "custom !="
  end
end

def value_equality
  a = Point.new(1)
  b = Point.new(1)
  c = Point.new(2)
  [a == b, a != b, a == c, a != c]
end

def explicit_not_equal
  Odd.new != Odd.new
end`)

	ctx := context.Background()
	values := callScript(t, ctx, script, "value_equality", nil, CallOptions{})
	compareArrays(t, values, []Value{NewBool(true), NewBool(false), NewBool(false), NewBool(true)})
	if got := callScript(t, ctx, script, "explicit_not_equal", nil, CallOptions{}); got.String() != "custom !=" {
		t.Fatalf("explicit != = %v, want custom !=", got)
	}
}

// TestIndexMethodDefinitions pins [] and []= dispatch, including the
// read-modify-write compound assignment that uses both.
func TestIndexMethodDefinitions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Box
  def [](i)
    i + 1
  end
end

class Grid
  def [](row, col)
    row * 10 + col
  end
end

class Counter
  def initialize
    @slots = {}
  end
  def [](key)
    @slots.fetch(key, 0)
  end
  def []=(key, value)
    @slots[key] = value
  end
end

def read_one
  Box.new[4]
end

def read_multi
  Grid.new[2, 3]
end

def write_and_read
  c = Counter.new
  c["a"] = 1
  c["a"] += 5
  [c["a"], c["missing"]]
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "read_one", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 5 {
		t.Fatalf("Box.new[4] = %v, want 5", got)
	}
	if got := callScript(t, ctx, script, "read_multi", nil, CallOptions{}); got.Kind() != KindInt || got.Int() != 23 {
		t.Fatalf("Grid.new[2, 3] = %v, want 23", got)
	}
	result := callScript(t, ctx, script, "write_and_read", nil, CallOptions{})
	compareArrays(t, result, []Value{NewInt(6), NewInt(0)})
}

// TestOperatorMethodErrors pins the failure modes: operators without a
// defining method keep their built-in errors, indexing without []/[]= names
// the missing method, private operator methods are rejected, and operator
// definitions outside a class fail to compile.
func TestOperatorMethodErrors(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Plain
end

class Hidden
  private
  def +(other)
    1
  end
end

def add_without_method
  Plain.new + 1
end

def index_without_method
  Plain.new[0]
end

def index_write_without_method
  p = Plain.new
  p[0] = 1
end

def private_operator
  Hidden.new + 1
end`)

	requireCallErrorContains(t, script, "add_without_method", nil, CallOptions{}, "unsupported addition operands")
	requireCallErrorContains(t, script, "index_without_method", nil, CallOptions{}, "Plain does not define []")
	requireCallErrorContains(t, script, "index_write_without_method", nil, CallOptions{}, "Plain does not define []=")
	requireCallErrorContains(t, script, "private_operator", nil, CallOptions{}, "private method +")

	requireCompileErrorContainsDefault(t, `def +(other)
  1
end`, "operator method + must be defined in a class")
	requireCompileErrorContainsDefault(t, `def [](i)
  i
end`, "operator method [] must be defined in a class")
}

// TestOperatorMethodLoopSignalsCannotCrossCallBoundary pins that a bare
// break/next inside an operator, [], or []= method raises the call-boundary
// error instead of silently breaking or continuing the caller's loop.
func TestOperatorMethodLoopSignalsCannotCrossCallBoundary(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Leaky
  def +(other)
    break
  end
  def [](i)
    next
  end
  def []=(i, v)
    break
  end
end

def loop_with_plus
  total = 0
  for n in [1, 2, 3]
    Leaky.new + n
    total = total + 1
  end
  total
end

def loop_with_index_read
  for n in [1, 2]
    Leaky.new[n]
  end
  "done"
end

def loop_with_index_write
  leaky = Leaky.new
  for n in [1, 2]
    leaky[n] = n
  end
  "done"
end`)

	requireCallErrorContains(t, script, "loop_with_plus", nil, CallOptions{}, "break cannot cross call boundary")
	requireCallErrorContains(t, script, "loop_with_index_read", nil, CallOptions{}, "next cannot cross call boundary")
	requireCallErrorContains(t, script, "loop_with_index_write", nil, CallOptions{}, "break cannot cross call boundary")
}
