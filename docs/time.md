# Time

VibeScript provides a `Time` object for working with instants in time, plus helpers on `Duration` for producing `Time` values (`ago`, `after`, `since`, etc.). Method declarations omit parentheses when there are no arguments:

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

`Time#to_s` uses RFC3339Nano. `Time#iso8601` and `Time#rfc3339` return RFC3339.

## Accessors and predicates

- Date/time parts: `year`, `month`/`mon`, `day`/`mday`, `hour`, `min`, `sec`, `usec`/`tv_usec`, `nsec`/`tv_nsec`, `subsec`
- Day offsets: `wday`, `yday`
- Zone/offset: `zone`, `utc_offset`/`gmt_offset`/`gmtoff`
- Flags: `utc?`, `dst?`, `sunday?`, `monday?`, ... through `saturday?`

## Conversions

- Epoch: `to_i`/`tv_sec`, `to_f`, `to_r`
- Zone conversion: `getutc`/`getgm`, `getlocal`, `utc`/`gmtime`, `localtime`
- String: `to_s` (RFC3339Nano)
- RFC3339 aliases: `iso8601`, `rfc3339`

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
