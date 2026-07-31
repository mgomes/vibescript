package runtime

import (
	"errors"
	"fmt"

	"github.com/mgomes/vibescript/internal/parser"
)

// Signature is the opt-in static contract a host callable publishes to the
// checker. Type spellings use the script annotation grammar ("int",
// "array<string>", "{ name: string }", "money | nil"); an empty spelling
// leaves that slot unknown. A published signature is also enforced at
// runtime, so the checker and the boundary can never disagree.
type Signature struct {
	// Params declares the positional parameters in order. Optional
	// parameters must trail the required ones.
	Params []SignatureParam
	// Result is the invariant result type spelling; empty keeps the result
	// unknown to the checker.
	Result string
	// AcceptsBlock permits a literal or forwarded block argument. The block
	// is passed through to the builtin unvalidated.
	AcceptsBlock bool
}

// SignatureParam declares one positional parameter of a host callable.
type SignatureParam struct {
	// Name labels the parameter in diagnostics.
	Name string
	// Type is the annotation spelling validated at the boundary; empty
	// leaves the parameter unknown.
	Type string
	// Optional marks a parameter the caller may omit.
	Optional bool
}

// NewTypedBuiltin builds a host builtin Value that publishes sig to the
// checker and validates calls against it at runtime. Keyword arguments are
// rejected: signatures describe positional contracts only. The returned
// value can be registered as an engine builtin, passed as a call-option
// global, or exposed as a capability method.
func NewTypedBuiltin(name string, fn BuiltinFunc, sig Signature) (Value, error) {
	// The wrapper and the published metadata read sig for the builtin's
	// lifetime, so the caller-owned params slice is copied up front: a host
	// reusing or mutating its slice must not change an already-registered
	// contract.
	sig.Params = append([]SignatureParam(nil), sig.Params...)
	spec, paramTypes, err := signatureCallSpec(name, sig)
	if err != nil {
		return Value{}, err
	}
	wrapped := func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		normalized, err := validateSignatureCall(exec, name, sig, paramTypes, args, kwargs, block)
		if err != nil {
			return NewNil(), err
		}
		result, err := fn(exec, receiver, normalized, kwargs, block)
		if err != nil || spec.resultType == nil {
			return result, err
		}
		validated, err := signatureNormalize(exec, result, spec.resultType)
		if err != nil {
			if isHostControlSignal(err) || isNormalizationLimitError(err) {
				return NewNil(), err
			}
			return NewNil(), errors.New(formatReturnTypeMismatch(name, err))
		}
		return validated, nil
	}
	val := NewBuiltin(name, wrapped)
	builtin := valueBuiltin(val)
	builtin.checkSpec = spec
	builtin.SignatureParams = signatureRuntimeParams(sig, paramTypes)
	return val, nil
}

// signatureRuntimeParams projects the signature into the parameter shape the
// runtime argument evaluator consults, so a bare zero-arg callback passed to
// a function-typed parameter is preserved instead of auto-invoked, exactly
// like the same annotation on a script function.
func signatureRuntimeParams(sig Signature, paramTypes []*TypeExpr) []Param {
	params := make([]Param, len(sig.Params))
	for i, param := range sig.Params {
		params[i] = Param{Name: param.Name, Kind: ParamNormal, Type: paramTypes[i]}
	}
	return params
}

// signatureCallSpec lowers a public Signature into the checker's static call
// contract, parsing every type spelling with the annotation grammar.
func signatureCallSpec(name string, sig Signature) (*staticCallSpec, []*TypeExpr, error) {
	spec := &staticCallSpec{
		maxArgs:        len(sig.Params),
		rejectKeywords: true,
		rejectBlock:    !sig.AcceptsBlock,
		usesBlock:      sig.AcceptsBlock,
		fromSignature:  true,
	}
	paramTypes := make([]*TypeExpr, len(sig.Params))
	paramNames := make([]string, len(sig.Params))
	sawOptional := false
	for i, param := range sig.Params {
		paramNames[i] = param.Name
		if param.Optional {
			sawOptional = true
		} else {
			if sawOptional {
				return nil, nil, fmt.Errorf("signature for %s: optional parameters must trail required ones", name)
			}
			spec.minArgs = i + 1
		}
		if param.Type == "" {
			continue
		}
		ty, err := parser.ParseTypeExpr(param.Type)
		if err != nil {
			return nil, nil, fmt.Errorf("signature for %s parameter %s: %w", name, signatureParamLabel(param, i), err)
		}
		paramTypes[i] = ty
	}
	spec.paramTypes = paramTypes
	spec.paramNames = paramNames
	if sig.Result != "" {
		ty, err := parser.ParseTypeExpr(sig.Result)
		if err != nil {
			return nil, nil, fmt.Errorf("signature for %s result: %w", name, err)
		}
		spec.resultType = ty
	}
	return spec, paramTypes, nil
}

