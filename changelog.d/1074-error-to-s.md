- **Fixed: a rescued error rendered as `<object>`.** `rescue => e` followed by
  `puts "failed: #{e}"` is the most common error-reporting idiom there is, and it
  printed `failed: <object>` -- a silent loss, since `e.message` and `e.to_s`
  both held the text. Interpolation, `puts`, and `print` now render the message,
  as in Ruby. `inspect` keeps the full detail.
