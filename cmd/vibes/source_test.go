package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgomes/vibescript/vibes"
)

func TestReadScriptSource(t *testing.T) {
	t.Parallel()

	const limit = 16
	tests := []struct {
		name     string
		size     int
		wantErr  string
		wantRead bool
	}{
		{
			name:     "under_limit",
			size:     limit - 1,
			wantRead: true,
		},
		{
			name:     "at_limit",
			size:     limit,
			wantRead: true,
		},
		{
			name:    "over_limit",
			size:    limit + 1,
			wantErr: "source exceeds maximum size (17 > 16 bytes)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := vibes.MustNewEngine(vibes.Config{MaxSourceBytes: limit})
			path := filepath.Join(t.TempDir(), "script.vibe")
			if err := os.WriteFile(path, []byte(strings.Repeat("a", tc.size)), 0o644); err != nil {
				t.Fatalf("write script: %v", err)
			}

			got, err := readScriptSource(engine, path)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("readScriptSource() err = nil, want %q", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("readScriptSource() err = %q, want %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("readScriptSource() err = %v, want nil", err)
			}
			if tc.wantRead && len(got) != tc.size {
				t.Fatalf("readScriptSource() read %d bytes, want %d", len(got), tc.size)
			}
		})
	}
}

func TestReadScriptSourceRejectsNonRegularFile(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	dir := t.TempDir()

	_, err := readScriptSource(engine, dir)
	if err == nil {
		t.Fatalf("readScriptSource(dir) err = nil, want non-regular file error")
	}
	if want := "is not a regular file"; !strings.Contains(err.Error(), want) {
		t.Fatalf("readScriptSource(dir) err = %v, want substring %q", err, want)
	}
}

func TestReadScriptSourceMissingFile(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	path := filepath.Join(t.TempDir(), "absent.vibe")

	_, err := readScriptSource(engine, path)
	if err == nil {
		t.Fatalf("readScriptSource(missing) err = nil, want stat error")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("readScriptSource(missing) err = %v, want not-exist error", err)
	}
}
