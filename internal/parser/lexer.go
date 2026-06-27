package parser

import (
	"fmt"
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

	// bracketDepth counts the open `(`, `[`, and `{` brackets the lexer has
	// scanned past. Each opener increments it and each matching closer
	// decrements it, so it names the bracket nesting level of the rune the
	// lexer is currently at. It tags pending ternaries so a `:` only matches a
	// `?` opened at the same nesting level, never a label `:` inside a hash,
	// array, or paren group opened after the `?`.
	bracketDepth int

	// ternaryStack holds the bracket nesting level recorded at each ternary
	// `?` whose separator `:` has not yet been scanned. A `:` in
	// expression-end position closes the innermost pending ternary only when it
	// sits at that ternary's nesting level; such a `:` is the ternary separator
	// rather than a quoted symbol or label introducer. Tagging each `?` with
	// its level keeps a label `:` inside a hash, array, or paren group opened
	// after the `?` (a deeper level) from being mistaken for the separator. The
	// lexer reads ahead of the parser, but this stack only relates `?` tokens
	// to the colons the lexer itself scans, so it stays self-consistent. The
	// parser captures and restores it with the rest of the lexer value during
	// speculative parsing; snapshot and restore deep-copy the slice so a
	// rolled-back speculation cannot leak pushes or pops into the live lexer.
	ternaryStack []int
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
					setDiagnostic(&tok, err)
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
					setDiagnostic(&tok, err)
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
		l.bracketDepth++
		l.readRune()
	case ')':
		tok = l.makeToken(ast.TokenRParen, ")")
		l.closeBracket()
		l.readRune()
	case '{':
		tok = l.makeToken(ast.TokenLBrace, "{")
		l.bracketDepth++
		l.readRune()
	case '}':
		tok = l.makeToken(ast.TokenRBrace, "}")
		l.closeBracket()
		l.readRune()
	case '[':
		tok = l.makeToken(ast.TokenLBracket, "[")
		l.bracketDepth++
		l.readRune()
	case ']':
		tok = l.makeToken(ast.TokenRBracket, "]")
		l.closeBracket()
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
		closesTernary := l.colonClosesTernary()
		if closesTernary {
			l.ternaryStack = l.ternaryStack[:len(l.ternaryStack)-1]
		}
		if quote := l.peekRune(); (quote == '"' || quote == '\'') && !closesTernary && l.colonStartsQuotedSymbol() {
			return l.scanQuotedSymbol(tok)
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
		switch l.peekRune() {
		case '&':
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenAnd, string(first)+string(l.ch))
			l.readRune()
		case '.':
			first := l.ch
			l.readRune()
			tok = l.makeToken(ast.TokenSafeNav, string(first)+string(l.ch))
			l.readRune()
		default:
			tok = l.makeToken(ast.TokenAmpersand, "&")
			l.readRune()
		}
	case '?':
		tok = l.makeToken(ast.TokenQuestion, "?")
		l.ternaryStack = append(l.ternaryStack, l.bracketDepth)
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
			setDiagnostic(&tok, err)
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
			setDiagnostic(&tok, err)
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
			num := l.readNumber()
			switch {
			case num.errMsg != "":
				setDiagnostic(&tok, num.errMsg)
			case num.isFloat:
				tok.Type = ast.TokenFloat
				tok.Literal = num.literal
			default:
				tok.Type = ast.TokenInt
				tok.Literal = num.literal
			}
			return tok
		default:
			tok = l.makeToken(ast.TokenIllegal, fmt.Sprintf("unexpected character %q", l.ch))
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
	structuralOffset := offset
	if start, ok := sourceOffsetForPosition(l.input, last.Pos); ok && start < offset {
		structuralOffset = start
	}
	bracketDepth, ternaryStack := lexerStructuralStateBefore(l.input, structuralOffset)

	l.offset = 0
	l.width = 0
	l.line = 1
	l.column = 0
	l.prevLine = 0
	l.prevColumn = 0
	l.ch = 0
	l.bracketDepth = bracketDepth
	l.ternaryStack = ternaryStack
	l.readRune()
	for l.currentOffset() < offset && l.ch != 0 {
		l.readRune()
	}
	l.lastToken = last
}

