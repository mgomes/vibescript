package runtime

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLevenshteinWithin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		a        string
		b        string
		limit    int
		want     int
		withinOK bool
	}{
		{name: "identical", a: "length", b: "length", limit: 2, want: 0, withinOK: true},
		{name: "single substitution", a: "lenxth", b: "length", limit: 2, want: 1, withinOK: true},
		{name: "single insertion", a: "lenth", b: "length", limit: 2, want: 1, withinOK: true},
		{name: "single deletion", a: "lengthh", b: "length", limit: 2, want: 1, withinOK: true},
		{name: "adjacent transposition costs two", a: "lenght", b: "length", limit: 2, want: 2, withinOK: true},
		{name: "abandons past limit", a: "abcdef", b: "uvwxyz", limit: 2, withinOK: false},
		{name: "length gap past limit", a: "ab", b: "abcdef", limit: 2, withinOK: false},
		{name: "length gap at limit", a: "abcd", b: "abcdef", limit: 2, want: 2, withinOK: true},
		{name: "argument order is symmetric", a: "length", b: "lenth", limit: 2, want: 1, withinOK: true},
		{name: "multibyte runes count once", a: "héllo", b: "hello", limit: 2, want: 1, withinOK: true},
		{name: "empty versus short", a: "", b: "ab", limit: 2, want: 2, withinOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := levenshteinWithin(tt.a, tt.b, tt.limit)
			if ok != tt.withinOK {
				t.Fatalf("levenshteinWithin(%q, %q, %d) ok = %v, want %v", tt.a, tt.b, tt.limit, ok, tt.withinOK)
			}
			if ok && got != tt.want {
				t.Fatalf("levenshteinWithin(%q, %q, %d) = %d, want %d", tt.a, tt.b, tt.limit, got, tt.want)
			}
		})
	}
}

func TestSuggestNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		missing    string
		candidates []string
		want       []string
	}{
		{
			name:       "exact length typo",
			missing:    "lenxth",
			candidates: []string{"length", "size"},
			want:       []string{"length"},
		},
		{
			name:       "transposition within cap",
			missing:    "lenght",
			candidates: []string{"length", "size"},
			want:       []string{"length"},
		},
		{
			name:       "too far excluded",
			missing:    "foo",
			candidates: []string{"barbaz", "quux"},
			want:       nil,
		},
		{
			name:       "short names require a single edit",
			missing:    "nmae",
			candidates: []string{"name"},
			want:       nil,
		},
		{
			name:       "short name single edit accepted",
			missing:    "siz",
			candidates: []string{"size"},
			want:       []string{"size"},
		},
		{
			name:       "ties order lexicographically and cap at three",
			missing:    "cat",
			candidates: []string{"rat", "hat", "fat", "bat"},
			want:       []string{"bat", "fat", "hat"},
		},
		{
			name:       "case-only mismatch ranks first",
			missing:    "length",
			candidates: []string{"lengthy", "Length"},
			want:       []string{"Length", "lengthy"},
		},
		{
			name:       "case-only mismatch accepted beyond distance cap",
			missing:    "LENGTH",
			candidates: []string{"length"},
			want:       []string{"length"},
		},
		{
			name:       "duplicate candidates collapse",
			missing:    "lenxth",
			candidates: []string{"length", "length"},
			want:       []string{"length"},
		},
		{
			name:       "identical candidate skipped",
			missing:    "size",
			candidates: []string{"size"},
			want:       nil,
		},
		{
			name:       "empty missing name yields nothing",
			missing:    "",
			candidates: []string{"size"},
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := suggestNames(tt.missing, tt.candidates)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("suggestNames(%q, %v) = %v, want %v", tt.missing, tt.candidates, got, tt.want)
			}
		})
	}
}

