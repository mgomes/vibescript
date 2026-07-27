- **Fixed: `format("%s", obj)` ignored a class's `to_s`.** `%s` is defined as the
  `to_s` form, but `format` was the last direct string conversion that did not
  consult it -- interpolation and `puts` were connected in #1055 and this was
  left out, so one conversion still disagreed with the other two.
