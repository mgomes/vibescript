package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

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