func lexerStructuralStateBefore(input string, offset int) (int, []int) {
	scan := newLexer(input)
	for scan.ch != 0 {
		if _, ok := scan.skipWhitespaceAndComments(); ok {
			continue
		}
		if scan.currentOffset() >= offset {
			break
		}
		tok := scan.NextToken()
		if tok.Type == ast.TokenEOF {
			break
		}
	}
	return scan.bracketDepth, append([]int(nil), scan.ternaryStack...)
}

func (l *lexer) makeToken(tt ast.TokenType, literal string) ast.Token {
	return ast.Token{Type: tt, Literal: literal, Pos: ast.Position{Line: l.line, Column: l.column}}
}

// setDiagnostic turns tok into an illegal token carrying msg as a lexer
// diagnostic, preserving the token's already-stamped position. The parser
// surfaces such literals verbatim, so the message must be human-readable
// rather than the raw offending source text.
func setDiagnostic(tok *ast.Token, msg string) {
	tok.Type = ast.TokenIllegal
	tok.Literal = msg
	tok.Diagnostic = true
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
				return ast.Token{Type: ast.TokenIllegal, Literal: err, Pos: pos, Diagnostic: true}, true
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

// numberToken is the lexer's classification of a scanned numeric literal.
// On success errMsg is empty and literal carries the underscore-stripped
// digits (prefix included for based literals); on failure errMsg holds a
// human-readable diagnostic and the literal is undefined.
type numberToken struct {
	literal string
	isFloat bool
	errMsg  string
}

const invalidNumericLiteral = "invalid numeric literal"

// readNumber scans a numeric literal beginning at the current rune. It
// recognizes Ruby-style base prefixes (0x/0X, 0b/0B, 0o/0O, 0d/0D) in
// addition to decimal integers and floats. Underscores are accepted as
// visual separators only between adjacent digits and are stripped from the
// returned literal. A prefix must be followed by at least one valid digit
// and the literal must not be immediately followed by an identifier rune;
// either violation yields an invalid-numeric-literal diagnostic so the
// caller can emit a precise parse error instead of leaving a stray
// identifier behind.
func (l *lexer) readNumber() numberToken {
	if l.ch == '0' {
		if prefix, base, ok := basePrefix(l.peekRune()); ok {
			return l.readPrefixedNumber(prefix, base)
		}
	}
	return l.readDecimalNumber()
}

// readDecimalNumber lexes a decimal integer or float beginning at the
// current rune. It returns the normalized literal (with visual-separator
// underscores stripped), whether the literal is a float, and a non-empty
// diagnostic when the literal is malformed.
//
// A literal is a float when it carries a decimal point or an exponent
// suffix. Exponent notation mirrors Ruby: an optional sign follows the
// e/E marker and at least one exponent digit is required, with underscores
// permitted only between digits. Malformed exponents such as 1e, 1e+, or
// 1e_3 yield a diagnostic instead of silently splitting into an integer
// followed by an identifier.
func (l *lexer) readDecimalNumber() numberToken {
	var sb strings.Builder
	var errMsg string
	hasDot := false
	hasExponent := false

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
		case r == '.' && !hasDot && !hasExponent && unicode.IsDigit(l.peekRuneN(1)):
			hasDot = true
			l.readRune()
			sb.WriteRune('.')
		case (r == 'e' || r == 'E') && !hasExponent && l.exponentMarkerAhead():
			if msg := l.readExponent(&sb); msg != "" {
				errMsg = msg
				goto done
			}
			hasExponent = true
		case unicode.IsDigit(r):
			l.readRune()
			sb.WriteRune(r)
		default:
			goto done
		}
	}

done:
	if errMsg == "" {
		if msg := l.rejectNumberSuffix(); msg != "" {
			errMsg = msg
		}
	}
	literal := sb.String()
	l.readRune()
	return numberToken{literal: literal, isFloat: hasDot || hasExponent, errMsg: errMsg}
}

