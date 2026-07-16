package main

import (
	"sort"

	"github.com/mgomes/vibescript/vibes/value"
)

// builtinCatalog is the tooling view of the runtime's registered builtins.
// Names come from Engine.Builtins rather than parallel REPL or LSP tables.
type builtinCatalog struct {
	topLevelNames   []string
	functionNames   []string
	documentedNames []string
	builtins        map[string]struct{}
	functions       map[string]struct{}
	namespaces      map[string]struct{}
}

func newBuiltinCatalog(builtins map[string]value.Value) builtinCatalog {
	catalog := builtinCatalog{
		topLevelNames: make([]string, 0, len(builtins)),
		builtins:      make(map[string]struct{}, len(builtins)),
		functions:     make(map[string]struct{}),
		namespaces:    make(map[string]struct{}),
	}
	for name, val := range builtins {
		catalog.topLevelNames = append(catalog.topLevelNames, name)
		catalog.builtins[name] = struct{}{}
		if isCallableValue(val) {
			catalog.addFunction(name)
			catalog.documentedNames = append(catalog.documentedNames, name)
			continue
		}
		if val.Kind() != value.KindObject {
			catalog.documentedNames = append(catalog.documentedNames, name)
			continue
		}
		catalog.namespaces[name] = struct{}{}
		for member, memberVal := range val.Hash() {
			qualified := name + "." + member
			catalog.documentedNames = append(catalog.documentedNames, qualified)
			if isCallableValue(memberVal) {
				catalog.addFunction(qualified)
			}
		}
	}
	sort.Strings(catalog.topLevelNames)
	sort.Strings(catalog.functionNames)
	sort.Strings(catalog.documentedNames)
	return catalog
}

func (c *builtinCatalog) addFunction(name string) {
	c.functionNames = append(c.functionNames, name)
	c.functions[name] = struct{}{}
}

func (c builtinCatalog) isBuiltin(name string) bool {
	_, ok := c.builtins[name]
	return ok
}

func (c builtinCatalog) isFunction(name string) bool {
	_, ok := c.functions[name]
	return ok
}

func (c builtinCatalog) isNamespace(name string) bool {
	_, ok := c.namespaces[name]
	return ok
}

func isCallableValue(val value.Value) bool {
	switch val.Kind() {
	case value.KindFunction, value.KindBuiltin, value.KindBlock, value.KindClass:
		return true
	default:
		return false
	}
}
