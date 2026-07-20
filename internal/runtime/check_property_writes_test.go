package runtime

import "testing"

// The checker seeds instance-method analysis with the contracts of typed
// accessor-backed instance variables and rejects direct writes whose known
// value is provably incompatible; unknown values pass and rely on the
// runtime guard.

func TestCheckTypedPropertyWriteContradictions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "ordinary method literal write",
			source: `
class User
  property name: string

  def corrupt
    @name = 1
  end
end
`,
			warning: "write to @name expected string, got int",
		},
		{
			name: "constructor literal write",
			source: `
class User
  property name: string

  def initialize
    @name = :oops
  end
end
`,
			warning: "write to @name expected string, got symbol",
		},
		{
			name: "getter only contract",
			source: `
class User
  getter age: int

  def wipe
    @age = "old"
  end
end
`,
			warning: "write to @age expected int, got string",
		},
		{
			name: "setter only contract",
			source: `
class User
  setter tag: string

  def retag
    @tag = false
  end
end
`,
			warning: "write to @tag expected string, got bool",
		},
		{
			name: "nullable property wrong arm",
			source: `
class User
  property nickname: string?

  def set
    @nickname = 5
  end
end
`,
			warning: "write to @nickname expected string?, got int",
		},
		{
			name: "included module accessor",
			source: `
module Named
  property name: string
end

class Robot
  include Named

  def rename
    @name = 9
  end
end
`,
			warning: "write to @name expected string, got int",
		},
		{
			name: "annotated param flows into write",
			source: `
class User
  property name: string

  def rename(value: int)
    @name = value
  end
end
`,
			warning: "write to @name expected string, got int",
		},
		{
			name: "known local flows into write",
			source: `
class User
  property name: string

  def rename
    value = 1
    @name = value
  end
end
`,
			warning: "write to @name expected string, got int",
		},
		{
			name: "container property literal write",
			source: `
class User
  property tags: array<string>

  def wipe
    @tags = 5
  end
end
`,
			warning: "write to @tags expected array<string>, got int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckTypedPropertyWriteStaysGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "unknown value passes",
			source: `
class User
  property name: string

  def rename(value)
    @name = value
  end
end
`,
		},
		{
			name: "compatible literal passes",
			source: `
class User
  property name: string

  def initialize
    @name = "ada"
  end
end
`,
		},
		{
			name: "untyped property stays dynamic",
			source: `
class Grab
  property bag

  def initialize
    @bag = 1
    @bag = "two"
  end
end
`,
		},
		{
			name: "undeclared ivar stays dynamic",
			source: `
class Grab
  def initialize
    @stash = 1
    @stash = "two"
  end
end
`,
		},
		{
			name: "nullable property accepts nil",
			source: `
class User
  property nickname: string?

  def clear
    @nickname = nil
  end
end
`,
		},
		{
			name: "compound writes stay quiet",
			source: `
class Counter
  property count: int

  def initialize
    @count = 0
  end

  def bump
    @count += 1
    @count = @count + 1
  end
end
`,
		},
		{
			name: "symbol coerces into declared enum",
			source: `
enum Status
  Draft
  Published
end

class Post
  property status: Status

  def initialize
    @status = :draft
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

// Ivar parameters are direct writes too: a known argument bound to an
// unannotated @ivar parameter checks against the property contract, an
// annotation that contradicts the contract warns at the definition, and the
// bound ivar fact drops the entry nil arm.
func TestCheckIvarParameterPropertyContracts(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(@name)
  end
end

def make
  User.new(1)
end
`), "call to User.new argument name expected string, got int")

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(@name: int)
  end
end
`), "write to @name expected string, got int")

	requireCheckWarningContains(t, compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property name: string

  def initialize(@name)
    takes_int(@name)
  end
end
`), "call to takes_int argument value expected int, got string")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(@name)
  end
end

def make(value)
  User.new(value)
  User.new("ada")
end
`))

	// Ivar parameters without a typed accessor contract stay dynamic.
	requireNoCheckWarnings(t, compileScriptDefault(t, `
class Point
  def initialize(@x, @y)
  end
end

def make
  Point.new(1, "two")
end
`))
}

// Reads observe the seeded contract: an unwritten typed ivar reads as the
// declared type or nil, and a checked write drops the entry nil arm.
func TestCheckTypedPropertyReadFacts(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property name: string

  def leak
    takes_int(@name)
  end
end
`), "call to takes_int argument value expected int, got string | nil")

	requireCheckWarningContains(t, compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property name: string

  def leak
    @name = "ada"
    takes_int(@name)
  end
end
`), "call to takes_int argument value expected int, got string")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def takes_string(value: string)
  value
end

class User
  property name: string

  def pass
    takes_string(@name)
  end
end
`))

	// Reads of undeclared or untyped ivars stay unknown.
	requireNoCheckWarnings(t, compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class Grab
  property bag

  def pass
    takes_int(@bag)
    takes_int(@stash)
  end
end
`))
}
