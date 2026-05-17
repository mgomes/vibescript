package parser

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgomes/vibescript/internal/ast"
)

// Lexer is the public name for the package-private lexer state.
type Lexer = lexer

// NewLexer returns a new lexer over the given source text.
func NewLexer(input string) *Lexer { return newLexer(input) }

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
	for i := range n + 1 {
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

func (l *lexer) NextToken() ast.Token {
	l.skipWhitespaceAndComments()

	tok := ast.Token{Pos: ast.Position{Line: l.line, Column: l.column}}

	switch l.ch {
	case 0:
		tok.Type = ast.TokenEOF
		tok.Literal = ""
	case '+':
		tok = l.makeToken(ast.TokenPlus, "+")
		l.readRune()
	case '-':
		if l.peekRune() == '>' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenArrow, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenMinus, "-")
			l.readRune()
		}
	case '*':
		tok = l.makeToken(ast.TokenAsterisk, "*")
		l.readRune()
	case '/':
		tok = l.makeToken(ast.TokenSlash, "/")
		l.readRune()
	case '%':
		tok = l.makeToken(ast.TokenPercent, "%")
		l.readRune()
	case '(':
		tok = l.makeToken(ast.TokenLParen, "(")
		l.readRune()
	case ')':
		tok = l.makeToken(ast.TokenRParen, ")")
		l.readRune()
	case '{':
		tok = l.makeToken(ast.TokenLBrace, "{")
		l.readRune()
	case '}':
		tok = l.makeToken(ast.TokenRBrace, "}")
		l.readRune()
	case '[':
		tok = l.makeToken(ast.TokenLBracket, "[")
		l.readRune()
	case ']':
		tok = l.makeToken(ast.TokenRBracket, "]")
		l.readRune()
	case ',':
		tok = l.makeToken(ast.TokenComma, ",")
		l.readRune()
	case ':':
		if l.peekRune() == ':' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenScope, string(first)+string(l.ch))
			l.readRune()
			return tok
		}
		if ast.IsIdentifierRune(l.peekRune()) {
			l.readRune()
			start := l.currentOffset()
			for ast.IsIdentifierRune(l.peekRune()) {
				l.readRune()
			}
			literal := l.input[start:l.offset]
			tok.Type = ast.TokenSymbol
			tok.Literal = literal
			l.readRune()
			return tok
		}
		tok = l.makeToken(ast.TokenColon, ":")
		l.readRune()
	case '.':
		if l.peekRune() == '.' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenRange, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenDot, ".")
			l.readRune()
		}
	case '!':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenNotEQ, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenBang, "!")
			l.readRune()
		}
	case '=':
		switch l.peekRune() {
		case '=':
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenEQ, string(first)+string(l.ch))
			l.readRune()
		case '>':
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenArrow, string(first)+string(l.ch))
			l.readRune()
		default:
			tok = l.makeToken(ast.TokenAssign, "=")
			l.readRune()
		}
	case '>':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenGTE, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenGT, ">")
			l.readRune()
		}
	case '<':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenLTE, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenLT, "<")
			l.readRune()
		}
	case '&':
		if l.peekRune() == '&' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenAnd, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenIllegal, string(l.ch))
			l.readRune()
		}
	case '|':
		if l.peekRune() == '|' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenOr, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenPipe, "|")
			l.readRune()
		}
	case '"':
		literal, err := l.readString()
		if err != "" {
			tok.Type = ast.TokenIllegal
			tok.Literal = err
		} else {
			tok.Type = ast.TokenString
			tok.Literal = literal
		}
	default:
		switch {
		case l.ch == '@':
			if l.peekRune() == '@' {
				l.readRune()
				l.readRune()
				start := l.currentOffset()
				for ast.IsIdentifierRune(l.peekRune()) {
					l.readRune()
				}
				literal := l.input[start:l.offset]
				tok.Type = ast.TokenClassVar
				tok.Literal = literal
				l.readRune()
				return tok
			}
			l.readRune()
			start := l.currentOffset()
			for ast.IsIdentifierRune(l.peekRune()) {
				l.readRune()
			}
			literal := l.input[start:l.offset]
			tok.Type = ast.TokenIvar
			tok.Literal = literal
			l.readRune()
			return tok
		case ast.IsIdentifierStart(l.ch):
			literal := l.readIdentifier()
			tok.Type = ast.LookupIdent(literal)
			tok.Literal = literal
			return tok
		case unicode.IsDigit(l.ch):
			literal, isFloat := l.readNumber()
			tok.Literal = literal
			if isFloat {
				tok.Type = ast.TokenFloat
			} else {
				tok.Type = ast.TokenInt
			}
			return tok
		default:
			tok = l.makeToken(ast.TokenIllegal, string(l.ch))
			l.readRune()
		}
	}

	return tok
}

func (l *lexer) currentOffset() int {
	return l.offset - l.width
}

func (l *lexer) makeToken(tt ast.TokenType, literal string) ast.Token {
	return ast.Token{Type: tt, Literal: literal, Pos: ast.Position{Line: l.line, Column: l.column}}
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
	for ast.IsIdentifierRune(l.peekRune()) {
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

// ast.Identifier classification and keyword lookup are now provided by
// internal/ast (ast.IsIdentifierStart, ast.IsIdentifierRune, ast.LookupIdent).
