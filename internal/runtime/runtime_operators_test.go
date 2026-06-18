package runtime

import "testing"

func TestWordBooleanOperators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def echo(**opts)
  opts
end

def run
  labels = {and: 1, or: 2}
  kwargs = echo(and: 3, or: 4)
  {
    and_false: true and false,
    and_value: "left" and "right",
    and_short: nil and missing,
    or_true: false or true,
    or_value: "left" or missing,
    precedence: true or false and false,
    hash_and: labels[:and],
    hash_or: labels[:or],
    kw_and: kwargs[:and],
    kw_or: kwargs[:or]
  }
end`)

	got := callFunc(t, script, "run", nil).Hash()
	want := map[string]Value{
		"and_false":  NewBool(false),
		"and_value":  NewString("right"),
		"and_short":  NewNil(),
		"hash_and":   NewInt(1),
		"hash_or":    NewInt(2),
		"kw_and":     NewInt(3),
		"kw_or":      NewInt(4),
		"or_true":    NewBool(true),
		"or_value":   NewString("left"),
		"precedence": NewBool(true),
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
    precedence: false or true ? "selected" : missing_precedence,
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
