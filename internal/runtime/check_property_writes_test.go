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

// A property expectation shapes evaluation as well as inference. Storing a
// bare callable, including inside a typed literal, must not walk it as an
// auto-call and erase the definitely-unset facts of other initializer ivars.
func TestCheckCallablePropertyWriteWalkUsesExpectation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "bare callable",
			source: `
class Holder
  property a: int
  property b: int
  property cb: function

  def initialize
    @cb = seed
    @a = @b
  end

  def seed
    @b = 1
  end
end
`,
		},
		{
			name: "callable in typed literal",
			source: `
class Holder
  property a: int
  property b: int
  property callbacks: array<function>

  def initialize
    @callbacks = [seed]
    @a = @b
  end

  def seed
    @b = 1
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), "write to @a expected int, got nil")
		})
	}

	// A non-callable contract retains normal auto-call semantics: seed runs
	// before the later read and may initialize @b.
	requireNoCheckWarnings(t, compileScriptDefault(t, `
class Holder
  property a: int
  property b: int
  property value: int

  def initialize
    @value = seed
    @a = @b
  end

  def seed
    @b = 1
    1
  end
end
`))
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

// A literal right-hand side makes the rest split deterministic: the rest
// ivar receives the materialized window as an array, fixed targets before
// and trailing targets after the rest map to concrete indices (padding with
// nil when the literal runs short), and only non-literal sources degrade to
// unknown.
func TestCheckRestDestructuredIvarWrites(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize
    head, *@name = [0, 1, 2]
    head
  end
end
`), "write to @name expected string, got array")

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property tags: array<string>

  def initialize
    head, *@tags = ["a", 1, 2]
    head
  end
end
`), "write to @tags expected array<string>")

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property count: int

  def trailing
    *rest, @count = [1, "x"]
    rest
  end
end
`), "write to @count expected int, got string")

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property count: int

  def padded_trailing
    a, *rest, @count = [1]
    [a, rest]
  end
end
`), "write to @count expected int, got nil")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property count: int
  property tags: array<string>

  def good
    @count, *@tags = [1, "a", "b"]
  end

  def unknown_source(items)
    head, *@tags = items
    head
  end
end
`))
}

// A short literal right-hand side pads the missing fixed targets with nil at
// runtime, so a padded ivar target is a known nil write: it warns against a
// non-nullable contract and stays quiet for nullable ones. Extra literal
// elements beyond the targets are dropped and keep the mapped checks.
func TestCheckPaddedDestructuredIvarWrites(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string

  def initialize
    @name, other = []
    other
  end
end
`), "write to @name expected string, got nil")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property nickname: string?
  property count: int

  def initialize
    @nickname, other = []
    other
  end

  def extras
    @count, @nickname = 1, "n", :extra
  end
end
`))

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property count: int

  def extras_bad
    @count, other = "x", 2, 3
    other
  end
end
`), "write to @count expected int, got string")
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

// Logical-assignment right-hand sides run only when the current ivar picks
// the assignment branch. A proven short circuit preserves other unset ivar
// facts; a proven or possible assignment still walks the RHS effects.
func TestCheckLogicalIvarWriteWalkRespectsShortCircuit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning bool
	}{
		{
			name: "or short-circuits",
			source: `
class User
  property a: int
  property b: int
  property c: int

  def initialize
    @b = 1
    @b ||= seed
    @a = @c
  end

  def seed
    @c = 1
    1
  end
end
`,
			warning: true,
		},
		{
			name: "and short-circuits",
			source: `
class User
  property a: int
  property b: int?
  property c: int

  def initialize
    @b &&= seed
    @a = @c
  end

  def seed
    @c = 1
    1
  end
end
`,
			warning: true,
		},
		{
			name: "or executes",
			source: `
class User
  property a: int
  property b: int?
  property c: int

  def initialize
    @b ||= seed
    @a = @c
  end

  def seed
    @c = 1
    1
  end
end
`,
		},
		{
			name: "and executes",
			source: `
class User
  property a: int
  property b: int?
  property c: int

  def initialize
    @b = 1
    @b &&= seed
    @a = @c
  end

  def seed
    @c = 1
    1
  end
end
`,
		},
		{
			name: "unknown may execute",
			source: `
class User
  property a: int
  property b: int?
  property c: int

  def initialize(value)
    @b = value
    @b ||= seed
    @a = @c
  end

  def seed
    @c = 1
    1
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.source)
			if tc.warning {
				requireCheckWarningContains(t, script, "write to @a expected int, got nil")
				return
			}
			requireNoCheckWarnings(t, script)
		})
	}
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

// Class.new allocates an empty ivar map before initialize runs, so every
// typed property reads as definitely nil until its first write. Ordinary
// methods retain the declared-type-or-nil entry fact because either state is
// possible when they are called.
func TestCheckInitializerIvarFactsStartUnset(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    @a = @b
  end
end
`), "write to @a expected int, got nil")

	// Binding a local is not a call and cannot initialize an instance
	// variable as a side effect. Reading a known scalar or dispatching one of
	// its pure registered members cannot do so either.
	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    x = 1
    x
    x.to_s
    @a = @b
  end
end
`), "write to @a expected int, got nil")

	// Container contracts do not produce stable ordinary-method facts, but
	// their initial value is still exactly nil on a fresh instance.
	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property name: string
  property tags: array<string>

  def initialize
    @name = @tags
  end
end
`), "write to @name expected string, got nil")

	requireCheckWarningContains(t, compileScriptDefault(t, `
module State
  property b: int
end

class User
  include State
  property a: int

  def initialize
    @a = @b
  end
end
`), "write to @a expected int, got nil")

	requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(@a = @b, @b = 1)
  end
end
`), "default value for @a expected int, got nil")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def copy
    @a = @b
  end
end
`))
}

// Once code may have written an initializer ivar, the constructor-only nil
// fact must not survive. Direct container writes and ivar parameters become
// unknown because container interiors are not stable facts, while calls and
// repeated regions conservatively widen any still-unset facts.
func TestCheckInitializerIvarFactsWidenAfterPossibleWrites(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property copy: array<string>
  property tags: array<string>

  def initialize
    @tags = []
    @copy = @tags
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def seed
  User.current.b = 1
end

class User
  property a: int
  property b: int

  def self.current
    @@current
  end

  def initialize
    @@current = self
    seed
    @a = @b
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property copy: array<string>
  property tags: array<string>

  def initialize(@tags)
    @copy = @tags
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    seed
    @a = @b
  end

  def seed
    @b = 1
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    self.b = 1
    @a = @b
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(values)
    for value in values
      @b = value
    end
    @a = @b
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(values)
    values.each do |value|
      @b = value
    end
    @a = @b
  end
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    yield self
    @a = @b
  end
end

def make
  User.new do |user|
    user.b = 1
  end
end
`))
}
