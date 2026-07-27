package parser

import (
	"fmt"

	"github.com/mgomes/vibescript/internal/ast"
)

// ordinaryParamOrderMessage explains an ordinary parameter that follows a
// rest, keyword, keyword rest, or block capture parameter.
//
// One spelling reaches here that is not an ordering mistake at all. `name:`
// followed by a bare identifier parses as a type annotation, which makes the
// parameter ordinary, which is what trips the ordering rule -- so `def f(a:,
// b: a)` reports parameter ordering when the author was writing the documented
// "a later default may reference an earlier parameter" feature. The boundary is
// invisible from the call site: `b: a * 1` and `b: (a)` are defaults, and only
// the bare `b: a` is not.
//
// The parenthesised form is a trivial fix once you know it exists, and the
// ordering message gives no hint that it does.
func ordinaryParamOrderMessage(param ast.Param, earlier []ast.Param) string {
	const orderMessage = "ordinary parameters must precede rest, keyword, keyword rest, and block capture parameters"
	name, ok := bareIdentifierTypeName(param.Type)
	if !ok || !namesEarlierParam(name, earlier) {
		return orderMessage
	}
	return fmt.Sprintf("%s: %s reads as a type annotation, not a default; write %s: (%s) to default %s to parameter %s",
		param.Name, name, param.Name, name, param.Name, name)
}

// bareIdentifierTypeName returns the name of a type annotation written as a
// single identifier that is not one of the builtin type names. A builtin name
// is a type beyond reasonable doubt, so a parameter that shadows one keeps the
// ordering message.
//
// A bare name the parser does not recognize as builtin becomes a named type
// (TypeEnum, or TypeUnknown when it cannot be classified at all), which is the
// same shape a reference to an earlier parameter would take.
func bareIdentifierTypeName(ty *ast.TypeExpr) (string, bool) {
	if ty == nil || ty.Name == "" {
		return "", false
	}
	if ty.Kind != ast.TypeEnum && ty.Kind != ast.TypeUnknown {
		return "", false
	}
	if ty.Nullable || len(ty.TypeArgs) > 0 || len(ty.Union) > 0 || ty.Shape != nil {
		return "", false
	}
	return ty.Name, true
}

// namesEarlierParam reports whether name matches a parameter already declared
// in the same list, which is what makes the default reading the likely one.
func namesEarlierParam(name string, earlier []ast.Param) bool {
	for _, param := range earlier {
		if param.Name == name {
			return true
		}
	}
	return false
}
