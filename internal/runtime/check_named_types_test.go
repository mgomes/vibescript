package runtime

import "testing"

// The scaffolding below binds named facts through annotated parameters: the
// checker seeds a parameter's fact from its annotation, so passing it onward
// exercises resolved named-type compatibility at the receiving boundary.

func TestCheckNamedTypeContradictions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "enum fact into different enum",
			source: `
enum Color
  Red
end

enum Status
  Active
end

def takes_status(value: Status)
  value
end

def run(c: Color)
  takes_status(c)
end
`,
			warning: "call to takes_status argument value expected Status, got Color",
		},
		{
			name: "enum fact into string boundary",
			source: `
enum Color
  Red
end

def takes_string(value: string)
  value
end

def run(c: Color)
  takes_string(c)
end
`,
			warning: "call to takes_string argument value expected string, got Color",
		},
		{
			name: "int return contradicts enum annotation",
			source: `
enum Color
  Red
end

def pick() -> Color
  5
end
`,
			warning: "return value expected Color, got int",
		},
		{
			name: "annotated enum return flows to boundary",
			source: `
enum Color
  Red
end

def pick(c: Color) -> Color
  c
end

def takes_int(value: int)
  value
end

def run(c: Color)
  takes_int(pick(c))
end
`,
			warning: "call to takes_int argument value expected int, got Color",
		},
		{
			name: "class fact into different class",
			source: `
class User
  def initialize()
  end
end

class Order
  def initialize()
  end
end

def takes_order(value: Order)
  value
end

def run(u: User)
  takes_order(u)
end
`,
			warning: "call to takes_order argument value expected Order, got User",
		},
		{
			name: "class fact into enum boundary",
			source: `
enum Color
  Red
end

class User
  def initialize()
  end
end

def takes_color(value: Color)
  value
end

def run(u: User)
  takes_color(u)
end
`,
			warning: "call to takes_color argument value expected Color, got User",
		},
		{
			name: "class fact into unrelated module",
			source: `
module Nameable
  def display_name
    "n"
  end
end

class User
  def initialize()
  end
end

def takes_nameable(value: Nameable)
  value
end

def run(u: User)
  takes_nameable(u)
end
`,
			warning: "call to takes_nameable argument value expected Nameable, got User",
		},
		{
			name: "enum fact into hash boundary",
			source: `
enum Color
  Red
end

def takes_hash(value: hash)
  value
end

def run(c: Color)
  takes_hash(c)
end
`,
			warning: "call to takes_hash argument value expected hash, got Color",
		},
		{
			name: "reassignment of enum local to int",
			source: `
enum Color
  Red
end

def run(c: Color)
  c = 5
end
`,
			warning: "reassignment of c expected Color, got int",
		},
		{
			name: "enum fact as JSON.parse_as payload",
			source: `
enum Color
  Red
end

def run(c: Color)
  JSON.parse_as(c, { name: string })
end
`,
			warning: "call to JSON.parse_as expects a JSON string as its first argument, got Color",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckNamedTypeCompatibilityPreserved(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "same enum stays compatible",
			source: `
enum Color
  Red
end

def takes_color(value: Color)
  value
end

def run(c: Color)
  takes_color(c)
end
`,
		},
		{
			name: "symbol coerces into enum",
			source: `
enum Color
  Red
end

def takes_color(value: Color)
  value
end

def run(s: symbol)
  takes_color(s)
  takes_color(:red)
end
`,
		},
		{
			name: "enum fact stays conservative at symbol boundary",
			source: `
enum Color
  Red
end

def takes_symbol(value: symbol)
  value
end

def run(c: Color)
  takes_symbol(c)
end
`,
		},
		{
			name: "nullable enum fact requires nullable boundary",
			source: `
enum Color
  Red
end

def takes_color(value: Color)
  value
end

def run(c: Color?)
  takes_color(c)
end
`,
			warning: "call to takes_color argument value expected Color, got Color?",
		},
		{
			name: "union fact requires full boundary assignability",
			source: `
enum Color
  Red
end

def takes_int(value: int)
  value
end

def run(v: Color | int)
  takes_int(v)
end
`,
			warning: "call to takes_int argument value expected int, got Color | int",
		},
		{
			name: "same class stays compatible",
			source: `
class User
  def initialize()
  end
end

def takes_user(value: User)
  value
end

def run(u: User)
  takes_user(u)
end
`,
		},
		{
			name: "class satisfies included module",
			source: `
module Nameable
  def display_name
    "n"
  end
end

class User
  include Nameable

  def initialize()
  end
end

def takes_nameable(value: Nameable)
  value
end

def run(u: User)
  takes_nameable(u)
end
`,
		},
		{
			name: "class satisfies transitively included module",
			source: `
module Printable
  def print_it
    "p"
  end
end

module Nameable
  include Printable

  def display_name
    "n"
  end
end

class User
  include Nameable

  def initialize()
  end
end

def takes_printable(value: Printable)
  value
end

def run(u: User)
  takes_printable(u)
end
`,
		},
		{
			name: "module facts stay compatible with each other",
			source: `
module Nameable
  def display_name
    "n"
  end
end

module Printable
  def print_it
    "p"
  end
end

def takes_printable(value: Printable)
  value
end

def run(n: Nameable)
  takes_printable(n)
end
`,
		},
		{
			name: "enum reassignment to symbol stays legal",
			source: `
enum Color
  Red
end

def run(c: Color)
  c = :Red
end
`,
		},
		{
			name: "nil initialization of enum local stays legal",
			source: `
enum Color
  Red
end

def run(c: Color?)
  c = nil
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

// TestCheckQualifiedNamedTypesCompareByResolvedIdentity pins that two
// spellings of one required enum stay compatible while enums from different
// modules contradict.
func TestCheckQualifiedNamedTypesCompareByResolvedIdentity(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t,
		moduleFile{path: "shapes.vibe", content: `
enum Level
  Low
  High
end
`},
		moduleFile{path: "grades.vibe", content: `
enum Grade
  Pass
  Fail
end
`},
	)
	engine := mustNewEngineWithModuleRoot(t, root)

	sameDef, err := engine.CompileSnippet(`
require("shapes", as: "A")
require("shapes", as: "B")

def takes_level(value: B.Level)
  value
end

def run(level: A.Level)
  takes_level(level)
end
`, "__main")
	if err != nil {
		t.Fatalf("compile snippet: %v", err)
	}
	requireNoCheckWarnings(t, sameDef)

	crossModule, err := engine.CompileSnippet(`
require("shapes", as: "A")
require("grades", as: "G")

def takes_grade(value: G.Grade)
  value
end

def run(level: A.Level)
  takes_grade(level)
end
`, "__main")
	if err != nil {
		t.Fatalf("compile snippet: %v", err)
	}
	requireCheckWarningContains(t, crossModule, "call to takes_grade argument value expected G.Grade, got A.Level")
}
