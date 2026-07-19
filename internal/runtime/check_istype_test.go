package runtime

import "testing"

// The is_type? universal predicate carries a typed contract and narrows known
// union locals through both condition branches.

func TestCheckIsTypeContract(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "result is bool",
			source: `
def takes_int(value: int)
  value
end

def run(s: string)
  takes_int(s.is_type?(:int))
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "rejects non atom argument",
			source: `
def run(s: string)
  s.is_type?(1)
end
`,
			warning: "call to is_type? argument 1 expected symbol | string, got int",
		},
		{
			name: "rejects extra arguments",
			source: `
def run(s: string)
  s.is_type?(:int, :string)
end
`,
			warning: "call to is_type? has too many arguments",
		},
		{
			name: "rejects generic atom spellings",
			source: `
def run(s: string)
  s.is_type?("array<int>")
end
`,
			warning: "is_type? supports type atoms only",
		},
		{
			name: "rejects unknown atoms",
			source: `
def run(s: string)
  s.is_type?(:whatever)
end
`,
			warning: "unknown type atom \"whatever\" in is_type?",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckIsTypeNarrowing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "true branch keeps matching arms",
			source: `
def takes_string(value: string)
  value
end

def run(v: int | string)
  if v.is_type?(:int)
    takes_string(v)
  end
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "false branch drops matching arms",
			source: `
def takes_int(value: int)
  value
end

def run(v: int | string)
  unless v.is_type?(:int)
    takes_int(v)
  end
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "nullable atom keeps nil in the true branch",
			source: `
def takes_string(value: string)
  value
end

def run(v: int | nil)
  if v.is_type?('int?')
    takes_string(v)
  end
end
`,
			warning: "call to takes_string argument value expected string, got int | nil",
		},
		{
			name: "number arm may satisfy int",
			source: `
def takes_string(value: string)
  value
end

def run(v: number | string)
  if v.is_type?(:int)
    takes_string(v)
  end
end
`,
			warning: "call to takes_string argument value expected string, got number",
		},
		{
			name: "guard clause narrowing survives",
			source: `
def takes_int(value: int)
  value
end

def run(v: int | string)
  return 0 unless v.is_type?(:int)
  takes_int("x")
  v
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckIsTypeNarrowingStaysGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "unknown receiver stays unknown",
			source: `
def takes_string(value: string)
  value
end

def run(v)
  if v.is_type?(:int)
    takes_string(v)
  end
end
`,
		},
		{
			name: "impossible branch is pruned",
			source: `
def takes_int(value: int)
  value
end

def run(s: string)
  if s.is_type?(:int)
    takes_int("x")
  end
end
`,
		},
		{
			name: "class receiver override disables narrowing",
			source: `
class Wrapper
  def initialize()
  end

  def is_type?(atom)
    true
  end
end

def takes_string(value: string)
  value
end

def run(w: Wrapper | string)
  unless w.is_type?(:string)
    takes_string(w)
  end
end
`,
		},
		{
			name: "non literal atom disables narrowing",
			source: `
def takes_string(value: string)
  value
end

def run(v: int | string, atom: symbol)
  if v.is_type?(atom)
    takes_string(v)
  end
end
`,
		},
		{
			name: "named atoms stay gradual",
			source: `
class User
  def initialize()
  end
end

def takes_string(value: string)
  value
end

def run(v: int | string)
  if v.is_type?(:User)
    takes_string(v)
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
