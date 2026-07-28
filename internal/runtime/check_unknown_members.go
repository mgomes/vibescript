package runtime

import "slices"

// vibes check did not reject an unknown method on a receiver whose type it
// statically knows, so `s.uppercase` on a `s: string` parameter reported "No
// issues found" and failed only at runtime.
//
// Choosing a wrong method name is the most frequent authoring mistake there is,
// and it was unassisted everywhere: completion offers every builtin member
// undifferentiated (#1057), check said nothing, and the runtime error arrived
// late. This closes the check-time half.
//
// Soundness rests on two existing guarantees rather than on new analysis:
//
//   - staticMemberReceiverKinds resolves a receiver to builtin dispatch kinds
//     only when no user-defined method or stored entry can intercept. Named
//     types, hash-like stores, and ambiguous numbers already return false
//     there, so a receiver that reaches this check really does dispatch to the
//     builtin table.
//   - the kinds below dispatch through newMemberTable(names), which is built
//     from the very name list consulted here. For those kinds the list is
//     authoritative by construction: a name absent from it cannot dispatch, so
//     reporting it cannot be a false positive.
//
// Kinds whose dispatch is a switch with a parallel hand-maintained list
// (duration, time, range, function, block) are deliberately excluded: there the
// list could drift behind the switch, and a drifted entry would report a
// working member as unknown, which is far worse than saying nothing.

// tableDrivenMemberKinds are the dispatch kinds whose member table is built
// from the name list, making that list the authoritative set.
//
// hash is deliberately absent. Its table is built from a name list like the
// others, but a hash also serves stored entries for any name the table does
// not own -- `({answer: 42}).answer` returns 42 -- so the list bounds only the
// builtin members, not what the receiver resolves. Treating it as
// authoritative reported valid code.
var tableDrivenMemberKinds = map[string]struct{}{
	"array":  {},
	"bool":   {},
	"float":  {},
	"int":    {},
	"money":  {},
	"nil":    {},
	"regex":  {},
	"string": {},
	"symbol": {},
}

// checkKnownReceiverMember reports a member that no possible receiver kind
// dispatches.
//
// A union reports only when every arm lacks the member, so a value that might
// be either of two kinds is reported solely when the call fails whichever it
// turns out to be. That keeps a union from being reported for a member one of
// its arms genuinely has.
func (c *scriptChecker) checkKnownReceiverMember(function string, member *MemberExpr) {
	if member == nil || member.Property == "" {
		return
	}
	kinds, ok := c.staticMemberReceiverKinds(member)
	if !ok || len(kinds) == 0 {
		return
	}
	for _, kind := range kinds {
		if _, authoritative := tableDrivenMemberKinds[kind]; !authoritative {
			return
		}
		if kindDispatchesMember(kind, member.Property) {
			return
		}
	}
	c.add(function, member.Pos(), "unknown %s member %s%s", kinds[0], member.Property,
		didYouMean(member.Property, memberSuggestionNames(kinds[0])))
}

// kindDispatchesMember reports whether kind resolves property, through either
// its own table or the universal fallback every value answers.
func kindDispatchesMember(kind, property string) bool {
	if slices.Contains(universalMemberNames, property) {
		return true
	}
	names, ok := ownMemberNames()[kind]
	if !ok {
		return true
	}
	return slices.Contains(names, property)
}

// memberSuggestionNames returns the candidates a did-you-mean draws from for a
// receiver kind.
func memberSuggestionNames(kind string) []string {
	names := ownMemberNames()[kind]
	candidates := make([]string, 0, len(names)+len(universalMemberNames))
	candidates = append(candidates, names...)
	candidates = append(candidates, universalMemberNames...)
	return candidates
}
