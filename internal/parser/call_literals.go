package parser

import (
	"fmt"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseCallExpression(function ast.Expression) ast.Expression {
	if function == nil {
		return nil
	}
	expr := &ast.CallExpr{Callee: function, Position: function.Pos()}
	args := []ast.Expression{}
	kwargs := []ast.KeywordArg{}

	if p.peekToken.Type == ast.TokenRParen {
		p.nextToken()
		expr.Args = args
		expr.KwArgs = kwargs
		return expr
	}

	p.nextToken()
	p.parseCallArgument(&args, &kwargs)

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		p.parseCallArgument(&args, &kwargs)
	}

	if !p.expectPeek(ast.TokenRParen) {
		return nil
	}

	expr.Args = args
	expr.KwArgs = kwargs
	if p.peekToken.Type == ast.TokenDo {
		p.nextToken()
		expr.Block = p.parseBlockLiteral()
	}
	return expr
}

func (p *parser) parseCallArgument(args *[]ast.Expression, kwargs *[]ast.KeywordArg) {
	if isLabelNameToken(p.curToken.Type) && p.peekToken.Type == ast.TokenColon {
		name := p.curToken.Literal
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == ast.TokenComma || p.curToken.Type == ast.TokenRParen {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for keyword argument %s", name))
			return
		}
		value := p.parseExpression(lowestPrec)
		if value == nil {
			return
		}
		*kwargs = append(*kwargs, ast.KeywordArg{Name: name, Value: value})
		return
	}

	expr := p.parseExpression(lowestPrec)
	if expr != nil {
		*args = append(*args, expr)
	}
}

func isLabelNameToken(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenIdent,
		ast.TokenDef, ast.TokenClass, ast.TokenEnum, ast.TokenSelf, ast.TokenPrivate, ast.TokenProperty, ast.TokenGetter, ast.TokenSetter,
		ast.TokenEnd, ast.TokenReturn, ast.TokenYield, ast.TokenDo, ast.TokenFor, ast.TokenWhile, ast.TokenUntil,
		ast.TokenBreak, ast.TokenNext, ast.TokenIn, ast.TokenIf, ast.TokenCase, ast.TokenWhen, ast.TokenElsif, ast.TokenElse,
		ast.TokenTrue, ast.TokenFalse, ast.TokenNil:
		return true
	default:
		return false
	}
}
