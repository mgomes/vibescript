// Package vibes is the embedder API for the Vibescript scripting
// language. Hosts compile a .vibe source into a Script and invoke its
// functions through an Engine:
//
//	engine, _ := vibes.NewEngine(vibes.Config{StepQuota: 50_000})
//	script, _ := engine.Compile(source)
//	result, _ := script.Call(
//	    ctx,
//	    "greet",
//	    []vibes.Value{vibes.NewString("world")},
//	    vibes.CallOptions{},
//	)
//
// The package exposes runtime values (Value, with kind-specific
// constructors and accessors), host-provided capability adapters
// (Database, EventPublisher, JobQueue, ContextCapabilityResolver) and
// the per-call Execution handle that builtins receive. Engine
// execution is bounded by Config (step and memory quotas, recursion
// limit, strict-effects mode, module allow/deny policies).
//
// See ../README.md for the language reference and
// ../docs/architecture.md for the runtime design.
package vibes