// rejectNumberSuffix guards the boundary just past a numeric literal. A number
// that directly abuts an identifier (no intervening whitespace or operator),
// such as 1e3foo, 123abc, or 1.5x, is malformed: Ruby reports a syntax error
// rather than splitting it into a number followed by an identifier. A keyword
// suffix is left intact because Ruby permits adjacency there (5if cond and
// 1e3if cond lex as the number followed by a modifier keyword). When the suffix
// is a plain identifier it is consumed so the whole offending run becomes a
// single diagnostic token instead of fragmenting into a stray identifier.
//
// It must be called at the done boundary while l.ch still holds the literal's
// final rune, so l.peekRune reports the first rune after the number.
func (l *lexer) rejectNumberSuffix() string {
	if !ast.IsIdentifierStart(l.peekRune()) {
		return ""
	}
	start := l.offset
	end := start
	for end < len(l.input) {
		r, w := utf8.DecodeRuneInString(l.input[end:])
		if !ast.IsIdentifierRune(r) {
			break
		}
		end += w
	}
	if ast.LookupIdent(l.input[start:end]) != ast.TokenIdent {
		return ""
	}
	for l.offset < end {
		l.readRune()
	}
	return "malformed numeric literal: identifier cannot immediately follow a number"
}

// exponentMarkerAhead reports whether the e/E rune at l.peekRune actually
// opens an exponent suffix rather than abutting an identifier. Mirroring Ruby,
// the marker begins an exponent when immediately followed by a digit or by a
// sign (+/-). A sign commits to the exponent even without a following digit, so
// 1e+ is reported as a malformed exponent. Otherwise the e/E belongs to a
// trailing identifier (5end keeps the end keyword while 5elf and 1e_3 fall to
// the numeric suffix guard) rather than being mis-lexed as a malformed exponent.
//
// The marker must be the lexer's current peek rune, so peekRuneN(1) is the rune
// immediately after it.
func (l *lexer) exponentMarkerAhead() bool {
	next := l.peekRuneN(1)
	return unicode.IsDigit(next) || next == '+' || next == '-'
}

// readExponent consumes an exponent suffix beginning at the e/E marker,
// which must be the lexer's current peek rune. It appends the consumed
// runes (minus visual-separator underscores) to sb and returns a
// diagnostic when the suffix is malformed. A malformed suffix either lacks
// any exponent digit (1e+, where the sign commits to an exponent) or carries
// an underscore that is not wedged between two digits (1e3_, 1e3__4); in both
// cases the marker, sign, and any stray runes are consumed to keep the span
// over the offending text.
func (l *lexer) readExponent(sb *strings.Builder) string {
	marker := l.peekRune()
	l.readRune()
	sb.WriteRune(marker)

	if sign := l.peekRune(); sign == '+' || sign == '-' {
		l.readRune()
		sb.WriteRune(sign)
	}

	if !unicode.IsDigit(l.peekRune()) {
		// The suffix opens with a non-digit (1e_3, 1e+_3). Consume the rest of
		// the malformed tail so the whole offending sequence becomes one illegal
		// token instead of leaving a stray identifier for the parser to choke
		// on, which would cascade into unrelated diagnostics in delimited
		// contexts such as [1e_3].
		l.consumeExponentTail()
		return "malformed exponent in numeric literal: expected digits after '" + string(marker) + "'"
	}

	for {
		switch r := l.peekRune(); {
		case r == '_':
			// Underscores are visual separators only between two digits. A
			// trailing or doubled underscore (1e3_, 1e3__4) is malformed, so
			// consume the rest of the offending tail and report rather than
			// letting the parser lex the dangling underscore as a separate
			// identifier.
			if unicode.IsDigit(l.ch) && unicode.IsDigit(l.peekRuneN(1)) {
				l.readRune()
				continue
			}
			l.readRune()
			l.consumeExponentTail()
			return "malformed exponent in numeric literal: underscore must sit between exponent digits"
		case unicode.IsDigit(r):
			l.readRune()
			sb.WriteRune(r)
		default:
			return ""
		}
	}
}

