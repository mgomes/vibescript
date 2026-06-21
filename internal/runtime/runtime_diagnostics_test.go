package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCompileMalformedCallTargetDoesNotPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("compile panicked: %v", r)
		}
	}()

	_ = compileScriptErrorDefault(t, `be(in (000000000`)
}

func TestParseErrorIncludesCodeFrameAndMissingValueMessage(t *testing.T) {
	t.Parallel()
	err := compileScriptErrorDefault(t, "def broken()\n  {foo: }\nend\n")
	msg := err.Error()
	if !strings.Contains(msg, "missing value for hash key foo") {
		t.Fatalf("expected missing hash value parse error, got: %s", msg)
	}
	if !strings.Contains(msg, "--> line 2, column") {
		t.Fatalf("expected codeframe line marker, got: %s", msg)
	}
	if !strings.Contains(msg, "{foo: }") {
		t.Fatalf("expected source line in codeframe, got: %s", msg)
	}
}

func TestParseErrorIncludesBlockParameterHint(t *testing.T) {
	t.Parallel()
	err := compileScriptErrorDefault(t, "def broken()\n  [1].each do |a,|\n    a\n  end\nend\n")
	msg := err.Error()
	if !strings.Contains(msg, "trailing comma in block parameter list") {
		t.Fatalf("expected trailing comma hint, got: %s", msg)
	}
}

