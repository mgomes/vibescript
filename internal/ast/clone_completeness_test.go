package ast

// This file is a completeness gate for clone.go at FIELD granularity. Gate
// coverage of the cloneStatement/cloneExpression type switches lives in
// internal/runtime's walker coverage test; this test catches the other
// historical failure mode: a node type whose case arm exists but silently
// drops or shallow-shares a field (ClassMemberDecl.Visibility/.Mixin and
// ClassStmt.Modules shipped exactly that way).
//
// For every Statement and Expression type it builds a fully populated
// instance via reflection (every field non-zero, interface fields filled with
// minimal concrete nodes, recursion depth-capped), clones it, and asserts
// both value equality and that no pointer, slice, or map is shared between
// the original and the clone.

import (
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// clonePrototypes lists one zero-value instance of every concrete AST node
// type. TestClonePrototypesCoverAllNodeTypes fails if a type implementing
// stmtNode()/exprNode() is missing here, so a new node type cannot dodge the
// field-completeness gate below.
func clonePrototypes() []Node {
	return []Node{
		// Statements.
		&FunctionStmt{},
		&ReturnStmt{},
		&RaiseStmt{},
		&AliasStmt{},
		&AssignStmt{},
		&LogicalStmt{},
		&ExprStmt{},
		&IfStmt{},
		&ForStmt{},
		&WhileStmt{},
		&UntilStmt{},
		&BreakStmt{},
		&NextStmt{},
		&RetryStmt{},
		&TryStmt{},
		&ClassStmt{},
		&EnumStmt{},
		// Expressions (statement-expressions above appear in both universes).
		&Identifier{},
		&IntegerLiteral{},
		&FloatLiteral{},
		&StringLiteral{},
		&RegexLiteral{},
		&BoolLiteral{},
		&NilLiteral{},
		&SymbolLiteral{},
		&ArrayLiteral{},
		&HashLiteral{},
		&CallExpr{},
		&SplatArg{},
		&MemberExpr{},
		&ScopeExpr{},
		&IndexExpr{},
		&DestructureTarget{},
		&IvarExpr{},
		&ClassVarExpr{},
		&UnaryExpr{},
		&BinaryExpr{},
		&ConditionalExpr{},
		&RescueExpr{},
		&IfExpr{},
		&RangeExpr{},
		&CaseExpr{},
		&BlockLiteral{},
		&YieldExpr{},
		&InterpolatedString{},
		&InterpolatedSymbol{},
		&ShapeLiteral{},
	}
}

// sharedCloneFieldExemptions lists field paths (e.g. "ClassStmt.Modules")
// that a clone may deliberately share with its original, each with a written
// justification. Every AST field is currently deep-copied, so the list is
// empty; think hard before adding to it.
var sharedCloneFieldExemptions = map[string]string{}

// TestClonePrototypesCoverAllNodeTypes cross-checks the prototype registry
// against the package source so a new node type cannot be added without also
// being registered for the field-completeness gate.
func TestClonePrototypesCoverAllNodeTypes(t *testing.T) {
	t.Parallel()

	universe := parseNodeTypeNames(t)
	if len(universe) == 0 {
		t.Fatal("no AST node types found by source scan; the gate is broken")
	}
	registered := make(map[string]struct{})
	for _, proto := range clonePrototypes() {
		registered[reflect.TypeOf(proto).Elem().Name()] = struct{}{}
	}
	for name := range universe {
		if _, ok := registered[name]; !ok {
			t.Errorf("node type %s implements stmtNode()/exprNode() but has no clonePrototypes entry; add one so clone.go stays field-complete for it", name)
		}
	}
	for name := range registered {
		if _, ok := universe[name]; !ok {
			t.Errorf("clonePrototypes lists %s, which implements neither stmtNode() nor exprNode(); remove it", name)
		}
	}
}

// TestCloneDeepCopiesEveryField populates every node type completely, clones
// it, and requires an equal, alias-free copy. A newly added field that
// clone.go fails to copy breaks the equality assertion; a field copied
// shallowly breaks the aliasing assertion.
func TestCloneDeepCopiesEveryField(t *testing.T) {
	t.Parallel()

	for _, proto := range clonePrototypes() {
		protoType := reflect.TypeOf(proto).Elem()
		name := protoType.Name()
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			populated := reflect.New(protoType)
			populateValue(populated.Elem(), 4)
			assertTopLevelFieldsPopulated(t, name, populated.Elem())

			node := populated.Interface()
			if stmt, ok := node.(Statement); ok {
				clone := cloneStatement(stmt)
				if !reflect.DeepEqual(stmt, clone) {
					t.Errorf("cloneStatement(%s) dropped or altered fields:\noriginal: %#v\nclone:    %#v", name, stmt, clone)
				}
				assertNoSharedMutableState(t, name, reflect.ValueOf(stmt), reflect.ValueOf(clone))
			}
			if expr, ok := node.(Expression); ok {
				clone := cloneExpression(expr)
				if !reflect.DeepEqual(expr, clone) {
					t.Errorf("cloneExpression(%s) dropped or altered fields:\noriginal: %#v\nclone:    %#v", name, expr, clone)
				}
				assertNoSharedMutableState(t, name, reflect.ValueOf(expr), reflect.ValueOf(clone))
			}
		})
	}
}

var (
	statementInterfaceType  = reflect.TypeOf((*Statement)(nil)).Elem()
	expressionInterfaceType = reflect.TypeOf((*Expression)(nil)).Elem()
	stringPartInterfaceType = reflect.TypeOf((*StringPart)(nil)).Elem()
)

