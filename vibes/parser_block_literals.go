package vibes

import "strings"

func (p *parser) parseBlockLiteral() *BlockLiteral {
	pos := p.curToken.Pos
	params := []Param{}

	p.nextToken()
	if p.curToken.Type == tokenPipe {
		var ok bool
		params, ok = p.parseBlockParameters()
		if !ok {
			return nil
		}
		p.nextToken()
	}

	body := p.parseBlock(tokenEnd)
	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &BlockLiteral{Params: params, Body: body, position: pos}
}

func (p *parser) parseBlockParameters() ([]Param, bool) {
	params := []Param{}
	p.nextToken()
	if p.curToken.Type == tokenPipe {
		return params, true
	}

	param, ok := p.parseBlockParameter()
	if !ok {
		return nil, false
	}
	params = append(params, param)

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == tokenPipe {
			p.addParseError(p.curToken.Pos, "trailing comma in block parameter list")
			return nil, false
		}
		param, ok := p.parseBlockParameter()
		if !ok {
			return nil, false
		}
		params = append(params, param)
	}

	if !p.expectPeek(tokenPipe) {
		return nil, false
	}

	return params, true
}

func (p *parser) parseBlockParameter() (Param, bool) {
	if p.curToken.Type != tokenIdent {
		p.errorExpected(p.curToken, "block parameter")
		return Param{}, false
	}
	param := Param{Name: p.curToken.Literal}
	if p.peekToken.Type == tokenColon {
		p.nextToken()
		p.nextToken()
		param.Type = p.parseBlockParamType()
		if param.Type == nil {
			return Param{}, false
		}
	}
	return param, true
}

func (p *parser) parseBlockParamType() *TypeExpr {
	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*TypeExpr{first}
	for p.peekToken.Type == tokenPipe && p.blockParamUnionContinues() {
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

func (p *parser) blockParamUnionContinues() bool {
	if p.peekToken.Type != tokenPipe {
		return false
	}

	savedLexer := *p.l
	savedCur := p.curToken
	savedPeek := p.peekToken
	savedErrors := len(p.errors)

	p.nextToken()
	p.nextToken()
	atom := p.parseTypeAtom()
	ok := atom != nil && (p.peekToken.Type == tokenComma || p.peekToken.Type == tokenPipe)

	p.l = &savedLexer
	p.curToken = savedCur
	p.peekToken = savedPeek
	p.errors = p.errors[:savedErrors]
	return ok
}
