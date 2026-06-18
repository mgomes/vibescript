package runtime

import (
	"context"
	"testing"
)

func TestPercentWordAndSymbolArrayLiterals(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [%w[alpha beta], %i[open closed], %w[alpha\ beta literal\n]]
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewArray([]Value{NewString("alpha"), NewString("beta")}),
		NewArray([]Value{NewSymbol("open"), NewSymbol("closed")}),
		NewArray([]Value{NewString("alpha beta"), NewString(`literal\n`)}),
	})
}
