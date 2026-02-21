package vibes

import "strings"

func (p *parser) parseParams() []Param {
	params := []Param{}
	for {
		if p.curToken.Type != tokenIdent && p.curToken.Type != tokenIvar {
			p.errorExpected(p.curToken, "parameter name")
			return params
		}
		param := Param{Name: p.curToken.Literal}
		if p.curToken.Type == tokenIvar {
			param.IsIvar = true
			param.Name = strings.TrimPrefix(param.Name, "@")
		}
		if p.peekToken.Type == tokenColon {
			p.nextToken()
			p.nextToken()
			param.Type = p.parseTypeExpr()
			if param.Type == nil {
				return params
			}
		}
		if p.peekToken.Type == tokenAssign {
			p.nextToken()
			p.nextToken()
			param.DefaultVal = p.parseExpression(lowestPrec)
		}
		params = append(params, param)
		if p.peekToken.Type != tokenComma {
			break
		}
		p.nextToken()
		p.nextToken()
	}
	return params
}

func (p *parser) parsePropertyDecl(kind TokenType) PropertyDecl {
	pos := p.curToken.Pos
	names := []string{}
	p.nextToken()
	if p.curToken.Type == tokenIdent {
		names = append(names, p.curToken.Literal)
		for p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			if p.curToken.Type != tokenIdent {
				p.errorExpected(p.curToken, "property name")
				break
			}
			names = append(names, p.curToken.Literal)
		}
	}
	return PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), position: pos}
}
