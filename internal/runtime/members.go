package runtime

import (
	"fmt"
	"maps"
	"slices"
)

func (exec *Execution) getMember(obj Value, property string, pos Position) (Value, error) {
	return exec.resolveMember(obj, property, pos, exec.isCurrentReceiver(obj))
}

// getPublicMember resolves a member as an external caller would, so private
// methods are rejected even when obj is the current receiver. It backs the
// `public_send`-style dispatch used by the reduce operation shorthand, where
// the documented contract is `accumulator.public_send(operation, item)`: an
// accumulator that happens to be self must not gain access to private methods.
func (exec *Execution) getPublicMember(obj Value, property string, pos Position) (Value, error) {
	return exec.resolveMember(obj, property, pos, false)
}

// universalMemberNames lists members exposed on every value kind, regardless
// of type. They are resolved by universalMember before the per-kind dispatch in
// resolveMember, so they take precedence over same-named hash or object keys
// just as Ruby's universal Object methods take precedence over data access.
var universalMemberNames = []string{"itself"}

// itselfMember is the shared builtin backing Ruby-style Object#itself. It closes
// over no receiver: it returns the receiver passed at call time, so identity is
// preserved without copying and existing value ownership and host-boundary
// isolation semantics stay intact. Because it carries no per-call state, a
// single instance is reused for every value.
var itselfMember = NewAutoBuiltin("object.itself", func(_ *Execution, receiver Value, args []Value, kwargs map[string]Value, _ Value) (Value, error) {
	if len(args) > 0 || len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("itself does not take arguments")
	}
	return receiver, nil
})

// universalMember resolves a member available on every value kind. It returns
// ok=false when property names no universal member, letting resolveMember fall
// through to per-kind dispatch.
func universalMember(property string) (Value, bool) {
	if property != "itself" {
		return NewNil(), false
	}
	return itselfMember, true
}

// resolveMember performs member resolution for getMember and getPublicMember.
// callerIsReceiver controls private-method visibility: only the current
// receiver may resolve private methods, so external/public dispatch passes
// false to keep privacy enforced regardless of which value is self.
func (exec *Execution) resolveMember(obj Value, property string, pos Position, callerIsReceiver bool) (Value, error) {
	if member, ok := universalMember(property); ok {
		return member, nil
	}
	switch obj.Kind() {
	case KindHash:
		member, err := hashMember(obj, property)
		if err == nil {
			return member, nil
		}
		if val, ok := obj.Hash()[property]; ok {
			return val, nil
		}
		return NewNil(), err
	case KindObject:
		if val, ok := obj.Hash()[property]; ok {
			return val, nil
		}
		member, err := hashMember(obj, property)
		if err != nil {
			return NewNil(), err
		}
		return member, nil
	case KindMoney:
		return moneyMember(obj.Money(), property)
	case KindDuration:
		return durationMember(obj.Duration(), property, pos)
	case KindTime:
		return timeMember(obj.Time(), property)
	case KindArray:
		return arrayMember(obj, property)
	case KindString:
		return stringMember(obj, property)
	case KindEnumValue:
		return exec.enumValueMember(obj, property, pos)
	case KindClass:
		return exec.classMember(obj, property, pos, callerIsReceiver)
	case KindInstance:
		return exec.instanceMember(obj, property, pos, callerIsReceiver)
	case KindInt:
		return exec.intMember(obj, property, pos)
	case KindFloat:
		return exec.floatMember(obj, property, pos)
	case KindRange:
		return exec.rangeMember(obj, property, pos)
	case KindFunction:
		return exec.functionMember(obj, property, pos)
	default:
		return NewNil(), exec.errorAt(pos, "unsupported member access on %s", obj.Kind())
	}
}

// functionMemberNames lists the members exposed on script function
// values. Keep it in sync with functionMember; it feeds "did you mean"
// suggestions and editor completion.
var functionMemberNames = []string{"call"}

// functionMember resolves member access on a script function value. Only
// `call` is supported: it returns a builtin that invokes the underlying
// function with the supplied args, kwargs, and block, mirroring direct
// `fn(...)` invocation (including its nil receiver) for Ruby-style
// `fn.call(...)` parity.
func (exec *Execution) functionMember(obj Value, property string, pos Position) (Value, error) {
	if property != "call" {
		return NewNil(), exec.errorAt(pos, "unknown member %s%s", property, didYouMean(property, functionMemberNames))
	}
	fn := valueFunction(obj)
	caller := NewAutoBuiltin("function.call", func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return exec.invokeCallable(obj, NewNil(), args, kwargs, block, pos)
	})
	callerBuiltin := valueBuiltin(caller)
	callerBuiltin.OptionsHashTarget = fn
	callerBuiltin.DirectCallAlias = true
	return caller, nil
}

