package runtime

import "testing"

func TestSymbolicBooleanOperators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def echo(**opts)
  opts
end

def run
  labels = {and: 1, or: 2, not: 3}
  kwargs = echo(and: 4, or: 5, not: 6)
  {
    and_false: true && false,
    and_value: "left" && "right",
    and_short: nil && missing,
    not_false_and_true: !false && true,
    not_true_or_true: !true || true,
    not_grouped_and: !(true && false),
    symbolic_not: !false,
    or_true: false || true,
    or_value: "left" || missing,
    precedence: true || false && false,
    hash_and: labels[:and],
    hash_or: labels[:or],
    hash_not: labels[:not],
    kw_and: kwargs[:and],
    kw_or: kwargs[:or],
    kw_not: kwargs[:not]
  }
end`)

	got := callFunc(t, script, "run", nil).Hash()
	want := map[string]Value{
		"and_false":          NewBool(false),
		"and_value":          NewString("right"),
		"and_short":          NewNil(),
		"hash_and":           NewInt(1),
		"hash_or":            NewInt(2),
		"hash_not":           NewInt(3),
		"kw_and":             NewInt(4),
		"kw_or":              NewInt(5),
		"kw_not":             NewInt(6),
		"not_false_and_true": NewBool(true),
		"not_true_or_true":   NewBool(true),
		"not_grouped_and":    NewBool(true),
		"or_true":            NewBool(true),
		"or_value":           NewString("left"),
		"precedence":         NewBool(true),
		"symbolic_not":       NewBool(true),
	}
	if diff := valueMapDiff(want, got); diff != "" {
		t.Fatalf("run() mismatch (-want +got):\n%s", diff)
	}
}

func TestTernaryConditionalExpressions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def label(active)
  active ? "active" : "inactive"
end

def run
  {
    true_branch: true ? "yes" : missing_true,
    false_branch: false ? missing_false : "no",
    nil_condition: nil ? missing_nil : "nil",
    value_condition: "present" ? 1 : 2,
    precedence: false || true ? "selected" : missing_precedence,
    nested: false ? missing_nested : true ? "nested" : "miss",
    true_label: label(true),
    false_label: label(false),
    multiline: true ?
      "multi"
    : missing_multiline
  }
end`)

	got := callFunc(t, script, "run", nil).Hash()
	want := map[string]Value{
		"false_branch":    NewString("no"),
		"false_label":     NewString("inactive"),
		"multiline":       NewString("multi"),
		"nested":          NewString("nested"),
		"nil_condition":   NewString("nil"),
		"precedence":      NewString("selected"),
		"true_branch":     NewString("yes"),
		"true_label":      NewString("active"),
		"value_condition": NewInt(1),
	}
	if diff := valueMapDiff(want, got); diff != "" {
		t.Fatalf("run() mismatch (-want +got):\n%s", diff)
	}
}
