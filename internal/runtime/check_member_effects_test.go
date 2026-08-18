package runtime

import (
	"fmt"
	"testing"
)

// TestMemberDispatchEffectResolution pins the effect resolver's boundaries:
// registered contracts answer for fixed kinds, kinds owning a universal
// name without a contract stay unknown, and mixed or dynamic arms stay
// unknown.
func TestMemberDispatchEffectResolution(t *testing.T) {
	t.Parallel()

	if effect := kindMemberEffect("array", "at"); effect != effectPure {
		t.Errorf("kindMemberEffect(array, at) = %v, want pure", effect)
	}
	if effect := kindMemberEffect("array", "push"); effect != effectUnknown {
		t.Errorf("kindMemberEffect(array, push) = %v, want unknown", effect)
	}
	if effect := kindMemberEffect("int", "nil?"); effect != effectPure {
		t.Errorf("kindMemberEffect(int, nil?) = %v, want pure via the universal contract", effect)
	}
	// duration owns eql? itself; its registered typed contract answers
	// ahead of the universal fallback.
	if effect := kindMemberEffect("duration", "eql?"); effect != effectPure {
		t.Errorf("kindMemberEffect(duration, eql?) = %v, want pure via the typed contract", effect)
	}

	if got := combineMemberEffects(effectPure, effectMutatesReceiver); got != effectMutatesReceiver {
		t.Errorf("combineMemberEffects(pure, mutates) = %v, want mutates-receiver", got)
	}
	if got := combineMemberEffects(effectMutatesReceiver, effectUnknown); got != effectUnknown {
		t.Errorf("combineMemberEffects(mutates, unknown) = %v, want unknown", got)
	}

	for _, effect := range []memberEffect{effectPure, effectMutatesReceiver, effectUnknown} {
		want := map[memberEffect]string{
			effectPure:            "pure",
			effectMutatesReceiver: "mutates-receiver",
			effectUnknown:         "unknown",
		}[effect]
		if got := fmt.Sprint(effect); got != want {
			t.Errorf("memberEffect(%d).String() = %q, want %q", effect, got, want)
		}
	}
}

// TestRegisteredContractsDeclareEffects requires every registry entry to
// carry an explicit effect classification: an unknown effect on a
// registered contract would silently disable the fact preservation the
// registry exists to prove.
func TestRegisteredContractsDeclareEffects(t *testing.T) {
	t.Parallel()

	for _, contract := range memberContracts {
		if contract.effect == effectUnknown {
			t.Errorf("contract %s.%s declares no receiver effect; classify it as pure or mutates-receiver", contract.receiver, contract.name)
		}
	}

	// The deprecated exported MutatesReceiver field must stay consistent
	// with the Effect classification it predates.
	for _, contract := range MemberContracts() {
		if contract.MutatesReceiver != (contract.Effect == "mutates-receiver") {
			t.Errorf("exported contract %s.%s: MutatesReceiver %v disagrees with Effect %q", contract.Receiver, contract.Name, contract.MutatesReceiver, contract.Effect)
		}
	}
}
