package parser

import (
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

// resolveType is the parser-local alias for ast.ResolveType.
func resolveType(name string) (ast.TypeKind, bool) { return ast.ResolveType(name) }

// maxTypeDepth bounds the mutual recursion between parseTypeExpr, parseTypeAtom,
// and parseTypeShape. Without it, a deeply nested type annotation (e.g.
// array<array<...>>) overflows the goroutine stack at parse time, an
// uncatchable fatal that crashes the host. The cap is generous for real code
// yet far below the stack-overflow threshold; it mirrors the runtime's default
// RecursionLimit of 64.
const maxTypeDepth = 64

func (p *parser) parseTypeExpr() *ast.TypeExpr {
	p.typeDepth++
	defer func() { p.typeDepth-- }()
	if p.typeDepth > maxTypeDepth {
		p.addParseError(p.curToken.Pos, "type annotation nesting too deep")
		return nil
	}

	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*ast.TypeExpr{first}
	for p.peekToken.Type == ast.TokenPipe {
		p.nextToken()
		p.nextToken()
		next := p.parseTypeAtom()
		if next == nil {
			return nil
		}
		union = append(union, next)
	}

	if len(union) == 1 {
		return first
	}

	names := make([]string, len(union))
	for i, option := range union {
		names[i] = ast.FormatTypeExpr(option)
	}
	return &ast.TypeExpr{
		Name:     strings.Join(names, " | "),
		Kind:     ast.TypeUnion,
		Union:    union,
		Position: first.Position,
	}
}

func (p *parser) parseTypeAtom() *ast.TypeExpr {
	if p.curToken.Type == ast.TokenLBrace {
		return p.parseTypeShape()
	}
	if p.curToken.Type != ast.TokenIdent && p.curToken.Type != ast.TokenNil {
		p.errorExpected(p.curToken, "type name")
		return nil
	}
	ty := &ast.TypeExpr{Name: p.curToken.Literal, Position: p.curToken.Pos}
	kind, nullable := resolveType(p.curToken.Literal)
	ty.Kind = kind
	ty.Nullable = nullable
	if ty.Kind == ast.TypeUnknown && p.curToken.Type == ast.TokenIdent {
		ty.Kind = ast.TypeEnum
		if nullable {
			ty.Name = strings.TrimSuffix(ty.Name, "?")
		}
	}

	if p.peekToken.Type == ast.TokenLT {
		if ty.Kind != ast.TypeArray && ty.Kind != ast.TypeHash {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("type %s does not accept type arguments", ty.Name))
			return nil
		}
		// A nullable suffix on the container name (e.g. array?<int>) is
		// misplaced. resolveType strips a trailing "?" and marks the type
		// nullable, so detect that here and reject the spelling rather than
		// silently accepting it. A suffix `?` on a generic container (e.g.
		// array<int>?) is not yet supported, so point users at the union
		// form (array<int> | nil), which is the documented nullable spelling
		// for compound types.
		if ty.Nullable {
			base := strings.TrimSuffix(ty.Name, "?")
			p.addParseError(p.curToken.Pos, fmt.Sprintf(
				"nullable suffix on %s is misplaced; write the nullable container as a union, e.g. %s<...> | nil, instead of %s?<...>",
				base, base, base))
			return nil
		}
		p.nextToken()
		p.nextToken()
		typeArgs := []*ast.TypeExpr{}
		for {
			arg := p.parseTypeExpr()
			if arg == nil {
				return nil
			}
			typeArgs = append(typeArgs, arg)

			if p.peekToken.Type == ast.TokenComma {
				p.nextToken()
				p.nextToken()
				continue
			}

			if p.peekToken.Type != ast.TokenGT {
				p.errorExpected(p.peekToken, ">")
				return nil
			}
			p.nextToken()
			break
		}
		ty.TypeArgs = typeArgs
		switch ty.Kind {
		case ast.TypeArray:
			if len(typeArgs) != 1 {
				p.addParseError(ty.Position, "array type expects exactly 1 type argument")
				return nil
			}
		case ast.TypeHash:
			if len(typeArgs) != 2 {
				p.addParseError(ty.Position, "hash type expects exactly 2 type arguments")
				return nil
			}
		}
	}

	return ty
}

