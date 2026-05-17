package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Config controls interpreter execution bounds and enforcement modes.
type Config = runtime.Config

// Engine executes Vibescript programs with deterministic limits.
type Engine = runtime.Engine

// NewEngine constructs an Engine with sane defaults and registers built-ins.
func NewEngine(cfg Config) (*Engine, error) { return runtime.NewEngine(cfg) }

// MustNewEngine constructs an Engine and panics if the configuration is invalid.
func MustNewEngine(cfg Config) *Engine { return runtime.MustNewEngine(cfg) }
