package vibes

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type StackFrame struct {
	Function string
	Pos      Position
}

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

const (
	runtimeErrorTypeBase      = "RuntimeError"
	runtimeErrorTypeAssertion = "AssertionError"
	runtimeErrorFrameHead     = 8
	runtimeErrorFrameTail     = 8
)

var (
	errLoopBreak           = errors.New("loop break")
	errLoopNext            = errors.New("loop next")
	errStepQuotaExceeded   = errors.New("step quota exceeded")
	errMemoryQuotaExceeded = errors.New("memory quota exceeded")
	stringTemplatePattern  = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*\}\}`)
)

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

func canonicalRuntimeErrorType(name string) (string, bool) {
	switch {
	case strings.EqualFold(name, runtimeErrorTypeBase), strings.EqualFold(name, "Error"):
		return runtimeErrorTypeBase, true
	case strings.EqualFold(name, runtimeErrorTypeAssertion):
		return runtimeErrorTypeAssertion, true
	default:
		return "", false
	}
}

func classifyRuntimeErrorType(err error) string {
	if err == nil {
		return runtimeErrorTypeBase
	}
	var assertionErr *assertionFailureError
	if errors.As(err, &assertionErr) {
		return runtimeErrorTypeAssertion
	}
	if runtimeErr, ok := err.(*RuntimeError); ok {
		if kind, known := canonicalRuntimeErrorType(runtimeErr.Type); known {
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
	if exec.memoryQuota > 0 && (exec.steps&15) == 0 {
		if err := exec.checkMemory(); err != nil {
			return err
		}
	}
	if exec.ctx != nil {
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

func (exec *Execution) newRuntimeErrorWithType(kind string, message string, pos Position) error {
	if canonical, ok := canonicalRuntimeErrorType(kind); ok {
		kind = canonical
	} else {
		kind = runtimeErrorTypeBase
	}

	frames := make([]StackFrame, 0, len(exec.callStack)+1)

	if len(exec.callStack) > 0 {
		// First frame: where the error occurred (within the current function)
		current := exec.callStack[len(exec.callStack)-1]
		frames = append(frames, StackFrame{Function: current.Function, Pos: pos})

		// Remaining frames: the call stack (where each function was called from)
		for i := len(exec.callStack) - 1; i >= 0; i-- {
			cf := exec.callStack[i]
			frames = append(frames, StackFrame(cf))
		}
	} else {
		// No call stack means error at script top level
		frames = append(frames, StackFrame{Function: "<script>", Pos: pos})
	}
	codeFrame := ""
	if exec.script != nil {
		codeFrame = formatCodeFrame(exec.script.source, pos)
	}
	return &RuntimeError{Type: kind, Message: message, CodeFrame: codeFrame, Frames: frames}
}

func (exec *Execution) wrapError(err error, pos Position) error {
	if err == nil {
		return nil
	}
	if isHostControlSignal(err) {
		return err
	}
	if _, ok := err.(*RuntimeError); ok {
		return err
	}
	return exec.newRuntimeErrorWithType(classifyRuntimeErrorType(err), err.Error(), pos)
}