func signatureParamLabel(param SignatureParam, index int) string {
	if param.Name != "" {
		return param.Name
	}
	return fmt.Sprintf("%d", index+1)
}

// validateSignatureCall enforces the published contract at runtime with the
// same shape rules the checker reports statically, normalizing each typed
// argument exactly like an annotated script parameter (symbols coerce into
// enums, named types resolve in the execution's scope). It returns the
// normalized arguments the host function should receive.
func validateSignatureCall(exec *Execution, name string, sig Signature, paramTypes []*TypeExpr, args []Value, kwargs map[string]Value, block Value) ([]Value, error) {
	minArgs := 0
	for i, param := range sig.Params {
		if !param.Optional {
			minArgs = i + 1
		}
	}
	if len(args) < minArgs {
		return nil, fmt.Errorf("%s expects at least %d arguments, got %d", name, minArgs, len(args))
	}
	if len(args) > len(sig.Params) {
		return nil, fmt.Errorf("%s expects at most %d arguments, got %d", name, len(sig.Params), len(args))
	}
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("%s does not take keyword arguments", name)
	}
	if !sig.AcceptsBlock && valueBlock(block) != nil {
		return nil, fmt.Errorf("%s does not take a block", name)
	}
	normalized := args
	copied := false
	for i, arg := range args {
		ty := paramTypes[i]
		if ty == nil {
			continue
		}
		val, err := signatureNormalize(exec, arg, ty)
		if err != nil {
			if isHostControlSignal(err) || isNormalizationLimitError(err) {
				return nil, err
			}
			return nil, fmt.Errorf("%s %s", name, formatArgumentTypeMismatch(signatureParamLabel(sig.Params[i], i), err))
		}
		if !copied {
			normalized = append([]Value(nil), args...)
			copied = true
		}
		normalized[i] = val
	}
	return normalized, nil
}

// signatureNormalize validates a value against a signature type. Named class
// contracts additionally accept an instance whose class matches the resolved
// definition by name and owner: signature validation runs against the
// execution's root context, while instances are built from per-call clones,
// so the pointer identity the shared normalizer uses is too strict here.
func signatureNormalize(exec *Execution, val Value, ty *TypeExpr) (Value, error) {
	out, err := normalizeValueForType(val, ty, signatureTypeContext(exec))
	if err == nil {
		return out, nil
	}
	if _, ok := errors.AsType[*typeMismatchError](err); ok && signatureInstanceMatchesNamed(exec, val, ty) {
		return val, nil
	}
	return out, err
}

// signatureInstanceMatchesNamed reports whether val is an instance of the
// class the named type resolves to, comparing definitions the clone-safe way
// (same name, same owner script) and honoring module contracts by include.
func signatureInstanceMatchesNamed(exec *Execution, val Value, ty *TypeExpr) bool {
	if ty == nil || ty.Kind != TypeEnum || val.Kind() != KindInstance {
		return false
	}
	inst := valueInstance(val)
	if inst == nil || inst.Class == nil {
		return false
	}
	match, ok, err := lookupNamedTypeForType(ty, signatureTypeContext(exec))
	if err != nil || !ok || match.class == nil {
		return false
	}
	if match.class.IsModule {
		return classIncludesModule(inst.Class, match.class.Name)
	}
	if inst.Class.Name != match.class.Name {
		return false
	}
	return inst.Class.owner != nil && inst.Class.owner == match.class.owner
}

// signatureTypeContext resolves signature type spellings the way annotated
// script parameters do: named types resolve against the source currently
// executing — a required module's own enums and classes when the call comes
// from module code — with the call root as fallback.
func signatureTypeContext(exec *Execution) typeContext {
	if exec == nil {
		return typeContext{}
	}
	owner := exec.currentSourceScript()
	if exec.bindingOwner != nil {
		// A default parameter expression runs before the callee's module
		// context is pushed; the binding owner is the source whose scope the
		// default (and any typed builtin it calls) resolves in.
		owner = exec.bindingOwner
	}
	return typeContext{owner: owner, env: exec.root, fallback: exec.root, exec: exec}
}
