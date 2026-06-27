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

// respondsTo reports whether receiver has a callable method named method. It
// consults each kind's method namespace only, never its data (hash keys, object
// entries, instance variables, or class variables), so storing a callable in a
// data slot does not make the receiver respond to that slot's name. The
// universal members (itself, eql?, equal?, and the introspection predicates)
// always respond, since resolveMember answers them for every value.
func (exec *Execution) respondsTo(receiver Value, method string, allowPrivate bool) bool {
	if isUniversalMember(method) {
		return true
	}
	switch receiver.Kind() {
	case KindHash, KindObject:
		// A name responds when it is a hash builtin method or a data entry
		// holding a callable. Plain data entries (constants, scalars, nested
		// records) are attributes, not methods, so they do not respond — this
		// keeps namespace functions such as Math.sqrt reporting true while
		// constants such as Math::PI report false.
		if _, ok := hashBuiltinMembers.lookup(method, hashMemberBuiltin); ok {
			return true
		}
		entry, ok := receiver.Hash()[method]
		return ok && isInvocable(entry)
	case KindInstance:
		return instanceRespondsTo(valueInstance(receiver), method, allowPrivate)
	case KindClass:
		return classRespondsTo(valueClass(receiver), method, allowPrivate)
	default:
		// Every non-container kind exposes only methods, so a successful
		// resolution means the name is a callable member.
		_, err := exec.resolveTypedMember(receiver, method, Position{}, allowPrivate)
		return err == nil
	}
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
// (mirroring Ruby, where respond_to? reports methods only). Private methods
// respond only when allowPrivate is set.
func instanceRespondsTo(inst *Instance, method string, allowPrivate bool) bool {
	if method == "class" {
		return true
	}
	fn, ok := inst.Class.Methods[method]
	if !ok {
		return false
	}
	return allowPrivate || !fn.Private
}

// classRespondsTo reports whether a class value has a member named method.
// `new` is the one always-available pseudo-method. Class variables are
// attributes, not methods, so they never respond. Private class methods respond
// only when allowPrivate is set.
func classRespondsTo(cl *ClassDef, method string, allowPrivate bool) bool {
	if method == "new" {
		return true
	}
	fn, ok := cl.ClassMethods[method]
	if !ok {
		return false
	}
	return allowPrivate || !fn.Private
}
