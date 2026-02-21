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
			classes[s.Name] = compileClassDef(s)
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
