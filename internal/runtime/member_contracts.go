package runtime

import "slices"

// This file is the central registry of builtin member contracts. The
// dispatch tables in members_*.go stay authoritative for behavior; a
// contract records that behavior statically so the checker and editor
// tooling consume one source instead of re-deriving it. Dispatch for
// unknown receivers and user-defined overrides is unaffected: contracts
// only apply where the checker already proves the receiver kind.

// universalReceiverKind marks a contract that applies to every receiver
// through the universal Object-level fallback in resolveMember, rather
// than one kind's typed dispatch.
const universalReceiverKind = "universal"

// memberContract is the runtime-owned contract of one builtin member: the
// receiver kind it dispatches on, its canonical name and aliases, the call
// shape with parameter and result types, and effect metadata.
type memberContract struct {
	// receiver is the runtime dispatch kind ("array", "string", ...), one
	// of the keys of MemberCompletionNames, or universalReceiverKind for
	// the Object-level fallback helpers.
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
	// valueMember marks members the runtime exposes as direct scalar
	// values rather than builtins: they contribute call.resultType as the
	// fact of a bare member read but must not register a callable spec
	// (`d.to_i()` attempts to call the returned int at runtime).
	valueMember bool
	// call is the statically enforced call shape: arity, keyword and block
	// acceptance, parameter types, and result type. For value members only
	// resultType applies.
	call staticCallSpec
	// effect classifies what a call may do to the receiver.
	effect memberEffect
}

// memberEffect classifies what a member does to its receiver, so consumers
// can decide which receiver facts survive a dispatch.
type memberEffect uint8

const (
	// effectUnknown is the zero value: the contract does not classify the
	// member, so consumers must assume it may do anything.
	effectUnknown memberEffect = iota
	// effectPure marks members that neither mutate their receiver nor run
	// user code during dispatch.
	effectPure
	// effectMutatesReceiver marks members that may modify their receiver
	// in place (push, map!, upcase!, ...).
	effectMutatesReceiver
)

// String renders the effect for exported contracts and diagnostics.
func (effect memberEffect) String() string {
	switch effect {
	case effectPure:
		return "pure"
	case effectMutatesReceiver:
		return "mutates-receiver"
	}
	return "unknown"
}

// scalarMemberSpec is the contract shared by the nullary scalar conversion
// members: no arguments, no keywords, no block, auto-invoked on a bare read.
func scalarMemberSpec(result *TypeExpr) staticCallSpec {
	return staticCallSpec{minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: result}
}

// scalarConversionContract is a registry entry for one nullary scalar
// conversion member with an invariant result.
func scalarConversionContract(receiver, name string, result *TypeExpr) memberContract {
	return memberContract{receiver: receiver, name: name, call: scalarMemberSpec(result), effect: effectPure}
}

// temporalEqlContract is the contract of the temporal eql? methods, which
// own dispatch ahead of the universal fallback. They reject keywords but
// intentionally ignore a supplied block.
func temporalEqlContract(receiver string) memberContract {
	return memberContract{
		receiver:   receiver,
		name:       "eql?",
		paramNames: []string{"other"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, resultType: checkTypeBool},
		effect:     effectPure,
	}
}

// temporalValueContract is a registry entry for one conversion-style
// temporal member the runtime exposes as a direct value rather than a
// builtin (see memberContract.valueMember). Reading the value is pure.
func temporalValueContract(receiver, name string, result *TypeExpr) memberContract {
	return memberContract{receiver: receiver, name: name, valueMember: true, call: staticCallSpec{resultType: result}, effect: effectPure}
}

