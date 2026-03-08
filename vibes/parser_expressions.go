package vibes

func (p *parser) parseExpression(precedence int) Expression {
	if p.lineLimitedExprs > 0 {
		return p.parseExpressionWithLineLimit(precedence, p.curToken.Pos.Line, true)
	}
	return p.parseExpressionWithLineLimit(precedence, 0, false)
}

func (p *parser) parseLineExpression(precedence int) Expression {
	p.lineLimitedExprs++
	defer func() {
		p.lineLimitedExprs--
	}()
	return p.parseExpression(precedence)
}

func (p *parser) parseExpressionWithLineLimit(precedence int, limitLine int, lineLimited bool) Expression {
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
		if lineLimited && p.peekToken.Pos.Line > limitLine && !lineLimitedContinuationToken(p.peekToken.Type) {
			return left
		}
		infix := p.infixFns[p.peekToken.Type]
		if infix == nil {
			return left
		}
		p.nextToken()
		left = infix(left)
		if left == nil {
			return nil
		}
		if lineLimited {
			limitLine = p.curToken.Pos.Line
		}
	}

	return left
}

func lineLimitedContinuationToken(tt TokenType) bool {
	switch tt {
	case tokenDot, tokenScope, tokenPlus, tokenSlash, tokenAsterisk, tokenPercent, tokenRange, tokenEQ, tokenNotEQ, tokenLT, tokenLTE, tokenGT, tokenGTE, tokenAnd, tokenOr:
		return true
	default:
		return false
	}
}
