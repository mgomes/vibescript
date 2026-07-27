package runtime

import (
	"testing"
)

// The runtime rejects concatenating a non-renderable operand, so the checker
// has to reject the same pairs when both types are known. Otherwise a
// statically typed script passes `vibes check` and then fails at the runtime
// guard, which is the divergence the checker exists to prevent.
func TestCheckerRejectsNonConcatenableOperandPairs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "string plus array",
			source:  "def f(a: string, b: array<int>) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			name:    "string plus hash",
			source:  "def f(a: string, b: hash) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			name:    "string plus nil",
			source:  "def f(a: string) -> string\n  a + nil\nend",
			wantErr: "unsupported addition operands",
		},
		{
			// Both alternatives an array<int>? can hold -- an array and nil --
			// are rejected, so no runtime value can succeed. Partial knowledge
			// about which one it is does not make the outcome partial.
			name:    "string plus nullable array",
			source:  "def f(a: string, b: array<int>?) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			name:    "string plus union of rejected kinds",
			source:  "def f(a: string, b: array<int> | hash) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			// The string-valued side may itself be nullable or a union. Both
			// string + array and nil + array are rejected, so no pairing works.
			name:    "nullable string plus array",
			source:  "def f(a: string?, b: array<int>) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			name:    "union string plus array",
			source:  "def f(a: string | int, b: array<int>) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			name:    "nullable number plus array",
			source:  "def f(a: number?, b: array<int>) -> string\n  a + b\nend",
			wantErr: "unsupported addition operands",
		},
		{
			// A callable renders as a placeholder and nil renders as empty, so
			// neither alternative of function? can concatenate.
			name:    "string plus nullable callable",
			source:  "def f(s: string, cb: function?) -> string\n  s + cb\nend",
			wantErr: "unsupported addition operands",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireCheckWarningContains(t, compileScript(t, tc.source), tc.wantErr)
		})
	}
}

// Pairs the runtime accepts must stay accepted, and a dynamic operand must stay
// silent so the checker remains gradual.
func TestCheckerAllowsConcatenableAndDynamicOperands(t *testing.T) {
	t.Parallel()

	sources := map[string]string{
		"string plus int":     "def f(a: string, b: int) -> string\n  a + b\nend",
		"string plus string":  "def f(a: string, b: string) -> string\n  a + b\nend",
		"string plus untyped": "def f(a: string, b) -> string\n  a + b\nend",
		"array plus array":    "def f(a: array<int>, b: array<int>) -> array<int>\n  a + b\nend",
		// A nullable string can still hold a string, so the outcome is genuinely
		// unknown and the runtime decides. Only a type whose every alternative
		// fails is reported here.
		"string plus nullable string": "def f(a: string, b: string?) -> string\n  a + b\nend",
		"string plus mixed union":     "def f(a: string, b: int | string) -> string\n  a + b\nend",
		// A pairing that can succeed keeps the expression silent even when one
		// side is nullable.
		"nullable string plus string": "def f(a: string?, b: string) -> string\n  a + b\nend",
		"nullable int plus int":       "def f(a: int?, b: int) -> int\n  a + b\nend",
		// number expands to int|float before the rule table is consulted, so a
		// nullable number must not read as having no valid pairing.
		"nullable number plus number": "def f(a: number?, b: number) -> number\n  a + b\nend",
	}

	for name, source := range sources {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScript(t, source))
		})
	}
}
