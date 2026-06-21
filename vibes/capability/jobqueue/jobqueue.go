// Package jobqueue defines the host-facing contract for the job-queue
// capability that Vibescript exposes to scripts. The runtime wraps a
// *Capability with a script-visible adapter; embedders implement
// JobQueue (and optionally JobQueueWithRetry) to back the methods.
package jobqueue

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// JobQueue exposes queue functionality to scripts via strongly-typed adapters.
type JobQueue interface {
	Enqueue(ctx context.Context, job JobQueueJob) (value.Value, error)
}

// JobQueueWithRetry extends JobQueue with a retry operation.
type JobQueueWithRetry interface {
	JobQueue
	Retry(ctx context.Context, req JobQueueRetryRequest) (value.Value, error)
}

// JobQueueJob captures a job invocation from script code.
type JobQueueJob struct {
	Name    string
	Payload map[string]value.Value
	Options JobQueueEnqueueOptions
}

// JobQueueEnqueueOptions represents keyword arguments supplied to enqueue.
type JobQueueEnqueueOptions struct {
	Delay  *time.Duration
	Key    *string
	Kwargs map[string]value.Value
}

// JobQueueRetryRequest captures retry invocations.
type JobQueueRetryRequest struct {
	JobID   string
	Options map[string]value.Value
}

// Capability binds a host JobQueue implementation under a script-visible
// name. The vibes package wraps it in a CapabilityAdapter; embedders
// construct one via NewCapability.
type Capability struct {
	Name  string
	Queue JobQueue
	Retry JobQueueWithRetry
}

// NewCapability validates the inputs and returns a bound Capability. It
// returns an error when name is empty or when queue is a nil
// implementation (typed or untyped).
func NewCapability(name string, queue JobQueue) (*Capability, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: job queue capability name must be non-empty")
	}
	if isNilImpl(queue) {
		return nil, fmt.Errorf("vibes: job queue capability requires a non-nil implementation")
	}
	cap := &Capability{Name: name, Queue: queue}
	if retry, ok := queue.(JobQueueWithRetry); ok {
		cap.Retry = retry
	}
	return cap, nil
}

// MustNewCapability is the panicking variant of NewCapability.
func MustNewCapability(name string, queue JobQueue) *Capability {
	cap, err := NewCapability(name, queue)
	if err != nil {
		panic(err)
	}
	return cap
}

// HasRetry reports whether the bound implementation supports retry.
func (c *Capability) HasRetry() bool { return c.Retry != nil }

// ParseEnqueueOptions converts kwargs received from a script into a
// structured JobQueueEnqueueOptions value. It is the safe public entry
// point: every extra keyword is checked to be data-only (no callables)
// and acyclic before it is cloned into the returned options, so direct
// embedders cannot smuggle a runtime-only value into the host. The name
// is used for error messages so they line up with the script-visible
// capability name.
func ParseEnqueueOptions(name string, kwargs map[string]value.Value) (JobQueueEnqueueOptions, error) {
	return parseEnqueueOptions(name, kwargs, true)
}

// ParseEnqueueOptionsValidated is the fast path for callers that have
// already enforced the enqueue data-only contract on kwargs (for example
// the runtime adapter, which validates arguments against the capability
// contract before dispatching). It still parses and clones delay, key,
// and extra kwargs, but skips the redundant data-only/cycle walk so the
// option graph is not traversed twice.
func ParseEnqueueOptionsValidated(name string, kwargs map[string]value.Value) (JobQueueEnqueueOptions, error) {
	return parseEnqueueOptions(name, kwargs, false)
}

func parseEnqueueOptions(name string, kwargs map[string]value.Value, validate bool) (JobQueueEnqueueOptions, error) {
	if len(kwargs) == 0 {
		return JobQueueEnqueueOptions{}, nil
	}

	var delay *time.Duration
	var key *string
	extra := make(map[string]value.Value)

	for k, v := range kwargs {
		switch k {
		case "delay":
			d, err := valueToTimeDuration(name, v)
			if err != nil {
				return JobQueueEnqueueOptions{}, err
			}
			if d < 0 {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue delay must be non-negative", name)
			}
			delay = &d
		case "key":
			if v.Kind() != value.KindString {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be a string", name)
			}
			s := v.String()
			if s == "" {
				return JobQueueEnqueueOptions{}, fmt.Errorf("%s.enqueue key must be non-empty", name)
			}
			key = &s
		default:
			if validate {
				label := fmt.Sprintf("%s.enqueue keyword %s", name, k)
				if err := validateDataOnly(label, v); err != nil {
					return JobQueueEnqueueOptions{}, err
				}
			}
			extra[k] = deepCloneValue(v)
		}
	}

	opts := JobQueueEnqueueOptions{Delay: delay, Key: key}
	if len(extra) > 0 {
		opts.Kwargs = extra
	}
	return opts, nil
}

