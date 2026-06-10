package value_test

import (
	"math"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestNewMoneyFromCents(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name         string
			cents        int64
			currency     string
			wantCurrency string
		}{
			{"uppercase", 1999, "USD", "USD"},
			{"lowercase_normalized", -5, "usd", "USD"},
			{"mixed_case_normalized", 0, "eUr", "EUR"},
			{"max_cents", math.MaxInt64, "USD", "USD"},
			{"min_cents", math.MinInt64, "USD", "USD"},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				m, err := value.NewMoneyFromCents(tc.cents, tc.currency)
				if err != nil {
					t.Fatalf("NewMoneyFromCents error: %v", err)
				}
				if m.Cents() != tc.cents {
					t.Errorf("Cents() = %d, want %d", m.Cents(), tc.cents)
				}
				if m.Currency() != tc.wantCurrency {
					t.Errorf("Currency() = %q, want %q", m.Currency(), tc.wantCurrency)
				}
			})
		}
	})

	t.Run("invalid_currency", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name     string
			currency string
			wantErr  string
		}{
			{"too_short", "US", `currency must be 3 letters, got "US"`},
			{"too_long", "USDT", `currency must be 3 letters, got "USDT"`},
			{"empty", "", `currency must be 3 letters, got ""`},
			{"digit", "U5D", `currency must be 3 letters, got "U5D"`},
			{"punctuation", "U$D", `currency must be 3 letters, got "U$D"`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := value.NewMoneyFromCents(100, tc.currency)
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("NewMoneyFromCents(100, %q) error = %v, want %q", tc.currency, err, tc.wantErr)
				}
			})
		}
	})
}

func TestMoneyString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cents int64
		want  string
	}{
		{"dollars_and_cents", 1999, "19.99 USD"},
		{"zero", 0, "0.00 USD"},
		{"cents_only", 5, "0.05 USD"},
		{"negative_cents_only", -1, "-0.01 USD"},
		{"negative", -1999, "-19.99 USD"},
		{"whole_dollars", 100, "1.00 USD"},
		{"max_int64", math.MaxInt64, "92233720368547758.07 USD"},
		{"min_int64", math.MinInt64, "-92233720368547758.08 USD"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := mustMoney(t, tc.cents, "USD")
			if got := m.String(); got != tc.want {
				t.Fatalf("Money{%d}.String() = %q, want %q", tc.cents, got, tc.want)
			}
		})
	}
}

func TestMoneyArithmetic(t *testing.T) {
	t.Parallel()

	t.Run("add", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			left, right int64
			want        int64
		}{
			{"positive", 100, 250, 350},
			{"negative_operand", 100, -250, -150},
			{"zero_identity", math.MaxInt64, 0, math.MaxInt64},
			{"min_plus_max", math.MinInt64, math.MaxInt64, -1},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := mustMoney(t, tc.left, "USD").Add(mustMoney(t, tc.right, "USD"))
				if err != nil {
					t.Fatalf("Add error: %v", err)
				}
				if got.Cents() != tc.want || got.Currency() != "USD" {
					t.Fatalf("Add = %d %s, want %d USD", got.Cents(), got.Currency(), tc.want)
				}
			})
		}
	})

	t.Run("sub", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			left, right int64
			want        int64
		}{
			{"positive", 250, 100, 150},
			{"result_negative", 100, 250, -150},
			{"min_minus_zero", math.MinInt64, 0, math.MinInt64},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got, err := mustMoney(t, tc.left, "USD").Sub(mustMoney(t, tc.right, "USD"))
				if err != nil {
					t.Fatalf("Sub error: %v", err)
				}
				if got.Cents() != tc.want {
					t.Fatalf("Sub = %d, want %d", got.Cents(), tc.want)
				}
			})
		}
	})

	t.Run("currency_mismatch", func(t *testing.T) {
		t.Parallel()
		usd := mustMoney(t, 100, "USD")
		eur := mustMoney(t, 100, "EUR")
		if _, err := usd.Add(eur); err == nil || err.Error() != "money currency mismatch" {
			t.Errorf("Add mismatch error = %v, want %q", err, "money currency mismatch")
		}
		if _, err := usd.Sub(eur); err == nil || err.Error() != "money currency mismatch" {
			t.Errorf("Sub mismatch error = %v, want %q", err, "money currency mismatch")
		}
	})

	t.Run("overflow_error_message", func(t *testing.T) {
		t.Parallel()
		_, err := mustMoney(t, math.MaxInt64, "USD").Add(mustMoney(t, 1, "USD"))
		if err == nil || err.Error() != "money arithmetic overflow" {
			t.Fatalf("Add overflow error = %v, want %q", err, "money arithmetic overflow")
		}
	})
}

func TestMoneyMulInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cents   int64
		factor  int64
		want    int64
		wantErr string
	}{
		{name: "positive", cents: 1999, factor: 3, want: 5997},
		{name: "negative_factor", cents: 1999, factor: -2, want: -3998},
		{name: "negative_cents", cents: -50, factor: 4, want: -200},
		{name: "both_negative", cents: -50, factor: -4, want: 200},
		{name: "zero_factor", cents: math.MaxInt64, factor: 0, want: 0},
		{name: "zero_cents", cents: 0, factor: math.MinInt64, want: 0},
		{name: "min_times_negative_one", cents: math.MinInt64, factor: -1, wantErr: "money arithmetic overflow"},
		{name: "negative_one_times_min", cents: -1, factor: math.MinInt64, wantErr: "money arithmetic overflow"},
		{name: "max_times_two", cents: math.MaxInt64, factor: 2, wantErr: "money arithmetic overflow"},
		{name: "max_times_negative_two", cents: math.MaxInt64, factor: -2, wantErr: "money arithmetic overflow"},
		{name: "max_times_max", cents: math.MaxInt64, factor: math.MaxInt64, wantErr: "money arithmetic overflow"},
		{name: "min_magnitude_product", cents: math.MinInt64 / 2, factor: 2, want: math.MinInt64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := mustMoney(t, tc.cents, "USD").MulInt(tc.factor)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("MulInt(%d, %d) error = %v, want %q", tc.cents, tc.factor, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("MulInt(%d, %d) error: %v", tc.cents, tc.factor, err)
			}
			if got.Cents() != tc.want {
				t.Fatalf("MulInt(%d, %d) = %d, want %d", tc.cents, tc.factor, got.Cents(), tc.want)
			}
			if got.Currency() != "USD" {
				t.Fatalf("MulInt currency = %q, want USD", got.Currency())
			}
		})
	}
}

func TestMoneyDivInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cents   int64
		divisor int64
		want    int64
		wantErr string
	}{
		{name: "exact", cents: 1000, divisor: 4, want: 250},
		{name: "truncates_toward_zero", cents: 7, divisor: 2, want: 3},
		{name: "negative_truncates_toward_zero", cents: -7, divisor: 2, want: -3},
		{name: "negative_divisor", cents: 100, divisor: -4, want: -25},
		{name: "divide_by_zero", cents: 100, divisor: 0, wantErr: "division by zero"},
		{name: "min_by_negative_one", cents: math.MinInt64, divisor: -1, wantErr: "money arithmetic overflow"},
		{name: "min_by_one", cents: math.MinInt64, divisor: 1, want: math.MinInt64},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := mustMoney(t, tc.cents, "USD").DivInt(tc.divisor)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("DivInt(%d, %d) error = %v, want %q", tc.cents, tc.divisor, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DivInt(%d, %d) error: %v", tc.cents, tc.divisor, err)
			}
			if got.Cents() != tc.want {
				t.Fatalf("DivInt(%d, %d) = %d, want %d", tc.cents, tc.divisor, got.Cents(), tc.want)
			}
		})
	}
}

func TestParseMoneyLiteral(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			input        string
			wantCents    int64
			wantCurrency string
		}{
			{"19.99 USD", 1999, "USD"},
			{"0.00 USD", 0, "USD"},
			{"-0.05 USD", -5, "USD"},
			{"+10 USD", 1000, "USD"},
			{"5 usd", 500, "USD"},
			{"0.5 USD", 50, "USD"},
			{".75 USD", 75, "USD"},
			{"92233720368547758.07 USD", math.MaxInt64, "USD"},
			{"-92233720368547758.08 USD", math.MinInt64, "USD"},
		}
		for _, tc := range tests {
			t.Run(tc.input, func(t *testing.T) {
				t.Parallel()
				m, err := value.ParseMoneyLiteral(tc.input)
				if err != nil {
					t.Fatalf("ParseMoneyLiteral(%q) error: %v", tc.input, err)
				}
				if m.Cents() != tc.wantCents || m.Currency() != tc.wantCurrency {
					t.Fatalf("ParseMoneyLiteral(%q) = %d %s, want %d %s",
						tc.input, m.Cents(), m.Currency(), tc.wantCents, tc.wantCurrency)
				}
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name    string
			input   string
			wantErr string
		}{
			{"empty", "", `invalid money literal ""`},
			{"amount_only", "10", `invalid money literal "10"`},
			{"extra_field", "10 USD tip", `invalid money literal "10 USD tip"`},
			{"three_decimals", "10.123 USD", `money literal supports at most 2 decimal places: "10.123 USD"`},
			{"alpha_amount", "1x USD", `invalid money amount "1x USD"`},
			{"alpha_fraction", "1.x5 USD", `invalid money amount "1.x5 USD"`},
			{"double_decimal", "1.2.3 USD", `invalid money amount "1.2.3 USD"`},
			{"bare_decimal_point", ". USD", `invalid money amount ". USD"`},
			{"bad_currency", "10 US", `currency must be 3 letters, got "US"`},
			{"whole_part_overflows_int64", "99999999999999999999 USD", `invalid money amount "99999999999999999999 USD"`},
			{"overflow_positive", "92233720368547758.08 USD", `invalid money amount "92233720368547758.08 USD"`},
			{"overflow_negative", "-92233720368547758.09 USD", `invalid money amount "-92233720368547758.09 USD"`},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				_, err := value.ParseMoneyLiteral(tc.input)
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("ParseMoneyLiteral(%q) error = %v, want %q", tc.input, err, tc.wantErr)
				}
			})
		}
	})
}
