# Control Flow and Ranges

VibeScript supports these control-flow forms:

- `if` / `elsif` / `else`
- `for` loops over arrays and ranges
- `while` and `until` loops
- loop control with `break` and `next`
- numeric ranges via `start..finish`

## `for` loops

```vibe
def sum_first_five()
  total = 0
  for n in 1..5
    total = total + n
  end
  total
end
```

## `while` and `until`

```vibe
def countdown(n)
  out = []
  while n > 0
    out = out + [n]
    n = n - 1
  end
  out
end

def count_up(limit)
  out = []
  n = 0
  until n >= limit
    out = out + [n]
    n = n + 1
  end
  out
end
```

## `break` and `next`

```vibe
def odds_under_five()
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

See `examples/control_flow/`, `examples/loops/`, and `examples/ranges/` for runnable scripts.
