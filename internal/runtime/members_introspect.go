package runtime

import "fmt"

// Universal object introspection predicates, available on every value kind the
// way Ruby's Object#respond_to?, #is_a?, #kind_of?, and #instance_of? are. They
// are resolved as a fallback in resolveMember so a script class may still
// override them with its own method of the same name.

const (
	respondToMemberName  = "respond_to?"
	isAMemberName        = "is_a?"
	kindOfMemberName     = "kind_of?"
	instanceOfMemberName = "instance_of?"
)

// isUniversalPredicate reports whether property names one of the universal
// introspection predicates.
func isUniversalPredicate(property string) bool {
	switch property {
	case respondToMemberName, isAMemberName, kindOfMemberName, instanceOfMemberName:
		return true
	default:
		return false
	}
}

// universalPredicate returns the auto-invoked builtin backing an introspection
// predicate for receiver. callerIsReceiver controls whether private methods are
// visible to respond_to?, matching the privacy stance of the dispatch that
// reached here.
func (exec *Execution) universalPredicate(property string, callerIsReceiver bool) Value {
	switch property {
	case respondToMemberName:
		return newRespondToBuiltin(callerIsReceiver)
	case isAMemberName, kindOfMemberName, instanceOfMemberName:
		return newClassPredicateBuiltin(property)
	default:
		// Unreachable: callers gate on isUniversalPredicate.
		return NewNil()
	}
}

// newRespondToBuiltin builds the respond_to?(name, include_all = false)
// predicate. name may be a symbol or string. include_all, when truthy, also
// reports private methods, matching Ruby's optional second argument.
func newRespondToBuiltin(callerIsReceiver bool) Value {
	name := respondToMemberName
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("%s does not take a block", name)
		}
		if len(args) < 1 || len(args) > 2 {
			return NewNil(), fmt.Errorf("%s expects 1 or 2 arguments", name)
		}
		method, ok := methodNameArg(args[0])
		if !ok {
			return NewNil(), fmt.Errorf("%s expects a symbol or string method name", name)
		}
		includePrivate := false
		if len(args) == 2 {
			if args[1].Kind() != KindBool {
				return NewNil(), fmt.Errorf("%s expects a boolean second argument", name)
			}
			includePrivate = args[1].Bool()
		}
		// Private methods are reported only when explicitly requested (Ruby's
		// include_all) or when the caller is the receiver itself and so could
		// already dispatch them.
		allowPrivate := includePrivate || callerIsReceiver
		return NewBool(exec.respondsTo(receiver, method, allowPrivate)), nil
	})
}

// newClassPredicateBuiltin builds an is_a?/kind_of?/instance_of? predicate, each
// distinguished by name only. All three currently test direct class identity:
// an instance belongs to exactly its own class. Vibescript has no inheritance,
// so is_a?/kind_of? (ancestry) and instance_of? (exact class) coincide; when a
// superclass chain is added, is_a?/kind_of? will additionally walk it.
func newClassPredicateBuiltin(name string) Value {
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		if valueBlock(block) != nil {
			return NewNil(), fmt.Errorf("%s does not take a block", name)
		}
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("%s expects exactly one argument", name)
		}
		if args[0].Kind() != KindClass {
			return NewNil(), fmt.Errorf("%s expects a class argument", name)
		}
		want := valueClass(args[0])
		if receiver.Kind() != KindInstance {
			return NewBool(false), nil
		}
		return NewBool(valueInstance(receiver).Class == want), nil
	})
}

// methodNameArg extracts a method name from a respond_to? argument. Ruby accepts
// both symbols and strings here and reports any other type as an error.
func methodNameArg(v Value) (string, bool) {
	switch v.Kind() {
	case KindSymbol, KindString:
		return v.String(), true
	default:
		return "", false
	}
}

// respondsTo reports whether receiver has a callable method named method, i.e.
// whether dispatching method on receiver (under the given privacy) would reach a
// callable rather than raise or return data. It mirrors resolveMember's
// per-kind decision so respond_to? never disagrees with actual dispatch. It
// consults each kind's method namespace only, never its data (hash keys, object
// entries, instance variables, or class variables), so storing a callable in a
// data slot does not make the receiver respond to that slot's name — except when
// the data slot itself holds a callable that real dispatch would invoke (an
// object's callable export, see the KindObject branch).
//
// The universal members (itself, nil?, eql?, equal?, the block helpers
// tap/yield_self, and the introspection predicates) respond on every value
// because resolveMember answers them as a fallback. A user-defined override of
// one of these names is a real method: it responds only under the same privacy
// as any other method, so a private override is not reported to an external
// caller (matching the dispatch that would raise). The data-eligible block
// helpers tap/yield_self are the exception to "always responds": a stored data
// slot of that name shadows them, so they respond only when not shadowed by
// non-callable data (see universalMemberResponds).
func (exec *Execution) respondsTo(receiver Value, method string, allowPrivate bool) bool {
	switch receiver.Kind() {
	case KindHash:
		// A hash builtin name always wins over a stored entry of the same name
		// (hashMember resolves the builtin before any data key). For universal
		// members, the data-safe helpers always win over a stored entry, so they
		// always respond; the data-eligible block helpers (tap/yield_self) are
		// shadowed by a stored entry, so they respond only when no entry shadows
		// them or the shadowing entry is itself callable.
		if isUniversalMember(method) {
			return universalMemberResponds(method, receiver.Hash())
		}
		if _, ok := hashBuiltinMembers.lookup(method, hashMemberBuiltin); ok {
			return true
		}
		entry, ok := receiver.Hash()[method]
		return ok && isInvocable(entry)
	case KindObject:
		return objectRespondsTo(receiver, method)
	case KindInstance:
		return instanceRespondsTo(valueInstance(receiver), method, allowPrivate)
	case KindClass:
		return classRespondsTo(valueClass(receiver), method, allowPrivate)
	default:
		// Every non-container kind exposes only methods and has no user-defined
		// overrides, so a universal member always responds and any other name
		// responds when it resolves to a callable member.
		if isUniversalMember(method) {
			return true
		}
		_, err := exec.resolveTypedMember(receiver, method, Position{}, allowPrivate)
		return err == nil
	}
}

