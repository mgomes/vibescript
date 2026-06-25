# Control Flow and Ranges

Vibescript supports these control-flow forms:

- `if` / `elsif` / `else`
- `case` / `when` expressions
- ternary expressions with `condition ? when_true : when_false`
- `for` loops over arrays and ranges
- `while` and `until` loops
- loop control with `break` and `next`
- numeric ranges via `start..finish` and `start...finish`

## `for` loops

Ranges with `..` include the final endpoint. Ranges with `...` exclude the
final endpoint. Descending ranges use the same rule. `do` may be used as an
optional body separator after the iterable expression.

```vibe
def sum_first_five
  total = 0
  for n in 1..5 do
    total = total + n
  end
  total
end
```

```vibe
def first_four
  out = []
  for n in 1...5
    out = out + [n]
  end
  out
end
```

## `case` / `when` expressions

`case` evaluates to the matching branch expression (or `nil` when no branch matches and no `else` is provided).

```vibe
def label(score)
  case score
  when 100
    "perfect"
  when 90, 95
    "great"
  when 80..99
    "passing"
  else
    "ok"
  end
end
```

Use `then` for compact single-line branch bodies:

```vibe
case score
when 100 then "perfect"
when 90, 95 then "great"
else "ok"
end
```

`when` range candidates test numeric membership. Inclusive and exclusive
endpoints follow the same `..` / `...` range semantics used by `for` loops.
Non-range candidates still use value equality.

The same logic is available directly through the case equality operator `===`,
where the left operand is the matcher: `(80..99) === score` returns the same
result the matching `when 80..99` clause would. See
[Operators](language_reference.md#operators).

Targetless `case` evaluates each `when` expression as a predicate in order.

## Range methods

Ranges also answer query and conversion helpers such as `cover?`, `include?`,
`first`, `last`, `size`, `exclude_end?`, and `to_a`. Because Vibescript iterates
descending ranges, `size` and `to_a` report that descending sequence rather than
the empty result Ruby produces. See the
[Ranges reference](stdlib_core_utilities.md#ranges).

```vibe
def label(score)
  case
  when score == 100
    "perfect"
  when score >= 80
    "passing"
  else
    "ok"
  end
end
```

## `if` expressions

`if` / `elsif` / `else` can be used where a value expression is expected.
The selected branch expression becomes the value. When no branch matches and
there is no `else`, the expression returns `nil`.

```vibe
def label(score)
  result = if score >= 90
    "great"
  elsif score >= 80
    "passing"
  else
    "retry"
  end
  result
end
```

## Ternary expressions

Use `condition ? when_true : when_false` for short conditional values. The
condition uses normal truthiness, the expression evaluates only the selected
branch, and nested ternaries associate to the right.

```vibe
def label(active)
  active ? "active" : "inactive"
end
```

## `while` and `until`

`do` may be used as an optional body separator after the condition.

```vibe
def countdown(n)
  out = []
  while n > 0 do
    out = out + [n]
    n = n - 1
  end
  out
end

def count_up(limit)
  out = []
  n = 0
  until n >= limit do
    out = out + [n]
    n = n + 1
  end
  out
end
```

## `break` and `next`

```vibe
def odds_under_five
  out = []
  for n in [1, 2, 3, 4, 5]
    if n == 5
      break
    end
    if n % 2 == 0
      next
    end
    out = out + [n]
  end
  out
end
```

Semantics:

- In nested loops, `break` and `next` target the nearest active loop.
- `break`/`next` used outside any loop raise runtime errors.
- `break`/`next` cannot cross call boundaries (for example from block callbacks back into outer loops).

## Quotas

Loop execution participates in step and memory quotas. Infinite loops will terminate with quota errors when limits are exceeded.

Every loop iteration consumes a step, including `for` iterations over arrays and ranges. This holds even when the loop body is empty, so a large `for` loop with no body still terminates once the step quota is reached and still observes a context that was canceled by the host before iteration completes.

See `examples/control_flow/`, `examples/loops/`, and `examples/ranges/` for runnable scripts.
