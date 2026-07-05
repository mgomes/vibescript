//go:build !darwin && !linux

package main

import "os"

// stampWatchTarget is the portable fallback for platforms without a
// dedicated Stat_t shim. Any error yields the zero stamp so a missing
// file's later reappearance registers as a change.
func stampWatchTarget(path string) fileStamp {
	info, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{modTime: info.ModTime(), size: info.Size()}
}
