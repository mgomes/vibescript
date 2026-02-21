package vibes

func compileClassDef(stmt *ClassStmt) *ClassDef {
	classDef := &ClassDef{
		Name:         stmt.Name,
		Methods:      make(map[string]*ScriptFunction),
		ClassMethods: make(map[string]*ScriptFunction),
		ClassVars:    make(map[string]Value),
		Body:         stmt.Body,
	}
	for _, prop := range stmt.Properties {
		for _, name := range prop.Names {
			if prop.Kind == "property" || prop.Kind == "getter" {
				getter := &ScriptFunction{
					Name: name,
					Body: []Statement{&ReturnStmt{Value: &IvarExpr{Name: name, position: prop.position}, position: prop.position}},
					Pos:  prop.position,
				}
				classDef.Methods[name] = getter
			}
			if prop.Kind == "property" || prop.Kind == "setter" {
				setter := &ScriptFunction{
					Name: name + "=",
					Params: []Param{{
						Name: "value",
					}},
					Body: []Statement{
						&AssignStmt{
							Target:   &IvarExpr{Name: name, position: prop.position},
							Value:    &Identifier{Name: "value", position: prop.position},
							position: prop.position,
						},
						&ReturnStmt{Value: &Identifier{Name: "value", position: prop.position}, position: prop.position},
					},
					Pos: prop.position,
				}
				classDef.Methods[name+"="] = setter
			}
		}
	}
	for _, fn := range stmt.Methods {
		classDef.Methods[fn.Name] = compileFunctionDef(fn)
	}
	for _, fn := range stmt.ClassMethods {
		classDef.ClassMethods[fn.Name] = compileFunctionDef(fn)
	}
	return classDef
}
