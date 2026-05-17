package parser

import (
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
