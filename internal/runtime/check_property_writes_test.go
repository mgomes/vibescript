package runtime

import (
	"context"
	"io"
	"strings"
	"testing"
)

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

func TestCheckConditionalMemberReceiverDoesNotShapeRHS(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  property callback: function
end

class User
  property a: int
  property b: int

  def initialize(left: Box, right: Box, flag: bool)
    (flag ? left : right).callback = seed
    @a = @b
  end

  def seed
    @b = 1
  end
end
`)
	requireNoCheckWarnings(t, script)
}

func TestCheckUnannotatedCallReceiverDoesNotShapeRHS(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  property callback: function
end

def make_box
  Box.new
end

class User
  property a: int
  property b: int

  def initialize
    make_box().callback = seed
    @a = @b
  end

  def seed
    @b = 1
  end
end
`)
	requireNoCheckWarnings(t, script)
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

func TestCheckDestructuredIvarWritesUseRetainedStaticArray(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Pair
  property a: int
  property b: int

  def initialize
    values = ["bad", 2]
    @a, @b = values
  end
end

def run
  Pair.new.a
end
`)
	requireCheckWarningContains(t, script, "write to @a expected int, got string")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @a expected int, got string",
	)

	script = compileScriptDefault(t, `
class Pair
  property a: int
  property b: int

  def initialize
    values = [1, 2]
    @a, @b = values
  end
end

def run
  Pair.new.b
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 2 {
		t.Fatalf("run() = %v, want 2", got)
	}

	script = compileScriptDefault(t, `
class Pair
  property a: int
  property b: int

  def initialize(flag: bool)
    values = flag ? ["bad", 2] : [1, 2]
    @a, @b = values
  end
end

def run(flag: bool)
  Pair.new(flag).a
end
`)
	requireNoCheckWarnings(t, script)
	got = callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewBool(false)},
		CallOptions{},
	)
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run(false) = %v, want 1", got)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewBool(true)},
		CallOptions{},
		"instance variable @a expected int, got string",
	)
}

func TestCheckDestructuredIvarWritesUseEvaluatedCallableFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		property string
		write    string
		want     string
	}{
		{
			name:     "array element",
			property: "callback: function",
			write:    "@callback, ignored = [factory.make, 1]",
			want:     "write to @callback expected function, got string",
		},
		{
			name:     "scalar",
			property: "callback: function",
			write:    "@callback, ignored = factory.make",
			want:     "write to @callback expected function, got string",
		},
		{
			name:     "rest array",
			property: "callbacks: array<function>",
			write:    "*@callbacks = [factory.make]",
			want:     "write to @callbacks expected array<function>, got array<string>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Factory
  def make() -> string
    "not callable"
  end
end

class User
  property `+tc.property+`

  def initialize
    factory = Factory.new()
    `+tc.write+`
  end
end

def run
  User.new()
end
`)

			requireCheckWarningContains(t, script, tc.want)
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				strings.TrimPrefix(tc.want, "write to "),
			)
		})
	}

	t.Run("implicit self array element", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property callback: function

  def make() -> string
    "not callable"
  end

  def initialize
    @callback, ignored = [make, 1]
  end
end

def run
  User.new()
end
`)

		requireCheckWarningContains(
			t,
			script,
			"write to @callback expected function, got string",
		)
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @callback expected function, got string",
		)
	})
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

// A scalar literal right-hand side becomes a one-element destructuring list:
// the first target receives the literal and every later fixed target receives
// nil. Dynamic sources stay gradual because they may evaluate to an array.
func TestCheckScalarDestructuredIvarWrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "incompatible first target",
			source: `
class User
  property name: string

  def initialize
    @name, other = 1
    other
  end
end
`,
			warning: "write to @name expected string, got int",
		},
		{
			name: "padded fixed target",
			source: `
class User
  property count: int
  property name: string

  def initialize
    @count, @name = 1
  end
end
`,
			warning: "write to @name expected string, got nil",
		},
		{
			name: "compatible scalar and padding",
			source: `
class User
  property count: int
  property nickname: string?

  def initialize
    @count, @nickname = 1
  end
end
`,
		},
		{
			name: "unknown source",
			source: `
class User
  property name: string

  def initialize(value)
    @name, other = value
    other
  end
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.source)
			if tc.warning != "" {
				requireCheckWarningContains(t, script, tc.warning)
				return
			}
			requireNoCheckWarnings(t, script)
		})
	}
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

// A property guard rejects its value before mutating the ivar. Failure paths
// therefore retain the pre-store fact, including across destructuring, so a
// rescue write is checked against the value the runtime actually preserved.
func TestCheckFailedIvarWritesPreserveFailureFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write string
	}{
		{name: "plain", write: "      @label = 1"},
		{name: "destructured", write: "      @label, ignored = [1, 0]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Counter
  property label: string

  def probe
    begin
`+tc.write+`
    rescue
      @label ||= 2
    end
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 2 {
				t.Fatalf("CheckWarnings() = %#v, want two rejected writes", warnings)
			}
			for _, warning := range warnings {
				if warning.Message != "write to @label expected string, got int" {
					t.Fatalf("CheckWarnings() = %#v, want only rejected label writes", warnings)
				}
			}
		})
	}
}

func TestCheckPossibleIvarWriteFailuresPreserveFailureFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write string
	}{
		{name: "plain", write: `      @label = flag ? "ok" : 1`},
		{name: "destructured", write: `      @label, ignored = [flag ? "ok" : 1, 0]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def takes_string(value: string)
  value
end

class Counter
  property label: string

  def initialize(flag: bool)
    begin
`+tc.write+`
    rescue
      takes_string(@label)
    end
  end
end
`)
			warnings := script.CheckWarnings()
			const want = "call to takes_string argument value expected string, got nil"
			if len(warnings) != 1 || warnings[0].Message != want {
				t.Fatalf("CheckWarnings() = %#v, want only %q", warnings, want)
			}
		})
	}
}

// Logical and compound assignments still pass their derived value through
// the property guard. A guard that cannot complete terminates that execution
// arm, so later unreachable diagnostics must not leak through it.
func TestCheckRejectedIvarSettersStopControlFlow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "or assign",
			source: `
class Counter
  property label: string

  def initialize
    @label ||= 1
    takes_int("bad")
  end
end
`,
			warning: "write to @label expected string, got int",
		},
		{
			name: "and assign",
			source: `
class Counter
  property label: string

  def probe
    @label = "ok"
    @label &&= 1
    takes_int("bad")
  end
end
`,
			warning: "write to @label expected string, got int",
		},
		{
			name: "compound rescue",
			source: `
class Counter
  property count: int

  def probe
    @count = 1
    begin
      @count += 1.5
    rescue
      @count = "bad"
    end
    takes_int("bad")
  end
end
`,
			warning: "write to @count expected int, got string",
		},
		{
			name: "possible or assign keeps the skipped truthy arm",
			source: `
class Counter
  property label: string

  def probe
    @label ||= 1
    if @label
      "set"
    else
      1 + nil
    end
  end
end
`,
			warning: "write to @label expected string, got int",
		},
		{
			name: "possible and assign keeps the skipped falsey arm",
			source: `
class Counter
  property flag: bool

  def probe
    @flag &&= 1
    if @flag
      1 + nil
    else
      takes_int("bad")
    end
  end
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "successful or assign joins its exact write with the skipped arm",
			source: `
class Counter
  property flag: bool

  def probe
    @flag ||= false
    if @flag
      takes_int("bad")
    end
  end
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end
`+tc.source)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 || warnings[0].Message != tc.warning {
				t.Fatalf("CheckWarnings() = %#v, want only %q", warnings, tc.warning)
			}
		})
	}
}

// A compound target is captured before its RHS evaluates. Even if an inline
// lambda mutates the same ivar, the operator still sees the original value;
// an invalid operator remains quiet but terminates that execution path.
func TestCheckCompoundIvarTargetPrecedesRHSEffects(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class Counter
  property value: bool | int

  def probe
    @value = true
    @value += (-> { @value = 1; true }.call() ? 1 : 1)
    takes_int("bad")
  end
end
`))
}

func TestCheckCompoundIvarRHSTypeFollowsRHSEffects(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def replacement(value)
  1
end

def takes_string(value: string)
  value
end

class Counter
  property count: int

  def probe
    @count = 1
    @count += (-> { JSON.stringify = replacement; true }.call() ? JSON.stringify({}) : JSON.stringify({}))
    takes_string(@count)
  end
end
`)
	const want = "call to takes_string argument value expected string, got int"
	warnings := script.CheckWarnings()
	if len(warnings) != 1 || warnings[0].Message != want {
		t.Fatalf("CheckWarnings() = %#v, want only %q", warnings, want)
	}
}

// A rejected logical store propagates through rescue and ensure like its
// runtime property guard: the body stops at the store, rescue sees the
// failure, and ensure still executes before the method exits.
func TestCheckRejectedIvarSetterFlowsThroughRescueAndEnsure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def takes_string(value: string)
  value
end

def takes_bool(value: bool)
  value
end

class Counter
  property label: string

  def initialize
    begin
      @label ||= 1
      takes_int("body")
    rescue
      takes_string(2)
    ensure
      takes_bool(3)
    end
  end
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 3 {
		t.Fatalf("CheckWarnings() = %#v, want the store, rescue, and ensure warnings", warnings)
	}
	want := map[string]bool{
		"write to @label expected string, got int":                     false,
		"call to takes_string argument value expected string, got int": false,
		"call to takes_bool argument value expected bool, got int":     false,
	}
	for _, warning := range warnings {
		if _, ok := want[warning.Message]; !ok {
			t.Fatalf("CheckWarnings() = %#v, unexpected warning %q", warnings, warning.Message)
		}
		want[warning.Message] = true
	}
	for message, found := range want {
		if !found {
			t.Fatalf("CheckWarnings() = %#v, missing %q", warnings, message)
		}
	}
}

// When only one logical-assignment arm evaluates the RHS or setter, a
// failure snapshot carries that selected arm into rescue. The skipped arm
// remains the only ordinary continuation.
func TestCheckLogicalIvarFailureSnapshotsUseTheWritingArm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rhs  string
		want []string
	}{
		{
			name: "setter rejects",
			rhs:  "1",
			want: []string{
				"write to @label expected string, got int",
				"call to takes_string argument value expected string, got nil",
			},
		},
		{
			name: "rhs aborts",
			rhs:  "1 + nil",
			want: []string{
				"unsupported addition operands int and nil",
				"call to takes_string argument value expected string, got nil",
			},
		},
		{
			name: "setter may reject",
			rhs:  `(flag ? "ok" : 1)`,
			want: []string{
				"call to takes_string argument value expected string, got nil",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def takes_string(value: string)
  value
end

class Counter
  property label: string

  def probe(flag: bool)
    begin
      @label ||= `+tc.rhs+`
    rescue
      takes_string(@label)
    end
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != len(tc.want) {
				t.Fatalf("CheckWarnings() = %#v, want %q", warnings, tc.want)
			}
			for i, warning := range warnings {
				if warning.Message != tc.want[i] {
					t.Fatalf("CheckWarnings()[%d] = %q, want %q", i, warning.Message, tc.want[i])
				}
			}
		})
	}
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
  property flag: bool

  def keep
    @name = "ok"
    @name ||= 1
  end

  def maybe_skip
    @name &&= 1
  end

  def keep_literal_bool
    @flag = true
    @flag ||= 1
    @flag = false
    @flag &&= 1
  end
end
`))
}

func TestCheckLogicalIvarWritesPreserveLiteralNilTruthiness(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int?
  property b: int
  property c: int

  def initialize
    @a = nil
    @a &&= -> { @b = 1 }.call()
    @c = @b
  end
end

def run
  User.new()
end
`)
	requireCheckWarningContains(t, script, "write to @c expected int, got nil")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @c expected int, got nil",
	)
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
			name: "literal true or short-circuits",
			source: `
class User
  property a: int
  property c: int
  property flag: bool

  def initialize
    @flag = true
    @flag ||= seed
    @a = @c
  end

  def seed
    @c = 1
    true
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
			name: "literal false and short-circuits",
			source: `
class User
  property a: int
  property c: int
  property flag: bool

  def initialize
    @flag = false
    @flag &&= seed
    @a = @c
  end

  def seed
    @c = 1
    false
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

func TestCheckTypedPropertyRetainedStaticValueNormalizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		write   string
		warning string
	}{
		{
			name:    "direct ivar write",
			write:   "@status = value",
			warning: "write to @status expected Status, got symbol",
		},
		{
			name:    "generated setter",
			write:   "self.status = value",
			warning: "argument value expected Status, got symbol",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
enum Status
  Draft
end

class User
  property status: Status

  def initialize
    value = :missing
    `+test.write+`
  end
end

def run
  User.new.status
end
`)
			requireCheckWarningContains(t, script, test.warning)
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"expected Status, got symbol",
			)
		})
	}

	script := compileScriptDefault(t, `
enum Status
  Draft
end

class User
  property status: Status

  def initialize
    value = :draft
    @status = value
  end
end

def run
  User.new.status
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("run() = %v, want Status::Draft", got)
	}

	script = compileScriptDefault(t, `
enum Status
  Draft
end

class User
  property status: Status

  def initialize(flag: bool)
    value = flag ? :draft : :missing
    @status = value
  end
end

def run(flag: bool)
  User.new(flag).status
end
`)
	requireNoCheckWarnings(t, script)
	got = callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewBool(true)},
		CallOptions{},
	)
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("run(true) = %v, want Status::Draft", got)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewBool(false)},
		CallOptions{},
		"expected Status, got symbol",
	)
}

