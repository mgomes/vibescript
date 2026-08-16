package runtime

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// wideClassBodySource builds a run of classes whose bodies each bind a large
// string constant, so class-body initialization allocates far more than the
// source spells.
func wideClassBodySource(classes, repeats int) string {
	var b strings.Builder
	for i := range classes {
		fmt.Fprintf(&b, "class Holder%06d\n  DATA = \"0123456789\" * %d\nend\n", i, repeats)
	}
	b.WriteString("\nexport def entry\n  1\nend\n")
	return b.String()
}

// TestRequiredModuleClassBodyInitializationIsMetered pins that the memory a
// required file's class bodies build is inside the calling execution's quota.
// A required module initializes before require publishes its exports, so the
// state it is building is reachable only through the environment it is being
// built in, and the checks running inside that initialization have to see it
// (#23). It reads process-wide allocation, so it must not run in parallel with
// anything else.
func TestRequiredModuleClassBodyInitializationIsMetered(t *testing.T) {
	dir := tempModuleTree(t, moduleFile{
		path:    "wide.vibe",
		content: wideClassBodySource(200, 20000),
	})
	engine := MustNewEngine(Config{
		ModulePaths: []string{dir},
		StepQuota:   Unlimited,
	})
	script := compileScriptWithEngine(t, engine, `def run
  require("wide")
  1
end`)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := script.Call(context.Background(), "run", nil, CallOptions{AllowRequire: true})
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("a required module built 400MB of class constants within the memory quota")
	}
	requireErrorContains(t, err, "memory quota exceeded")

	if estimatorVerify {
		t.Logf("estimator oracle enabled: skipping the allocation bound (%d bytes)", after.TotalAlloc-before.TotalAlloc)
		return
	}
	// Unrooted, the same module ran every body to completion and allocated the
	// full expansion before anything stopped it.
	const limit = 128 << 20
	if got := after.TotalAlloc - before.TotalAlloc; got > limit {
		t.Fatalf("requiring the module allocated %d bytes, want at most %d", got, limit)
	}
}

// TestOrdinaryRequiredModuleClassBodyStaysWithinDefaultQuotas pins that the
// metering above leaves a normal required file alone: its class constants are
// readable through the classes that declare them and cost nothing a
// default-profile call notices.
func TestOrdinaryRequiredModuleClassBodyStaysWithinDefaultQuotas(t *testing.T) {
	t.Parallel()

	dir := tempModuleTree(t, moduleFile{
		path: "limits.vibe",
		content: `class Limits
  MAX = 9
  LABEL = "limit"
end

export def describe
  "#{Limits::LABEL}:#{Limits::MAX}"
end
`,
	})
	engine := MustNewEngine(Config{ModulePaths: []string{dir}})
	script := compileScriptWithEngine(t, engine, `def run
  limits = require("limits")
  limits.describe()
end`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{AllowRequire: true})
	if !got.Equal(NewString("limit:9")) {
		t.Fatalf("run = %v, want \"limit:9\"", got)
	}
}
