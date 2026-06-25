# Language Reference

This is the consolidated reference for Vibescript syntax and core semantics.
Use this with focused guides in `docs/` for deeper examples.

## Source Structure

- Files are UTF-8 text, typically with `.vibe` extension.
- `#` starts a comment that runs to end-of-line.
- Top-level declarations are functions, classes, and enums. Executable top-level
  statements form the default script body when a file is run without
  `-function`, and form a module initializer when a file is loaded with
  `require`. Statements are separated by newlines or semicolons.
- Expressions can be used as statements.

## Values and Literals

Vibescript supports these literal/value categories:

- `nil`, `true`, `false`
- integers and floats (`1`, `42`, `3.14`)
- strings (`"hello"`, `"hello #{name}"`)
- symbols (`:name`)
- arrays (`[1, 2, 3]`)
- hashes (`{name: "Ada", active: true}`)
- ranges (`1..5`, `1...5`)
- duration literals (`5.minutes`, `2.days`)

Hash literals support label keys (`name:`) and quoted string keys (`"name":`).
Ruby's hash rocket syntax (`=>`) is not supported.

Ranges with `..` include the final endpoint. Ranges with `...` exclude it.

Double-quoted strings support `#{...}` interpolation. Each interpolation must
contain one expression; the expression value is converted with the same string
form used by `to_s`. Escape an interpolation marker as `\#{...}` for literal
text. Single-quoted strings do not interpolate.

See `docs/arrays.md`, `docs/hashes.md`, `docs/strings.md`, `docs/durations.md`,
and `docs/time.md` for full method coverage.

## Variables and Assignment

Variables are dynamically bound by assignment:

```vibe
total = 0
total = total + 10
```

Parallel and destructuring assignment split array values across targets:

```vibe
a, b = [1, 2]
first, *middle, last = [1, 2, 3, 4]
x, (y, z) = [1, [2, 3]]
```

Missing values bind as `nil`, extra values are ignored unless captured by a
`*rest` target, and scalar right-hand values are treated as one value.

Index assignment is supported for mutable collections:

```vibe
items = [1, 2, 3]
items[0] = 10
```

Compound assignment is supported for single assignment targets, including
variables, member targets, and index targets:

```vibe
total += amount
items[0] *= 2
record[:score] **= 2
```

Supported compound assignment operators are `+=`, `-=`, `*=`, `/=`, `%=`, and
`**=`. They reuse the corresponding arithmetic operator semantics.

## Functions

Define functions with `def`/`end`:

```vibe
def add(a, b)
  a + b
end
```

Function features:

- Positional arguments.
- Keyword/default arguments.
- Optional type annotations.
- Optional return type annotations.
- Optional block parameters.

Typed signature example:

```vibe
def charge(amount: int, currency: string = "USD") -> hash
  {amount: amount, currency: currency}
end
```

### Parameter forms

A parameter's spelling chooses how it receives a value. The token after the
colon disambiguates the keyword and typed forms:

| Form | Meaning |
| --- | --- |
| `name` | required positional parameter |
| `name = default` | optional positional parameter |
| `name: Type` | typed positional parameter |
| `name: Type = default` | typed positional parameter with a default |
| `name:` | required keyword-only parameter |
| `name: default` | optional keyword-only parameter |
| `*rest` | captures extra positional arguments |
| `**rest` | captures extra keyword arguments |
| `&block` | captures a passed block |

A keyword-only parameter is bound only by a matching keyword label; it never
accepts a positional argument. The optional form supplies its default when the
label is omitted, and a later default may reference an earlier parameter:

```vibe
def connect(host:, port: 8080, scheme: "https", timeout: port * 2)
  "#{scheme}://#{host}:#{port}"
end

connect(host: "example.com")            # uses port 8080, scheme "https"
connect(host: "example.com", port: 443) # overrides port
```

Because `name: Type` declares a typed positional parameter, a bare identifier
after the colon resolves as a type name, not a keyword default: write `a: int`
for a typed positional and `a: 0` for an optional keyword. The `name: nil`
spelling is the optional keyword default `nil`, matching Ruby and the stdlib's
documented optional keywords; a bare `nil` positional type would be useless.
When a keyword default must reference another name on its own, wrap it in
parentheses (`a: (other)`) so it parses as an expression.