func TestCheckTypedPropertyRetainedFalsePreservesTruthiness(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool

  def initialize
    value = false
    @flag = value
    if @flag
      takes_int("bad")
    end
  end
end

def run
  User.new.flag
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindBool || got.Bool() {
		t.Fatalf("run() = %v, want false", got)
	}
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

func TestCheckTypedIvarParamStoreUsesNormalizedValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       string
		checkWarning string
	}{
		{
			name: "supplied argument",
			source: `
enum Status
  Draft
end

class User
  property status: symbol

  def initialize(@status: Status)
  end
end

def build
  User.new(:draft)
end
`,
			checkWarning: "call to User.new argument status expected symbol, got Status",
		},
		{
			name: "default argument",
			source: `
enum Status
  Draft
end

class User
  property status: symbol

  def initialize(@status: Status = :draft)
  end
end

def build
  User.new
end
`,
			checkWarning: "default value for @status expected symbol, got Status",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, test.source)
			requireCheckWarningContains(t, script, test.checkWarning)
			requireCallErrorContains(
				t,
				script,
				"build",
				nil,
				CallOptions{},
				"instance variable @status expected symbol, got Status",
			)
		})
	}
}

func TestCheckTypedIvarParamRetainedStaticValueNormalizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "supplied argument",
			source: `
enum Status
  Draft
end

class User
  property status: Status

  def initialize(@status: symbol)
  end
end

def run
  value = :missing
  User.new(value).status
end
`,
			warning: "call to User.new argument status expected Status, got symbol",
		},
		{
			name: "call-specific default",
			source: `
enum Status
  Draft
end

class User
  property status: Status

  def initialize(value: symbol, @status: Status = value)
  end
end

def run
  User.new(:missing).status
end
`,
			warning: "default value for status expected Status, got symbol",
		},
		{
			name: "call-specific property default",
			source: `
enum Status
  Draft
end

class User
  property status: Status

  def initialize(value: symbol, @status: symbol = value)
  end
end

def run
  User.new(:missing).status
end
`,
			warning: "default value for @status expected Status, got symbol",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, test.source)
			requireCheckWarningContains(t, script, test.warning)
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"expected Status, got symbol",
			)
		})
	}

	script := compileScriptDefault(t, `
enum Status
  Draft
end

class User
  property status: Status

  def initialize(@status: symbol)
  end
end

def run
  value = :draft
  User.new(value).status
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindEnumValue || valueEnumValue(got).Name != "Draft" {
		t.Fatalf("run() = %v, want Status::Draft", got)
	}
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

func TestCheckIvarParamDefaultUsesOneEvaluatedValueForBothContracts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def make()
  "bad"
end

def replacement(value)
  1
end

def takes_int(value: int)
  value
end

class Installer
  property callback: function | int

  def initialize(@callback: string | int = make)
    JSON.stringify = replacement
  end
end

def run()
  begin
    Installer.new()
  rescue
    nil
  end
  takes_int(JSON.stringify({}))
end
`)
	requireCheckWarningContains(
		t,
		script,
		"call to takes_int argument value expected int, got string",
	)
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"argument value expected int, got string",
	)
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

func TestCheckCompoundIndexSetterWidensInitializerIvarFacts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(@box: User? = nil)
    if @box
      for index in [0]
        @box[index] += 1
      end
      @a = @b
    else
      @b = 0
      @a = @b
    end
  end

  def [](index)
    0
  end

  def []=(index, value)
    @b = value
  end
end

def run
  user = User.new
  user.send(:initialize, user)
  user.a
end
`)
	requireNoCheckWarnings(t, script)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

// Repeated regions preserve definitely-unset initializer ivars they cannot
// write. Direct writes widen only their target, while calls that may mutate
// self still widen every unset ivar conservatively.
func TestCheckInitializerIvarFactsRespectRepeatedRegionEffects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		parameters string
		properties string
		region     string
		methods    string
		warning    bool
	}{
		{
			name: "pure for",
			region: `    for x in [1]
      x
    end`,
			warning: true,
		},
		{
			name: "builtin binary operator",
			region: `    for x in [1]
      1 + 2
    end`,
			warning: true,
		},
		{
			name:       "builtin index read",
			parameters: "(items: array<int>)",
			region: `    for x in [1]
      items[0]
    end`,
			warning: true,
		},
		{
			name: "literal hash index read",
			region: `    for x in [1]
      { value: 1 }[:value]
    end`,
			warning: true,
		},
		{
			name: "empty Hash constructor",
			region: `    for x in [1]
      Hash.new()
    end`,
			warning: true,
		},
		{
			name: "pure block",
			region: `    [1].fetch(2) do
      1
    end`,
			warning: true,
		},
		{
			name: "pure typed block parameter",
			region: `    [1].fetch(2) do |index: int|
      index
    end`,
			warning: true,
		},
		{
			name: "ignored literal block",
			region: `    (1..2).to_a do
      @b = 1
    end`,
			warning: true,
		},
		{
			name:    "ignored forwarded block",
			region:  `    (1..2).to_a(&seed)`,
			methods: initializerIvarSeedMethod("value", "value"),
			warning: true,
		},
		{
			name: "assigned lambda stays inert",
			region: `    for x in [1]
      callback = -> { @b = 1 }
    end`,
			warning: true,
		},
		{
			name: "standalone lambda stays inert",
			region: `    for x in [1]
      -> { @b = 1 }
    end`,
			warning: true,
		},
		{
			name: "nested lambda stays inert",
			region: `    for x in [1]
      callbacks = [-> { @b = 1 }]
    end`,
			warning: true,
		},
		{
			name: "nested hash lambda stays inert",
			region: `    for x in [1]
      callbacks = { run: -> { @b = 1 } }
    end`,
			warning: true,
		},
		{
			name: "selected lambda stays inert",
			region: `    for x in [1]
      callback = true ? -> { @b = 1 } : nil
    end`,
			warning: true,
		},
		{
			name: "short circuit lambda stays inert",
			region: `    for x in [1]
      callback = nil || -> { @b = 1 }
    end`,
			warning: true,
		},
		{
			name: "lambda truthiness stays inert",
			region: `    for x in [1]
      ignored = !(-> { @b = 1 })
    end`,
			warning: true,
		},
		{
			name: "interpolated lambda stays inert",
			region: `    for x in [1]
      ignored = "#{-> { @b = 1 }}"
    end`,
			warning: true,
		},
		{
			name: "named lambda constructor stays inert",
			region: `    for x in [1]
      callback = lambda do
        @b = 1
      end
    end`,
			warning: true,
		},
		{
			name: "proc constructor stays inert",
			region: `    for x in [1]
      callback = proc do
        @b = 1
      end
    end`,
			warning: true,
		},
		{
			name: "Proc new constructor stays inert",
			region: `    for x in [1]
      callback = Proc.new do
        @b = 1
      end
    end`,
			warning: true,
		},
		{
			name: "Hash default constructor stays inert",
			region: `    for x in [1]
      defaults = Hash.new do |hash, key|
        @b = 1
      end
    end`,
			warning: true,
		},
		{
			name: "forwarded lambda constructor stays inert",
			region: `    callback = -> { @b = 1 }
    for x in [1]
      lambda(&callback)
    end`,
			warning: true,
		},
		{
			name: "forwarded proc constructor stays inert",
			region: `    callback = -> { @b = 1 }
    for x in [1]
      proc(&callback)
    end`,
			warning: true,
		},
		{
			name: "forwarded Hash default constructor stays inert",
			region: `    callback = -> { @b = 1 }
    for x in [1]
      Hash.new(&callback)
    end`,
			warning: true,
		},
		{
			name: "stored lambda invocation may write observed ivar",
			region: `    for x in [1]
      callback = -> { @b = 1 }
      callback.call()
    end`,
		},
		{
			name: "immediate lambda invocation may write observed ivar",
			region: `    for x in [1]
      -> { @b = 1 }.call()
    end`,
		},
		{
			name: "immediate named lambda invocation may write observed ivar",
			region: `    for x in [1]
      lambda { @b = 1 }.call()
    end`,
		},
		{
			name: "hash default block may run on lookup",
			region: `    for x in [1]
      Hash.new { |hash, key| @b = 1 }[:missing]
    end`,
		},
		{
			name:       "typed hash default block may run on lookup",
			parameters: "(@defaults: hash<symbol, int> = Hash.new { |hash, key| @b = 1 })",
			region: `    for x in [1]
      @defaults[:missing]
    end`,
		},
		{
			name: "statically skipped safe call",
			region: `    for x in [1]
      nil&.missing(seed)
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name: "statically skipped safe member",
			region: `    for x in [1]
      nil&.missing
    end`,
			warning: true,
		},
		{
			name: "short-circuited call",
			region: `    for x in [1]
      true || seed
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name: "unreachable if branch",
			region: `    for x in [1]
      if false
        seed
      end
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name: "unreachable expression branches",
			region: `    for x in [1]
      false ? seed : 1
      value = if false
        seed
      else
        1
      end
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name: "unreachable nested loop",
			region: `    for x in [1]
      while false
        seed
      end
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name: "call after break",
			region: `    for x in [1]
      break
      seed
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name:       "assigned ivar enables branch mutation",
			properties: "  property flag: bool\n",
			region: `    for x in [1]
      @flag = true
      if @flag
        @b = 1
      end
    end`,
		},
		{
			name: "assigned local enables branch mutation",
			region: `    for x in [nil]
      x = true
      if x
        @b = 1
      end
    end`,
		},
		{
			name:       "assigned ivar enables logical mutation",
			properties: "  property flag: bool\n",
			region: `    for x in [1]
      @flag = true
      @flag && seed
    end`,
			methods: initializerIvarSeedMethod("", "1"),
		},
		{
			name:       "later iteration enables nested mutation",
			properties: "  property flag: bool\n",
			region: `    for x in [1, 2]
      while @flag
        @b = 1
        break
      end
      @flag = true
    end`,
		},
		{
			name:       "assigned safe receiver runs block",
			properties: "  property flag: bool\n",
			region: `    for x in [1]
      @flag = true
      @flag&.tap do
        @b = 1
      end
    end`,
		},
		{
			name:       "for writes unrelated ivar",
			properties: "  property c: int\n",
			region: `    for x in [1]
      @c = x
    end`,
			warning: true,
		},
		{
			name:       "for destructures unrelated ivar",
			properties: "  property c: int\n",
			region: `    for x in [1]
      @c, ignored = [x, 2]
    end`,
			warning: true,
		},
		{
			name:       "block writes unrelated ivar",
			properties: "  property c: int\n",
			region: `    [1].fetch(2) do
      @c = 1
    end`,
			warning: true,
		},
		{
			name: "for writes observed ivar",
			region: `    for x in [1]
      @b = x
    end`,
		},
		{
			name: "for destructures observed ivar",
			region: `    for x in [1]
      @b, ignored = [x, 2]
    end`,
		},
		{
			name: "block writes observed ivar",
			region: `    [1].fetch(2) do
      @b = 1
    end`,
		},
		{
			name: "for may call mutator",
			region: `    for x in [1]
      seed
    end`,
			methods: initializerIvarSeedMethod("", "1"),
		},
		{
			name: "block may call mutator",
			region: `    [1].fetch(2) do
      seed
    end`,
			methods: initializerIvarSeedMethod("", "1"),
		},
		{
			name: "later while condition may call mutator",
			region: `    flag = 1
    while flag || seed
      flag = nil
      next
    end`,
			methods: `
  def seed
    @b = 1
    false
  end
`,
		},
		{
			name: "later until condition may call mutator",
			region: `    flag = nil
    until flag && seed
      flag = 1
      next
    end`,
			methods: initializerIvarSeedMethod("", "1"),
		},
		{
			name: "single pass loop skips later condition",
			region: `    flag = 1
    while flag || seed
      break
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name:       "terminating branches skip later condition",
			parameters: "(stop: bool)",
			region: `    flag = 1
    while flag || seed
      if stop
        return
      end
      break
    end`,
			methods: initializerIvarSeedMethod("", "1"),
			warning: true,
		},
		{
			name:    "forwarded block may call mutator",
			region:  `    [1].fetch(2, &seed)`,
			methods: initializerIvarSeedMethod("index", "index + 1"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := `
class User
  property a: int
  property b: int
` + tc.properties + `
  def initialize` + tc.parameters + `
` + tc.region + `
    @a = @b
  end
` + tc.methods + `
end
`
			script := compileScriptDefault(t, source)
			if tc.warning {
				requireCheckWarningContains(t, script, "write to @a expected int, got nil")
				return
			}
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckInitializerIvarEffectsWidenExactBooleanFacts(t *testing.T) {
	t.Parallel()

	t.Run("repeated write", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property flag: bool
  property a: int
  property b: int

  def initialize
    @flag = false
    for value in [1]
      @flag = true
    end
    if @flag
      @b = 1
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})

	t.Run("same self call", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool

  def set_flag
    @flag = true
  end

  def initialize
    @flag = false
    set_flag()
    if @flag
      takes_int("bad")
    end
  end
end

def run
  User.new()
end
`)
		requireCheckWarningContains(
			t,
			script,
			"call to takes_int argument value expected int, got string",
		)
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"argument value expected int, got string",
		)
	})

	t.Run("unrelated self call", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool
  property b: int

  def set_b
    @b = 1
  end

  def initialize
    @flag = false
    set_b
    if @flag
      takes_int("bad")
    end
  end
end

def run
  User.new().b
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})

	t.Run("assignment call bypasses target binding", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool
  property b: int

  def touch
    @b = 1
  end

  def initialize
    @flag = false
    touch = nil
    touch = touch()
    if @flag
      takes_int("bad")
    end
  end
end

def run
  User.new().flag
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindBool || got.Bool() {
			t.Fatalf("run() = %v, want false", got)
		}
	})

	for _, tc := range []struct {
		name       string
		statements string
	}{
		{
			name: "plain",
			statements: `    touch = nil
    touch = touch()`,
		},
		{
			name: "or assignment",
			statements: `    touch = nil
    touch ||= touch()`,
		},
		{
			name: "and assignment",
			statements: `    touch = true
    touch &&= touch()`,
		},
		{
			name: "compound assignment",
			statements: `    touch = 1
    touch += touch()`,
		},
	} {
		t.Run("assignment bypass effects/"+tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def touch
    @b = 1
  end

  def initialize(fail: bool)
`+tc.statements+`
    if fail
      @a = @c
    else
      @a = @b
    end
  end
end

def run
  User.new(false).a
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @a expected int, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
			}
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}

	t.Run("rejected self write", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool

  def set_flag
    @flag = "bad"
  end

  def initialize
    @flag = false
    begin
      set_flag()
    rescue
      nil
    end
    if @flag
      takes_int("bad")
    end
  end
end

def run
  User.new().flag
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			warnings[0].Message != "write to @flag expected bool, got string" {
			t.Fatalf("CheckWarnings() = %#v, want only the rejected @flag write", warnings)
		}
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindBool || got.Bool() {
			t.Fatalf("run() = %v, want false", got)
		}
	})

	for _, tc := range []struct {
		name       string
		method     string
		invocation string
	}{
		{name: "builtin call", method: "puts", invocation: "puts()"},
		{name: "builtin value", method: "puts", invocation: "puts"},
		{name: "block predicate call", method: "block_given?", invocation: "block_given?()"},
		{name: "block predicate value", method: "block_given?", invocation: "block_given?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithConfig(t, Config{OutputWriter: io.Discard}, `
def takes_int(value: int)
  value
end

class User
  property flag: bool

  def `+tc.method+`
    @flag = true
  end

  def initialize
    @flag = false
    `+tc.invocation+`
    unless @flag
      takes_int("bad")
    end
  end
end

def run
  User.new()
end
`)
			requireCheckWarningContains(
				t,
				script,
				"call to takes_int argument value expected int, got string",
			)
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"argument value expected int, got string",
			)
		})
	}
}

func TestCheckInitializerIvarBlockConstructorBodiesRemainChecked(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		constructor string
	}{
		{name: "lambda", constructor: `lambda { @b = "bad" }`},
		{name: "proc", constructor: `proc { @b = "bad" }`},
		{name: "Proc new", constructor: `Proc.new { @b = "bad" }`},
		{name: "Hash new", constructor: `Hash.new { |hash, key| @b = "bad" }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property b: int

  def initialize
    `+tc.constructor+`
  end
end
`), "write to @b expected int, got string")
		})
	}
}

func TestCheckBlockConstructorBodyUsesCurrentCapturedFacts(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def escape(value)
  value
end

def takes_int(value: int)
  value
end

def run
  values = ["s"]
  escape(values)
  proc { takes_int(values[0]) }
end
	`))
}

