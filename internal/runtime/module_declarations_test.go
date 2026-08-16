package runtime

import (
	"context"
	"testing"
)

func TestModuleDeclarationModuleFunctions(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Billing
  def self.code
    "ok"
  end

  def self.total(amount, rate)
    amount * rate
  end
end

def code
  Billing.code
end

def total
  Billing.total(10, 3)
end
`)

	if got := callFunc(t, script, "code", nil); !got.Equal(NewString("ok")) {
		t.Fatalf("code = %v, want ok", got)
	}
	if got := callFunc(t, script, "total", nil); !got.Equal(NewInt(30)) {
		t.Fatalf("total = %v, want 30", got)
	}
}

func TestModuleConstants(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Config
  LIMIT = 2 + 3

  def self.limit
    LIMIT
  end
end

def scoped
  Config::LIMIT
end

def dotted
  Config.LIMIT
end

def through_function
  Config.limit
end
`)

	for _, fn := range []string{"scoped", "dotted", "through_function"} {
		if got := callFunc(t, script, fn, nil); !got.Equal(NewInt(5)) {
			t.Fatalf("%s = %v, want 5", fn, got)
		}
	}
}

func TestNestedModuleDeclarations(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Outer
  GREETING = "hi"

  module Inner
    BASE = 2

    def self.double
      BASE * 2
    end
  end
end

def scoped_constant
  Outer::Inner::BASE
end

def scoped_function
  Outer::Inner.double
end

def parent_constant
  Outer::GREETING
end
`)

	if got := callFunc(t, script, "scoped_constant", nil); !got.Equal(NewInt(2)) {
		t.Fatalf("scoped_constant = %v, want 2", got)
	}
	if got := callFunc(t, script, "scoped_function", nil); !got.Equal(NewInt(4)) {
		t.Fatalf("scoped_function = %v, want 4", got)
	}
	if got := callFunc(t, script, "parent_constant", nil); !got.Equal(NewString("hi")) {
		t.Fatalf("parent_constant = %v, want hi", got)
	}
}

func TestModuleCannotBeInstantiated(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Billing
end

def build
  Billing.new
end
`)
	requireCallErrorContains(t, script, "build", nil, CallOptions{}, "module Billing cannot be instantiated")
}

func TestModuleStateIsolatedPerCall(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
module Registry
  COUNT = 0

  def self.bump
    COUNT = COUNT + 1
    COUNT
  end
end

def bump_twice
  Registry.bump
  Registry.bump
end
`)

	for call := range 2 {
		if got := callFunc(t, script, "bump_twice", nil); !got.Equal(NewInt(2)) {
			t.Fatalf("call %d: bump_twice = %v, want 2 (module state leaked across calls)", call, got)
		}
	}
}

func TestModuleDuplicateNames(t *testing.T) {
	t.Parallel()
	requireCompileErrorContainsDefault(t, `
module Billing
end

module Billing
end
`, "duplicate module Billing")

	requireCompileErrorContainsDefault(t, `
class Billing
end

module Billing
end
`, "duplicate module Billing")
}

// A module is a namespace, so it declares functions with def self.name. A
// plain def has no receiver to run on and is rejected where it is written.
func TestModuleInstanceMethodIsACompileError(t *testing.T) {
	t.Parallel()
	requireCompileErrorContainsDefault(t, `
module Named
  def display_name
    "named"
  end
end
`, "def display_name in module Named must be def self.display_name")
}

func TestCompileSnippetInitializesModuleBodyInSourceOrder(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	script, err := engine.CompileSnippet(`limit = 10

module Settings
  LIMIT = limit

  def self.limit
    LIMIT
  end
end

Settings.limit
`, "__eval__")
	if err != nil {
		t.Fatalf("CompileSnippet failed: %v", err)
	}

	got := callScript(t, context.Background(), script, "__eval__", nil, CallOptions{})
	if got.Kind() != KindInt || got.Int() != 10 {
		t.Fatalf("__eval__() = %#v, want 10", got)
	}
}
