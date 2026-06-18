// Package ast contains the Vibescript abstract syntax tree node types and
// related lexical primitives. It is an internal package: only code within
// the github.com/mgomes/vibescript module can import it.
package ast

import "github.com/mgomes/vibescript/vibes/source"

// Position is an alias for source.Position so that AST consumers can
// receive positions without importing the source package directly.
type Position = source.Position

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

// ParamKind identifies how a function parameter receives values.
type ParamKind int

const (
	ParamNormal ParamKind = iota
	ParamRest
	ParamKeywordRest
	ParamBlock
)

// Param represents a function or block parameter.
type Param struct {
	Name       string
	Kind       ParamKind
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
	Position Position
}
