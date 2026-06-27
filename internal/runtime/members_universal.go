package runtime

import (
	"fmt"
	"slices"
)

// universalMemberNames lists the Object-level helpers exposed on every value via
// the universal fallback in resolveMember. They back the Ruby-style methods that
// Object defines for all values:
//
//   - eql?/equal? — the equality predicates: `eql?` reports hash-key equality and
//     `equal?` reports object identity.
//   - tap/yield_self — the block helpers: `tap` yields the receiver to its block
//     and returns the receiver (threading side effects through a pipeline without
//     changing the value), while `yield_self` yields the receiver and returns the
//     block's result (rewriting a value inline).
//
// Unlike the per-type member tables these are resolved centrally, after
// type-specific members and user-defined methods, so a value's own members (and
// any class override) always take precedence, matching Ruby's overridable
// Object-level helpers. Editor completion surfaces them on every receiver via
// withUniversalMembers.
var universalMemberNames = []string{"eql?", "equal?", "tap", "yield_self"}

// isUniversalMemberName reports whether property names one of the Object-level
// helpers that every value answers through the universal fallback.
func isUniversalMemberName(property string) bool {
	switch property {
	case "eql?", "equal?", "tap", "yield_self":
		return true
	default:
		return false
	}
}

// isUniversalPredicate reports whether property names one of the equality
// predicates. The predicates are never stored data on a hash or object, so they
// follow a different resolution order than the block helpers (see
// universalPredicateAlwaysWins).
func isUniversalPredicate(property string) bool {
	switch property {
	case "eql?", "equal?":
		return true
	default:
		return false
	}
}

// isCallableMember reports whether a value stored under a member name is a
// callable method export rather than plain data. Only functions and builtins
// are invocable as methods, so only they may shadow a universal predicate; a
// stored function/builtin keyed eql?/equal? is a module export or capability
// method that overrides the predicate, while any other stored value is data
// and must let the universal predicate answer.
func isCallableMember(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin:
		return true
	default:
		return false
	}
}

// universalMember resolves an Object-level helper that applies uniformly across
// all value kinds. It is consulted only after a value's own type-specific
// members and any user-defined methods have failed to resolve property, so
// existing members (including a class's own override) always take precedence.
//
// The equality predicates bind to the receiver: their builtins carry the
// receiver's kind in their name so argument errors read naturally (for example
// "int.eql? expects 1 argument"). The block helpers take the receiver at call
// time, so they return a kind-agnostic auto-builtin.
func universalMember(obj Value, property string) (Value, bool) {
	switch property {
	case "eql?":
		return bindEqualityPredicate("eql?", obj, Value.Eql), true
	case "equal?":
		return bindEqualityPredicate("equal?", obj, Value.Identical), true
	case "tap":
		return newUniversalBlockBuiltin("tap", true), true
	case "yield_self":
		return newUniversalBlockBuiltin("yield_self", false), true
	default:
		return NewNil(), false
	}
}

// newUniversalBlockBuiltin returns the auto-invoked builtin for a universal
// block helper. When returnReceiver is true the helper returns its receiver
// (Object#tap); otherwise it returns the block's result (Object#yield_self).
// Both require a block, take no positional or keyword arguments, and pass the
// receiver as the block's single argument, matching Ruby's Object#tap and
// Object#yield_self.
func newUniversalBlockBuiltin(name string, returnReceiver bool) Value {
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) > 0 {
			return NewNil(), fmt.Errorf("%s does not take arguments", name)
		}
		if len(kwargs) > 0 {
			return NewNil(), fmt.Errorf("%s does not take keyword arguments", name)
		}
		if valueBlock(block) == nil {
			return NewNil(), fmt.Errorf("%s requires a block", name)
		}
		runner, err := newBlockCallRunner(exec, block, name)
		if err != nil {
			return NewNil(), err
		}
		result, err := runner.call([]Value{receiver})
		if err != nil {
			return NewNil(), err
		}
		if returnReceiver {
			return receiver, nil
		}
		return result, nil
	})
}

