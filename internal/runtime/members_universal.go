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
	// These predicates require an operand, so they use non-auto builtins:
	// bare access (probe = obj.eql?) yields the bound builtin for a later call
	// rather than auto-invoking with no argument, matching Duration#eql? and
	// Time#eql?. The receiver is captured here rather than read from the call's
	// receiver argument so a stored builtin still compares against the original
	// value when later invoked as probe(other), where no receiver is bound.
	switch property {
	case "eql?":
		name := fmt.Sprintf("%s.eql?", obj.Kind())
		return NewBuiltin(name, func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := requireEqualityPredicateCall(name, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			return NewBool(obj.Eql(args[0])), nil
		}), true
	case "equal?":
		name := fmt.Sprintf("%s.equal?", obj.Kind())
		return NewBuiltin(name, func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if err := requireEqualityPredicateCall(name, args, kwargs, block); err != nil {
				return NewNil(), err
			}
			return NewBool(obj.Identical(args[0])), nil
		}), true
	default:
		return NewNil(), false
	}
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
