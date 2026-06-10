# Error Message Conventions

Error text becomes de-facto API the moment a host starts matching on
it. This document freezes the phrasing conventions for Vibescript
diagnostics ahead of 1.0 so new error sites stay consistent, and it
describes the supported ways for hosts to match errors programmatically
so message text stays free to evolve.

Audience: contributors adding or changing error sites. For the
user-facing tour of errors and `begin`/`rescue`, see
[`errors.md`](errors.md).

## Error surfaces

Vibescript reports problems on three distinct surfaces. Each has its
own conventions; do not mix them.

1. **Parse errors** (`internal/parser`) — problems in script source,
   reported with a position and code frame.
2. **Runtime errors** (`internal/runtime`) — problems while executing a
   script, reported as `vibes.RuntimeError` with a stack trace.
3. **Host-configuration errors** (`vibes` facade) — misuse of the Go
   API by the embedding host. These are prefixed `vibes: ` (for
   example `vibes: module path %q is not a directory`) and never reach
   scripts. CLI flag errors in `cmd/vibes` are a fourth, separate
   surface and follow CLI conventions, not these.

## Subject prefix rule

Runtime messages name their subject first, using the same spelling the
script author typed or could type:

- **Built-in member methods** carry the receiver kind:
  `string.sub`, `array.pop`, `hash.fetch`, `int.times`, `float.clamp`,
  `time.eql?`, `duration.eql?`.
- **Namespaced builtins** carry the namespace: `JSON.parse`,
  `Regex.match`, `Time.parse`, `Duration.build`, `Tasks.run`.
- **Global builtins** use their bare name: `uuid`, `random_id`,
  `to_int`, `now`, `assert`, `require`.
- **Module loader errors** are prefixed `require: `.
- **Strict-effects violations** are prefixed `strict effects: `.

## Message families

Use the family whose semantic matches; do not invent a new phrasing for
a semantic that already has one.

### Parse errors

| Family | Template | When |
| --- | --- | --- |
| Expectation | `expected X, got Y` | The parser needed a specific token or construct (`expected end, got EOF`). Always via `errorExpected`. |
| Unexpected token | `unexpected token X` | No parse rule applies at this token. |
| Invalid construct | `invalid X` | A literal or construct is malformed (`invalid integer literal`). |
| Missing piece | `missing value for X` | A construct started but a required part is absent (`missing value for keyword argument foo`). |
| Duplicate | `duplicate X NAME` | The same name is declared twice (`duplicate shape field id`). |
| Placement | `X is only supported ...` / `X cannot be used with ...` / `X expects ...` | A keyword appears in the wrong position (`export is only supported for top-level functions`). |
| Structural requirement | `X must ...` / `X requires ...` | A construct is well-formed but violates a rule (`enum Status must define at least one member`, `begin requires rescue and/or ensure`). |
| Unknown name | `unknown X NAME` | A name is not recognized (`unknown rescue error type Foo`). |

### Runtime errors

| Family | Template | When |
| --- | --- | --- |
| Arity / shape | `X expects Y` | The call shape is wrong: wrong count or wrong general form (`string.sub expects pattern and replacement`, `array.fetch expects index and optional default`, `random_id expects at most one length argument`). |
| Positional refusal | `X does not take arguments` | The callable accepts no positional arguments at all. |
| Positional refusal (kwargs allowed) | `X does not take positional arguments` | The callable rejects positional arguments but accepts keyword arguments (`Time.now does not take positional arguments`). |
| Keyword refusal | `X does not accept keyword arguments` | No keyword arguments are accepted. |
| Unknown keyword | `X unknown keyword argument NAME` | Keyword arguments are accepted, but not this one. |
| Block refusal | `X does not accept blocks` | A block was passed to something that never takes one. |
| Block requirement | `X requires a block` | A block is mandatory and missing. |
| Requirement | `X requires Y` | Some other input is mandatory and missing (`assert requires a condition argument`). |
| Constraint | `X must be Y` | An argument arrived but has the wrong type or range (`string.sub pattern must be string`, `array.chunk size must be a positive integer`, `index must be integer`). |
| Type mismatch | `expected T, got U` | Declared-type checks. Composes as `argument NAME expected T, got U` for parameters, `return value for FN expected T, got U` for returns, and `LABEL expected T, got U` for capability contracts. Types render in Vibescript surface syntax (see below). |
| Unsupported operation | `unsupported X` | The language defines no behavior for this combination (`unsupported addition operands`, `unsupported member access on int`). Use for refused operations, not malformed input. |
| Unknown lookup | `unknown X NAME` | A member, method, type, or enum lookup failed (`unknown string method foo`, `unknown type Foo`). Variables use the dedicated spelling `undefined variable NAME`; functions and modules use `... not found` (`function foo not found`, `require: module "foo" not found`). |
| Invalid input | `invalid X` / `X invalid Y: DETAIL` | A value is malformed for its purpose (`invalid assignment target`, `JSON.parse invalid JSON: ...`, `string.match invalid regex: ...`). |
| Forbidden state | `cannot X` | An operation is forbidden on this value or in this state (`cannot index int`, `cannot iterate over nil`, `break cannot cross call boundary`, `task handle cannot be used after task scope exits`). |
| Guard limits | `X exceeds limit N bytes` / `... quota exceeded (N)` / `... exceeded (limit N)` | Sandbox limits. These messages were audited recently; keep their exact shapes (`step quota exceeded (50000)`, `memory quota exceeded (1048576 bytes)`, `recursion depth exceeded (limit 200)`, `string.gsub output exceeds limit 1048576 bytes`). |
| Duplicate | `duplicate X NAME` | Compile-time redefinition (`duplicate function foo`, `duplicate top-level name Foo`). |
| Domain-specific | fixed strings | Arithmetic and bounds keep their conventional short forms: `division by zero`, `modulo by zero`, `array index out of bounds`, `string index out of bounds`, `money currency mismatch` (arithmetic, from vibes/value), `money currency mismatch for comparison` (comparisons, from the runtime). |

