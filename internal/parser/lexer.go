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

	// prevLine/prevColumn hold the position of the rune consumed
	// before ch, i.e. the final rune of the token that scanning just
	// moved past. NextToken derives exclusive token ends from them.
	prevLine   int
	prevColumn int

	ch        rune
	lastToken ast.Token
}

func newLexer(input string) *lexer {
	l := &lexer{input: input, line: 1, column: 0}
	l.readRune()
	return l
}

func (l *lexer) readRune() {
	l.prevLine, l.prevColumn = l.line, l.column
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
	tok := l.scanToken()
	if tok.Type != ast.TokenEOF {
		tok.End = ast.Position{Line: l.prevLine, Column: l.prevColumn + 1}
		l.lastToken = tok
	}
	return tok
}

func (l *lexer) scanToken() ast.Token {
	if tok, ok := l.skipWhitespaceAndComments(); ok {
		return tok
	}

	tok := ast.Token{Pos: ast.Position{Line: l.line, Column: l.column}}

	switch l.ch {
	case 0:
		tok.Type = ast.TokenEOF
		tok.Literal = ""
	case '+':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenPlusAssign, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenPlus, "+")
			l.readRune()
		}
	case '-':
		if l.peekRune() == '>' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenThinArrow, string(first)+string(l.ch))
			l.readRune()
		} else if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenMinusAssign, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenMinus, "-")
			l.readRune()
		}
	case '*':
		if l.peekRune() == '*' {
			first := l.ch
			l.readRune()
			if l.peekRune() == '=' {
				second := l.ch
				l.readRune()
				tok = l.makeToken(ast.TokenPowerAssign, string(first)+string(second)+string(l.ch))
				l.readRune()
			} else {
				tok = l.makeToken(ast.TokenPower, string(first)+string(l.ch))
				l.readRune()
			}
		} else if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenAsteriskAssign, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenAsterisk, "*")
			l.readRune()
		}
	case '/':
		if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenSlashAssign, string(first)+string(l.ch))
			l.readRune()
		} else {
			tok = l.makeToken(ast.TokenSlash, "/")
			l.readRune()
		}
	case '%':
		switch l.peekRune() {
		case 'w':
			if l.canStartPercentArrayLiteral() && isPercentLiteralDelimiter(l.peekRuneN(1)) {
				entries, err := l.readPercentArrayLiteral()
				if err != "" {
					tok.Type = ast.TokenIllegal
					tok.Literal = err
				} else {
					tok.Type = ast.TokenWords
					tok.Literal = encodePercentLiteralEntries(entries)
				}
			} else {
				tok = l.makeToken(ast.TokenPercent, "%")
				l.readRune()
			}
		case 'i':
			if l.canStartPercentArrayLiteral() && isPercentLiteralDelimiter(l.peekRuneN(1)) {
				entries, err := l.readPercentArrayLiteral()
				if err != "" {
					tok.Type = ast.TokenIllegal
					tok.Literal = err
				} else {
					tok.Type = ast.TokenSymbols
					tok.Literal = encodePercentLiteralEntries(entries)
				}
			} else {
				tok = l.makeToken(ast.TokenPercent, "%")
				l.readRune()
			}
		case '=':
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenPercentAssign, string(first)+string(l.ch))
			l.readRune()
		default:
			tok = l.makeToken(ast.TokenPercent, "%")
			l.readRune()
		}
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
	case ';':
		tok = l.makeToken(ast.TokenSemicolon, ";")
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
			if l.peekRune() == '.' {
				second := l.ch
				l.readRune()
				tok = l.makeToken(ast.TokenRangeExcl, string(first)+string(second)+string(l.ch))
				l.readRune()
			} else {
				tok = l.makeToken(ast.TokenRange, string(first)+string(l.ch))
				l.readRune()
			}
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
			if l.peekRuneN(1) == '=' {
				start := ast.Position{Line: l.line, Column: l.column}
				first := l.ch
				l.readRune()
				second := l.ch
				l.readRune()
				tok = ast.Token{Type: ast.TokenCaseEQ, Literal: string(first) + string(second) + string(l.ch), Pos: start}
				l.readRune()
				break
			}
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
		if l.peekRune() == '=' && l.peekRuneN(1) == '>' {
			start := ast.Position{Line: l.line, Column: l.column}
			first := l.ch
			l.readRune()
			second := l.ch
			l.readRune()
			tok = ast.Token{Type: ast.TokenSpaceship, Literal: string(first) + string(second) + string(l.ch), Pos: start}
			l.readRune()
		} else if l.peekRune() == '=' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenLTE, string(first)+string(l.ch))
			l.readRune()
		} else if l.peekRune() == '<' {
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenShovel, string(first)+string(l.ch))
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
			tok = l.makeToken(ast.TokenAmpersand, "&")
			l.readRune()
		}
	case '?':
		tok = l.makeToken(ast.TokenQuestion, "?")
		l.readRune()
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
		literal, interpolated, err := l.readDoubleQuotedString()
		if err != "" {
			tok.Type = ast.TokenIllegal
			tok.Literal = err
		} else if interpolated {
			tok.Type = ast.TokenInterpolatedString
			tok.Literal = literal
		} else {
			tok.Type = ast.TokenString
			tok.Literal = literal
		}
	case '\'':
		literal, err := l.readSingleQuotedString()
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

