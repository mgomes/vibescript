package runtime

import "testing"

func TestCheckKnownUnionBoundaryAssignability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "call argument",
			source: `
def takes_int(value: int)
  value
end

def run(flag: bool)
  value = flag ? 1 : "bad"
  takes_int(value)
end
`,
			warning: "call to takes_int argument value expected int, got int | string",
		},
		{
			name: "known arm beside any",
			source: `
def takes_int(value: int)
  value
end

def run(flag: bool, opaque: any)
  value = flag ? opaque : "bad"
  takes_int(value)
end
`,
			warning: "call to takes_int argument value expected int, got any | string",
		},
		{
			name: "parameter default",
			source: `
def run(flag: bool, value: int = flag ? 1 : "bad")
  value
end
`,
			warning: "default value for value expected int, got int | string",
		},
		{
			name: "explicit return",
			source: `
def run(flag: bool) -> int
  return flag ? 1 : "bad"
end
`,
			warning: "return value expected int, got int | string",
		},
		{
			name: "implicit return",
			source: `
def run(flag: bool) -> int
  flag ? 1 : "bad"
end
`,
			warning: "return value expected int, got int | string",
		},
		{
			name: "nullable return",
			source: `
def run(flag: bool) -> int
  flag ? 1 : nil
end
`,
			warning: "return value expected int, got int | nil",
		},
		{
			name: "nullable boundary",
			source: `
def run(flag: bool) -> int?
  flag ? 1 : "bad"
end
`,
			warning: "return value expected int?, got int | string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tt.source), tt.warning)
		})
	}
}

func TestCheckExactEnumSymbolAlternativesAtBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "call argument",
			source: `
enum Color
  Red
end

def takes_color(value: Color)
  value
end

def run(flag: bool)
  takes_color(flag ? :red : :blue)
end
`,
			warning: "call to takes_color argument value expected Color, got symbol",
		},
		{
			name: "parameter default",
			source: `
enum Color
  Red
end

def run(flag: bool, value: Color = flag ? :red : :blue)
  value
end
`,
			warning: "default value for value expected Color, got symbol",
		},
		{
			name: "explicit return",
			source: `
enum Color
  Red
end

def run(flag: bool) -> Color
  return flag ? :red : :blue
end
`,
			warning: "return value expected Color, got symbol",
		},
		{
			name: "implicit return",
			source: `
enum Color
  Red
end

def run(flag: bool) -> Color
  flag ? :red : :blue
end
`,
			warning: "return value expected Color, got symbol",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tt.source), tt.warning)
		})
	}
}

func TestCheckKnownUnionBoundaryAssignabilityRecurses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "nullable",
			source: `
def takes_int(value: int)
  value
end

def run(value: int?)
  takes_int(value)
end
`,
			warning: "call to takes_int argument value expected int, got int?",
		},
		{
			name: "array element",
			source: `
def takes_ints(values: array<int>)
  values
end

def run(values: array<int | string>)
  takes_ints(values)
end
`,
			warning: "call to takes_ints argument values expected array<int>, got array<int | string>",
		},
		{
			name: "hash value",
			source: `
def takes_counts(values: hash<string, int>)
  values
end

def run(values: hash<string, int | string>)
  takes_counts(values)
end
`,
			warning: "call to takes_counts argument values expected hash<string, int>, got hash<string, int | string>",
		},
		{
			name: "shape field",
			source: `
def takes_record(value: { count: int })
  value
end

def run(value: { count: int | string })
  takes_record(value)
end
`,
			warning: "call to takes_record argument value expected { count: int }, got { count: int | string }",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tt.source), tt.warning)
		})
	}
}

func TestCheckKnownUnionShapeHashKeyAssignability(t *testing.T) {
	t.Parallel()

	inferred := &TypeExpr{
		Kind: TypeShape,
		Name: mixedKeysMarker(false, false, true),
		Shape: map[string]*TypeExpr{
			"1": checkTypeInt,
		},
	}
	for _, keyType := range []*TypeExpr{
		checkTypeString,
		checkTypeSymbol,
		unionTypeExprs(checkTypeString, checkTypeSymbol),
	} {
		required := &TypeExpr{
			Kind:     TypeHash,
			TypeArgs: []*TypeExpr{keyType, checkTypeInt},
		}
		if !boundaryTypeRejected(inferred, required, nil) {
			t.Errorf("boundaryTypeRejected(%s, %s) = false, want true", formatTypeExpr(inferred), formatTypeExpr(required))
		}
	}

	for _, keyType := range []*TypeExpr{
		checkTypeInt,
		unionTypeExprs(checkTypeString, checkTypeInt),
	} {
		required := &TypeExpr{
			Kind:     TypeHash,
			TypeArgs: []*TypeExpr{keyType, checkTypeInt},
		}
		if boundaryTypeRejected(inferred, required, nil) {
			t.Errorf("boundaryTypeRejected(%s, %s) = true, want false", formatTypeExpr(inferred), formatTypeExpr(required))
		}
	}
}

