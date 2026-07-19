package runtime

import (
	"slices"
	"testing"
)

// The member contract registry must stay complete and honest: every public
// member the runtime dispatches is either registered with a contract or
// explicitly exempted, and every registry or exemption entry names a member
// the runtime actually dispatches.

func TestPublicMembersRegisteredOrExempt(t *testing.T) {
	t.Parallel()

	public := MemberCompletionNames()
	registered := registeredMemberNames(t)
	universalExempt := make(map[string]struct{}, len(universalMemberContractExemptions))
	for _, name := range universalMemberContractExemptions {
		if !slices.Contains(universalMemberNames, name) {
			t.Errorf("universal exemption %s is not a universal member; remove the stale entry", name)
		}
		universalExempt[name] = struct{}{}
	}
	for _, name := range universalMemberNames {
		if _, ok := universalExempt[name]; !ok {
			t.Errorf("universal member %s has no contract and no exemption; register it or add it to universalMemberContractExemptions", name)
		}
	}

	for receiver, exempt := range memberContractExemptions {
		names, known := public[receiver]
		if !known {
			t.Errorf("exemptions name unknown receiver kind %s", receiver)
			continue
		}
		for _, name := range exempt {
			if !slices.Contains(names, name) {
				t.Errorf("exemption %s.%s is not a public member; remove the stale entry", receiver, name)
			}
			if _, ok := registered[receiver+"."+name]; ok {
				t.Errorf("%s.%s is both registered and exempted; remove the exemption", receiver, name)
			}
		}
	}

	for receiver, names := range public {
		exempt := memberContractExemptions[receiver]
		for _, name := range names {
			if _, ok := universalExempt[name]; ok {
				continue
			}
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

// registeredMemberNames indexes the registry by "<receiver>.<name>" for
// canonical names and aliases, and validates each contract's internal
// consistency along the way.
func registeredMemberNames(t *testing.T) map[string]struct{} {
	t.Helper()

	public := MemberCompletionNames()
	registered := make(map[string]struct{}, len(memberContracts))
	for _, contract := range memberContracts {
		names, known := public[contract.receiver]
		if !known {
			t.Errorf("contract %s.%s names unknown receiver kind %s", contract.receiver, contract.name, contract.receiver)
			continue
		}
		for _, name := range append([]string{contract.name}, contract.aliases...) {
			key := contract.receiver + "." + name
			if _, dup := registered[key]; dup {
				t.Errorf("member %s is registered twice", key)
			}
			registered[key] = struct{}{}
			if !slices.Contains(names, name) {
				t.Errorf("contract %s names a member the runtime does not dispatch", key)
			}
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
	return registered
}

// TestStaticMemberSpecsDeriveFromRegistry pins the checker's member spec
// index to the registry contents, so a spec cannot be edited or added
// outside the central registry.
func TestStaticMemberSpecsDeriveFromRegistry(t *testing.T) {
	t.Parallel()

	total := 0
	for _, contract := range memberContracts {
		total += 1 + len(contract.aliases)
	}
	if len(staticMemberSpecs) != total {
		t.Fatalf("staticMemberSpecs has %d entries, want %d derived from the registry", len(staticMemberSpecs), total)
	}
	for _, contract := range memberContracts {
		for _, name := range append([]string{contract.name}, contract.aliases...) {
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
