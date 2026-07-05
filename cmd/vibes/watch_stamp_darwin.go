//go:build darwin

package main

import (
	"errors"
	"syscall"
	"time"
)

// stampWatchTarget stats path into a stack-allocated Stat_t, avoiding
// the per-call FileInfo allocations of os.Stat: the polling loop stamps
// every known file on each tick, so large module roots pay this cost
// even when nothing changed. It follows symlinks like os.Stat, and any
// error yields the zero stamp so a missing file's later reappearance
// registers as a change. The time.Unix construction mirrors os.Stat's,
// keeping stamps comparable with FileInfo-derived values.
func stampWatchTarget(path string) fileStamp {
	var st syscall.Stat_t
	for {
		err := syscall.Stat(path, &st)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EINTR) {
			return fileStamp{}
		}
	}
	return fileStamp{modTime: time.Unix(st.Mtimespec.Unix()), size: st.Size}
}
