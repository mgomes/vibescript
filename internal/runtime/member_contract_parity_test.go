package runtime

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// The member contract registry says what each builtin member's contract is;
// this parity gate proves the checker and the live runtime both honor it.
// Every registered contract generates probes — call shapes inside and
// outside the contract, keyword and block acceptance, bare-read
// auto-invocation, and result kinds — and each probe runs through both
// checker analysis (CheckWarnings) and runtime dispatch (Script.Call) on a
// representative receiver. A probe whose outcome disagrees with the
// contract fails naming the member, the probe, and both observed sides, so
// a contract cannot drift from the behavior it claims to describe.

// parityReceiver is the representative receiver for one dispatch kind:
// either a literal spelling probed in place, or a typed probe parameter
// whose fixture value is passed through Script.Call.
type parityReceiver struct {
	paramType string
	literal   string
	fixture   Value
}

func parityReceivers(t *testing.T) map[string]parityReceiver {
	t.Helper()
	return map[string]parityReceiver{
		// The string fixture is numeric so the to_i/to_f conversion
		// contracts can be probed in-contract: the runtime rejects
		// non-numeric strings at execution time.
		"string":   {literal: `"12"`},
		"int":      {literal: "1"},
		"float":    {literal: "1.5"},
		"bool":     {literal: "true"},
		"nil":      {literal: "nil"},
		"symbol":   {literal: ":ok"},
		"array":    {literal: "[1, 2, 3]"},
		"hash":     {literal: "({a: 1})"},
		"money":    {paramType: "money", fixture: mustMoneyValue(t, "1.00 USD")},
		"duration": {paramType: "duration", fixture: NewDuration(durationFromSeconds(90))},
		"time":     {paramType: "time", fixture: NewTime(time.Unix(0, 0).UTC())},
		"range":    {paramType: "range", fixture: NewRange(Range{Start: 1, End: 3})},
		// Universal helpers answer on every value; an int exercises the
		// fallback because int owns none of the registered universal names.
		universalReceiverKind: {literal: "1"},
	}
}

// parityArguments lists a full-arity in-contract argument spelling per
// member that takes positional arguments. Probes slice it to the arity
// under test; the gate requires the list to match the contract's maxArgs.
// "Foo" references the probe preamble's empty class for the class
// predicates.
var parityArguments = map[string][]string{
	"array.at":               {"0"},
	"array.fetch":            {"0", "9"},
	"array.slice":            {"0", "1"},
	"string.slice":           {"0", "1"},
	"duration.eql?":          {"1"},
	"time.eql?":              {"1"},
	"universal.eql?":         {"1"},
	"universal.equal?":       {"1"},
	"universal.respond_to?":  {":to_s", "true"},
	"universal.is_type?":     {":int"},
	"universal.is_a?":        {"Foo"},
	"universal.kind_of?":     {"Foo"},
	"universal.instance_of?": {"Foo"},
}

// parityProbeExemptions lists probes that cannot assert parity
// generically, keyed "<receiver>.<member>/<probe>", each with the reason
// it is skipped. The gate fails on entries that match no generated probe,
// so an exemption cannot outlive its reason.
var parityProbeExemptions = map[string]string{}

// parityProbe is one generated behavior check: the probe body, whether the
// checker must warn, whether the runtime must error, and optionally the
// runtime result kind the contract promises.
type parityProbe struct {
	label      string
	body       string
	wantWarn   bool
	wantError  bool
	wantKind   ValueKind
	checkKind  bool
	needsClass bool
}

func TestMemberContractRuntimeParity(t *testing.T) {
	t.Parallel()

	receivers := parityReceivers(t)
	engine := MustNewEngine(Config{})
	used := make(map[string]struct{})
	for _, contract := range memberContracts {
		receiver, known := receivers[contract.receiver]
		if !known {
			t.Fatalf("no parity receiver fixture for kind %s", contract.receiver)
		}
		key := contract.receiver + "." + contract.name
		args := parityArguments[key]
		if !contract.valueMember && contract.call.maxArgs >= 0 && len(args) != contract.call.maxArgs {
			t.Fatalf("parityArguments[%s] has %d spellings, want maxArgs %d", key, len(args), contract.call.maxArgs)
		}
		for _, probe := range contractParityProbes(contract, receiver, args) {
			exemptKey := key + "/" + probe.label
			if _, exempt := parityProbeExemptions[exemptKey]; exempt {
				used[exemptKey] = struct{}{}
				continue
			}
			runParityProbe(t, engine, receiver, key, probe)
		}
	}
	for key := range parityProbeExemptions {
		if _, ok := used[key]; !ok {
			t.Errorf("parity exemption %s matches no generated probe; remove the stale entry", key)
		}
	}
}

