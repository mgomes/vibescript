//go:build (aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris) && !illumos

package runtime

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadModuleSourceRejectsFIFOWithoutBlocking(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/pipe.vibe"
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	engine := MustNewEngine(Config{})

	done := make(chan error, 1)
	go func() {
		_, err := engine.readModuleSource(path)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("readModuleSource(FIFO) error = %v, want non-regular file", err)
		}
	case <-time.After(time.Second):
		t.Fatal("readModuleSource(FIFO) blocked")
	}
}