func TestDidYouMean(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		missing    string
		candidates []string
		want       string
	}{
		{
			name:       "no close match",
			missing:    "zzzzzz",
			candidates: []string{"length", "size"},
			want:       "",
		},
		{
			name:       "single match",
			missing:    "lenght",
			candidates: []string{"length", "size"},
			want:       ` (did you mean "length"?)`,
		},
		{
			name:       "two matches",
			missing:    "uppcase",
			candidates: []string{"upcase", "upcase!", "downcase"},
			want:       ` (did you mean "upcase" or "upcase!"?)`,
		},
		{
			name:       "three matches",
			missing:    "cat",
			candidates: []string{"rat", "hat", "fat", "bat"},
			want:       ` (did you mean "bat", "fat", or "hat"?)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := didYouMean(tt.missing, tt.candidates)
			if got != tt.want {
				t.Fatalf("didYouMean(%q, %v) = %q, want %q", tt.missing, tt.candidates, got, tt.want)
			}
		})
	}
}

// TestMemberSuggestionCandidatesResolve guards the suggestion candidate
// lists against drifting from the dispatch switches they mirror: every
// candidate must resolve as a real member of its type.
func TestMemberSuggestionCandidatesResolve(t *testing.T) {
	t.Parallel()
	money, err := parseMoneyLiteral("1.00 USD")
	if err != nil {
		t.Fatalf("parse money literal: %v", err)
	}

	tables := []struct {
		kind    string
		names   []string
		resolve func(name string) error
	}{
		{
			kind:  "string",
			names: stringMemberNames,
			resolve: func(name string) error {
				_, err := stringMember(NewString("vibe"), name)
				return err
			},
		},
		{
			kind:  "array",
			names: arrayMemberNames,
			resolve: func(name string) error {
				_, err := arrayMember(NewArray(nil), name)
				return err
			},
		},
		{
			kind:  "hash",
			names: hashMemberNames,
			resolve: func(name string) error {
				_, err := hashMember(NewHash(map[string]Value{}), name)
				return err
			},
		},
		{
			kind:  "int",
			names: intMemberNames,
			resolve: func(name string) error {
				_, err := (&Execution{}).intMember(NewInt(1), name, Position{})
				return err
			},
		},
		{
			kind:  "float",
			names: floatMemberNames,
			resolve: func(name string) error {
				_, err := (&Execution{}).floatMember(NewFloat(1), name, Position{})
				return err
			},
		},
		{
			kind:  "money",
			names: moneyMemberNames,
			resolve: func(name string) error {
				_, err := moneyMember(money, name)
				return err
			},
		},
		{
			kind:  "duration",
			names: durationMemberNames,
			resolve: func(name string) error {
				_, err := durationMember(secondsDuration(1, "seconds"), name, Position{})
				return err
			},
		},
		{
			kind:  "time",
			names: timeMemberNames,
			resolve: func(name string) error {
				_, err := timeMember(time.Unix(0, 0).UTC(), name)
				return err
			},
		},
	}

	for _, table := range tables {
		t.Run(table.kind, func(t *testing.T) {
			t.Parallel()
			for _, name := range table.names {
				if err := table.resolve(name); err != nil {
					t.Errorf("%s member %q listed as suggestion candidate but does not resolve: %v", table.kind, name, err)
				}
			}
		})
	}
}

func TestLookupFailuresIncludeSuggestions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		script string
		errMsg string
	}{
		{
			name: "undefined variable suggests local",
			script: `def run()
  length = 5
  lenght
end`,
			errMsg: `undefined variable lenght (did you mean "length"?)`,
		},
		{
			name:   "undefined variable suggests function",
			script: "def helper()\n  1\nend\n\ndef run()\n  helpr()\nend",
			errMsg: `undefined variable helpr (did you mean "helper"?)`,
		},
		{
			name:   "undefined variable without close match has no suffix",
			script: `def run() zzzzzz end`,
			errMsg: "undefined variable zzzzzz",
		},
		{
			name:   "string method typo",
			script: `def run() "hello".uppcase end`,
			errMsg: `unknown string method uppcase (did you mean "upcase" or "upcase!"?)`,
		},
		{
			name:   "array method typo",
			script: `def run() [1, 2].lenght end`,
			errMsg: `unknown array method lenght (did you mean "length"?)`,
		},
		{
			name: "hash method typo suggests data key",
			script: `def run()
  h = { counter: 1 }
  h.countr
end`,
			errMsg: `unknown hash method countr (did you mean "counter"?)`,
		},
		{
			name:   "int member typo",
			script: `def run() 5.tims end`,
			errMsg: `unknown int member tims (did you mean "times"?)`,
		},
		{
			name:   "duration method typo",
			script: `def run() 60.seconds.in_minuts end`,
			errMsg: `unknown duration method in_minuts (did you mean "in_minutes"?)`,
		},
		{
			name:   "time method typo",
			script: `def run() Time.parse("2024-01-01T00:00:00Z").yer end`,
			errMsg: `unknown time method yer (did you mean "year"?)`,
		},
		{
			name: "instance method typo",
			script: `class Greeter
  def greet
    "hi"
  end
end

def run()
  g = Greeter.new
  g.gret
end`,
			errMsg: `unknown member gret (did you mean "greet"?)`,
		},
		{
			name: "class member typo",
			script: `class Counter
  def self.instances
    1
  end
end

def run()
  Counter.instnces
end`,
			errMsg: `unknown class member instnces (did you mean "instances"?)`,
		},
		{
			name: "enum member typo",
			script: `enum Status
  Draft
  Published
end

def run()
  Status::Drafd
end`,
			errMsg: `unknown enum member Status::Drafd (did you mean "Draft"?)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, tt.script)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, tt.errMsg)
		})
	}
}

