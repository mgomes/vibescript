package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	defaultWatchInterval         = 300 * time.Millisecond
	defaultWatchFullScanInterval = 5 * time.Second
)

// fileStamp is the change signature for one watched file. Comparing
// snapshots of stamps detects edits, deletions, and newly added files
// without OS-specific notification APIs.
type fileStamp struct {
	modTime time.Time
	size    int64
}

type watchSnapshot map[string]fileStamp

// watchTargetState owns the current snapshot plus a scratch map so each
// rescan refills the previous scan's storage instead of allocating a
// fresh map for large module roots. The watch loop is single-goroutine
// (one select loop per mode), so the state needs no locking.
type watchTargetState struct {
	snapshot watchSnapshot
	scratch  watchSnapshot
}

// watchScript runs the script once, then re-runs it whenever the script
// file or any .vibe file in its module directories changes. Run failures
// are reported to status without ending the watch; the loop exits only
// when ctx is canceled.
func watchScript(ctx context.Context, inv runInvocation, interval time.Duration, out, status io.Writer) error {
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	state := &watchTargetState{snapshot: snapshotWatchTargets(inv)}
	fmt.Fprintf(status, "watching %d file(s); press ctrl-c to stop\n", len(state.snapshot))
	runWatched(ctx, inv, out, status)

	notifier, err := newWatchNotifier(inv)
	if err != nil {
		fmt.Fprintf(status, "filesystem notifications unavailable: %v; falling back to polling\n", err)
		return watchScriptPolling(ctx, inv, interval, state, out, status)
	}
	fallback, err := watchScriptNotifications(ctx, inv, interval, state, notifier, out, status)
	if err != nil {
		return err
	}
	if fallback {
		return watchScriptPolling(ctx, inv, interval, state, out, status)
	}
	return nil
}

func watchScriptNotifications(ctx context.Context, inv runInvocation, interval time.Duration, state *watchTargetState, notifier *watchNotifier, out, status io.Writer) (bool, error) {
	defer func() {
		_ = notifier.Close()
	}()
	rescanTicker := time.NewTicker(watchFullScanInterval(interval))
	defer rescanTicker.Stop()
	debounce := time.NewTimer(interval)
	if !debounce.Stop() {
		<-debounce.C
	}
	pendingChange := false
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(status, "watch stopped")
			return false, nil
		case event, ok := <-notifier.Events():
			if !ok {
				fmt.Fprintln(status, "filesystem notifications stopped; falling back to polling")
				return true, nil
			}
			if notifier.handleEvent(event) {
				pendingChange = true
				resetTimer(debounce, interval)
			}
		case err, ok := <-notifier.Errors():
			if ok && err != nil {
				fmt.Fprintf(status, "filesystem notification error: %v\n", err)
			}
		case <-debounce.C:
			if !pendingChange {
				continue
			}
			pendingChange = false
			rerunIfWatchTargetsChanged(ctx, inv, state, out, status)
		case <-rescanTicker.C:
			rerunIfWatchTargetsChanged(ctx, inv, state, out, status)
		}
	}
}

func watchScriptPolling(ctx context.Context, inv runInvocation, interval time.Duration, state *watchTargetState, out, status io.Writer) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	rescanTicker := time.NewTicker(watchFullScanInterval(interval))
	defer rescanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(status, "watch stopped")
			return nil
		case <-ticker.C:
			if !watchKnownSnapshotChanged(state.snapshot) {
				continue
			}
			rerunIfWatchTargetsChanged(ctx, inv, state, out, status)
		case <-rescanTicker.C:
			rerunIfWatchTargetsChanged(ctx, inv, state, out, status)
		}
	}
}

func runWatched(ctx context.Context, inv runInvocation, out, status io.Writer) {
	if err := executeScript(ctx, inv, out, status); err != nil {
		fmt.Fprintln(status, err)
	}
}

func rerunIfWatchTargetsChanged(ctx context.Context, inv runInvocation, state *watchTargetState, out, status io.Writer) {
	state.scratch = snapshotWatchTargetsInto(state.scratch, inv)
	if maps.Equal(state.snapshot, state.scratch) {
		return
	}
	state.snapshot, state.scratch = state.scratch, state.snapshot
	fmt.Fprintf(status, "change detected, re-running %s\n", filepath.Base(inv.scriptPath))
	runWatched(ctx, inv, out, status)
}

