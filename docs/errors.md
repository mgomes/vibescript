# Errors and Debugging

Vibescript surfaces both parse-time and runtime failures with line and column information.

Message phrasing follows fixed conventions; contributors changing or
adding diagnostics and hosts matching errors programmatically should
read [`error_conventions.md`](error_conventions.md).

## Parse Errors

Compilation failures include a parser message and a source code frame:

```text
parse error at 2:9: missing value for hash key foo
  --> line 2, column 9
 2 |   {foo: }
   |         ^
```

Common parser diagnostics:

- `invalid hash pair: expected key like name: or "name":`
- `missing value for hash key ...`
- `parallel assignment targets require '='`
- `duplicate rest assignment target`
- `invalid destructuring assignment target`
- `trailing comma in block parameter list`

Hosts that need positions programmatically (editors, linters, CI
annotators) should not scrape the error text: `vibes.ParseIssues(err)`
returns the structured failures behind a `Compile` error — start
position, optional offending-token end position, and the bare message —
in source order. It returns nil for errors that carry no positions, such
as duplicate top-level name failures.

## Runtime Errors

Runtime failures include:

- the runtime message (`division by zero`, `undefined variable ...`, etc.)
- a code frame for the failure location
- a stack trace (`at function (line:column)`)

```text
division by zero
  --> line 3, column 9
 3 |   a / b
   |         ^
  at divide (3:9)
  at calculate (7:7)
```

## Type Errors

Typed argument and return checks include:

- parameter or function context
- expected type
- actual runtime type

```text
argument payload expected { id: string, score: int }, got { id: string, score: string }
```

For composite values, actual types include shape/element detail (`array<int | string>`, `{ id: string, ... }`) to make fixes local and explicit.

## Loop Control Errors

Loop control diagnostics are explicit:

- `break used outside of loop`
- `next used outside of loop`
- `break cannot cross call boundary`
- `next cannot cross call boundary`

These boundary errors happen when `break`/`next` are raised inside called blocks/functions and attempt to escape into an outer loop.

## Structured Error Handling

Use `begin` with `rescue` and/or `ensure` for script-level recovery:

```vibe
def safe_div(a, b)
  begin
    a / b
  rescue RuntimeError => err
    audit(err.message)
    "fallback"
  ensure
    audit("safe_div attempted")
  end
end
```

Order multiple `rescue` clauses from specific to general; the first clause
whose type matches the raised error handles it, as in Ruby:

```vibe
def parse_config(raw)
  begin
    decode(raw)
  rescue TypeError => err
    audit("bad shape: " + err.message)
    default_config
  rescue RuntimeError => err
    audit("unexpected: " + err.message)
    raise
  end
end
```

Re-raise the current rescued error with `raise`:

```vibe
begin
  risky_call
rescue(AssertionError)
  audit("recovering assertion")
  raise
end
```

Semantics:

- `rescue` runs only when the `begin` body raises an error.
- A `begin` block may carry multiple ordered `rescue` clauses; the first clause whose type matches handles the error, so order handlers from specific to general.
- `rescue` supports optional typed matching via `rescue <Type>` and the older `rescue(<Type>)` form.
- `rescue` supports `AssertionError`, `LimitError`, `RuntimeError`, and unions such as `rescue AssertionError | RuntimeError`.
- Genuine sandbox exhaustion — a tripped step quota, a tripped memory quota, or `string.scan`'s output cap — is never rescuable, by any clause type. The exhaustion latches the execution, so `ensure` bodies and `retry` cannot run work past it either. `rescue LimitError` still matches recursion-limit terminations, stdlib per-operation guards (an oversized `random_id` length or a `format` rendering past its fixed output cap, for example), and script-raised `raise LimitError` errors: those describe one rejected operation, not a spent budget.
- `rescue => err` and `rescue RuntimeError => err` bind an object for the handler body with `type`, `message`, and `code_frame` fields.
- `else` runs only when the `begin` body finishes without a rescued error.
- `ensure` runs on success, on the rescue path, and on ordinary failure paths. After latched exhaustion the runtime still attempts the `ensure`, but the statement-level charge re-raises the quota error before the first statement executes, so the body's cleanup work is skipped entirely past a genuine budget kill.
- Without `rescue`, original runtime errors still propagate after `ensure` executes.
- Unmatched typed rescues do not swallow the original error.
- `raise` inside `rescue` re-raises the original error and preserves its stack frames.
- `raise "message"` raises a new runtime error. Bare `raise` outside `rescue` is a runtime error.

## REPL Debugging

The REPL stores the previous failure. Use:

- `:last_error` to print the latest compile/runtime error.

This is useful after long output or when a failure scrolls out of view.
