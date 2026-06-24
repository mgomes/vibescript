- **Fixed: `String#chomp` and `String#chomp!` treat a `nil` separator as "do not
  chomp".** Passing `nil` now returns the string unchanged (`chomp`) or `nil`
  because no change occurs (`chomp!`), matching Ruby, instead of raising
  `separator must be string`. The default separator, empty-string separator, and
  explicit string separator behaviors are unchanged.
