//go:build goexperiment.goroutineleakprofile

package runtime

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if code == 0 {
		if err := checkGoroutineLeakProfile(os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}
	os.Exit(code)
}

func checkGoroutineLeakProfile(out io.Writer) error {
	profile := pprof.Lookup("goroutineleak")
	if profile == nil {
		return fmt.Errorf("goroutineleak profile unavailable")
	}

	var buf bytes.Buffer
	if err := profile.WriteTo(&buf, 1); err != nil {
		return fmt.Errorf("write goroutineleak profile: %w", err)
	}
	if leaks := profile.Count(); leaks > 0 {
		if _, err := out.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("write goroutineleak profile output: %w", err)
		}
		return fmt.Errorf("goroutineleak profile found %d leaked goroutine(s)", leaks)
	}
	return nil
}
