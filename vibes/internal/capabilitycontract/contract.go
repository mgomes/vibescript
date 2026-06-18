// Package capabilitycontract centralizes the helper utilities shared by
// the carved vibes/capability/* subpackages. The helpers operate on
// vibes/value.Value so they stay free of runtime-AST imports and can be
// reused by every capability adapter without forcing each one to take a
// hard dependency on the vibes package.
package capabilitycontract

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"

	"github.com/mgomes/vibescript/vibes/value"
)

// NameArg validates that val is a non-empty string or symbol and returns
// its textual form. Used by capability adapters to interpret leading
// "name" arguments such as the collection passed to db.find.
func NameArg(method, label string, val value.Value) (string, error) {
	switch val.Kind() {
	case value.KindString, value.KindSymbol:
		name := val.String()
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("%s expects %s as non-empty string or symbol", method, label)
		}
		return name, nil
	default:
		return "", fmt.Errorf("%s expects %s as string or symbol", method, label)
	}
}

// CloneKwargs returns a deep copy of the keyword-arguments map suitable
// for handing to host callbacks without exposing script-side aliases.
func CloneKwargs(kwargs map[string]value.Value) map[string]value.Value {
	if len(kwargs) == 0 {
		return nil
	}
	return CloneHash(kwargs)
}

// CloneKwargsDataOnly validates and deep-copies keyword arguments in one
// pass so host callbacks receive isolated data-only values.
func CloneKwargsDataOnly(method string, kwargs map[string]value.Value) (map[string]value.Value, error) {
	if len(kwargs) == 0 {
		return nil, nil
	}
	out := make(map[string]value.Value, len(kwargs))
	for key, val := range kwargs {
		cloned, err := CloneDataOnlyValue(fmt.Sprintf("%s keyword %s", method, key), val)
		if err != nil {
			return nil, err
		}
		out[key] = cloned
	}
	return out, nil
}

// CloneHash returns a deep copy of the provided string-keyed map. An
// empty input returns an empty (non-nil) map to preserve historical
// behavior of the in-package helper this replaced.
func CloneHash(src map[string]value.Value) map[string]value.Value {
	if len(src) == 0 {
		return map[string]value.Value{}
	}
	out := make(map[string]value.Value, len(src))
	for k, v := range src {
		out[k] = DeepCloneValue(v)
	}
	return out
}

// DeepCloneValue recursively copies arrays, hashes, and objects so the
// host receives a fully isolated snapshot of script-supplied data.
// Scalar kinds and runtime-only kinds are returned unchanged.
func DeepCloneValue(val value.Value) value.Value {
	switch val.Kind() {
	case value.KindArray:
		arr := val.Array()
		cloned := make([]value.Value, len(arr))
		for i, elem := range arr {
			cloned[i] = DeepCloneValue(elem)
		}
		return value.NewArray(cloned)
	case value.KindHash:
		hash := val.Hash()
		cloned := make(map[string]value.Value, len(hash))
		for k, v := range hash {
			cloned[k] = DeepCloneValue(v)
		}
		return value.NewHash(cloned)
	case value.KindObject:
		obj := val.Hash()
		cloned := make(map[string]value.Value, len(obj))
		for k, v := range obj {
			cloned[k] = DeepCloneValue(v)
		}
		return value.NewObject(cloned)
	default:
		return val
	}
}

// CloneHashValue checks that val is a hash or object, validates that it
// contains only data values, and returns an isolated copy of its entries.
func CloneHashValue(label string, val value.Value) (map[string]value.Value, error) {
	cloned, err := CloneDataOnlyValue(label, val)
	if err != nil {
		return nil, err
	}
	if cloned.Kind() != value.KindHash && cloned.Kind() != value.KindObject {
		return nil, fmt.Errorf("%s expected hash, got %s", label, valueKindName(val.Kind()))
	}
	return cloned.Hash(), nil
}

// CloneDataOnlyValue validates and deep-copies val in one graph walk.
func CloneDataOnlyValue(label string, val value.Value) (value.Value, error) {
	cloned, issue := cloneDataOnlyValue(val, newSeenSet())
	if err := dataOnlyIssueError(label, issue); err != nil {
		return value.NewNil(), err
	}
	return cloned, nil
}

