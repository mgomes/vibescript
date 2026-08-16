package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// The names ADR-006 item 1 removed from the language. A script reaches the task
// machinery only through the `Tasks` constant and sleeping only through the
// `sleep` builtin, so those two are the entry points; the rest are the members
// that used to hang off them, listed so that reintroducing one under its old
// spelling is caught here rather than in a review.
var removedTaskAndSleepBuiltins = []string{
	"Tasks",
	"Tasks.run",
	"Tasks.map",
	"tasks.spawn",
	"tasks.wait",
	"task.value",
	"sleep",
}

// TestScriptCannotReachTasksOrSleep is the removal verification for ADR-006
// item 1: hosts own concurrency and delay. It fails if script source can reach
// a task or sleep entry point again.
//
// Each source is what the removed feature was actually spelled as, and the
// assertion is that the name resolves to nothing at all rather than to some
// other value that happens to reject the call. "undefined variable" is the
// runtime's answer for a name the language does not have, which is the property
// being pinned; an error mentioning arity or types would mean the name still
// resolves to something.
func TestScriptCannotReachTasksOrSleep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantUnbound string
	}{
		{
			name:        "Tasks.run",
			source:      "def go\n  Tasks.run { |tasks| tasks }\nend\n",
			wantUnbound: "Tasks",
		},
		{
			name:        "Tasks.map",
			source:      "def double(n)\n  n * 2\nend\n\ndef go\n  Tasks.map([1, 2], with: :double)\nend\n",
			wantUnbound: "Tasks",
		},
		{
			name:        "Tasks constant alone",
			source:      "def go\n  Tasks\nend\n",
			wantUnbound: "Tasks",
		},
		{
			name:        "sleep",
			source:      "def go\n  sleep(0)\nend\n",
			wantUnbound: "sleep",
		},
		{
			name:        "sleep without parens",
			source:      "def go\n  sleep 0\nend\n",
			wantUnbound: "sleep",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			engine := MustNewEngine(Config{})
			script, err := engine.Compile(tc.source)
			if err != nil {
				// A compile-time rejection is a stronger removal than a runtime
				// one, so it satisfies the property as long as it names the
				// identifier.
				if !strings.Contains(err.Error(), tc.wantUnbound) {
					t.Fatalf("compile error does not name %q: %v", tc.wantUnbound, err)
				}
				return
			}
			_, err = script.Call(context.Background(), "go", nil, CallOptions{})
			if err == nil {
				t.Fatalf("script reached a removed entry point: %s", tc.source)
			}
			want := "undefined variable " + tc.wantUnbound
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("want error containing %q, got %v", want, err)
			}
		})
	}
}

// TestEngineRegistersNoTaskOrSleepBuiltin checks the registry rather than the
// script surface, so a builtin registered but not yet spelled in a test script
// is still caught.
func TestEngineRegistersNoTaskOrSleepBuiltin(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	registered := map[string]struct{}{}
	for name, val := range engine.Builtins() {
		registered[name] = struct{}{}
		if builtin := valueBuiltin(val); builtin != nil {
			registered[builtin.Name] = struct{}{}
		}
		if val.Kind() != KindObject {
			continue
		}
		for member, memberVal := range val.Hash() {
			registered[name+"."+member] = struct{}{}
			if builtin := valueBuiltin(memberVal); builtin != nil {
				registered[builtin.Name] = struct{}{}
			}
		}
	}

	for _, name := range removedTaskAndSleepBuiltins {
		if _, ok := registered[name]; ok {
			t.Errorf("builtin %q is registered again", name)
		}
		if _, ok := engine.builtinCallSpec(name); ok {
			t.Errorf("checker knows a static call spec for %q", name)
		}
	}
}

// TestConfigCarriesNoTaskOrSleepQuota pins the public embedding surface: step,
// memory, and recursion are the sandbox budgets, and no field reintroduces a
// concurrency pool or a sleeping allowance beside them.
func TestConfigCarriesNoTaskOrSleepQuota(t *testing.T) {
	t.Parallel()

	types := []reflect.Type{reflect.TypeOf(Config{}), reflect.TypeOf(QuotaProfile{})}
	for _, typ := range types {
		for i := range typ.NumField() {
			name := strings.ToLower(typ.Field(i).Name)
			if strings.Contains(name, "task") || strings.Contains(name, "sleep") {
				t.Errorf("%s.%s reintroduces a task or sleep quota", typ.Name(), typ.Field(i).Name)
			}
		}
	}

	for _, profile := range []QuotaProfile{ProfileLow, ProfileMedium, ProfileHigh, ProfileXHigh} {
		cfg := Config{}
		profile.ApplyTo(&cfg)
		summary := MustNewEngine(cfg).ConfigSummary()
		for _, banned := range []string{"task", "sleep"} {
			if strings.Contains(summary, banned) {
				t.Errorf("profile %s summary %q still reports %q", profile.Name, summary, banned)
			}
		}
	}
}
