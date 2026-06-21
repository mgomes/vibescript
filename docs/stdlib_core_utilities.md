# Stdlib Method Reference

This page is the canonical reference for every built-in method and global
function the interpreter ships. Each entry is derived from the runtime member
dispatch tables, so the listing is complete: a method appears here if and only
if the interpreter implements it.

The narrative guides ([strings.md](strings.md), [arrays.md](arrays.md),
[hashes.md](hashes.md), [durations.md](durations.md), [time.md](time.md),
[builtins.md](builtins.md), [tasks.md](tasks.md)) explain idioms and patterns
in depth; this page favors compact signatures and one-line descriptions.

## How to Read Signatures

- `method(arg, optional = default, keyword: default) -> return_type` describes
  positional arguments, defaults, and keyword arguments.
- `{ |item| }` marks a method that takes a block, written
  `do |item| ... end` in Vibescript.
- `a | b` in a return type means the method returns either type.
- No method mutates its receiver. Every transform returns a new value,
  including Ruby-style bang methods. (Index and member assignment —
  `arr[0] = x`, `hash[:k] = v` — do mutate in place and are visible
  through aliases; only the method surface is copy-on-transform.)
- Bang variants (`strip!`, `gsub!`, ...) return the transformed value, or
  `nil` when nothing changed.

```vibe
"hello".strip!    # nil (nothing to strip)
"  hello ".strip! # "hello"
```

## Strings

See [strings.md](strings.md) for worked examples. Indexes and lengths count
Unicode characters, not bytes, unless noted.

### Inspection

- `size -> int` – number of characters.
- `length -> int` – alias for `size`.
- `bytesize -> int` – number of UTF-8 bytes.
- `empty? -> bool` – true when the string has no characters.
- `ord -> int` – codepoint of the first character; errors on an empty string.
- `chr -> string | nil` – first character, or `nil` for an empty string.

### Search and Matching

- `start_with?(*prefixes) -> bool` – true when the string begins with any of
  the given prefixes. Candidates are checked left to right and matching
  short-circuits, so a non-string is only rejected if reached before a match.
- `end_with?(*suffixes) -> bool` – true when the string ends with any of the
  given suffixes, with the same left-to-right short-circuit behavior.
- `include?(substring) -> bool` – true when `substring` occurs anywhere.
- `index(substring, offset = 0) -> int | nil` – first character index at or
  after `offset`; `nil` when not found.
- `rindex(substring, offset = size) -> int | nil` – last character index at or
  before `offset`; `nil` when not found.
- `match(pattern) -> array | nil` – regex match returning
  `[full, capture1, ...]` (unmatched groups are `nil`); `nil` when no match.
- `scan(pattern) -> array` – all non-overlapping full regex matches.