func TestCheckKnownUnionBoundaryAssignabilityStaysGradual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "any value",
			source: `
def takes_int(value: int)
  value
end

def run(value: any)
  takes_int(value)
end
`,
		},
		{
			name: "unknown value",
			source: `
def takes_int(value: int)
  value
end

def run(value)
  takes_int(value)
end
`,
		},
		{
			name: "unknown JSON value",
			source: `
def takes_int(value: int)
  value
end

def run(raw: string)
  takes_int(JSON.parse(raw)["count"])
end
`,
		},
		{
			name: "dynamic dispatch result",
			source: `
def takes_int(value: int)
  value
end

def run(value)
  takes_int(value.count)
end
`,
		},
		{
			name: "nested any",
			source: `
def takes_ints(values: array<int>)
  values
end

def run(values: array<any>)
  takes_ints(values)
end
`,
		},
		{
			name: "matching union",
			source: `
def accept(value: int | string)
  value
end

def run(value: int | string)
  accept(value)
end
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, tt.source))
		})
	}
}

func TestCheckKnownUnionRestAggregateAssignability(t *testing.T) {
	t.Parallel()

	rest := compileScriptDefault(t, `
def collect(*values: array<int> | array<string>)
  values
end

def run(count: int, label: string)
  collect(count, label)
end
`)
	requireCheckWarningContains(t, rest, "call to collect argument values expected array<int> | array<string>, got array<int | string>")

	keywords := compileScriptDefault(t, `
def collect(**values: { count: int, label: int } | { count: string, label: string })
  values
end

def run(count: int, label: string)
  collect(count:, label:)
end
`)
	requireCheckWarningContains(t, keywords, "call to collect argument values expected { count: int, label: int } | { count: string, label: string }, got { count: int, label: string }")
}

func TestCheckKnownUnionRestAggregateCorrelation(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def collect(*values: array<int> | array<string>)
  values
end

def run(value: int | string, unknown)
  collect(value)
  collect(value, value)
  collect(value, unknown)
end
`))

	independent := compileScriptDefault(t, `
def collect(*values: array<int> | array<string>)
  values
end

def run(left: int | string, right: int | string)
  collect(left, right)
end
`)
	requireCheckWarningContains(t, independent, "call to collect argument values expected array<int> | array<string>, got array<int | string>")

	sharedReturnType := compileScriptDefault(t, `
def choose(flag: bool) -> int | string
  flag ? 1 : "value"
end

def collect(*values: array<int> | array<string>)
  values
end

def run(left_flag: bool, right_flag: bool)
  left = choose(left_flag)
  right = choose(right_flag)
  collect(left, right)
end
`)
	requireCheckWarningContains(t, sharedReturnType, "call to collect argument values expected array<int> | array<string>, got array<int | string>")

	mutatedSource := compileScriptDefault(t, `
def collect(*values: array<int> | array<string>)
  values
end

def run(flag: bool)
  collect(
    flag ? 1 : "left",
    -> { flag = !flag }.call(),
    flag ? 2 : "right",
  )
end
`)
	requireCheckWarningContains(t, mutatedSource, "call to collect argument values expected array<int> | array<string>, got array<int | string>")

	mutatedKeywordSource := compileScriptDefault(t, `
def collect(**values: { left: int, ignored: int, right: int } | { left: string, ignored: int, right: string })
  values
end

def run(flag: bool)
  collect(
    left: flag ? 1 : "left",
    ignored: [-> { flag = !flag; 0 }.call(), 0][1],
    right: flag ? 2 : "right",
  )
end
`)
	requireCheckWarningContains(t, mutatedKeywordSource, "call to collect argument values expected { ignored: int, left: int, right: int } | { ignored: int, left: string, right: string }, got { ignored: int, left: int | string, right: int | string }")
}

