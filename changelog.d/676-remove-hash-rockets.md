- **Removed: hash rocket (`=>`) literal syntax.** Hash literals only accept
  colon-style keys: shorthand labels (`name:`) and quoted string keys
  (`"name":`). Ruby's `=>` syntax is no longer part of the hash grammar, so write
  `{ name: "Ada" }` instead of `{ :name => "Ada" }`. To key a hash on a value
  computed at runtime, assign into the hash with index access after building it.
