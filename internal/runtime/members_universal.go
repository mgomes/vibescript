package runtime

import "fmt"

// universalMemberNames lists the predicates exposed on every value via the
// universal fallback in resolveMember. They back the Ruby-style equality
// predicates that Object defines for all values: `eql?` for hash-key equality
// and `equal?` for object identity. Unlike the per-type member tables these are
// resolved centrally, after type-specific members and user-defined methods, so
// a class may still override them with its own definitions.
var universalMemberNames = []string{"eql?", "equal?"}

// isUniversalPredicate reports whether property names one of the equality
// predicates that every value answers through the universal fallback.
func isUniversalPredicate(property string) bool {
	switch property {
	case "eql?", "equal?":
		return true
	default:
		return false
	}
}

// universalMember resolves the equality predicates that apply uniformly across
// all value kinds. It is consulted only after a value's own type-specific
// members and any user-defined methods have failed to resolve property, so
// existing members (including a class's own eql?/equal?) always take precedence.
// The returned builtins carry the receiver's kind in their name so argument
// errors read naturally (for example "int.eql? expects 1 argument").
func universalMember(obj Value, property string) (Value, bool) {
	switch property {
	case "eql?":
		return bindEqualityPredicate("eql?", obj, Value.Eql), true
	case "equal?":
		return bindEqualityPredicate("equal?", obj, Value.Identical), true
	default:
		return NewNil(), false
	}
}

// bindEqualityPredicate builds a receiver-bound eql?/equal? predicate. These
// predicates require an operand, so they use non-auto builtins: bare access
// (probe = obj.eql?) yields the bound builtin for a later call rather than
// auto-invoking with no argument, matching Duration#eql? and Time#eql?.
//
// The receiver is read from the builtin's captured value rather than closed over
// directly, for two reasons. First, recording it as a captured value lets the
// memory estimator charge its payload while the bound builtin is reachable;
// without this a stored probe such as `probe = huge_hash.eql?` would retain the
// receiver in a Go closure the quota cannot see. Second, it makes the predicate
// rebindable: when Script.Call host-clones a returned graph holding both a
// receiver and a predicate bound to it, the clone walk rewrites the captured
// receiver to its clone and calls RebindReceiver, so a re-entering
// probe(clonedReceiver) compares the cloned receiver against itself and still
// reports identity rather than comparing against the stale pre-clone value.
func bindEqualityPredicate(property string, receiver Value, compare func(Value, Value) bool) Value {
	name := fmt.Sprintf("%s.%s", receiver.Kind(), property)
	val := NewCapturingBuiltin(name, func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := requireEqualityPredicateCall(name, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		return NewBool(compare(receiver, args[0])), nil
	}, receiver)
	valueBuiltin(val).RebindReceiver = func(clonedReceiver Value) Value {
		return bindEqualityPredicate(property, clonedReceiver, compare)
	}
	return val
}

// requireEqualityPredicateCall enforces the shared call shape for the eql? and
// equal? predicates: exactly one positional argument, no keyword arguments, and
// no block. The other operand may be any value, since these predicates report
// false rather than raising when the kinds differ.
func requireEqualityPredicateCall(name string, args []Value, kwargs map[string]Value, block Value) error {
	if len(kwargs) > 0 {
		return fmt.Errorf("%s does not accept keyword arguments", name)
	}
	if valueBlock(block) != nil {
		return fmt.Errorf("%s does not accept a block", name)
	}
	if len(args) != 1 {
		return fmt.Errorf("%s expects 1 argument, got %d", name, len(args))
	}
	return nil
}
