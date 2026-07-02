package ast

import (
	"sort"
	"unicode"

	"github.com/mgomes/vibescript/vibes/source"
)

// IsIdentifierStart reports whether r can be the first rune of a
// Vibescript identifier.
func IsIdentifierStart(r rune) bool {
	return unicode.IsLetter(r) || r == '_'
}

// IsIdentifierRune reports whether r can appear in a Vibescript
// identifier after the first rune.
func IsIdentifierRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '?' || r == '!'
}

// TokenType identifies the lexical category of a token.
type TokenType string

const (
	TokenIllegal TokenType = "ILLEGAL"
	TokenEOF     TokenType = "EOF"

	TokenIdent              TokenType = "IDENT"
	TokenInt                TokenType = "INT"
	TokenFloat              TokenType = "FLOAT"
	TokenString             TokenType = "STRING"
	TokenInterpolatedString TokenType = "INTERPOLATED_STRING"
	TokenSymbol             TokenType = "SYMBOL"
	TokenWords              TokenType = "WORDS"
	TokenSymbols            TokenType = "SYMBOLS"
	TokenInterpWords        TokenType = "INTERP_WORDS"
	TokenInterpSymbols      TokenType = "INTERP_SYMBOLS"

	TokenAssign         TokenType = "="
	TokenPlusAssign     TokenType = "+="
	TokenMinusAssign    TokenType = "-="
	TokenAsteriskAssign TokenType = "*="
	TokenPowerAssign    TokenType = "**="
	TokenSlashAssign    TokenType = "/="
	TokenPercentAssign  TokenType = "%="
	TokenPlus           TokenType = "+"
	TokenMinus          TokenType = "-"
	TokenBang           TokenType = "!"
	TokenNot            TokenType = "NOT"
	TokenAsterisk       TokenType = "*"
	TokenPower          TokenType = "**"
	TokenSlash          TokenType = "/"
	TokenPercent        TokenType = "%"
	TokenLT             TokenType = "<"
	TokenShovel         TokenType = "<<"
	TokenGT             TokenType = ">"
	TokenLTE            TokenType = "<="
	TokenGTE            TokenType = ">="
	TokenSpaceship      TokenType = "<=>"
	TokenEQ             TokenType = "=="
	TokenCaseEQ         TokenType = "==="
	TokenNotEQ          TokenType = "!="
	TokenAnd            TokenType = "&&"
	TokenOr             TokenType = "||"
	TokenAmpersand      TokenType = "&"
	TokenQuestion       TokenType = "?"

	TokenComma     TokenType = ","
	TokenSemicolon TokenType = ";"
	TokenColon     TokenType = ":"
	TokenScope     TokenType = "::"
	TokenDot       TokenType = "."
	TokenSafeNav   TokenType = "&."
	TokenRange     TokenType = ".."
	TokenRangeExcl TokenType = "..."
	TokenLParen    TokenType = "("
	TokenRParen    TokenType = ")"
	TokenLBrace    TokenType = "{"
	TokenRBrace    TokenType = "}"
	TokenLBracket  TokenType = "["
	TokenRBracket  TokenType = "]"
	TokenPipe      TokenType = "|"
	TokenArrow     TokenType = "=>"
	TokenThinArrow TokenType = "->"
	TokenIvar      TokenType = "IVAR"
	TokenClassVar  TokenType = "CLASSVAR"

	TokenDef      TokenType = "DEF"
	TokenClass    TokenType = "CLASS"
	TokenEnum     TokenType = "ENUM"
	TokenExport   TokenType = "EXPORT"
	TokenSelf     TokenType = "SELF"
	TokenPrivate  TokenType = "PRIVATE"
	TokenProperty TokenType = "PROPERTY"
	TokenGetter   TokenType = "GETTER"
	TokenSetter   TokenType = "SETTER"
	TokenBegin    TokenType = "BEGIN"
	TokenRescue   TokenType = "RESCUE"
	TokenEnsure   TokenType = "ENSURE"
	TokenRaise    TokenType = "RAISE"
	TokenEnd      TokenType = "END"
	TokenReturn   TokenType = "RETURN"
	TokenYield    TokenType = "YIELD"
	TokenDo       TokenType = "DO"
	TokenThen     TokenType = "THEN"
	TokenFor      TokenType = "FOR"
	TokenWhile    TokenType = "WHILE"
	TokenUntil    TokenType = "UNTIL"
	TokenBreak    TokenType = "BREAK"
	TokenNext     TokenType = "NEXT"
	TokenRetry    TokenType = "RETRY"
	TokenIn       TokenType = "IN"
	TokenIf       TokenType = "IF"
	TokenUnless   TokenType = "UNLESS"
	TokenCase     TokenType = "CASE"
	TokenWhen     TokenType = "WHEN"
	TokenElsif    TokenType = "ELSIF"
	TokenElse     TokenType = "ELSE"
	TokenTrue     TokenType = "TRUE"
	TokenFalse    TokenType = "FALSE"
	TokenNil      TokenType = "NIL"
)

// Token captures lexical information for the parser.
type Token struct {
	Type    TokenType
	Literal string
	Pos     source.Position
	// End is the exclusive position just past the token's final rune,
	// stamped by the lexer from the source text. It is the zero
	// Position only for EOF.
	End source.Position
	// Diagnostic marks an illegal token whose Literal is a human-readable
	// lexer diagnostic (such as a malformed numeric literal) rather than the
	// raw offending source text. The parser surfaces such literals verbatim,
	// while plain illegal characters fall back to a generic message.
	Diagnostic bool
}

var keywordTokenTypes = map[string]TokenType{
	"def":      TokenDef,
	"class":    TokenClass,
	"enum":     TokenEnum,
	"export":   TokenExport,
	"self":     TokenSelf,
	"private":  TokenPrivate,
	"property": TokenProperty,
	"getter":   TokenGetter,
	"setter":   TokenSetter,
	"begin":    TokenBegin,
	"rescue":   TokenRescue,
	"ensure":   TokenEnsure,
	"raise":    TokenRaise,
	"end":      TokenEnd,
	"return":   TokenReturn,
	"yield":    TokenYield,
	"do":       TokenDo,
	"then":     TokenThen,
	"for":      TokenFor,
	"while":    TokenWhile,
	"until":    TokenUntil,
	"break":    TokenBreak,
	"next":     TokenNext,
	"retry":    TokenRetry,
	"in":       TokenIn,
	"if":       TokenIf,
	"unless":   TokenUnless,
	"case":     TokenCase,
	"when":     TokenWhen,
	"elsif":    TokenElsif,
	"else":     TokenElse,
	"true":     TokenTrue,
	"false":    TokenFalse,
	"nil":      TokenNil,
}

// Keywords returns the parser's reserved keyword literals in sorted order.
func Keywords() []string {
	keywords := make([]string, 0, len(keywordTokenTypes))
	for keyword := range keywordTokenTypes {
		keywords = append(keywords, keyword)
	}
	sort.Strings(keywords)
	return keywords
}

// LookupIdent returns the TokenType for an identifier literal, falling
// back to TokenIdent when the input is not a reserved keyword.
func LookupIdent(ident string) TokenType {
	if tokenType, ok := keywordTokenTypes[ident]; ok {
		return tokenType
	}
	return TokenIdent
}
