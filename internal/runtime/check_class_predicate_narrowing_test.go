package runtime

import (
	"strings"
	"testing"
)

// Class predicates narrow known nominal unions through both condition
// branches when every arm provably reaches the runtime universal predicate.

const classPredicateNarrowingPrelude = `
module Payable
end

class User
  include Payable

  def initialize()
  end
end

class Order
  def initialize()
  end
end

def takes_user(value: User)
  value
end

def takes_order(value: Order)
  value
end
`

func TestCheckClassPredicateNarrowing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "true branch keeps the matching class",
			source: `
def run(flag)
  u = flag ? User.new : Order.new
  if u.is_a?(User)
    takes_order(u)
  end
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "false branch drops the matching class",
			source: `
def run(flag)
  u = flag ? User.new : Order.new
  unless u.is_a?(User)
    takes_user(u)
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "instance_of narrows exactly",
			source: `
def run(flag)
  u = flag ? User.new : Order.new
  if u.instance_of?(Order)
    takes_user(u)
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "nil arm drops on the true path",
			source: `
def run(flag)
  u = flag ? User.new : nil
  if u.is_a?(User)
    takes_order(u)
  end
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "module ancestry narrows is_a",
			source: `
def run(flag)
  u = flag ? User.new : Order.new
  if u.is_a?(Payable)
    takes_order(u)
  end
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "qualified module ancestry narrows kind_of",
			source: `
module Billing
  module QualifiedPayable
  end
end

class QualifiedUser
  include Billing::QualifiedPayable

  def initialize()
  end
end

def takes_qualified_user(value: QualifiedUser)
  value
end

def run(u: QualifiedUser | Order)
  unless u.kind_of?(Billing::QualifiedPayable)
    takes_qualified_user(u)
  end
end
`,
			warning: "call to takes_qualified_user argument value expected QualifiedUser, got Order",
		},
		{
			name: "container arm keeps the receiver fact",
			source: `
def run(flag)
  u = flag ? User.new : []
  if u.is_a?(User)
    takes_order(u)
  end
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "guard clause narrowing survives",
			source: `
def run(flag)
  u = flag ? User.new : Order.new
  return 0 unless u.is_a?(User)
  takes_order(u)
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "proven pure prior calls preserve narrowing",
			source: `
def top_noop()
  1
end

def block_swap(k)
  k.User = Order
end

class PureDispatch
  def initialize()
  end

  def +(other)
    1
  end

  def [](index)
    1
  end

  def []=(index, value)
    1
  end

  def trigger=(value)
    1
  end
end

class Holder
  def self.noop()
    1
  end

  top_noop()
  noop()
  1.nil?()
  random_id()
  [1].fetch(0) { block_swap(self) }
  pure = PureDispatch.new
  pure + 1
  pure[0]
  indexed = PureDispatch.new
  indexed[0] = 1
  assigned = PureDispatch.new
  assigned.trigger = 1

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "later opaque call preserves earlier narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
    swap(self)
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "exception-only opaque call does not poison later narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    begin
      if flag
        swap(self)
        raise "boom"
      end
    rescue RuntimeError
      return
    end

    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "unreachable logical assignment calls preserve narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Mutator
  def initialize()
  end
end