// snapshotWatchTargets stamps the script file plus every .vibe file under
// the module directories. The walk is recursive because require requests
// resolve nested paths (require "sub/helper") below each module root.
// Files that fail to stat (mid-save renames, dangling symlinks) get a
// zero stamp, so their later reappearance registers as a change and
// triggers a re-run.
func snapshotWatchTargets(inv runInvocation) watchSnapshot {
	return snapshotWatchTargetsInto(nil, inv)
}

// snapshotWatchTargetsInto refills dst (allocating it when nil) so
// steady-state rescans reuse the previous scan's map storage. WalkDir
// distinguishes files from directories by dirent type, so only .vibe
// entries pay a stat; stampWatchTarget follows symlinks the way the
// symlink branch of the old FileInfo walk did.
func snapshotWatchTargetsInto(dst watchSnapshot, inv runInvocation) watchSnapshot {
	if dst == nil {
		dst = watchSnapshot{}
	} else {
		clear(dst)
	}
	scriptPath := resolveWatchPath(inv.scriptPath)
	dst[scriptPath] = stampWatchTarget(scriptPath)
	for _, dir := range inv.moduleDirs {
		_ = filepath.WalkDir(resolveWatchPath(dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() || filepath.Ext(entry.Name()) != ".vibe" {
				return nil
			}
			dst[path] = stampWatchTarget(path)
			return nil
		})
	}
	return dst
}

func watchKnownSnapshotChanged(snapshot watchSnapshot) bool {
	for path, stamp := range snapshot {
		if stampWatchTarget(path) != stamp {
			return true
		}
	}
	return false
}

func watchFullScanInterval(interval time.Duration) time.Duration {
	fullScanInterval := interval * 20
	if fullScanInterval < interval {
		return defaultWatchFullScanInterval
	}
	if fullScanInterval < defaultWatchInterval {
		return defaultWatchInterval
	}
	if fullScanInterval > defaultWatchFullScanInterval {
		return defaultWatchFullScanInterval
	}
	return fullScanInterval
}

func resetTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

type watchNotifier struct {
	watcher *fsnotify.Watcher
	dirs    map[string]struct{}
}

func newWatchNotifier(inv runInvocation) (*watchNotifier, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	notifier := &watchNotifier{
		watcher: watcher,
		dirs:    make(map[string]struct{}),
	}
	for _, dir := range inv.moduleDirs {
		if err := notifier.watchTree(dir); err != nil {
			_ = watcher.Close()
			return nil, err
		}
	}
	return notifier, nil
}

func (n *watchNotifier) Events() <-chan fsnotify.Event {
	return n.watcher.Events
}

func (n *watchNotifier) Errors() <-chan error {
	return n.watcher.Errors
}

func (n *watchNotifier) Close() error {
	return n.watcher.Close()
}

func (n *watchNotifier) watchTree(root string) error {
	root = resolveWatchPath(root)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		return n.watchDir(path)
	})
}

func resolveWatchPath(path string) string {
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolvedPath)
	}
	return filepath.Clean(path)
}

func (n *watchNotifier) watchDir(path string) error {
	cleanPath := filepath.Clean(path)
	if _, ok := n.dirs[cleanPath]; ok {
		return nil
	}
	if err := n.watcher.Add(cleanPath); err != nil {
		return err
	}
	n.dirs[cleanPath] = struct{}{}
	return nil
}

func (n *watchNotifier) handleEvent(event fsnotify.Event) bool {
	if event.Name == "" {
		return false
	}
	cleanPath := filepath.Clean(event.Name)

	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(cleanPath); err == nil && info.IsDir() {
			_ = n.watchTree(cleanPath)
			return true
		}
	}

	if _, watchedDir := n.dirs[cleanPath]; watchedDir && (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
		delete(n.dirs, cleanPath)
		return true
	}

	if filepath.Ext(cleanPath) != ".vibe" {
		return false
	}
	return event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)
}
