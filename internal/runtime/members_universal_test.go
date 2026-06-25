package runtime

import "testing"

// TestEqualPredicateEnumClonedIdentity confirms equal? on enums and enum values
// reports backing-storage identity, not the structural equivalence Equal uses.
// A value cloned out to the host (for example, by a capability return) holds a
// fresh backing pointer while keeping the same owner script and member name.
// enumDefsEqual/enumValueDefsEqual treat such a clone as == (and eql?) to the
// original, but equal? must report false because the two no longer share
// storage. equal? against the same backing pointer still reports true.
func TestEqualPredicateEnumClonedIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `enum Status
  Draft
  Published
end`)

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	clonedDraft := cloneValueForHost(statusDraft)
	if clonedDraft.Kind() != KindEnumValue {
		t.Fatalf("cloneValueForHost(Status::Draft) kind = %v, want enum value", clonedDraft.Kind())
	}
	if EnumValueOf(clonedDraft) == EnumValueOf(statusDraft) {
		t.Fatal("cloneValueForHost(Status::Draft) shares the original backing pointer; test cannot observe the identity contract")
	}
	if !clonedDraft.Equal(statusDraft) {
		t.Fatal("cloned enum value should remain == to the original")
	}
	if clonedDraft.Eql(statusDraft) != clonedDraft.Equal(statusDraft) {
		t.Fatal("enum value eql? should match == (same kind and value)")
	}
	if clonedDraft.Identical(statusDraft) {
		t.Fatal("cloned enum value must not be equal? to the original; it holds distinct storage")
	}
	if !statusDraft.Identical(statusDraft) {
		t.Fatal("an enum value must be equal? to itself")
	}

	enumDef := NewEnum(script.enums["Status"])
	clonedEnum := cloneValueForHost(enumDef)
	if clonedEnum.Kind() != KindEnum {
		t.Fatalf("cloneValueForHost(Status) kind = %v, want enum", clonedEnum.Kind())
	}
	if EnumOf(clonedEnum) == EnumOf(enumDef) {
		t.Fatal("cloneValueForHost(Status) shares the original backing pointer; test cannot observe the identity contract")
	}
	if !clonedEnum.Equal(enumDef) {
		t.Fatal("cloned enum should remain == to the original")
	}
	if clonedEnum.Identical(enumDef) {
		t.Fatal("cloned enum must not be equal? to the original; it holds distinct storage")
	}
	if !enumDef.Identical(enumDef) {
		t.Fatal("an enum must be equal? to itself")
	}
}

// TestEqualPredicateEnumDispatchesIdentity confirms the universal equal?
// predicate on enums and enum values routes through Value.Identical rather than
// Value.Equal. The predicate builtin captures the receiver, so invoking it with
// a host clone that is Equal-but-not-Identical to the receiver must report
// false, while the receiver compared with itself reports true.
func TestEqualPredicateEnumDispatchesIdentity(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `enum Status
  Draft
  Published
end`)

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	clonedDraft := cloneValueForHost(statusDraft)

	probe, ok := universalMember(statusDraft, "equal?")
	if !ok {
		t.Fatal("universalMember did not resolve equal? for an enum value")
	}
	builtin := valueBuiltin(probe)
	if builtin == nil {
		t.Fatal("equal? did not resolve to a builtin")
	}

	got, err := builtin.Fn(nil, NewNil(), []Value{clonedDraft}, nil, NewNil())
	if err != nil {
		t.Fatalf("equal? against host clone: %v", err)
	}
	if got.Bool() {
		t.Fatal("equal? against a host clone returned true; it must dispatch to Identical and report false")
	}

	got, err = builtin.Fn(nil, NewNil(), []Value{statusDraft}, nil, NewNil())
	if err != nil {
		t.Fatalf("equal? against self: %v", err)
	}
	if !got.Bool() {
		t.Fatal("equal? against the same backing value returned false; it must report true")
	}
}

