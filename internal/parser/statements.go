package parser

import (
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseStatement() ast.Statement {
	switch p.curToken.Type {
	case ast.TokenDef:
		return p.parseFunctionStatement()
	case ast.TokenClass:
		return p.parseClassStatement()
	case ast.TokenEnum:
		return p.parseEnumStatement()
	case ast.TokenExport:
		return p.parseExportStatement()
	case ast.TokenPrivate:
		return p.parsePrivateStatement()
	case ast.TokenReturn:
		return p.parseReturnStatement()
	case ast.TokenRaise:
		return p.parseRaiseStatement()
	case ast.TokenIf:
		return p.parseIfStatement()
	case ast.TokenFor:
		return p.parseForStatement()
	case ast.TokenWhile:
		return p.parseWhileStatement()
	case ast.TokenUntil:
		return p.parseUntilStatement()
	case ast.TokenBreak:
		return p.parseBreakStatement()
	case ast.TokenNext:
		return p.parseNextStatement()
	case ast.TokenBegin:
		return p.parseBeginStatement()
	case ast.TokenIdent:
		if p.curToken.Literal == "assert" {
			return p.parseAssertStatement()
		}
		return p.parseExpressionOrAssignStatement()
	default:
		return p.parseExpressionOrAssignStatement()
	}
}

func (p *parser) parseReturnStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.peekToken.Type == ast.TokenEOF || p.peekToken.Type == ast.TokenEnd || p.peekToken.Type == ast.TokenElse || p.peekToken.Type == ast.TokenElsif || p.peekToken.Type == ast.TokenEnsure || p.peekToken.Type == ast.TokenRescue || p.peekToken.Pos.Line != pos.Line {
		return &ast.ReturnStmt{Position: pos}
	}
	p.nextToken()
	value := p.parseLineExpression(lowestPrec)
	if value == nil {
		return nil
	}
	return &ast.ReturnStmt{Value: value, Position: pos}
}

func (p *parser) parseRaiseStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.peekToken.Type == ast.TokenEOF || p.peekToken.Type == ast.TokenEnd || p.peekToken.Type == ast.TokenElse || p.peekToken.Type == ast.TokenElsif || p.peekToken.Type == ast.TokenEnsure || p.peekToken.Type == ast.TokenRescue || p.peekToken.Pos.Line != pos.Line {
		return &ast.RaiseStmt{Position: pos}
	}
	p.nextToken()
	value := p.parseLineExpression(lowestPrec)
	if value == nil {
		return nil
	}
	return &ast.RaiseStmt{Value: value, Position: pos}
}

func (p *parser) parseBlock(stop ...ast.TokenType) []ast.Statement {
	stmts := []ast.Statement{}
	stopSet := make(map[ast.TokenType]struct{}, len(stop))
	for _, tt := range stop {
		stopSet[tt] = struct{}{}
	}
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()

	for {
		if _, ok := stopSet[p.curToken.Type]; ok || p.curToken.Type == ast.TokenEOF {
			return stmts
		}
		stmt := p.parseStatement()
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		p.nextToken()
	}
}

func (p *parser) parseIfStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLineExpression(lowestPrec)
	if condition == nil {
		return nil
	}

	p.nextToken()
	consequent := p.parseBlock(ast.TokenEnd, ast.TokenElse, ast.TokenElsif)

	var elseifClauses []*ast.IfStmt
	for p.curToken.Type == ast.TokenElsif {
		p.nextToken()
		cond := p.parseLineExpression(lowestPrec)
		if cond == nil {
			return nil
		}
		p.nextToken()
		body := p.parseBlock(ast.TokenEnd, ast.TokenElse, ast.TokenElsif)
		clause := &ast.IfStmt{Condition: cond, Consequent: body, Position: cond.Pos()}
		elseifClauses = append(elseifClauses, clause)
	}

	var alternate []ast.Statement
	if p.curToken.Type == ast.TokenElse {
		p.nextToken()
		alternate = p.parseBlock(ast.TokenEnd)
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.IfStmt{Condition: condition, Consequent: consequent, ElseIf: elseifClauses, Alternate: alternate, Position: pos}
}

