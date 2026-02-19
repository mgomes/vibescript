package vibes

// TokenType identifies the lexical category of a token.
type TokenType string

const (
	tokenIllegal TokenType = "ILLEGAL"
	tokenEOF     TokenType = "EOF"

	tokenIdent  TokenType = "IDENT"
	tokenInt    TokenType = "INT"
	tokenFloat  TokenType = "FLOAT"
	tokenString TokenType = "STRING"
	tokenSymbol TokenType = "SYMBOL"

	tokenAssign   TokenType = "="
	tokenPlus     TokenType = "+"
	tokenMinus    TokenType = "-"
	tokenBang     TokenType = "!"
	tokenAsterisk TokenType = "*"
	tokenSlash    TokenType = "/"
	tokenPercent  TokenType = "%"
	tokenLT       TokenType = "<"
	tokenGT       TokenType = ">"
	tokenLTE      TokenType = "<="
	tokenGTE      TokenType = ">="
	tokenEQ       TokenType = "=="
	tokenNotEQ    TokenType = "!="
	tokenAnd      TokenType = "&&"
	tokenOr       TokenType = "||"

	tokenComma    TokenType = ","
	tokenColon    TokenType = ":"
	tokenDot      TokenType = "."
	tokenRange    TokenType = ".."
	tokenLParen   TokenType = "("
	tokenRParen   TokenType = ")"
	tokenLBrace   TokenType = "{"
	tokenRBrace   TokenType = "}"
	tokenLBracket TokenType = "["
	tokenRBracket TokenType = "]"
	tokenPipe     TokenType = "|"
	tokenArrow    TokenType = "=>"
	tokenIvar     TokenType = "IVAR"
	tokenClassVar TokenType = "CLASSVAR"

	tokenDef      TokenType = "DEF"
	tokenClass    TokenType = "CLASS"
	tokenSelf     TokenType = "SELF"
	tokenPrivate  TokenType = "PRIVATE"
	tokenProperty TokenType = "PROPERTY"
	tokenGetter   TokenType = "GETTER"
	tokenSetter   TokenType = "SETTER"
	tokenEnd      TokenType = "END"
	tokenReturn   TokenType = "RETURN"
	tokenYield    TokenType = "YIELD"
	tokenDo       TokenType = "DO"
	tokenFor      TokenType = "FOR"
	tokenWhile    TokenType = "WHILE"
	tokenUntil    TokenType = "UNTIL"
	tokenIn       TokenType = "IN"
	tokenIf       TokenType = "IF"
	tokenElsif    TokenType = "ELSIF"
	tokenElse     TokenType = "ELSE"
	tokenTrue     TokenType = "TRUE"
	tokenFalse    TokenType = "FALSE"
	tokenNil      TokenType = "NIL"
)

// Token captures lexical information for the parser.
type Token struct {
	Type    TokenType
	Literal string
	Pos     Position
}

// Position identifies a byte offset in the source file.
type Position struct {
	Line   int
	Column int
}
