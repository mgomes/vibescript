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

## Random IDs

### `uuid()`

Returns an RFC 4122 version 4 UUID string:

```vibe
event_id = uuid()
```

### `random_id(length = 16)`

Returns an alphanumeric random identifier string:

```vibe
short = random_id(8)
token = random_id()
```

## JSON

### `JSON.parse(string)`

Parses a JSON string into VibeScript values (`hash`, `array`, `string`, `int`,
`float`, `bool`, `nil`):

```vibe
payload = JSON.parse("{\"id\":\"p-1\",\"score\":10}")
payload[:score] # 10
```

### `JSON.stringify(value)`

Serializes supported values (`hash`/`object`, `array`, scalar primitives) into
a JSON string:

```vibe
raw = JSON.stringify({ id: "p-1", score: 10, tags: ["a", "b"] })
```

## Regex

### `Regex.match(pattern, text)`

Returns the first match string or `nil` when no match exists.

### `Regex.replace(text, pattern, replacement)`

Replaces the first regex match in `text`.

### `Regex.replace_all(text, pattern, replacement)`

Replaces all regex matches in `text`.

```vibe
Regex.match("ID-[0-9]+", "ID-12 ID-34")                  # "ID-12"
Regex.replace("ID-12 ID-34", "ID-[0-9]+", "X")           # "X ID-34"
Regex.replace_all("ID-12 ID-34", "ID-[0-9]+", "X")       # "X X"
Regex.replace("ID-12", "ID-([0-9]+)", "X-$1")            # "X-12"
```

## Module Loading

### `require(module_name, as: alias?)`

Loads a module from configured module search paths and returns an object containing the module's exported functions. Modules can mark exports explicitly with `export def ...`; when no explicit exports are declared, public (non-underscore) functions are exported by default. Exported functions are injected into globals only when the name is still free (existing globals keep precedence), and `as:` can be used to bind the module object explicitly:

```vibe
def calculate_total(amount)
  require("fee_calculator", as: "helpers")
  amount + helpers.calculate_fee(amount)
end
```

See `examples/module_require.md` for detailed usage patterns.
