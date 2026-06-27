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
}

type assertionFailureError struct {
	message string
}

func (e *assertionFailureError) Error() string {
	return e.message
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
	var private *privateMemberError
	return errors.As(err, &private)
}

const (
	runtimeErrorTypeBase      = ast.RuntimeErrorTypeBase
	runtimeErrorTypeAssertion = ast.RuntimeErrorTypeAssertion
	runtimeErrorTypeLimit     = ast.RuntimeErrorTypeLimit
	runtimeErrorFrameHead     = 8
	runtimeErrorFrameTail     = 8
	stepSlowPathMask          = 15
)

var (
	errLoopBreak           = errors.New("loop break")
	errLoopNext            = errors.New("loop next")
	errStepQuotaExceeded   = errors.New("step quota exceeded")
	errMemoryQuotaExceeded = errors.New("memory quota exceeded")
)

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
	if errors.Is(err, errStepQuotaExceeded) || errors.Is(err, errMemoryQuotaExceeded) {
		return runtimeErrorTypeLimit
	}
	var assertionErr *assertionFailureError
	if errors.As(err, &assertionErr) {
		return runtimeErrorTypeAssertion
	}
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		if kind, known := ast.CanonicalRuntimeErrorType(runtimeErr.Type); known {
			return kind
		}
	}
	return runtimeErrorTypeBase
}

func newAssertionFailureError(message string) error {
	return &assertionFailureError{message: message}
}

func (exec *Execution) step() error {
	exec.steps++
	if exec.quota > 0 && exec.steps > exec.quota {
		return fmt.Errorf("%w (%d)", errStepQuotaExceeded, exec.quota)
	}
	onSlowPath := (exec.steps & stepSlowPathMask) == 0
	if onSlowPath {
		if exec.memoryQuota > 0 {
			if err := exec.checkMemory(); err != nil {
				return err
			}
		}
	}
	if exec.ctx != nil && (exec.steps == 1 || onSlowPath) {
		select {
		case <-exec.ctx.Done():
			return exec.ctx.Err()
		default:
		}
	}
	return nil
}

func (exec *Execution) errorAt(pos Position, format string, args ...any) error {
	return exec.newRuntimeError(fmt.Sprintf(format, args...), pos)
}

func (exec *Execution) newRuntimeError(message string, pos Position) error {
	return exec.newRuntimeErrorWithType(runtimeErrorTypeBase, message, pos)
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
	if isHostControlSignal(err) {
		return err
	}
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		return err
	}
	return exec.newRuntimeErrorWithType(classifyRuntimeErrorType(err), err.Error(), pos)
}
