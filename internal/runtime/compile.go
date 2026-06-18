package runtime

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/internal/parser"
)

func (e *Engine) Compile(source string) (*Script, error) {
	script, _, _, err := CompileWithProgram(e, source)
	return script, err
}

// CompileWithProgram compiles source and returns the parsed program from the
// same parser pass. It is intended for internal tooling paths that need both
// diagnostics and navigation data without reparsing clean source.
func CompileWithProgram(e *Engine, source string) (*Script, *ast.Program, []error, error) {
	program, parseErrors, err := parseSource(e, source)
	if err != nil {
		return nil, program, parseErrors, err
	}

	script, err := compileParsed(e, source, program)
	return script, program, nil, err
}

// CompileSnippet compiles source as an inline snippet. Top-level declarations
// remain top-level, while executable top-level statements are moved into a
// synthetic entrypoint function so callers can invoke the snippet through the
// same Script.Call contract as ordinary scripts.
func (e *Engine) CompileSnippet(source, entrypoint string) (*Script, error) {
	script, _, _, err := CompileSnippetWithProgram(e, source, entrypoint)
	return script, err
}

// CompileSnippetWithProgram compiles source as an inline snippet and returns
// the parsed program from the same parser pass. The returned program reflects
// the user's source; only the compiled script receives the synthetic entrypoint.
func CompileSnippetWithProgram(e *Engine, source, entrypoint string) (*Script, *ast.Program, []error, error) {
	if strings.TrimSpace(entrypoint) == "" {
		return nil, nil, nil, fmt.Errorf("snippet entrypoint cannot be empty")
	}

	program, parseErrors, err := parseSource(e, source)
	if err != nil {
		return nil, program, parseErrors, err
	}

	script, err := compileParsed(e, source, snippetEntrypointProgram(program, entrypoint))
	return script, program, nil, err
}

func parseSource(e *Engine, source string) (*ast.Program, []error, error) {
	if e.config.MaxSourceBytes > 0 && len(source) > e.config.MaxSourceBytes {
		return nil, nil, fmt.Errorf("source exceeds maximum size (%d > %d bytes)", len(source), e.config.MaxSourceBytes)
	}

	program, parseErrors := parser.Parse(source)
	if len(parseErrors) > 0 {
		return program, parseErrors, combineErrors(parseErrors)
	}

	return program, nil, nil
}

func snippetEntrypointProgram(program *ast.Program, entrypoint string) *ast.Program {
	if program == nil {
		return &ast.Program{}
	}

	out := &ast.Program{Statements: make([]ast.Statement, 0, len(program.Statements)+1)}
	body := make([]ast.Statement, 0)
	pos := Position{Line: 1, Column: 1}
	for _, stmt := range program.Statements {
		switch stmt.(type) {
		case *FunctionStmt, *ClassStmt, *EnumStmt:
			out.Statements = append(out.Statements, stmt)
		default:
			if len(body) == 0 {
				pos = stmt.Pos()
			}
			body = append(body, stmt)
		}
	}
	out.Statements = append(out.Statements, &FunctionStmt{Name: entrypoint, Body: body, Position: pos})
	return out
}

func compileParsed(e *Engine, source string, program *ast.Program) (*Script, error) {
	if program == nil {
		return nil, fmt.Errorf("program is nil")
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
					Body: []Statement{&ReturnStmt{Value: &IvarExpr{Name: name, Position: prop.Position}, Position: prop.Position}},
					Pos:  prop.Position,
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
							Target:   &IvarExpr{Name: name, Position: prop.Position},
							Value:    &Identifier{Name: "value", Position: prop.Position},
							Position: prop.Position,
						},
						&ReturnStmt{Value: &Identifier{Name: "value", Position: prop.Position}, Position: prop.Position},
					},
					Pos: prop.Position,
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

func compileEnumDef(stmt *EnumStmt) (*EnumDef, error) {
	if strings.HasSuffix(stmt.Name, "?") {
		return nil, fmt.Errorf("enum name %s must not end with '?'", stmt.Name)
	}
	if typ, _ := ast.ResolveType(stmt.Name); typ != TypeUnknown {
		return nil, fmt.Errorf("enum name %s conflicts with built-in type", stmt.Name)
	}
	enumDef := &EnumDef{
		Name:         stmt.Name,
		Members:      make(map[string]*EnumValueDef, len(stmt.Members)),
		MembersByKey: make(map[string]*EnumValueDef, len(stmt.Members)),
		Order:        make([]string, 0, len(stmt.Members)),
	}
	for i, member := range stmt.Members {
		symbol := enumMemberSymbol(member.Name)
		if _, exists := enumDef.Members[member.Name]; exists {
			return nil, fmt.Errorf("duplicate enum member %s.%s", stmt.Name, member.Name)
		}
		if prior, exists := enumDef.MembersByKey[symbol]; exists {
			return nil, fmt.Errorf("enum %s member %s conflicts with %s after symbol normalization", stmt.Name, member.Name, prior.Name)
		}
		value := &EnumValueDef{
			Enum:   enumDef,
			Name:   member.Name,
			Symbol: symbol,
			Index:  i,
		}
		enumDef.Members[member.Name] = value
		enumDef.MembersByKey[symbol] = value
		enumDef.Order = append(enumDef.Order, member.Name)
	}
	return enumDef, nil
}

func enumMemberSymbol(name string) string {
	if name == "" {
		return ""
	}

	var b strings.Builder
	runes := []rune(name)
	lastUnderscore := false
	for i, r := range runes {
		if r == '_' {
			if b.Len() > 0 && !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
			continue
		}
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				var next rune
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				if prev != '_' && (unicode.IsLower(prev) || unicode.IsDigit(prev) || (next != 0 && unicode.IsLower(next))) {
					b.WriteRune('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
			continue
		}
		b.WriteRune(unicode.ToLower(r))
		lastUnderscore = false
	}
	return b.String()
}

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

func combineErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	return &combinedError{errs: errs}
}

// combinedError aggregates multiple errors while keeping the individual
// errors reachable through Unwrap, so structured data (such as parse
// positions) survives aggregation instead of being flattened to text.
type combinedError struct {
	errs []error
}

func (e *combinedError) Error() string {
	msgs := make([]string, len(e.errs))
	for i, err := range e.errs {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "\n\n")
}

func (e *combinedError) Unwrap() []error {
	return e.errs
}
