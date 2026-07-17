package runtime

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// builtinTypedContract renders one builtin's typed metadata for comparison.
type builtinTypedContract struct {
	params   string
	keywords string
	result   string
}

func renderTypedContract(spec *staticCallSpec) (builtinTypedContract, bool) {
	if spec == nil {
		return builtinTypedContract{}, false
	}
	if len(spec.paramTypes) == 0 && len(spec.keywordTypes) == 0 && spec.resultType == nil {
		return builtinTypedContract{}, false
	}
	var params []string
	for _, ty := range spec.paramTypes {
		if ty == nil {
			params = append(params, "_")
			continue
		}
		params = append(params, formatTypeExpr(ty))
	}
	var keywords []string
	for name, ty := range spec.keywordTypes {
		keywords = append(keywords, fmt.Sprintf("%s: %s", name, formatTypeExpr(ty)))
	}
	sort.Strings(keywords)
	result := ""
	if spec.resultType != nil {
		result = formatTypeExpr(spec.resultType)
	}
	return builtinTypedContract{
		params:   strings.Join(params, ", "),
		keywords: strings.Join(keywords, ", "),
		result:   result,
	}, true
}

// typedBuiltinContracts walks the live registry (including namespace members)
// and returns every builtin that declares typed metadata.
func typedBuiltinContracts(engine *Engine) map[string]builtinTypedContract {
	out := make(map[string]builtinTypedContract)
	for name, val := range engine.Builtins() {
		switch val.Kind() {
		case KindBuiltin:
			if contract, ok := renderTypedContract(valueBuiltin(val).checkSpec); ok {
				out[name] = contract
			}
		case KindObject:
			for member, memberVal := range val.Hash() {
				if memberVal.Kind() != KindBuiltin {
					continue
				}
				if contract, ok := renderTypedContract(valueBuiltin(memberVal).checkSpec); ok {
					out[name+"."+member] = contract
				}
			}
		}
	}
	return out
}

// TestCoreBuiltinTypedContracts pins the typed contracts of the default
// registry in both directions: an entry here without a matching registry
// contract fails, and a registry contract missing from this table fails, so
// registrations and checker expectations cannot drift apart silently.
func TestCoreBuiltinTypedContracts(t *testing.T) {
	t.Parallel()

	want := map[string]builtinTypedContract{
		"assert":      {result: "nil"},
		"format":      {params: "string", result: "string"},
		"sprintf":     {params: "string", result: "string"},
		"lambda":      {result: "function"},
		"proc":        {result: "function"},
		"money":       {params: "string", result: "money"},
		"money_cents": {params: "number, string", result: "money"},
		"print":       {result: "nil"},
		"puts":        {result: "nil"},
		"warn":        {result: "nil"},
		"now":         {result: "string"},
		"rand":        {params: "int | range | nil", result: "number"},
		"sleep":       {params: "number", result: "int"},
		"srand":       {params: "int?", result: "int?"},
		"uuid":        {result: "string"},
		"random_id":   {params: "int", result: "string"},
		"to_int":      {params: "int | float | string", result: "int"},
		"to_float":    {params: "int | float | string", result: "float"},

		"JSON.parse":     {params: "string"},
		"JSON.stringify": {result: "string"},

		"Proc.new": {result: "function"},
		"Hash.new": {result: "hash"},

		"Regex.match":       {params: "string, string", result: "string?"},
		"Regex.replace":     {params: "string, string, string", result: "string"},
		"Regex.replace_all": {params: "string, string, string", result: "string"},
		"Regexp.escape":     {params: "string", result: "string"},
		"Regexp.quote":      {params: "string", result: "string"},
		"Regexp.new":        {params: "string"},
		"Regexp.last_match": {result: "nil"},

		"Duration.build": {
			params:   "number",
			keywords: "days: number, hours: number, minutes: number, seconds: number, weeks: number",
			result:   "duration",
		},
		"Duration.parse": {params: "string", result: "duration"},

		"Time.new":    {keywords: "in: string?", result: "time"},
		"Time.local":  {result: "time"},
		"Time.mktime": {result: "time"},
		"Time.utc":    {result: "time"},
		"Time.gm":     {result: "time"},
		"Time.at":     {params: "_, _, symbol", keywords: "in: string?", result: "time"},
		"Time.now":    {keywords: "in: string?", result: "time"},
		"Time.parse":  {params: "string, string?", keywords: "in: string?", result: "time"},

		"Math.sqrt":  {params: "number", result: "float"},
		"Math.cbrt":  {params: "number", result: "float"},
		"Math.sin":   {params: "number", result: "float"},
		"Math.cos":   {params: "number", result: "float"},
		"Math.tan":   {params: "number", result: "float"},
		"Math.asin":  {params: "number", result: "float"},
		"Math.acos":  {params: "number", result: "float"},
		"Math.atan":  {params: "number", result: "float"},
		"Math.exp":   {params: "number", result: "float"},
		"Math.log":   {params: "number, number", result: "float"},
		"Math.log2":  {params: "number", result: "float"},
		"Math.log10": {params: "number", result: "float"},
		"Math.atan2": {params: "number, number", result: "float"},
		"Math.hypot": {params: "number, number", result: "float"},
	}

	got := typedBuiltinContracts(MustNewEngine(Config{}))
	for name, wantContract := range want {
		gotContract, ok := got[name]
		if !ok {
			t.Errorf("registry is missing the expected typed contract for %s", name)
			continue
		}
		if gotContract != wantContract {
			t.Errorf("typed contract for %s = %+v, want %+v", name, gotContract, wantContract)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("registry declares a typed contract for %s that this table does not pin", name)
		}
	}
}

