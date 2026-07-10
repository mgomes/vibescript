# ADR-004: Infer local types and check typed boundaries statically

## Status

Accepted - 2026-07-09

## Decision

Vibescript will add implicit local type inference to the check path. Locals take
the types of the expressions assigned to them, annotations remain compile-time
facts, and the checker reports an error wherever known types contradict. The
current CLI exposes this path through `vibes run -check`; a future top-level
`vibes check` command should use the same semantic contract.

The governing principle is: **error on known contradictions, permit unknowns,
and make unknowns easy to validate at boundaries.**

To make boundary validation ergonomic, Vibescript will also add
`JSON.parse_as(raw, shape)`: it parses JSON and validates the result against a
shape in one step, and the checker treats its result as that shape type.

This is still gradual typing, with a substantially better inference engine. It
is not a switch to whole-program static model.

## Context

Vibescript supports optional type annotations and enforces them at runtime. The
checker catches direct contract violations such as a string literal passed to an
`int` parameter:

```vibe
def takes_int(value: int)
  value
end

takes_int("1")
```

But it does not carry local type facts through bindings:

```vibe
def takes_int(value: int)
  value
end

value = "1"
takes_int(value)
```

This program passes `vibes run -check` today and fails only when executed. That
is the wrong contract for deployment gates and for AI-generated code: if a host
can check a script before deployment, every statically knowable typed-boundary
error should be rejected before runtime.

Crystal proves that a Ruby-shaped language can infer nearly everything, but its
inference works because every expression must ultimately have a compile-time
type. That requirement is exactly what makes JSON painful in Crystal: dynamic
payloads become `JSON::Any`, unions, casts, or declared serializable structs.
Adopting that model wholesale would reintroduce the same complexity precisely
where Vibescript's main use case lives — host payloads, loose JSON, and
capability data flowing into short scripts. Vibescript also keeps Ruby-like
runtime semantics: values are dynamically tagged, hosts inject globals and
capabilities, and dispatch can be dynamic.

The problem, then, is to get Crystal's useful inference for local code without
inheriting its requirement that every value be statically typed.

## Design

Locals receive inferred types, and known contradictions are static errors:

```vibe
name = "Mauricio"   # inferred string
count = 1           # inferred int
count = name        # check error: count is int, name is string
```

Typed boundaries remain optional:

```vibe
def create_user(name: string) -> User
  User.new(name)
end
```

Dynamic inputs remain allowed:

```vibe
body = JSON.parse(raw)
create_user(body["name"])
```

Here `body["name"]` is unknown statically, so `vibes check` accepts the call and
`create_user` validates the value at runtime, exactly as it does today. Unknown
values are never rejected merely because the checker cannot prove their type.

Apps wanting stronger guarantees validate once at the edge:

```vibe
body = JSON.parse_as(raw, {
  name: string,
  email: string
})

create_user(body["name"])
```

After that validation, `body` is a known shape, `body["name"]` is a known
`string`, and everything downstream is inferred and checked. Shape-annotated
parameters already provide the same edge validation at function boundaries;
`JSON.parse_as` covers the common inline case where a payload is parsed and
consumed in the same scope.

Concretely, the checker maintains a local type environment while walking
reachable code:

- Assignments bind the inferred type of the right-hand side to the local.
- Sequential reassignment to a conflicting type is a static error; assignments
  in sibling branches merge into unions.
- Annotated parameters enter the function body with their declared type.
- Known function calls check argument types and expose their annotated return
  type to the caller.
- Annotated returns check the inferred type of explicit and implicit return
  expressions.
- Operators reject operands known to be invalid.
- Shape-typed values carry field-level facts, so indexing with a known key
  yields the field's type.
- `nil` checks and kind predicates may narrow union types over time.

When the checker proves a violation, `vibes check` reports an error such as:

```text
call to takes_int argument value expected int, got string
```

This design takes the useful parts of Crystal:

- Locals receive inferred types.
- Operators reject known-invalid operands.
- Calls and returns enforce declared contracts.
- AI-generated code gets concrete diagnostics from `vibes check`.

And it preserves the useful parts of a scripting language:

- Function annotations remain optional.
- Untyped JSON and host values can flow dynamically.
- Unknown values are not rejected merely because the checker cannot prove their
  type.
- Runtime checks cover the uncertainty.

`any` remains an explicit escape hatch: it tells the checker to stop proving
facts about a value. Deployment policies may warn or error on `any` at public
entrypoints, but that is policy layered on top. The core rule does not change:
known mismatches are static errors; unknowns defer to runtime contracts.

## Non-goals

- No Crystal-style requirement that every expression in every script be
  statically typed before execution.
- No change to runtime value representation or Ruby-like dynamic dispatch.
- No removal of runtime boundary checks; the runtime remains the final guard for
  dynamic host data and unchecked paths.
- No whole-program inference across unknown host globals, arbitrary dynamic
  dispatch, or capability implementations in the first implementation.

## Consequences

Typed annotations become deployment-time contracts rather than runtime guards.
Local contradictions that a user reasonably expects a type checker to catch are
caught before execution, and AI-generated scripts get a tighter correction loop:
wrong operators, wrong argument types, wrong return types, and shape mismatches
produce concrete diagnostics before a host deploys the script.

The JSON path gets a one-step validated entry. A script that calls
`JSON.parse_as` at the edge gets full static checking downstream without any
other annotations.

The implementation becomes more complex. The checker needs a local type
environment, type joins for branches, a representation for unknown values, and
care not to guess: when host data or dynamic dispatch cannot be proven, it must
defer to runtime checks rather than reject. `vibes check` becomes a real
semantic pass with its own compatibility surface.

`JSON.parse_as` carries specific costs: shape literals must become legal in
expression position (today shapes exist only in annotation positions), which is
parser and runtime work; it adds stdlib API surface; and its validation
failures must raise with the same semantics as existing typed-boundary errors.

## Alternatives Considered

### Keep the current gradual contract checker

Rejected. Runtime-only enforcement catches errors too late for deployment gates,
and it misses simple local contradictions — `value = "1"; takes_int(value)`
passes the check path today.

### Adopt Crystal's static model wholesale

Rejected. Full inference requires every expression to have a compile-time type,
which forces `JSON::Any`-style wrappers, casts, and declared structs onto
dynamic payloads — the exact data Vibescript scripts exist to handle. We want
Crystal's inference for local code, not its obligations for dynamic data.

### Require annotations everywhere

Rejected. Mandatory annotations make small scripts heavier and do not solve
dynamic host data by themselves. Local inference gives most of the benefit for
typed code while keeping scripts readable.

### Lean on `any` for gradual typing

Rejected as the primary model. `any` is useful as an escape hatch, but making it
central would hide exactly the class of mistakes `vibes check` should catch, and
its contagious propagation rules complicate the language.

### Treat unknown values as static errors

Rejected for the base checker. That would make gradual adoption and
host-provided data painful. Deployment policies may require stricter annotations
at entrypoints, but the language-level checker must distinguish "known wrong"
from "not statically known."
