package runtime

import (
	"math"
	"math/big"

	"github.com/mgomes/vibescript/vibes/value"
)

// This file holds the big-integer arms of the integer operators. The compact
// int64 fast paths stay in values.go untouched; these helpers run only when a
// compact operation overflows or an operand already carries a big payload.
// Every result routes through value.AdoptBigInt, which re-normalizes to the
// compact representation when a result fits int64, upholding the canonical
// invariant (see vibes/value/bigint.go).

// bigIntOperand returns the big-integer form of an int operand without
// copying: the immutable payload for big values, a fresh big.Int for compact
// values. Callers must treat the result as read-only.
func bigIntOperand(v Value) *big.Int {
	if bi, ok := value.BigIntPayload(v); ok {
		return bi
	}
	return big.NewInt(v.Int())
}

func addIntValuesBig(left, right Value) Value {
	return value.AdoptBigInt(new(big.Int).Add(bigIntOperand(left), bigIntOperand(right)))
}

func subIntValuesBig(left, right Value) Value {
	return value.AdoptBigInt(new(big.Int).Sub(bigIntOperand(left), bigIntOperand(right)))
}

func mulIntValuesBig(left, right Value) Value {
	return value.AdoptBigInt(new(big.Int).Mul(bigIntOperand(left), bigIntOperand(right)))
}

// bigFloorDivMod returns the floored quotient and modulo of a and b, matching
// the compact floorDivInt/floorModInt semantics (and Ruby's / and %): the
// remainder takes the divisor's sign. big.Int's QuoRem is truncated division,
// so the pair is adjusted by one step when the truncated remainder's sign
// disagrees with the divisor's. (big.Int.DivMod is Euclidean — the remainder
// is always non-negative — which diverges from Ruby for negative divisors, so
// it is deliberately not used here.)
func bigFloorDivMod(a, b *big.Int) (*big.Int, *big.Int) {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() != 0 && (r.Sign() < 0) != (b.Sign() < 0) {
		q.Sub(q, bigIntOne)
		r.Add(r, b)
	}
	return q, r
}

var bigIntOne = big.NewInt(1)

func floorDivIntValuesBig(left, right Value) Value {
	q, _ := bigFloorDivMod(bigIntOperand(left), bigIntOperand(right))
	return value.AdoptBigInt(q)
}

func floorModIntValuesBig(left, right Value) Value {
	_, r := bigFloorDivMod(bigIntOperand(left), bigIntOperand(right))
	return value.AdoptBigInt(r)
}

// remainderIntValuesBig returns the truncated-division remainder (sign follows
// the dividend), matching Ruby's Numeric#remainder for integer operands.
func remainderIntValuesBig(left, right Value) Value {
	return value.AdoptBigInt(new(big.Int).Rem(bigIntOperand(left), bigIntOperand(right)))
}

func negIntValueBig(v Value) Value {
	return value.AdoptBigInt(new(big.Int).Neg(bigIntOperand(v)))
}

func absIntValueBig(v Value) Value {
	return value.AdoptBigInt(new(big.Int).Abs(bigIntOperand(v)))
}

// powIntValuesBig computes base**exp for a non-negative compact exponent when
// the compact power overflowed or the base carries a big payload. Callers
// preflight the projected result size against the sandbox quotas before
// invoking it (see checkIntPowerGuards).
func powIntValuesBig(base Value, exp int64) Value {
	return value.AdoptBigInt(new(big.Int).Exp(bigIntOperand(base), big.NewInt(exp), nil))
}

// intValueIsZero reports whether an int operand (compact or big) is zero. By
// the canonical invariant a big payload is never zero, so only the compact
// form is consulted.
func intValueIsZero(v Value) bool {
	n, ok := v.CompactInt()
	return ok && n == 0
}

// compareIntValuesBig orders two int operands when at least one carries a big
// payload. The canonical invariant makes the mixed cases free: a big payload
// lies strictly outside the int64 range, so its sign alone decides the order
// against any compact operand.
func compareIntValuesBig(left, right Value) int {
	lbi, lok := value.BigIntPayload(left)
	rbi, rok := value.BigIntPayload(right)
	switch {
	case lok && rok:
		return lbi.Cmp(rbi)
	case lok:
		if lbi.Sign() > 0 {
			return 1
		}
		return -1
	default:
		if rbi.Sign() > 0 {
			return -1
		}
		return 1
	}
}

// compareBigIntFloat orders a big integer against a float exactly, with no
// precision loss, matching Ruby's exact Integer/Float comparisons: the float
// converts to a big.Float (always exact for finite values) and the comparison
// happens in arbitrary precision. NaN is unordered; infinities order by sign
// (every big integer is finite).
func compareBigIntFloat(bi *big.Int, f float64) (order int, ordered bool) {
	if math.IsNaN(f) {
		return 0, false
	}
	if math.IsInf(f, 1) {
		return -1, true
	}
	if math.IsInf(f, -1) {
		return 1, true
	}
	return new(big.Float).SetInt(bi).Cmp(new(big.Float).SetFloat64(f)), true
}

