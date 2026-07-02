package runtime

import (
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

// resolveMember performs member resolution for getMember and getPublicMember.
// callerIsReceiver controls private-method visibility: only the current
// receiver may resolve private methods, so external/public dispatch passes
// false to keep privacy enforced regardless of which value is self.
//
// The universal Object-level helpers — itself, nil?, the equality predicates
// eql?/equal?, the block helpers tap/yield_self, and the introspection
// predicates respond_to?/is_a?/kind_of?/instance_of? — are resolved as a
// fallback after the type-specific dispatch reports the member as unknown, so a
// value's own members and any user-defined methods of the same name always take
// precedence, matching Ruby's overridable Object-level helpers. A private
// override is not "unknown": resolveTypedMember reports it with a private-member
// error, which suppresses the fallback so the privacy block still raises rather
// than silently resolving the universal builtin. Resolving as a fallback in
// resolveMember (rather than per-kind dispatch) keeps the no-paren form
// (probe = obj.itself) and the parenthesized form (obj.itself()) in agreement:
// a user-defined itself wins in both, since both routes share this resolution.
//
// Hash receivers are the exception for the data-safe helpers itself/nil?/eql?/
// equal? and the introspection predicates respond_to?/is_a?/kind_of?/
// instance_of?: a stored hash entry keyed one of them is data rather than a
// method, so the typed dispatch can never own that member and the universal
// helper always wins. Resolving it before typed dispatch keeps identity O(1): it
// skips hashMember's miss path, which would otherwise materialize did-you-mean
// candidates from every stored key only for resolveMember to discard that error
// in favor of the universal builtin. The block helpers tap/yield_self are not in
// this exception: a hash entry keyed tap/yield_self is ordinary data the typed
// dispatch returns, so they fall back only on a miss — but that miss is still
// reported cheaply, not routed through hashMember's candidate-building path (see
// resolveTypedMember).
//
// Object receivers are NOT in the data-safe exception, but they are not uniform
// either. KindObject backs two distinct shapes: module/capability namespaces,
// whose map holds callable exports (a required module's `def eql?` is collected
// into NewObject(exports)), and ordinary data objects returned by hosts/
// capabilities, whose map holds data fields. A stored eql?/equal? must shadow the
// universal helper only when it is a callable export (so a module's `def eql?`
// overrides like a class method); a plain data field keyed eql?/equal? is data,
// exactly like a hash entry, and must not shadow identity. Object dispatch
// therefore consults the typed member first but reports the helper as a miss
// when the stored entry is non-callable data, so the universal fallback answers
// it.
func (exec *Execution) resolveMember(obj Value, property string, pos Position, callerIsReceiver bool) (Value, error) {
	if isUniversalDataSafe(property) && universalMemberAlwaysWins(obj.Kind()) {
		if helper, ok := exec.universalMember(obj, property, callerIsReceiver); ok {
			return helper, nil
		}
	}
	member, err := exec.resolveTypedMember(obj, property, pos, callerIsReceiver)
	if err != nil && !isPrivateMemberError(err) && isUniversalMember(property) {
		if helper, ok := exec.universalMember(obj, property, callerIsReceiver); ok {
			return helper, nil
		}
	}
	return member, err
}

// universalMemberAlwaysWins reports whether a receiver kind has no typed member
// or user-defined method that could shadow the data-safe universal helpers
// (itself/nil?/eql?/equal? and the introspection predicates respond_to?/is_a?/
// kind_of?/instance_of?), so they may be resolved before typed dispatch. Only
// hash receivers qualify: hashMember exposes no such builtin and a stored entry
// of that name is data rather than a method, so the universal helper is the sole
// resolution and resolving it first avoids hashMember's expensive miss path. The
// caller gates this on isUniversalDataSafe; the data-eligible block helpers
// tap/yield_self never take this shortcut because a stored hash entry of that
// name is data the typed dispatch must return.
//
// Object receivers do not qualify even though they also store entries in a map:
// an object's entries may be callable namespace exports (a module's exported `def
// eql?` lands there), so a stored callable itself/eql?/equal? is a real member
// that must shadow the universal helper. Resolving the universal helper first
// would make that export unreachable, so object dispatch runs first and
// resolveTypedMember decides per entry whether the stored value is a callable
// export or non-callable data.
func universalMemberAlwaysWins(kind ValueKind) bool {
	return kind == KindHash
}

// resolveTypedMember dispatches member resolution to the handler for obj's kind,
// before the universal Object-level fallback in resolveMember is considered.
func (exec *Execution) resolveTypedMember(obj Value, property string, pos Position, callerIsReceiver bool) (Value, error) {
	switch obj.Kind() {
	case KindHash:
		// Universal Object-level helpers are answered by resolveMember's fallback,
		// so a miss for one must be reported cheaply rather than routed through
		// hashMember, whose miss path materializes did-you-mean candidates from
		// every stored key -- that O(n) work would scale with the hash size only to
		// be discarded in favor of the universal builtin.
		if isUniversalMember(property) {
			// The block helpers tap/yield_self are data-eligible: a stored hash entry
			// keyed tap/yield_self is ordinary data the typed dispatch owns, so it
			// shadows the helper. The data-safe helpers itself/eql?/equal? are never
			// stored data (universalMemberAlwaysWins resolves them before dispatch),
			// so a same-named entry stays readable only as data via index access
			// (h["eql?"]) and never reaches this branch in the always-wins path.
			if !isUniversalDataSafe(property) {
				if val, ok := hashMemberData(obj, property); ok {
					return val, nil
				}
			}
			return NewNil(), exec.errorAt(pos, "unknown member %s", property)
		}
		member, err := hashMember(obj, property)
		if err == nil {
			return member, nil
		}
		if val, ok := hashMemberData(obj, property); ok {
			return val, nil
		}
		return NewNil(), err
	case KindObject:
		// An object's map backs both module/capability namespaces (callable
		// exports) and ordinary data objects (data fields). A stored universal-member
		// entry shadows the universal helper only when it is a callable export (a
		// module's exported `def eql?` or `def tap`); the treatment of a non-callable
		// data field then depends on whether the helper is data-safe.
		if val, ok := obj.Hash()[property]; ok {
			if !isUniversalMember(property) || isCallableMember(val) {
				return val, nil
			}
			// A non-callable data field keyed by a data-eligible helper (tap/
			// yield_self) is ordinary data the typed dispatch returns, exactly like a
			// hash entry of that name, so it shadows the block helper.
			if !isUniversalDataSafe(property) {
				return val, nil
			}
			// A non-callable data field keyed by a data-safe helper (itself/nil?/eql?/
			// equal? or an introspection predicate) is data: leave it to
			// resolveMember's universal fallback so obj.equal?(obj) stays true and a
			// `{"respond_to?": 1}` field does not suppress respond_to?. The field
			// stays readable as data through index access (obj["eql?"]).
			return NewNil(), exec.errorAt(pos, "unknown member %s", property)
		}
		// A universal member that is not a stored member is answered by
		// resolveMember's fallback. Report the miss with a cheap fixed error rather
		// than routing through hashMember, whose miss path materializes did-you-mean
		// candidates from every export -- that work would scale with the object size
		// only to be discarded in favor of the universal builtin.
		if isUniversalMember(property) {
			return NewNil(), exec.errorAt(pos, "unknown member %s", property)
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
	case KindBlock:
		return exec.blockMember(obj, property, pos)
	case KindSymbol:
		return exec.symbolMember(obj, property, pos)
	case KindNil:
		return exec.nilMember(obj, property, pos)
	case KindBool:
		return exec.boolMember(obj, property, pos)
	default:
		return NewNil(), exec.errorAt(pos, "unsupported member access on %s", obj.Kind())
	}
}

func hashMemberData(obj Value, property string) (Value, bool) {
	if val, ok, err := hashGet(obj, NewSymbol(property)); err == nil && ok {
		return val, true
	}
	if val, ok, err := hashGet(obj, NewString(property)); err == nil && ok {
		return val, true
	}
	return NewNil(), false
}

func hashMemberAssignmentKey(obj Value, property string) Value {
	if _, ok, err := hashGet(obj, NewSymbol(property)); err == nil && ok {
		return NewSymbol(property)
	}
	if _, ok, err := hashGet(obj, NewString(property)); err == nil && ok {
		return NewString(property)
	}
	return NewSymbol(property)
}

// functionMemberNames lists the members exposed on script function
// values. Keep it in sync with functionMember; it feeds "did you mean"
// suggestions and editor completion.
var functionMemberNames = []string{"call"}

var blockMemberNames = []string{"call"}

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
	caller := NewCapturingBuiltin("function.call", func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return exec.invokeCallable(obj, NewNil(), args, kwargs, block, pos)
	}, obj)
	callerBuiltin := valueBuiltin(caller)
	callerBuiltin.AutoInvoke = true
	callerBuiltin.OptionsHashTarget = fn
	callerBuiltin.DirectCallAlias = true
	return caller, nil
}

