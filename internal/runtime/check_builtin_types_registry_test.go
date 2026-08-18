package runtime

import (
	"testing"
)

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
		{name: "srand accepts nothing", source: "def run()\n  srand()\nend\n"},
		{name: "srand accepts int seed", source: "def run()\n  srand(7)\nend\n"},
		{name: "money_cents accepts float cents", source: "def run()\n  money_cents(100.0, \"usd\")\nend\n"},
		{name: "money_cents accepts int cents", source: "def run()\n  money_cents(100, \"usd\")\nend\n"},
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