func TestCheckStoredBlockConstructorEffectsStayGradual(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		constructor string
		invoke      string
	}{
		{
			name:        "proc",
			constructor: `proc { value = "s"; JSON.stringify = replacement }`,
			invoke:      `handler.call()`,
		},
		{
			name:        "Proc new",
			constructor: `Proc.new { value = "s"; JSON.stringify = replacement }`,
			invoke:      `handler.call()`,
		},
		{
			name:        "Hash new",
			constructor: `Hash.new { |hash, key| value = "s"; JSON.stringify = replacement }`,
			invoke:      `handler[:missing]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def replacement(value)
  1
end

def takes_string(value: string)
  value
end

def takes_int(value: int)
  value
end

def run
  value = 1
  handler = `+tc.constructor+`
  `+tc.invoke+`
  takes_string(value)
  takes_int(JSON.stringify({}))
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckRejectedStoredBlockCallDoesNotApplyBodyEffects(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def replacement(value)
  1
end

def takes_string(value: string)
  value
end

def run
  handler = proc { JSON.stringify = replacement }
  begin
    handler.call(value: 1)
  rescue
    nil
  end
  takes_string(JSON.stringify({}))
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindString {
		t.Fatalf("run() = %v, want string", got)
	}
}

func TestCheckStoredProcAutosplatBindingEffects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		invocation string
		wantB      int64
	}{
		{
			name: "proc autosplats before nested typed binding",
			invocation: `proc { |((value: int): array<int>), ignored: string|
        @b = 1
      }.call([[[1]], "ok"])`,
			wantB: 1,
		},
		{
			name: "repeated proc call keeps unrelated ivar exact",
			invocation: `for item in [1]
        proc { |((value: int): array<int>), ignored: string|
          @b = 1
        }.call([[[item]], "ok"])
      end`,
			wantB: 1,
		},
		{
			name: "lambda keeps strict arity and does not autosplat",
			invocation: `callback = lambda { |((value: int): array<int>), ignored: string|
        @b = 1
      }
      callback.call([[[1]], "ok"])`,
			wantB: 0,
		},
		{
			name: "single proc parameter does not autosplat",
			invocation: `proc { |((value: int): array<int>)|
        @b = 1
      }.call([[[1]], "ok"])`,
			wantB: 0,
		},
		{
			name: "proc stops before body on nested type failure",
			invocation: `proc { |((value: string): array<int>), ignored: string|
        @b = 1
      }.call([[[1]], "ok"])`,
			wantB: 0,
		},
		{
			name: "proc stops before body on later parameter failure",
			invocation: `proc { |((value: int): array<int>), ignored: int|
        @b = 1
      }.call([[[1]], "ok"])`,
			wantB: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property b: int
  property flag: bool

  def initialize
    @b = 0
    @flag = false
    begin
      `+tc.invocation+`
    rescue
      nil
    end
    if @flag
      takes_int("bad")
    end
  end
end

def run
  user = User.new()
  [user.b, user.flag]
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			compareArrays(t, got, []Value{NewInt(tc.wantB), NewBool(false)})
		})
	}
}

func TestCheckStoredProcCallsUseRetainedArgumentExpectations(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		setup         string
		invocation    string
		falseMayWrite bool
	}{
		{
			name:       "positional",
			setup:      `    callback = proc { |fn: function| nil }`,
			invocation: `    callback.call(seed)`,
		},
		{
			name:       "destructure",
			setup:      `    callback = proc { |(fn: function)| nil }`,
			invocation: `    callback.call([seed])`,
		},
		{
			name:       "destructure rest",
			setup:      `    callback = proc { |(head: int, *fns: array<function>)| nil }`,
			invocation: `    callback.call([1, seed])`,
		},
		{
			name:       "proc autosplat",
			setup:      `    callback = proc { |fn: function, ignored: int| nil }`,
			invocation: `    callback.call([seed, 1])`,
		},
		{
			name:       "keyword",
			setup:      `    callback = proc { |fn: function| nil }`,
			invocation: `    callback.call(fn: seed)`,
		},
		{
			name: `mixed exact alternatives`,
			setup: `    callback = flag ?
      proc { |fn: function| nil } :
      proc { |value: int| nil }`,
			invocation:    `    callback.call(seed)`,
			falseMayWrite: true,
		},
		{
			name: `mixed exact destructure alternatives`,
			setup: `    callback = flag ?
      proc { |(fn: function)| nil } :
      proc { |(value: int)| nil }`,
			invocation:    `    callback.call([seed])`,
			falseMayWrite: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def seed
    @b = 1
    7
  end

  def initialize(flag: bool)
`+tc.setup+`
    begin
`+tc.invocation+`
    rescue
      nil
    end
    @a = @b
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				[]Value{NewBool(true)},
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
			if tc.falseMayWrite {
				got := callScript(
					t,
					context.Background(),
					script,
					"run",
					[]Value{NewBool(false)},
					CallOptions{},
				)
				if got.Kind() != KindInt || got.Int() != 1 {
					t.Fatalf("run(false) = %v, want 1", got)
				}
			}
		})
	}
}

func TestRetainedBlockArgumentExpectationKeepsCallableAcrossLargeUnion(t *testing.T) {
	t.Parallel()

	types := []*TypeExpr{
		checkTypeInt,
		checkTypeFloat,
		checkTypeString,
		checkTypeBool,
		checkTypeSymbol,
		checkTypeNil,
		checkTypeFunction,
	}
	blocks := make([]capturedBlockLiteralValue, 0, len(types))
	for _, ty := range types {
		blocks = append(blocks, capturedBlockLiteralValue{
			block: &BlockLiteral{Params: []Param{{Name: "value", Type: ty}}},
		})
	}

	got := retainedBlockPositionalArgumentExpectation(blocks, 0, 1)
	if !got.includesCallable() {
		t.Fatalf("retainedBlockPositionalArgumentExpectation() = %s, want callable expectation", formatTypeExpr(got.ty))
	}
}

func TestCheckStoredProcArgumentExpectationBoundaries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		setup      string
		invocation string
	}{
		{
			name:       "non-callable parameter",
			setup:      `    callback = proc { |value: int| nil }`,
			invocation: `    callback.call(seed)`,
		},
		{
			name:       "positional splat",
			setup:      `    callback = proc { |fn: function| nil }`,
			invocation: `    callback.call(*[seed])`,
		},
		{
			name:       "keyword splat",
			setup:      `    callback = proc { |fn: function| nil }`,
			invocation: `    callback.call(**{ fn: seed })`,
		},
		{
			name: `non-callable exact alternatives`,
			setup: `    callback = flag ?
      proc { |value: int| nil } :
      proc { |value: string| nil }`,
			invocation: `    callback.call(seed)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def seed
    @b = 1
    7
  end

  def initialize(flag: bool)
`+tc.setup+`
    begin
`+tc.invocation+`
    rescue
      nil
    end
    @a = @b
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
			requireNoCheckWarnings(t, script)
			for _, flag := range []bool{false, true} {
				got := callScript(
					t,
					context.Background(),
					script,
					"run",
					[]Value{NewBool(flag)},
					CallOptions{},
				)
				if got.Kind() != KindInt || got.Int() != 1 {
					t.Fatalf("run(%t) = %v, want 1", flag, got)
				}
			}
		})
	}
}

func TestCheckStoredBlockCallUsesEvaluatedCalleeIdentity(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def replacement_int(value)
  1
end

def takes_int(value: int)
  value
end

def run
  callback = proc { |ignored| nil }
  callback.call(-> {
    callback = proc { |ignored| JSON.stringify = replacement_int }
    nil
  }.call())
  takes_int(JSON.stringify({}))
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"argument value expected int, got string",
	)
}

