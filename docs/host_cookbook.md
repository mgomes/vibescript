# Host Integration Cookbook

This cookbook complements `docs/integration.md` with production-focused
embedding patterns.

## 1. Hardened Engine Configuration

Use explicit quotas and strict effects in production hosts:

```go
engine, err := vibes.NewEngine(vibes.Config{
    StepQuota:              20_000,
    MemoryQuotaBytes:       256 << 10, // 256 KiB
    RecursionLimit:         32,
    StrictEffects:          true,
    DefaultTaskConcurrency: 4,
    MaxTaskConcurrency:     16,
    ModulePaths:            []string{"/srv/vibes/modules"},
})
if err != nil {
    return err
}
```

Why: this keeps runaway scripts bounded and forces side effects through approved
capability adapters. The task settings let scripts express bounded fanout
without exceeding host pool sizes or upstream rate limits. If a host sets only a
lower `MaxTaskConcurrency`, the implicit default fanout follows that lower cap.

## 2. Request-Scoped Execution

Compile once when possible, execute per request with fresh globals:

```go
script, err := engine.Compile(source)
if err != nil {
    return err
}

func evaluate(ctx context.Context, tenant string, input value.Value) (value.Value, error) {
    return script.Call(ctx, "run", []value.Value{input}, vibes.CallOptions{
        Globals: map[string]value.Value{
            "tenant": value.NewString(tenant),
        },
        Capabilities: []vibes.CapabilityAdapter{
            paymentsAdapter{},
            eventsAdapter{},
        },
    })
}
```

Why: per-call globals avoid cross-request state leakage while keeping compile
time amortized.

## 3. Capability Surface Design

Expose narrow, intention-revealing capability methods instead of generic
arbitrary dispatch.

Good:

- `payments.create_charge(customer_id, cents)`
- `events.track(name, payload)`

Avoid:

- `db.exec(sql)`
- `http.request(url, method, headers, body)`

Why: small surfaces are easier to contract-test and audit.

## 4. Module Governance

Treat module loading as policy-controlled:

```go
engine, err := vibes.NewEngine(vibes.Config{
    ModulePaths:     []string{"/srv/vibes/modules"},
    ModuleAllowList: []string{"billing/*", "shared/*"},
    ModuleDenyList:  []string{"billing/internal/*"},
})
```

Why: this reduces accidental coupling and blocks unsafe internal helpers from
becoming de-facto public APIs.

### Gate untrusted scripts with CheckedCall

`Script.Call` stays gradual: provable contradictions warn under
`CheckWarnings*` but the call still runs and relies on runtime contracts.
Deployment pipelines and untrusted-script boundaries that want a hard static
gate use the opt-in combined API:

```go
result, warnings, err := script.CheckedCall(ctx, "run", args, opts)
switch {
case len(warnings) > 0:
        // Static gate: the script did not execute.
case err != nil:
        // Runtime failure from the executed call.
}
```

The static phase checks the exact call — same function, same argument
values, same options — so the gate and the execution can never disagree
about names, inputs, or bound host surfaces.

## 5. Failure Handling and Observability

On script failures, capture:

- Script/function name and version.
- Tenant/workflow identifiers.
- Sanitized runtime error (`err.Error()`), without leaking secrets.
- Step/memory policy values for quick triage.

Prefer structured logs and metrics over string parsing. Keep parse/runtime
errors user-visible only when messages are sanitized for your domain.

## 6. Upgrade Workflow

For each Vibescript version bump:

1. Run `go test ./...` and representative script smoke tests.
2. Re-run docs/examples smoke checks in CI.
3. Read release notes for deprecations and migration steps.
4. Roll out behind feature flags if scripts are business-critical.

## 7. Dev Mode: Live-Reloading Scripts and Modules

During development, enable `Config.DevMode` so edits to `require`d modules are
picked up without restarting the host or calling `ClearModuleCache()`:

```go
engine, err := vibes.NewEngine(vibes.Config{
    ModulePaths: []string{"./modules"},
    DevMode:     true, // development only
})
```

In dev mode every `require` revalidates its cached module against the source
file's mtime+size and recompiles on change, and require misses are re-resolved
from disk so newly created module files load without a restart. Each `Call`
still sees a single consistent version of every module it requires, even if a
file changes mid-call. Do not enable it in production: each require costs a
stat, and a reload is not atomic across concurrently running calls.

The engine compiles top-level sources from text, not files, so reloading the
script itself stays a host concern. The same stamp-and-recompile pattern is a
few lines:

```go
type liveScript struct {
    engine *vibes.Engine
    path   string

    mu     sync.Mutex
    mtime  time.Time
    size   int64
    script *vibes.Script
}

func (l *liveScript) Current() (*vibes.Script, error) {
    info, err := os.Stat(l.path)
    if err != nil {
        return nil, err
    }
    l.mu.Lock()
    defer l.mu.Unlock()
    if l.script != nil && info.ModTime().Equal(l.mtime) && info.Size() == l.size {
        return l.script, nil
    }
    source, err := os.ReadFile(l.path)
    if err != nil {
        return nil, err
    }
    script, err := l.engine.Compile(string(source))
    if err != nil {
        return nil, err
    }
    l.mtime, l.size, l.script = info.ModTime(), info.Size(), script
    return script, nil
}
```

In-flight calls finish on the script they started with — the same guarantee
dev mode gives modules. `engine.ClearModuleCache()` remains available in both
modes as the manual, production-safe invalidation.
