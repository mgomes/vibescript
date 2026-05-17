package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// RuntimeError describes a script-level error raised during execution.
type RuntimeError = runtime.RuntimeError

// StackFrame describes a single frame in a RuntimeError stack trace.
type StackFrame = runtime.StackFrame
