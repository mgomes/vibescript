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

func TestCheckHashBackedShapeMutatorShadows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		source   string
		warnings []string
	}{
		{
			name: "store",
			source: `
def takes_int(value: int)
  value
end

def f(user: { store: int })
  user.store(:store, "bad")
  takes_int("reachable")
end
`,
			warnings: []string{
				"write to user field store expected int, got string",
				"call to takes_int argument value expected int, got string",
			},
		},
		{
			name: "replace",
			source: `
def takes_int(value: int)
  value
end

def f(user: { replace: int })
  user.replace({ replace: "bad" })
  takes_int("reachable")
end
`,
			warnings: []string{
				"write to user field replace expected int, got string",
				"call to takes_int argument value expected int, got string",
			},
		},
		{
			name: "optional noncallable store",
			source: `
def takes_int(value: int)
  value
end

def f(user: { store?: int })
  user.store(:extra, 1)
  takes_int("reachable")
end
`,
			warnings: []string{
				"write to user adds field extra to exact shape { store?: int }",
				"call to takes_int argument value expected int, got string",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.source)
			for _, warning := range tc.warnings {
				requireCheckWarningContains(t, script, warning)
			}
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



`)

	compareArrays(t, callFunc(t, script, "store_splat", nil), []Value{
		NewInt(2),
		NewInt(2),
	})
}

func TestHashWriteDynamicExpansionsMatchReachableRuntimeCalls(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `
def store_expansion(h: hash<string, int>, other: hash<string, int>, args: array<any>, opts)
  h.store(*args, **opts)
  other[:bad] = 1
  [h["stored"], other[:bad]]
end
`)

	cases := []struct {
		name string
		args []Value
	}{
		{
			name: "store_expansion",
			args: []Value{
				NewHash(nil),
				NewHash(nil),
				NewArray([]Value{NewString("stored"), NewInt(1)}),
				NewHash(nil),
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compareArrays(t, callFunc(t, script, tc.name, tc.args), []Value{
				NewInt(1),
				NewInt(1),
			})
		})
	}
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
