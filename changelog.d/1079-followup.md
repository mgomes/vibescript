- **Fixed: an unbounded decimal conversion could occupy a worker.**
  `big.Int.SetString` is superlinear, so the linear step charge passed well
  before a multi-million-digit conversion finished. String conversions are now
  capped at the same 100,000 digits the parser already applies to integer
  literals, and the source string is charged alongside the parsed bignum since
  both are live at once.