Rendering rules inside messages:

- **Value kinds** render via `Kind.String()` (lowercase: `int`,
  `string`, `hash`).
- **Types in mismatch messages** render in Vibescript surface syntax:
  expected types via `ast.FormatTypeExpr` (`array<int>`,
  `{ id: string, score: int }`), actual values via
  `formatValueTypeExpr`, which includes element/shape detail
  (`array<int | string>`) and collapses cycles (`array<...>`).
- **Script-provided names** (members, keywords, variables) are
  interpolated bare, not quoted. Host-provided strings that may be
  empty or contain whitespace (module names, aliases, JSON keys) are
  quoted with `%q`.
- Messages start lowercase unless the subject is a capitalized name
  (`JSON.parse ...`, `Time.parse ...`), carry no trailing period, and
  use Go `%w` wrapping for upstream causes (`invalid time: %w`).

## Positions and code frames

- **Parse errors** render as `parse error at LINE:COL: MESSAGE`
  followed by a code frame. Multiple parse errors are joined by a
  blank line; each keeps its own frame.
- **Runtime errors** render the bare message first, then the code
  frame, then stack frames as `  at FUNCTION (LINE:COL)` (or
  `(line N)` when the column is unknown). Stacks longer than 16 frames
  elide the middle: 8 head frames, `... N frames omitted ...`, 8 tail
  frames. Top-level failures use the synthetic function name
  `<script>`.
- **Code frames** are produced by `vibes/source.FormatCodeFrame`:

  ```text
    --> line 3, column 9
   3 |   a / b
     |         ^
  ```

- Positions are 1-indexed line and column throughout. Never embed
  positions inside the message text itself; they belong to the
  rendering layer (`parse error at ...` prefix, stack frames) and to
  the structured carriers (`ParseIssue.Pos/End`, `StackFrame.Pos`).

## The did-you-mean suffix

Lookup-failure messages (unknown method/member, undefined variable,
function not found, module not found) may append a suggestion suffix
built by `didYouMean` in `internal/runtime/suggest.go`:

```text
unknown int method tims (did you mean "times"?)
```

Rules:

- The suffix is strictly additive: the base message must read
  correctly without it, and matchers must not rely on it.
- It is only for lookup failures, never for constraint or arity
  errors.
- Candidates are quoted, capped at three, ranked by edit distance.
- Do not hand-roll suggestions; pass the candidate list to
  `didYouMean` so thresholds stay uniform.

## Programmatic matching

Hosts should never scrape message text. The supported channels today:

- **Parse errors:** `vibes.ParseIssues(err)` extracts the structured
  failures behind a `Compile` error — start position, optional
  end-of-token position, and the bare message — in source order. It
  returns nil for compile failures that carry no positions (size
  limits, duplicate top-level names).
- **Runtime errors:** `errors.As(err, &re)` with
  `*vibes.RuntimeError` exposes `Type`, `Message`, `CodeFrame`, and
  `Frames`. `Type` is the stable taxonomy: `RuntimeError`
  (everything), `AssertionError` (failed `assert`), and `LimitError`
  (step quota, memory quota, and recursion-limit terminations),
  canonicalized by `ast.CanonicalRuntimeErrorType`. The same names are
  what scripts match in `rescue(...)` clauses, so the script-facing and
  host-facing taxonomies are one system. Extend it by adding a constant
  in `internal/ast/errortypes.go` and a classification rule in
  `classifyRuntimeErrorType` — do not invent a parallel code registry.

### Error codes before 1.0

A per-message string-code registry is **not** recommended. It would
freeze exactly the thing this document tries to keep flexible, and the
existing `Type` field already provides the right extension point.

Guard-limit terminations use the canonical `LimitError` type. Hosts
that need to bill, retry, or log quota-killed scripts differently from
buggy scripts should branch on `RuntimeError.Type`, not message text
and not `errors.Is`; `RuntimeError.Unwrap` intentionally returns nil.

Parse-error codes are likewise not needed now: hosts display parse
errors rather than branch on them, and `ParseIssue` can grow an
optional `Code` field later without breaking anyone.

## Known outliers (follow-up candidates)

Deliberately left in place to keep the normalization diff reviewable;
align them opportunistically when touching the surrounding code:

- `internal/runtime/members_temporal.go`: `format expects a Go layout
  string` and `round`/`ceil`/`floor` `does not accept precision` lack
  the `time.` receiver prefix required by the subject rule.
- `internal/runtime/values.go`: `comparator must be numeric` is only
  ever re-wrapped by its caller (`array.sort block must return numeric
  comparator`) and never surfaces as-is.
- `internal/runtime/eval.go`: `block required` is the prefix-less
  fallback of `ensureBlock` for the host-facing `CallBlock` path,
  where no callable name exists.
- `internal/runtime/builtins.go` / `compile.go`: `JSON.parse
  unsupported value type %T` and `unsupported top-level statement %T`
  render Go type names. Both are defensive internal-invariant paths
  believed unreachable from scripts; if either becomes reachable it
  must switch to kind rendering.
