package vibes

import "fmt"

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