func TestCheckCapturedBlockConstructorIdentityDoesNotEscapeValidationWalk(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run
  Hash.new do |hash, key|
    callback = proc { nil }
    Hash.new do |nested, nested_key|
      nil
    end
  end
end
`)
	fn := script.functions["run"]
	outerStatement, ok := fn.Body[0].(*ExprStmt)
	if !ok {
		t.Fatalf("run body statement = %T, want *ExprStmt", fn.Body[0])
	}
	outer, ok := outerStatement.Expr.(*CallExpr)
	if !ok || outer.Block == nil {
		t.Fatalf("run body expression = %T, want blocked *CallExpr", outerStatement.Expr)
	}
	procStatement, ok := outer.Block.Body[0].(*AssignStmt)
	if !ok {
		t.Fatalf("outer block statement 0 = %T, want *AssignStmt", outer.Block.Body[0])
	}
	procCall, ok := procStatement.Value.(*CallExpr)
	if !ok {
		t.Fatalf("proc assignment value = %T, want *CallExpr", procStatement.Value)
	}
	hashStatement, ok := outer.Block.Body[1].(*ExprStmt)
	if !ok {
		t.Fatalf("outer block statement 1 = %T, want *ExprStmt", outer.Block.Body[1])
	}
	hashCall, ok := hashStatement.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("nested Hash.new expression = %T, want *CallExpr", hashStatement.Expr)
	}

	checker := scriptChecker{
		script:          script,
		typeRoot:        checkTypeRoot(script, nil),
		runtimeTypeRoot: checkTypeRoot(script, nil),
		evaluatedBlockValues: map[Expression][]capturedBlockLiteralValue{
			outer: {{block: outer.Block}},
		},
		evaluatedHashDefaults: map[Expression][]directCoreHashDefaultCapture{
			outer: {{freshEmpty: true}},
		},
	}
	checker.checkCapturedBlockLiteral("run", outer.Block, false)
	if len(checker.evaluatedBlockValues) != 1 ||
		len(checker.evaluatedBlockValues[outer]) != 1 {
		t.Fatalf(
			"evaluatedBlockValues = %#v, want only the enclosing evaluation fact",
			checker.evaluatedBlockValues,
		)
	}
	if len(checker.evaluatedHashDefaults) != 1 ||
		len(checker.evaluatedHashDefaults[outer]) != 1 {
		t.Fatalf(
			"evaluatedHashDefaults = %#v, want only the enclosing evaluation fact",
			checker.evaluatedHashDefaults,
		)
	}

	globals := map[string]Value{
		"Hash": NewInt(1),
		"proc": NewInt(1),
	}
	checker.optionGlobals = globals
	checker.optionGlobalsOverride = true
	checker.hostGlobals = checkHostGlobals(globals)
	checker.typeRoot = checkTypeRoot(script, globals)
	checker.runtimeTypeRoot = checkTypeRoot(script, globals)
	if values, exact := checker.capturedBlockLiteralValueAlternatives(procCall); exact {
		t.Fatalf("capturedBlockLiteralValueAlternatives() = %#v, want shadowed proc", values)
	}
	if defaults, exact := checker.captureDirectCoreHashDefaults(hashCall); exact {
		t.Fatalf("captureDirectCoreHashDefaults() = %#v, want shadowed Hash.new", defaults)
	}
}

func TestCheckInitializerIvarBlockConstructorsDoNotExportFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		constructor string
	}{
		{name: "lambda", constructor: `lambda { @b = 1 }`},
		{name: "proc", constructor: `proc { @b = 1 }`},
		{name: "Proc new", constructor: `Proc.new { @b = 1 }`},
		{name: "Hash new", constructor: `Hash.new { |hash, key| @b = 1 }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    `+tc.constructor+`
    @a = @b
  end
end
`), "write to @a expected int, got nil")
		})
	}
}

func TestCheckInitializerIvarBlockConstructorShadowing(t *testing.T) {
	t.Parallel()

	t.Run("script lambda", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def lambda(&callback)
  callback.call()
end

class User
  property a: int
  property b: int

  def initialize
    for x in [1]
      lambda do
        @b = 1
      end
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})

	t.Run("host Hash new", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for x in [1]
      Hash.new do
        @b = 1
      end
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
		hostHash := NewObject(map[string]Value{
			"new": NewBuiltin(
				"Hash.new",
				func(
					exec *Execution,
					_ Value,
					_ []Value,
					_ map[string]Value,
					block Value,
				) (Value, error) {
					return exec.CallBlock(block, nil)
				},
			),
		})
		options := CallOptions{Globals: map[string]Value{"Hash": hostHash}}
		requireNoCheckWarningsWithOptions(t, script, options)
		got := callScript(t, context.Background(), script, "run", nil, options)
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})
}

func TestCheckInitializerIvarLambdaInvocationContexts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{name: "index", expression: `      @invoker[-> { @b = 1 }]`},
		{name: "binary operator", expression: `      @invoker + -> { @b = 1 }`},
		{name: "compound operator", expression: `      @invoker += -> { @b = 1 }`},
		{name: "stored callback operator", expression: `      @invoker + @callback`},
		{name: "stored callback index", expression: `      @invoker[@callback]`},
		{
			name: "wrapped binary operator",
			expression: `      @invoker + (begin
        -> { @b = 1 }
      end)`,
		},
		{
			name: "wrapped index",
			expression: `      @invoker[(begin
        -> { @b = 1 }
      end)]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Invoker
  def [](callback)
    callback.call()
  end

  def +(callback)
    callback.call()
    self
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int
  property callback: function

  def initialize(@invoker)
    @callback = -> { @b = 1 }
    for x in [1]
`+tc.expression+`
    end
    @a = @b
  end
end

def run
  User.new(Invoker.new()).a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarCurrentSelfSetterWidensOnlyWrittenFact(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize(flag: bool)
    self.b = 1
    if flag
      @a = @b
    else
      @a = @c
    end
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
	}

	got := callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewBool(true)},
		CallOptions{},
	)
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run(true) = %v, want 1", got)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewBool(false)},
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarCurrentSelfOperatorWidensOnlyWrittenFact(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize(flag: bool)
    self + 1
    if flag
      @a = @b
    else
      @a = @c
    end
  end

  def +(value: int)
    @b = value
    self
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
	}

	got := callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewBool(true)},
		CallOptions{},
	)
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run(true) = %v, want 1", got)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewBool(false)},
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarCurrentSelfIndexWidensOnlyWrittenFact(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize(flag: bool)
    self[1]
    if flag
      @a = @b
    else
      @a = @c
    end
  end

  def [](value: int)
    @b = value
    self
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
	}

	got := callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewBool(true)},
		CallOptions{},
	)
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run(true) = %v, want 1", got)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewBool(false)},
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarCurrentSelfDispatchBindingPrefixes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
		method     string
	}{
		{
			name:       "binary operator",
			expression: "      self + 1",
			method: `  def +(@b: int, failure: int = stop_now())
    self
  end`,
		},
		{
			name:       "index getter",
			expression: "      self[1]",
			method: `  def [](@b: int, failure: int = stop_now())
    self
  end`,
		},
		{
			name:       "member setter",
			expression: "      self.b = 1",
			method: `  def b=(@b: int, failure: int = stop_now())
    @b
  end`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def stop_now
  raise "stop"
end

class User
  property a: int
  property b: int

  def initialize
    begin
`+tc.expression+`
    rescue
      @a = @b
    end
  end

`+tc.method+`
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarDefaultPrefixKeepsCallerLambdaOwnership(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def stop_now
  raise "stop"
end

class Invoker
  def +(callback, own: -> { @c = "set" }, trigger: callback.call(), own_run: own.call(), failure: stop_now())
    self
  end
end

class User
  property a: int
  property b: int
  property s: string
  property c: string

  def initialize(invoker: Invoker)
    begin
      invoker + -> { @b = 1 }
    rescue
      @a = @b
      @s = @c
    end
  end
end

def run
  User.new(Invoker.new())
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @s expected string, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want only the callee-owned @c warning", warnings)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @s expected string, got nil",
	)
}

func TestCheckInitializerIvarNestedSelfDispatchesRemainConservative(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		invoke  string
		methods string
	}{
		{
			name:   "bare implicit self helper",
			invoke: "    self + 1",
			methods: `  def +(value: int)
    seed
    self
  end

  def seed
    @b = 1
  end`,
		},
		{
			name:   "same-class receiver may alias self",
			invoke: "    self + self",
			methods: `  def +(other: User)
    other.seed()
    self
  end

  def seed
    @b = 1
  end`,
		},
		{
			name:   "nested overloaded operator",
			invoke: "    self + 1",
			methods: `  def +(value: int)
    self - value
    self
  end

  def -(value: int)
    @b = value
    self
  end`,
		},
		{
			name:   "nested index getter",
			invoke: "    self + 1",
			methods: `  def +(value: int)
    self[0]
    self
  end

  def [](index: int)
    @b = 1
    index
  end`,
		},
		{
			name:   "nested index setter",
			invoke: "    self + 1",
			methods: `  def +(value: int)
    self[0] = value
    self
  end

  def []=(index: int, value: int)
    @b = value
  end`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
`+tc.invoke+`
    @a = @b
  end

`+tc.methods+`
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarBoundSelfCallbackWidenOnlyWrittenFacts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Invoker
  def callback=(callback: function)
    callback.call(1)
  end
end

class User
  property a: int
  property b: int
  property c: int

  def initialize(invoker: Invoker, flag: bool)
    invoker.callback = seed
    if flag
      @a = @b
    else
      @a = @c
    end
  end

  def seed(value: int)
    @b = value
  end
end

def run(flag: bool)
  User.new(Invoker.new(), flag).a
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
	}

	got := callScript(
		t,
		context.Background(),
		script,
		"run",
		[]Value{NewBool(true)},
		CallOptions{},
	)
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run(true) = %v, want 1", got)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		[]Value{NewBool(false)},
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestScriptFunctionEffectScanTracksCopiedSelfCallbackAliases(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def apply(callback: function)
  alias_cb = callback
  alias_cb.call(1)
end

class User
  property a: int
  property b: int

  def initialize
    apply(self.seed)
    @a = @b
  end

  def seed(value: int)
    @b = value
  end
end

def run
  User.new().a
end
`)
	requireNoCheckWarnings(t, script)

	user := script.classes["User"]
	apply := script.functions["apply"]
	initialize := user.Methods["initialize"]
	stmt, ok := initialize.Body[0].(*ExprStmt)
	if !ok {
		t.Fatalf("initialize body = %T, want *ExprStmt", initialize.Body[0])
	}
	call, ok := stmt.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("initialize expression = %T, want *CallExpr", stmt.Expr)
	}
	checker := &scriptChecker{
		script:          script,
		typeRoot:        checkTypeRoot(script, nil),
		runtimeTypeRoot: checkTypeRoot(script, nil),
		selfClass:       user,
	}
	scan := checker.scriptFunctionEffectScan(call, staticCallable{
		name:       "apply",
		fn:         apply,
		resolution: calleeDirect,
	})
	seed := user.Methods["seed"]
	if scan == nil {
		t.Fatal("scriptFunctionEffectScan() = nil")
	}
	if _, invoked := scan.invokedSelfFunctions[seed]; !invoked {
		t.Fatal("scriptFunctionEffectScan() lost current-self provenance through a local alias")
	}
	if _, written := scan.directIvarWrites[seed]["b"]; !written {
		t.Fatalf("scriptFunctionEffectScan() writes = %#v, want @b", scan.directIvarWrites[seed])
	}
	if scan.invokedUnknownCallable {
		t.Fatal("scriptFunctionEffectScan() marked an exact copied self callback unknown")
	}
}

func TestScriptFunctionEffectScanTracksExplicitSelfCallback(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Invoker
  def invoke(callback: function)
    callback.call()
  end
end

class User
  property b: int

  def initialize(invoker: Invoker)
    invoker.invoke(self.seed)
  end

  def seed
    @b = 1
  end
end

def run
  User.new(Invoker.new()).b
end
`)
	requireNoCheckWarnings(t, script)
	user := script.classes["User"]
	invoker := script.classes["Invoker"]
	initialize := user.Methods["initialize"]
	stmt, ok := initialize.Body[0].(*ExprStmt)
	if !ok {
		t.Fatalf("initialize body = %T, want *ExprStmt", initialize.Body[0])
	}
	call, ok := stmt.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("initialize expression = %T, want *CallExpr", stmt.Expr)
	}

	checker := &scriptChecker{
		script:          script,
		typeRoot:        checkTypeRoot(script, nil),
		runtimeTypeRoot: checkTypeRoot(script, nil),
		selfClass:       user,
	}
	scan := checker.scriptFunctionEffectScan(call, staticCallable{
		name:       "Invoker#invoke",
		fn:         invoker.Methods["invoke"],
		resolution: calleeMemberMethod,
	})
	seed := user.Methods["seed"]
	if scan == nil {
		t.Fatal("scriptFunctionEffectScan() = nil")
	}
	if _, invoked := scan.invokedSelfFunctions[seed]; !invoked {
		t.Fatal("scriptFunctionEffectScan() did not retain the explicit-self callback receiver")
	}
	if _, written := scan.directIvarWrites[seed]["b"]; !written {
		t.Fatalf("scriptFunctionEffectScan() writes = %#v, want @b", scan.directIvarWrites[seed])
	}
	if scan.invokedUnknownCallable {
		t.Fatal("scriptFunctionEffectScan() marked an exact explicit-self callback unknown")
	}

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarImplicitSelfCallableBindingsPreserveUnrelatedFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		invoke     string
		invocation string
	}{
		{
			name: "attached block",
			invoke: `  def invoke(&block: function)
    block.call()
  end`,
			invocation: `    invoke do
      @b = 1
    end`,
		},
		{
			name: "positional rest",
			invoke: `  def invoke(*callbacks: array<function>)
    callbacks[0].call()
  end`,
			invocation: `    invoke(-> { @b = 1 })`,
		},
		{
			name: "keyword rest",
			invoke: `  def invoke(**callbacks: hash<string, function>)
    callbacks["cb"].call()
  end`,
			invocation: `    invoke(cb: -> { @b = 1 })`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool
  property b: int

  def initialize
    @flag = false
`+tc.invocation+`
    if @flag
      takes_int("bad")
    end
  end

`+tc.invoke+`
end

def run
  User.new().b
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarUnknownVariadicCallableRemainsConservative(t *testing.T) {
	t.Parallel()

	requireCheckWarningContains(t, compileScriptDefault(t, `
def takes_int(value: int)
  value
end

class User
  property flag: bool

  def initialize(callback: function)
    @flag = false
    invoke(callback)
    if @flag
      takes_int("bad")
    end
  end

  def invoke(*callbacks: array<function>)
    callbacks[0].call()
  end
end
`), "call to takes_int argument value expected int, got string")
}

func TestCheckRecursiveVariadicCallableFactsTerminate(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def recur(n: int, *callbacks: array<function>) -> nil
  if n > 0
    recur(n - 1, -> {})
  end
end

def run
  recur(1, -> {})
end
`))
}

func TestCheckInitializerIvarHelperHashDefaultEffects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		helper string
	}{
		{
			name: "parameter default",
			helper: `  def helper(trigger = Hash.new { |hash, key| @b = 1 }[:missing])
    trigger
  end`,
		},
		{
			name: "method body",
			helper: `  def helper
    Hash.new { |hash, key| @b = 1 }[:missing]
  end`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    helper()
    @a = @b
  end

`+tc.helper+`
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarHelperHashDefaultEffectBoundaries(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		helper string
	}{
		{
			name: "present key",
			helper: `  def helper
    {present: 0}[:present]
  end`,
		},
		{
			name: "value default without callback",
			helper: `  def helper
    Hash.new(0)[:missing]
  end`,
		},
		{
			name: "selector does not complete",
			helper: `  def helper
    begin
      Hash.new { |hash, key| @b = 1 }[
        -> { raise "stop" }.call()
      ]
    rescue
      nil
    end
  end`,
		},
		{
			name: "callback exits before write",
			helper: `  def helper
    begin
      Hash.new do |hash, key|
        raise "stop"
        @b = 1
      end[:missing]
    rescue
      nil
    end
  end`,
		},
		{
			name: "noncompleting default prevents body",
			helper: `  def helper(trigger = Hash.new { |hash, key| raise "stop" }[:missing])
    @b = 1
  end`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    begin
      helper()
    rescue
      nil
    end
    @a = @b
  end

`+tc.helper+`
end

def run
  User.new().a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"expected int, got nil",
			)
		})
	}

	t.Run("callback write survives noncompletion", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    begin
      helper()
    rescue
      nil
    end
    @a = @b
  end

  def helper
    Hash.new do |hash, key|
      @b = 1
      raise "stop"
    end[:missing]
  end
end

def run
  User.new().a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})
}

func TestCheckInitializerIvarDistinctSameClassSetterRemainsConservative(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize(other: User? = nil)
    if other
      other.b = 1
      @a = @c
    else
      @a = 1
    end
  end
end

def run
  User.new(User.new()).a
end
`)
	requireNoCheckWarnings(t, script)
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarDispatchesApplyEffectsWithoutLoop(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{name: "index", expression: `    @invoker[-> { @b = 1 }]`},
		{name: "binary operator", expression: `    @invoker + -> { @b = 1 }`},
		{name: "compound operator", expression: `    @invoker += -> { @b = 1 }`},
		{name: "Hash default lookup", expression: `    Hash.new { |hash, key| @b = 1 }[:missing]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Invoker
  def [](callback)
    callback.call()
  end

  def +(callback)
    callback.call()
    self
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int

  def initialize(@invoker)
`+tc.expression+`
    @a = @b
  end
end

def run
  User.new(Invoker.new()).a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarDispatchesPreserveExactEffects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{name: "index", expression: `    @invoker[-> { @b = 1 }]`},
		{name: "binary operator", expression: `    @invoker + -> { @b = 1 }`},
		{name: "compound operator", expression: `    @invoker += -> { @b = 1 }`},
		{name: "Hash default lookup", expression: `    Hash.new { |hash, key| @b = 1 }[:missing]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Invoker
  def [](callback)
    callback.call()
  end

  def +(callback)
    callback.call()
    self
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int
  property c: int

  def initialize(@invoker)
`+tc.expression+`
    @a = @b
    @a = @c
  end
end

def run
  User.new(Invoker.new()).a
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				!strings.Contains(warnings[0].Message, "write to @a expected int, got nil") {
				t.Fatalf("CheckWarnings() = %#v, want one nil write warning", warnings)
			}
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarPassiveDispatchesPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{name: "index", expression: `    @invoker[-> { @b = 1 }]`},
		{name: "binary operator", expression: `    @invoker + -> { @b = 1 }`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Invoker
  def [](callback)
    0
  end

  def +(callback)
    self
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int

  def initialize(@invoker)
`+tc.expression+`
    @a = @b
  end
end

def run
  User.new(Invoker.new()).a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarDispatchBindingFailuresPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
		loop       bool
	}{
		{name: "binary", expression: `@invoker + -> { @b = 1 }`},
		{name: "index", expression: `@invoker[-> { @b = 1 }]`},
		{name: "compound", expression: `@invoker += -> { @b = 1 }`},
		{name: "binary in loop", expression: `@invoker + -> { @b = 1 }`, loop: true},
		{name: "index in loop", expression: `@invoker[-> { @b = 1 }]`, loop: true},
		{name: "compound in loop", expression: `@invoker += -> { @b = 1 }`, loop: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			region := "      " + tc.expression
			if tc.loop {
				region = "      for value in [1]\n        " + tc.expression + "\n      end"
			}
			script := compileScriptDefault(t, `
class Invoker
  def [](callback, extra)
    callback.call()
  end

  def +(callback, extra)
    callback.call()
    self
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int

  def initialize(@invoker)
    begin
`+region+`
    ensure
      @a = @b
    end
  end
end

def run
  User.new(Invoker.new()).a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarDispatchDefaultPrefixesRetainCallbackWrites(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		method     string
		expression string
	}{
		{
			name: "binary operator",
			method: `  def +(callback, own: -> { @c = "set" }, trigger: callback.call(), own_run: own.call(), failure: stop_now())
    self
  end`,
			expression: `      invoker + -> { @b = 1 }`,
		},
		{
			name: "index getter",
			method: `  def [](callback, own: -> { @c = "set" }, trigger: callback.call(), own_run: own.call(), failure: stop_now())
    nil
  end`,
			expression: `      invoker[-> { @b = 1 }]`,
		},
		{
			name: "member setter",
			method: `  def value=(callback, own: -> { @c = "set" }, trigger: callback.call(), own_run: own.call(), failure: stop_now())
    callback
  end`,
			expression: `      invoker.value = -> { @b = 1 }`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def stop_now
  raise "stop"
end

class Invoker
`+tc.method+`
end

class User
  property a: int
  property b: int
  property s: string
  property c: string

  def initialize(invoker: Invoker)
    begin
`+tc.expression+`
    rescue
      @a = @b
      @s = @c
    end
  end
end

def run
  User.new(Invoker.new())
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @s expected string, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want only the unrelated @c warning", warnings)
			}
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @s expected string, got nil",
			)
		})
	}

	t.Run("same-class body remains gated", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def stop_now
  raise "stop"
end

class User
  property a: int
  property b: int
  property s: string
  property c: string

  def +(callback, trigger: callback.call(), failure: stop_now())
    @c = "set"
    self
  end

  def initialize(active: bool, other: User | nil = nil)
    if !active
      return
    end
    begin
      other + -> { @b = 1 }
    rescue
      @a = @b
      @s = @c
    end
  end
end

def run
  User.new(true, User.new(false))
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			warnings[0].Message != "write to @s expected string, got nil" {
			t.Fatalf("CheckWarnings() = %#v, want only the unreachable-body @c warning", warnings)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @s expected string, got nil",
		)
	})
}

func TestCheckInitializerIvarNotEqualDispatchUsesRuntimePrecedence(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Invoker
  def !=(callback, extra)
    false
  end

  def ==(callback)
    callback.call()
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int

  def initialize(@invoker)
    begin
      @invoker != -> { @b = 1 }
    ensure
      @a = @b
    end
  end
end

def run
  User.new(Invoker.new()).a
end
`)
	requireCheckWarningContains(t, script, "write to @a expected int, got nil")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarDispatchCompletionUsesRuntimeSelection(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		classes      string
		expression   string
		construction string
	}{
		{
			name: "not equal does not fall back",
			classes: `
class Receiver
  def !=(value, extra)
    false
  end

  def ==(value)
    true
  end
end
`,
			expression:   `Receiver.new() != 1`,
			construction: `User.new()`,
		},
		{
			name: "private index",
			classes: `
class Receiver
  private

  def [](index)
    1
  end
end
`,
			expression:   `Receiver.new()[0]`,
			construction: `User.new()`,
		},
		{
			name: "missing index",
			classes: `
class Receiver
end
`,
			expression:   `Receiver.new()[0]`,
			construction: `User.new()`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.classes+`
class User
  property a: int
  property b: int

  def initialize
    `+tc.expression+`
    @a = @b
  end
end

def run
  begin
    `+tc.construction+`
  rescue
    1
  end
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarIndexCompletionPreservesBuiltinUnionArm(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Receiver
  def [](index, extra)
    1
  end
end

class User
  property a: int
  property b: int

  def initialize(receiver: Receiver | array<int>)
    receiver[0]
    @a = @b
  end
end

def run
  User.new([]).a
end
`)
	requireCheckWarningContains(t, script, "write to @a expected int, got nil")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"expected int, got nil",
	)
}

func TestCheckInitializerIvarPureHashLookupsPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	for _, expression := range []string{
		`Hash.new[:missing]`,
		`Hash.new()[:missing]`,
		`Hash.new(0)[:missing]`,
	} {
		t.Run(expression, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    `+expression+`
    @a = @b
  end
end
`), "write to @a expected int, got nil")
		})
	}
}

func TestCheckCapturedBlockValidationDoesNotMutateOuterInference(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize
    defaults = Hash.new { |hash, key| @b = 1 }
    proc { defaults = Hash.new() }
    defaults[:missing]
    @a = @b
    @a = @c
  end
end

def run
  User.new().a
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to @a expected int, got nil") {
		t.Fatalf("CheckWarnings() = %#v, want only the @c nil write", warnings)
	}
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarHashDefaultAliasesPreserveExactEffects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		setup        string
		wantBWarning bool
		runWritesB   bool
	}{
		{
			name:         "hash literal",
			setup:        `    defaults = {}`,
			wantBWarning: true,
		},
		{
			name:         "bare Hash new",
			setup:        `    defaults = Hash.new()`,
			wantBWarning: true,
		},
		{
			name:         "value Hash new",
			setup:        `    defaults = Hash.new(0)`,
			wantBWarning: true,
		},
		{
			name: "lambda callback",
			setup: `    callback = lambda { |hash, key| @b = 1 }
    defaults = Hash.new(&callback)`,
			runWritesB: true,
		},
		{
			name: "proc callback",
			setup: `    callback = proc { |hash, key| @b = 1 }
    defaults = Hash.new(&callback)`,
			runWritesB: true,
		},
		{
			name: "Proc new callback",
			setup: `    callback = Proc.new { |hash, key| @b = 1 }
    defaults = Hash.new(&callback)`,
			runWritesB: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int
  property control: int

  def initialize
`+tc.setup+`
    alias_defaults = defaults
    alias_defaults[:missing]
    @a = @b
    @control = @c
  end
end

def run
  User.new().a
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 {
				t.Fatalf("CheckWarnings() = %#v, want one nil write warning", warnings)
			}
			want := "write to @control expected int, got nil"
			if tc.wantBWarning {
				want = "write to @a expected int, got nil"
			}
			if !strings.Contains(warnings[0].Message, want) {
				t.Fatalf("CheckWarnings() = %#v, want %q", warnings, want)
			}
			if tc.runWritesB {
				requireCallErrorContains(
					t,
					script,
					"run",
					nil,
					CallOptions{},
					"instance variable @control expected int, got nil",
				)
			}
		})
	}
}

func TestCheckInitializerIvarHashDefaultFreezesConstructorIdentity(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def run
  Hash.new { |hash, key| @b = 1 }
end
`)
	fn := script.functions["run"]
	statement, ok := fn.Body[0].(*ExprStmt)
	if !ok {
		t.Fatalf("run body statement = %T, want *ExprStmt", fn.Body[0])
	}
	call, ok := statement.Expr.(*CallExpr)
	if !ok {
		t.Fatalf("run body expression = %T, want *CallExpr", statement.Expr)
	}

	checker := scriptChecker{
		script:          script,
		typeRoot:        checkTypeRoot(script, nil),
		runtimeTypeRoot: checkTypeRoot(script, nil),
	}
	target, resolved := checker.resolveCallable(call)
	if !checker.callTargetsBlockCapturingBuiltin(call, target, resolved) {
		t.Fatal("Hash.new did not initially resolve to the core constructor")
	}
	checker.captureEvaluatedRetainedConstructor(call, target, true)

	globals := map[string]Value{"Hash": NewInt(1)}
	checker.optionGlobals = globals
	checker.optionGlobalsOverride = true
	checker.hostGlobals = checkHostGlobals(globals)
	checker.typeRoot = checkTypeRoot(script, globals)
	checker.runtimeTypeRoot = checkTypeRoot(script, globals)
	reboundTarget, reboundResolved := checker.resolveCallable(call)
	if checker.callTargetsBlockCapturingBuiltin(call, reboundTarget, reboundResolved) {
		t.Fatal("shadowed Hash.new still resolved to the core constructor")
	}

	defaults, exact := checker.captureDirectCoreHashDefaults(call)
	if !exact || len(defaults) != 1 || defaults[0].block != call.Block ||
		defaults[0].unknown {
		t.Fatalf("captureDirectCoreHashDefaults() = %#v, want the evaluated block", defaults)
	}
	index := &IndexExpr{
		Object:  call,
		Indices: []Expression{&SymbolLiteral{Name: "missing"}},
	}
	effects, mayRun, _ := checker.indexReadIvarEffects(
		index,
		checkTypeHash,
		defaults,
	)
	if !mayRun || effects.unknown {
		t.Fatalf(
			"indexReadIvarEffects() = (%#v, %t), want exact effects",
			effects,
			mayRun,
		)
	}
	if _, written := effects.writes["b"]; !written || len(effects.writes) != 1 {
		t.Fatalf("indexReadIvarEffects().writes = %#v, want only b", effects.writes)
	}
}

func TestCheckInitializerIvarAliasedHashDefaultCompletion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		constructor string
	}{
		{
			name:        "attached block next",
			constructor: `Hash.new { |hash, key| next 1 }`,
		},
		{
			name:        "proc next",
			constructor: `Hash.new(&proc { |hash, key| next 1 })`,
		},
		{
			name:        "Proc new next",
			constructor: `Hash.new(&Proc.new { |hash, key| next 1 })`,
		},
		{
			name:        "lambda return",
			constructor: `Hash.new(&lambda { |hash, key| return 1 })`,
		},
		{
			name:        "lambda break",
			constructor: `Hash.new(&lambda { |hash, key| break 1 })`,
		},
		{
			name:        "nested lambda return",
			constructor: `Hash.new(&proc { |hash, key| -> { return 1 }.call() })`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    defaults = `+tc.constructor+`
    defaults[:missing]
    @a = "bad"
  end
end

def run
  User.new().a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got string")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got string",
			)
		})
	}

	t.Run("callback body raises", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int
  property control: int

  def initialize
    defaults = Hash.new do |hash, key|
      @b = 1
      raise "stop"
    end
    alias_defaults = defaults
    begin
      alias_defaults[:missing]
      @c = "unreachable"
    rescue
      @a = @b
      @control = @c
    end
  end
end

def run
  User.new().a
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to @control expected int, got nil") {
			t.Fatalf("CheckWarnings() = %#v, want one unrelated nil write warning", warnings)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @control expected int, got nil",
		)
	})

	t.Run("callback unmatched raise survives ensure", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    defaults = Hash.new do |hash, key|
      begin
        raise "stop"
      ensure
        nil
      end
    end
    begin
      defaults[:missing]
      @a = "bad"
    rescue
      nil
    end
  end
end

def run
  User.new()
  true
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindBool || !got.Bool() {
			t.Fatalf("run() = %v, want true", got)
		}
	})

	t.Run("strict callback rejects arguments", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize
    callback = lambda { |hash, key, extra| @b = 1 }
    defaults = Hash.new(&callback)
    alias_defaults = defaults
    begin
      alias_defaults[:missing]
      @c = "unreachable"
    rescue
      @a = @b
    end
  end
end

def run
  User.new().a
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to @a expected int, got nil") {
			t.Fatalf("CheckWarnings() = %#v, want one rejected-callback nil write warning", warnings)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got nil",
		)
	})
}

