package runtime

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

// EnumDef represents a user-defined enumeration with named members.
type EnumDef struct {
	Name         string
	Members      map[string]*EnumValueDef
	MembersByKey map[string]*EnumValueDef
	Order        []string
	owner        *Script
}

// EnumValueDef represents a single member within an EnumDef.
type EnumValueDef struct {
	Enum   *EnumDef
	Name   string
	Symbol string
	Index  int
}

func enumDefsEqual(left, right *EnumDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	if left.Name != right.Name {
		return false
	}
	if left.owner == nil || right.owner == nil {
		return false
	}
	return left.owner == right.owner
}

func enumValueDefsEqual(left, right *EnumValueDef) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil {
		return false
	}
	return left.Name == right.Name &&
		left.Symbol == right.Symbol &&
		left.Index == right.Index &&
		enumDefsEqual(left.Enum, right.Enum)
}