class Holder
  def check(mutator: Mutator, u: User | Order)
    mutator ||= swap(self)
    nothing = nil
    nothing &&= swap(self)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "return-only loop call preserves narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    while flag
      swap(self)
      return
    end
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "return-only ensure call preserves narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    begin
      1
    ensure
      if flag
        swap(self)
        return
      end
    end
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "return-only protected call preserves narrowing after ensure",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    begin
      if flag
        swap(self)
        return
      end
    ensure
      1
    end
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "ensure return suppresses protected break effects",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(u: User | Order)
    for stop in [true]
      begin
        swap(self)
        break
      ensure
        return
      end
    end
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
		{
			name: "unrelated instance writes do not shadow class constants",
			source: `
class Holder
  def configure()
    Order.User = Order
    User = Order
    self.User = Order
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
			warning: "call to takes_user argument value expected User, got Order",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, classPredicateNarrowingPrelude+tc.source), tc.warning)
		})
	}
}

func TestCheckClassPredicateDisabledNarrowingKeepsUnionBoundaryChecks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		source   string
		function string
		gradual  bool
	}{
		{
			name: "override disables narrowing",
			source: `
class Sneaky
  def initialize()
  end

  def is_a?(target)
    true
  end
end

def run(flag)
  u = flag ? Sneaky.new : Order.new
  unless u.is_a?(Order)
    takes_order(u)
  end
end
`,
		},
		{
			name: "dynamic class argument disables narrowing",
			source: `
def run(flag, k)
  u = flag ? User.new : Order.new
  unless u.is_a?(k)
    takes_order(u)
  end
end
`,
		},
		{
			name: "qualified namespace write disables narrowing",
			source: `
module Billing
  module QualifiedPayable
  end
end

class QualifiedUser
  include Billing::QualifiedPayable

  def initialize()
  end
end

def takes_qualified_user(value: QualifiedUser)
  value
end

def run(u: QualifiedUser | Order)
  Billing.QualifiedPayable = Order
  unless u.kind_of?(Billing::QualifiedPayable)
    takes_qualified_user(u)
  end
end
`,
		},
		{
			name: "opaque call disables qualified narrowing",
			source: `
module Billing
  module QualifiedPayable
  end
end

class QualifiedUser
  include Billing::QualifiedPayable

  def initialize()
  end
end

def takes_qualified_user(value: QualifiedUser)
  value
end

def run(trigger, u: QualifiedUser | Order)
  trigger
  unless u.kind_of?(Billing::QualifiedPayable)
    takes_qualified_user(u)
  end
end
`,
		},
		{
			name: "self class constant disables narrowing",
			source: `
class Wrapper
  User = 1

  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "included module constant disables narrowing",
			source: `
module Aliases
  User = 2
end

class Holder
  include Aliases

  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "method class var write disables narrowing",
			source: `
class Holder
  def initialize()
  end

  def stash(v)
    @@User = v
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "instance self class write disables narrowing",
			source: `
class Holder
  def initialize()
  end

  def stash(v)
    self.class.User = v
  end

  def check(u: User | Order)
    if u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "stored class self write disables narrowing",
			source: `
class Holder
  self.class = self
  self.class.User = Order

  def initialize()
  end

  def check(u: User | Order)
    if u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "included self class write uses receiver setters",
			source: `
module Mutator
  def self.User=(value)
    value
  end

  def stash(value)
    self.class.User = value
  end
end

class Holder
  include Mutator

  def initialize()
  end

  def check(u: User | Order)
    if u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "member-assigned constant disables narrowing",
			source: `
class Holder
  self.User = Order

  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_order(u)
    end
    u
  end
end
`,
		},
		{
			name: "partial-path local assignment disables narrowing",
			source: `
def run(flag)
  u = flag ? User.new : Order.new
  if flag
    User = Order
  end
  unless u.is_a?(User)
    takes_order(u)
  end
end
`,
		},
		{
			name: "unknown receiver stays unknown",
			source: `
def run(u)
  if u.is_a?(User)
    takes_order(u)
  end
end
`,
			gradual: true,
		},
		{
			name: "partial-path opaque call disables narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    if flag
      swap(self)
    end
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
		},
		{
			name: "opaque call before break disables narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  for stop in [true, false]
    if stop
      swap(self)
      break
    end
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
		},
		{
			name: "opaque call before next disables narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  for skip in [true, false]
    if skip
      swap(self)
      next
    end
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
		},
		{
			name: "opaque call before rescue disables narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    begin
      if flag
        swap(self)
        raise "boom"
      end
    rescue RuntimeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end
`,
		},
		{
			name:     "direct write before rescue disables narrowing",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run(flag: bool)
  begin
    if flag
      Holder.User = Order
      raise "boom"
    end
  rescue RuntimeError
    Holder.new.check(User.new)
  end
end
`,
		},
		{
			name:     "partial-path direct write disables narrowing",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run(flag: bool)
  if flag
    Holder.User = Order
  end
  Holder.new.check(User.new)
end
`,
		},
		{
			name:     "direct write before break disables narrowing",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run()
  for stop in [true, false]
    if stop
      Holder.User = Order
      break
    end
  end
  Holder.new.check(User.new)
end
`,
		},
		{
			name:     "opaque ensure after break disables narrowing",
			function: "run",
			source: `
def swap()
  Holder.User = Order
end

class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run()
  for stop in [true]
    begin
      break
    ensure
      swap()
    end
  end
  Holder.new.check(User.new)
end
`,
		},
		{
			name:     "direct ensure after next disables narrowing",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run()
  for skip in [true]
    begin
      next
    ensure
      Holder.User = Order
    end
  end
  Holder.new.check(User.new)
end
`,
		},
		{
			name:     "ensure break preserves protected exception effects",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run()
  for stop in [true]
    begin
      Holder.User = Order
      raise "boom"
    ensure
      break
    end
  end
  Holder.new.check(User.new)
end
`,
		},
		{
			name:     "destructured direct write disables narrowing",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run()
  Holder.User, ignored = [Order, 0]
  Holder.new.check(User.new)
end
`,
		},
		{
			name:     "parallel setter disables later narrowing",
			function: "run",
			source: `
class Mutator
  def trigger=(value)
    Holder.User = Order
    value
  end
end

class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
    0
  end
end

def run(mutator: Mutator, values: array<int>)
  mutator.trigger, values[Holder.new.check(User.new)] = [1, 2]
end
`,
		},
		{
			name: "hash default proc read disables narrowing",
			source: `
class Holder
  def check(defaults: hash, u: User | Order)
    defaults[:missing]
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`,
		},
		{
			name: "opaque call before ensure disables narrowing",
			source: `
def swap(k)
  k.User = Order
end

class Holder
  def check(flag: bool, u: User | Order)
    begin
      if flag
        swap(self)
        raise "boom"
      end
    ensure
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end
`,
		},
		{
			name:     "direct write before ensure disables narrowing",
			function: "run",
			source: `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run(flag: bool)
  begin
    if flag
      Holder.User = Order
      raise "boom"
    end
  ensure
    Holder.new.check(User.new)
  end
end
`,
		},
		{
			name: "opaque rescue expression disables narrowing",
			source: `
def swap_and_raise(k)
  k.User = Order
  raise "boom"
end

class Holder
  def check(u: User | Order)
    swap_and_raise(self) rescue (u.is_a?(User) || takes_user(u))
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classPredicateNarrowingPrelude+tc.source)
			if tc.function == "" {
				if tc.gradual {
					requireNoCheckWarnings(t, script)
					return
				}
				requireCheckWarningContains(t, script, "argument value expected")
				return
			}
			if warnings := script.CheckWarningsForFunction(tc.function); len(warnings) > 0 {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want none", tc.function, warnings)
			}
		})
	}
}

func TestCheckClassPredicateNarrowingBailsAfterRuntimeClassConstantWrite(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+`
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
    u
  end
end

Holder.User = Order
Holder.new.check(User.new)
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	requireNoCheckWarnings(t, script)
}