func TestCheckInitializerIvarHashDefaultFreshness(t *testing.T) {
	t.Parallel()

	t.Run("callback mutation invalidates every alias", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    defaults = Hash.new do |hash, key|
      hash[key] = 1
      raise "stop"
    end
    alias_defaults = defaults
    begin
      alias_defaults[:missing]
    rescue
      nil
    end
    defaults[:missing]
    @a = "bad"
  end
end

def run
  User.new().a
end
`)
		requireCheckWarningContains(t, script, "write to @a expected int, got string")
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got string",
		)
	})

	t.Run("selector mutation is observed before lookup", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    defaults = Hash.new do |hash, key|
      raise "stop"
    end
    defaults[defaults.store(:missing, :missing)]
    @a = "bad"
  end
end

def run
  User.new().a
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to @a expected int, got string") {
			t.Fatalf("CheckWarnings() = %#v, want one reached bad write warning", warnings)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got string",
		)
	})

	t.Run("case receiver aliases observe selector mutation", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize(flag: bool)
    defaults = Hash.new { |hash, key| raise "stop" }
    other = Hash.new { |hash, key| raise "stop" }
    (case flag
     when true
       defaults
     else
       other
     end)[defaults.store(:missing, :missing)]
    @a = "bad"
  end
end

def run
  User.new(true).a
end
`)
		requireCheckWarningContains(t, script, "write to @a expected int, got string")
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got string",
		)
	})

	t.Run("selector rebind keeps the evaluated receiver", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    defaults = Hash.new do |hash, key|
      raise "stop"
    end
    begin
      defaults[-> { defaults = Hash.new(); :missing }.call()]
      @a = "bad"
    rescue
      @a = 1
    end
  end
end

def run
  User.new().a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})

	t.Run("conditional keeps its evaluated constructor arm", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    flag = true
    defaults = flag ?
      Hash.new(-> { flag = false; 0 }.call()) :
      Hash.new { |hash, key| raise "stop" }
    defaults[:missing]
    @a = "bad"
  end
end

def run
  User.new().a
end
`)
		requireCheckWarningContains(t, script, "write to @a expected int, got string")
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got string",
		)
	})
}

func TestCheckInitializerIvarImmediateLambdaWidensOnlyWrittenFactsWithoutLoop(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{name: "stabby", expression: `    -> { @b = 1 }.call()`},
		{name: "named", expression: `    lambda { @b = 1 }.call()`},
		{
			name: "forwarded nil block",
			expression: `    callback = nil
    -> { @b = 1 }.call(&callback)`,
		},
		{
			name: "empty keyword splat",
			expression: `    options = {}
    -> { @b = 1 }.call(**options)`,
		},
		{
			name: "assigned exact positional splat",
			expression: `    values = [1]
    ->(value: int) { @b = 1 }.call(*values)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize
`+tc.expression+`
    @a = @b
    @a = @c
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				!strings.Contains(warnings[0].Message, "write to @a expected int, got nil") {
				t.Fatalf("CheckWarnings() = %#v, want one nil write warning", warnings)
			}
		})
	}
}