func TestCheckKnownUnionRestAggregatePreservesExactLiterals(t *testing.T) {
	t.Parallel()

	positional := compileScriptDefault(t, `
enum Color
  Red
end

def collect(*values: array<Color> | array<int>)
  values
end

def run(value: int)
  collect(:blue, value)
end
`)
	requireCheckWarningContains(t, positional, "call to collect argument values expected array<Color> | array<int>, got array<symbol | int>")

	positionalAlternatives := compileScriptDefault(t, `
enum Color
  Red
end

def collect(*values: array<Color> | array<int>)
  values
end

def run(flag: bool)
  collect(flag ? :blue : :green)
end
`)
	requireCheckWarningContains(t, positionalAlternatives, "call to collect argument values expected array<Color> | array<int>, got array<symbol>")

	keywords := compileScriptDefault(t, `
enum Color
  Red
end

def collect(**values: hash<string, Color> | nil)
  values
end

def run(opaque)
  collect(color: :blue, other: opaque)
end
`)
	requireCheckWarningContains(t, keywords, "call to collect argument values expected hash<string, Color> | nil, got { color: symbol, other: unknown }")

	keywordAlternatives := compileScriptDefault(t, `
enum Color
  Red
end

def collect(**values: hash<string, Color> | nil)
  values
end

def run(flag: bool)
  collect(color: flag ? :blue : :green)
end
`)
	requireCheckWarningContains(t, keywordAlternatives, "call to collect argument values expected hash<string, Color> | nil, got { color: symbol }")
}

func TestCheckKnownUnionArrayLiteralCorrelation(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(values: array<int> | array<string>)
  values
end

def run(value: int | string, opaque)
  accept([value])
  alias = value
  accept([value, alias])
  accept([value, opaque])
  values = [value, opaque]
  accept(values)
end
`))

	mixed := compileScriptDefault(t, `
def accept(values: array<int> | array<string>)
  values
end

def run()
  accept([1, "bad"])
end
`)
	requireCheckWarningContains(t, mixed, "call to accept argument values expected array<int> | array<string>, got array<int | string>")

	sharedReturnType := compileScriptDefault(t, `
def choose(flag: bool) -> int | string
  flag ? 1 : "value"
end

def accept(values: array<int> | array<string>)
  values
end

def run(left_flag: bool, right_flag: bool)
  left = choose(left_flag)
  right = choose(right_flag)
  accept([left, right])
end
`)
	requireCheckWarningContains(t, sharedReturnType, "call to accept argument values expected array<int> | array<string>, got array<int | string>")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(values: array<int> | array<string>)
  values
end

def run(flag: bool)
  accept([flag ? 1 : "left", flag ? 2 : "right"])
end
`))

	antiCorrelated := compileScriptDefault(t, `
def accept(values: array<int> | array<string>)
  values
end

def run(flag: bool)
  accept([flag ? 1 : "left", flag ? "right" : 2])
end
`)
	requireCheckWarningContains(t, antiCorrelated, "call to accept argument values expected array<int> | array<string>, got array<int | string>")

	mutatedSource := compileScriptDefault(t, `
def accept(values: array<int> | array<string>)
  values
end

def run(flag: bool)
  accept([
    flag ? 1 : "left",
    -> { flag = !flag }.call(),
    flag ? 2 : "right",
  ])
end
`)
	requireCheckWarningContains(t, mutatedSource, "call to accept argument values expected array<int> | array<string>, got array<int | string>")

	write := compileScriptDefault(t, `
def mutate(values: array<array<int>>, value: int | string)
  values[0] = [value]
end
`)
	requireCheckWarningContains(t, write, "write to values expected element array<int>, got array<int | string>")

	alternative := &TypeExpr{
		Kind:     TypeArray,
		Name:     literalAlternativeElementsMarker,
		TypeArgs: []*TypeExpr{unionTypeExprs(checkTypeInt, checkTypeString)},
	}
	if typeExprsDisjoint(alternative, &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{checkTypeInt}}, nil) {
		t.Error("alternative array is disjoint from a boundary accepted by one alternative")
	}
	if !typeExprsDisjoint(alternative, &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{checkTypeBool}}, nil) {
		t.Error("alternative array overlaps a boundary rejected by every alternative")
	}

	reachable := compileScriptDefault(t, `
def mutate(values: array<int>, target: array<int>)
  target << "bad"
end

def run(value: int | string, target: array<int>)
  mutate([value], target)
end
`)
	foundWrite := false
	for _, warning := range reachable.CheckWarningsForFunction("run") {
		if warning.Message == "write to target expected element int, got string" {
			foundWrite = true
			break
		}
	}
	if !foundWrite {
		t.Errorf("CheckWarningsForFunction(%q) = %#v, want reachable body write", "run", reachable.CheckWarningsForFunction("run"))
	}
}

func TestCheckKnownUnionShapeCorrelation(t *testing.T) {
	t.Parallel()

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def run(value: int | string)
  accept({ left: value, right: value })
end
`))

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def run(value: int | string)
  or_alias = nil
  or_alias ||= value
  and_alias = true
  and_alias &&= value
  accept({ left: value, right: or_alias })
  accept({ left: value, right: and_alias })
end
`))

	unknownLogicalAlias := compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def run(value: int | string, other: int | string | nil)
  other ||= value
  accept({ left: value, right: other })
