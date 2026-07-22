package runtime

import (
	"strings"
	"testing"
)

func TestCheckMethodReturnSummaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "instance method",
			source: `
class Counter
  def value()
    42
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Counter.new.value())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "class method",
			source: `
class Counter
  def self.value()
    42
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Counter.value())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "annotated receiver",
			source: `
class Counter
  def value()
    42
  end
end

def takes_string(value: string)
  value
end

def run(counter: Counter)
  takes_string(counter.value())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "branch results join into a union",
			source: `
class Picker
  def value(flag)
    if flag
      1
    else
      "x"
    end
  end
end

def takes_hash(value: hash)
  value
end

def run(flag)
  takes_hash(Picker.new.value(flag))
end
`,
			warning: "call to takes_hash argument value expected hash, got int | string",
		},
		{
			name: "missing else adds nil fallthrough",
			source: `
class Picker
  def self.value(flag)
    if flag
      1
    end
  end
end

def takes_string(value: string)
  value
end

def run(flag)
  takes_string(Picker.value(flag))
end
`,
			warning: "call to takes_string argument value expected string, got int | nil",
		},
		{
			name: "method summaries chain through nominal results",
			source: `
class Product
  def count()
    42
  end
end

class Factory
  def self.build()
    Product.new
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Factory.build().count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "bare instance method auto call",
			source: `
class Counter
  def value()
    42
  end
end

def takes_string(value: string)
  value
end

def run()
  value = Counter.new.value
  takes_string(value)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "bare class method auto call",
			source: `
class Counter
  def self.value()
    42
  end
end

def takes_string(value: string)
  value
end

def run()
  value = Counter.value
  takes_string(value)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "safe navigation adds nil",
			source: `
class Counter
  def value()
    42
  end
end

def takes_string(value: string)
  value
end

def run(counter: Counter?)
  takes_string(counter&.value())
end
`,
			warning: "call to takes_string argument value expected string, got int?",
		},
		{
			name: "parenthesized method keywords do not collapse into options hash",
			source: `
class Picker
  def value(options = 1, flag = false)
    options
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Picker.new.value(flag: true))
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

func TestCheckMethodReturnSummariesPreserveMethodKeywordBinding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "instance method",
			source: `
class Picker
  def value(options = 1, flag = false)
    takes_hash(options)
  end
end

def takes_hash(value: hash)
  value
end

def takes_int(value: int)
  value
end

def run()
  Picker.new.value(flag: true)
  takes_int("unreachable")
end
`,
		},
		{
			name: "class method",
			source: `
class Picker
  def self.value(options = 1, flag = false)
    takes_hash(options)
  end
end

def takes_hash(value: hash)
  value
end

def takes_int(value: int)
  value
end

def run()
  Picker.value(flag: true)
  takes_int("unreachable")
end
`,
		},
		{
			name: "exact dynamic instance method",
			source: `
class FirstPicker
  def value(options = 1, flag = false)
    takes_hash(options)
  end
end

class SecondPicker
  def value(options = 1, flag = false)
    takes_hash(options)
  end
end

def takes_hash(value: hash)
  value
end

def takes_int(value: int)
  value
end

def run(flag: bool)
  picker = flag ? FirstPicker.new : SecondPicker.new
  picker.value(flag: true)
  takes_int("unreachable")
end
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			warnings := strings.Join(checkWarningMessages(compileScriptDefault(t, tc.source).CheckWarnings()), "\n")
			if strings.Contains(warnings, "call to takes_int") {
				t.Fatalf("CheckWarnings() = %q, unreachable call was checked", warnings)
			}
		})
	}
}

func TestCheckMethodReturnSummariesStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "dynamic receiver",
			source: `
def takes_string(value: string)
  value
end

def run(receiver)
  takes_string(receiver.value())
end
`,
		},
		{
			name: "shadowed class receiver",
			source: `
class Counter
  def self.value()
    42
  end
end

def takes_string(value: string)
  value
end

def run(Counter)
  takes_string(Counter.value())
end
`,
		},
		{
			name: "recursive instance method",
			source: `
class Counter
  def value(n)
    if n > 0
      Counter.new.value(n - 1)
    else
      0
    end
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Counter.new.value(3))
end
`,
		},
		{
			name: "mutually recursive class methods",
			source: `
class Counter
  def self.up(n)
    if n > 0
      Counter.down(n - 1)
    else
      0
    end
  end

  def self.down(n)
    Counter.up(n)
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Counter.up(3))
end
`,
		},
		{
			name: "unknown method branch poisons known arms",
			source: `
class Picker
  def value(flag, receiver)
    if flag
      1
    else
      receiver.value()
    end
  end
end

def takes_string(value: string)
  value
end

def run(flag, receiver)
  takes_string(Picker.new.value(flag, receiver))
end
`,
		},
		{
			name: "instance method overriding universal dispatch",
			source: `
class Counter
  def nil?()
    1
  end
end

def takes_string(value: string)
  value
end

def run(counter: Counter)
  takes_string(counter.nil?())
end
`,
		},
		{
			name: "class method overriding universal dispatch",
			source: `
class Counter
  def self.nil?()
    1
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Counter.nil?())
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

func TestCheckMethodReturnAnnotationsStayAuthoritative(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Counter
  def value() -> int | string
    42
  end
end

def takes_bool(value: bool)
  value
end

def run()
  takes_bool(Counter.new.value())
end
`)

	requireCheckWarningContains(t, script, "call to takes_bool argument value expected bool, got int | string")
}

func TestCheckMethodReturnSummariesRespectHostClassOverrides(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
class Counter
  def self.value()
    42
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(Counter.value())
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")

	hostCounter := NewObject(map[string]Value{
		"value": NewBuiltin("Counter.value", func(_ *Execution, _ Value, _ []Value, _ map[string]Value, _ Value) (Value, error) {
			return NewString("host"), nil
		}),
	})
	requireNoCheckWarningsWithOptions(t, script, CallOptions{
		Globals: map[string]Value{"Counter": hostCounter},
	})
}
