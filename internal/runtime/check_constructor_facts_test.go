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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, constructorFactsPrelude+tc.source))
		})
	}
}
