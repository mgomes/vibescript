package vibes

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

type lexer struct {
	input string

	offset int
	width  int

	line   int
	column int

	ch rune
}

func newLexer(input string) *lexer {
	l := &lexer{input: input, line: 1, column: 0}
	l.readRune()
	return l
}

func (l *lexer) readRune() {
	if l.offset >= len(l.input) {
		l.width = 0
		l.ch = 0
		return
	}

	r, w := utf8.DecodeRuneInString(l.input[l.offset:])
	l.width = w
	l.offset += w

	if r == '\n' {
		l.line++
		l.column = 0
	} else {
		l.column++
	}

	l.ch = r
}

func (l *lexer) peekRune() rune {
	if l.offset >= len(l.input) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.input[l.offset:])
	return r
}

func (l *lexer) peekRuneN(n int) rune {
	idx := l.offset
	var r rune
	var w int
	for i := 0; i <= n; i++ {
		if idx >= len(l.input) {
			return 0
		}
		r, w = utf8.DecodeRuneInString(l.input[idx:])
		if i == n {
			return r
		}
		idx += w
	}
	return 0
}

func (l *lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	tok := Token{Pos: Position{Line: l.line, Column: l.column}}

	switch l.ch {
	case 0:
		tok.Type = tokenEOF
		tok.Literal = ""
	case '+':
		tok = l.makeToken(tokenPlus, "+")
		l.readRune()
	case '-':
		if l.peekRune() == '>' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenArrow, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenMinus, "-")
			l.readRune()
		}
	case '*':
		tok = l.makeToken(tokenAsterisk, "*")
		l.readRune()
	case '/':
		tok = l.makeToken(tokenSlash, "/")
		l.readRune()
	case '%':
		tok = l.makeToken(tokenPercent, "%")
		l.readRune()
	case '(':
		tok = l.makeToken(tokenLParen, "(")
		l.readRune()
	case ')':
		tok = l.makeToken(tokenRParen, ")")
		l.readRune()
	case '{':
		tok = l.makeToken(tokenLBrace, "{")
		l.readRune()
	case '}':
		tok = l.makeToken(tokenRBrace, "}")
		l.readRune()
	case '[':
		tok = l.makeToken(tokenLBracket, "[")
		l.readRune()
	case ']':
		tok = l.makeToken(tokenRBracket, "]")
		l.readRune()
	case ',':
		tok = l.makeToken(tokenComma, ",")
		l.readRune()
	case ':':
		if isIdentifierRune(l.peekRune()) {
			l.readRune()
			start := l.currentOffset()
			for isIdentifierRune(l.peekRune()) {
				l.readRune()
			}
			literal := l.input[start:l.offset]
			tok.Type = tokenSymbol
			tok.Literal = literal
			l.readRune()
			return tok
		}
		tok = l.makeToken(tokenColon, ":")
		l.readRune()
	case '.':
		if l.peekRune() == '.' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenRange, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenDot, ".")
			l.readRune()
		}
	case '!':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenNotEQ, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenBang, "!")
			l.readRune()
		}
	case '=':
		switch l.peekRune() {
		case '=':
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenEQ, string(first)+string(l.ch))
			l.readRune()
		case '>':
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenArrow, string(first)+string(l.ch))
			l.readRune()
		default:
			tok = l.makeToken(tokenAssign, "=")
			l.readRune()
		}
	case '>':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenGTE, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenGT, ">")
			l.readRune()
		}
	case '<':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenLTE, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenLT, "<")
			l.readRune()
		}
	case '&':
		if l.peekRune() == '&' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenAnd, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenIllegal, string(l.ch))
			l.readRune()
		}
	case '|':
		if l.peekRune() == '|' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(tokenOr, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(tokenPipe, "|")
			l.readRune()
		}
	case '"':
		literal, err := l.readString()
		if err != "" {
			tok.Type = tokenIllegal
			tok.Literal = err
		} else {
			tok.Type = tokenString
			tok.Literal = literal
		}
	default:
		switch {
		case l.ch == '@':
			if l.peekRune() == '@' {
				l.readRune()
				l.readRune()
				start := l.currentOffset()
				for isIdentifierRune(l.peekRune()) {
					l.readRune()
				}
				literal := l.input[start:l.offset]
				tok.Type = tokenClassVar
				tok.Literal = literal
				l.readRune()
				return tok
			}
			l.readRune()
			start := l.currentOffset()
			for isIdentifierRune(l.peekRune()) {
				l.readRune()
			}
			literal := l.input[start:l.offset]
			tok.Type = tokenIvar
			tok.Literal = literal
			l.readRune()
			return tok
		case isIdentifierStart(l.ch):
			literal := l.readIdentifier()
			tok.Type = lookupIdent(literal)
			tok.Literal = literal
			return tok
		case unicode.IsDigit(l.ch):
			literal, isFloat := l.readNumber()
			tok.Literal = literal
			if isFloat {
				tok.Type = tokenFloat
			} else {
				tok.Type = tokenInt
			}
			return tok
		default:
			tok = l.makeToken(tokenIllegal, string(l.ch))
			l.readRune()
		}
	}

	return tok
}

