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

// Direct writes and ivar parameter defaults infer their value under the
// property contract expectation, mirroring the runtime: a bare callable
// assigned to a function-typed property is stored un-invoked, so its
// auto-invoked result type must not warn.
func TestCheckCallableContractWritesStayQuiet(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class Holder
  property cb: function

  def initialize(@cb = rand)
  end

  def set
    @cb = rand
  end
end
`))

	requireCheckWarningContains(t, compileScriptDefault(t, `
class Holder
  property cb: function

  def set
    @cb = 5
  end
end
`), "write to @cb expected function, got int")

	requireCheckWarningContains(t, compileScriptDefault(t, `
class Holder
  property cb: function

  def initialize(@cb = 5)
  end
end
`), "default value for @cb expected function, got int")
}

// Ivar parameter facts bind at each parameter's own position, so an earlier
// default reads later ivars as still unset while a later default sees the
// earlier binding.
func TestCheckIvarDefaultBindingOrder(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class Holder
  property a: string?
  property b: int

  def initialize(@a = @b, @b = 1)
  end
end
`))

	requireCheckWarningContains(t, compileScriptDefault(t, `
class Holder
  property a: string?
  property b: int

  def initialize(@b = 1, @a = @b)
  end
end
`), "default value for @a expected string?, got int")
}

// Class-method ivar parameters never write instance ivars at runtime — the
// binding only defines a local when self is not an instance — so property
// contracts do not apply to their arguments, annotations, or defaults.
func TestCheckClassMethodIvarParamsSkipContracts(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property name: string

  def self.build(@name: int = 2)
    name
  end
end

def make
  User.build(1)
end
`))
}

// Store-contract checks resolve method ownership even when the first call
// checked is reached before any function scope initialized the ownership
// maps, such as a constructor call in a class body.
func TestCheckClassBodyConstructorStoreContract(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(@name)
  end
end

class Registry
  DEFAULT = User.new(1)
end
`), "call to User.new argument name expected string, got int")
}

// Instance variables inside destructuring targets check like bare ivar
// writes when the element values are known, and stay gradual otherwise.
func TestCheckDestructuredIvarWrites(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class Counter
  property count: int
  property label: string

  def initialize
    @count, @label = 1, 2
  end
end
`), "write to @label expected string, got int")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class Counter
  property count: int
  property label: string

  def initialize
    @count, @label = 1, "c"
  end

  def adopt(pair)
    @count, @label = pair
  end

  def spread(items)
    @count, *rest = items
    rest
  end
end
`))
}

// An ||= write on a falsey (in particular unset) property assigns the RHS
// through the same runtime guard as a plain write, so a provably
// incompatible RHS warns; compatible and callable-preserving spellings stay
// quiet.
func TestCheckOrAssignIvarWrites(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class Counter
  property seed: string

  def default_bad
    @seed ||= 2
  end
end
`), "write to @seed expected string, got int")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class Counter
  property seed: string
  property cb: function

  def defaults
    @seed ||= "s"
    @cb ||= rand
  end
end
`))
}

// Logical ivar writes consult the current fact's truthiness: a definitely
// truthy fact makes &&= always assign (so its RHS checks) and makes ||=
// always short-circuit (so its RHS never warns).
func TestCheckLogicalIvarWritesRespectTruthiness(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def clobber
    @name = "ok"
    @name &&= 1
  end
end
`), "write to @name expected string, got int")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property name: string

  def keep
    @name = "ok"
    @name ||= 1
  end

  def maybe_skip
    @name &&= 1
  end
end
`))
}

// A skipped &&= write leaves an unset property nil, so the fact keeps its
// nil arm and the falsey branch stays reachable for diagnostics.
func TestCheckAndAssignKeepsNilArm(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def check
    @name &&= "x"
    if @name
      "set"
    else
      1 + nil
    end
  end
end
`), "unsupported addition operands int and nil")
}

// A literal write validates through normalization, not only kind
// disjointness: a symbol that names no member of the declared enum warns
// even though symbols as a kind coerce into enums.
func TestCheckTypedPropertyLiteralWriteNormalizes(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
enum Status
  Draft
  Published
end

class Post
  property status: Status

  def initialize
    @status = :bogus
  end
end
`), "write to @status expected Status")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
enum Status
  Draft
  Published
end

class Post
  property status: Status

  def initialize
    @status = :published
  end
end
`))
}

// An annotated ivar parameter's call sites check the property store contract
// as well as the annotation: a value can satisfy the annotation and still
// provably fail the ivar store at binding.
func TestCheckAnnotatedIvarParamStoreContract(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(@name: string | int)
  end
end

def make
  User.new(1)
end
`), "call to User.new argument name expected string, got int")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(@name: string | int)
  end
end

def make
  User.new("ada")
end
`))
}

// An ivar parameter default checks against the property contract with the
// facts of the parameters bound before it, matching the runtime's binding
// order.
func TestCheckIvarParamDefaultAfterPriorParams(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(seed: int = 1, @name = seed)
  end
end
`), "default value for @name expected string, got int")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize(seed: string = "s", @name = seed)
  end
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
