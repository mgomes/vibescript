package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// Config controls interpreter execution bounds and enforcement modes.
type Config = runtime.Config

// Engine executes Vibescript programs with deterministic limits.
type Engine = runtime.Engine

// Unlimited disables a quota when supplied as a Config quota value. It is
// distinct from a zero value, which selects the built-in default.
const Unlimited = runtime.Unlimited

// QuotaProfile is a named bundle of the step, memory, recursion, and maximum
// sleeping quotas. ApplyTo writes every one of them, so a host layering its own
// override on a profile must set it after applying, not before.
type QuotaProfile = runtime.QuotaProfile

// The named quota profiles, in ascending order of generosity. The lower rungs
// model a constrained sandbox budget; xhigh runs a script like a normal
// interpreter (unlimited steps and memory, a high but finite recursion cap).
var (
	ProfileLow    = runtime.ProfileLow
	ProfileMedium = runtime.ProfileMedium
	ProfileHigh   = runtime.ProfileHigh
	ProfileXHigh  = runtime.ProfileXHigh
)

// QuotaProfileByName returns the profile with the given name, matched
// case-insensitively, and reports whether one was found.
func QuotaProfileByName(name string) (QuotaProfile, bool) { return runtime.QuotaProfileByName(name) }

// QuotaProfileNames returns the profile names in ascending order of generosity.
func QuotaProfileNames() []string { return runtime.QuotaProfileNames() }

// NewEngine constructs an Engine with sane defaults and registers built-ins.
func NewEngine(cfg Config) (*Engine, error) { return runtime.NewEngine(cfg) }

// MustNewEngine is like NewEngine but panics if cfg is invalid.
// Intended for package-level variable initialization and tests where
// invalid input is a programmer error and recovery is not meaningful.
// In production code prefer NewEngine and handle the error.
func MustNewEngine(cfg Config) *Engine { return runtime.MustNewEngine(cfg) }
