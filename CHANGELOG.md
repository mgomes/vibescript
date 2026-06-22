# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- Ongoing work toward the next pre-1.0 release.
- **Added: Ruby-style range query and conversion helpers.** Ranges now answer
  `cover?`, `include?`, and `member?` membership predicates (numeric arguments
  are tested against the range bounds, exclusivity, and direction; any other
  type is never a member and returns `false` rather than raising), the metadata
  helpers `first`, `last`, `size`, and `exclude_end?`, and the `to_a`
  materialization. Because Vibescript iterates descending ranges such as `5..1`,
  `size`, `to_a`, `first(n)`, and `last(n)` report that descending sequence
  rather than the empty result Ruby produces; the remaining helpers match Ruby.
  `to_a` and the counted `first(n)`/`last(n)` forms build their arrays under the
  sandbox step and memory quotas so large ranges fail safely instead of
  exhausting memory.
- **Added: Ruby-style `Time` HTTP/XML/RFC date helpers.** `Time#xmlschema` is an
  alias for `Time#iso8601` (including its optional `ndigits` precision argument).
  `Time#httpdate` renders the HTTP-date / IMF-fixdate form (RFC 7231), always in
  GMT, e.g. `"Tue, 02 Jan 2024 03:04:05 GMT"`. `Time#rfc2822` and its alias
  `Time#rfc822` render the RFC 2822 mail date preserving the receiver's zone
  offset; a genuine UTC receiver uses the `-0000` zone Ruby reserves for
  timestamps without real zone information while an explicit zero offset uses
  `+0000`. `httpdate`, `rfc2822`, and `rfc822` drop sub-second precision and take
  no arguments, raising on any positional or keyword argument.
- **Added: Ruby-style `String#chop` and `String#chop!`.** `chop` removes the
  last character, treating a trailing `"\r\n"` as a single record separator and
  otherwise removing one full Unicode character rather than one byte; an empty
  string is returned unchanged. `chop!` returns the chopped string and returns
  `nil` when there is nothing to remove (the empty-string case), matching the
  existing copy-on-transform bang helper convention.
- **Added: Ruby-style `Array#transpose`.** `transpose` swaps the rows and
  columns of a matrix made of equal-length array rows, so
  `[[1, 2], [3, 4]].transpose` returns `[[1, 3], [2, 4]]`. An empty array
  transposes to `[]`, and rows of zero length collapse to no columns. It
  rejects extra arguments, raises when any element is not an array, and
  raises when the rows differ in length, reporting the offending index.
- **Added: Ruby-style float special values and division-by-zero behavior.** Float
  division by zero with the `/` operator (and `Float#fdiv`/`Integer#fdiv`) now
  follows IEEE 754 like Ruby instead of raising: a finite nonzero numerator
  yields `Infinity` or `-Infinity` and a zero numerator yields `NaN`. Integer
  division by zero (`1 / 0`) still raises, matching Ruby's `ZeroDivisionError`.
  Floats gained `nan?` (true only for `NaN`), `infinite?` (`1` for `Infinity`,
  `-1` for `-Infinity`, `nil` otherwise), and `finite?` (true when neither
  infinite nor `NaN`). Special values print as `Infinity`, `-Infinity`, and
  `NaN`, and `JSON.stringify` continues to reject non-finite floats because JSON
  has no representation for them. `div`, `divmod`, `modulo`, and `remainder` keep
  raising on a zero divisor, matching Ruby. Comparisons follow IEEE 754:
  comparisons against `NaN` are unordered, so `<`, `<=`, `>`, and `>=` return
  `false` and the spaceship operator `<=>` returns `nil`. Coercing a non-finite
  float to an integer now raises rather than silently yielding a garbage value,
  so a `NaN`/`Infinity` range endpoint, `money_cents` amount, or duration operand
  reports a clear error.
