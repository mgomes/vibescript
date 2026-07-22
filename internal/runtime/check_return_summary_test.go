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
			name: "nested exiting ensure replaces outer deferred returns",
			source: `
def forced()
  begin
    begin
      return 1
    ensure
      return "forced"
    end
  ensure
    cleanup = true
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
			name: "nested inferred exiting ensure replaces outer deferred returns",
			source: `
def forced()
  begin
    begin
      return 1
    ensure
      value = nil
      if value == nil
        return "forced"
      end
    end
  ensure
    cleanup = true
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
			name: "mutating argument keeps the resolved builtin result",
			source: `
def replacement(value)
  1
end

def mutate_serializer()
  JSON.stringify = replacement
  {}
end

def build()
  JSON.stringify(mutate_serializer())
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
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
			name: "benign bare method preserves cached summaries",
			source: `
class Observer
  def touch()
    1
  end
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
  Observer.new.touch
  takes_int(serialize())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "nominal receiver scan ignores same named mutators",
			source: `
def replacement(value)
  1
end

class Mutator
  def touch()
    JSON.stringify = replacement
  end
end

class Observer
  def touch()
    1
  end
end

def touch_through(receiver: Observer)
  receiver.touch()
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
  touch_through(Observer.new)
  takes_int(serialize())
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
			name: "supplied argument skips the default's namespace writes",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def flip(x = install_serializer())
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  flip(1)
  takes_int(serialize())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "assignment targets do not auto-invoke shadowed functions",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def shadow()
  install_serializer = 1
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(shadow())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "supplied call shape skips the default in the summary",
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
  takes_int(serialize(1))
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "definitely omitted parameter carries the default fact",
			source: `
def build_count(x = 1)
  x
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
			name: "compound assignment finals record the combined result",
			source: `
def build_count()
  x = 1
  x += 2
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
			name: "returned path namespace writes stay scoped to the try",
			source: `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run(flag)
  begin
    if flag
      JSON.stringify = replacement
      return
    end
  ensure
    flag = false
  end
  takes_int(JSON.stringify({}))
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "uninvoked lambda writes do not mark the namespace",
			source: `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run()
  handler = ->() { JSON.stringify = replacement }
  takes_int(JSON.stringify({}))
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "uninvoked lambda builtin writes do not mark the namespace",
			source: `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run()
  handler = lambda { JSON.stringify = replacement }
  takes_int(JSON.stringify({}))
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "inferred exiting ensure replaces deferred returns",
			source: `
def f()
  begin
    return 1
  ensure
    x = nil
    if x == nil
      return "s"
    end
  end
end

def takes_int(value: int)
  value
end

def run()
  takes_int(f())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "collapsed options hash supplies the parameter",
			source: `
def build(opts = 1)
  opts
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build(name: "x"))
end
`,
			warning: "call to takes_int argument value expected int, got hash",
		},
		{
			name: "collapsed options hash consumes later keyword names",
			source: `
def build(opts = nil, value = 1)
  value
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build(name: "x", value: 2))
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "collapsed options hash skips the default's namespace writes",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def flip(x = install_serializer())
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  flip(name: "y")
  takes_int(serialize())
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "parenless method options hash skips the positional default",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

class Mutator
  def mutate(a = install_serializer(), b = 0)
  end
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  Mutator.new.mutate b: 1
  takes_int(serialize())
end
`,
			warning: "call to takes_int argument value expected int, got string",
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
		{
			name: "supplied argument skips empty body return default",
			source: `
def nothing(value = [1].each { return 1 })
end

def takes_string(value: string)
  value
end

def run()
  takes_string(nothing(0))
end
`,
			warning: "call to takes_string argument value expected string, got nil",
		},
		{
			name: "supplied argument skips a yielding default",
			source: `
def replacement(value)
  1
end

def invoke(_ = yield)
end

def build()
  invoke(0) do
    JSON.stringify = replacement
    return 1
  end
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
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

func TestCheckFunctionReturnSummariesStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "empty body return default poisons nil summary",
			source: `
def build(value = [1].each { return 1 })
end

def takes_string(value: string)
  value
end

def run()
  takes_string(build())
end
`,
		},
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
			name: "unresolved first class calls poison later namespace facts",
			source: `
def replacement(value)
  1
end

def mutate(_value)
  JSON.stringify = replacement
end

def build()
  cb = mutate
  cb.call(0)
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
end
`,
		},
		{
			name: "unresolved direct calls poison later namespace facts",
			source: `
def replacement(value)
  1
end

def mutate(_value)
  JSON.stringify = replacement
end

def build()
  cb = mutate
  cb(0)
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
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
			name: "later calls see namespace mutation from an earlier argument",
			source: `
def replacement(value)
  1
end

def mutate_serializer()
  JSON.stringify = replacement
  {}
end

def build()
  JSON.stringify(mutate_serializer())
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
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
			name: "default hidden namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def flip(x = install_serializer())
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
  flip()
  takes_int(serialize())
end
`,
		},
		{
			name: "single branch namespace mutation survives the join",
			source: `
def replacement(value)
  1
end

def mutate_serializer()
  JSON.stringify = replacement
end

def serialize(flag)
  if flag
    mutate_serializer()
  end
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run(flag)
  takes_int(serialize(flag))
end
`,
		},
		{
			name: "block namespace mutation survives the block restore",
			source: `
def replacement(value)
  1
end

def build()
  [1].each { JSON.stringify = replacement }
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
end
`,
		},
		{
			name: "yielding default block namespace mutation survives the block restore",
			source: `
def replacement(value)
  1
end

def invoke(_ = yield)
end

def build()
  invoke() { JSON.stringify = replacement }
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
end
`,
		},
		{
			name: "splatted call keeps the default's namespace writes",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def flip(x = install_serializer())
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

def run(values)
  takes_string(serialize())
  flip(*values)
  takes_int(serialize())
end
`,
		},
		{
			name: "splatted call keeps the default fact unknown",
			source: `
def build_count(x = 1)
  x
end

def takes_string(value: string)
  value
end

def run(a)
  takes_string(build_count(*a))
end
`,
		},
		{
			name: "method callee namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

class Mutator
  def mutate()
    JSON.stringify = replacement
  end
end

def wrapper()
  Mutator.new.mutate()
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
			name: "bare method namespace mutation invalidates a cached summary",
			source: `
def replacement(value)
  1
end

class Mutator
  def mutate()
    JSON.stringify = replacement
  end
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
  Mutator.new.mutate
  takes_int(serialize())
end
`,
		},
		{
			name: "nominal receiver mutation propagates through a helper",
			source: `
def replacement(value)
  1
end

class Mutator
  def mutate()
    JSON.stringify = replacement
  end
end

def mutate_through(receiver: Mutator)
  receiver.mutate()
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
  mutate_through(Mutator.new)
  takes_int(serialize())
end
`,
		},
		{
			name: "parenthesized method keywords still run skipped positional defaults",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

class Mutator
  def mutate(a = install_serializer(), b = 0)
  end
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
  Mutator.new.mutate(b: 1)
  takes_int(serialize())
end
`,
		},
		{
			name: "logical assignment finals keep the existing arm",
			source: `
def build()
  x = 1
  x ||= "fallback"
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build())
end
`,
		},
		{
			name: "fallthrough namespace writes persist past the try",
			source: `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run(flag)
  begin
    if flag
      JSON.stringify = replacement
    end
  ensure
    flag = false
  end
  takes_int(JSON.stringify({}))
end
`,
		},
		{
			name: "ensure namespace writes persist past the try",
			source: `
def replacement(value)
  1
end

def takes_int(value: int)
  value
end

def run(flag)
  begin
    if flag
      JSON.stringify = replacement
      return
    end
  ensure
    JSON.stringify = replacement
  end
  takes_int(JSON.stringify({}))
end
`,
		},
		{
			name: "escaping lambda argument writes stay possible",
			source: `
def replacement(value)
  1
end

def consume(cb)
end

def takes_int(value: int)
  value
end

def run()
  consume(->() { JSON.stringify = replacement })
  takes_int(JSON.stringify({}))
end
`,
		},
		{
			name: "escaping lambda builtin argument writes stay possible",
			source: `
def replacement(value)
  1
end

def consume(cb)
end

def takes_int(value: int)
  value
end

def run()
  consume(lambda { JSON.stringify = replacement })
  takes_int(JSON.stringify({}))
end
`,
		},
		{
			name: "collapsed options hash does not carry the default fact",
			source: `
def build(opts = 1)
  opts
end

def takes_hash(value: hash)
  value
end

def run()
  takes_hash(build(name: "x"))
end
`,
		},
		{
			name: "uncollapsed keywords still supply later parameters",
			source: `
def build(opts = nil, value = "fallback")
  value
end

def takes_int(value: int)
  value
end

def run()
  takes_int(build(opts: 0, value: 2))
end
`,
		},
		{
			name: "collapsed options hash runs later default effects",
			source: `
def replacement(value)
  1
end

def install_serializer()
  JSON.stringify = replacement
end

def flip(opts = nil, value = install_serializer())
end

def serialize()
  JSON.stringify({})
end

def takes_int(value: int)
  value
end

def run()
  flip(name: "x", value: 2)
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
			name: "yielding default block returns poison the summary",
			source: `
def invoke(_ = yield)
end

def build()
  invoke() { return "s" }
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

func TestCheckFunctionReturnSummariesDoNotEnqueueUnreachableCalls(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def helper() -> int
  "bad"
end

def wrapper()
  helper()
end

def takes_bool(value: bool)
  value
end

def run()
  takes_bool(true || wrapper())
end
`)

	if warnings := script.CheckWarningsForFunction("run"); len(warnings) != 0 {
		t.Errorf("CheckWarningsForFunction(%q) = %#v, want none for short-circuited wrapper", "run", warnings)
	}
}

func TestCheckFunctionReturnSummariesDoNotCapturePreRequireCallState(t *testing.T) {
	t.Parallel()

	script := compileScriptWithEngine(t, moduleTestEngine(t), `
def helper() -> Status
  :draft
end

def wrapper()
  helper()
end

def takes_bool(value: bool)
  value
end

def run()
  takes_bool(true || wrapper())
  require("enum_status")
  helper()
end
`)

	if warnings := script.CheckWarningsForFunction("run"); len(warnings) != 0 {
		t.Errorf("CheckWarningsForFunction(%q) = %#v, want none after require", "run", warnings)
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

// TestCheckFunctionReturnSummariesCollectDefaultRequires pins that a require
// inside a parameter default binds its exports for the body walk, so the
// summary sees the module function the body calls.
func TestCheckFunctionReturnSummariesCollectDefaultRequires(t *testing.T) {
	t.Parallel()

	root := tempModuleTree(t, moduleFile{path: "counts.vibe", content: `
export def build_count() -> int
  42
end
`})
	engine := mustNewEngineWithModuleRoot(t, root)
	script, err := engine.CompileSnippet(`
def wrapper(_ = require("counts"))
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
