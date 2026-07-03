package runtime

import "testing"

func TestRubyTruthinessOnlyNilAndFalseAreFalsy(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def classify(value)
  if value
    "truthy"
  else
    "falsy"
  end
end

def run
  values = [nil, false, 0, 0.0, "", [], {}, "x"]
  values.map do |value|
    classify(value)
  end
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{
		NewString("falsy"),
		NewString("falsy"),
		NewString("truthy"),
		NewString("truthy"),
		NewString("truthy"),
		NewString("truthy"),
		NewString("truthy"),
		NewString("truthy"),
	})
}
