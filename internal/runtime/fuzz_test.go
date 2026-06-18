package runtime

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mgomes/vibescript/internal/parser"
	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
)

const (
	fuzzMaxSourceBytes = 4096
	fuzzMaxTextBytes   = 4096
)

func FuzzLexerTokenStreamTerminates(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("def run() 1 end"),
		[]byte("class Box\n  property value\nend"),
		[]byte("\"unterminated"),
		[]byte("@ @@ : :: .. =>"),
		[]byte("# comment only\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		source := string(limitFuzzBytes(raw, fuzzMaxSourceBytes))
		lexer := parser.NewLexer(source)
		last := Position{Line: 1, Column: 0}
		tokenBudget := max(len(source)+2, 32)

		for range tokenBudget {
			tok := lexer.NextToken()
			if tok.Pos.Line <= 0 {
				t.Fatalf("lexer.NextToken(%q) returned token %s with invalid line %d", fuzzSnippet(source), tok.Type, tok.Pos.Line)
			}
			if tok.Type != tokenEOF && tok.Pos.Column <= 0 {
				t.Fatalf("lexer.NextToken(%q) returned token %s with invalid column %d", fuzzSnippet(source), tok.Type, tok.Pos.Column)
			}
			if positionBefore(tok.Pos, last) {
				t.Fatalf("lexer.NextToken(%q) returned token %s at %d:%d after %d:%d", fuzzSnippet(source), tok.Type, tok.Pos.Line, tok.Pos.Column, last.Line, last.Column)
			}
			if tok.Type == tokenEOF {
				return
			}
			last = tok.Pos
		}

		t.Fatalf("lexer.NextToken(%q) did not reach EOF within %d tokens", fuzzSnippet(source), tokenBudget)
	})
}

func FuzzParserSuccessfulProgramsHaveCompleteAST(f *testing.F) {
	for _, seed := range []string{
		"",
		"def run()\n  1\nend",
		"def run(value)\n  if value\n    value\n  else\n    nil\n  end\nend",
		"class Point\n  property x, y\n  def initialize(@x, @y)\n  end\nend",
		"enum Status\n  Draft\n  Published\nend",
		"def run(values)\n  values.map do |value|\n    value * 2\n  end\nend",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		source = limitFuzzString(source, fuzzMaxSourceBytes)
		program, parseErrors := parser.Parse(source)
		if len(parseErrors) > 0 {
			return
		}

		if err := validateFuzzProgram(program); err != nil {
			t.Fatalf("ParseProgram(%q) produced invalid AST: %v", fuzzSnippet(source), err)
		}

		cloned := &Program{Statements: cloneStatements(program.Statements)}
		if err := validateFuzzProgram(cloned); err != nil {
			t.Fatalf("cloneStatements(ParseProgram(%q)) produced invalid AST: %v", fuzzSnippet(source), err)
		}
		if !reflect.DeepEqual(program, cloned) {
			t.Fatalf("cloneStatements(ParseProgram(%q)) changed the AST", fuzzSnippet(source))
		}
	})
}

func FuzzCompileScriptDoesNotPanic(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("def run() 1 end"))
	f.Add([]byte("def broken("))
	f.Add([]byte("begin\n  raise(\"boom\")\nrescue\n  1\nend"))
	f.Add([]byte("require(\"../path\")"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		raw = limitFuzzBytes(raw, fuzzMaxSourceBytes)
		engine := MustNewEngine(Config{})
		_, _ = engine.Compile(string(raw))
	})
}

func FuzzGeneratedScriptSemantics(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		{1, 2, 3, 4, 5, 6, 7, 8, 9},
		[]byte("collections and classes"),
		{255, 128, 64, 32, 16, 8, 4, 2, 1},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		data := newFuzzData(limitFuzzBytes(raw, 128))
		source, input, want := generatedScriptCase(data)

		engine := MustNewEngine(Config{
			StepQuota:        10000,
			MemoryQuotaBytes: 128 * 1024,
		})
		script, err := engine.Compile(source)
		if err != nil {
			t.Fatalf("Compile(generated script) failed: %v\n%s", err, source)
		}

		got, err := script.Call(context.Background(), "run", []Value{NewInt(input)}, CallOptions{})
		if err != nil {
			t.Fatalf("Call(run, %d) failed: %v\n%s", input, err, source)
		}
		if !got.Equal(want) {
			t.Fatalf("Call(run, %d) = %s, want %s\n%s", input, got.String(), want.String(), source)
		}
	})
}

func FuzzRuntimeEdgeCasesDoNotPanic(f *testing.F) {
	script := compileScriptDefault(f, `
def run(text, pattern)
  stable_groups = text.split("").group_by_stable do |char|
    if char == ""
      :empty
    else
      :present
    end
  end

  {
    match: Regex.match(pattern, text),
    replace_all: Regex.replace_all(text, pattern, "x"),
    json: JSON.stringify({ text: text }),
    template: "value={{text}}".template({ text: text }),
    chunks: text.split("").chunk(4),
    windows: text.split("").window(2),
    stable_groups: stable_groups
  }
end
`)

	f.Add("", "a*")
	f.Add("ID-12 ID-34", "ID-[0-9]+")
	f.Add(strings.Repeat("a", 256), "(")
	f.Add("  hello \n world  ", "\\s+")
	f.Add("{}", ".*")

	f.Fuzz(func(t *testing.T, text, pattern string) {
		text = limitFuzzString(text, fuzzMaxTextBytes)
		if len(pattern) > 1024 {
			pattern = pattern[:1024]
		}
		_, _ = script.Call(context.Background(), "run", []Value{
			NewString(text),
			NewString(pattern),
		}, CallOptions{})
	})
}

