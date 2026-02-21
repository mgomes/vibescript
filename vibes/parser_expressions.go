package vibes

import (
	"fmt"
)

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

func (p *parser) parseArrayLiteral() Expression {
	pos := p.curToken.Pos
	elements := []Expression{}

	if p.peekToken.Type == tokenRBracket {
		p.nextToken()
		return &ArrayLiteral{Elements: elements, position: pos}
	}

	p.nextToken()
	elements = append(elements, p.parseExpression(lowestPrec))

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		elements = append(elements, p.parseExpression(lowestPrec))
	}

	if !p.expectPeek(tokenRBracket) {
		return nil
	}

	return &ArrayLiteral{Elements: elements, position: pos}
}

func (p *parser) parseHashLiteral() Expression {
	pos := p.curToken.Pos
	pairs := []HashPair{}

	if p.peekToken.Type == tokenRBrace {
		p.nextToken()
		return &HashLiteral{Pairs: pairs, position: pos}
	}

	p.nextToken()
	if pair := p.parseHashPair(); pair.Key != nil {
		pairs = append(pairs, pair)
	}

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		if pair := p.parseHashPair(); pair.Key != nil {
			pairs = append(pairs, pair)
		}
	}

	if !p.expectPeek(tokenRBrace) {
		return nil
	}

	return &HashLiteral{Pairs: pairs, position: pos}
}

func (p *parser) parseHashPair() HashPair {
	if !isLabelNameToken(p.curToken.Type) || p.peekToken.Type != tokenColon {
		p.addParseError(p.curToken.Pos, "invalid hash pair: expected symbol-style key like name:")
		return HashPair{}
	}

	key := &SymbolLiteral{Name: p.curToken.Literal, position: p.curToken.Pos}
	p.nextToken()
	p.nextToken()
	if p.curToken.Type == tokenComma || p.curToken.Type == tokenRBrace {
		p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for hash key %s", key.Name))
		return HashPair{}
	}

	value := p.parseExpression(lowestPrec)
	if value == nil {
		return HashPair{}
	}
	return HashPair{Key: key, Value: value}
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