func TestCheckClassPredicateNarrowingBailsAfterOpaqueClassConstantWrite(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+`
def swap(k)
  k.User = Order
end

class Holder
  swap(self)

  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
    u
  end
end
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_user argument value expected User, got User | Order")
}

func TestCheckClassPredicateNarrowingBailsAfterOpaqueDispatch(t *testing.T) {
	t.Parallel()

	const mutator = `
class Mutator
  def initialize()
  end

  def +(other)
    Holder.User = Order
    self
  end

  def [](index)
    Holder.User = Order
    nil
  end

  def []=(index, value)
    Holder.User = Order
    value
  end

  def trigger=(value)
    Holder.User = Order
    value
  end

  def self.class_trigger=(value)
    Holder.User = Order
    value
  end
end
`
	const holder = `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
    u
  end
end
`
	cases := []struct {
		name     string
		dispatch string
	}{
		{
			name:     "binary operator",
			dispatch: "  Mutator.new + 0\n",
		},
		{
			name:     "index read",
			dispatch: "  Mutator.new[0]\n",
		},
		{
			name:     "index assignment",
			dispatch: "  mutator = Mutator.new\n  mutator[0] = 1\n",
		},
		{
			name:     "compound operator assignment",
			dispatch: "  mutator = Mutator.new\n  mutator += 1\n",
		},
		{
			name:     "member setter",
			dispatch: "  Mutator.new.trigger = 1\n",
		},
		{
			name:     "class member setter",
			dispatch: "  Mutator.class_trigger = 1\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := classPredicateNarrowingPrelude + mutator + holder +
				"def run()\n" + tc.dispatch + "  Holder.new.check(User.new)\nend\n"
			script := compileScriptDefault(t, source)
			if warnings := script.CheckWarningsForFunction("run"); len(warnings) > 0 {
				t.Fatalf("CheckWarningsForFunction(%q) = %#v, want none", "run", warnings)
			}
		})
	}
}

func TestCheckClassPredicateNarrowingChecksDynamicDispatchAtCallState(t *testing.T) {
	t.Parallel()

	holder := `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`
	cases := []struct {
		name   string
		source string
	}{
		{
			name: "class alias",
			source: `
klass = Holder
klass.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "or-assigned class alias",
			source: `
klass = Holder
klass ||= Holder
klass.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "and-assigned class alias",
			source: `
klass = Order
klass &&= Holder
klass.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "nil or-assigned class alias",
			source: `
klass = nil
klass ||= Holder
klass.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "nil logical assignment class alias",
			source: `
klass = nil
klass &&= Order
klass ||= Holder
klass.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "dynamic argument receiver",
			source: `
def invoke(target)
  target.check(User.new)
end

Holder.User = Order
invoke(Holder.new)
`,
		},
		{
			name: "class-valued argument receiver",
			source: `
def invoke(klass)
  klass.new.check(User.new)
end

Holder.User = Order
invoke(Holder)
`,
		},
		{
			name: "multiple class-valued argument receivers",
			source: `
class OtherHolder
  def initialize()
  end

  def check(u)
    u
  end
end

def invoke(klass)
  klass.new.check(User.new)
end

Holder.User = Order
invoke((1 == 2) ? Holder : OtherHolder)
`,
		},
		{
			name: "array class value receiver",
			source: `
Holder.User = Order
[Holder][0].new.check(User.new)
`,
		},
		{
			name: "conditional class value receiver",
			source: `
Holder.User = Order
(true ? Holder : Holder).new.check(User.new)
`,
		},
		{
			name: "hash class value receiver",
			source: `
Holder.User = Order
{key: Holder}[:key].new.check(User.new)
`,
		},
		{
			name: "negative array index class value receiver",
			source: `
Holder.User = Order
[Order, Holder][-1].new.check(User.new)
`,
		},
		{
			name: "last duplicate hash class value receiver",
			source: `
Holder.User = Order
{key: Order, key: Holder}[:key].new.check(User.new)
`,
		},
		{
			name: "destructured class value receiver",
			source: `
klass, ignored = [Holder, nil]
Holder.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "function return class value receiver",
			source: `
def holder_class()
  Holder
end

Holder.User = Order
holder_class().new.check(User.new)
`,
		},
		{
			name: "logical class value receiver",
			source: `
Holder.User = Order
(nil || Holder).new.check(User.new)
`,
		},
		{
			name: "itself class value receiver",
			source: `
Holder.User = Order
Holder.itself().new.check(User.new)
`,
		},
		{
			name: "send",
			source: `
Holder.User = Order
Holder.new.send(:check, User.new)
`,
		},
		{
			name: "public send",
			source: `
Holder.User = Order
Holder.new.public_send("check", User.new)
`,
		},
		{
			name: "send receiver captured before arguments",
			source: `
def touch(value)
  value
end

targets = [Holder.new]
Holder.User = Order
targets[0].send(:check, touch(targets) && User.new)
`,
		},
		{
			name: "bound callable argument",
			source: `
def invoke(f: function)
  f.call(User.new)
end

Holder.User = Order
invoke(Holder.new.check)
`,
		},
		{
			name: "conditional bound callable argument",
			source: `
def invoke(f: function)
  f.call(User.new)
end

Holder.User = Order
invoke(true ? Holder.new.check : Holder.new.check)
`,
		},
		{
			name: "multiple bound callable argument",
			source: `
class OtherHolder
  def initialize()
  end

  def check(u)
    u
  end
end

def invoke(f: function)
  f.call(User.new)
end

Holder.User = Order
invoke((1 == 2) ? Holder.new.check : OtherHolder.new.check)
`,
		},
		{
			name: "default receiver",
			source: `
def invoke(target = Holder.new)
  target.check(User.new)
end

Holder.User = Order
invoke()
`,
		},
		{
			name: "exact receiver in rescue",
			source: `
Holder.User = Order
begin
  klass = Holder
  raise "boom"
rescue RuntimeError
  klass.new.check(User.new)
end
`,
		},
		{
			name: "exact receiver in ensure",
			source: `
Holder.User = Order
begin
  klass = Holder
  raise "boom"
ensure
  klass.new.check(User.new)
end
`,
		},
		{
			name: "inert lambda preserves exact receiver",
			source: `
klass = Holder
unused = -> { klass = Order }
Holder.User = Order
klass.new.check(User.new)
`,
		},
		{
			name: "operator body",
			source: `
class Mutator
  def initialize()
  end

  def +(other)
    Holder.User = Order
    Holder.new.check(User.new)
    0
  end
end

Mutator.new + 0
`,
		},
		{
			name: "index body",
			source: `
class Mutator
  def initialize()
  end

  def [](index)
    Holder.User = Order
    Holder.new.check(User.new)
    0
  end
end

Mutator.new[0]
`,
		},
		{
			name: "index setter body",
			source: `
class Mutator
  def initialize()
  end

  def []=(index, value)
    Holder.User = Order
    Holder.new.check(User.new)
    value
  end
end

Mutator.new[0] = 1
`,
		},
		{
			name: "member setter body",
			source: `
class Mutator
  def initialize()
  end

  def trigger=(value)
    Holder.User = Order
    Holder.new.check(User.new)
    value
  end
end

Mutator.new.trigger = 1
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := MustNewEngine(Config{})
			script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+holder+tc.source, "run")
			if err != nil {
				t.Fatalf("CompileSnippet() error = %v", err)
			}
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckClassPredicateNarrowingChecksDynamicConstructorsAtCallState(t *testing.T) {
	t.Parallel()

	calls := []string{
		"[Holder][0].new(User.new)",
		"[Holder][0].public_send(:new, User.new)",
	}
	for _, call := range calls {
		t.Run(call, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
class Holder
  def initialize(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
    JSON.parse()
  end
end

def run()
  Holder.User = Order
  `+call+`
end
`)
			warnings := script.CheckWarningsForFunction("run")
			messages := make([]string, 0, len(warnings))
			for _, warning := range warnings {
				messages = append(messages, warning.Message)
			}
			got := strings.Join(messages, "\n")
			if !strings.Contains(got, "call to JSON.parse has too few arguments") {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want reachable initializer warning", "run", got)
			}
			stale := "call to takes_user argument value expected User, got Order"
			if strings.Contains(got, stale) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, do not want stale initializer warning %q", "run", got, stale)
			}
		})
	}
}

func TestCheckDynamicInitializeDispatchRespectsVisibility(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		call        string
		wantWarning bool
	}{
		{name: "explicit member", call: "holder.initialize(User.new)"},
		{name: "public send", call: "holder.public_send(:initialize, User.new)"},
		{name: "private send", call: "holder.send(:initialize, User.new)", wantWarning: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
class Holder
  def initialize(u)
    JSON.parse()
  end
end

def run(holder: Holder)
  `+tc.call+`
end
`)
			warnings := script.CheckWarningsForFunction("run")
			gotWarning := false
			for _, warning := range warnings {
				if strings.Contains(warning.Message, "call to JSON.parse has too few arguments") {
					gotWarning = true
				}
			}
			if gotWarning != tc.wantWarning {
				t.Fatalf("CheckWarningsForFunction(%q) initializer warning = %t, want %t: %#v", "run", gotWarning, tc.wantWarning, warnings)
			}
		})
	}
}

