- **Fixed: an incomparable comparison could not be rescued and pointed at line
  1.** `1 < "a"` raised an error that `rescue` could not catch and that reported
  the start of the script rather than the comparison, because the relational
  operators returned without positioning the error. Sandbox limits stay
  deliberately uncatchable; an ordinary type error is now both catchable and
  located.
