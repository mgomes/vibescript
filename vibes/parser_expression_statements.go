package vibes

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