func TestCheckClassPredicateNarrowingPreservesPreDispatchWarnings(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+`
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

klass = Holder
klass.new.check(Order.new)
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_user argument value expected User, got Order")
}

func TestCheckClassPredicateNarrowingPreservesDynamicDispatchWarnings(t *testing.T) {
	t.Parallel()

	holder := `
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`
	cases := []struct {
		name   string
		source string
	}{
		{
			name:   "array class value receiver",
			source: "[Holder][0].new.check(Order.new)\n",
		},
		{
			name:   "conditional class value receiver",
			source: "(true ? Holder : Holder).new.check(Order.new)\n",
		},
		{
			name: "or-assigned class receiver skips rhs effects",
			source: `
def replace_holder_user()
  Holder.User = Order
  Order
end

klass = Holder
klass ||= replace_holder_user()
klass.new.check(Order.new)
`,
		},
		{
			name:   "hash class value receiver",
			source: "{key: Holder}[:key].new.check(Order.new)\n",
		},
		{
			name:   "send",
			source: "Holder.new.send(:check, Order.new)\n",
		},
		{
			name: "send before later mutation",
			source: `
Holder.new.send(:check, Order.new)
Holder.User = Order
`,
		},
		{
			name:   "public send",
			source: "Holder.new.public_send(:check, Order.new)\n",
		},
		{
			name: "bound callable argument",
			source: `
def invoke(f: function)
  f.call(Order.new)
end

invoke(Holder.new.check)
`,
		},
		{
			name: "conditional bound callable argument",
			source: `
def invoke(f: function)
  f.call(Order.new)
end

invoke(true ? Holder.new.check : Holder.new.check)
`,
		},
		{
			name: "default receiver",
			source: `
def invoke(target = Holder.new)
  target.check(Order.new)
end

invoke()
`,
		},
		{
			name: "explicit argument does not use default receiver",
			source: `
def invoke(target = Holder.new)
  target.check(Order.new)
end

invoke(1)
`,
		},
		{
			name: "unrelated opaque receiver",
			source: `
def identity(value)
  value
end

identity(1)
1.check(User.new)
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := MustNewEngine(Config{})
			script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+holder+tc.source, "run")
			if err != nil {
				t.Fatalf("CompileSnippet() error = %v", err)
			}
			requireCheckWarningContains(t, script, "call to takes_user argument value expected User, got Order")
		})
	}
}

