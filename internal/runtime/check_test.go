package runtime

import (
	"strings"
	"testing"
)

func TestCheckWarningsValidateTypeAnnotations(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def accept(v: int | Missing) -> int | Missing
  v
end
`)

	requireCheckWarningContains(t, script, "unknown type Missing")
}

func TestCheckWarningsValidateTypedDefaultsAndReturns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "typed default parameter",
			source: `def run(v: int = "bad")
  v
end`,
			want: "default value for v expected int, got string",
		},
		{
			name: "explicit return value",
			source: `def run() -> int
  return "bad"
end`,
			want: "return value expected int, got string",
		},
		{
			name: "implicit literal return value",
			source: `def run() -> int
  "bad"
end`,
			want: "return value expected int, got string",
		},
		{
			name: "empty typed function body",
			source: `def run() -> int
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "if statement without else",
			source: `def run(flag) -> int
  if flag
    1
  end
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "typed method return",
			source: `class Box
  def take() -> int
    "bad"
  end
end`,
			want: "return value expected int, got string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

func TestCheckWarningsValidateStaticCallContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "function argument type",
			source: `def accept(v: int)
  v
end

def run()
  accept("bad")
end`,
			want: "call to accept argument v expected int, got string",
		},
		{
			name: "function arity",
			source: `def one(v)
  v
end

def run()
  one(1, 2)
end`,
			want: "call to one has unexpected positional arguments",
		},
		{
			name: "typed rest argument",
			source: `def collect(*items: array<int>)
  items
end

def run()
  collect(1, "bad")
end`,
			want: "call to collect argument items expected array<int>, got array<int | string>",
		},
		{
			name: "method argument type",
			source: `class Box
  def take(v: int)
    v
  end
end

def run()
  Box.new.take("bad")
end`,
			want: "call to Box#take argument v expected int, got string",
		},
		{
			name: "method arity",
			source: `class Box
  def take(v)
    v
  end
end

def run()
  Box.new.take(1, 2)
end`,
			want: "call to Box#take has unexpected positional arguments",
		},
		{
			name: "constructor arity",
			source: `class Box
  def initialize(v)
  end
end

def run()
  Box.new()
end`,
			want: "call to Box.new is missing argument v",
		},
		{
			name: "auto-invoked constructor arity",
			source: `class Box
  def initialize(v)
  end
end

def run()
  Box.new
end`,
			want: "call to Box.new is missing argument v",
		},
		{
			name: "auto-invoked method arity",
			source: `class Box
  def take(v)
    v
  end
end

def run()
  Box.new.take
end`,
			want: "call to Box#take is missing argument v",
		},
		{
			name: "builtin arity",
			source: `def run()
  JSON.parse("{}", "extra")
end`,
			want: "call to JSON.parse has too many arguments",
		},
		{
			name: "regex replace block",
			source: `def run()
  Regex.replace("a", "a", "b") do
    "x"
  end
end`,
			want: "call to Regex.replace does not accept a block",
		},
		{
			name: "regex replace all block",
			source: `def run()
  Regex.replace_all("a", "a", "b") do
    "x"
  end
end`,
			want: "call to Regex.replace_all does not accept a block",
		},
		{
			name: "array builtin arity",
			source: `def run()
  [1, 2].fetch()
end`,
			want: "call to array.fetch has too few arguments",
		},
		{
			name: "empty if consequent",
			source: `def run(flag) -> int
  if flag
  else
    1
  end
end`,
			want: "typed return int can implicitly return nil",
		},
		{
			name: "try rescue return value",
			source: `def run() -> int
  begin
    1
  rescue RuntimeError
    "bad"
  end
end`,
			want: "return value expected int, got string",
		},
		{
			name: "break value",
			source: `def run()
  while true
    break JSON.parse()
  end
end`,
			want: "call to JSON.parse has too few arguments",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireCheckWarningContains(t, script, tc.want)
		})
	}
}

func TestCheckWarningsHonorRuntimeContractSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "rescue types are error classes",
			source: `def run()
  begin
    1 / 0
  rescue RuntimeError
    nil
  end
end`,
		},
		{
			name: "unreachable tail after explicit return",
			source: `def run() -> int
  return 1
  nil
end`,
		},
		{
			name: "unreachable tail after raise",
			source: `def run() -> int
  raise "boom"
  nil
end`,
		},
		{
			name: "begin body return skips unreachable else",
			source: `def run() -> int
  begin
    return 1
  rescue RuntimeError
    2
  else
    "bad"
  end
end`,
		},
		{
			name: "begin body conditional return skips unreachable else",
			source: `def run(flag) -> int
  begin
    if flag
      return 1
    else
      return 2
    end
  rescue RuntimeError
    3
  else
    "bad"
  end
end`,
		},
		{
			name: "parenless options hash",
			source: `def configure(opts: { retries: int })
  opts[:retries]
end

def run()
  configure retries: 3
end`,
		},
		{
			name: "parenthesized function options hash",
			source: `def configure(opts: { retries: int })
  opts[:retries]
end

def run()
  configure(retries: 3)
end`,
		},
		{
			name: "enum return symbol coercion",
			source: `enum Status
  Draft
end

def run() -> Status
  :draft
end`,
		},
		{
			name: "enum argument symbol coercion",
			source: `enum Status
  Draft
end

def identity(status: Status) -> Status
  status
end

def run()
  identity(:draft)
end`,
		},
		{
			name: "local function binding shadows top-level function",
			source: `def target(a)
  a
end

def optional(a = 1)
  a
end

def run()
  target = optional
  target()
end`,
		},
		{
			name: "local receiver shadows builtin namespace",
			source: `def run(JSON)
  JSON.parse()
end`,
		},
		{
			name: "shadowed class receiver skips chained constructor check",
			source: `class Box
  def take()
    1
  end
end

def run(Box)
  Box.new.take(1, 2, 3)
end`,
		},
		{
			name: "explicit member call with arguments is not auto-checked as empty",
			source: `class Box
  def take(v)
    v
  end
end

def run()
  Box.new.take(1)
end`,
		},
		{
			name: "bare non-auto namespace member stays callable value",
			source: `def run()
  JSON.parse
end`,
		},
		{
			name: "reassigned builtin namespace member skips builtin contract",
			source: `def parse(raw, extra)
  raw
end

def run()
  JSON.parse = parse
  JSON.parse("{}", "extra")
end`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tc.source)
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestRuntimeResolvesAllUnionMembersBeforeMatching(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def accept(v: int | Missing) -> int | Missing
  v
end

def run()
  accept(1)
end
`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unknown type Missing")
}

func requireNoCheckWarnings(t *testing.T, script *Script) {
	t.Helper()

	if warnings := script.CheckWarnings(); len(warnings) > 0 {
		t.Fatalf("CheckWarnings() = %#v, want none", warnings)
	}
}

func requireCheckWarningContains(t *testing.T, script *Script, want string) {
	t.Helper()

	warnings := script.CheckWarnings()
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	got := strings.Join(messages, "\n")
	if !strings.Contains(got, want) {
		t.Fatalf("CheckWarnings() = %q, want substring %q", got, want)
	}
}
