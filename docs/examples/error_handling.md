# Guarding Against Bad Input

```vibe
# vibe: 0.2

# Ensures donations are positive and in the supported currency.
def validate_donation(donation)
  assert donation[:amount] > money("0.00 USD"), "donation must be positive"
  assert donation[:currency] == "USD", "only USD donations supported"
  donation
end

# Guards required hash keys.
def require_keys(record, keys)
  keys.each do |key|
    assert record[key] != nil, "missing #{key}"
  end
  record
end

# Normalizes optional metadata when present.
def normalize_metadata(record)
  meta = record[:meta]
  if meta == nil
    return record.merge(meta: {})
  end

  clean = meta.merge({
    tag: meta[:tag] || "uncategorized",
    source: meta[:source] || "unknown"
  })
  record.merge(meta: clean)
end
```
