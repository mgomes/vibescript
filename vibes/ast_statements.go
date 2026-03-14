package vibes

// FunctionStmt represents a function or method definition.
type FunctionStmt struct {
	Name          string
	Params        []Param
	ReturnTy      *TypeExpr
	Body          []Statement
	IsClassMethod bool
	Exported      bool
	Private       bool
	position      Position
}

func (s *FunctionStmt) stmtNode()     {}
func (s *FunctionStmt) Pos() Position { return s.position }

// ReturnStmt represents a return statement.
type ReturnStmt struct {
	Value    Expression
	position Position
}

func (s *ReturnStmt) stmtNode()     {}
func (s *ReturnStmt) Pos() Position { return s.position }

// RaiseStmt represents a raise statement that throws an error.
type RaiseStmt struct {
	Value    Expression
	position Position
}

func (s *RaiseStmt) stmtNode()     {}
func (s *RaiseStmt) Pos() Position { return s.position }

// AssignStmt represents a variable assignment.
type AssignStmt struct {
	Target   Expression
	Value    Expression
	position Position
}

func (s *AssignStmt) stmtNode()     {}
func (s *AssignStmt) Pos() Position { return s.position }

// ExprStmt wraps an expression used as a statement.
type ExprStmt struct {
	Expr     Expression
	position Position
}

func (s *ExprStmt) stmtNode()     {}
func (s *ExprStmt) Pos() Position { return s.position }

// IfStmt represents an if/elsif/else conditional statement.
type IfStmt struct {
	Condition  Expression
	Consequent []Statement
	ElseIf     []*IfStmt
	Alternate  []Statement
	position   Position
}

func (s *IfStmt) stmtNode()     {}
func (s *IfStmt) Pos() Position { return s.position }

// ForStmt represents a for-in loop.
type ForStmt struct {
	Iterator string
	Iterable Expression
	Body     []Statement
	position Position
}

func (s *ForStmt) stmtNode()     {}
func (s *ForStmt) Pos() Position { return s.position }

// WhileStmt represents a while loop.
type WhileStmt struct {
	Condition Expression
	Body      []Statement
	position  Position
}

func (s *WhileStmt) stmtNode()     {}
func (s *WhileStmt) Pos() Position { return s.position }

// UntilStmt represents an until loop (loops while condition is false).
type UntilStmt struct {
	Condition Expression
	Body      []Statement
	position  Position
}

func (s *UntilStmt) stmtNode()     {}
func (s *UntilStmt) Pos() Position { return s.position }

// BreakStmt represents a break statement that exits a loop.
type BreakStmt struct {
	position Position
}

func (s *BreakStmt) stmtNode()     {}
func (s *BreakStmt) Pos() Position { return s.position }

// NextStmt represents a next statement that skips to the next loop iteration.
type NextStmt struct {
	position Position
}

func (s *NextStmt) stmtNode()     {}
func (s *NextStmt) Pos() Position { return s.position }

// TryStmt represents a begin/rescue/ensure error-handling block.
type TryStmt struct {
	Body     []Statement
	RescueTy *TypeExpr
	Rescue   []Statement
	Ensure   []Statement
	position Position
}

func (s *TryStmt) stmtNode()     {}
func (s *TryStmt) Pos() Position { return s.position }

// PropertyDecl represents a property, getter, or setter declaration in a class.
type PropertyDecl struct {
	Names    []string
	Kind     string // property/getter/setter
	position Position
}

// ClassStmt represents a class definition.
type ClassStmt struct {
	Name         string
	Methods      []*FunctionStmt
	ClassMethods []*FunctionStmt
	Properties   []PropertyDecl
	Body         []Statement
	position     Position
}

func (s *ClassStmt) stmtNode()     {}
func (s *ClassStmt) Pos() Position { return s.position }

// EnumMemberStmt represents a single member in an enum definition.
type EnumMemberStmt struct {
	Name     string
	position Position
}

// EnumStmt represents an enum definition.
type EnumStmt struct {
	Name     string
	Members  []EnumMemberStmt
	position Position
}

func (s *EnumStmt) stmtNode()     {}
func (s *EnumStmt) Pos() Position { return s.position }