// TestEqlPredicate exercises the universal eql? predicate across core value
// kinds: it reports hash-key equality, so operands must share a kind and value.
// An Int never eql-matches a Float even when their numeric values coincide,
// mirroring Ruby's 1.eql?(1.0) == false.
func TestEqlPredicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "int equal", expr: `1.eql?(1)`, want: NewBool(true)},
		{name: "int unequal", expr: `1.eql?(2)`, want: NewBool(false)},
		{name: "int vs float", expr: `1.eql?(1.0)`, want: NewBool(false)},
		{name: "float equal", expr: `1.0.eql?(1.0)`, want: NewBool(true)},
		{name: "float vs int", expr: `1.0.eql?(1)`, want: NewBool(false)},
		{name: "string equal", expr: `"a".eql?("a")`, want: NewBool(true)},
		{name: "string unequal", expr: `"a".eql?("b")`, want: NewBool(false)},
		{name: "string vs int", expr: `"1".eql?(1)`, want: NewBool(false)},
		{name: "symbol equal", expr: `:a.eql?(:a)`, want: NewBool(true)},
		{name: "symbol vs string", expr: `:a.eql?("a")`, want: NewBool(false)},
		{name: "bool equal", expr: `true.eql?(true)`, want: NewBool(true)},
		{name: "bool unequal", expr: `true.eql?(false)`, want: NewBool(false)},
		{name: "nil equal", expr: `nil.eql?(nil)`, want: NewBool(true)},
		{name: "nil vs false", expr: `nil.eql?(false)`, want: NewBool(false)},
		{name: "range equal", expr: `(1..3).eql?(1..3)`, want: NewBool(true)},
		{name: "range unequal", expr: `(1..3).eql?(1..4)`, want: NewBool(false)},
		{name: "array by content", expr: `[1, 2].eql?([1, 2])`, want: NewBool(true)},
		{name: "array unequal content", expr: `[1, 2].eql?([1, 3])`, want: NewBool(false)},
		{name: "hash by content", expr: `{ a: 1 }.eql?({ a: 1 })`, want: NewBool(true)},
		{name: "duration equal", expr: `1.hour.eql?(1.hour)`, want: NewBool(true)},
		{name: "time equal", expr: `Time.utc(2024, 1, 1).eql?(Time.utc(2024, 1, 1))`, want: NewBool(true)},
		{name: "money equal", expr: `money("1.00 USD").eql?(money("1.00 USD"))`, want: NewBool(true)},
		{name: "money currency mismatch", expr: `money("1.00 USD").eql?(money("1.00 EUR"))`, want: NewBool(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEqualPredicateImmutableValues confirms equal? treats immutable scalar
// kinds as identical when they share a kind and value, since the language
// exposes no separate identity for equal immutables (1.equal?(1) is true).
func TestEqualPredicateImmutableValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want Value
	}{
		{name: "int identical", expr: `1.equal?(1)`, want: NewBool(true)},
		{name: "int different", expr: `1.equal?(2)`, want: NewBool(false)},
		{name: "int vs float", expr: `1.equal?(1.0)`, want: NewBool(false)},
		{name: "float identical", expr: `1.0.equal?(1.0)`, want: NewBool(true)},
		{name: "string identical content", expr: `"a".equal?("a")`, want: NewBool(true)},
		{name: "string different content", expr: `"a".equal?("b")`, want: NewBool(false)},
		{name: "symbol identical", expr: `:a.equal?(:a)`, want: NewBool(true)},
		{name: "bool identical", expr: `true.equal?(true)`, want: NewBool(true)},
		{name: "nil identical", expr: `nil.equal?(nil)`, want: NewBool(true)},
		{name: "range identical", expr: `(1..3).equal?(1..3)`, want: NewBool(true)},
		{name: "duration identical", expr: `1.hour.equal?(1.hour)`, want: NewBool(true)},
		{name: "time identical", expr: `Time.utc(2024, 1, 1).equal?(Time.utc(2024, 1, 1))`, want: NewBool(true)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tc.want) {
				t.Fatalf("%s = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestEqualPredicateReferenceIdentity confirms equal? on mutable composites and
// script instances reports backing-storage identity: a value is identical to
// itself and to an alias, but two independently constructed composites with the
// same contents are not identical even though they are eql?.
func TestEqualPredicateReferenceIdentity(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name: "array",
			script: `def run()
  a = [1, 2, 3]
  b = a
  c = [1, 2, 3]
  [a.equal?(b), a.equal?(c), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
		},
		{
			name: "hash",
			script: `def run()
  a = { x: 1 }
  b = a
  c = { x: 1 }
  [a.equal?(b), a.equal?(c), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true)},
		},
		{
			name: "instance",
			script: `class Box
end

def run()
  a = Box.new
  b = a
  c = Box.new
  [a.equal?(b), a.equal?(c), a.eql?(b), a.eql?(c)]
end`,
			want: []Value{NewBool(true), NewBool(false), NewBool(true), NewBool(false)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

// TestEqualityPredicateUserOverride confirms a class may define its own eql? or
// equal? methods, which take precedence over the universal fallback exactly as
// Ruby overrides Object#eql?/Object#equal?.
func TestEqualityPredicateUserOverride(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
	}{
		{name: "eql override", method: "eql?"},
		{name: "equal override", method: "equal?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, `class Tag
  def `+tc.method+`(other)
    "overridden"
  end
end

def run()
  Tag.new.`+tc.method+`(Tag.new)
end`)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(NewString("overridden")) {
				t.Fatalf("%s override = %v, want overridden", tc.method, got)
			}
		})
	}
}

// TestEqualityPredicateNotShadowedByStoredKeys confirms a hash or namespace
// object entry keyed "eql?" or "equal?" is treated as data, not a member, so it
// never preempts the universal equality predicate. Member dispatch (h.eql?)
// resolves the predicate while index access (h["eql?"]) still reads the stored
// value, matching Ruby's separation of method calls from element lookup.
func TestEqualityPredicateNotShadowedByStoredKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		want   []Value
	}{
		{
			name: "hash eql key",
			script: `def run()
  h = { "eql?": "data" }
  [h.eql?(h), h.eql?({ x: 1 }), h["eql?"]]
end`,
			want: []Value{NewBool(true), NewBool(false), NewString("data")},
		},
		{
			name: "hash equal key",
			script: `def run()
  h = { "equal?": 99 }
  alias = h
  [h.equal?(alias), h.equal?({}), h["equal?"]]
end`,
			want: []Value{NewBool(true), NewBool(false), NewInt(99)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			result := callFunc(t, script, "run", nil)
			compareArrays(t, result, tc.want)
		})
	}
}

// TestEqualityPredicatePrivateOverrideRaises confirms a private eql?/equal?
// override is not silently bypassed by the universal fallback. External lookup
// paths — a bound member probe and the reduce(:op) shorthand — must raise the
// private-method error rather than resolving the builtin predicate.
func TestEqualityPredicatePrivateOverrideRaises(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		script string
		fn     string
		want   string
	}{
		{
			name: "bound probe of private eql",
			script: `class Tag
  private def eql?(other)
    true
  end
end

def run()
  probe = Tag.new.eql?
  probe(Tag.new)
end`,
			fn:   "run",
			want: "private method eql?",
		},
		{
			name: "direct call of private equal",
			script: `class Tag
  private def equal?(other)
    true
  end
end

def run()
  Tag.new.equal?(Tag.new)
end`,
			fn:   "run",
			want: "private method equal?",
		},
		{
			name: "reduce sends private eql",
			script: `class Tag
  private def eql?(other)
    true
  end
end

def run()
  [Tag.new, Tag.new].reduce(:eql?)
end`,
			fn:   "run",
			want: "private method eql?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.script)
			requireCallErrorContains(t, script, tc.fn, nil, CallOptions{}, tc.want)
		})
	}
}

// TestEqualityPredicatePrivateMethodInternalAccess confirms the private-member
// suppression only blocks external callers: the receiver itself may still invoke
// its private eql?/equal? override, so the universal fallback never masks a
// legitimately reachable private method.
func TestEqualityPredicatePrivateMethodInternalAccess(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `class Tag
  private def eql?(other)
    "internal"
  end

  def probe(other)
    eql?(other)
  end
end

def run()
  Tag.new.probe(Tag.new)
end`)
	got := callFunc(t, script, "run", nil)
	if !got.Equal(NewString("internal")) {
		t.Fatalf("internal private eql? call = %v, want internal", got)
	}
}

// TestEqualityPredicateStoredBuiltin confirms the stored-member-call path, where
// the bound predicate builtin is invoked separately from the receiver, follows
// the same contract as a direct call.
func TestEqualityPredicateStoredBuiltin(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `def run()
  probe = 1.eql?
  identity = 1.equal?
  [probe(1), probe(1.0), probe(2), identity(1), identity(2)]
end`)
	result := callFunc(t, script, "run", nil)
	compareArrays(t, result, []Value{
		NewBool(true),
		NewBool(false),
		NewBool(false),
		NewBool(true),
		NewBool(false),
	})
}

// TestEqualityPredicateCallErrors confirms eql? and equal? reject the wrong
// arity, keyword arguments, and blocks rather than silently coercing them.
func TestEqualityPredicateCallErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "eql no args", expr: `1.eql?()`, want: "int.eql? expects 1 argument, got 0"},
		{name: "eql two args", expr: `1.eql?(1, 2)`, want: "int.eql? expects 1 argument, got 2"},
		{name: "equal no args", expr: `1.equal?()`, want: "int.equal? expects 1 argument, got 0"},
		{name: "eql kwargs", expr: `1.eql?(other: 1)`, want: "int.eql? does not accept keyword arguments"},
		{name: "eql block", expr: `1.eql?(1) { 2 }`, want: "int.eql? does not accept a block"},
		{name: "string eql block", expr: `"a".eql?("a") { 2 }`, want: "string.eql? does not accept a block"},
		{name: "array equal arity", expr: `[1].equal?()`, want: "array.equal? expects 1 argument, got 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tc.want)
		})
	}
}
