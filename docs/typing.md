# Gradual Typing

Vibescript supports optional type annotations on parameters and return values. Unannotated code is not type-checked at runtime; annotations opt you into runtime checks, and the static check path (`vibes check` or `vibes run -check`) additionally infers local types to catch known contradictions before execution (see [Static checking](#static-checking) below).

## Supported types

Type names are case-insensitive:

- `int`, `float`, `number`
- `string`, `bool`, `nil`
- `duration`, `time`, `money`
- `array`, `hash`/`object`, `range`, `function`
- top-level enum names such as `Status`
- `any` (no checks)

Parametric containers:

- `array<T>` checks every element against `T`
- `hash<K, V>` checks each key against `K` and each value against `V`
- Example: `array<int>`, `array<int | string>`, `hash<string, int>`

Shape types for object/hash payload contracts:

- `{ id: string, score: int }` requires exactly those keys
- Field values are recursively type-checked
- Extra keys and missing keys fail validation
- A `?` on a field name marks the field optional: `{ name: string, age?: int }`
  accepts payloads with or without `age`, and a present `age` must be an `int`
- Optional is about presence, nullable about the value: `age?: int` rejects a
  present `nil`, while `age?: int?` allows the field to be absent or `nil`
- Fields stay required by default; a field whose name literally ends in `?`
  is spelled with a string key (`{ "valid?": bool }`)

Nullable: append `?` to allow `nil` (e.g., `string?`, `time?`, `int?`).

For a generic container, the `?` belongs after the type arguments, not on the
container name. Write the nullable container as a union for now: `array<int> | nil`,
`hash<string, int> | nil`. The misplaced spellings `array?<int>` and
`hash?<string, int>` are rejected as parse errors. (Untyped nullable containers
such as `array?` and `hash?`, without type arguments, are accepted.)

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

def nil_result -> nil
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
pick_second 1, m: 2
```

Use a trailing colon in a parameter list for required keyword-only parameters,
and in a call for local-variable keyword shorthand:

```vibe
def greet(name:)
  "hello " + name
end

name = "Ada"
greet(name:)
greet name:
```

Unknown keyword args and missing required args raise errors.

Parenless label arguments can also supply a positional options hash when the
callee has no matching keyword/parameter name:

```vibe
def configure(opts)
  opts[:retries]
end

configure retries: 3
```

## Returns

If a return type is annotated, the returned value is checked. If omitted, no return check is enforced.

## Static checking

`vibes check` (and `vibes run -check`) validates typed boundaries before
execution. Locals implicitly take the types of the expressions assigned to
them, and the checker reports an error wherever known types contradict
(ADR-004):

```vibe
def takes_int(value: int)
  value
end

value = "1"
takes_int(value)   # check error: argument value expected int, got string
```

The governing rule is: **error on known contradictions, permit unknowns**.

- Assignments bind the inferred type of the right-hand side to the local;
  reassigning a local to a conflicting type is an error, while `nil`
  re-initialization and numeric widening stay legal.
- Assignments in sibling branches merge into unions.
- Annotated parameters enter the function body with their declared type, and
  known calls check argument types and expose their annotated return type.
- Operators reject operands known to be invalid at runtime (`1 + nil`).
- Shape-typed values carry field-level facts, so indexing with a known key
  yields the field's type. Reading an optional field infers the field type
  joined with `nil`, since the field may be absent.
- Values the checker cannot prove (JSON payloads, host globals, dynamic
  dispatch) are never rejected; the runtime checks remain the final guard.
- Core builtins with fixed contracts participate: `to_int("1")` is known to
  return an `int`, `Math.sqrt("x")` is rejected, and helpers such as `money`,
  `uuid`, `Duration.build`, and the `Time` constructors expose their argument
  and result types. Builtins with argument-dependent results (`JSON.parse`)
  stay unknown, and a host override removes the default contract entirely.
- Resolved named types compare by identity: a `Color` value contradicts a
  `Status` boundary, a class instance contradicts an unrelated class or
  module, and named values contradict primitive and container boundaries.
  Symbols keep coercing into enums, a class keeps satisfying modules it
  includes, and unresolved or host-supplied names stay conservative.
- Control flow narrows nullable locals: truthiness tests, explicit nil
  comparisons, and `nil?` predicates with proven universal dispatch refine the
  local in both branches, including
  `unless`, `elsif`, negation, short-circuits, and guard clauses that exit
  early. Unknown values stay unknown, and branches re-join into the wider
  fact afterwards.
- Constructor results are nominal: `u = User.new` gives `u` the fact `User`,
  boundary checks compare it by class identity, and instance methods called
  on it check their argument shapes and expose their annotated return types.
  Shadowed class names and dynamic constructor dispatch stay unknown.
- Scalar member contracts resolve from receiver facts: `s.to_i` on a known
  string is an `int`, universal predicates such as `nil?` and `respond_to?`
  are `bool`, and safe navigation adds `nil` to the result (`x&.to_s` is
  `string?`). Class instances (user methods take precedence) and unknown
  receivers stay unknown.

### `JSON.parse_as`

`JSON.parse_as(raw, shape)` parses JSON and validates the result against a
shape in one step, with the same semantics as typed parameter boundaries. The
checker treats its result as that shape, so everything downstream is inferred
and checked without further annotations:

```vibe
body = JSON.parse_as(raw, {
  name: string,
  email: string
})

create_user(body["name"])   # body["name"] is a known string
```

Optional fields fit JSON payloads whose keys may be omitted:

```vibe
body = JSON.parse_as(raw, {
  name: string,
  age?: int
})

body["age"]   # int | nil: present values validated as int, absent reads nil
```

Shape literals are legal in expression position: a braced group whose field
values all name built-in types (including unions, `array<T>`/`hash<K, V>`
arguments, and nested shapes) is a first-class shape value, assignable and
reusable. A braced group with value expressions, unknown identifiers, or
fields naming locals in scope stays an ordinary hash literal, and a group
whose type names are shadowed at runtime — a host-provided global named
`string`, for example — also keeps its hash semantics, so existing embedded
scripts read the host value exactly as before. Validation failures raise the
standard typed-boundary error, e.g.
`JSON.parse_as value expected { name: string }, got { name: int }`.

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

Shapes are strict. Missing or extra keys fail checks. Mark a key that may be
legitimately absent as optional (`points?: int`) instead of loosening the whole
contract.

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

## Type semantics

### Container compatibility rules

Container annotations are checked by validating contained values against the declared type.

- `array<T>` validates every element with `T`.
- `hash<K, V>` validates every key with `K` and every value with `V`.

Practical consequence:

- `array<int>` is valid for `array<number>` because each `int` satisfies `number`.
- `array<number>` is only valid for `array<int>` when all elements are actually ints.

### `T?` and `T | nil`

`T?` and `T | nil` are equivalent at runtime: both accept `nil` and values matching `T`.

- Prefer `T?` for simple optional values.
- Use `T | nil` when you are already expressing a broader union (for example `int | string | nil`).

### Coercion policy

Typed checks do not perform implicit coercion.

- `int` does not accept `"1"`.
- `string` does not accept `1`.
- Enum-typed boundaries may coerce matching symbols such as `:draft` into
  their enum member value.

Convert explicitly before calling typed boundaries (for example `.to_i`, `.to_f`, `.string`, parsers/builders for time and duration).

### Unknown keyword args under typed signatures

Unknown keyword arguments are strict for all function calls, including typed signatures.

- Extra keyword arguments raise `unexpected keyword argument <name>`.
- This behavior is the same for typed and untyped functions.

## Notes and limitations

- Types are nominal by kind.
- Hash keys keep their runtime identity, so `hash<K, V>` validates the actual key
  values. For example, `hash<int, string>` accepts a hash built with
  `h[1] = "one"`, and `hash<string, string>` accepts `{ "name": "Ada" }` but
  rejects the symbol-keyed `{ name: "Ada" }`.
- Shape types are strict: keys must match exactly, except fields marked
  optional with `?`, which may be absent.
- Type names are case-insensitive (`Int` == `int`).
