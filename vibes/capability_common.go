package vibes

import (
	"fmt"
	"reflect"
	"strings"
)

func capabilityNameArg(method string, label string, val Value) (string, error) {
	switch val.Kind() {
	case KindString, KindSymbol:
		name := val.String()
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("%s expects %s as non-empty string or symbol", method, label)
		}
		return name, nil
	default:
		return "", fmt.Errorf("%s expects %s as string or symbol", method, label)
	}
}

func cloneCapabilityKwargs(kwargs map[string]Value) map[string]Value {
	if len(kwargs) == 0 {
		return nil
	}
	return cloneHash(kwargs)
}

func validateCapabilityKwargsDataOnly(method string, kwargs map[string]Value) error {
	for key, val := range kwargs {
		if err := validateCapabilityDataOnlyValue(fmt.Sprintf("%s keyword %s", method, key), val); err != nil {
			return err
		}
	}
	return nil
}

func isNilCapabilityImplementation(impl any) bool {
	if impl == nil {
		return true
	}
	val := reflect.ValueOf(impl)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}
