package runtime

import "fmt"

// Several divergences from Ruby are deliberate and documented, but their
// diagnostics name the missing thing without naming the supported alternative,
// which strands an author arriving with Ruby habits -- the population this
// language is written for. An agent reads the error far more often than it
// reads the docs, so the error is where the idiom has to be taught.

// classMemberAlternatives maps a Ruby class-body macro onto the spelling this
// language uses, so the unknown-member error can point somewhere.
var classMemberAlternatives = map[string]string{
	"attr_accessor": `use "property x" for a reader and writer`,
	"attr_reader":   `use "getter x"`,
	"attr_writer":   `use "setter x"`,
}

// classMemberAlternativeHint returns the trailing hint for a class member that
// is a known Ruby spelling of something this language provides differently.
func classMemberAlternativeHint(property string) string {
	alternative, ok := classMemberAlternatives[property]
	if !ok {
		return ""
	}
	// The argument shape differs too: property takes a bare name, not a
	// symbol, so an author who only learns the new name hits a second error.
	return fmt.Sprintf(" (%s; the name is bare, not a symbol)", alternative)
}

// topLevelBindingHint returns the trailing hint for an undefined variable that
// is nonetheless assigned at the script's top level.
//
// Defining a lookup table or config constant at the top of a file and reading
// it from a function is one of the most natural program shapes there is, and
// "undefined variable LIMIT" gives no route forward: didYouMean cannot fire
// because the name genuinely is not in scope, so nothing hints that the
// binding exists one scope away.
func (c *scriptChecker) topLevelBindingHint(name string) string {
	if !c.hasTopLevelBinding(name) {
		return ""
	}
	return " (functions do not capture top-level bindings; declare it in a module and reference it qualified, as in Config::" + name + ")"
}

// hasTopLevelBinding reports whether the entrypoint assigns name directly in
// its own body.
func (c *scriptChecker) hasTopLevelBinding(name string) bool {
	if c.script == nil || c.script.entrypoint == "" {
		return false
	}
	entry := c.script.functions[c.script.entrypoint]
	if entry == nil {
		return false
	}
	for _, stmt := range entry.Body {
		assign, ok := stmt.(*AssignStmt)
		if !ok {
			continue
		}
		if ident, ok := assign.Target.(*Identifier); ok && ident.Name == name {
			return true
		}
	}
	return false
}
