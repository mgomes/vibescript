package runtime

import "testing"

func TestIfExpressions(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def pick(value)
  value
end

def choose(value)
  label = if value == 1
    "one"
  elsif value == 2
    "two"
  else
    "other"
  end
  label
end

def missing_else(flag)
  value = if flag
    "enabled"
  end
  value
end

def return_if_expression(flag)
  return if flag
    "yes"
  else
    "no"
  end
end

def argument_if_expression(flag)
  pick(if flag then "yes" else "no" end)
end

def nested_if_expression(a, b)
  value = if a
    if b
      "both"
    else
      "a"
    end
  else
    "none"
  end
  value
end`)

	tests := []struct {
		name string
		fn   string
		args []Value
		want Value
	}{
		{name: "choose_first", fn: "choose", args: []Value{NewInt(1)}, want: NewString("one")},
		{name: "choose_elsif", fn: "choose", args: []Value{NewInt(2)}, want: NewString("two")},
		{name: "choose_else", fn: "choose", args: []Value{NewInt(3)}, want: NewString("other")},
		{name: "missing_else_true", fn: "missing_else", args: []Value{NewBool(true)}, want: NewString("enabled")},
		{name: "missing_else_false", fn: "missing_else", args: []Value{NewBool(false)}, want: NewNil()},
		{name: "return_true", fn: "return_if_expression", args: []Value{NewBool(true)}, want: NewString("yes")},
		{name: "return_false", fn: "return_if_expression", args: []Value{NewBool(false)}, want: NewString("no")},
		{name: "argument_true", fn: "argument_if_expression", args: []Value{NewBool(true)}, want: NewString("yes")},
		{name: "argument_false", fn: "argument_if_expression", args: []Value{NewBool(false)}, want: NewString("no")},
		{name: "nested_both", fn: "nested_if_expression", args: []Value{NewBool(true), NewBool(true)}, want: NewString("both")},
		{name: "nested_outer", fn: "nested_if_expression", args: []Value{NewBool(true), NewBool(false)}, want: NewString("a")},
		{name: "nested_none", fn: "nested_if_expression", args: []Value{NewBool(false), NewBool(true)}, want: NewString("none")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := callFunc(t, script, tc.fn, tc.args); !got.Equal(tc.want) {
				t.Fatalf("%s(%v) = %v, want %v", tc.fn, tc.args, got, tc.want)
			}
		})
	}
}
