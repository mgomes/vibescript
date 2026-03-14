package vibes

// Node is the interface implemented by all AST nodes.
type Node interface {
	Pos() Position
}

// Statement is the interface implemented by all statement AST nodes.
type Statement interface {
	Node
	stmtNode()
}

// Expression is the interface implemented by all expression AST nodes.
type Expression interface {
	Node
	exprNode()
}

// Program represents the top-level AST node containing all statements.
type Program struct {
	Statements []Statement
}

func (p *Program) Pos() Position {
	if len(p.Statements) == 0 {
		return Position{}
	}
	return p.Statements[0].Pos()
}

// Param represents a function or block parameter.
type Param struct {
	Name       string
	Type       *TypeExpr
	DefaultVal Expression
	IsIvar     bool
}

// TypeKind identifies the category of a type expression.
type TypeKind int

const (
	// TypeAny is the unconstrained type that matches any value.
	TypeAny TypeKind = iota
	TypeInt
	TypeFloat
	TypeNumber
	TypeString
	TypeBool
	TypeNil
	TypeDuration
	TypeTime
	TypeMoney
	TypeArray
	TypeHash
	TypeFunction
	TypeShape
	TypeUnion
	TypeEnum
	TypeUnknown
)

// TypeExpr represents a type annotation in the source code.
type TypeExpr struct {
	Name     string
	Kind     TypeKind
	Nullable bool
	TypeArgs []*TypeExpr
	Shape    map[string]*TypeExpr
	Union    []*TypeExpr
	position Position
}