end
`)
	requireCheckWarningContains(t, unknownLogicalAlias, "call to accept argument value expected { left: int, right: int } | { left: string, right: string }, got { left: int | string, right: int | string | nil }")

	forwarded := compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def forward(value)
  accept(value)
end

def run(value: int | string)
  forward({ left: value, right: value })
end
`)
	if warnings := forwarded.CheckWarningsForFunction("run"); len(warnings) > 0 {
		t.Fatalf("CheckWarningsForFunction(%q) = %#v, want none", "run", warnings)
	}

	independent := compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def run(left: int | string, right: int | string)
  accept({ left:, right: })
end
`)
	requireCheckWarningContains(t, independent, "call to accept argument value expected { left: int, right: int } | { left: string, right: string }, got { left: int | string, right: int | string }")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def run(flag: bool)
  accept({
    left: flag ? 1 : "left",
    right: flag ? 2 : "right"
  })
end
`))

	antiCorrelated := compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: string, right: string })
  value
end

def run(flag: bool)
  accept({
    left: flag ? 1 : "left",
    right: flag ? "right" : 2
  })
end
`)
	requireCheckWarningContains(t, antiCorrelated, "call to accept argument value expected { left: int, right: int } | { left: string, right: string }, got { left: int | string, right: string | int }")

	mutatedSource := compileScriptDefault(t, `
def accept(value: { left: int, ignored: int, right: int } | { left: string, ignored: int, right: string })
  value
end

def run(flag: bool)
  accept({
    left: flag ? 1 : "left",
    ignored: [-> { flag = !flag; 0 }.call(), 0][1],
    right: flag ? 2 : "right"
  })
end
`)
	requireCheckWarningContains(t, mutatedSource, "call to accept argument value expected { ignored: int, left: int, right: int } | { ignored: int, left: string, right: string }, got { ignored: int, left: int | string, right: int | string }")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: float, right: float })
  value
end

def run(exponent: int)
  value = 2 ** exponent
  accept({ left: value, right: value })
end
`))

	sharedSingleton := compileScriptDefault(t, `
def accept(value: { left: int, right: int } | { left: float, right: float })
  value
end

def run(left_exponent: int, right_exponent: int)
  accept({ left: 2 ** left_exponent, right: 2 ** right_exponent })
end
`)
	requireCheckWarningContains(t, sharedSingleton, "call to accept argument value expected { left: int, right: int } | { left: float, right: float }, got { left: number, right: number }")

	requireNoCheckWarnings(t, compileScriptDefault(t, `
def accept(value: { other?: bool } | { label: string })
  value
end

def run(value: { label?: string })
  accept(value)
end
`))

	optionalMismatch := compileScriptDefault(t, `
def accept(value: { other?: bool } | { label: string })
  value
end

def run(value: { label?: int })
  accept(value)
end
`)
	requireCheckWarningContains(t, optionalMismatch, "call to accept argument value expected { other?: bool } | { label: string }, got { label?: int }")
}

func TestCheckKnownUnionSpecialBoundaries(t *testing.T) {
	t.Parallel()

	raw := compileScriptDefault(t, `
def run(raw: string | int)
  JSON.parse_as(raw, int)
end
`)
	requireCheckWarningContains(t, raw, "call to JSON.parse_as expects a JSON string as its first argument, got string | int")

	schema := compileScriptDefault(t, `
def run(raw: string, flag: bool, opaque: any)
  candidate = flag ? opaque : 1
  JSON.parse_as(raw, candidate)
end
`)
	requireCheckWarningContains(t, schema, "call to JSON.parse_as expects a type literal as its second argument, got any | int")

	classPredicate := compileScriptDefault(t, `
def run(flag: bool, opaque: any)
  candidate = flag ? opaque : 1
  "value".is_a?(candidate)
end
`)
	requireCheckWarningContains(t, classPredicate, "call to is_a? expects a class argument, got any | int")
}

func TestBoundaryTypeRejectedRecursesPastDisjointnessDepth(t *testing.T) {
	t.Parallel()

	inferred := &TypeExpr{Kind: TypeUnion, Union: []*TypeExpr{checkTypeInt, checkTypeString}}
	required := checkTypeInt
	for range maxTypeArmDepth + 2 {
		inferred = &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{inferred}}
		required = &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{required}}
	}
	if !boundaryTypeRejected(inferred, required, nil) {
		t.Errorf("boundaryTypeRejected(%s, %s) = false, want true", formatTypeExpr(inferred), formatTypeExpr(required))
	}

	unknown := &TypeExpr{Kind: TypeAny}
	for range maxTypeArmDepth + 2 {
		unknown = &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{unknown}}
	}
	if boundaryTypeRejected(unknown, required, nil) {
		t.Errorf("boundaryTypeRejected(%s, %s) = true, want false", formatTypeExpr(unknown), formatTypeExpr(required))
	}
}
