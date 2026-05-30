package value

// Money is a domain-shaped scalar that also serves as a Value payload
// (KindMoney). It lives in the value package alongside Value itself
// because of that coupling; see doc.go for the rationale.

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
)

// errMoneyOverflow is returned when a money operation would exceed the int64
// cents range. Money is a correctness-critical domain type: silently wrapping a
// currency amount produces a wrong ledger value with no signal, so arithmetic
// reports overflow rather than wrapping. (Plain int arithmetic in the language
// still wraps; money is deliberately stricter, matching the existing overflow
// guard in ParseMoneyLiteral and the error-returning Add/Sub/DivInt.)
var errMoneyOverflow = errors.New("money arithmetic overflow")

// addInt64Checked returns a+b, or false if the signed addition overflows int64.
func addInt64Checked(a, b int64) (int64, bool) {
	sum := a + b
	// Overflow iff a and b share a sign that differs from the result's sign.
	if (a > 0 && b > 0 && sum < 0) || (a < 0 && b < 0 && sum >= 0) {
		return 0, false
	}
	return sum, true
}

// subInt64Checked returns a-b, or false if the signed subtraction overflows
// int64. It tests the difference directly (no intermediate negation), so a
// representable result whose operands include MinInt64 -- e.g. -1 - MinInt64 =
// MaxInt64 -- is accepted rather than rejected. Overflow occurs exactly when a
// and b have different signs and the result's sign differs from a's.
func subInt64Checked(a, b int64) (int64, bool) {
	diff := a - b
	if (a^b)&(a^diff) < 0 {
		return 0, false
	}
	return diff, true
}

// mulInt64Checked returns a*b, or false if the signed multiplication overflows
// int64. It works on magnitudes via bits.Mul64 so it has no MinInt64/-1 trap.
func mulInt64Checked(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	negative := (a < 0) != (b < 0)
	ua := uint64(a)
	if a < 0 {
		ua = -ua
	}
	ub := uint64(b)
	if b < 0 {
		ub = -ub
	}
	hi, lo := bits.Mul64(ua, ub)
	if hi != 0 {
		return 0, false // magnitude needs more than 64 bits
	}
	if negative {
		if lo > uint64(math.MaxInt64)+1 { // > |MinInt64|
			return 0, false
		}
		return -int64(lo), true
	}
	if lo > uint64(math.MaxInt64) {
		return 0, false
	}
	return int64(lo), true
}

// Money represents an ISO-4217 currency amount stored as integer cents.
type Money struct {
	cents    int64
	currency string
}

// Currency returns the ISO-4217 currency code.
func (m Money) Currency() string { return m.currency }

// Cents returns the amount in the smallest currency unit.
func (m Money) Cents() int64 { return m.cents }

// String returns the amount formatted as "X.XX CUR".
func (m Money) String() string {
	sign := ""
	var cents uint64
	if m.cents < 0 {
		sign = "-"
		cents = uint64(-(m.cents + 1)) + 1
	} else {
		cents = uint64(m.cents)
	}
	dollars := cents / 100
	rem := cents % 100
	return fmt.Sprintf("%s%d.%02d %s", sign, dollars, rem, m.currency)
}

// Add returns the sum of m and other, or an error if their currencies differ
// or the result would overflow the int64 cents range.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("money currency mismatch")
	}
	cents, ok := addInt64Checked(m.cents, other.cents)
	if !ok {
		return Money{}, errMoneyOverflow
	}
	return Money{cents: cents, currency: m.currency}, nil
}

// Sub returns m minus other, or an error if their currencies differ or the
// result would overflow the int64 cents range.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("money currency mismatch")
	}
	cents, ok := subInt64Checked(m.cents, other.cents)
	if !ok {
		return Money{}, errMoneyOverflow
	}
	return Money{cents: cents, currency: m.currency}, nil
}

