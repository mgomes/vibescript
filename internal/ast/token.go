package ast

import (
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

	TokenIdent  TokenType = "IDENT"
	TokenInt    TokenType = "INT"
	TokenFloat  TokenType = "FLOAT"
	TokenString TokenType = "STRING"
	TokenSymbol TokenType = "SYMBOL"

	TokenAssign   TokenType = "="
	TokenPlus     TokenType = "+"
	TokenMinus    TokenType = "-"
	TokenBang     TokenType = "!"
	TokenAsterisk TokenType = "*"
	TokenSlash    TokenType = "/"
	TokenPercent  TokenType = "%"
	TokenLT       TokenType = "<"
	TokenGT       TokenType = ">"
	TokenLTE      TokenType = "<="
	TokenGTE      TokenType = ">="
	TokenEQ       TokenType = "=="
	TokenNotEQ    TokenType = "!="
	TokenAnd      TokenType = "&&"
	TokenOr       TokenType = "||"

	TokenComma    TokenType = ","
	TokenColon    TokenType = ":"
	TokenScope    TokenType = "::"
	TokenDot      TokenType = "."
	TokenRange    TokenType = ".."
	TokenLParen   TokenType = "("
	TokenRParen   TokenType = ")"
	TokenLBrace   TokenType = "{"
	TokenRBrace   TokenType = "}"
	TokenLBracket TokenType = "["
	TokenRBracket TokenType = "]"
	TokenPipe     TokenType = "|"
	TokenArrow    TokenType = "=>"
	TokenIvar     TokenType = "IVAR"
	TokenClassVar TokenType = "CLASSVAR"

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
	TokenFor      TokenType = "FOR"
	TokenWhile    TokenType = "WHILE"
	TokenUntil    TokenType = "UNTIL"
	TokenBreak    TokenType = "BREAK"
	TokenNext     TokenType = "NEXT"
	TokenIn       TokenType = "IN"
	TokenIf       TokenType = "IF"
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
}

// LookupIdent returns the TokenType for an identifier literal, falling
// back to TokenIdent when the input is not a reserved keyword.
func LookupIdent(ident string) TokenType {
	switch ident {
	case "def":
		return TokenDef
	case "class":
		return TokenClass
	case "enum":
		return TokenEnum
	case "export":
		return TokenExport
	case "self":
		return TokenSelf
	case "private":
		return TokenPrivate
	case "property":
		return TokenProperty
	case "getter":
		return TokenGetter
	case "setter":
		return TokenSetter
	case "begin":
		return TokenBegin
	case "rescue":
		return TokenRescue
	case "ensure":
		return TokenEnsure
	case "raise":
		return TokenRaise
	case "end":
		return TokenEnd
	case "return":
		return TokenReturn
	case "yield":
		return TokenYield
	case "do":
		return TokenDo
	case "for":
		return TokenFor
	case "while":
		return TokenWhile
	case "until":
		return TokenUntil
	case "break":
		return TokenBreak
	case "next":
		return TokenNext
	case "in":
		return TokenIn
	case "if":
		return TokenIf
	case "case":
		return TokenCase
	case "when":
		return TokenWhen
	case "elsif":
		return TokenElsif
	case "else":
		return TokenElse
	case "true":
		return TokenTrue
	case "false":
		return TokenFalse
	case "nil":
		return TokenNil
	}
	return TokenIdent
}
