package parser

import (
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseBlockLiteral() *ast.BlockLiteral {
	pos := p.curToken.Pos
	params := []ast.Param{}

	p.nextToken()
	if p.curToken.Type == ast.TokenPipe {
		var ok bool
		params, ok = p.parseBlockParameters()
		if !ok {
			return nil
		}
		p.nextToken()
	}

	body := p.parseBlock(ast.TokenEnd)
	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.BlockLiteral{Params: params, Body: body, Position: pos}
}

func (p *parser) parseBlockParameters() ([]ast.Param, bool) {
	params := []ast.Param{}
	p.nextToken()
	if p.curToken.Type == ast.TokenPipe {
		return params, true
	}

	param, ok := p.parseBlockParameter()
	if !ok {
		return nil, false
	}
	params = append(params, param)

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == ast.TokenPipe {
			p.addParseError(p.curToken.Pos, "trailing comma in block parameter list")
			return nil, false
		}
		param, ok := p.parseBlockParameter()
		if !ok {
			return nil, false
		}
		params = append(params, param)
	}

	if !p.expectPeek(ast.TokenPipe) {
		return nil, false
	}

	return params, true
}

func (p *parser) parseBlockParameter() (ast.Param, bool) {
	if p.curToken.Type != ast.TokenIdent {
		p.errorExpected(p.curToken, "block parameter")
		return ast.Param{}, false
	}
	param := ast.Param{Name: p.curToken.Literal}
	if p.peekToken.Type == ast.TokenColon {
		p.nextToken()
		p.nextToken()
		param.Type = p.parseBlockParamType()
		if param.Type == nil {
			return ast.Param{}, false
		}
	}
	return param, true
}

func (p *parser) parseBlockParamType() *ast.TypeExpr {
	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*ast.TypeExpr{first}
	for p.peekToken.Type == ast.TokenPipe && p.blockParamUnionContinues() {
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

func (p *parser) blockParamUnionContinues() bool {
	if p.peekToken.Type != ast.TokenPipe {
		return false
	}

	savedLexer := *p.l
	savedCur := p.curToken
	savedPeek := p.peekToken
	savedErrors := len(p.errors)

	p.nextToken()
	p.nextToken()
	atom := p.parseTypeAtom()
	ok := atom != nil && (p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenPipe)

	p.l = &savedLexer
	p.curToken = savedCur
	p.peekToken = savedPeek
	p.errors = p.errors[:savedErrors]
	return ok
}
