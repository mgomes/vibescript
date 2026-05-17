package parser

import (
	"strconv"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseIdentifier() ast.Expression {
	return &ast.Identifier{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseIntegerLiteral() ast.Expression {
	value, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid integer literal")
		return nil
	}
	return &ast.IntegerLiteral{Value: value, Position: p.curToken.Pos}
}

func (p *parser) parseFloatLiteral() ast.Expression {
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid float literal")
		return nil
	}
	return &ast.FloatLiteral{Value: value, Position: p.curToken.Pos}
}

func (p *parser) parseStringLiteral() ast.Expression {
	return &ast.StringLiteral{Value: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseBooleanLiteral() ast.Expression {
	return &ast.BoolLiteral{Value: p.curToken.Type == ast.TokenTrue, Position: p.curToken.Pos}
}

func (p *parser) parseNilLiteral() ast.Expression {
	return &ast.NilLiteral{Position: p.curToken.Pos}
}

func (p *parser) parseSymbolLiteral() ast.Expression {
	return &ast.SymbolLiteral{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseIvarLiteral() ast.Expression {
	if p.curToken.Literal == "" {
		p.errorExpected(p.curToken, "instance variable name")
		return nil
	}
	return &ast.IvarExpr{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseClassVarLiteral() ast.Expression {
	if p.curToken.Literal == "" {
		p.errorExpected(p.curToken, "class variable name")
		return nil
	}
	return &ast.ClassVarExpr{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseSelfLiteral() ast.Expression {
	return &ast.Identifier{Name: "self", Position: p.curToken.Pos}
}