func TestCoreBuiltinTypedArgumentWarnings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "conversion result contradicts string boundary",
			source: `
def takes_string(value: string)
  value
end

def run()
  takes_string(to_int("1"))
end
`,
			warning: "call to takes_string argument value expected string, got int",
		},
		{
			name:    "to_int rejects hash",
			source:  "def run()\n  to_int({})\nend\n",
			warning: "call to to_int argument 1 expected int | float | string, got",
		},
		{
			name:    "money rejects int literal",
			source:  "def run()\n  money(5)\nend\n",
			warning: "call to money argument 1 expected string, got int",
		},
		{
			name:    "sleep rejects symbol",
			source:  "def run()\n  sleep(:soon)\nend\n",
			warning: "call to sleep argument 1 expected number, got symbol",
		},
		{
			name:    "rand rejects float bound",
			source:  "def run()\n  rand(0.5)\nend\n",
			warning: "call to rand argument 1 expected int | range | nil, got float",
		},
		{
			name:    "Math.sqrt rejects string",
			source:  "def run()\n  Math.sqrt(\"x\")\nend\n",
			warning: "call to Math.sqrt argument 1 expected number, got string",
		},
		{
			name:    "Duration.build rejects string part",
			source:  "def run()\n  Duration.build(weeks: \"x\")\nend\n",
			warning: "call to Duration.build argument weeks expected number, got string",
		},
		{
			name:    "Time.parse rejects int input",
			source:  "def run()\n  Time.parse(123)\nend\n",
			warning: "call to Time.parse argument 1 expected string, got int",
		},
		{
			name:    "Regex.match result contradicts int boundary",
			source:  "def takes_int(value: int)\n  value\nend\n\ndef run()\n  takes_int(Regex.match(\"a\", \"abc\"))\nend\n",
			warning: "call to takes_int argument value expected int, got string?",
		},
		{
			name:    "now result contradicts time boundary",
			source:  "def takes_time(value: time)\n  value\nend\n\ndef run()\n  takes_time(now)\nend\n",
			warning: "call to takes_time argument value expected time, got string",
		},
		{
			name:    "uuid result flows through local",
			source:  "def takes_int(value: int)\n  value\nend\n\ndef run()\n  id = uuid\n  takes_int(id)\nend\n",
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name:    "Time.now result contradicts string boundary",
			source:  "def takes_string(value: string)\n  value\nend\n\ndef run()\n  takes_string(Time.now())\nend\n",
			warning: "call to takes_string argument value expected string, got time",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScriptDefault(t, tc.source), tc.warning)
		})
	}
}

func TestCoreBuiltinTypedContractsStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{name: "to_int accepts int", source: "def run()\n  to_int(1)\nend\n"},
		{name: "to_int accepts float", source: "def run()\n  to_int(1.0)\nend\n"},
		{name: "to_int accepts string", source: "def run()\n  to_int(\"42\")\nend\n"},
		{name: "to_int accepts unknown", source: "def run(v)\n  to_int(v)\nend\n"},
		{name: "rand accepts no bound", source: "def run()\n  rand\nend\n"},
		{name: "rand accepts nil bound", source: "def run()\n  rand(nil)\nend\n"},
		{name: "rand accepts int bound", source: "def run()\n  rand(6)\nend\n"},
		{name: "rand accepts range bound", source: "def run()\n  rand(1..10)\nend\n"},
		{name: "rand result satisfies number boundary", source: "def takes_number(value: number)\n  value\nend\n\ndef run()\n  takes_number(rand(6))\nend\n"},
		{name: "srand accepts nothing", source: "def run()\n  srand\nend\n"},
		{name: "srand accepts int seed", source: "def run()\n  srand(7)\nend\n"},
		{name: "money_cents accepts float cents", source: "def run()\n  money_cents(100.0, \"usd\")\nend\n"},
		{name: "money_cents accepts int cents", source: "def run()\n  money_cents(100, \"usd\")\nend\n"},
		{name: "sleep accepts int and float", source: "def run()\n  sleep(1)\n  sleep(0.25)\nend\n"},
		{name: "Math.sqrt accepts int", source: "def run()\n  Math.sqrt(9)\nend\n"},
		{name: "Duration.build accepts named parts", source: "def run()\n  Duration.build(hours: 2, minutes: 30)\nend\n"},
		{name: "Duration.build accepts seconds", source: "def run()\n  Duration.build(90)\nend\n"},
		{name: "Time.parse accepts layout and zone", source: "def run()\n  Time.parse(\"2024-01-01\", \"2006-01-02\", in: \"UTC\")\nend\n"},
		{name: "Time.at accepts symbol unit", source: "def run()\n  Time.at(1700000000, 500, :millisecond)\nend\n"},
		{name: "JSON parse result stays unknown", source: "def takes_int(value: int)\n  value\nend\n\ndef run()\n  takes_int(JSON.parse(\"1\"))\nend\n"},
		{name: "format result satisfies string boundary", source: "def takes_string(value: string)\n  value\nend\n\ndef run()\n  takes_string(format(\"%d\", 1))\nend\n"},
		{name: "Hash.new result satisfies hash boundary", source: "def takes_hash(value: hash)\n  value\nend\n\ndef run()\n  takes_hash(Hash.new)\nend\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, tc.source))
		})
	}
}

func TestCoreBuiltinTypedContractsHostOverride(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	engine.RegisterBuiltin("to_int", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		return NewString("host"), nil
	})
	script := compileScriptWithEngine(t, engine, `
def run()
  to_int({})
end
`)
	requireNoCheckWarnings(t, script)
}
