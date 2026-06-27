package runtime

import "fmt"

// The scalar kinds nil and bool expose the universal `inspect`, `to_s`,
// `string`, and `nil?` methods. Each has its own member table so the builtins
// are constructed once and cached, and its own *MemberNames list so "did you
// mean" suggestions and editor completion resolve. TestMemberSuggestionCandidatesResolve
// enforces that every listed name resolves through the matching build switch.
// Symbol members live in members_symbol.go because symbols also expose Ruby's
// name-conversion helpers.
var (
	nilMemberNames    = []string{"inspect", "to_s", "string", "nil?"}
	nilBuiltinMembers = newMemberTable(nilMemberNames)

	boolMemberNames    = []string{"inspect", "to_s", "string", "nil?"}
	boolBuiltinMembers = newMemberTable(boolMemberNames)
)

func (exec *Execution) nilMember(obj Value, property string, pos Position) (Value, error) {
	if member, ok := nilBuiltinMembers.lookup(property, nilMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), exec.errorAt(pos, "unknown nil method %s%s", property, didYouMean(property, nilMemberNames))
}

func nilMemberBuiltin(property string) (Value, error) {
	switch property {
	case "inspect":
		return newInspectBuiltin("nil"), nil
	case "to_s", "string":
		return newToStringBuiltin("nil", property), nil
	case "nil?":
		return newNilPredicateBuiltin("nil"), nil
	default:
		return NewNil(), fmt.Errorf("unknown nil method %s", property)
	}
}

func (exec *Execution) boolMember(obj Value, property string, pos Position) (Value, error) {
	if member, ok := boolBuiltinMembers.lookup(property, boolMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), exec.errorAt(pos, "unknown bool method %s%s", property, didYouMean(property, boolMemberNames))
}

func boolMemberBuiltin(property string) (Value, error) {
	switch property {
	case "inspect":
		return newInspectBuiltin("bool"), nil
	case "to_s", "string":
		return newToStringBuiltin("bool", property), nil
	case "nil?":
		return newNilPredicateBuiltin("bool"), nil
	default:
		return NewNil(), fmt.Errorf("unknown bool method %s", property)
	}
}

// requireNullaryCall enforces the call shape shared by the Ruby-style nullary
// conversion and predicate methods (to_s, string, to_i, to_f, nil?, id2name,
// intern, to_sym): no positional arguments, no keyword arguments, and no block.
// name identifies the receiver method in the error so it reads naturally (for
// example "int.to_i does not take keyword arguments"). Rejecting kwargs and a
// block keeps a stray argument from being silently dropped — for example
// `"42".to_i(base: 16)` raises rather than quietly parsing base 10.
func requireNullaryCall(name string, args []Value, kwargs map[string]Value, block Value) error {
	if len(args) > 0 {
		return fmt.Errorf("%s does not take arguments", name)
	}
	if len(kwargs) > 0 {
		return fmt.Errorf("%s does not take keyword arguments", name)
	}
	if valueBlock(block) != nil {
		return fmt.Errorf("%s does not take a block", name)
	}
	return nil
}

// newToStringBuiltin returns a no-argument builtin that renders the receiver as
// a string using the same display form string interpolation produces (Ruby's
// Object#to_s). typeName names the receiver in the builtin's identifier and in
// argument errors (for example "int.to_s"); property is the invoked name so the
// shared `to_s` and `string` aliases each report under the name the script used.
// The rendering of every scalar kind this serves (nil, bool, int, float, string,
// symbol) is bounded by the value's own footprint, so no memory projection is
// needed the way aggregate interpolation requires one.
func newToStringBuiltin(typeName, property string) Value {
	name := typeName + "." + property
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := requireNullaryCall(name, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		return NewString(receiver.String()), nil
	})
}

// newNilPredicateBuiltin returns a no-argument builtin implementing Ruby's
// Object#nil?, true only for the nil receiver and false for every other value.
// typeName names the receiver in the builtin's identifier and argument errors.
func newNilPredicateBuiltin(typeName string) Value {
	name := typeName + ".nil?"
	return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := requireNullaryCall(name, args, kwargs, block); err != nil {
			return NewNil(), err
		}
		return NewBool(receiver.IsNil()), nil
	})
}
