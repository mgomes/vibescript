package parser

import (
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseStatement() ast.Statement {
	var stmt ast.Statement
	switch p.curToken.Type {
	case ast.TokenDef:
		stmt = p.parseFunctionStatement()
	case ast.TokenClass:
		stmt = p.parseClassStatement()
	case ast.TokenEnum:
		stmt = p.parseEnumStatement()
	case ast.TokenExport:
		stmt = p.parseExportStatement()
	case ast.TokenPrivate:
		stmt = p.parsePrivateStatement()
	case ast.TokenReturn:
		stmt = p.parseReturnStatement()
	case ast.TokenRaise:
		stmt = p.parseRaiseStatement()
	case ast.TokenIf:
		stmt = p.parseIfStatement()
	case ast.TokenUnless:
		stmt = p.parseUnlessStatement()
	case ast.TokenFor:
		stmt = p.parseForStatement()
	case ast.TokenWhile:
		stmt = p.parseWhileStatement()
	case ast.TokenUntil:
		stmt = p.parseUntilStatement()
	case ast.TokenBreak:
		stmt = p.parseBreakStatement()
	case ast.TokenNext:
		stmt = p.parseNextStatement()
	case ast.TokenBegin:
		stmt = p.parseBeginStatement()
	case ast.TokenIdent:
		if p.curToken.Literal == "assert" {
			stmt = p.parseAssertStatement()
		} else {
			stmt = p.parseExpressionOrAssignStatement()
		}
	default:
		stmt = p.parseExpressionOrAssignStatement()
	}
	return p.parseStatementModifier(stmt)
}

func (p *parser) skipStatementSeparators() {
	for p.curToken.Type == ast.TokenSemicolon {
		p.nextToken()
	}
}

func (p *parser) parseStatementModifier(stmt ast.Statement) ast.Statement {
	if stmt == nil || !isStatementModifier(p.peekToken.Type) || p.peekToken.Pos.Line != p.curToken.Pos.Line {
		return stmt
	}

	modifier := p.peekToken
	if !canUseStatementModifier(stmt) {
		p.nextToken()
		p.nextToken()
		_ = p.parseLineExpression(lowestPrec)
		p.addParseError(modifier.Pos, fmt.Sprintf("modifier %s is only supported after expression or assignment statements", strings.ToLower(string(modifier.Type))))
		return stmt
	}

	p.nextToken()
	p.nextToken()
	condition := p.parseLineExpression(lowestPrec)
	if condition == nil {
		return nil
	}

	body := []ast.Statement{stmt}
	switch modifier.Type {
	case ast.TokenWhile:
		return &ast.WhileStmt{Condition: condition, Body: body, Position: modifier.Pos}
	case ast.TokenUntil:
		return &ast.UntilStmt{Condition: condition, Body: body, Position: modifier.Pos}
	case ast.TokenUnless:
		return &ast.IfStmt{Condition: condition, Alternate: body, Position: modifier.Pos}
	default:
		return stmt
	}
}

func isStatementModifier(tt ast.TokenType) bool {
	return tt == ast.TokenWhile || tt == ast.TokenUntil || tt == ast.TokenUnless
}

func canUseStatementModifier(stmt ast.Statement) bool {
	switch stmt.(type) {
	case *ast.AssignStmt, *ast.ExprStmt:
		return true
	default:
		return false
	}
}

func (p *parser) parseReturnStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.peekEndsStatement(pos) {
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
	if p.peekEndsStatement(pos) {
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
		p.skipStatementSeparators()
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

func (p *parser) parseUnlessStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLineExpression(lowestPrec)
	if condition == nil {
		return nil
	}

	p.nextToken()
	body := p.parseBlock(ast.TokenEnd, ast.TokenElse)

	var alternate []ast.Statement
	if p.curToken.Type == ast.TokenElse {
		p.nextToken()
		alternate = p.parseBlock(ast.TokenEnd)
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.IfStmt{Condition: condition, Consequent: alternate, Alternate: body, Position: pos}
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
	body := p.parseBlock(ast.TokenRescue, ast.TokenElse, ast.TokenEnsure, ast.TokenEnd)

	var rescueTy *ast.TypeExpr
	var rescueBinding string
	var rescuePos ast.Position
	var rescueBody []ast.Statement
	rescuePresent := false
	if p.curToken.Type == ast.TokenRescue {
		rescuePresent = true
		rescuePos = p.curToken.Pos
		var ok bool
		rescueTy, rescueBinding, ok = p.parseRescueClause(rescuePos)
		if !ok {
			return nil
		}
		p.nextToken()
		rescueBody = p.parseBlock(ast.TokenElse, ast.TokenEnsure, ast.TokenEnd)
	}

	var elseBody []ast.Statement
	if p.curToken.Type == ast.TokenElse {
		if !rescuePresent {
			p.addParseError(p.curToken.Pos, "begin else requires rescue")
			return nil
		}
		p.nextToken()
		elseBody = p.parseBlock(ast.TokenEnsure, ast.TokenEnd)
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

	return &ast.TryStmt{Body: body, RescueTy: rescueTy, RescueBinding: rescueBinding, RescuePosition: rescuePos, Rescue: rescueBody, Else: elseBody, Ensure: ensureBody, Position: pos}
}

func (p *parser) parseRescueClause(rescuePos ast.Position) (*ast.TypeExpr, string, bool) {
	if p.peekToken.Pos.Line != rescuePos.Line {
		return nil, "", true
	}

	var rescueTy *ast.TypeExpr
	switch p.peekToken.Type {
	case ast.TokenLParen:
		p.nextToken()
		p.nextToken()
		rescueTy = p.parseTypeExpr()
		if rescueTy == nil {
			return nil, "", false
		}
		if !p.validateRescueTypeExpr(rescueTy, rescuePos) {
			return nil, "", false
		}
		if !p.expectPeek(ast.TokenRParen) {
			return nil, "", false
		}
	case ast.TokenIdent:
		p.nextToken()
		rescueTy = p.parseTypeExpr()
		if rescueTy == nil {
			return nil, "", false
		}
		if !p.validateRescueTypeExpr(rescueTy, rescuePos) {
			return nil, "", false
		}
	case ast.TokenArrow:
	default:
		return nil, "", true
	}

	binding := ""
	if p.peekToken.Type == ast.TokenArrow && p.peekToken.Pos.Line == rescuePos.Line {
		var ok bool
		binding, ok = p.parseRescueBinding()
		if !ok {
			return nil, "", false
		}
	}
	return rescueTy, binding, true
}

func (p *parser) parseRescueBinding() (string, bool) {
	p.nextToken()
	if p.peekToken.Type != ast.TokenIdent {
		p.addParseError(p.peekToken.Pos, "rescue binding must be an identifier")
		return "", false
	}
	p.nextToken()
	return p.curToken.Literal, true
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
		p.skipStatementSeparators()
		if p.curToken.Type == ast.TokenEnd || p.curToken.Type == ast.TokenEOF {
			break
		}
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
		p.skipStatementSeparators()
		if p.curToken.Type == ast.TokenEnd || p.curToken.Type == ast.TokenEOF {
			break
		}
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
		p.skipStatementSeparators()
		if p.curToken.Type == ast.TokenEnd || p.curToken.Type == ast.TokenEOF {
			break
		}
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
	seenRest := false
	seenKeyword := false
	seenKeywordRest := false
	seenBlock := false
	for {
		param, paramPos, ok := p.parseParam()
		if !ok {
			return params
		}
		switch param.Kind {
		case ast.ParamNormal:
			if seenRest || seenKeyword || seenKeywordRest || seenBlock {
				p.addParseError(paramPos, "ordinary parameters must precede rest, keyword, keyword rest, and block capture parameters")
				return params
			}
		case ast.ParamRest:
			if seenRest {
				p.addParseError(paramPos, "duplicate rest parameter")
				return params
			}
			if seenKeyword || seenKeywordRest || seenBlock {
				p.addParseError(paramPos, "rest parameter must precede keyword, keyword rest, and block capture parameters")
				return params
			}
			seenRest = true
		case ast.ParamKeyword:
			if seenKeywordRest || seenBlock {
				p.addParseError(paramPos, "keyword parameters must precede keyword rest and block capture parameters")
				return params
			}
			seenKeyword = true
		case ast.ParamKeywordRest:
			if seenKeywordRest {
				p.addParseError(paramPos, "duplicate keyword rest parameter")
				return params
			}
			if seenBlock {
				p.addParseError(paramPos, "keyword rest parameter must precede block capture parameter")
				return params
			}
			seenKeywordRest = true
		case ast.ParamBlock:
			if seenBlock {
				p.addParseError(paramPos, "duplicate block capture parameter")
				return params
			}
			seenBlock = true
		}

		params = append(params, param)
		if p.peekToken.Type != ast.TokenComma {
			break
		}
		if param.Kind == ast.ParamBlock {
			p.addParseError(p.peekToken.Pos, "block capture parameter must be last")
			return params
		}
		p.nextToken()
		p.nextToken()
	}
	return params
}

func (p *parser) parseParam() (ast.Param, ast.Position, bool) {
	kind := ast.ParamNormal
	switch p.curToken.Type {
	case ast.TokenAsterisk:
		kind = ast.ParamRest
		if p.peekToken.Type == ast.TokenAsterisk {
			kind = ast.ParamKeywordRest
			p.nextToken()
		}
		p.nextToken()
	case ast.TokenPower:
		kind = ast.ParamKeywordRest
		p.nextToken()
	case ast.TokenAmpersand:
		kind = ast.ParamBlock
		p.nextToken()
	}

	if p.curToken.Type != ast.TokenIdent && (kind != ast.ParamNormal || p.curToken.Type != ast.TokenIvar) {
		p.errorExpected(p.curToken, parameterNameExpectation(kind))
		return ast.Param{}, ast.Position{}, false
	}
	pos := p.curToken.Pos
	param := ast.Param{Name: p.curToken.Literal, Kind: kind}
	if p.curToken.Type == ast.TokenIvar {
		param.IsIvar = true
		param.Name = strings.TrimPrefix(param.Name, "@")
	}
	if p.peekToken.Type == ast.TokenColon {
		p.nextToken()
		if kind == ast.ParamNormal && !param.IsIvar && (p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenRParen) {
			param.Kind = ast.ParamKeyword
			return param, pos, true
		}
		p.nextToken()
		param.Type = p.parseTypeExpr()
		if param.Type == nil {
			return ast.Param{}, ast.Position{}, false
		}
	}
	if p.peekToken.Type == ast.TokenAssign {
		if kind != ast.ParamNormal {
			p.addParseError(p.peekToken.Pos, "capture parameters cannot have default values")
			return ast.Param{}, ast.Position{}, false
		}
		p.nextToken()
		p.nextToken()
		param.DefaultVal = p.parseExpression(lowestPrec)
	}
	return param, pos, true
}

func parameterNameExpectation(kind ast.ParamKind) string {
	switch kind {
	case ast.ParamRest:
		return "rest parameter name"
	case ast.ParamKeywordRest:
		return "keyword rest parameter name"
	case ast.ParamBlock:
		return "block capture parameter name"
	default:
		return "parameter name"
	}
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
	if p.curToken.Type == ast.TokenAsterisk {
		target := p.parseDestructureTargetList(nil)
		if target == nil {
			return nil
		}
		return p.parseAssignmentValue(target)
	}

	expr := p.parseLineExpression(lowestPrec)
	if expr == nil {
		return nil
	}

	if p.canAttachPeekBlock() {
		p.nextToken()
		expr = p.callWithBlock(expr, p.parseBlockLiteral())
	}

	if p.peekToken.Type == ast.TokenComma {
		target := p.parseDestructureTargetList(expr)
		if target == nil {
			return nil
		}
		return p.parseAssignmentValue(target)
	}

	if isAssignmentOperator(p.peekToken.Type) && isAssignable(expr) {
		return p.parseAssignmentValue(expr)
	}

	return &ast.ExprStmt{Expr: expr, Position: expr.Pos()}
}

func (p *parser) parseAssignmentValue(target ast.Expression) ast.Statement {
	operatorToken := p.peekToken.Type
	if !isAssignmentOperator(operatorToken) {
		p.addParseError(p.curToken.Pos, "parallel assignment targets require '='")
		return nil
	}
	if operatorToken != ast.TokenAssign {
		if _, ok := target.(*ast.DestructureTarget); ok {
			p.addParseErrorSpan(p.peekToken.Pos, tokenEnd(p.peekToken), "compound assignment is not supported for destructuring targets")
			p.recoverAssignmentRemainder()
			return nil
		}
	}

	pos := target.Pos()
	p.nextToken()
	p.nextToken()
	value := p.parseExpressionWithBlock()
	return &ast.AssignStmt{Target: target, Value: value, Operator: compoundAssignmentOperator(operatorToken), Position: pos}
}

func (p *parser) recoverAssignmentRemainder() {
	startLine := p.peekToken.Pos.Line
	for p.peekToken.Type != ast.TokenEOF && p.peekToken.Type != ast.TokenSemicolon && p.peekToken.Pos.Line == startLine {
		p.nextToken()
	}
}

func isAssignmentOperator(tt ast.TokenType) bool {
	return tt == ast.TokenAssign || compoundAssignmentOperator(tt) != ""
}

func compoundAssignmentOperator(tt ast.TokenType) ast.TokenType {
	switch tt {
	case ast.TokenPlusAssign:
		return ast.TokenPlus
	case ast.TokenMinusAssign:
		return ast.TokenMinus
	case ast.TokenAsteriskAssign:
		return ast.TokenAsterisk
	case ast.TokenPowerAssign:
		return ast.TokenPower
	case ast.TokenSlashAssign:
		return ast.TokenSlash
	case ast.TokenPercentAssign:
		return ast.TokenPercent
	default:
		return ""
	}
}

func (p *parser) parseDestructureTargetList(first ast.Expression) ast.Expression {
	var pos ast.Position
	elements := []ast.DestructureElement{}
	seenRest := false

	if first != nil {
		if !isAssignable(first) {
			p.addParseError(first.Pos(), "invalid destructuring assignment target")
			return nil
		}
		pos = first.Pos()
		elements = append(elements, ast.DestructureElement{Target: first})
	} else {
		element, ok := p.parseDestructureElement()
		if !ok {
			return nil
		}
		pos = element.Target.Pos()
		seenRest = element.Rest
		elements = append(elements, element)
	}

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		element, ok := p.parseDestructureElement()
		if !ok {
			return nil
		}
		if element.Rest {
			if seenRest {
				p.addParseError(element.Target.Pos(), "duplicate rest assignment target")
				return nil
			}
			seenRest = true
		}
		elements = append(elements, element)
	}

	return &ast.DestructureTarget{Elements: elements, Position: pos}
}

func (p *parser) parseDestructureElement() (ast.DestructureElement, bool) {
	rest := false
	if p.curToken.Type == ast.TokenAsterisk {
		rest = true
		p.nextToken()
	}

	target := p.parseDestructureSingleTarget()
	if target == nil {
		return ast.DestructureElement{}, false
	}
	return ast.DestructureElement{Target: target, Rest: rest}, true
}

func (p *parser) parseDestructureSingleTarget() ast.Expression {
	switch p.curToken.Type {
	case ast.TokenLParen:
		return p.parseNestedDestructureTarget(ast.TokenRParen, ")")
	case ast.TokenLBracket:
		return p.parseNestedDestructureTarget(ast.TokenRBracket, "]")
	default:
		target := p.parseLineExpression(lowestPrec)
		if target == nil {
			return nil
		}
		if !isAssignable(target) {
			p.addParseError(target.Pos(), "invalid destructuring assignment target")
			return nil
		}
		return target
	}
}

func (p *parser) parseNestedDestructureTarget(stop ast.TokenType, _ string) ast.Expression {
	pos := p.curToken.Pos
	if p.peekToken.Type == stop {
		p.errorExpected(p.peekToken, "destructuring assignment target")
		return nil
	}

	p.nextToken()
	target := p.parseDestructureTargetList(nil)
	if target == nil {
		return nil
	}
	if !p.expectPeek(stop) {
		return nil
	}
	destructure, ok := target.(*ast.DestructureTarget)
	if !ok {
		return nil
	}
	return &ast.DestructureTarget{Elements: destructure.Elements, Position: pos}
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
	if p.peekEndsStatement(pos) {
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

func (p *parser) peekEndsStatement(pos ast.Position) bool {
	switch p.peekToken.Type {
	case ast.TokenEOF, ast.TokenSemicolon, ast.TokenEnd, ast.TokenElse, ast.TokenElsif, ast.TokenEnsure, ast.TokenRescue:
		return true
	default:
		return p.peekToken.Pos.Line != pos.Line
	}
}

func isAssignable(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.Identifier, *ast.MemberExpr, *ast.IndexExpr, *ast.IvarExpr, *ast.ClassVarExpr:
		return true
	default:
		return false
	}
}
