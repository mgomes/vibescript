package vibes

import (
	"fmt"
	"strings"
)

func resolveType(name string) (TypeKind, bool) {
	nullable := false
	if strings.HasSuffix(name, "?") {
		nullable = true
		name = strings.TrimSuffix(name, "?")
	}
	switch strings.ToLower(name) {
	case "any":
		return TypeAny, nullable
	case "int":
		return TypeInt, nullable
	case "float":
		return TypeFloat, nullable
	case "number":
		return TypeNumber, nullable
	case "string":
		return TypeString, nullable
	case "bool":
		return TypeBool, nullable
	case "nil":
		return TypeNil, nullable
	case "duration":
		return TypeDuration, nullable
	case "time":
		return TypeTime, nullable
	case "money":
		return TypeMoney, nullable
	case "array":
		return TypeArray, nullable
	case "hash", "object":
		return TypeHash, nullable
	case "function":
		return TypeFunction, nullable
	}
	return TypeUnknown, nullable
}

func (p *parser) parseTypeExpr() *TypeExpr {
	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*TypeExpr{first}
	for p.peekToken.Type == tokenPipe {
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
		names[i] = formatTypeExpr(option)
	}
	return &TypeExpr{
		Name:     strings.Join(names, " | "),
		Kind:     TypeUnion,
		Union:    union,
		position: first.position,
	}
}

func (p *parser) parseTypeAtom() *TypeExpr {
	if p.curToken.Type == tokenLBrace {
		return p.parseTypeShape()
	}
	if p.curToken.Type != tokenIdent && p.curToken.Type != tokenNil {
		p.errorExpected(p.curToken, "type name")
		return nil
	}
	ty := &TypeExpr{Name: p.curToken.Literal, position: p.curToken.Pos}
	kind, nullable := resolveType(p.curToken.Literal)
	ty.Kind = kind
	ty.Nullable = nullable

	if p.peekToken.Type == tokenLT {
		if ty.Kind != TypeArray && ty.Kind != TypeHash {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("type %s does not accept type arguments", ty.Name))
			return nil
		}
		p.nextToken()
		p.nextToken()
		typeArgs := []*TypeExpr{}
		for {
			arg := p.parseTypeExpr()
			if arg == nil {
				return nil
			}
			typeArgs = append(typeArgs, arg)

			if p.peekToken.Type == tokenComma {
				p.nextToken()
				p.nextToken()
				continue
			}

			if p.peekToken.Type != tokenGT {
				p.errorExpected(p.peekToken, ">")
				return nil
			}
			p.nextToken()
			break
		}
		ty.TypeArgs = typeArgs
		switch ty.Kind {
		case TypeArray:
			if len(typeArgs) != 1 {
				p.addParseError(ty.position, "array type expects exactly 1 type argument")
				return nil
			}
		case TypeHash:
			if len(typeArgs) != 2 {
				p.addParseError(ty.position, "hash type expects exactly 2 type arguments")
				return nil
			}
		}
	}

	return ty
}

func (p *parser) parseTypeShape() *TypeExpr {
	pos := p.curToken.Pos
	fields := make(map[string]*TypeExpr)

	if p.peekToken.Type == tokenRBrace {
		p.nextToken()
		return &TypeExpr{
			Kind:     TypeShape,
			Shape:    fields,
			position: pos,
		}
	}

	p.nextToken()
	for {
		key, ok := p.parseTypeShapeFieldName()
		if !ok {
			return nil
		}
		if p.peekToken.Type != tokenColon {
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

		if p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			continue
		}
		if p.peekToken.Type != tokenRBrace {
			p.errorExpected(p.peekToken, "}")
			return nil
		}
		p.nextToken()
		break
	}

	return &TypeExpr{
		Kind:     TypeShape,
		Shape:    fields,
		position: pos,
	}
}

func (p *parser) parseTypeShapeFieldName() (string, bool) {
	switch p.curToken.Type {
	case tokenIdent, tokenString, tokenSymbol:
		return p.curToken.Literal, true
	default:
		p.errorExpected(p.curToken, "shape field name")
		return "", false
	}
}