- **Added: Ruby-style string padding helpers.** `String#center`, `String#ljust`,
  and `String#rjust` pad a string to a requested width, defaulting to a single
  space and accepting a custom pad string that is repeated and truncated to fill
  the span. Width is measured in characters (Unicode code points) like
  Vibescript's other string methods, a `Float` width is truncated toward zero as
  Ruby does, a width at or below the receiver's length returns it unchanged, and
  an empty pad string is rejected. Oversized widths are checked against the
  memory quota before any buffer is allocated, so they fail fast instead of
  materializing a huge string.
- **Added: Ruby-style subsecond parts for `Time.local`, `mktime`, `utc`, and
  `gm`.** These calendar constructors now read their seventh positional argument
  as microseconds-with-fraction instead of routing it through timezone parsing.
  Integer microseconds are exact and floats carry sub-microsecond precision down
  to the nanosecond, while a non-numeric microsecond argument raises a runtime
  error. `Time.new` keeps its Ruby distinction of accepting a zone/offset in the
  seventh position. Unlike Ruby, a string microsecond argument is rejected rather
  than coerced via leading-digit parsing.
- **Added: Ruby-style `Array#each_slice`, `each_cons`, `reverse_each`, and
  `cycle`.** `each_slice(n)` yields non-overlapping slices (including a shorter
  trailing slice) and `each_cons(n)` yields sliding windows; both require a
  positive integer size and yield freshly copied arrays that do not alias the
  receiver. `reverse_each` yields values in reverse index order and returns the
  receiver. `cycle(n)` repeats the array `n` times (a non-positive count is a
  no-op like Ruby), while omitting the count or passing `nil` cycles forever; the
  cycle charges a step per yield so the step quota and context cancellation bound
  even an empty block body. The slice/window/cycle forms return `nil` to match
  Ruby.
- **Fixed: `Hash#except` ignores unsupported key types like Ruby misses.**
  `Hash#except` no longer raises when given an argument whose type cannot be a
  hash key (anything other than a symbol or string). Because Vibescript hash keys
  are only symbols or strings, such an argument can never match an entry, so it is
  now treated as a Ruby-style miss and ignored. Mixed argument lists still exclude
  the supported keys, so `{ a: 1 }.except(1)` returns `{ a: 1 }` while
  `{ a: 1 }.except(1, :a)` returns `{}`.
- **Fixed: `Hash#slice` omits unsupported candidate keys like Ruby misses.**
  `Hash#slice` no longer raises when given a candidate key whose type cannot be a
  hash key (anything other than a symbol or string). Such a candidate can never
  match an entry, so it is treated as a Ruby-style miss and dropped from the
  result instead of failing. Mixed argument lists still keep the supported keys,
  so `{ a: 1, b: 2 }.slice(:a, 1)` returns `{ a: 1 }` while
  `{ a: 1 }.slice(1)` and `{ a: 1 }.slice` both return `{}`.
- **Added: Ruby-style numeric rounding precision.** `Float#round`, `Float#floor`,
  and `Float#ceil` now accept an optional Integer precision: positive `ndigits`
  keep the value a float rounded to that many fractional digits, while zero or
  negative `ndigits` return an integer bucketed to a power of ten. `Integer#round`,
  `Integer#floor`, and `Integer#ceil` gained the same precision argument, leaving
  the value unchanged for non-negative precision and bucketing for negative
  precision. Float rounding matches Ruby's half-away-from-zero correction, and
  any conversion back to an integer keeps the existing 64-bit overflow checks
  instead of widening like Ruby's bignums.
- **Added: Ruby-style numeric division helpers.** Integers and floats now expose
  `div` (floored division returning an integer), `divmod` (the floored quotient
  paired with the divisor-signed modulo), `fdiv` (floating division), and
  `remainder` (truncated-division remainder whose sign follows the receiver, so
  it differs from `%` for mixed-sign operands). A zero divisor errors for all
  four, and quotients outside the 64-bit range error rather than wrapping. Ruby's
  `fdiv` infinity result is intentionally an error instead, matching the `/`
  operator, and `quo` is intentionally omitted because Vibescript has no rational
  number type.
