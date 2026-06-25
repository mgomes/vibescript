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

// TestOptionalKeywordParameterHashDefault verifies that a `{ ... }` keyword
// default is bound as a hash literal: the default hash applies when the keyword
// is omitted, an explicit keyword overrides it, and a hash default may reference
// an earlier keyword parameter.
func TestOptionalKeywordParameterHashDefault(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def opts_default(opts: { retry: 3 })
      opts[:retry]
    end

    def empty_default(opts: {})
      opts.size
    end

    def chained_hash(a:, b: { sum: a + 1 })
      b[:sum]
    end

    def bare_chained_hash(a:, b: { sum: a })
      b[:sum]
    end

    def nil_field_default(opts: { previous: nil })
      opts[:previous] == nil
    end
    `)

	t.Run("default_hash_applies_when_omitted", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "opts_default", nil); !got.Equal(NewInt(3)) {
			t.Fatalf("opts_default() = %#v, want 3", got)
		}
	})

	t.Run("explicit_hash_overrides_default", func(t *testing.T) {
		t.Parallel()
		got := callScript(t, context.Background(), script, "opts_default", nil, CallOptions{
			Keywords: map[string]Value{"opts": NewHash(map[string]Value{"retry": NewInt(9)})},
		})
		if !got.Equal(NewInt(9)) {
			t.Fatalf("opts_default(opts: {retry: 9}) = %#v, want 9", got)
		}
	})

	t.Run("empty_hash_default", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "empty_default", nil); !got.Equal(NewInt(0)) {
			t.Fatalf("empty_default() = %#v, want 0", got)
		}
	})

	t.Run("hash_default_references_earlier_keyword", func(t *testing.T) {
		t.Parallel()
		got := callScript(t, context.Background(), script, "chained_hash", nil, CallOptions{
			Keywords: map[string]Value{"a": NewInt(2)},
		})
		if !got.Equal(NewInt(3)) {
			t.Fatalf("chained_hash(a: 2) = %#v, want 3", got)
		}
	})

	// A bare identifier hash value (no trailing operator) referencing an earlier
	// keyword parameter must still bind as a hash default rather than being
	// misclassified as a positional shape annotation. Matches Ruby:
	// `def g(a:, b: { sum: a }); b; end; g(a: 2) # => {sum: 2}`.
	t.Run("bare_ident_hash_default_references_earlier_keyword", func(t *testing.T) {
		t.Parallel()
		got := callScript(t, context.Background(), script, "bare_chained_hash", nil, CallOptions{
			Keywords: map[string]Value{"a": NewInt(2)},
		})
		if !got.Equal(NewInt(2)) {
			t.Fatalf("bare_chained_hash(a: 2) = %#v, want 2", got)
		}
	})

	t.Run("nil_valued_hash_default_binds_keyword", func(t *testing.T) {
		t.Parallel()
		if got := callFunc(t, script, "nil_field_default", nil); !got.Equal(NewBool(true)) {
			t.Fatalf("nil_field_default() = %#v, want true", got)
		}
		got := callScript(t, context.Background(), script, "nil_field_default", nil, CallOptions{
			Keywords: map[string]Value{"opts": NewHash(map[string]Value{"previous": NewInt(1)})},
		})
		if !got.Equal(NewBool(false)) {
			t.Fatalf("nil_field_default(opts: {previous: 1}) = %#v, want false", got)
		}
	})
}

// TestOptionalKeywordParameterLessThanDefault verifies that a keyword default
// expression starting with an earlier keyword parameter followed by `<` is
// evaluated as a comparison rather than a generic type continuation, so the
// default reflects the prior parameter's value at call time.
func TestOptionalKeywordParameterLessThanDefault(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def f(limit:, ok: limit < 10)
      ok
    end
    `)

	below := callScript(t, context.Background(), script, "f", nil, CallOptions{
		Keywords: map[string]Value{"limit": NewInt(3)},
	})
	if !below.Equal(NewBool(true)) {
		t.Fatalf("f(limit: 3) = %#v, want true", below)
	}

	above := callScript(t, context.Background(), script, "f", nil, CallOptions{
		Keywords: map[string]Value{"limit": NewInt(30)},
	})
	if !above.Equal(NewBool(false)) {
		t.Fatalf("f(limit: 30) = %#v, want false", above)
	}
}
