package vibes

import (
	"fmt"
	"strconv"
)

type parseError struct {
	pos Position
	msg string
}

func (e *parseError) Error() string {
	return fmt.Sprintf("parse error at %d:%d: %s", e.pos.Line, e.pos.Column, e.msg)
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
	p.registerPrefix(tokenLParen, p.parseGroupedExpression)
	p.registerPrefix(tokenLBracket, p.parseArrayLiteral)
	p.registerPrefix(tokenLBrace, p.parseHashLiteral)
	p.registerPrefix(tokenBang, p.parsePrefixExpression)
	p.registerPrefix(tokenMinus, p.parsePrefixExpression)

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

func (p *parser) parseStatement() Statement {
	switch p.curToken.Type {
	case tokenDef:
		return p.parseFunctionStatement()
	case tokenReturn:
		return p.parseReturnStatement()
	case tokenIf:
		return p.parseIfStatement()
	case tokenFor:
		return p.parseForStatement()
	case tokenIdent:
		if p.curToken.Literal == "assert" {
			return p.parseAssertStatement()
		}
		return p.parseExpressionOrAssignStatement()
	default:
		return p.parseExpressionOrAssignStatement()
	}
}

func (p *parser) parseFunctionStatement() Statement {
	pos := p.curToken.Pos
	if !p.expectPeek(tokenIdent) {
		return nil
	}
	name := p.curToken.Literal

	if !p.expectPeek(tokenLParen) {
		return nil
	}

	params := []string{}
	if p.peekToken.Type == tokenRParen {
		p.nextToken()
	} else {
		p.nextToken()
		if p.curToken.Type != tokenIdent {
			p.errorExpected(p.curToken, "parameter name")
			return nil
		}
		params = append(params, p.curToken.Literal)
		for p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			if p.curToken.Type != tokenIdent {
				p.errorExpected(p.curToken, "parameter name")
				return nil
			}
			params = append(params, p.curToken.Literal)
		}
		if !p.expectPeek(tokenRParen) {
			return nil
		}
	}

	body := []Statement{}
	p.nextToken()
	for p.curToken.Type != tokenEnd && p.curToken.Type != tokenEOF {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
		p.nextToken()
	}

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &FunctionStmt{Name: name, Params: params, Body: body, position: pos}
}

func (p *parser) parseReturnStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	value := p.parseExpression(lowestPrec)
	return &ReturnStmt{Value: value, position: pos}
}

func (p *parser) parseIfStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseExpression(lowestPrec)

	p.nextToken()
	consequent := p.parseBlock(tokenEnd, tokenElse, tokenElsif)

	var elseifClauses []*IfStmt
	for p.curToken.Type == tokenElsif {
		p.nextToken()
		cond := p.parseExpression(lowestPrec)
		p.nextToken()
		body := p.parseBlock(tokenEnd, tokenElse, tokenElsif)
		clause := &IfStmt{Condition: cond, Consequent: body, position: cond.Pos()}
		elseifClauses = append(elseifClauses, clause)
	}

	var alternate []Statement
	if p.curToken.Type == tokenElse {
		p.nextToken()
		alternate = p.parseBlock(tokenEnd)
	}

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &IfStmt{Condition: condition, Consequent: consequent, ElseIf: elseifClauses, Alternate: alternate, position: pos}
}

func (p *parser) parseForStatement() Statement {
	pos := p.curToken.Pos
	if !p.expectPeek(tokenIdent) {
		return nil
	}
	iterator := p.curToken.Literal

	if !p.expectPeek(tokenIn) {
		return nil
	}

	p.nextToken()
	iterable := p.parseExpression(lowestPrec)

	p.nextToken()
	body := p.parseBlock(tokenEnd)

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ForStmt{Iterator: iterator, Iterable: iterable, Body: body, position: pos}
}

func (p *parser) parseBlock(stop ...TokenType) []Statement {
	stmts := []Statement{}
	stopSet := make(map[TokenType]struct{}, len(stop))
	for _, tt := range stop {
		stopSet[tt] = struct{}{}
	}

	for {
		if _, ok := stopSet[p.curToken.Type]; ok || p.curToken.Type == tokenEOF {
			return stmts
		}
		stmt := p.parseStatement()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		p.nextToken()
	}
}

