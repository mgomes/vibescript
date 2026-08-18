package runtime

import (
	"context"
	"io"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestWidenUnsetInstanceIvarFactsSkipsCleanState(t *testing.T) {
	t.Parallel()

	checker := &scriptChecker{
		selfClass:  &ClassDef{},
		localTypes: []checkTypeFrame{{}},
	}
	checker.bindLocalTypeInCurrentFrame(ivarFactKey("name"), checkTypeNil)
	if !checker.instanceIvarFactsDirty {
		t.Fatal("binding an ivar fact did not mark widening state dirty")
	}

	checker.widenUnsetInstanceIvarFacts()
	if checker.instanceIvarFactsDirty {
		t.Fatal("full-class widening did not mark ivar facts clean")
	}
	checker.widenUnsetInstanceIvarFacts()
	if checker.instanceIvarFactsDirty {
		t.Fatal("repeated widening changed clean ivar state")
	}
}

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

func TestCheckConditionalMemberReceiverDoesNotShapeRHS(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  property callback: string
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
	warnings := script.CheckWarnings()
	const want = "call to Box#callback= argument value expected string, got int"
	if len(warnings) != 1 || warnings[0].Message != want {
		t.Fatalf("CheckWarnings() = %#v, want only %q", warnings, want)
	}
}

func TestCheckUnannotatedCallReceiverDoesNotShapeRHS(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Box
  property callback: string
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
	warnings := script.CheckWarnings()
	const want = "call to Box#callback= argument value expected string, got int"
	if len(warnings) != 1 || warnings[0].Message != want {
		t.Fatalf("CheckWarnings() = %#v, want only %q", warnings, want)
	}
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

	script = compileScriptDefault(t, `
class Pair
  property a: int?

  def initialize(flag: bool)
    values = flag ? ["bad"] : ["worse"]
    @a, ignored = values
  end
end
`)
	requireCheckWarningContains(t, script, "write to @a expected int?, got string")
}

func TestCheckDestructuredIvarWritesPreserveEvaluatedScalarSnapshot(t *testing.T) {
	t.Parallel()

	t.Run("later live value cannot create a false positive", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property a: int

  def initialize
    source = [1]
    source[0], @a = ["bad", source[0]]
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

	t.Run("later live value cannot hide an invalid write", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
enum Status
  Draft
end

class User
  property status: Status

  def initialize
    source = [:bogus]
    source[0], @status = [:draft, source[0]]
  end
end

def run
  User.new()
end
`)

		requireCheckWarningContains(
			t,
			script,
			"write to @status expected Status, got symbol",
		)
		requireCallErrorContains(
			t,
			script,
			"run",
			nil,
			CallOptions{},
			"instance variable @status expected Status, got symbol",
		)
	})
}

func TestCheckNestedDestructuredIvarWritesRespectEvaluationOrder(t *testing.T) {
	t.Parallel()

	t.Run("nested level preserves its scalar snapshot", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property p: int

  def initialize
    source = [[1]]
    ignored, (source[0][0], @p) = [0, ["ok", source[0][0]]]
  end
end

def run
  User.new().p
end
`)

		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})

	t.Run("nested level preserves its outer container reference", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property p: int

  def initialize
    source = [[1]]
    source, (@p, ignored) = [[[2]], source[0]]
  end
end

def run
  User.new().p
end
`)

		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})

	t.Run("outer rest preserves its scalar snapshot", func(t *testing.T) {
		t.Parallel()

		script := compileScriptDefault(t, `
class User
  property p: int

  def initialize
    source = ["ok", 1]
    source[1], *(@p, ignored) = source
  end
end

def run
  User.new().p
end
`)

		requireNoCheckWarnings(t, script)
		got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
		if got.Kind() != KindInt || got.Int() != 1 {
			t.Fatalf("run() = %v, want 1", got)
		}
	})
}

// A literal right-hand side makes the rest split deterministic: the rest
// ivar receives the materialized window as an array, fixed targets before
// and trailing targets after the rest map to concrete indices (padding with
// nil when the literal runs short), and non-literal sources without a
// projectable type degrade to unknown.
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

// appendDestructureTargets closes a script with a destructure of source() into
// count targets.
func appendDestructureTargets(b *strings.Builder, count int) {
	for i := range count {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("a")
		b.WriteString(strconv.Itoa(i))
	}
	b.WriteString(" = source()\n  a0\nend\n")
}

// appendIntUnion writes count int arms joined into one union.
func appendIntUnion(b *strings.Builder, count int) {
	for i := range count {
		if i > 0 {
			b.WriteString("|")
		}
		b.WriteString("int")
	}
}

// wideUnionDestructureSource puts the union at the top of the return type, so
// every arm is an outer arm the projection iterates.
func wideUnionDestructureSource(count int) string {
	var b strings.Builder
	b.WriteString("def source() -> ")
	for i := range count {
		if i > 0 {
			b.WriteString("|")
		}
		b.WriteString("array<int>")
	}
	b.WriteString("\n  [1]\nend\n\ndef run\n  ")
	appendDestructureTargets(&b, count)
	return b.String()
}

// nestedUnionDestructureSource puts the union inside the array element, so the
// return type flattens to a single outer arm however wide the union is: an
// arm-count budget sees one arm, but every target still canonicalizes the whole
// union when it joins its candidate with nil.
func nestedUnionDestructureSource(count int) string {
	var b strings.Builder
	b.WriteString("def source() -> array<")
	appendIntUnion(&b, count)
	b.WriteString(">\n  [1]\nend\n\ndef run\n  ")
	appendDestructureTargets(&b, count)
	return b.String()
}

// deepUnionDestructureSource wraps the union one level deeper, so the projected
// candidate keeps it whole instead of deduplicating to int|nil. The cost then
// lands in the bytes each canonicalization builds rather than in the number of
// allocations, which is why the budget is measured both ways.
func deepUnionDestructureSource(count int) string {
	var b strings.Builder
	b.WriteString("def source() -> array<array<")
	appendIntUnion(&b, count)
	b.WriteString(">>\n  [[1]]\nend\n\ndef run\n  ")
	appendDestructureTargets(&b, count)
	return b.String()
}

// Three placements of one wide declared union. Each makes the projection cost
// the target count times the union's size; only the first also makes it the
// target count times the outer arm count.
var typedDestructureBudgetShapes = []struct {
	name  string
	build func(count int) string
}{
	{name: "union at the top of the return type", build: wideUnionDestructureSource},
	{name: "union inside the array element", build: nestedUnionDestructureSource},
	{name: "union below the array element", build: deepUnionDestructureSource},
}

// checkWarningCost reports the heap allocations and the bytes one CheckWarnings
// pass performs.
func checkWarningCost(tb testing.TB, script *Script) (allocs, bytes uint64) {
	tb.Helper()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	script.CheckWarnings()
	runtime.ReadMemStats(&after)
	return after.Mallocs - before.Mallocs, after.TotalAlloc - before.TotalAlloc
}

// The projection derives a candidate type per target and joins it, and joining
// canonicalizes the whole candidate, so its cost is the target count times the
// value type's size. Declared annotations are capped neither to
// maxInferredUnionArms nor in nesting, so all three placements below make that
// product quadratic in the source size, in a static check that runs outside the
// runtime's step and memory quotas. Measured unbudgeted at 1600 arms over 1600
// targets: 15.4M allocations and 592 MiB for the top union, 20.0M and 159 MiB
// for the nested one, and 1.4 GiB for the union below the element, which
// allocates only 45K times and so escapes an allocation count alone. All three
// fit in well under 90 KiB of source, far inside the default 1 MiB limit.
//
// Deliberately serial: runtime.MemStats is process-wide, and a concurrent
// test's allocations would land in the delta.
func TestCheckTypedDestructureProjectionStaysLinearInSourceSize(t *testing.T) {
	const (
		small = 400
		large = 1600
		// large is 4x small in both dimensions, so a linear cost grows about
		// 4x while the unbudgeted quadratic cost grows about 16x.
		maxGrowth = 8
	)

	for _, shape := range typedDestructureBudgetShapes {
		t.Run(shape.name, func(t *testing.T) {
			smallAllocs, smallBytes := checkWarningCost(t, compileScriptDefault(t, shape.build(small)))
			largeAllocs, largeBytes := checkWarningCost(t, compileScriptDefault(t, shape.build(large)))
			if largeAllocs > smallAllocs*maxGrowth {
				t.Errorf(
					"checking %d over %d allocated %d times against %d for %d over %d, want at most %dx growth",
					large, large, largeAllocs, smallAllocs, small, small, maxGrowth,
				)
			}
			if largeBytes > smallBytes*maxGrowth {
				t.Errorf(
					"checking %d over %d allocated %d bytes against %d for %d over %d, want at most %dx growth",
					large, large, largeBytes, smallBytes, small, small, maxGrowth,
				)
			}
		})
	}
}

// The budget must leave ordinary annotations projectable, nested ones included:
// a handful of arms over a handful of targets stays far below it and still
// reports the write that contradicts the property contract.
func TestCheckTypedDestructureProjectionKeepsOrdinaryUnions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		annotation string
		body       string
		want       string
	}{
		{
			name:       "union at the top of the return type",
			annotation: "array<string>|array<float>|array<bool>|string",
			body:       `["bad"]`,
			want:       "write to @a expected int",
		},
		{
			name:       "union inside the array element",
			annotation: "array<string|float|bool>",
			body:       `["bad"]`,
			want:       "write to @a expected int",
		},
		{
			name:       "union below the array element",
			annotation: "array<array<string|float|bool>>",
			body:       `[["bad"]]`,
			want:       "write to @a expected int",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			script := compileScriptDefault(t, `
def source() -> `+tc.annotation+`
  `+tc.body+`
end

class User
  property a: int

  def initialize
    @a, b, c, d = source()
    b
  end
end

def run
  User.new()
end
`)

			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

// Exceeding the budget must stay a fallback to unknown element facts, never an
// error and never a warning of its own: the same script is silent past the
// budget and keeps running, so a host statically gating untrusted scripts sees
// less inference rather than a rejection.
func TestCheckTypedDestructureProjectionBudgetFallsBackQuietly(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString("def source() -> array<")
	appendIntUnion(&b, 4096)
	b.WriteString(">\n  [1]\nend\n\nclass User\n  property a: int\n\n" +
		"  def initialize\n    @a, b = source()\n    b\n  end\nend\n\n" +
		"def run\n  User.new()\n  1\nend\n")

	script := compileScriptDefault(t, b.String())
	requireNoCheckWarnings(t, script)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 1 {
		t.Fatalf("run() = %v, want 1", got)
	}
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
// incompatible RHS warns; compatible spellings, including an auto-invoked
// bare builtin, stay quiet.
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
  property roll: number

  def defaults
    @seed ||= "s"
    @roll ||= rand
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

	requireCheckWarningContains(t, compileScriptDefault(t, `
def takes_string(value: string)
  value
end

class User
  property name: string

  def pass
    takes_string(@name)
  end
end
`), "call to takes_string argument value expected string, got string | nil")

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
		`{}[:missing]`,
		`{}["missing"]`,
		`{count: 1}[:missing]`,
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

// TestFreshInferenceScopeKeepsIvarDirtyMarker pins the dirty marker across
// withFreshLocalInferenceScope: the reset keeps the local type frames (a
// non-executing default walk reads the seeded @ facts through it), so the
// marker describing those frames must survive into the fresh scope and be
// restored with the other inference facts on exit.
func TestFreshInferenceScopeKeepsIvarDirtyMarker(t *testing.T) {
	t.Parallel()

	checker := &scriptChecker{
		selfClass:  &ClassDef{},
		localTypes: []checkTypeFrame{{}},
	}
	checker.bindLocalTypeInCurrentFrame(ivarFactKey("name"), checkTypeNil)
	if !checker.instanceIvarFactsDirty {
		t.Fatal("binding an ivar fact did not mark widening state dirty")
	}
	restore := checker.withFreshLocalInferenceScope()
	if !checker.instanceIvarFactsDirty {
		t.Fatal("the fresh scope cleared the marker while keeping the local type frames")
	}
	checker.instanceIvarFactsDirty = false
	restore()
	if !checker.instanceIvarFactsDirty {
		t.Fatal("the restore did not bring the entry marker back")
	}
}
