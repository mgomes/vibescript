package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseGroupedExpression() ast.Expression {
	p.nextToken()
	expr := p.parseExpression(lowestPrec)
	if !p.expectPeek(ast.TokenRParen) {
		return nil
	}
	return expr
}

func (p *parser) parsePrefixExpression() ast.Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	p.nextToken()
	right := p.parseExpression(precPrefix)
	if right == nil {
		return nil
	}
	return &ast.UnaryExpr{Operator: operator, Right: right, Position: pos}
}

func (p *parser) parseInfixExpression(left ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	if right == nil {
		return nil
	}
	return &ast.BinaryExpr{Left: left, Operator: operator, Right: right, Position: pos}
}

func (p *parser) parseRangeExpression(left ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	if right == nil {
		return nil
	}
	return &ast.RangeExpr{Start: left, End: right, Position: pos}
}
