package runtime

import "testing"

// Entry writes to a local whose fact is a declared hash<K, V> check the key
// against K and the value against V; field writes to a declared shape fact
// check the field's declared type and exactness. Witnessed literal and
// JSON.parse_as shapes carry their store's key representation and refine in
// place instead of warning: they are evidence, not contracts.

func TestTypeExprSatisfiesRejectsOpenShapeForTypedHash(t *testing.T) {
	t.Parallel()

	declared := &TypeExpr{
		Kind:     TypeHash,
		TypeArgs: []*TypeExpr{checkTypeString, checkTypeInt},
	}
	openEmpty := &TypeExpr{
		Kind:  TypeShape,
		Name:  shapeKeysStringMarker,
		Shape: map[string]*TypeExpr{},
		Open:  true,
	}
	openWitnessed := &TypeExpr{
		Kind: TypeShape,
		Name: shapeKeysStringMarker,
		Shape: map[string]*TypeExpr{
			"id": checkTypeInt,
		},
		Open: true,
	}
	cases := []struct {
		name    string
		written *TypeExpr
	}{
		{
			name:    "empty",
			written: openEmpty,
		},
		{
			name:    "witnessed field",
			written: openWitnessed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if typeExprSatisfies(tc.written, declared, nil) {
				t.Fatal("open shape satisfies typed hash")
			}
		})
	}
	if !typeExprSatisfies(openEmpty, &TypeExpr{Kind: TypeHash}, nil) {
		t.Fatal("open shape does not satisfy bare hash")
	}
	anyValues := &TypeExpr{
		Kind:     TypeHash,
		TypeArgs: []*TypeExpr{checkTypeString, {Kind: TypeAny}},
	}
	if !typeExprSatisfies(openEmpty, anyValues, nil) {
		t.Fatal("string-keyed open shape does not satisfy hash<string, any>")
	}
	closedEmpty := *openEmpty
	closedEmpty.Open = false
	if !typeExprSatisfies(&closedEmpty, declared, nil) {
		t.Fatal("exact empty shape does not satisfy typed hash")
	}
}

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
			name: "store value from an exact literal splat",
			source: `
def f(h: hash<string, int>)
  h.store(*["a", "bad"])
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "store value from an exact local splat",
			source: `
def f(h: hash<string, int>)
  args = ["a", "bad"]
  h.store(*args)
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "splatted literal merge entries are checked",
			source: `
def f(h: hash<string, int>)
  h.merge!(*[{ "a": "bad" }])
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "splatted local merge entries are checked",
			source: `
def f(h: hash<string, int>)
  args = [{ "a": "bad" }]
  h.merge!(*args)
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "splatted local update entries are checked",
			source: `
def f(h: hash<string, int>)
  args = [{ "a": "bad" }]
  h.update(*args)
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "splatted literal shape merge entries are checked",
			source: `
def f(user: { name: string })
  user.merge!(*[{ extra: 1 }])
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "hash-element splat keeps later merge entries checked",
			source: `
def f(h: hash<string, int>)
  h.merge!(*[{ "a": 1 }], { "b": "bad" })
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
			name: "string and symbol merge keys with the same spelling stay distinct",
			source: `
def f(h: hash<string, int>)
  h.merge!({ a: 1, "a": 2 })
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
			name: "member write to typed hash value",
			source: `
def f(h: hash<string, int>)
  h.value = "bad"
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "compound member write to typed hash value",
			source: `
def f(h: hash<string, int>)
  h.value += 0.5
end
`,
			warning: "write to h expected value int, got float",
		},
		{
			name: "logical member write to typed hash value",
			source: `
