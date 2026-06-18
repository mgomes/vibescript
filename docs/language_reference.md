# Language Reference

This is the consolidated reference for Vibescript syntax and core semantics.
Use this with focused guides in `docs/` for deeper examples.

## Source Structure

- Files are UTF-8 text, typically with `.vibe` extension.
- `#` starts a comment that runs to end-of-line.
- Top-level declarations are functions, classes, and enums. Executable top-level
  statements form the default script body when a file is run without
  `-function`, and form a module initializer when a file is loaded with
  `require`.
- Expressions can be used as statements.

## Values and Literals

Vibescript supports these literal/value categories:

- `nil`, `true`, `false`
- integers and floats (`1`, `42`, `3.14`)
- strings (`"hello"`)
- symbols (`:name`)
- arrays (`[1, 2, 3]`)
- hashes (`{name: "Ada", active: true}`)
- ranges (`1..5`, `1...5`)
- duration literals (`5.minutes`, `2.days`)

Hash literals support label keys (`name:`), quoted string keys (`"name":`), and
hash rockets (`:name => value`, `"name" => value`, or an expression that
evaluates to a symbol or string).

Ranges with `..` include the final endpoint. Ranges with `...` exclude it.

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

Single positional calls may omit parentheses when the argument stays on
the same line:

```vibe
fees.apply amount
normalize input
```

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
- Comparison: `==`, `!=`, `<`, `<=`, `>`, `>=`
- Boolean: `&&`/`and`, `||`/`or`, unary `!`
- Conditional: `condition ? when_true : when_false`

Operator precedence follows conventional arithmetic/boolean ordering.
Exponentiation with `**` is right-associative and binds more tightly than
unary `-`, so `-2 ** 2` is parsed as `-(2 ** 2)`. Integer powers stay `int`
when the exponent is non-negative and the result fits in 64 bits; mixed
numeric powers and negative integer exponents return `float`. Integer
overflow and non-finite float powers raise runtime errors. `and` has the same
precedence as `&&`, and `or` has the same precedence as `||`. Ternary
conditionals have lower precedence than `or`, associate to the right, and
evaluate only the selected branch.

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
