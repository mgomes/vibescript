package runtime

import "testing"

func TestCheckFunctionReturnSummaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "implicit int result contradicts string boundary",
			source: `
def build_count()
  41 + 1
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "bare parenless call carries the summary",
			source: `
def build_count()
  42
end

def takes_string(value: string)
  value
end

def run()
  count = build_count
  takes_string(count)
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "local binding wins over same named function summary",
			source: `
def build_count()
  42
end

def shadowed_count()
  build_count = "local"
  build_count
end

def takes_int(value: int)
  value
end

def run()
  takes_int(shadowed_count())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "parameterized bare function stays a function value",
			source: `
def transform(value)
  42
end

def takes_int(value: int)
  value
end

def run()
  takes_int(transform)
end
`,
			warning: "call to takes_int argument value expected int, got function",
		},
		{
			name: "returned parameterized function stays a function value",
			source: `
def transform(value)
  42
end

def expose_transform()
  transform
end

def takes_int(value: int)
  value
end

def run()
  takes_int(expose_transform())
end
`,
			warning: "call to takes_int argument value expected int, got function",
		},
		{
			name: "call member invocation carries the summary",
			source: `
def transform(value)
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(transform.call(1))
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "explicit returns summarize",
			source: `
def pick(flag)
  if flag
    return 1
  end
  return 2
end

def takes_string(value: string)
  value
end

def run(flag)
  takes_string(pick(flag))
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "branch results join into a union",
			source: `
def pick(flag)
  if flag
    1
  else
    "x"
  end
end

def takes_hash(value: hash)
  value
end

def run(flag)
  takes_hash(pick(flag))
end
`,
			warning: "call to takes_hash argument value expected hash, got int | string",
		},
		{
			name: "missing else adds the nil fallthrough arm",
			source: `
def maybe(flag)
  if flag
    1
  end
end

def takes_string(value: string)
  value
end

def run(flag)
  takes_string(maybe(flag))
end
`,
			warning: "call to takes_string argument value expected string, got int | nil",
		},
		{
			name: "guard clause return joins the final expression",
			source: `
def pick(flag)
  return "s" unless flag
  1
end

def takes_hash(value: hash)
  value
end

def run(flag)
  takes_hash(pick(flag))
end
`,
			warning: "call to takes_hash argument value expected hash, got string | int",
		},
		{
			name: "summaries chain through callees",
			source: `
def build_count()
  42
end

def outer()
  build_count()
end

def takes_string(value: string)
  value
end

def run()
  takes_string(outer())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "summary dependency order follows calls not names",
			source: `
def a_outer()
  z_build_count()
end

def z_build_count()
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(a_outer())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "always exiting ensure replaces earlier returns",
			source: `
def forced()
  begin
    return 1
  ensure
    return "forced"
  end
end

def takes_int(value: int)
  value
end

def run()
  takes_int(forced())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "dead loop returns do not contaminate the summary",
			source: `
def build_count()
  while false
    return "unreachable"
  end
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "lambda returns do not contaminate the summary",
			source: `
def build_count()
  ->() { return "lambda" }
  lambda { return "lambda helper" }
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "invoked lambda returns stay local",
			source: `
def build_count()
  helper = ->() { return "lambda" }
  helper.call
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "value blocks without returns keep the summary",
			source: `