func (p *parser) parseExpressionOrAssignStatement() Statement {
	expr := p.parseExpression(lowestPrec)
	if expr == nil {
		return nil
	}

	if p.peekToken.Type == tokenDo {
		p.nextToken()
		block := p.parseBlockLiteral()
		var call *CallExpr
		if existing, ok := expr.(*CallExpr); ok {
			call = existing
		} else {
			call = &CallExpr{Callee: expr, position: expr.Pos()}
		}
		call.Block = block
		expr = call
	}

	if p.peekToken.Type == tokenAssign && isAssignable(expr) {
		pos := expr.Pos()
		p.nextToken()
		p.nextToken()
		value := p.parseExpression(lowestPrec)
		return &AssignStmt{Target: expr, Value: value, position: pos}
	}

	return &ExprStmt{Expr: expr, position: expr.Pos()}
}
func (p *parser) parseAssertStatement() Statement {
	pos := p.curToken.Pos
	callee := &Identifier{Name: p.curToken.Literal, position: pos}
	args := []Expression{}
	p.nextToken()
	if p.curToken.Type == tokenEOF || p.curToken.Type == tokenEnd {
		return &ExprStmt{Expr: callee, position: pos}
	}
	first := p.parseExpression(lowestPrec)
	if first != nil {
		args = append(args, first)
		for p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			args = append(args, p.parseExpression(lowestPrec))
		}
	}
	call := &CallExpr{Callee: callee, Args: args, position: pos}
	return &ExprStmt{Expr: call, position: pos}
}

