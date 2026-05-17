package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Config controls interpreter execution bounds and enforcement modes.
type Config = runtime.Config

// Engine executes Vibescript programs with deterministic limits.
type Engine = runtime.Engine

// NewEngine constructs an Engine with sane defaults and registers built-ins.
func NewEngine(cfg Config) (*Engine, error) { return runtime.NewEngine(cfg) }

// MustNewEngine is like NewEngine but panics if cfg is invalid.
// Intended for package-level variable initialization and tests where
// invalid input is a programmer error and recovery is not meaningful.
// In production code prefer NewEngine and handle the error.
func MustNewEngine(cfg Config) *Engine { return runtime.MustNewEngine(cfg) }
