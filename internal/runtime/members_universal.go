package runtime

import "fmt"

// universalMemberNames lists the members exposed on every value via the
// universal fallback in resolveMember. They back the Ruby-style members that
// Object defines for all values: `itself` returns the receiver, `eql?` is
// hash-key equality, `equal?` is object identity, and the introspection
// predicates `respond_to?`, `is_a?`, `kind_of?`, and `instance_of?` report a
// value's methods and class. Unlike the per-type member tables these are
// resolved centrally, after type-specific members and user-defined methods, so
// a class may still override them with its own definitions, and a stored data
// slot (hash key, ivar, class var, object data field) keyed with one of these
// names never shadows them. It feeds editor completion (MemberCompletionNames)
// and "did you mean" suggestions.
var universalMemberNames = []string{
	"itself",
	"eql?",
	"equal?",
	respondToMemberName,
	isAMemberName,
	kindOfMemberName,
	instanceOfMemberName,
}

// isUniversalMember reports whether property names one of the members that every
// value answers through the universal fallback.
func isUniversalMember(property string) bool {
	switch property {
	case "itself", "eql?", "equal?":
		return true
	default:
		return isUniversalPredicate(property)
	}
}

// isCallableMember reports whether a value stored under a member name is a
// callable method export rather than plain data. Only functions and builtins
// are invocable as methods, so only they may shadow a universal member; a
// stored function/builtin keyed itself/eql?/equal? is a module export or
// capability method that overrides the member, while any other stored value is
// data and must let the universal member answer.
func isCallableMember(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindBuiltin:
		return true
	default:
		return false
	}
}

// universalMember resolves the members that apply uniformly across all value
// kinds. It is consulted only after a value's own type-specific members and any
// user-defined methods have failed to resolve property, so existing members
// (including a class's own itself/eql?/equal?) always take precedence. The
// returned builtins carry the receiver's kind in their name so argument errors
// read naturally (for example "int.eql? expects 1 argument").
//
// callerIsReceiver controls whether the respond_to? predicate may report private
// methods: only the receiver itself can already dispatch them, so a public
// dispatch reaching here passes false to keep them hidden. The value-only
// members (itself, eql?, equal?) ignore it and delegate to universalValueMember.
func (exec *Execution) universalMember(obj Value, property string, callerIsReceiver bool) (Value, bool) {
	switch property {
	case respondToMemberName, isAMemberName, kindOfMemberName, instanceOfMemberName:
		return exec.universalPredicate(property, callerIsReceiver), true
	default:
		return universalValueMember(obj, property)
	}
}

// universalValueMember resolves the universal members whose result depends only
// on the receiver value: itself returns the receiver, eql? is hash-key equality,
// and equal? is object identity. They need neither an Execution nor the caller's
// privacy stance, so this is a pure function the introspection predicates'
// exec-aware sibling delegates to.
func universalValueMember(obj Value, property string) (Value, bool) {
	switch property {
	case "itself":
		return bindItself(obj), true
	case "eql?":
		return bindEqualityPredicate("eql?", obj, Value.Eql), true
	case "equal?":
		return bindEqualityPredicate("equal?", obj, Value.Identical), true
	default:
		return NewNil(), false
	}
}

// bindItself builds the Ruby-style Object#itself member for a receiver. It is an
// auto-invoked, zero-arity builtin: bare access (probe = obj.itself) invokes it
// immediately and yields the receiver, matching Ruby where itself returns self
// rather than a bound method. Because it auto-invokes, the builtin value is
// never durably reachable — it is constructed, run, and discarded in the same
// member access — so unlike the bound equality predicates it needs neither a
// captured-value charge nor a clone hook. The receiver is returned unchanged, so
// value ownership and the host-boundary isolation already established for it are
// preserved without copying.
func bindItself(receiver Value) Value {
	name := fmt.Sprintf("%s.itself", receiver.Kind())
	return NewAutoBuiltin(name, func(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := requireItselfCall(name, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		return receiver, nil
	})
}

// requireItselfCall enforces itself's zero-arity shape: no positional
// arguments, no keyword arguments, and no block, mirroring Ruby's
// Object#itself.
func requireItselfCall(name string, args []Value, kwargs map[string]Value, block Value) error {
	if len(kwargs) > 0 {
		return fmt.Errorf("%s does not accept keyword arguments", name)
	}
	if valueBlock(block) != nil {
		return fmt.Errorf("%s does not accept a block", name)
	}
	if len(args) > 0 {
		return fmt.Errorf("%s expects 0 arguments, got %d", name, len(args))
	}
	return nil
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
