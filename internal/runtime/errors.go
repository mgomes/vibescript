package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes/source"
)

var _ error = (*RuntimeError)(nil)

// StackFrame represents a single entry in a runtime error's call stack.
type StackFrame struct {
	Function string
	Pos      Position
	// Source is the module path for module-backed frames. It is empty for
	// root scripts compiled directly by an embedder.
	Source string
}

// RuntimeError represents a Vibescript runtime error with a call stack and source context.
type RuntimeError struct {
	Type      string
	Message   string
	CodeFrame string
	Frames    []StackFrame

	// latchedExhaustion marks an error wrapped from the execution's genuine
	// budget exhaustion (see Execution.exhausted). It is unexported so
	// adapters constructing RuntimeErrors cannot forge it; only wrapError
	// sets it, and only when the error it wraps carries the latched value.
	latchedExhaustion bool
}

type assertionFailureError struct {
	message string
}

func (e *assertionFailureError) Error() string {
	return e.message
}

type typedRuntimeError struct {
	kind string
	err  error
}

type guardLimitError struct {
	err error
}

func (e *typedRuntimeError) Error() string {
	return e.err.Error()
}

func (e *typedRuntimeError) Unwrap() error {
	return e.err
}

func (e *guardLimitError) Error() string {
	return e.err.Error()
}

func (e *guardLimitError) Unwrap() error {
	return e.err
}

func (e *guardLimitError) LimitError() bool {
	return true
}

// privateMemberError marks a member-resolution failure that occurred because the
// member exists but is private to the receiver. It wraps the formatted runtime
// error so callers still surface the full "private method" message, while member
// dispatch can distinguish it from a genuine unknown-member miss via errors.As.
// The universal members rely on that distinction so a private override of
// itself/eql?/equal? still raises instead of falling through to the builtin.
type privateMemberError struct {
	err error
}

func (e *privateMemberError) Error() string { return e.err.Error() }

func (e *privateMemberError) Unwrap() error { return e.err }

// privateMemberAccess wraps a formatted "private method" runtime error so member
// resolution can recognize it as a privacy block rather than a missing member.
func privateMemberAccess(err error) error {
	return &privateMemberError{err: err}
}

// isPrivateMemberError reports whether err signals a member blocked by privacy,
// as opposed to a member that does not exist on the receiver at all.
func isPrivateMemberError(err error) bool {
	_, ok := errors.AsType[*privateMemberError](err)
	return ok
}

const (
	runtimeErrorTypeBase      = ast.RuntimeErrorTypeBase
	runtimeErrorTypeStandard  = ast.RuntimeErrorTypeStandard
	runtimeErrorTypeAssertion = ast.RuntimeErrorTypeAssertion
	runtimeErrorTypeLimit     = ast.RuntimeErrorTypeLimit
	runtimeErrorTypeType      = ast.RuntimeErrorTypeType
	runtimeErrorTypeZeroDiv   = ast.RuntimeErrorTypeZeroDiv
	runtimeErrorTypeLocalJump = ast.RuntimeErrorTypeLocalJump
	runtimeErrorTypeArgument  = ast.RuntimeErrorTypeArgument
	runtimeErrorFrameHead     = 8
	runtimeErrorFrameTail     = 8
	stepSlowPathMask          = 15
)

var (
	errLoopBreak           = errors.New("loop break")
	errLoopNext            = errors.New("loop next")
	errRescueRetry         = errors.New("rescue retry")
	errStepQuotaExceeded   = errors.New("step quota exceeded")
	errMemoryQuotaExceeded = errors.New("memory quota exceeded")
	errOutputLimitExceeded = errors.New("output limit exceeded")
)

type loopBreakError struct {
	value Value
}

type loopNextError struct {
	value Value
}

type functionReturnError struct {
	value Value
}

func (e *loopBreakError) Error() string {
	return errLoopBreak.Error()
}

func (e *loopBreakError) Unwrap() error {
	return errLoopBreak
}

func newLoopBreakValue(value Value) error {
	return &loopBreakError{value: value}
}

func loopBreakValue(err error) (Value, bool) {
	if breakErr, ok := errors.AsType[*loopBreakError](err); ok {
		return breakErr.value, true
	}
	return NewNil(), false
}

func (e *loopNextError) Error() string {
	return errLoopNext.Error()
}

func (e *loopNextError) Unwrap() error {
	return errLoopNext
}

func newLoopNextValue(value Value) error {
	return &loopNextError{value: value}
}

