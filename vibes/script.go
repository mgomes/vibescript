package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Script represents a parsed Vibescript module ready for execution.
type Script = runtime.Script

// ParamKind identifies how a function parameter receives values.
type ParamKind = runtime.ParamKind

const (
	ParamNormal      = runtime.ParamNormal
	ParamKeyword     = runtime.ParamKeyword
	ParamRest        = runtime.ParamRest
	ParamKeywordRest = runtime.ParamKeywordRest
	ParamBlock       = runtime.ParamBlock
)

// CallOptions configures globals, capabilities, and other settings for a script invocation.
type CallOptions = runtime.CallOptions
