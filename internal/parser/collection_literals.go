package parser

import (
	"fmt"

	"github.com/mgomes/vibescript/internal/ast"
)

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
