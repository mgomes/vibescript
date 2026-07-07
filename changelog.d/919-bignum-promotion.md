- **Changed: integers are arbitrary precision with Ruby-style transparent
  promotion (#919).** Arithmetic (`+ - * / % **`, unary minus), `abs`,
  `succ`/`pred`, `div`/`divmod`/`modulo`/`remainder`, rounding,
  `sum`/`reduce`, and `Float`→`int` conversions promote past the signed
  64-bit range instead of raising `... result out of int64 range`; integer
  literals of any length now parse (previously `invalid integer literal`);
  and `JSON.parse` reads oversized integer tokens as exact integers
  (previously degraded to floats) while `JSON.stringify` emits full
  decimals. Division and modulo keep floor semantics for big operands, big
  vs float comparisons are exact, big values work as hash keys, and typed
  `int` contracts accept them. Hosts get `value.NewBigInt`, `Value.BigInt`,
  and `Value.IsBigInt`; `Value.Int` still returns `0` for out-of-range
  integers (never a truncation), and `ValueToInt64` rejects them. Scripts
  or hosts relying on the old overflow errors as range guards must validate
  magnitudes explicitly (see the migration guide). Deliberate 64-bit
  boundaries error loudly: range endpoints, `times`/`upto`/`downto`/`step`
  iteration bounds, `Money`/`Duration`/`Time` arithmetic, string
  `hex`/`oct`, and index/count/size/precision argument positions. The
  sandbox charges big payloads by size, scales step costs with operand
  words, preflights `**` and oversized products in O(1)
  (`2 ** 10_000_000_000` rejects instantly), and preflights digit counts
  before any base conversion renders.