- **Added: Ruby-style `String#casecmp` and `String#casecmp?`.** `casecmp`
  case-insensitively compares two strings (folding only ASCII letters and
  comparing other bytes ordinally) and returns `-1`, `0`, `1`, or `nil` for a
  non-string argument, matching Ruby. `casecmp?` returns a boolean using Unicode
  simple case folding (consistent with `upcase`/`downcase`) or `nil` for a
  non-string argument; full-fold expansions such as `ß` matching `SS` are not
  applied. When either operand contains invalid UTF-8, `casecmp?` folds
  byte-wise over ASCII letters so distinct byte sequences stay distinct,
  preserving byte identity like Ruby's binary-string path.
- **Added: Ruby-style `Array#reject`, `take_while`, `drop_while`, `grep`, and
  `grep_v`.** `reject` is the inverse of `select`; `take_while` and `drop_while`
  split on the first block miss with early-stop semantics; `grep` and `grep_v`
  filter using the language's case-equality direction (`pattern === element`,
  the same matcher as `case`/`when`), so a `Range` matches by membership and
  other values by equality, with an optional block transforming each kept
  element. Regex patterns are not yet available, so string patterns match by
  equality rather than substring.
- **Added: Ruby-style `Time#iso8601(ndigits)` precision.** `Time#iso8601` and its
  `Time#rfc3339` alias now accept an optional non-negative `ndigits` argument that
  appends fractional-second digits, truncated toward zero like Ruby. No argument
  keeps whole-second RFC3339 output, the timezone offset is preserved, and digits
  beyond nanosecond resolution are zero-padded (capped at 100 digits to bound
  allocations). Negative, non-integer, out-of-range, or extra arguments raise a
  clear runtime error.
- **Added: Ruby-style `values_at`, `zip`, `take`, and `drop` collection helpers.**
  `Hash#values_at(*keys)` returns values in requested key order with `nil` for
  missing keys. `Array#zip(*arrays)` combines arrays element-wise into rows keyed
  to the receiver's length, padding short arrays with `nil` and rejecting
  non-array arguments. `Array#take(n)` and `Array#drop(n)` return prefix and
  suffix slices without mutating the receiver, truncating fractional counts like
  Ruby's `to_int` conversion and rejecting negative counts.
- **Added: Ruby-style offset arguments for `Time#getlocal` and
  `Time#localtime`.** Both now accept an optional timezone offset (for example
  `"+05:30"`, `"-04:00"`, a named zone, or `"UTC"`) and return the same instant
  in that zone, falling back to the host's local zone when the argument is
  omitted or `nil`. The offset uses the shared zone-parsing rules, and the
  receiver is never mutated, so `localtime` fits Vibescript's immutable value
  model while matching Ruby's non-mutating `getlocal(offset)` result.
- **Added: Ruby-style `String#partition` and `String#rpartition`.** Both split a
  string into a three-element `[head, separator, tail]` triple around the first
  (`partition`) or last (`rpartition`) occurrence of the separator. A missing
  separator keeps the whole string on the head (`partition`) or tail
  (`rpartition`) with empty surrounding segments, and an empty separator matches
  at the start or end respectively, matching Ruby. The separator must be a
  string.
- **Added: Ruby-style `Hash#fetch_values`.** `Hash#fetch_values(*keys)` returns
  the values for several keys at once, in the requested order. Unlike
  `values_at`, it raises a `key not found` error for any missing key; pass a
  block to compute a replacement value for each missing key instead of raising.
- **Added: Ruby-style `Time#to_a` tuple conversion.** `Time#to_a` returns the
  positional field tuple `[sec, min, hour, mday, month, year, wday, yday, isdst,
  zone]`, matching Ruby for compatibility with positional field processing. Field
  values reuse the existing `Time` accessors, so UTC, local, and offset receivers
  stay consistent across both forms.
