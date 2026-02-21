package vibes

import (
	"fmt"
	"strings"
)

type parseError struct {
	pos    Position
	msg    string
	source string
}

func (e *parseError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "parse error at %d:%d: %s", e.pos.Line, e.pos.Column, e.msg)
	if frame := formatCodeFrame(e.source, e.pos); frame != "" {
		b.WriteString("\n")
		b.WriteString(frame)
	}
	return b.String()
}

func (p *parser) errorExpected(tok Token, expected string) {
	p.addParseError(tok.Pos, fmt.Sprintf("expected %s, got %s", expected, tokenLabel(tok.Type)))
}

func (p *parser) errorUnexpected(tok Token) {
	p.addParseError(tok.Pos, fmt.Sprintf("unexpected token %s", tokenLabel(tok.Type)))
}

func (p *parser) addParseError(pos Position, msg string) {
	p.errors = append(p.errors, &parseError{pos: pos, msg: msg, source: p.l.input})
}

func tokenLabel(tt TokenType) string {
	switch tt {
	case tokenIllegal:
		return "invalid token"
	case tokenEOF:
		return "end of input"
	case tokenIdent:
		return "identifier"
	case tokenInt:
		return "integer"
	case tokenFloat:
		return "float"
	case tokenString:
		return "string"
	case tokenSymbol:
		return "symbol"
	case tokenIvar:
		return "instance variable"
	case tokenClassVar:
		return "class variable"
	case tokenDef:
		return "'def'"
	case tokenClass:
		return "'class'"
	case tokenExport:
		return "'export'"
	case tokenSelf:
		return "'self'"
	case tokenPrivate:
		return "'private'"
	case tokenProperty:
		return "'property'"
	case tokenGetter:
		return "'getter'"
	case tokenSetter:
		return "'setter'"
	case tokenEnd:
		return "'end'"
	case tokenRaise:
		return "'raise'"
	case tokenReturn:
		return "'return'"
	case tokenYield:
		return "'yield'"
	case tokenDo:
		return "'do'"
	case tokenFor:
		return "'for'"
	case tokenIn:
		return "'in'"
	case tokenIf:
		return "'if'"
	case tokenElsif:
		return "'elsif'"
	case tokenElse:
		return "'else'"
	case tokenTrue:
		return "'true'"
	case tokenFalse:
		return "'false'"
	case tokenNil:
		return "'nil'"
	default:
		if len(tt) == 1 || strings.HasPrefix(string(tt), "<") || strings.HasPrefix(string(tt), ">") {
			return fmt.Sprintf("%q", string(tt))
		}
		return fmt.Sprintf("%q", strings.ToLower(string(tt)))
	}
}
