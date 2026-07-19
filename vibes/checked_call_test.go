package vibes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
)

func TestCheckedCallGatesOnDiagnostics(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`
def run(count: int)
  count + 1
end
`)
	if err != nil {
		t.Fatal(err)
	}

	executed, warnings, err := script.CheckedCall(context.Background(), "run", []value.Value{value.NewInt(41)}, vibes.CallOptions{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("CheckedCall(valid) = warnings %v, err %v; want clean execution", warnings, err)
	}
	if executed.Int() != 42 {
		t.Fatalf("CheckedCall(valid) = %s, want 42", executed)
	}

	blocked, warnings, err := script.CheckedCall(context.Background(), "run", []value.Value{value.NewString("nope")}, vibes.CallOptions{})
	if err != nil {
		t.Fatalf("CheckedCall(contradiction) error = %v, want static gate with nil error", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "expected int, got string") {
		t.Fatalf("CheckedCall(contradiction) warnings = %v, want argument mismatch", warnings)
	}
	if !blocked.IsNil() {
		t.Fatalf("CheckedCall(contradiction) result = %s, want nil (not executed)", blocked)
	}
}

func TestCheckedCallReportsRuntimeFailuresSeparately(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`
def run()
  raise "boom"
end
`)
	if err != nil {
		t.Fatal(err)
	}

	_, warnings, err := script.CheckedCall(context.Background(), "run", nil, vibes.CallOptions{})
	if len(warnings) != 0 {
		t.Fatalf("CheckedCall(runtime failure) warnings = %v, want none", warnings)
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("CheckedCall(runtime failure) error = %v, want raised boom", err)
	}
}

func TestCheckedCallDoesNotExecuteOnScriptDiagnostics(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	engine.RegisterBuiltin("record", func(exec *vibes.Execution, receiver value.Value, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
		t.Fatal("record ran even though the static gate should have blocked execution")
		return value.NewNil(), nil
	})
	script, err := engine.Compile(`
def takes_int(v: int)
  v
end

def run()
  record()
  takes_int("nope")
end
`)
	if err != nil {
		t.Fatal(err)
	}

	_, warnings, err := script.CheckedCall(context.Background(), "run", nil, vibes.CallOptions{})
	if err != nil || len(warnings) == 0 {
		t.Fatalf("CheckedCall(script diagnostic) = warnings %v, err %v; want static gate", warnings, err)
	}
}

func TestCheckedCallBindsCapabilitiesUnderCallContext(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}
	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`
def run()
  request["id"]
end
`)
	if err != nil {
		t.Fatal(err)
	}

	adapter := vibes.MustNewContextCapability("request", func(ctx context.Context) (value.Value, error) {
		id, _ := ctx.Value(ctxKey{}).(string)
		if id == "" {
			return value.NewNil(), errors.New("bind must see the invocation context")
		}
		return value.NewHash(map[string]value.Value{"id": value.NewString(id)}), nil
	})

	ctx := context.WithValue(context.Background(), ctxKey{}, "req-1")
	got, warnings, err := script.CheckedCall(ctx, "run", nil, vibes.CallOptions{Capabilities: []vibes.CapabilityAdapter{adapter}})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("CheckedCall(context capability) = warnings %v, err %v; want clean run", warnings, err)
	}
	if got.String() != "req-1" {
		t.Fatalf("CheckedCall(context capability) = %s, want req-1", got)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, warnings, err = script.CheckedCall(canceled, "run", nil, vibes.CallOptions{Capabilities: []vibes.CapabilityAdapter{adapter}})
	if len(warnings) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("CheckedCall(canceled) = warnings %v, err %v; want context.Canceled with no adapter work", warnings, err)
	}
}
