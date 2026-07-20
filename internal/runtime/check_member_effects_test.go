package runtime

import (
	"fmt"
	"testing"
)

// Receiver facts survive member calls the contract registry proves pure and
// are still discarded for known mutators, unregistered members, dynamic
// dispatch, and user overrides. The observable channel is the argument
// mismatch diagnostic: a preserved array<int> fact contradicts a string
// parameter, while a poisoned fact stays silent.

const memberEffectsProbePrelude = `def takes(value: string)
  value
end

`

func requireArrayFactSurvives(t *testing.T, body string) {
	t.Helper()
	source := memberEffectsProbePrelude + body
	requireCheckWarningContains(
		t,
		compileScriptDefault(t, source),
		"call to takes argument value expected string, got array<int>",
	)
}

func requireArrayFactPoisoned(t *testing.T, body string) {
	t.Helper()
	source := memberEffectsProbePrelude + body
	requireNoCheckWarnings(t, compileScriptDefault(t, source))
}

func TestPureMemberCallsPreserveReceiverFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"registered pure read", "def probe()\n  a = [1, 2, 3]\n  a.at(0)\n  takes(a)\nend"},
		{"pure read with optional arity", "def probe()\n  a = [1, 2, 3]\n  a.slice(0, 1)\n  takes(a)\nend"},
		{"pure fetch without block", "def probe()\n  a = [1, 2, 3]\n  a.fetch(0)\n  takes(a)\nend"},
		{"universal predicate", "def probe()\n  a = [1, 2, 3]\n  a.nil?\n  takes(a)\nend"},
		{"chained pure calls", "def probe()\n  a = [1, 2, 3]\n  a.slice(0, 1).at(0)\n  takes(a)\nend"},
		{"alias of pure receiver", "def probe()\n  a = [1, 2, 3]\n  b = a\n  a.at(0)\n  takes(b)\nend"},
		{"safe navigation pure call", "def probe(a: array<int>?)\n  a&.at(0)\n  takes(a)\nend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireArrayFactSurvives(t, tc.body)
		})
	}

	t.Run("safe navigation preserved fact", func(t *testing.T) {
		t.Parallel()
		requireCheckWarningContains(
			t,
			compileScriptDefault(t, memberEffectsProbePrelude+"def probe(a: array<int>?)\n  a&.at(0)\n  takes(a)\nend"),
			"got array<int>?",
		)
	})
}

func TestUnclassifiedMemberCallsStillPoisonReceiverFacts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{"unregistered mutator", "def probe()\n  a = [1, 2, 3]\n  a.push(9)\n  takes(a)\nend"},
		{"unregistered reader", "def probe()\n  a = [1, 2, 3]\n  a.size\n  takes(a)\nend"},
		{"alias of mutated receiver", "def probe()\n  a = [1, 2, 3]\n  b = a\n  a.push(9)\n  takes(b)\nend"},
		{"block runs user code", "def probe()\n  a = [1, 2, 3]\n  a.fetch(9) { 0 }\n  takes(a)\nend"},
		{"impure argument", "def probe()\n  a = [1, 2, 3]\n  a.at(idx())\n  takes(a)\nend\n\ndef idx()\n  0\nend"},
		{"safe navigation mutator", "def probe(a: array<int>?)\n  a&.push(9)\n  takes(a)\nend"},
		{"dynamic receiver argument escape", "def probe(a)\n  b = [1, 2, 3]\n  a.at(b)\n  takes(b)\nend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireArrayFactPoisoned(t, tc.body)
		})
	}
}

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
}
