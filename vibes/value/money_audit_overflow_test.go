package value

import (
	"math"
	"testing"
)

// Regression tests for audit finding `money-arith-silent-int64-overflow` (sev3):
// Money.Add/Sub/MulInt previously performed unchecked int64 arithmetic on
// `cents`, so a script-reachable operation could silently overflow and mint a
// Money value that misrepresents the true result. MulInt was worst: its old
// `(Money)` signature had no error channel, so it could not even signal overflow.
//
// Post-fix expectation (this version): all three detect overflow and return an
// error rather than wrapping. These tests PASS on fixed source and FAIL if the
// checks are reverted. Run:
//   go test ./vibes/value/ -run Audit -v

func auditMoney(cents int64) Money { return Money{cents: cents, currency: "USD"} }

func TestAuditMoneyAddOverflow(t *testing.T) {
	got, err := auditMoney(math.MaxInt64).Add(auditMoney(1))
	if err == nil {
		t.Errorf("Add(MaxInt64,1): want overflow error; got nil err, cents=%d (silent wrap) "+
			"[money-arith-silent-int64-overflow]", got.Cents())
	}
}

func TestAuditMoneySubOverflow(t *testing.T) {
	got, err := auditMoney(math.MinInt64).Sub(auditMoney(1))
	if err == nil {
		t.Errorf("Sub(MinInt64,1): want underflow error; got nil err, cents=%d (silent wrap) "+
			"[money-arith-silent-int64-overflow]", got.Cents())
	}
}

func TestAuditMoneyMulIntOverflow(t *testing.T) {
	got, err := auditMoney(math.MaxInt64/2 + 1).MulInt(2) // would overflow
	if err == nil {
		t.Errorf("MulInt((MaxInt64/2+1)*2): want overflow error; got nil err, cents=%d "+
			"(silent wrap) [money-arith-silent-int64-overflow]", got.Cents())
	}
}

func TestAuditMoneyDivIntOverflow(t *testing.T) {
	// MinInt64 / -1 is the one signed-division overflow: true result is
	// |MinInt64|, not representable in int64. Go yields MinInt64 with nil err.
	got, err := auditMoney(math.MinInt64).DivInt(-1)
	if err == nil {
		t.Errorf("DivInt(MinInt64,-1): want overflow error; got nil err, cents=%d "+
			"(silent overflow) [money-arith-silent-int64-overflow]", got.Cents())
	}
}

// Boundary: operations that do NOT overflow must still succeed and return the
// correct value (guards against an over-eager check that rejects valid math).
func TestAuditMoneyNoFalsePositiveOverflow(t *testing.T) {
	sum, err := auditMoney(100).Add(auditMoney(250))
	if err != nil || sum.Cents() != 350 {
		t.Errorf("Add(100,250): want 350/nil, got %d/%v", sum.Cents(), err)
	}
	diff, err := auditMoney(250).Sub(auditMoney(100))
	if err != nil || diff.Cents() != 150 {
		t.Errorf("Sub(250,100): want 150/nil, got %d/%v", diff.Cents(), err)
	}
	prod, err := auditMoney(100).MulInt(-3)
	if err != nil || prod.Cents() != -300 {
		t.Errorf("MulInt(100,-3): want -300/nil, got %d/%v", prod.Cents(), err)
	}
	// MinInt64 magnitude is representable as a product: (MinInt64) * 1.
	edge, err := auditMoney(math.MinInt64).MulInt(1)
	if err != nil || edge.Cents() != math.MinInt64 {
		t.Errorf("MulInt(MinInt64,1): want MinInt64/nil, got %d/%v", edge.Cents(), err)
	}
	// Ordinary division still works and the divide-by-zero guard is intact.
	half, err := auditMoney(1000).DivInt(4)
	if err != nil || half.Cents() != 250 {
		t.Errorf("DivInt(1000,4): want 250/nil, got %d/%v", half.Cents(), err)
	}
	if _, err := auditMoney(100).DivInt(0); err == nil {
		t.Errorf("DivInt(100,0): want division-by-zero error, got nil")
	}
	// Subtracting MinInt64 can yield a representable result; it must NOT be
	// rejected just because |MinInt64| is not itself representable. Codex
	// caught an earlier negate-then-add implementation erroring here.
	repr, err := auditMoney(-1).Sub(auditMoney(math.MinInt64))
	if err != nil || repr.Cents() != math.MaxInt64 {
		t.Errorf("Sub(-1, MinInt64): want MaxInt64/nil, got %d/%v", repr.Cents(), err)
	}
	// And a genuinely overflowing subtraction still errors.
	if _, err := auditMoney(math.MaxInt64).Sub(auditMoney(-1)); err == nil {
		t.Errorf("Sub(MaxInt64, -1): want overflow error, got nil")
	}
}
