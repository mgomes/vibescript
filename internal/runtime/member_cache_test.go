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

func TestStoredTemporalMembersStayBoundToReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def stored_temporal_methods()
      timestamp = Time.parse("2024-01-02T03:04:05Z")
      format = timestamp.format
      equal = timestamp.eql?
      after = Duration.parse("PT60S").after

      {
        year: format("2006"),
        equal: equal(Time.parse("2024-01-02T03:04:05Z")),
        shifted: after(Time.parse("2024-01-02T03:04:00Z")).format("15:04:05")
      }
    end
    `)

	result := callFunc(t, script, "stored_temporal_methods", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	values := result.Hash()
	if !values["year"].Equal(NewString("2024")) {
		t.Fatalf("stored time.format result = %v, want 2024", values["year"])
	}
	if !values["equal"].Equal(NewBool(true)) {
		t.Fatalf("stored time.eql? result = %v, want true", values["equal"])
	}
	if !values["shifted"].Equal(NewString("03:05:00")) {
		t.Fatalf("stored duration.after result = %v, want 03:05:00", values["shifted"])
	}
}
