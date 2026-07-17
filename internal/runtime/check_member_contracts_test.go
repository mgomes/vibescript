package runtime

import "testing"

// Scalar member contracts resolve from inferred receiver facts, not only
// literal receivers, and universal predicates carry fixed boolean results
// when no receiver arm can override universal dispatch.

func TestCheckScalarMemberContractContradictions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "literal receiver conversion result",
			source: `
def takes_string(value: string)
  value
end

def run()
  takes_string("42".to_i)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "fact receiver conversion result",
			source: `
def takes_string(value: string)
  value
end

def run(s: string)
  takes_string(s.to_i)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "bare auto-invoked conversion flows through a local",
			source: `
def takes_string(value: string)
  value
end

def run(s: string)
  count = s.to_i
  takes_string(count)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "int receiver to_s",
			source: `
def takes_int(value: int)
  value
end

def run(n: int)
  takes_int(n.to_s)
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "float receiver to_i",
			source: `
def takes_string(value: string)
  value
end

def run(f: float)
  takes_string(f.to_i)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "string receiver to_sym",
			source: `
def takes_int(value: int)
  value
end

def run(s: string)
  takes_int(s.to_sym)
end
`,
			warning: "call to takes_int argument value expected int, got symbol",
		},
		{
			name: "money receiver to_s",
			source: `
def takes_int(value: int)
  value
end

def run(m: money)
  takes_int(m.to_s)
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "conversion result from assignment fact",
			source: `
def takes_string(value: string)
  value
end

def run()
  count = 41
  takes_string(count.to_i)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "nil predicate result",
			source: `
def takes_int(value: int)
  value
end

def run(n: int)
  takes_int(n.nil?)
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "nil predicate on nullable receiver",
			source: `
def takes_int(value: int)
  value
end

def run(x: string?)
  takes_int(x.frozen?)
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "respond_to result",
			source: `
def takes_int(value: int)
  value
end

def run(s: string)
  takes_int(s.respond_to?(:to_i))
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "respond_to rejects non-name argument",
			source: `
def run(s: string)
  s.respond_to?(1)
end
`,
			warning: "call to respond_to? argument 1 expected symbol | string, got int",
		},
		{
			name: "respond_to rejects non-boolean visibility flag",
			source: `
def run(s: string)
  s.respond_to?(:to_i, 1)
end
`,
			warning: "call to respond_to? argument 2 expected bool, got int",
		},
		{
			name: "conversion rejects arguments via fact receiver",
			source: `
def run(s: string)
  s.to_i(10)
end
`,
			warning: "call to string.to_i has too many arguments",
		},
		{
			name: "slice shape applies to fact receiver",
			source: `
def run(s: string)
  s.slice()
end
`,
			warning: "call to string.slice has too few arguments",
		},
		{
			name: "safe navigation result includes nil",
			source: `
def takes_int(value: int)
  value
end

def run(s: string?)
  takes_int(s&.to_s)
end
`,
			warning: "call to takes_int argument value expected int, got string?",
		},
		{
			name: "nil-only safe navigation call result",
			source: `
def takes_string(value: string)
  value
end

def run()
  receiver = nil
  takes_string(receiver&.to_s())
end
`,
			warning: "call to takes_string argument value expected string, got nil",
		},
		{
			name: "nil-only safe navigation bare result",
			source: `
def takes_string(value: string)
  value
end

def run()
  receiver = nil
  takes_string(receiver&.to_s)
end
`,
			warning: "call to takes_string argument value expected string, got nil",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCheckScalarMemberContractsStayGradual(t *testing.T) {
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
  takes_string(v.to_i)
end
`,
		},
		{
			name: "class receiver keeps user conversion unknown",
			source: `
class Wrapper
  def initialize()
  end

  def to_i
    "not an int"
  end
end

def takes_string(value: string)
  value
end

def run(w: Wrapper)
  takes_string(w.to_i)
end
`,
		},
		{
			name: "class receiver keeps overridden predicate unknown",
			source: `
class Wrapper
  def initialize()
  end

  def nil?
    1
  end
end

def takes_string(value: string)
  value
end

def run(w: Wrapper)
  takes_string(w.nil?)
end
`,
		},
		{
			name: "hash receiver keeps universal helpers unknown",
			source: `
def takes_string(value: string)
  value
end

def run(h: hash)
  takes_string(h.nil?)
end
`,
		},
		{
			name: "bare equality predicates remain bound callables",
			source: `
def run(value: string)
  eql = value.eql?
  equal = value.equal?
  eql.call(value)
  equal.call(value)
end
`,
		},
		{
			name: "mixed receiver kinds stay unknown",
			source: `
def takes_string(value: string)
  value
end

def run(v: int | string)
  takes_string(v.to_i)
end
`,
		},
		{
			name: "nullable receiver keeps conversions unknown",
			source: `
def takes_string(value: string)
  value
end

def run(s: string?)
  takes_string(s.to_i)
end
`,
		},
		{
			name: "matching conversion result stays silent",
			source: `
def takes_int(value: int)
  value
end

def run(s: string)
  takes_int(s.to_i)
end
`,
		},
		{
			name: "safe navigation result satisfies nullable boundary",
			source: `
def takes_string(value: string?)
  value
end

def run(s: string?)
  takes_string(s&.to_s)
end
`,
		},
		{
			name: "predicate result satisfies bool boundary",
			source: `
def takes_bool(value: bool)
  value
end

def run(n: int)
  takes_bool(n.nil?)
  takes_bool(n.eql?(1))
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
