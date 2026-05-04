package vibes

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const defaultMaxSourceBytes = 1 << 20

// Config controls interpreter execution bounds and enforcement modes.
type Config struct {
	StepQuota        int
	MemoryQuotaBytes int
	StrictEffects    bool
	RecursionLimit   int
	ModulePaths      []string
	ModuleAllowList  []string
	ModuleDenyList   []string
	RandomReader     io.Reader
	MaxCachedModules int
	MaxSourceBytes   int
}

// Engine executes VibeScript programs with deterministic limits.
type Engine struct {
	config     Config
	builtins   map[string]Value
	builtinsMu sync.RWMutex
	modules    map[string]moduleEntry
	modPaths   []string
	modMu      sync.RWMutex
	randomMu   sync.Mutex
}

// NewEngine constructs an Engine with sane defaults and registers built-ins.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.StepQuota <= 0 {
		cfg.StepQuota = 50000
	}
	if cfg.MemoryQuotaBytes <= 0 {
		cfg.MemoryQuotaBytes = 64 * 1024
	}
	if cfg.RecursionLimit <= 0 {
		cfg.RecursionLimit = 64
	}
	if cfg.MaxCachedModules == 0 {
		cfg.MaxCachedModules = 1000
	}
	if cfg.MaxSourceBytes < 0 {
		return nil, fmt.Errorf("vibes: max source bytes cannot be negative")
	}
	if cfg.MaxSourceBytes == 0 {
		cfg.MaxSourceBytes = defaultMaxSourceBytes
	}
	if cfg.RandomReader == nil {
		cfg.RandomReader = cryptorand.Reader
	}

	if err := validateModulePaths(cfg.ModulePaths); err != nil {
		return nil, err
	}
	if err := validateModulePolicyPatterns(cfg.ModuleAllowList, "allow"); err != nil {
		return nil, err
	}
	if err := validateModulePolicyPatterns(cfg.ModuleDenyList, "deny"); err != nil {
		return nil, err
	}

	cfg.ModulePaths = append([]string(nil), cfg.ModulePaths...)
	cfg.ModuleAllowList = append([]string(nil), cfg.ModuleAllowList...)
	cfg.ModuleDenyList = append([]string(nil), cfg.ModuleDenyList...)

	engine := &Engine{
		config:   cfg,
		builtins: make(map[string]Value),
		modules:  make(map[string]moduleEntry),
		modPaths: append([]string(nil), cfg.ModulePaths...),
	}

	registerCoreBuiltins(engine)
	registerDataBuiltins(engine)
	registerDurationBuiltins(engine)
	registerTimeBuiltins(engine)

	return engine, nil
}

func (e *Engine) randomBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("random source failed: invalid byte request")
	}
	buf := make([]byte, n)
	e.randomMu.Lock()
	defer e.randomMu.Unlock()
	if _, err := io.ReadFull(e.config.RandomReader, buf); err != nil {
		return nil, fmt.Errorf("random source failed: %w", err)
	}
	return buf, nil
}

func validateModulePaths(paths []string) error {
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("vibes: module path cannot be empty")
		}
		stat, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("vibes: invalid module path %q: %w", path, err)
		}
		if !stat.IsDir() {
			return fmt.Errorf("vibes: module path %q is not a directory", path)
		}
	}
	return nil
}

// MustNewEngine constructs an Engine or panics if the config is invalid.
func MustNewEngine(cfg Config) *Engine {
	engine, err := NewEngine(cfg)
	if err != nil {
		panic(err)
	}
	return engine
}

// RegisterBuiltin registers a callable global available to scripts.
func (e *Engine) RegisterBuiltin(name string, fn BuiltinFunc) {
	e.builtinsMu.Lock()
	defer e.builtinsMu.Unlock()

	e.builtins[name] = NewBuiltin(name, fn)
}

// RegisterZeroArgBuiltin registers a builtin that can be invoked without arguments or parentheses.
func (e *Engine) RegisterZeroArgBuiltin(name string, fn BuiltinFunc) {
	e.builtinsMu.Lock()
	defer e.builtinsMu.Unlock()

	e.builtins[name] = NewAutoBuiltin(name, fn)
}

