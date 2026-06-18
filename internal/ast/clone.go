package ast

// CloneParams returns a deep copy of the given parameter list.
func CloneParams(params []Param) []Param {
	return cloneParams(params)
}

// CloneTypeExpr returns a deep copy of the given type expression.
func CloneTypeExpr(ty *TypeExpr) *TypeExpr {
	return cloneTypeExpr(ty)
}

// CloneStatements returns a deep copy of the given statement slice.
func CloneStatements(statements []Statement) []Statement {
	return cloneStatements(statements)
}

func cloneParams(params []Param) []Param {
	if params == nil {
		return nil
	}
	out := make([]Param, len(params))
	for i, param := range params {
		out[i] = Param{
			Name:       param.Name,
			Kind:       param.Kind,
			Type:       cloneTypeExpr(param.Type),
			DefaultVal: cloneExpression(param.DefaultVal),
			IsIvar:     param.IsIvar,
		}
	}
	return out
}

func cloneTypeExpr(ty *TypeExpr) *TypeExpr {
	if ty == nil {
		return nil
	}
	clone := *ty
	if ty.TypeArgs != nil {
		clone.TypeArgs = make([]*TypeExpr, len(ty.TypeArgs))
		for i, arg := range ty.TypeArgs {
			clone.TypeArgs[i] = cloneTypeExpr(arg)
		}
	}
	if ty.Shape != nil {
		clone.Shape = make(map[string]*TypeExpr, len(ty.Shape))
		for name, field := range ty.Shape {
			clone.Shape[name] = cloneTypeExpr(field)
		}
	}
	if ty.Union != nil {
		clone.Union = make([]*TypeExpr, len(ty.Union))
		for i, option := range ty.Union {
			clone.Union[i] = cloneTypeExpr(option)
		}
	}
	return &clone
}

func cloneStatements(statements []Statement) []Statement {
	if statements == nil {
		return nil
	}
	out := make([]Statement, len(statements))
	for i, stmt := range statements {
		out[i] = cloneStatement(stmt)
	}
	return out
}

func cloneStatement(stmt Statement) Statement {
	switch s := stmt.(type) {
	case nil:
		return nil
	case *FunctionStmt:
		return cloneFunctionStmt(s)
	case *ReturnStmt:
		clone := *s
		clone.Value = cloneExpression(s.Value)
		return &clone
	case *RaiseStmt:
		clone := *s
		clone.Value = cloneExpression(s.Value)
		return &clone
	case *AssignStmt:
		clone := *s
		clone.Target = cloneExpression(s.Target)
		clone.Value = cloneExpression(s.Value)
		return &clone
	case *ExprStmt:
		clone := *s
		clone.Expr = cloneExpression(s.Expr)
		return &clone
	case *IfStmt:
		return cloneIfStmt(s)
	case *ForStmt:
		clone := *s
		clone.Iterable = cloneExpression(s.Iterable)
		clone.Body = cloneStatements(s.Body)
		return &clone
	case *WhileStmt:
		clone := *s
		clone.Condition = cloneExpression(s.Condition)
		clone.Body = cloneStatements(s.Body)
		return &clone
	case *UntilStmt:
		clone := *s
		clone.Condition = cloneExpression(s.Condition)
		clone.Body = cloneStatements(s.Body)
		return &clone
	case *BreakStmt:
		clone := *s
		return &clone
	case *NextStmt:
		clone := *s
		return &clone
	case *TryStmt:
		clone := *s
		clone.Body = cloneStatements(s.Body)
		clone.RescueTy = cloneTypeExpr(s.RescueTy)
		clone.Rescue = cloneStatements(s.Rescue)
		clone.Ensure = cloneStatements(s.Ensure)
		return &clone
	case *ClassStmt:
		clone := *s
		clone.Methods = cloneFunctionStmts(s.Methods)
		clone.ClassMethods = cloneFunctionStmts(s.ClassMethods)
		clone.Properties = clonePropertyDecls(s.Properties)
		clone.Body = cloneStatements(s.Body)
		return &clone
	case *EnumStmt:
		clone := *s
		clone.Members = append([]EnumMemberStmt(nil), s.Members...)
		return &clone
	default:
		return stmt
	}
}

func cloneFunctionStmt(stmt *FunctionStmt) *FunctionStmt {
	if stmt == nil {
		return nil
	}
	clone := *stmt
	clone.Params = cloneParams(stmt.Params)
	clone.ReturnTy = cloneTypeExpr(stmt.ReturnTy)
	clone.Body = cloneStatements(stmt.Body)
	return &clone
}

func cloneFunctionStmts(functions []*FunctionStmt) []*FunctionStmt {
	if functions == nil {
		return nil
	}
	out := make([]*FunctionStmt, len(functions))
	for i, fn := range functions {
		out[i] = cloneFunctionStmt(fn)
	}
	return out
}

func cloneIfStmt(stmt *IfStmt) *IfStmt {
	if stmt == nil {
		return nil
	}
	clone := *stmt
	clone.Condition = cloneExpression(stmt.Condition)
	clone.Consequent = cloneStatements(stmt.Consequent)
	if stmt.ElseIf != nil {
		clone.ElseIf = make([]*IfStmt, len(stmt.ElseIf))
		for i, branch := range stmt.ElseIf {
			clone.ElseIf[i] = cloneIfStmt(branch)
		}
	}
	clone.Alternate = cloneStatements(stmt.Alternate)
	return &clone
}

