package vibes

import (
	"context"
	"fmt"
	"os"
	"sync"
)

// Config controls interpreter execution bounds and enforcement modes.
type Config struct {
	StepQuota        int
	MemoryQuotaBytes int
	StrictEffects    bool
	ModulePaths      []string
	MaxCachedModules int
}

// Engine executes VibeScript programs with deterministic limits.
type Engine struct {
	config   Config
	builtins map[string]Value
	modules  map[string]moduleEntry
	modPaths []string
	modMu    sync.RWMutex
}

// NewEngine constructs an Engine with sane defaults and registers built-ins.
func NewEngine(cfg Config) *Engine {
	if cfg.StepQuota <= 0 {
		cfg.StepQuota = 50000
	}
	if cfg.MemoryQuotaBytes <= 0 {
		cfg.MemoryQuotaBytes = 64 * 1024
	}
	if cfg.MaxCachedModules == 0 {
		cfg.MaxCachedModules = 1000
	}

	for _, path := range cfg.ModulePaths {
		if stat, err := os.Stat(path); err != nil {
			panic(fmt.Sprintf("vibes: invalid module path %q: %v", path, err))
		} else if !stat.IsDir() {
			panic(fmt.Sprintf("vibes: module path %q is not a directory", path))
		}
	}

	engine := &Engine{
		config:   cfg,
		builtins: make(map[string]Value),
		modules:  make(map[string]moduleEntry),
		modPaths: append([]string(nil), cfg.ModulePaths...),
	}

	engine.RegisterBuiltin("assert", builtinAssert)
	engine.RegisterBuiltin("money", builtinMoney)
	engine.RegisterBuiltin("money_cents", builtinMoneyCents)
	engine.RegisterBuiltin("require", builtinRequire)
	engine.RegisterBuiltin("now", builtinNow)

	return engine
}

// RegisterBuiltin registers a callable global available to scripts.
func (e *Engine) RegisterBuiltin(name string, fn BuiltinFunc) {
	e.builtins[name] = NewBuiltin(name, fn)
}

// Builtins returns a copy of the registered builtin map.
func (e *Engine) Builtins() map[string]Value {
	out := make(map[string]Value, len(e.builtins))
	for k, v := range e.builtins {
		out[k] = v
	}
	return out
}

// Execute compiles the provided source ensuring it is valid under current config.
func (e *Engine) Execute(ctx context.Context, script string) error {
	_, err := e.Compile(script)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// ConfigSummary provides a human-readable description of the interpreter limits.
func (e *Engine) ConfigSummary() string {
	return fmt.Sprintf("steps=%d memory=%dB strict_effects=%t", e.config.StepQuota, e.config.MemoryQuotaBytes, e.config.StrictEffects)
}
