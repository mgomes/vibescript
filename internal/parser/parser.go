package parser

import (
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes/source"
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
	return newParser(source).parseProgram()
}

func (p *parser) parseProgram() (*ast.Program, []error) {
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

const (
	lowestPrec = iota
	precAssign
	precOr
	precAnd
	precEquality
	precComparison
	precRange
	precSum
	precProduct
	precPrefix
	precCall
)

var precedences = map[ast.TokenType]int{
	ast.TokenOr:       precOr,
	ast.TokenAnd:      precAnd,
	ast.TokenEQ:       precEquality,
	ast.TokenNotEQ:    precEquality,
	ast.TokenLT:       precComparison,
	ast.TokenLTE:      precComparison,
	ast.TokenGT:       precComparison,
	ast.TokenGTE:      precComparison,
	ast.TokenRange:    precRange,
	ast.TokenPlus:     precSum,
	ast.TokenMinus:    precSum,
	ast.TokenSlash:    precProduct,
	ast.TokenAsterisk: precProduct,
	ast.TokenPercent:  precProduct,
	ast.TokenLParen:   precCall,
	ast.TokenDot:      precCall,
	ast.TokenScope:    precCall,
	ast.TokenLBracket: precCall,
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

var _ error = (*parseError)(nil)

type parseError struct {
	pos    ast.Position
	msg    string
	source string
}

func (e *parseError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "parse error at %d:%d: %s", e.pos.Line, e.pos.Column, e.msg)
	if frame := source.FormatCodeFrame(e.source, e.pos); frame != "" {
		b.WriteString("\n")
		b.WriteString(frame)
	}
	return b.String()
}

func (p *parser) errorExpected(tok ast.Token, expected string) {
	p.addParseError(tok.Pos, fmt.Sprintf("expected %s, got %s", expected, tokenLabel(tok.Type)))
}

func (p *parser) errorUnexpected(tok ast.Token) {
	p.addParseError(tok.Pos, fmt.Sprintf("unexpected token %s", tokenLabel(tok.Type)))
}

func (p *parser) addParseError(pos ast.Position, msg string) {
	p.errors = append(p.errors, &parseError{pos: pos, msg: msg, source: p.l.input})
}

func tokenLabel(tt ast.TokenType) string {
	switch tt {
	case ast.TokenIllegal:
		return "invalid token"
	case ast.TokenEOF:
		return "end of input"
	case ast.TokenIdent:
		return "identifier"
	case ast.TokenInt:
		return "integer"
	case ast.TokenFloat:
		return "float"
	case ast.TokenString:
		return "string"
	case ast.TokenSymbol:
		return "symbol"
	case ast.TokenIvar:
		return "instance variable"
	case ast.TokenClassVar:
		return "class variable"
	case ast.TokenDef:
		return "'def'"
	case ast.TokenClass:
		return "'class'"
	case ast.TokenEnum:
		return "'enum'"
	case ast.TokenExport:
		return "'export'"
	case ast.TokenSelf:
		return "'self'"
	case ast.TokenPrivate:
		return "'private'"
	case ast.TokenProperty:
		return "'property'"
	case ast.TokenGetter:
		return "'getter'"
	case ast.TokenSetter:
		return "'setter'"
	case ast.TokenEnd:
		return "'end'"
	case ast.TokenRaise:
		return "'raise'"
	case ast.TokenReturn:
		return "'return'"
	case ast.TokenYield:
		return "'yield'"
	case ast.TokenDo:
		return "'do'"
	case ast.TokenFor:
		return "'for'"
	case ast.TokenIn:
		return "'in'"
	case ast.TokenIf:
		return "'if'"
	case ast.TokenElsif:
		return "'elsif'"
	case ast.TokenElse:
		return "'else'"
	case ast.TokenTrue:
		return "'true'"
	case ast.TokenFalse:
		return "'false'"
	case ast.TokenNil:
		return "'nil'"
	default:
		if len(tt) == 1 || strings.HasPrefix(string(tt), "<") || strings.HasPrefix(string(tt), ">") {
			return fmt.Sprintf("%q", string(tt))
		}
		return fmt.Sprintf("%q", strings.ToLower(string(tt)))
	}
}
