# Errors and Debugging

VibeScript surfaces both parse-time and runtime failures with line and column information.

## Parse Errors

Compilation failures include a parser message and a source code frame:

```text
parse error at 2:12: missing value for keyword argument foo
  --> line 2, column 12
 2 |   call(foo: )
   |            ^
```

Common parser diagnostics:

- `invalid hash pair: expected symbol-style key like name:`
- `missing value for hash key ...`
- `missing value for keyword argument ...`
- `trailing comma in block parameter list`

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

## REPL Debugging

The REPL stores the previous failure. Use:

- `:last_error` to print the latest compile/runtime error.

This is useful after long output or when a failure scrolls out of view.
