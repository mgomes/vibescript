package parser

import (
	"strings"
	"testing"

	"github.com/mgomes/vibescript/internal/ast"
)

// A migration diagnostic recommends code. Code the parser rejects is worse
// guidance than none: the author follows it and lands on a second error that
// explains nothing. These tests take the recommendations from where the
// messages build them rather than from remembered examples, so editing a
// message cannot introduce advice that does not parse.

// classMemberSpellings lists the method-name forms a class body accepts. The
// operator half is what `def self.` cannot spell, which is the whole reason
// the module diagnostics have to choose between two recommendations.
func classMemberSpellings() []string {
	return []string{
		"display_name", "valid?", "save!", "name=",
		"+", "-", "*", "/", "%", "**", "<<", "&",
		"==", "!=", "<", "<=", ">", ">=", "<=>", "[]", "[]=",
	}
}

// declarationFor spells a one-line definition of the named method, so a
// spelling can be fed back through the parser in the position it came from.
func declarationFor(name string) string {
	params := "(other)"
	if name == "[]=" {
		params = "(index, value)"
	}
	return "def " + name + params + "\n    1\n  end"
}

// TestClassMemberSpellingsCoverTheGrammar keeps the table honest: every
// spelling in it must be a method a class body actually accepts, and the
// operator names must be exactly what the grammar records for them.
func TestClassMemberSpellingsCoverTheGrammar(t *testing.T) {
	t.Parallel()

	for _, name := range classMemberSpellings() {
		program, errs := Parse("class C\n  " + declarationFor(name) + "\nend")
		if len(errs) > 0 {
			t.Fatalf("class C with %s does not parse: %v", name, errs[0])
		}
		class, ok := program.Statements[0].(*ast.ClassStmt)
		if !ok || len(class.Methods) != 1 {
			t.Fatalf("class C with %s did not record one method", name)
		}
		if got := class.Methods[0].Name; got != name {
			t.Fatalf("class C with %s recorded method %q", name, got)
		}
	}
}

// TestModuleMemberDiagnosticsRecommendCodeThatParses runs every replacement a
// module-member diagnostic can emit back through the parser. A name that can
// follow `self.` must be recommended in that form and the form must parse; a
// name that cannot must be recommended something else entirely, never a
// `def self.` spelling the parser will reject.
func TestModuleMemberDiagnosticsRecommendCodeThatParses(t *testing.T) {
	t.Parallel()

	for _, name := range classMemberSpellings() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, errs := Parse("module Naming\n  " + declarationFor(name) + "\nend")
			if len(errs) != 1 {
				t.Fatalf("module Naming with %s produced %d errors, want 1", name, len(errs))
			}
			message := errs[0].Error()

			form, spellable := moduleFunctionForm(name)
			if !spellable {
				if strings.Contains(message, "def self.") {
					t.Fatalf("%s cannot be a module function, but the error recommends one: %s", name, message)
				}
				if !strings.Contains(message, "class") {
					t.Fatalf("%s has no module form, so the error must point at a class: %s", name, message)
				}
				return
			}
			if !strings.Contains(message, form) {
				t.Fatalf("the error for %s does not recommend %q: %s", name, form, message)
			}
			if _, errs := Parse("module Naming\n  " + form + "(other)\n    1\n  end\nend"); len(errs) > 0 {
				t.Fatalf("the error for %s recommends %q, which does not parse: %v", name, form, errs[0])
			}
		})
	}
}

// TestRecommendedSnippetsParse runs the two spellings the prose recommends,
// which reach every module diagnostic through moduleNamespaceReplacement.
func TestRecommendedSnippetsParse(t *testing.T) {
	t.Parallel()

	if !strings.Contains(moduleNamespaceReplacement, moduleFunctionExample) ||
		!strings.Contains(moduleNamespaceReplacement, namespaceCallExample) {
		t.Fatal("the replacement prose no longer carries the snippets this test checks")
	}
	if _, errs := Parse("module Naming\n  " + moduleFunctionExample + "(person)\n    1\n  end\nend"); len(errs) > 0 {
		t.Fatalf("the recommended declaration %q does not parse: %v", moduleFunctionExample, errs[0])
	}
	if _, errs := Parse(namespaceCallExample + "\n"); len(errs) > 0 {
		t.Fatalf("the recommended call %q does not parse: %v", namespaceCallExample, errs[0])
	}
}
