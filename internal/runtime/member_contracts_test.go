package runtime

import (
	"slices"
	"testing"
)

// The member contract registry must stay complete and honest: every public
// member the runtime dispatches is either registered with a contract or
// explicitly exempted, and every registry or exemption entry names a member
// the runtime actually dispatches. Receiver-owned members are checked
// against their own kind's worklist even when a universal helper shares the
// name (duration and time dispatch their own eql?); the universal
// contracts and exemptions cover only the true universal-fallback helpers.

func TestPublicMembersRegisteredOrExempt(t *testing.T) {
	t.Parallel()

	own := ownMemberNames()
	registered, universal := registeredMemberNames(t)
	universalExempt := make(map[string]struct{}, len(universalMemberContractExemptions))
	for _, name := range universalMemberContractExemptions {
		if !slices.Contains(universalMemberNames, name) {
			t.Errorf("universal exemption %s is not a universal member; remove the stale entry", name)
		}
		if _, ok := universal[name]; ok {
			t.Errorf("universal member %s is both registered and exempted; remove the exemption", name)
		}
		universalExempt[name] = struct{}{}
	}
	for _, name := range universalMemberNames {
		if _, ok := universal[name]; ok {
			continue
		}
		if _, ok := universalExempt[name]; !ok {
			t.Errorf("universal member %s has no contract and no exemption; register it or add it to universalMemberContractExemptions", name)
		}
	}

	for receiver, exempt := range memberContractExemptions {
		names, known := own[receiver]
		if !known {
			t.Errorf("exemptions name unknown receiver kind %s", receiver)
			continue
		}
		for _, name := range exempt {
			if !slices.Contains(names, name) {
				t.Errorf("exemption %s.%s is not a receiver-owned member; remove the stale entry", receiver, name)
			}
			if _, ok := registered[receiver+"."+name]; ok {
				t.Errorf("%s.%s is both registered and exempted; remove the exemption", receiver, name)
			}
		}
	}

	for receiver, names := range own {
		exempt := memberContractExemptions[receiver]
		for _, name := range names {
			if _, ok := registered[receiver+"."+name]; ok {
				continue
			}
			if slices.Contains(exempt, name) {
				continue
			}
			t.Errorf("public member %s.%s has no contract and no exemption; register it or add it to memberContractExemptions", receiver, name)
		}
	}
}

// registeredMemberNames indexes the registry for canonical names and
// aliases — typed-dispatch and value members by "<receiver>.<name>",
// universal contracts by bare name — and validates each contract's
// internal consistency along the way.
func registeredMemberNames(t *testing.T) (typed, universal map[string]struct{}) {
	t.Helper()

	own := ownMemberNames()
	typed = make(map[string]struct{}, len(memberContracts))
	universal = make(map[string]struct{})
	for _, contract := range memberContracts {
		names, known := own[contract.receiver]
		if contract.receiver == universalReceiverKind {
			names, known = universalMemberNames, true
		}
		if !known {
			t.Errorf("contract %s.%s names unknown receiver kind %s", contract.receiver, contract.name, contract.receiver)
			continue
		}
		for _, name := range append([]string{contract.name}, contract.aliases...) {
			index, key := typed, contract.receiver+"."+name
			if contract.receiver == universalReceiverKind {
				index, key = universal, name
			}
			if _, dup := index[key]; dup {
				t.Errorf("member %s.%s is registered twice", contract.receiver, name)
			}
			index[key] = struct{}{}
			if !slices.Contains(names, name) {
				t.Errorf("contract %s.%s names a member its dispatch does not own", contract.receiver, name)
			}
		}
		if contract.valueMember {
			call := contract.call
			if call.resultType == nil || call.minArgs != 0 || call.maxArgs != 0 || call.autoInvoke || call.usesBlock || len(contract.paramNames) != 0 {
				t.Errorf("value contract %s.%s must declare only a result type", contract.receiver, contract.name)
			}
			continue
		}
		if contract.call.maxArgs >= 0 && len(contract.paramNames) != contract.call.maxArgs {
			t.Errorf("contract %s.%s declares %d parameter names for %d positional slots", contract.receiver, contract.name, len(contract.paramNames), contract.call.maxArgs)
		}
		if contract.call.maxArgs < 0 && len(contract.paramNames) < contract.call.minArgs {
			t.Errorf("contract %s.%s declares %d parameter names but requires %d arguments", contract.receiver, contract.name, len(contract.paramNames), contract.call.minArgs)
		}
		if len(contract.call.paramTypes) > len(contract.paramNames) {
			t.Errorf("contract %s.%s declares %d parameter types for %d named parameters", contract.receiver, contract.name, len(contract.call.paramTypes), len(contract.paramNames))
		}
	}
	return typed, universal
}

// TestCheckerMemberSpecsDeriveFromRegistry pins the checker's three member
// spec indexes to the registry contents, so a spec cannot be edited or
// added outside the central registry.
func TestCheckerMemberSpecsDeriveFromRegistry(t *testing.T) {
	t.Parallel()

	typedTotal, universalTotal, valueTotal := 0, 0, 0
	for _, contract := range memberContracts {
		entries := 1 + len(contract.aliases)
		switch {
		case contract.receiver == universalReceiverKind:
			universalTotal += entries
		case contract.valueMember:
			valueTotal += entries
		default:
			typedTotal += entries
		}
	}
	if len(staticMemberSpecs) != typedTotal {
		t.Fatalf("staticMemberSpecs has %d entries, want %d derived from the registry", len(staticMemberSpecs), typedTotal)
	}
	if len(universalMemberSpecs) != universalTotal {
		t.Fatalf("universalMemberSpecs has %d entries, want %d derived from the registry", len(universalMemberSpecs), universalTotal)
	}
	if len(staticMemberValueTypes) != valueTotal {
		t.Fatalf("staticMemberValueTypes has %d entries, want %d derived from the registry", len(staticMemberValueTypes), valueTotal)
	}
	for _, contract := range memberContracts {
		for _, name := range append([]string{contract.name}, contract.aliases...) {
			switch {
			case contract.receiver == universalReceiverKind:
				spec, ok := universalMemberSpecs[name]
				if !ok {
					t.Fatalf("universalMemberSpecs is missing %s", name)
				}
				if spec.minArgs != contract.call.minArgs || spec.maxArgs != contract.call.maxArgs {
					t.Fatalf("universalMemberSpecs entry %s does not match its contract", name)
				}
			case contract.valueMember:
				ty, ok := staticMemberValueTypes[contract.receiver+"."+name]
				if !ok {
					t.Fatalf("staticMemberValueTypes is missing %s.%s", contract.receiver, name)
				}
				if ty != contract.call.resultType {
					t.Fatalf("staticMemberValueTypes entry %s.%s does not match its contract", contract.receiver, name)
				}
			default:
				spec, ok := staticMemberSpecs[contract.receiver+"."+name]
				if !ok {
					t.Fatalf("staticMemberSpecs is missing %s.%s", contract.receiver, name)
				}
				if spec.minArgs != contract.call.minArgs || spec.maxArgs != contract.call.maxArgs {
					t.Fatalf("staticMemberSpecs entry %s.%s does not match its contract", contract.receiver, name)
				}
			}
		}
	}
}
