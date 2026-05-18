package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func TestRapidModulePolicyNormalizationProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		rawPattern := rapidRawModulePath().Draw(rt, "raw pattern")
		rawModule := rapidRawModulePath().Draw(rt, "raw module")

		normalizedPattern := normalizeModulePolicyPattern(rawPattern)
		if got := normalizeModulePolicyPattern(normalizedPattern); got != normalizedPattern {
			rt.Fatalf("normalizeModulePolicyPattern(%q) after normalizeModulePolicyPattern(%q) = %q, want %q", normalizedPattern, rawPattern, got, normalizedPattern)
		}

		normalizedModule := normalizeModulePolicyModuleName(rawModule)
		if got := normalizeModulePolicyModuleName(normalizedModule); got != normalizedModule {
			rt.Fatalf("normalizeModulePolicyModuleName(%q) after normalizeModulePolicyModuleName(%q) = %q, want %q", normalizedModule, rawModule, got, normalizedModule)
		}

		module := drawRapidModuleName(rt, "module")
		spellings := []string{
			module,
			"./" + module,
			module + ".vibe",
			strings.ReplaceAll(module, "/", "\\") + ".vibe",
		}
		wantCanonical := normalizeModulePolicyModuleName(module)
		for _, spelling := range spellings {
			if got := normalizeModulePolicyModuleName(spelling); got != wantCanonical {
				rt.Fatalf("normalizeModulePolicyModuleName(%q) = %q, want %q", spelling, got, wantCanonical)
			}
		}

		prefix := drawRapidModuleName(rt, "wildcard prefix")
		leaf := drawRapidIdentifier(rt, "wildcard leaf")
		pattern := normalizeModulePolicyPattern(prefix + "/*")
		nestedModule := normalizeModulePolicyModuleName(prefix + "/" + leaf)
		if !modulePolicyMatch(pattern, nestedModule) {
			rt.Fatalf("modulePolicyMatch(%q, %q) = false, want true", pattern, nestedModule)
		}

		engine, err := NewEngine(Config{
			ModuleAllowList: []string{module},
			ModuleDenyList:  []string{module},
		})
		if err != nil {
			rt.Fatalf("NewEngine(module policy %q) error = %v, want nil", module, err)
		}
		if err := engine.enforceModulePolicy(module); err == nil {
			rt.Fatalf("enforceModulePolicy(%q) error = nil, want deny-list override error", module)
		}
	})
}

func TestRapidValueJSONAndCloneProperties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		value := drawRapidJSONValue(rt, 4)

		encoded, err := builtinJSONStringify(nil, NewNil(), []Value{value}, nil, NewNil())
		if err != nil {
			rt.Fatalf("JSON.stringify(%s) error = %v, want nil", value.String(), err)
		}
		decoded, err := builtinJSONParse(nil, NewNil(), []Value{encoded}, nil, NewNil())
		if err != nil {
			rt.Fatalf("JSON.parse(JSON.stringify(%s)) error = %v, want nil", value.String(), err)
		}
		if diff := valueDiff(value, decoded); diff != "" {
			rt.Fatalf("JSON.parse(JSON.stringify(%s)) mismatch (-want +got):\n%s", value.String(), diff)
		}

		cloned := deepCloneValue(value)
		if diff := valueDiff(value, cloned); diff != "" {
			rt.Fatalf("deepCloneValue(%s) mismatch (-want +got):\n%s", value.String(), diff)
		}

		beforeMutation := deepCloneValue(value)
		if mutateRapidFirstContainer(cloned) {
			if diff := valueDiff(beforeMutation, value); diff != "" {
				rt.Fatalf("mutating deepCloneValue(%s) changed original (-want +got):\n%s", beforeMutation.String(), diff)
			}
		}
	})
}

