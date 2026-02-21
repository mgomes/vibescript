package vibes

import "strconv"

func (p *parser) parseIdentifier() Expression {
	return &Identifier{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseIntegerLiteral() Expression {
	value, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid integer literal")
		return nil
	}
	return &IntegerLiteral{Value: value, position: p.curToken.Pos}
}

func (p *parser) parseFloatLiteral() Expression {
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid float literal")
		return nil
	}
	return &FloatLiteral{Value: value, position: p.curToken.Pos}
}

func (p *parser) parseStringLiteral() Expression {
	return &StringLiteral{Value: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseBooleanLiteral() Expression {
	return &BoolLiteral{Value: p.curToken.Type == tokenTrue, position: p.curToken.Pos}
}

func (p *parser) parseNilLiteral() Expression {
	return &NilLiteral{position: p.curToken.Pos}
}

func (p *parser) parseSymbolLiteral() Expression {
	return &SymbolLiteral{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseIvarLiteral() Expression {
	return &IvarExpr{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseClassVarLiteral() Expression {
	return &ClassVarExpr{Name: p.curToken.Literal, position: p.curToken.Pos}
}

func (p *parser) parseSelfLiteral() Expression {
	return &Identifier{Name: "self", position: p.curToken.Pos}
}
