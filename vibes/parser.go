package vibes

import (
	"fmt"
	"strconv"
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

	insideClass bool
	privateNext bool
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
	case tokenClass:
		return p.parseClassStatement()
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
	p.nextToken()

	isClassMethod := false
	var name string
	if p.curToken.Type == tokenSelf && p.peekToken.Type == tokenDot {
		isClassMethod = true
		p.nextToken() // consume dot
		if !p.expectPeek(tokenIdent) {
			return nil
		}
		name = p.curToken.Literal
		p.nextToken()
	} else {
		if p.curToken.Type != tokenIdent {
			p.errorExpected(p.curToken, "function name")
			return nil
		}
		name = p.curToken.Literal
		p.nextToken()
	}

	if p.curToken.Type == tokenAssign {
		name += "="
		p.nextToken()
	}

	params := []Param{}
	var returnTy *TypeExpr
	// Optional parens on the same line
	if p.curToken.Type == tokenLParen && p.curToken.Pos.Line == pos.Line {
		if p.peekToken.Type == tokenRParen {
			p.nextToken() // consume ')'
			p.nextToken()
		} else {
			p.nextToken()
			params = p.parseParams()
			if !p.expectPeek(tokenRParen) {
				return nil
			}
			p.nextToken()
		}
	}
	if p.curToken.Type == tokenArrow {
		p.nextToken()
		returnTy = p.parseTypeExpr()
		if returnTy == nil {
			return nil
		}
		p.nextToken()
	}
	body := []Statement{}
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

	private := false
	if p.insideClass && p.privateNext {
		private = true
		p.privateNext = false
	}

	return &FunctionStmt{Name: name, Params: params, ReturnTy: returnTy, Body: body, IsClassMethod: isClassMethod, Private: private, position: pos}
}

func (p *parser) parseParams() []Param {
	params := []Param{}
	for {
		if p.curToken.Type != tokenIdent && p.curToken.Type != tokenIvar {
			p.errorExpected(p.curToken, "parameter name")
			return params
		}
		param := Param{Name: p.curToken.Literal}
		if p.curToken.Type == tokenIvar {
			param.IsIvar = true
			param.Name = strings.TrimPrefix(param.Name, "@")
		}
		if p.peekToken.Type == tokenColon {
			p.nextToken()
			p.nextToken()
			param.Type = p.parseTypeExpr()
			if param.Type == nil {
				return params
			}
		}
		if p.peekToken.Type == tokenAssign {
			p.nextToken()
			p.nextToken()
			param.DefaultVal = p.parseExpression(lowestPrec)
		}
		params = append(params, param)
		if p.peekToken.Type != tokenComma {
			break
		}
		p.nextToken()
		p.nextToken()
	}
	return params
}

func (p *parser) parseReturnStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	value := p.parseExpression(lowestPrec)
	return &ReturnStmt{Value: value, position: pos}
}

func (p *parser) parseClassStatement() Statement {
	pos := p.curToken.Pos
	if !p.expectPeek(tokenIdent) {
		return nil
	}
	name := p.curToken.Literal
	p.nextToken()

	stmt := &ClassStmt{
		Name:     name,
		position: pos,
	}

	prevInside := p.insideClass
	prevPrivate := p.privateNext
	p.insideClass = true
	p.privateNext = false

	for p.curToken.Type != tokenEnd && p.curToken.Type != tokenEOF {
		switch p.curToken.Type {
		case tokenDef:
			fnStmt := p.parseFunctionStatement()
			if fnStmt == nil {
				return nil
			}
			fn := fnStmt.(*FunctionStmt)
			if fn.IsClassMethod {
				stmt.ClassMethods = append(stmt.ClassMethods, fn)
			} else {
				stmt.Methods = append(stmt.Methods, fn)
			}
		case tokenPrivate:
			if p.peekToken.Type == tokenDef {
				p.privateNext = true
				p.nextToken()
				continue
			}
			p.privateNext = true
		case tokenProperty, tokenGetter, tokenSetter:
			decl := p.parsePropertyDecl(p.curToken.Type)
			stmt.Properties = append(stmt.Properties, decl)
		default:
			s := p.parseStatement()
			if s != nil {
				stmt.Body = append(stmt.Body, s)
			}
		}
		p.nextToken()
	}

	p.insideClass = prevInside
	p.privateNext = prevPrivate

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return stmt
}

