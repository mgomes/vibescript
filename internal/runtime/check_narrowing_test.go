package runtime

import "testing"

// Narrowed facts are observed through two sinks: operand rejections that only
// fire once a local is known nil-only (`-x` → "unsupported unary - operand
// nil") or known non-nil at a disjoint boundary, and condition decisions that
// prune statically dead branches.

func TestCheckNullableNarrowingContradictions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "nil? true path is nil only",
			source: `
def f(x: int?)
  if x.nil?
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "nil? false path feeds typed boundary",
			source: `
def takes_int(value: int)
  value
end

def f(x: int?)
  if x.nil?
    takes_int(x)
  end
end
`,
			warning: "call to takes_int argument value expected int, got nil",
		},
		{
			name: "negated nil? narrows the else branch",
			source: `
def f(x: int?)
  if !x.nil?
    1
  else
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "equality comparison narrows",
			source: `
def f(x: int?)
  if x == nil
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "reversed equality comparison narrows",
			source: `
def f(x: int?)
  if nil == x
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "inequality comparison narrows the else branch",
			source: `
def f(x: int?)
  if x != nil
    1
  else
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "unless narrows its body to falsy",
			source: `
def f(x: int?)
  unless x
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "elsif condition narrows its branch",
			source: `
def f(x: int?, flag)
  if flag
    1
  elsif x.nil?
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "ternary narrows the consequent",
			source: `
def f(x: int?)
  y = x.nil? ? -x : 0
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "guard clause narrows the fall through",
			source: `
def f(x: int?)
  return 0 unless x.nil?
  y = -x
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "raise guard narrows the fall through",
			source: `
def f(x: int?)
  raise "missing" if x != nil
  y = -x
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "short circuit and narrows its right operand",
			source: `
def f(x: int?)
  x.nil? && -x
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "short circuit or narrows its right operand",
			source: `
def f(x: int?)
  !x.nil? || -x
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "nil? call form narrows",
			source: `
def f(x: int?)
  if x.nil?()
    y = -x
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "while condition narrows its body with the true outcome",
			source: `
def f(x: int?)
  while x.nil?
    y = -x
    break
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "until condition narrows its body with the false outcome",
			source: `
def f(x: int?)
  until x != nil
    y = -x
    break
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "nullable array nil? keeps its fact for the true path",
			source: `
def f(values: array<int>?)
  if values.nil?
    y = -values
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "nullable hash nil? call keeps its fact for the true path",
			source: `
def f(values: hash<string, int>?)
  if values.nil?()
    y = -values
  end
end
`,
			warning: "unsupported unary - operand nil",
		},
		{
			name: "nullable shape nil? guard keeps its fact for the false path",
			source: `
def takes_string(value: string)
  value
end

def f(value: { id: int }?)
  return if value.nil?
  takes_string(value[:id])
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckNullableNarrowingStaysConservative(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "truthiness prunes a nil-guarded dead branch",
			source: `
def f(x: int?)
  return "" if x.nil?
  if x
    1
  else
    y = -"s"
  end
end
`,
		},
		{
			name: "branch join re-widens the fact",
			source: `
def f(x: int?)
  if x.nil?
    1
  end
  y = -x
end
`,
		},
		{
			name: "loop exit re-widens the body fact",
			source: `
def f(x: int?)
  while x.nil?
    break
  end
  y = -x
end
`,
		},
		{
			name: "assignment in the narrowed branch joins back",
			source: `
def takes_int(value: int)
  value
end

def f(x: int?)
  if x.nil?
    x = 1
  end
  takes_int(x)
end
`,
		},
		{
			name: "unknown locals stay unknown",
			source: `
def f(v)
  if v.nil?
    y = -v
  end
end
`,
		},
		{
			name: "bool arms survive the falsy path",
			source: `
def f(flag: bool?)
  unless flag
    y = -flag
  end
end
`,
		},
		{
			name: "safe navigation nil? does not narrow",
			source: `
def f(x: int?)
  if x&.nil?
    y = -x
  end
end
`,
		},
		{
			name: "named arms disable nil-only narrowing",
			source: `
class Sneaky
  def initialize()
  end

  def nil?()
    true
  end
end

def f(s: Sneaky?)
  if s.nil?
    y = -s
  end
end
`,
		},
		{
			name: "container union with a named nil? override stays conservative",
			source: `
class Sneaky
  def initialize()
  end

  def nil?()
    true
  end
end

def f(value: array<int> | Sneaky | nil)
  if value.nil?
    y = -value
  end
end
`,
		},
		{
			name: "nil? with arguments does not narrow",
			source: `
def f(x: int?)
  if x.respond_to?(:nil?)
    y = -x
  end
end
`,
		},
		{
			// Begin blocks join their body state conservatively (a rescue
			// path may observe partial execution), so narrowing established
			// inside the body re-widens after `end` instead of persisting.
			name: "begin block join re-widens narrowed facts",
			source: `
def f(x: int?)
  begin
    return 0 unless x.nil?
  ensure
    x
  end
  y = -x
end
`,
		},
		{
			name: "dead nil branch does not invent facts",
			source: `
def f(x: int)
  if x.nil?
    y = -x
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
