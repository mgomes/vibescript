package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseExpressionOrAssignStatement() ast.Statement {
	expr := p.parseExpression(lowestPrec)
	if expr == nil {
		return nil
	}

	if p.peekToken.Type == ast.TokenDo {
		p.nextToken()
		block := p.parseBlockLiteral()
		var call *ast.CallExpr
		if existing, ok := expr.(*ast.CallExpr); ok {
			call = existing
		} else {
			call = &ast.CallExpr{Callee: expr, Position: expr.Pos()}
		}
		call.Block = block
		expr = call
	}

	if p.peekToken.Type == ast.TokenAssign && isAssignable(expr) {
		pos := expr.Pos()
		p.nextToken()
		p.nextToken()
		value := p.parseExpressionWithBlock()
		return &ast.AssignStmt{Target: expr, Value: value, Position: pos}
	}

	return &ast.ExprStmt{Expr: expr, Position: expr.Pos()}
}

func (p *parser) parseExpressionWithBlock() ast.Expression {
	expr := p.parseExpression(lowestPrec)
	if expr == nil {
		return nil
	}
	if p.peekToken.Type == ast.TokenDo {
		p.nextToken()
		block := p.parseBlockLiteral()
		var call *ast.CallExpr
		if existing, ok := expr.(*ast.CallExpr); ok {
			call = existing
		} else {
			call = &ast.CallExpr{Callee: expr, Position: expr.Pos()}
		}
		call.Block = block
		return call
	}
	return expr
}

func (p *parser) parseAssertStatement() ast.Statement {
	pos := p.curToken.Pos
	callee := &ast.Identifier{Name: p.curToken.Literal, Position: pos}
	args := []ast.Expression{}
	p.nextToken()
	if p.curToken.Type == ast.TokenEOF || p.curToken.Type == ast.TokenEnd {
		return &ast.ExprStmt{Expr: callee, Position: pos}
	}
	first := p.parseExpression(lowestPrec)
	if first != nil {
		args = append(args, first)
		for p.peekToken.Type == ast.TokenComma {
			p.nextToken()
			p.nextToken()
			args = append(args, p.parseExpression(lowestPrec))
		}
	}
	call := &ast.CallExpr{Callee: callee, Args: args, Position: pos}
	return &ast.ExprStmt{Expr: call, Position: pos}
}
