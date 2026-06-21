package main

import (
	"fmt"
	"os"

	"github.com/mgomes/vibescript/vibes"
)

// readScriptSource reads a script file for compilation while enforcing the
// engine's source-size limit before the full file is loaded into memory. It
// stats the file first so oversized inputs are rejected without reading them,
// mirroring how the engine guards module loading. The returned error wording
// matches the engine's own source-size rejection for consistency across the
// CLI and module loader.
func readScriptSource(engine *vibes.Engine, path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if limit := engine.MaxSourceBytes(); limit > 0 && info.Size() > int64(limit) {
		return nil, fmt.Errorf("source exceeds maximum size (%d > %d bytes)", info.Size(), limit)
	}
	return os.ReadFile(path)
}
