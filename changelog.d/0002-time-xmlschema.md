- **Added: Ruby-style `Time` HTTP/XML/RFC date helpers.** `Time#xmlschema` is an
  alias for `Time#iso8601` (including its optional `ndigits` precision argument).
  `Time#httpdate` renders the HTTP-date / IMF-fixdate form (RFC 7231), always in
  GMT, e.g. `"Tue, 02 Jan 2024 03:04:05 GMT"`. `Time#rfc2822` and its alias
  `Time#rfc822` render the RFC 2822 mail date preserving the receiver's zone
  offset; a genuine UTC receiver uses the `-0000` zone Ruby reserves for
  timestamps without real zone information while an explicit zero offset uses
  `+0000`. `httpdate`, `rfc2822`, and `rfc822` drop sub-second precision and take
  no arguments, raising on any positional or keyword argument.
