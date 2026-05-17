package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Script represents a parsed Vibescript module ready for execution.
type Script = runtime.Script

// CallOptions configures globals, capabilities, and other settings for a script invocation.
type CallOptions = runtime.CallOptions