func loopNextValue(err error) (Value, bool) {
	if nextErr, ok := errors.AsType[*loopNextError](err); ok {
		return nextErr.value, true
	}
	return NewNil(), false
}

func (e *functionReturnError) Error() string {
	return "function return"
}

func newFunctionReturnValue(value Value) error {
	return &functionReturnError{value: value}
}

func functionReturnValue(err error) (Value, bool) {
	if err == nil {
		return NewNil(), false
	}
	if returnErr, ok := errors.AsType[*functionReturnError](err); ok {
		return returnErr.value, true
	}
	return NewNil(), false
}

// Error returns the error message with a code frame and formatted stack trace.
func (re *RuntimeError) Error() string {
	var b strings.Builder
	b.WriteString(re.Message)
	if re.CodeFrame != "" {
		b.WriteString("\n")
		b.WriteString(re.CodeFrame)
	}
	renderFrame := func(frame StackFrame) {
		if frame.Pos.Line > 0 && frame.Pos.Column > 0 {
			fmt.Fprintf(&b, "\n  at %s (%d:%d)", frame.Function, frame.Pos.Line, frame.Pos.Column)
		} else if frame.Pos.Line > 0 {
			fmt.Fprintf(&b, "\n  at %s (line %d)", frame.Function, frame.Pos.Line)
		} else {
			fmt.Fprintf(&b, "\n  at %s", frame.Function)
		}
	}

	if len(re.Frames) <= runtimeErrorFrameHead+runtimeErrorFrameTail {
		for _, frame := range re.Frames {
			renderFrame(frame)
		}
		return b.String()
	}

	for _, frame := range re.Frames[:runtimeErrorFrameHead] {
		renderFrame(frame)
	}
	omitted := len(re.Frames) - (runtimeErrorFrameHead + runtimeErrorFrameTail)
	fmt.Fprintf(&b, "\n  ... %d frames omitted ...", omitted)
	for _, frame := range re.Frames[len(re.Frames)-runtimeErrorFrameTail:] {
		renderFrame(frame)
	}

	return b.String()
}

// Unwrap returns nil to satisfy the error unwrapping interface.
// RuntimeError is a terminal error that wraps the original error message but not the error itself.
func (re *RuntimeError) Unwrap() error {
	return nil
}

func classifyRuntimeErrorType(err error) string {
	if err == nil {
		return runtimeErrorTypeBase
	}
	if errors.Is(err, errStepQuotaExceeded) || errors.Is(err, errMemoryQuotaExceeded) || errors.Is(err, errOutputLimitExceeded) {
		return runtimeErrorTypeLimit
	}
	// errors.As rather than AsType: the target is a method-set interface that
	// does not itself satisfy error, which AsType's constraint requires.
	var limitErr interface{ LimitError() bool }
	if errors.As(err, &limitErr) && limitErr.LimitError() {
		return runtimeErrorTypeLimit
	}
	if _, ok := errors.AsType[*assertionFailureError](err); ok {
		return runtimeErrorTypeAssertion
	}
	if typedErr, ok := errors.AsType[*typedRuntimeError](err); ok {
		if kind, known := ast.CanonicalRuntimeErrorType(typedErr.kind); known {
			return kind
		}
	}
	if runtimeErr, ok := errors.AsType[*RuntimeError](err); ok {
		if kind, known := ast.CanonicalRuntimeErrorType(runtimeErr.Type); known {
			return kind
		}
	}
	return runtimeErrorTypeBase
}

func newAssertionFailureError(message string) error {
	return &assertionFailureError{message: message}
}

func newTypedRuntimeError(kind string, err error) error {
	if err == nil {
		err = errors.New("")
	}
	return &typedRuntimeError{kind: kind, err: err}
}

func guardLimitErrorf(format string, args ...any) error {
	return &guardLimitError{err: fmt.Errorf(format, args...)}
}

func zeroDivisionErrorf(format string, args ...any) error {
	return newTypedRuntimeError(runtimeErrorTypeZeroDiv, fmt.Errorf(format, args...))
}

// latchExhaustion records a genuine budget-exhaustion error on the execution.
// The first error wins; step() re-raises it on every subsequent charge and
// rescue refuses to match anything while it is set (see canRescueRuntimeError),
// so quota exhaustion terminates the script no matter what absorbs the
// original error value.
func (exec *Execution) latchExhaustion(err error) error {
	if exec.exhausted == nil {
		exec.exhausted = err
	}
	return err
}

