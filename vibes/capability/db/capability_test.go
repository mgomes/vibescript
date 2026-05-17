package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/capability/db"
	"github.com/mgomes/vibescript/vibes/value"
)

type dbCapabilityStub struct {
	findCalls    []db.DBFindRequest
	findCtx      []context.Context
	findResult   value.Value
	findErr      error
	queryCalls   []db.DBQueryRequest
	queryCtx     []context.Context
	queryResult  value.Value
	queryErr     error
	updateCalls  []db.DBUpdateRequest
	updateCtx    []context.Context
	updateResult value.Value
	updateErr    error
	sumCalls     []db.DBSumRequest
	sumCtx       []context.Context
	sumResult    value.Value
	sumErr       error
	eachCalls    []db.DBEachRequest
	eachCtx      []context.Context
	eachRows     []value.Value
	eachErr      error
}

var _ db.Database = (*dbCapabilityStub)(nil)

func (s *dbCapabilityStub) Find(ctx context.Context, req db.DBFindRequest) (value.Value, error) {
	s.findCalls = append(s.findCalls, req)
	s.findCtx = append(s.findCtx, ctx)
	if s.findErr != nil {
		return value.NewNil(), s.findErr
	}
	if s.findResult.IsNil() {
		return value.NewNil(), nil
	}
	return s.findResult, nil
}

func (s *dbCapabilityStub) Query(ctx context.Context, req db.DBQueryRequest) (value.Value, error) {
	s.queryCalls = append(s.queryCalls, req)
	s.queryCtx = append(s.queryCtx, ctx)
	if s.queryErr != nil {
		return value.NewNil(), s.queryErr
	}
	if s.queryResult.IsNil() {
		return value.NewArray(nil), nil
	}
	return s.queryResult, nil
}

func (s *dbCapabilityStub) Update(ctx context.Context, req db.DBUpdateRequest) (value.Value, error) {
	s.updateCalls = append(s.updateCalls, req)
	s.updateCtx = append(s.updateCtx, ctx)
	if s.updateErr != nil {
		return value.NewNil(), s.updateErr
	}
	return s.updateResult, nil
}

func (s *dbCapabilityStub) Sum(ctx context.Context, req db.DBSumRequest) (value.Value, error) {
	s.sumCalls = append(s.sumCalls, req)
	s.sumCtx = append(s.sumCtx, ctx)
	if s.sumErr != nil {
		return value.NewNil(), s.sumErr
	}
	return s.sumResult, nil
}

func (s *dbCapabilityStub) Each(ctx context.Context, req db.DBEachRequest) ([]value.Value, error) {
	s.eachCalls = append(s.eachCalls, req)
	s.eachCtx = append(s.eachCtx, ctx)
	if s.eachErr != nil {
		return nil, s.eachErr
	}
	return append([]value.Value(nil), s.eachRows...), nil
}

func TestDBCapabilityFindAndContextPropagation(t *testing.T) {
	t.Parallel()
	stub := &dbCapabilityStub{
		findResult: value.NewHash(map[string]value.Value{
			"id": value.NewString("player-7"),
		}),
	}
	script := compileScriptDefault(t, `def run(id)
  db.find("Player", id, include: "team")
end`)

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("trace"), "enabled")
	result := callScript(t, ctx, script, "run", []value.Value{value.NewString("player-7")}, callOptionsWithCapabilities(
		vibes.MustNewDBCapability("db", stub),
	))
	if result.Kind() != value.KindHash || result.Hash()["id"].String() != "player-7" {
		t.Fatalf("unexpected result: %#v", result)
	}

	if len(stub.findCalls) != 1 {
		t.Fatalf("expected 1 find call, got %d", len(stub.findCalls))
	}
	call := stub.findCalls[0]
	if call.Collection != "Player" {
		t.Fatalf("unexpected collection: %s", call.Collection)
	}
	if call.ID.Kind() != value.KindString || call.ID.String() != "player-7" {
		t.Fatalf("unexpected id: %#v", call.ID)
	}
	if len(call.Options) != 1 || call.Options["include"].String() != "team" {
		t.Fatalf("unexpected options: %#v", call.Options)
	}
	if len(stub.findCtx) != 1 || stub.findCtx[0].Value(ctxKey("trace")) != "enabled" {
		t.Fatalf("context value not propagated")
	}
}