def f(h: hash<string, int>)
  h.value &&= "bad"
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "member write has a string or symbol hash key",
			source: `
def f(h: hash<int, int>)
  h.value = 1
end
`,
			warning: "write to h expected key int, got string | symbol",
		},
		{
			name: "compatible member write preserves a dual-key hash",
			source: `
def f(h: hash<string | symbol, int>)
  h.value = 1
  h["bad"] = "bad"
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "member write to declared shape field",
			source: `
def f(user: { name: string })
  user.name = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "member write adds a declared shape field",
			source: `
def f(user: { name: string })
  user.extra = 1
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "compound member write to declared shape field",
			source: `
def f(user: { count: int })
  user.count += 0.5
end
`,
			warning: "write to user field count expected int, got float",
		},
		{
			name: "and assignment writes a declared shape field",
			source: `
def f(user: { name: string })
  user.name &&= 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "or assignment may write a nullable declared shape field",
			source: `
def f(user: { name: string? })
  user.name ||= 1
end
`,
			warning: "write to user field name expected string?, got int",
		},
		{
			name: "logical universal member write adds an exact shape field",
			source: `
def f(user: { name: string })
  user.nil? ||= 1
end
`,
			warning: "write to user adds field nil? to exact shape { name: string }",
		},
		{
			name: "logical universal member write checks a typed hash key",
			source: `
def f(h: hash<int, int>)
  h.nil? ||= 1
end
`,
			warning: "write to h expected key int, got string | symbol",
		},
		{
			name: "skipped member assignment preserves a declared shape",
			source: `
def f(user: { name: string })
  user.name ||= 1
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "skipped member assignment preserves a typed hash",
			source: `
def f(h: hash<string, int>)
  h.value ||= 1
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued missing member assignment preserves a declared shape",
			source: `
def f(user: { name: string })
  begin
    user.extra ||= 1
  rescue
    nil
  end
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "member assignment does not weaken its newly retained child",
			source: `
def f(user: { child: hash<string, int> }, child: hash<string, int>)
  user.child = child
  child[:bad] = 1
end
`,
			warning: "write to child expected key string, got symbol",
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
			name: "compatible exact store splat preserves the fact",
			source: `
def f(h: hash<string, int>)
  args = ["a", 1]
  h.store(*args)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "compatible exact merge splat preserves the fact",
			source: `
def f(h: hash<string, int>)
  args = [{ "a": 1 }]
  h.merge!(*args)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "empty exact merge splat preserves the fact",
			source: `
def f(h: hash<string, int>)
  args = []
  h.merge!(*args)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued empty exact store splat preserves the fact",
			source: `
def f(h: hash<string, int>)
  begin
    h.store(*[])
  rescue
    nil
  end
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
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
			name: "replace checks a literal value",
			source: `
def f(h: hash<string, int>)
  h.replace({ "a": "bad" })
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "replace checks a literal key",
			source: `
def f(h: hash<string, int>)
  h.replace({ bad: 1 })
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "replace checks an exact local source",
			source: `
def f(h: hash<string, int>)
  replacement = { "a": "bad" }
  h.replace(replacement)
end
`,
			warning: "write to h expected hash<string, int>, got { a: string }",
		},
		{
			name: "replace exact splat checks its expanded hash",
			source: `
def f(h: hash<string, int>)
  args = [{ "a": "bad" }]
  h.replace(*args)
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "compatible replace exact splat preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  args = [{ "a": 1 }]
  h.replace(*args)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "compatible replace preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  h.replace({ "a": 1 })
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "compatible exact local replace preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  replacement = { "a": 1 }
  h.replace(replacement)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "empty replace preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  h.replace({})
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "replace does not retain a scalar source hash root",
			source: `
def consume(value)
  value
end

def f(h: hash<string, int>, replacement: hash<string, int>)
  h.replace(replacement)
  consume(replacement)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "compatible typed replace source preserves the hash fact",
			source: `
def f(h: hash<string, int>, replacement: hash<string, int>)
  h.replace(replacement)
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued non-hash replace preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  begin
    h.replace(1)
  rescue
    nil
  end
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued empty replace splat preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  begin
    h.replace(*[])
  rescue
    nil
  end
  h[:bad] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "replace checks a declared shape field",
			source: `
def f(user: { name: string })
  user.replace({ name: 1 })
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "replace rejects an extra declared shape field",
			source: `
def f(user: { name: string })
  user.replace({ name: "ok", extra: 1 })
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "replace requires every declared shape field",
			source: `
def f(user: { name: string })
  user.replace({})
end
`,
			warning: "write to user removes required field name from exact shape { name: string }",
		},
		{
			name: "compatible replace preserves the declared shape",
			source: `
def f(user: { name: string })
  user.replace({ name: "ok" })
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "store on a declared shape rejects an extra field",
			source: `
def f(user: { name: string })
  user.store(:extra, 1)
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "store on a declared shape checks the field type",
			source: `
def f(user: { name: string })
  user.store(:name, 1)
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "store on a literal shape refines the field",
			source: `
def takes_string(value: string)
  value
end

def f
  h = { name: "x" }
  h.store(:name, 1)
  takes_string(h[:name])
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name: "store field on a literal hash does not shadow the builtin",
			source: `
def takes_int(value: int)
  value
end

def f
  h = { store: 0 }
  h.store(:store, "bad")
  takes_int(h[:store])
end
`,
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name: "merge on a declared shape rejects an extra field",
			source: `
def f(user: { name: string })
  user.merge!({ extra: 1 })
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "shape merge checks an exact shape local argument",
			source: `
def f(user: { name: string })
  extra = { extra: 1 }
  user.merge!(extra)
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
		},
		{
			name: "shape merge checks a shape local field type",
			source: `
def f(user: { name: string })
  patch = { name: 1 }
  user.merge!(patch)
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "shape merge checks a required shape parameter field",
			source: `
def f(user: { name: string }, patch: { extra: int })
  user.merge!(patch)
end
`,
			warning: "write to user adds field extra to exact shape { name: string }",
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
			name: "container write to an unaliased literal refines the shape",
			source: `
def takes_strings(value: array<string>)
  value
end

def f
  h = {}
  v = [1]
  h[:profile] = v
  takes_strings(h[:profile])
end
`,
			warning: "call to takes_strings argument value expected array<string>, got array<int>",
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
		{
			name: "zero-argument shape merge preserves the fact",
			source: `
def f(user: { name: string })
  user.merge!()
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "empty-literal shape merge preserves the fact",
			source: `
def f(user: { name: string })
  user.merge!({})
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "empty-splat shape merge preserves the fact",
			source: `
def f(user: { name: string })
  user.merge!(*[])
  user[:name] = 1
end
`,
			warning: "write to user field name expected string, got int",
		},
		{
			name: "rescued unstorable index key preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  begin
    h[{}] = 1
  rescue
    nil
  end
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued unstorable store key preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  begin
    h.store({}, 1)
  rescue
    nil
  end
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued invalid merge argument preserves the hash fact",
			source: `
def f(h: hash<string, int>)
  begin
    h.merge!(1)
  rescue
    nil
  end
  h[:sym] = 1
end
`,
			warning: "write to h expected key string, got symbol",
		},
		{
			name: "rescued store abort preserves retained child facts",
			source: `
def f(h: hash<string, array<int>>, child: array<int>)
  h["child"] = child
  begin
    h.store({}, child)
  rescue
    nil
  end
  child << "bad"
end
`,
			warning: "write to child expected element int, got string",
		},
		{
			name: "literal merge uses the value fact from its evaluation point",
			source: `
def f(h: hash<string, int>)
  value = "bad"
  h.merge!({ "first": value }, lambda { value = 1; {} }.call)
end
`,
			warning: "write to h expected value int, got string",
		},
		{
			name: "callable shape shadow keeps the continuation reachable",
			source: `
def takes_int(value: int)
  value
end

def f(user: { store: function })
  user.store({}, 1)
  takes_int("bad")
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
			name: "unstorable index key stops the continuation",
			source: `
def takes_int(value: int)
  value
end

def f(h: hash<string, int>)
  h[{}] = 1
  takes_int("unreachable")
end
`,
		},
		{
			name: "unstorable store key stops the continuation",
			source: `
def takes_int(value: int)
  value
end

def f(h: hash<string, int>)
  h.store({}, 1)
  takes_int("unreachable")
end
`,
		},
		{
			name: "invalid merge argument stops the continuation",
			source: `
def takes_int(value: int)
  value
end

def f(h: hash<string, int>)
  h.merge!(1)
  takes_int("unreachable")
end
`,
		},
		{
			name: "literal merge keeps an earlier entry value fact",
			source: `
def f(h: hash<string, int>)
  value = 1
  h.merge!({
    "first": value,
    "second": lambda { value = []; 2 }.call,
  })
end
`,
		},
		{
			name: "shape merge keeps a value fact before later arguments",
			source: `
def f(user: { first: int, second: int })
  value = 1
  user.merge!({ first: value }, lambda { value = { second: 2 } }.call)
end
`,
		},
		{
			name: "typed hash merge ignores an overwritten literal value",
			source: `
def f(h: hash<string, int>)
  h.merge!({ "a": "bad", "a": 1 })
end
`,
		},
		{
			name: "shape merge ignores an overwritten literal field",
			source: `
def f(user: { name: string })
  user.merge!({ name: 1, name: "ok" })
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
			name: "skipped member or assignment does not write",
			source: `
def f(user: { name: string })
  user.name ||= 1
end
`,
		},
		{
			name: "false universal member skips and assignment",
			source: `
def f(user: { name: string })
  user.nil? &&= 1
end
`,
		},
		{
			name: "missing compound member aborts before an extra field write",
			source: `
def f(user: { name: string })
  user.extra += 1
end
`,
		},
		{
			name: "missing logical member aborts before an extra field write",
			source: `
def f(user: { name: string })
  user.extra ||= 1
end
`,
		},
		{
			name: "single-key typed hash member write weakens gradually",
			source: `
def f(h: hash<string, int>)
  h.value = 1
  h[:bad] = 1
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
			name: "possibly empty incompatible replace source stays gradual",
			source: `
def f(h: hash<string, int>, replacement: hash<string, string>)
  h.replace(replacement)
  h[:bad] = 1
end
`,
		},
		{
			name: "unknown replace source weakens the fact",
			source: `
def f(h: hash<string, int>, replacement)
  h.replace(replacement)
  h[:bad] = 1
end
`,
		},
		{
			name: "consumed replace result weakens the fact",
			source: `
def consume(value)
  value
end

def f(h: hash<string, int>)
  consume(h.replace({ "a": 1 }))
  h[:bad] = 1
end
`,
		},
		{
			name: "open shape replacement weakens an exact shape",
			source: `
def f(user: { name: string }, replacement: { ... })
  user.replace(replacement)
  user[:extra] = 1
end
`,
		},
		{
			name: "shape replace shadowed by a data field stays gradual",
			source: `
def f(user: { replace: int })
  user.replace({ extra: 1 })
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
			name: "partially compatible entry value still links its root",
			source: `
def helper(x)
  x
end

def f(h: hash<string, array<int>>, v: array<number>)
  h["a"] = v
  helper(h)
  v << "bad"
end
`,
		},
		{
			name: "partially compatible literal merge value still links its root",
			source: `
def helper(x)
  x
end

def f(h: hash<string, array<int>>, v: array<number>)
  h.merge!({ "a": v })
  helper(h)
  v << "bad"
end
`,
		},
		{
			name: "aliased literal shape write links its retained value",
			source: `
def helper(x)
  x
end

def f(v: array<int>)
  h = {}
  g = h
  h[:profile] = v
  helper(g)
  v << "bad"
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
			name: "shape mutator shadowed by a callable field stays gradual",
			source: `
def f(user: { store: function }, v)
  user.store(:extra, v)
end
`,
		},
		{
			name: "shape store shadowed by a data field stays gradual",
			source: `
def f(user: { store: int })
  user.store(:extra, 1)
end
`,
		},
		{
			name: "shape merge shadowed by a data field stays gradual",
			source: `
def f(user: { "merge!": int })
  user.merge!({ extra: 1 })
end
`,
		},
		{
			name: "conflict-block shape merge keeps escape semantics",
			source: `
def f(user: { name: string }, other: hash<symbol, array<int>>)
  user.merge!(other) do |key, old, new|
    old
  end
  other[true] = [1]
end
`,
		},
		{
			name: "non-hash argument aborts a declared shape merge",
			source: `
def f(user: { name: string })
  user.merge!({ extra: 1 }, 5)
end
`,
		},
		{
			name: "optional extra field in a shape merge may be absent",
			source: `
def f(user: { name: string }, patch: { extra?: int })
  user.merge!(patch)
end
`,
		},
		{
			name: "optional present field in a shape merge may be absent",
			source: `
def f(user: { name: string }, patch: { name?: int })
  user.merge!(patch)
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
			name: "unknown merge splat stays gradual and weakens",
			source: `
def f(h: hash<string, int>, v)
  h.merge!(*v)
  h[:sym] = 1
end
`,
		},
		{
			name: "provably non-hash splatted merge element aborts the call",
			source: `
def f(h: hash<string, int>)
  h.merge!(*[1], { "a": "bad" })
end
`,
		},
		{
			name: "merge splat abort uses its evaluation-time fact",
			source: `
def helper(x)
  x
end

def f(h: hash<string, int>)
  v = [1]
  h.merge!(*v, helper(v), { "a": "bad" })
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
		{
			name: "open shape receiver mutators stay gradual",
			source: `
def f(user: { name: string, ... })
  user.store(:extra, 1)
  user.merge!({ extra: 2 })
end
`,
		},
		{
			name: "open empty shape merge source may write",
			source: `
def f(h: hash<string, int>, patch: { ... })
  h.merge!(patch)
  h[:symbol] = 1
end
`,
		},
		{
			name: "union with open shape merge source may write",
			source: `
def f(h: hash<string, int>, patch: hash<string, int> | { ... })
  h.merge!(patch)
  h[:symbol] = 1
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

func TestHashWriteExactSplatsMatchRuntimeArguments(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def store_splat
  h = { "a": 1 }
  args = ["a", 2]
  result = h.store(*args)
  [result, h["a"]]
end

def merge_splat
  h = { "a": 1 }
  args = [{ "b": 2 }, { "a": 3 }]
  result = h.merge!(*args)
  [result.equal?(h), h["a"], h["b"]]
end

def update_splat
  h = { "a": 1 }
  args = [{ "b": 2 }]
  result = h.update(*args)
  [result.equal?(h), h["a"], h["b"]]
end

def empty_merge_splat
  h = { "a": 1 }
  result = h.merge!(*[])
  [result.equal?(h), h["a"]]
end
`)

	compareArrays(t, callFunc(t, script, "store_splat", nil), []Value{
		NewInt(2),
		NewInt(2),
	})
	compareArrays(t, callFunc(t, script, "merge_splat", nil), []Value{
		NewBool(true),
		NewInt(3),
		NewInt(2),
	})
	compareArrays(t, callFunc(t, script, "update_splat", nil), []Value{
		NewBool(true),
		NewInt(1),
		NewInt(2),
	})
	compareArrays(t, callFunc(t, script, "empty_merge_splat", nil), []Value{
		NewBool(true),
		NewInt(1),
	})
}

