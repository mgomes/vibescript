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

## Migration examples

Use a boundary-first strategy: annotate entrypoints that receive external data, then tighten helpers and block callbacks.

### 1) Start with function boundaries

Before:

```vibe
def calculate_total(items)
  items.reduce(0) do |acc, item|
    acc + item[:amount]
  end
end
```

After:

```vibe
def calculate_total(items: array<{ amount: int }>) -> int
  items.reduce(0) do |acc: int, item: { amount: int }|
    acc + item[:amount]
  end
end
```

### 2) Migrate optional values with nullable or unions

Before:

```vibe
def normalize_id(id)
  if id == nil
    "unknown"
  else
    id.string
  end
end
```

After:

```vibe
def normalize_id(id: int | string | nil) -> string
  if id == nil
    "unknown"
  else
    id.string
  end
end
```

Use `T?` when the only optional case is `nil`, and use unions when multiple concrete kinds are allowed.

### 3) Convert loose hashes to shape contracts

Before:

```vibe
def reward(payload)
  { id: payload[:id], points: payload[:points] + 10 }
end
```

After:

```vibe
def reward(payload: { id: string, points: int }) -> { id: string, points: int }
  { id: payload[:id], points: payload[:points] + 10 }
end
```

Shapes are strict. Missing or extra keys fail checks.

### 4) Annotate block signatures where callbacks matter

Before:

```vibe
def render_scores(scores)
  scores.map do |s|
    s + 1
  end
end
```

After:

```vibe
def render_scores(scores: array<int>) -> array<int>
  scores.map do |s: int|
    s + 1
  end
end
```

Typed blocks catch callback mismatches at runtime with errors that include parameter name, expected type, and actual type.

### 5) Roll out incrementally

- Add annotations to one high-value path first.
- Keep internal helpers untyped until boundary contracts stabilize.
- Use `any` as a temporary bridge during migration.
- Replace `any` with concrete or shape types once call sites are clean.
- Watch runtime type errors in staging, then tighten signatures further.

## Time and Duration

Duration methods like `ago`/`after` return `Time`. Typed signatures use `time` or `time?` for those values.

## Notes and limitations

- Types are nominal by kind.
- Hash keys are runtime strings, so `hash<K, V>` key checks run against string keys.
- Shape types are strict: keys must match exactly.
- Type names are case-insensitive (`Int` == `int`).
