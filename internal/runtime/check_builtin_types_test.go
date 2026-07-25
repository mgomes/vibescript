package runtime

import "testing"

// newEngineWithProbeBuiltins registers default (non-host) builtins carrying
// typed static contracts on a fresh engine, mirroring how core builtins are
// registered, so the checker's use of typed metadata can be exercised without
// depending on any real builtin's contract.
func newEngineWithProbeBuiltins(t testing.TB) *Engine {
	t.Helper()
	engine := MustNewEngine(Config{})
	probeFn := func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewInt(42), nil
	}
	engine.registerDefaultBuiltin(builtinDefinition{
		name: "probe",
		fn:   probeFn,
		checkSpec: &staticCallSpec{
			minArgs:         1,
			maxArgs:         3,
			allowedKeywords: keywordSet("mode", "extra"),
			paramTypes:      []*TypeExpr{checkTypeString, nil},
			keywordTypes:    map[string]*TypeExpr{"mode": checkTypeInt},
			resultType:      checkTypeInt,
		},
	})
	engine.registerDefaultBuiltin(builtinDefinition{
		name: "probe_untyped",
		fn:   probeFn,
		checkSpec: &staticCallSpec{
			minArgs: 0,
			maxArgs: 1,
		},
	})
	engine.registerDefaultBuiltin(builtinDefinition{
		name:       "probe_token",
		fn:         probeFn,
		autoInvoke: true,
		checkSpec: &staticCallSpec{
			minArgs:    0,
			maxArgs:    0,
			resultType: checkTypeString,
		},
	})
	return engine
}

func TestCheckBuiltinTypedArguments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		source  string
		warning string
	}{
		{
			name: "positional mismatch warns",
			source: `
def run()
  probe(1)
end
`,
			warning: "call to probe argument 1 expected string, got int",
		},
		{
			name: "inferred local mismatch warns",
			source: `
def run()
  value = 1
  probe(value)
end
`,
			warning: "call to probe argument 1 expected string, got int",
		},
		{
			name: "keyword mismatch warns",
			source: `
def run()
  probe("x", mode: "fast")
end
`,
			warning: "call to probe argument mode expected int, got string",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), tc.source)
			requireCheckWarningContains(t, script, tc.warning)
		})
	}
}

func TestCheckBuiltinTypedArgumentsStayGradual(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "matching argument",
			source: `
def run()
  probe("x")
end
`,
		},
		{
			name: "unknown argument value",
			source: `
def run(value)
  probe(value)
end
`,
		},
		{
			name: "position without declared type",
			source: `
def run()
  probe("x", 1)
end
`,
		},
		{
			name: "position past declared list",
			source: `
def run()
  probe("x", 1, :extra)
end
`,
		},
		{
			name: "keyword without declared type",
			source: `
def run()
  probe("x", extra: [1])
end
`,
		},
		{
			name: "splat call defers to runtime",
			source: `
def run()
  args = [1, 2]
  probe(*args)
end
`,
		},
		{
			name: "spec without types",
			source: `
def run()
  probe_untyped(1)
end
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), tc.source)
			requireNoCheckWarnings(t, script)
		})
	}
}

func TestCheckBuiltinResultTypeFlows(t *testing.T) {
	t.Parallel()

	script := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), `
def takes_string(value: string)
  value
end

def run()
  takes_string(probe("x"))
end
`)
	requireCheckWarningContains(t, script, "call to takes_string argument value expected string, got int")

	assigned := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), `
def takes_string(value: string)
  value
end

def run()
  count = probe("x")
  takes_string(count)
end
`)
	requireCheckWarningContains(t, assigned, "call to takes_string argument value expected string, got int")

	unmodeled := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), `
def takes_string(value: string)
  value
end

def run()
  takes_string(probe_untyped(1))
end
`)
	requireNoCheckWarnings(t, unmodeled)
}

func TestCheckBuiltinAutoInvokeIdentifierFact(t *testing.T) {
	t.Parallel()

	script := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), `
def takes_int(value: int)
  value
end

def run()
  token = probe_token
  takes_int(token)
end
`)
	requireCheckWarningContains(t, script, "call to takes_int argument value expected int, got string")

	shadowed := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), `
def takes_int(value: int)
  value
end

def run(probe_token)
  takes_int(probe_token)
end
`)
	requireNoCheckWarnings(t, shadowed)

	shadowedLocal := compileScriptWithEngine(t, newEngineWithProbeBuiltins(t), `
def takes_int(value: int)
  value
end

def run(flag)
  if flag
    probe_token = 1
  end
  token = probe_token
  takes_int(token)
end
`)
	requireCheckWarningContains(t, shadowedLocal, "call to takes_int argument value expected int, got int | nil")
}

func TestCheckBuiltinTypedMetadataHostOverride(t *testing.T) {
	t.Parallel()

	engine := newEngineWithProbeBuiltins(t)
	engine.RegisterBuiltin("probe", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return NewString("host"), nil
	})
	script := compileScriptWithEngine(t, engine, `
def takes_string(value: string)
  value
end

def run()
  takes_string(probe(1))
end
`)
	requireNoCheckWarnings(t, script)
}

func TestCloneBuiltinValuePreservesCheckSpec(t *testing.T) {
	t.Parallel()

	engine := newEngineWithProbeBuiltins(t)
	engine.builtinsMu.RLock()
	original := valueBuiltin(engine.builtins["probe"])
	engine.builtinsMu.RUnlock()
	if original == nil || original.checkSpec == nil {
		t.Fatalf("probe builtin is missing its check spec")
	}

	snapshot := engine.Builtins()
	cloned := valueBuiltin(snapshot["probe"])
	if cloned == nil {
		t.Fatalf("cloned probe builtin is not a builtin value")
	}
	if cloned.checkSpec != original.checkSpec {
		t.Fatalf("cloned builtin lost its typed metadata: got %p, want %p", cloned.checkSpec, original.checkSpec)
	}
}
