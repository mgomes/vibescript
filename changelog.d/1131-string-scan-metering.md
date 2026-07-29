- **Fixed: string methods charge the step quota for the bytes they scan.** A
  string method's work grows with its receiver but it dispatches as a single
  call, so charging a flat handful of steps let a script process a
  host-supplied string of any size on a constant budget: repeatedly upcasing an
  800 KB string burned five minutes inside the default 1M-step profile before
  the quota fired. String methods now charge one step per 64 bytes of receiver,
  the same rate big-integer operands are charged at. Methods whose cost does not
  grow with the receiver (`bytesize`, `empty?`, `getbyte`, `ord`, `chr`, `to_s`,
  `clear`, `byteslice`, `to_sym`, `intern`) stay exempt, and any receiver
  shorter than 64 bytes rounds down to no charge, so ordinary short strings are
  unaffected.
  Rendering a value charges for the bytes it prints -- `inspect`,
  interpolation, `to_s`, `puts`, and `join` alike -- so converting a large
  string to a symbol stays free while printing that symbol's name does not.
  `format` charges for its pattern as well as its arguments: a 512 KB pattern
  with no arguments at all previously ran for over a minute inside the default
  profile without the quota firing.
  The charge covers string arguments copied into a result as well as the
  receiver, so a short receiver with a large argument (`"".concat(s)`) costs
  the same as the reverse.
  Operations whose output size comes from a number rather than their inputs --
  `ljust`, `rjust`, `center`, and `String#*` -- charge for the bytes they write,
  since a few-byte receiver can produce megabytes.