func registerCoreBuiltins(engine *Engine) {
	for _, builtin := range []struct {
		name       string
		fn         BuiltinFunc
		autoInvoke bool
	}{
		{name: "assert", fn: builtinAssert},
		{name: "money", fn: builtinMoney},
		{name: "money_cents", fn: builtinMoneyCents},
		{name: "require", fn: builtinRequire},
		{name: "now", fn: builtinNow, autoInvoke: true},
		{name: "uuid", fn: builtinUUID, autoInvoke: true},
		{name: "random_id", fn: builtinRandomID},
		{name: "to_int", fn: builtinToInt},
		{name: "to_float", fn: builtinToFloat},
	} {
		if builtin.autoInvoke {
			engine.RegisterZeroArgBuiltin(builtin.name, builtin.fn)
			continue
		}
		engine.RegisterBuiltin(builtin.name, builtin.fn)
	}
}

// Builtins returns a copy of the registered builtin map.
func (e *Engine) Builtins() map[string]Value {
	return e.builtinSnapshot()
}

func (e *Engine) builtinSnapshot() map[string]Value {
	e.builtinsMu.RLock()
	defer e.builtinsMu.RUnlock()

	out := make(map[string]Value, len(e.builtins))
	for name, builtin := range e.builtins {
		out[name] = cloneBuiltinValue(builtin)
	}
	return out
}

func (e *Engine) builtinCount() int {
	e.builtinsMu.RLock()
	defer e.builtinsMu.RUnlock()

	return len(e.builtins)
}

func (e *Engine) defineBuiltinsForCall(root *Env) {
	e.builtinsMu.RLock()
	defer e.builtinsMu.RUnlock()

	for name, builtin := range e.builtins {
		root.Define(name, cloneBuiltinValueForCall(builtin))
	}
}

func cloneBuiltinValueForCall(val Value) Value {
	switch val.Kind() {
	case KindArray:
		arr := val.Array()
		cloned := make([]Value, len(arr))
		for i, elem := range arr {
			cloned[i] = cloneBuiltinValueForCall(elem)
		}
		return NewArray(cloned)
	case KindHash:
		return NewHash(cloneBuiltinMapForCall(val.Hash()))
	case KindObject:
		return NewObject(cloneBuiltinMapForCall(val.Hash()))
	default:
		return val
	}
}

func cloneBuiltinMapForCall(src map[string]Value) map[string]Value {
	if src == nil {
		return nil
	}
	out := make(map[string]Value, len(src))
	for name, val := range src {
		out[name] = cloneBuiltinValueForCall(val)
	}
	return out
}

func cloneBuiltinValue(val Value) Value {
	switch val.Kind() {
	case KindBuiltin:
		builtin := val.Builtin()
		if builtin == nil {
			return val
		}
		return newBuiltin(builtin.Name, builtin.Fn, builtin.AutoInvoke)
	case KindArray:
		arr := val.Array()
		cloned := make([]Value, len(arr))
		for i, elem := range arr {
			cloned[i] = cloneBuiltinValue(elem)
		}
		return NewArray(cloned)
	case KindHash:
		return NewHash(cloneBuiltinMap(val.Hash()))
	case KindObject:
		return NewObject(cloneBuiltinMap(val.Hash()))
	default:
		return val
	}
}

func cloneBuiltinMap(src map[string]Value) map[string]Value {
	if src == nil {
		return nil
	}
	out := make(map[string]Value, len(src))
	for name, val := range src {
		out[name] = cloneBuiltinValue(val)
	}
	return out
}

// ClearModuleCache drops all cached modules and returns the number of entries removed.
// Long-running hosts can call this between script runs to force fresh module reloads.
func (e *Engine) ClearModuleCache() int {
	e.modMu.Lock()
	defer e.modMu.Unlock()

	count := len(e.modules)
	clear(e.modules)
	return count
}

// Execute compiles the provided source ensuring it is valid under current config.
func (e *Engine) Execute(ctx context.Context, script string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

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
	return fmt.Sprintf("steps=%d memory=%dB recursion=%d strict_effects=%t", e.config.StepQuota, e.config.MemoryQuotaBytes, e.config.RecursionLimit, e.config.StrictEffects)
}
