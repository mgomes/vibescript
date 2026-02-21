package vibes

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

type ReturnStmt struct {
	Value    Expression
	position Position
}

func (s *ReturnStmt) stmtNode()     {}
func (s *ReturnStmt) Pos() Position { return s.position }

type RaiseStmt struct {
	Value    Expression
	position Position
}

func (s *RaiseStmt) stmtNode()     {}
func (s *RaiseStmt) Pos() Position { return s.position }

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

type ForStmt struct {
	Iterator string
	Iterable Expression
	Body     []Statement
	position Position
}

func (s *ForStmt) stmtNode()     {}
func (s *ForStmt) Pos() Position { return s.position }

type WhileStmt struct {
	Condition Expression
	Body      []Statement
	position  Position
}

func (s *WhileStmt) stmtNode()     {}
func (s *WhileStmt) Pos() Position { return s.position }

type UntilStmt struct {
	Condition Expression
	Body      []Statement
	position  Position
}

func (s *UntilStmt) stmtNode()     {}
func (s *UntilStmt) Pos() Position { return s.position }

type BreakStmt struct {
	position Position
}

func (s *BreakStmt) stmtNode()     {}
func (s *BreakStmt) Pos() Position { return s.position }

type NextStmt struct {
	position Position
}

func (s *NextStmt) stmtNode()     {}
func (s *NextStmt) Pos() Position { return s.position }

type TryStmt struct {
	Body     []Statement
	RescueTy *TypeExpr
	Rescue   []Statement
	Ensure   []Statement
	position Position
}

func (s *TryStmt) stmtNode()     {}
func (s *TryStmt) Pos() Position { return s.position }

type PropertyDecl struct {
	Names    []string
	Kind     string // property/getter/setter
	position Position
}

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
