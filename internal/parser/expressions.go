package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseExpression(precedence int) ast.Expression {
	if p.lineLimitedExprs > 0 {
		return p.parseExpressionWithLineLimit(precedence, p.curToken.Pos.Line, true)
	}
	return p.parseExpressionWithLineLimit(precedence, 0, false)
}

func (p *parser) parseLineExpression(precedence int) ast.Expression {
	p.lineLimitedExprs++
	defer func() {
		p.lineLimitedExprs--
	}()
	return p.parseExpression(precedence)
}

func (p *parser) parseExpressionWithLineLimit(precedence, limitLine int, lineLimited bool) ast.Expression {
	prefix := p.prefixFns[p.curToken.Type]
	if prefix == nil {
		p.errorUnexpected(p.curToken)
		return nil
	}

	left := prefix()
	if left == nil {
		return nil
	}

	for p.peekToken.Type != ast.TokenEOF && precedence < p.peekPrecedence() {
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

func lineLimitedContinuationToken(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenDot, ast.TokenScope, ast.TokenPlus, ast.TokenSlash, ast.TokenAsterisk, ast.TokenPercent, ast.TokenRange, ast.TokenEQ, ast.TokenNotEQ, ast.TokenLT, ast.TokenLTE, ast.TokenGT, ast.TokenGTE, ast.TokenAnd, ast.TokenOr:
		return true
	default:
		return false
	}
}
