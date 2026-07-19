package runtime

import (
	"fmt"
	"testing"
)

// Scalar member contracts resolve from inferred receiver facts, not only
// literal receivers, and universal predicates carry fixed boolean results
// when no receiver arm can override universal dispatch.

func TestCheckFixedScalarConversionInventory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		params     string
		expression string
		result     string
		mismatch   string
		autoInvoke bool
	}{
		{name: "nil to_s", expression: "nil.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "nil string", expression: "nil.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "bool to_s", expression: "true.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "bool string", expression: "true.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol id2name", expression: ":ok.id2name", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol to_s", expression: ":ok.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol string", expression: ":ok.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "symbol to_sym", expression: ":ok.to_sym", result: "symbol", mismatch: "int", autoInvoke: true},
		{name: "string to_i", params: "value: string", expression: "value.to_i", result: "int", mismatch: "string", autoInvoke: true},
		{name: "string to_f", params: "value: string", expression: "value.to_f", result: "float", mismatch: "string", autoInvoke: true},
		{name: "string to_s", params: "value: string", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "string string", params: "value: string", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "string to_sym", params: "value: string", expression: "value.to_sym", result: "symbol", mismatch: "int", autoInvoke: true},
		{name: "string intern", params: "value: string", expression: "value.intern", result: "symbol", mismatch: "int", autoInvoke: true},
		{name: "int to_i", params: "value: int", expression: "value.to_i", result: "int", mismatch: "string", autoInvoke: true},
		{name: "int to_f", params: "value: int", expression: "value.to_f", result: "float", mismatch: "string", autoInvoke: true},
		{name: "int to_s", params: "value: int", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "int string", params: "value: int", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "float to_i", params: "value: float", expression: "value.to_i", result: "int", mismatch: "string", autoInvoke: true},
		{name: "float to_f", params: "value: float", expression: "value.to_f", result: "float", mismatch: "string", autoInvoke: true},
		{name: "float to_s", params: "value: float", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "float string", params: "value: float", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "money to_s", params: "value: money", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "money string", params: "value: money", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "duration to_i", params: "value: duration", expression: "value.to_i", result: "int", mismatch: "string"},
		{name: "duration to_s", params: "value: duration", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "duration string", params: "value: duration", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "time to_i", params: "value: time", expression: "value.to_i", result: "int", mismatch: "string"},
		{name: "time tv_sec", params: "value: time", expression: "value.tv_sec", result: "int", mismatch: "string"},
		{name: "time to_f", params: "value: time", expression: "value.to_f", result: "float", mismatch: "string"},
		{name: "time to_r", params: "value: time", expression: "value.to_r", result: "float", mismatch: "string"},
		{name: "time to_s", params: "value: time", expression: "value.to_s", result: "string", mismatch: "int", autoInvoke: true},
		{name: "time string", params: "value: time", expression: "value.string", result: "string", mismatch: "int", autoInvoke: true},
		{name: "time to_a", params: "value: time", expression: "value.to_a", result: "array", mismatch: "string"},
		{name: "range to_a", params: "value: range", expression: "value.to_a", result: "array<int>", mismatch: "string", autoInvoke: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expressions := []string{tc.expression}
			if tc.autoInvoke {
				expressions = append(expressions, tc.expression+"()")
			}
			for _, expression := range expressions {
				matching := fmt.Sprintf(`
def accept(value: %s)
  value
end

def run(%s)
  accept(%s)
end
`, tc.result, tc.params, expression)
				requireNoCheckWarnings(t, compileScriptDefault(t, matching))

				contradiction := fmt.Sprintf(`
def reject(value: %s)
  value
end

def run(%s)
  reject(%s)
end
`, tc.mismatch, tc.params, expression)
				requireCheckWarningContains(
					t,
					compileScriptDefault(t, contradiction),
					fmt.Sprintf("call to reject argument value expected %s, got %s", tc.mismatch, tc.result),
				)
			}
		})
	}
}

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
			name: "literal hash predicate result",
			source: `
def takes_int(value: int)
  value
end

def run()
  takes_int(({}).nil?)
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "typed hash predicate result",
			source: `
