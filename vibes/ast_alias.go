package vibes

import (
	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes/source"
)

// Position is the public source-location type exposed on RuntimeError
// and related public surfaces. It now lives in vibes/source so the AST
// (in internal/ast) and the public error surface can share a single
// definition without forcing AST consumers to import vibes.
type Position = source.Position

// The following type aliases re-export the moved AST types so that
// callers of the vibes package built before v0.28.3 continue to
// compile. The AST is now an internal implementation detail and these
// aliases will be removed in v0.29.0.
//
// Deprecated: AST node access is no longer part of the supported
// public surface. Drive scripts through the vibes Engine and Script
// types instead.
type (
	Node       = ast.Node
	Statement  = ast.Statement
	Expression = ast.Expression
	Program    = ast.Program

	Param    = ast.Param
	TypeExpr = ast.TypeExpr
	TypeKind = ast.TypeKind

	Token     = ast.Token
	TokenType = ast.TokenType

	FunctionStmt   = ast.FunctionStmt
	ReturnStmt     = ast.ReturnStmt
	RaiseStmt      = ast.RaiseStmt
	AssignStmt     = ast.AssignStmt
	ExprStmt       = ast.ExprStmt
	IfStmt         = ast.IfStmt
	ForStmt        = ast.ForStmt
	WhileStmt      = ast.WhileStmt
	UntilStmt      = ast.UntilStmt
	BreakStmt      = ast.BreakStmt
	NextStmt       = ast.NextStmt
	TryStmt        = ast.TryStmt
	PropertyDecl   = ast.PropertyDecl
	ClassStmt      = ast.ClassStmt
	EnumMemberStmt = ast.EnumMemberStmt
	EnumStmt       = ast.EnumStmt

	Identifier         = ast.Identifier
	IntegerLiteral     = ast.IntegerLiteral
	FloatLiteral       = ast.FloatLiteral
	StringLiteral      = ast.StringLiteral
	BoolLiteral        = ast.BoolLiteral
	NilLiteral         = ast.NilLiteral
	SymbolLiteral      = ast.SymbolLiteral
	ArrayLiteral       = ast.ArrayLiteral
	HashPair           = ast.HashPair
	HashLiteral        = ast.HashLiteral
	CallExpr           = ast.CallExpr
	KeywordArg         = ast.KeywordArg
	MemberExpr         = ast.MemberExpr
	ScopeExpr          = ast.ScopeExpr
	IndexExpr          = ast.IndexExpr
	IvarExpr           = ast.IvarExpr
	ClassVarExpr       = ast.ClassVarExpr
	UnaryExpr          = ast.UnaryExpr
	BinaryExpr         = ast.BinaryExpr
	RangeExpr          = ast.RangeExpr
	CaseWhenClause     = ast.CaseWhenClause
	CaseExpr           = ast.CaseExpr
	BlockLiteral       = ast.BlockLiteral
	YieldExpr          = ast.YieldExpr
	InterpolatedString = ast.InterpolatedString
	StringPart         = ast.StringPart
	StringText         = ast.StringText
	StringExpr         = ast.StringExpr
)

// TypeKind constants re-exported for the deprecated alias surface.
const (
	TypeAny      = ast.TypeAny
	TypeInt      = ast.TypeInt
	TypeFloat    = ast.TypeFloat
	TypeNumber   = ast.TypeNumber
	TypeString   = ast.TypeString
	TypeBool     = ast.TypeBool
	TypeNil      = ast.TypeNil
	TypeDuration = ast.TypeDuration
	TypeTime     = ast.TypeTime
	TypeMoney    = ast.TypeMoney
	TypeArray    = ast.TypeArray
	TypeHash     = ast.TypeHash
	TypeFunction = ast.TypeFunction
	TypeShape    = ast.TypeShape
	TypeUnion    = ast.TypeUnion
	TypeEnum     = ast.TypeEnum
	TypeUnknown  = ast.TypeUnknown
)

