package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseExpression(precedence int) ast.Expression {
	if p.lineLimitedExprs > 0 {
		return p.parseExpressionWithLineLimit(precedence, p.curToken.Pos.Line, true)
	}
	return p.parseExpressionWithLineLimit(precedence, 0, false)
}

func (p *parser) parseLineExpression(precedence int) ast.Expression {
	p.lineLimitedExprs++
	defer func() {
		p.lineLimitedExprs--
	}()
	return p.parseExpression(precedence)
}

func (p *parser) parseExpressionWithLineLimit(precedence, limitLine int, lineLimited bool) ast.Expression {
	prefix := p.prefixFns[p.curToken.Type]
	if prefix == nil {
		p.errorUnexpected(p.curToken)
		return nil
	}

	left := prefix()
	if left == nil {
		return nil
	}

	for p.peekToken.Type != ast.TokenEOF && precedence < p.peekPrecedence() {
		if lineLimited && p.peekToken.Pos.Line > limitLine && !p.lineLimitedContinuationToken(p.peekToken) {
			return left
		}
		infix := p.infixFns[p.peekToken.Type]
		if infix == nil {
			return left
		}
		p.nextToken()
		left = infix(left)
		if left == nil {
			return nil
		}
		if lineLimited {
			limitLine = p.curToken.Pos.Line
		}
	}

	return left
}

func (p *parser) lineLimitedContinuationToken(tok ast.Token) bool {
	switch tok.Type {
	case ast.TokenDot, ast.TokenScope, ast.TokenPlus, ast.TokenSlash, ast.TokenAsterisk, ast.TokenPercent, ast.TokenRange, ast.TokenEQ, ast.TokenNotEQ, ast.TokenLT, ast.TokenLTE, ast.TokenGT, ast.TokenGTE, ast.TokenAnd, ast.TokenOr:
		return true
	case ast.TokenMinus:
		return p.minusContinuesLine(tok)
	default:
		return false
	}
}

func (p *parser) minusContinuesLine(tok ast.Token) bool {
	if p.peekPeek.Type == ast.TokenEOF {
		return false
	}
	if p.peekPeek.Pos.Line > tok.Pos.Line {
		return true
	}
	return p.peekPeek.Pos.Line == tok.Pos.Line && p.peekPeek.Pos.Column > tok.End.Column
}

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

func (p *parser) parseGroupedExpression() ast.Expression {
	p.nextToken()
	expr := p.parseExpression(lowestPrec)
	if !p.expectPeek(ast.TokenRParen) {
		return nil
	}
	return expr
}

func (p *parser) parsePrefixExpression() ast.Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	p.nextToken()
	right := p.parseExpression(precPrefix)
	if right == nil {
		return nil
	}
	return &ast.UnaryExpr{Operator: operator, Right: right, Position: pos}
}

func (p *parser) parseInfixExpression(left ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	if right == nil {
		return nil
	}
	return &ast.BinaryExpr{Left: left, Operator: operator, Right: right, Position: pos}
}

func (p *parser) parseRangeExpression(left ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	precedence := p.curPrecedence()
	p.nextToken()
	right := p.parseExpression(precedence)
	if right == nil {
		return nil
	}
	return &ast.RangeExpr{Start: left, End: right, Position: pos}
}

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

func (p *parser) parseArrayLiteral() ast.Expression {
	pos := p.curToken.Pos
	elements := []ast.Expression{}

	if p.peekToken.Type == ast.TokenRBracket {
		p.nextToken()
		return &ast.ArrayLiteral{Elements: elements, Position: pos}
	}

	p.nextToken()
	elements = append(elements, p.parseExpression(lowestPrec))

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		elements = append(elements, p.parseExpression(lowestPrec))
	}

	if !p.expectPeek(ast.TokenRBracket) {
		return nil
	}

	return &ast.ArrayLiteral{Elements: elements, Position: pos}
}

func (p *parser) parseHashLiteral() ast.Expression {
	pos := p.curToken.Pos
	pairs := []ast.HashPair{}

	if p.peekToken.Type == ast.TokenRBrace {
		p.nextToken()
		return &ast.HashLiteral{Pairs: pairs, Position: pos}
	}

	p.nextToken()
	if pair := p.parseHashPair(); pair.Key != nil {
		pairs = append(pairs, pair)
	}

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		if pair := p.parseHashPair(); pair.Key != nil {
			pairs = append(pairs, pair)
		}
	}

	if !p.expectPeek(ast.TokenRBrace) {
		return nil
	}

	return &ast.HashLiteral{Pairs: pairs, Position: pos}
}

func (p *parser) parseHashPair() ast.HashPair {
	if !isLabelNameToken(p.curToken.Type) || p.peekToken.Type != ast.TokenColon {
		p.addParseError(p.curToken.Pos, "invalid hash pair: expected symbol-style key like name:")
		return ast.HashPair{}
	}

	key := &ast.SymbolLiteral{Name: p.curToken.Literal, Position: p.curToken.Pos}
	p.nextToken()
	p.nextToken()
	if p.curToken.Type == ast.TokenComma || p.curToken.Type == ast.TokenRBrace {
		p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for hash key %s", key.Name))
		return ast.HashPair{}
	}

	value := p.parseExpression(lowestPrec)
	if value == nil {
		return ast.HashPair{}
	}
	return ast.HashPair{Key: key, Value: value}
}