func (l *lexer) currentOffset() int {
	return l.offset - l.width
}

func (l *lexer) makeToken(tt TokenType, literal string) Token {
	return Token{Type: tt, Literal: literal, Pos: Position{Line: l.line, Column: l.column}}
}

func (l *lexer) skipWhitespaceAndComments() {
	for {
		switch l.ch {
		case ' ', '\t', '\r', '\n':
			l.readRune()
			continue
		case '#':
			l.skipComment()
			continue
		default:
			return
		}
	}
}

func (l *lexer) skipComment() {
	for l.ch != 0 && l.ch != '\n' {
		l.readRune()
	}
}

func (l *lexer) readIdentifier() string {
	start := l.currentOffset()
	for isIdentifierRune(l.peekRune()) {
		l.readRune()
	}
	literal := l.input[start:l.offset]
	l.readRune()
	return literal
}

func (l *lexer) readNumber() (string, bool) {
	var sb strings.Builder
	hasDot := false

	// current rune is part of the number
	sb.WriteRune(l.ch)

	for {
		r := l.peekRune()
		switch {
		case r == '_':
			// Allow underscores as visual separators; ignore them in the literal.
			// Only consume if surrounded by digits.
			beforeDigit := unicode.IsDigit(l.ch)
			afterDigit := unicode.IsDigit(l.peekRuneN(1))
			if beforeDigit && afterDigit {
				l.readRune()
				continue
			}
			goto done
		case r == '.' && !hasDot && unicode.IsDigit(l.peekRuneN(1)):
			hasDot = true
			l.readRune()
			sb.WriteRune('.')
		case unicode.IsDigit(r):
			l.readRune()
			sb.WriteRune(r)
		default:
			goto done
		}
	}

done:
	literal := sb.String()
	l.readRune()
	return literal, hasDot
}

func (l *lexer) readString() (string, string) {
	var sb strings.Builder

	for {
		l.readRune()
		switch l.ch {
		case 0:
			return "", "unterminated string"
		case '"':
			l.readRune()
			return sb.String(), ""
		case '\\':
			next := l.peekRune()
			switch next {
			case '"', '\\':
				l.readRune()
				sb.WriteRune(next)
			case 'n':
				l.readRune()
				sb.WriteByte('\n')
			case 't':
				l.readRune()
				sb.WriteByte('\t')
			default:
				l.readRune()
				sb.WriteRune(next)
			}
		default:
			sb.WriteRune(l.ch)
		}
	}
}

func isIdentifierStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

func isIdentifierRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '?' || r == '!'
}

func lookupIdent(ident string) TokenType {
	switch ident {
	case "def":
		return tokenDef
	case "class":
		return tokenClass
	case "self":
		return tokenSelf
	case "private":
		return tokenPrivate
	case "property":
		return tokenProperty
	case "getter":
		return tokenGetter
	case "setter":
		return tokenSetter
	case "end":
		return tokenEnd
	case "return":
		return tokenReturn
	case "yield":
		return tokenYield
	case "do":
		return tokenDo
	case "for":
		return tokenFor
	case "in":
		return tokenIn
	case "if":
		return tokenIf
	case "elsif":
		return tokenElsif
	case "else":
		return tokenElse
	case "true":
		return tokenTrue
	case "false":
		return tokenFalse
	case "nil":
		return tokenNil
	}
	return tokenIdent
}
