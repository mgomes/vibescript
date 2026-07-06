package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Script represents a parsed Vibescript module ready for execution.
type Script = runtime.Script

// ParamKind identifies how a function parameter receives values.
type ParamKind = runtime.ParamKind

const (
	// ParamNormal is an ordinary positional parameter (def f(a)).
	ParamNormal = runtime.ParamNormal
	// ParamKeyword is a keyword parameter (def f(a:)), filled from
	// CallOptions.Keywords or a script-side keyword argument.
	ParamKeyword = runtime.ParamKeyword
	// ParamRest is a splat parameter (def f(*rest)) that collects the
	// remaining positional arguments into an array.
	ParamRest = runtime.ParamRest
	// ParamKeywordRest is a double-splat parameter (def f(**opts)) that
	// collects the remaining keyword arguments into a hash.
	ParamKeywordRest = runtime.ParamKeywordRest
	// ParamBlock is an explicit block parameter (def f(&blk)).
	ParamBlock = runtime.ParamBlock
)

// CallOptions configures globals, capabilities, and other settings for a script invocation.
type CallOptions = runtime.CallOptions
