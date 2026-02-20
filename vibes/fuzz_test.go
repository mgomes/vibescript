package vibes

import (
	"context"
	"strings"
	"testing"
)

func FuzzCompileScriptDoesNotPanic(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("def run() 1 end"))
	f.Add([]byte("def broken("))
	f.Add([]byte("begin\n  raise(\"boom\")\nrescue\n  1\nend"))
	f.Add([]byte("require(\"../path\")"))

	f.Fuzz(func(t *testing.T, raw []byte) {
		engine := MustNewEngine(Config{})
		_, _ = engine.Compile(string(raw))
	})
}

func FuzzRuntimeEdgeCasesDoNotPanic(f *testing.F) {
	engine := MustNewEngine(Config{})
	script, err := engine.Compile(`
def run(text, pattern)
  stable_groups = text.split("").group_by_stable do |char|
    if char == ""
      :empty
    else
      :present
    end
  end

  {
    match: Regex.match(pattern, text),
    replace_all: Regex.replace_all(text, pattern, "x"),
    json: JSON.stringify({ text: text }),
    template: "value={{text}}".template({ text: text }),
    chunks: text.split("").chunk(4),
    windows: text.split("").window(2),
    stable_groups: stable_groups
  }
end
`)
	if err != nil {
		f.Fatalf("compile failed: %v", err)
	}

	f.Add("", "a*")
	f.Add("ID-12 ID-34", "ID-[0-9]+")
	f.Add(strings.Repeat("a", 256), "(")
	f.Add("  hello \n world  ", "\\s+")
	f.Add("{}", ".*")

	f.Fuzz(func(t *testing.T, text string, pattern string) {
		if len(text) > 4096 {
			text = text[:4096]
		}
		if len(pattern) > 1024 {
			pattern = pattern[:1024]
		}
		_, _ = script.Call(context.Background(), "run", []Value{
			NewString(text),
			NewString(pattern),
		}, CallOptions{})
	})
}
