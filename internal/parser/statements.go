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
	case ast.TokenIf:
		return &ast.IfStmt{Condition: condition, Consequent: body, Position: modifier.Pos}
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
	return tt == ast.TokenIf || tt == ast.TokenWhile || tt == ast.TokenUntil || tt == ast.TokenUnless
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
	p.consumeConditionalBodySeparator()
	consequent := p.parseBlock(ast.TokenEnd, ast.TokenElse, ast.TokenElsif)

	var elseifClauses []*ast.IfStmt
	for p.curToken.Type == ast.TokenElsif {
		p.nextToken()
		cond := p.parseLineExpression(lowestPrec)
		if cond == nil {
			return nil
		}
		p.nextToken()
		p.consumeConditionalBodySeparator()
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
	p.consumeConditionalBodySeparator()
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

func (p *parser) consumeConditionalBodySeparator() {
	if p.curToken.Type == ast.TokenThen {
		p.nextToken()
	}
	p.skipStatementSeparators()
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
	iterable := p.parseLoopConditionExpression()
	if iterable == nil {
		return nil
	}

	p.advanceToLoopBody()
	// The iterator binds a local in the surrounding scope, so register it
	// before parsing the body for name-sensitive parsing decisions such as
	// percent-literal vs modulo disambiguation.
	p.declareLocal(iterator)
	body := p.parseBlock(ast.TokenEnd)

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.ForStmt{Iterator: iterator, Iterable: iterable, Body: body, Position: pos}
}

func (p *parser) parseWhileStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLoopConditionExpression()
	if condition == nil {
		return nil
	}

	p.advanceToLoopBody()
	body := p.parseBlock(ast.TokenEnd)

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.WhileStmt{Condition: condition, Body: body, Position: pos}
}

func (p *parser) parseUntilStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLoopConditionExpression()
	if condition == nil {
		return nil
	}

	p.advanceToLoopBody()
	body := p.parseBlock(ast.TokenEnd)

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return &ast.UntilStmt{Condition: condition, Body: body, Position: pos}
}

func (p *parser) parseLoopConditionExpression() ast.Expression {
	return p.parseLineExpressionUntil(lowestPrec, ast.TokenDo)
}

func (p *parser) advanceToLoopBody() {
	if p.peekToken.Type == ast.TokenDo && p.peekToken.Pos.Line == p.curToken.Pos.Line {
		p.nextToken()
	}
	p.nextToken()
}

func (p *parser) parseBreakStatement() ast.Statement {
	pos := p.curToken.Pos
	if p.peekEndsStatement(pos) {
		return &ast.BreakStmt{Position: pos}
	}
	p.nextToken()
	value := p.parseLineExpression(lowestPrec)
	if value == nil {
		return nil
	}
	return &ast.BreakStmt{Value: value, Position: pos}
}

func (p *parser) parseNextStatement() ast.Statement {
	return &ast.NextStmt{Position: p.curToken.Pos}
}

func (p *parser) parseBeginStatement() ast.Statement {
	pos := p.curToken.Pos
	p.nextToken()
	body := p.parseBlock(ast.TokenRescue, ast.TokenElse, ast.TokenEnsure, ast.TokenEnd)
	return p.parseRescueElseEnsureTail(pos, body, "begin")
}

func (p *parser) parseRescueElseEnsureTail(pos ast.Position, body []ast.Statement, owner string) ast.Statement {
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
			p.recoverToBlockEnd()
			return nil
		}
		p.nextToken()
		// The rescue binding is local only within the rescue body: at runtime
		// it lives in a child env and is undefined afterward. Other locals
		// assigned in the body belong to the surrounding scope, so parse the
		// body in the current scope and remove only the binding afterward
		// (unless it was already a local before the handler).
		bindingWasLocal := rescueBinding != "" && p.localDeclaredInTop(rescueBinding)
		if rescueBinding != "" {
			p.declareLocal(rescueBinding)
		}
		rescueBody = p.parseBlock(ast.TokenElse, ast.TokenEnsure, ast.TokenEnd)
		if rescueBinding != "" && !bindingWasLocal {
			p.undeclareLocal(rescueBinding)
		}
	}

	var elseBody []ast.Statement
	if p.curToken.Type == ast.TokenElse {
		if !rescuePresent {
			p.addParseError(p.curToken.Pos, fmt.Sprintf("%s else requires rescue", owner))
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
		p.addParseError(pos, fmt.Sprintf("%s requires rescue and/or ensure", owner))
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
			p.recoverRescueHeaderRemainder(rescuePos.Line)
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
			p.recoverRescueHeaderRemainder(rescuePos.Line)
			return nil, "", false
		}
	case ast.TokenArrow:
	case ast.TokenThinArrow:
		p.addParseError(p.peekToken.Pos, "rescue binding must use =>")
		p.recoverRescueHeaderRemainder(rescuePos.Line)
		return nil, "", false
	default:
		return nil, "", true
	}

	binding := ""
	if p.peekToken.Type == ast.TokenThinArrow && p.peekToken.Pos.Line == rescuePos.Line {
		p.addParseError(p.peekToken.Pos, "rescue binding must use =>")
		p.recoverRescueHeaderRemainder(rescuePos.Line)
		return nil, "", false
	}
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
		p.recoverRescueHeaderRemainder(p.curToken.Pos.Line)
		return "", false
	}
	p.nextToken()
	return p.curToken.Literal, true
}

