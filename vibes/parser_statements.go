package vibes

import (
	"fmt"
	"strings"
)

func (p *parser) parseStatement() Statement {
	switch p.curToken.Type {
	case tokenDef:
		return p.parseFunctionStatement()
	case tokenClass:
		return p.parseClassStatement()
	case tokenExport:
		return p.parseExportStatement()
	case tokenPrivate:
		return p.parsePrivateStatement()
	case tokenReturn:
		return p.parseReturnStatement()
	case tokenRaise:
		return p.parseRaiseStatement()
	case tokenIf:
		return p.parseIfStatement()
	case tokenFor:
		return p.parseForStatement()
	case tokenWhile:
		return p.parseWhileStatement()
	case tokenUntil:
		return p.parseUntilStatement()
	case tokenBreak:
		return p.parseBreakStatement()
	case tokenNext:
		return p.parseNextStatement()
	case tokenBegin:
		return p.parseBeginStatement()
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
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()
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

func (p *parser) parseExportStatement() Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "export is only supported for top-level functions")
		return nil
	}
	if !p.expectPeek(tokenDef) {
		return nil
	}
	fnStmt := p.parseFunctionStatement()
	if fnStmt == nil {
		return nil
	}
	fn, ok := fnStmt.(*FunctionStmt)
	if !ok {
		p.addParseError(pos, "export expects a function definition")
		return nil
	}
	if fn.IsClassMethod {
		p.addParseError(pos, "export cannot be used with class methods")
		return nil
	}
	fn.Exported = true
	return fn
}

func (p *parser) parsePrivateStatement() Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "private is only supported for top-level functions and class methods")
		return nil
	}
	if !p.expectPeek(tokenDef) {
		return nil
	}
	fnStmt := p.parseFunctionStatement()
	if fnStmt == nil {
		return nil
	}
	fn, ok := fnStmt.(*FunctionStmt)
	if !ok {
		p.addParseError(pos, "private expects a function definition")
		return nil
	}
	if fn.IsClassMethod {
		p.addParseError(pos, "private cannot be used with class methods")
		return nil
	}
	fn.Private = true
	return fn
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

func (p *parser) parseRaiseStatement() Statement {
	pos := p.curToken.Pos
	if p.peekToken.Type == tokenEOF || p.peekToken.Type == tokenEnd || p.peekToken.Type == tokenEnsure || p.peekToken.Type == tokenRescue || p.peekToken.Pos.Line != pos.Line {
		return &RaiseStmt{position: pos}
	}
	p.nextToken()
	value := p.parseExpression(lowestPrec)
	if value == nil {
		return nil
	}
	return &RaiseStmt{Value: value, position: pos}
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
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()

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

func (p *parser) parseWhileStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseExpression(lowestPrec)

	p.nextToken()
	body := p.parseBlock(tokenEnd)

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &WhileStmt{Condition: condition, Body: body, position: pos}
}

func (p *parser) parseUntilStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseExpression(lowestPrec)

	p.nextToken()
	body := p.parseBlock(tokenEnd)

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &UntilStmt{Condition: condition, Body: body, position: pos}
}

func (p *parser) parseBreakStatement() Statement {
	return &BreakStmt{position: p.curToken.Pos}
}

func (p *parser) parseNextStatement() Statement {
	return &NextStmt{position: p.curToken.Pos}
}

func (p *parser) parseBeginStatement() Statement {
	pos := p.curToken.Pos
	p.nextToken()
	body := p.parseBlock(tokenRescue, tokenEnsure, tokenEnd)

	var rescueTy *TypeExpr
	var rescueBody []Statement
	if p.curToken.Type == tokenRescue {
		rescuePos := p.curToken.Pos
		if p.peekToken.Type == tokenLParen && p.peekToken.Pos.Line == rescuePos.Line {
			p.nextToken()
			p.nextToken()
			rescueTy = p.parseTypeExpr()
			if rescueTy == nil {
				return nil
			}
			if !p.validateRescueTypeExpr(rescueTy, rescuePos) {
				return nil
			}
			if !p.expectPeek(tokenRParen) {
				return nil
			}
		}
		p.nextToken()
		rescueBody = p.parseBlock(tokenEnsure, tokenEnd)
	}

	var ensureBody []Statement
	if p.curToken.Type == tokenEnsure {
		p.nextToken()
		ensureBody = p.parseBlock(tokenEnd)
	}

	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
		return nil
	}

	if len(rescueBody) == 0 && len(ensureBody) == 0 {
		p.addParseError(pos, "begin requires rescue and/or ensure")
		return nil
	}

	return &TryStmt{Body: body, RescueTy: rescueTy, Rescue: rescueBody, Ensure: ensureBody, position: pos}
}

func (p *parser) validateRescueTypeExpr(ty *TypeExpr, pos Position) bool {
	if ty == nil {
		p.addParseError(pos, "rescue type cannot be empty")
		return false
	}

	if ty.Kind == TypeUnion {
		ok := true
		for _, option := range ty.Union {
			if !p.validateRescueTypeExpr(option, option.position) {
				ok = false
			}
		}
		return ok
	}

	if len(ty.TypeArgs) > 0 || len(ty.Shape) > 0 {
		p.addParseError(pos, fmt.Sprintf("rescue type must be an error class, got %s", formatTypeExpr(ty)))
		return false
	}
	if _, ok := canonicalRuntimeErrorType(ty.Name); !ok {
		p.addParseError(pos, fmt.Sprintf("unknown rescue error type %s", ty.Name))
		return false
	}
	return true
}

func (p *parser) parseBlock(stop ...TokenType) []Statement {
	stmts := []Statement{}
	stopSet := make(map[TokenType]struct{}, len(stop))
	for _, tt := range stop {
		stopSet[tt] = struct{}{}
	}
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()

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