// Token type constants kept available for backwards compatibility with
// any external code that previously inspected operator AST nodes. The
// vibes package no longer ships token-level helpers; use the parser
// directly through Engine.Compile for parsing needs.
const (
	tokenIllegal  = ast.TokenIllegal
	tokenEOF      = ast.TokenEOF
	tokenIdent    = ast.TokenIdent
	tokenInt      = ast.TokenInt
	tokenFloat    = ast.TokenFloat
	tokenString   = ast.TokenString
	tokenSymbol   = ast.TokenSymbol
	tokenAssign   = ast.TokenAssign
	tokenPlus     = ast.TokenPlus
	tokenMinus    = ast.TokenMinus
	tokenBang     = ast.TokenBang
	tokenAsterisk = ast.TokenAsterisk
	tokenSlash    = ast.TokenSlash
	tokenPercent  = ast.TokenPercent
	tokenLT       = ast.TokenLT
	tokenGT       = ast.TokenGT
	tokenLTE      = ast.TokenLTE
	tokenGTE      = ast.TokenGTE
	tokenEQ       = ast.TokenEQ
	tokenNotEQ    = ast.TokenNotEQ
	tokenAnd      = ast.TokenAnd
	tokenOr       = ast.TokenOr
	tokenComma    = ast.TokenComma
	tokenColon    = ast.TokenColon
	tokenScope    = ast.TokenScope
	tokenDot      = ast.TokenDot
	tokenRange    = ast.TokenRange
	tokenLParen   = ast.TokenLParen
	tokenRParen   = ast.TokenRParen
	tokenLBrace   = ast.TokenLBrace
	tokenRBrace   = ast.TokenRBrace
	tokenLBracket = ast.TokenLBracket
	tokenRBracket = ast.TokenRBracket
	tokenPipe     = ast.TokenPipe
	tokenArrow    = ast.TokenArrow
	tokenIvar     = ast.TokenIvar
	tokenClassVar = ast.TokenClassVar
	tokenDef      = ast.TokenDef
	tokenClass    = ast.TokenClass
	tokenEnum     = ast.TokenEnum
	tokenExport   = ast.TokenExport
	tokenSelf     = ast.TokenSelf
	tokenPrivate  = ast.TokenPrivate
	tokenProperty = ast.TokenProperty
	tokenGetter   = ast.TokenGetter
	tokenSetter   = ast.TokenSetter
	tokenBegin    = ast.TokenBegin
	tokenRescue   = ast.TokenRescue
	tokenEnsure   = ast.TokenEnsure
	tokenRaise    = ast.TokenRaise
	tokenEnd      = ast.TokenEnd
	tokenReturn   = ast.TokenReturn
	tokenYield    = ast.TokenYield
	tokenDo       = ast.TokenDo
	tokenFor      = ast.TokenFor
	tokenWhile    = ast.TokenWhile
	tokenUntil    = ast.TokenUntil
	tokenBreak    = ast.TokenBreak
	tokenNext     = ast.TokenNext
	tokenIn       = ast.TokenIn
	tokenIf       = ast.TokenIf
	tokenCase     = ast.TokenCase
	tokenWhen     = ast.TokenWhen
	tokenElsif    = ast.TokenElsif
	tokenElse     = ast.TokenElse
	tokenTrue     = ast.TokenTrue
	tokenFalse    = ast.TokenFalse
	tokenNil      = ast.TokenNil
)

// AST clone helpers preserved as package-private aliases for the
// runtime, which deep-copies functions/blocks/classes when binding
// them across module boundaries.
func cloneParams(params []Param) []Param            { return ast.CloneParams(params) }
func cloneTypeExpr(ty *TypeExpr) *TypeExpr          { return ast.CloneTypeExpr(ty) }
func cloneStatements(stmts []Statement) []Statement { return ast.CloneStatements(stmts) }