// universalMemberResponds reports whether a universal member named method
// responds for a receiver whose data slots are data (a hash map, an object's
// field map, an instance's ivars, or a class's class vars). It mirrors how
// resolveMember resolves the helper against a stored entry of the same name:
//
//   - A data-safe helper (itself/nil?/eql?/equal? or an introspection predicate)
//     always wins over a stored data entry, so it always responds.
//   - A data-eligible block helper (tap/yield_self) is shadowed by a stored entry:
//     dispatch returns that entry, so the receiver responds only when no entry
//     shadows the helper or the shadowing entry is itself callable.
//
// method must be a universal member; callers gate on isUniversalMember.
func universalMemberResponds(method string, data map[string]Value) bool {
	if isUniversalDataSafe(method) {
		return true
	}
	entry, ok := data[method]
	if !ok {
		return true
	}
	return isInvocable(entry)
}

// objectRespondsTo reports whether an object value responds to method, matching
// the KindObject branch of resolveTypedMember. An object's map backs both
// module/capability namespaces (callable exports) and ordinary data objects
// (data fields), and a stored entry shadows the hash builtin of the same name:
// dispatch returns the entry, so the receiver responds only when that entry is a
// callable export. A non-callable data field keyed with a method name therefore
// does NOT respond even when a hash builtin of that name exists, because real
// dispatch would return (and try to call) the stored data, not the builtin.
//
// A universal member is shadowed by a non-callable data field only when it is a
// data-eligible block helper (tap/yield_self); a data-safe helper always wins,
// and a callable export of any universal name is itself callable.
func objectRespondsTo(receiver Value, method string) bool {
	if isUniversalMember(method) {
		return universalMemberResponds(method, receiver.Hash())
	}
	if entry, ok := receiver.Hash()[method]; ok {
		return isInvocable(entry)
	}
	_, ok := hashBuiltinMembers.lookup(method, hashMemberBuiltin)
	return ok
}

// isInvocable reports whether v can be called. invokeCallable accepts only
// these kinds, so they are exactly the values respond_to? counts as methods
// when found in a hash or namespace data slot.
func isInvocable(v Value) bool {
	switch v.Kind() {
	case KindFunction, KindBuiltin:
		return true
	default:
		return false
	}
}

// instanceRespondsTo reports whether an instance has a method named method.
// `class` is the one always-available pseudo-method. Instance variables are
// attributes, not methods, so they never respond even when readable as members
// (mirroring Ruby, where respond_to? reports methods only). A user-defined method
// (including an override of a universal member such as respond_to?) responds only
// per privacy: a private method responds when allowPrivate is set, matching the
// dispatch that would otherwise raise. A universal member with no override
// responds because resolveMember answers it via the universal fallback, except a
// data-eligible block helper (tap/yield_self) shadowed by a non-callable ivar:
// dispatch returns that ivar, so the receiver does not respond.
func instanceRespondsTo(inst *Instance, method string, allowPrivate bool) bool {
	if method == "class" {
		return true
	}
	if fn, ok := inst.Class.Methods[method]; ok {
		return allowPrivate || !fn.Private
	}
	if isUniversalMember(method) {
		return universalMemberResponds(method, inst.Ivars)
	}
	return false
}

// classRespondsTo reports whether a class value has a member named method.
// `new` is the one always-available pseudo-method. Class variables are
// attributes, not methods, so they never respond. A user-defined class method
// (including an override of a universal member) responds only per privacy: a
// private class method responds when allowPrivate is set. A universal member with
// no override responds because resolveMember answers it via the universal
// fallback, except a data-eligible block helper (tap/yield_self) shadowed by a
// non-callable class var: dispatch returns that class var, so the class does not
// respond.
func classRespondsTo(cl *ClassDef, method string, allowPrivate bool) bool {
	if method == "new" {
		return true
	}
	if fn, ok := cl.ClassMethods[method]; ok {
		return allowPrivate || !fn.Private
	}
	if isUniversalMember(method) {
		return universalMemberResponds(method, cl.ClassVars)
	}
	return false
}
