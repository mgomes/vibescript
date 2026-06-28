package runtime

import (
	"context"
	"testing"
)

func TestRubyStyleStringFormatting(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run
  [
    "%s:%03d" % ["id", 7],
    format("%.2f", 1.234),
    sprintf("%x", 255),
    "%s" % :ok,
    5 % 2
  ]
end

def bad_format
  format()
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	compareArrays(t, got, []Value{
		NewString("id:007"),
		NewString("1.23"),
		NewString("ff"),
		NewString("ok"),
		NewInt(1),
	})
	requireCallErrorContains(t, script, "bad_format", nil, CallOptions{}, "format expects a format string")
}

func TestRubyStyleStringFormattingRejectsOversizedOutputBeforeFormatting(t *testing.T) {
	t.Parallel()

	script := compileScriptWithConfig(t, Config{MemoryQuotaBytes: 4096}, `def builtin_format
  format("%1000000s", "")
end

def operator_format
  "%1000000s" % ""
end`)

	requireCallRuntimeErrorType(t, script, "builtin_format", nil, CallOptions{}, runtimeErrorTypeLimit)
	requireCallRuntimeErrorType(t, script, "operator_format", nil, CallOptions{}, runtimeErrorTypeLimit)

	capped := compileScript(t, `def run
  format("%1048577s", "")
end`)
	requireCallErrorContains(t, capped, "run", nil, CallOptions{}, "format width exceeds limit")
}
