package runtime

import "testing"

func TestWordBooleanOperators(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  {
    and_false: true and false,
    and_value: "left" and "right",
    and_short: nil and missing,
    or_true: false or true,
    or_value: "left" or missing,
    precedence: true or false and false
  }
end`)

	got := callFunc(t, script, "run", nil).Hash()
	want := map[string]Value{
		"and_false":  NewBool(false),
		"and_value":  NewString("right"),
		"and_short":  NewNil(),
		"or_true":    NewBool(true),
		"or_value":   NewString("left"),
		"precedence": NewBool(true),
	}
	if diff := valueMapDiff(want, got); diff != "" {
		t.Fatalf("run() mismatch (-want +got):\n%s", diff)
	}
}