def build_count()
  [1, 2].each { |n| n }
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "rescue arms join the summary",
			source: `
def guarded()
  begin
    1
  rescue
    "fallback"
  end
end

def takes_hash(value: hash)
  value
end

def run()
  takes_hash(guarded())
end
`,
			warning: "call to takes_hash argument value expected hash, got int | string",
		},
		{
			name: "empty rescue arms contribute no summary arm",
			source: `
def guarded()
  begin
    "s"
  rescue TypeError
  rescue
    1
  end
end

def takes_hash(value: hash)
  value
end

def run()
  takes_hash(guarded())
end
`,
			warning: "call to takes_hash argument value expected hash, got string | int",
		},
		{
			name: "empty rescue clause keeps the body summary",
			source: `
def guarded()
  begin
    1
  rescue TypeError
  rescue
    2
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(guarded())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "call member invocation carries an empty body nil summary",
			source: `
def empty(value)
end

def takes_string(value: string)
  value
end

def run()
  takes_string(empty.call(1))
end
`,
			warning: "call to takes_string argument value expected string, got nil",
		},
		{
			name: "self mutating callee keeps this call's pre-mutation result",
			source: `
def replacement(value)
  1
end

def fn()
  saved = JSON.stringify({})
  JSON.stringify = replacement
  saved
end

def takes_int(value: int)
  value
end

def run()
  takes_int(fn())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "self mutating bare auto-invoke keeps the pre-mutation result",
			source: `
def replacement(value)
  1
end

def fn()
  saved = JSON.stringify({})
  JSON.stringify = replacement
  saved
end

def takes_int(value: int)
  value
end

def run()
  result = fn
  takes_int(result)
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "benign parameter defaults keep the summary",
			source: `
def build_count(n = 2)
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "empty body summarizes as nil",
			source: `
def nothing()
end

def takes_string(value: string)
  value
end

def run()
  takes_string(nothing())
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

func TestCheckFunctionReturnSummariesStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "recursive functions stay unknown",
			source: `
def countdown(n)
  if n > 0
    countdown(n - 1)
  else
    0
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(countdown(3))
end
`,
		},
		{
			name: "mutually recursive functions stay unknown",
			source: `
def ping(n)
  if n > 0
    pong(n - 1)
  else
    0
  end
end

def pong(n)
  ping(n)
end

def takes_string(value: string)
  value
end

def run()
  takes_string(ping(3))
end
`,
		},
		{
			name: "unknown result paths stay unknown",
			source: `
def dyn(v)
  v.transform
end

def takes_string(value: string)
  value
end

def run(v)
  takes_string(dyn(v))
end
`,
		},
		{
			name: "one unknown branch poisons known return arms",
			source: `
def maybe_dynamic(flag, value)
  if flag
    1
  else
    value.transform
  end
end

def takes_string(value: string)
  value
end

def run(flag, value)
  takes_string(maybe_dynamic(flag, value))
end
`,
		},
		{
			name: "summaries are not reused after namespace mutation",
			source: `
def replacement(value)
  1
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  JSON.stringify = replacement
  takes_int(serialize())
end
`,
		},
		{
			name: "ensure mutation invalidates a returned container fact",
			source: `
def build_user()
  user = { name: "Ada" }
  begin
    return user
  ensure
    user["name"] = 42
  end
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build_user()["name"])
end
`,
		},
		{
			name: "block hidden namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

def mutate_serializer()
  [1].each { JSON.stringify = replacement }
end

def serialize()
  JSON.stringify({})
end

def takes_string(value: string)
  value
end

def takes_int(value: int)
  value
end

def run()
  takes_string(serialize())
  mutate_serializer()
  takes_int(serialize())
end
`,
		},
		{
			name: "namespace mutating default poisons the body summary",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def serialize(_ = install_serializer())
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(serialize())
end
`,
		},
		{
			name: "self mutating callee stays unknown after its first call",
			source: `
def replacement(value)
  1
end

def fn()
  saved = JSON.stringify({})
  JSON.stringify = replacement
  saved
end

def takes_string(value: string)
  value
end

def run()
  fn()
  takes_string(fn())
end
`,
		},
		{
			name: "transitive callee namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

def mutate_serializer()
  JSON.stringify = replacement
end

def wrapper()
  mutate_serializer()
end

def serialize()
  JSON.stringify({})
end

def takes_string(value: string)
  value
end

def takes_int(value: int)
  value
end

def run()
  takes_string(serialize())
  wrapper()
  takes_int(serialize())
end
`,
		},
		{
			name: "cyclic helper namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

def ping(flag)
  if flag
    pong(false)
  end
end

def pong(flag)
  JSON.stringify = replacement
  if flag
    ping(false)
  end
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_string(serialize())
  ping(true)
  takes_int(serialize())
end

def takes_string(value: string)
  value
end
`,
		},
		{
			name: "callee namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

def mutate_serializer()
  JSON.stringify = replacement
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  serialize()
  mutate_serializer()
  takes_int(serialize())
end
`,
		},
		{
			name: "block returns poison the summary",
			source: `
def find_marker()
  [1].each { return "s" }
  0
end

def takes_int(value: int)
  value
end

def takes_string(value: string)
  value
end

def run()
  takes_int(find_marker())
  takes_string(find_marker())
end
`,
		},
		{
			name: "nested block returns poison the summary",
			source: `
def scan()
  [[1]].each do |row|
    row.each { return "s" }
  end
  0
end

def takes_int(value: int)
  value
end

def takes_string(value: string)
  value
end

def run()
  takes_int(scan())
  takes_string(scan())
end
`,
		},
		{
			name: "invoked proc returns poison the summary",
			source: `
def build()
  handler = proc { return "s" }
  handler.call
  0
end

def takes_int(value: int)
  value
end

def takes_string(value: string)
  value
end

def run()
  takes_int(build())
  takes_string(build())
end
`,
		},
		{
			name: "discarded proc returns poison the summary",
			source: `
def build_count()
  proc { return "s" }
  42
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build_count())
end
`,
		},
		{
			name: "loop finals stay unknown",
			source: `
def spin()
  while false
    1
  end
end

def takes_string(value: string)
  value
end

def run()
  takes_string(spin())
end
`,
		},
		{
			name: "raise-only bodies stay unknown",
			source: `
def boom()
  raise "nope"
end

def takes_string(value: string)
  value
end

def run()
  takes_string(boom())
end
`,
		},
		{
			name: "nullable summary overlaps its boundary",
			source: `
def maybe(flag)
  if flag
    "name"
  end
end

def takes_string(value: string)
  value
end

def run(flag)
  takes_string(maybe(flag))
end
`,
		},
		{
			name: "explicit annotations stay authoritative",
			source: `
def build() -> int
  1
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
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

func TestCheckFunctionReturnSummariesUseEntrypointImports(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "counts.vibe", content: `
export def build_count() -> int
  42
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script, err := engine.CompileSnippet(`
require("counts")

def wrapper()
  build_count()
end

def takes_string(value: string)
  value
end

takes_string(wrapper())
`, "run")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")
}

// TestCheckFunctionReturnSummariesSkipForeignFunctions pins the issue scope:
// required-module functions keep unknown results even when their bodies are
// summarizable.
func TestCheckFunctionReturnSummariesSkipForeignFunctions(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "counts.vibe", content: `
export def build_count()
  42
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, `
def takes_string(value: string)
  value
end

def run()
  require("counts")
  takes_string(build_count())
end
`)
	requireNoCheckWarnings(t, script)
}