func isAssignable(expr Expression) bool {
	switch expr.(type) {
	case *Identifier, *MemberExpr, *IndexExpr:
		return true
	default:
		return false
	}
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

var precedences = map[TokenType]int{
	tokenOr:       precOr,
	tokenAnd:      precAnd,
	tokenEQ:       precEquality,
	tokenNotEQ:    precEquality,
	tokenLT:       precComparison,
	tokenLTE:      precComparison,
	tokenGT:       precComparison,
	tokenGTE:      precComparison,
	tokenRange:    precRange,
	tokenPlus:     precSum,
	tokenMinus:    precSum,
	tokenSlash:    precProduct,
	tokenAsterisk: precProduct,
	tokenPercent:  precProduct,
	tokenLParen:   precCall,
	tokenDot:      precCall,
	tokenLBracket: precCall,
}

func (p *parser) parseExpression(precedence int) Expression {
	prefix := p.prefixFns[p.curToken.Type]
	if prefix == nil {
		p.errorUnexpected(p.curToken)
		return nil
	}

	left := prefix()

	for p.peekToken.Type != tokenEOF && precedence < p.peekPrecedence() {
		infix := p.infixFns[p.peekToken.Type]
		if infix == nil {
			return left
		}
		p.nextToken()
		left = infix(left)
	}

	return left
}

func (p *parser) parseIdentifier() Expression {
	return &Identifier{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseIntegerLiteral() Expression {
	value, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
	if err != nil {
		p.errors = append(p.errors, &parseError{pos: p.curToken.Pos, msg: "invalid integer literal"})
		return nil
	}
	return &IntegerLiteral{Value: value, position: p.curToken.Pos}
}

func (p *parser) parseFloatLiteral() Expression {
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.errors = append(p.errors, &parseError{pos: p.curToken.Pos, msg: "invalid float literal"})
		return nil
	}
	return &FloatLiteral{Value: value, position: p.curToken.Pos}
}

func (p *parser) parseStringLiteral() Expression {
	return &StringLiteral{Value: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseBooleanLiteral() Expression {
	return &BoolLiteral{Value: p.curToken.Type == tokenTrue, position: p.curToken.Pos}
}

func (p *parser) parseNilLiteral() Expression {
	return &NilLiteral{position: p.curToken.Pos}
}

func (p *parser) parseSymbolLiteral() Expression {
	return &SymbolLiteral{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseGroupedExpression() Expression {
	p.nextToken()
	expr := p.parseExpression(lowestPrec)
	if !p.expectPeek(tokenRParen) {
		return nil
	}
	return expr
}

func (p *parser) parseArrayLiteral() Expression {
	pos := p.curToken.Pos
	elements := []Expression{}

	if p.peekToken.Type == tokenRBracket {
		p.nextToken()
		return &ArrayLiteral{Elements: elements, position: pos}
	}

	p.nextToken()
	elements = append(elements, p.parseExpression(lowestPrec))

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		elements = append(elements, p.parseExpression(lowestPrec))
	}

	if !p.expectPeek(tokenRBracket) {
		return nil
	}

	return &ArrayLiteral{Elements: elements, position: pos}
}

func (p *parser) parseHashLiteral() Expression {
	pos := p.curToken.Pos
	pairs := []HashPair{}

	if p.peekToken.Type == tokenRBrace {
		p.nextToken()
		return &HashLiteral{Pairs: pairs, position: pos}
	}

	p.nextToken()
	if pair := p.parseHashPair(); pair.Key != nil {
		pairs = append(pairs, pair)
	}

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		if pair := p.parseHashPair(); pair.Key != nil {
			pairs = append(pairs, pair)
		}
	}

	if !p.expectPeek(tokenRBrace) {
		return nil
	}

	return &HashLiteral{Pairs: pairs, position: pos}
}

func (p *parser) parseHashPair() HashPair {
	if p.curToken.Type != tokenIdent || p.peekToken.Type != tokenColon {
		p.errors = append(p.errors, &parseError{
			pos: p.curToken.Pos,
			msg: "hash keys must use symbol shorthand (name:)",
		})
		return HashPair{}
	}

	key := &SymbolLiteral{Name: p.curToken.Literal, position: p.curToken.Pos}
	p.nextToken()
	p.nextToken()

	value := p.parseExpression(lowestPrec)
	return HashPair{Key: key, Value: value}
}

func (p *parser) parsePrefixExpression() Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	p.nextToken()
	right := p.parseExpression(precPrefix)
	return &UnaryExpr{Operator: operator, Right: right, position: pos}
}

func (p *parser) parseInfixExpression(left Expression) Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	return &BinaryExpr{Left: left, Operator: operator, Right: right, position: pos}
}

func (p *parser) parseRangeExpression(left Expression) Expression {
	pos := p.curToken.Pos
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	return &RangeExpr{Start: left, End: right, position: pos}
}

func (p *parser) parseCallExpression(function Expression) Expression {
	expr := &CallExpr{Callee: function, position: function.Pos()}
	args := []Expression{}
	kwargs := []KeywordArg{}

	if p.peekToken.Type == tokenRParen {
		p.nextToken()
		expr.Args = args
		expr.KwArgs = kwargs
		return expr
	}

	p.nextToken()
	p.parseCallArgument(&args, &kwargs)

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		p.parseCallArgument(&args, &kwargs)
	}

	if !p.expectPeek(tokenRParen) {
		return nil
	}

	expr.Args = args
	expr.KwArgs = kwargs
	if p.peekToken.Type == tokenDo {
		p.nextToken()
		expr.Block = p.parseBlockLiteral()
	}
	return expr
}

func (p *parser) parseCallArgument(args *[]Expression, kwargs *[]KeywordArg) {
	if p.curToken.Type == tokenIdent && p.peekToken.Type == tokenColon {
		name := p.curToken.Literal
		p.nextToken()
		p.nextToken()
		value := p.parseExpression(lowestPrec)
		*kwargs = append(*kwargs, KeywordArg{Name: name, Value: value})
		return
	}

	expr := p.parseExpression(lowestPrec)
	if expr != nil {
		*args = append(*args, expr)
	}
}

func (p *parser) parseBlockLiteral() *BlockLiteral {
	pos := p.curToken.Pos
	params := []string{}

	p.nextToken()
	if p.curToken.Type == tokenPipe {
		var ok bool
		params, ok = p.parseBlockParameters()
		if !ok {
			return nil
		}
		p.nextToken()
	}

	body := p.parseBlock(tokenEnd)
	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &BlockLiteral{Params: params, Body: body, position: pos}
}

func (p *parser) parseBlockParameters() ([]string, bool) {
	params := []string{}
	p.nextToken()
	if p.curToken.Type == tokenPipe {
		return params, true
	}

	if p.curToken.Type != tokenIdent {
		p.errorExpected(p.curToken, "block parameter")
		return nil, false
	}
	params = append(params, p.curToken.Literal)

	for p.peekToken.Type == tokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != tokenIdent {
			p.errorExpected(p.curToken, "block parameter")
			return nil, false
		}
		params = append(params, p.curToken.Literal)
	}

	if !p.expectPeek(tokenPipe) {
		return nil, false
	}

	return params, true
}

func (p *parser) parseMemberExpression(object Expression) Expression {
	p.nextToken()
	if p.curToken.Type != tokenIdent {
		p.errorExpected(p.curToken, "identifier after .")
		return nil
	}
	return &MemberExpr{Object: object, Property: p.curToken.Literal, position: object.Pos()}
}

func (p *parser) parseIndexExpression(object Expression) Expression {
	pos := p.curToken.Pos
	p.nextToken()
	index := p.parseExpression(lowestPrec)
	if !p.expectPeek(tokenRBracket) {
		return nil
	}
	return &IndexExpr{Object: object, Index: index, position: pos}
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
	p.errorExpected(p.peekToken, string(tt))
	return false
}

func (p *parser) errorExpected(tok Token, expected string) {
	p.errors = append(p.errors, &parseError{pos: tok.Pos, msg: fmt.Sprintf("expected %s, got %s", expected, tok.Type)})
}

func (p *parser) errorUnexpected(tok Token) {
	p.errors = append(p.errors, &parseError{pos: tok.Pos, msg: fmt.Sprintf("unexpected token %s", tok.Type)})
}
