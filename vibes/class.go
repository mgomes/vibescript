package vibes

// ClassDef represents a user-defined class with its methods and class-level state.
type ClassDef struct {
	Name         string
	Methods      map[string]*ScriptFunction
	ClassMethods map[string]*ScriptFunction
	ClassVars    map[string]Value
	Body         []Statement
	owner        *Script
}

// Instance represents a runtime instance of a ClassDef with its own instance variables.
type Instance struct {
	Class *ClassDef
	Ivars map[string]Value
}
