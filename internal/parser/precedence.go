package parser

import (
	"github.com/mgomes/vibescript/internal/ast"
)

func isAssignable(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.Identifier, *ast.MemberExpr, *ast.IndexExpr, *ast.IvarExpr, *ast.ClassVarExpr:
		return true
	default:
		return false
	}
}

const (
	lowestPrec = iota
	precAssign
	precOr
	precAnd
	precEquality
	precComparison
	precRange
	precSum
	precProduct
	precPrefix
	precCall
)

var precedences = map[ast.TokenType]int{
	ast.TokenOr:       precOr,
	ast.TokenAnd:      precAnd,
	ast.TokenEQ:       precEquality,
	ast.TokenNotEQ:    precEquality,
	ast.TokenLT:       precComparison,
	ast.TokenLTE:      precComparison,
	ast.TokenGT:       precComparison,
	ast.TokenGTE:      precComparison,
	ast.TokenRange:    precRange,
	ast.TokenPlus:     precSum,
	ast.TokenMinus:    precSum,
	ast.TokenSlash:    precProduct,
	ast.TokenAsterisk: precProduct,
	ast.TokenPercent:  precProduct,
	ast.TokenLParen:   precCall,
	ast.TokenDot:      precCall,
	ast.TokenScope:    precCall,
	ast.TokenLBracket: precCall,
}
