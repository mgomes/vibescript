// Package vibescript embeds repository documentation for tooling
// packages. go:embed cannot reference parent directories, so commands
// such as cmd/vibes access docs/ through this root-level package
// instead of carrying a second copy of the files.
package vibescript

import _ "embed"

// BuiltinsDoc is the contents of docs/builtins.md, the reference for
// every builtin function and namespace member available to scripts.
//
//go:embed docs/builtins.md
var BuiltinsDoc string

// StdlibDoc is the contents of docs/stdlib_core_utilities.md, the
// compact per-receiver-type reference for every builtin value member
// (string, array, hash, numeric, money, duration, time, symbol, range,
// and regex methods plus the universal Object-level helpers).
//
//go:embed docs/stdlib_core_utilities.md
var StdlibDoc string

// StringsDoc is the contents of docs/strings.md, the narrative string
// method guide; tooling parses it for members the compact reference
// does not list (for example the bang variants).
//
//go:embed docs/strings.md
var StringsDoc string

// ArraysDoc is the contents of docs/arrays.md, the narrative array
// method guide.
//
//go:embed docs/arrays.md
var ArraysDoc string

// HashesDoc is the contents of docs/hashes.md, the narrative hash
// method guide.
//
//go:embed docs/hashes.md
var HashesDoc string

// TimeDoc is the contents of docs/time.md, the narrative time guide.
//
//go:embed docs/time.md
var TimeDoc string

// DurationsDoc is the contents of docs/durations.md, the narrative
// duration method guide.
//
//go:embed docs/durations.md
var DurationsDoc string
