package vibes

import "sort"

// Function looks up a compiled function by name.
func (s *Script) Function(name string) (*ScriptFunction, bool) {
	fn, ok := s.functions[name]
	return fn, ok
}

// Functions returns compiled functions in deterministic name order.
func (s *Script) Functions() []*ScriptFunction {
	names := make([]string, 0, len(s.functions))
	for name := range s.functions {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ScriptFunction, 0, len(names))
	for _, name := range names {
		out = append(out, s.functions[name])
	}
	return out
}

// Classes returns compiled classes in deterministic name order.
func (s *Script) Classes() []*ClassDef {
	names := make([]string, 0, len(s.classes))
	for name := range s.classes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*ClassDef, 0, len(names))
	for _, name := range names {
		out = append(out, s.classes[name])
	}
	return out
}

func (s *Script) bindFunctionOwnership() {
	for _, fn := range s.functions {
		fn.owner = s
	}
	for _, classDef := range s.classes {
		classDef.owner = s
		for _, fn := range classDef.Methods {
			fn.owner = s
		}
		for _, fn := range classDef.ClassMethods {
			fn.owner = s
		}
	}
}

func cloneFunctionsForCall(functions map[string]*ScriptFunction, env *Env) map[string]*ScriptFunction {
	cloned := make(map[string]*ScriptFunction, len(functions))
	for name, fn := range functions {
		cloned[name] = cloneFunctionForEnv(fn, env)
	}
	return cloned
}

func cloneClassesForCall(classes map[string]*ClassDef, env *Env) map[string]*ClassDef {
	cloned := make(map[string]*ClassDef, len(classes))
	for name, classDef := range classes {
		classClone := &ClassDef{
			Name:         classDef.Name,
			Methods:      make(map[string]*ScriptFunction, len(classDef.Methods)),
			ClassMethods: make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
			ClassVars:    make(map[string]Value),
			Body:         classDef.Body,
			owner:        classDef.owner,
		}
		for methodName, method := range classDef.Methods {
			classClone.Methods[methodName] = cloneFunctionForEnv(method, env)
		}
		for methodName, method := range classDef.ClassMethods {
			classClone.ClassMethods[methodName] = cloneFunctionForEnv(method, env)
		}
		cloned[name] = classClone
	}
	return cloned
}