func (exec *Execution) classMember(obj Value, property string, pos Position, callerIsReceiver bool) (Value, error) {
	cl := valueClass(obj)
	if property == "new" {
		constructor := NewAutoBuiltin(cl.Name+".new", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			inst := &Instance{Class: cl, Ivars: make(map[string]Value)}
			instVal := NewInstance(inst)
			if initFn, ok := cl.Methods["initialize"]; ok {
				if _, err := exec.callFunction(initFn, instVal, args, kwargs, block, pos); err != nil {
					return NewNil(), err
				}
			}
			return instVal, nil
		})
		if initFn, ok := cl.Methods["initialize"]; ok {
			valueBuiltin(constructor).OptionsHashTarget = initFn
		}
		return constructor, nil
	}
	if fn, ok := cl.ClassMethods[property]; ok {
		if fn.Private && !callerIsReceiver {
			return NewNil(), exec.errorAt(pos, "private method %s", property)
		}
		method := NewAutoBuiltin(cl.Name+"."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.callFunction(fn, obj, args, kwargs, block, pos)
		})
		valueBuiltin(method).OptionsHashTarget = fn
		return method, nil
	}
	if val, ok := cl.ClassVars[property]; ok {
		return val, nil
	}
	candidates := make([]string, 0, len(cl.ClassMethods)+len(cl.ClassVars)+1)
	candidates = append(candidates, "new")
	candidates = appendAccessibleMethodNames(candidates, cl.ClassMethods, callerIsReceiver)
	candidates = slices.AppendSeq(candidates, maps.Keys(cl.ClassVars))
	return NewNil(), exec.errorAt(pos, "unknown class member %s%s", property, didYouMean(property, candidates))
}

func (exec *Execution) instanceMember(obj Value, property string, pos Position, callerIsReceiver bool) (Value, error) {
	inst := valueInstance(obj)
	if property == "class" {
		return NewClass(inst.Class), nil
	}
	if fn, ok := inst.Class.Methods[property]; ok {
		if fn.Private && !callerIsReceiver {
			return NewNil(), exec.errorAt(pos, "private method %s", property)
		}
		method := NewAutoBuiltin(inst.Class.Name+"#"+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.callFunction(fn, obj, args, kwargs, block, pos)
		})
		valueBuiltin(method).OptionsHashTarget = fn
		return method, nil
	}
	if val, ok := inst.Ivars[property]; ok {
		return val, nil
	}
	candidates := make([]string, 0, len(inst.Class.Methods)+len(inst.Ivars)+1)
	candidates = append(candidates, "class")
	candidates = appendAccessibleMethodNames(candidates, inst.Class.Methods, callerIsReceiver)
	candidates = slices.AppendSeq(candidates, maps.Keys(inst.Ivars))
	return NewNil(), exec.errorAt(pos, "unknown member %s%s", property, didYouMean(property, candidates))
}

func (exec *Execution) getScopedMember(obj Value, property string, pos Position) (Value, error) {
	if obj.Kind() == KindObject {
		// Namespace objects such as Math expose constants and module
		// functions, so `Math::PI` resolves the same member that `Math.PI`
		// would, matching Ruby's `::` constant access on a module.
		if val, ok := obj.Hash()[property]; ok {
			return val, nil
		}
		candidates := slices.Collect(maps.Keys(obj.Hash()))
		return NewNil(), exec.errorAt(pos, "unknown member %s%s", property, didYouMean(property, candidates))
	}
	if obj.Kind() != KindEnum {
		return NewNil(), exec.errorAt(pos, "scoped member access is only supported on enums and namespaces")
	}
	enumDef := valueEnum(obj)
	if enumDef == nil {
		return NewNil(), exec.errorAt(pos, "unknown enum %s", property)
	}
	member, ok := enumDef.Members[property]
	if !ok {
		candidates := slices.Collect(maps.Keys(enumDef.Members))
		return NewNil(), exec.errorAt(pos, "unknown enum member %s::%s%s", enumDef.Name, property, didYouMean(property, candidates))
	}
	return NewEnumValue(member), nil
}

func (exec *Execution) enumValueMember(obj Value, property string, pos Position) (Value, error) {
	member := valueEnumValue(obj)
	if member == nil {
		return NewNil(), exec.errorAt(pos, "unknown enum member")
	}
	switch property {
	case "name":
		return NewString(member.Name), nil
	case "symbol":
		return NewSymbol(member.Symbol), nil
	case "enum":
		return NewEnum(member.Enum), nil
	default:
		return NewNil(), exec.errorAt(pos, "unknown enum member property %s%s", property, didYouMean(property, []string{"name", "symbol", "enum"}))
	}
}

// appendAccessibleMethodNames collects method names for did-you-mean
// candidates, omitting private methods unless the caller is the receiver
// itself — suggestions must not point at members the call site cannot
// invoke (or disclose their existence).
func appendAccessibleMethodNames(candidates []string, methods map[string]*ScriptFunction, callerIsReceiver bool) []string {
	for name, fn := range methods {
		if fn.Private && !callerIsReceiver {
			continue
		}
		candidates = append(candidates, name)
	}
	return candidates
}

// MemberCompletionNames returns the builtin member-method names per
// receiver type, for editor tooling such as LSP completion. Each list
// includes the universal members (such as itself) available on every value
// kind. The slices are copies; callers may sort or mutate them freely.
func MemberCompletionNames() map[string][]string {
	withUniversal := func(names []string) []string {
		return append(slices.Clone(names), universalMemberNames...)
	}
	return map[string][]string{
		"string":   withUniversal(stringMemberNames),
		"symbol":   withUniversal(symbolMemberNames),
		"array":    withUniversal(arrayMemberNames),
		"hash":     withUniversal(hashMemberNames),
		"int":      withUniversal(intMemberNames),
		"float":    withUniversal(floatMemberNames),
		"money":    withUniversal(moneyMemberNames),
		"duration": withUniversal(durationMemberNames),
		"time":     withUniversal(timeMemberNames),
		"range":    withUniversal(rangeMemberNames),
		"function": withUniversal(functionMemberNames),
	}
}
