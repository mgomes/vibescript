// Package parser contains the Vibescript lexer and parser. It produces
// internal/ast trees consumed by the runtime. It is an internal
// package and is not part of the supported public API.
package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

type (
	prefixParseFn func() ast.Expression
	infixParseFn  func(ast.Expression) ast.Expression
)

type parser struct {
	l *lexer

	curToken  ast.Token
	peekToken ast.Token

	errors []error

	prefixFns map[ast.TokenType]prefixParseFn
	infixFns  map[ast.TokenType]infixParseFn

	insideClass      bool
	privateNext      bool
	lineLimitedExprs int
	statementNesting int
}

func newParser(input string) *parser {
	l := newLexer(input)
	p := &parser{l: l}

	p.prefixFns = make(map[ast.TokenType]prefixParseFn)
	p.infixFns = make(map[ast.TokenType]infixParseFn)

	p.registerPrefix(ast.TokenIdent, p.parseIdentifier)
	p.registerPrefix(ast.TokenInt, p.parseIntegerLiteral)
	p.registerPrefix(ast.TokenFloat, p.parseFloatLiteral)
	p.registerPrefix(ast.TokenString, p.parseStringLiteral)
	p.registerPrefix(ast.TokenTrue, p.parseBooleanLiteral)
	p.registerPrefix(ast.TokenFalse, p.parseBooleanLiteral)
	p.registerPrefix(ast.TokenNil, p.parseNilLiteral)
	p.registerPrefix(ast.TokenSymbol, p.parseSymbolLiteral)
	p.registerPrefix(ast.TokenIvar, p.parseIvarLiteral)
	p.registerPrefix(ast.TokenClassVar, p.parseClassVarLiteral)
	p.registerPrefix(ast.TokenSelf, p.parseSelfLiteral)
	p.registerPrefix(ast.TokenLParen, p.parseGroupedExpression)
	p.registerPrefix(ast.TokenLBracket, p.parseArrayLiteral)
	p.registerPrefix(ast.TokenLBrace, p.parseHashLiteral)
	p.registerPrefix(ast.TokenBang, p.parsePrefixExpression)
	p.registerPrefix(ast.TokenMinus, p.parsePrefixExpression)
	p.registerPrefix(ast.TokenYield, p.parseYieldExpression)
	p.registerPrefix(ast.TokenCase, p.parseCaseExpression)

	p.infixFns[ast.TokenPlus] = p.parseInfixExpression
	p.infixFns[ast.TokenMinus] = p.parseInfixExpression
	p.infixFns[ast.TokenSlash] = p.parseInfixExpression
	p.infixFns[ast.TokenAsterisk] = p.parseInfixExpression
	p.infixFns[ast.TokenPercent] = p.parseInfixExpression
	p.infixFns[ast.TokenRange] = p.parseRangeExpression
	p.infixFns[ast.TokenEQ] = p.parseInfixExpression
	p.infixFns[ast.TokenNotEQ] = p.parseInfixExpression
	p.infixFns[ast.TokenLT] = p.parseInfixExpression
	p.infixFns[ast.TokenLTE] = p.parseInfixExpression
	p.infixFns[ast.TokenGT] = p.parseInfixExpression
	p.infixFns[ast.TokenGTE] = p.parseInfixExpression
	p.infixFns[ast.TokenAnd] = p.parseInfixExpression
	p.infixFns[ast.TokenOr] = p.parseInfixExpression
	p.infixFns[ast.TokenLParen] = p.parseCallExpression
	p.infixFns[ast.TokenDot] = p.parseMemberExpression
	p.infixFns[ast.TokenScope] = p.parseScopeExpression
	p.infixFns[ast.TokenLBracket] = p.parseIndexExpression

	p.nextToken()
	p.nextToken()

	return p
}

func (p *parser) registerPrefix(tt ast.TokenType, fn prefixParseFn) {
	p.prefixFns[tt] = fn
}

func (p *parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

// Parse lexes and parses the given source text and returns the
// resulting AST together with any parse errors encountered. It is the
// stable entry point used by callers within the module.
func Parse(source string) (*ast.Program, []error) {
	return newParser(source).ParseProgram()
}

func (p *parser) ParseProgram() (*ast.Program, []error) {
	program := &ast.Program{}

	for p.curToken.Type != ast.TokenEOF {
		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}
		p.nextToken()
	}

	return program, p.errors
}

func (p *parser) curPrecedence() int {
	if prec, ok := precedences[p.curToken.Type]; ok {
		return prec
	}
	return lowestPrec
}

func (p *parser) peekPrecedence() int {
	if prec, ok := precedences[p.peekToken.Type]; ok {
		return prec
	}
	return lowestPrec
}

func (p *parser) expectPeek(tt ast.TokenType) bool {
	if p.peekToken.Type == tt {
		p.nextToken()
		return true
	}
	p.errorExpected(p.peekToken, tokenLabel(tt))
	return false
}
