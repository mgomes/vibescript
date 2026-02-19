# Gradual Typing

VibeScript supports optional type annotations on parameters and return values. Unannotated code is not type-checked; annotations opt you into runtime checks.

## Supported types

Type names are case-insensitive:

- `int`, `float`, `number`
- `string`, `bool`, `nil`
- `duration`, `time`, `money`
- `array`, `hash`/`object`, `function`
- `any` (no checks)

Parametric containers:

- `array<T>` checks every element against `T`
- `hash<K, V>` checks each key against `K` and each value against `V`
- Example: `array<int>`, `array<int | string>`, `hash<string, int>`

Shape types for object/hash payload contracts:

- `{ id: string, score: int }` requires exactly those keys
- Field values are recursively type-checked
- Extra keys and missing keys fail validation

Nullable: append `?` to allow `nil` (e.g., `string?`, `time?`, `int?`).

Unions: join allowed types with `|` (e.g., `int | string`, `int | nil`).

## Function definitions

Method declarations omit parentheses when there are no args:

```vibe
def pick_second(n: int, m: int) -> int
  m
end

def pick_optional(label: string? = nil) -> string?
  label
end

def normalize_id(id: int | string) -> string
  id.string
end

def apply_bonus(payload: { id: string, points: int }) -> { id: string, points: int }
  { id: payload[:id], points: payload[:points] + 5 }
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

- Types are nominal by kind.
- Hash keys are runtime strings, so `hash<K, V>` key checks run against string keys.
- Shape types are strict: keys must match exactly.
- Type names are case-insensitive (`Int` == `int`).
