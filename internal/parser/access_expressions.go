package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseMemberExpression(object ast.Expression) ast.Expression {
	if object == nil {
		return nil
	}
	p.nextToken()
	if !isMemberNameToken(p.curToken.Type) {
		p.errorExpected(p.curToken, "member name")
		return nil
	}
	return &ast.MemberExpr{Object: object, Property: p.curToken.Literal, Position: object.Pos()}
}

func (p *parser) parseScopeExpression(object ast.Expression) ast.Expression {
	if object == nil {
		return nil
	}
	p.nextToken()
	if p.curToken.Type != ast.TokenIdent && p.curToken.Type != ast.TokenEnum {
		p.errorExpected(p.curToken, "identifier")
		return nil
	}
	return &ast.ScopeExpr{Object: object, Property: p.curToken.Literal, Position: object.Pos()}
}

func (p *parser) parseIndexExpression(object ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	p.nextToken()
	index := p.parseExpression(lowestPrec)
	if index == nil {
		return nil
	}
	if !p.expectPeek(ast.TokenRBracket) {
		return nil
	}
	return &ast.IndexExpr{Object: object, Index: index, Position: pos}
}

func isMemberNameToken(tt ast.TokenType) bool {
	if isLabelNameToken(tt) {
		return true
	}
	switch tt {
	case ast.TokenExport, ast.TokenBegin, ast.TokenRescue, ast.TokenEnsure, ast.TokenRaise:
		return true
	default:
		return false
	}
}