// consumeExponentTail advances past the run of identifier runes (letters,
// digits, and underscores) that follows a malformed exponent marker. It keeps
// the diagnostic token's span over the entire offending sequence so a malformed
// exponent never fragments into a separate identifier token, mirroring Ruby's
// single "trailing sign/underscore" error for inputs such as 1e+foo or 5e+end.
func (l *lexer) consumeExponentTail() {
	for ast.IsIdentifierRune(l.peekRune()) {
		l.readRune()
	}
}

// readPrefixedNumber scans a based literal whose leading '0' is the current
// rune and whose base marker (x/b/o/d) is the next rune. base reports the
// numeric radix and prefix carries the marker rune for the returned literal.
func (l *lexer) readPrefixedNumber(prefix rune, base int) numberToken {
	var sb strings.Builder
	sb.WriteByte('0')
	sb.WriteRune(prefix)

	// Consume the '0' and the prefix marker so the current rune sits on the
	// first body character of the literal.
	l.readRune()

	digits := 0
	for {
		r := l.peekRune()
		switch {
		case r == '_':
			// Underscores are valid only between two body digits.
			if isBaseDigit(l.peekRuneN(1), base) && digits > 0 {
				l.readRune()
				continue
			}
			return l.invalidPrefixedNumber()
		case isBaseDigit(r, base):
			l.readRune()
			sb.WriteRune(r)
			digits++
		default:
			goto done
		}
	}

done:
	if digits == 0 {
		return l.invalidPrefixedNumber()
	}
	// A based literal followed directly by a name rune (an out-of-range digit, a
	// stray letter, or a leading-underscore name) is never valid; the fractional
	// dot is likewise rejected since based literals are integers. The '?' and '!'
	// suffixes are excluded: they are operators (e.g. the ternary '?') that
	// terminate the literal rather than glue onto it, matching how the decimal
	// path leaves "1?2:3" as an integer followed by the ternary.
	next := l.peekRune()
	if isNumericTrailRune(next) || (next == '.' && isBaseDigit(l.peekRuneN(1), 10)) {
		return l.invalidPrefixedNumber()
	}
	literal := sb.String()
	l.readRune()
	return numberToken{literal: literal}
}

// invalidPrefixedNumber consumes the remaining identifier and fractional runes
// of a malformed based literal so the lexer resumes scanning past it, then
// reports the invalid-numeric-literal diagnostic.
func (l *lexer) invalidPrefixedNumber() numberToken {
	for isNumericTrailRune(l.peekRune()) {
		l.readRune()
	}
	if l.peekRune() == '.' && unicode.IsDigit(l.peekRuneN(1)) {
		l.readRune()
		for isNumericTrailRune(l.peekRune()) {
			l.readRune()
		}
	}
	l.readRune()
	return numberToken{errMsg: invalidNumericLiteral}
}

// isNumericTrailRune reports whether r, appearing immediately after a numeric
// literal, indicates a malformed literal (a digit, letter, or underscore glued
// onto the digits) rather than a following operator. Unlike
// ast.IsIdentifierRune it excludes the '?' and '!' method-name suffixes, since
// those are operator runes (the ternary '?', logical negation '!') that
// terminate the literal instead of extending it.
func isNumericTrailRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// basePrefix maps a base-marker rune to its prefix rune and radix.
func basePrefix(r rune) (prefix rune, base int, ok bool) {
	switch r {
	case 'x', 'X':
		return r, 16, true
	case 'b', 'B':
		return r, 2, true
	case 'o', 'O':
		return r, 8, true
	case 'd', 'D':
		return r, 10, true
	default:
		return 0, 0, false
	}
}

