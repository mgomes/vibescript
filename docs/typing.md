# Gradual Typing

VibeScript supports optional type annotations on parameters and return values. Unannotated code is not type-checked; annotations opt you into runtime checks.

## Supported types

Type names are case-insensitive:

- `int`, `float`, `number`
- `string`, `bool`, `nil`
- `duration`, `time`, `money`
- `array`, `hash`/`object`, `function`
- `any` (no checks)

Nullable: append `?` to allow `nil` (e.g., `string?`, `time?`, `int?`).

## Function definitions

Method declarations omit parentheses when there are no args:

```vibe
def pick_second(n: int, m: int) -> int
  m
end

def pick_optional(label: string? = nil) -> string?
  label
end

def nil_result() -> nil
  nil
end
```

Defaults are evaluated at call time in the caller’s environment.

## Calls: positional and keyword

Arguments can be bound positionally or by name (or mixed):

```vibe
pick_second(1, 2)
pick_second(n: 1, m: 2)
pick_second(1, m: 2)
```

Unknown keyword args and missing required args raise errors.

## Returns

If a return type is annotated, the returned value is checked. If omitted, no return check is enforced.

## Time and Duration

Duration methods like `ago`/`after` return `Time`. Typed signatures use `time` or `time?` for those values.

## Notes and limitations

- Types are nominal by kind (no generics like `array<int>` yet).
- Nullable via `?` only; unions beyond `nil` are not supported yet.
- Type names are case-insensitive (`Int` == `int`).
