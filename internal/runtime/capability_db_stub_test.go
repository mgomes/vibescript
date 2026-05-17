package runtime

import "context"

// dbCapabilityStub is the shared in-package test double used by
// capability_foundations_test.go. The carved vibes/capability/db
// subpackage owns the production code path, but the foundation tests
// stay in this package because they cover cross-capability interaction
// alongside events, jobs, and context.
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

var _ Database = (*dbCapabilityStub)(nil)

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
