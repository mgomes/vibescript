- **Fixed: `String#index` and `String#rindex` accept negative offsets like
  Ruby.** A negative offset now counts back from the end of the string, so the
  search starts at `size + offset`, and the call returns `nil` when that
  effective offset falls before the start of the string. Previously both methods
  rejected negative offsets with an error. For example, `"hello".index("l", -3)`
  returns `2` and `"hello".rindex("l", -2)` returns `3`.
