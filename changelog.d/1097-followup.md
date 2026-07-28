- **Fixed: any object with a `to_s` field had it rendered.** The shortcut that
  renders a rescued error's message keyed on the field name alone, so ordinary
  host data carrying a string field of that name had its payload rendered in
  place of `<object>`. Only the two bags that deliberately publish a string form
  -- a rescued error and match data -- are rendered.
