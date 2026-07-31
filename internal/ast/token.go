package ast

import (
	"sort"
	"strconv"
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
type TokenType int

const (
	// TokenNone is the zero TokenType: no token. A zero-valued Token carries
	// it, and AST nodes whose operator slot is empty (a plain assignment's
	// Operator, for example) hold it where the string-typed representation
	// held "". It is never produced by the lexer.
	TokenNone TokenType = iota
	TokenIllegal
	TokenEOF

	TokenIdent
	TokenInt
	TokenFloat
	TokenString
	TokenInterpolatedString
	TokenSymbol
	TokenWords
	TokenSymbols
	TokenInterpWords
	TokenInterpSymbols
	TokenRegex

	TokenAssign
	TokenPlusAssign
	TokenMinusAssign
	TokenAsteriskAssign
	TokenPowerAssign
	TokenSlashAssign
	TokenPercentAssign
	TokenAndAssign
	TokenOrAssign
	TokenPlus
	TokenMinus
	TokenBang
	TokenAsterisk
	TokenPower
	TokenSlash
	TokenPercent
	TokenLT
	TokenShovel
	TokenGT
	TokenLTE
	TokenGTE
	TokenSpaceship
	TokenEQ
	TokenCaseEQ
	TokenNotEQ
	TokenMatch
	TokenNotMatch
	TokenAnd
	TokenOr
	TokenAmpersand
	TokenQuestion

	TokenComma
	TokenSemicolon
	TokenColon
	TokenScope
	TokenDot
	TokenSafeNav
	TokenRange
	TokenRangeExcl
	TokenLParen
	TokenRParen
	TokenLBrace
	TokenRBrace
	TokenLBracket
	TokenRBracket
	TokenPipe
	TokenArrow
	TokenThinArrow
	TokenIvar
	TokenClassVar

	TokenDef
	TokenClass
	TokenEnum
	TokenExport
	TokenSelf
	TokenPrivate
	TokenProperty
	TokenGetter
	TokenSetter
	TokenBegin
	TokenRescue
	TokenEnsure
	TokenRaise
	TokenEnd
	TokenReturn
	TokenYield
	TokenDo
	TokenThen
	TokenFor
	TokenWhile
	TokenUntil
	TokenBreak
	TokenNext
	TokenRetry
	TokenIn
	TokenIf
	TokenUnless
	TokenCase
	TokenWhen
	TokenElsif
	TokenElse
	TokenTrue
	TokenFalse
	TokenNil
)

var tokenTypeStrings = [...]string{
	TokenIllegal:            "ILLEGAL",
	TokenEOF:                "EOF",
	TokenIdent:              "IDENT",
	TokenInt:                "INT",
	TokenFloat:              "FLOAT",
	TokenString:             "STRING",
	TokenInterpolatedString: "INTERPOLATED_STRING",
	TokenSymbol:             "SYMBOL",
	TokenWords:              "WORDS",
	TokenSymbols:            "SYMBOLS",
	TokenInterpWords:        "INTERP_WORDS",
	TokenInterpSymbols:      "INTERP_SYMBOLS",
	TokenRegex:              "REGEX",
	TokenAssign:             "=",
	TokenPlusAssign:         "+=",
	TokenMinusAssign:        "-=",
	TokenAsteriskAssign:     "*=",
	TokenPowerAssign:        "**=",
	TokenSlashAssign:        "/=",
	TokenPercentAssign:      "%=",
	TokenAndAssign:          "&&=",
	TokenOrAssign:           "||=",
	TokenPlus:               "+",
	TokenMinus:              "-",
	TokenBang:               "!",
	TokenAsterisk:           "*",
	TokenPower:              "**",
	TokenSlash:              "/",
	TokenPercent:            "%",
	TokenLT:                 "<",
	TokenShovel:             "<<",
	TokenGT:                 ">",
	TokenLTE:                "<=",
	TokenGTE:                ">=",
	TokenSpaceship:          "<=>",
	TokenEQ:                 "==",
	TokenCaseEQ:             "===",
	TokenNotEQ:              "!=",
	TokenMatch:              "=~",
	TokenNotMatch:           "!~",
	TokenAnd:                "&&",
	TokenOr:                 "||",
	TokenAmpersand:          "&",
	TokenQuestion:           "?",
	TokenComma:              ",",
	TokenSemicolon:          ";",
	TokenColon:              ":",
	TokenScope:              "::",
	TokenDot:                ".",
	TokenSafeNav:            "&.",
	TokenRange:              "..",
	TokenRangeExcl:          "...",
	TokenLParen:             "(",
	TokenRParen:             ")",
	TokenLBrace:             "{",
	TokenRBrace:             "}",
	TokenLBracket:           "[",
	TokenRBracket:           "]",
	TokenPipe:               "|",
	TokenArrow:              "=>",
	TokenThinArrow:          "->",
	TokenIvar:               "IVAR",
	TokenClassVar:           "CLASSVAR",
	TokenDef:                "DEF",
	TokenClass:              "CLASS",
	TokenEnum:               "ENUM",
	TokenExport:             "EXPORT",
	TokenSelf:               "SELF",
	TokenPrivate:            "PRIVATE",
	TokenProperty:           "PROPERTY",
	TokenGetter:             "GETTER",
	TokenSetter:             "SETTER",
	TokenBegin:              "BEGIN",
	TokenRescue:             "RESCUE",
	TokenEnsure:             "ENSURE",
	TokenRaise:              "RAISE",
	TokenEnd:                "END",
	TokenReturn:             "RETURN",
	TokenYield:              "YIELD",
	TokenDo:                 "DO",
	TokenThen:               "THEN",
	TokenFor:                "FOR",
	TokenWhile:              "WHILE",
	TokenUntil:              "UNTIL",
	TokenBreak:              "BREAK",
	TokenNext:               "NEXT",
	TokenRetry:              "RETRY",
	TokenIn:                 "IN",
	TokenIf:                 "IF",
	TokenUnless:             "UNLESS",
	TokenCase:               "CASE",
	TokenWhen:               "WHEN",
	TokenElsif:              "ELSIF",
	TokenElse:               "ELSE",
	TokenTrue:               "TRUE",
	TokenFalse:              "FALSE",
	TokenNil:                "NIL",
}

// String returns the token type's source spelling for operators and
// punctuation, and its diagnostic name for literal classes, exactly as the
// previous string-typed constants read.
func (t TokenType) String() string {
	if int(t) < len(tokenTypeStrings) {
		return tokenTypeStrings[t]
	}
	return "token(" + strconv.Itoa(int(t)) + ")"
}

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
