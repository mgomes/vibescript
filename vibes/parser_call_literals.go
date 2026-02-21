package vibes

import "fmt"

func (p *parser) parseCallExpression(function Expression) Expression {
	if function == nil {
		return nil
	}
	expr := &CallExpr{Callee: function, position: function.Pos()}
	args := []Expression{}
	kwargs := []KeywordArg{}

	if p.peekToken.Type == tokenRParen {
		p.nextToken()
		expr.Args = args
		expr.KwArgs = kwargs
		return expr
	}

	p.nextToken()
	p.parseCallArgument(&args, &kwargs)

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		p.parseCallArgument(&args, &kwargs)
	}

	if !p.expectPeek(tokenRParen) {
		return nil
	}

	expr.Args = args
	expr.KwArgs = kwargs
	if p.peekToken.Type == tokenDo {
		p.nextToken()
		expr.Block = p.parseBlockLiteral()
	}
	return expr
}

func (p *parser) parseCallArgument(args *[]Expression, kwargs *[]KeywordArg) {
	if isLabelNameToken(p.curToken.Type) && p.peekToken.Type == tokenColon {
		name := p.curToken.Literal
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == tokenComma || p.curToken.Type == tokenRParen {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for keyword argument %s", name))
			return
		}
		value := p.parseExpression(lowestPrec)
		if value == nil {
			return
		}
		*kwargs = append(*kwargs, KeywordArg{Name: name, Value: value})
		return
	}

	expr := p.parseExpression(lowestPrec)
	if expr != nil {
		*args = append(*args, expr)
	}
}

func isLabelNameToken(tt TokenType) bool {
	switch tt {
	case tokenIdent,
		tokenDef, tokenClass, tokenSelf, tokenPrivate, tokenProperty, tokenGetter, tokenSetter,
		tokenEnd, tokenReturn, tokenYield, tokenDo, tokenFor, tokenWhile, tokenUntil,
		tokenBreak, tokenNext, tokenIn, tokenIf, tokenCase, tokenWhen, tokenElsif, tokenElse,
		tokenTrue, tokenFalse, tokenNil:
		return true
	default:
		return false
	}
}