// seek repositions the lexer so the next scanned token begins at or after
// the given byte offset. Line and column state is rebuilt by replaying
// readRune from the start of the input, reusing the normal position
// bookkeeping rather than recomputing it. last becomes lastToken so
// gating that depends on the preceding token (such as percent-literal and
// newline handling) behaves as if that token had just been scanned.
func (l *lexer) seek(offset int, last ast.Token) {
	l.offset = 0
	l.width = 0
	l.line = 1
	l.column = 0
	l.prevLine = 0
	l.prevColumn = 0
	l.ch = 0
	l.readRune()
	for l.currentOffset() < offset && l.ch != 0 {
		l.readRune()
	}
	l.lastToken = last
}

func (l *lexer) makeToken(tt ast.TokenType, literal string) ast.Token {
	return ast.Token{Type: tt, Literal: literal, Pos: ast.Position{Line: l.line, Column: l.column}}
}

func (l *lexer) skipWhitespaceAndComments() (ast.Token, bool) {
	for {
		switch l.ch {
		case ' ', '\t', '\r', '\n':
			l.readRune()
			continue
		case '#':
			l.skipComment()
			continue
		case '=':
			if !l.atLineLeadingWhitespace() || !l.blockCommentMarkerAtCurrent("=begin") {
				return ast.Token{}, false
			}
			pos := ast.Position{Line: l.line, Column: l.column}
			if err := l.skipBlockComment(); err != "" {
				return ast.Token{Type: ast.TokenIllegal, Literal: err, Pos: pos}, true
			}
			continue
		default:
			return ast.Token{}, false
		}
	}
}

func (l *lexer) skipComment() {
	for l.ch != 0 && l.ch != '\n' {
		l.readRune()
	}
}

func (l *lexer) skipBlockComment() string {
	for l.ch != 0 && l.ch != '\n' {
		l.readRune()
	}
	if l.ch == '\n' {
		l.readRune()
	}

	for {
		for l.ch == ' ' || l.ch == '\t' || l.ch == '\r' {
			l.readRune()
		}
		if l.ch == 0 {
			return "unterminated block comment"
		}
		if l.atLineLeadingWhitespace() && l.blockCommentMarkerAtCurrent("=end") {
			for l.ch != 0 && l.ch != '\n' {
				l.readRune()
			}
			return ""
		}
		for l.ch != 0 && l.ch != '\n' {
			l.readRune()
		}
		if l.ch == '\n' {
			l.readRune()
		}
	}
}

