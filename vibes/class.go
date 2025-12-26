package vibes

type ClassDef struct {
	Name         string
	Methods      map[string]*ScriptFunction
	ClassMethods map[string]*ScriptFunction
	ClassVars    map[string]Value
	Body         []Statement
}

type Instance struct {
	Class *ClassDef
	Ivars map[string]Value
}
