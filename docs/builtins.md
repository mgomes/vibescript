# Built-in Functions

VibeScript provides several built-in functions available globally in all scripts.

## Assertions

### `assert(condition, message)`

Raises an error if `condition` is falsy. Use for validating preconditions.

```vibe
def validate_amount(amount)
  assert amount > 0, "amount must be positive"
  amount
end
```

## Money

### `money(string)`

Parses a money value from a string in the format `"amount CURRENCY"`:

```vibe
total = money("100.50 USD")
fee = money("2.50 USD")
net = total - fee  # money("98.00 USD")
```

### `money_cents(cents, currency)`

Creates a money value from an integer cent amount:

```vibe
price = money_cents(2550, "USD")  # $25.50 USD
```

## Time

### `now()`

Returns the current UTC timestamp as an ISO 8601 / RFC 3339 formatted string:

```vibe
def log_event(name)
  {
    event: name,
    timestamp: now()
  }
end

# Returns: { event: "user_signup", timestamp: "2025-01-15T10:30:45Z" }
```

**Note:** The `now()` function returns a string, not a time object. This is suitable for logging and timestamping.

For time manipulation in VibeScript, use the `Time` object (`Time.now`, `Time.parse`, `Time.utc`, etc.). See `docs/time.md`.

## Module Loading

### `require(module_name, as: alias?)`

Loads a module from configured module search paths and returns an object containing the module's exported functions. Modules can mark exports explicitly with `export def ...`; when no explicit exports are declared, public (non-underscore) functions are exported by default:

```vibe
def calculate_total(amount)
  require("fee_calculator", as: "helpers")
  amount + helpers.calculate_fee(amount)
end
```

See `examples/module_require.md` for detailed usage patterns.
