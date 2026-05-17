package parser

import (
	"fmt"

	"github.com/mgomes/vibescript/internal/ast"
)

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