A keyword default may be a full expression, including one that references an
earlier parameter with a comparison (`def f(limit:, ok: limit < 10)`). A `{ ... }`
default is a hash literal whenever its contents are values rather than types, so
`def f(opts: { retry: 3 })` and `def f(opts: {})` both declare hash defaults. A
hash value may reference an earlier parameter directly, including as a bare
identifier (`def g(a:, b: { sum: a })`); no parentheses are needed inside the
braces. The `name: { field: Type }` spelling, whose field values are themselves
types, stays a typed positional parameter with a shape type.

### Function values

A function referenced by name (without calling it) is a value that can be
passed to other functions and invoked. Both direct `fn(...)` invocation and
Ruby-style `fn.call(...)` are supported and behave identically, forwarding
positional arguments, keyword arguments, and an optional block:

```vibe
def inc(n)
  n + 1
end

def twice_direct(fn)
  fn(2)
end

def twice_call(fn)
  fn.call(2)
end
```

Argument arity and type errors raised by `fn.call(...)` point at the call
site, the same as direct invocation. The only member exposed on a function
value is `call`.

A function with at least one parameter becomes a value when referenced by
name. A zero-arity function is auto-invoked when referenced by name, so it
cannot yet be passed as a function value (and therefore cannot be reached by
`fn.call`); passing zero-arity functions as values is tracked separately.

## Classes

Class declarations are supported for grouping behavior and methods:

```vibe
class Counter
  def bump(value: int) -> int
    value + 1
  end
end
```

Inheritance is not supported.

See `docs/classes.md` for class methods, `@`/`@@` variables, accessors, and
privacy semantics.

## Enums

Enums declare nominal state sets:

```vibe
enum Status
  Draft
  Published
end
```

Members are accessed with `::`:

```vibe
Status::Draft
```

See `docs/enums.md` for coercion, equality, and serialization behavior.

## Method Calls

Calls support positional args and keyword args:

```vibe
fees.apply(amount)
require("billing/rules", as: "rules")
```

Calls may omit parentheses when all arguments stay on the same line:

```vibe
fees.apply amount
normalize input
add 1, 2
require "billing/rules", as: "rules"
render status: "ok"
```

Label arguments bind as keyword arguments when the callee accepts them. When a
script function has a positional hash/options parameter instead, the same source
form is passed as a final options hash. This options-hash binding applies to
plain function calls in both parenless and parenthesized form, so the two are
interchangeable:

```vibe
accept_options retry: true, limit: 3
accept_options(retry: true, limit: 3)
```

Invoking a function value through its `call` alias follows the same rule, so
`accept_options.call(retry: true, limit: 3)` binds the options hash exactly like
the direct `accept_options(retry: true, limit: 3)` form. A function value reached
through member access binds the same way, so calling a module function such as
`rules.accept_options(retry: true, limit: 3)` matches the direct form too.

The synthesized hash is type-checked against a typed options parameter, so
`accept_options(retry: "soon")` is rejected with the shape mismatch when the
parameter declares `{ retry: bool, limit: int }`.

Constructor calls (`Klass.new(...)`) and method calls (`receiver.method(...)`)
keep strict parenthesized keyword binding: a parenthesized keyword that has no
matching keyword parameter does not collapse into a positional options hash.
This includes an instance method named `call`, which stays distinct from a
function value's `call` alias. Their parenless forms still pass the options
hash, mirroring the historical behavior.

Positional arguments must come before keyword labels. A positional argument that
follows a keyword label, such as `collect(first: 1, "tail")`, is a parse error in
both the parenthesized and parenless forms, matching Ruby. Keyword labels after a
positional argument are fine, so `collect("head", first: 1)` is accepted.

Blocks can be passed with `do ... end`:

```vibe
numbers.map do |n|
  n * 2
end
```

Ruby-style ampersand block forwarding and symbol-to-proc shorthand are not
supported; use an explicit `do ... end` or brace block.

Ruby-style safe navigation (`receiver&.member`) is not supported. Use an
explicit nil check:

```vibe
if user == nil
  nil
else
  user.name
end
```

## Operators

Core operator families:

- Arithmetic: `+`, `-`, `*`, `/`, `%`, `**`
- Comparison: `==`, `!=`, `<`, `<=`, `>`, `>=`, `<=>`
- Case equality: `===`
- Boolean: `&&`/`and`, `||`/`or`, unary `!`/`not`
- Unary sign: prefix `-` negates a number; prefix `+` is the identity on
  numbers and strings
- Conditional: `condition ? when_true : when_false`