func valueToTimeDuration(name string, val value.Value) (time.Duration, error) {
	switch val.Kind() {
	case value.KindDuration:
		secs := val.Duration().Seconds()
		return time.Duration(secs) * time.Second, nil
	case value.KindInt, value.KindFloat:
		secs, err := value.ValueToInt64(val)
		if err != nil {
			return 0, err
		}
		return time.Duration(secs) * time.Second, nil
	default:
		return 0, fmt.Errorf("%s.enqueue delay must be duration or numeric seconds", name)
	}
}

// isNilImpl reports whether impl is an untyped or typed nil. It is
// duplicated here rather than imported from vibes to keep this package
// free of an import cycle.
func isNilImpl(impl any) bool {
	if impl == nil {
		return true
	}
	val := reflect.ValueOf(impl)
	switch val.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return val.IsNil()
	default:
		return false
	}
}

// deepCloneValue mirrors vibes' deepCloneValue for data-only kinds so
// option parsing can defensively clone hash arguments without reaching
// back into vibes. Runtime-only kinds (block, builtin, class, ...) are
// rejected by validateDataOnly before reaching this clone, so they are
// returned unchanged here rather than silently leaking.
func deepCloneValue(v value.Value) value.Value {
	switch v.Kind() {
	case value.KindArray:
		arr := v.Array()
		cloned := make([]value.Value, len(arr))
		for i, elem := range arr {
			cloned[i] = deepCloneValue(elem)
		}
		return value.NewArray(cloned)
	case value.KindHash:
		hash := v.Hash()
		cloned := make(map[string]value.Value, len(hash))
		for k, val := range hash {
			cloned[k] = deepCloneValue(val)
		}
		return value.NewHash(cloned)
	case value.KindObject:
		obj := v.Hash()
		cloned := make(map[string]value.Value, len(obj))
		for k, val := range obj {
			cloned[k] = deepCloneValue(val)
		}
		return value.NewObject(cloned)
	default:
		return v
	}
}

// validateDataOnly rejects values that embed callables or cyclic references.
// The jobqueue package inlines this check rather than depending on the parent
// vibes package: only data-shaped kinds (Array, Hash, Object) require
// traversal, so a self-contained scanner suffices. It mirrors the events
// package's validator so both carved capabilities enforce the same contract.
func validateDataOnly(label string, val value.Value) error {
	if newCallableScanner().containsCallable(val) {
		return fmt.Errorf("%s must be data-only", label)
	}
	if newCycleScanner().containsCycle(val) {
		return fmt.Errorf("%s must not contain cyclic references", label)
	}
	return nil
}

type callableScanner struct {
	seenArrays map[sliceID]struct{}
	seenMaps   map[uintptr]struct{}
}

func newCallableScanner() *callableScanner {
	return &callableScanner{
		seenArrays: make(map[sliceID]struct{}),
		seenMaps:   make(map[uintptr]struct{}),
	}
}

func (s *callableScanner) containsCallable(val value.Value) bool {
	switch val.Kind() {
	case value.KindFunction, value.KindBuiltin, value.KindBlock, value.KindClass, value.KindInstance:
		return true
	case value.KindArray:
		values := val.Array()
		id := identityOf(values)
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		s.seenArrays[id] = struct{}{}
		return slices.ContainsFunc(values, s.containsCallable)
	case value.KindHash, value.KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		s.seenMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCallable(item) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

type cycleScanner struct {
	visitingArrays map[sliceID]struct{}
	visitingMaps   map[uintptr]struct{}
	seenArrays     map[sliceID]struct{}
	seenMaps       map[uintptr]struct{}
}

func newCycleScanner() *cycleScanner {
	return &cycleScanner{
		visitingArrays: make(map[sliceID]struct{}),
		visitingMaps:   make(map[uintptr]struct{}),
		seenArrays:     make(map[sliceID]struct{}),
		seenMaps:       make(map[uintptr]struct{}),
	}
}

func (s *cycleScanner) containsCycle(val value.Value) bool {
	switch val.Kind() {
	case value.KindArray:
		values := val.Array()
		id := identityOf(values)
		if _, seen := s.seenArrays[id]; seen {
			return false
		}
		if _, visiting := s.visitingArrays[id]; visiting {
			return true
		}
		s.visitingArrays[id] = struct{}{}
		if slices.ContainsFunc(values, s.containsCycle) {
			return true
		}
		delete(s.visitingArrays, id)
		s.seenArrays[id] = struct{}{}
		return false
	case value.KindHash, value.KindObject:
		entries := val.Hash()
		ptr := reflect.ValueOf(entries).Pointer()
		if _, seen := s.seenMaps[ptr]; seen {
			return false
		}
		if _, visiting := s.visitingMaps[ptr]; visiting {
			return true
		}
		s.visitingMaps[ptr] = struct{}{}
		for _, item := range entries {
			if s.containsCycle(item) {
				return true
			}
		}
		delete(s.visitingMaps, ptr)
		s.seenMaps[ptr] = struct{}{}
		return false
	default:
		return false
	}
}

type sliceID struct {
	Ptr uintptr
	Len int
	Cap int
}

func identityOf(values []value.Value) sliceID {
	return sliceID{
		Ptr: reflect.ValueOf(values).Pointer(),
		Len: len(values),
		Cap: cap(values),
	}
}
