package vibes

import (
	"context"
	"strings"
	"testing"
)

type dbCapabilityStub struct {
	findCalls    []DBFindRequest
	findCtx      []context.Context
	findResult   Value
	findErr      error
	queryCalls   []DBQueryRequest
	queryCtx     []context.Context
	queryResult  Value
	queryErr     error
	updateCalls  []DBUpdateRequest
	updateCtx    []context.Context
	updateResult Value
	updateErr    error
	sumCalls     []DBSumRequest
	sumCtx       []context.Context
	sumResult    Value
	sumErr       error
	eachCalls    []DBEachRequest
	eachCtx      []context.Context
	eachRows     []Value
	eachErr      error
}

func (s *dbCapabilityStub) Find(ctx context.Context, req DBFindRequest) (Value, error) {
	s.findCalls = append(s.findCalls, req)
	s.findCtx = append(s.findCtx, ctx)
	if s.findErr != nil {
		return NewNil(), s.findErr
	}
	if s.findResult.IsNil() {
		return NewNil(), nil
	}
	return s.findResult, nil
}

func (s *dbCapabilityStub) Query(ctx context.Context, req DBQueryRequest) (Value, error) {
	s.queryCalls = append(s.queryCalls, req)
	s.queryCtx = append(s.queryCtx, ctx)
	if s.queryErr != nil {
		return NewNil(), s.queryErr
	}
	if s.queryResult.IsNil() {
		return NewArray(nil), nil
	}
	return s.queryResult, nil
}

func (s *dbCapabilityStub) Update(ctx context.Context, req DBUpdateRequest) (Value, error) {
	s.updateCalls = append(s.updateCalls, req)
	s.updateCtx = append(s.updateCtx, ctx)
	if s.updateErr != nil {
		return NewNil(), s.updateErr
	}
	return s.updateResult, nil
}

func (s *dbCapabilityStub) Sum(ctx context.Context, req DBSumRequest) (Value, error) {
	s.sumCalls = append(s.sumCalls, req)
	s.sumCtx = append(s.sumCtx, ctx)
	if s.sumErr != nil {
		return NewNil(), s.sumErr
	}
	return s.sumResult, nil
}

func (s *dbCapabilityStub) Each(ctx context.Context, req DBEachRequest) ([]Value, error) {
	s.eachCalls = append(s.eachCalls, req)
	s.eachCtx = append(s.eachCtx, ctx)
	if s.eachErr != nil {
		return nil, s.eachErr
	}
	return append([]Value(nil), s.eachRows...), nil
}