func (exec *Execution) blockMember(obj Value, property string, pos Position) (Value, error) {
	if property != "call" {
		return NewNil(), exec.errorAt(pos, "unknown member %s%s", property, didYouMean(property, blockMemberNames))
	}
	caller := NewCapturingBuiltin("block.call", func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return exec.invokeCallable(obj, NewNil(), args, kwargs, block, pos)
	}, obj)
	callerBuiltin := valueBuiltin(caller)
	callerBuiltin.AutoInvoke = true
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
				if _, err := exec.callFunctionIgnoringReturn(initFn, instVal, args, kwargs, block, pos); err != nil {
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
			return NewNil(), privateMemberAccess(exec.errorAt(pos, "private method %s", property))
		}
		method := NewAutoBuiltin(cl.Name+"."+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.callFunction(fn, obj, args, kwargs, block, pos)
		})
		valueBuiltin(method).OptionsHashTarget = fn
		return method, nil
	}
	// A stored class var keyed by a data-safe helper (itself/nil?/eql?/equal? or an
	// introspection predicate respond_to?/is_a?/kind_of?/instance_of?) is data,
	// not a member, but those helpers are never reachable as data via dispatch, so
	// the class var must not preempt the universal helper (resolveMember supplies
	// it via the unknown-member fallback). A stored class var keyed by a
	// data-eligible block helper ("tap"/"yield_self") is ordinary data the
	// dispatch returns, so it shadows the helper. A user-defined class method of
	// any of these names is resolved above and still overrides the universal
	// helper.
	if !isUniversalDataSafe(property) {
		if val, ok := cl.ClassVars[property]; ok {
			return val, nil
		}
	}
	candidates := make([]string, 0, len(cl.ClassMethods)+len(cl.ClassVars)+1+len(universalMemberNames))
	candidates = append(candidates, "new")
	candidates = appendAccessibleMethodNames(candidates, cl.ClassMethods, callerIsReceiver)
	candidates = slices.AppendSeq(candidates, maps.Keys(cl.ClassVars))
	candidates = append(candidates, universalMemberNames...)
	return NewNil(), exec.errorAt(pos, "unknown class member %s%s", property, didYouMean(property, candidates))
}

