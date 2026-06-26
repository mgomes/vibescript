package ast

// Identifier represents a named reference in an expression.
type Identifier struct {
	Name     string
	Position Position
}

func (e *Identifier) exprNode()     {}
func (e *Identifier) Pos() Position { return e.Position }

// IntegerLiteral represents an integer constant.
type IntegerLiteral struct {
	Value    int64
	Position Position
}

func (e *IntegerLiteral) exprNode()     {}
func (e *IntegerLiteral) Pos() Position { return e.Position }

// FloatLiteral represents a floating-point constant.
type FloatLiteral struct {
	Value    float64
	Position Position
}

func (e *FloatLiteral) exprNode()     {}
func (e *FloatLiteral) Pos() Position { return e.Position }

// StringLiteral represents a plain string constant.
type StringLiteral struct {
	Value    string
	Position Position
}

func (e *StringLiteral) exprNode()     {}
func (e *StringLiteral) Pos() Position { return e.Position }

// BoolLiteral represents a boolean constant (true or false).
type BoolLiteral struct {
	Value    bool
	Position Position
}

func (e *BoolLiteral) exprNode()     {}
func (e *BoolLiteral) Pos() Position { return e.Position }

// NilLiteral represents the nil literal.
type NilLiteral struct {
	Position Position
}

func (e *NilLiteral) exprNode()     {}
func (e *NilLiteral) Pos() Position { return e.Position }

// SymbolLiteral represents a symbol literal (e.g. :foo).
type SymbolLiteral struct {
	Name     string
	Position Position
}

func (e *SymbolLiteral) exprNode()     {}
func (e *SymbolLiteral) Pos() Position { return e.Position }

// ArrayLiteral represents an array literal expression.
type ArrayLiteral struct {
	Elements []Expression
	Position Position
}

func (e *ArrayLiteral) exprNode()     {}
func (e *ArrayLiteral) Pos() Position { return e.Position }

// HashPair represents a single key-value pair in a hash literal.
type HashPair struct {
	Key   Expression
	Value Expression
}

// HashLiteral represents a hash/map literal expression.
type HashLiteral struct {
	Pairs    []HashPair
	Position Position
}

func (e *HashLiteral) exprNode()     {}
func (e *HashLiteral) Pos() Position { return e.Position }

// CallExpr represents a function or method call.
type CallExpr struct {
	Callee Expression
	Args   []Expression
	KwArgs []KeywordArg
	// KeywordOptionsHash marks calls whose keyword arguments are eligible to
	// collapse into a trailing positional options hash when the callee has no
	// matching keyword parameter, mirroring how Ruby binds an options hash to a
	// positional parameter. It is set for parenless calls and for parenthesized
	// calls; the runtime applies the collapse only when the resolved callee
	// supports it. Parenthesized member calls additionally consult
	// Parenthesized so method and constructor calls stay strict while a
	// function value's call alias keeps direct-call parity.
	KeywordOptionsHash bool
	// Parenthesized reports whether the call used explicit parentheses. The
	// runtime keeps parenthesized method and constructor calls strict, so it
	// only collapses their keyword arguments into an options hash for the
	// parenless form.
	Parenthesized bool
	Block         *BlockLiteral
	Position      Position
}

func (e *CallExpr) exprNode()     {}
func (e *CallExpr) Pos() Position { return e.Position }

// KeywordArg represents a named argument in a function call.
type KeywordArg struct {
	Name  string
	Value Expression
}

// MemberExpr represents a dot-access property lookup (e.g. obj.prop).
type MemberExpr struct {
	Object   Expression
	Property string
	Position Position
}

func (e *MemberExpr) exprNode()     {}
func (e *MemberExpr) Pos() Position { return e.Position }

// ScopeExpr represents a scope-resolution access (e.g. Mod::Name).
type ScopeExpr struct {
	Object   Expression
	Property string
	Position Position
}

func (e *ScopeExpr) exprNode()     {}
func (e *ScopeExpr) Pos() Position { return e.Position }

// IndexExpr represents a bracket-index access (e.g. arr[0]).
type IndexExpr struct {
	Object   Expression
	Index    Expression
	Position Position
}

func (e *IndexExpr) exprNode()     {}
func (e *IndexExpr) Pos() Position { return e.Position }