func TestDBCapabilityFindAndContextPropagation(t *testing.T) {
	stub := &dbCapabilityStub{
		findResult: NewHash(map[string]Value{
			"id": NewString("player-7"),
		}),
	}
	script := compileScriptDefault(t, `def run(id)
  db.find("Player", id, include: "team")
end`)

	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("trace"), "enabled")
	result := callScript(t, ctx, script, "run", []Value{NewString("player-7")}, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	if result.Kind() != KindHash || result.Hash()["id"].String() != "player-7" {
		t.Fatalf("unexpected result: %#v", result)
	}

	if len(stub.findCalls) != 1 {
		t.Fatalf("expected 1 find call, got %d", len(stub.findCalls))
	}
	call := stub.findCalls[0]
	if call.Collection != "Player" {
		t.Fatalf("unexpected collection: %s", call.Collection)
	}
	if call.ID.Kind() != KindString || call.ID.String() != "player-7" {
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
	stub := &dbCapabilityStub{
		eachRows: []Value{
			NewHash(map[string]Value{"amount": NewInt(10)}),
			NewHash(map[string]Value{"amount": NewInt(15)}),
			NewHash(map[string]Value{"amount": NewInt(5)}),
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
		MustNewDBCapability("db", stub),
	))
	if result.Kind() != KindInt || result.Int() != 30 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(stub.eachCalls) != 1 {
		t.Fatalf("expected 1 each call, got %d", len(stub.eachCalls))
	}
	call := stub.eachCalls[0]
	if call.Collection != "ScoreEntry" {
		t.Fatalf("unexpected collection: %s", call.Collection)
	}
	if where := call.Options["where"]; where.Kind() != KindHash {
		t.Fatalf("expected where hash option, got %#v", where)
	}
}

func TestDBCapabilityEachLoopControlCannotCrossCallbackBoundary(t *testing.T) {
	stub := &dbCapabilityStub{
		eachRows: []Value{
			NewHash(map[string]Value{"id": NewString("p-1")}),
			NewHash(map[string]Value{"id": NewString("p-2")}),
		},
	}
	script := compileScriptDefault(t, `def break_from_callback()
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
end`)

	err := callScriptErr(t, context.Background(), script, "break_from_callback", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "break used outside of loop")

	err = callScriptErr(t, context.Background(), script, "next_from_callback", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "next used outside of loop")
}

func TestDBCapabilityRejectsCallableUpdateAttributes(t *testing.T) {
	stub := &dbCapabilityStub{}
	script := compileScriptDefault(t, `def helper(value)
  value
end

def run()
  db.update("Player", "p-1", { callback: helper })
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "db.update attributes must be data-only")
}

func TestDBCapabilityRejectsNonHashUpdateAttributes(t *testing.T) {
	stub := &dbCapabilityStub{}
	script := compileScriptDefault(t, `def run()
  db.update("Player", "p-1", 123)
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "db.update attributes expected hash, got int")
}

func TestDBCapabilityEachRequiresBlock(t *testing.T) {
	stub := &dbCapabilityStub{}
	script := compileScriptDefault(t, `def run()
  db.each("Player")
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "db.each requires a block")
}

func TestDBCapabilityRejectsCallableReturn(t *testing.T) {
	stub := &dbCapabilityStub{
		findResult: NewObject(map[string]Value{
			"save": NewBuiltin("leak.save", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
				return NewString("ok"), nil
			}),
		}),
	}
	script := compileScriptDefault(t, `def run()
  db.find("Player", "p-1")
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "db.find return value must be data-only")
}

func TestDBCapabilityRejectsCallableRows(t *testing.T) {
	stub := &dbCapabilityStub{
		eachRows: []Value{
			NewObject(map[string]Value{
				"run": NewBuiltin("row.run", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
					return NewNil(), nil
				}),
			}),
		},
	}
	script := compileScriptDefault(t, `def run()
  db.each("Player") do |row|
    row
  end
end`)

	err := callScriptErr(t, context.Background(), script, "run", nil, callOptionsWithCapabilities(
		MustNewDBCapability("db", stub),
	))
	requireErrorContains(t, err, "db.each row 0 must be data-only")
}

func TestDBCapabilityReturnsAreClonedFromHostState(t *testing.T) {
	stub := &dbCapabilityStub{
		findResult: NewHash(map[string]Value{
			"profile": NewHash(map[string]Value{
				"name": NewString("host"),
			}),
		}),
		queryResult: NewArray([]Value{
			NewHash(map[string]Value{
				"profile": NewHash(map[string]Value{
					"name": NewString("row-host"),
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
		MustNewDBCapability("db", stub),
	))

	findName := stub.findResult.Hash()["profile"].Hash()["name"]
	if findName.Kind() != KindString || findName.String() != "host" {
		t.Fatalf("find host result mutated by script: %#v", stub.findResult)
	}

	queryName := stub.queryResult.Array()[0].Hash()["profile"].Hash()["name"]
	if queryName.Kind() != KindString || queryName.String() != "row-host" {
		t.Fatalf("query host result mutated by script: %#v", stub.queryResult)
	}
}

func TestNewDBCapabilityRejectsInvalidArguments(t *testing.T) {
	stub := &dbCapabilityStub{}

	if _, err := NewDBCapability("", stub); err == nil || !strings.Contains(err.Error(), "name must be non-empty") {
		t.Fatalf("expected empty name error, got %v", err)
	}

	var db Database
	if _, err := NewDBCapability("db", db); err == nil || !strings.Contains(err.Error(), "requires a non-nil implementation") {
		t.Fatalf("expected nil db error, got %v", err)
	}

	var typedNil *dbCapabilityStub
	if _, err := NewDBCapability("db", typedNil); err == nil || !strings.Contains(err.Error(), "requires a non-nil implementation") {
		t.Fatalf("expected typed nil db error, got %v", err)
	}
}