func TestCheckClassPredicateNarrowingRespectsDynamicDispatchOverrides(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		override string
		call     string
	}{
		{
			name: "send",
			override: `
  def send(name, value)
    value
  end
`,
			call: "Holder.new.send(:check, User.new)\n",
		},
		{
			name: "public send",
			override: `
  def public_send(name, value)
    value
  end
`,
			call: "Holder.new.public_send(:check, User.new)\n",
		},
		{
			name: "itself",
			override: `
  def self.itself()
    Order
  end
`,
			call: "Holder.itself().new.check(User.new)\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := classPredicateNarrowingPrelude + `
class Holder
` + tc.override + `
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

Holder.User = Order
` + tc.call
			engine := MustNewEngine(Config{})
			script, err := engine.CompileSnippet(source, "run")
			if err != nil {
				t.Fatalf("CompileSnippet() error = %v", err)
			}
			requireCheckWarningContains(t, script, "call to takes_user argument value expected User, got Order")
		})
	}
}

func TestCheckClassPredicateNarrowingAppliesOnlyUsedDefaultEffects(t *testing.T) {
	t.Parallel()

	source := classPredicateNarrowingPrelude + `
class Holder
  def self.mutate()
    Holder.User = Order
  end

  def self.invoke(unused = mutate())
    u = Order.new
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end
`
	cases := []struct {
		name        string
		call        string
		wantWarning bool
	}{
		{name: "omitted", call: "Holder.invoke()\n"},
		{name: "explicit", call: "Holder.invoke(1)\n", wantWarning: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := MustNewEngine(Config{})
			script, err := engine.CompileSnippet(source+tc.call, "run")
			if err != nil {
				t.Fatalf("CompileSnippet() error = %v", err)
			}
			if tc.wantWarning {
				requireCheckWarningContains(t, script, "call to takes_user argument value expected User, got Order")
				return
			}
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckClassPredicateNarrowingAppliesDefaultEffectsBeforeBindingFailure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

def install(first = mutate_holder_user(Holder), later: int:)
end

class Holder
  def self.check(u: User | Order)
    begin
      install(later: "bad")
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(u: User | Order)
  Holder.check(u)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	got := strings.Join(checkWarningMessages(warnings), "\n")
	if !strings.Contains(got, "call to install argument later expected int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want binding warning", "run", got)
	}
	if strings.Contains(got, "call to takes_user argument value expected User, got Order") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want no narrowing warning", "run", got)
	}
}

func TestCheckClassPredicateNarrowingAppliesOpaqueCallbackDefaultEffectsBeforeBindingFailure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
class Holder
  def initialize()
  end

  def check(value: User | Order)
    unless value.is_a?(User)
      takes_order(value)
    end
  end
end

def install(callback: function, trigger = callback.call(), later: int:)
end

def run(callback: function)
  begin
    install(callback, later: "bad")
  rescue TypeError
    nil
  end
  Holder.new.check(User.new)
end
`)
	got := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
	for _, want := range []string{
		"call to install argument later expected int, got string",
		"call to takes_order argument value expected Order, got User",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
		}
	}
}

