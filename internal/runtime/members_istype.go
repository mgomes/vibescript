package runtime

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The is_type? universal predicate tests a value against a built-in type atom
// without coercion: `1.is_type?(:int)` is true, `1.is_type?(:float)` is false,
// and `"5".is_type?(:int)` is false even though the string converts. The atom
// is a symbol or string naming a type — a primitive (`:int`, `:string`,
// `:bool`, `:symbol`, `:nil`, `:number`, `:duration`, `:time`, `:money`), a
// bare container (`:array`, `:hash`, `:range`, `:function`), a class or enum
// name (`:User`, `:Status`, matched by exact name), or any of these with a
// trailing `?` for its nullable form (`:int?` is int or nil). Parameterized
// spellings such as `array<int>` are rejected: the atom vocabulary stays
// deliberately small until the syntax is extended (see issue #971).
const isTypeMemberName = "is_type?"

// newIsTypePredicateBuiltin builds the is_type? predicate. The receiver's
// answer reuses the runtime's strict boundary matcher, so an atom answers
// exactly the way the same spelling behaves as a typed-parameter annotation,
// minus coercion: symbols never coerce into enums here.
func newIsTypePredicateBuiltin() Value {
	name := isTypeMemberName
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("%s does not take a block", name)
		}
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("%s expects exactly one argument", name)
		}
		text, ok := typeAtomArg(args[0])
		if !ok {
			return NewNil(), fmt.Errorf("%s expects a symbol or string type atom", name)
		}
		ty, err := parseTypeAtom(text)
		if err != nil {
			return NewNil(), err
		}
		matches, err := valueMatchesType(receiver, ty)
		if err != nil {
			return NewNil(), err
		}
		return NewBool(matches), nil
	})
}

// typeAtomArg extracts the atom text from an is_type? argument. Symbols and
// strings are accepted, mirroring respond_to?'s method-name argument.
func typeAtomArg(v Value) (string, bool) {
	switch v.Kind() {
	case KindSymbol, KindString:
		return v.String(), true
	default:
		return "", false
	}
}

// builtinTypeAtoms maps the built-in atom spellings to their type kinds. The
// spellings match the annotation grammar's primitive and bare container names.
var builtinTypeAtoms = map[string]TypeKind{
	"nil":      TypeNil,
	"bool":     TypeBool,
	"int":      TypeInt,
	"float":    TypeFloat,
	"number":   TypeNumber,
	"string":   TypeString,
	"symbol":   TypeSymbol,
	"array":    TypeArray,
	"hash":     TypeHash,
	"range":    TypeRange,
	"function": TypeFunction,
	"duration": TypeDuration,
	"time":     TypeTime,
	"money":    TypeMoney,
}

// parseTypeAtom parses an is_type? atom: a built-in type name or a class/enum
// name, optionally suffixed with `?` for the nullable form. Anything wider —
// generics, unions, shapes, `any` (which every value satisfies) — is rejected
// so the predicate stays a meaningful test.
func parseTypeAtom(text string) (*TypeExpr, error) {
	nullable := strings.HasSuffix(text, "?")
	base := strings.TrimSuffix(text, "?")
	if base == "" || !isTypeAtomIdent(base) {
		return nil, fmt.Errorf("%s supports type atoms only, got %q", isTypeMemberName, text)
	}
	ty := &TypeExpr{}
	if kind, ok := builtinTypeAtoms[base]; ok {
		ty.Kind = kind
	} else {
		first, _ := utf8.DecodeRuneInString(base)
		if !unicode.IsUpper(first) {
			return nil, fmt.Errorf("unknown type atom %q in %s", text, isTypeMemberName)
		}
		ty.Kind = TypeEnum
		ty.Name = base
	}
	ty.Nullable = nullable
	return ty, nil
}

// isTypeAtomIdent reports whether base is a plain identifier: letters, digits,
// and underscores only, not starting with a digit.
func isTypeAtomIdent(base string) bool {
	for i, r := range base {
		switch {
		case unicode.IsLetter(r) || r == '_':
		case unicode.IsDigit(r) && i > 0:
		default:
			return false
		}
	}
	return true
}
