package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Script represents a parsed Vibescript module ready for execution.
type Script = runtime.Script

// ScriptFunction represents a user-defined function within a Vibescript module.
type ScriptFunction = runtime.ScriptFunction

// CallOptions configures globals, capabilities, and other settings for a script invocation.
type CallOptions = runtime.CallOptions