// MulInt multiplies m by the given integer factor, preserving the currency, or
// returns an error if the result would overflow the int64 cents range.
func (m Money) MulInt(factor int64) (Money, error) {
	cents, ok := mulInt64Checked(m.cents, factor)
	if !ok {
		return Money{}, errMoneyOverflow
	}
	return Money{cents: cents, currency: m.currency}, nil
}

// DivInt divides m by the given integer divisor, returning an error on
// division by zero or on the one signed-division overflow case
// (MinInt64 / -1, whose true result is not representable in int64).
func (m Money) DivInt(divisor int64) (Money, error) {
	if divisor == 0 {
		return Money{}, errors.New("division by zero")
	}
	if m.cents == math.MinInt64 && divisor == -1 {
		return Money{}, errMoneyOverflow
	}
	return Money{cents: m.cents / divisor, currency: m.currency}, nil
}

// ParseMoneyLiteral parses a textual money literal of the form "X.XX CUR".
func ParseMoneyLiteral(input string) (Money, error) {
	parts := strings.Fields(input)
	if len(parts) != 2 {
		return Money{}, fmt.Errorf("invalid money literal %q", input)
	}
	amount := parts[0]
	currency, err := normalizeMoneyCurrency(parts[1])
	if err != nil {
		return Money{}, err
	}
	negative := false
	if trimmed, ok := strings.CutPrefix(amount, "-"); ok {
		negative = true
		amount = trimmed
	} else if trimmed, ok := strings.CutPrefix(amount, "+"); ok {
		amount = trimmed
	}

	whole := amount
	fraction := "0"
	if strings.Contains(amount, ".") {
		split := strings.SplitN(amount, ".", 2)
		whole = split[0]
		fraction = split[1]
	}

	if whole == "" && fraction == "" {
		return Money{}, fmt.Errorf("invalid money amount %q", input)
	}
	if whole != "" && !isDecimalDigits(whole) {
		return Money{}, fmt.Errorf("invalid money amount %q", input)
	}
	if fraction != "" && !isDecimalDigits(fraction) {
		return Money{}, fmt.Errorf("invalid money amount %q", input)
	}
	if len(fraction) > 2 {
		return Money{}, fmt.Errorf("money literal supports at most 2 decimal places: %q", input)
	}

	for len(fraction) < 2 {
		fraction += "0"
	}

	dollars := int64(0)
	if whole != "" {
		parsed, err := strconv.ParseInt(whole, 10, 64)
		if err != nil {
			return Money{}, fmt.Errorf("invalid money amount %q", input)
		}
		dollars = parsed
	}

	centsPart, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("invalid money amount %q", input)
	}

	limit := uint64(1<<63 - 1)
	if negative {
		limit = uint64(1) << 63
	}
	magnitudeDollars := uint64(dollars)
	magnitudeCents := uint64(centsPart)
	if magnitudeDollars > (limit-magnitudeCents)/100 {
		return Money{}, fmt.Errorf("invalid money amount %q", input)
	}
	magnitude := magnitudeDollars*100 + magnitudeCents
	total := int64(magnitude)
	if negative {
		if magnitude == uint64(1)<<63 {
			total = int64(-1 << 63)
		} else {
			total = -total
		}
	}

	return Money{cents: total, currency: currency}, nil
}

// NewMoneyFromCents constructs a Money from an integer cents value and a
// currency code.
func NewMoneyFromCents(cents int64, currency string) (Money, error) {
	normalized, err := normalizeMoneyCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	return Money{cents: cents, currency: normalized}, nil
}

func isDecimalDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func normalizeMoneyCurrency(currency string) (string, error) {
	if len(currency) != 3 {
		return "", fmt.Errorf("currency must be 3 letters, got %q", currency)
	}
	for i := range 3 {
		b := currency[i]
		if (b < 'A' || b > 'Z') && (b < 'a' || b > 'z') {
			return "", fmt.Errorf("currency must be 3 letters, got %q", currency)
		}
	}
	return strings.ToUpper(currency), nil
}
