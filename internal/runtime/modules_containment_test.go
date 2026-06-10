package runtime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// These tests pin the module containment guarantees from the v0.27
// hardening work: module roots are resolved and frozen at engine
// creation, realpath containment applies to the resolved root, and
// filesystem changes after engine creation degrade to clean require
// errors instead of panics or escapes.

func TestRequireThroughSymlinkedModuleRoot(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is environment-specific on Windows")
	}

	realRoot := tempModuleTree(t, moduleFile{
		path: "nested/helper.vibe",
		content: `def value()
  7
end
`,
	})
	outsideRoot := tempModuleTree(t, moduleFile{
		path: "secret.vibe",
		content: `def hidden()
  42
end
`,
	})
	if err := os.Symlink(filepath.Join(outsideRoot, "secret.vibe"), filepath.Join(realRoot, "escape.vibe")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	linkRoot := filepath.Join(t.TempDir(), "mods")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	engine := mustNewEngineWithModuleRoot(t, linkRoot)

	inside := compileScriptWithEngine(t, engine, `def run()
  mod = require("nested/helper")
  mod.value()
end`)
	result, err := inside.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("require through symlinked root failed: %v", err)
	}
	if !result.Equal(NewInt(7)) {
		t.Fatalf("require through symlinked root = %#v, want 7", result)
	}

	escape := compileScriptWithEngine(t, engine, `def run()
  require("escape")
end`)
	requireCallErrorContains(t, escape, "run", nil, CallOptions{}, "escapes module root")
}

func TestRequireRejectsModuleFileSymlinkEscape(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is environment-specific on Windows")
	}

	moduleRoot := tempModuleTree(t, moduleFile{
		path: "entry.vibe",
		content: `def run()
  require("./escape")
end
`,
	})
	outsideRoot := tempModuleTree(t, moduleFile{
		path: "secret.vibe",
		content: `def hidden()
  42
end
`,
	})
	if err := os.Symlink(filepath.Join(outsideRoot, "secret.vibe"), filepath.Join(moduleRoot, "escape.vibe")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	engine := mustNewEngineWithModuleRoot(t, moduleRoot)

	searchScript := compileScriptWithEngine(t, engine, `def run()
  require("escape")
end`)
	requireCallErrorContains(t, searchScript, "run", nil, CallOptions{}, "escapes module root")

	relativeScript := compileScriptWithEngine(t, engine, `def run()
  mod = require("entry")
  mod.run()
end`)
	requireCallErrorContains(t, relativeScript, "run", nil, CallOptions{}, "escapes module root")
}

func TestRequireFailsCleanlyWhenModuleRootDisappears(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		disturb func(t *testing.T, root string)
	}{
		{
			name: "renamed",
			disturb: func(t *testing.T, root string) {
				if err := os.Rename(root, root+"-moved"); err != nil {
					t.Fatalf("rename module root: %v", err)
				}
			},
		},
		{
			name: "removed",
			disturb: func(t *testing.T, root string) {
				if err := os.RemoveAll(root); err != nil {
					t.Fatalf("remove module root: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "mods")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatalf("mkdir module root: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "helper.vibe"), []byte("def value()\n  1\nend\n"), 0o644); err != nil {
				t.Fatalf("write module: %v", err)
			}

			engine := mustNewEngineWithModuleRoot(t, root)
			script := compileScriptWithEngine(t, engine, `def run()
  require("helper")
end`)

			tc.disturb(t, root)

			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "require:")
		})
	}
}

func TestRequireFailsCleanlyAfterPermissionRevocation(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("chmod permission bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permission checks")
	}

	t.Run("module_file_unreadable", func(t *testing.T) {
		t.Parallel()
		moduleRoot := tempModuleTree(t, moduleFile{
			path: "helper.vibe",
			content: `def value()
  1
end
`,
		})
		engine := mustNewEngineWithModuleRoot(t, moduleRoot)
		script := compileScriptWithEngine(t, engine, `def run()
  require("helper")
end`)

		modulePath := filepath.Join(moduleRoot, "helper.vibe")
		if err := os.Chmod(modulePath, 0o000); err != nil {
			t.Fatalf("chmod module file: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(modulePath, 0o644); err != nil {
				t.Errorf("restore module file permissions: %v", err)
			}
		})

		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "permission denied")
	})

	t.Run("module_directory_unreadable", func(t *testing.T) {
		t.Parallel()
		moduleRoot := tempModuleTree(t, moduleFile{
			path: "sub/helper.vibe",
			content: `def value()
  1
end
`,
		})
		engine := mustNewEngineWithModuleRoot(t, moduleRoot)
		script := compileScriptWithEngine(t, engine, `def run()
  require("sub/helper")
end`)

		subDir := filepath.Join(moduleRoot, "sub")
		if err := os.Chmod(subDir, 0o000); err != nil {
			t.Fatalf("chmod module directory: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(subDir, 0o755); err != nil {
				t.Errorf("restore module directory permissions: %v", err)
			}
		})

		requireCallErrorContains(t, script, "run", nil, CallOptions{}, "require:")
	})
}
