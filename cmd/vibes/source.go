package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mgomes/vibescript/vibes"
)

// readScriptSource reads a script file for compilation while enforcing the
// engine's source-size limit before the full file is loaded into memory. The
// returned error wording matches the engine's own source-size rejection for
// consistency across the CLI and module loader.
//
// The file is opened once and both inspected and read through that single
// descriptor: a path-based stat followed by a separate read is racy, because
// the file could be replaced or grown between the two operations and the read
// would still load the new, oversized contents. The read is additionally
// bounded at the limit plus one byte so a file that grows after the stat
// cannot defeat the guard.
func readScriptSource(engine *vibes.Engine, path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}

	limit := engine.MaxSourceBytes()
	if limit > 0 && info.Size() > int64(limit) {
		return nil, fmt.Errorf("source exceeds maximum size (%d > %d bytes)", info.Size(), limit)
	}
	if limit <= 0 {
		return io.ReadAll(f)
	}

	// Read at most limit+1 bytes from the descriptor we statted so a file that
	// is replaced or grown after the stat cannot make us allocate more than the
	// limit; the extra byte distinguishes an at-limit file from an over-limit
	// one without loading the whole oversized file.
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("source exceeds maximum size (> %d bytes)", limit)
	}
	return data, nil
}
