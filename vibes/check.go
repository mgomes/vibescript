package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// CheckWarning is a statically detected contract issue reported by the
// checker (see Script.CheckWarnings and the docs/typing.md gradual-typing
// contract). It is the stable public name for the diagnostic type: Function
// names the containing function, Pos carries the source position, Message is
// the human-readable diagnostic, and Source is the file path of the required
// module the warning originates in (empty for the checked script itself).
//
// The type is an alias for the internal checker representation, so values
// returned by Script.CheckWarnings, CheckWarningsWithOptions, and
// CheckWarningsForFunction can be stored and named through this type without
// importing implementation packages.
type CheckWarning = runtime.CheckWarning