func clonePropertyDecls(properties []PropertyDecl) []PropertyDecl {
	if properties == nil {
		return nil
	}
	out := make([]PropertyDecl, len(properties))
	for i, property := range properties {
		out[i] = property
		if property.Names != nil {
			out[i].Names = append([]string{}, property.Names...)
		}
	}
	return out
}

func cloneExpressions(expressions []Expression) []Expression {
	if expressions == nil {
		return nil
	}
	out := make([]Expression, len(expressions))
	for i, expr := range expressions {
		out[i] = cloneExpression(expr)
	}
	return out
}

func cloneExpression(expr Expression) Expression {
	switch e := expr.(type) {
	case nil:
		return nil
	case *Identifier:
		clone := *e
		return &clone
	case *IntegerLiteral:
		clone := *e
		return &clone
	case *FloatLiteral:
		clone := *e
		return &clone
	case *StringLiteral:
		clone := *e
		return &clone
	case *BoolLiteral:
		clone := *e
		return &clone
	case *NilLiteral:
		clone := *e
		return &clone
	case *SymbolLiteral:
		clone := *e
		return &clone
	case *ArrayLiteral:
		clone := *e
		clone.Elements = cloneExpressions(e.Elements)
		return &clone
	case *HashLiteral:
		clone := *e
		clone.Pairs = cloneHashPairs(e.Pairs)
		return &clone
	case *CallExpr:
		clone := *e
		clone.Callee = cloneExpression(e.Callee)
		clone.Args = cloneExpressions(e.Args)
		clone.KwArgs = cloneKeywordArgs(e.KwArgs)
		clone.Block = cloneBlockLiteral(e.Block)
		return &clone
	case *MemberExpr:
		clone := *e
		clone.Object = cloneExpression(e.Object)
		return &clone
	case *ScopeExpr:
		clone := *e
		clone.Object = cloneExpression(e.Object)
		return &clone
	case *IndexExpr:
		clone := *e
		clone.Object = cloneExpression(e.Object)
		clone.Index = cloneExpression(e.Index)
		return &clone
	case *DestructureTarget:
		clone := *e
		clone.Elements = cloneDestructureElements(e.Elements)
		return &clone
	case *IvarExpr:
		clone := *e
		return &clone
	case *ClassVarExpr:
		clone := *e
		return &clone
	case *UnaryExpr:
		clone := *e
		clone.Right = cloneExpression(e.Right)
		return &clone
	case *BinaryExpr:
		clone := *e
		clone.Left = cloneExpression(e.Left)
		clone.Right = cloneExpression(e.Right)
		return &clone
	case *RangeExpr:
		clone := *e
		clone.Start = cloneExpression(e.Start)
		clone.End = cloneExpression(e.End)
		return &clone
	case *CaseExpr:
		clone := *e
		clone.Target = cloneExpression(e.Target)
		clone.Clauses = cloneCaseWhenClauses(e.Clauses)
		clone.ElseExpr = cloneExpression(e.ElseExpr)
		return &clone
	case *BlockLiteral:
		return cloneBlockLiteral(e)
	case *YieldExpr:
		clone := *e
		clone.Args = cloneExpressions(e.Args)
		return &clone
	case *InterpolatedString:
		clone := *e
		clone.Parts = cloneStringParts(e.Parts)
		return &clone
	default:
		return expr
	}
}

func cloneDestructureElements(elements []DestructureElement) []DestructureElement {
	if elements == nil {
		return nil
	}
	out := make([]DestructureElement, len(elements))
	for i, element := range elements {
		out[i] = DestructureElement{
			Target: cloneExpression(element.Target),
			Rest:   element.Rest,
		}
	}
	return out
}

func cloneHashPairs(pairs []HashPair) []HashPair {
	if pairs == nil {
		return nil
	}
	out := make([]HashPair, len(pairs))
	for i, pair := range pairs {
		out[i] = HashPair{
			Key:   cloneExpression(pair.Key),
			Value: cloneExpression(pair.Value),
		}
	}
	return out
}

func cloneKeywordArgs(args []KeywordArg) []KeywordArg {
	if args == nil {
		return nil
	}
	out := make([]KeywordArg, len(args))
	for i, arg := range args {
		out[i] = KeywordArg{
			Name:  arg.Name,
			Value: cloneExpression(arg.Value),
		}
	}
	return out
}

func cloneCaseWhenClauses(clauses []CaseWhenClause) []CaseWhenClause {
	if clauses == nil {
		return nil
	}
	out := make([]CaseWhenClause, len(clauses))
	for i, clause := range clauses {
		out[i] = CaseWhenClause{
			Values: cloneExpressions(clause.Values),
			Result: cloneExpression(clause.Result),
		}
	}
	return out
}

func cloneBlockLiteral(block *BlockLiteral) *BlockLiteral {
	if block == nil {
		return nil
	}
	clone := *block
	clone.Params = cloneParams(block.Params)
	clone.Body = cloneStatements(block.Body)
	return &clone
}

func cloneStringParts(parts []StringPart) []StringPart {
	if parts == nil {
		return nil
	}
	out := make([]StringPart, len(parts))
	for i, part := range parts {
		switch p := part.(type) {
		case StringText:
			out[i] = p
		case StringExpr:
			out[i] = StringExpr{Expr: cloneExpression(p.Expr)}
		default:
			out[i] = part
		}
	}
	return out
}