def takes_int(value: int)
  value
end

def run(value: hash<string, string>)
  takes_int(value.nil?)
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "typed shape predicate result",
			source: `
def takes_int(value: int)
  value
end

def run(value: { name: string })
  takes_int(value.nil?())
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "typed hash pure predicate preserves receiver fact",
			source: `
def takes_int(value: int)
  value
end

def run(value: hash<string, string>)
  takes_int(value.frozen?)
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "typed shape pure predicate call preserves receiver fact",
			source: `
def takes_int(value: int)
  value
end

def run(value: { name: string })
  takes_int(value.respond_to?(:name))
end
`,
			warning: "call to takes_int argument value expected int, got bool",
		},
		{
			name: "mixed safe hash and scalar predicate result",
			source: `
def takes_int(value: int)
  value
end

def run(value: hash<string, string> | string)
  takes_int(value.nil?)
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
			name: "is_a rejects non-class argument",
			source: `
def run(s: string)
  s.is_a?(1)
end
`,
			warning: "call to is_a? expects a class argument, got int",
		},
		{
			name: "kind_of rejects non-class argument",
			source: `
def run(s: string)
  s.kind_of?("Foo")
end
`,
			warning: "call to kind_of? expects a class argument, got string",
		},
		{
			name: "instance_of rejects non-class union argument",
			source: `
def run(s: string, v: int | string)
  s.instance_of?(v)
end
`,
			warning: "call to instance_of? expects a class argument, got int | string",
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
			name: "conversion rejects forwarded block",
			source: `
def run(s: string, blk: function)
  s.to_i(&blk)
end
`,
			warning: "call to string.to_i does not accept a block",
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
			name: "safe navigation direct value result includes nil",
			source: `
def takes_string(value: string)
  value
end

def run(t: time?)
  takes_string(t&.to_i)
end
`,
			warning: "call to takes_string argument value expected string, got int?",
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
			name: "callable hash receiver keeps universal helpers unknown",
			source: `
def takes_string(value: string)
  value
end

def run(h: hash<string, function>)
  takes_string(h.nil?)
end
`,
		},
		{
			name: "callable shape receiver keeps universal helpers unknown",
			source: `
def takes_string(value: string)
  value
end

def run(value: { nil?: function })
  takes_string(value.nil?)
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
			name: "class predicate accepts class argument",
			source: `
class Marker
  def initialize()
  end
end

def run(s: string)
  s.is_a?(Marker)
end
`,
		},
		{
			name: "class predicate keeps unknown argument gradual",
			source: `
def run(s: string, k)
  s.instance_of?(k)
end
`,
		},
		{
			name: "possibly nil forwarded block stays gradual",
			source: `
def run(s: string, blk)
  s.to_i(&blk)
end
`,
		},
		{
			name: "mutating predicate argument poisons the receiver fact",
			source: `
def takes_int(value: int)
  value
end

def run()
  x = {name: "a"}
  y = x
  x.eql?(y.store(:name, 1))
  takes_int(x[:name])
end
`,
		},
		{
			name: "predicate call with impure argument poisons the receiver fact",
			source: `
def name_key()
  :name
end

def takes_int(value: int)
  value
end

def run()
  x = {name: "a"}
  x.respond_to?(name_key())
  takes_int(x[:name])
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
			name: "safe navigation direct value satisfies nullable boundary",
			source: `
def takes_int(value: int?)
  value
end

def run(t: time?)
  takes_int(t&.to_i)
end
`,
		},
		{
			name: "range conversion keeps ignored block unevaluated",
			source: `
def takes_array(value: array)
  value
end

def run(r: range)
  takes_array(r.to_a { missing_name })
end
`,
		},
		{
			name: "temporal equality keeps ignored block unevaluated",
			source: `
def run(d: duration, t: time)
  d.eql?(d) { missing_duration_block }
  t.eql?(t) { missing_time_block }
end
`,
		},
		{
			name: "mixed temporal equality keeps ignored block unevaluated",
			source: `
def run(value: duration | time, other: duration | time)
  value.eql?(other) { missing_union_block }
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
