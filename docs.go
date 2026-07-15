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
