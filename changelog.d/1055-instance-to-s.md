- **Fixed: string interpolation and `puts` ignored a class's `to_s`.** They
  rendered the `<ClassName instance>` placeholder even when the class defined
  `to_s`, so a value printed one way when interpolated and another when `.to_s`
  was called explicitly. Both now dispatch to a user-defined `to_s`, falling back
  to the placeholder when the class defines none, when its `to_s` cannot be
  called with zero arguments, or when it returns a non-string. As in Ruby, a
  value nested inside a container still renders through the container.
