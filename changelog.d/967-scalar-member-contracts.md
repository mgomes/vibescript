- **Improved: the checker knows scalar member contracts.** Conversions such as
  `to_i`, `to_f`, `to_s`, and `to_sym` and universal predicates such as `nil?`,
  `eql?`, and `respond_to?` now declare fixed results resolved from the
  receiver's inferred type, safe navigation adds `nil` to known results, and
  class instances or dynamic receivers stay unknown.
