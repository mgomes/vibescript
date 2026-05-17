package runtime

import (
	"errors"
	"fmt"
)

var (
	capabilityTypeAny = &TypeExpr{
		Name: "any",
		Kind: TypeAny,
	}
	capabilityTypeHash = &TypeExpr{
		Name: "hash",
		Kind: TypeHash,
	}
)

func cloneCapabilityKwargs(kwargs map[string]Value) map[string]Value {
	if len(kwargs) == 0 {
		return nil
	}
	return cloneHash(kwargs)
}

func validateCapabilityKwargsDataOnly(method string, kwargs map[string]Value) error {
	for key, val := range kwargs {
		if err := validateCapabilityTypedValue(fmt.Sprintf("%s keyword %s", method, key), val, capabilityTypeAny); err != nil {
			return err
		}
	}
	return nil
}

func validateCapabilityTypedValue(label string, val Value, ty *TypeExpr) error {
	if err := validateCapabilityDataOnlyValue(label, val); err != nil {
		return err
	}
	if err := checkValueType(val, ty); err != nil {
		var mismatch *typeMismatchError
		if errors.As(err, &mismatch) {
			return fmt.Errorf("%s expected %s, got %s", label, mismatch.Expected, mismatch.Actual)
		}
		return err
	}
	return nil
}

func validateCapabilityHashValue(label string, val Value) error {
	return validateCapabilityTypedValue(label, val, capabilityTypeHash)
}

func capabilityValidateAnyReturn(method string) func(result Value) error {
	return func(result Value) error {
		return validateCapabilityTypedValue(method+" return value", result, capabilityTypeAny)
	}
}

func cloneCapabilityMethodResult(method string, result Value) (Value, error) {
	if err := validateCapabilityTypedValue(method+" return value", result, capabilityTypeAny); err != nil {
		return NewNil(), err
	}
	return deepCloneValue(result), nil
}