func FuzzJSONValueRoundTripPreservesStructure(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("null bool int string array hash"),
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		{255, 254, 253, 252, 251, 250},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		data := newFuzzData(limitFuzzBytes(raw, 256))
		value := data.jsonValue(4)

		encoded, err := builtinJSONStringify(nil, NewNil(), []Value{value}, nil, NewNil())
		if err != nil {
			t.Fatalf("JSON.stringify(%s) failed: %v", value.String(), err)
		}
		decoded, err := builtinJSONParse(nil, NewNil(), []Value{encoded}, nil, NewNil())
		if err != nil {
			t.Fatalf("JSON.parse(JSON.stringify(%s)) failed: %v", value.String(), err)
		}
		if !decoded.Equal(value) {
			t.Fatalf("JSON.parse(JSON.stringify(%s)) = %s, want %s", value.String(), decoded.String(), value.String())
		}
	})
}

func FuzzValueOperationsPreserveInvariants(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("scalar"),
		[]byte("nested arrays and hashes"),
		{0, 17, 34, 51, 68, 85, 102, 119, 136},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		data := newFuzzData(limitFuzzBytes(raw, 256))
		value := data.value(4)

		if !value.Equal(value) {
			t.Fatalf("Value.Equal(%s, itself) = false, want true", value.String())
		}
		if got := value.Kind().String(); got == "" {
			t.Fatalf("Value.Kind().String() for %s = empty string, want non-empty", value.String())
		}

		_ = value.Truthy()
		_ = value.String()

		deepClone := deepCloneValue(value)
		if !deepClone.Equal(value) {
			t.Fatalf("deepCloneValue(%s) = %s, want equal value", value.String(), deepClone.String())
		}

		hostClone := cloneValueForHost(value)
		if !hostClone.Equal(value) {
			t.Fatalf("cloneValueForHost(%s) = %s, want equal value", value.String(), hostClone.String())
		}

		ty := data.typeExpr(3)
		_ = checkValueType(value, ty)
	})
}

func FuzzModuleRequestNormalization(f *testing.F) {
	for _, seed := range []string{
		"",
		"helper",
		"./relative/helper",
		"../parent/helper",
		"shared\\math",
		"/absolute/path",
		"../../escape",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		name = limitFuzzString(name, 1024)
		request, err := parseModuleRequest(name)
		if err != nil {
			return
		}
		if request.raw != name {
			t.Fatalf("parseModuleRequest(%q).raw = %q, want original input", fuzzSnippet(name), request.raw)
		}
		if request.normalized == "" || request.normalized == "." {
			t.Fatalf("parseModuleRequest(%q).normalized = %q, want module path", fuzzSnippet(name), request.normalized)
		}
		if filepath.IsAbs(request.normalized) {
			t.Fatalf("parseModuleRequest(%q).normalized = %q, want relative path", fuzzSnippet(name), request.normalized)
		}
		if filepath.Clean(request.normalized) != request.normalized {
			t.Fatalf("parseModuleRequest(%q).normalized = %q, want clean path", fuzzSnippet(name), request.normalized)
		}
		if !request.explicitRelative && containsPathTraversal(request.normalized) {
			t.Fatalf("parseModuleRequest(%q) allowed non-explicit traversal path %q", fuzzSnippet(name), request.normalized)
		}

		if strings.TrimSpace(request.normalized) == request.normalized {
			reparsed, err := parseModuleRequest(request.normalized)
			if err != nil {
				t.Fatalf("parseModuleRequest(%q) succeeded, but reparsing normalized path %q failed: %v", fuzzSnippet(name), request.normalized, err)
			}
			if reparsed.normalized != request.normalized {
				t.Fatalf("parseModuleRequest(%q) normalized to %q, reparsed normalized path to %q", fuzzSnippet(name), request.normalized, reparsed.normalized)
			}
		}
	})
}

func FuzzModuleAliasValidation(f *testing.F) {
	f.Add("", 0)
	f.Add("helpers", 1)
	f.Add(" if ", 2)
	f.Add("123bad", 3)
	f.Add("héllo", 4)
	f.Add("helper_name", 5)

	f.Fuzz(func(t *testing.T, raw string, selector int) {
		raw = limitFuzzString(raw, 256)

		aliasValue := fuzzModuleAliasValue(raw, selector)
		alias, err := parseRequireAlias(map[string]Value{"as": aliasValue})
		if err == nil {
			if !isValidModuleAlias(alias) {
				t.Fatalf("parseRequireAlias(as: %s) = %q, want valid alias", aliasValue.String(), alias)
			}
			if alias != strings.TrimSpace(aliasValue.String()) {
				t.Fatalf("parseRequireAlias(as: %s) = %q, want trimmed value", aliasValue.String(), alias)
			}

			module := NewObject(map[string]Value{"value": NewInt(1)})
			root := newEnv(nil)
			if err := validateRequireAliasBinding(root, alias, module); err != nil {
				t.Fatalf("validateRequireAliasBinding(empty root, %q) failed: %v", alias, err)
			}

			conflicting := newEnv(nil)
			conflicting.Define(alias, NewString("existing"))
			if err := validateRequireAliasBinding(conflicting, alias, module); err == nil {
				t.Fatalf("validateRequireAliasBinding(conflicting root, %q) = nil, want conflict", alias)
			}
		}

		_, _ = parseRequireAlias(nil)
		_, _ = parseRequireAlias(map[string]Value{raw: aliasValue})
		_, _ = parseRequireAlias(map[string]Value{
			"as": aliasValue,
			raw:  NewString("extra"),
		})
	})
}

