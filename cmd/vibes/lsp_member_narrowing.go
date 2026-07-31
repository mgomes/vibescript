package main

import (
	"slices"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/internal/parser"
	"github.com/mgomes/vibescript/vibes"
)

// Completion after "." offered the union of every builtin member -- 306 items
// -- whatever the receiver was, so a string receiver was offered `amount` and
// `cents` (money) and `ago` and `before` (temporal). It could not rule out a
// wrong method name, which is the most frequent mistake there is to make here.
//
// Narrowing needs the receiver's type, and the receiver cannot be read from the
// document as typed: completion fires when the buffer holds a bare trailing
// dot, which is a syntax error yielding no member node at all. A probe name is
// spliced in at the cursor so the line parses, and the parser hands back the
// receiver it built.
//
// Whenever the receiver is anything this cannot resolve from syntax, the full
// union is returned, so a dynamic receiver keeps working exactly as before.
// That direction matters: narrowing wrongly would *hide* members that apply,
// with nothing to tell the author they were hidden.

// completionProbeMember replaces any partially typed member name at the cursor
// so the receiver parses the same way whether the cursor sits right after the
// dot or a few characters into the name. It is a name no script would write.
const completionProbeMember = "vibesCompletionProbe__"

// narrowedMemberCompletionItems returns the items for the receiver at the
// cursor, or nil when its type is not decidable from syntax.
func narrowedMemberCompletionItems(source string, lines []string, line, character int) []map[string]any {
	receiver, ok := completionReceiverKind(source, lines, line, character)
	if !ok {
		return nil
	}
	names, ok := vibes.MemberCompletionNames()[receiver]
	if !ok || len(names) == 0 {
		return nil
	}
	return memberCompletionItemsForReceiver(receiver, names)
}

// completionReceiverKind reports the receiver kind at the cursor, named as
// MemberCompletionNames keys it.
func completionReceiverKind(source string, lines []string, line, character int) (string, bool) {
	probed, ok := spliceCompletionProbe(source, lines, line, character)
	if !ok {
		return "", false
	}
	receiver, params, ok := parser.MemberReceiverFor(probed, completionProbeMember)
	if !ok {
		return "", false
	}
	return staticReceiverKind(receiver, params)
}

// spliceCompletionProbe replaces the partially typed member name at the cursor
// with the probe, leaving the receiver untouched.
func spliceCompletionProbe(source string, lines []string, line, character int) (string, bool) {
	if line < 0 || line >= len(lines) {
		return "", false
	}
	runes := []rune(lines[line])
	end := min(utf16OffsetToRuneIndex(lines[line], character), len(runes))
	start := end
	for start > 0 && isWordRune(runes[start-1]) {
		start--
	}
	if start == 0 || runes[start-1] != '.' {
		return "", false
	}
	sourceLines := strings.Split(source, "\n")
	if line >= len(sourceLines) {
		return "", false
	}
	sourceLines[line] = string(runes[:start]) + completionProbeMember + string(runes[end:])
	return strings.Join(sourceLines, "\n"), true
}

// staticReceiverKind maps a receiver expression to its dispatch kind when that
// is decidable from syntax alone. Anything else reports false so the caller
// falls back to the full union.
func staticReceiverKind(receiver ast.Expression, params []ast.Param) (string, bool) {
	switch node := receiver.(type) {
	case *ast.StringLiteral, *ast.InterpolatedString:
		return "string", true
	case *ast.IntegerLiteral:
		return "int", true
	case *ast.FloatLiteral:
		return "float", true
	case *ast.BoolLiteral:
		return "bool", true
	case *ast.SymbolLiteral, *ast.InterpolatedSymbol:
		return "symbol", true
	case *ast.ArrayLiteral:
		return "array", true
	case *ast.HashLiteral:
		return "hash", true
	case *ast.RegexLiteral:
		return "regex", true
	case *ast.Identifier:
		return declaredParamKind(node.Name, params)
	}
	return "", false
}

// declaredParamKind resolves an identifier receiver against the enclosing
// function's declared parameter types. Only an explicit annotation counts:
// nothing is inferred here, so `def f(s: string)` narrows and an unannotated
// parameter does not.
func declaredParamKind(name string, params []ast.Param) (string, bool) {
	for _, param := range params {
		if param.Name == name {
			return typeExprReceiverKind(param.Type)
		}
	}
	return "", false
}

// typeExprReceiverKind maps a declared type to the dispatch kind whose members
// apply to it. A nullable or union type reports false: its members are not the
// members of any one kind.
func typeExprReceiverKind(ty *ast.TypeExpr) (string, bool) {
	if ty == nil || ty.Nullable {
		return "", false
	}
	switch ty.Kind {
	case ast.TypeString:
		return "string", true
	case ast.TypeInt:
		return "int", true
	case ast.TypeFloat:
		return "float", true
	case ast.TypeBool:
		return "bool", true
	case ast.TypeSymbol:
		return "symbol", true
	case ast.TypeArray:
		return "array", true
	case ast.TypeHash:
		return "hash", true
	case ast.TypeMoney:
		return "money", true
	case ast.TypeDuration:
		return "duration", true
	case ast.TypeTime:
		return "time", true
	case ast.TypeRange:
		return "range", true
	}
	return "", false
}

// memberCompletionItemsForReceiver builds the items for one receiver kind,
// matching the shape of the union items so clients see no difference beyond
// the shorter list.
func memberCompletionItemsForReceiver(receiver string, names []string) []map[string]any {
	sorted := make([]string, len(names))
	copy(sorted, names)
	slices.Sort(sorted)

	contracts := memberContractsByName()
	items := make([]map[string]any, 0, len(sorted))
	for _, label := range sorted {
		item := map[string]any{
			"label":  label,
			"kind":   2, // Method
			"detail": receiver,
		}
		// The union list withholds prose docs for names several kinds share,
		// because it has no receiver to pick an entry with. Here the receiver
		// is known, so the ambiguity that motivated withholding them is gone.
		documentation := unambiguousMemberDocMarkdown(label)
		if signatures := memberContractSignatures(label, contracts[label]); signatures != "" {
			if documentation != "" {
				documentation += "\n\n" + signatures
			} else {
				documentation = signatures
			}
		}
		if documentation != "" {
			item["documentation"] = map[string]any{"kind": "markdown", "value": documentation}
		}
		items = append(items, item)
	}
	return items
}