func TestDBCapabilityEachInvokesBlock(t *testing.T) {
	t.Parallel()
	stub := &dbCapabilityStub{
		eachRows: []value.Value{
			value.NewHash(map[string]value.Value{"amount": value.NewInt(10)}),
			value.NewHash(map[string]value.Value{"amount": value.NewInt(15)}),
			value.NewHash(map[string]value.Value{"amount": value.NewInt(5)}),
		},
	}
	script := compileScriptDefault(t, `def run()
  total = 0
  db.each("ScoreEntry", where: { player_id: "p-1" }) do |entry|
    total = total + entry[:amount]
  end
  total
end`)

	result := callScript(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		vibes.MustNewDBCapability("db", stub),
	))
	if result.Kind() != value.KindInt || result.Int() != 30 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(stub.eachCalls) != 1 {
		t.Fatalf("expected 1 each call, got %d", len(stub.eachCalls))
	}
	call := stub.eachCalls[0]
	if call.Collection != "ScoreEntry" {
		t.Fatalf("unexpected collection: %s", call.Collection)
	}
	if where := call.Options["where"]; where.Kind() != value.KindHash {
		t.Fatalf("expected where hash option, got %#v", where)
	}
}

func TestDBCapabilityEachLoopControlCannotCrossCallbackBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fn      string
		wantErr string
	}{
		{name: "break_from_callback", fn: "break_from_callback", wantErr: "break used outside of loop"},
		{name: "next_from_callback", fn: "next_from_callback", wantErr: "next used outside of loop"},
	}
	source := `def break_from_callback()
  db.each("Player") do |row|
    if row[:id] == "p-2"
      break
    end
  end
end

def next_from_callback()
  db.each("Player") do |row|
    if row[:id] == "p-2"
      next
    end
  end
end`

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &dbCapabilityStub{
				eachRows: []value.Value{
					value.NewHash(map[string]value.Value{"id": value.NewString("p-1")}),
					value.NewHash(map[string]value.Value{"id": value.NewString("p-2")}),
				},
			}
			script := compileScriptDefault(t, source)
			err := callScriptErr(t, context.Background(), script, tc.fn, nil, callOptionsWithCapabilities(
				vibes.MustNewDBCapability("db", stub),
			))
			requireErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestDBCapabilityRejectsInvalidScriptInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		source  string
		fn      string
		stub    *dbCapabilityStub
		wantErr string
	}{
		{
			name: "callable_update_attributes",
			source: `def helper(value)
  value
end

def run()
  db.update("Player", "p-1", { callback: helper })
end`,
			fn:      "run",
			stub:    &dbCapabilityStub{},
			wantErr: "db.update attributes must be data-only",
		},
		{
			name: "non_hash_update_attributes",
			source: `def run()
  db.update("Player", "p-1", 123)
end`,
			fn:      "run",
			stub:    &dbCapabilityStub{},
			wantErr: "db.update attributes expected hash, got int",
		},
		{
			name: "each_missing_block",
			source: `def run()
  db.each("Player")
end`,
			fn:      "run",
			stub:    &dbCapabilityStub{},
			wantErr: "db.each requires a block",
		},
		{
			name: "callable_find_return",
			source: `def run()
  db.find("Player", "p-1")
end`,
			fn: "run",
			stub: &dbCapabilityStub{
				findResult: value.NewObject(map[string]value.Value{
					"save": vibes.NewBuiltin("leak.save", func(exec *vibes.Execution, receiver value.Value, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
						return value.NewString("ok"), nil
					}),
				}),
			},
			wantErr: "db.find return value must be data-only",
		},
		{
			name: "callable_each_row",
			source: `def run()
  db.each("Player") do |row|
    row
  end
end`,
			fn: "run",
			stub: &dbCapabilityStub{
				eachRows: []value.Value{
					value.NewObject(map[string]value.Value{
						"run": vibes.NewBuiltin("row.run", func(exec *vibes.Execution, receiver value.Value, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
							return value.NewNil(), nil
						}),
					}),
				},
			},
			wantErr: "db.each row 0 must be data-only",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tc.source)
			err := callScriptErr(t, context.Background(), script, tc.fn, nil, callOptionsWithCapabilities(
				vibes.MustNewDBCapability("db", tc.stub),
			))
			requireErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestDBCapabilityReturnsAreClonedFromHostState(t *testing.T) {
	t.Parallel()
	stub := &dbCapabilityStub{
		findResult: value.NewHash(map[string]value.Value{
			"profile": value.NewHash(map[string]value.Value{
				"name": value.NewString("host"),
			}),
		}),
		queryResult: value.NewArray([]value.Value{
			value.NewHash(map[string]value.Value{
				"profile": value.NewHash(map[string]value.Value{
					"name": value.NewString("row-host"),
				}),
			}),
		}),
	}
	script := compileScriptDefault(t, `def run()
  player = db.find("Player", "p-1")
  player[:profile][:name] = "script"

  rows = db.query("Player")
  rows[0][:profile][:name] = "row-script"
end`)

	callScript(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		vibes.MustNewDBCapability("db", stub),
	))

	findName := stub.findResult.Hash()["profile"].Hash()["name"]
	if findName.Kind() != value.KindString || findName.String() != "host" {
		t.Fatalf("find host result mutated by script: %#v", stub.findResult)
	}

	queryName := stub.queryResult.Array()[0].Hash()["profile"].Hash()["name"]
	if queryName.Kind() != value.KindString || queryName.String() != "row-host" {
		t.Fatalf("query host result mutated by script: %#v", stub.queryResult)
	}
}

func TestNewDBCapabilityRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	stub := &dbCapabilityStub{}
	var nilImpl db.Database
	var typedNil *dbCapabilityStub

	tests := []struct {
		name    string
		capName string
		impl    db.Database
		wantErr string
	}{
		{name: "empty_name", capName: "", impl: stub, wantErr: "name must be non-empty"},
		{name: "nil_interface", capName: "db", impl: nilImpl, wantErr: "requires a non-nil implementation"},
		{name: "typed_nil", capName: "db", impl: typedNil, wantErr: "requires a non-nil implementation"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := vibes.NewDBCapability(tc.capName, tc.impl)
			requireErrorContains(t, err, tc.wantErr)
		})
	}
}

// Inline harness helpers — the in-package vibes test helpers cannot be
// imported from this external test, so this file mirrors the slice
// required to drive a script against a capability adapter.

func compileScriptDefault(t testing.TB, source string) *vibes.Script {
	t.Helper()
	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(source)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	return script
}

func callScript(t testing.TB, ctx context.Context, script *vibes.Script, fn string, args []value.Value, opts vibes.CallOptions) value.Value {
	t.Helper()
	result, err := script.Call(ctx, fn, args, opts)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	return result
}

func callScriptErr(t testing.TB, ctx context.Context, script *vibes.Script, fn string, args []value.Value, opts vibes.CallOptions) error {
	t.Helper()
	_, err := script.Call(ctx, fn, args, opts)
	if err == nil {
		t.Fatalf("expected call to fail")
	}
	return err
}

func callOptionsWithCapabilities(capabilities ...vibes.CapabilityAdapter) vibes.CallOptions {
	return vibes.CallOptions{Capabilities: capabilities}
}

func requireErrorContains(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if got := err.Error(); !strings.Contains(got, want) {
		t.Fatalf("unexpected error: %s", got)
	}
}