// boundReceiver holds the receiver of a bound eql?/equal? predicate in a mutable
// cell. The cell lets the clone walks register a predicate's clone before
// resolving its receiver: they build an empty clone (with a fresh cell), cache
// it, recurse into the receiver, then install the cloned receiver into the cell.
// A receiver graph that reaches the predicate bound to it (for example an array
// `a` storing `p = a.eql?`, returned as `[p, a]`) therefore dedups to one clone
// rather than minting a second during the recursion that the outer call then
// overwrites — which would make aliases that shared a builtin before the boundary
// report not-`equal?` after it.
type boundReceiver struct {
	value Value
}

// bindEqualityPredicate builds a receiver-bound eql?/equal? predicate. These
// predicates require an operand, so they use non-auto builtins: bare access
// (probe = obj.eql?) yields the bound builtin for a later call rather than
// auto-invoking with no argument, matching Duration#eql? and Time#eql?.
//
// The receiver is read from a mutable cell that is also mirrored into the
// builtin's captured value, for three reasons. First, recording it as a captured
// value lets the memory estimator charge its payload while the bound builtin is
// reachable; without this a stored probe such as `probe = huge_hash.eql?` would
// retain the receiver in a Go closure the quota cannot see. Second, it makes the
// predicate rebindable: when Script.Call host-clones (or re-roots) a returned (or
// inbound) graph holding both a receiver and a predicate bound to it, the clone
// walk rewrites the cell to the receiver's clone via the builtin's BoundReceiver
// hook, so a re-entering probe(clonedReceiver) compares the cloned receiver
// against itself and still reports identity rather than against the stale value.
// Third, the cell is mutable so the clone can be registered before its receiver
// is resolved, keeping recursive receiver graphs deduplicated to one clone.
func bindEqualityPredicate(property string, receiver Value, compare func(Value, Value) bool) Value {
	cell := &boundReceiver{value: receiver}
	return newBoundEqualityPredicate(property, cell, compare)
}

// newBoundEqualityPredicate builds the bound predicate builtin around an existing
// receiver cell. The builtin's BoundReceiver hook exposes a two-phase clone that
// the host clone and inbound rebind walks use to deduplicate recursive aliases.
func newBoundEqualityPredicate(property string, cell *boundReceiver, compare func(Value, Value) bool) Value {
	name := fmt.Sprintf("%s.%s", cell.value.Kind(), property)
	val := NewCapturingBuiltin(name, func(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := requireEqualityPredicateCall(name, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		return NewBool(compare(cell.value, args[0])), nil
	}, cell.value)
	builtin := valueBuiltin(val)
	builtin.BoundReceiver = &boundReceiverClone{
		// reserve builds an empty clone (fresh cell, source receiver mirrored so
		// the builtin's state stays valid before the receiver resolves) plus a
		// setter that installs the cloned receiver into both the cell and the
		// captured slot. The caller registers the clone in its clone cache before
		// recursing into the receiver, so a receiver that reaches this predicate
		// dedups against the registered clone instead of minting a second.
		reserve: func() (Value, *boundReceiver) {
			clonedCell := &boundReceiver{value: cell.value}
			return newBoundEqualityPredicate(property, clonedCell, compare), clonedCell
		},
		receiver: cell,
	}
	return val
}

// boundReceiverClone supports the two-phase clone of a bound equality predicate.
// reserve builds an empty clone the caller registers before recursing into the
// receiver; the returned cell receives the cloned receiver once it is resolved.
// receiver is the source predicate's cell, read by the clone walks to find the
// value to clone.
type boundReceiverClone struct {
	reserve  func() (Value, *boundReceiver)
	receiver *boundReceiver
}

// setBoundReceiver installs the resolved (cloned or rebound) receiver into a
// clone's cell and mirrors it into the builtin's captured slot so the memory
// estimator continues to charge the live receiver's payload.
func setBoundReceiver(builtin *Builtin, cell *boundReceiver, receiver Value) {
	cell.value = receiver
	if len(builtin.CapturedValues) > 0 {
		builtin.CapturedValues[0] = receiver
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

// withUniversalMembers returns a fresh slice holding names followed by the
// universal Object-level helper names, skipping any a type already lists itself
// (Duration and Time define their own eql?) so the result has no duplicates.
func withUniversalMembers(names []string) []string {
	out := slices.Clone(names)
	for _, name := range universalMemberNames {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}
