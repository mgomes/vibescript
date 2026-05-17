package runtime

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/capability/contextcap"
	"github.com/mgomes/vibescript/vibes/capability/db"
	"github.com/mgomes/vibescript/vibes/capability/events"
	"github.com/mgomes/vibescript/vibes/capability/jobqueue"
)

// Internal aliases for jobqueue capability types so runtime code (and
// tests) can keep referring to short names that match the public vibes
// facade.
type (
	JobQueue               = jobqueue.JobQueue
	JobQueueWithRetry      = jobqueue.JobQueueWithRetry
	JobQueueJob            = jobqueue.JobQueueJob
	JobQueueEnqueueOptions = jobqueue.JobQueueEnqueueOptions
	JobQueueRetryRequest   = jobqueue.JobQueueRetryRequest
)

// jobQueueCapability adapts a *jobqueue.Capability into the
// CapabilityAdapter and CapabilityContractProvider interfaces consumed
// by the runtime. Bind exposes enqueue (and retry when supported) as
// builtin methods under the capability name.
type jobQueueCapability struct {
	inner *jobqueue.Capability
}

// NewJobQueueCapability constructs a CapabilityAdapter that delegates to a
// *jobqueue.Capability. It is the runtime-facing entry point used by the
// vibes facade.
func NewJobQueueCapability(name string, impl JobQueue) (CapabilityAdapter, error) {
	inner, err := jobqueue.NewCapability(name, impl)
	if err != nil {
		return nil, err
	}
	return &jobQueueCapability{inner: inner}, nil
}

// MustNewJobQueueCapability is the panicking variant of NewJobQueueCapability.
func MustNewJobQueueCapability(name string, impl JobQueue) CapabilityAdapter {
	cap, err := NewJobQueueCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return cap
}

func (c *jobQueueCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	name := c.inner.Name
	methods := map[string]Value{
		"enqueue": NewBuiltin(name+".enqueue", c.callEnqueue),
	}
	if c.inner.HasRetry() {
		methods["retry"] = NewBuiltin(name+".retry", c.callRetry)
	}
	return map[string]Value{name: NewObject(methods)}, nil
}

func (c *jobQueueCapability) callEnqueue(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	name := c.inner.Name
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("%s.enqueue expects job name and payload", name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s.enqueue does not accept blocks", name)
	}

	jobNameVal := args[0]
	switch jobNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return NewNil(), fmt.Errorf("%s.enqueue expects job name as string or symbol", name)
	}

	payloadVal := args[1]
	if payloadVal.Kind() != KindHash && payloadVal.Kind() != KindObject {
		return NewNil(), fmt.Errorf("%s.enqueue expects payload hash", name)
	}

	options, err := jobqueue.ParseEnqueueOptions(name, kwargs)
	if err != nil {
		return NewNil(), err
	}

	job := jobqueue.JobQueueJob{
		Name:    jobNameVal.String(),
		Payload: cloneHash(payloadVal.Hash()),
		Options: options,
	}

	result, err := c.inner.Queue.Enqueue(exec.Context(), job)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(name+".enqueue", result)
}

func (c *jobQueueCapability) callRetry(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	name := c.inner.Name
	if c.inner.Retry == nil {
		return NewNil(), fmt.Errorf("%s.retry is not supported", name)
	}
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s.retry expects job id and optional options hash", name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s.retry does not accept blocks", name)
	}

	idVal := args[0]
	if idVal.Kind() != KindString {
		return NewNil(), fmt.Errorf("%s.retry expects job id string", name)
	}

	options := make(map[string]Value)
	if len(args) > 1 {
		optsVal := args[1]
		if optsVal.Kind() != KindHash && optsVal.Kind() != KindObject {
			return NewNil(), fmt.Errorf("%s.retry options must be hash", name)
		}
		options = mergeHash(options, cloneHash(optsVal.Hash()))
	}
	options = mergeHash(options, cloneCapabilityKwargs(kwargs))

	req := jobqueue.JobQueueRetryRequest{JobID: idVal.String(), Options: options}
	result, err := c.inner.Retry.Retry(exec.Context(), req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(name+".retry", result)
}

