- **Added: optional shape fields.** A `?` on a shape field name marks the
  field optional: `{ name: string, age?: int }` accepts payloads with or
  without `age`, and a present `age` still validates as `int`. Optionality is
  distinct from nullability (`age?: int?` may be absent or `nil`), fields stay
  required by default, and optional fields work in nested shapes and
  `JSON.parse_as`. The checker infers `T | nil` for optional field reads and
  no longer flags payloads that merely omit optional fields. A bare shape
  field label ending in `?` now spells optionality; a field whose name
  literally ends in `?` takes a string key (`{ "valid?": bool }`).
