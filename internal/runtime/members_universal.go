package runtime

import (
	"fmt"
	"slices"
)

// Universal members are Ruby's Object-level block helpers exposed on every core
// value kind. `tap` yields the receiver to its block and returns the receiver,
// so it threads side effects through a pipeline without changing the value.
// `yield_self` yields the receiver and returns the block's result, so it
// rewrites a value inline. Both require a block, take no positional or keyword
// arguments, and pass the receiver as the block's single argument, matching
// Ruby's Object#tap and Object#yield_self.
//
// They resolve through resolveMember as a fallback after each kind's own
// dispatch, so a user-defined method, hash key, or instance variable named
// `tap` or `yield_self` still wins. Keeping them out of the per-kind
// *MemberNames lists preserves the invariant that every listed name resolves
// through its own dispatch switch (TestMemberSuggestionCandidatesResolve);
// editor completion still surfaces them through MemberCompletionNames, which
// appends universalMemberNames to every receiver type.
var universalMemberNames = []string{"tap", "yield_self"}

// isUniversalMemberName reports whether property names a universal Object-level
// helper handled by universalMember.
func isUniversalMemberName(property string) bool {
	return property == "tap" || property == "yield_self"
}

// universalMember builds the universal Object-level helper named by property.
// It assumes property is one of universalMemberNames; callers gate on
// isUniversalMemberName first.
func universalMember(property string) Value {
	switch property {
	case "tap":
		return newUniversalBlockBuiltin("tap", true)
	case "yield_self":
		return newUniversalBlockBuiltin("yield_self", false)
	default:
		// Unreachable: callers gate on isUniversalMemberName.
		return NewNil()
	}
}

// newUniversalBlockBuiltin returns the auto-invoked builtin for a universal
// block helper. When returnReceiver is true the helper returns its receiver
// (Object#tap); otherwise it returns the block's result (Object#yield_self).
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

// appendUniversalMemberNames returns names with the universal Object-level
// helper names appended, deduplicating so a list that already mentions one is
// not given a duplicate. The result is a fresh slice; the input is never
// mutated.
func appendUniversalMemberNames(names []string) []string {
	out := slices.Clone(names)
	for _, universal := range universalMemberNames {
		if !slices.Contains(out, universal) {
			out = append(out, universal)
		}
	}
	return out
}
