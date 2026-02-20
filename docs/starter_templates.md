# Starter Templates

VibeScript ships starter scaffolds in `templates/embedding/` for common host
integration scenarios.

## Included Scenarios

### HTTP Handler

Path: `templates/embedding/http_handler/main.go.tmpl`

Use when scripts are evaluated per incoming request. This template shows:

- A single compiled script reused across requests.
- Request-scoped globals.
- Hardened runtime quotas.

### Background Job

Path: `templates/embedding/background_job/main.go.tmpl`

Use when scripts run on schedules or queue consumers. This template shows:

- Deterministic periodic execution.
- Isolated `Script.Call` invocations per job tick.
- Conservative defaults for long-running workers.

### Module Host

Paths:

- `templates/embedding/module_host/main.go.tmpl`
- `templates/embedding/module_host/workflows/public/fees.vibe`

Use when scripts are split into reusable modules. This template shows:

- `ModulePaths` setup for script repositories.
- `ModuleAllowList`/`ModuleDenyList` policy boundaries.
- `require` usage for explicit module coupling.

## Adoption Notes

1. Copy the closest template into your host codebase.
2. Replace placeholder capability adapters with domain-specific adapters.
3. Set quotas from production traffic profiles.
4. Add integration tests for your script entrypoints before rollout.
