- **Added: Ruby-style `Array#filter_map`.** `filter_map` fuses `map` with a
  truthiness filter, calling the block once per element and collecting each
  truthy result while dropping falsy ones. Like `select` and `reject`, it uses
  Vibescript's truthiness, so `nil`, `false`, `0`, `""`, and empty collections
  are all dropped. It requires a block, takes no arguments, and materializes its
  result under the sandbox step and memory quotas so large inputs fail safely
  and long iterations honor cancellation.
