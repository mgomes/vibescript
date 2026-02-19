# Core Stdlib Utilities

This page covers the core utility helpers added in v0.18.

## JSON

```vibe
def parse_payload(raw)
  JSON.parse(raw)
end

def emit_payload(record)
  JSON.stringify(record)
end
```

## Regex

```vibe
def normalize_ids(text)
  Regex.replace_all(text, "ID-[0-9]+", "X")
end

def first_id(text)
  Regex.match("ID-[0-9]+", text)
end
```

## Random IDs

```vibe
def new_event_id()
  uuid()
end

def short_token()
  random_id(8)
end
```

## Numeric Conversion

```vibe
def parse_score(raw_score)
  to_int(raw_score)
end

def parse_ratio(raw_ratio)
  to_float(raw_ratio)
end
```

## Common Time Parsing

`Time.parse` accepts common formats without manually passing a layout:

- RFC3339 / RFC3339Nano
- RFC1123 / RFC1123Z
- `YYYY-MM-DD` and `YYYY-MM-DD HH:MM:SS`
- `YYYY/MM/DD` and `YYYY/MM/DD HH:MM:SS`
- `MM/DD/YYYY` and `MM/DD/YYYY HH:MM:SS`

```vibe
def parse_seen_at(raw)
  Time.parse(raw, in: "UTC")
end
```
