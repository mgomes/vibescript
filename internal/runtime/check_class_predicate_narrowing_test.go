package runtime

import "testing"

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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, classPredicateNarrowingPrelude+tc.source), tc.warning)
		})
	}
}

func TestCheckClassPredicateNarrowingStaysGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
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
			name: "unknown receiver stays unknown",
			source: `
def run(u)
  if u.is_a?(User)
    takes_order(u)
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, classPredicateNarrowingPrelude+tc.source))
		})
	}
}
