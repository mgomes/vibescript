package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseYieldExpression() ast.Expression {
	pos := p.curToken.Pos
	var args []ast.Expression
	if p.peekToken.Type == ast.TokenLParen {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != ast.TokenRParen {
			args = append(args, p.parseExpression(lowestPrec))
			for p.peekToken.Type == ast.TokenComma {
				p.nextToken()
				p.nextToken()
				args = append(args, p.parseExpression(lowestPrec))
			}
			if !p.expectPeek(ast.TokenRParen) {
				return nil
			}
		}
	} else if p.peekToken.Pos.Line == pos.Line && p.prefixFns[p.peekToken.Type] != nil {
		p.nextToken()
		args = append(args, p.parseExpression(lowestPrec))
	}
	return &ast.YieldExpr{Args: args, Position: pos}
}
