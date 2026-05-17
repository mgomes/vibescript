// Package vibes is the embedder API for the Vibescript scripting
// language. Hosts compile a .vibe source into a Script and invoke its
// functions through an Engine:
//
//	engine, _ := vibes.NewEngine(vibes.Config{StepQuota: 50_000})
//	script, _ := engine.Compile(source)
//	result, _ := script.Call(
//	    ctx,
//	    "greet",
//	    []value.Value{value.NewString("world")},
//	    vibes.CallOptions{},
//	)
//
// Runtime values live in github.com/mgomes/vibescript/vibes/value.
// Host-provided capability contracts live under
// github.com/mgomes/vibescript/vibes/capability. This package provides
// the Engine/Script execution surface, capability adapter constructors,
// runtime errors, and the per-call Execution handle that builtins
// receive. Engine execution is bounded by Config (step and memory
// quotas, recursion limit, strict-effects mode, module allow/deny
// policies).
//
// See ../README.md for the language reference and
// ../docs/architecture.md for the runtime design.
package vibes
