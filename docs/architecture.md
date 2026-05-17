# Internal Architecture

This document summarizes how core interpreter subsystems fit together.

## Execution Flow

High-level call path:

1. `Script.Call(...)` clones function/class declarations into an isolated call environment.
2. Builtins, globals, capabilities, and module context are bound into root env.
3. Class bodies are evaluated to initialize class variables.
4. Target function arguments are bound and type-checked.
5. Statement/expression evaluators execute the script and enforce:
   - step quota
   - recursion limit
   - memory quota

Key files:

- `vibes/engine.go`, `vibes/script.go`, `vibes/execution.go` (public aliases and constructors)
- `internal/runtime/engine.go` (engine construction and compile/execute entrypoints)
- `internal/runtime/script.go` (script call surface and call-time orchestration)
- `internal/runtime/execution.go` (execution state, stack, env, module, and receiver helpers)
- `internal/runtime/call.go` (callable dispatch, argument binding, blocks, and builtin invocation)
- `internal/runtime/eval.go` (statement, expression, assignment, operator, control-flow, and error execution)
- `internal/runtime/members*.go` (member dispatch for arrays, hashes, strings, numerics, temporal values, classes, instances, enums, and objects)
- `internal/runtime/types*.go` (declared-type formatting and validation)
- `internal/runtime/values.go` (value conversion, sorting, flattening, arithmetic, and comparison helpers)

## Parsing And AST

Pipeline:

1. `lexer` tokenizes source.
2. `parser` builds AST statements/expressions.
3. `Engine.Compile(...)` lowers AST declarations into `ScriptFunction` and `ClassDef`.

Key files:

- `internal/parser/lexer.go` (tokenization)
- `internal/parser/parser.go` (parser core initialization and token stream helpers)
- `internal/parser/expressions.go` (expression, access, call, block, collection, and scalar literal parsing)
- `internal/parser/statements.go` (statement, declaration, control-flow, class, function modifier, and parse-error handling)
- `internal/parser/types.go` (type-expression parsing)
- `internal/ast/*.go` (private AST node definitions, formatting, cloning, and token/source metadata)
- `internal/runtime/compile.go` (AST lowering into compiled script functions/classes and compile-error aggregation)

## Modules (`require`)

`require` runtime behavior:

1. Parse module request and optional alias.
2. Resolve relative or search-path module file.
3. Enforce allow/deny policy rules.
4. Compile + cache module script by normalized cache key.
5. Execute module in a module-local env.
6. Export non-private functions to module object.
7. Inject non-conflicting exports into globals and optionally bind alias.

Key files:

- `internal/runtime/modules.go` (module request parsing, relative/search-path loading, policy enforcement, compile/cache behavior, cycle reporting, exports, and alias binding)

## Builtins

Builtins are registered during engine initialization:

- core registration entrypoint: `registerCoreBuiltins(...)` in `internal/runtime/engine.go`
- domain files:
  - `internal/runtime/builtins.go` (core helpers, numeric helpers, JSON, Regex, duration, time, and namespace registration)

## Capability Adapters

Capabilities expose host functionality to scripts through typed contracts and runtime adapters.

Key files:

- `vibes/capability/{contextcap,db,events,jobqueue}` (host-facing interfaces, request types, per-capability validation, and examples)
- `vibes/capability_*.go` (top-level constructor wrappers returning `vibes.CapabilityAdapter`)
- `internal/runtime/capabilities.go` (capability adapter interfaces and binding contracts)
- `internal/runtime/capability_adapters.go` (runtime wrappers for first-party capability packages)
- `vibes/internal/capabilitycontract/contract.go` (shared clone, data-only validation, and nil-implementation helpers for capability packages)

## Refactor Constraints

When refactoring internals:

- Preserve runtime error text when possible (tests assert key messages).
- Keep parser behavior stable unless paired with migration/docs updates.
- Run `go test ./...` and style gates after every atomic change.