func FuzzScalarInputParsersAndConversions(f *testing.F) {
	f.Add("", "", float64(0))
	f.Add("42", "2006-01-02", float64(42))
	f.Add("12.34 USD", "2006-01-02T15:04:05Z07:00", float64(12.5))
	f.Add("usd", "", float64(0))
	f.Add("PT1H30M", "", float64(-3))
	f.Add("2024-01-02T03:04:05Z", time.RFC3339, float64(1.25))
	f.Add("-92233720368547758.08 USD", "", float64(math.NaN()))

	f.Fuzz(func(t *testing.T, text, layout string, floatInput float64) {
		text = limitFuzzString(text, 512)
		layout = limitFuzzString(layout, 256)

		if money, err := parseMoneyLiteral(text); err == nil {
			reparsed, reparseErr := parseMoneyLiteral(money.String())
			if reparseErr != nil {
				t.Fatalf("parseMoneyLiteral(%q) succeeded as %s, but parseMoneyLiteral(String()) failed: %v", text, money.String(), reparseErr)
			}
			if reparsed != money {
				t.Fatalf("parseMoneyLiteral(%q).String() round trip = %#v, want %#v", text, reparsed, money)
			}
		}

		if duration, err := parseDurationString(text); err == nil {
			reparsed, reparseErr := parseDurationString(duration.ISO8601())
			if reparseErr != nil {
				t.Fatalf("parseDurationString(%q) succeeded as %s, but parseDurationString(iso8601()) failed: %v", text, duration.ISO8601(), reparseErr)
			}
			if reparsed != duration {
				t.Fatalf("parseDurationString(%q).iso8601() round trip = %#v, want %#v", text, reparsed, duration)
			}
		}

		loc, locErr := parseLocationString(text)
		if locErr == nil && text == "" && loc != nil {
			t.Fatalf("parseLocationString(\"\") = %v, want nil location", loc)
		}

		hasLayout := strings.TrimSpace(layout) != ""
		if parsed, err := parseTimeString(text, layout, hasLayout, nil); err == nil {
			if year := parsed.Year(); year >= 1 && year <= 9999 {
				roundTrip, reparseErr := parseTimeString(parsed.Format(time.RFC3339Nano), "", false, nil)
				if reparseErr != nil {
					t.Fatalf("parseTimeString(%q, %q) succeeded as %s, but RFC3339Nano reparse failed: %v", text, layout, parsed.Format(time.RFC3339Nano), reparseErr)
				}
				if !roundTrip.Equal(parsed) {
					t.Fatalf("parseTimeString(%q, %q) RFC3339Nano round trip = %s, want %s", text, layout, roundTrip.Format(time.RFC3339Nano), parsed.Format(time.RFC3339Nano))
				}
			}
		}
		if locErr == nil {
			_, _ = parseTimeString(text, layout, hasLayout, loc)
		}

		if got, err := builtinToInt(nil, NewNil(), []Value{NewString(text)}, nil, NewNil()); err == nil {
			want, parseErr := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
			if parseErr != nil {
				t.Fatalf("to_int(%q) succeeded with %s, but strconv.ParseInt failed: %v", text, got.String(), parseErr)
			}
			if got.Kind() != KindInt || got.Int() != want {
				t.Fatalf("to_int(%q) = %s, want %d", text, got.String(), want)
			}
		}
		if got, err := builtinToFloat(nil, NewNil(), []Value{NewString(text)}, nil, NewNil()); err == nil {
			if got.Kind() != KindFloat {
				t.Fatalf("to_float(%q) kind = %s, want float", text, got.Kind())
			}
			if math.IsNaN(got.Float()) || math.IsInf(got.Float(), 0) {
				t.Fatalf("to_float(%q) = %v, want finite float", text, got.Float())
			}
		}
		if got, err := builtinToInt(nil, NewNil(), []Value{NewFloat(floatInput)}, nil, NewNil()); err == nil && got.Kind() != KindInt {
			t.Fatalf("to_int(%v) kind = %s, want int", floatInput, got.Kind())
		}
		if got, err := builtinToFloat(nil, NewNil(), []Value{NewFloat(floatInput)}, nil, NewNil()); err == nil && got.Kind() != KindFloat {
			t.Fatalf("to_float(%v) kind = %s, want float", floatInput, got.Kind())
		}

		for _, cents := range []int64{0, int64(1<<63 - 1), int64(-1 << 63)} {
			if money, err := newMoneyFromCents(cents, text); err == nil {
				currency := money.Currency()
				if len(currency) != 3 {
					t.Fatalf("newMoneyFromCents(%d, %q).Currency() = %q, want 3-byte currency", cents, text, currency)
				}
				for i := range 3 {
					if currency[i] < 'A' || currency[i] > 'Z' {
						t.Fatalf("newMoneyFromCents(%d, %q).Currency() = %q, want uppercase ASCII currency", cents, text, currency)
					}
				}
				reparsed, reparseErr := parseMoneyLiteral(money.String())
				if reparseErr != nil {
					t.Fatalf("newMoneyFromCents(%d, %q) formatted as %q, but parseMoneyLiteral failed: %v", cents, text, money.String(), reparseErr)
				}
				if reparsed != money {
					t.Fatalf("newMoneyFromCents(%d, %q).String() round trip = %#v, want %#v", cents, text, reparsed, money)
				}
			}
		}
	})
}

