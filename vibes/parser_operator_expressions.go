package vibes

func (p *parser) parseGroupedExpression() Expression {
	p.nextToken()
	expr := p.parseExpression(lowestPrec)
	if !p.expectPeek(tokenRParen) {
		return nil
	}
	return expr
}

func (p *parser) parsePrefixExpression() Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	p.nextToken()
	right := p.parseExpression(precPrefix)
	return &UnaryExpr{Operator: operator, Right: right, position: pos}
}

func (p *parser) parseInfixExpression(left Expression) Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	return &BinaryExpr{Left: left, Operator: operator, Right: right, position: pos}
}

func (p *parser) parseRangeExpression(left Expression) Expression {
	pos := p.curToken.Pos
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	return &RangeExpr{Start: left, End: right, position: pos}
}
