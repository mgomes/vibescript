package vibes

import "github.com/mgomes/vibescript/vibes/source"

// Position is the public source-location type exposed on RuntimeError
// and related public surfaces. It is an alias for source.Position so the
// AST (in internal/ast) and the public error surface share a single
// definition without forcing AST consumers to import vibes.
type Position = source.Position