func FuzzModulePolicyValidation(f *testing.F) {
	f.Add("", "", "allow")
	f.Add("*", "helper.vibe", "allow")
	f.Add("shared/*", "shared/math.vibe", "deny")
	f.Add("[", "helper", "allow")
	f.Add("./nested\\*.vibe,admin/*", "nested/tool.vibe", "")

	f.Fuzz(func(t *testing.T, rawPatterns, rawModule, label string) {
		rawPatterns = limitFuzzString(rawPatterns, 512)
		rawModule = limitFuzzString(rawModule, 512)
		label = limitFuzzString(label, 64)
		patterns := fuzzPolicyPatterns(rawPatterns)

		for _, pattern := range patterns {
			normalized := normalizeModulePolicyPattern(pattern)
			if normalized != "" && normalizeModulePolicyPattern(normalized) != normalized {
				t.Fatalf("normalizeModulePolicyPattern(%q) = %q, want idempotent normalization", pattern, normalized)
			}
		}
		module := normalizeModulePolicyModuleName(rawModule)
		if module != "" && normalizeModulePolicyModuleName(module) != module {
			t.Fatalf("normalizeModulePolicyModuleName(%q) = %q, want idempotent normalization", rawModule, module)
		}

		err := validateModulePolicyPatterns(patterns, label)
		if err != nil {
			return
		}

		matches := modulePolicyAnyMatch(patterns, module)
		allowEngine, err := NewEngine(Config{ModuleAllowList: patterns})
		if err != nil {
			t.Fatalf("NewEngine(ModuleAllowList: %v) failed after validation: %v", patterns, err)
		}
		allowErr := allowEngine.enforceModulePolicy(rawModule)
		// With no allow-list configured, all modules (including empty-
		// normalized ones) are allowed. Otherwise the module must
		// normalize non-empty and match a pattern.
		wantAllow := len(patterns) == 0 || (module != "" && matches)
		if wantAllow && allowErr != nil {
			t.Fatalf("allow-list %v rejected expected-allowed module %q: %v", patterns, rawModule, allowErr)
		}
		if !wantAllow && allowErr == nil {
			t.Fatalf("allow-list %v accepted expected-denied module %q", patterns, rawModule)
		}

		denyEngine, err := NewEngine(Config{ModuleDenyList: patterns})
		if err != nil {
			t.Fatalf("NewEngine(ModuleDenyList: %v) failed after validation: %v", patterns, err)
		}
		denyErr := denyEngine.enforceModulePolicy(rawModule)
		// With a deny-list configured, empty-normalized modules are
		// rejected as invalid (no policy bypass) and matching modules
		// are denied. Everything else passes.
		wantDeny := len(patterns) > 0 && (module == "" || matches)
		if wantDeny && denyErr == nil {
			t.Fatalf("deny-list %v accepted expected-denied module %q", patterns, rawModule)
		}
		if !wantDeny && denyErr != nil {
			t.Fatalf("deny-list %v rejected expected-allowed module %q: %v", patterns, rawModule, denyErr)
		}
	})
}

func FuzzCapabilityInputValidation(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(""),
		[]byte("payload"),
		[]byte("typed arrays and hashes"),
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		data := newFuzzData(limitFuzzBytes(raw, 256))
		value := data.value(4)

		if err := validateCapabilityDataOnlyValue("payload", value); err != nil {
			t.Fatalf("validateCapabilityDataOnlyValue(payload, %s) failed for generated data-only value: %v", value.String(), err)
		}
		if err := validateCapabilityTypedValue("payload", value, capabilityTypeAny); err != nil {
			t.Fatalf("validateCapabilityTypedValue(payload, %s, any) failed: %v", value.String(), err)
		}
		clone, err := cloneCapabilityMethodResult("capability.method", value)
		if err != nil {
			t.Fatalf("cloneCapabilityMethodResult(%s) failed: %v", value.String(), err)
		}
		if !clone.Equal(value) {
			t.Fatalf("cloneCapabilityMethodResult(%s) = %s, want equal value", value.String(), clone.String())
		}

		ty := data.typeExpr(3)
		_ = validateCapabilityTypedValue("payload", value, ty)
		_ = validateCapabilityKwargsDataOnly("method", map[string]Value{
			data.key(0): value,
			data.key(1): data.value(2),
		})

		options, err := jobqueue.ParseEnqueueOptions("Jobs", fuzzJobQueueKwargs(data, value))
		if err == nil {
			if options.Delay != nil && *options.Delay < 0 {
				t.Fatalf("jobqueue.ParseEnqueueOptions returned negative delay %s", options.Delay.String())
			}
			if options.Key != nil && *options.Key == "" {
				t.Fatalf("jobqueue.ParseEnqueueOptions returned empty key")
			}
		}

		callable := NewBuiltin("fuzz.call", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewNil(), nil
		})
		if err := validateCapabilityDataOnlyValue("callable", callable); err == nil {
			t.Fatalf("validateCapabilityDataOnlyValue(callable builtin) = nil, want data-only error")
		}
		if _, err := cloneCapabilityMethodResult("capability.method", callable); err == nil {
			t.Fatalf("cloneCapabilityMethodResult(callable builtin) = nil error, want data-only error")
		}
		nestedCallable := NewArray([]Value{value, callable})
		if err := validateCapabilityDataOnlyValue("nested callable", nestedCallable); err == nil {
			t.Fatalf("validateCapabilityDataOnlyValue(nested callable) = nil, want data-only error")
		}
	})
}

