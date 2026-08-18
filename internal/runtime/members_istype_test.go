package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestIsTypeAtomIdentSegments pins the accept/reject set of the qualified-atom
// check, whose segment counting no longer runs off a split of every dot.
func TestIsTypeAtomIdentSegments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		base string
		want bool
	}{
		{base: "int", want: true},
		{base: "User", want: true},
		{base: "_x9", want: true},
		{base: "lv.Level", want: true},
		{base: "", want: false},
		{base: "9x", want: false},
		{base: ".", want: false},
		{base: "lv.", want: false},
		{base: ".Level", want: false},
		{base: "a.b", want: true},
		{base: "a.b.c", want: false},
		{base: "a..b", want: false},
		{base: "array<int>", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.base, func(t *testing.T) {
			t.Parallel()
			if got := isTypeAtomIdent(tc.base); got != tc.want {
				t.Fatalf("isTypeAtomIdent(%q) = %v, want %v", tc.base, got, tc.want)
			}
		})
	}
}

func TestIsTypePredicateNamedAtoms(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `enum Status
  Draft
end

class User
  def initialize()
  end
end

def run()
  [User.new.is_type?(:User), User.new.is_type?(:Status), Status::Draft.is_type?(:Status), Status::Draft.is_type?("Status?"), User.new.is_type?(:symbol)]
end`)
	got := callFunc(t, script, "run", nil)
	want := []bool{true, false, true, true, false}
	arr := got.Array()
	if len(arr) != len(want) {
		t.Fatalf("run() returned %d results, want %d", len(arr), len(want))
	}
	for i, w := range want {
		if arr[i].Kind() != KindBool || arr[i].Bool() != w {
			t.Fatalf("result %d = %#v, want %v", i, arr[i], w)
		}
	}
}

func TestIsTypePredicateErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want string
	}{
		{name: "no arguments", expr: "1.is_type?()", want: "is_type? expects exactly one argument"},
		{name: "non atom argument", expr: "1.is_type?(2)", want: "is_type? expects a symbol or string type atom"},
		{name: "generic spelling", expr: "1.is_type?(\"array<int>\")", want: "is_type? supports type atoms only"},
		{name: "unknown lowercase atom", expr: "1.is_type?(:whatever)", want: "unknown type atom"},
		{name: "any is rejected", expr: "1.is_type?(:any)", want: "unknown type atom"},
		{name: "block rejected", expr: "1.is_type?(:int) { 2 }", want: "is_type? does not take a block"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, "def run()\n  "+tc.expr+"\nend")
			_, err := script.Call(t.Context(), "run", nil, CallOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s error = %v, want substring %q", tc.expr, err, tc.want)
			}
		})
	}
}

func TestIsTypePredicateClassOverrideWins(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class Sneaky
  def initialize()
  end

  def is_type?(atom)
    "overridden"
  end
end

def run()
  Sneaky.new.is_type?(:int)
end`)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindString || got.String() != "overridden" {
		t.Fatalf("override result = %#v, want \"overridden\"", got)
	}
}

func TestIsTypePredicateQualifiedModuleAtoms(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{path: "levels.vibe", content: "enum Level\n  Low\n  High\nend\n\ndef pick() -> Level\n  :low\nend\n"})
	engine := mustNewEngineWithModuleRoot(t, dir)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("levels", as: "lv")
  v = lv.pick()
  [v.is_type?("lv.Level"), v.is_type?("lv.Level?"), 1.is_type?("lv.Level"), nil.is_type?("lv.Level?")]
end
`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call(run) error: %v", err)
	}
	want := []bool{true, true, false, true}
	arr := got.Array()
	if len(arr) != len(want) {
		t.Fatalf("run() returned %d results, want %d", len(arr), len(want))
	}
	for i, w := range want {
		if arr[i].Kind() != KindBool || arr[i].Bool() != w {
			t.Fatalf("result %d = %#v, want %v", i, arr[i], w)
		}
	}
}

func TestIsTypePredicateQualifiedAtomsKeepDefinitionIdentity(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t,
		moduleFile{path: "a.vibe", content: "enum Status\n  Ok\nend\n\ndef pick() -> Status\n  :ok\nend\n"},
		moduleFile{path: "b.vibe", content: "enum Status\n  Ok\nend\n"},
	)
	engine := mustNewEngineWithModuleRoot(t, dir)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("a", as: "a")
  require("b", as: "b")
  v = a.pick()
  [v.is_type?("a.Status"), v.is_type?("b.Status")]
end
`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call(run) error: %v", err)
	}
	arr := got.Array()
	if len(arr) != 2 || !arr[0].Bool() || arr[1].Bool() {
		t.Fatalf("run() = %v, want [true, false]: same-named enums from different modules stay distinct", got)
	}
}

func TestIsTypePredicateUnqualifiedModuleAtomUsesModuleDefinition(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t,
		moduleFile{path: "a.vibe", content: "enum Status\n  Ok\nend\n"},
		moduleFile{path: "b.vibe", content: `enum Status
  Ok
end

def pick() -> Status
  :ok
end

def matches(value)
  value.is_type?(:Status)
end
`},
	)
	engine := mustNewEngineWithModuleRoot(t, dir)
	script := compileScriptWithEngine(t, engine, `
def run()
  require("a")
  require("b", as: "b")
  b.matches(b.pick())
end
`)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("Call(run) error: %v", err)
	}
	if got.Kind() != KindBool || !got.Bool() {
		t.Fatalf("run() = %#v, want true: unqualified atoms use their module definition", got)
	}
}

func TestIsTypePredicateUnknownQualifiedAtomErrors(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, "def run()\n  1.is_type?(\"missing.Level\")\nend")
	nilScript := compileScriptDefault(t, "def run()\n  nil.is_type?(\"missing.Level?\")\nend")
	if _, err := nilScript.Call(context.Background(), "run", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "unknown type atom") {
		t.Fatalf("nil receiver error = %v, want unknown qualified atom error", err)
	}
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "unknown type atom \"missing.Level\"") {
		t.Fatalf("Call error = %v, want unknown qualified atom error", err)
	}
}

func TestIsTypePredicateNamedAtomsAreCaseExact(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `class User
  def initialize()
  end
end

def run()
  User.new.is_type?(:USER)
end`)
	got := callFunc(t, script, "run", nil)
	if got.Kind() != KindBool || got.Bool() {
		t.Fatalf("is_type?(:USER) = %#v, want false: named atoms match by exact spelling", got)
	}

	nilScript := compileScript(t, `class User
  def initialize()
  end
end

def run()
  [nil.is_type?("USER?"), nil.is_type?("User?")]
end`)
	arr := callFunc(t, nilScript, "run", nil).Array()
	if len(arr) != 2 || arr[0].Bool() || !arr[1].Bool() {
		t.Fatalf("nullable named atoms on nil = %v, want [false, true]: only a resolved exact name admits nil", arr)
	}
}