`match` and `scan` treat `pattern` as a regex and enforce the
[regex guard limits](#guard-limits).

```vibe
"2024-03-05".match("([0-9]+)-([0-9]+)") # ["2024-03", "2024", "03"]
```

### Slicing and Concatenation

- `slice(index) -> string | nil` – single character at `index`; `nil` when out
  of bounds.
- `slice(index, length) -> string | nil` – substring of up to `length`
  characters starting at `index`.
- `concat(*strings) -> string` – receiver with all arguments appended.
- `replace(replacement) -> string` – returns `replacement` (compatibility
  shim for Ruby's mutating `replace`).
- `clear -> string` – returns `""`.

### Case and Ordering Transforms

- `upcase -> string` – uppercase all characters (Unicode case mapping,
  locale-insensitive).
- `downcase -> string` – lowercase all characters.
- `capitalize -> string` – uppercase the first character, lowercase the rest.
- `swapcase -> string` – flip the case of each letter.
- `reverse -> string` – characters in reverse order.

### Whitespace and Affix Trimming

- `strip -> string` – trim leading and trailing whitespace.
- `lstrip -> string` – trim leading whitespace.
- `rstrip -> string` – trim trailing whitespace.
- `squish -> string` – trim both ends and collapse internal whitespace runs to
  a single space.
- `chomp(separator = nil) -> string` – remove one trailing `"\r\n"`, `"\n"`,
  or `"\r"`; with a `separator` remove that suffix once; with `""` remove all
  trailing newlines.
- `delete_prefix(prefix) -> string` – remove `prefix` when present.
- `delete_suffix(suffix) -> string` – remove `suffix` when present.

### Replacement, Splitting, and Templating

- `sub(pattern, replacement, regex: false) -> string` – replace the first
  occurrence of `pattern`.
- `gsub(pattern, replacement, regex: false) -> string` – replace every
  occurrence of `pattern`.
- `split(separator = nil) -> array` – split on whitespace (dropping empty
  fields) without arguments, or on `separator` when given.
- `chars -> array` – array of the string's Unicode characters, one per code
  point (rune-aware, like `length` and `slice`).
- `lines -> array` – array of lines split on `"\n"`, retaining the trailing
  newline on each line; an empty string yields no lines and carriage returns
  stay attached so `"\r\n"` endings round-trip.
- `template(context, strict: false) -> string` – interpolate `{{key.path}}`
  placeholders from a hash; `strict: true` errors on missing placeholders.

With `regex: true`, `sub`/`gsub` compile `pattern` as a regex, support `$1`
style group expansion in `replacement`, and enforce the
[regex guard limits](#guard-limits).

### Bang Variants

Each of the following returns the transformed string, or `nil` when the
transform changed nothing: `strip!`, `lstrip!`, `rstrip!`, `squish!`,
`chomp!`, `delete_prefix!`, `delete_suffix!`, `upcase!`, `downcase!`,
`capitalize!`, `swapcase!`, `reverse!`, `sub!`, `gsub!`.

## Arrays

See [arrays.md](arrays.md) for worked examples. Arrays also support `+`
(concatenation) and `-` (value subtraction) operators.

### Inspection

- `size -> int` – element count.
- `length -> int` – alias for `size`.
- `empty? -> bool` – true when the array has no elements.

### Iteration

- `each { |item| } -> array` – yield each element; returns the receiver.
- `map { |item| } -> array` – new array of block results.
- `select { |item| } -> array` – elements for which the block is truthy.
- `find { |item| } -> value | nil` – first element matching the block.
- `find_index { |item| } -> int | nil` – index of the first match.
- `reduce(initial = nil) { |acc, item| } -> value` – fold left; without
  `initial` the first element seeds the accumulator (errors on an empty
  array).

### Membership and Counting

- `include?(value) -> bool` – membership test using value equality.
- `index(value, offset = 0) -> int | nil` – first index of `value` at or
  after `offset`.
- `rindex(value, offset = last_index) -> int | nil` – last index of `value`
  at or before `offset`.
- `fetch(index, default = nil) -> value` – element at `index`, or
  `default`/`nil` when out of bounds.
- `count -> int` – element count.
- `count(value) -> int` – occurrences of `value`.
- `count { |item| } -> int` – elements for which the block is truthy.
- `any? { |item| } -> bool` – true when any element (or block result) is
  truthy.
- `all? { |item| } -> bool` – true when every element (or block result) is
  truthy.
- `none? { |item| } -> bool` – true when no element (or block result) is
  truthy.

### Building and Slicing

- `push(*values) -> array` – new array with `values` appended.
- `pop(n = nil) -> hash` – returns `{ array:, popped: }`; bare `pop` pops one
  element (`popped` is the value or `nil`), `pop(n)` pops up to `n` elements
  (`popped` is an array).
- `first -> value | nil` / `first(n) -> array` – leading element(s).
- `last -> value | nil` / `last(n) -> array` – trailing element(s).
- `uniq -> array` – distinct values, keeping first occurrences.
- `compact -> array` – elements with `nil` entries removed.
- `flatten(depth = nil) -> array` – collapse nested arrays; flattens fully
  without a depth.
- `chunk(size) -> array` – consecutive slices of `size` elements (last chunk
  may be shorter).
- `window(size) -> array` – overlapping windows of `size` elements; empty when
  `size` exceeds the array length.
- `join(separator = "") -> string` – stringified elements joined by
  `separator`.
- `reverse -> array` – elements in reverse order.

Because array methods never mutate the receiver, `pop` hands back both
halves of the result:

```vibe
items = [1, 2, 3]
items.pop    # {array: [1, 2], popped: 3}
items.pop(2) # {array: [1], popped: [2, 3]}
```

### Aggregation, Ordering, and Grouping

- `sum -> int | float` – total of numeric elements (`0` for an empty array).
- `sort -> array` – stable sort using natural ordering.
- `sort { |a, b| } -> array` – stable sort using a comparator block returning
  a negative, zero, or positive number.
- `sort_by { |item| } -> array` – stable sort by the block's key for each
  element.
- `partition { |item| } -> array` – `[matching, non_matching]` pair of arrays.
- `group_by { |item| } -> hash` – group elements by block result (must be a
  symbol or string).
- `group_by_stable { |item| } -> array` – `[key, items]` pairs preserving
  first-seen group order.
- `tally -> hash` / `tally { |item| } -> hash` – occurrence counts keyed by
  element (or block result); keys must be symbols or strings.
- `min -> value | nil` / `max -> value | nil` – smallest/largest element using
  natural ordering; `nil` for an empty array.
- `minmax -> array` – `[min, max]` in one pass; `[nil, nil]` for an empty array.
- `min_by { |item| } -> value | nil` / `max_by { |item| } -> value | nil` –
  element with the smallest/largest block key; `nil` for an empty array. Ties
  resolve to the first matching element.

String and symbol ordering uses deterministic codepoint comparison (no locale
collation).

```vibe
[5, 1, 4].sort do |a, b|
  b - a
end
# [5, 4, 1]
```

## Hashes

See [hashes.md](hashes.md) for worked examples. Hash keys use one string lookup
space; shorthand symbol labels and quoted string keys normalize to the same
entries. `keys`, `values`, and all block-based iteration visit entries in
sorted key order for determinism.

Property access (`record.name`) resolves the hash methods below before stored
keys, so method names stay stable even when data contains the same key:

```vibe
sizes = { size: "XL" }
sizes.size            # 1
sizes[:size]          # "XL"
{ color: "red" }.size # 1
```

Use index access (`hash[:size]`) to read entries whose names collide with hash
methods.

### Inspection

- `size -> int` – entry count.
- `length -> int` – alias for `size`.
- `empty? -> bool` – true when the hash has no entries.
- `key?(key) -> bool` – true when `key` is present.
- `has_key?(key) -> bool` – alias for `key?`.
- `member?(key) -> bool` – alias for `key?`.
- `include?(key) -> bool` – alias for `key?`.
- `value?(value) -> bool` – true when any stored value equals `value` using `==`.
- `has_value?(value) -> bool` – alias for `value?`.

### Access

- `fetch(key, default = nil) -> value` – value for `key`, or `default`/`nil`
  when missing.
- `dig(*keys) -> value | nil` – nested lookup following `keys`; `nil` when any
  step is missing.
- `keys -> array` – symbol keys in sorted order.
- `values -> array` – values in sorted key order.

### Iteration

- `each { |key, value| } -> hash` – yield each pair; returns the receiver.
- `each_key { |key| } -> hash` – yield each key.
- `each_value { |value| } -> hash` – yield each value.

### Transform and Filter

- `merge(other) -> hash` – combined entries; `other` wins on key conflicts.
- `store(key, value) -> hash` – new hash with `key` assigned to `value`; the
  receiver is left unchanged (immutable-style, unlike Ruby's mutating `store`).
- `slice(*keys) -> hash` – only the listed keys (missing keys are skipped).
- `except(*keys) -> hash` – all entries except the listed keys.
- `select { |key, value| } -> hash` – entries for which the block is truthy.
- `reject { |key, value| } -> hash` – entries for which the block is falsy.
- `compact -> hash` – entries with `nil` values removed.
- `transform_keys { |key| } -> hash` – rename keys via the block (must return
  a symbol or string).
- `deep_transform_keys { |key| } -> hash` – `transform_keys` applied
  recursively through nested hashes and arrays; rejects cyclic structures.
- `remap_keys(mapping) -> hash` – rename keys using a `{ old: :new }` hash;
  unmapped keys pass through.
- `transform_values { |value| } -> hash` – replace each value with the block
  result.

## Integers

### Duration Constructors

Each returns a `duration` spanning that many units. Singular forms are
aliases, so `1.second` reads naturally.

- `seconds` / `second` -> duration
- `minutes` / `minute` -> duration
- `hours` / `hour` -> duration
- `days` / `day` -> duration
- `weeks` / `week` -> duration

### Numeric Helpers

- `abs -> int` – absolute value; errors on the minimum 64-bit integer.
- `clamp(min, max) -> int` – receiver bounded to `[min, max]`; both bounds
  must be integers with `min <= max`.
- `even? -> bool` – true for even integers.
- `odd? -> bool` – true for odd integers.
- `times { |i| } -> int` – run the block with `0..n-1`; returns the receiver.
- `zero? -> bool` – true when the integer is `0`.
- `positive? -> bool` – true when greater than `0`.
- `negative? -> bool` – true when less than `0`.
- `nonzero? -> int?` – the receiver when nonzero, otherwise `nil`, matching
  Ruby (the result is truthy exactly when the number is nonzero).
- `next -> int` / `succ -> int` – the next integer (`self + 1`); errors on
  64-bit overflow rather than wrapping.
- `pred -> int` – the previous integer (`self - 1`); errors on 64-bit
  underflow rather than wrapping.
- `div(n) -> int` – floored division; the result has the divisor's sign for
  mixed-sign operands. A zero divisor errors, and the one quotient outside the
  64-bit range (`min_int.div(-1)`) errors rather than wrapping.
- `divmod(n) -> [quotient, modulo]` – the floored quotient and the modulo whose
  sign follows the divisor. With integer arguments both elements are integers;
  a float argument makes the modulo a float.
- `fdiv(n) -> float` – floating division. Unlike Ruby, a zero divisor errors
  rather than yielding infinity, matching the `/` operator.
- `remainder(n) -> int|float` – remainder whose sign follows the receiver
  (truncated division), which differs from `%` for operands of opposite sign;
  a zero divisor errors.
- `modulo(n) -> int|float` – the `%` operator as a method: the result's sign
  follows the divisor (floored division). Integer operands yield an integer;
  any float operand yields a float; a zero divisor errors.

## Floats

- `abs -> float` – absolute value.
- `clamp(min, max) -> float` – receiver bounded to `[min, max]`; bounds may be
  int or float with `min <= max`.
- `round -> int` – round half away from zero; errors when the result overflows
  a 64-bit integer.
- `floor -> int` – round toward negative infinity.
- `ceil -> int` – round toward positive infinity.
- `zero? -> bool` – true when the value is `0.0`.
- `positive? -> bool` – true when greater than `0.0`.
- `negative? -> bool` – true when less than `0.0`.
- `nonzero? -> float?` – the receiver when nonzero, otherwise `nil`, matching
  Ruby (the result is truthy exactly when the number is nonzero).
- `div(n) -> int` – floored division returning an integer; a zero divisor
  errors, as does a quotient outside the 64-bit range.
- `divmod(n) -> [int, float]` – the floored quotient (an integer) and the
  float modulo whose sign follows the divisor.
- `fdiv(n) -> float` – floating division; a zero divisor errors rather than
  yielding infinity, matching the `/` operator.
- `remainder(n) -> float` – remainder whose sign follows the receiver
  (truncated division); a zero divisor errors.
- `modulo(n) -> float` – the `%` operator as a method: the result's sign
  follows the divisor (floored division); a zero divisor errors.

`round`, `floor`, and `ceil` take no precision argument and always return an
`int`.

Vibescript has no rational number type, so Ruby's `quo` (which returns a
`Rational` for integer operands) is intentionally not provided; use `fdiv` for
floating division.

## Money

Money values are created with the `money` and `money_cents` builtins and
support arithmetic and comparison operators.

- `currency -> string` – ISO currency code, e.g. `"USD"`.
- `cents -> int` – total amount in minor units.
- `amount -> string` – formatted amount with currency, e.g. `"100.50 USD"`.
- `format -> string` – same as `amount`.

```vibe
m = money("100.50 USD")
m.cents    # 10050
m.currency # "USD"
m.amount   # "100.50 USD"
```

## Durations

See [durations.md](durations.md) for arithmetic and worked examples.

### Whole-Unit Conversions

Each returns an `int` truncated toward zero. Singular forms are aliases.

- `seconds` / `second` -> int – total seconds.
- `minutes` / `minute` -> int – total whole minutes.
- `hours` / `hour` -> int – total whole hours.
- `days` / `day` -> int – total whole days.
- `weeks` / `week` -> int – total whole weeks.

### Fractional Conversions

Each returns a `float`. Months use 30-day and years 365-day approximations.

- `in_seconds -> float`
- `in_minutes -> float`
- `in_hours -> float`
- `in_days -> float`
- `in_weeks -> float`
- `in_months -> float` – approximate (30-day months).
- `in_years -> float` – approximate (365-day years).

```vibe
90.seconds.minutes    # 1 (truncated)
90.seconds.in_minutes # 1.5
```

### Formatting and Conversion

- `iso8601 -> string` – ISO 8601 duration, e.g. `"PT1H30M"`.
- `parts -> hash` – `{ days:, hours:, minutes:, seconds: }` breakdown.
- `to_i -> int` – total seconds.
- `to_s -> string` – seconds string, e.g. `"5400s"`.
- `format -> string` – same as `to_s`.
- `eql?(other) -> bool` – true when both durations span the same seconds.

```vibe
shift = 90.minutes
shift.parts   # {days: 0, hours: 1, minutes: 30, seconds: 0}
shift.iso8601 # "PT1H30M"
```

### Anchoring to Times

Each accepts an optional `Time` (or RFC3339 string) and defaults to the
current time; the result is a UTC `Time`.

- `after(start = Time.now) -> time` – `start` plus the duration.
- `since(start = Time.now) -> time` – alias for `after`.
- `from_now(start = Time.now) -> time` – alias for `after`.
- `ago(start = Time.now) -> time` – `start` minus the duration.
- `before(start = Time.now) -> time` – alias for `ago`.
- `until(start = Time.now) -> time` – alias for `ago`.

```vibe
5.minutes.ago(Time.utc(2024, 1, 1)).iso8601 # "2023-12-31T23:55:00Z"
```

## Times

See [time.md](time.md) for construction, zone handling, and layout-based
formatting. Times also support `time + duration`, `time - duration`, and
`time - time -> duration` arithmetic.

### Components

- `year -> int` – calendar year.
- `month` / `mon` -> int – month of year (1-12).
- `day` / `mday` -> int – day of month.
- `hour -> int` – hour of day (0-23).
- `min -> int` – minute of hour.
- `sec -> int` – second of minute.
- `usec` / `tv_usec` -> int – microsecond component.
- `nsec` / `tv_nsec` -> int – nanosecond component.
- `subsec -> float` – fractional second as a float.
- `wday -> int` – day of week (0 = Sunday).
- `yday -> int` – day of year (1-366).

### Zone and Offset

- `zone -> string` – zone abbreviation, e.g. `"UTC"`.
- `utc_offset` / `gmt_offset` / `gmtoff` -> int – offset from UTC in seconds.

### Predicates

- `utc?` / `gmt?` -> bool – true for UTC times.
- `dst?` / `isdst` -> bool – true when daylight saving time is in effect.
- `sunday?`, `monday?`, `tuesday?`, `wednesday?`, `thursday?`, `friday?`,
  `saturday?` -> bool – day-of-week checks.

### Conversions

- `to_i` / `tv_sec` -> int – seconds since the Unix epoch.
- `to_f -> float` – epoch seconds with fractional part.
- `to_r -> float` – same as `to_f` (rationals are not supported).
- `to_s -> string` – RFC3339Nano representation.
- `to_a -> array` – positional tuple `[sec, min, hour, mday, month, year, wday,
  yday, isdst, zone]`, matching Ruby's field order and the receiver's zone.
- `iso8601` / `rfc3339` -> string – RFC3339 representation.
- `hash -> int` – nanoseconds since the Unix epoch (identity value).

### Zone Conversion

- `utc` / `gmtime` -> time – the same instant in UTC.
- `getutc` / `getgm` -> time – aliases for `utc`.
- `localtime -> time` – the same instant in the host's local zone.
- `getlocal -> time` – alias for `localtime`.

### Formatting

- `format(layout) -> string` – format with a Go layout string (reference time
  `Mon Jan 2 15:04:05 MST 2006`).
- `strftime` – not supported; raises an error directing you to `format`.

### Comparison and Rounding

- `<=>(other) -> int` – `-1`, `0`, or `1` ordering against another time.
- `eql?(other) -> bool` – true when both times are the same instant.
- `round(ndigits = 0) -> time` – round to the given number of fractional-second
  digits, half away from zero. No argument or `0` rounds to whole seconds;
  positive `ndigits` rounds to that many digits (e.g. `3` for milliseconds, `6`
  for microseconds), capped at nanosecond resolution. `ndigits` must be a
  non-negative `Integer`; other values raise an error.
- `floor -> time` – truncate to the whole second.
- `ceil -> time` – round up to the next whole second.

## Enum Values

Enum members obtained via `EnumName::member` expose three properties (see
[enums.md](enums.md)):

- `name -> string` – member name, e.g. `"active"`.
- `symbol -> symbol` – member symbol, e.g. `:active`.
- `enum -> enum` – the defining enum.

## Ranges

Ranges (`1..5`, `1...5`) have no methods; they are consumed by `for ... in`
loops. `case`/`when` uses range candidates as numeric membership tests. See
[control-flow.md](control-flow.md).

## Builtin Functions

Global functions and namespaces available in every script. See
[builtins.md](builtins.md) for narrative examples.

### Global Functions

- `assert(condition, message = nil, message: nil) -> nil` – raise an assertion
  failure when `condition` is falsy; the message comes from the second
  positional argument or the `message:` keyword.
- `money(literal) -> money` – parse a `"amount CURRENCY"` string, e.g.
  `money("25.00 USD")`.
- `money_cents(cents, currency) -> money` – build money from integer minor
  units, e.g. `money_cents(2550, "USD")`.
- `now -> string` – current UTC instant as an RFC3339 string (use `Time.now`
  for a `time` value).
- `uuid -> string` – RFC 9562 version 7 UUID.
- `random_id(length = 16) -> string` – unbiased alphanumeric token; `length`
  must be between 1 and 1024.
- `to_int(value) -> int` – convert an int, integral float, or base-10 numeric
  string; errors otherwise.
- `to_float(value) -> float` – convert an int, float, or finite numeric
  string; errors otherwise.
- `require(module_name, as: nil) -> object` – load a module and return its
  exports; `as:` binds the module object to a name. See
  [builtins.md](builtins.md#module-loading).

`now` and `uuid` auto-invoke, so they can be called without parentheses.

### JSON

- `JSON.parse(string) -> value` – parse JSON into hashes, arrays, strings,
  ints, floats, bools, and nils; rejects trailing data.
- `JSON.stringify(value) -> string` – serialize hashes/objects, arrays, and
  scalars; symbols and enum values become strings; rejects cyclic structures.

Both directions enforce a 1 MiB payload limit and reject more than 10,000
nested arrays/objects.

### Regex

Note the argument order: `match` takes the pattern first, while the replace
helpers take the text first.

Regex patterns are quoted strings. Ruby-style `/pattern/` regex literals are
not supported.

- `Regex.match(pattern, text) -> string | nil` – first match, or `nil`.
- `Regex.replace(text, pattern, replacement) -> string` – replace the first
  match; `replacement` supports `$1` style group expansion.
- `Regex.replace_all(text, pattern, replacement) -> string` – replace every
  match.

```vibe
Regex.match("ID-[0-9]+", "ID-12 ID-34")       # "ID-12"
Regex.replace("ID-12", "ID-([0-9]+)", "X-$1") # "X-12"
```

Patterns use Go's RE2 syntax and enforce the
[regex guard limits](#guard-limits).

### Duration

- `Duration.build(seconds) -> duration` – build from total seconds.
- `Duration.build(weeks:, days:, hours:, minutes:, seconds:) -> duration` –
  build from named parts; at least one part is required (a bare
  `Duration.build()` errors), and positional seconds and named parts are
  mutually exclusive.
- `Duration.parse(string) -> duration` – parse Go duration strings (`"1h30m"`,
  whole seconds only) or ISO 8601 durations (`"PT90S"`, `"P2W"`).

```vibe
Duration.parse("1h30m").seconds # 5400
Duration.parse("P2W").days      # 14
```

### Time

Zone keywords accept IANA names (`"America/New_York"`), `"UTC"`/`"GMT"`,
`"LOCAL"`, or numeric offsets like `"+05:30"`.

- `Time.new(year, month, day, hour = 0, min = 0, sec = 0, zone = nil,
  in: nil) -> time` – build from calendar parts (local zone by default).
- `Time.local(...)` / `Time.mktime(...)` -> time – like `Time.new` with the
  local zone as the default; an explicit zone argument still overrides it.
- `Time.utc(...)` / `Time.gm(...)` -> time – like `Time.new` with UTC as the
  default; an explicit zone argument still overrides it
  (`Time.utc(2024, 1, 1, 0, 0, 0, "+05:30")` is `+05:30`, not UTC).
- `Time.at(epoch_seconds, in: nil) -> time` – build from Unix epoch seconds
  (int or float).
- `Time.now(in: nil) -> time` – current time (local zone by default).
- `Time.parse(string, layout = nil, in: nil) -> time` – parse a time string;
  without a layout it tries RFC3339/RFC3339Nano, RFC1123/RFC1123Z,
  `YYYY-MM-DD[THH:MM:SS]`, `YYYY-MM-DD HH:MM:SS`, `YYYY/MM/DD[ HH:MM:SS]`,
  and `MM/DD/YYYY[ HH:MM:SS]`.

### Tasks

Structured concurrency entry points; see [tasks.md](tasks.md) for the task
manager API, retention rules, and concurrency settings.

- `Tasks.run(max: nil) { |tasks| } -> value` – run a block with a task
  manager for spawning concurrent work; returns the block's value.
- `Tasks.map(items, with: function_name, max: nil) -> array` – apply a named
  function to each element concurrently, preserving order.

The manager passed to the `Tasks.run` block exposes two methods, and
`spawn` returns a task handle with one; all of them raise once the task
scope has exited:

- `tasks.spawn(function_name, args..., keyword: ...) -> task` – start the
  named function concurrently with the given arguments; returns a handle.
- `tasks.wait -> nil` – block until every spawned task has finished.
- `task.value -> value` – wait for this task and return its result,
  raising the task's error if it failed.

## Guard Limits

JSON, regex, and ID helpers enforce fixed input-guard limits so hostile
data cannot exhaust host memory or CPU. The limits are not configurable
and apply to the `JSON`/`Regex` builtins and the regex-enabled string
members (`match`, `scan`, `sub`, `gsub`, and their `!` variants):

| Guard | Limit |
| --- | --- |
| `JSON.parse` input / `JSON.stringify` output | 1 MiB |
| `JSON.parse` / `JSON.stringify` nesting depth | 10,000 arrays/objects |
| Regex pattern size (`Regex.*`, `match`, `scan`, `sub`/`gsub` with `regex: true`) | 16 KiB |
| Regex text, replacement, and output size | 1 MiB |
| `random_id` length | 1024 characters |

Exceeding a limit raises a runtime error naming the offending guard.
The canonical values live in the documented const block in
`internal/runtime/limits.go`; the README's "Runtime Sandbox & Limits"
section summarizes them alongside the configurable engine quotas.

For a runnable end-to-end sample, see `examples/stdlib/core_utilities.vibe`.
