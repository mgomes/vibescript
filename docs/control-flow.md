# Control Flow and Ranges

VibeScript supports familiar Ruby control structures:

- `if/elsif/else` expressions
- `for` loops iterating arrays or ranges
- Numeric ranges via `start..finish`

Example range usage:

```vibe
def fizzbuzz(limit)
  for n in 1..limit
    if n % 15 == 0
      puts("FizzBuzz")
    elsif n % 3 == 0
      puts("Fizz")
    elsif n % 5 == 0
      puts("Buzz")
    else
      puts(n)
    end
  end
end
```

See `examples/loops/` and `examples/ranges/` for runnable scripts.
