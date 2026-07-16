package main

import (
	"slices"
	"testing"
)

func TestBuiltinCatalogClassifiesRuntimeRegistry(t *testing.T) {
	t.Parallel()

	catalog := newTestBuiltinCatalog(t)
	for name, names := range map[string][]string{
		"top-level":  catalog.topLevelNames,
		"functions":  catalog.functionNames,
		"documented": catalog.documentedNames,
	} {
		if !slices.IsSorted(names) {
			t.Errorf("%s builtin names are not sorted: %v", name, names)
		}
	}

	tests := []struct {
		name      string
		builtin   bool
		function  bool
		namespace bool
	}{
		{name: "format", builtin: true, function: true},
		{name: "Math", builtin: true, namespace: true},
		{name: "Math.sqrt", function: true},
		{name: "Math.PI"},
		{name: "not_a_builtin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := catalog.isBuiltin(tt.name); got != tt.builtin {
				t.Errorf("isBuiltin(%q) = %v, want %v", tt.name, got, tt.builtin)
			}
			if got := catalog.isFunction(tt.name); got != tt.function {
				t.Errorf("isFunction(%q) = %v, want %v", tt.name, got, tt.function)
			}
			if got := catalog.isNamespace(tt.name); got != tt.namespace {
				t.Errorf("isNamespace(%q) = %v, want %v", tt.name, got, tt.namespace)
			}
		})
	}

	if !slices.Contains(catalog.documentedNames, "Math.PI") {
		t.Error("documented builtin names do not include Math.PI")
	}
}