func TestUndefinedVariableWithoutCloseMatchOmitsSuggestion(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `def run() zzzzzz end`)
	err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
	if strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("expected no suggestion, got: %v", err)
	}
}

func TestHostCallSuggestsFunctionNames(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, "def runner()\n  1\nend")
	_, err := script.Call(context.Background(), "runer", nil, CallOptions{})
	requireErrorContains(t, err, `function runer not found (did you mean "runner"?)`)
}

func TestRequireSuggestsCloseModuleNames(t *testing.T) {
	t.Parallel()

	moduleSource := "def double(x)\n  x * 2\nend\n"

	t.Run("search path module", func(t *testing.T) {
		t.Parallel()
		root := tempModuleTree(t,
			moduleFile{path: "helpers.vibe", content: moduleSource},
			moduleFile{path: "shared/math.vibe", content: moduleSource},
		)
		engine := mustNewEngineWithModuleRoot(t, root)
		script := compileScriptWithEngine(t, engine, "def run()\n  require(\"helprs\")\nend")
		requireCallErrorContains(t, script, "run", nil, CallOptions{},
			`require: module "helprs" not found (did you mean "helpers"?)`)
	})

	t.Run("nested search path module", func(t *testing.T) {
		t.Parallel()
		root := tempModuleTree(t,
			moduleFile{path: "helpers.vibe", content: moduleSource},
			moduleFile{path: "shared/math.vibe", content: moduleSource},
		)
		engine := mustNewEngineWithModuleRoot(t, root)
		script := compileScriptWithEngine(t, engine, "def run()\n  require(\"shared/maht\")\nend")
		requireCallErrorContains(t, script, "run", nil, CallOptions{},
			`require: module "shared/maht" not found (did you mean "shared/math"?)`)
	})

	t.Run("relative module", func(t *testing.T) {
		t.Parallel()
		root := tempModuleTree(t,
			moduleFile{path: "entry.vibe", content: "def boot()\n  require(\"./helprs\")\nend\n"},
			moduleFile{path: "helpers.vibe", content: moduleSource},
		)
		engine := mustNewEngineWithModuleRoot(t, root)
		script := compileScriptWithEngine(t, engine, "def run()\n  mod = require(\"entry\")\n  mod.boot()\nend")
		requireCallErrorContains(t, script, "run", nil, CallOptions{},
			`require: module "./helprs" not found (did you mean "./helpers"?)`)
	})

	t.Run("no close match omits suggestion", func(t *testing.T) {
		t.Parallel()
		root := tempModuleTree(t, moduleFile{path: "helpers.vibe", content: moduleSource})
		engine := mustNewEngineWithModuleRoot(t, root)
		script := compileScriptWithEngine(t, engine, "def run()\n  require(\"zzzzzz\")\nend")
		err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
		requireErrorContains(t, err, `module "zzzzzz" not found`)
		if strings.Contains(err.Error(), "did you mean") {
			t.Fatalf("expected no suggestion, got: %v", err)
		}
	})

	t.Run("policy denied modules are not suggested", func(t *testing.T) {
		t.Parallel()
		root := tempModuleTree(t, moduleFile{path: "secret.vibe", content: moduleSource})
		engine := MustNewEngine(Config{ModulePaths: []string{root}, ModuleDenyList: []string{"secret"}})
		script := compileScriptWithEngine(t, engine, "def run()\n  require(\"secrt\")\nend")
		err := callScriptErr(t, context.Background(), script, "run", nil, CallOptions{})
		requireErrorContains(t, err, `module "secrt" not found`)
		if strings.Contains(err.Error(), "did you mean") {
			t.Fatalf("expected denied module to be excluded from suggestions, got: %v", err)
		}
	})
}

