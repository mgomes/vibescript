package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// writeModuleStamped writes content and pins the file's mtime, so edits are
// visible to dev mode's mtime+size check even on filesystems with coarse
// timestamps.
func writeModuleStamped(t testing.TB, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func devModeEngineWithRoot(t testing.TB, root string) *Engine {
	t.Helper()
	return MustNewEngine(Config{ModulePaths: []string{root}, DevMode: true})
}

func valueModuleSource(v int) string {
	return fmt.Sprintf("def value()\n  %d\nend\n", v)
}

func callRunInt(t testing.TB, script *Script) int64 {
	t.Helper()
	result, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if result.Kind() != KindInt {
		t.Fatalf("expected int result, got %#v", result)
	}
	return result.Int()
}

func TestDevModeReloadsEditedModuleBetweenCalls(t *testing.T) {
	t.Parallel()
	root := tempModuleTree(t, moduleFile{path: "dynamic.vibe", content: valueModuleSource(1)})
	modulePath := filepath.Join(root, "dynamic.vibe")
	base := time.Now().Add(-time.Hour)

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("dynamic")
  mod.value()
end`)

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected first value 1, got %d", got)
	}

	writeModuleStamped(t, modulePath, valueModuleSource(2), base.Add(time.Second))

	if got := callRunInt(t, script); got != 2 {
		t.Fatalf("expected reloaded value 2 without ClearModuleCache, got %d", got)
	}
}

func TestDevModeUnchangedModuleServesCachedScript(t *testing.T) {
	t.Parallel()
	root := tempModuleTree(t, moduleFile{path: "dynamic.vibe", content: valueModuleSource(1)})

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("dynamic")
  mod.value()
end`)

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected value 1, got %d", got)
	}
	if len(engine.modules) != 1 {
		t.Fatalf("expected 1 cached module, got %d", len(engine.modules))
	}
	var firstScript *Script
	for _, entry := range engine.modules {
		firstScript = entry.script
	}

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected value 1, got %d", got)
	}
	if len(engine.modules) != 1 {
		t.Fatalf("expected 1 cached module after second call, got %d", len(engine.modules))
	}
	for _, entry := range engine.modules {
		if entry.script != firstScript {
			t.Fatalf("expected unchanged module to reuse cached script, got a recompile")
		}
	}
}

func TestDevModeFindsNewlyCreatedModule(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("late")
  mod.value()
end`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, `module "late" not found`)

	writeModuleStamped(t, filepath.Join(root, "late.vibe"), valueModuleSource(7), time.Now().Add(-time.Hour))

	if got := callRunInt(t, script); got != 7 {
		t.Fatalf("expected newly created module value 7, got %d", got)
	}
}

func TestDevModeReloadsRelativeRequire(t *testing.T) {
	t.Parallel()
	root := tempModuleTree(t,
		moduleFile{path: "a.vibe", content: `def value()
  b = require("./b")
  b.value()
end
`},
		moduleFile{path: "b.vibe", content: valueModuleSource(1)},
	)
	base := time.Now().Add(-time.Hour)

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  a = require("a")
  a.value()
end`)

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected first value 1, got %d", got)
	}

	writeModuleStamped(t, filepath.Join(root, "b.vibe"), valueModuleSource(2), base.Add(time.Second))

	if got := callRunInt(t, script); got != 2 {
		t.Fatalf("expected reloaded relative module value 2, got %d", got)
	}
}

func TestDevModeClearModuleCacheStillWorks(t *testing.T) {
	t.Parallel()
	root := tempModuleTree(t, moduleFile{path: "dynamic.vibe", content: valueModuleSource(1)})

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("dynamic")
  mod.value()
end`)

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected value 1, got %d", got)
	}
	if cleared := engine.ClearModuleCache(); cleared != 1 {
		t.Fatalf("expected 1 cleared module, got %d", cleared)
	}
	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected value 1 after cache clear, got %d", got)
	}
}

func TestDevModeStaleCompileErrorDoesNotPoisonCache(t *testing.T) {
	t.Parallel()
	root := tempModuleTree(t, moduleFile{path: "dynamic.vibe", content: valueModuleSource(1)})
	modulePath := filepath.Join(root, "dynamic.vibe")
	base := time.Now().Add(-time.Hour)

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("dynamic")
  mod.value()
end`)

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected value 1, got %d", got)
	}

	writeModuleStamped(t, modulePath, "def value(\n", base.Add(time.Second))
	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "require: compiling")

	writeModuleStamped(t, modulePath, valueModuleSource(3), base.Add(2*time.Second))
	if got := callRunInt(t, script); got != 3 {
		t.Fatalf("expected recovered value 3, got %d", got)
	}
}

func TestDevModeConcurrentReload(t *testing.T) {
	t.Parallel()
	root := tempModuleTree(t, moduleFile{path: "dynamic.vibe", content: valueModuleSource(1)})
	modulePath := filepath.Join(root, "dynamic.vibe")
	base := time.Now().Add(-time.Hour)

	engine := devModeEngineWithRoot(t, root)
	script := compileScriptWithEngine(t, engine, `def run()
  mod = require("dynamic")
  mod.value()
end`)

	if got := callRunInt(t, script); got != 1 {
		t.Fatalf("expected first value 1, got %d", got)
	}

	writeModuleStamped(t, modulePath, valueModuleSource(2), base.Add(time.Second))

	const goroutines = 10
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	recordErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}

	for range goroutines {
		wg.Go(func() {
			result, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				recordErr(err)
				return
			}
			if result.Kind() != KindInt || result.Int() != 2 {
				recordErr(fmt.Errorf("expected reloaded value 2, got %#v", result))
			}
		})
	}

	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent reload failed: %v", errs[0])
	}
	if len(engine.modules) != 1 {
		t.Fatalf("expected 1 cached module after concurrent reload, got %d", len(engine.modules))
	}
}
