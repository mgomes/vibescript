package vibes

type Node interface {
	Pos() Position
}

type Statement interface {
	Node
	stmtNode()
}

type Expression interface {
	Node
	exprNode()
}

type Program struct {
	Statements []Statement
}

func (p *Program) Pos() Position {
	if len(p.Statements) == 0 {
		return Position{}
	}
	return p.Statements[0].Pos()
}

type FunctionStmt struct {
	Name     string
	Params   []string
	Body     []Statement
	position Position
}

func (s *FunctionStmt) stmtNode()     {}
func (s *FunctionStmt) Pos() Position { return s.position }

type ReturnStmt struct {
	Value    Expression
	position Position
}

func (s *ReturnStmt) stmtNode()     {}
func (s *ReturnStmt) Pos() Position { return s.position }

type AssignStmt struct {
	Target   Expression
	Value    Expression
	position Position
}

func (s *AssignStmt) stmtNode()     {}
func (s *AssignStmt) Pos() Position { return s.position }

type ExprStmt struct {
	Expr     Expression
	position Position
}

func (s *ExprStmt) stmtNode()     {}
func (s *ExprStmt) Pos() Position { return s.position }

type IfStmt struct {
	Condition  Expression
	Consequent []Statement
	ElseIf     []*IfStmt
	Alternate  []Statement
	position   Position
}

func (s *IfStmt) stmtNode()     {}
func (s *IfStmt) Pos() Position { return s.position }

type Identifier struct {
	Name     string
	position Position
}

func (e *Identifier) exprNode()     {}
func (e *Identifier) Pos() Position { return e.position }

type IntegerLiteral struct {
	Value    int64
	position Position
}

func (e *IntegerLiteral) exprNode()     {}
func (e *IntegerLiteral) Pos() Position { return e.position }

type FloatLiteral struct {
	Value    float64
	position Position
}

func (e *FloatLiteral) exprNode()     {}
func (e *FloatLiteral) Pos() Position { return e.position }

type StringLiteral struct {
	Value    string
	position Position
}

func (e *StringLiteral) exprNode()     {}
func (e *StringLiteral) Pos() Position { return e.position }

type BoolLiteral struct {
	Value    bool
	position Position
}

func (e *BoolLiteral) exprNode()     {}
func (e *BoolLiteral) Pos() Position { return e.position }

type NilLiteral struct {
	position Position
}

func (e *NilLiteral) exprNode()     {}
func (e *NilLiteral) Pos() Position { return e.position }

type SymbolLiteral struct {
	Name     string
	position Position
}

func (e *SymbolLiteral) exprNode()     {}
func (e *SymbolLiteral) Pos() Position { return e.position }

type ArrayLiteral struct {
	Elements []Expression
	position Position
}

func (e *ArrayLiteral) exprNode()     {}
func (e *ArrayLiteral) Pos() Position { return e.position }

type HashPair struct {
	Key   Expression
	Value Expression
}

type HashLiteral struct {
	Pairs    []HashPair
	position Position
}

func (e *HashLiteral) exprNode()     {}
func (e *HashLiteral) Pos() Position { return e.position }

type CallExpr struct {
	Callee   Expression
	Args     []Expression
	KwArgs   []KeywordArg
	position Position
}

func (e *CallExpr) exprNode()     {}
func (e *CallExpr) Pos() Position { return e.position }

type KeywordArg struct {
	Name  string
	Value Expression
}

type MemberExpr struct {
	Object   Expression
	Property string
	position Position
}

func (e *MemberExpr) exprNode()     {}
func (e *MemberExpr) Pos() Position { return e.position }

type IndexExpr struct {
	Object   Expression
	Index    Expression
	position Position
}

func (e *IndexExpr) exprNode()     {}
func (e *IndexExpr) Pos() Position { return e.position }

type UnaryExpr struct {
	Operator TokenType
	Right    Expression
	position Position
}

func (e *UnaryExpr) exprNode()     {}
func (e *UnaryExpr) Pos() Position { return e.position }

type BinaryExpr struct {
	Left     Expression
	Operator TokenType
	Right    Expression
	position Position
}

func (e *BinaryExpr) exprNode()     {}
func (e *BinaryExpr) Pos() Position { return e.position }

type InterpolatedString struct {
	Parts    []StringPart
	position Position
}

type StringPart interface {
	isStringPart()
}

type StringText struct {
	Text string
}

func (StringText) isStringPart() {}

type StringExpr struct {
	Expr Expression
}

func (StringExpr) isStringPart() {}

func (s *InterpolatedString) exprNode()     {}
func (s *InterpolatedString) Pos() Position { return s.position }
