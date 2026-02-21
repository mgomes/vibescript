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

type Param struct {
	Name       string
	Type       *TypeExpr
	DefaultVal Expression
	IsIvar     bool
}

type TypeKind int

const (
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
	TypeUnknown
)

type TypeExpr struct {
	Name     string
	Kind     TypeKind
	Nullable bool
	TypeArgs []*TypeExpr
	Shape    map[string]*TypeExpr
	Union    []*TypeExpr
	position Position
}