func generatedScriptCase(data *fuzzData) (string, int64, Value) {
	base := data.int64(-20, 20)
	delta := data.int64(-20, 20)
	first := data.int64(-20, 20)
	second := data.int64(-20, 20)
	input := data.int64(-20, 20)
	evenMultiplier := data.int64(1, 5)
	oddMultiplier := data.int64(1, 5)
	offset := data.int64(-10, 10)
	limit := data.int64(-80, 80)

	source := fmt.Sprintf(`
enum Status
  Draft
  Published
end

class Box
  property value

  def initialize(@value)
  end

  def bump(delta)
    @value = @value + delta
    @value
  end
end

def helper(n)
  if n %% 2 == 0
    n * %d + %d
  else
    n * %d - %d
  end
end

def run(input)
  box = Box.new(%d)
  bumped = box.bump(%d)
  values = [bumped, %d, %d, input]
  mapped = values.map do |n|
    helper(n)
  end
  selected = mapped.select do |n|
    n >= %d
  end
  total = mapped.reduce(0) do |acc, n|
    acc + n
  end
  label = case Status::Draft
  when Status::Published
    "published"
  when Status::Draft
    "draft"
  else
    "other"
  end
  JSON.parse(JSON.stringify({
    total: total,
    selected: selected,
    first: mapped[0],
    size: mapped.size,
    label: label,
    enum: Status::Draft.symbol
  }))
end
`, evenMultiplier, offset, oddMultiplier, offset, base, delta, first, second, limit)

	values := []int64{base + delta, first, second, input}
	mapped := make([]Value, len(values))
	selected := make([]Value, 0, len(values))
	total := int64(0)
	for i, value := range values {
		n := generatedHelper(value, evenMultiplier, oddMultiplier, offset)
		mapped[i] = NewInt(n)
		if n >= limit {
			selected = append(selected, NewInt(n))
		}
		total += n
	}

	want := NewHash(map[string]Value{
		"total":    NewInt(total),
		"selected": NewArray(selected),
		"first":    mapped[0],
		"size":     NewInt(int64(len(mapped))),
		"label":    NewString("draft"),
		"enum":     NewString("draft"),
	})
	return source, input, want
}

func generatedHelper(n, evenMultiplier, oddMultiplier, offset int64) int64 {
	if n%2 == 0 {
		return n*evenMultiplier + offset
	}
	return n*oddMultiplier - offset
}

func fuzzPolicyPatterns(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t'
	})
	if len(fields) == 0 {
		return []string{raw}
	}
	if len(fields) > 4 {
		fields = fields[:4]
	}
	return fields
}

func modulePolicyAnyMatch(patterns []string, module string) bool {
	for _, raw := range patterns {
		pattern := normalizeModulePolicyPattern(raw)
		if pattern == "" {
			continue
		}
		if modulePolicyMatch(pattern, module) {
			return true
		}
	}
	return false
}

func fuzzModuleAliasValue(raw string, selector int) Value {
	choice := selector % 4
	if choice < 0 {
		choice += 4
	}
	switch choice {
	case 0:
		return NewString(raw)
	case 1:
		return NewString(" " + raw + " ")
	case 2:
		return NewSymbol(strings.TrimSpace(raw))
	default:
		return NewInt(int64(selector))
	}
}

func fuzzJobQueueKwargs(data *fuzzData, value Value) map[string]Value {
	kwargs := map[string]Value{
		data.key(0): value,
	}
	switch data.intn(5) {
	case 0:
		kwargs["delay"] = NewDuration(durationFromSeconds(data.int64(0, 3600)))
	case 1:
		kwargs["delay"] = NewDuration(durationFromSeconds(data.int64(-3600, -1)))
	case 2:
		kwargs["delay"] = NewInt(data.int64(-3600, 3600))
	case 3:
		kwargs["delay"] = NewString(data.text(16))
	default:
	}
	if data.intn(3) == 0 {
		kwargs["key"] = NewString(data.text(16))
	} else if data.intn(3) == 1 {
		kwargs["key"] = value
	}
	return kwargs
}

type fuzzData struct {
	raw []byte
	pos int
}

func newFuzzData(raw []byte) *fuzzData {
	return &fuzzData{raw: raw}
}

func (d *fuzzData) byte() byte {
	if len(d.raw) == 0 {
		d.pos++
		return 0
	}
	b := d.raw[d.pos%len(d.raw)]
	d.pos++
	return b
}

func (d *fuzzData) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(d.byte()) % n
}

func (d *fuzzData) int64(min, max int64) int64 {
	if max <= min {
		return min
	}
	span := max - min + 1
	return min + int64(d.byte())%span
}

func (d *fuzzData) text(maxLen int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 _-./\n\t"

	length := d.intn(maxLen + 1)
	var b strings.Builder
	b.Grow(length)
	for range length {
		b.WriteByte(alphabet[d.intn(len(alphabet))])
	}
	return b.String()
}

func (d *fuzzData) key(index int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz"

	length := 1 + d.intn(8)
	var b strings.Builder
	fmt.Fprintf(&b, "k%d_", index)
	for range length {
		b.WriteByte(alphabet[d.intn(len(alphabet))])
	}
	return b.String()
}

func (d *fuzzData) jsonValue(depth int) Value {
	if depth <= 0 {
		switch d.intn(4) {
		case 0:
			return NewNil()
		case 1:
			return NewBool(d.intn(2) == 1)
		case 2:
			return NewInt(d.int64(-1000, 1000))
		default:
			return NewString(d.text(32))
		}
	}

	switch d.intn(6) {
	case 0:
		return NewNil()
	case 1:
		return NewBool(d.intn(2) == 1)
	case 2:
		return NewInt(d.int64(-1000, 1000))
	case 3:
		return NewString(d.text(32))
	case 4:
		length := d.intn(5)
		values := make([]Value, length)
		for i := range values {
			values[i] = d.jsonValue(depth - 1)
		}
		return NewArray(values)
	default:
		length := d.intn(5)
		values := make(map[string]Value, length)
		for i := range length {
			values[d.key(i)] = d.jsonValue(depth - 1)
		}
		return NewHash(values)
	}
}

