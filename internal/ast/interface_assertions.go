package ast

// Compile-time assertions that the representative AST node types
// satisfy the corresponding interfaces.
var (
	_ Node       = (*Program)(nil)
	_ Statement  = (*FunctionStmt)(nil)
	_ Expression = (*Identifier)(nil)
	_ Expression = (*ConditionalExpr)(nil)
	_ StringPart = StringText{}
	_ StringPart = StringExpr{}
)
