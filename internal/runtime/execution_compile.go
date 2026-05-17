package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/internal/parser"
)

func (e *Engine) Compile(source string) (*Script, error) {
	if e.config.MaxSourceBytes > 0 && len(source) > e.config.MaxSourceBytes {
		return nil, fmt.Errorf("source exceeds maximum size (%d > %d bytes)", len(source), e.config.MaxSourceBytes)
	}

	program, parseErrors := parser.Parse(source)
	if len(parseErrors) > 0 {
		return nil, combineErrors(parseErrors)
	}

	functions := make(map[string]*ScriptFunction)
	classes := make(map[string]*ClassDef)
	enums := make(map[string]*EnumDef)

	for _, stmt := range program.Statements {
		switch s := stmt.(type) {
		case *FunctionStmt:
			if _, exists := functions[s.Name]; exists {
				return nil, fmt.Errorf("duplicate function %s", s.Name)
			}
			if _, exists := classes[s.Name]; exists {
				return nil, fmt.Errorf("duplicate top-level name %s", s.Name)
			}
			if _, exists := enums[s.Name]; exists {
				return nil, fmt.Errorf("duplicate top-level name %s", s.Name)
			}
			functions[s.Name] = compileFunctionDef(s)
		case *ClassStmt:
			if _, exists := classes[s.Name]; exists {
				return nil, fmt.Errorf("duplicate class %s", s.Name)
			}
			if _, exists := functions[s.Name]; exists {
				return nil, fmt.Errorf("duplicate top-level name %s", s.Name)
			}
			if _, exists := enums[s.Name]; exists {
				return nil, fmt.Errorf("duplicate top-level name %s", s.Name)
			}
			classes[s.Name] = compileClassDef(s)
		case *EnumStmt:
			if _, exists := enums[s.Name]; exists {
				return nil, fmt.Errorf("duplicate enum %s", s.Name)
			}
			if _, exists := functions[s.Name]; exists {
				return nil, fmt.Errorf("duplicate top-level name %s", s.Name)
			}
			if _, exists := classes[s.Name]; exists {
				return nil, fmt.Errorf("duplicate top-level name %s", s.Name)
			}
			enumDef, err := compileEnumDef(s)
			if err != nil {
				return nil, err
			}
			enums[s.Name] = enumDef
		default:
			return nil, fmt.Errorf("unsupported top-level statement %T", stmt)
		}
	}

	script := &Script{engine: e, functions: functions, classes: classes, enums: enums, source: source}
	script.bindFunctionOwnership()
	return script, nil
}
