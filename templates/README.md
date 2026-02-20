# Starter Templates

These templates are reference scaffolds for common host integration scenarios.
They are intentionally minimal and use `.tmpl` so you can copy/adapt them
without affecting module builds.

## Available Templates

- `templates/embedding/http_handler/`:
  request-scoped execution from an HTTP endpoint.
- `templates/embedding/background_job/`:
  deterministic script execution in a worker/job runner.
- `templates/embedding/module_host/`:
  module-based script repository with policy-controlled `require`.