The spaceship operator `<=>` returns `-1`, `0`, or `1` for ordered operands and
`nil` when the two operands cannot be ordered (different kinds, money values in
different currencies, or a `NaN` on either side), matching Ruby's spaceship
contract. The relational operators `<`, `<=`, `>`, `>=` instead raise on
incomparable operands, matching Ruby's `ArgumentError`.

The case equality operator `===` treats its left operand as a matcher and its
right operand as the value being tested, mirroring how a `case`/`when` clause
compares its patterns. A range matcher checks membership, so `(1..3) === 2` is
`true` and `(1...3) === 3` is `false`. Every other matcher falls back to `==`,
so `1 === 1` is `true` and `2 === (1..3)` is `false` (the integer `2` is not a
range). Because the scalar path reuses `==`, integers and floats remain distinct
kinds, so `1 === 1.0` is `false`, unlike Ruby. Regex and class matchers will be
added alongside the corresponding language features.

Operator precedence follows conventional arithmetic/boolean ordering.
Exponentiation with `**` is right-associative and binds more tightly than
unary `-`, so `-2 ** 2` is parsed as `-(2 ** 2)`. Integer powers stay `int`
when the exponent is non-negative and the result fits in 64 bits; mixed
numeric powers and negative integer exponents return `float`. Integer
overflow and non-finite float powers raise runtime errors. Division follows
Ruby: integer division by zero (`1 / 0`) raises, while float division by zero
(`1.0 / 0`) follows IEEE 754 and yields `Infinity`, `-Infinity`, or `NaN`.
Inspect those special values with `Float#nan?`, `Float#infinite?`, and
`Float#finite?`. `not` has the same prefix precedence as `!`, `and` has the same
precedence as `&&`, and `or` has the same precedence as `||`. Ternary
conditionals have lower precedence than `or`, associate to the right, and
evaluate only the selected branch.

Prefix `+` mirrors Ruby's unary plus: it returns integers, floats, and strings
unchanged and raises on any other operand. Because Vibescript strings are
immutable values, `+"x"` yields the same string value.

A leading `+` or `-` at the start of a fresh line follows Vibescript's
indented-continuation rule, which is shared with `-` and intentionally differs
from Ruby. When the sign sits flush against its operand it begins a new
statement (`total\n+amount` parses as two statements, matching Ruby). When the
sign is separated from its operand by surrounding whitespace it continues the
previous line as a binary operator (`total\n + amount` is addition). Ruby treats
both forms as a new statement and would instead parse `total\n + amount` as the
two statements `total` and `+amount`; Vibescript deliberately supports the
spaced form as an explicit operator continuation so multi-line arithmetic can be
indented under its first operand.

## Control Flow

Conditionals:

```vibe
if amount > 0
  "ok"
elsif amount == 0
  "zero"
else
  "invalid"
end

unless amount <= 0
  "ok"
else
  "invalid"
end
```

`if` / `elsif` / `else` can also be used as a value-producing expression:

```vibe
status = if active
  "open"
else
  "closed"
end
```

Looping:

```vibe
for item in items
  if item == nil
    next
  end
end
```

Supported control-flow constructs include:

- `if` / `elsif` / `else`
- `unless` / `else`
- `while`
- `until`
- `for ... in`
- `break`
- `next`
- `return`

Short expression and assignment statements can also use modifier loops and
`unless` conditionals:

```vibe
i = i + 1 while i < 3
i = i + 1 until i >= 3
status = "open" unless suspended
```

Ternary conditionals are expressions:

```vibe
status = active ? "open" : "closed"
```

## Error Handling

Raise explicit failures:

```vibe
raise("missing configuration")
```

Structured handling supports `rescue`/`ensure`:

```vibe
def run
  begin
    risky
  rescue RuntimeError => err
    err.message
  ensure
    cleanup
  end
end
```

See `docs/errors.md` for parser/runtime error formats and stack traces.

## Modules

Load shared code with `require`:

```vibe
helpers = require("public/helpers", as: "helpers")
helpers.normalize(input)
```

Module resolution is governed by host `Config.ModulePaths` and policy lists.

## Typing

Typing is gradual and optional:

- annotate parameters and returns where helpful.
- mark nullable types with `?`.
- rely on runtime contract checks for typed boundaries.

See `docs/typing.md` for complete behavior.

## Built-in Facilities

Notable built-ins include:

- Assertions and conversion helpers.
- `Time`, `Duration`, `Money` helpers.
- `JSON` and `Regex` utility families.

See `docs/builtins.md` and family-specific docs for full API details.