func (c *jobQueueCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	name := c.inner.Name
	contracts := map[string]CapabilityMethodContract{
		name + ".enqueue": {
			ValidateArgs:   c.validateEnqueueContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(name + ".enqueue"),
		},
	}
	if c.inner.HasRetry() {
		contracts[name+".retry"] = CapabilityMethodContract{
			ValidateArgs:   c.validateRetryContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(name + ".retry"),
		}
	}
	return contracts
}

func (c *jobQueueCapability) validateEnqueueContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.inner.Name + ".enqueue"

	if len(args) != 2 {
		return fmt.Errorf("%s expects job name and payload", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}

	jobNameVal := args[0]
	switch jobNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return fmt.Errorf("%s expects job name as string or symbol", method)
	}

	if err := validateCapabilityHashValue(method+" payload", args[1]); err != nil {
		return err
	}

	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *jobQueueCapability) validateRetryContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.inner.Name + ".retry"

	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("%s expects job id and optional options hash", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}

	idVal := args[0]
	if idVal.Kind() != KindString {
		return fmt.Errorf("%s expects job id string", method)
	}

	if len(args) == 2 {
		if err := validateCapabilityHashValue(method+" options", args[1]); err != nil {
			return err
		}
	}

	return validateCapabilityKwargsDataOnly(method, kwargs)
}

// ContextCapabilityResolver is an internal alias for contextcap.Resolver
// so runtime code (and tests) can keep using the short name that matches
// the public vibes facade.
type ContextCapabilityResolver = contextcap.Resolver

// NewContextCapability constructs a data-only context capability adapter
// that bridges a contextcap.Resolver into the runtime CapabilityAdapter
// interface. The vibes facade re-exports this entry point under the same
// name.
func NewContextCapability(name string, resolver ContextCapabilityResolver) (CapabilityAdapter, error) {
	inner, err := contextcap.NewCapability(name, resolver)
	if err != nil {
		return nil, err
	}
	return &contextCapabilityAdapter{inner: inner}, nil
}

// MustNewContextCapability is the panicking variant of NewContextCapability.
func MustNewContextCapability(name string, resolver ContextCapabilityResolver) CapabilityAdapter {
	cap, err := NewContextCapability(name, resolver)
	if err != nil {
		panic(err)
	}
	return cap
}

// contextCapabilityAdapter bridges contextcap.Capability into vibes's
// CapabilityAdapter interface, which is anchored on the vibes-side
// CapabilityBinding type and cannot live in the carved subpackage.
type contextCapabilityAdapter struct {
	inner *contextcap.Capability
}

func (a *contextCapabilityAdapter) Bind(binding CapabilityBinding) (map[string]Value, error) {
	return a.inner.Bind(binding.Context)
}

// Internal aliases for db capability types so runtime code (and tests)
// can keep referring to short names that match the public vibes facade.
type (
	Database        = db.Database
	DatabaseReader  = db.DatabaseReader
	DatabaseWriter  = db.DatabaseWriter
	DBFindRequest   = db.DBFindRequest
	DBQueryRequest  = db.DBQueryRequest
	DBUpdateRequest = db.DBUpdateRequest
	DBSumRequest    = db.DBSumRequest
	DBEachRequest   = db.DBEachRequest
)

// NewDBCapability constructs a database capability adapter bound to the
// provided script-facing name. The vibes facade re-exports this entry
// point under the same name.
func NewDBCapability(name string, impl Database) (CapabilityAdapter, error) {
	cap, err := db.NewCapability(name, impl)
	if err != nil {
		return nil, err
	}
	return &dbCapabilityAdapter{cap: cap}, nil
}

// MustNewDBCapability is the panicking variant of NewDBCapability.
func MustNewDBCapability(name string, impl Database) CapabilityAdapter {
	cap, err := NewDBCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return cap
}

