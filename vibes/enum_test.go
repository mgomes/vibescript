package vibes

import (
	"context"
	"testing"
)

func TestEnumsProvideNominalValuesAndTypedCoercion(t *testing.T) {
	script := compileScript(t, `
enum Status
  Draft
  Published
end

enum ReviewState
  Draft
  Approved
end

def identity(status: Status) -> Status
  status
end

def typed_return_symbol() -> Status
  :draft
end

def typed_array(values: array<Status>) -> array<Status>
  values
end

def status_label(status: Status) -> string
  case status
  when Status::Draft
    "draft"
  when Status::Published
    "published"
  else
    "other"
  end
end

def render(status: Status) -> string
  "status={{value}}".template({ value: status })
end

def encode(status: Status) -> string
  JSON.stringify({ status: status })
end

def review_draft() -> ReviewState
  ReviewState::Draft
end

def facts()
  {
    same: Status::Draft == Status::Draft,
    symbol_same: Status::Draft == :draft,
    cross_enum_same: Status::Draft == ReviewState::Draft,
    name: Status::Draft.name,
    symbol: Status::Draft.symbol,
    enum_name: Status::Draft.enum
  }
end`)

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	statusPublished := enumTestValue(t, script, "Status", "Published")
	reviewDraft := enumTestValue(t, script, "ReviewState", "Draft")

	got := callFunc(t, script, "identity", []Value{NewSymbol("draft")})
	if !got.Equal(statusDraft) {
		t.Fatalf("expected symbol arg to coerce to Status::Draft, got %#v", got)
	}

	returned := callFunc(t, script, "typed_return_symbol", nil)
	if !returned.Equal(statusDraft) {
		t.Fatalf("expected typed return symbol to coerce, got %#v", returned)
	}

	arrayResult := callFunc(t, script, "typed_array", []Value{NewArray([]Value{NewSymbol("draft"), NewSymbol("published")})})
	compareArrays(t, arrayResult, []Value{statusDraft, statusPublished})

	label := callFunc(t, script, "status_label", []Value{NewSymbol("published")})
	if !label.Equal(NewString("published")) {
		t.Fatalf("expected published label, got %#v", label)
	}

	rendered := callFunc(t, script, "render", []Value{NewSymbol("draft")})
	if !rendered.Equal(NewString("status=draft")) {
		t.Fatalf("unexpected render output: %#v", rendered)
	}

	encoded := callFunc(t, script, "encode", []Value{NewSymbol("draft")})
	if !encoded.Equal(NewString(`{"status":"draft"}`)) {
		t.Fatalf("unexpected JSON output: %#v", encoded)
	}

	facts := callFunc(t, script, "facts", nil)
	if facts.Kind() != KindHash {
		t.Fatalf("expected hash, got %#v", facts)
	}
	factHash := facts.Hash()
	if !factHash["same"].Bool() {
		t.Fatalf("expected same enum member equality")
	}
	if factHash["symbol_same"].Bool() {
		t.Fatalf("enum value should not compare equal to raw symbol")
	}
	if factHash["cross_enum_same"].Bool() {
		t.Fatalf("different enums with same member name should not compare equal")
	}
	if !factHash["name"].Equal(NewString("Draft")) {
		t.Fatalf("unexpected enum member name: %#v", factHash["name"])
	}
	if !factHash["symbol"].Equal(NewSymbol("draft")) {
		t.Fatalf("unexpected enum member symbol: %#v", factHash["symbol"])
	}
	if !factHash["enum_name"].Equal(NewEnum(script.enums["Status"])) {
		t.Fatalf("unexpected enum owner: %#v", factHash["enum_name"])
	}

	reviewValue := callFunc(t, script, "review_draft", nil)
	if !reviewValue.Equal(reviewDraft) {
		t.Fatalf("unexpected review draft value: %#v", reviewValue)
	}

	requireCallErrorContains(t, script, "identity", []Value{reviewDraft}, CallOptions{}, "argument status expected Status, got ReviewState")
	requireCallErrorContains(t, script, "identity", []Value{NewSymbol("missing")}, CallOptions{}, "argument status expected Status, got symbol")
}

func TestEnumReturnTypeRejectsWrongEnum(t *testing.T) {
	script := compileScript(t, `
enum Status
  Draft
end

enum ReviewState
  Draft
end

def bad_return() -> Status
  ReviewState::Draft
end`)

	requireCallErrorContains(t, script, "bad_return", nil, CallOptions{}, "return value for bad_return expected Status, got ReviewState")
}

func TestNullableEnumTypesAcceptNilAndEnumValues(t *testing.T) {
	script := compileScript(t, `
enum Status
  Draft
  Published
end

def echo(status: Status?) -> Status?
  status
end
`)

	if got := callFunc(t, script, "echo", []Value{NewNil()}); got.Kind() != KindNil {
		t.Fatalf("expected nil echo result, got %#v", got)
	}

	statusDraft := enumTestValue(t, script, "Status", "Draft")
	got := callFunc(t, script, "echo", []Value{NewSymbol("draft")})
	if !got.Equal(statusDraft) {
		t.Fatalf("expected symbol arg to coerce to Status::Draft, got %#v", got)
	}

	got = callFunc(t, script, "echo", []Value{statusDraft})
	if !got.Equal(statusDraft) {
		t.Fatalf("expected enum arg to round-trip, got %#v", got)
	}
}

func TestLookupEnumInEnvSkipsNonEnumShadowBindings(t *testing.T) {
	enumDef, err := compileEnumDef(&EnumStmt{
		Name: "Status",
		Members: []EnumMemberStmt{
			{Name: "Draft"},
		},
	})
	if err != nil {
		t.Fatalf("compile enum: %v", err)
	}

	root := newEnv(nil)
	root.Define("Status", NewEnum(enumDef))

	shadow := newEnv(root)
	shadow.Define("Status", NewString("shadow"))

	got, ok := lookupEnumInEnv(shadow, "Status")
	if !ok {
		t.Fatalf("expected lookup to resolve parent enum")
	}
	if got != enumDef {
		t.Fatalf("expected parent enum def, got %#v", got)
	}
}

func TestEnumModuleExportsAndTypedCalls(t *testing.T) {
	engine := moduleTestEngine(t)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("enum_status")
  status = mod.Status
  first = Status::Draft
  second = status::Published
  third = mod.default_status()
  fourth = mod.normalize(:published)
  values = [first, second, third, fourth]
  values
end`)

	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}

	moduleEntry, err := engine.loadModule("enum_status", nil)
	if err != nil {
		t.Fatalf("load module: %v", err)
	}
	enumDef := moduleEntry.script.enums["Status"]
	compareArrays(t, result, []Value{
		NewEnumValue(enumDef.Members["Draft"]),
		NewEnumValue(enumDef.Members["Published"]),
		NewEnumValue(enumDef.Members["Draft"]),
		NewEnumValue(enumDef.Members["Published"]),
	})
}

func enumTestValue(t *testing.T, script *Script, enumName string, member string) Value {
	t.Helper()
	enumDef, ok := script.enums[enumName]
	if !ok {
		t.Fatalf("missing enum %s", enumName)
	}
	memberDef, ok := enumDef.Members[member]
	if !ok {
		t.Fatalf("missing enum member %s::%s", enumName, member)
	}
	return NewEnumValue(memberDef)
}
