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

- `vibes/execution.go` (core evaluator, call orchestration)
- `vibes/execution_types.go` (type-checking + type formatting helpers)
- `vibes/execution_values.go` (value conversion, arithmetic, comparison helpers)

## Parsing And AST

Pipeline:

1. `lexer` tokenizes source.
2. `parser` builds AST statements/expressions.
3. `Engine.Compile(...)` lowers AST declarations into `ScriptFunction` and `ClassDef`.

Key files:

- `vibes/lexer.go`
- `vibes/parser.go` (parser core + precedence + token/error helpers)
- `vibes/parser_statements.go` (statement-level parsing)
- `vibes/parser_types.go` (type-expression parsing)
- `vibes/ast.go`

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

- `vibes/modules.go` (module request parsing, path resolution, policy, cache/load)
- `vibes/modules_require.go` (runtime require execution, export/alias behavior, cycle reporting)

## Builtins

Builtins are registered during engine initialization:

- core registration entrypoint: `registerCoreBuiltins(...)` in `vibes/interpreter.go`
- domain files:
  - `vibes/builtins.go` (core/id helpers)
  - `vibes/builtins_numeric.go`
  - `vibes/builtins_json_regex.go`

## Refactor Constraints

When refactoring internals:

- Preserve runtime error text when possible (tests assert key messages).
- Keep parser behavior stable unless paired with migration/docs updates.
- Run `go test ./...` and style gates after every atomic change.
