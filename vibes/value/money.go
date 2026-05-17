package value

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

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

// Add returns the sum of m and other, or an error if their currencies differ.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("money currency mismatch")
	}
	return Money{cents: m.cents + other.cents, currency: m.currency}, nil
}

// Sub returns m minus other, or an error if their currencies differ.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("money currency mismatch")
	}
	return Money{cents: m.cents - other.cents, currency: m.currency}, nil
}

// MulInt multiplies m by the given integer factor, preserving the currency.
func (m Money) MulInt(factor int64) Money {
	return Money{cents: m.cents * factor, currency: m.currency}
}

// DivInt divides m by the given integer divisor, returning an error on
// division by zero.
func (m Money) DivInt(divisor int64) (Money, error) {
	if divisor == 0 {
		return Money{}, errors.New("division by zero")
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
