# Language Reference

This is the consolidated reference for VibeScript syntax and core semantics.
Use this with focused guides in `docs/` for deeper examples.

## Source Structure

- Files are UTF-8 text, typically with `.vibe` extension.
- `#` starts a comment that runs to end-of-line.
- Top-level declarations are functions and classes.
- Expressions can be used as statements.

## Values and Literals

VibeScript supports these literal/value categories:

- `nil`, `true`, `false`
- integers and floats (`1`, `42`, `3.14`)
- strings (`"hello"`)
- symbols (`:name`)
- arrays (`[1, 2, 3]`)
- hashes (`{name: "Ada", active: true}`)
- ranges (`1..5`, `1...5`)
- duration literals (`5.minutes`, `2.days`)

See `docs/arrays.md`, `docs/hashes.md`, `docs/strings.md`, `docs/durations.md`,
and `docs/time.md` for full method coverage.

## Variables and Assignment

Variables are dynamically bound by assignment:

```vibe
total = 0
total = total + 10
```

Index assignment is supported for mutable collections:

```vibe
items = [1, 2, 3]
items[0] = 10
```

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

## Method Calls

Calls support positional args and keyword args:

```vibe
fees.apply(amount)
require("billing/rules", as: "rules")
```

Blocks can be passed with `do ... end`:

```vibe
numbers.map do |n|
  n * 2
end
```

## Operators

Core operator families:

- Arithmetic: `+`, `-`, `*`, `/`, `%`
- Comparison: `==`, `!=`, `<`, `<=`, `>`, `>=`
- Boolean: `and`, `or`, unary `!`

Operator precedence follows conventional arithmetic/boolean ordering.

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
- `while`
- `until`
- `for ... in`
- `break`
- `next`
- `return`

## Error Handling

Raise explicit failures:

```vibe
raise("missing configuration")
```

Structured handling supports `rescue`/`ensure`:

```vibe
def run
  risky()
rescue
  "fallback"
ensure
  cleanup()
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
