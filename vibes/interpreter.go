package vibes

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"sync"
	"time"
)

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
}

// Engine executes VibeScript programs with deterministic limits.
type Engine struct {
	config   Config
	builtins map[string]Value
	modules  map[string]moduleEntry
	modPaths []string
	modMu    sync.RWMutex
	randomMu sync.Mutex
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
	engine.RegisterZeroArgBuiltin("now", builtinNow)
	engine.RegisterZeroArgBuiltin("uuid", builtinUUID)
	engine.RegisterBuiltin("random_id", builtinRandomID)
	engine.builtins["JSON"] = NewObject(map[string]Value{
		"parse":     NewBuiltin("JSON.parse", builtinJSONParse),
		"stringify": NewBuiltin("JSON.stringify", builtinJSONStringify),
	})
	engine.builtins["Duration"] = NewObject(map[string]Value{
		"build": NewBuiltin("Duration.build", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 1 && len(kwargs) == 0 {
				secs, err := numericToSeconds(args[0])
				if err != nil {
					return NewNil(), err
				}
				return NewDuration(Duration{seconds: secs}), nil
			}
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("Duration.build accepts either seconds or named parts, not both") //nolint:staticcheck // class.method reference
			}
			if len(kwargs) == 0 {
				return NewNil(), fmt.Errorf("Duration.build expects seconds or named parts") //nolint:staticcheck // class.method reference
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
					return NewNil(), fmt.Errorf("Duration.build unknown part %q", key) //nolint:staticcheck // class.method reference
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
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "weeks", err) //nolint:staticcheck // class.method reference
			}
			days, err := parsePart("days")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "days", err) //nolint:staticcheck // class.method reference
			}
			hours, err := parsePart("hours")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "hours", err) //nolint:staticcheck // class.method reference
			}
			minutes, err := parsePart("minutes")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "minutes", err) //nolint:staticcheck // class.method reference
			}
			seconds, err := parsePart("seconds")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "seconds", err) //nolint:staticcheck // class.method reference
			}
			return NewDuration(durationFromParts(weeks, days, hours, minutes, seconds)), nil
		}),
		"parse": NewBuiltin("Duration.parse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("Duration.parse expects a duration string") //nolint:staticcheck // class.method reference
			}
			parsed, err := parseDurationString(args[0].String())
			if err != nil {
				return NewNil(), err
			}
			return NewDuration(parsed), nil
		}),
	})
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
				return NewNil(), fmt.Errorf("Time.at expects seconds since epoch") //nolint:staticcheck // class.method reference
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
				return NewNil(), fmt.Errorf("Time.now does not take positional arguments") //nolint:staticcheck // class.method reference
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
				return NewNil(), fmt.Errorf("Time.parse expects a time string and optional layout") //nolint:staticcheck // class.method reference
			}
			for key := range kwargs {
				if key != "in" {
					return NewNil(), fmt.Errorf("Time.parse unknown keyword %q", key) //nolint:staticcheck // class.method reference
				}
			}

			layout := ""
			hasLayout := false
			if len(args) == 2 {
				if args[1].Kind() == KindString {
					layout = args[1].String()
					hasLayout = true
				} else if args[1].Kind() != KindNil {
					return NewNil(), fmt.Errorf("Time.parse layout must be string") //nolint:staticcheck // class.method reference
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
	e.builtins[name] = NewBuiltin(name, fn)
}

// RegisterZeroArgBuiltin registers a builtin that can be invoked without arguments or parentheses.
func (e *Engine) RegisterZeroArgBuiltin(name string, fn BuiltinFunc) {
	e.builtins[name] = NewAutoBuiltin(name, fn)
}

// Builtins returns a copy of the registered builtin map.
func (e *Engine) Builtins() map[string]Value {
	out := make(map[string]Value, len(e.builtins))
	maps.Copy(out, e.builtins)
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