func (exec *Execution) instanceMember(obj Value, property string, pos Position, callerIsReceiver bool) (Value, error) {
	inst := valueInstance(obj)
	if property == "class" {
		return NewClass(inst.Class), nil
	}
	if fn, ok := inst.Class.Methods[property]; ok {
		if fn.Private && !callerIsReceiver {
			return NewNil(), privateMemberAccess(exec.errorAt(pos, "private method %s", property))
		}
		method := NewAutoBuiltin(inst.Class.Name+"#"+property, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return exec.callFunction(fn, obj, args, kwargs, block, pos)
		})
		valueBuiltin(method).OptionsHashTarget = fn
		return method, nil
	}
	// A stored ivar keyed by a data-safe helper (itself/nil?/eql?/equal? or an
	// introspection predicate respond_to?/is_a?/kind_of?/instance_of?) is data,
	// not a member, but those helpers are never reachable as data via dispatch, so
	// the ivar must not preempt the universal helper (resolveMember supplies it via
	// the unknown-member fallback). A stored ivar keyed by a data-eligible block
	// helper ("tap"/"yield_self") is ordinary data the dispatch returns, exactly
	// like a hash entry of that name, so it shadows the helper. A user-defined
	// instance method of any of these names is resolved above and still overrides
	// the universal helper.
	if !isUniversalDataSafe(property) {
		if val, ok := inst.Ivars[property]; ok {
			return val, nil
		}
	}
	candidates := make([]string, 0, len(inst.Class.Methods)+len(inst.Ivars)+1+len(universalMemberNames))
	candidates = append(candidates, "class")
	candidates = appendAccessibleMethodNames(candidates, inst.Class.Methods, callerIsReceiver)
	candidates = slices.AppendSeq(candidates, maps.Keys(inst.Ivars))
	candidates = append(candidates, universalMemberNames...)
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
// receiver type, for editor tooling such as LSP completion. The slices
// are copies; callers may sort or mutate them freely. Each type's list
// includes the universal Object-level helpers (itself, nil?, eql?, equal?, tap,
// yield_self) and the introspection predicates (respond_to?, is_a?, kind_of?,
// instance_of?), which resolve on every value through resolveMember's fallback
// even though they live outside the per-kind dispatch switches.
func MemberCompletionNames() map[string][]string {
	return map[string][]string{
		"string":   withUniversalMembers(stringMemberNames),
		"symbol":   withUniversalMembers(symbolMemberNames),
		"array":    withUniversalMembers(arrayMemberNames),
		"hash":     withUniversalMembers(hashMemberNames),
		"int":      withUniversalMembers(intMemberNames),
		"float":    withUniversalMembers(floatMemberNames),
		"money":    withUniversalMembers(moneyMemberNames),
		"duration": withUniversalMembers(durationMemberNames),
		"time":     withUniversalMembers(timeMemberNames),
		"range":    withUniversalMembers(rangeMemberNames),
		"function": withUniversalMembers(functionMemberNames),
		"block":    withUniversalMembers(blockMemberNames),
		"nil":      withUniversalMembers(nilMemberNames),
		"bool":     withUniversalMembers(boolMemberNames),
	}
}
