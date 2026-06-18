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

// watchScript runs the script once, then re-runs it whenever the script
// file or any .vibe file in its module directories changes. Run failures
// are reported to status without ending the watch; the loop exits only
// when ctx is canceled.
func watchScript(ctx context.Context, inv runInvocation, interval time.Duration, out, status io.Writer) error {
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	snapshot := snapshotWatchTargets(inv)
	fmt.Fprintf(status, "watching %d file(s); press ctrl-c to stop\n", len(snapshot))
	runWatched(ctx, inv, out, status)

	notifier, err := newWatchNotifier(inv)
	if err != nil {
		fmt.Fprintf(status, "filesystem notifications unavailable: %v; falling back to polling\n", err)
		return watchScriptPolling(ctx, inv, interval, snapshot, out, status)
	}
	snapshot, fallback, err := watchScriptNotifications(ctx, inv, interval, snapshot, notifier, out, status)
	if err != nil {
		return err
	}
	if fallback {
		return watchScriptPolling(ctx, inv, interval, snapshot, out, status)
	}
	return nil
}

func watchScriptNotifications(ctx context.Context, inv runInvocation, interval time.Duration, snapshot watchSnapshot, notifier *watchNotifier, out, status io.Writer) (watchSnapshot, bool, error) {
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
			return snapshot, false, nil
		case event, ok := <-notifier.Events():
			if !ok {
				fmt.Fprintln(status, "filesystem notifications stopped; falling back to polling")
				return snapshot, true, nil
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
			rerunIfWatchTargetsChanged(ctx, inv, &snapshot, out, status)
		case <-rescanTicker.C:
			rerunIfWatchTargetsChanged(ctx, inv, &snapshot, out, status)
		}
	}
}

func watchScriptPolling(ctx context.Context, inv runInvocation, interval time.Duration, snapshot watchSnapshot, out, status io.Writer) error {
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
			if !watchKnownSnapshotChanged(snapshot) {
				continue
			}
			rerunIfWatchTargetsChanged(ctx, inv, &snapshot, out, status)
		case <-rescanTicker.C:
			rerunIfWatchTargetsChanged(ctx, inv, &snapshot, out, status)
		}
	}
}

func runWatched(ctx context.Context, inv runInvocation, out, status io.Writer) {
	if err := executeScript(ctx, inv, out); err != nil {
		fmt.Fprintln(status, err)
	}
}

func rerunIfWatchTargetsChanged(ctx context.Context, inv runInvocation, snapshot *watchSnapshot, out, status io.Writer) {
	current := snapshotWatchTargets(inv)
	if maps.Equal(*snapshot, current) {
		return
	}
	*snapshot = current
	fmt.Fprintf(status, "change detected, re-running %s\n", filepath.Base(inv.scriptPath))
	runWatched(ctx, inv, out, status)
}

// snapshotWatchTargets stamps the script file plus every .vibe file under
// the module directories. The walk is recursive because require requests
// resolve nested paths (require "sub/helper") below each module root.
// Files that fail to stat (mid-save renames, deletions) get a zero stamp,
// so their later reappearance registers as a change and triggers a re-run.
func snapshotWatchTargets(inv runInvocation) watchSnapshot {
	snapshot := watchSnapshot{}
	stamp := func(path string) {
		snapshot[path] = stampWatchTarget(path)
	}

	stamp(inv.scriptPath)
	for _, dir := range inv.moduleDirs {
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || filepath.Ext(entry.Name()) != ".vibe" {
				return nil
			}
			stamp(path)
			return nil
		})
	}
	return snapshot
}

func watchKnownSnapshotChanged(snapshot watchSnapshot) bool {
	for path, stamp := range snapshot {
		if stampWatchTarget(path) != stamp {
			return true
		}
	}
	return false
}

func stampWatchTarget(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}
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
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		return n.watchDir(path)
	})
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