// isBaseDigit reports whether r is a valid digit in the given radix.
func isBaseDigit(r rune, base int) bool {
	var v int
	switch {
	case r >= '0' && r <= '9':
		v = int(r - '0')
	case r >= 'a' && r <= 'f':
		v = int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		v = int(r-'A') + 10
	default:
		return false
	}
	return v < base
}

func (l *lexer) readDoubleQuotedString() (string, bool, string) {
	var decoded strings.Builder
	var raw strings.Builder
	interpolated := false

	for {
		l.readRune()
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
				interpolated = true
				if errMsg := l.consumeInterpolation(&raw); errMsg != "" {
					return "", false, errMsg
				}
			}
		default:
			raw.WriteRune(l.ch)
			decoded.WriteRune(l.ch)
		}
	}
}

// consumeInterpolation reads the body of a "#{...}" interpolation that the
// caller has just opened (the leading "#{" is already consumed) and appends
// every rune up to and including the matching "}" to raw. It maintains a stack
// of interpolation and string contexts so that nested double-quoted strings,
// single-quoted strings, and further interpolations balance correctly instead
// of guessing where the enclosing string ends. The decoded builder is not
// updated because an interpolated string always returns its raw text; the
// parser re-scans it with the same nesting rules in findStringInterpolationEnd.
//
// It returns an error message when the input ends before every context closes.
func (l *lexer) consumeInterpolation(raw *strings.Builder) string {
	// stack holds the open contexts. isInterp reports whether a context is an
	// interpolation expression (true) or a string literal (false). For
	// interpolation contexts braceDepth tracks unmatched "{" so that an inner
	// "}" only closes the interpolation once its own braces are balanced. For
	// string contexts quote holds the delimiting rune so the matching closing
	// quote can be recognized.
	type context struct {
		isInterp   bool
		quote      rune
		braceDepth int
	}
	stack := []context{{isInterp: true}}

	for {
		l.readRune()
		if l.ch == 0 {
			return "unterminated string"
		}
		raw.WriteRune(l.ch)

		top := &stack[len(stack)-1]
		if top.isInterp {
			switch l.ch {
			case '{':
				top.braceDepth++
			case '}':
				if top.braceDepth > 0 {
					top.braceDepth--
				} else {
					stack = stack[:len(stack)-1]
					if len(stack) == 0 {
						return ""
					}
				}
			case '"', '\'':
				stack = append(stack, context{quote: l.ch})
			}
			continue
		}

		// Inside a string literal.
		switch l.ch {
		case '\\':
			// A backslash escapes the next rune so an escaped quote does not
			// close the string. Single-quoted strings only treat \' and \\ as
			// escapes, but consuming the following rune is harmless for balance
			// because no other escape can introduce or close a context.
			if l.peekRune() != 0 {
				l.readRune()
				raw.WriteRune(l.ch)
			}
		case top.quote:
			stack = stack[:len(stack)-1]
		case '#':
			// Only double-quoted strings interpolate; single quotes are literal.
			if top.quote == '"' && l.peekRune() == '{' {
				l.readRune()
				raw.WriteRune(l.ch)
				stack = append(stack, context{isInterp: true})
			}
		}
	}
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

// scanQuotedSymbol scans a quoted symbol literal such as :"foo-bar" or
// :'foo bar', producing a TokenSymbol whose literal is the decoded name. It is
// called with l.ch on the leading colon and the next rune being the opening
// quote, and tok already carries the colon's position. The quoted body reuses
// the string scanners, so single-quoted symbols take no escapes beyond \\ and
// \', and double-quoted symbols decode the same \n, \t, \", and \\ escapes that
// string literals do. Interpolation inside a double-quoted symbol is rejected:
// dynamic symbols are out of scope, and accepting the raw #{...} text as a
// literal name would silently produce the wrong symbol. An empty quoted symbol
// (:"") is a valid symbol whose name is the empty string, mirroring Ruby.
func (l *lexer) scanQuotedSymbol(tok ast.Token) ast.Token {
	l.readRune()
	switch l.ch {
	case '"':
		literal, interpolated, errMsg := l.readDoubleQuotedString()
		switch {
		case errMsg != "":
			setDiagnostic(&tok, errMsg)
		case interpolated:
			setDiagnostic(&tok, "interpolation is not allowed in a symbol literal")
		default:
			tok.Type = ast.TokenSymbol
			tok.Literal = literal
		}
	case '\'':
		literal, errMsg := l.readSingleQuotedString()
		if errMsg != "" {
			setDiagnostic(&tok, errMsg)
		} else {
			tok.Type = ast.TokenSymbol
			tok.Literal = literal
		}
	}
	return tok
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

// closeBracket records that the bracket currently under l.ch closes a `(`, `[`,
// or `{`. It discards any pending ternary `?` recorded at a deeper nesting level
// than the level the closer returns to: a ternary whose `?` sits inside the
// bracket can never be completed by a `:` outside it, so such an entry is dead
// and would otherwise linger and mis-match a later colon. The depth is floored
// at zero so unbalanced input cannot drive it negative.
func (l *lexer) closeBracket() {
	if l.bracketDepth > 0 {
		l.bracketDepth--
	}
	for len(l.ternaryStack) > 0 && l.ternaryStack[len(l.ternaryStack)-1] > l.bracketDepth {
		l.ternaryStack = l.ternaryStack[:len(l.ternaryStack)-1]
	}
}

// colonClosesTernary reports whether the colon currently under l.ch is the
// separator of an open ternary expression rather than the start of a symbol or
// label. The ternary separator follows the consequent, so it sits in
// expression-end position while a ternary `?` is still pending. It closes the
// innermost pending ternary only when the colon sits at that ternary's bracket
// nesting level: a label `:` inside a hash, array, or paren group opened after
// the `?` sits one level deeper and must not be mistaken for the separator
// (flag ? {a: 1} :"no" keeps the inner `a:` a label and the outer `:` the
// separator). Resolving this from the pending-ternary stack and the previous
// token keeps both the same-line form (flag ? 1 :"no") and the line-leading
// multiline form (flag ?\n  1\n  :"no") parsing as separator + value, where
// Ruby's lexer would otherwise read the colon-quote as a symbol. The
// consequent's own leading symbol (flag ? :"a" : :"b") is in expression-start
// position, so it is not mistaken for the separator.
func (l *lexer) colonClosesTernary() bool {
	if len(l.ternaryStack) == 0 {
		return false
	}
	if l.ternaryStack[len(l.ternaryStack)-1] != l.bracketDepth {
		return false
	}
	return canEndExpressionToken(l.lastToken.Type)
}

// colonStartsQuotedSymbol reports whether a colon followed by a quote should be
// lexed as a quoted symbol literal (:"foo") rather than a hash or keyword-argument
// label separator that happens to precede a quoted string. It is consulted only
// after colonClosesTernary has ruled out the ternary separator.
//
// A label separator abuts the name it labels with no intervening whitespace
// ({name:"Ada"}, call(name:"Ada"), {rescue:"x"}). When whitespace (including a
// line break) precedes the colon, it is never a label separator, so forms like
// the parenless call argument emit :"foo-bar" and the keyword + symbol return
// :"x" read as quoted symbols. When the colon abuts the previous token, it is a
// label separator only when that token can name a label; otherwise (a value such
// as a closing paren or integer) Ruby still reads the colon-quote as a symbol.
func (l *lexer) colonStartsQuotedSymbol() bool {
	if !l.colonAbutsPreviousToken() {
		return true
	}
	return !isLabelNameToken(l.lastToken)
}

// colonAbutsPreviousToken reports whether the colon currently under l.ch
// immediately follows the previous token with no intervening whitespace, as in
// the no-space label form rescue:"x". A space before the colon (return :"x")
// makes it non-abutting.
func (l *lexer) colonAbutsPreviousToken() bool {
	start := l.currentOffset()
	if start == 0 {
		return false
	}
	prev, _ := utf8.DecodeLastRuneInString(l.input[:start])
	return !unicode.IsSpace(prev)
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
