# Gradual Typing

Vibescript supports optional type annotations on parameters and return values.
Annotations add runtime contracts, and the checker additionally infers local
types to catch known contradictions before execution. Unannotated values remain
gradual rather than becoming errors merely because their types are unknown.

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
- A trailing `...` marks the shape open: `{ name: string, ... }` validates the
  declared fields and lets undeclared extra fields pass unchecked. Shapes stay
  exact (closed) by default; `{ ... }` alone accepts any hash. Open and exact
  shapes nest freely.

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

The check path validates typed boundaries before execution. Locals implicitly
take the types of the expressions assigned to them, and the checker reports an
error wherever known types contradict (ADR-004):

```vibe
def takes_int(value: int)
  value
end

value = "1"
takes_int(value)   # check error: argument value expected int, got string
```

The governing rule is: **error on known contradictions, permit unknowns, and
enforce unknown values at runtime contracts**.

- Assignments bind the inferred type of the right-hand side to the local;
  reassigning a local to a conflicting type is an error, while `nil`
  re-initialization and numeric widening stay legal.
- Assignments in sibling branches merge into unions.
- Annotated parameters enter the function body with their declared type, and
  known calls check argument types and expose their annotated return type.
- Operators reject operands known to be invalid at runtime (`1 + nil`).
- Shape-typed values carry field-level facts, so indexing with a known key
  yields the field's type. Reading an optional field infers the field type
  joined with `nil`, since the field may be absent. On an open shape, reads of
  undeclared fields stay unknown rather than inferring `nil`.
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
  Symbols keep coercing into enums, and unresolved or host-supplied names stay
  conservative. A module names a namespace rather than a type, so no value
  satisfies a module annotation.
- Control flow narrows nullable locals: truthiness tests, explicit nil
  comparisons, and `nil?` predicates with proven universal dispatch refine the
  local in both branches, including
  `unless`, `elsif`, negation, short-circuits, and guard clauses that exit
  early. Unknown values stay unknown, and branches re-join into the wider
  fact afterwards.
- Unannotated script functions get inferred return summaries: every known
  result path (explicit returns, the implicit final expression, and nil
  fallthrough) joins into the call's result fact. A single unknown path —
  recursion, dynamic dispatch, unmodeled constructs — keeps the whole result
  unknown, and explicit return annotations always win.
- Constructor results are nominal: `u = User.new` gives `u` the fact `User`,
  boundary checks compare it by class identity, and instance methods called
  on it check their argument shapes and expose their annotated return types.
  Shadowed class names and dynamic constructor dispatch stay unknown.
- Class predicates narrow nominal unions: `u.is_a?(User)`,
  `u.kind_of?(User)`, and `u.instance_of?(User)` against a statically resolved
  class refine a known union local in both branches, including guard clauses.
  Narrowing applies only when every arm provably reaches the runtime universal
  predicate — an arm whose class overrides the predicate or a dynamic argument
  leaves the fact unchanged.
- `is_type?` narrows known unions: `value.is_type?(:int)` with a literal
  built-in atom refines a known union local in both branches — the true path
  keeps arms that may satisfy the atom, the false path drops arms that always
  do. The atom is a symbol or string naming a primitive (`:int`, `:string`,
  `:bool`, `:symbol`, `:nil`, `:number`, `:duration`, `:time`, `:money`), a
  bare container (`:array`, `:hash`/`:object`, `:range`, `:function`), a class or enum
  name (matched by exact name, no ancestry), or any of these with a trailing
  `?` for the nullable form (`'int?'`). The test never coerces —
  `"5".is_type?(:int)` is `false` — and parameterized spellings such as
  `array<int>` are rejected. Class and enum atoms answer at runtime but do
  not narrow yet, and receivers that may override `is_type?` stay unchanged.
- Scalar member contracts resolve from receiver facts: `s.to_i` on a known
  string is an `int`, universal predicates such as `nil?` and `respond_to?`
  are `bool`, and safe navigation adds `nil` to the result (`x&.to_s` is
  `string?`). Class instances (user methods take precedence) and unknown
  receivers stay unknown.
- Member contracts also classify receiver effects. A container fact survives
  a call the registry proves pure (`a.at(0)`, `a.nil?`), including through
  aliases, chained calls, and safe navigation. The modeled array writes
  described below can also preserve a compatible fact. Other known mutators,
  unregistered members, blocks whose effects cannot be modeled, impure
  arguments, dynamic dispatch, and user overrides still discard the
  receiver's facts — as does a pure read that may hand back a nested mutable
  element (`a.at(0)` on `array<array<int>>`), since a chained mutation through
  that alias could not be traced back to `a`.
- Element writes to a local known to be `array<T>` — `items << v`,
  `items[i] = v`, and the in-place mutators `push`/`append`/`prepend`/
  `unshift`/`insert`/`fill` — are checked against `T`: a value that can never
  satisfy `T` is reported at the write, a provably compatible write keeps the
  known element type, and an unknown value conservatively widens the local
  back to unknown. For `fill`, the checker composes exact selector outcomes
  with value or block results, including implicit `nil` padding, and preserves
  the fact only when every completing path remains compatible. Unknown
  receivers, unresolved fill outcomes, `array<any>`, and untyped arrays stay
  gradual.