func TestCheckInitializerIvarImmediateLambdaPreEntryFailuresPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		parameters string
		expression string
		arguments  string
	}{
		{
			name: "attached block",
			expression: `      -> { @b = 1 }.call() do
        nil
      end`,
		},
		{
			name: "forwarded nonnil block",
			expression: `      callback = -> {}
      -> { @b = 1 }.call(&callback)`,
		},
		{
			name:       "ordinary keyword",
			expression: `      -> { @b = 1 }.call(value: 1)`,
		},
		{
			name: "nonempty keyword splat",
			expression: `      options = { value: 1 }
      -> { @b = 1 }.call(**options)`,
		},
		{
			name:       "typed positional splat",
			parameters: "(values: array<string>)",
			expression: `      ->(value: int) { @b = 1 }.call(*values)`,
			arguments:  `["bad"]`,
		},
		{
			name: "assigned exact positional splat",
			expression: `      values = ["bad"]
      ->(value: int) { @b = 1 }.call(*values)`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize`+tc.parameters+`
    begin
`+tc.expression+`
    rescue
      @a = @b
    end
  end
end

def run
  User.new(`+tc.arguments+`).a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarDirectHashDefaultLambdaArity(t *testing.T) {
	t.Parallel()

	t.Run("rejecting named lambda", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    begin
      Hash.new(&lambda { |hash, key, extra| @b = 1 })[:missing]
    rescue
      @a = @b
    end
  end
end

def run
  User.new().a
end
`)
		requireCheckWarningContains(t, script, "write to @a expected int, got nil")
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got nil",
		)
	})

	t.Run("matching named lambda", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    Hash.new(&lambda { |hash, key| @b = 1 })[:missing]
    @a = @b
  end
end

def run
  User.new().a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})
}

func TestCheckInitializerIvarDirectHashDefaultPreEntryFailuresPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{
			name:       "typed hash receiver",
			expression: `Hash.new { |hash: hash<symbol, int>, key| @b = 1 }[:missing]`,
		},
		{
			name:       "unsupported key",
			expression: `Hash.new { |hash, key| @b = 1 }[{}]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    begin
      `+tc.expression+`
    rescue
      @a = @b
    end
  end
end

def run
  User.new().a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarNestedTypedBlockBindingSuccessKeepsPostEntryEffects(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{
			name: "immediate lambda success",
			expression: `      ->(((value: int): array<int>)) {
        @b = 1
        raise "stop"
      }.call([[1]])`,
		},
		{
			name: "hash default success",
			expression: `      Hash.new { |hash: hash, ((key: symbol): symbol)|
        @b = 1
        raise "stop"
      }[:missing]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    begin
`+tc.expression+`
    rescue
      @a = @b
    end
  end
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarNestedTypedBlockBindingRejectionKeepsPreEntryFacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{
			name: "immediate lambda outer element type",
			expression: `      ->(((value: string): array<int>)) {
        @b = true
      }.call([["ok"]])`,
		},
		{
			name: "immediate lambda nested element type",
			expression: `      ->(((value: int): array<string>)) {
        @b = true
      }.call([["bad"]])`,
		},
		{
			name: "immediate lambda exact splat",
			expression: `      ->(((value: string): array<int>)) {
        @b = true
      }.call(*[[["ok"]]])`,
		},
		{
			name: "hash default outer element type",
			expression: `      Hash.new { |hash: hash, ((key: symbol): int)|
        @b = true
      }[:missing]`,
		},
		{
			name: "hash default nested element type",
			expression: `      Hash.new { |hash: hash, ((key: int): symbol)|
        @b = true
      }[:missing]`,
		},
		{
			name: "hash default receiver nested element type",
			expression: `      Hash.new { |((value: int): hash), key|
        @b = true
      }[:missing]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: bool
  property c: int

  def initialize
    @a = 1
    begin
`+tc.expression+`
    rescue
      @b = false
    end
    if @b
      @a = @c
    end
  end
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarNestedTypedBlockBindingRejectsAbstractSplatAndRest(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		param      string
		expression string
		argument   string
	}{
		{
			name:  "abstract splat nested rejection",
			param: "values: array<array<array<string>>>",
			expression: `      ->(((value: int): array<string>)) {
        @b = true
      }.call(*values)`,
			argument: `[[["bad"]]]`,
		},
		{
			name:  "abstract splat exact rest cardinality",
			param: "values: array<int>",
			expression: `      ->((*(first: int, second: int))) {
        @b = true
      }.call(*values)`,
			argument: `[1]`,
		},
		{
			name:  "recursive array normalization keeps source cardinality",
			param: "values: array<array<int>>",
			expression: `      ->((((leaf: string)): array<array<string> | any>)) {
        @b = true
      }.call([values])`,
			argument: `[[1]]`,
		},
		{
			name:  "hash normalization keeps empty shape correlation",
			param: "items: hash<int, int>",
			expression: `      ->(((value: {foo: string}): {foo?: string} | any)) {
        @b = true
      }.call([items])`,
			argument: `{}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: bool

  def initialize(`+tc.param+`)
    @a = 1
    begin
`+tc.expression+`
    rescue
      nil
    end
    @a = @b
  end
end

def run
  User.new(`+tc.argument+`).a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
		})
	}
}

func TestCheckInitializerIvarNestedTypedBlockBindingKeepsPartialArraysGradual(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(values: array)
    values << "bad"
    begin
      ->((first: int)) {
        @b = 1
        raise "stop"
      }.call(values)
    rescue
      @a = @b
    end
  end
end

def run
  User.new([1]).a
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarHashDefaultWaitsForSelectorCompletion(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      begin
        Hash.new { |hash, key| @b = 1 }[
          -> { raise "stop" }.call()
        ]
      rescue
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
	requireCheckWarningContains(t, script, "write to @a expected int, got nil")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"expected int, got nil",
	)
}

func TestCheckInitializerIvarImmediateLambdaPostEntryFailureRetainsWrites(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    begin
      -> {
        @b = 1
        raise "boom"
      }.call()
    rescue
      @a = @b
    end
  end
end

def run
  User.new().a
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarRejectedLambdaDefaultDoesNotReachBody(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(trigger = -> { @b = 1 }.call() do
    nil
  end)
    @a = @b
  end
end

def run
  begin
    User.new()
  rescue
    1
  end
end
`)
	if warnings := script.CheckWarningsForFunction("run"); len(warnings) != 0 {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want none", "run", warnings)
	}
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarExactLambdaSplatAlternativesDoNotInventFailureArm(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: string

  def initialize(flag: bool)
    args = flag ? [1] : [2]
    begin
      ->(value: int) { @b = value.to_s }.call(*args)
    rescue
      @a = @b
    end
    @a = 1
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to @a expected int, got string") ||
		strings.Contains(warnings[0].Message, "nil") {
		t.Fatalf("CheckWarnings() = %#v, want one post-entry string warning", warnings)
	}
	for _, flag := range []bool{false, true} {
		got := callScript(
			t,
			context.Background(),
			script,
			"run",
			[]Value{NewBool(flag)},
			CallOptions{},
		)
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run(%t) = %v, want 1", flag, got)
		}
	}
}

func TestCheckInitializerIvarRepeatedExactLambdaSplatsShareTheirSourceChoice(t *testing.T) {
	t.Parallel()

	t.Run("unreachable cross-alternative arity", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(flag: bool)
    values = flag ? [1] : []
    begin
      ->(value: int) {
        @b = 1
      }.call(*values, *values)
    rescue
      @a = @b
    end
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			!strings.Contains(warnings[0].Message, "write to @a expected int, got nil") ||
			strings.Contains(warnings[0].Message, " or ") {
			t.Fatalf("CheckWarnings() = %#v, want one nil-only write warning", warnings)
		}
		for _, flag := range []bool{false, true} {
			requireCallErrorContains(
				t,
				script,
				"run",
				[]Value{NewBool(flag)},
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		}
	})

	t.Run("reachable correlated arity", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(flag: bool)
    values = flag ? [1] : []
    begin
      ->(left: int, right: int) {
        @b = left + right
      }.call(*values, *values)
    rescue
      nil
    end
    @a = @b
  end
end

def run(flag: bool)
  User.new(flag).a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(
			t,
			context.Background(),
			script,
			"run",
			[]Value{NewBool(true)},
			CallOptions{},
		)
		if got.Kind() != KindInt || got.Int() != 2 {
			t.Fatalf("run(true) = %v, want 2", got)
		}
	})

	t.Run("independent sources retain cartesian choices", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(left_flag: bool, right_flag: bool)
    left = left_flag ? [1] : []
    right = right_flag ? [2] : []
    begin
      ->(value: int) {
        @b = value
      }.call(*left, *right)
    rescue
      nil
    end
    @a = @b
  end
end

def run(left_flag: bool, right_flag: bool)
  User.new(left_flag, right_flag).a
end
`)
		requireNoCheckWarnings(t, script)
		got := callScript(
			t,
			context.Background(),
			script,
			"run",
			[]Value{NewBool(false), NewBool(true)},
			CallOptions{},
		)
		if got.Kind() != KindInt || got.Int() != 2 {
			t.Fatalf("run(false, true) = %v, want 2", got)
		}
	})
}

func TestExactPositionalArgumentVariantsCorrelatesOnlyStableSources(t *testing.T) {
	t.Parallel()

	one := &ArrayLiteral{Elements: []Expression{&IntegerLiteral{Value: 1}}}
	empty := &ArrayLiteral{}
	firstAlternatives := []Expression{one, empty}
	for _, tc := range []struct {
		name             string
		secondName       string
		secondGeneration uint64
		secondValues     []Expression
		wantCorrelated   bool
		wantLengths      map[int]int
	}{
		{
			name:             "same binding and alternatives",
			secondName:       "values",
			secondGeneration: 7,
			secondValues:     []Expression{one, empty},
			wantCorrelated:   true,
			wantLengths:      map[int]int{0: 1, 2: 1},
		},
		{
			name:             "new binding generation",
			secondName:       "values",
			secondGeneration: 8,
			secondValues:     []Expression{one, empty},
			wantLengths:      map[int]int{0: 1, 1: 2, 2: 1},
		},
		{
			name:             "different alternative order",
			secondName:       "values",
			secondGeneration: 7,
			secondValues:     []Expression{empty, one},
			wantLengths:      map[int]int{0: 1, 1: 2, 2: 1},
		},
		{
			name:             "different local",
			secondName:       "other",
			secondGeneration: 7,
			secondValues:     []Expression{one, empty},
			wantLengths:      map[int]int{0: 1, 1: 2, 2: 1},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			firstValue := &Identifier{Name: "values"}
			secondValue := &Identifier{Name: tc.secondName}
			firstSplat := &SplatArg{Value: firstValue}
			secondSplat := &SplatArg{Value: secondValue}
			checker := scriptChecker{
				callArgumentStaticValues: map[Expression][]Expression{
					firstValue:  firstAlternatives,
					secondValue: tc.secondValues,
				},
				callArgumentSplatSources: map[*SplatArg]checkCallSplatSource{
					firstSplat: {
						name:         "values",
						generation:   7,
						alternatives: firstAlternatives,
					},
					secondSplat: {
						name:         tc.secondName,
						generation:   tc.secondGeneration,
						alternatives: tc.secondValues,
					},
				},
			}
			variants, exact, correlated := checker.exactPositionalArgumentVariants(
				&CallExpr{Args: []Expression{firstSplat, secondSplat}},
				32,
			)
			if !exact {
				t.Fatalf("exactPositionalArgumentVariants(%q) exact = false, want true", tc.name)
			}
			if correlated != tc.wantCorrelated {
				t.Fatalf(
					"exactPositionalArgumentVariants(%q) correlated = %t, want %t",
					tc.name,
					correlated,
					tc.wantCorrelated,
				)
			}
			gotLengths := make(map[int]int)
			for _, variant := range variants {
				gotLengths[len(variant)]++
			}
			if len(gotLengths) != len(tc.wantLengths) {
				t.Fatalf(
					"exactPositionalArgumentVariants(%q) lengths = %v, want %v",
					tc.name,
					gotLengths,
					tc.wantLengths,
				)
			}
			for length, want := range tc.wantLengths {
				if got := gotLengths[length]; got != want {
					t.Fatalf(
						"exactPositionalArgumentVariants(%q) length %d count = %d, want %d",
						tc.name,
						length,
						got,
						want,
					)
				}
			}
		})
	}
}

func TestCheckInitializerIvarImmediateLambdaWidensOnlyWrittenFacts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int
  property c: int

  def initialize
    for x in [1]
      -> { @b = 1 }.call()
    end
    @a = @b
    @a = @c
  end
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		!strings.Contains(warnings[0].Message, "write to @a expected int, got nil") {
		t.Fatalf("CheckWarnings() = %#v, want one nil write warning", warnings)
	}
}

func TestCheckInitializerIvarDeepLambdaInvocationContext(t *testing.T) {
	t.Parallel()

	const nesting = 80
	callback := `-> { @b = 1 }`
	for range nesting {
		callback = "[" + callback + "]"
	}
	unwrap := strings.Repeat("    callback = callback[0]\n", nesting)
	script := compileScriptDefault(t, `
class Invoker
  def +(callback)
`+unwrap+`    callback.call()
    self
  end
end

class User
  property invoker: Invoker
  property a: int
  property b: int

  def initialize(@invoker)
    for x in [1]
      @invoker + `+callback+`
    end
    @a = @b
  end
end

def run
  User.new(Invoker.new()).a
end
`)
	requireNoCheckWarnings(t, script)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarPassiveSettersPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		setup      string
		parameters string
		write      string
	}{
		{
			name:  "builtin array",
			write: "items = [0]\n      items[0] = 1",
		},
		{
			name:  "builtin hash",
			write: "items = {}\n      items[:value] = 1",
		},
		{
			name:       "raw member",
			setup:      "class Box\nend\n",
			parameters: "(box: Box)",
			write:      "box.value = 1",
		},
		{
			name: "passive index setter",
			setup: `class Box
  def []=(index, value)
    1
  end
end
`,
			parameters: "(box: Box)",
			write:      "box[0] = 1",
		},
		{
			name:  "hash default is not a write callback",
			write: "Hash.new { |hash, key| @b = 1 }[:missing] = 0",
		},
	}
	for _, tc := range cases {
		for _, looped := range []bool{false, true} {
			name := tc.name
			if looped {
				name += " in loop"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				write := "    " + tc.write
				if looped {
					write = "    for iteration in [1]\n      " +
						strings.ReplaceAll(tc.write, "\n", "\n      ") +
						"\n    end"
				}
				script := compileScriptDefault(t, tc.setup+`
class User
  property a: int
  property b: int

  def initialize`+tc.parameters+`
`+write+`
    @a = @b
  end
end
`)
				warnings := script.CheckWarnings()
				if len(warnings) != 1 ||
					warnings[0].Message != "write to @a expected int, got nil" {
					t.Fatalf("CheckWarnings() = %#v, want one unset @b warning", warnings)
				}
			})
		}
	}
}

func TestCheckInitializerIvarSetterCallbacksUseRuntimeDispatch(t *testing.T) {
	t.Parallel()

	const classes = `
class Writer
  def []=(index, callback)
    callback.call()
  end
end

class Passive
  def [](callback)
    callback.call()
    0
  end

  def []=(index, value)
    1
  end
end

class SelectorWriter
  def []=(callback, value)
    callback.call()
  end
end
`
	cases := []struct {
		name         string
		setup        string
		write        string
		reassignment bool
	}{
		{
			name:  "direct setter callback",
			write: "writer[0] = -> { @b = 1 }",
		},
		{
			name:  "looped setter callback",
			write: "for iteration in [1]\n      writer[0] = -> { @b = 1 }\n    end",
		},
		{
			name:  "plain assignment does not invoke getter",
			write: "passive[-> { @b = 1 }] = 0",
		},
		{
			name:         "selector rebind keeps evaluated receiver",
			setup:        "box = writer",
			write:        "box[-> { box = passive; 0 }.call()] = -> { @b = 1 }",
			reassignment: true,
		},
		{
			name:  "rhs effects select the later receiver",
			setup: "choose_writer = false",
			write: `(choose_writer ? selector_writer : passive)[-> { @b = 1 }] =
      -> { choose_writer = true; 0 }.call()`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, classes+`
class User
  property a: int
  property b: int
  property c: int

  def initialize(writer: Writer, passive: Passive, selector_writer: SelectorWriter, flag: bool)
    `+tc.setup+`
    `+tc.write+`
    if flag
      @a = @b
    else
      @a = @c
    end
  end
end
`)
			warnings := script.CheckWarnings()
			if tc.name == "plain assignment does not invoke getter" {
				if len(warnings) != 2 {
					t.Fatalf("CheckWarnings() = %#v, want unset @b and @c warnings", warnings)
				}
				return
			}
			if tc.reassignment {
				if len(warnings) != 2 ||
					!strings.Contains(warnings[0].Message, "reassignment of box expected") ||
					warnings[1].Message != "write to @a expected int, got nil" {
					t.Fatalf("CheckWarnings() = %#v, want reassignment and unset @c warnings", warnings)
				}
				return
			}
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @a expected int, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
			}
		})
	}
}

func TestCheckInitializerIvarSetterEntryFailuresPreserveFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		setter string
	}{
		{
			name: "private",
			setter: `  private
  def []=(index, callback)
    callback.call()
  end`,
		},
		{
			name: "arity",
			setter: `  def []=(value)
    value.call()
  end`,
		},
		{
			name: "type",
			setter: `  def []=(index: string, callback)
    callback.call()
  end`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Box
`+tc.setter+`
end

class User
  property a: int
  property b: int

  def initialize(box: Box)
    begin
      box[0] = -> { @b = 1 }
    rescue
      @a = @b
    end
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @a expected int, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want one pre-entry unset warning", warnings)
			}
		})
	}
}

func TestCheckInitializerIvarSetterPostEntryFailureRetainsCallbackWrites(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  def []=(index, callback)
    callback.call()
    raise "boom"
  end
end

class User
  property a: int
  property b: int

  def initialize(box: Box)
    begin
      box[0] = -> { @b = 1 }
    rescue
      @a = @b
    end
  end
end

def run
  User.new(Box.new()).a
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarSetterUsesEvaluatedArgumentFacts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  def []=(index, callback: function)
    callback.call()
  end
end

class User
  property a: int
  property b: int
  property c: int

  def initialize(box: Box, flag: bool)
    callbacks = [-> { @b = 1 }]
    box[
      -> {
        callbacks = [-> { @c = 1 }]
        0
      }.call()
    ] = callbacks[0]
    if flag
      @a = @b
    else
      @a = @c
    end
  end
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
	}
}

func TestCheckInitializerIvarRepeatedEvaluationStopsAfterFailures(t *testing.T) {
	t.Parallel()

	regions := map[string]string{
		"empty for": `    for value in []
      @b = 1
    end`,
		"array element": `    for value in [1]
      begin
        [-> { raise "stop" }.call(), -> { @b = 1 }.call()]
      rescue
        nil
      end
    end`,
		"hash value": `    for value in [1]
      begin
        {
          "first": -> { raise "stop" }.call(),
          "second": -> { @b = 1 }.call()
        }
      rescue
        nil
      end
    end`,
		"missing callee": `    for value in [1]
      begin
        nil.missing(-> { @b = 1 }.call())
      rescue
        nil
      end
    end`,
		"range endpoint": `    for value in [1]
      begin
        (1 / 0)..(-> { @b = 1; 2 }.call())
      rescue
        nil
      end
    end`,
		"invalid range start": `    for value in [1]
      begin
        "bad"..(-> { @b = 1; 2 }.call())
      rescue
        nil
      end
    end`,
		"oversized range start": `    for value in [1]
      begin
        9223372036854775808..(-> { @b = 1; 2 }.call())
      rescue
        nil
      end
    end`,
		"conditional branches": `    for value in [1]
      begin
        -> { raise "stop" }.call() ? -> { @b = 1 }.call() : -> { @b = 1 }.call()
      rescue
        nil
      end
    end`,
		"if statement branches and tail": `    for value in [1]
      begin
        if -> { raise "stop" }.call()
          -> { @b = 1 }.call()
        else
          -> { @b = 1 }.call()
        end
        -> { @b = 1 }.call()
      rescue
        nil
      end
    end`,
		"if expression later branch": `    for value in [1]
      begin
        selected = if false
          0
        elsif -> { raise "stop" }.call()
          1
        else
          -> { @b = 1; 2 }.call()
        end
      rescue
        nil
      end
    end`,
		"raise message": `    for value in [1]
      begin
        raise -> { raise "stop" }.call(), -> { @b = 1; "message" }.call()
      rescue
        nil
      end
    end`,
		"for iterable body and tail": `    for value in [1]
      begin
        for nested in -> { raise "stop" }.call()
          -> { @b = 1 }.call()
        end
        -> { @b = 1 }.call()
      rescue
        nil
      end
    end`,
		"while condition body and tail": `    for value in [1]
      begin
        while -> { raise "stop" }.call()
          -> { @b = 1 }.call()
        end
        -> { @b = 1 }.call()
      rescue
        nil
      end
    end`,
		"until condition body and tail": `    for value in [1]
      begin
        until -> { raise "stop" }.call()
          -> { @b = 1 }.call()
        end
        -> { @b = 1 }.call()
      rescue
        nil
      end
    end`,
		"yield argument": `    for value in [1]
      begin
        yield -> { raise "stop" }.call(), -> { @b = 1 }.call()
      rescue
        nil
      end
    end`,
		"interpolation part": `    for value in [1]
      begin
        "#{-> { raise "stop" }.call()}#{-> { @b = 1 }.call()}"
      rescue
        nil
      end
    end`,
		"case target": `    for value in [1]
      begin
        case -> { raise "stop" }.call()
        when -> { @b = 1; 1 }.call()
          -> { @b = 1 }.call()
        else
          -> { @b = 1 }.call()
        end
      rescue
        nil
      end
    end`,
		"case value": `    for value in [1]
      begin
        case 1
        when -> { raise "stop" }.call(), -> { @b = 1; 1 }.call()
          -> { @b = 1 }.call()
        else
          -> { @b = 1 }.call()
        end
      rescue
        nil
      end
    end`,
		"unreachable rescue fallback": `    for value in [1]
      1 rescue -> { @b = 1 }.call()
    end`,
	}

	for name, region := range regions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
`+region+`
    @a = @b
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @a expected int, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want the unset @b warning", warnings)
			}
		})
	}
}

func TestCheckInitializerIvarRangeStartFailureSkipsEndEffects(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      begin
        (1 / 0)..(-> { @b = 1; 2 }.call())
      rescue
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
	requireCheckWarningContains(t, script, "write to @a expected int, got nil")
	requireCallErrorContains(
		t,
		script,
		"run",
		nil,
		CallOptions{},
		"instance variable @a expected int, got nil",
	)
}

func TestCheckInitializerIvarBlocklessYieldDoesNotWidenRepeatedFacts(t *testing.T) {
	t.Parallel()

	t.Run("blockless constructor", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      begin
        yield
      rescue
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new() do |value|
    value.b = 1
  end
  User.new().a
end
`)
		if warnings := script.CheckWarnings(); len(warnings) != 0 {
			t.Fatalf("CheckWarnings() = %#v, want the pristine block path to stay conservative", warnings)
		}
		warnings := script.CheckWarningsForFunction("run")
		gotWarnings := strings.Join(checkWarningMessages(warnings), "\n")
		if !strings.Contains(gotWarnings, "write to @a expected int, got nil") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %q, want the unset @b warning",
				"run",
				gotWarnings,
			)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got nil",
		)
	})

	t.Run("explicit nil block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      begin
        yield
      rescue
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new(&nil).a
end
`)
		warnings := script.CheckWarningsForFunction("run")
		gotWarnings := strings.Join(checkWarningMessages(warnings), "\n")
		if !strings.Contains(gotWarnings, "write to @a expected int, got nil") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %q, want the unset @b warning",
				"run",
				gotWarnings,
			)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got nil",
		)
	})

	t.Run("forwarded explicit nil block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def callback
    yield
  end

  def initialize
    for value in [1]
      begin
        callback(&nil)
      rescue
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
		warnings := script.CheckWarningsForFunction("run")
		gotWarnings := strings.Join(checkWarningMessages(warnings), "\n")
		if !strings.Contains(gotWarnings, "write to @a expected int, got nil") {
			t.Fatalf(
				"CheckWarningsForFunction(%q) = %q, want the unset @b warning",
				"run",
				gotWarnings,
			)
		}
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @a expected int, got nil",
		)
	})

	t.Run("constructor with block", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      yield self
    end
    @a = @b
  end
end

def run
  user = User.new() do |value|
    value.b = 1
  end
  user.a
end
`)
		if warnings := script.CheckWarningsForFunction("run"); len(warnings) != 0 {
			t.Fatalf("CheckWarningsForFunction(%q) = %#v, want none", "run", warnings)
		}
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})
}

