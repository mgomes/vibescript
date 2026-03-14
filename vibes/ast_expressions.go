package vibes

// Identifier represents a named reference in an expression.
type Identifier struct {
	Name     string
	position Position
}

func (e *Identifier) exprNode()     {}
func (e *Identifier) Pos() Position { return e.position }

// IntegerLiteral represents an integer constant.
type IntegerLiteral struct {
	Value    int64
	position Position
}

func (e *IntegerLiteral) exprNode()     {}
func (e *IntegerLiteral) Pos() Position { return e.position }

// FloatLiteral represents a floating-point constant.
type FloatLiteral struct {
	Value    float64
	position Position
}

func (e *FloatLiteral) exprNode()     {}
func (e *FloatLiteral) Pos() Position { return e.position }

// StringLiteral represents a plain string constant.
type StringLiteral struct {
	Value    string
	position Position
}

func (e *StringLiteral) exprNode()     {}
func (e *StringLiteral) Pos() Position { return e.position }

// BoolLiteral represents a boolean constant (true or false).
type BoolLiteral struct {
	Value    bool
	position Position
}

func (e *BoolLiteral) exprNode()     {}
func (e *BoolLiteral) Pos() Position { return e.position }

// NilLiteral represents the nil literal.
type NilLiteral struct {
	position Position
}

func (e *NilLiteral) exprNode()     {}
func (e *NilLiteral) Pos() Position { return e.position }

// SymbolLiteral represents a symbol literal (e.g. :foo).
type SymbolLiteral struct {
	Name     string
	position Position
}

func (e *SymbolLiteral) exprNode()     {}
func (e *SymbolLiteral) Pos() Position { return e.position }

// ArrayLiteral represents an array literal expression.
type ArrayLiteral struct {
	Elements []Expression
	position Position
}

func (e *ArrayLiteral) exprNode()     {}
func (e *ArrayLiteral) Pos() Position { return e.position }

// HashPair represents a single key-value pair in a hash literal.
type HashPair struct {
	Key   Expression
	Value Expression
}

// HashLiteral represents a hash/map literal expression.
type HashLiteral struct {
	Pairs    []HashPair
	position Position
}

func (e *HashLiteral) exprNode()     {}
func (e *HashLiteral) Pos() Position { return e.position }

// CallExpr represents a function or method call.
type CallExpr struct {
	Callee   Expression
	Args     []Expression
	KwArgs   []KeywordArg
	Block    *BlockLiteral
	position Position
}

func (e *CallExpr) exprNode()     {}
func (e *CallExpr) Pos() Position { return e.position }

// KeywordArg represents a named argument in a function call.
type KeywordArg struct {
	Name  string
	Value Expression
}

// MemberExpr represents a dot-access property lookup (e.g. obj.prop).
type MemberExpr struct {
	Object   Expression
	Property string
	position Position
}

func (e *MemberExpr) exprNode()     {}
func (e *MemberExpr) Pos() Position { return e.position }

// ScopeExpr represents a scope-resolution access (e.g. Mod::Name).
type ScopeExpr struct {
	Object   Expression
	Property string
	position Position
}

func (e *ScopeExpr) exprNode()     {}
func (e *ScopeExpr) Pos() Position { return e.position }

// IndexExpr represents a bracket-index access (e.g. arr[0]).
type IndexExpr struct {
	Object   Expression
	Index    Expression
	position Position
}

func (e *IndexExpr) exprNode()     {}
func (e *IndexExpr) Pos() Position { return e.position }

// IvarExpr represents an instance variable reference (e.g. @name).
type IvarExpr struct {
	Name     string
	position Position
}

func (e *IvarExpr) exprNode()     {}
func (e *IvarExpr) Pos() Position { return e.position }

// ClassVarExpr represents a class variable reference (e.g. @@count).
type ClassVarExpr struct {
	Name     string
	position Position
}

func (e *ClassVarExpr) exprNode()     {}
func (e *ClassVarExpr) Pos() Position { return e.position }

// UnaryExpr represents a unary operator expression (e.g. -x, !y).
type UnaryExpr struct {
	Operator TokenType
	Right    Expression
	position Position
}

func (e *UnaryExpr) exprNode()     {}
func (e *UnaryExpr) Pos() Position { return e.position }

// BinaryExpr represents a binary operator expression (e.g. a + b).
type BinaryExpr struct {
	Left     Expression
	Operator TokenType
	Right    Expression
	position Position
}

func (e *BinaryExpr) exprNode()     {}
func (e *BinaryExpr) Pos() Position { return e.position }

// RangeExpr represents a range expression (e.g. 1..10).
type RangeExpr struct {
	Start    Expression
	End      Expression
	position Position
}

func (e *RangeExpr) exprNode()     {}
func (e *RangeExpr) Pos() Position { return e.position }

// CaseWhenClause represents a single when branch in a case expression.
type CaseWhenClause struct {
	Values []Expression
	Result Expression
}

// CaseExpr represents a case/when expression.
type CaseExpr struct {
	Target   Expression
	Clauses  []CaseWhenClause
	ElseExpr Expression
	position Position
}

func (e *CaseExpr) exprNode()     {}
func (e *CaseExpr) Pos() Position { return e.position }

// BlockLiteral represents an inline block (closure) expression.
type BlockLiteral struct {
	Params   []Param
	Body     []Statement
	position Position
}

func (b *BlockLiteral) exprNode()     {}
func (b *BlockLiteral) Pos() Position { return b.position }

// YieldExpr represents a yield call that invokes the enclosing block.
type YieldExpr struct {
	Args     []Expression
	position Position
}

func (y *YieldExpr) exprNode()     {}
func (y *YieldExpr) Pos() Position { return y.position }

// InterpolatedString represents a string containing embedded expressions.
type InterpolatedString struct {
	Parts    []StringPart
	position Position
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
func (s *InterpolatedString) Pos() Position { return s.position }
