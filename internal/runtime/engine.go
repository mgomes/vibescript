package runtime

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxSourceBytes     = 1 << 20
	defaultTaskConcurrency    = 4
	defaultMaxTaskConcurrency = 64
)

// Config controls interpreter execution bounds and enforcement modes.
type Config struct {
	StepQuota              int
	MemoryQuotaBytes       int
	StrictEffects          bool
	RecursionLimit         int
	ModulePaths            []string
	ModuleAllowList        []string
	ModuleDenyList         []string
	RandomReader           io.Reader
	MaxCachedModules       int
	MaxSourceBytes         int
	DefaultTaskConcurrency int
	MaxTaskConcurrency     int
}

// Engine executes Vibescript programs with deterministic limits.
type Engine struct {
	config     Config
	builtins   map[string]Value
	builtinsMu sync.RWMutex
	modules    map[string]moduleEntry
	modPaths   []string
	modMu      sync.RWMutex
	randomMu   sync.Mutex

	// builtinProto is the frozen env shared as every call root's parent.
	// It holds the builtins whose values need no per-call cloning
	// (everything except array/hash/object builtins), so call setup
	// skips re-defining them. Rebuilt lazily after RegisterBuiltin.
	builtinProto   *Env
	clonedBuiltins int
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
	if cfg.MaxTaskConcurrency <= 0 {
		cfg.MaxTaskConcurrency = defaultMaxTaskConcurrency
	}
	if cfg.DefaultTaskConcurrency <= 0 {
		cfg.DefaultTaskConcurrency = defaultTaskConcurrencyForMax(cfg.MaxTaskConcurrency)
	}
	if cfg.DefaultTaskConcurrency > cfg.MaxTaskConcurrency {
		return nil, fmt.Errorf("vibes: default task concurrency cannot exceed max task concurrency")
	}
	if cfg.RandomReader == nil {
		cfg.RandomReader = cryptorand.Reader
	}

	modulePaths, err := normalizeModulePaths(cfg.ModulePaths)
	if err != nil {
		return nil, err
	}
	if err := validateModulePolicyPatterns(cfg.ModuleAllowList, "allow"); err != nil {
		return nil, err
	}
	if err := validateModulePolicyPatterns(cfg.ModuleDenyList, "deny"); err != nil {
		return nil, err
	}

	cfg.ModulePaths = modulePaths
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
	registerTaskBuiltins(engine)

	return engine, nil
}

func defaultTaskConcurrencyForMax(max int) int {
	if max < defaultTaskConcurrency {
		return max
	}
	return defaultTaskConcurrency
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

func normalizeModulePaths(paths []string) ([]string, error) {
	normalized := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, fmt.Errorf("vibes: module path cannot be empty")
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("vibes: invalid module path %q: %w", path, err)
		}
		stat, err := os.Stat(absPath)
		if err != nil {
			return nil, fmt.Errorf("vibes: invalid module path %q: %w", path, err)
		}
		if !stat.IsDir() {
			return nil, fmt.Errorf("vibes: module path %q is not a directory", path)
		}
		resolvedPath, err := filepath.EvalSymlinks(absPath)
		if err != nil {
			return nil, fmt.Errorf("vibes: invalid module path %q: %w", path, err)
		}
		normalized = append(normalized, filepath.Clean(resolvedPath))
	}
	return normalized, nil
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
	e.builtinProto = nil
}