func (p *parser) parsePropertyDecl(kind TokenType) PropertyDecl {
	pos := p.curToken.Pos
	names := []string{}
	p.nextToken()
	if p.curToken.Type == tokenIdent {
		names = append(names, p.curToken.Literal)
		for p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			if p.curToken.Type != tokenIdent {
				p.errorExpected(p.curToken, "property name")
				break
			}
			names = append(names, p.curToken.Literal)
		}
	}
	return PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), position: pos}
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
		value := p.parseExpressionWithBlock()
		return &AssignStmt{Target: expr, Value: value, position: pos}
	}

	return &ExprStmt{Expr: expr, position: expr.Pos()}
}

func (p *parser) parseExpressionWithBlock() Expression {
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
		return call
	}
	return expr
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
	case *Identifier, *MemberExpr, *IndexExpr, *IvarExpr, *ClassVarExpr:
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
		p.addParseError(p.curToken.Pos, "invalid integer literal")
		return nil
	}
	return &IntegerLiteral{Value: value, position: p.curToken.Pos}
}

func (p *parser) parseFloatLiteral() Expression {
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid float literal")
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

func (p *parser) parseIvarLiteral() Expression {
	return &IvarExpr{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseClassVarLiteral() Expression {
	return &ClassVarExpr{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseSelfLiteral() Expression {
	return &Identifier{Name: "self", position: p.curToken.Pos}
}

func (p *parser) parseYieldExpression() Expression {
	pos := p.curToken.Pos
	var args []Expression
	if p.peekToken.Type == tokenLParen {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != tokenRParen {
			args = append(args, p.parseExpression(lowestPrec))
			for p.peekToken.Type == tokenComma {
				p.nextToken()
				p.nextToken()
				args = append(args, p.parseExpression(lowestPrec))
			}
			if !p.expectPeek(tokenRParen) {
				return nil
			}
		}
	} else if p.prefixFns[p.peekToken.Type] != nil {
		p.nextToken()
		args = append(args, p.parseExpression(lowestPrec))
	}
	return &YieldExpr{Args: args, position: pos}
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
		p.addParseError(p.curToken.Pos, "invalid hash pair: expected symbol-style key like name:")
		return HashPair{}
	}

	key := &SymbolLiteral{Name: p.curToken.Literal, position: p.curToken.Pos}
	p.nextToken()
	p.nextToken()
	if p.curToken.Type == tokenComma || p.curToken.Type == tokenRBrace {
		p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for hash key %s", key.Name))
		return HashPair{}
	}

	value := p.parseExpression(lowestPrec)
	if value == nil {
		return HashPair{}
	}
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
	if (p.curToken.Type == tokenIdent || p.curToken.Type == tokenIn) && p.peekToken.Type == tokenColon {
		name := p.curToken.Literal
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == tokenComma || p.curToken.Type == tokenRParen {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for keyword argument %s", name))
			return
		}
		value := p.parseExpression(lowestPrec)
		if value == nil {
			return
		}
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
		if p.curToken.Type == tokenPipe {
			p.addParseError(p.curToken.Pos, "trailing comma in block parameter list")
			return nil, false
		}
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
	if object == nil {
		return nil
	}
	p.nextToken()
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

func resolveType(name string) (TypeKind, bool) {
	nullable := false
	if strings.HasSuffix(name, "?") {
		nullable = true
		name = strings.TrimSuffix(name, "?")
	}
	switch strings.ToLower(name) {
	case "any":
		return TypeAny, nullable
	case "int":
		return TypeInt, nullable
	case "float":
		return TypeFloat, nullable
	case "number":
		return TypeNumber, nullable
	case "string":
		return TypeString, nullable
	case "bool":
		return TypeBool, nullable
	case "nil":
		return TypeNil, nullable
	case "duration":
		return TypeDuration, nullable
	case "time":
		return TypeTime, nullable
	case "money":
		return TypeMoney, nullable
	case "array":
		return TypeArray, nullable
	case "hash", "object":
		return TypeHash, nullable
	case "function":
		return TypeFunction, nullable
	}
	return TypeUnknown, nullable
}

func (p *parser) parseTypeExpr() *TypeExpr {
	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*TypeExpr{first}
	for p.peekToken.Type == tokenPipe {
		p.nextToken()
		p.nextToken()
		next := p.parseTypeAtom()
		if next == nil {
			return nil
		}
		union = append(union, next)
	}

	if len(union) == 1 {
		return first
	}

	names := make([]string, len(union))
	for i, option := range union {
		names[i] = formatTypeExpr(option)
	}
	return &TypeExpr{
		Name:     strings.Join(names, " | "),
		Kind:     TypeUnion,
		Union:    union,
		position: first.position,
	}
}

func (p *parser) parseTypeAtom() *TypeExpr {
	if p.curToken.Type == tokenLBrace {
		return p.parseTypeShape()
	}
	if p.curToken.Type != tokenIdent && p.curToken.Type != tokenNil {
		p.errorExpected(p.curToken, "type name")
		return nil
	}
	ty := &TypeExpr{Name: p.curToken.Literal, position: p.curToken.Pos}
	kind, nullable := resolveType(p.curToken.Literal)
	ty.Kind = kind
	ty.Nullable = nullable

	if p.peekToken.Type == tokenLT {
		p.nextToken()
		p.nextToken()
		typeArgs := []*TypeExpr{}
		for {
			arg := p.parseTypeExpr()
			if arg == nil {
				return nil
			}
			typeArgs = append(typeArgs, arg)

			if p.peekToken.Type == tokenComma {
				p.nextToken()
				p.nextToken()
				continue
			}

			if p.peekToken.Type != tokenGT {
				p.errorExpected(p.peekToken, ">")
				return nil
			}
			p.nextToken()
			break
		}
		ty.TypeArgs = typeArgs
	}

	return ty
}

func (p *parser) parseTypeShape() *TypeExpr {
	pos := p.curToken.Pos
	fields := make(map[string]*TypeExpr)

	if p.peekToken.Type == tokenRBrace {
		p.nextToken()
		return &TypeExpr{
			Kind:     TypeShape,
			Shape:    fields,
			position: pos,
		}
	}

	p.nextToken()
	for {
		key, ok := p.parseTypeShapeFieldName()
		if !ok {
			return nil
		}
		if p.peekToken.Type != tokenColon {
			p.errorExpected(p.peekToken, ":")
			return nil
		}
		p.nextToken()
		p.nextToken()
		fieldType := p.parseTypeExpr()
		if fieldType == nil {
			return nil
		}
		if _, exists := fields[key]; exists {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("duplicate shape field %s", key))
			return nil
		}
		fields[key] = fieldType

		if p.peekToken.Type == tokenComma {
			p.nextToken()
			p.nextToken()
			continue
		}
		if p.peekToken.Type != tokenRBrace {
			p.errorExpected(p.peekToken, "}")
			return nil
		}
		p.nextToken()
		break
	}

	return &TypeExpr{
		Kind:     TypeShape,
		Shape:    fields,
		position: pos,
	}
}

func (p *parser) parseTypeShapeFieldName() (string, bool) {
	switch p.curToken.Type {
	case tokenIdent, tokenString, tokenSymbol:
		return p.curToken.Literal, true
	default:
		p.errorExpected(p.curToken, "shape field name")
		return "", false
	}
}