func TestCheckClassPredicateNarrowingAppliesDynamicDefaultEffectsBeforeBindingFailure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

def first(value = mutate_holder_user(Holder), later: int:)
end

def second(value = mutate_holder_user(Holder), later: int:)
end

class Holder
  def self.check(flag: bool, u: User | Order)
    callback = flag ? first : second
    begin
      callback.call(later: "bad")
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(flag: bool, u: User | Order)
  Holder.check(flag, u)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	got := strings.Join(checkWarningMessages(warnings), "\n")
	for _, want := range []string{
		"call to first.call argument later expected int, got string",
		"call to second.call argument later expected int, got string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
		}
	}
	if strings.Contains(got, "call to takes_user argument value expected User, got Order") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want no narrowing warning", "run", got)
	}
}

func TestCheckClassPredicateNarrowingAppliesSafeNavigationDefaultEffectsBeforeBindingFailure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

class Installer
  def install(first = mutate_holder_user(Holder), later: int:)
  end
end

class Holder
  def self.check(nullable: bool, raise_first: bool, u: User | Order)
    installer = nullable ? nil : Installer.new
    begin
      raise TypeError if raise_first
      installer&.install(later: "bad")
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(nullable: bool, raise_first: bool, u: User | Order)
  Holder.check(nullable, raise_first, u)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	got := strings.Join(checkWarningMessages(warnings), "\n")
	if !strings.Contains(got, "call to Installer#install argument later expected int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want binding warning", "run", got)
	}
	if strings.Contains(got, "call to takes_user argument value expected User, got Order") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want no narrowing warning", "run", got)
	}
}

func TestCheckClassPredicateNarrowingAppliesConditionalDefaultEffectsBeforeBindingFailure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

def install(first = mutate_holder_user(Holder), later: int:)
end

class Holder
  def self.check(explicit: bool, choose: bool, u: User | Order)
    begin
      raise TypeError if explicit
      choose ? install(later: "bad") : 1
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(explicit: bool, choose: bool, u: User | Order)
  Holder.check(explicit, choose, u)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	got := strings.Join(checkWarningMessages(warnings), "\n")
	if !strings.Contains(got, "call to install argument later expected int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want binding warning", "run", got)
	}
	if strings.Contains(got, "call to takes_user argument value expected User, got Order") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want no narrowing warning", "run", got)
	}
}