func (exec *Execution) step() error {
	if exec.exhausted != nil {
		return exec.exhausted
	}
	exec.steps++
	if exec.quota > 0 && exec.steps > exec.quota {
		return exec.latchExhaustion(fmt.Errorf("%w (%d)", errStepQuotaExceeded, exec.quota))
	}
	onSlowPath := (exec.steps & stepSlowPathMask) == 0
	if onSlowPath {
		// Inside an accumulator-metered section the periodic reachable-graph
		// walk is skipped: the section's build loop charges all of its
		// allocation before performing it and never re-enters script code, so
		// the walk would re-measure an unchanged graph (see
		// beginAccumulatorMeteredSection). The step quota above and the
		// context check below still run every period.
		if exec.memoryQuota > 0 && exec.accumMeteredSections == 0 {
			if err := exec.checkMemory(); err != nil {
				return err
			}
		}
	}
	if exec.ctx != nil && (exec.steps == 1 || onSlowPath) {
		if err := exec.checkContext(); err != nil {
			return err
		}
	}
	return nil
}

func (exec *Execution) checkContext() error {
	if exec.ctx == nil {
		return nil
	}
	select {
	case <-exec.ctx.Done():
		return exec.ctx.Err()
	default:
		return nil
	}
}

// stepN charges n steps at once, observing the same quota and running step's
// periodic slow-path checks a single time. Big-integer operations use it to
// scale their cost with operand size (one step per 8 words by convention)
// without paying n separate slow-path polls; the quota still trips at exactly
// the same total step count it would for n individual step calls.
func (exec *Execution) stepN(n int) error {
	if n > 1 {
		exec.steps += n - 1
	}
	return exec.step()
}

// checkStepBudgetFor reports whether at least n more steps may be charged
// without exhausting the step quota, and whether the context is still live. It
// is used by builtins that will charge one step per element of a known-size
// receiver: when the remaining quota cannot cover all n steps the per-element
// loop is guaranteed to fail, so rejecting up front lets such a builtin skip bulk
// work (for example sorting a hash's keys) that the loop would otherwise perform
// before the first step() fails. A non-positive n performs only the cancellation
// check, so callers can pass a receiver length directly even when it is zero
// without requiring an element step that the loop would never charge. Like step,
// the per-element charge still observes the quota and cancellation, so this is
// purely an early-out and never accepts a build the loop would reject.
func (exec *Execution) checkStepBudgetFor(n int) error {
	if exec.exhausted != nil {
		return exec.exhausted
	}
	if n < 0 {
		n = 0
	}
	if n > 0 && exec.quota > 0 && exec.quota-exec.steps < n {
		// The pre-flight latches like the per-element loop it stands in for:
		// it fires exactly when that loop was guaranteed to grind the quota
		// to zero, so the budget is as spent as if the loop had run.
		return exec.latchExhaustion(fmt.Errorf("%w (%d)", errStepQuotaExceeded, exec.quota))
	}
	return exec.checkContext()
}

func (exec *Execution) errorAt(pos Position, format string, args ...any) error {
	return exec.newRuntimeError(fmt.Sprintf(format, args...), pos)
}

func (exec *Execution) newRuntimeError(message string, pos Position) error {
	return exec.newRuntimeErrorWithType(runtimeErrorTypeBase, message, pos)
}

func (exec *Execution) argumentErrorAt(pos Position, format string, args ...any) error {
	return exec.newRuntimeErrorWithType(runtimeErrorTypeArgument, fmt.Sprintf(format, args...), pos)
}

func (exec *Execution) localJumpErrorAt(pos Position, format string, args ...any) error {
	return exec.newRuntimeErrorWithType(runtimeErrorTypeLocalJump, fmt.Sprintf(format, args...), pos)
}

