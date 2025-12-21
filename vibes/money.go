package vibes

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

func (m Money) Currency() string { return m.currency }
func (m Money) Cents() int64     { return m.cents }

func (m Money) String() string {
	sign := ""
	cents := m.cents
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	dollars := cents / 100
	rem := cents % 100
	return fmt.Sprintf("%s%d.%02d %s", sign, dollars, rem, m.currency)
}

func (m Money) add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("money currency mismatch")
	}
	return Money{cents: m.cents + other.cents, currency: m.currency}, nil
}

func (m Money) sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("money currency mismatch")
	}
	return Money{cents: m.cents - other.cents, currency: m.currency}, nil
}

func (m Money) mulInt(factor int64) Money {
	return Money{cents: m.cents * factor, currency: m.currency}
}

func (m Money) divInt(divisor int64) (Money, error) {
	if divisor == 0 {
		return Money{}, errors.New("division by zero")
	}
	return Money{cents: m.cents / divisor, currency: m.currency}, nil
}

func parseMoneyLiteral(input string) (Money, error) {
	parts := strings.Fields(input)
	if len(parts) != 2 {
		return Money{}, fmt.Errorf("invalid money literal %q", input)
	}
	amount, currency := parts[0], strings.ToUpper(parts[1])
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

	total := dollars*100 + centsPart
	if negative {
		total = -total
	}

	return Money{cents: total, currency: currency}, nil
}

func newMoneyFromCents(cents int64, currency string) (Money, error) {
	if len(currency) != 3 {
		return Money{}, fmt.Errorf("currency must be 3 letters, got %q", currency)
	}
	return Money{cents: cents, currency: strings.ToUpper(currency)}, nil
}
