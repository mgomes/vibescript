package runtime

import "slices"

// This file is the central registry of builtin member contracts. The
// dispatch tables in members_*.go stay authoritative for behavior; a
// contract records that behavior statically so the checker and editor
// tooling consume one source instead of re-deriving it. Dispatch for
// unknown receivers and user-defined overrides is unaffected: contracts
// only apply where the checker already proves the receiver kind.

// memberContract is the runtime-owned contract of one builtin member: the
// receiver kind it dispatches on, its canonical name and aliases, the call
// shape with parameter and result types, and effect metadata.
type memberContract struct {
	// receiver is the runtime dispatch kind ("array", "string", ...), one
	// of the keys of MemberCompletionNames.
	receiver string
	// name is the canonical member name as dispatched at runtime.
	name string
	// aliases are alternate spellings that resolve to the same builtin and
	// share this contract.
	aliases []string
	// paramNames are the display names of the positional parameters,
	// index-aligned with call.paramTypes, for rendered signatures. Indexes
	// at or past call.minArgs are optional.
	paramNames []string
	// call is the statically enforced call shape: arity, keyword and block
	// acceptance, parameter types, and result type.
	call staticCallSpec
	// effects is the member's effect metadata.
	effects memberEffects
}

// memberEffects records what a member call may do beyond computing its
// result, so consumers can decide which receiver facts survive a call.
type memberEffects struct {
	// mutatesReceiver marks members that may modify their receiver in
	// place (push, map!, upcase!, ...).
	mutatesReceiver bool
}

// memberContracts is the registry of builtin member contracts, ordered by
// receiver kind, then name. Public members not yet migrated are listed in
// memberContractExemptions; the registry-completeness test requires every
// public member to appear in exactly one of the two.
var memberContracts = []memberContract{
	{
		receiver:   "array",
		name:       "at",
		paramNames: []string{"index"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, autoInvoke: true},
	},
	{
		receiver:   "array",
		name:       "fetch",
		paramNames: []string{"index", "default"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, autoInvoke: true, usesBlock: true},
	},
	{
		receiver:   "array",
		name:       "slice",
		paramNames: []string{"start", "length"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, rejectKeywords: true, autoInvoke: true},
	},
	{
		receiver:   "string",
		name:       "slice",
		paramNames: []string{"start", "length"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, autoInvoke: true},
	},
}

// staticMemberSpecs indexes the registered contracts by "<receiver>.<name>"
// for the checker's member resolution; aliases index the same contract.
var staticMemberSpecs = buildStaticMemberSpecs(memberContracts)

func buildStaticMemberSpecs(contracts []memberContract) map[string]staticCallSpec {
	specs := make(map[string]staticCallSpec, len(contracts))
	for _, contract := range contracts {
		specs[contract.receiver+"."+contract.name] = contract.call
		for _, alias := range contract.aliases {
			specs[contract.receiver+"."+alias] = contract.call
		}
	}
	return specs
}

// MemberParam is one positional parameter of an exported member contract.
type MemberParam struct {
	// Name is the display name used in rendered signatures.
	Name string
	// Type is the rendered parameter type; empty when undeclared.
	Type string
	// Optional reports whether the call may omit the parameter.
	Optional bool
}

// MemberContract is the exported view of one registered builtin member
// contract, for editor tooling such as LSP completion.
type MemberContract struct {
	// Receiver is the runtime receiver kind providing the member.
	Receiver string
	// Name is the canonical member name; Aliases resolve to the same
	// builtin under the same contract.
	Name    string
	Aliases []string
	// Params describes the positional parameters; Variadic reports an
	// unbounded tail beyond them.
	Params   []MemberParam
	Variadic bool
	// TakesBlock reports whether the member consumes a block argument.
	TakesBlock bool
	// AutoInvoke reports whether a bare member read invokes the builtin.
	AutoInvoke bool
	// Result is the rendered invariant result type; empty when unknown.
	Result string
	// MutatesReceiver reports whether a call may modify the receiver in
	// place.
	MutatesReceiver bool
}

// MemberContracts returns the registered builtin member contracts for
// editor tooling, ordered by receiver kind, then name. The returned
// slices are copies; callers may mutate them freely.
func MemberContracts() []MemberContract {
	out := make([]MemberContract, 0, len(memberContracts))
	for _, contract := range memberContracts {
		out = append(out, exportedMemberContract(contract))
	}
	return out
}

func exportedMemberContract(contract memberContract) MemberContract {
	params := make([]MemberParam, 0, len(contract.paramNames))
	for i, name := range contract.paramNames {
		param := MemberParam{Name: name, Optional: i >= contract.call.minArgs}
		if i < len(contract.call.paramTypes) && contract.call.paramTypes[i] != nil {
			param.Type = formatTypeExpr(contract.call.paramTypes[i])
		}
		params = append(params, param)
	}
	result := ""
	if contract.call.resultType != nil {
		result = formatTypeExpr(contract.call.resultType)
	}
	return MemberContract{
		Receiver:        contract.receiver,
		Name:            contract.name,
		Aliases:         slices.Clone(contract.aliases),
		Params:          params,
		Variadic:        contract.call.maxArgs < 0,
		TakesBlock:      contract.call.usesBlock,
		AutoInvoke:      contract.call.autoInvoke,
		Result:          result,
		MutatesReceiver: contract.effects.mutatesReceiver,
	}
}
