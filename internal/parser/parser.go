package parser

import (
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes/source"
)

type parser struct {
	l *lexer

	curToken  ast.Token
	peekToken ast.Token
	peekPeek  ast.Token

	errors []error

	insideClass      bool
	privateNext      bool
	lineLimitedExprs int
	lineLimitedStops []ast.TokenType
	statementNesting int
	typeDepth        int
	localScopes      []localScope
}

// localScope records the local names declared within a single lexical
// scope. funcDef marks scopes introduced by a function definition, which
// act as a lookup boundary: name resolution does not see locals declared
// in scopes enclosing a function. Block scopes leave funcDef false so
// they continue to close over their surrounding locals.
type localScope struct {
	names   map[string]struct{}
	funcDef bool
}

func newParser(input string) *parser {
	l := newLexer(input)
	p := &parser{l: l, localScopes: []localScope{{names: map[string]struct{}{}}}}

	p.nextToken()
	p.nextToken()
	p.nextToken()

	return p
}

func (p *parser) pushLocalScope(params []ast.Param, funcDef bool) {
	scope := localScope{names: map[string]struct{}{}, funcDef: funcDef}
	p.localScopes = append(p.localScopes, scope)
	for _, param := range params {
		p.declareParamLocal(param)
	}
}

func (p *parser) popLocalScope() {
	if len(p.localScopes) <= 1 {
		return
	}
	p.localScopes = p.localScopes[:len(p.localScopes)-1]
}

func (p *parser) declareParamLocal(param ast.Param) {
	if param.Target != nil {
		p.declareLocalTarget(param.Target)
		return
	}
	if param.Name != "" && !param.IsIvar {
		p.declareLocal(param.Name)
	}
}

func (p *parser) declareLocalTarget(target ast.Expression) {
	switch t := target.(type) {
	case *ast.Identifier:
		p.declareLocal(t.Name)
	case *ast.DestructureTarget:
		for _, element := range t.Elements {
			p.declareLocalTarget(element.Target)
		}
	}
}

func (p *parser) declareLocal(name string) {
	if len(p.localScopes) == 0 {
		p.localScopes = append(p.localScopes, localScope{names: map[string]struct{}{}})
	}
	p.localScopes[len(p.localScopes)-1].names[name] = struct{}{}
}

func (p *parser) isLocalName(name string) bool {
	for i := len(p.localScopes) - 1; i >= 0; i-- {
		if _, ok := p.localScopes[i].names[name]; ok {
			return true
		}
		// A function definition is a lookup boundary: locals declared in
		// scopes enclosing the function (including snippet top-level
		// locals) are not visible inside the function body.
		if p.localScopes[i].funcDef {
			break
		}
	}
	return false
}

func (p *parser) nextToken() {
	p.curToken = p.peekToken
	p.peekToken = p.peekPeek
	p.peekPeek = p.l.NextToken()
}

// reprimeAt repositions the lexer to resume scanning at the given byte
// offset and rebuilds the lookahead from there. It is used after the
// parser consumes a construct directly from source (such as a
// percent-array call argument) whose interior the lexer may have
// tokenized incorrectly while filling its lookahead.
//
// last is the synthetic token standing in for the consumed construct. It
// becomes curToken so the expression-parsing contract holds (curToken is
// left on the construct's final token, not the token after it) and the
// lexer's lastToken, so token-adjacency gating stays correct. The two
// following tokens are scanned fresh from offset.
func (p *parser) reprimeAt(offset int, last ast.Token) {
	p.l.seek(offset, last)
	p.curToken = last
	p.peekToken = p.l.NextToken()
	p.peekPeek = p.l.NextToken()
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
		p.skipStatementSeparators()
		if p.curToken.Type == ast.TokenEOF {
			break
		}
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
	precConditional
	precOr
	precAnd
	precEquality
	precComparison
	precRange
	precSum
	precProduct
	precPrefix
	precPower
	precCall
)

var precedences = map[ast.TokenType]int{
	ast.TokenQuestion:  precConditional,
	ast.TokenOr:        precOr,
	ast.TokenAnd:       precAnd,
	ast.TokenEQ:        precEquality,
	ast.TokenNotEQ:     precEquality,
	ast.TokenLT:        precComparison,
	ast.TokenLTE:       precComparison,
	ast.TokenGT:        precComparison,
	ast.TokenGTE:       precComparison,
	ast.TokenSpaceship: precComparison,
	ast.TokenRange:     precRange,
	ast.TokenRangeExcl: precRange,
	ast.TokenPlus:      precSum,
	ast.TokenMinus:     precSum,
	ast.TokenSlash:     precProduct,
	ast.TokenAsterisk:  precProduct,
	ast.TokenPercent:   precProduct,
	ast.TokenPower:     precPower,
	ast.TokenLParen:    precCall,
	ast.TokenDot:       precCall,
	ast.TokenScope:     precCall,
	ast.TokenLBracket:  precCall,
	ast.TokenDo:        precCall,
	ast.TokenLBrace:    precCall,
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
	end    ast.Position
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

// Pos returns the 1-indexed source position where the error starts.
func (e *parseError) Pos() ast.Position { return e.pos }

// End returns the exclusive 1-indexed end of the offending token, or a
// zero Position when the span is unknown.
func (e *parseError) End() ast.Position { return e.end }

// Message returns the error text without the position prefix or the
// rendered code frame.
func (e *parseError) Message() string { return e.msg }

func (p *parser) errorExpected(tok ast.Token, expected string) {
	p.addParseErrorSpan(tok.Pos, tokenEnd(tok), fmt.Sprintf("expected %s, got %s", expected, tokenLabel(tok.Type)))
}

func (p *parser) errorUnexpected(tok ast.Token) {
	p.addParseErrorSpan(tok.Pos, tokenEnd(tok), fmt.Sprintf("unexpected token %s", tokenLabel(tok.Type)))
}

func (p *parser) addParseError(pos ast.Position, msg string) {
	p.addParseErrorSpan(pos, ast.Position{}, msg)
}

func (p *parser) addParseErrorSpan(pos, end ast.Position, msg string) {
	p.errors = append(p.errors, &parseError{pos: pos, end: end, msg: msg, source: p.l.input})
}

// tokenEnd returns the lexer-stamped exclusive end of the token. EOF
// carries no span, yielding the zero Position.
func tokenEnd(tok ast.Token) ast.Position {
	return tok.End
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
	case ast.TokenString, ast.TokenInterpolatedString:
		return "string"
	case ast.TokenSymbol:
		return "symbol"
	case ast.TokenWords:
		return "percent word array"
	case ast.TokenSymbols:
		return "percent symbol array"
	case ast.TokenSemicolon:
		return "\";\""
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
	case ast.TokenThen:
		return "'then'"
	case ast.TokenFor:
		return "'for'"
	case ast.TokenIn:
		return "'in'"
	case ast.TokenIf:
		return "'if'"
	case ast.TokenUnless:
		return "'unless'"
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
	case ast.TokenNot:
		return "'not'"
	default:
		if len(tt) == 1 || strings.HasPrefix(string(tt), "<") || strings.HasPrefix(string(tt), ">") {
			return fmt.Sprintf("%q", string(tt))
		}
		return fmt.Sprintf("%q", strings.ToLower(string(tt)))
	}
}
