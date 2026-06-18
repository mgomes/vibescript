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
	Position Position
}

func (s *RaiseStmt) stmtNode()     {}
func (s *RaiseStmt) Pos() Position { return s.Position }

// AssignStmt represents a variable assignment.
type AssignStmt struct {
	Target   Expression
	Value    Expression
	Position Position
}

func (s *AssignStmt) stmtNode()     {}
func (s *AssignStmt) Pos() Position { return s.Position }

// ExprStmt wraps an expression used as a statement.
type ExprStmt struct {
	Expr     Expression
	Position Position
}

func (s *ExprStmt) stmtNode()     {}
func (s *ExprStmt) Pos() Position { return s.Position }

// IfStmt represents an if/elsif/else conditional statement.
type IfStmt struct {
	Condition  Expression
	Consequent []Statement
	ElseIf     []*IfStmt
	Alternate  []Statement
	Position   Position
}

func (s *IfStmt) stmtNode()     {}
func (s *IfStmt) Pos() Position { return s.Position }

// ForStmt represents a for-in loop.
type ForStmt struct {
	Iterator string
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
	Position  Position
}

func (s *WhileStmt) stmtNode()     {}
func (s *WhileStmt) Pos() Position { return s.Position }

// UntilStmt represents an until loop (loops while condition is false).
type UntilStmt struct {
	Condition Expression
	Body      []Statement
	Position  Position
}

func (s *UntilStmt) stmtNode()     {}
func (s *UntilStmt) Pos() Position { return s.Position }

// BreakStmt represents a break statement that exits a loop.
type BreakStmt struct {
	Position Position
}

func (s *BreakStmt) stmtNode()     {}
func (s *BreakStmt) Pos() Position { return s.Position }

// NextStmt represents a next statement that skips to the next loop iteration.
type NextStmt struct {
	Position Position
}

func (s *NextStmt) stmtNode()     {}
func (s *NextStmt) Pos() Position { return s.Position }

// TryStmt represents a begin/rescue/ensure error-handling block.
type TryStmt struct {
	Body     []Statement
	RescueTy *TypeExpr
	Rescue   []Statement
	Else     []Statement
	Ensure   []Statement
	Position Position
}

func (s *TryStmt) stmtNode()     {}
func (s *TryStmt) Pos() Position { return s.Position }

// PropertyDecl represents a property, getter, or setter declaration in a class.
type PropertyDecl struct {
	Names    []string
	Kind     string // property/getter/setter
	Position Position
}

// ClassStmt represents a class definition.
type ClassStmt struct {
	Name         string
	Methods      []*FunctionStmt
	ClassMethods []*FunctionStmt
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