// memberContracts is the registry of builtin member contracts, ordered by
// receiver kind, then name, with the universal fallback contracts last.
// Public members not yet migrated are listed in memberContractExemptions;
// the registry-completeness test requires every public member to appear in
// exactly one of the two.
var memberContracts = []memberContract{
	{
		receiver:   "array",
		name:       "at",
		paramNames: []string{"index"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, autoInvoke: true},
		effect:     effectPure,
	},
	{
		receiver:   "array",
		name:       "fetch",
		paramNames: []string{"index", "default"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, autoInvoke: true, usesBlock: true},
		effect:     effectPure,
	},
	{
		receiver:   "array",
		name:       "slice",
		paramNames: []string{"start", "length"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, rejectKeywords: true, autoInvoke: true},
		effect:     effectPure,
	},
	{
		receiver:   "string",
		name:       "slice",
		paramNames: []string{"start", "length"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, autoInvoke: true},
		effect:     effectPure,
	},

	// Callable scalar conversions are nullary auto-invoked builtins with
	// invariant results (members_scalar.go, members_symbol.go,
	// members_string.go, members_numeric.go, members_temporal.go).
	scalarConversionContract("nil", "to_s", checkTypeString),
	scalarConversionContract("nil", "string", checkTypeString),
	scalarConversionContract("bool", "to_s", checkTypeString),
	scalarConversionContract("bool", "string", checkTypeString),
	scalarConversionContract("symbol", "id2name", checkTypeString),
	scalarConversionContract("symbol", "to_s", checkTypeString),
	scalarConversionContract("symbol", "string", checkTypeString),
	scalarConversionContract("symbol", "to_sym", checkTypeSymbol),
	scalarConversionContract("string", "to_i", checkTypeInt),
	scalarConversionContract("string", "to_f", checkTypeFloat),
	scalarConversionContract("string", "to_s", checkTypeString),
	scalarConversionContract("string", "string", checkTypeString),
	scalarConversionContract("string", "to_sym", checkTypeSymbol),
	scalarConversionContract("string", "intern", checkTypeSymbol),
	scalarConversionContract("int", "to_i", checkTypeInt),
	scalarConversionContract("int", "to_f", checkTypeFloat),
	scalarConversionContract("int", "to_s", checkTypeString),
	scalarConversionContract("int", "string", checkTypeString),
	scalarConversionContract("float", "to_i", checkTypeInt),
	scalarConversionContract("float", "to_f", checkTypeFloat),
	scalarConversionContract("float", "to_s", checkTypeString),
	scalarConversionContract("float", "string", checkTypeString),
	scalarConversionContract("money", "to_s", checkTypeString),
	scalarConversionContract("money", "string", checkTypeString),
	scalarConversionContract("duration", "to_s", checkTypeString),
	scalarConversionContract("duration", "string", checkTypeString),
	temporalEqlContract("duration"),
	scalarConversionContract("time", "to_s", checkTypeString),
	scalarConversionContract("time", "string", checkTypeString),
	temporalEqlContract("time"),
	{
		// range.to_a ignores a block at runtime, so it cannot use the
		// stricter scalarMemberSpec contract shared by the other
		// conversion builtins.
		receiver: "range",
		name:     "to_a",
		call:     staticCallSpec{minArgs: 0, maxArgs: 0, rejectKeywords: true, autoInvoke: true, resultType: checkTypeIntArray},
		effect:   effectPure,
	},

	// Conversion-style temporal members the runtime exposes as direct
	// values rather than builtins. They contribute a result fact to a bare
	// member read but stay outside staticMemberSpecs: `d.to_i()` attempts
	// to call the returned int, so they must not resolve as callables.
	temporalValueContract("duration", "to_i", checkTypeInt),
	temporalValueContract("time", "to_i", checkTypeInt),
	temporalValueContract("time", "tv_sec", checkTypeInt),
	temporalValueContract("time", "to_f", checkTypeFloat),
	temporalValueContract("time", "to_r", checkTypeFloat),
	temporalValueContract("time", "to_a", checkTypeArray),

	// Object-level predicates with fixed boolean results
	// (members_universal.go). Their specs apply only when every known
	// receiver arm dispatches them through the runtime universal fallback
	// — no class instances, whose user methods take precedence.
	{
		receiver: universalReceiverKind,
		name:     "nil?",
		call:     staticCallSpec{minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
		effect:   effectPure,
	},
	{
		receiver: universalReceiverKind,
		name:     "frozen?",
		call:     staticCallSpec{minArgs: 0, maxArgs: 0, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
		effect:   effectPure,
	},
	{
		receiver:   universalReceiverKind,
		name:       "eql?",
		paramNames: []string{"other"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, resultType: checkTypeBool},
		effect:     effectPure,
	},
	{
		receiver:   universalReceiverKind,
		name:       "equal?",
		paramNames: []string{"other"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, resultType: checkTypeBool},
		effect:     effectPure,
	},
	{
		receiver:   universalReceiverKind,
		name:       "respond_to?",
		paramNames: []string{"name", "include_all"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 2, rejectKeywords: true, rejectBlock: true, autoInvoke: true, paramTypes: []*TypeExpr{checkTypeMethodName, checkTypeBool}, resultType: checkTypeBool},
		effect:     effectPure,
	},
	{
		receiver:   universalReceiverKind,
		name:       "is_a?",
		paramNames: []string{"class"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
		effect:     effectPure,
	},
	{
		receiver:   universalReceiverKind,
		name:       "kind_of?",
		paramNames: []string{"class"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
		effect:     effectPure,
	},
	{
		receiver:   universalReceiverKind,
		name:       "instance_of?",
		paramNames: []string{"class"},
		call:       staticCallSpec{minArgs: 1, maxArgs: 1, rejectKeywords: true, rejectBlock: true, autoInvoke: true, resultType: checkTypeBool},
		effect:     effectPure,
	},
}

// staticMemberSpecs indexes the registered typed-dispatch contracts by
// "<receiver>.<name>" for the checker's member resolution; aliases index
// the same contract. Universal contracts live in universalMemberSpecs and
// value members in staticMemberValueTypes instead.
var staticMemberSpecs = buildStaticMemberSpecs(memberContracts)

func buildStaticMemberSpecs(contracts []memberContract) map[string]staticCallSpec {
	specs := make(map[string]staticCallSpec, len(contracts))
	for _, contract := range contracts {
		if contract.receiver == universalReceiverKind || contract.valueMember {
			continue
		}
		specs[contract.receiver+"."+contract.name] = contract.call
		for _, alias := range contract.aliases {
			specs[contract.receiver+"."+alias] = contract.call
		}
	}
	return specs
}

// universalMemberSpecs indexes the universal-fallback contracts by member
// name for the checker's universal dispatch resolution.
var universalMemberSpecs = buildUniversalMemberSpecs(memberContracts)

func buildUniversalMemberSpecs(contracts []memberContract) map[string]staticCallSpec {
	specs := make(map[string]staticCallSpec)
	for _, contract := range contracts {
		if contract.receiver != universalReceiverKind {
			continue
		}
		specs[contract.name] = contract.call
		for _, alias := range contract.aliases {
			specs[alias] = contract.call
		}
	}
	return specs
}

// staticMemberValueTypes indexes the value-member contracts by
// "<receiver>.<name>": members the runtime exposes as direct values, whose
// contract contributes a result fact to a bare member read only.
var staticMemberValueTypes = buildStaticMemberValueTypes(memberContracts)

func buildStaticMemberValueTypes(contracts []memberContract) map[string]*TypeExpr {
	types := make(map[string]*TypeExpr)
	for _, contract := range contracts {
		if !contract.valueMember {
			continue
		}
		types[contract.receiver+"."+contract.name] = contract.call.resultType
		for _, alias := range contract.aliases {
			types[contract.receiver+"."+alias] = contract.call.resultType
		}
	}
	return types
}

// staticMemberEffects indexes the registered typed-dispatch and value
// contracts' receiver effects by "<receiver>.<name>"; aliases share the
// contract's effect. Absent keys mean the member's effect is unknown.
var staticMemberEffects = buildStaticMemberEffects(memberContracts)

func buildStaticMemberEffects(contracts []memberContract) map[string]memberEffect {
	effects := make(map[string]memberEffect, len(contracts))
	for _, contract := range contracts {
		if contract.receiver == universalReceiverKind {
			continue
		}
		effects[contract.receiver+"."+contract.name] = contract.effect
		for _, alias := range contract.aliases {
			effects[contract.receiver+"."+alias] = contract.effect
		}
	}
	return effects
}

// universalMemberEffects indexes the universal-fallback contracts' receiver
// effects by member name. Absent keys mean the helper's effect is unknown.
var universalMemberEffects = buildUniversalMemberEffects(memberContracts)

func buildUniversalMemberEffects(contracts []memberContract) map[string]memberEffect {
	effects := make(map[string]memberEffect)
	for _, contract := range contracts {
		if contract.receiver != universalReceiverKind {
			continue
		}
		effects[contract.name] = contract.effect
		for _, alias := range contract.aliases {
			effects[alias] = contract.effect
		}
	}
	return effects
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
	// Receiver is the runtime receiver kind providing the member, or
	// "universal" for the Object-level fallback helpers every value
	// answers.
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
	// ValueMember reports a member exposed as a direct value rather than
	// a callable: reading it yields Result and calling it is not part of
	// the contract.
	ValueMember bool
	// Result is the rendered invariant result type; empty when unknown.
	Result string
	// Effect is the member's declared receiver effect: "pure",
	// "mutates-receiver", or "unknown".
	Effect string
	// MutatesReceiver reports whether a call may modify the receiver in
	// place.
	//
	// Deprecated: use Effect, which also distinguishes pure members from
	// unclassified ones.
	MutatesReceiver bool
}

// MemberContracts returns the registered builtin member contracts for
// editor tooling, ordered by receiver kind, then name, with the universal
// contracts last. The returned slices are copies; callers may mutate them
// freely.
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
		ValueMember:     contract.valueMember,
		Result:          result,
		Effect:          contract.effect.String(),
		MutatesReceiver: contract.effect == effectMutatesReceiver,
	}
}

// memberContractExemptions lists the public members each receiver kind
// dispatches that have no registered contract yet. Together with the
// universal exemptions below it is the explicit worklist of unmigrated
// members: the registry-completeness test requires every public member to
// be registered or listed here, so adding a member forces a deliberate
// choice, and migrating one requires removing its exemption.
var memberContractExemptions = map[string][]string{
	"array": {
		"all?", "any?", "append", "chunk", "chunk_while", "clear", "combination",
		"compact", "compact!", "count", "cycle", "delete", "delete_if",
		"difference", "dig", "drop", "drop_while", "each", "each_cons",
		"each_slice", "each_with_index", "empty?", "fill", "filter_map", "find",
		"find_index", "first", "flatten", "grep", "grep_v", "group_by",
		"group_by_stable", "include?", "index", "insert", "inspect", "join",
		"keep_if", "last", "length", "map", "map!", "map_with_index", "max",
		"max_by", "min", "min_by", "minmax", "none?", "one?", "partition",
		"permutation", "pop", "prepend", "product", "push", "reduce", "reject",
		"reject!", "repeated_combination", "repeated_permutation", "reverse",
		"reverse!", "reverse_each", "rindex", "rotate", "sample", "select",
		"select!", "shift", "shuffle", "size", "slice_when", "sort", "sort!",
		"sort_by", "sum", "take", "take_while", "tally", "to_h", "transpose",
		"union", "uniq", "uniq!", "unshift", "values_at", "window", "zip",
	},
	"block": {
		"call", "lambda?",
	},
	"bool": {
		"inspect",
	},
	"duration": {
		"after", "ago", "before", "between?", "day", "days", "format",
		"from_now", "hour", "hours", "in_days", "in_hours", "in_minutes",
		"in_months", "in_seconds", "in_weeks", "in_years", "iso8601", "minute",
		"minutes", "parts", "second", "seconds", "since", "until", "week",
		"weeks",
	},
	"float": {
		"abs", "between?", "ceil", "clamp", "div", "divmod", "fdiv", "finite?",
		"floor", "infinite?", "inspect", "modulo", "nan?", "negative?",
		"nonzero?", "positive?", "remainder", "round", "zero?",
	},
	"function": {
		"call",
	},
	"hash": {
		"clear", "compact", "deep_transform_keys", "default", "default_proc",
		"delete", "delete_if", "dig", "each", "each_key", "each_value",
		"each_with_index", "empty?", "except", "fetch", "fetch_values",
		"flatten", "has_key?", "has_value?", "include?", "inspect", "keep_if",
		"key?", "keys", "length", "map_with_index", "member?", "merge",
		"merge!", "reject", "remap_keys", "replace", "select", "size", "slice",
		"store", "to_a", "transform_keys", "transform_values", "update",
		"value?", "values", "values_at",
	},
	"int": {
		"abs", "between?", "ceil", "clamp", "day", "days", "div", "divmod",
		"downto", "even?", "fdiv", "floor", "hour", "hours", "inspect",
		"minute", "minutes", "modulo", "negative?", "next", "nonzero?", "odd?",
		"positive?", "pred", "remainder", "round", "second", "seconds", "step",
		"succ", "times", "upto", "week", "weeks", "zero?",
	},
	"money": {
		"amount", "between?", "cents", "currency", "format",
	},
	"nil": {
		"inspect",
	},
	"range": {
		"count", "cover?", "each", "exclude_end?", "find", "first", "include?",
		"last", "map", "max", "member?", "min", "reduce", "reject", "select",
		"size", "step", "sum",
	},
	"regex": {
		"flags", "inspect", "match", "match?", "source",
	},
	"string": {
		"between?", "bytes", "bytesize", "byteslice", "capitalize",
		"capitalize!", "casecmp", "casecmp?", "center", "chars", "chomp",
		"chomp!", "chop", "chop!", "chr", "clamp", "clear", "codepoints",
		"concat", "count", "delete", "delete!", "delete_prefix",
		"delete_prefix!", "delete_suffix", "delete_suffix!", "downcase",
		"downcase!", "each_byte", "each_char", "each_codepoint", "each_line",
		"empty?", "end_with?", "getbyte", "gsub", "gsub!", "hex", "include?",
		"index", "insert", "inspect", "length", "lines", "ljust", "lstrip",
		"lstrip!", "match", "match?", "oct", "ord", "partition", "prepend",
		"replace", "reverse", "reverse!", "rindex", "rjust", "rpartition",
		"rstrip", "rstrip!", "scan", "size", "split", "squeeze", "squeeze!",
		"squish", "squish!", "start_with?", "strip", "strip!", "sub", "sub!",
		"swapcase", "swapcase!", "template", "tr", "tr!", "upcase", "upcase!",
	},
	"symbol": {
		"inspect",
	},
	"time": {
		"<=>", "between?", "ceil", "day", "dst?", "floor", "format", "friday?",
		"getgm", "getlocal", "getutc", "gmt?", "gmt_offset", "gmtime", "gmtoff",
		"hash", "hour", "httpdate", "isdst", "iso8601", "localtime", "mday",
		"min", "mon", "monday?", "month", "nsec", "rfc2822", "rfc3339",
		"rfc822", "round", "saturday?", "sec", "strftime", "subsec", "sunday?",
		"thursday?", "tuesday?", "tv_nsec", "tv_usec", "usec", "utc", "utc?",
		"utc_offset", "wday", "wednesday?", "xmlschema", "yday", "year",
		"zone",
	},
}

// universalMemberContractExemptions lists the Object-level helpers from
// universalMemberNames that have no registered contract yet. They resolve
// on every receiver through the universal fallback in resolveMember.
var universalMemberContractExemptions = []string{
	"itself",
	"dup",
	"clone",
	"freeze",
	"send",
	"public_send",
	"tap",
	"yield_self",
}