// compareIntFloatValues orders an int (compact or big) against a float. A big
// operand uses the exact big.Float comparison; compact operands keep the
// historical float64 conversion so existing int64 comparison behavior is
// byte-identical.
func compareIntFloatValues(intVal Value, f float64, intOnLeft bool) (order int, ordered bool) {
	if bi, ok := value.BigIntPayload(intVal); ok {
		order, ordered = compareBigIntFloat(bi, f)
	} else {
		lf := float64(intVal.Int())
		switch {
		case math.IsNaN(f):
			return 0, false
		case lf < f:
			order, ordered = -1, true
		case lf > f:
			order, ordered = 1, true
		default:
			order, ordered = 0, true
		}
	}
	if !intOnLeft {
		order = -order
	}
	return order, ordered
}

// rangeContainsBigInt reports whether a big integer lies within an int64-
// bounded range. The canonical invariant puts every big payload strictly
// outside the int64 range, so membership reduces to the open sides: a positive
// big integer exceeds every possible bound (only an endless range contains
// it), and a negative one is below every bound (only a beginless range
// contains it). Exclusivity cannot matter because a big value never equals an
// int64 endpoint.
func rangeContainsBigInt(rng Range, v Value) bool {
	bi, ok := value.BigIntPayload(v)
	if !ok {
		return false
	}
	if bi.Sign() > 0 {
		return rng.Endless
	}
	return rng.Beginless
}

// powerBigExponent resolves base**exp for an integer base when the exponent
// carries a big payload. Ruby keeps the powers that stay trivially
// representable — 0, 1, and -1 raised to any positive exponent — and raises
// "exponent is too large" for every other integer base, since the result's
// bit length alone would exceed any plausible memory bound. A big negative
// exponent reports handled=false so it falls through to the float path,
// matching the treatment of compact negative exponents.
func powerBigExponent(base, exp Value) (Value, bool, error) {
	bi, ok := value.BigIntPayload(exp)
	if !ok {
		return NewNil(), false, nil
	}
	if bi.Sign() < 0 {
		return NewNil(), false, nil
	}
	if b, bok := base.CompactInt(); bok {
		switch b {
		case 0:
			return NewInt(0), true, nil
		case 1:
			return NewInt(1), true, nil
		case -1:
			if bi.Bit(0) == 0 {
				return NewInt(1), true, nil
			}
			return NewInt(-1), true, nil
		}
	}
	return NewNil(), true, guardLimitErrorf("integer exponentiation exponent is too large")
}

// int64OperandForDomain extracts an int64 from a numeric operand feeding an
// int64-domain operation (duration, time, money arithmetic). A big integer
// reports the domain's existing overflow convention ("<method> result out of
// int64 range") instead of the generic conversion error, since the operation
// as a whole cannot be represented in the domain.
func int64OperandForDomain(val Value, method string) (int64, error) {
	if val.Kind() == KindInt {
		if n, ok := val.CompactInt(); ok {
			return n, nil
		}
		return 0, int64RangeError(method)
	}
	return valueToInt64(val)
}

// moneyIntOperand extracts the int64 factor/divisor for money arithmetic. A
// big integer reports money's existing overflow convention, since money's
// cents domain stays int64.
func moneyIntOperand(val Value) (int64, error) {
	if n, ok := val.CompactInt(); ok {
		return n, nil
	}
	return 0, value.ErrMoneyOverflow
}

// bigIntStepWordsPerStep is the number of big-integer payload words one
// sandbox step covers when an arithmetic operation charges for big operands.
// Under the conventional 50k step quota this stops a chain of roughly 400k
// words (~25M bits) of operand traffic — million-bit multiplications cost
// thousands of steps instead of hiding as O(1) ops.
const bigIntStepWordsPerStep = 8

// bigIntMulPreflightWords is the projected product size (in words) above which
// a multiplication preflights the result allocation against the memory quota
// before computing. Small promotions skip the base-walk so ordinary
// slightly-over-int64 arithmetic stays cheap.
const bigIntMulPreflightWords = 64

