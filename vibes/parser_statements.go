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

func (p *parser) parseExpressionOrAssignStatement() Statement {
	expr := p.parseExpression(lowestPrec)
	if expr == nil {
		return nil
	}

	if p.peekToken.Type == tokenDo {
		p.nextToken()
		block := p.parseBlockLiteral()
		var call *CallExpr
		if existing, ok := expr.(*CallExpr); ok {
			call = existing
		} else {
			call = &CallExpr{Callee: expr, position: expr.Pos()}
		}
		call.Block = block
		expr = call
	}

	if p.peekToken.Type == tokenAssign && isAssignable(expr) {
		pos := expr.Pos()
		p.nextToken()
		p.nextToken()
		value := p.parseExpressionWithBlock()
		return &AssignStmt{Target: expr, Value: value, position: pos}
	}

	return &ExprStmt{Expr: expr, position: expr.Pos()}
}

func (p *parser) parseExpressionWithBlock() Expression {
	expr := p.parseExpression(lowestPrec)
	if expr == nil {
		return nil
	}
	if p.peekToken.Type == tokenDo {
		p.nextToken()
		block := p.parseBlockLiteral()
		var call *CallExpr
		if existing, ok := expr.(*CallExpr); ok {
			call = existing
		} else {
			call = &CallExpr{Callee: expr, position: expr.Pos()}
		}
		call.Block = block
		return call
	}
	return expr
}
func (p *parser) parseAssertStatement() Statement {
	pos := p.curToken.Pos
	callee := &Identifier{Name: p.curToken.Literal, position: pos}
	args := []Expression{}
	p.nextToken()
	if p.curToken.Type == tokenEOF || p.curToken.Type == tokenEnd {
		return &ExprStmt{Expr: callee, position: pos}
	}
	first := p.parseExpression(lowestPrec)
	if first != nil {
		args = append(args, first)
		for p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			args = append(args, p.parseExpression(lowestPrec))
		}
	}
	call := &CallExpr{Callee: callee, Args: args, position: pos}
	return &ExprStmt{Expr: call, position: pos}
}
