package runtime

import (
	"context"
	"testing"
)

func TestRequiredKeywordParameterAndKeywordShorthand(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes(name:)
  name
end

def normal(name)
  name
end

def run
  name = "Ada"
  [takes(name:), takes(name: "Grace"), normal(name:)]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewString("Ada"),
		NewString("Grace"),
		NewString("Ada"),
	})
}

func TestRequiredKeywordParameterRejectsPositionalArgument(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def takes(name:)
  name
end`)

	requireCallErrorContains(t, script, "takes", []Value{NewString("Ada")}, CallOptions{}, "missing keyword argument name")
}