func (p *parser) parseForStatement() ast.Statement {
	pos := p.curToken.Pos
	if !p.expectPeek(ast.TokenIdent) {
		return nil
	}
	iterator := p.curToken.Literal

	if !p.expectPeek(ast.TokenIn) {
		return nil
	}

	p.nextToken()
	iterable := p.parseLineExpression(lowestPrec)
	if iterable == nil {
		return nil
	}

	p.nextToken()
	body := p.parseBlock(ast.TokenEnd)

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.ForStmt{Iterator: iterator, Iterable: iterable, Body: body, Position: pos}
}

func (p *parser) parseWhileStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLineExpression(lowestPrec)
	if condition == nil {
		return nil
	}

	p.nextToken()
	body := p.parseBlock(ast.TokenEnd)

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.WhileStmt{Condition: condition, Body: body, Position: pos}
}

func (p *parser) parseUntilStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLineExpression(lowestPrec)
	if condition == nil {
		return nil
	}

	p.nextToken()
	body := p.parseBlock(ast.TokenEnd)

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.UntilStmt{Condition: condition, Body: body, Position: pos}
}

func (p *parser) parseBreakStatement() ast.Statement {
	return &ast.BreakStmt{Position: p.curToken.Pos}
}

func (p *parser) parseNextStatement() ast.Statement {
	return &ast.NextStmt{Position: p.curToken.Pos}
}

func (p *parser) parseBeginStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	body := p.parseBlock(ast.TokenRescue, ast.TokenEnsure, ast.TokenEnd)

	var rescueTy *ast.TypeExpr
	var rescueBody []ast.Statement
	if p.curToken.Type == ast.TokenRescue {
		rescuePos := p.curToken.Pos
		if p.peekToken.Type == ast.TokenLParen && p.peekToken.Pos.Line == rescuePos.Line {
			p.nextToken()
			p.nextToken()
			rescueTy = p.parseTypeExpr()
			if rescueTy == nil {
				return nil
			}
			if !p.validateRescueTypeExpr(rescueTy, rescuePos) {
				return nil
			}
			if !p.expectPeek(ast.TokenRParen) {
				return nil
			}
		}
		p.nextToken()
		rescueBody = p.parseBlock(ast.TokenEnsure, ast.TokenEnd)
	}

	var ensureBody []ast.Statement
	if p.curToken.Type == ast.TokenEnsure {
		p.nextToken()
		ensureBody = p.parseBlock(ast.TokenEnd)
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
		return nil
	}

	if len(rescueBody) == 0 && len(ensureBody) == 0 {
		p.addParseError(pos, "begin requires rescue and/or ensure")
		return nil
	}

	return &ast.TryStmt{Body: body, RescueTy: rescueTy, Rescue: rescueBody, Ensure: ensureBody, Position: pos}
}

func (p *parser) validateRescueTypeExpr(ty *ast.TypeExpr, pos ast.Position) bool {
	if ty == nil {
		p.addParseError(pos, "rescue type cannot be empty")
		return false
	}

	if ty.Kind == ast.TypeUnion {
		ok := true
		for _, option := range ty.Union {
			if !p.validateRescueTypeExpr(option, option.Position) {
				ok = false
			}
		}
		return ok
	}

	if len(ty.TypeArgs) > 0 || len(ty.Shape) > 0 {
		p.addParseError(pos, fmt.Sprintf("rescue type must be an error class, got %s", ast.FormatTypeExpr(ty)))
		return false
	}
	if _, ok := ast.CanonicalRuntimeErrorType(ty.Name); !ok {
		p.addParseError(pos, fmt.Sprintf("unknown rescue error type %s", ty.Name))
		return false
	}
	return true
}

