package vibes

import (
	"fmt"
	"reflect"
	"strings"
)

func shouldExportModuleFunction(fn *ScriptFunction) bool {
	return fn != nil && !fn.Private
}

func parseRequireAlias(kwargs map[string]Value) (string, error) {
	if len(kwargs) == 0 {
		return "", nil
	}
	if len(kwargs) != 1 {
		for key := range kwargs {
			if key != "as" {
				return "", fmt.Errorf("require: unknown keyword argument %s", key)
			}
		}
		return "", fmt.Errorf("require: unknown keyword arguments")
	}

	aliasVal, ok := kwargs["as"]
	if !ok {
		for key := range kwargs {
			return "", fmt.Errorf("require: unknown keyword argument %s", key)
		}
		return "", fmt.Errorf("require: unknown keyword arguments")
	}

	var aliasName string
	switch aliasVal.Kind() {
	case KindString, KindSymbol:
		aliasName = strings.TrimSpace(aliasVal.String())
	default:
		return "", fmt.Errorf("require: alias must be a string or symbol")
	}

	if !isValidModuleAlias(aliasName) {
		return "", fmt.Errorf("require: invalid alias %q", aliasName)
	}

	return aliasName, nil
}

func isValidModuleAlias(name string) bool {
	if name == "" {
		return false
	}
	runes := []rune(name)
	if len(runes) == 0 || !isIdentifierStart(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !isIdentifierRune(r) {
			return false
		}
	}
	return lookupIdent(name) == tokenIdent
}

func bindRequireAlias(root *Env, alias string, module Value) error {
	if err := validateRequireAliasBinding(root, alias, module); err != nil {
		return err
	}
	if alias == "" {
		return nil
	}
	root.Define(alias, module)
	return nil
}

func validateRequireAliasBinding(root *Env, alias string, module Value) error {
	if alias == "" {
		return nil
	}
	if existing, ok := root.Get(alias); ok {
		if existing.Kind() == KindObject && module.Kind() == KindObject && reflect.ValueOf(existing.Hash()).Pointer() == reflect.ValueOf(module.Hash()).Pointer() {
			return nil
		}
		return fmt.Errorf("require: alias %q already defined", alias)
	}
	return nil
}

func bindModuleExportsWithoutOverwrite(root *Env, exports map[string]Value) {
	for name, fnVal := range exports {
		if _, exists := root.Get(name); exists {
			continue
		}
		root.Define(name, fnVal)
	}
}
