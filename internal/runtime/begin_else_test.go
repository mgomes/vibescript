package runtime

import (
	"context"
	"testing"
)

func TestBeginRescueElseSemantics(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def success
  trace = []
  result = nil
  begin
    trace = trace + ["body"]
    result = 1
  rescue
    trace = trace + ["rescue"]
    result = 2
  else
    trace = trace + ["else"]
    result = 3
  ensure
    trace = trace + ["ensure"]
  end
  [result, trace]
end

def rescued
  trace = []
  result = nil
  begin
    trace = trace + ["body"]
    1 / 0
  rescue
    trace = trace + ["rescue"]
    result = 2
  else
    trace = trace + ["else"]
    result = 3
  ensure
    trace = trace + ["ensure"]
  end
  [result, trace]
end

def body_return
  trace = []
  begin
    trace = trace + ["body"]
    return [1, trace]
  rescue
    trace = trace + ["rescue"]
  else
    trace = trace + ["else"]
  ensure
    trace = trace + ["ensure"]
  end
end

def else_error
  begin
    1
  rescue
    "rescued"
  else
    1 / 0
  end
end`)

	success := callScript(t, context.Background(), script, "success", nil, CallOptions{})
	compareArrays(t, success, []Value{
		NewInt(3),
		NewArray([]Value{NewString("body"), NewString("else"), NewString("ensure")}),
	})

	rescued := callScript(t, context.Background(), script, "rescued", nil, CallOptions{})
	compareArrays(t, rescued, []Value{
		NewInt(2),
		NewArray([]Value{NewString("body"), NewString("rescue"), NewString("ensure")}),
	})

	bodyReturn := callScript(t, context.Background(), script, "body_return", nil, CallOptions{})
	compareArrays(t, bodyReturn, []Value{
		NewInt(1),
		NewArray([]Value{NewString("body")}),
	})

	requireCallErrorContains(t, script, "else_error", nil, CallOptions{}, "division by zero")
}
