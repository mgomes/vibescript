package vibes

func isAssignable(expr Expression) bool {
	switch expr.(type) {
	case *Identifier, *MemberExpr, *IndexExpr, *IvarExpr, *ClassVarExpr:
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

var precedences = map[TokenType]int{
	tokenOr:       precOr,
	tokenAnd:      precAnd,
	tokenEQ:       precEquality,
	tokenNotEQ:    precEquality,
	tokenLT:       precComparison,
	tokenLTE:      precComparison,
	tokenGT:       precComparison,
	tokenGTE:      precComparison,
	tokenRange:    precRange,
	tokenPlus:     precSum,
	tokenMinus:    precSum,
	tokenSlash:    precProduct,
	tokenAsterisk: precProduct,
	tokenPercent:  precProduct,
	tokenLParen:   precCall,
	tokenDot:      precCall,
	tokenLBracket: precCall,
}
