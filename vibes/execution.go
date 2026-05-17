package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Execution holds the runtime state for a single script evaluation.
// It is the per-call handle passed to builtin functions and capability
// adapters. Embedders should not rely on its internal shape; treat it
// as opaque and use the exported methods (Context, Step, CallBlock).
type Execution = runtime.Execution
