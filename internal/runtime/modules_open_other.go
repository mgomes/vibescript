//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package runtime

import "os"

func openModuleSource(path string) (*os.File, error) {
	return os.Open(path)
}
