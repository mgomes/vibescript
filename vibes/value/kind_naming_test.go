package value_test

import (
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

func TestValueKindStringNamesClassAndInstance(t *testing.T) {
	t.Parallel()
	if got := value.KindClass.String(); got != "class" {
		t.Fatalf("KindClass.String() = %q, want %q", got, "class")
	}
	if got := value.KindInstance.String(); got != "instance" {
		t.Fatalf("KindInstance.String() = %q, want %q", got, "instance")
	}
}

func TestParseMoneyLiteralRejectsBareSigns(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"- USD", "+ USD"} {
		if _, err := value.ParseMoneyLiteral(input); err == nil {
			t.Fatalf("ParseMoneyLiteral(%q) = nil error, want rejection", input)
		}
	}
}
