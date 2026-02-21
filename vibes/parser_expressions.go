package vibes

func (p *parser) parseExpression(precedence int) Expression {
	prefix := p.prefixFns[p.curToken.Type]
	if prefix == nil {
		p.errorUnexpected(p.curToken)
		return nil
	}

	left := prefix()
	if left == nil {
		return nil
	}

	for p.peekToken.Type != tokenEOF && precedence < p.peekPrecedence() {
		infix := p.infixFns[p.peekToken.Type]
		if infix == nil {
			return left
		}
		p.nextToken()
		left = infix(left)
		if left == nil {
			return nil
		}
	}

	return left
}

func (p *parser) parseYieldExpression() Expression {
	pos := p.curToken.Pos
	var args []Expression
	if p.peekToken.Type == tokenLParen {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != tokenRParen {
			args = append(args, p.parseExpression(lowestPrec))
			for p.peekToken.Type == tokenComma {
				p.nextToken()
				p.nextToken()
				args = append(args, p.parseExpression(lowestPrec))
			}
			if !p.expectPeek(tokenRParen) {
				return nil
			}
		}
	} else if p.prefixFns[p.peekToken.Type] != nil {
		p.nextToken()
		args = append(args, p.parseExpression(lowestPrec))
	}
	return &YieldExpr{Args: args, position: pos}
}

func (p *parser) parseCaseExpression() Expression {
	pos := p.curToken.Pos
	p.nextToken()
	target := p.parseExpression(lowestPrec)
	if target == nil {
		return nil
	}

	p.nextToken()
	clauses := []CaseWhenClause{}
	for p.curToken.Type == tokenWhen {
		p.nextToken()
		values := []Expression{}
		first := p.parseExpression(lowestPrec)
		if first == nil {
			return nil
		}
		values = append(values, first)
		for p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			value := p.parseExpression(lowestPrec)
			if value == nil {
				return nil
			}
			values = append(values, value)
		}

		p.nextToken()
		result := p.parseExpressionWithBlock()
		if result == nil {
			return nil
		}
		clauses = append(clauses, CaseWhenClause{Values: values, Result: result})
		p.nextToken()
	}

	if len(clauses) == 0 {
		p.errorExpected(p.curToken, "when")
		return nil
	}

	var elseExpr Expression
	if p.curToken.Type == tokenElse {
		p.nextToken()
		elseExpr = p.parseExpressionWithBlock()
		if elseExpr == nil {
			return nil
		}
		p.nextToken()
	}

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
		return nil
	}

	return &CaseExpr{Target: target, Clauses: clauses, ElseExpr: elseExpr, position: pos}
}

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

func (p *parser) parseMemberExpression(object Expression) Expression {
	if object == nil {
		return nil
	}
	p.nextToken()
	return &MemberExpr{Object: object, Property: p.curToken.Literal, position: object.Pos()}
}

func (p *parser) parseIndexExpression(object Expression) Expression {
	pos := p.curToken.Pos
	p.nextToken()
	index := p.parseExpression(lowestPrec)
	if !p.expectPeek(tokenRBracket) {
		return nil
	}
	return &IndexExpr{Object: object, Index: index, position: pos}
}
