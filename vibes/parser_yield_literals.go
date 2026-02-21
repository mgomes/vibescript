package vibes

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
