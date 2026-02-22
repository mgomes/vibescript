package vibes

import "context"

func newExecutionForCall(script *Script, ctx context.Context, root *Env, opts CallOptions) *Execution {
	return &Execution{
		engine:          script.engine,
		script:          script,
		ctx:             ctx,
		quota:           script.engine.config.StepQuota,
		memoryQuota:     script.engine.config.MemoryQuotaBytes,
		recursionCap:    script.engine.config.RecursionLimit,
		callStack:       make([]callFrame, 0, 8),
		root:            root,
		moduleLoadStack: make([]string, 0, 8),
		moduleStack:     make([]moduleContext, 0, 8),
		receiverStack:   make([]Value, 0, 8),
		envStack:        make([]*Env, 0, 8),
		strictEffects:   script.engine.config.StrictEffects,
		allowRequire:    opts.AllowRequire,
	}
}