func (d *fuzzData) value(depth int) Value {
	if depth <= 0 {
		return d.scalarValue()
	}

	switch d.intn(11) {
	case 0:
		return d.scalarValue()
	case 1:
		return NewFloat(float64(d.int64(-1000, 1000)) / 4)
	case 2:
		money, _ := newMoneyFromCents(d.int64(-100000, 100000), "USD")
		return NewMoney(money)
	case 3:
		return NewDuration(durationFromSeconds(d.int64(-100000, 100000)))
	case 4:
		return NewTime(time.Unix(d.int64(0, 4_102_444_800), 0).UTC())
	case 5:
		start := d.int64(-100, 100)
		return NewRange(Range{Start: start, End: start + d.int64(0, 20)})
	case 6:
		length := d.intn(5)
		values := make([]Value, length)
		for i := range values {
			values[i] = d.value(depth - 1)
		}
		return NewArray(values)
	case 7:
		length := d.intn(5)
		values := make(map[string]Value, length)
		for i := range length {
			values[d.key(i)] = d.value(depth - 1)
		}
		return NewHash(values)
	case 8:
		length := d.intn(5)
		values := make(map[string]Value, length)
		for i := range length {
			values[d.key(i)] = d.value(depth - 1)
		}
		return NewObject(values)
	default:
		return d.jsonValue(depth)
	}
}

func (d *fuzzData) scalarValue() Value {
	switch d.intn(5) {
	case 0:
		return NewNil()
	case 1:
		return NewBool(d.intn(2) == 1)
	case 2:
		return NewInt(d.int64(math.MinInt16, math.MaxInt16))
	case 3:
		return NewString(d.text(64))
	default:
		return NewSymbol(d.key(0))
	}
}

func (d *fuzzData) typeExpr(depth int) *TypeExpr {
	if depth <= 0 {
		return &TypeExpr{Kind: d.scalarTypeKind()}
	}

	switch d.intn(5) {
	case 0:
		return &TypeExpr{Kind: TypeAny}
	case 1:
		return &TypeExpr{Kind: TypeArray, TypeArgs: []*TypeExpr{d.typeExpr(depth - 1)}}
	case 2:
		return &TypeExpr{Kind: TypeHash, TypeArgs: []*TypeExpr{
			{Kind: TypeString},
			d.typeExpr(depth - 1),
		}}
	case 3:
		return &TypeExpr{Kind: TypeShape, Shape: map[string]*TypeExpr{
			d.key(0): d.typeExpr(depth - 1),
			d.key(1): d.typeExpr(depth - 1),
		}}
	default:
		return &TypeExpr{Kind: TypeUnion, Union: []*TypeExpr{
			{Kind: d.scalarTypeKind()},
			d.typeExpr(depth - 1),
		}}
	}
}

func (d *fuzzData) scalarTypeKind() TypeKind {
	switch d.intn(9) {
	case 0:
		return TypeNil
	case 1:
		return TypeBool
	case 2:
		return TypeInt
	case 3:
		return TypeFloat
	case 4:
		return TypeNumber
	case 5:
		return TypeString
	case 6:
		return TypeDuration
	case 7:
		return TypeTime
	default:
		return TypeMoney
	}
}

func validateFuzzProgram(program *Program) error {
	if program == nil {
		return fmt.Errorf("program is nil")
	}
	return validateFuzzStatements("program", program.Statements)
}

func validateFuzzStatements(context string, statements []Statement) error {
	for i, stmt := range statements {
		if err := validateFuzzStatement(fmt.Sprintf("%s[%d]", context, i), stmt); err != nil {
			return err
		}
	}
	return nil
}

func validateFuzzStatement(context string, stmt Statement) error {
	if stmt == nil {
		return fmt.Errorf("%s statement is nil", context)
	}
	if err := validateFuzzNodePosition(context, stmt); err != nil {
		return err
	}

	switch s := stmt.(type) {
	case *FunctionStmt:
		return validateFuzzFunctionStmt(context, s)
	case *ReturnStmt:
		if s.Value == nil {
			return nil
		}
		return validateFuzzExpression(context+".value", s.Value)
	case *RaiseStmt:
		if s.Value == nil {
			return nil
		}
		return validateFuzzExpression(context+".value", s.Value)
	case *AssignStmt:
		if err := validateFuzzExpression(context+".target", s.Target); err != nil {
			return err
		}
		return validateFuzzExpression(context+".value", s.Value)
	case *ExprStmt:
		return validateFuzzExpression(context+".expr", s.Expr)
	case *IfStmt:
		return validateFuzzIfStmt(context, s)
	case *ForStmt:
		if s.Iterator == "" {
			return fmt.Errorf("%s iterator is empty", context)
		}
		if err := validateFuzzExpression(context+".iterable", s.Iterable); err != nil {
			return err
		}
		return validateFuzzStatements(context+".body", s.Body)
	case *WhileStmt:
		if err := validateFuzzExpression(context+".condition", s.Condition); err != nil {
			return err
		}
		return validateFuzzStatements(context+".body", s.Body)
	case *UntilStmt:
		if err := validateFuzzExpression(context+".condition", s.Condition); err != nil {
			return err
		}
		return validateFuzzStatements(context+".body", s.Body)
	case *BreakStmt, *NextStmt:
		return nil
	case *TryStmt:
		if err := validateFuzzTypeExpr(context+".rescue_type", s.RescueTy); err != nil {
			return err
		}
		if err := validateFuzzStatements(context+".body", s.Body); err != nil {
			return err
		}
		if err := validateFuzzStatements(context+".rescue", s.Rescue); err != nil {
			return err
		}
		if err := validateFuzzStatements(context+".else", s.Else); err != nil {
			return err
		}
		return validateFuzzStatements(context+".ensure", s.Ensure)
	case *ClassStmt:
		if s.Name == "" {
			return fmt.Errorf("%s class name is empty", context)
		}
		for i, method := range s.Methods {
			if err := validateFuzzFunctionStmt(fmt.Sprintf("%s.methods[%d]", context, i), method); err != nil {
				return err
			}
		}
		for i, method := range s.ClassMethods {
			if err := validateFuzzFunctionStmt(fmt.Sprintf("%s.class_methods[%d]", context, i), method); err != nil {
				return err
			}
		}
		for i, property := range s.Properties {
			if property.Kind == "" {
				return fmt.Errorf("%s.properties[%d] kind is empty", context, i)
			}
			for j, name := range property.Names {
				if name == "" {
					return fmt.Errorf("%s.properties[%d].names[%d] is empty", context, i, j)
				}
			}
		}
		return validateFuzzStatements(context+".body", s.Body)
	case *EnumStmt:
		if s.Name == "" {
			return fmt.Errorf("%s enum name is empty", context)
		}
		for i, member := range s.Members {
			if member.Name == "" {
				return fmt.Errorf("%s.members[%d] name is empty", context, i)
			}
		}
		return nil
	default:
		return fmt.Errorf("%s has unknown statement type %T", context, stmt)
	}
}

