package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseCaseExpression() ast.Expression {
	pos := p.curToken.Pos
	p.nextToken()
	target := p.parseExpression(lowestPrec)
	if target == nil {
		return nil
	}

	p.nextToken()
	clauses := []ast.CaseWhenClause{}
	for p.curToken.Type == ast.TokenWhen {
		p.nextToken()
		values := []ast.Expression{}
		first := p.parseExpression(lowestPrec)
		if first == nil {
			return nil
		}
		values = append(values, first)
		for p.peekToken.Type == ast.TokenComma {
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
		clauses = append(clauses, ast.CaseWhenClause{Values: values, Result: result})
		p.nextToken()
	}

	if len(clauses) == 0 {
		p.errorExpected(p.curToken, "when")
		return nil
	}

	var elseExpr ast.Expression
	if p.curToken.Type == ast.TokenElse {
		p.nextToken()
		elseExpr = p.parseExpressionWithBlock()
		if elseExpr == nil {
			return nil
		}
		p.nextToken()
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
		return nil
	}

	return &ast.CaseExpr{Target: target, Clauses: clauses, ElseExpr: elseExpr, Position: pos}
}
