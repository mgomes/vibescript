- **Added: arrays compare and sort.** `[1, 2] <=> [1, 3]` returned nil and
  `[[2, 1], [1, 9]].sort` failed, so an array of arrays could not be ordered at
  all. Arrays now compare lexicographically as in Ruby: the first differing
  element decides, and a shorter array with an equal prefix sorts first. This
  reaches further than arrays themselves, because a hash entry is a pair, so
  `hash.to_a.sort` and `sort_by { |r| [r[:a], r[:b]] }` now work. As in Ruby, the
  relational operators still reject arrays -- `Array` defines `<=>` but does not
  include `Comparable`.
