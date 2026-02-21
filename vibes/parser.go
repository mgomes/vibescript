package vibes

import (
	"fmt"
	"strings"
)

type parseError struct {
	pos    Position
	msg    string
	source string
}

func (e *parseError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "parse error at %d:%d: %s", e.pos.Line, e.pos.Column, e.msg)
	if frame := formatCodeFrame(e.source, e.pos); frame != "" {
		b.WriteString("\n")
		b.WriteString(frame)
	}
	return b.String()
}

type (
	prefixParseFn func() Expression
	infixParseFn  func(Expression) Expression
)

type parser struct {
	l *lexer

	curToken  Token
	peekToken Token

	errors []error

	prefixFns map[TokenType]prefixParseFn
	infixFns  map[TokenType]infixParseFn

	insideClass      bool
	privateNext      bool
	statementNesting int
}

func newParser(input string) *parser {
	l := newLexer(input)
	p := &parser{l: l}

	p.prefixFns = make(map[TokenType]prefixParseFn)
	p.infixFns = make(map[TokenType]infixParseFn)

	p.registerPrefix(tokenIdent, p.parseIdentifier)
	p.registerPrefix(tokenInt, p.parseIntegerLiteral)
	p.registerPrefix(tokenFloat, p.parseFloatLiteral)
	p.registerPrefix(tokenString, p.parseStringLiteral)
	p.registerPrefix(tokenTrue, p.parseBooleanLiteral)
	p.registerPrefix(tokenFalse, p.parseBooleanLiteral)
	p.registerPrefix(tokenNil, p.parseNilLiteral)
	p.registerPrefix(tokenSymbol, p.parseSymbolLiteral)
	p.registerPrefix(tokenIvar, p.parseIvarLiteral)
	p.registerPrefix(tokenClassVar, p.parseClassVarLiteral)
	p.registerPrefix(tokenSelf, p.parseSelfLiteral)
	p.registerPrefix(tokenLParen, p.parseGroupedExpression)
	p.registerPrefix(tokenLBracket, p.parseArrayLiteral)
	p.registerPrefix(tokenLBrace, p.parseHashLiteral)
	p.registerPrefix(tokenBang, p.parsePrefixExpression)
	p.registerPrefix(tokenMinus, p.parsePrefixExpression)
	p.registerPrefix(tokenYield, p.parseYieldExpression)
	p.registerPrefix(tokenCase, p.parseCaseExpression)

	p.infixFns[tokenPlus] = p.parseInfixExpression
	p.infixFns[tokenMinus] = p.parseInfixExpression
	p.infixFns[tokenSlash] = p.parseInfixExpression
	p.infixFns[tokenAsterisk] = p.parseInfixExpression
	p.infixFns[tokenPercent] = p.parseInfixExpression
	p.infixFns[tokenRange] = p.parseRangeExpression
	p.infixFns[tokenEQ] = p.parseInfixExpression
	p.infixFns[tokenNotEQ] = p.parseInfixExpression
	p.infixFns[tokenLT] = p.parseInfixExpression
	p.infixFns[tokenLTE] = p.parseInfixExpression
	p.infixFns[tokenGT] = p.parseInfixExpression
	p.infixFns[tokenGTE] = p.parseInfixExpression
	p.infixFns[tokenAnd] = p.parseInfixExpression
	p.infixFns[tokenOr] = p.parseInfixExpression
	p.infixFns[tokenLParen] = p.parseCallExpression
	p.infixFns[tokenDot] = p.parseMemberExpression
	p.infixFns[tokenLBracket] = p.parseIndexExpression

	p.nextToken()
	p.nextToken()

	return p
}

func (p *parser) registerPrefix(tt TokenType, fn prefixParseFn) {
	p.prefixFns[tt] = fn
}

func (p *parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.l.NextToken()
}

func (p *parser) ParseProgram() (*Program, []error) {
	program := &Program{}

	for p.curToken.Type != tokenEOF {
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

func (p *parser) expectPeek(tt TokenType) bool {
	if p.peekToken.Type == tt {
		p.nextToken()
		return true
	}
	p.errorExpected(p.peekToken, tokenLabel(tt))
	return false
}

func (p *parser) errorExpected(tok Token, expected string) {
	p.addParseError(tok.Pos, fmt.Sprintf("expected %s, got %s", expected, tokenLabel(tok.Type)))
}

func (p *parser) errorUnexpected(tok Token) {
	p.addParseError(tok.Pos, fmt.Sprintf("unexpected token %s", tokenLabel(tok.Type)))
}

func (p *parser) addParseError(pos Position, msg string) {
	p.errors = append(p.errors, &parseError{pos: pos, msg: msg, source: p.l.input})
}

func tokenLabel(tt TokenType) string {
	switch tt {
	case tokenIllegal:
		return "invalid token"
	case tokenEOF:
		return "end of input"
	case tokenIdent:
		return "identifier"
	case tokenInt:
		return "integer"
	case tokenFloat:
		return "float"
	case tokenString:
		return "string"
	case tokenSymbol:
		return "symbol"
	case tokenIvar:
		return "instance variable"
	case tokenClassVar:
		return "class variable"
	case tokenDef:
		return "'def'"
	case tokenClass:
		return "'class'"
	case tokenExport:
		return "'export'"
	case tokenSelf:
		return "'self'"
	case tokenPrivate:
		return "'private'"
	case tokenProperty:
		return "'property'"
	case tokenGetter:
		return "'getter'"
	case tokenSetter:
		return "'setter'"
	case tokenEnd:
		return "'end'"
	case tokenRaise:
		return "'raise'"
	case tokenReturn:
		return "'return'"
	case tokenYield:
		return "'yield'"
	case tokenDo:
		return "'do'"
	case tokenFor:
		return "'for'"
	case tokenIn:
		return "'in'"
	case tokenIf:
		return "'if'"
	case tokenElsif:
		return "'elsif'"
	case tokenElse:
		return "'else'"
	case tokenTrue:
		return "'true'"
	case tokenFalse:
		return "'false'"
	case tokenNil:
		return "'nil'"
	default:
		if len(tt) == 1 || strings.HasPrefix(string(tt), "<") || strings.HasPrefix(string(tt), ">") {
			return fmt.Sprintf("%q", string(tt))
		}
		return fmt.Sprintf("%q", strings.ToLower(string(tt)))
	}
}