func validateFuzzFunctionStmt(context string, stmt *FunctionStmt) error {
	if stmt == nil {
		return fmt.Errorf("%s function is nil", context)
	}
	if err := validateFuzzNodePosition(context, stmt); err != nil {
		return err
	}
	if stmt.Name == "" {
		return fmt.Errorf("%s function name is empty", context)
	}
	for i, param := range stmt.Params {
		if param.Name == "" {
			return fmt.Errorf("%s.params[%d] name is empty", context, i)
		}
		if err := validateFuzzTypeExpr(fmt.Sprintf("%s.params[%d].type", context, i), param.Type); err != nil {
			return err
		}
		if param.DefaultVal != nil {
			if err := validateFuzzExpression(fmt.Sprintf("%s.params[%d].default", context, i), param.DefaultVal); err != nil {
				return err
			}
		}
	}
	if err := validateFuzzTypeExpr(context+".return_type", stmt.ReturnTy); err != nil {
		return err
	}
	return validateFuzzStatements(context+".body", stmt.Body)
}

func validateFuzzIfStmt(context string, stmt *IfStmt) error {
	if stmt == nil {
		return fmt.Errorf("%s if statement is nil", context)
	}
	if err := validateFuzzExpression(context+".condition", stmt.Condition); err != nil {
		return err
	}
	if err := validateFuzzStatements(context+".consequent", stmt.Consequent); err != nil {
		return err
	}
	for i, branch := range stmt.ElseIf {
		if err := validateFuzzIfStmt(fmt.Sprintf("%s.elseif[%d]", context, i), branch); err != nil {
			return err
		}
	}
	return validateFuzzStatements(context+".alternate", stmt.Alternate)
}

func validateFuzzExpression(context string, expr Expression) error {
	if expr == nil {
		return fmt.Errorf("%s expression is nil", context)
	}
	if err := validateFuzzNodePosition(context, expr); err != nil {
		return err
	}

	switch e := expr.(type) {
	case *Identifier:
		if e.Name == "" {
			return fmt.Errorf("%s identifier name is empty", context)
		}
		return nil
	case *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral:
		return nil
	case *SymbolLiteral:
		if e.Name == "" {
			return fmt.Errorf("%s symbol name is empty", context)
		}
		return nil
	case *ArrayLiteral:
		for i, elem := range e.Elements {
			if err := validateFuzzExpression(fmt.Sprintf("%s.elements[%d]", context, i), elem); err != nil {
				return err
			}
		}
		return nil
	case *HashLiteral:
		for i, pair := range e.Pairs {
			if err := validateFuzzExpression(fmt.Sprintf("%s.pairs[%d].key", context, i), pair.Key); err != nil {
				return err
			}
			if err := validateFuzzExpression(fmt.Sprintf("%s.pairs[%d].value", context, i), pair.Value); err != nil {
				return err
			}
		}
		return nil
	case *CallExpr:
		if err := validateFuzzExpression(context+".callee", e.Callee); err != nil {
			return err
		}
		for i, arg := range e.Args {
			if err := validateFuzzExpression(fmt.Sprintf("%s.args[%d]", context, i), arg); err != nil {
				return err
			}
		}
		for i, arg := range e.KwArgs {
			if arg.Name == "" {
				return fmt.Errorf("%s.kwargs[%d] name is empty", context, i)
			}
			if err := validateFuzzExpression(fmt.Sprintf("%s.kwargs[%d].value", context, i), arg.Value); err != nil {
				return err
			}
		}
		return validateFuzzBlockLiteral(context+".block", e.Block)
	case *MemberExpr:
		if e.Property == "" {
			return fmt.Errorf("%s member property is empty", context)
		}
		return validateFuzzExpression(context+".object", e.Object)
	case *ScopeExpr:
		if e.Property == "" {
			return fmt.Errorf("%s scope property is empty", context)
		}
		return validateFuzzExpression(context+".object", e.Object)
	case *IndexExpr:
		if err := validateFuzzExpression(context+".object", e.Object); err != nil {
			return err
		}
		return validateFuzzExpression(context+".index", e.Index)
	case *DestructureTarget:
		if len(e.Elements) == 0 {
			return fmt.Errorf("%s destructuring target has no elements", context)
		}
		seenRest := false
		for i, element := range e.Elements {
			if element.Rest {
				if seenRest {
					return fmt.Errorf("%s.elements[%d] has duplicate rest target", context, i)
				}
				seenRest = true
			}
			if err := validateFuzzExpression(fmt.Sprintf("%s.elements[%d].target", context, i), element.Target); err != nil {
				return err
			}
		}
		return nil
	case *IvarExpr:
		if e.Name == "" {
			return fmt.Errorf("%s instance variable name is empty", context)
		}
		return nil
	case *ClassVarExpr:
		if e.Name == "" {
			return fmt.Errorf("%s class variable name is empty", context)
		}
		return nil
	case *UnaryExpr:
		return validateFuzzExpression(context+".right", e.Right)
	case *BinaryExpr:
		if err := validateFuzzExpression(context+".left", e.Left); err != nil {
			return err
		}
		return validateFuzzExpression(context+".right", e.Right)
	case *ConditionalExpr:
		if err := validateFuzzExpression(context+".condition", e.Condition); err != nil {
			return err
		}
		if err := validateFuzzExpression(context+".consequent", e.Consequent); err != nil {
			return err
		}
		return validateFuzzExpression(context+".alternate", e.Alternate)
	case *RangeExpr:
		if err := validateFuzzExpression(context+".start", e.Start); err != nil {
			return err
		}
		return validateFuzzExpression(context+".end", e.End)
	case *CaseExpr:
		if e.Target != nil {
			if err := validateFuzzExpression(context+".target", e.Target); err != nil {
				return err
			}
		}
		if len(e.Clauses) == 0 {
			return fmt.Errorf("%s case has no clauses", context)
		}
		for i, clause := range e.Clauses {
			if len(clause.Values) == 0 {
				return fmt.Errorf("%s.clauses[%d] has no values", context, i)
			}
			for j, value := range clause.Values {
				if err := validateFuzzExpression(fmt.Sprintf("%s.clauses[%d].values[%d]", context, i, j), value); err != nil {
					return err
				}
			}
			if err := validateFuzzExpression(fmt.Sprintf("%s.clauses[%d].result", context, i), clause.Result); err != nil {
				return err
			}
		}
		if e.ElseExpr != nil {
			return validateFuzzExpression(context+".else", e.ElseExpr)
		}
		return nil
	case *BlockLiteral:
		return validateFuzzBlockLiteral(context, e)
	case *YieldExpr:
		for i, arg := range e.Args {
			if err := validateFuzzExpression(fmt.Sprintf("%s.args[%d]", context, i), arg); err != nil {
				return err
			}
		}
		return nil
	case *InterpolatedString:
		for i, part := range e.Parts {
			switch p := part.(type) {
			case StringText:
			case StringExpr:
				if err := validateFuzzExpression(fmt.Sprintf("%s.parts[%d].expr", context, i), p.Expr); err != nil {
					return err
				}
			default:
				return fmt.Errorf("%s.parts[%d] has unknown string part type %T", context, i, part)
			}
		}
		return nil
	default:
		return fmt.Errorf("%s has unknown expression type %T", context, expr)
	}
}

