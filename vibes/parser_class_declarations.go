package vibes

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