func (exec *Execution) newRuntimeErrorWithType(kind, message string, pos Position) error {
	if canonical, ok := ast.CanonicalRuntimeErrorType(kind); ok {
		kind = canonical
	} else {
		kind = runtimeErrorTypeBase
	}

	frames := make([]StackFrame, 0, len(exec.callStack)+1)

	if len(exec.callStack) > 0 {
		// First frame: where the error occurred (within the current function)
		current := exec.callStack[len(exec.callStack)-1]
		frames = append(frames, StackFrame{Function: current.Function, Pos: pos, Source: stackFrameSource(current.functionScript)})

		// Remaining frames: the call stack (where each function was called from)
		for i := len(exec.callStack) - 1; i >= 0; i-- {
			cf := exec.callStack[i]
			frames = append(frames, StackFrame{Function: cf.Function, Pos: cf.Pos, Source: stackFrameSource(cf.callSiteScript)})
		}
	} else {
		// No call stack means error at script top level
		frames = append(frames, StackFrame{Function: "<script>", Pos: pos, Source: stackFrameSource(exec.currentSourceScript())})
	}
	codeFrame := ""
	sourceScript := exec.script
	if len(exec.callStack) > 0 && exec.callStack[len(exec.callStack)-1].functionScript != nil {
		sourceScript = exec.callStack[len(exec.callStack)-1].functionScript
	}
	if sourceScript != nil {
		codeFrame = source.FormatCodeFrame(sourceScript.source, pos)
	}
	return &RuntimeError{Type: kind, Message: message, CodeFrame: codeFrame, Frames: frames}
}

func stackFrameSource(script *Script) string {
	if script == nil {
		return ""
	}
	return script.modulePath
}

func (exec *Execution) wrapError(err error, pos Position) error {
	if err == nil {
		return nil
	}
	if isHostControlSignal(err) || isFunctionReturnSignal(err) || isRescueRetrySignal(err) {
		return err
	}
	if isNonLocalReturnSignal(err) {
		// A non-local return must reach its home invocation intact; flattening
		// it into a RuntimeError would lose the target token and make it
		// rescuable on the way.
		return err
	}
	if _, ok := errors.AsType[*RuntimeError](err); ok {
		return err
	}
	wrapped := exec.newRuntimeErrorWithType(classifyRuntimeErrorType(err), err.Error(), pos)
	if exec.exhausted != nil && errors.Is(err, exec.exhausted) {
		if re, ok := errors.AsType[*RuntimeError](wrapped); ok {
			re.latchedExhaustion = true
		}
	}
	return wrapped
}

// authenticatedExhaustionFrames returns the RuntimeError whose credential
// ties err to the execution's latched exhaustion, or nil. It walks the whole
// unwrap tree — including errors.Join and multi-%w aggregates, where an
// unrelated RuntimeError can sit on an earlier branch than the marked one —
// rather than taking the first RuntimeError errors.As would. The caller must
// treat only its location data (CodeFrame, Frames) as usable: the exported
// fields of a RuntimeError are mutable by any holder of the pointer and
// survive a shallow copy together with the unexported marker, so the
// authoritative class and message are always rebuilt from the latch itself.
func authenticatedExhaustionFrames(err error) *RuntimeError {
	queue := []error{err}
	for len(queue) > 0 {
		e := queue[0]
		queue = queue[1:]
		if e == nil {
			continue
		}
		if re, ok := e.(*RuntimeError); ok {
			if re.latchedExhaustion {
				return re
			}
			// RuntimeError.Unwrap returns nil by design; nothing below it.
			continue
		}
		switch u := e.(type) {
		case interface{ Unwrap() error }:
			queue = append(queue, u.Unwrap())
		case interface{ Unwrap() []error }:
			queue = append(queue, u.Unwrap()...)
		}
	}
	return nil
}

// canonicalExhaustionMessage extracts the underlying quota message from a
// latched exhaustion error. A task-boundary latch holds a wrapper whose
// Error() renders the worker's code frame and stack; surfacing that rendering
// as a RuntimeError Message — beside separately copied frames — printed every
// frame twice and made the programmatic message multiline.
func canonicalExhaustionMessage(exhausted error) string {
	if re, ok := errors.AsType[*RuntimeError](exhausted); ok {
		return re.Message
	}
	return exhausted.Error()
}

// errorCarriesGenuineExhaustion reports whether err carries an authenticated
// budget exhaustion from any execution: the raw quota sentinels, or a
// RuntimeError that wrapError marked while wrapping one. Task workers run
// under their own executions with their own latches, so a worker's genuine
// kill reaches the parent as a wrapped error rather than through the parent's
// latch — the rescue gate refuses it by this credential instead. Recursion
// caps, stdlib guards, and script-raised LimitErrors carry neither the
// sentinels nor the marker, so they stay rescuable.
func errorCarriesGenuineExhaustion(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errStepQuotaExceeded) || errors.Is(err, errMemoryQuotaExceeded) || errors.Is(err, errOutputLimitExceeded) {
		return true
	}
	return authenticatedExhaustionFrames(err) != nil
}