- **Added: Ruby-style `String#chars` and `String#lines`.** `chars` returns an
  array of the string's Unicode characters using the existing rune-aware
  semantics, and `lines` splits on `"\n"` while retaining the trailing newline
  on each line, leaving carriage returns attached so `"\r\n"` endings round-trip.
- **Added: Ruby-style hash member, value, and store helpers.** `Hash#member?`
  joins `key?`/`has_key?`/`include?` as a key-membership alias, `Hash#value?` and
  `Hash#has_value?` report value membership using the same `==` equality as the
  rest of the language, and `Hash#store(key, value)` returns a new hash with the
  key assigned. Like the other method-based hash helpers, `store` is
  immutable-style and leaves the receiver unchanged.
- **Added: Ruby-style conflict blocks for `Hash#merge`.** `Hash#merge` now
  honors an optional block to resolve key conflicts: for keys present in both
  hashes the block is yielded `(key, old_value, new_value)` and its result is
  stored, while keys present on only one side are copied without invoking the
  block. Without a block the incoming hash still wins on conflicts. The conflict
  key is yielded as a symbol, matching the other hash helpers, and the block was
  previously accepted but silently ignored.
- **Added: Ruby-style `call` on function values.** A function value now exposes
  a `call` member so `fn.call(...)` mirrors direct `fn(...)` invocation,
  forwarding positional arguments, keyword arguments, and an optional block.
  Arity and type errors stay anchored at the call site, and `call` is the only
  member offered (with a "did you mean" hint for typos).
- **Hardened CLI source-size enforcement.** `vibes run`, `vibes analyze`, and
  `vibes test` now read each script through a single size-checked descriptor,
  bounded at the engine's configured source-size limit, so an oversized file
  (even one swapped or grown after the check) is rejected before it is loaded
  fully into memory.
- **Improved: Ruby-style `String#start_with?` and `String#end_with?`.** Both
  predicates now accept one or more string candidates and return true when any
  matches. Candidates are checked left to right and matching short-circuits like
  Ruby, so a non-string candidate is only rejected if reached before a match.
- **Hardened the public jobqueue option parser.** `jobqueue.ParseEnqueueOptions`
  now rejects extra enqueue keywords that are not data-only or that contain
  cyclic references instead of cloning them through to the host, closing a
  contract gap for embedders that call it directly. A new
  `jobqueue.ParseEnqueueOptionsValidated` fast path lets the runtime adapter skip
  the redundant walk when it has already enforced the contract, and the carved
  package gained direct unit tests for constructor validation, retry detection,
  option parsing, cloning, and invalid/cyclic values.
- **Added: `Time#round` precision argument.** `Time#round` now accepts an
  optional Ruby-style `ndigits` (defaulting to `0`) so `round(3)` and `round(6)`
  produce millisecond and microsecond precision, with non-negative `Integer`
  validation and clear errors on misuse.
- **Fixed: Hash membership predicates align with Ruby.** `Hash#key?`,
  `Hash#has_key?`, and `Hash#include?` now return `false` for candidate keys of
  unsupported types instead of raising, matching Ruby's predicate semantics.
- **Added: Ruby-style numeric predicate and successor helpers.** Integers and
  floats gain `zero?`, `positive?`, `negative?`, and `nonzero?` (returning the
  receiver or `nil`), and integers gain `next`/`succ` and `pred`.
- **Added: Ruby-style `Array#min`, `#max`, `#minmax`, `#min_by`, and `#max_by`.**
  The extrema helpers reuse the comparison semantics of `sort`/`sort_by`, return
  `nil` (or `[nil, nil]` for `minmax`) on empty arrays, resolve ties to the first
  matching element, participate in step/cancellation accounting for the block
  forms, and raise clear errors on incomparable mixed values.

