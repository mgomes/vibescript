package ast

// FunctionStmt represents a function or method definition.
type FunctionStmt struct {
	Name          string
	Params        []Param
	ReturnTy      *TypeExpr
	Body          []Statement
	IsClassMethod bool
	Exported      bool
	Private       bool
	Position      Position
}

func (s *FunctionStmt) stmtNode()     {}
func (s *FunctionStmt) Pos() Position { return s.Position }

// ReturnStmt represents a return statement.
type ReturnStmt struct {
	Value    Expression
	Position Position
}

func (s *ReturnStmt) stmtNode()     {}
func (s *ReturnStmt) Pos() Position { return s.Position }

// RaiseStmt represents a raise statement that throws an error.
type RaiseStmt struct {
	Value    Expression
	Message  Expression
	Position Position
}

func (s *RaiseStmt) stmtNode()     {}
func (s *RaiseStmt) Pos() Position { return s.Position }

// AliasStmt represents a Ruby-style function or method alias declaration.
type AliasStmt struct {
	NewName  string
	OldName  string
	Method   bool
	Position Position
}

func (s *AliasStmt) stmtNode()     {}
func (s *AliasStmt) Pos() Position { return s.Position }

// AssignStmt represents a variable assignment.
type AssignStmt struct {
	Target Expression
	Value  Expression
	// Operator is empty for plain assignment and stores the compound
	// assignment operator otherwise.
	Operator TokenType
	Position Position
}

func (s *AssignStmt) stmtNode()     {}
func (s *AssignStmt) Pos() Position { return s.Position }

// LogicalStmt represents a low-precedence statement-level `and` or `or`.
type LogicalStmt struct {
	Left     Statement
	Operator TokenType
	Right    Statement
	Position Position
}

func (s *LogicalStmt) stmtNode()     {}
func (s *LogicalStmt) Pos() Position { return s.Position }

// ExprStmt wraps an expression used as a statement.
type ExprStmt struct {
	Expr     Expression
	Position Position
}

func (s *ExprStmt) stmtNode()     {}
func (s *ExprStmt) Pos() Position { return s.Position }

// IfStmt represents an if/elsif/else conditional statement.
type IfStmt struct {
	Condition         Expression
	Consequent        []Statement
	ElseIf            []*IfStmt
	Alternate         []Statement
	AlternateFirst    bool
	ModifierBodyFirst bool
	Position          Position
}

func (s *IfStmt) stmtNode()     {}
func (s *IfStmt) exprNode()     {}
func (s *IfStmt) Pos() Position { return s.Position }

// ForStmt represents a for-in loop.
type ForStmt struct {
	Target   Expression
	Iterable Expression
	Body     []Statement
	Position Position
}

func (s *ForStmt) stmtNode()     {}
func (s *ForStmt) Pos() Position { return s.Position }

// WhileStmt represents a while loop.
type WhileStmt struct {
	Condition Expression
	Body      []Statement
	BodyFirst bool
	Position  Position
}

func (s *WhileStmt) stmtNode()     {}
func (s *WhileStmt) Pos() Position { return s.Position }

// UntilStmt represents an until loop (loops while condition is false).
type UntilStmt struct {
	Condition Expression
	Body      []Statement
	BodyFirst bool
	Position  Position
}

func (s *UntilStmt) stmtNode()     {}
func (s *UntilStmt) exprNode()     {}
func (s *UntilStmt) Pos() Position { return s.Position }

func (s *ForStmt) exprNode()   {}
func (s *WhileStmt) exprNode() {}

// BreakStmt represents a break statement that exits a loop.
type BreakStmt struct {
	Value    Expression
	Position Position
}

func (s *BreakStmt) stmtNode()     {}
func (s *BreakStmt) Pos() Position { return s.Position }

// NextStmt represents a next statement that skips to the next loop iteration.
type NextStmt struct {
	Value    Expression
	Position Position
}

func (s *NextStmt) stmtNode()     {}
func (s *NextStmt) Pos() Position { return s.Position }

// RetryStmt represents a retry statement inside a rescue handler.
type RetryStmt struct {
	Position Position
}

func (s *RetryStmt) stmtNode()     {}
func (s *RetryStmt) Pos() Position { return s.Position }

// RescueClause is one ordered handler in a begin/rescue block. Ty narrows the
// error classes the clause handles (nil catches any rescuable error), Binding
// names the rescued error inside Body, and Position is the rescue keyword's.
type RescueClause struct {
	Ty       *TypeExpr
	Binding  string
	Body     []Statement
	Position Position
}

// TryStmt represents a begin/rescue/ensure error-handling block. Rescues holds
// the handlers in source order; at runtime the first clause whose type matches
// the raised error handles it, mirroring Ruby's ordered rescue dispatch.
type TryStmt struct {
	Body     []Statement
	Rescues  []RescueClause
	Else     []Statement
	Ensure   []Statement
	Position Position
}

func (s *TryStmt) stmtNode()     {}
func (s *TryStmt) exprNode()     {}
func (s *TryStmt) Pos() Position { return s.Position }

// PropertyDecl represents a property, getter, or setter declaration in a class.
type PropertyDecl struct {
	Names    []PropertyName
	Kind     string // property/getter/setter
	Position Position
}

// PropertyName is a single accessor name with an optional type annotation.
type PropertyName struct {
	Name string
	Type *TypeExpr
}

// ClassMemberDecl preserves the source order of class-level declarations.
type ClassMemberDecl struct {
	Function *FunctionStmt
	Alias    *AliasStmt
	Property *PropertyDecl
}

// ClassStmt represents a class definition.
type ClassStmt struct {
	Name         string
	Members      []ClassMemberDecl
	Methods      []*FunctionStmt
	ClassMethods []*FunctionStmt
	Aliases      []*AliasStmt
	Properties   []PropertyDecl
	Body         []Statement
	Position     Position
}

func (s *ClassStmt) stmtNode()     {}
func (s *ClassStmt) Pos() Position { return s.Position }

// EnumMemberStmt represents a single member in an enum definition.
type EnumMemberStmt struct {
	Name     string
	Position Position
}

// EnumStmt represents an enum definition.
type EnumStmt struct {
	Name     string
	Members  []EnumMemberStmt
	Position Position
}

func (s *EnumStmt) stmtNode()     {}
func (s *EnumStmt) Pos() Position { return s.Position }
