# Host Integration Cookbook

This cookbook complements `docs/integration.md` with production-focused
embedding patterns.

## 1. Hardened Engine Configuration

Use explicit quotas and strict effects in production hosts:

```go
engine, err := vibes.NewEngine(vibes.Config{
    StepQuota:        20_000,
    MemoryQuotaBytes: 256 << 10, // 256 KiB
    RecursionLimit:   32,
    StrictEffects:    true,
    ModulePaths:      []string{"/srv/vibes/modules"},
})
if err != nil {
    return err
}
```

Why: this keeps runaway scripts bounded and forces side effects through approved
capability adapters.

## 2. Request-Scoped Execution

Compile once when possible, execute per request with fresh globals:

```go
script, err := engine.Compile(source)
if err != nil {
    return err
}

func evaluate(ctx context.Context, tenant string, input vibes.Value) (vibes.Value, error) {
    return script.Call(ctx, "run", []vibes.Value{input}, vibes.CallOptions{
        Globals: map[string]vibes.Value{
            "tenant": vibes.NewString(tenant),
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

## 5. Failure Handling and Observability

On script failures, capture:

- Script/function name and version.
- Tenant/workflow identifiers.
- Sanitized runtime error (`err.Error()`), without leaking secrets.
- Step/memory policy values for quick triage.

Prefer structured logs and metrics over string parsing. Keep parse/runtime
errors user-visible only when messages are sanitized for your domain.

## 6. Upgrade Workflow

For each VibeScript version bump:

1. Run `go test ./...` and representative script smoke tests.
2. Re-run docs/examples smoke checks in CI.
3. Read release notes for deprecations and migration steps.
4. Roll out behind feature flags if scripts are business-critical.