func TestCheckClassPredicateNarrowingKeepsCaughtSafeNavigationEffectsLocal(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

class Installer
  def install(first = mutate_holder_user(Holder), later: int:)
  end
end

class Holder
  def self.check(explicit: bool, present: bool, u: User | Order)
    installer = present ? Installer.new : nil
    begin
      raise TypeError if explicit
      installer&.install(later: "bad") rescue 1
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(explicit: bool, present: bool, u: User | Order)
  Holder.check(explicit, present, u)
end
`)
	got := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
	for _, want := range []string{
		"call to Installer#install argument later expected int, got string",
		"call to takes_user argument value expected User, got Order",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
		}
	}
}

func TestCheckClassPredicateNarrowingAppliesRescueFallbackDefaultEffects(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

def install(first = mutate_holder_user(Holder), later: int:)
end

def maybe(flag: bool)
  if flag
    raise TypeError
  end
  1
end

class Holder
  def self.check(explicit: bool, inner: bool, u: User | Order)
    begin
      raise TypeError if explicit
      maybe(inner) rescue install(later: "bad")
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(explicit: bool, inner: bool, u: User | Order)
  Holder.check(explicit, inner, u)
end
`)
	warnings := script.CheckWarningsForFunction("run")
	got := strings.Join(checkWarningMessages(warnings), "\n")
	if !strings.Contains(got, "call to install argument later expected int, got string") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want binding warning", "run", got)
	}
	if strings.Contains(got, "call to takes_user argument value expected User, got Order") {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want no narrowing warning", "run", got)
	}
}

func TestCheckClassPredicateNarrowingAppliesAutoCallDefaultEffectsBeforeBindingFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		source       string
		defaultCount int
	}{
		{
			name: "resolved",
			source: `
class Installer
  def initialize()
  end

  def install(first = mutate_holder_user(Holder), later: int = "bad")
  end
end

class Holder
  def self.check(u: User | Order)
    begin
      Installer.new.install
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(u: User | Order)
  Holder.check(u)
end
`,
			defaultCount: 1,
		},
		{
			name: "exact dynamic",
			source: `
class First
  def initialize()
  end

  def install(first = mutate_holder_user(Holder), later: int = "bad")
  end
end

class Second
  def initialize()
  end

  def install(first = mutate_holder_user(Holder), later: int = "bad")
  end
end

class Holder
  def self.check(flag: bool, u: User | Order)
    installer = flag ? First.new : Second.new
    begin
      installer.install
    rescue TypeError
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(flag: bool, u: User | Order)
  Holder.check(flag, u)
end
`,
			defaultCount: 2,
		},
		{
			name: "exact dynamic body",
			source: `
class First
  def initialize()
  end

  def install()
    mutate_holder_user(Holder)
  end
end

class Second
  def initialize()
  end

  def install()
    mutate_holder_user(Holder)
  end
end

class Holder
  def self.check(flag: bool, u: User | Order)
    installer = flag ? First.new : Second.new
    installer.install
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run(flag: bool, u: User | Order)
  Holder.check(flag, u)
end
`,
		},
		{
			name: "inexact dynamic body",
			source: `
class Mutator
  def initialize()
  end

  def install()
    Holder.User = Order
  end
end

class Holder
  def self.check(receiver, u: User | Order)
    receiver.install
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run(receiver, u: User | Order)
  Holder.check(receiver, u)
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end
`+tc.source)
			got := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
			defaultWarning := "default value for later expected int, got string"
			if count := strings.Count(got, defaultWarning); count != tc.defaultCount {
				t.Fatalf("CheckWarningsForFunction(%q) default warnings = %d, want %d: %q", "run", count, tc.defaultCount, got)
			}
			if unwanted := "call to takes_user argument value expected User, got Order"; strings.Contains(got, unwanted) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want no narrowing warning", "run", got)
			}
		})
	}
}

