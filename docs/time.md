# Time

Vibescript provides a `Time` object for working with instants in time, plus helpers on `Duration` for producing `Time` values (`ago`, `after`, `since`, etc.). Method declarations omit parentheses when there are no arguments:

```vibe
def midnight_utc
  Time.utc(2024, 1, 1)
end
```

## Creating Time values

- `Time.new(year, month, day, hour=0, min=0, sec=0, zone=nil, in: zone)`
- `Time.local(...)` / `Time.mktime(...)`
- `Time.utc(...)` / `Time.gm(...)`
- `Time.at(seconds_since_epoch, in: zone)`
- `Time.now(in: zone)`
- `Time.parse(string, layout=nil, in: zone)`

Zones accept Go-style names (e.g. `"America/New_York"`), `"UTC"`/`"GMT"`, `"LOCAL"`, or numeric offsets like `"+05:30"`.
Without an explicit `layout`, `Time.parse` accepts common formats such as RFC3339/RFC1123, `YYYY-MM-DD`, `YYYY/MM/DD`, `YYYY-MM-DD HH:MM:SS`, and `MM/DD/YYYY` (with optional time).

## Formatting

Use Go layouts with `Time#format` (not `strftime`). Layouts are built from the reference time `Mon Jan 2 15:04:05 MST 2006`:

```vibe
def formatted_timestamp
  t = Time.utc(2000, 1, 1, 20, 15, 1)
  {
    short_date: t.format("2006-01-02"),
    long_time: t.format("15:04:05"),
    rfc3339:   t.format("2006-01-02T15:04:05Z07:00")
  }
end
```

`Time#to_s` uses RFC3339Nano. `Time#iso8601` and `Time#rfc3339` return RFC3339, defaulting to whole-second precision.

Both accept an optional `ndigits` argument (matching Ruby's `Time#iso8601(ndigits = 0)`) to append fractional-second digits. Fractional seconds are truncated toward zero, and requesting more digits than the nanosecond clock can resolve zero-pads the remainder:

```vibe
def fractional_timestamps
  t = Time.parse("1970-01-01T00:00:00.123456Z")
  {
    seconds:      t.iso8601,     # "1970-01-01T00:00:00Z"
    milliseconds: t.iso8601(3),  # "1970-01-01T00:00:00.123Z"
    microseconds: t.iso8601(6),  # "1970-01-01T00:00:00.123456Z"
    padded:       t.iso8601(12)  # "1970-01-01T00:00:00.123456000000Z"
  }
end
```

A negative `ndigits`, a non-integer argument, more than one argument, or a precision above 100 digits raises a runtime error.

## Accessors and predicates

- Date/time parts: `year`, `month`/`mon`, `day`/`mday`, `hour`, `min`, `sec`, `usec`/`tv_usec`, `nsec`/`tv_nsec`, `subsec`
- Day offsets: `wday`, `yday`
- Zone/offset: `zone`, `utc_offset`/`gmt_offset`/`gmtoff`
- Flags: `utc?`, `dst?`, `sunday?`, `monday?`, ... through `saturday?`

## Conversions

- Epoch: `to_i`/`tv_sec`, `to_f`, `to_r`
- Zone conversion: `getutc`/`getgm`, `utc`/`gmtime`, and `getlocal(offset = nil)`/`localtime(offset = nil)`. With no argument the latter two convert to the host's local zone; passing a zone such as `"+05:30"`, `"-04:00"`, `"America/New_York"`, or `"UTC"` returns the same instant in that zone. They always return a new `Time` rather than mutating the receiver.
- String: `to_s` (RFC3339Nano)
- RFC3339 aliases: `iso8601(ndigits = 0)`, `rfc3339(ndigits = 0)` (optional fractional-second precision)
- Tuple: `to_a` returns `[sec, min, hour, mday, month, year, wday, yday, isdst, zone]`, matching Ruby's positional field order. Field values reuse the individual accessors, so the result reflects the receiver's UTC/local/offset zone.

## Comparisons and math

- Compare: `<=>`, `eql?`
- Add/sub durations: `time + duration`, `time - duration`
- Difference of times: `time - time` → `Duration`

## Duration shortcuts that yield Time

Durations can move times forward/backward:

```vibe
def five_minutes_ago
  5.minutes.ago(Time.now)
end

def in_two_hours
  2.hours.after(Time.now)
end

```
