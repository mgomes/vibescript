package parser

import (
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseParams() []ast.Param {
	params := []ast.Param{}
	for {
		if p.curToken.Type != ast.TokenIdent && p.curToken.Type != ast.TokenIvar {
			p.errorExpected(p.curToken, "parameter name")
			return params
		}
		param := ast.Param{Name: p.curToken.Literal}
		if p.curToken.Type == ast.TokenIvar {
			param.IsIvar = true
			param.Name = strings.TrimPrefix(param.Name, "@")
		}
		if p.peekToken.Type == ast.TokenColon {
			p.nextToken()
			p.nextToken()
			param.Type = p.parseTypeExpr()
			if param.Type == nil {
				return params
			}
		}
		if p.peekToken.Type == ast.TokenAssign {
			p.nextToken()
			p.nextToken()
			param.DefaultVal = p.parseExpression(lowestPrec)
		}
		params = append(params, param)
		if p.peekToken.Type != ast.TokenComma {
			break
		}
		p.nextToken()
		p.nextToken()
	}
	return params
}

func (p *parser) parsePropertyDecl(kind ast.TokenType) ast.PropertyDecl {
	pos := p.curToken.Pos
	names := []string{}
	p.nextToken()
	if p.curToken.Type != ast.TokenIdent {
		p.errorExpected(p.curToken, "property name")
		return ast.PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), Position: pos}
	}
	names = append(names, p.curToken.Literal)
	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != ast.TokenIdent {
			p.errorExpected(p.curToken, "property name")
			break
		}
		names = append(names, p.curToken.Literal)
	}
	return ast.PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), Position: pos}
}