func validateFuzzBlockLiteral(context string, block *BlockLiteral) error {
	if block == nil {
		return nil
	}
	if err := validateFuzzNodePosition(context, block); err != nil {
		return err
	}
	for i, param := range block.Params {
		if param.Name == "" && param.Target == nil {
			return fmt.Errorf("%s.params[%d] name is empty", context, i)
		}
		if param.Target != nil {
			if err := validateFuzzExpression(fmt.Sprintf("%s.params[%d].target", context, i), param.Target); err != nil {
				return err
			}
		}
		if err := validateFuzzTypeExpr(fmt.Sprintf("%s.params[%d].type", context, i), param.Type); err != nil {
			return err
		}
		if param.DefaultVal != nil {
			if err := validateFuzzExpression(fmt.Sprintf("%s.params[%d].default", context, i), param.DefaultVal); err != nil {
				return err
			}
		}
	}
	return validateFuzzStatements(context+".body", block.Body)
}

func validateFuzzTypeExpr(context string, ty *TypeExpr) error {
	if ty == nil {
		return nil
	}
	for i, arg := range ty.TypeArgs {
		if arg == nil {
			return fmt.Errorf("%s.type_args[%d] is nil", context, i)
		}
		if err := validateFuzzTypeExpr(fmt.Sprintf("%s.type_args[%d]", context, i), arg); err != nil {
			return err
		}
	}
	for field, fieldType := range ty.Shape {
		if field == "" {
			return fmt.Errorf("%s shape field name is empty", context)
		}
		if fieldType == nil {
			return fmt.Errorf("%s shape field %q type is nil", context, field)
		}
		if err := validateFuzzTypeExpr(fmt.Sprintf("%s.shape[%s]", context, field), fieldType); err != nil {
			return err
		}
	}
	for i, option := range ty.Union {
		if option == nil {
			return fmt.Errorf("%s.union[%d] is nil", context, i)
		}
		if err := validateFuzzTypeExpr(fmt.Sprintf("%s.union[%d]", context, i), option); err != nil {
			return err
		}
	}
	return nil
}

func validateFuzzNodePosition(context string, node Node) error {
	pos := node.Pos()
	if pos.Line <= 0 || pos.Column <= 0 {
		return fmt.Errorf("%s has invalid position %d:%d", context, pos.Line, pos.Column)
	}
	return nil
}

func positionBefore(pos, last Position) bool {
	if pos.Line != last.Line {
		return pos.Line < last.Line
	}
	return pos.Column < last.Column
}

func limitFuzzBytes(raw []byte, limit int) []byte {
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}

func limitFuzzString(raw string, limit int) string {
	if len(raw) <= limit {
		return raw
	}
	return raw[:limit]
}

func fuzzSnippet(source string) string {
	source = strings.ReplaceAll(source, "\n", "\\n")
	source = strings.ReplaceAll(source, "\t", "\\t")
	if len(source) <= 160 {
		return source
	}
	return source[:160] + "..."
}
