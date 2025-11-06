# Duration Methods

VibeScript provides duration literals and helper methods for time-based calculations.

## Creating Durations

Duration literals can be created using numeric values with time unit suffixes:

```vibe
reminder = 5.minutes
timeout = 2.hours
delay = 30.seconds
long_wait = 7.days
```

Supported units: `second`/`seconds`, `minute`/`minutes`, `hour`/`hours`, `day`/`days`

## Duration Methods

### `seconds`

Returns the total duration in seconds as an integer:

```vibe
(2.minutes).seconds  # 120
(1.hour).seconds     # 3600
```

### `minutes`

Returns the duration converted to minutes, truncated to a whole number:

```vibe
(90.seconds).minutes  # 1 (truncated from 1.5)
(2.hours).minutes     # 120
```

**Note:** This method truncates fractional minutes. Use `.seconds / 60` if you need fractional values.

### `hours`

Returns the duration converted to hours, truncated to a whole number:

```vibe
(7200.seconds).hours  # 2
(90.minutes).hours    # 1 (truncated from 1.5)
```

**Note:** This method truncates fractional hours. Use `.seconds / 3600` if you need fractional values.

### `format`

Returns a string representation of the duration:

```vibe
(2.hours).format    # "7200s"
(30.seconds).format # "30s"
```

## Arithmetic

Durations support basic arithmetic operations:

```vibe
# Addition
total = 1.hour + 30.minutes  # 5400 seconds

# Subtraction
remaining = 2.hours - 15.minutes  # 6300 seconds

# Comparison
if delay > 5.minutes
  # ...
end
```

## Example: Scheduling

```vibe
def schedule_reminder(event, advance_notice)
  event_time = event[:scheduled_at]
  reminder_time = event_time - advance_notice

  {
    event_id: event[:id],
    reminder_at: reminder_time,
    advance_seconds: advance_notice.seconds
  }
end

schedule_reminder(
  { id: 123, scheduled_at: "2025-01-15T10:00:00Z" },
  30.minutes
)
```
