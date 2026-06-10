package main

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"time"
)

const defaultWatchInterval = 300 * time.Millisecond

// fileStamp is the change signature for one watched file. Comparing
// snapshots of stamps detects edits, deletions, and newly added files
// without OS-specific notification APIs.
type fileStamp struct {
	modTime time.Time
	size    int64
}

type watchSnapshot map[string]fileStamp

// watchScript runs the script once, then re-runs it whenever the script
// file or any .vibe file in its module directories changes. Run failures
// are reported to status without ending the watch; the loop exits only
// when ctx is canceled.
func watchScript(ctx context.Context, inv runInvocation, interval time.Duration, out, status io.Writer) error {
	snapshot := snapshotWatchTargets(inv)
	fmt.Fprintf(status, "watching %d file(s); press ctrl-c to stop\n", len(snapshot))
	runWatched(ctx, inv, out, status)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(status, "watch stopped")
			return nil
		case <-ticker.C:
			current := snapshotWatchTargets(inv)
			if maps.Equal(snapshot, current) {
				continue
			}
			snapshot = current
			fmt.Fprintf(status, "change detected, re-running %s\n", filepath.Base(inv.scriptPath))
			runWatched(ctx, inv, out, status)
		}
	}
}

func runWatched(ctx context.Context, inv runInvocation, out, status io.Writer) {
	if err := executeScript(ctx, inv, out); err != nil {
		fmt.Fprintln(status, err)
	}
}

// snapshotWatchTargets stamps the script file plus every .vibe file in
// the module directories. Files that fail to stat (mid-save renames,
// deletions) get a zero stamp, so their later reappearance registers as
// a change and triggers a re-run.
func snapshotWatchTargets(inv runInvocation) watchSnapshot {
	snapshot := watchSnapshot{}
	stamp := func(path string) {
		info, err := os.Stat(path)
		if err != nil {
			snapshot[path] = fileStamp{}
			return
		}
		snapshot[path] = fileStamp{modTime: info.ModTime(), size: info.Size()}
	}

	stamp(inv.scriptPath)
	for _, dir := range inv.moduleDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".vibe" {
				continue
			}
			stamp(filepath.Join(dir, entry.Name()))
		}
	}
	return snapshot
}