// checkBigIntOperationGuards scales the sandbox cost of an integer operation
// with its big operands: steps proportional to the operands' total word count,
// plus (for multiplication) a memory preflight of the product's projected word
// count, which is bounded by lwords+rwords. Callers invoke it only when at
// least one operand carries a big payload, keeping the compact path free of
// extra work.
func (exec *Execution) checkBigIntOperationGuards(operator TokenType, left, right Value) error {
	words := 0
	if bi, ok := value.BigIntPayload(left); ok {
		words += len(bi.Bits())
	}
	if bi, ok := value.BigIntPayload(right); ok {
		words += len(bi.Bits())
	}
	if words == 0 {
		return nil
	}
	if operator == tokenAsterisk && words > bigIntMulPreflightWords {
		// A product's size is at most the sum of its factors' sizes (+1 word).
		if err := exec.checkProjectedBigIntBytes(words + 1); err != nil {
			return err
		}
	}
	return exec.stepN(1 + words/bigIntStepWordsPerStep)
}

// checkIntPowerGuards preflights an integer exponentiation before it runs
// (#604 convention): the projected result size — roughly exp x bits(base) —
// is charged against the memory quota and the step quota in O(1), so
// `2 ** 10_000_000_000` rejects immediately instead of attempting the
// allocation. Bases -1, 0, and 1 never grow and skip the guards; negative and
// big exponents resolve inside powerValues (float fallthrough or Ruby's
// "exponent is too large").
func (exec *Execution) checkIntPowerGuards(base, exp Value) error {
	e, ok := exp.CompactInt()
	if !ok || e <= 0 {
		return nil
	}
	// Projected result bits: exp x log2(|base|), rounded up. Using the exact
	// log2 for compact bases (rather than the bit length) keeps the common
	// power-of-two bases from projecting twice their true size; big bases use
	// the bit length, an upper bound within one bit per multiplication.
	var baseLog2 float64
	if bi, bok := value.BigIntPayload(base); bok {
		baseLog2 = float64(bi.BitLen())
	} else {
		b, _ := base.CompactInt()
		if b >= -1 && b <= 1 {
			return nil
		}
		mag := uint64(b)
		if b < 0 {
			mag = -mag
		}
		baseLog2 = math.Log2(float64(mag))
	}
	projWords := math.MaxInt / (estimatedBigIntWordBytes * 2)
	if projBits := float64(e) * baseLog2; projBits < float64(math.MaxInt/16) {
		projWords = int(projBits)/64 + 2
	}
	if projWords <= bigIntMulPreflightWords {
		// Small projected results (a few thousand bits) stay near-free, so
		// ordinary compact exponentiation charges nothing extra here.
		return nil
	}
	if err := exec.checkProjectedBigIntBytes(projWords); err != nil {
		return err
	}
	return exec.stepN(1 + projWords/bigIntStepWordsPerStep)
}

// chargeBigIntReceiverSteps scales the step cost of a unary big-integer
// member operation (abs, succ, pred, rounding) with the receiver's word
// count, mirroring checkBigIntOperationGuards for single-operand work. It is
// a no-op for compact receivers.
func (exec *Execution) chargeBigIntReceiverSteps(v Value) error {
	bi, ok := value.BigIntPayload(v)
	if !ok {
		return nil
	}
	return exec.stepN(1 + len(bi.Bits())/bigIntStepWordsPerStep)
}

// bigIntDecimalDigitsUpperBound is the runtime-side twin of the value
// package's decimal-length upper bound, used by format projections that
// already hold the payload.
func bigIntDecimalDigitsUpperBound(bi *big.Int) int {
	bits := bi.BitLen()
	if bits == 0 {
		return 1
	}
	digits := bits*30103/100000 + 1
	if bi.Sign() < 0 {
		digits++
	}
	return digits
}

// chargeBigIntKeySteps scales the step cost of canonicalizing one hash key
// with the key's word count when it carries a big payload (hash set/get/
// delete, membership probes, aggregation keys), matching the arithmetic
// convention of 1 + words/8. The canonical hex conversion is linear in words,
// so the charge bounds its CPU under the step quota; compact keys are a no-op.
func (exec *Execution) chargeBigIntKeySteps(key Value) error {
	bi, ok := value.BigIntPayload(key)
	if !ok {
		return nil
	}
	return exec.stepN(1 + len(bi.Bits())/bigIntStepWordsPerStep)
}

// chargeBigIntElementKeySteps charges up front for canonicalizing every
// big-integer element of the given slices. The set-building helpers (uniq,
// union, difference, & and array -) canonicalize each element at least once,
// so their entry points charge the summed word count before any conversion
// runs; slices of compact values charge nothing beyond the scan.
func (exec *Execution) chargeBigIntElementKeySteps(slices ...[]Value) error {
	words := 0
	for _, values := range slices {
		for _, v := range values {
			if bi, ok := value.BigIntPayload(v); ok {
				words += len(bi.Bits())
			}
		}
	}
	if words == 0 {
		return nil
	}
	return exec.stepN(1 + words/bigIntStepWordsPerStep)
}
