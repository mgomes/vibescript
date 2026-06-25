package runtime

import "fmt"

// symbolMemberNames mirrors the names dispatched by symbolMember and feeds
// "did you mean" suggestions on the error path. Keep it in sync with the
// switch below; TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves.
var symbolMemberNames = []string{"id2name", "to_s", "to_sym"}

var symbolBuiltinMembers = newMemberTable(symbolMemberNames)

func symbolMember(sym Value, property string) (Value, error) {
	if member, ok := symbolBuiltinMembers.lookup(property, symbolMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), fmt.Errorf("unknown symbol method %s%s", property, didYouMean(property, symbolMemberNames))
}

func symbolMemberBuiltin(property string) (Value, error) {
	switch property {
	case "id2name", "to_s":
		name := "symbol." + property
		return NewAutoBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("%s does not take arguments", name)
			}
			return NewString(receiver.String()), nil
		}), nil
	case "to_sym":
		return NewAutoBuiltin("symbol.to_sym", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("symbol.to_sym does not take arguments")
			}
			return receiver, nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown symbol method %s", property)
	}
}
