package runtime

import (
	"context"
	"strings"
	"testing"
)

// TestOptionalKeywordParameterDefaults exercises the binding of optional
// keyword-only parameters declared with `name: default`: the default applies
// when the keyword is omitted, an explicit keyword overrides it, and a default
// expression may reference an earlier parameter.
func TestOptionalKeywordParameterDefaults(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def single(a: 0)
      a
    end

    def chained(a:, b: a + 1)
      b
    end

    def mixed(x, a: 10)
      x + a
    end

    def nil_default(a: nil)
      a == nil
    end
    `)

	t.Run("default_applies_when_omitted", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "single", nil); !got.Equal(NewInt(0)) {
			t.Fatalf("single() = %#v, want 0", got)
		}
	})

	t.Run("explicit_keyword_overrides_default", func(t *testing.T) {
		t.Parallel()
		got := callScript(t, context.Background(), script, "single", nil, CallOptions{
			Keywords: map[string]Value{"a": NewInt(7)},
		})
		if !got.Equal(NewInt(7)) {
			t.Fatalf("single(a: 7) = %#v, want 7", got)
		}
	})

	t.Run("default_references_earlier_keyword", func(t *testing.T) {
		t.Parallel()
		got := callScript(t, context.Background(), script, "chained", nil, CallOptions{
			Keywords: map[string]Value{"a": NewInt(2)},
		})
		if !got.Equal(NewInt(3)) {
			t.Fatalf("chained(a: 2) = %#v, want 3", got)
		}
	})

	t.Run("positional_with_optional_keyword", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "mixed", []Value{NewInt(2)}); !got.Equal(NewInt(12)) {
			t.Fatalf("mixed(2) = %#v, want 12", got)
		}
		got := callScript(t, context.Background(), script, "mixed", []Value{NewInt(2)}, CallOptions{
			Keywords: map[string]Value{"a": NewInt(5)},
		})
		if !got.Equal(NewInt(7)) {
			t.Fatalf("mixed(2, a: 5) = %#v, want 7", got)
		}
	})

	t.Run("nil_default", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "nil_default", nil); !got.Equal(NewBool(true)) {
			t.Fatalf("nil_default() = %#v, want true", got)
		}
	})
}

// TestOptionalKeywordParameterRejectsPositional verifies that an optional
// keyword-only parameter cannot be satisfied by a positional argument, matching
// the required keyword form: only an out-of-place positional argument is
// reported, never a silent bind.
func TestOptionalKeywordParameterRejectsPositional(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def f(a: 0)
      a
    end
    `)

	requireCallErrorContains(t, script, "f", []Value{NewInt(7)}, CallOptions{}, "unexpected positional arguments")
}

// TestOptionalKeywordParameterRejectsUnknownKeyword verifies that supplying a
// keyword that matches no parameter is rejected even when an optional keyword
// parameter is present.
func TestOptionalKeywordParameterRejectsUnknownKeyword(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def f(a: 0)
      a
    end
    `)

	requireCallErrorContains(t, script, "f", nil, CallOptions{
		Keywords: map[string]Value{"b": NewInt(1)},
	}, "unexpected keyword argument b")
}

// TestOptionalKeywordParameterStillRequiresBareKeyword verifies that the
// optional default form does not weaken the bare `name:` required keyword form:
// omitting a required keyword that sits beside an optional one still errors.
func TestOptionalKeywordParameterStillRequiresBareKeyword(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def f(a:, b: 10)
      a + b
    end
    `)

	if got := callScript(t, context.Background(), script, "f", nil, CallOptions{
		Keywords: map[string]Value{"a": NewInt(1)},
	}); !got.Equal(NewInt(11)) {
		t.Fatalf("f(a: 1) = %#v, want 11", got)
	}
	requireCallErrorContains(t, script, "f", nil, CallOptions{}, "missing keyword argument a")
}

// TestOptionalKeywordParameterDefaultMemorySafety verifies that evaluating an
// optional keyword default whose value exceeds the memory quota surfaces the
// limit error rather than silently producing the value, exercising the same
// quota path the runtime applies to positional defaults.
func TestOptionalKeywordParameterDefaultMemorySafety(t *testing.T) {
	t.Parallel()
	largeCSV := strings.Repeat("abcdefghij,", 1500)
	source := `def run(payload: "` + largeCSV + `".split(","))
  payload.size
end`
	script := compileScriptWithConfig(t, Config{StepQuota: 20000, MemoryQuotaBytes: 2048}, source)

	requireCallRuntimeErrorType(t, script, "run", nil, CallOptions{}, runtimeErrorTypeLimit)
}

// TestOptionalKeywordParameterTypedPositionalUnaffected verifies that the
// type-annotation forms keep their positional binding semantics: a typed
// positional parameter still accepts a positional argument, and its `= default`
// still applies when omitted.
func TestOptionalKeywordParameterTypedPositionalUnaffected(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def typed(a: int)
      a + 1
    end

    def typed_default(a: int = 5)
      a + 1
    end
    `)

	if got := callFunc(t, script, "typed", []Value{NewInt(2)}); !got.Equal(NewInt(3)) {
		t.Fatalf("typed(2) = %#v, want 3", got)
	}
	if got := callFunc(t, script, "typed_default", nil); !got.Equal(NewInt(6)) {
		t.Fatalf("typed_default() = %#v, want 6", got)
	}
	if got := callFunc(t, script, "typed_default", []Value{NewInt(9)}); !got.Equal(NewInt(10)) {
		t.Fatalf("typed_default(9) = %#v, want 10", got)
	}
}
