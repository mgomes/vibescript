package vibes

func (p *parser) parseStatement() Statement {
	switch p.curToken.Type {
	case tokenDef:
		return p.parseFunctionStatement()
	case tokenClass:
		return p.parseClassStatement()
	case tokenExport:
		return p.parseExportStatement()
	case tokenPrivate:
		return p.parsePrivateStatement()
	case tokenReturn:
		return p.parseReturnStatement()
	case tokenRaise:
		return p.parseRaiseStatement()
	case tokenIf:
		return p.parseIfStatement()
	case tokenFor:
		return p.parseForStatement()
	case tokenWhile:
		return p.parseWhileStatement()
	case tokenUntil:
		return p.parseUntilStatement()
	case tokenBreak:
		return p.parseBreakStatement()
	case tokenNext:
		return p.parseNextStatement()
	case tokenBegin:
		return p.parseBeginStatement()
	case tokenIdent:
		if p.curToken.Literal == "assert" {
			return p.parseAssertStatement()
		}
		return p.parseExpressionOrAssignStatement()
	default:
		return p.parseExpressionOrAssignStatement()
	}
}

func (p *parser) parseReturnStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	value := p.parseExpression(lowestPrec)
	return &ReturnStmt{Value: value, position: pos}
}

func (p *parser) parseRaiseStatement() Statement {
	pos := p.curToken.Pos
	if p.peekToken.Type == tokenEOF || p.peekToken.Type == tokenEnd || p.peekToken.Type == tokenEnsure || p.peekToken.Type == tokenRescue || p.peekToken.Pos.Line != pos.Line {
		return &RaiseStmt{position: pos}
	}
	p.nextToken()
	value := p.parseExpression(lowestPrec)
	if value == nil {
		return nil
	}
	return &RaiseStmt{Value: value, position: pos}
}

func (p *parser) parseBlock(stop ...TokenType) []Statement {
	stmts := []Statement{}
	stopSet := make(map[TokenType]struct{}, len(stop))
	for _, tt := range stop {
		stopSet[tt] = struct{}{}
	}
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()

	for {
		if _, ok := stopSet[p.curToken.Type]; ok || p.curToken.Type == tokenEOF {
			return stmts
		}
		stmt := p.parseStatement()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		p.nextToken()
	}
}
