package vibes

func compileFunctionDef(stmt *FunctionStmt) *ScriptFunction {
	return &ScriptFunction{
		Name:     stmt.Name,
		Params:   stmt.Params,
		ReturnTy: stmt.ReturnTy,
		Body:     stmt.Body,
		Pos:      stmt.Pos(),
		Exported: stmt.Exported,
		Private:  stmt.Private,
	}
}