## v0.50.0 - 2026-06-11

- **Added: stronger CLI workflows.** `vibes run` now supports inline `-e`
  evaluation and recursive `-watch` mode, and the new `vibes test` command runs
  `.vibe` test files with module-aware fixture coverage.
- **Added: broader LSP support.** The language server now exposes document
  formatting through `vibes fmt`, context-aware completion, signature help,
  go-to-definition, and document symbols, with live-buffer re-anchoring for
  completion and navigation targets.
- **Improved host-facing diagnostics.** Structured parse-error positions are
  available through the public API and LSP, inline snippets remap diagnostics to
  user source positions, lookup failures include did-you-mean suggestions, and
  runtime error wording now follows the documented error-message conventions.
- **Fixed parser and runtime edge cases.** Newline statement boundaries no longer
  accidentally chain line-start expressions, line-ending minus continuations are
  preserved, trailing brace blocks parse correctly, `case` ranges match by
  membership, enum value kinds render distinctly, and hash method names win over
  colliding hash keys in member dispatch.
- **Hardened runtime accounting and arithmetic.** Sandbox limit terminations are
  classified distinctly, capability argument validation runs once while still
  counting validated arguments against memory quota, and integer, duration, and
  time arithmetic now reject `int64` overflow instead of silently wrapping.
- **Improved runtime performance.** Per-call environment and builtin churn,
  memory-estimation work, regex compilation, step context polling, builtin member
  dispatch allocation, and scalar-key array set operations were all tightened
  with regression coverage and benchmark-focused checks.
- **Expanded quality gates and documentation.** CI now enforces coverage, the
  stdlib and LSP documentation were expanded, benchmark artifacts and hotspot
  profiles were refreshed, input-guard limits were centralized, module
  containment edge cases were pinned, and public facade/value package tests were
  added.

## v0.40.0 - 2026-06-06

- **Added: `Tasks` structured concurrency for bounded in-script fanout.**
  Scripts can now use `Tasks.run` to create an automatically awaited task scope,
  `tasks.spawn` to start named function calls, `task.value` to wait for a single
  result, and `Tasks.map` to collect ordered concurrent results.
- **Added host-controlled task concurrency settings.**
  `Config.DefaultTaskConcurrency` defaults task fanout to `4` unless the host
  cap is lower, and `Config.MaxTaskConcurrency` caps script-provided `max:`
  overrides. Requests above the host cap raise an error instead of being
  silently clamped.
- **Hardened task isolation, cancellation, and quota accounting.** Task
  arguments, keyword arguments, results, and inherited mutable globals are cloned
  across task boundaries; task failures propagate through handles or scope exit;
  retained task results count against the parent memory quota while the task
  scope is alive.
- Added a Tasks ADR, README and host-cookbook coverage, a runnable Tasks example,
  `# vibe: 0.4` example headers, deterministic `testing/synctest` coverage for
  concurrency behavior, and a Go 1.26 goroutine leak profile CI gate.

## v0.31.0 - 2026-05-30

- **Fixed: `Money` arithmetic now rejects `int64` overflow instead of silently
  wrapping.** `Add`, `Sub`, `MulInt`, and `DivInt` detect overflow (including
  the `-MinInt64` and `MinInt64 / -1` edges) and return an error, matching the
  range check `ParseMoneyLiteral` already enforces. **Breaking (embedders):
  `value.Money.MulInt` now returns `(Money, error)` instead of `Money`; update
  call sites to handle the error.** Plain integer arithmetic in scripts still
  wraps — money is deliberately stricter.
- **Fixed: deeply nested type annotations can no longer crash the host.** The
  parser bounds type-annotation recursion at depth 64 and emits a normal parse
  error (`type annotation nesting too deep`) instead of overflowing the
  goroutine stack on attacker-supplied source reached through `Engine.Compile`.