// RegisterZeroArgBuiltin registers a builtin that can be invoked without arguments or parentheses.
func (e *Engine) RegisterZeroArgBuiltin(name string, fn BuiltinFunc) {
	e.builtinsMu.Lock()
	defer e.builtinsMu.Unlock()

	e.builtins[name] = NewAutoBuiltin(name, fn)
	e.builtinProto = nil
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

// callRootParent returns the engine's frozen builtin proto env, building
// it on first use and after builtin registration changes.
func (e *Engine) callRootParent() *Env {
	e.builtinsMu.RLock()
	proto := e.builtinProto
	e.builtinsMu.RUnlock()
	if proto != nil {
		return proto
	}

	e.builtinsMu.Lock()
	defer e.builtinsMu.Unlock()
	if e.builtinProto != nil {
		return e.builtinProto
	}
	proto = newEnv(nil)
	proto.growStatics(len(e.builtins))
	cloned := 0
	for name, builtin := range e.builtins {
		if builtinNeedsCallClone(builtin) {
			cloned++
			continue
		}
		proto.DefineStatic(name, builtin)
	}
	proto.frozen = true
	e.builtinProto = proto
	e.clonedBuiltins = cloned
	return proto
}

// clonedBuiltinCount reports how many builtins defineBuiltinsForCall will
// define per call, for pre-sizing the root statics map.
func (e *Engine) clonedBuiltinCount() int {
	e.builtinsMu.RLock()
	defer e.builtinsMu.RUnlock()
	return e.clonedBuiltins
}

// builtinNeedsCallClone reports whether a builtin value is mutable from
// scripts (arrays, hashes, object namespaces like JSON or Time) and must
// therefore be deep-cloned into each call root for isolation.
func builtinNeedsCallClone(val Value) bool {
	switch val.Kind() {
	case KindArray, KindHash, KindObject:
		return true
	default:
		return false
	}
}

func (e *Engine) defineBuiltinsForCall(root *Env) {
	e.builtinsMu.RLock()
	defer e.builtinsMu.RUnlock()

	for name, builtin := range e.builtins {
		if !builtinNeedsCallClone(builtin) {
			continue
		}
		root.DefineStatic(name, cloneBuiltinValueForCall(builtin))
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
		builtin := valueBuiltin(val)
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
	return fmt.Sprintf("steps=%d memory=%dB recursion=%d strict_effects=%t tasks=%d/%d", e.config.StepQuota, e.config.MemoryQuotaBytes, e.config.RecursionLimit, e.config.StrictEffects, e.config.DefaultTaskConcurrency, e.config.MaxTaskConcurrency)
}

func registerDataBuiltins(engine *Engine) {
	engine.builtins["JSON"] = NewObject(map[string]Value{
		"parse":     NewBuiltin("JSON.parse", builtinJSONParse),
		"stringify": NewBuiltin("JSON.stringify", builtinJSONStringify),
	})
	engine.builtins["Regex"] = NewObject(map[string]Value{
		"match":       NewBuiltin("Regex.match", builtinRegexMatch),
		"replace":     NewBuiltin("Regex.replace", builtinRegexReplace),
		"replace_all": NewBuiltin("Regex.replace_all", builtinRegexReplaceAll),
	})
}

func registerDurationBuiltins(engine *Engine) {
	engine.builtins["Duration"] = NewObject(map[string]Value{
		"build": NewBuiltin("Duration.build", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 1 && len(kwargs) == 0 {
				secs, err := numericToSeconds(args[0])
				if err != nil {
					return NewNil(), err
				}
				return NewDuration(durationFromSeconds(secs)), nil
			}
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("Duration.build accepts either seconds or named parts, not both")
			}
			if len(kwargs) == 0 {
				return NewNil(), fmt.Errorf("Duration.build expects seconds or named parts")
			}
			allowed := map[string]struct{}{
				"weeks":   {},
				"days":    {},
				"hours":   {},
				"minutes": {},
				"seconds": {},
			}
			for key := range kwargs {
				if _, ok := allowed[key]; !ok {
					return NewNil(), fmt.Errorf("Duration.build unknown part %q", key)
				}
			}

			parsePart := func(name string) (int64, error) {
				if v, ok := kwargs[name]; ok {
					return numericToSeconds(v)
				}
				return 0, nil
			}
			weeks, err := parsePart("weeks")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %w", "weeks", err)
			}
			days, err := parsePart("days")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %w", "days", err)
			}
			hours, err := parsePart("hours")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %w", "hours", err)
			}
			minutes, err := parsePart("minutes")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %w", "minutes", err)
			}
			seconds, err := parsePart("seconds")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %w", "seconds", err)
			}
			return NewDuration(durationFromParts(weeks, days, hours, minutes, seconds)), nil
		}),
		"parse": NewBuiltin("Duration.parse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("Duration.parse expects a duration string")
			}
			parsed, err := parseDurationString(args[0].String())
			if err != nil {
				return NewNil(), err
			}
			return NewDuration(parsed), nil
		}),
	})
}

func registerTimeBuiltins(engine *Engine) {
	engine.builtins["Time"] = NewObject(map[string]Value{
		"new": NewBuiltin("Time.new", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			loc := time.Local
			if zone, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(zone)
				if err != nil {
					return NewNil(), err
				}
				if parsed != nil {
					loc = parsed
				}
			}
			t, err := timeFromParts(args, loc)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"local": NewBuiltin("Time.local", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.Local)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"mktime": NewAutoBuiltin("Time.mktime", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.Local)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"utc": NewBuiltin("Time.utc", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.UTC)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"gm": NewAutoBuiltin("Time.gm", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.UTC)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"at": NewBuiltin("Time.at", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("Time.at expects seconds since epoch")
			}
			var loc *time.Location
			if in, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(in)
				if err != nil {
					return NewNil(), err
				}
				loc = parsed
			}
			t, err := timeFromEpoch(args[0], loc)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"now": NewAutoBuiltin("Time.now", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("Time.now does not take positional arguments")
			}
			loc := time.Local
			if in, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(in)
				if err != nil {
					return NewNil(), err
				}
				if parsed != nil {
					loc = parsed
				}
			}
			return NewTime(time.Now().In(loc)), nil
		}),
		"parse": NewBuiltin("Time.parse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 || args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("Time.parse expects a time string and optional layout")
			}
			for key := range kwargs {
				if key != "in" {
					return NewNil(), fmt.Errorf("Time.parse unknown keyword %q", key)
				}
			}

			layout := ""
			hasLayout := false
			if len(args) == 2 {
				if args[1].Kind() == KindString {
					layout = args[1].String()
					hasLayout = true
				} else if args[1].Kind() != KindNil {
					return NewNil(), fmt.Errorf("Time.parse layout must be string")
				}
			}

			var loc *time.Location
			if in, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(in)
				if err != nil {
					return NewNil(), err
				}
				loc = parsed
			}

			t, err := parseTimeString(args[0].String(), layout, hasLayout, loc)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
	})
}
