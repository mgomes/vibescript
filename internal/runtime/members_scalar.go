package runtime

import "fmt"

// The scalar kinds symbol, nil, and bool expose only the universal `inspect`
// method. Each has its own member table so the builtin is constructed once and
// cached, and its own *MemberNames list so "did you mean" suggestions and editor
// completion resolve. TestMemberSuggestionCandidatesResolve enforces that every
// listed name resolves through the matching build switch.
var (
	symbolMemberNames    = []string{"inspect"}
	symbolBuiltinMembers = newMemberTable(symbolMemberNames)

	nilMemberNames    = []string{"inspect"}
	nilBuiltinMembers = newMemberTable(nilMemberNames)

	boolMemberNames    = []string{"inspect"}
	boolBuiltinMembers = newMemberTable(boolMemberNames)
)

func (exec *Execution) symbolMember(obj Value, property string, pos Position) (Value, error) {
	if member, ok := symbolBuiltinMembers.lookup(property, symbolMemberBuiltin); ok {
		return member, nil
	}
	return NewNil(), exec.errorAt(pos, "unknown symbol method %s%s", property, didYouMean(property, symbolMemberNames))
}

func symbolMemberBuiltin(property string) (Value, error) {
	switch property {
	case "inspect":
		return newInspectBuiltin("symbol"), nil
	default:
		return NewNil(), fmt.Errorf("unknown symbol method %s", property)
	}
}

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
	default:
		return NewNil(), fmt.Errorf("unknown bool method %s", property)
	}
}
