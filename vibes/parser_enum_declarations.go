package vibes

import "fmt"

func (p *parser) parseEnumStatement() Statement {
	pos := p.curToken.Pos
	if p.insideClass || p.statementNesting > 0 {
		p.addParseError(pos, "enum is only supported at the top level")
		return nil
	}
	if !p.expectPeek(tokenIdent) {
		return nil
	}
	name := p.curToken.Literal
	p.nextToken()

	stmt := &EnumStmt{
		Name:     name,
		Members:  make([]EnumMemberStmt, 0),
		position: pos,
	}
	memberNames := make(map[string]struct{})

	for p.curToken.Type != tokenEnd && p.curToken.Type != tokenEOF {
		if p.curToken.Type != tokenIdent && p.curToken.Type != tokenEnum {
			p.errorExpected(p.curToken, "enum member name")
			return nil
		}
		member := EnumMemberStmt{
			Name:     p.curToken.Literal,
			position: p.curToken.Pos,
		}
		if _, exists := memberNames[member.Name]; exists {
			p.addParseError(member.position, fmt.Sprintf("duplicate enum member %s", member.Name))
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
	if p.curToken.Type != tokenEnd {
		p.errorExpected(p.curToken, "end")
	}

	return stmt
}
