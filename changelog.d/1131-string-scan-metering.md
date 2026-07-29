- **Fixed: string methods charge the step quota for the bytes they scan.** A
  string method's work grows with its receiver but it dispatches as a single
  call, so charging a flat handful of steps let a script process a
  host-supplied string of any size on a constant budget: repeatedly upcasing an
  800 KB string burned five minutes inside the default 1M-step profile before
  the quota fired. String methods now charge one step per 64 bytes of receiver,
  the same rate big-integer operands are charged at. Methods whose cost does not
  grow with the receiver (`bytesize`, `empty?`, `getbyte`, `ord`, `chr`, `to_s`,
  `clear`) stay exempt, and any receiver shorter than 64 bytes rounds down to no
  charge, so ordinary short strings are unaffected.