type dbCapabilityAdapter struct {
	cap *db.Capability
}

func (a *dbCapabilityAdapter) Bind(_ CapabilityBinding) (map[string]Value, error) {
	name := a.cap.Name()
	methods := map[string]Value{
		"find":   NewBuiltin(name+".find", a.wrapCall(a.cap.CallFind)),
		"query":  NewBuiltin(name+".query", a.wrapCall(a.cap.CallQuery)),
		"update": NewBuiltin(name+".update", a.wrapCall(a.cap.CallUpdate)),
		"sum":    NewBuiltin(name+".sum", a.wrapCall(a.cap.CallSum)),
		"each":   NewBuiltin(name+".each", a.wrapCall(a.cap.CallEach)),
	}
	return map[string]Value{name: NewObject(methods)}, nil
}

func (a *dbCapabilityAdapter) CapabilityContracts() map[string]CapabilityMethodContract {
	src := a.cap.Contracts()
	out := make(map[string]CapabilityMethodContract, len(src))
	for k, v := range src {
		out[k] = CapabilityMethodContract{
			ValidateArgs:   v.ValidateArgs,
			ValidateReturn: v.ValidateReturn,
		}
	}
	return out
}

func (a *dbCapabilityAdapter) wrapCall(fn func(db.ExecutionContext, []Value, map[string]Value, Value) (Value, error)) BuiltinFunc {
	return func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return fn(exec, args, kwargs, block)
	}
}

// Internal aliases for events capability types so runtime code (and tests)
// can keep referring to short names that match the public vibes facade.
type (
	EventPublisher      = events.Publisher
	EventPublishRequest = events.PublishRequest
)

// NewEventsCapability constructs a CapabilityAdapter that delegates to a
// *events.Capability. The vibes facade re-exports this entry point under
// the same name.
func NewEventsCapability(name string, publisher EventPublisher) (CapabilityAdapter, error) {
	inner, err := events.NewCapability(name, publisher)
	if err != nil {
		return nil, err
	}
	return &eventsCapability{inner: inner}, nil
}

// MustNewEventsCapability is the panicking variant of NewEventsCapability.
func MustNewEventsCapability(name string, publisher EventPublisher) CapabilityAdapter {
	cap, err := NewEventsCapability(name, publisher)
	if err != nil {
		panic(err)
	}
	return cap
}

// eventsCapability bridges an *events.Capability into the runtime by
// implementing CapabilityAdapter and CapabilityContractProvider.
type eventsCapability struct {
	inner *events.Capability
}

func (c *eventsCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	method := c.inner.PublishMethodName()
	return map[string]CapabilityMethodContract{
		method: {
			ValidateArgs:   c.validatePublishArgs,
			ValidateReturn: c.inner.ValidatePublishReturn,
		},
	}
}

func (c *eventsCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	methods := map[string]Value{
		"publish": NewBuiltin(c.inner.PublishMethodName(), c.callPublish),
	}
	return map[string]Value{c.inner.Name: NewObject(methods)}, nil
}

func (c *eventsCapability) callPublish(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	result, err := c.inner.Publish(exec.Context(), args, kwargs, !block.IsNil())
	if err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (c *eventsCapability) validatePublishArgs(args []Value, kwargs map[string]Value, block Value) error {
	return c.inner.ValidatePublishArgs(args, kwargs, !block.IsNil())
}

var (
	_ CapabilityAdapter = (*contextCapabilityAdapter)(nil)
	_ CapabilityAdapter = (*dbCapabilityAdapter)(nil)
	_ CapabilityAdapter = (*eventsCapability)(nil)
	_ CapabilityAdapter = (*jobQueueCapability)(nil)
)

var (
	_ CapabilityContractProvider = (*dbCapabilityAdapter)(nil)
	_ CapabilityContractProvider = (*eventsCapability)(nil)
	_ CapabilityContractProvider = (*jobQueueCapability)(nil)
)
