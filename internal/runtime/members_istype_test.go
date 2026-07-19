package runtime

import (
	"context"
	"strings"
	"testing"
)

func TestIsTypePredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{name: "int matches int", expr: "1.is_type?(:int)", want: true},
		{name: "int is not float", expr: "1.is_type?(:float)", want: false},
		{name: "int matches number", expr: "1.is_type?(:number)", want: true},
		{name: "float matches number", expr: "1.5.is_type?(:number)", want: true},
		{name: "string does not coerce to int", expr: "\"5\".is_type?(:int)", want: false},
		{name: "string matches string", expr: "\"5\".is_type?(:string)", want: true},
		{name: "string atom spelling", expr: "1.is_type?(\"int\")", want: true},
		{name: "bool matches bool", expr: "true.is_type?(:bool)", want: true},
		{name: "nil matches nil", expr: "nil.is_type?(:nil)", want: true},
		{name: "nil matches nullable", expr: "nil.is_type?(\"int?\")", want: true},
		{name: "int matches nullable int", expr: "1.is_type?(\"int?\")", want: true},
		{name: "string misses nullable int", expr: "\"s\".is_type?(\"int?\")", want: false},
		{name: "symbol matches symbol", expr: ":ok.is_type?(:symbol)", want: true},
		{name: "symbol is not string", expr: ":ok.is_type?(:string)", want: false},
		{name: "array matches array", expr: "[1].is_type?(:array)", want: true},
		{name: "hash matches hash", expr: "({\"a\": 1}).is_type?(:hash)", want: true},
		{name: "object alias matches hash", expr: "({\"a\": 1}).is_type?(:object)", want: true},
		{name: "nullable object alias", expr: "nil.is_type?('object?')", want: true},
		{name: "range matches range", expr: "(1..3).is_type?(:range)", want: true},
		{name: "lambda matches function", expr: "->(x) { x }.is_type?(:function)", want: true},
		{name: "duration matches duration", expr: "5.minutes.is_type?(:duration)", want: true},
		{name: "duration is not time", expr: "5.minutes.is_type?(:time)", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := evalUniversal(t, tc.expr)
			if got.Kind() != KindBool || got.Bool() != tc.want {
				t.Fatalf("%s = %#v, want %v", tc.expr, got, tc.want)
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

func TestIsTypePredicateUnknownQualifiedAtomErrors(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, "def run()\n  1.is_type?(\"missing.Level\")\nend")
	if _, err := script.Call(context.Background(), "run", nil, CallOptions{}); err == nil || !strings.Contains(err.Error(), "unknown type atom \"missing.Level\"") {
		t.Fatalf("Call error = %v, want unknown qualified atom error", err)
	}
}