- **Fixed: capability contracts now follow builtins captured in closures and
  blocks.** The contract scanner descends into script-function and block
  environments — with a cycle guard and an ambient-global stop — so a contracted
  builtin captured in a closure no longer escapes its `ValidateArgs` /
  `ValidateReturn` enforcement, and an unrelated same-named global is never bound
  to a capability scope through a script-supplied closure. Defense-in-depth: no
  bundled capability returns a closure-wrapped builtin, so default embedders were
  not exposed.

## v0.30.0 - 2026-05-30

- **Fixed: `||` and `&&` now return the surviving operand, not a coerced
  boolean.** `a || b` is `a ? a : b` and `a && b` is `a ? b : a` (Ruby
  semantics), so the documented `value = optional || default` idiom works.
  Previously both collapsed to `true`/`false`. Truthiness rules are unchanged.

## v0.29.0 - 2026-05-17

- Completed the embedder-facing package refactor: value-system types now live
  in `github.com/mgomes/vibescript/vibes/value`, capability adapter contracts
  live under `vibes/capability/{contextcap,db,events,jobqueue}`, and source
  positions live in `vibes/source`.
- Hid the AST, parser, runtime engine, module loader, builtins, and runtime
  capability adapters under `internal/` packages so the public API is centered
  on `vibes.Engine`, `vibes.Script`, capability construction, runtime errors,
  and value/capability subpackages.
- **Breaking (embedders): removed the v0.28 deprecation alias bridge.**
  Update imports from root `vibes` to the new subpackages:
  - `vibes.Value`, `Money`, `Duration`, `Range`, `KindXxx`, and `NewXxx` move
    to `github.com/mgomes/vibescript/vibes/value`.
  - `vibes.Database`, `DatabaseReader`, `DatabaseWriter`, `DBFindRequest`, and
    related database request/result types move to
    `github.com/mgomes/vibescript/vibes/capability/db`.
  - `vibes.EventPublisher` and `EventPublishRequest` move to
    `github.com/mgomes/vibescript/vibes/capability/events` as
    `events.Publisher` and `events.PublishRequest`.
  - `vibes.JobQueue`, `vibes.JobQueueWithRetry`, `vibes.JobQueueJob`,
    `vibes.JobQueueEnqueueOptions`, and `vibes.JobQueueRetryRequest` move to
    `github.com/mgomes/vibescript/vibes/capability/jobqueue` as
    `jobqueue.JobQueue`, `jobqueue.JobQueueWithRetry`,
    `jobqueue.JobQueueJob`, `jobqueue.JobQueueEnqueueOptions`, and
    `jobqueue.JobQueueRetryRequest`.
  - `vibes.ContextCapabilityResolver` moves to
    `github.com/mgomes/vibescript/vibes/capability/contextcap` as
    `contextcap.Resolver`.
  - Previously deprecated AST/parser types under `vibes` are removed; drive
    scripts through `vibes.Engine` and `vibes.Script` instead.
- **Breaking (embedders): root `vibes` no longer exports direct runtime payload
  constructors or accessors** for blocks, classes, instances, enums, or script
  functions. Use the documented engine/script APIs and typed payload markers on
  `value.Value`.
- Updated `Value` runtime-bound accessors so builtin, class, instance, function,
  block, enum, and enum-value accessors return `value.*Payload` marker
  interfaces instead of concrete runtime types. Data-only accessors such as
  `Bool`, `Int`, `Float`, `String`, `Array`, `Hash`, `Money`, `Duration`,
  `Time`, and `Range` are unchanged.
- Extracted `cmd/vibes analyze` into `internal/tools/analyze`, added CLI package
  documentation, added public API Godoc examples, and documented `vibes/value`
  as the home for `Money`, `Duration`, and `Range`.
- Added a `golangci-lint` baseline, opt-in pre-commit hook, contribution guide,
  and stronger lint/benchmark automation.