func (l *lexer) blockCommentMarkerAtCurrent(marker string) bool {
	start := l.currentOffset()
	if !strings.HasPrefix(l.input[start:], marker) {
		return false
	}
	next := start + len(marker)
	if next >= len(l.input) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(l.input[next:])
	switch r {
	case 0, ' ', '\t', '\r', '\n':
		return true
	default:
		return false
	}
}

func (l *lexer) atLineLeadingWhitespace() bool {
	idx := l.currentOffset()
	for idx > 0 {
		r, w := utf8.DecodeLastRuneInString(l.input[:idx])
		if r == '\n' {
			return true
		}
		if r != ' ' && r != '\t' && r != '\r' {
			return false
		}
		idx -= w
	}
	return true
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

func (l *lexer) readDoubleQuotedString() (string, bool, string) {
	var decoded strings.Builder
	var raw strings.Builder
	interpolated := false
	interpolationDepth := 0
	var interpolationQuote rune

	for {
		l.readRune()
		if interpolationDepth > 0 {
			if interpolationQuote == 0 && l.ch == '"' && !l.interpolationQuoteHasClose('"') {
				l.readRune()
				return raw.String(), true, ""
			}
			switch l.ch {
			case 0:
				return "", false, "unterminated string"
			case '\\':
				raw.WriteRune(l.ch)
				decoded.WriteRune(l.ch)
				if interpolationQuote != 0 && l.peekRune() != 0 {
					l.readRune()
					raw.WriteRune(l.ch)
					decoded.WriteRune(l.ch)
				}
			default:
				raw.WriteRune(l.ch)
				decoded.WriteRune(l.ch)
				if interpolationQuote != 0 {
					if l.ch == interpolationQuote {
						interpolationQuote = 0
					}
					continue
				}
				switch l.ch {
				case '\'', '"':
					interpolationQuote = l.ch
				case '{':
					interpolationDepth++
				case '}':
					interpolationDepth--
				}
			}
			continue
		}

		switch l.ch {
		case 0:
			return "", false, "unterminated string"
		case '"':
			l.readRune()
			if interpolated {
				return raw.String(), true, ""
			}
			return decoded.String(), false, ""
		case '\\':
			next := l.peekRune()
			if next == 0 {
				return "", false, "unterminated string"
			}
			switch next {
			case '"', '\\':
				l.readRune()
				raw.WriteRune('\\')
				raw.WriteRune(next)
				decoded.WriteRune(next)
			case 'n':
				l.readRune()
				raw.WriteRune('\\')
				raw.WriteRune(next)
				decoded.WriteByte('\n')
			case 't':
				l.readRune()
				raw.WriteRune('\\')
				raw.WriteRune(next)
				decoded.WriteByte('\t')
			default:
				l.readRune()
				raw.WriteRune('\\')
				raw.WriteRune(next)
				decoded.WriteRune(next)
			}
		case '#':
			raw.WriteRune(l.ch)
			decoded.WriteRune(l.ch)
			if l.peekRune() == '{' {
				l.readRune()
				raw.WriteRune(l.ch)
				decoded.WriteRune(l.ch)
				interpolated = true
				interpolationDepth = 1
			}
		default:
			raw.WriteRune(l.ch)
			decoded.WriteRune(l.ch)
		}
	}
}

func (l *lexer) interpolationQuoteHasClose(quote rune) bool {
	idx := l.offset
	for idx < len(l.input) {
		r, width := utf8.DecodeRuneInString(l.input[idx:])
		idx += width
		if r == '\\' {
			if idx < len(l.input) {
				_, escapedWidth := utf8.DecodeRuneInString(l.input[idx:])
				idx += escapedWidth
			}
			continue
		}
		if r == quote {
			return true
		}
	}
	return false
}

func (l *lexer) readSingleQuotedString() (string, string) {
	var sb strings.Builder

	for {
		l.readRune()
		switch l.ch {
		case 0:
			return "", "unterminated string"
		case '\'':
			l.readRune()
			return sb.String(), ""
		case '\\':
			next := l.peekRune()
			switch next {
			case '\'', '\\':
				l.readRune()
				sb.WriteRune(next)
			default:
				sb.WriteRune(l.ch)
			}
		default:
			sb.WriteRune(l.ch)
		}
	}
}

func (l *lexer) readPercentArrayLiteral() ([]string, string) {
	l.readRune()
	l.readRune()
	open := l.ch
	close, paired := percentLiteralClose(open)
	if close == 0 {
		return nil, "invalid percent array delimiter"
	}

	depth := 1
	var raw strings.Builder
	for {
		l.readRune()
		switch l.ch {
		case 0:
			return nil, "unterminated percent array literal"
		case '\\':
			raw.WriteRune(l.ch)
			if l.peekRune() != 0 {
				l.readRune()
				raw.WriteRune(l.ch)
			}
		default:
			if paired && l.ch == open {
				depth++
			}
			if l.ch == close {
				depth--
				if depth == 0 {
					l.readRune()
					return splitPercentLiteralWords(raw.String(), open, close), ""
				}
			}
			raw.WriteRune(l.ch)
		}
	}
}

func isPercentLiteralDelimiter(r rune) bool {
	return r != 0 && !unicode.IsSpace(r) && !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

func (l *lexer) canStartPercentArrayLiteral() bool {
	start := l.currentOffset()
	if start == 0 {
		return true
	}
	prev, _ := utf8.DecodeLastRuneInString(l.input[:start])
	if unicode.IsSpace(prev) {
		if l.atLineLeadingWhitespace() {
			return true
		}
		return !canEndExpressionToken(l.lastToken.Type)
	}
	return !canEndExpressionToken(l.lastToken.Type)
}

func canEndExpressionToken(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenIdent, ast.TokenInt, ast.TokenFloat, ast.TokenString, ast.TokenInterpolatedString,
		ast.TokenSymbol, ast.TokenWords, ast.TokenSymbols, ast.TokenTrue, ast.TokenFalse, ast.TokenNil,
		ast.TokenSelf, ast.TokenIvar, ast.TokenClassVar, ast.TokenRParen, ast.TokenRBracket,
		ast.TokenRBrace, ast.TokenEnd:
		return true
	default:
		return false
	}
}

func percentLiteralClose(open rune) (rune, bool) {
	switch open {
	case '[':
		return ']', true
	case '(':
		return ')', true
	case '{':
		return '}', true
	case '<':
		return '>', true
	default:
		if !isPercentLiteralDelimiter(open) {
			return 0, false
		}
		return open, false
	}
}

const percentLiteralEntrySeparator = "\x00"

func encodePercentLiteralEntries(entries []string) string {
	return strings.Join(entries, percentLiteralEntrySeparator)
}

func decodePercentLiteralEntries(literal string) []string {
	if literal == "" {
		return nil
	}
	return strings.Split(literal, percentLiteralEntrySeparator)
}

func splitPercentLiteralWords(raw string, open, close rune) []string {
	var words []string
	var sb strings.Builder
	inWord := false
	escaped := false

	flush := func() {
		if !inWord {
			return
		}
		words = append(words, sb.String())
		sb.Reset()
		inWord = false
	}

	for _, r := range raw {
		if escaped {
			if isPercentWordEscapable(r, open, close) {
				sb.WriteRune(r)
			} else {
				sb.WriteRune('\\')
				sb.WriteRune(r)
			}
			inWord = true
			escaped = false
			continue
		}

		switch {
		case r == '\\':
			escaped = true
			inWord = true
		case unicode.IsSpace(r):
			flush()
		default:
			sb.WriteRune(r)
			inWord = true
		}
	}

	if escaped {
		sb.WriteRune('\\')
	}
	flush()
	return words
}

func isPercentWordEscapable(r, open, close rune) bool {
	return unicode.IsSpace(r) || r == '\\' || r == open || r == close
}

// ast.Identifier classification and keyword lookup are now provided by
// internal/ast (ast.IsIdentifierStart, ast.IsIdentifierRune, ast.LookupIdent).
