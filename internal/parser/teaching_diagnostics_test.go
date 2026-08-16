package parser

import (
	"strings"
	"testing"
)

// Inheritance is deliberately absent with module functions as the documented
// alternative, but the parse error said only `unexpected token "<"`, which
// reads as a syntax slip and leaves the author retrying variants.
func TestClassInheritanceErrorNamesTheReplacement(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"class A\nend\nclass B < A\nend",
		"class B < SomeModule\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			_, errs := parseSource(t, source)
			if len(errs) == 0 {
				t.Fatalf("expected a parse error for class inheritance")
			}
			joined := joinTeachingErrors(errs)
			if !strings.Contains(joined, "inheritance is not supported") {
				t.Fatalf("error = %s, want it to name the decision", joined)
			}
			if !strings.Contains(joined, "def self.name") {
				t.Fatalf("error = %s, want it to name the replacement", joined)
			}
		})
	}
}

// An author arriving from attr_accessor :x reaches property :x next, and
// "expected property name, got symbol" does not say that the argument shape
// differs too.
func TestPropertySymbolErrorNamesTheBareForm(t *testing.T) {
	t.Parallel()

	_, errs := parseSource(t, "class A\n  property :x\nend")
	if len(errs) == 0 {
		t.Fatalf("expected a parse error for property :x")
	}
	joined := joinTeachingErrors(errs)
	if !strings.Contains(joined, "property x") || !strings.Contains(joined, "not property :x") {
		t.Fatalf("error = %s, want it to show the bare form", joined)
	}
}

// The valid spellings must keep parsing, and a less-than comparison must not
// be mistaken for an inheritance attempt.
func TestTeachingDiagnosticsDoNotBreakValidSource(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"class A\n  property x\nend",
		"class A\n  getter x\nend",
		"class A\n  setter x\nend",
		"class A\nend",
		"x = 1 < 2",
		"def f(a, b)\n  a < b\nend",
		"module M\nend",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			if _, errs := parseSource(t, source); len(errs) != 0 {
				t.Fatalf("%s: unexpected parse errors: %v", source, errs)
			}
		})
	}
}

func joinTeachingErrors(errs []error) string {
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, "\n")
}
