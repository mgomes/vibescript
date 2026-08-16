package parser

import (
	"unicode/utf8"

	"github.com/mgomes/vibescript/internal/ast"
)

// maxDiagnosticSourceBytes bounds how much source text one diagnostic may
// reproduce. An identifier is as long as the source cares to spell it, and a
// message that quotes one in full makes the size of the error list a multiple
// of the size of the input rather than a property of the parser. The bound is
// wide enough that ordinary names survive intact and narrow enough that a full
// error list stays small.
const maxDiagnosticSourceBytes = 64

// srcText wraps text taken from the source for use as a diagnostic argument.
// It formats to a bounded prefix of that text, and only when the diagnostic is
// actually built, so passing it costs nothing for an error the parse-error cap
// discards.
type srcText string

func (s srcText) String() string {
	return truncateSourceText(string(s))
}

// truncateSourceText shortens text to maxDiagnosticSourceBytes, cutting on a
// rune boundary so the result stays valid UTF-8, and marks the cut.
func truncateSourceText(text string) string {
	if len(text) <= maxDiagnosticSourceBytes {
		return text
	}
	cut := maxDiagnosticSourceBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "..."
}

// typeText names a parsed type in a diagnostic. Building the spelling is
// itself work proportional to the source, so it happens inside String, which
// runs only for a diagnostic the error budget keeps.
type typeText struct{ ty *ast.TypeExpr }

func (t typeText) String() string {
	return truncateSourceText(ast.FormatTypeExpr(t.ty))
}

// targetText names a destructuring target in a diagnostic, on the same terms
// as typeText. An empty target reads as the anonymous rest marker.
type targetText struct{ target ast.Expression }

func (t targetText) String() string {
	name := ast.FormatDestructureTarget(t.target)
	if name == "" {
		return "*"
	}
	return truncateSourceText(name)
}