func TestCheckInitializerIvarRepeatedTryFollowsRuntimePaths(t *testing.T) {
	t.Parallel()

	regions := map[string]string{
		"exact rescue selection": `    for value in [1]
      begin
        raise TypeError, "stop"
      rescue ArgumentError
        @b = 1
      rescue TypeError
        nil
      end
    end`,
		"else only follows normal completion": `    for value in [1]
      begin
        raise TypeError, "stop"
      rescue TypeError
        nil
      else
        @b = 1
      end
    end`,
		"unmatched error stops the protected tail": `    for value in [1]
      begin
        begin
          raise TypeError, "stop"
        rescue ArgumentError
          nil
        end
        @b = 1
      rescue TypeError
        nil
      end
    end`,
		"break does not enter rescue": `    for value in [1]
      begin
        break
      rescue
        @b = 1
      end
      @b = 1
    end`,
		"literal local assignment does not enter rescue": `    for value in [1]
      begin
        x = 1
      rescue
        @b = 1
      end
    end`,
	}

	for name, region := range regions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
`+region+`
    @a = @b
  end
end

def run
  User.new().a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarRepeatedTryKeepsDynamicRaiseKindsConservative(t *testing.T) {
	t.Parallel()

	regions := map[string]string{
		"shadowed error class": `    ArgumentError = "not a class"
    for value in [1]
      begin
        raise ArgumentError, "stop"
      rescue ArgumentError
        nil
      rescue TypeError
        @b = 1
      end
    end`,
		"oversized error message": `    for value in [1]
      begin
        raise ArgumentError, 9223372036854775808
      rescue ArgumentError
        nil
      rescue TypeError
        @b = 1
      end
    end`,
	}

	for name, region := range regions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
`+region+`
    @a = @b
  end
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarRepeatedTryAlwaysRunsEnsure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      begin
        begin
          raise TypeError, "stop"
        rescue ArgumentError
          nil
        ensure
          @b = 1
        end
      rescue TypeError
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarStaticallyEnteredLoopStopsUnreachableTail(t *testing.T) {
	t.Parallel()

	regions := map[string]string{
		"nonempty for": `      begin
        for nested in [1]
          raise "stop"
        end
        @b = 1
      rescue
        nil
      end`,
		"while true": `      begin
        while true
          raise "stop"
        end
        @b = 1
      rescue
        nil
      end`,
		"until false": `      begin
        until false
          raise "stop"
        end
        @b = 1
      rescue
        nil
      end`,
	}

	for name, region := range regions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
`+region+`
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got nil")
			requireCallErrorContains(
				t,
				script,
				"run",
				nil,
				CallOptions{},
				"instance variable @a expected int, got nil",
			)
		})
	}
}