func TestCheckClassPredicateNarrowingIgnoresUnreachedEffectsAfterBindingFailure(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		definition string
		call       string
	}{
		{
			name: "body",
			definition: `def install(first = 1, later: int:)
  mutate_holder_user(Holder)
end`,
			call: `install(later: "bad")`,
		},
		{
			name: "later default",
			definition: `def bad()
  "bad"
end

def install(first: int = bad(), second = mutate_holder_user(Holder))
end`,
			call: `install()`,
		},
		{
			name: "shape failure",
			definition: `def install(required, later = mutate_holder_user(Holder))
end`,
			call: `install()`,
		},
		{
			name: "raising earlier default",
			definition: `def abort_default()
  raise TypeError, "boom"
end

def install(first = abort_default(), later = mutate_holder_user(Holder))
end`,
			call: `install()`,
		},
		{
			name: "recursive pure default",
			definition: `def install(first = install(later: 1), later: int:)
end`,
			call: `install(later: "bad")`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
def mutate_holder_user(k)
  k.User = Order
  1
end

`+tc.definition+`

class Holder
  def self.check(u: User | Order)
    begin
      `+tc.call+`
    rescue
      unless u.is_a?(User)
        takes_user(u)
      end
    end
  end
end

def run(u: User | Order)
  Holder.check(u)
end
`)
			got := strings.Join(checkWarningMessages(script.CheckWarningsForFunction("run")), "\n")
			want := "call to takes_user argument value expected User, got Order"
			if !strings.Contains(got, want) {
				t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
			}
		})
	}
}

func TestCheckClassPredicateNarrowingIgnoresUnusedDefaultCallEffects(t *testing.T) {
	t.Parallel()

	source := classPredicateNarrowingPrelude + `
def mutate_class_constant()
  User.Shadow = Order
end

def touch(value = mutate_class_constant())
  1
end

class Holder
  def self.check(u: User | Order)
    touch(1)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

def run(u: User | Order)
  Holder.check(u)
end
`
	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(source, "entry")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	warnings := script.CheckWarningsForFunction("run")
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	got := strings.Join(messages, "\n")
	want := "call to takes_user argument value expected User, got Order"
	if !strings.Contains(got, want) {
		t.Fatalf("CheckWarningsForFunction(%q) = %q, want substring %q", "run", got, want)
	}
}

func TestCheckClassPredicateNarrowingMergesFiniteReceiverCandidates(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+`
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

class OtherHolder
  def initialize()
  end

  def check(u)
    u
  end
end

def run(flag: bool, choose: bool)
  if flag
    klass = choose ? Holder : OtherHolder
  else
    klass = choose ? OtherHolder : Holder
  end
  Holder.User = Order
  klass.new.check(User.new)
end
`, "entry")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	if warnings := script.CheckWarningsForFunction("run"); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForFunction() = %#v, want none", warnings)
	}
}

func TestCheckClassPredicateNarrowingTracksClassMutationAcrossCalls(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		definition  string
		mutation    string
		wantWarning bool
	}{
		{
			name:        "direct class write",
			mutation:    "Holder.User = Order",
			wantWarning: true,
		},
		{
			name: "instance self class write",
			definition: `  def stash(value)
    self.class.User = value
  end
`,
			mutation:    "holder.stash(Order)",
			wantWarning: true,
		},
		{
			name: "included self class write",
			definition: `  include Mutator
`,
			mutation:    "holder.stash(Order)",
			wantWarning: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
module Mutator
  def stash(value)
    self.class.User = value
  end
end

class Holder
`+tc.definition+`
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_order(u)
    end
  end
end

def run()
  holder = Holder.new
  `+tc.mutation+`
  holder.check(User.new)
end
`)
			warnings := script.CheckWarningsForFunction("run")
			gotWarning := false
			for _, warning := range warnings {
				if strings.Contains(warning.Message, "call to takes_order argument value expected Order, got User") {
					gotWarning = true
				}
			}
			if gotWarning != tc.wantWarning {
				t.Fatalf("CheckWarningsForFunction() warning = %t, want %t: %#v", gotWarning, tc.wantWarning, warnings)
			}
		})
	}
}

func TestCheckClassPredicateNarrowingKeepsExactCallFactsBeforeClassMutation(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, classPredicateNarrowingPrelude+`
class Holder
  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_order(u)
    end
  end
end

def run()
  Holder.new.check(User.new)
end
`)
	if warnings := script.CheckWarningsForFunction("run"); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForFunction() = %#v, want none", warnings)
	}
}

func TestCheckDynamicModuleConstructorDoesNotReachInstanceMethods(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
module Factory
  def build(value)
    JSON.stringify = replacement
    takes_bool(value)
  end
end

def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def takes_bool(value: bool)
  value
end

def serialize()
  JSON.stringify({})
end

def run()
  target = Factory
  begin
    target.new.build("bad")
  rescue RuntimeError
    nil
  end
  takes_int(serialize())
end
`)
	warnings := script.CheckWarningsForFunction("run")
	intWarnings := 0
	boolWarnings := 0
	for _, warning := range warnings {
		if strings.Contains(warning.Message, "call to takes_int argument value expected int, got string") {
			intWarnings++
		}
		if strings.Contains(warning.Message, "call to takes_bool argument value expected bool, got string") {
			boolWarnings++
		}
	}
	if intWarnings != 1 || boolWarnings != 0 {
		t.Fatalf("CheckWarningsForFunction() = %#v, want one int warning and no bool warning", warnings)
	}
}

func TestCheckClassPredicateNarrowingRespectsClassSetters(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(classPredicateNarrowingPrelude+`
module Mutator
  def stash(value)
    self.class.User = value
  end
end

class Holder
  include Mutator

  def self.User=(value)
    1
  end

  def initialize()
  end

  def check(u: User | Order)
    unless u.is_a?(User)
      takes_user(u)
    end
  end
end

Holder.User = Order
Holder.new.check(Order.new)
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_user argument value expected User, got Order")
}

func TestCheckClassPredicateNarrowingKeepsClassBindingAcrossRequire(t *testing.T) {
	t.Parallel()

	engine := moduleTestEngine(t)
	script, err := engine.CompileSnippet(`
class Status
  def initialize()
  end
end

class Other
  def initialize()
  end
end

def takes_status(value: Status)
  value
end

class Holder
  def initialize()
  end

  def check(value: Status | Other)
    unless value.is_a?(Status)
      takes_status(value)
    end
  end
end

require("enum_status")
Holder.new.check(Other.new)
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet() error = %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_status argument value expected Status, got Other")
}