// contractParityProbes generates the probe set for one contract. Value
// members probe the bare read (typed result) and the call form (the
// checker leaves the unregistered call unknown; the runtime rejects
// calling the returned value). Callable members probe a valid minimum-arity
// call, the bare read, arity outside the contract on both sides, keyword
// and literal-block handling in whichever direction the contract declares,
// rejection where the contract rejects blocks.
func contractParityProbes(contract memberContract, receiver parityReceiver, args []string) []parityProbe {
	recv := receiver.literal
	if receiver.paramType != "" {
		recv = "value"
	}
	member := recv + "." + contract.name
	call := func(spellings ...string) string {
		return member + "(" + strings.Join(spellings, ", ") + ")"
	}
	resultKind, checkKind := parityResultKind(contract.call.resultType)
	needsClass := slices.Contains(args, "Foo")

	if contract.valueMember {
		return []parityProbe{
			{label: "value-read", body: "r = " + member + "\n  r", wantKind: resultKind, checkKind: checkKind},
			{label: "value-call", body: call(), wantError: true},
		}
	}

	valid := args[:contract.call.minArgs]
	probes := []parityProbe{
		{label: "valid", body: call(valid...), wantKind: resultKind, checkKind: checkKind, needsClass: needsClass},
	}
	bare := parityProbe{label: "bare-read", body: "r = " + member + "\n  r"}
	switch {
	case !contract.call.autoInvoke:
		// Without auto-invoke a bare read used to yield the bound builtin;
		// callable values are removed, so the read itself is the error.
		bare.wantWarn, bare.wantError = true, true
	case contract.call.minArgs > 0:
		// Auto-invoking without the required arguments must fail on both
		// sides.
		bare.wantWarn, bare.wantError = true, true
	default:
		bare.wantKind, bare.checkKind = resultKind, checkKind
	}
	probes = append(probes, bare)
	if contract.call.minArgs > 0 {
		probes = append(probes, parityProbe{
			label: "arity-low", body: call(args[:contract.call.minArgs-1]...),
			wantWarn: true, wantError: true, needsClass: needsClass,
		})
	}
	if contract.call.maxArgs >= 0 {
		probes = append(probes, parityProbe{
			label: "arity-high", body: call(append(slices.Clone(args), "9")...),
			wantWarn: true, wantError: true, needsClass: needsClass,
		})
	}
	rejected := contract.call.rejectKeywords
	probes = append(probes, parityProbe{
		label: "keywords", body: call(append(slices.Clone(valid), "k: 1")...),
		wantWarn: rejected, wantError: rejected, needsClass: needsClass,
	})
	probes = append(probes, parityProbe{
		label: "block", body: call(valid...) + " { 1 }",
		wantWarn: contract.call.rejectBlock, wantError: contract.call.rejectBlock, needsClass: needsClass,
	})
	return probes
}

// parityResultKind maps a declared invariant result type to the runtime
// value kind a successful probe must produce.
func parityResultKind(ty *TypeExpr) (ValueKind, bool) {
	if ty == nil {
		return KindNil, false
	}
	switch ty.Kind {
	case TypeString:
		return KindString, true
	case TypeInt:
		return KindInt, true
	case TypeFloat:
		return KindFloat, true
	case TypeBool:
		return KindBool, true
	case TypeSymbol:
		return KindSymbol, true
	case TypeArray:
		return KindArray, true
	}
	return KindNil, false
}

func runParityProbe(t *testing.T, engine *Engine, receiver parityReceiver, key string, probe parityProbe) {
	t.Helper()

	params := ""
	var callArgs []Value
	if receiver.paramType != "" {
		params = "value: " + receiver.paramType
		callArgs = []Value{receiver.fixture}
	}
	preamble := ""
	if probe.needsClass {
		preamble = "class Foo\nend\n\n"
	}
	source := fmt.Sprintf("%sdef probe(%s)\n  %s\nend\n", preamble, params, probe.body)

	script, err := engine.Compile(source)
	if err != nil {
		t.Errorf("%s/%s: probe does not compile: %v\nsource:\n%s", key, probe.label, err, source)
		return
	}
	warnings := script.CheckWarnings()
	if probe.wantWarn != (len(warnings) > 0) {
		t.Errorf("%s/%s: checker disagreement: want warning %v, got %v\nsource:\n%s", key, probe.label, probe.wantWarn, checkWarningMessages(warnings), source)
	}
	result, err := script.Call(context.Background(), "probe", callArgs, CallOptions{})
	if probe.wantError != (err != nil) {
		t.Errorf("%s/%s: runtime disagreement: want error %v, got %v\nsource:\n%s", key, probe.label, probe.wantError, err, source)
		return
	}
	if err == nil && probe.checkKind && result.Kind() != probe.wantKind {
		t.Errorf("%s/%s: result disagreement: contract declares %v, runtime returned %v\nsource:\n%s", key, probe.label, probe.wantKind, result.Kind(), source)
	}
}

func checkWarningMessages(warnings []CheckWarning) []string {
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	return messages
}
