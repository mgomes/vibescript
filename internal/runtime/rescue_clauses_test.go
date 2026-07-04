package runtime

import (
	"context"
	"testing"
)

// TestMultipleRescueClauseDispatch pins Ruby's ordered rescue dispatch: the
// first clause whose type matches the raised error handles it, so handlers
// order from specific to general, and a leading general clause shadows later
// specific ones.
func TestMultipleRescueClauseDispatch(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def second_clause_matches
  begin
    1 / 0
  rescue AssertionError
    :assertion
  rescue ZeroDivisionError
    :zero_div
  end
end

def specific_before_general
  begin
    1 / 0
  rescue ZeroDivisionError
    :specific
  rescue StandardError
    :general
  end
end

def general_first_shadows
  begin
    1 / 0
  rescue StandardError
    :general
  rescue ZeroDivisionError
    :specific
  end
end

def untyped_catch_all_last
  begin
    raise "boom"
  rescue ZeroDivisionError
    :zero_div
  rescue
    :fallback
  end
end

def no_clause_matches
  begin
    1 / 0
  rescue AssertionError
    :assertion
  rescue TypeError
    :type
  end
end

def per_clause_bindings(fail_type)
  begin
    if fail_type == :zero
      1 / 0
    else
      raise "boom"
    end
  rescue ZeroDivisionError => zerr
    ["zero", zerr.message]
  rescue RuntimeError => rerr
    ["runtime", rerr.message]
  end
end

def rescue_body_error_skips_later_clauses
  begin
    1 / 0
  rescue ZeroDivisionError
    raise "from handler"
  rescue RuntimeError
    :should_not_run
  end
end

def else_and_ensure_with_clauses
  trace = []
  begin
    trace = trace + ["body"]
  rescue ZeroDivisionError
    trace = trace + ["zero"]
  rescue
    trace = trace + ["other"]
  else
    trace = trace + ["else"]
  ensure
    trace = trace + ["ensure"]
  end
  trace
end`)

	ctx := context.Background()

	if got := callScript(t, ctx, script, "second_clause_matches", nil, CallOptions{}); got.String() != "zero_div" {
		t.Fatalf("second_clause_matches = %v, want :zero_div", got)
	}
	if got := callScript(t, ctx, script, "specific_before_general", nil, CallOptions{}); got.String() != "specific" {
		t.Fatalf("specific_before_general = %v, want :specific", got)
	}
	if got := callScript(t, ctx, script, "general_first_shadows", nil, CallOptions{}); got.String() != "general" {
		t.Fatalf("general_first_shadows = %v, want :general", got)
	}
	if got := callScript(t, ctx, script, "untyped_catch_all_last", nil, CallOptions{}); got.String() != "fallback" {
		t.Fatalf("untyped_catch_all_last = %v, want :fallback", got)
	}

	requireCallErrorContains(t, script, "no_clause_matches", nil, CallOptions{}, "division by zero")

	zero := callScript(t, ctx, script, "per_clause_bindings", []Value{NewSymbol("zero")}, CallOptions{})
	compareArrays(t, zero, []Value{NewString("zero"), NewString("division by zero")})
	boom := callScript(t, ctx, script, "per_clause_bindings", []Value{NewSymbol("boom")}, CallOptions{})
	compareArrays(t, boom, []Value{NewString("runtime"), NewString("boom")})

	// An error raised inside a matched handler propagates; later clauses of the
	// same begin never re-handle it, matching Ruby.
	requireCallErrorContains(t, script, "rescue_body_error_skips_later_clauses", nil, CallOptions{}, "from handler")

	trace := callScript(t, ctx, script, "else_and_ensure_with_clauses", nil, CallOptions{})
	compareArrays(t, trace, []Value{NewString("body"), NewString("else"), NewString("ensure")})
}

// TestFunctionLevelMultipleRescueClauses pins that a def-level rescue tail
// accepts ordered clauses just like an explicit begin block.
func TestFunctionLevelMultipleRescueClauses(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(kind)
  if kind == :zero
    1 / 0
  end
  raise "boom"
rescue ZeroDivisionError
  :zero_div
rescue RuntimeError => e
  e.message
end`)

	ctx := context.Background()
	if got := callScript(t, ctx, script, "run", []Value{NewSymbol("zero")}, CallOptions{}); got.String() != "zero_div" {
		t.Fatalf("run(:zero) = %v, want :zero_div", got)
	}
	if got := callScript(t, ctx, script, "run", []Value{NewSymbol("other")}, CallOptions{}); got.String() != "boom" {
		t.Fatalf("run(:other) = %v, want boom", got)
	}
}

// TestMultipleRescueClausesCheckMode pins that check mode accepts typed
// returns across every clause and scopes each binding to its own clause.
func TestMultipleRescueClausesCheckMode(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(kind) -> string
  if kind == :zero
    1 / 0
  end
  raise "boom"
rescue ZeroDivisionError => zerr
  zerr.message
rescue RuntimeError => rerr
  rerr.message
end`)

	requireNoCheckWarnings(t, script)
}
