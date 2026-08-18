package runtime

import "testing"

func TestBuiltinMemberDispatchReusesCachedBuiltinValues(t *testing.T) {
	t.Parallel()
	money, err := parseMoneyLiteral("1.00 USD")
	if err != nil {
		t.Fatalf("parse money: %v", err)
	}

	exec := &Execution{}
	cases := []struct {
		name     string
		receiver Value
		property string
	}{
		{name: "array", receiver: NewArray([]Value{}), property: "push"},
		{name: "hash", receiver: NewHash(map[string]Value{}), property: "merge"},
		{name: "string", receiver: NewString("abc"), property: "length"},
		{name: "int", receiver: NewInt(7), property: "abs"},
		{name: "float", receiver: NewFloat(1.5), property: "round"},
		{name: "money", receiver: NewMoney(money), property: "format"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, err := exec.getMember(tc.receiver, tc.property, Position{})
			if err != nil {
				t.Fatalf("first member lookup: %v", err)
			}
			second, err := exec.getMember(tc.receiver, tc.property, Position{})
			if err != nil {
				t.Fatalf("second member lookup: %v", err)
			}
			if valueBuiltin(first) == nil || valueBuiltin(second) == nil {
				t.Fatalf("member %s did not resolve to builtins", tc.property)
			}
			if valueBuiltin(first) != valueBuiltin(second) {
				t.Fatalf("member %s did not reuse cached builtin payload", tc.property)
			}
		})
	}
}
