package vibes

func (p *parser) parseFunctionStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()

	isClassMethod := false
	var name string
	if p.curToken.Type == tokenSelf && p.peekToken.Type == tokenDot {
		isClassMethod = true
		p.nextToken() // consume dot
		if !p.expectPeek(tokenIdent) {
			return nil
		}
		name = p.curToken.Literal
		p.nextToken()
	} else {
		if p.curToken.Type != tokenIdent {
			p.errorExpected(p.curToken, "function name")
			return nil
		}
		name = p.curToken.Literal
		p.nextToken()
	}

	if p.curToken.Type == tokenAssign {
		name += "="
		p.nextToken()
	}

	params := []Param{}
	var returnTy *TypeExpr
	// Optional parens on the same line
	if p.curToken.Type == tokenLParen && p.curToken.Pos.Line == pos.Line {
		if p.peekToken.Type == tokenRParen {
			p.nextToken() // consume ')'
			p.nextToken()
		} else {
			p.nextToken()
			params = p.parseParams()
			if !p.expectPeek(tokenRParen) {
				return nil
			}
			p.nextToken()
		}
	}
	if p.curToken.Type == tokenArrow {
		p.nextToken()
		returnTy = p.parseTypeExpr()
		if returnTy == nil {
			return nil
		}
		p.nextToken()
	}
	body := []Statement{}
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()
	for p.curToken.Type != tokenEnd && p.curToken.Type != tokenEOF {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
		p.nextToken()
	}

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	private := false
	if p.insideClass && p.privateNext {
		private = true
		p.privateNext = false
	}

	return &FunctionStmt{Name: name, Params: params, ReturnTy: returnTy, Body: body, IsClassMethod: isClassMethod, Private: private, position: pos}
}
