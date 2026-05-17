package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

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