func (p *parser) parseFunctionStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()

	isClassMethod := false
	var name string
	if p.curToken.Type == ast.TokenSelf && p.peekToken.Type == ast.TokenDot {
		isClassMethod = true
		p.nextToken() // consume dot
		if !p.expectPeek(ast.TokenIdent) {
			return nil
		}
		name = p.curToken.Literal
		p.nextToken()
	} else {
		if p.curToken.Type != ast.TokenIdent {
			p.errorExpected(p.curToken, "function name")
			return nil
		}
		name = p.curToken.Literal
		p.nextToken()
	}

	if p.curToken.Type == ast.TokenAssign {
		name += "="
		p.nextToken()
	}

	params := []ast.Param{}
	var returnTy *ast.TypeExpr
	// Optional parens on the same line
	if p.curToken.Type == ast.TokenLParen && p.curToken.Pos.Line == pos.Line {
		if p.peekToken.Type == ast.TokenRParen {
			p.nextToken() // consume ')'
			p.nextToken()
		} else {
			p.nextToken()
			params = p.parseParams()
			if !p.expectPeek(ast.TokenRParen) {
				return nil
			}
			p.nextToken()
		}
	}
	if p.curToken.Type == ast.TokenArrow {
		p.nextToken()
		returnTy = p.parseTypeExpr()
		if returnTy == nil {
			return nil
		}
		p.nextToken()
	}
	body := []ast.Statement{}
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()
	for p.curToken.Type != ast.TokenEnd && p.curToken.Type != ast.TokenEOF {
		stmt := p.parseStatement()
		if stmt != nil {
			body = append(body, stmt)
		}
		p.nextToken()
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	private := false
	if p.insideClass && p.privateNext {
		private = true
		p.privateNext = false
	}

	return &ast.FunctionStmt{Name: name, Params: params, ReturnTy: returnTy, Body: body, IsClassMethod: isClassMethod, Private: private, Position: pos}
}

func (p *parser) parseClassStatement() ast.Statement {
	pos := p.curToken.Pos
	if !p.expectPeek(ast.TokenIdent) {
		return nil
	}
	name := p.curToken.Literal
	p.nextToken()

	stmt := &ast.ClassStmt{
		Name:     name,
		Position: pos,
	}

	prevInside := p.insideClass
	prevPrivate := p.privateNext
	p.insideClass = true
	p.privateNext = false
	p.statementNesting++
	defer func() {
		p.statementNesting--
	}()

	for p.curToken.Type != ast.TokenEnd && p.curToken.Type != ast.TokenEOF {
		switch p.curToken.Type {
		case ast.TokenDef:
			fnStmt := p.parseFunctionStatement()
			if fnStmt == nil {
				return nil
			}
			fn := fnStmt.(*ast.FunctionStmt)
			if fn.IsClassMethod {
				stmt.ClassMethods = append(stmt.ClassMethods, fn)
			} else {
				stmt.Methods = append(stmt.Methods, fn)
			}
		case ast.TokenPrivate:
			if p.peekToken.Type == ast.TokenDef {
				p.privateNext = true
				p.nextToken()
				continue
			}
			p.privateNext = true
		case ast.TokenProperty, ast.TokenGetter, ast.TokenSetter:
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

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return stmt
}

func (p *parser) parseEnumStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "enum is only supported at the top level")
		return nil
	}
	if !p.expectPeek(ast.TokenIdent) {
		return nil
	}
	name := p.curToken.Literal
	p.nextToken()

	stmt := &ast.EnumStmt{
		Name:     name,
		Members:  make([]ast.EnumMemberStmt, 0),
		Position: pos,
	}
	memberNames := make(map[string]struct{})

	for p.curToken.Type != ast.TokenEnd && p.curToken.Type != ast.TokenEOF {
		if p.curToken.Type != ast.TokenIdent && p.curToken.Type != ast.TokenEnum {
			p.errorExpected(p.curToken, "enum member name")
			return nil
		}
		member := ast.EnumMemberStmt{
			Name:     p.curToken.Literal,
			Position: p.curToken.Pos,
		}
		if _, exists := memberNames[member.Name]; exists {
			p.addParseError(member.Position, fmt.Sprintf("duplicate enum member %s", member.Name))
			return nil
		}
		memberNames[member.Name] = struct{}{}
		stmt.Members = append(stmt.Members, member)
		p.nextToken()
	}

	if len(stmt.Members) == 0 {
		p.addParseError(pos, fmt.Sprintf("enum %s must define at least one member", name))
		return nil
	}
	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return stmt
}

func (p *parser) parseExportStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "export is only supported for top-level functions")
		return nil
	}
	if !p.expectPeek(ast.TokenDef) {
		return nil
	}
	fnStmt := p.parseFunctionStatement()
	if fnStmt == nil {
		return nil
	}
	fn, ok := fnStmt.(*ast.FunctionStmt)
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

