package vibes

import (
	"errors"
	"fmt"
)

func (e *Engine) Compile(source string) (*Script, error) {
	p := newParser(source)
	program, parseErrors := p.ParseProgram()
	if len(parseErrors) > 0 {
		return nil, combineErrors(parseErrors)
	}

	functions := make(map[string]*ScriptFunction)
	classes := make(map[string]*ClassDef)

	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *FunctionStmt:
			if _, exists := functions[s.Name]; exists {
				return nil, fmt.Errorf("duplicate function %s", s.Name)
			}
			functions[s.Name] = &ScriptFunction{Name: s.Name, Params: s.Params, ReturnTy: s.ReturnTy, Body: s.Body, Pos: s.Pos(), Exported: s.Exported, Private: s.Private}
		case *ClassStmt:
			if _, exists := classes[s.Name]; exists {
				return nil, fmt.Errorf("duplicate class %s", s.Name)
			}
			classDef := &ClassDef{
				Name:         s.Name,
				Methods:      make(map[string]*ScriptFunction),
				ClassMethods: make(map[string]*ScriptFunction),
				ClassVars:    make(map[string]Value),
				Body:         s.Body,
			}
			for _, prop := range s.Properties {
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
			for _, fn := range s.Methods {
				classDef.Methods[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, ReturnTy: fn.ReturnTy, Body: fn.Body, Pos: fn.Pos(), Private: fn.Private}
			}
			for _, fn := range s.ClassMethods {
				classDef.ClassMethods[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, ReturnTy: fn.ReturnTy, Body: fn.Body, Pos: fn.Pos(), Private: fn.Private}
			}
			classes[s.Name] = classDef
		default:
			return nil, fmt.Errorf("unsupported top-level statement %T", stmt)
		}
	}

	script := &Script{engine: e, functions: functions, classes: classes, source: source}
	script.bindFunctionOwnership()
	return script, nil
}

func combineErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := ""
	for _, err := range errs {
		if msg != "" {
			msg += "\n\n"
		}
		msg += err.Error()
	}
	return errors.New(msg)
}
