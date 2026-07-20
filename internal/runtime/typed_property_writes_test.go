package runtime

import "testing"

// Direct instance-variable writes must honor the contract declared by a
// typed accessor: the write validates when it executes, not when a later
// getter or boundary happens to observe the value.
func TestTypedPropertyDirectWriteValidates(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class User
  property name: string
  getter age: int
  setter tag: string

  def initialize
    @name = "ada"
    @age = 30
    @tag = "vip"
  end

  def rename(value)
    @name = value
  end

  def set_age(value)
    @age = value
  end

  def retag(value)
    @tag = value
  end

  def stored_name
    @name
  end
end

def good_rename
  u = User.new
  u.rename("grace")
  u.stored_name
end

def bad_rename
  User.new.rename(1)
end

def bad_age_write
  User.new.set_age("old")
end

def bad_tag_write
  User.new.retag(99)
end
`)

	if got := callFunc(t, script, "good_rename", nil); !got.Equal(NewString("grace")) {
		t.Fatalf("good_rename = %v, want \"grace\"", got)
	}
	requireCallErrorContains(t, script, "bad_rename", nil, CallOptions{}, "instance variable @name expected string, got int")
	requireCallErrorContains(t, script, "bad_age_write", nil, CallOptions{}, "instance variable @age expected int, got string")
	requireCallErrorContains(t, script, "bad_tag_write", nil, CallOptions{}, "instance variable @tag expected string, got int")
}

func TestTypedPropertyConstructorWriteValidates(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class User
  property name: string

  def initialize(name)
    @name = name
  end
end

class Point
  property x: int

  def initialize(@x)
  end
end

def bad_constructor_write
  User.new(7)
end

def bad_ivar_param
  Point.new("east")
end

def good_ivar_param
  Point.new(3).x
end
`)

	if got := callFunc(t, script, "good_ivar_param", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("good_ivar_param = %v, want 3", got)
	}
	requireCallErrorContains(t, script, "bad_constructor_write", nil, CallOptions{}, "instance variable @name expected string, got int")
	requireCallErrorContains(t, script, "bad_ivar_param", nil, CallOptions{}, "instance variable @x expected int, got string")
}

// Untyped accessors and undeclared instance variables stay fully dynamic.
func TestUntypedIvarWritesStayDynamic(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Grab
  property bag
  getter view: string
  setter slot

  def initialize
    @bag = 1
    @bag = "two"
    @stash = :loose
    @stash = 3.5
    @slot = 1
    @slot = :sym
  end

  def stash
    @stash
  end
end

def dynamic_ok
  Grab.new.stash
end
`)

	if got := callFunc(t, script, "dynamic_ok", nil); !got.Equal(NewFloat(3.5)) {
		t.Fatalf("dynamic_ok = %v, want 3.5", got)
	}
}

// An accessor declared by an included module backs the ivar with the same
// contract in the including class's methods.
func TestTypedPropertyContractFromIncludedModule(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Named
  property name: string
end

class Robot
  include Named

  def initialize
    @name = "r2"
  end

  def rename(value)
    @name = value
  end
end

def good_module_write
  r = Robot.new
  r.rename("c3")
  r.name
end

def bad_module_write
  Robot.new.rename(4)
end
`)

	if got := callFunc(t, script, "good_module_write", nil); !got.Equal(NewString("c3")) {
		t.Fatalf("good_module_write = %v, want \"c3\"", got)
	}
	requireCallErrorContains(t, script, "bad_module_write", nil, CallOptions{}, "instance variable @name expected string, got int")
}

func TestNullableTypedPropertyDirectWrite(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class User
  property nickname: string?

  def initialize
    @nickname = nil
  end

  def set_nickname(value)
    @nickname = value
  end
end

def nil_ok
  u = User.new
  u.set_nickname("ace")
  u.set_nickname(nil)
  u.nickname
end

def bad_nickname
  User.new.set_nickname(5)
end
`)

	if got := callFunc(t, script, "nil_ok", nil); got.Kind() != KindNil {
		t.Fatalf("nil_ok = %v, want nil", got)
	}
	requireCallErrorContains(t, script, "bad_nickname", nil, CallOptions{}, "instance variable @nickname expected string?, got int")
}

// Compound, logical, and destructuring writes are direct writes too.
func TestTypedPropertyCompoundAndDestructuredWrites(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Counter
  property count: int
  property label: string
  property seed: string

  def initialize
    @count = 0
    @label = "c"
  end

  def bump_bad
    @count += 0.5
  end

  def default_bad
    @seed ||= 2
  end

  def spread_bad
    @count, @label = 1, 2
  end

  def clear_label
    @label = nil
  end
end

def bad_compound
  Counter.new.bump_bad
end

def bad_logical
  c = Counter.new
  c.default_bad
end

def bad_destructure
  Counter.new.spread_bad
end

def bad_nil_write
  Counter.new.clear_label
end
`)

	requireCallErrorContains(t, script, "bad_compound", nil, CallOptions{}, "instance variable @count expected int, got float")
	requireCallErrorContains(t, script, "bad_logical", nil, CallOptions{}, "instance variable @seed expected string, got int")
	requireCallErrorContains(t, script, "bad_destructure", nil, CallOptions{}, "instance variable @label expected string, got int")
	requireCallErrorContains(t, script, "bad_nil_write", nil, CallOptions{}, "instance variable @label expected string, got nil")
}

// A direct write normalizes like any typed boundary: a matching symbol
// coerces into the declared enum before it lands in the ivar.
func TestTypedPropertyDirectWriteNormalizes(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
enum Status
  Draft
  Published
end

class Post
  property status: Status

  def initialize
    @status = :draft
  end

  def publish
    @status = :published
  end

  def draft_value
    @status == Status::Draft
  end
end

def normalized_write
  p = Post.new
  p.draft_value
end
`)

	if got := callFunc(t, script, "normalized_write", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("normalized_write = %v, want true", got)
	}
}

// A generated untyped setter keeps direct writes dynamic even when the
// getter half is typed: the declared write contract is the setter's.
func TestUntypedGeneratedSetterKeepsWritesDynamic(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Cell
  getter value: int
  setter value

  def initialize
    @value = "raw"
  end

  def raw
    @value
  end
end

def dynamic_setter
  Cell.new.raw
end
`)

	if got := callFunc(t, script, "dynamic_setter", nil); !got.Equal(NewString("raw")) {
		t.Fatalf("dynamic_setter = %v, want \"raw\"", got)
	}
}