func (p *parser) parsePrivateStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "private is only supported for top-level functions and class methods")
		return nil
	}
	if !p.expectPeek(ast.TokenDef) {
		return nil
	}
	fnStmt := p.parseFunctionStatement()
	if fnStmt == nil {
		return nil
	}
	fn, ok := fnStmt.(*ast.FunctionStmt)
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

func (p *parser) parseParams() []ast.Param {
	params := []ast.Param{}
	for {
		if p.curToken.Type != ast.TokenIdent && p.curToken.Type != ast.TokenIvar {
			p.errorExpected(p.curToken, "parameter name")
			return params
		}
		param := ast.Param{Name: p.curToken.Literal}
		if p.curToken.Type == ast.TokenIvar {
			param.IsIvar = true
			param.Name = strings.TrimPrefix(param.Name, "@")
		}
		if p.peekToken.Type == ast.TokenColon {
			p.nextToken()
			p.nextToken()
			param.Type = p.parseTypeExpr()
			if param.Type == nil {
				return params
			}
		}
		if p.peekToken.Type == ast.TokenAssign {
			p.nextToken()
			p.nextToken()
			param.DefaultVal = p.parseExpression(lowestPrec)
		}
		params = append(params, param)
		if p.peekToken.Type != ast.TokenComma {
			break
		}
		p.nextToken()
		p.nextToken()
	}
	return params
}

func (p *parser) parsePropertyDecl(kind ast.TokenType) ast.PropertyDecl {
	pos := p.curToken.Pos
	names := []string{}
	p.nextToken()
	if p.curToken.Type != ast.TokenIdent {
		p.errorExpected(p.curToken, "property name")
		return ast.PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), Position: pos}
	}
	names = append(names, p.curToken.Literal)
	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != ast.TokenIdent {
			p.errorExpected(p.curToken, "property name")
			break
		}
		names = append(names, p.curToken.Literal)
	}
	return ast.PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), Position: pos}
}

func (p *parser) parseExpressionOrAssignStatement() ast.Statement {
	expr := p.parseLineExpression(lowestPrec)
	if expr == nil {
		return nil
	}

	if p.canAttachPeekBlock() {
		p.nextToken()
		expr = p.callWithBlock(expr, p.parseBlockLiteral())
	}

	if p.peekToken.Type == ast.TokenAssign && isAssignable(expr) {
		pos := expr.Pos()
		p.nextToken()
		p.nextToken()
		value := p.parseExpressionWithBlock()
		return &ast.AssignStmt{Target: expr, Value: value, Position: pos}
	}

	return &ast.ExprStmt{Expr: expr, Position: expr.Pos()}
}

func (p *parser) parseExpressionWithBlock() ast.Expression {
	expr := p.parseLineExpression(lowestPrec)
	if expr == nil {
		return nil
	}
	if p.canAttachPeekBlock() {
		p.nextToken()
		return p.callWithBlock(expr, p.parseBlockLiteral())
	}
	return expr
}

func (p *parser) parseAssertStatement() ast.Statement {
	pos := p.curToken.Pos
	callee := &ast.Identifier{Name: p.curToken.Literal, Position: pos}
	args := []ast.Expression{}
	if p.peekToken.Type == ast.TokenEOF || p.peekToken.Type == ast.TokenEnd || p.peekToken.Type == ast.TokenElse || p.peekToken.Type == ast.TokenElsif || p.peekToken.Type == ast.TokenEnsure || p.peekToken.Type == ast.TokenRescue || p.peekToken.Pos.Line != pos.Line {
		return &ast.ExprStmt{Expr: callee, Position: pos}
	}
	p.nextToken()
	first := p.parseLineExpression(lowestPrec)
	if first != nil {
		args = append(args, first)
		for p.peekToken.Type == ast.TokenComma {
			p.nextToken()
			p.nextToken()
			args = append(args, p.parseLineExpression(lowestPrec))
		}
	}
	call := &ast.CallExpr{Callee: callee, Args: args, Position: pos}
	return &ast.ExprStmt{Expr: call, Position: pos}
}

func isAssignable(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.Identifier, *ast.MemberExpr, *ast.IndexExpr, *ast.IvarExpr, *ast.ClassVarExpr:
		return true
	default:
		return false
	}
}
