package runtime

import "testing"

const constructorFactsPrelude = `
enum Status
  Active
end

class User
  def initialize()
  end

  def rename(name: string)
    name
  end

  def label() -> string
    "user"
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

def takes_int(value: int)
  value
end
`

func TestCheckConstructorNominalFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "constructor result contradicts other class",
			source: `
def run()
  takes_order(User.new)
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "fact flows through a local",
			source: `
def run()
  u = User.new
  takes_order(u)
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "fact flows through branches into a union",
			source: `
def takes_status(value: Status)
  value
end

def run(flag)
  u = flag ? User.new : Order.new
  takes_status(u)
end
`,
			warning: "call to takes_status argument value expected Status, got User | Order",
		},
		{
			name: "reassignment across classes is a contradiction",
			source: `
def run()
  u = User.new
  u = Order.new
end
`,
			warning: "reassignment of u expected User, got Order",
		},
		{
			name: "annotated return rejects the wrong class",
			source: `
def make() -> Order
  User.new
end
`,
			warning: "return value expected Order, got User",
		},
		{
			name: "reassignment to a disjoint kind",
			source: `
def run()
  u = User.new
  u = 5
end
`,
			warning: "reassignment of u expected User, got int",
		},
		{
			name: "instance method shape resolves from the fact",
			source: `
def run()
  u = User.new
  u.rename()
end
`,
			warning: "call to User#rename is missing argument name",
		},
		{
			name: "instance method argument types resolve from the fact",
			source: `
def run()
  u = User.new
  u.rename(5)
end
`,
			warning: "call to User#rename argument name expected string, got int",
		},
		{
			name: "instance method result flows to boundaries",
			source: `
def run()
  u = User.new
  takes_int(u.label())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "annotated parameter resolves methods too",
			source: `
def run(u: User)
  u.rename()
end
`,
			warning: "call to User#rename is missing argument name",
		},
		{
			name: "constructor without initialize still carries the fact",
			source: `
class Bare
end

def run()
  takes_order(Bare.new)
end
`,
			warning: "call to takes_order argument value expected Order, got Bare",
		},
		{
			name: "constructor splat keeps its invariant result",
			source: `
def run()
  takes_order(User.new(*[]))
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "non nil safe constructor keeps its result",
			source: `
def run()
  takes_order(User&.new)
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "non nil safe method keeps its annotated result",
			source: `
def run(user: User)
  takes_int(user&.label())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "bare method keeps its annotated result",
			source: `
def run(user: User)
  takes_int(user.label)
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "conditional nominal receiver resolves method shape",
			source: `
def run(flag)
  (flag ? User.new : User.new).rename()
end
`,
			warning: "call to User#rename is missing argument name",
		},
		{
			name: "annotated call receiver resolves method shape",
			source: `
def make_user() -> User
  User.new
end

def run()
  make_user().rename()
end
`,
			warning: "call to User#rename is missing argument name",
		},
		{
			name: "constructor after splat keeps runtime auto invocation",
			source: `
class Builder
  def initialize(required)
  end
end

def accept(*values: array<function>)
  values
end

def run()
  accept(*[], Builder.new)
end
`,
			warning: "call to Builder.new is missing argument required",
		},
		{
			name: "constructor in keyword splat keeps runtime auto invocation",
			source: `
class Builder
  def initialize(required)
  end
end

def accept(**options: hash<string, function>)
  options
end

def run()
  accept(**Builder.new)
end
`,
			warning: "call to Builder.new is missing argument required",
		},
		{
			name: "rescue does not propagate structured callable expectations",
			source: `
def accept(values: array<function>)
  values
end

def run()
  accept(([User.new] rescue [User.new]))
end
`,
			warning: "call to accept argument values expected array<function>, got array<User>",
		},
		{
			name: "non bindable member still auto invokes under callable expectation",
			source: `
def accept(fn: function)
  fn
end

def run()
  accept([1].at)
end
`,
			warning: "call to array.at has too few arguments",
		},
		{
			name: "expected branch inference narrows the receiver",
			source: `
def run(flag: bool)
  u = flag ? User.new : nil
  takes_int(u ? u.label() : "s")
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, constructorFactsPrelude+tc.source), tc.warning)
		})
	}
}

func TestCheckConstructorNominalFactsStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "same class boundary stays silent",
			source: `
def run()
  u = User.new
  takes_user(u)
end
`,
		},
		{
			name: "shadowed class name stays dynamic",
			source: `
def pick()
  1
end

def run()
  User = pick()
  u = User.new
  takes_order(u)
end
`,
		},
		{
			name: "dynamic constructor dispatch stays unknown",
			source: `
def pick()
  User
end

def run()
  klass = pick()
  u = klass.new
  takes_order(u)
end
`,
		},
		{
			name: "nullable fact keeps methods dynamic",
			source: `
def run(flag)
  u = nil
  if flag
    u = User.new
  end
  u.rename()
end
`,
		},
		{
			name: "module annotations keep methods dynamic",
			source: `
module Nameable
  def display_name
    "n"
  end
end

def run(n: Nameable)
  n.display_name(1, 2, 3)
end
`,
		},
		{
			name: "constructor member on module is not an instance fact",
			source: `
module Factory
end

def run()
  takes_order(Factory.new)
end
`,
		},
		{
			name: "bare constructor remains callable under a function boundary",
			source: `
def takes_function(value: function)
  value
end

def run()
  takes_function(User.new)
end
`,
		},
		{
			name: "callable constructor is not auto invoked for shape checking",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_function(value: function)
  value
end

def run()
  takes_function(Builder.new)
end
`,
		},
		{
			name: "conditional constructors inherit callable expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_function(value: function)
  value
end

def run(flag)
  takes_function(flag ? Builder.new : Builder.new)
end
`,
		},
		{
			name: "if constructors inherit callable expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_function(value: function)
  value
end

def run(flag)
  takes_function(if flag then Builder.new else Builder.new end)
end
`,
		},
		{
			name: "case constructors inherit callable expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_function(value: function)
  value
end

def run(flag)
  takes_function(case flag when true then Builder.new else Builder.new end)
end
`,
		},
		{
			name: "array constructors inherit callable element expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_functions(values: array<function>)
  values
end

def run()
  takes_functions([Builder.new])
end
`,
		},
		{
			name: "shape constructors inherit callable field expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_options(options: { callback: function })
  options
end

def run()
  takes_options({ callback: Builder.new })
end
`,
		},
		{
			name: "conditional arrays retain callable element expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_functions(values: array<function>)
  values
end

def run(flag)
  takes_functions(flag ? [Builder.new] : [Builder.new])
end
`,
		},
		{
			name: "constructor default inherits callable expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def takes_default(value: function = Builder.new)
  value
end

def run()
  takes_default()
end
`,
		},
		{
			name: "collapsed options hash inherits callable field expectation",
			source: `
class Builder
  def initialize(required)
  end
end

def accept(options: { cb: function })
  options
end

def run()
  accept cb: Builder.new
end
`,
		},
		{
			name: "nullable safe method remains callable when dispatch runs",
			source: `
class Worker
  def build(required) -> string
    required
  end
end

def accept(fn: function)
  fn
end

def run(worker: Worker?)
  accept(worker&.build)
end
`,
		},
		{
			name: "bare builtin identifier remains callable",
			source: `
def accept(fn: function)
  fn
end

def run()
  accept(rand)
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, constructorFactsPrelude+tc.source))
		})
	}
}
