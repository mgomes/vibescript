package runtime

func (s *Script) symbolLiteralValue(lit *SymbolLiteral) Value {
	if lit == nil {
		return NewNil()
	}
	if s != nil && s.symbolLiterals != nil {
		if val, ok := s.symbolLiterals[lit]; ok {
			return val
		}
	}
	return NewSymbol(lit.Name)
}

func (exec *Execution) symbolLiteralValue(lit *SymbolLiteral) Value {
	if lit == nil {
		return NewNil()
	}
	if exec != nil && exec.script != nil {
		return exec.script.symbolLiteralValue(lit)
	}
	return NewSymbol(lit.Name)
}

func collectSymbolLiterals(script *Script) map[*SymbolLiteral]Value {
	if script == nil {
		return nil
	}
	collector := symbolLiteralCollector{}
	for _, fn := range script.functions {
		collector.collectFunction(fn)
	}
	for _, classDef := range script.classes {
		collector.collectStatements(classDef.Body)
		for _, fn := range classDef.Methods {
			collector.collectFunction(fn)
		}
		for _, fn := range classDef.ClassMethods {
			collector.collectFunction(fn)
		}
	}
	return collector.values
}

type symbolLiteralCollector struct {
	values map[*SymbolLiteral]Value
}

func (c *symbolLiteralCollector) add(lit *SymbolLiteral) {
	if lit == nil {
		return
	}
	if c.values == nil {
		c.values = make(map[*SymbolLiteral]Value)
	}
	c.values[lit] = NewSymbol(lit.Name)
}

func (c *symbolLiteralCollector) collectFunction(fn *ScriptFunction) {
	if fn == nil {
		return
	}
	c.collectParams(fn.Params)
	c.collectStatements(fn.Body)
}

func (c *symbolLiteralCollector) collectFunctionStmt(fn *FunctionStmt) {
	if fn == nil {
		return
	}
	c.collectParams(fn.Params)
	c.collectStatements(fn.Body)
}

func (c *symbolLiteralCollector) collectParams(params []Param) {
	for _, param := range params {
		c.collectExpression(param.DefaultVal)
		c.collectExpression(param.Target)
	}
}

func (c *symbolLiteralCollector) collectStatements(statements []Statement) {
	for _, stmt := range statements {
		c.collectStatement(stmt)
	}
}

func (c *symbolLiteralCollector) collectStatement(stmt Statement) {
	switch typed := stmt.(type) {
	case nil:
		return
	case *FunctionStmt:
		c.collectFunctionStmt(typed)
	case *ReturnStmt:
		c.collectExpression(typed.Value)
	case *RaiseStmt:
		c.collectExpression(typed.Value)
		c.collectExpression(typed.Message)
	case *AssignStmt:
		c.collectExpression(typed.Target)
		c.collectExpression(typed.Value)
	case *LogicalStmt:
		c.collectStatement(typed.Left)
		c.collectStatement(typed.Right)
	case *ExprStmt:
		c.collectExpression(typed.Expr)
	case *IfStmt:
		c.collectExpression(typed.Condition)
		c.collectStatements(typed.Consequent)
		for _, branch := range typed.ElseIf {
			c.collectStatement(branch)
		}
		c.collectStatements(typed.Alternate)
	case *ForStmt:
		c.collectExpression(typed.Target)
		c.collectExpression(typed.Iterable)
		c.collectStatements(typed.Body)
	case *WhileStmt:
		c.collectExpression(typed.Condition)
		c.collectStatements(typed.Body)
	case *UntilStmt:
		c.collectExpression(typed.Condition)
		c.collectStatements(typed.Body)
	case *BreakStmt:
		c.collectExpression(typed.Value)
	case *TryStmt:
		c.collectStatements(typed.Body)
		for i := range typed.Rescues {
			c.collectStatements(typed.Rescues[i].Body)
		}
		c.collectStatements(typed.Else)
		c.collectStatements(typed.Ensure)
	case *ClassStmt:
		c.collectStatements(typed.Body)
		for _, fn := range typed.Methods {
			c.collectFunctionStmt(fn)
		}
		for _, fn := range typed.ClassMethods {
			c.collectFunctionStmt(fn)
		}
	}
}

func (c *symbolLiteralCollector) collectExpression(expr Expression) {
	switch typed := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *IvarExpr, *ClassVarExpr:
		return
	case *SymbolLiteral:
		c.add(typed)
	case *ArrayLiteral:
		for _, elem := range typed.Elements {
			c.collectExpression(elem)
		}
	case *HashLiteral:
		for _, pair := range typed.Pairs {
			c.collectExpression(pair.Key)
			c.collectExpression(pair.Value)
		}
	case *CallExpr:
		c.collectExpression(typed.Callee)
		for _, arg := range typed.Args {
			c.collectExpression(arg)
		}
		for _, kwarg := range typed.KwArgs {
			c.collectExpression(kwarg.Value)
		}
		c.collectExpression(typed.BlockArg)
		c.collectBlock(typed.Block)
	case *MemberExpr:
		c.collectExpression(typed.Object)
	case *ScopeExpr:
		c.collectExpression(typed.Object)
	case *IndexExpr:
		c.collectExpression(typed.Object)
		for _, index := range typed.Indices {
			c.collectExpression(index)
		}
	case *DestructureTarget:
		for _, element := range typed.Elements {
			c.collectExpression(element.Target)
		}
	case *SplatArg:
		c.collectExpression(typed.Value)
	case *UnaryExpr:
		c.collectExpression(typed.Right)
	case *BinaryExpr:
		c.collectExpression(typed.Left)
		c.collectExpression(typed.Right)
	case *ConditionalExpr:
		c.collectExpression(typed.Condition)
		c.collectExpression(typed.Consequent)
		c.collectExpression(typed.Alternate)
	case *RescueExpr:
		c.collectExpression(typed.Body)
		c.collectExpression(typed.Fallback)
	case *IfExpr:
		c.collectExpression(typed.Condition)
		c.collectExpression(typed.Consequent)
		for _, branch := range typed.ElseIf {
			c.collectExpression(branch.Condition)
			c.collectExpression(branch.Result)
		}
		c.collectExpression(typed.Alternate)
	case *RangeExpr:
		c.collectExpression(typed.Start)
		c.collectExpression(typed.End)
	case *CaseExpr:
		c.collectExpression(typed.Target)
		for _, clause := range typed.Clauses {
			for _, value := range clause.Values {
				c.collectExpression(value.Expr)
			}
			c.collectExpression(clause.Result)
		}
		c.collectExpression(typed.ElseExpr)
	case *BlockLiteral:
		c.collectBlock(typed)
	case *YieldExpr:
		for _, arg := range typed.Args {
			c.collectExpression(arg)
		}
	case *InterpolatedString:
		c.collectStringParts(typed.Parts)
	case *InterpolatedSymbol:
		c.collectStringParts(typed.Parts)
	}
}

func (c *symbolLiteralCollector) collectBlock(block *BlockLiteral) {
	if block == nil {
		return
	}
	c.collectParams(block.Params)
	c.collectStatements(block.Body)
}

func (c *symbolLiteralCollector) collectStringParts(parts []StringPart) {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok {
			c.collectExpression(exprPart.Expr)
		}
	}
}