func (p *parser) parseTypeShape() *ast.TypeExpr {
	pos := p.curToken.Pos
	fields := make(map[string]*ast.TypeExpr)
	p.shapeStructurallyInvalid = false

	if p.peekToken.Type == ast.TokenRBrace {
		p.nextToken()
		return &ast.TypeExpr{
			Kind:     ast.TypeShape,
			Shape:    fields,
			Position: pos,
		}
	}

	p.nextToken()
	for {
		key, ok := p.parseTypeShapeFieldName()
		if !ok {
			return nil
		}
		if p.peekToken.Type != ast.TokenColon {
			p.errorExpected(p.peekToken, ":")
			return nil
		}
		p.nextToken()
		p.nextToken()
		fieldType := p.parseTypeExpr()
		if fieldType == nil {
			return nil
		}
		if prior, exists := fields[key]; exists {
			// A repeated key reaches here only after both field values parsed as
			// complete type expressions ending at a `,` or `}` boundary, so the
			// brace group is clearly a shape annotation rather than a hash
			// literal. Mark it as a structural shape error worth surfacing,
			// unless either repeated value is a bare identifier naming a local
			// value: such a reference is a hash default (mirroring the
			// shapeFieldNamesLocalValue disambiguation), so leave the flag clear
			// and let the group fall back to a hash default.
			if !p.typeAtomNamesLocalValue(prior) && !p.typeAtomNamesLocalValue(fieldType) {
				p.shapeStructurallyInvalid = true
			}
			p.addParseError(p.curToken.Pos, fmt.Sprintf("duplicate shape field %s", key))
			return nil
		}
		fields[key] = fieldType

		if p.peekToken.Type == ast.TokenComma {
			p.nextToken()
			p.nextToken()
			continue
		}
		if p.peekToken.Type != ast.TokenRBrace {
			// A complete field value followed by another `label:` field opener is a
			// missing field separator (`{ id: string name: int }`). A brace group
			// cannot reach here as a valid hash literal: `value label:` without a
			// separator is malformed in Ruby too, so the braces are a malformed shape
			// annotation rather than a hash default. Mark it structurally invalid so
			// the speculative parameter-list parse routes the group to the type path
			// and re-emits the shape diagnostic instead of silently reinterpreting
			// the braces as a keyword default, which the line-limited hash parse
			// would otherwise accept in parenless parameter syntax.
			//
			// Any other trailing token leaves the flag clear: in the speculative
			// parse it usually means the value was an expression that stopped at an
			// operator (`{ sum: a + 1 }`), so the group falls back to a hash default.
			if p.peekStartsShapeField() {
				p.shapeStructurallyInvalid = true
			}
			p.errorExpected(p.peekToken, "}")
			return nil
		}
		p.nextToken()
		break
	}

	return &ast.TypeExpr{
		Kind:     ast.TypeShape,
		Shape:    fields,
		Position: pos,
	}
}

func (p *parser) parseTypeShapeFieldName() (string, bool) {
	if tokenStartsShapeFieldName(p.curToken) {
		return p.curToken.Literal, true
	}
	p.errorExpected(p.curToken, "shape field name")
	return "", false
}

// peekStartsShapeField reports whether the lookahead tokens open another shape
// field, i.e. a field-name token followed by a `:`. parseTypeShape uses it to
// recognize a missing field separator (`{ id: string name: int }`) after a
// complete field value, distinguishing it from an expression continuation
// (`{ sum: a + 1 }`) that should fall back to a hash default.
func (p *parser) peekStartsShapeField() bool {
	return tokenStartsShapeFieldName(p.peekToken) && p.peekPeek.Type == ast.TokenColon
}

// tokenStartsShapeFieldName reports whether tok can name a shape field. Shape
// field names accept the same tokens as hash keys: bare identifiers, string and
// symbol literals, enum names, and the word-form boolean keywords (`and`, `or`,
// `not`) used as ordinary labels.
func tokenStartsShapeFieldName(tok ast.Token) bool {
	switch tok.Type {
	case ast.TokenIdent, ast.TokenString, ast.TokenSymbol, ast.TokenEnum:
		return true
	case ast.TokenAnd, ast.TokenOr, ast.TokenNot:
		return isWordBooleanKeywordToken(tok)
	default:
		return false
	}
}