// populateValue sets v to a fully non-zero value: strings "x", bools true,
// numbers 1, slices and maps with one populated element, pointers to
// populated values, and interface fields filled with minimal concrete nodes.
// depth caps recursion through self-referential shapes (IfStmt.ElseIf,
// TypeExpr.TypeArgs, ClassStmt.Modules).
func populateValue(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Slice:
		if depth <= 0 {
			return
		}
		slice := reflect.MakeSlice(v.Type(), 1, 1)
		populateValue(slice.Index(0), depth-1)
		v.Set(slice)
	case reflect.Map:
		if depth <= 0 {
			return
		}
		m := reflect.MakeMap(v.Type())
		key := reflect.New(v.Type().Key()).Elem()
		populateValue(key, depth-1)
		val := reflect.New(v.Type().Elem()).Elem()
		populateValue(val, depth-1)
		m.SetMapIndex(key, val)
		v.Set(m)
	case reflect.Pointer:
		if depth <= 0 {
			return
		}
		ptr := reflect.New(v.Type().Elem())
		populateValue(ptr.Elem(), depth-1)
		v.Set(ptr)
	case reflect.Interface:
		if depth <= 0 {
			return
		}
		v.Set(minimalInterfaceFill(v.Type(), depth-1))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if !field.CanSet() {
				continue
			}
			populateValue(field, depth)
		}
	}
}

// minimalInterfaceFill returns a populated leaf implementation for the AST
// interfaces that appear as fields: Expression and Node get an *Identifier,
// Statement an *ExprStmt, StringPart a StringExpr.
func minimalInterfaceFill(interfaceType reflect.Type, depth int) reflect.Value {
	var fill reflect.Value
	switch interfaceType {
	case expressionInterfaceType:
		fill = reflect.New(reflect.TypeOf(Identifier{}))
	case statementInterfaceType:
		fill = reflect.New(reflect.TypeOf(ExprStmt{}))
	case stringPartInterfaceType:
		value := reflect.New(reflect.TypeOf(StringExpr{}))
		populateValue(value.Elem(), depth)
		return value.Elem()
	default:
		fill = reflect.New(reflect.TypeOf(Identifier{}))
	}
	populateValue(fill.Elem(), depth)
	return fill
}

// assertTopLevelFieldsPopulated guards the gate against a populateValue bug
// silently weakening it: every exported field of the node under test must be
// non-zero before cloning, otherwise a dropped copy of that field would go
// unnoticed.
func assertTopLevelFieldsPopulated(t *testing.T, name string, v reflect.Value) {
	t.Helper()

	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if !field.IsExported() {
			continue
		}
		if v.Field(i).IsZero() {
			t.Fatalf("%s.%s was not populated; fix populateValue so the clone gate keeps covering it", name, field.Name)
		}
	}
}

// assertNoSharedMutableState walks the original and the clone in lockstep and
// fails if any reachable pointer, slice backing array, or map is shared,
// unless the field path is listed in sharedCloneFieldExemptions.
func assertNoSharedMutableState(t *testing.T, path string, original, clone reflect.Value) {
	t.Helper()

	if reason, ok := sharedCloneFieldExemptions[path]; ok {
		_ = reason
		return
	}
	if original.Kind() != clone.Kind() {
		return // DeepEqual already reported the divergence.
	}
	switch original.Kind() {
	case reflect.Pointer:
		if original.IsNil() || clone.IsNil() {
			return
		}
		if original.Pointer() == clone.Pointer() {
			t.Errorf("%s: clone shares the original's pointer; clone.go must deep-copy it", path)
			return
		}
		assertNoSharedMutableState(t, path, original.Elem(), clone.Elem())
	case reflect.Slice:
		if original.IsNil() || clone.IsNil() {
			return
		}
		if original.Len() > 0 && original.Pointer() == clone.Pointer() {
			t.Errorf("%s: clone shares the original's slice backing array; clone.go must copy the slice", path)
			return
		}
		for i := 0; i < original.Len() && i < clone.Len(); i++ {
			assertNoSharedMutableState(t, path+"["+strconv.Itoa(i)+"]", original.Index(i), clone.Index(i))
		}
	case reflect.Map:
		if original.IsNil() || clone.IsNil() {
			return
		}
		if original.Pointer() == clone.Pointer() {
			t.Errorf("%s: clone shares the original's map; clone.go must copy the map", path)
			return
		}
		for _, key := range original.MapKeys() {
			cloneVal := clone.MapIndex(key)
			if !cloneVal.IsValid() {
				continue // DeepEqual already reported the missing key.
			}
			assertNoSharedMutableState(t, path+"["+key.String()+"]", original.MapIndex(key), cloneVal)
		}
	case reflect.Interface:
		if original.IsNil() || clone.IsNil() {
			return
		}
		assertNoSharedMutableState(t, path, original.Elem(), clone.Elem())
	case reflect.Struct:
		for i := 0; i < original.NumField(); i++ {
			assertNoSharedMutableState(t, path+"."+original.Type().Field(i).Name, original.Field(i), clone.Field(i))
		}
	}
}

// parseNodeTypeNames scans this package's source for pointer-receiver
// stmtNode()/exprNode() methods and returns the receiver type names.
func parseNodeTypeNames(t *testing.T) map[string]struct{} {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	names := make(map[string]struct{})
	for _, entry := range entries {
		fileName := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(fileName, ".go") || strings.HasSuffix(fileName, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		parsed, err := goparser.ParseFile(fset, filepath.Join(".", fileName), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", fileName, err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if fn.Name.Name != "stmtNode" && fn.Name.Name != "exprNode" {
				continue
			}
			recv := fn.Recv.List[0].Type
			if star, ok := recv.(*goast.StarExpr); ok {
				recv = star.X
			}
			if ident, ok := recv.(*goast.Ident); ok {
				names[ident.Name] = struct{}{}
			}
		}
	}
	return names
}