// IsNilImplementation reports whether impl is a nil interface or a
// typed-nil pointer / channel / func / map / slice. Capability
// constructors use it to reject zero-value implementations that would
// later panic with a nil-pointer dereference at call time.
func IsNilImplementation(impl any) bool {
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

// EnsureBlock returns a "%s requires a block" error when block is not a
// block Value. The name argument is the script-facing method name.
func EnsureBlock(block value.Value, name string) error {
	if block.Kind() != value.KindBlock {
		if name != "" {
			return fmt.Errorf("%s requires a block", name)
		}
		return fmt.Errorf("block required")
	}
	return nil
}

// ValidateDataOnlyValue rejects callable payloads (functions, blocks,
// builtins, classes, instances) and cyclic structures anywhere inside
// val. Capability boundaries call it so host code never receives a
// script-side callable it cannot safely invoke.
func ValidateDataOnlyValue(label string, val value.Value) error {
	switch validateDataOnly(val, newSeenSet(), newSeenSet()) {
	case dataOnlyCallable:
		return fmt.Errorf("%s must be data-only", label)
	case dataOnlyCycle:
		return fmt.Errorf("%s must not contain cyclic references", label)
	}
	return nil
}

// ValidateHashValue checks that val is a hash (or object) and data-only.
// The error string format matches the original validateCapabilityHashValue
// wrapper so capability tests that grep on it continue to pass.
func ValidateHashValue(label string, val value.Value) error {
	if err := ValidateDataOnlyValue(label, val); err != nil {
		return err
	}
	if val.Kind() != value.KindHash && val.Kind() != value.KindObject {
		return fmt.Errorf("%s expected hash, got %s", label, valueKindName(val.Kind()))
	}
	return nil
}

// ValidateKwargsDataOnly applies ValidateDataOnlyValue to every keyword
// argument, labeling errors with method and keyword name.
func ValidateKwargsDataOnly(method string, kwargs map[string]value.Value) error {
	for key, val := range kwargs {
		if err := ValidateDataOnlyValue(fmt.Sprintf("%s keyword %s", method, key), val); err != nil {
			return err
		}
	}
	return nil
}

// ValidateAnyReturn returns the post-call return validator used in
// CapabilityMethodContract.ValidateReturn entries for methods that
// allow any data-only return value.
func ValidateAnyReturn(method string) func(result value.Value) error {
	return func(result value.Value) error {
		return ValidateDataOnlyValue(method+" return value", result)
	}
}

// CloneMethodResult validates and deep-copies a host-returned Value so
// the host's mutable state is not aliased into the script heap.
func CloneMethodResult(method string, result value.Value) (value.Value, error) {
	return CloneDataOnlyValue(method+" return value", result)
}

type seenSet struct {
	arrays map[value.SliceIdentity]struct{}
	maps   map[uintptr]struct{}
}

func newSeenSet() *seenSet {
	return &seenSet{
		arrays: map[value.SliceIdentity]struct{}{},
		maps:   map[uintptr]struct{}{},
	}
}

type dataOnlyResult uint8

const (
	dataOnlyOK dataOnlyResult = iota
	dataOnlyCallable
	dataOnlyCycle
)

func dataOnlyIssueError(label string, issue dataOnlyResult) error {
	switch issue {
	case dataOnlyCallable:
		return fmt.Errorf("%s must be data-only", label)
	case dataOnlyCycle:
		return fmt.Errorf("%s must not contain cyclic references", label)
	default:
		return nil
	}
}

func sliceIdentity(values []value.Value) value.SliceIdentity {
	return value.SliceIdentity{
		Ptr: uintptr(unsafe.Pointer(unsafe.SliceData(values))),
		Len: len(values),
		Cap: cap(values),
	}
}

func validateDataOnly(val value.Value, visiting, seen *seenSet) dataOnlyResult {
	switch val.Kind() {
	case value.KindFunction, value.KindBuiltin, value.KindBlock, value.KindClass, value.KindInstance:
		return dataOnlyCallable
	case value.KindArray:
		values := val.Array()
		id := sliceIdentity(values)
		if _, ok := seen.arrays[id]; ok {
			return dataOnlyOK
		}
		if _, ok := visiting.arrays[id]; ok {
			return dataOnlyCycle
		}
		visiting.arrays[id] = struct{}{}
		issue := dataOnlyOK
		for _, item := range values {
			switch result := validateDataOnly(item, visiting, seen); result {
			case dataOnlyCallable:
				return dataOnlyCallable
			case dataOnlyCycle:
				issue = dataOnlyCycle
			}
		}
		delete(visiting.arrays, id)
		seen.arrays[id] = struct{}{}
		return issue
	case value.KindHash, value.KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, ok := seen.maps[ptr]; ok {
			return dataOnlyOK
		}
		if _, ok := visiting.maps[ptr]; ok {
			return dataOnlyCycle
		}
		visiting.maps[ptr] = struct{}{}
		issue := dataOnlyOK
		for _, item := range entries {
			switch result := validateDataOnly(item, visiting, seen); result {
			case dataOnlyCallable:
				return dataOnlyCallable
			case dataOnlyCycle:
				issue = dataOnlyCycle
			}
		}
		delete(visiting.maps, ptr)
		seen.maps[ptr] = struct{}{}
		return issue
	default:
		return dataOnlyOK
	}
}

func cloneDataOnlyValue(val value.Value, visiting *seenSet) (value.Value, dataOnlyResult) {
	switch val.Kind() {
	case value.KindFunction, value.KindBuiltin, value.KindBlock, value.KindClass, value.KindInstance:
		return value.NewNil(), dataOnlyCallable
	case value.KindArray:
		values := val.Array()
		id := sliceIdentity(values)
		if _, ok := visiting.arrays[id]; ok {
			return value.NewNil(), dataOnlyCycle
		}
		visiting.arrays[id] = struct{}{}
		cloned := make([]value.Value, len(values))
		issue := dataOnlyOK
		for i, item := range values {
			next, result := cloneDataOnlyValue(item, visiting)
			switch result {
			case dataOnlyCallable:
				return value.NewNil(), dataOnlyCallable
			case dataOnlyCycle:
				issue = dataOnlyCycle
			default:
				cloned[i] = next
			}
		}
		delete(visiting.arrays, id)
		if issue != dataOnlyOK {
			return value.NewNil(), issue
		}
		return value.NewArray(cloned), dataOnlyOK
	case value.KindHash:
		return cloneDataOnlyMap(val.Hash(), visiting, value.NewHash)
	case value.KindObject:
		return cloneDataOnlyMap(val.Hash(), visiting, value.NewObject)
	default:
		return val, dataOnlyOK
	}
}

func cloneDataOnlyMap(
	entries map[string]value.Value,
	visiting *seenSet,
	construct func(map[string]value.Value) value.Value,
) (value.Value, dataOnlyResult) {
	ptr := reflect.ValueOf(entries).Pointer()
	if _, ok := visiting.maps[ptr]; ok {
		return value.NewNil(), dataOnlyCycle
	}
	visiting.maps[ptr] = struct{}{}
	cloned := make(map[string]value.Value, len(entries))
	issue := dataOnlyOK
	for key, item := range entries {
		next, result := cloneDataOnlyValue(item, visiting)
		switch result {
		case dataOnlyCallable:
			return value.NewNil(), dataOnlyCallable
		case dataOnlyCycle:
			issue = dataOnlyCycle
		default:
			cloned[key] = next
		}
	}
	delete(visiting.maps, ptr)
	if issue != dataOnlyOK {
		return value.NewNil(), issue
	}
	return construct(cloned), dataOnlyOK
}

func valueKindName(kind value.ValueKind) string {
	switch kind {
	case value.KindNil:
		return "nil"
	case value.KindBool:
		return "bool"
	case value.KindInt:
		return "int"
	case value.KindFloat:
		return "float"
	case value.KindString:
		return "string"
	case value.KindArray:
		return "array"
	case value.KindHash:
		return "hash"
	case value.KindFunction:
		return "function"
	case value.KindBuiltin:
		return "builtin"
	case value.KindMoney:
		return "money"
	case value.KindDuration:
		return "duration"
	case value.KindTime:
		return "time"
	case value.KindSymbol:
		return "symbol"
	case value.KindObject:
		return "object"
	case value.KindRange:
		return "range"
	case value.KindBlock:
		return "block"
	case value.KindEnum:
		return "enum"
	case value.KindEnumValue:
		return "enum value"
	case value.KindClass:
		return "class"
	case value.KindInstance:
		return "instance"
	}
	return "unknown"
}
