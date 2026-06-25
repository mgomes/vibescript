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
		// A nullable suffix on the container name (e.g. array?<int>) belongs
		// after the type arguments. resolveType strips a trailing "?" and
		// marks the type nullable, so detect that here and reject the
		// misplaced spelling rather than silently accepting it.
		if ty.Nullable {
			base := strings.TrimSuffix(ty.Name, "?")
			p.addParseError(p.curToken.Pos, fmt.Sprintf(
				"nullable suffix on %s must follow its type arguments; write %s<...>? instead of %s?<...>",
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
		if _, exists := fields[key]; exists {
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
	switch p.curToken.Type {
	case ast.TokenIdent, ast.TokenString, ast.TokenSymbol, ast.TokenEnum:
		return p.curToken.Literal, true
	case ast.TokenAnd, ast.TokenOr, ast.TokenNot:
		if isWordBooleanKeywordToken(p.curToken) {
			return p.curToken.Literal, true
		}
	default:
	}
	p.errorExpected(p.curToken, "shape field name")
	return "", false
}