- Modernized the test suite with focused internal runtime/parser/AST packages,
  table-driven cases, `cmp.Diff` snapshots, shared CLI helpers, Godoc examples,
  and broader safe `t.Parallel` coverage.

## v0.28.2 - 2026-05-16

- Fixed a quadratic `combineErrors` path where invalid-UTF-8 input drove CPU usage that scaled with the square of the number of parse errors, closing a cheap server-side DoS vector.
- Closed a module-policy bypass where require arguments that normalized to empty (e.g. `.vibe`, `..vibe`, `0.vibe.vibe`) silently skipped allow/deny enforcement.
- Aligned module-policy normalization with the loader by stripping at most one implicit `.vibe` so allow-lists no longer widen to sibling files like `helper.vibe.vibe` or `pkg/..vibe`.
- Added the fuzz-minimized regression inputs to the committed corpus and expanded the policy invariant tests so the bypass classes are pinned for future runs.

## v0.28.1 - 2026-05-15

- Fixed module policy normalization so whitespace-only path segments cannot produce non-idempotent policy patterns or module names.
- Added the fuzz-minimized module policy case to the committed corpus so the regression is replayed by normal tests and future fuzz runs.

## v0.28.0 - 2026-05-15

- Added broad fuzz coverage across command input paths, formatting, lexing, parsing, compilation, runtime execution, JSON/value conversion, module handling, capability validation, and scalar input helpers.
- Added a `just fuzz` sweep with a 10-second default and a nightly GitHub Actions fuzz workflow for heavier automated coverage.
- Raised the LSP JSON-RPC payload cap so valid near-1 MiB source files are not rejected solely because of protocol framing overhead.
- Restored dot access for keyword-named hash/object members loaded from JSON or remapped data, such as `payload.raise` and `payload.begin`.

## v0.27.0 - 2026-05-04

- Hardened engine API snapshot boundaries so caller-mutated snapshots cannot corrupt later executions, including deep-cloned object-valued builtin tables.
- Tightened module containment by freezing configured module roots at engine creation, preventing cwd/symlink drift, and rejecting non-regular module files before reading.
- Aligned regex-based string helpers with the guarded `Regex` builtins for pattern, input, replacement, and output size limits.
- Added containment coverage for cyclic host arrays, mutable API snapshots, module root drift, regex guard bypasses, and related breakout paths while preserving benchmark smoke gates.
- Cleaned up Go API boundary and test hygiene with stronger error matching, interface checks, documentation, and focused performance follow-ups.

## v0.26.2 - 2026-03-08

- Fixed newline-sensitive parsing in control-flow headers and statement expressions so next-line literals no longer get consumed accidentally while explicit multiline continuations still work.
- Made `&&` and `||` short-circuit lazily and aligned integer division/modulo with Ruby-style floor semantics for signed integer algorithm ports.
- Added Ruby-friendly array query aliases and helpers with `length`, `empty?`, and `fetch`, plus stricter `array.fetch` index validation.
- Expanded regression coverage for Rosetta-style examples with multiline header parsing, short-circuit guards, signed integer arithmetic, and array helper behavior.

## v0.21.0 - 2026-03-08

- Added nominal enums with `::` member access, reflective member helpers, and typed symbol coercion across function and block boundaries.
- Hardened enum and type normalization with case-insensitive resolution, stricter enum-name validation, shadowed-scope lookup fixes, union/hash-key fast-path fixes, and recursive normalization guards.
- Added runnable enum examples and integration coverage, upgraded the REPL to Bubble Tea v2, and added a `just install` recipe for the CLI.
- Strengthened release and quality automation with a race-detector lane, fuzz and benchmark gate tuning, editor support docs, and idempotent release tag reruns.

## v0.20.0 - 2026-02-23

- Runtime call-path performance and benchmark-gating improvements ahead of 1.0.

## v0.1.0 - 2026-02-19

- Initial pre-1.0 project baseline and public documentation set.