- Entry writes to a local known to be `hash<K, V>` — `h[k] = v`, `store`,
  and the in-place `merge!`/`update` — check the key against `K` and the
  value against `V` the same way. Writes to a local with a declared shape
  type check the field's declared type, and a statically known key outside
  the shape is reported (shapes are exact). Hash and shape literals stay
  writable: the checker updates their known fields in place instead of
  reporting, and unknown keys or values widen the fact back to unknown.
- Typed accessor-backed instance variables carry their property contract
  into instance-method bodies for direct-write checking: `@name = 1` against
  `property name: string` is an error when the value's known type contradicts
  the contract. Reads of scalar properties observe the declared type (or
  `nil` before the first write), and a checked write drops the nil arm.
  Container-typed property reads stay gradual because mutations through
  nested aliases cannot be tracked safely. Unknown values pass and the
  runtime guard validates the write when it executes; untyped accessors and
  undeclared instance variables stay dynamic.

### What a clean check means

A clean check means that the selected scope contains no contradiction the
checker can prove. It is not proof that every value is type-safe. In particular:

- Branches form union facts. Every finite known arm must satisfy a typed
  boundary; one compatible arm cannot hide another known mismatch. `any` and
  unknown arms remain gradual and are checked at runtime, but they do not hide
  incompatible known arms.
- Calls through a known function, class, builtin, or published host signature
  contribute argument and result facts. Unknown receivers, user-overridable
  members, and host callables without signatures remain dynamic.
- `JSON.parse` returns an unknown fact. `JSON.parse_as` validates a declared
  contract at runtime and gives the checker that declared result fact.
- Mutable-container facts survive operations the member-contract registry
  proves pure and the compatible modeled writes described above. Other known
  mutators, unregistered calls, blocks, impure arguments, dynamic dispatch,
  and aliases to nested mutable values discard facts the checker can no longer
  trust. Runtime boundary checks remain authoritative.

### Checking scopes

Choose a scope that matches the operation the host will perform:

- `vibes check script.vibe` checks top-level code, every function, and every
  class method in the file without executing it. The equivalent embedding API
  is `Script.CheckWarnings` (or `CheckWarningsWithOptions` when host globals and
  capabilities matter).
- `vibes run -check [-function name] script.vibe [args...]` checks the exact CLI
  invocation path. Embedders use `CheckWarningsForFunction` for a named path or
  `CheckWarningsForCall` to include concrete arguments, keywords, globals, and
  capabilities. `CheckedCall` checks those same inputs and executes only if the
  diagnostic list is empty.
- `vibes run -check -e 'source'` checks an inline snippet as a whole: its
  entrypoint plus every declared function and method, even when uncalled.

`vibes run -check` and `vibes check` use the same gradual rule, but they do not
select the same scope. Use the whole-file command for repository and deployment
gates, and the per-call form when checking one concrete host invocation.

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

An open shape validates the fields you care about without enumerating the
whole payload:

```vibe
body = JSON.parse_as(raw, { name: string, ... })

body["name"]   # known string
body["role"]   # unknown: undeclared fields pass through unchecked
```

JSON roots are not always objects. Array, primitive, nullable, and union
contracts work at the root too, spelled directly as the second argument:

```vibe
scores = JSON.parse_as(raw, array<int>)
count  = JSON.parse_as(raw, int)
note   = JSON.parse_as(raw, string?)
mixed  = JSON.parse_as(raw, int | string)
rows   = JSON.parse_as(raw, array<{ id: string }>)
```

Validation uses the same normalization and diagnostics as shape roots
(`JSON.parse_as value expected array<int>, got array<int | string>`), and the
checker carries the declared root as the result fact. These non-shape type
literals are recognized in parenthesized call arguments; a spelling that also
reads as a value (a local named `int`, for example) keeps its value reading,
mirroring the shape-literal shadowing rules below.

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

### Complete checker examples

The examples in this section are complete programs. Documentation CI compiles,
analyzes, and checks fences marked `vibe check`; `vibe check-error` records the
diagnostic a deliberately invalid example teaches. These markers are Markdown
metadata, not Vibescript syntax.

Nullable and union annotations remain gradual while carrying facts the checker
can narrow or validate:

```vibe check
def label(value: string?) -> string
  if value == nil
    return "missing"
  else
    return value
  end
end

def stringify(value: int | string) -> string
  value.string
end

label(nil)
stringify(7)
```

Shapes expose field facts, and constructed class instances carry nominal class
facts into known calls:

```vibe check
class Account
  def name -> string
    "Ada"
  end
end

def account_name(account: Account) -> string
  account.name
end

def payload_name(raw: string) -> string
  payload = JSON.parse_as(raw, { name: string, ... })
  payload["name"]
end

account_name(Account.new)
payload_name('{"name":"Ada","active":true}')
```

Plain JSON stays intentionally dynamic. The checker permits the unknown field,
and `takes_int` enforces the actual value when `count_from` runs:

```vibe check
def takes_int(value: int) -> int
  value
end

def count_from(raw: string) -> int
  body = JSON.parse(raw)
  takes_int(body["count"])
end
```

A provable contradiction is documented with its stable diagnostic text, not a
source column:

```vibe check-error="call to takes_int argument value expected int, got string"
def takes_int(value: int)
  value
end

value = "1"
takes_int(value)
```

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
contract, and use an open shape (`{ id: string, ... }`) when the payload
carries extra fields you do not model.

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
  optional with `?`, which may be absent, and open shapes (trailing `...`),
  which permit undeclared extra fields.
- Type names are case-insensitive (`Int` == `int`).