func TestHashMemberAssignmentMatchesRuntimeKeySelection(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def existing_symbol
  h = { name: "before" }
  h.name = 1
  [h[:name], h["name"]]
end

def existing_string
  h = { "name": "before" }
  h.name = 1
  [h[:name], h["name"]]
end

def symbol_wins
  h = { name: "symbol", "name": "string" }
  h.name = 1
  [h[:name], h["name"]]
end

def missing_key
  h = {}
  h.name = 1
  [h[:name], h["name"]]
end

def compound_and_logical
  h = { count: 1, name: "before", fallback: nil }
  h.count += 2
  h.name &&= "after"
  h.fallback ||= "set"
  [h[:count], h[:name], h[:fallback]]
end
`)

	compareArrays(t, callFunc(t, script, "existing_symbol", nil), []Value{
		NewInt(1),
		NewNil(),
	})
	compareArrays(t, callFunc(t, script, "existing_string", nil), []Value{
		NewNil(),
		NewInt(1),
	})
	compareArrays(t, callFunc(t, script, "symbol_wins", nil), []Value{
		NewInt(1),
		NewString("string"),
	})
	compareArrays(t, callFunc(t, script, "missing_key", nil), []Value{
		NewInt(1),
		NewNil(),
	})
	compareArrays(t, callFunc(t, script, "compound_and_logical", nil), []Value{
		NewInt(3),
		NewString("after"),
		NewString("set"),
	})
}

func TestHashReplaceMatchesRuntimeWholeStore(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def replace_entries
  h = { "old": 1 }
  replacement = { "a": 2 }
  result = h.replace(replacement)
  replacement["a"] = 3
  [result.equal?(h), h["old"], h["a"], replacement["a"]]
end

def replace_exact_splat
  h = { "old": 1 }
  args = [{ "a": 2 }]
  result = h.replace(*args)
  [result.equal?(h), h["old"], h["a"]]
end

def replace_self
  h = { name: 1 }
  result = h.replace(h)
  [result.equal?(h), h[:name]]
end
`)

	compareArrays(t, callFunc(t, script, "replace_entries", nil), []Value{
		NewBool(true),
		NewNil(),
		NewInt(2),
		NewInt(3),
	})
	compareArrays(t, callFunc(t, script, "replace_exact_splat", nil), []Value{
		NewBool(true),
		NewNil(),
		NewInt(2),
	})
	compareArrays(t, callFunc(t, script, "replace_self", nil), []Value{
		NewBool(true),
		NewInt(1),
	})
}