func TestRapidCollectionHelperLaws(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def array_props(values)
  reversed = values.reverse
  unique = values.uniq
  {
    reverse_twice: reversed.reverse,
    uniq_once: unique,
    uniq_twice: unique.uniq,
    size: values.size
  }
end

def hash_props(record)
  {
    size: record.size,
    keys_size: record.keys.size,
    values_size: record.values.size
  }
end

def string_props(text)
  stripped = text.strip
  {
    strip_once: stripped,
    strip_twice: stripped.strip,
    reverse_twice: text.reverse.reverse
  }
end
`)

	rapid.Check(t, func(rt *rapid.T) {
		values := rapid.SliceOfN(rapidScalarValue(), 0, 16).Draw(rt, "array values")
		arrayArg := NewArray(append([]Value(nil), values...))
		arrayProps := callRapidFunction(rt, script, "array_props", []Value{arrayArg}).Hash()
		if diff := valueDiff(arrayArg, arrayProps["reverse_twice"]); diff != "" {
			rt.Fatalf("array_props(%s).reverse_twice mismatch (-want +got):\n%s", arrayArg.String(), diff)
		}
		if diff := valueDiff(arrayProps["uniq_once"], arrayProps["uniq_twice"]); diff != "" {
			rt.Fatalf("array_props(%s).uniq_twice mismatch (-want +got):\n%s", arrayArg.String(), diff)
		}
		if got, want := arrayProps["size"], NewInt(int64(len(values))); !got.Equal(want) {
			rt.Fatalf("array_props(%s).size = %s, want %s", arrayArg.String(), got.String(), want.String())
		}

		record := rapid.MapOfN(rapidIdentifier(), rapidScalarValue(), 0, 12).Draw(rt, "record")
		hashArg := NewHash(cloneRapidHash(record))
		hashProps := callRapidFunction(rt, script, "hash_props", []Value{hashArg}).Hash()
		for _, key := range []string{"size", "keys_size", "values_size"} {
			if got, want := hashProps[key], NewInt(int64(len(record))); !got.Equal(want) {
				rt.Fatalf("hash_props(%s).%s = %s, want %s", hashArg.String(), key, got.String(), want.String())
			}
		}

		text := rapid.StringMatching(`[A-Za-z0-9 _.\-\t\n]{0,64}`).Draw(rt, "text")
		stringArg := NewString(text)
		stringProps := callRapidFunction(rt, script, "string_props", []Value{stringArg}).Hash()
		if diff := valueDiff(stringProps["strip_once"], stringProps["strip_twice"]); diff != "" {
			rt.Fatalf("string_props(%q).strip_twice mismatch (-want +got):\n%s", text, diff)
		}
		if diff := valueDiff(stringArg, stringProps["reverse_twice"]); diff != "" {
			rt.Fatalf("string_props(%q).reverse_twice mismatch (-want +got):\n%s", text, diff)
		}
	})
}

func TestRapidModuleCacheFollowsClearReloadModel(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		actions := rapid.SliceOfN(rapid.SampledFrom([]rapidModuleCacheAction{
			rapidModuleCacheCall,
			rapidModuleCacheUpdate,
			rapidModuleCacheClear,
		}), 1, 16).Draw(rt, "actions")

		root := t.TempDir()
		modulePath := filepath.Join(root, "dynamic.vibe")
		diskVersion := int64(1)
		writeRapidDynamicModule(rt, modulePath, diskVersion)

		engine, err := NewEngine(Config{ModulePaths: []string{root}})
		if err != nil {
			rt.Fatalf("NewEngine(Config{ModulePaths: [%q]}) error = %v, want nil", root, err)
		}
		script, err := engine.Compile(`def run()
  mod = require("dynamic")
  mod.value()
end
`)
		if err != nil {
			rt.Fatalf("Compile(module cache driver) error = %v, want nil", err)
		}

		var cachedVersion int64
		for step, action := range actions {
			switch action {
			case rapidModuleCacheCall:
				result, err := script.Call(context.Background(), "run", nil, CallOptions{})
				if err != nil {
					rt.Fatalf("module cache step %d call error = %v, want nil", step, err)
				}

				want := diskVersion
				if cachedVersion != 0 {
					want = cachedVersion
				} else {
					cachedVersion = diskVersion
				}
				if result.Kind() != KindInt || result.Int() != want {
					rt.Fatalf("module cache step %d call = %s (%s), want int %d with disk=%d cached=%d", step, result.String(), result.Kind(), want, diskVersion, cachedVersion)
				}

			case rapidModuleCacheUpdate:
				diskVersion++
				writeRapidDynamicModule(rt, modulePath, diskVersion)

			case rapidModuleCacheClear:
				wantCleared := 0
				if cachedVersion != 0 {
					wantCleared = 1
				}
				if got := engine.ClearModuleCache(); got != wantCleared {
					rt.Fatalf("module cache step %d ClearModuleCache() = %d, want %d with disk=%d cached=%d", step, got, wantCleared, diskVersion, cachedVersion)
				}
				cachedVersion = 0

			default:
				rt.Fatalf("module cache step %d action = %s, want known action", step, action)
			}
		}
	})
}

type rapidModuleCacheAction int

const (
	rapidModuleCacheCall rapidModuleCacheAction = iota
	rapidModuleCacheUpdate
	rapidModuleCacheClear
)

func (a rapidModuleCacheAction) String() string {
	switch a {
	case rapidModuleCacheCall:
		return "call"
	case rapidModuleCacheUpdate:
		return "update"
	case rapidModuleCacheClear:
		return "clear"
	default:
		return fmt.Sprintf("unknown(%d)", int(a))
	}
}

func callRapidFunction(rt *rapid.T, script *Script, name string, args []Value) Value {
	result, err := script.Call(context.Background(), name, args, CallOptions{})
	if err != nil {
		rt.Fatalf("call %s(%v) error = %v, want nil", name, args, err)
	}
	if result.Kind() != KindHash {
		rt.Fatalf("call %s(%v) = %s (%s), want hash", name, args, result.String(), result.Kind())
	}
	return result
}

func rapidRawModulePath() *rapid.Generator[string] {
	return rapid.StringMatching(`[ A-Za-z0-9_./\\*\-]{0,64}`)
}

func rapidIdentifier() *rapid.Generator[string] {
	return rapid.StringMatching(`[a-z][a-z0-9_]{0,10}`)
}

func rapidScalarValue() *rapid.Generator[Value] {
	return rapid.Custom(func(rt *rapid.T) Value {
		switch rapid.IntRange(0, 4).Draw(rt, "scalar kind") {
		case 0:
			return NewNil()
		case 1:
			return NewBool(rapid.Bool().Draw(rt, "bool"))
		case 2:
			return NewInt(int64(rapid.IntRange(-1000, 1000).Draw(rt, "int")))
		case 3:
			return NewString(rapidSafeString().Draw(rt, "string"))
		default:
			return NewSymbol(drawRapidIdentifier(rt, "symbol"))
		}
	})
}

func rapidSafeString() *rapid.Generator[string] {
	return rapid.StringMatching(`[A-Za-z0-9 _.\-]{0,32}`)
}

func drawRapidJSONValue(rt *rapid.T, depth int) Value {
	maxKind := 3
	if depth > 0 {
		maxKind = 5
	}
	switch rapid.IntRange(0, maxKind).Draw(rt, "json kind") {
	case 0:
		return NewNil()
	case 1:
		return NewBool(rapid.Bool().Draw(rt, "json bool"))
	case 2:
		return NewInt(int64(rapid.IntRange(-1000, 1000).Draw(rt, "json int")))
	case 3:
		return NewString(rapidSafeString().Draw(rt, "json string"))
	case 4:
		values := rapid.SliceOfN(rapid.Custom(func(rt *rapid.T) Value {
			return drawRapidJSONValue(rt, depth-1)
		}), 0, 6).Draw(rt, "json array")
		return NewArray(values)
	default:
		values := rapid.MapOfN(rapidIdentifier(), rapid.Custom(func(rt *rapid.T) Value {
			return drawRapidJSONValue(rt, depth-1)
		}), 0, 6).Draw(rt, "json hash")
		return NewHash(values)
	}
}

func mutateRapidFirstContainer(value Value) bool {
	switch value.Kind() {
	case KindArray:
		array := value.Array()
		if len(array) == 0 {
			return false
		}
		array[0] = NewString("\x00rapid mutation")
		return true
	case KindHash, KindObject:
		value.Hash()["\x00rapid mutation"] = NewInt(-1)
		return true
	default:
		return false
	}
}

func cloneRapidHash(values map[string]Value) map[string]Value {
	cloned := make(map[string]Value, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func drawRapidIdentifier(rt *rapid.T, label string) string {
	return rapidIdentifier().Draw(rt, label)
}

func drawRapidModuleName(rt *rapid.T, label string) string {
	parts := rapid.SliceOfN(rapidIdentifier(), 1, 4).Draw(rt, label+" parts")
	return strings.Join(parts, "/")
}

func writeRapidDynamicModule(rt *rapid.T, path string, version int64) {
	content := fmt.Sprintf(`def value()
  %d
end
`, version)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		rt.Fatalf("os.WriteFile(%q, dynamic module version %d) error = %v, want nil", path, version, err)
	}
}