func (p *parser) recoverRescueHeaderRemainder(line int) {
	for p.peekToken.Type != ast.TokenEOF && p.peekToken.Pos.Line == line && p.peekToken.Type != ast.TokenSemicolon {
		p.nextToken()
	}
}

func (p *parser) recoverToBlockEnd() {
	depth := 0
	pendingDoDepth := -1
	pendingDoLine := -1
	for p.curToken.Type != ast.TokenEOF {
		p.nextToken()
		if pendingDoLine != -1 && p.curToken.Pos.Line != pendingDoLine {
			pendingDoDepth = -1
			pendingDoLine = -1
		}
		switch p.curToken.Type {
		case ast.TokenDef, ast.TokenClass, ast.TokenEnum, ast.TokenBegin, ast.TokenIf, ast.TokenUnless, ast.TokenCase:
			depth++
		case ast.TokenFor, ast.TokenWhile, ast.TokenUntil:
			depth++
			pendingDoDepth = depth
			pendingDoLine = p.curToken.Pos.Line
		case ast.TokenDo:
			if pendingDoDepth == depth && pendingDoLine == p.curToken.Pos.Line {
				pendingDoDepth = -1
				pendingDoLine = -1
				break
			}
			depth++
		case ast.TokenEnd:
			if depth == 0 {
				return
			}
			depth--
			if pendingDoDepth > depth {
				pendingDoDepth = -1
				pendingDoLine = -1
			}
		}
	}
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

	// Push the function scope before parsing parameters so each parameter is
	// visible to later parameters' default values, matching the runtime which
	// binds earlier parameters before evaluating later defaults. The scope is
	// also kept active through any rescue/else/ensure tail so the tail resolves
	// function-body locals for name-sensitive parsing such as percent-literal
	// vs modulo disambiguation.
	p.pushLocalScope(nil, true)
	defer p.popLocalScope()

	params := []ast.Param{}
	var returnTy *ast.TypeExpr
	// Optional parens on the same line.
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
	} else if p.curToken.Pos.Line == pos.Line && isFunctionParamStart(p.curToken.Type) {
		params = p.parseParamsWithOptions(paramParseOptions{lineLimitedDefaults: true})
		p.nextToken()
	}
	if p.curToken.Type == ast.TokenThinArrow {
		p.nextToken()
		returnTy = p.parseTypeExpr()
		if returnTy == nil {
			return nil
		}
		p.nextToken()
	}
	body := p.parseBlock(ast.TokenRescue, ast.TokenElse, ast.TokenEnsure, ast.TokenEnd)
	switch p.curToken.Type {
	case ast.TokenRescue, ast.TokenElse, ast.TokenEnsure:
		tryStmt := p.parseRescueElseEnsureTail(pos, body, "function")
		if tryStmt == nil {
			return nil
		}
		body = []ast.Statement{tryStmt}
	case ast.TokenEnd:
	default:
		p.errorExpected(p.curToken, "end")
		return nil
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
	if p.peekToken.Type == ast.TokenShovel {
		p.addParseError(p.peekToken.Pos, "class << self definitions are not supported; use def self.name")
		return nil
	}
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

type paramParseOptions struct {
	lineLimitedDefaults bool
}

func (p *parser) parseParams() []ast.Param {
	return p.parseParamsWithOptions(paramParseOptions{})
}

func (p *parser) parseParamsWithOptions(options paramParseOptions) []ast.Param {
	params := []ast.Param{}
	seenRest := false
	seenKeyword := false
	seenKeywordRest := false
	seenBlock := false
	for {
		param, paramPos, ok := p.parseParam(options)
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
		// Declare the parameter as a local now so later parameters' default
		// values resolve it (see parseFunctionStatement for why the scope is
		// already active here).
		p.declareParamLocal(param)
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

func (p *parser) parseParam(options paramParseOptions) (ast.Param, ast.Position, bool) {
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
		if kind == ast.ParamNormal && !param.IsIvar && p.peekEndsRequiredKeywordParam(options) {
			param.Kind = ast.ParamKeyword
			return param, pos, true
		}
		// `name: default` declares an optional keyword-only parameter, while
		// `name: Type` declares a typed positional parameter. The token after
		// the colon disambiguates the two forms; see colonIntroducesKeywordDefault.
		if kind == ast.ParamNormal && !param.IsIvar && p.colonIntroducesKeywordDefault(options) {
			param.Kind = ast.ParamKeyword
			p.nextToken()
			if options.lineLimitedDefaults {
				param.DefaultVal = p.parseLineExpressionUntil(lowestPrec, ast.TokenComma)
			} else {
				param.DefaultVal = p.parseExpression(lowestPrec)
			}
			return param, pos, true
		}
		p.nextToken()
		param.Type = p.parseTypeExpr()
		if param.Type == nil {
			return ast.Param{}, ast.Position{}, false
		}
		if err := captureParamTypeError(param); err != "" {
			p.addParseError(param.Type.Position, err)
			return ast.Param{}, ast.Position{}, false
		}
		if kind == ast.ParamNormal && !param.IsIvar && p.peekToken.Type == ast.TokenColon {
			p.nextToken()
			if !p.peekEndsRequiredKeywordParam(options) {
				p.addParseError(p.curToken.Pos, "typed required keyword parameter must end after trailing ':'")
				return ast.Param{}, ast.Position{}, false
			}
			param.Kind = ast.ParamKeyword
		}
	}
	if p.peekToken.Type == ast.TokenAssign {
		if kind != ast.ParamNormal {
			p.addParseError(p.peekToken.Pos, "capture parameters cannot have default values")
			return ast.Param{}, ast.Position{}, false
		}
		p.nextToken()
		p.nextToken()
		if options.lineLimitedDefaults {
			param.DefaultVal = p.parseLineExpressionUntil(lowestPrec, ast.TokenComma)
		} else {
			param.DefaultVal = p.parseExpression(lowestPrec)
		}
	}
	return param, pos, true
}

func captureParamTypeError(param ast.Param) string {
	switch param.Kind {
	case ast.ParamRest:
		if !restCaptureTypeAcceptsArray(param.Type) {
			return fmt.Sprintf("rest parameter %s captures an array; annotate it as array<...> or any", param.Name)
		}
	case ast.ParamKeywordRest:
		if !keywordRestCaptureTypeAcceptsHash(param.Type) {
			return fmt.Sprintf("keyword rest parameter %s captures a hash; annotate it as hash<...>, object, a shape type, or any", param.Name)
		}
	}
	return ""
}

func restCaptureTypeAcceptsArray(ty *ast.TypeExpr) bool {
	if ty == nil {
		return true
	}
	switch ty.Kind {
	case ast.TypeAny, ast.TypeArray:
		return true
	case ast.TypeUnion:
		for _, option := range ty.Union {
			if restCaptureTypeAcceptsArray(option) {
				return true
			}
		}
	}
	return false
}

func keywordRestCaptureTypeAcceptsHash(ty *ast.TypeExpr) bool {
	if ty == nil {
		return true
	}
	switch ty.Kind {
	case ast.TypeAny, ast.TypeHash, ast.TypeShape:
		return true
	case ast.TypeUnion:
		for _, option := range ty.Union {
			if keywordRestCaptureTypeAcceptsHash(option) {
				return true
			}
		}
	}
	return false
}

func isFunctionParamStart(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenIdent, ast.TokenIvar, ast.TokenAsterisk, ast.TokenPower, ast.TokenAmpersand:
		return true
	default:
		return false
	}
}

func (p *parser) peekEndsRequiredKeywordParam(options paramParseOptions) bool {
	switch p.peekToken.Type {
	case ast.TokenComma, ast.TokenRParen:
		return true
	case ast.TokenThinArrow, ast.TokenSemicolon, ast.TokenEOF:
		return options.lineLimitedDefaults
	default:
		return options.lineLimitedDefaults && p.peekToken.Pos.Line != p.curToken.Pos.Line
	}
}

// colonIntroducesKeywordDefault reports whether the token sequence following a
// parameter's `:` introduces an optional keyword-only default value rather than
// a type annotation. It is consulted only after peekEndsRequiredKeywordParam has
// ruled out the bare `name:` required-keyword form, so the colon is always
// followed by at least one more token here.
//
// Vibescript overloads the post-colon position: `name: Type` is a typed
// positional parameter while `name: default` is an optional keyword-only
// parameter. The decision rests on whether the colon is followed by something
// that can begin a type annotation:
//
//   - A value-only token (a literal, prefix operator, grouping, etc.) cannot
//     start a type, so it begins a default value.
//   - `nil` reads as a default value (`a: nil`), matching Ruby and the stdlib's
//     documented optional keywords; a bare `nil` positional type is useless.
//     A `|` continuation keeps it a type, so a nil-leading union annotation
//     (`a: nil | int`) parses as the union rather than a `nil` default.
//   - `{` opens either a shape type (`name: { field: Type }`) or a hash literal
//     default (`name: { key: value }`). The two share a `{ name: X }` skeleton,
//     so a bounded speculative parse decides: the brace group reads as a shape
//     type only when it parses as one and is followed by a parameter boundary,
//     otherwise it is a hash default.
//   - A bare identifier is the genuinely ambiguous case. It reads as a type when
//     it stands alone at a parameter boundary or continues as a type (`<` for
//     generic arguments on a container type, `|` for a union). Otherwise the
//     identifier begins an expression (`a + 1`, `helper(x)`, `obj.value`) and is
//     treated as a default value.
func (p *parser) colonIntroducesKeywordDefault(options paramParseOptions) bool {
	switch p.peekToken.Type {
	case ast.TokenNil:
		// A bare `nil` is a default value, but a `|` continuation makes it the
		// head of a nil-leading union type annotation (`a: nil | int`).
		return p.peekPeek.Type != ast.TokenPipe
	case ast.TokenLBrace:
		return !p.bracedGroupIsShapeType(options)
	case ast.TokenIdent:
		return p.identAfterColonStartsExpression(options)
	default:
		return prefixParserKind(p.peekToken.Type) != prefixParserNone
	}
}

// bracedGroupIsShapeType reports whether the brace group beginning at
// peekToken is a shape type rather than a hash literal default. The two
// share a `{ field: X }` skeleton, so a bounded speculative parse decides:
// the group is a shape type only when the whole group parses as one,
// because a shape type's field values are themselves types all the way
// down. `{ x: int }` parses as a shape, while `{ retry: 3 }` does not (an
// integer is not a type) and `{ a: { b: 1 } }` does not (the nested
// integer is not a type), so both are hash defaults.
//
// A clean shape parse is necessary but not sufficient: a hash default
// whose values happen to be type names (`{ x: int }.merge(...)`) parses
// as a shape but continues with a postfix expression. The group is a
// shape type only when it also reaches a parameter boundary (`,` `)` `=`,
// or the line-limited terminators), so a postfix continuation marks the
// group as a hash default instead.
//
// The empty group `{}` is treated as a hash default rather than an empty
// shape type. An empty shape type as a parameter annotation is degenerate
// (it accepts only an empty hash), whereas `opts: {}` is the common
// Ruby-style empty-hash default. An empty *nested* shape is degenerate for
// the same reason: `{ headers: {} }` reads as a shape whose `headers` field
// accepts only an empty hash, but the Ruby-style intent is the hash default
// `def f(opts: { headers: {} })`. shapeHasEmptyNestedShape marks such groups
// as hash defaults too, mirroring the top-level `{}` case at any depth.
//
// A clean shape parse with a bare `nil` field type is likewise degenerate:
// `{ previous: nil }` reads as a shape whose `previous` field accepts only
// nil, but the Ruby-style intent is the hash default
// `def f(opts: { previous: nil })`. shapeHasDegenerateNilField marks such
// groups as hash defaults; a nullable union field (`{ previous: int | nil }`)
// is a legitimate shape and is left untouched.
//
// The parser state is fully restored afterward regardless of the outcome,
// leaving the real parse to proceed from the colon.
func (p *parser) bracedGroupIsShapeType(options paramParseOptions) bool {
	if p.peekPeek.Type == ast.TokenRBrace {
		return false
	}

	saved := p.snapshot()
	defer p.restore(saved)

	p.nextToken()
	shape := p.parseTypeExpr()
	if shape == nil {
		// A clean failure where the contents were not types (`{ retry: 3 }`)
		// is a hash default. A structural shape error (`{ id: string, id: int }`)
		// instead has field values that all parsed as types, so the braces are a
		// malformed shape annotation, not a hash default: route them to the type
		// path so the real parse re-emits the shape diagnostic rather than
		// silently turning a typed positional parameter into a keyword default.
		return p.shapeStructurallyInvalid
	}
	if len(p.errors) != saved.errorCount {
		return false
	}
	if shapeHasDegenerateNilField(shape) {
		return false
	}
	if shapeHasEmptyNestedShape(shape) {
		return false
	}
	if p.shapeFieldNamesLocalValue(shape) {
		return false
	}
	return p.typeAnnotationBoundaryFollows(options)
}

// shapeFieldNamesLocalValue reports whether ty is a shape type with a bare
// identifier field type that names a local value in scope, looking through
// nested shapes.
//
// A bare identifier field such as the `a` in `{ sum: a }` parses cleanly as a
// type atom, so the speculative shape parse alone cannot tell `def f(opts: {
// status: pending })` (a shape whose `status` field is the `pending` enum type)
// from `def g(a:, b: { sum: a })` (a hash default whose `sum` value references
// the earlier keyword parameter `a`). When the identifier names a value already
// in scope it is a value reference, so the brace group is a hash default rather
// than a shape type. This mirrors identAfterColonStartsExpression and
// identLessThanStartsExpression, which already treat a bare identifier naming a
// local value as the head of a default expression.
//
// A bare identifier whose spelling matches a built-in type (such as the `time`
// in `def f(time:, opts: { start: time })`) parses to that built-in's kind
// (TypeTime here) while keeping the original literal in Name, so the check
// inspects the name rather than the kind. This covers common parameter names
// like time, string, int, hash, and array.
//
// The check targets bare atoms only. A union field (`{ x: a | b }`) stays a
// type because `|` continues a type annotation; a nullable atom (`{ x: a? }`)
// is type syntax; and an identifier appearing as a generic type argument
// (`{ x: array<a> }`) is a genuine type, matching how those forms are
// disambiguated outside shapes.
func (p *parser) shapeFieldNamesLocalValue(ty *ast.TypeExpr) bool {
	if ty == nil || ty.Kind != ast.TypeShape {
		return false
	}
	for _, fieldType := range ty.Shape {
		if fieldType == nil {
			continue
		}
		if p.typeAtomNamesLocalValue(fieldType) {
			return true
		}
		if p.shapeFieldNamesLocalValue(fieldType) {
			return true
		}
	}
	return false
}

// typeAtomNamesLocalValue reports whether ty is a bare named type atom whose
// spelling names a local value in scope. A bare atom is a single identifier
// with no type modifiers: it carries no generic arguments, is not nullable, and
// is neither a shape nor a union (those forms are unambiguous type syntax). The
// spelling is checked rather than the resolved kind so that an identifier
// matching a built-in type name (resolved to that built-in's kind) is still
// recognized as a value reference when it names a local.
func (p *parser) typeAtomNamesLocalValue(ty *ast.TypeExpr) bool {
	if ty == nil || ty.Name == "" || ty.Nullable || len(ty.TypeArgs) > 0 {
		return false
	}
	switch ty.Kind {
	case ast.TypeShape, ast.TypeUnion:
		return false
	default:
		return p.isLocalName(ty.Name)
	}
}

// bracedFieldIsHashDefault reports whether a braced-group field value, parsed as
// a type expression during the speculative shape parse, actually reads as a hash
// default rather than a shape field type. A value qualifies when it is:
//
//   - a bare identifier naming a local value (typeAtomNamesLocalValue),
//   - a bare `nil` atom (the degenerate field type recognized by
//     shapeHasDegenerateNilField),
//   - an empty shape `{}` (the degenerate empty-hash default), or
//   - a nested shape that is itself a hash default, i.e. one with a degenerate
//     `nil` field (shapeHasDegenerateNilField), an empty nested shape
//     (shapeHasEmptyNestedShape), or a field naming a local value
//     (shapeFieldNamesLocalValue).
//
// The nested-shape cases mirror the disambiguation bracedGroupIsShapeType
// applies to the whole group, so a nested value like `{ previous: nil }` or
// `{ sum: a }` is recognized as a hash default at any depth, not only when it is
// the outermost group.
//
// parseTypeShape uses it on the repeated values of a duplicate key so that a
// group like `{ previous: nil, previous: nil }`, `{ x: a, x: a }`, or
// `{ x: { previous: nil }, x: { previous: nil } }` is left to fall back to a
// hash default instead of being marked a structural shape error.
func (p *parser) bracedFieldIsHashDefault(ty *ast.TypeExpr) bool {
	if ty == nil {
		return false
	}
	if ty.Kind == ast.TypeNil {
		return true
	}
	if typeIsEmptyShape(ty) {
		return true
	}
	if p.typeAtomNamesLocalValue(ty) {
		return true
	}
	return shapeHasDegenerateNilField(ty) ||
		shapeHasEmptyNestedShape(ty) ||
		p.shapeFieldNamesLocalValue(ty)
}

// shapeHasDegenerateNilField reports whether ty is a shape type with a field
// whose type is the bare `nil` atom, looking through nested shapes.
//
// A `nil`-only field type accepts solely the value nil, which is degenerate as
// a positional annotation, so a group like `{ previous: nil }` is the common
// Ruby-style hash default (`def f(opts: { previous: nil })`) rather than a
// shape type. The check targets the bare atom only: a nullable union such as
// `{ previous: int | nil }` is a legitimate shape field and is left untouched.
func shapeHasDegenerateNilField(ty *ast.TypeExpr) bool {
	if ty == nil || ty.Kind != ast.TypeShape {
		return false
	}
	for _, fieldType := range ty.Shape {
		if fieldType == nil {
			continue
		}
		if fieldType.Kind == ast.TypeNil || shapeHasDegenerateNilField(fieldType) {
			return true
		}
	}
	return false
}

// typeIsEmptyShape reports whether ty is the empty shape `{}`, i.e. a shape
// type with no fields. An empty shape is degenerate as a parameter annotation
// (it accepts only an empty hash), so it is the Ruby-style empty-hash default
// rather than a meaningful shape type, matching the top-level `{}` handling in
// bracedGroupIsShapeType.
func typeIsEmptyShape(ty *ast.TypeExpr) bool {
	return ty != nil && ty.Kind == ast.TypeShape && len(ty.Shape) == 0
}

// shapeHasEmptyNestedShape reports whether ty is a shape type with a field
// whose type is an empty shape `{}`, looking through nested shapes.
//
// An empty shape field type accepts solely an empty hash, which is degenerate
// as a positional annotation, so a group like `{ headers: {} }` is the common
// Ruby-style hash default (`def f(opts: { headers: {} })`) rather than a shape
// type. This mirrors shapeHasDegenerateNilField for the empty-shape case,
// extending the top-level `{}` handling in bracedGroupIsShapeType to any depth.
func shapeHasEmptyNestedShape(ty *ast.TypeExpr) bool {
	if ty == nil || ty.Kind != ast.TypeShape {
		return false
	}
	for _, fieldType := range ty.Shape {
		if fieldType == nil {
			continue
		}
		if typeIsEmptyShape(fieldType) || shapeHasEmptyNestedShape(fieldType) {
			return true
		}
	}
	return false
}

// typeAnnotationBoundaryFollows reports whether peekToken terminates a
// parameter's type annotation. Valid terminators are a comma or closing
// paren (the next parameter or the list end), an `=` introducing a
// `name: Type = default` positional default, and, for line-limited
// parameter lists, the constructs that end such a list.
func (p *parser) typeAnnotationBoundaryFollows(options paramParseOptions) bool {
	switch p.peekToken.Type {
	case ast.TokenComma, ast.TokenRParen, ast.TokenAssign:
		return true
	case ast.TokenThinArrow, ast.TokenSemicolon, ast.TokenEOF:
		return options.lineLimitedDefaults
	default:
		return options.lineLimitedDefaults && p.peekToken.Pos.Line != p.curToken.Pos.Line
	}
}

// identAfterColonStartsExpression reports whether an identifier that follows a
// parameter's `:` begins a default-value expression rather than a bare type
// name. The token after the identifier decides: a parameter boundary keeps the
// identifier a standalone type, `|` continues it as a union type, `<` is
// disambiguated by identLessThanStartsExpression on whether the identifier is a
// value, and anything else (other binary operators, calls, member access,
// indexing) makes it the head of an expression.
func (p *parser) identAfterColonStartsExpression(options paramParseOptions) bool {
	switch p.peekPeek.Type {
	case ast.TokenComma, ast.TokenRParen, ast.TokenAssign, ast.TokenPipe, ast.TokenColon:
		// A boundary (`,` `)`), an `=` introducing a `name: Type = default`
		// positional default, a `|` union continuation, or a trailing `:` for
		// typed required keywords (`name: Type:`) all keep the identifier a type
		// name.
		return false
	case ast.TokenLT:
		return p.identLessThanStartsExpression()
	case ast.TokenThinArrow, ast.TokenSemicolon, ast.TokenEOF:
		return !options.lineLimitedDefaults
	default:
		if options.lineLimitedDefaults && p.peekPeek.Pos.Line != p.peekToken.Pos.Line {
			return false
		}
		return true
	}
}

// identLessThanStartsExpression reports whether `ident <` (with ident at
// peekToken) begins a default-value expression rather than continuing a
// generic type annotation.
//
// A built-in generic container name (`array`, `hash`, `object`) always
// continues as a type: `<` opens its type arguments and nothing else can.
// This takes precedence over any value local that shadows the name, so
// `def f(array, values: array<int>)` keeps `array<int>` a generic type
// even though an earlier parameter is named `array`. Built-in generic
// type parsing is never shadowed by value locals.
//
// Otherwise the `<` is a comparison only when the identifier names a
// value, i.e. a local already in scope such as an earlier keyword
// parameter, so `def f(limit:, ok: limit < 10)` reads as a default
// expression. Failing both, the identifier is a (scalar or enum) type
// name and `<` produces the clear "does not accept type arguments"
// diagnostic rather than a misparsed comparison.
func (p *parser) identLessThanStartsExpression() bool {
	if isGenericContainerTypeName(p.peekToken.Literal) {
		return false
	}
	return p.isLocalName(p.peekToken.Literal)
}

// isGenericContainerTypeName reports whether name resolves to a built-in
// container type that accepts angle-bracket type arguments (`array<...>`,
// `hash<...>`, `object<...>`). These are the only types parseTypeAtom lets
// carry type arguments, so a following `<` always continues the type.
func isGenericContainerTypeName(name string) bool {
	kind, _ := resolveType(name)
	return kind == ast.TypeArray || kind == ast.TypeHash
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
	decl := ast.PropertyDecl{Names: names, Kind: strings.ToLower(string(kind)), Position: pos}
	if p.peekToken.Type == ast.TokenColon {
		p.nextToken()
		p.nextToken()
		decl.Type = p.parseTypeExpr()
	}
	return decl
}

func (p *parser) parseExpressionOrAssignStatement() ast.Statement {
	if p.curToken.Type == ast.TokenAsterisk {
		target := p.parseDestructureTargetList(nil, false)
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
		target := p.parseDestructureTargetList(expr, false)
		if target == nil {
			return nil
		}
		return p.parseAssignmentValue(target)
	}

	if isAssignmentOperator(p.peekToken.Type) {
		if pos, ok := safeNavigationPos(expr); ok {
			p.addParseError(pos, "safe navigation cannot be used as an assignment target")
			p.recoverAssignmentRemainder()
			return nil
		}
		if isAssignable(expr) {
			return p.parseAssignmentValue(expr)
		}
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
	stmt := &ast.AssignStmt{Target: target, Value: value, Operator: compoundAssignmentOperator(operatorToken), Position: pos}
	p.declareLocalTarget(target)
	return stmt
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

func (p *parser) parseDestructureTargetList(first ast.Expression, allowElementTypes bool) ast.Expression {
	var pos ast.Position
	elements := []ast.DestructureElement{}
	seenRest := false

	if first != nil {
		if pos, ok := safeNavigationPos(first); ok {
			p.addParseError(pos, "safe navigation cannot be used as an assignment target")
			return nil
		}
		if !isAssignable(first) {
			p.addParseError(first.Pos(), "invalid destructuring assignment target")
			return nil
		}
		pos = first.Pos()
		elements = append(elements, ast.DestructureElement{Target: first, Position: first.Pos()})
	} else {
		element, ok := p.parseDestructureElement(allowElementTypes)
		if !ok {
			return nil
		}
		pos = element.Position
		seenRest = element.Rest
		elements = append(elements, element)
	}

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		element, ok := p.parseDestructureElement(allowElementTypes)
		if !ok {
			return nil
		}
		if element.Rest {
			if seenRest {
				p.addParseError(element.Position, "duplicate rest assignment target")
				return nil
			}
			seenRest = true
		}
		elements = append(elements, element)
	}

	return &ast.DestructureTarget{Elements: elements, Position: pos}
}

func (p *parser) parseDestructureElement(allowType bool) (ast.DestructureElement, bool) {
	rest := false
	if p.curToken.Type == ast.TokenAsterisk {
		restPos := p.curToken.Pos
		// A bare "*" not followed by a target is an anonymous (discard) rest.
		// Leave curToken on "*" so the caller's lookahead sees the terminator.
		if isAnonymousRestTerminator(p.peekToken.Type) {
			return ast.DestructureElement{Rest: true, Position: restPos}, true
		}
		rest = true
		p.nextToken()
	}

	target := p.parseDestructureSingleTarget(allowType)
	if target == nil {
		return ast.DestructureElement{}, false
	}
	element := ast.DestructureElement{Target: target, Rest: rest, Position: target.Pos()}
	if allowType && p.peekToken.Type == ast.TokenColon {
		p.nextToken()
		p.nextToken()
		element.Type = p.parseTypeExpr()
		if element.Type == nil {
			return ast.DestructureElement{}, false
		}
	}
	return element, true
}

// isAnonymousRestTerminator reports whether tt ends a bare "*" destructuring
// target, leaving an anonymous (discard) rest with no bound name. A "*" is
// anonymous when the next token closes the target list (an assignment operator),
// continues it (a comma), or closes a nested target group (")" or "]").
func isAnonymousRestTerminator(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenComma, ast.TokenRParen, ast.TokenRBracket:
		return true
	default:
		return isAssignmentOperator(tt)
	}
}

func (p *parser) parseDestructureSingleTarget(allowElementTypes bool) ast.Expression {
	switch p.curToken.Type {
	case ast.TokenLParen:
		return p.parseNestedDestructureTarget(ast.TokenRParen, ")", allowElementTypes)
	case ast.TokenLBracket:
		return p.parseNestedDestructureTarget(ast.TokenRBracket, "]", allowElementTypes)
	default:
		target := p.parseLineExpression(lowestPrec)
		if target == nil {
			return nil
		}
		if pos, ok := safeNavigationPos(target); ok {
			p.addParseError(pos, "safe navigation cannot be used as an assignment target")
			return nil
		}
		if !isAssignable(target) {
			p.addParseError(target.Pos(), "invalid destructuring assignment target")
			return nil
		}
		return target
	}
}

func (p *parser) parseNestedDestructureTarget(stop ast.TokenType, _ string, allowElementTypes bool) ast.Expression {
	pos := p.curToken.Pos
	if p.peekToken.Type == stop {
		p.errorExpected(p.peekToken, "destructuring assignment target")
		return nil
	}

	p.nextToken()
	target := p.parseDestructureTargetList(nil, allowElementTypes)
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
	case ast.TokenEOF, ast.TokenSemicolon, ast.TokenEnd, ast.TokenElse, ast.TokenElsif, ast.TokenEnsure, ast.TokenRescue, ast.TokenRBrace:
		return true
	default:
		return p.peekToken.Pos.Line != pos.Line
	}
}

