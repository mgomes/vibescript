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
		"inspect", "string", "to_s",
	},
	"duration": {
		"after", "ago", "before", "between?", "day", "days", "eql?", "format",
		"from_now", "hour", "hours", "in_days", "in_hours", "in_minutes",
		"in_months", "in_seconds", "in_weeks", "in_years", "iso8601", "minute",
		"minutes", "parts", "second", "seconds", "since", "string", "to_i",
		"to_s", "until", "week", "weeks",
	},
	"float": {
		"abs", "between?", "ceil", "clamp", "div", "divmod", "fdiv", "finite?",
		"floor", "infinite?", "inspect", "modulo", "nan?", "negative?",
		"nonzero?", "positive?", "remainder", "round", "string", "to_f", "to_i",
		"to_s", "zero?",
	},
	"function": {
		"call",
	},
	"hash": {
		"clear", "compact", "deep_transform_keys", "default", "default_proc",
		"delete", "delete_if", "dig", "each", "each_key", "each_value",
		"each_with_index", "empty?", "except", "fetch", "fetch_values",
		"flatten", "has_key?", "has_value?", "include?", "inspect", "keep_if",
		"key?", "keys", "length", "map_with_index", "member?", "merge", "merge!",
		"reject", "remap_keys", "replace", "select", "size", "slice", "store",
		"to_a", "transform_keys", "transform_values", "update", "value?",
		"values", "values_at",
	},
	"int": {
		"abs", "between?", "ceil", "clamp", "day", "days", "div", "divmod",
		"downto", "even?", "fdiv", "floor", "hour", "hours", "inspect", "minute",
		"minutes", "modulo", "negative?", "next", "nonzero?", "odd?",
		"positive?", "pred", "remainder", "round", "second", "seconds", "step",
		"string", "succ", "times", "to_f", "to_i", "to_s", "upto", "week",
		"weeks", "zero?",
	},
	"money": {
		"amount", "between?", "cents", "currency", "format", "string", "to_s",
	},
	"nil": {
		"inspect", "string", "to_s",
	},
	"range": {
		"count", "cover?", "each", "exclude_end?", "find", "first", "include?",
		"last", "map", "max", "member?", "min", "reduce", "reject", "select",
		"size", "step", "sum", "to_a",
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
		"index", "insert", "inspect", "intern", "length", "lines", "ljust",
		"lstrip", "lstrip!", "match", "match?", "oct", "ord", "partition",
		"prepend", "replace", "reverse", "reverse!", "rindex", "rjust",
		"rpartition", "rstrip", "rstrip!", "scan", "size", "split", "squeeze",
		"squeeze!", "squish", "squish!", "start_with?", "string", "strip",
		"strip!", "sub", "sub!", "swapcase", "swapcase!", "template", "to_f",
		"to_i", "to_s", "to_sym", "tr", "tr!", "upcase", "upcase!",
	},
	"symbol": {
		"id2name", "inspect", "string", "to_s", "to_sym",
	},
	"time": {
		"<=>", "between?", "ceil", "day", "dst?", "eql?", "floor", "format",
		"friday?",
		"getgm", "getlocal", "getutc", "gmt?", "gmt_offset", "gmtime", "gmtoff",
		"hash", "hour", "httpdate", "isdst", "iso8601", "localtime", "mday",
		"min", "mon", "monday?", "month", "nsec", "rfc2822", "rfc3339", "rfc822",
		"round", "saturday?", "sec", "strftime", "string", "subsec", "sunday?",
		"thursday?", "to_a", "to_f", "to_i", "to_r", "to_s", "tuesday?",
		"tv_nsec", "tv_sec", "tv_usec", "usec", "utc", "utc?", "utc_offset",
		"wday", "wednesday?", "xmlschema", "yday", "year", "zone",
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
	"frozen?",
	"nil?",
	"eql?",
	"equal?",
	"send",
	"public_send",
	"tap",
	"yield_self",
	"respond_to?",
	"is_a?",
	"kind_of?",
	"instance_of?",
}