func (p *parser) parseBlockLiteral() *ast.BlockLiteral {
	pos := p.curToken.Pos
	params := []ast.Param{}
	stopToken := ast.TokenEnd
	stopName := "end"
	if p.curToken.Type == ast.TokenLBrace {
		stopToken = ast.TokenRBrace
		stopName = "}"
	}

	p.nextToken()
	if p.curToken.Type == ast.TokenPipe {
		var ok bool
		params, ok = p.parseBlockParameters()
		if !ok {
			return nil
		}
		p.nextToken()
	}

	body := p.parseBlock(stopToken)
	if p.curToken.Type != stopToken {
		p.errorExpected(p.curToken, stopName)
	}

	return &ast.BlockLiteral{Params: params, Body: body, Position: pos}
}

func (p *parser) parseBlockParameters() ([]ast.Param, bool) {
	params := []ast.Param{}
	p.nextToken()
	if p.curToken.Type == ast.TokenPipe {
		return params, true
	}

	param, ok := p.parseBlockParameter()
	if !ok {
		return nil, false
	}
	params = append(params, param)

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == ast.TokenPipe {
			p.addParseError(p.curToken.Pos, "trailing comma in block parameter list")
			return nil, false
		}
		param, ok := p.parseBlockParameter()
		if !ok {
			return nil, false
		}
		params = append(params, param)
	}

	if !p.expectPeek(ast.TokenPipe) {
		return nil, false
	}

	return params, true
}

func (p *parser) parseBlockParameter() (ast.Param, bool) {
	if p.curToken.Type != ast.TokenIdent {
		p.errorExpected(p.curToken, "block parameter")
		return ast.Param{}, false
	}
	param := ast.Param{Name: p.curToken.Literal}
	if p.peekToken.Type == ast.TokenColon {
		p.nextToken()
		p.nextToken()
		param.Type = p.parseBlockParamType()
		if param.Type == nil {
			return ast.Param{}, false
		}
	}
	return param, true
}

func (p *parser) parseBlockParamType() *ast.TypeExpr {
	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*ast.TypeExpr{first}
	for p.peekToken.Type == ast.TokenPipe && p.blockParamUnionContinues() {
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
		names[i] = ast.FormatTypeExpr(option)
	}
	return &ast.TypeExpr{
		Name:     strings.Join(names, " | "),
		Kind:     ast.TypeUnion,
		Union:    union,
		Position: first.Position,
	}
}

func (p *parser) blockParamUnionContinues() bool {
	if p.peekToken.Type != ast.TokenPipe {
		return false
	}

	savedLexer := *p.l
	savedCur := p.curToken
	savedPeek := p.peekToken
	savedPeekPeek := p.peekPeek
	savedErrors := len(p.errors)

	p.nextToken()
	p.nextToken()
	atom := p.parseTypeAtom()
	ok := atom != nil && (p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenPipe)

	p.l = &savedLexer
	p.curToken = savedCur
	p.peekToken = savedPeek
	p.peekPeek = savedPeekPeek
	p.errors = p.errors[:savedErrors]
	return ok
}

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
	if p.canAttachPeekBlock() {
		p.nextToken()
		expr.Block = p.parseBlockLiteral()
	}
	return expr
}

func (p *parser) parseTrailingBlockExpression(callee ast.Expression) ast.Expression {
	return p.callWithBlock(callee, p.parseBlockLiteral())
}

func (p *parser) callWithBlock(callee ast.Expression, block *ast.BlockLiteral) ast.Expression {
	if callee == nil {
		return nil
	}
	var call *ast.CallExpr
	if existing, ok := callee.(*ast.CallExpr); ok {
		call = existing
	} else {
		call = &ast.CallExpr{Callee: callee, Position: callee.Pos()}
	}
	call.Block = block
	return call
}

func (p *parser) canAttachPeekBlock() bool {
	if p.peekToken.Type == ast.TokenDo {
		return true
	}
	return p.peekToken.Type == ast.TokenLBrace && p.peekToken.Pos.Line == p.curToken.Pos.Line
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

func (p *parser) parseCaseExpression() ast.Expression {
	pos := p.curToken.Pos
	p.nextToken()
	target := p.parseLineExpression(lowestPrec)
	if target == nil {
		return nil
	}

	p.nextToken()
	clauses := []ast.CaseWhenClause{}
	for p.curToken.Type == ast.TokenWhen {
		p.nextToken()
		values := []ast.Expression{}
		first := p.parseLineExpression(lowestPrec)
		if first == nil {
			return nil
		}
		values = append(values, first)
		for p.peekToken.Type == ast.TokenComma {
			p.nextToken()
			p.nextToken()
			value := p.parseLineExpression(lowestPrec)
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

func (p *parser) parseYieldExpression() ast.Expression {
	pos := p.curToken.Pos
	var args []ast.Expression
	if p.peekToken.Type == ast.TokenLParen {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != ast.TokenRParen {
			args = append(args, p.parseExpression(lowestPrec))
			for p.peekToken.Type == ast.TokenComma {
				p.nextToken()
				p.nextToken()
				args = append(args, p.parseExpression(lowestPrec))
			}
			if !p.expectPeek(ast.TokenRParen) {
				return nil
			}
		}
	} else if p.peekToken.Pos.Line == pos.Line && p.prefixFns[p.peekToken.Type] != nil {
		p.nextToken()
		args = append(args, p.parseExpression(lowestPrec))
	}
	return &ast.YieldExpr{Args: args, Position: pos}
}