func TestCheckInitializerIvarStaticallyEnteredLoopControlReachesTail(t *testing.T) {
	t.Parallel()

	regions := map[string]string{
		"nonempty for": `    for nested in [1]
      break
    end`,
		"nonempty for protected next": `    for nested in [1]
      begin
        next
      ensure
        nil
      end
    end`,
		"while true": `    while true
      break
    end`,
		"while true protected break": `    while true
      begin
        break
      ensure
        nil
      end
    end`,
		"until false": `    until false
      break
    end`,
	}

	for name, region := range regions {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
`+region+`
    @b = 1
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarRetainedProcReturnBypassesRescueAndRunsEnsure(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    @a = 1
    begin
      proc { return }.call()
    rescue
      @a = "rescued"
    ensure
      @b = 2
    end
    @a = "continued"
  end
end

def run
  user = User.new()
  user.a + user.b
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 3 {
		t.Fatalf("run() = %v, want 3", got)
	}
}

func TestCheckInitializerIvarRetainedBlockLocalAndFailureCompletion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		region string
	}{
		{
			name: "raise reaches rescue",
			region: `    callback = proc { raise "stop" }
    begin
      callback.call()
    rescue
      @b = 2
    end`,
		},
		{
			name: "lambda return stays local",
			region: `    callback = lambda { return }
    callback.call()
    @b = 2`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    @a = 1
`+tc.region+`
    @a = @b
  end
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 2 {
				t.Fatalf("run() = %v, want 2", got)
			}
		})
	}
}

func TestCheckInitializerIvarRetainedProcReturnStopsKnownEnteredLoop(t *testing.T) {
	t.Parallel()

	loops := map[string]string{
		"nonempty for": `    for item in [1]
      callback.call()
    end`,
		"while true": `    while true
      callback.call()
    end`,
		"until false": `    until false
      callback.call()
    end`,
	}

	for name, loop := range loops {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    @a = 1
    callback = proc { return }
`+loop+`
    @a = "continued"
  end
end

def run
  User.new().a
end
`)
			requireNoCheckWarnings(t, script)
			got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
			if got.Kind() != KindInt || got.Int() != 1 {
				t.Fatalf("run() = %v, want 1", got)
			}
		})
	}
}

func TestCheckInitializerIvarRetainedProcReturnKeepsMaybeZeroLoopTail(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		param      string
		loop       string
		runtimeArg Value
	}{
		{
			name:  "for unknown collection",
			param: "items: array<int>",
			loop: `    for item in items
      callback.call()
    end`,
			runtimeArg: NewArray(nil),
		},
		{
			name:  "while unknown condition",
			param: "flag: bool",
			loop: `    while flag
      callback.call()
    end`,
			runtimeArg: NewBool(false),
		},
		{
			name:  "until unknown condition",
			param: "flag: bool",
			loop: `    until flag
      callback.call()
    end`,
			runtimeArg: NewBool(true),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
class User
  property a: int

  def initialize(`+tc.param+`)
    @a = 1
    callback = proc { return }
`+tc.loop+`
    @a = "continued"
  end
end

def run(value)
  User.new(value).a
end
`)
			requireCheckWarningContains(t, script, "write to @a expected int, got string")
			requireCallErrorContains(
				t,
				script,
				"run",
				[]Value{tc.runtimeArg},
				CallOptions{},
				"instance variable @a expected int, got string",
			)
		})
	}
}

func TestCheckInitializerIvarMinimumRangeStartReachesEndEffects(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize
    for value in [1]
      (-9223372036854775808)..(-> { @b = 1; 2 }.call())
    end
    @a = @b
  end
end

def run
  User.new().a
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarCaseKeepsLaterCandidateEffects(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class User
  property a: int
  property b: int

  def initialize(target: int)
    for value in [1]
      begin
        case target
        when 1, -> { @b = 1; 2 }.call()
          -> { raise "stop" }.call()
        end
      rescue
        nil
      end
    end
    @a = @b
  end
end

def run
  User.new(2).a
end
`)
	requireNoCheckWarnings(t, script)
	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
}

func TestCheckInitializerIvarAssignmentReadsDoNotInvokeStoredCallbacks(t *testing.T) {
	t.Parallel()

	for _, region := range []string{
		`    callback ||= nil`,
		`    for value in [1]
      callback ||= nil
    end`,
		`    for value in [1]
      @callback = callback
    end`,
	} {
		script := compileScriptDefault(t, `
class User
  property callback: function
  property a: int
  property b: int

  def initialize
    callback = -> { @b = 1 }
`+region+`
    @a = @b
  end
end
`)
		warnings := script.CheckWarnings()
		if len(warnings) != 1 ||
			warnings[0].Message != "write to @a expected int, got nil" {
			t.Fatalf("CheckWarnings() = %#v, want the unset @b warning", warnings)
		}
	}
}

func TestCheckInitializerIvarIndexGetterUsesEvaluatedSelectors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		expression string
	}{
		{
			name: "direct",
			expression: `    box[
      choose_b ? -> { @b = 1 } : -> { @c = 1 },
      -> {
        choose_b = false
        0
      }.call()
    ]`,
		},
		{
			name: "repeated",
			expression: `    for value in [1]
      choose_b = true
      box[
        choose_b ? -> { @b = 1 } : -> { @c = 1 },
        -> {
          choose_b = false
          0
        }.call()
      ]
    end`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Box
  def [](callback: function, index: int) -> int
    callback.call()
    0
  end

  def []=(callback: function, index: int, value: int)
    nil
  end
end

class User
  property a: int
  property b: int
  property c: int

  def initialize(box: Box, flag: bool)
    choose_b = true
`+tc.expression+`
    if flag
      @a = @b
    else
      @a = @c
    end
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @a expected int, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
			}
		})
	}
}

func TestCheckInitializerIvarRejectedExactCallbacksPreserveUnsetFacts(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Invoker
  def +(callback: function)
    callback.call(1)
    self
  end
end

class User
  property a: int
  property b: int

  def initialize(invoker: Invoker)
    for value in [1]
      invoker + -> { @b = 1 }
    end
    @a = @b
  end
end
`)
	warnings := script.CheckWarnings()
	if len(warnings) != 1 ||
		warnings[0].Message != "write to @a expected int, got nil" {
		t.Fatalf("CheckWarnings() = %#v, want the unset @b warning", warnings)
	}
}

func TestCheckInitializerIvarOperatorUsesEvaluatedRightValue(t *testing.T) {
	t.Parallel()

	right := `callbacks[
      -> {
        callbacks = [-> { @c = "set" }]
        true
      }.call() ? 0 : 0
    ]`
	cases := []struct {
		name      string
		statement string
	}{
		{name: "direct", statement: `    invoker + ` + right},
		{name: "compound", statement: `    current = invoker
    current += ` + right},
		{name: "repeated", statement: `    for value in [1]
      callbacks = [-> { @b = 1 }]
      invoker + ` + right + `
    end`},
		{name: "repeated compound", statement: `    for value in [1]
      callbacks = [-> { @b = 1 }]
      current = invoker
      current += ` + right + `
    end`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
class Invoker
  def +(callback: function)
    callback.call()
    self
  end
end

class User
  property a: int
  property b: int
  property c: string
  property d: string

  def initialize(invoker: Invoker, flag: bool)
    callbacks = [-> { @b = 1 }]
`+tc.statement+`
    if flag
      @a = @b
    else
      @d = @c
    end
  end
end
`)
			warnings := script.CheckWarnings()
			if len(warnings) != 1 ||
				warnings[0].Message != "write to @d expected string, got nil" {
				t.Fatalf("CheckWarnings() = %#v, want only the unset @c warning", warnings)
			}
		})
	}
}

func initializerIvarSeedMethod(param, value string) string {
	if param != "" {
		param = "(" + param + ")"
	}
	return `
  def seed` + param + `
    @b = ` + value + `
  end
`
}
