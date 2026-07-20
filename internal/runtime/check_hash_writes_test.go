package runtime

import "testing"

// Entry writes to a local whose fact is a declared hash<K, V> check the key
// against K and the value against V; field writes to a declared shape fact
// check the field's declared type and exactness. Witnessed literal and
// JSON.parse_as shapes carry their store's key representation and refine in
// place instead of warning: they are evidence, not contracts.

func TestCheckHashWriteContradictions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "key write to typed hash",
			source: `
def f(h: hash<string, int>)
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "value write to typed hash",
			source: `
def f(h: hash<string, int>)
  h["a"] = "bad"
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "value write against a union value bound",
			source: `
def f(h: hash<string, int | nil>)
  h["a"] = "bad"
end
`,
			warning: "write to h expected value int | nil, got string",
		},
		{
			name: "store key",
			source: `
def f(h: hash<string, int>)
  h.store(:sym, 1)
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "store value",
			source: `
def f(h: hash<string, int>)
  h.store("a", "bad")
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "merge with a field contradicting the value bound",
			source: `
def f(h: hash<symbol, int>)
  h.merge!({ a: "bad" })
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "merge with a contradicting key representation",
			source: `
def f(h: hash<symbol, int>)
  h.merge!({ "a": 1 })
end
`,
			warning: "write to h expected key symbol, got string",
		},
		{
			name: "merge source hash is not retained by the receiver",
			source: `
def f(h: hash<string, int>, other: hash<string, int>)
  h.merge!(other)
  other["b"] = "bad"
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "conflict block cannot hide an impossible key",
			source: `
def f(h: hash<string, int>)
  h.merge!({ a: 1 }) do |key, old, new|
    old
  end
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "conflict block cannot hide a value behind an impossible key",
			source: `
def f(h: hash<symbol, int>)
  h.merge!({ "a": true }) do |key, old, new|
    old
  end
end
`,
			warning: "write to h expected value int, got bool",
		},
		{
			name: "mixed-key local merge argument contradicts a single-representation bound",
			source: `
def f(h: hash<string, int>)
  bad = { a: 1, "b": 2 }
  h.merge!(bad)
end
`,
			warning: "write to h expected hash<string, int>, got",
		},
		{
			name: "mixed-key local contradicts a union bound excluding symbols",
			source: `
def f(h: hash<string | int, int>)
  bad = { a: 1, "b": 2 }
  h.merge!(bad)
end
`,
			warning: "write to h expected hash<string | int, int>, got",
		},
		{
			name: "compatible mixed-key local merge preserves the fact",
			source: `
def f(h: hash<string | symbol, int>)
  bad = { a: 1, "b": 2 }
  h.merge!(bad)
  h[true] = 1
end
`,
			warning: "write to h expected key string | symbol, got bool",
		},
		{
			name: "mixed-key local contradicts a symbol-keyed boundary",
			source: `
def g(h: hash<symbol, int>)
  h
end

def f
  bad = { a: 1, "b": 2 }
  g(bad)
end
`,
			warning: "call to g argument h expected hash<symbol, int>, got",
		},
		{
			name: "mixed-key merge literal diagnoses each entry",
			source: `
def f(h: hash<string, int>)
  h.merge!({ a: 1, "b": 2 })
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "compatible mixed-key merge preserves the fact",
			source: `
def f(h: hash<string | symbol, int>)
  h.merge!({ a: 1, "b": 2 })
  h[true] = 1
end
`,
			warning: "write to h expected key string | symbol, got bool",
		},
		{
			name: "update alias of merge",
			source: `
def f(h: hash<symbol, int>)
  h.update({ a: "bad" })
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "merge with a local shape fact keeps the whole-shape check",
			source: `
def f(h: hash<symbol, int>)
  bad = { a: "bad" }
  h.merge!(bad)
end
`,
			warning: "write to h expected hash<symbol, int>, got { a: string }",
		},
		{
			name: "declared shape field type",
			source: `
def f(user: { name: string })
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "declared shape field type through a string key",
			source: `
def f(user: { name: string })
  user["name"] = 42
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "declared shape extra field",
			source: `
def f(user: { name: string })
  user[:extra] = 1
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "compatible entry write preserves the fact",
			source: `
def f(h: hash<string, int>)
  h["a"] = 1
  h[:sym] = 2
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "compatible store preserves the fact",
			source: `
def f(h: hash<string, int>)
  h.store("a", 1)
  h["b"] = "bad"
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "compatible merge preserves the fact",
			source: `
def f(h: hash<symbol, int>)
  h.merge!({ a: 1 })
  h[:b] = "bad"
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "safe navigation store checks the nullable bound",
			source: `
def f(h: hash<string, int>?)
  h&.store(:sym, 1)
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "nullable hash narrowed by a nil guard",
			source: `
def f(h: hash<string, int>?)
  return if h.nil?
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "local seeded by a declared return type",
			source: `
def build -> hash<string, int>
  {}
end

def f
  h = build()
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "write through an alias of the typed hash",
			source: `
def f(h: hash<string, int>)
  g = h
  g[:sym] = 1
end
`,
			warning: "write to g expected key string, got symbol",
		},
		{
			name: "write inside a conditional branch",
			source: `
def f(h: hash<string, int>, flag)
  if flag
    h[:sym] = 1
  end
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "refined parse_as field contradicts a later boundary",
			source: `
def g(user: { name: string })
  user
end

def f(raw: string)
  body = JSON.parse_as(raw, { name: string })
  body["name"] = 1
  g(body)
end
`,
			warning: "call to g argument user expected { name: string }, got { name: int }",
		},
		{
			name: "string write to an empty literal adopts the representation",
			source: `
def takes_string(value: string)
  value
end

def f
  h = {}
  h["name"] = 1
  takes_string(h["name"])
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "symbol write to an empty literal refines the shape",
			source: `
def takes_string(value: string)
  value
end

def f
  h = {}
  h[:name] = 1
  takes_string(h[:name])
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "refined parse_as field feeds a typed read",
			source: `
def takes_string(value: string)
  value
end

def f(raw: string)
  body = JSON.parse_as(raw, { name: string })
  body["name"] = 1
  takes_string(body["name"])
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

func TestCheckHashWritesStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "compatible writes stay silent",
			source: `
def f(h: hash<string, int>)
  h["a"] = 1
  h.store("b", 2)
  h.merge!({ "c": 3 })
end
`,
		},
		{
			name: "compatible declared shape write weakens silently",
			source: `
def f(user: { name: string })
  user["name"] = "x"
  user[:name] = "y"
end
`,
		},
		{
			name: "unknown keys and values stay silent",
			source: `
def f(h: hash<string, int>, k, v)
  h[k] = 1
  h["a"] = v
  h.store(k, v)
end
`,
		},
		{
			name: "unknown receiver stays silent",
			source: `
def f(h)
  h[:a] = "s"
  h.store(:a, 1)
  h.merge!({ a: 1 })
end
`,
		},
		{
			name: "any-typed hash stays silent",
			source: `
def f(h: hash<any, any>)
  h[:a] = "s"
  h["b"] = nil
  h.store(1, true)
end
`,
		},
		{
			name: "bare hash annotation stays silent",
			source: `
def f(h: hash)
  h[:a] = "s"
  h["b"] = 1
end
`,
		},
		{
			name: "nullable hash without a guard stays silent",
			source: `
def f(h: hash<string, int>?)
  h[:sym] = 1
end
`,
		},
		{
			name: "literal shape is evidence and accepts retypes and new fields",
			source: `
def f
  h = { name: "x" }
  h[:name] = 1
  h[:extra] = true
end
`,
		},
		{
			name: "write through the other representation after adoption weakens",
			source: `
def takes_string(value: string)
  value
end

def f
  h = {}
  h["a"] = 1
  h[:a] = "s"
  takes_string(h["a"])
end
`,
		},
		{
			name: "merge source with container values keeps the receiver linked",
			source: `
def helper(a)
  a
end

def f(h: hash<symbol, array<int>>, other: hash<symbol, array<int>>)
  h.merge!(other)
  helper(other)
  h[true] = [1]
end
`,
		},
		{
			name: "unstorable index key aborts before the value writes",
			source: `
def f(h: hash<any, int>)
  h[{ a: 1 }] = "bad"
end
`,
		},
		{
			name: "unstorable store key aborts before the value writes",
			source: `
def f(h: hash<any, int>)
  h.store({ a: 1 }, "bad")
end
`,
		},
		{
			name: "provably non-hash merge argument aborts the call",
			source: `
def f(h: hash<string, int>)
  h.merge!({ "a": "bad" }, 1)
end
`,
		},
		{
			name: "provably non-array merge splat aborts the call",
			source: `
def f(h: hash<string, int>)
  h.merge!(*1, { "a": "bad" })
end
`,
		},
		{
			name: "callable value bounds are not modeled as builtin mutators",
			source: `
def f(h: hash<string, function>, v)
  h.store(:sym, v)
end
`,
		},
		{
			name: "mixed-key local merge into a union key bound stays silent",
			source: `
def f(h: hash<string | symbol, int>)
  bad = { a: 1, "b": 2 }
  h.merge!(bad)
end
`,
		},
		{
			name: "mixed-key literal shape stays silent",
			source: `
def f
  h = { a: 1, "b": 2 }
  h[:c] = 3
end
`,
		},
		{
			name: "unknown entry write weakens the fact for later writes",
			source: `
def f(h: hash<string, int>, v)
  h["a"] = v
  h[:sym] = 1
end
`,
		},
		{
			name: "consumed store result drops the bound",
			source: `
def f(h: hash<string, int>)
  x = h.store("a", 1)
  h[:sym] = 1
end
`,
		},
		{
			name: "merge with a conflict block checks and preserves nothing",
			source: `
def f(h: hash<symbol, int>)
  h.merge!({ a: "bad" }) do |key, old, new|
    old
  end
  h[:b] = "bad"
end
`,
		},
		{
			name: "merge with a possibly empty hash argument stays silent",
			source: `
def f(h: hash<symbol, int>, other: hash<symbol, string>)
  h.merge!(other)
  h[:b] = "bad"
end
`,
		},
		{
			name: "receiver escaping into a call drops the bound",
			source: `
def helper(a)
  a
end

def f(h: hash<string, int>)
  helper(h)
  h[:sym] = 1
end
`,
		},
		{
			name: "unknown write through an alias weakens the group",
			source: `
def f(h: hash<string, int>, v)
  g = h
  g["a"] = v
  h[:sym] = 1
end
`,
		},
		{
			name: "loop body writes weaken the fact",
			source: `
def f(h: hash<string, int>, flag)
  while flag
    h[:sym] = 1
  end
  h[:sym] = 2
end
`,
		},
		{
			name: "block body writes weaken the fact",
			source: `
def f(h: hash<string, int>)
  [1].each do |i|
    h[:sym] = i
  end
  h[:sym] = 2
end
`,
		},
		{
			name: "parse_as write with an unknown value weakens the shape",
			source: `
def takes_string(value: string)
  value
end

def f(raw: string, v)
  body = JSON.parse_as(raw, { name: string })
  body["name"] = v
  takes_string(body["name"])
end
`,
		},
		{
			name: "parse_as write through the other key representation weakens",
			source: `
def g(user: { name: string })
  user
end

def f(raw: string)
  body = JSON.parse_as(raw, { name: string })
  body[:name] = 1
  g(body)
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
