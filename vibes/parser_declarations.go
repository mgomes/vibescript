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

func (p *parser) parseExportStatement() Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "export is only supported for top-level functions")
		return nil
	}
	if !p.expectPeek(tokenDef) {
		return nil
	}
	fnStmt := p.parseFunctionStatement()
	if fnStmt == nil {
		return nil
	}
	fn, ok := fnStmt.(*FunctionStmt)
	if !ok {
		p.addParseError(pos, "export expects a function definition")
		return nil
	}
	if fn.IsClassMethod {
		p.addParseError(pos, "export cannot be used with class methods")
		return nil
	}
	fn.Exported = true
	return fn
}

func (p *parser) parsePrivateStatement() Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "private is only supported for top-level functions and class methods")
		return nil
	}
	if !p.expectPeek(tokenDef) {
		return nil
	}
	fnStmt := p.parseFunctionStatement()
	if fnStmt == nil {
		return nil
	}
	fn, ok := fnStmt.(*FunctionStmt)
	if !ok {
		p.addParseError(pos, "private expects a function definition")
		return nil
	}
	if fn.IsClassMethod {
		p.addParseError(pos, "private cannot be used with class methods")
		return nil
	}
	fn.Private = true
	return fn
}

func (p *parser) parseClassStatement() Statement {
	pos := p.curToken.Pos
	if !p.expectPeek(tokenIdent) {
		return nil
	}
	name := p.curToken.Literal
	p.nextToken()

	stmt := &ClassStmt{
		Name:     name,
		position: pos,
	}

	prevInside := p.insideClass
	prevPrivate := p.privateNext
	p.insideClass = true
	p.privateNext = false
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()

	for p.curToken.Type != tokenEnd && p.curToken.Type != tokenEOF {
		switch p.curToken.Type {
		case tokenDef:
			fnStmt := p.parseFunctionStatement()
			if fnStmt == nil {
				return nil
			}
			fn := fnStmt.(*FunctionStmt)
			if fn.IsClassMethod {
				stmt.ClassMethods = append(stmt.ClassMethods, fn)
			} else {
				stmt.Methods = append(stmt.Methods, fn)
			}
		case tokenPrivate:
			if p.peekToken.Type == tokenDef {
				p.privateNext = true
				p.nextToken()
				continue
			}
			p.privateNext = true
		case tokenProperty, tokenGetter, tokenSetter:
			decl := p.parsePropertyDecl(p.curToken.Type)
			stmt.Properties = append(stmt.Properties, decl)
		default:
			s := p.parseStatement()
			if s != nil {
				stmt.Body = append(stmt.Body, s)
			}
		}
		p.nextToken()
	}

	p.insideClass = prevInside
	p.privateNext = prevPrivate

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return stmt
}