func TestRuntimeErrorStackTrace(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inner()
      assert false, "boom"
    end

    def middle()
      inner()
    end

    def outer()
      middle()
    end
    `)

	_, err := script.Call(context.Background(), "outer", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if !strings.Contains(rtErr.Message, "boom") {
		t.Fatalf("message mismatch: %v", rtErr.Message)
	}
	if len(rtErr.Frames) < 4 {
		t.Fatalf("expected at least 4 frames, got %d", len(rtErr.Frames))
	}
	wantFrames := []string{"inner", "inner", "middle", "outer"}
	for i, want := range wantFrames {
		if rtErr.Frames[i].Function != want {
			t.Fatalf("frame %d: expected %s, got %s", i, want, rtErr.Frames[i].Function)
		}
	}
	if rtErr.CodeFrame == "" {
		t.Fatalf("expected runtime codeframe to be present")
	}
	if !strings.Contains(rtErr.Error(), "--> line") {
		t.Fatalf("expected formatted runtime error to include codeframe marker")
	}
}

func TestRuntimeErrorCondensesDeepStackRendering(t *testing.T) {
	t.Parallel()
	script := compileScriptWithConfig(t, Config{RecursionLimit: 128}, `
    def recurse(n)
      if n <= 0
        1 / 0
      end
      recurse(n - 1)
    end

    def run()
      recurse(40)
    end
    `)

	_, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if len(rtErr.Frames) <= 16 {
		t.Fatalf("expected deep frame set, got %d", len(rtErr.Frames))
	}
	rendered := rtErr.Error()
	if !strings.Contains(rendered, "frames omitted") {
		t.Fatalf("expected deep stack output to include omitted-frame marker: %s", rendered)
	}
	if !strings.Contains(rendered, "at recurse") {
		t.Fatalf("expected deep stack output to include recurse frames: %s", rendered)
	}
}

func TestMethodErrorHandling(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		errMsg string
	}{
		{
			name:   "string.split with non-string separator",
			script: `def run() "hello".split(123) end`,
			errMsg: "separator must be string",
		},
		{
			name:   "array.flatten with negative depth",
			script: `def run() [[1, 2]].flatten(-1) end`,
			errMsg: "must be non-negative",
		},
		{
			name:   "array.join with non-string separator",
			script: `def run() [1, 2, 3].join(123) end`,
			errMsg: "separator must be string",
		},
		{
			name:   "array.find without block",
			script: `def run() [1, 2, 3].find end`,
			errMsg: "array.find requires a block",
		},
		{
			name:   "array.find_index with argument",
			script: `def run() [1, 2, 3].find_index(1) end`,
			errMsg: "array.find_index does not take arguments",
		},
		{
			name:   "array.index with invalid offset",
			script: `def run() [1, 2, 3].index(2, -1) end`,
			errMsg: "offset must be non-negative integer",
		},
		{
			name:   "array.rindex with too many args",
			script: `def run() [1, 2, 3].rindex(2, 1, 0) end`,
			errMsg: "expects value and optional offset",
		},
		{
			name:   "array.rindex validates offset on empty array",
			script: `def run() [].rindex(1, -1) end`,
			errMsg: "offset must be non-negative integer",
		},
		{
			name:   "array.fetch with missing index",
			script: `def run [1, 2, 3].fetch end`,
			errMsg: "expects index and optional default",
		},
		{
			name:   "array.fetch with non-integer index",
			script: `def run [1, 2, 3].fetch("1") end`,
			errMsg: "index must be integer",
		},
		{
			name:   "array.fetch with fractional float index",
			script: `def run [1, 2, 3].fetch(1.5) end`,
			errMsg: "index must be integer",
		},
		{
			name: "array.count with argument and block",
			script: `def run()
  [1, 1].count(1) do |v|
    v == 1
  end
end`,
			errMsg: "does not accept both argument and block",
		},
		{
			name:   "array.any? with argument",
			script: `def run() [1].any?(1) end`,
			errMsg: "array.any? does not take arguments",
		},
		{
			name:   "array.sort with incomparable values",
			script: `def run() [1, "a"].sort end`,
			errMsg: "values are not comparable",
		},
		{
			name: "array.sort with non-numeric comparator",
			script: `def run()
  [2, 1].sort do |a, b|
    a > b
  end
end`,
			errMsg: "block must return numeric comparator",
		},
		{
			name:   "array.sort_by without block",
			script: `def run() [1, 2].sort_by end`,
			errMsg: "array.sort_by requires a block",
		},
		{
			name: "array.sort_by with incomparable keys",
			script: `def run()
  [1, 2].sort_by do |v|
    if v == 1
      "one"
    else
      2
    end
  end
end`,
			errMsg: "block values are not comparable",
		},
		{
			name:   "array.partition without block",
			script: `def run() [1, 2].partition end`,
			errMsg: "array.partition requires a block",
		},
		{
			name: "array.group_by with unsupported group key",
			script: `def run()
  [1, 2].group_by do |v|
    v
  end
end`,
			errMsg: "block must return symbol or string",
		},
		{
			name:   "array.tally with unsupported values",
			script: `def run() [1, 2].tally end`,
			errMsg: "values must be symbol or string",
		},
		{
			name:   "array.tally with argument",
			script: `def run() ["a"].tally(1) end`,
			errMsg: "array.tally does not take arguments",
		},
		{
			name:   "string unknown method",
			script: `def run() "hello".unknown_method() end`,
			errMsg: "unknown string method",
		},
		{
			name:   "string.empty? with argument",
			script: `def run() "hello".empty?(1) end`,
			errMsg: "string.empty? does not take arguments",
		},
		{
			name:   "string.start_with? with non-string prefix",
			script: `def run() "hello".start_with?(123) end`,
			errMsg: "prefix must be string",
		},
		{
			name:   "string.end_with? with missing suffix",
			script: `def run() "hello".end_with? end`,
			errMsg: "expects at least one suffix",
		},
		{
			name:   "string.lstrip with argument",
			script: `def run() " hello".lstrip(1) end`,
			errMsg: "string.lstrip does not take arguments",
		},
		{
			name:   "string.chomp with non-string separator",
			script: `def run() "line\n".chomp(123) end`,
			errMsg: "separator must be string",
		},
		{
			name:   "string.delete_prefix with non-string prefix",
			script: `def run() "hello".delete_prefix(123) end`,
			errMsg: "prefix must be string",
		},
		{
			name:   "string.delete_suffix with missing suffix",
			script: `def run() "hello".delete_suffix end`,
			errMsg: "expects exactly one suffix",
		},
		{
			name:   "string.include? with non-string substring",
			script: `def run() "hello".include?(123) end`,
			errMsg: "substring must be string",
		},
		{
			name:   "string.index with invalid offset",
			script: `def run() "hello".index("e", -1) end`,
			errMsg: "offset must be non-negative integer",
		},
		{
			name:   "string.rindex with too many args",
			script: `def run() "hello".rindex("l", 0, 1) end`,
			errMsg: "expects substring and optional offset",
		},
		{
			name:   "string.slice with non-int length",
			script: `def run() "hello".slice(1, "x") end`,
			errMsg: "length must be integer",
		},
		{
			name:   "string.capitalize with argument",
			script: `def run() "hello".capitalize(1) end`,
			errMsg: "string.capitalize does not take arguments",
		},
		{
			name:   "string.sub with non-string replacement",
			script: `def run() "hello".sub("l", 1) end`,
			errMsg: "replacement must be string",
		},
		{
			name:   "string.gsub with missing argument",
			script: `def run() "hello".gsub("l") end`,
			errMsg: "expects pattern and replacement",
		},
		{
			name:   "string.match with invalid regex",
			script: `def run() "hello".match("[") end`,
			errMsg: "invalid regex",
		},
		{
			name:   "string.scan with non-string pattern",
			script: `def run() "hello".scan(1) end`,
			errMsg: "pattern must be string",
		},
		{
			name:   "string.match with keyword argument",
			script: `def run() "hello".match("h", foo: true) end`,
			errMsg: "does not accept keyword arguments",
		},
		{
			name:   "string.scan with keyword argument",
			script: `def run() "hello".scan("h", foo: true) end`,
			errMsg: "does not accept keyword arguments",
		},
		{
			name:   "string.ord on empty string",
			script: `def run() "".ord end`,
			errMsg: "requires non-empty string",
		},
		{
			name:   "string.sub with non-bool regex keyword",
			script: `def run() "ID-12".sub("ID-[0-9]+", "X", regex: 1) end`,
			errMsg: "regex keyword must be bool",
		},
		{
			name:   "string.gsub with unknown keyword",
			script: `def run() "ID-12".gsub("ID-[0-9]+", "X", foo: true) end`,
			errMsg: "supports only regex keyword",
		},
		{
			name:   "string.concat with non-string argument",
			script: `def run() "hello".concat(1) end`,
			errMsg: "expects string arguments",
		},
		{
			name:   "string.replace with non-string replacement",
			script: `def run() "hello".replace(1) end`,
			errMsg: "replacement must be string",
		},
		{
			name:   "string.strip! with argument",
			script: `def run() "hello".strip!(1) end`,
			errMsg: "string.strip! does not take arguments",
		},
		{
			name:   "string.squish with argument",
			script: `def run() "hello".squish(1) end`,
			errMsg: "string.squish does not take arguments",
		},
		{
			name:   "string.gsub! with missing argument",
			script: `def run() "hello".gsub!("l") end`,
			errMsg: "expects pattern and replacement",
		},
		{
			name:   "string.template without context",
			script: `def run() "hello {{name}}".template end`,
			errMsg: "expects exactly one context hash",
		},
		{
			name:   "string.template with non-hash context",
			script: `def run() "hello {{name}}".template(1) end`,
			errMsg: "context must be hash",
		},
		{
			name:   "string.template with unknown keyword",
			script: `def run() "hello {{name}}".template({}, foo: true) end`,
			errMsg: "supports only strict keyword",
		},
		{
			name:   "string.template with non-bool strict keyword",
			script: `def run() "hello {{name}}".template({}, strict: 1) end`,
			errMsg: "strict keyword must be bool",
		},
		{
			name:   "string.template strict missing key",
			script: `def run() "hello {{name}}".template({}, strict: true) end`,
			errMsg: "missing placeholder name",
		},
		{
			name:   "string.template with non-scalar value",
			script: `def run() "hello {{items}}".template({ items: [1, 2] }) end`,
			errMsg: "placeholder items value must be scalar",
		},
		{
			name:   "hash.size with argument",
			script: `def run() {a: 1}.size(1) end`,
			errMsg: "hash.size does not take arguments",
		},
		{
			name:   "hash.fetch with too many args",
			script: `def run() {a: 1}.fetch(:a, 1, 2) end`,
			errMsg: "expects key and optional default",
		},
		{
			name:   "hash.dig without keys",
			script: `def run() {a: 1}.dig end`,
			errMsg: "expects at least one key",
		},
		{
			name:   "hash.dig with unsupported key type",
			script: `def run() {a: 1}.dig(1) end`,
			errMsg: "path keys must be symbol or string",
		},
		{
			name:   "hash.each without block",
			script: `def run() {a: 1}.each end`,
			errMsg: "hash.each requires a block",
		},
		{
			name:   "hash.select without block",
			script: `def run() {a: 1}.select end`,
			errMsg: "hash.select requires a block",
		},
		{
			name:   "hash.slice with unsupported key type",
			script: `def run() {a: 1}.slice(1) end`,
			errMsg: "keys must be symbol or string",
		},
		{
			name: "hash.transform_keys invalid return type",
			script: `def run()
  {a: 1}.transform_keys do |k|
    1
  end
end`,
			errMsg: "block must return symbol or string",
		},
		{
			name:   "hash unknown method",
			script: `def run() {a: 1}.unknown_method() end`,
			errMsg: "unknown hash method",
		},
		{
			name:   "array unknown method",
			script: `def run() [1, 2].unknown_method() end`,
			errMsg: "unknown array method",
		},
		{
			name:   "Time.parse unknown keyword",
			script: `def run() Time.parse("2024-01-01T00:00:00Z", foo: "bar") end`,
			errMsg: "unknown keyword",
		},
		{
			name:   "Time.parse layout must be string",
			script: `def run() Time.parse("2024-01-01T00:00:00Z", 123) end`,
			errMsg: "layout must be string",
		},
		{
			name:   "int.clamp with wrong arity",
			script: `def run() 5.clamp(1) end`,
			errMsg: "expects min and max",
		},
		{
			name:   "int.clamp with inverted bounds",
			script: `def run() 5.clamp(10, 1) end`,
			errMsg: "min must be <= max",
		},
		{
			name:   "float.round with float precision",
			script: `def run() 1.5.round(1.0) end`,
			errMsg: "precision must be an Integer",
		},
		{
			name:   "float.clamp with non-numeric bounds",
			script: `def run() 1.5.clamp("a", 2.0) end`,
			errMsg: "expects numeric min and max",
		},
		{
			name:   "float.round overflow",
			script: `def run() 100000000000000000000.0.round end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.floor overflow",
			script: `def run() 100000000000000000000.0.floor end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.ceil overflow",
			script: `def run() 100000000000000000000.0.ceil end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.round int64 boundary overflow",
			script: `def run() 9223372036854775808.0.round end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.floor int64 boundary overflow",
			script: `def run() 9223372036854775808.0.floor end`,
			errMsg: "out of int64 range",
		},
		{
			name:   "float.ceil int64 boundary overflow",
			script: `def run() 9223372036854775808.0.ceil end`,
			errMsg: "out of int64 range",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.script)
			_, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err == nil {
				t.Fatalf("expected error containing %q", tt.errMsg)
			}
			requireErrorContains(t, err, tt.errMsg)
		})
	}
}

func TestRuntimeErrorFromBuiltin(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def divide(a, b)
      a / b
    end

    def calculate()
      divide(10, 0)
    end
    `)

	_, err := script.Call(context.Background(), "calculate", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error for division by zero")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}
	if !strings.Contains(rtErr.Message, "division by zero") {
		t.Fatalf("expected division by zero error, got: %v", rtErr.Message)
	}

	// Should have stack frames showing where the error occurred
	if len(rtErr.Frames) < 2 {
		t.Fatalf("expected at least 2 frames, got %d", len(rtErr.Frames))
	}

	// Error occurred in divide function
	if rtErr.Frames[0].Function != "divide" {
		t.Fatalf("expected divide frame first, got %s", rtErr.Frames[0].Function)
	}
}

func TestRuntimeErrorNoCallStack(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def test()
      1 / 0
    end
    `)

	_, err := script.Call(context.Background(), "test", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected runtime error")
	}

	var rtErr *RuntimeError
	if !errors.As(err, &rtErr) {
		t.Fatalf("expected RuntimeError, got %T", err)
	}

	// Should have at least the error location
	if len(rtErr.Frames) == 0 {
		t.Fatalf("expected at least one frame")
	}
}