func TestHashReplaceShapeCheckMatchesRuntimeKeyIdentity(t *testing.T) {
	t.Parallel()

	colliding := compileScriptDefault(t, `
def replace_fields(user: { name: int })
  user.replace({ name: 1, "name": 2 })
end
`)
	requireCheckWarningContains(
		t,
		colliding,
		"write to user adds field name to exact shape { name: int }",
	)

	receiver := NewTypedHash(1)
	if err := receiver.HashSet(NewSymbol("name"), NewInt(0)); err != nil {
		t.Fatalf("HashSet(:name, 0) error = %v", err)
	}
	got := callFunc(t, colliding, "replace_fields", []Value{receiver})
	if got.HashLen() != 2 {
		t.Fatalf("replace_fields({name: 0}).HashLen() = %d, want 2", got.HashLen())
	}
	if value, ok, err := got.HashGet(NewSymbol("name")); err != nil {
		t.Fatalf("replace_fields({name: 0})[:name] error = %v", err)
	} else if !ok || !value.Equal(NewInt(1)) {
		t.Errorf("replace_fields({name: 0})[:name] = %v, %t, want 1, true", value, ok)
	}
	if value, ok, err := got.HashGet(NewString("name")); err != nil {
		t.Fatalf(`replace_fields({name: 0})["name"] error = %v`, err)
	} else if !ok || !value.Equal(NewInt(2)) {
		t.Errorf(`replace_fields({name: 0})["name"] = %v, %t, want 2, true`, value, ok)
	}

	overwritten := compileScriptDefault(t, `
def replace_fields(user: { name: int })
  user.replace({ name: "discarded", name: 2 })
end
`)
	requireNoCheckWarnings(t, overwritten)

	receiver = NewTypedHash(1)
	if err := receiver.HashSet(NewSymbol("name"), NewInt(0)); err != nil {
		t.Fatalf("HashSet(:name, 0) error = %v", err)
	}
	got = callFunc(t, overwritten, "replace_fields", []Value{receiver})
	if got.HashLen() != 1 {
		t.Fatalf("replace_fields({name: 0}).HashLen() = %d, want 1", got.HashLen())
	}
	if value, ok, err := got.HashGet(NewSymbol("name")); err != nil {
		t.Fatalf("replace_fields({name: 0})[:name] error = %v", err)
	} else if !ok || !value.Equal(NewInt(2)) {
		t.Errorf("replace_fields({name: 0})[:name] = %v, %t, want 2, true", value, ok)
	}
}