func isAssignable(expr ast.Expression) bool {
	if _, ok := safeNavigationPos(expr); ok {
		return false
	}
	switch expr.(type) {
	case *ast.MemberExpr, *ast.Identifier, *ast.IndexExpr, *ast.IvarExpr, *ast.ClassVarExpr:
		return true
	default:
		return false
	}
}

// safeNavigationPos reports whether an assignment target contains a
// safe-navigation operator anywhere in its receiver chain, returning the
// position of the first such operator. Safe navigation (`object&.prop` or
// `object&.method(...)`) short-circuits to nil on a nil receiver, so allowing
// it on the left of an assignment would silently assign through nil. Vibescript
// rejects every such target—`user&.name = x`, `user&.profile.name = x`, and
// `user&.items[0] = x` alike—so the receiver chain is walked rather than only
// the outermost node.
func safeNavigationPos(expr ast.Expression) (ast.Position, bool) {
	for {
		switch e := expr.(type) {
		case *ast.MemberExpr:
			if e.Safe {
				return e.Position, true
			}
			expr = e.Object
		case *ast.CallExpr:
			if e.Safe {
				return e.Position, true
			}
			expr = e.Callee
		case *ast.IndexExpr:
			expr = e.Object
		default:
			return ast.Position{}, false
		}
	}
}