// DestructureElement represents one target in a destructuring assignment. An
// anonymous rest target (a bare "*") has a nil Target with Rest set true; its
// Position records the location of the "*" for diagnostics.
type DestructureElement struct {
	Target   Expression
	Rest     bool
	Position Position
}

// DestructureTarget represents a comma-separated assignment target list.
type DestructureTarget struct {
	Elements []DestructureElement
	Position Position
}

func (e *DestructureTarget) exprNode()     {}
func (e *DestructureTarget) Pos() Position { return e.Position }

// IvarExpr represents an instance variable reference (e.g. @name).
type IvarExpr struct {
	Name     string
	Position Position
}

func (e *IvarExpr) exprNode()     {}
func (e *IvarExpr) Pos() Position { return e.Position }

// ClassVarExpr represents a class variable reference (e.g. @@count).
type ClassVarExpr struct {
	Name     string
	Position Position
}

func (e *ClassVarExpr) exprNode()     {}
func (e *ClassVarExpr) Pos() Position { return e.Position }

// UnaryExpr represents a unary operator expression (e.g. -x, !y).
type UnaryExpr struct {
	Operator TokenType
	Right    Expression
	Position Position
}

func (e *UnaryExpr) exprNode()     {}
func (e *UnaryExpr) Pos() Position { return e.Position }

// BinaryExpr represents a binary operator expression (e.g. a + b).
type BinaryExpr struct {
	Left     Expression
	Operator TokenType
	Right    Expression
	Position Position
}

func (e *BinaryExpr) exprNode()     {}
func (e *BinaryExpr) Pos() Position { return e.Position }

// ConditionalExpr represents a ternary conditional expression
// (e.g. condition ? consequent : alternate).
type ConditionalExpr struct {
	Condition  Expression
	Consequent Expression
	Alternate  Expression
	Position   Position
}

func (e *ConditionalExpr) exprNode()     {}
func (e *ConditionalExpr) Pos() Position { return e.Position }

// IfExprBranch represents one elsif branch in an if expression.
type IfExprBranch struct {
	Condition Expression
	Result    Expression
}

// IfExpr represents a value-producing if/elsif/else expression.
type IfExpr struct {
	Condition  Expression
	Consequent Expression
	ElseIf     []IfExprBranch
	Alternate  Expression
	Position   Position
}

func (e *IfExpr) exprNode()     {}
func (e *IfExpr) Pos() Position { return e.Position }

// RangeExpr represents a range expression (e.g. 1..10).
type RangeExpr struct {
	Start     Expression
	End       Expression
	Exclusive bool
	Position  Position
}

func (e *RangeExpr) exprNode()     {}
func (e *RangeExpr) Pos() Position { return e.Position }

// CaseWhenClause represents a single when branch in a case expression.
type CaseWhenClause struct {
	Values []Expression
	Result Expression
}

// CaseExpr represents a case/when expression.
type CaseExpr struct {
	// Target is nil for targetless case expressions where each when value is
	// evaluated as a predicate.
	Target   Expression
	Clauses  []CaseWhenClause
	ElseExpr Expression
	Position Position
}

func (e *CaseExpr) exprNode()     {}
func (e *CaseExpr) Pos() Position { return e.Position }

// BlockLiteral represents an inline block (closure) expression.
type BlockLiteral struct {
	Params   []Param
	Body     []Statement
	Position Position
}

func (b *BlockLiteral) exprNode()     {}
func (b *BlockLiteral) Pos() Position { return b.Position }

// YieldExpr represents a yield call that invokes the enclosing block.
type YieldExpr struct {
	Args     []Expression
	Position Position
}

func (y *YieldExpr) exprNode()     {}
func (y *YieldExpr) Pos() Position { return y.Position }

// InterpolatedString represents a string containing embedded expressions.
type InterpolatedString struct {
	Parts    []StringPart
	Position Position
}

// StringPart is the interface for parts of an interpolated string.
type StringPart interface {
	isStringPart()
}

// StringText represents a literal text segment in an interpolated string.
type StringText struct {
	Text string
}

func (StringText) isStringPart() {}

// StringExpr represents an embedded expression segment in an interpolated string.
type StringExpr struct {
	Expr Expression
}

func (StringExpr) isStringPart() {}

func (s *InterpolatedString) exprNode()     {}
func (s *InterpolatedString) Pos() Position { return s.Position }