func TestPrivateMethodsAreNotSuggestedToOutsideCallers(t *testing.T) {
	t.Parallel()
	script := compileScriptDefault(t, `class Vault
  def open_lid()
    1
  end

  private def secret()
    2
  end

  def probe()
    self.secrez
  end
end

def from_outside()
  Vault.new.secrez
end

def from_inside()
  Vault.new.probe
end`)

	outsideErr := callScriptErr(t, context.Background(), script, "from_outside", nil, CallOptions{})
	requireErrorContains(t, outsideErr, "unknown member secrez")
	if strings.Contains(outsideErr.Error(), "secret") {
		t.Fatalf("outside caller suggestion discloses private method: %v", outsideErr)
	}

	insideErr := callScriptErr(t, context.Background(), script, "from_inside", nil, CallOptions{})
	requireErrorContains(t, insideErr, `did you mean "secret"?`)
}

func TestModuleSuggestionsPreserveLiteralExtensions(t *testing.T) {
	t.Parallel()
	moduleSource := "def double(x)\n  x * 2\nend\n"
	root := tempModuleTree(t,
		moduleFile{path: "helper.vibe.vibe", content: moduleSource},
	)
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, "def run()\n  require(\"helper.vibe.vib\")\nend")
	requireCallErrorContains(t, script, "run", nil, CallOptions{},
		`require: module "helper.vibe.vib" not found (did you mean "helper.vibe.vibe"?)`)
}

func TestRelativeModuleSuggestionsKeepRawSubdirectoryPrefix(t *testing.T) {
	t.Parallel()
	moduleSource := "def double(x)\n  x * 2\nend\n"
	root := tempModuleTree(t,
		moduleFile{path: "lib/entry.vibe", content: "def boot()\n  require(\"./sub/helprs\")\nend\n"},
		moduleFile{path: "lib/sub/helpers.vibe", content: moduleSource},
	)
	engine := mustNewEngineWithModuleRoot(t, root)
	script := compileScriptWithEngine(t, engine, "def run()\n  mod = require(\"lib/entry\")\n  mod.boot()\nend")
	requireCallErrorContains(t, script, "run", nil, CallOptions{},
		`require: module "./sub/helprs" not found (did you mean "./sub/helpers"?)`)
}

func TestModuleSuggestionsExcludeSymlinksEscapingRoot(t *testing.T) {
	t.Parallel()
	moduleSource := "def double(x)\n  x * 2\nend\n"
	outside := t.TempDir()
	outsideModule := filepath.Join(outside, "escape.vibe")
	if err := os.WriteFile(outsideModule, []byte(moduleSource), 0o644); err != nil {
		t.Fatalf("write outside module: %v", err)
	}

	root := tempModuleTree(t, moduleFile{path: "entry.vibe", content: "def boot()\n  require(\"./escap\")\nend\n"})
	if err := os.Symlink(outsideModule, filepath.Join(root, "escape.vibe")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	engine := mustNewEngineWithModuleRoot(t, root)

	searchScript := compileScriptWithEngine(t, engine, "def run()\n  require(\"escap\")\nend")
	searchErr := callScriptErr(t, context.Background(), searchScript, "run", nil, CallOptions{})
	requireErrorContains(t, searchErr, `module "escap" not found`)
	if strings.Contains(searchErr.Error(), "did you mean") {
		t.Fatalf("search-path suggestion discloses root-escaping symlink: %v", searchErr)
	}

	relativeScript := compileScriptWithEngine(t, engine, "def run()\n  mod = require(\"entry\")\n  mod.boot()\nend")
	relativeErr := callScriptErr(t, context.Background(), relativeScript, "run", nil, CallOptions{})
	requireErrorContains(t, relativeErr, `module "./escap" not found`)
	if strings.Contains(relativeErr.Error(), "did you mean") {
		t.Fatalf("relative suggestion discloses root-escaping symlink: %v", relativeErr)
	}
}
