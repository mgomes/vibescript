package runtime

import (
	"context"
	"testing"
)

func TestParenlessSingleArgumentCalls(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def id(x)
      x * 10
    end

    def call_id()
      id 2
    end

    def assign_id()
      value = id 3
      value
    end

    def return_id()
      return id 4
    end

    def push_arg()
      [1].push 2
    end
    `)

	if got := callFunc(t, script, "call_id", nil); !got.Equal(NewInt(20)) {
		t.Fatalf("call_id mismatch: %v", got)
	}
	if got := callFunc(t, script, "assign_id", nil); !got.Equal(NewInt(30)) {
		t.Fatalf("assign_id mismatch: %v", got)
	}
	if got := callFunc(t, script, "return_id", nil); !got.Equal(NewInt(40)) {
		t.Fatalf("return_id mismatch: %v", got)
	}
	compareArrays(t, callFunc(t, script, "push_arg", nil), []Value{NewInt(1), NewInt(2)})
}

// TestParenlessReservedWordKeywordLabels verifies that reserved keywords that
// are not prefix expressions (such as `rescue`, `ensure`, and `begin`) are
// accepted as keyword-argument labels in parenless calls and flow through to
// the receiving function, mirroring Ruby's parenless keyword-argument syntax.
func TestParenlessReservedWordKeywordLabels(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def accept(opts)
      opts
    end

    def single_label
      accept rescue: "retry"
    end

    def multiple_labels
      accept begin: 1, rescue: 2, ensure: 3
    end
    `)

	single := callFunc(t, script, "single_label", nil)
	compareHash(t, single.Hash(), map[string]Value{"rescue": NewString("retry")})

	multiple := callFunc(t, script, "multiple_labels", nil)
	compareHash(t, multiple.Hash(), map[string]Value{
		"begin":  NewInt(1),
		"rescue": NewInt(2),
		"ensure": NewInt(3),
	})
}

func TestParenlessArgumentListCalls(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def add(a, b)
      a + b
    end

    def keywords(name: string = "x", retries: int = 0)
      [name, retries]
    end

    def normal_keyword(name)
      name
    end

    def accept(opts)
      opts[:a] + opts[:b]
    end

    def mixed(prefix, opts)
      prefix + opts[:suffix]
    end

    def keyword_and_positional(opts, name:)
      [opts, name]
    end

    def call_add
      add 1, 2
    end

    def call_keywords
      keywords name: "Ada", retries: 3
    end

    def call_keyword_shorthand
      name = "Grace"
      retries = 4
      keywords name:, retries:
    end

    def call_normal_keyword
      normal_keyword name: "Ada"
    end

    def call_bare_options
      accept a: 1, b: 2
    end

    def call_mixed
      mixed "pre", suffix: "fix"
    end

    def parenthesized_options
      accept(a: 1, b: 2)
    end

    def strict_keyword_signature
      keyword_and_positional retry: true, name: "Ada"
    end
    `)

	if got := callFunc(t, script, "call_add", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("call_add() = %v, want 3", got)
	}
	compareArrays(t, callFunc(t, script, "call_keywords", nil), []Value{NewString("Ada"), NewInt(3)})
	compareArrays(t, callFunc(t, script, "call_keyword_shorthand", nil), []Value{NewString("Grace"), NewInt(4)})
	if got := callFunc(t, script, "call_normal_keyword", nil); !got.Equal(NewString("Ada")) {
		t.Fatalf("call_normal_keyword() = %v, want Ada", got)
	}
	if got := callFunc(t, script, "call_bare_options", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("call_bare_options() = %v, want 3", got)
	}
	if got := callFunc(t, script, "call_mixed", nil); !got.Equal(NewString("prefix")) {
		t.Fatalf("call_mixed() = %v, want prefix", got)
	}
	if got := callFunc(t, script, "parenthesized_options", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("parenthesized_options() = %v, want 3", got)
	}
	requireCallErrorContains(t, script, "strict_keyword_signature", nil, CallOptions{}, "missing argument opts")
}

func TestParenlessBareOptionsRejectLaterPositionals(t *testing.T) {
	t.Parallel()

	requireCompileErrorContainsDefault(t, `
    def collect(opts, value)
      [opts, value]
    end

    def run
      collect first: 1, "tail"
    end
    `, "positional arguments cannot follow bare keyword arguments in parenless calls")
}

func TestParenlessBareOptionsForConstructors(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Person
      def initialize(opts)
        @name = opts[:name]
      end

      def name
        @name
      end
    end

    def call_constructor
      person = Person.new name: "Ada"
      person.name
    end

    def strict_parenthesized_constructor
      Person.new(name: "Ada")
    end
    `)

	if got := callFunc(t, script, "call_constructor", nil); !got.Equal(NewString("Ada")) {
		t.Fatalf("call_constructor() = %v, want Ada", got)
	}
	requireCallErrorContains(t, script, "strict_parenthesized_constructor", nil, CallOptions{}, "missing argument opts")
}

func TestParenlessBareOptionsForClonedConstructors(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	producer := compileScriptWithEngine(t, engine, `
    class Person
      def initialize(opts)
        @name = opts[:name]
      end

      def name
        @name
      end
    end

    def export_class
      Person
    end
    `)
	consumer := compileScriptWithEngine(t, engine, `
    def call_constructor(ctor)
      person = ctor name: "Ada"
      person.name
    end
    `)

	classValue, err := producer.Call(context.Background(), "export_class", nil, CallOptions{})
	if err != nil {
		t.Fatalf("export_class() error = %v", err)
	}
	exec := newExecutionForCall(producer, context.Background(), newEnv(nil), CallOptions{})
	constructor, err := exec.getMember(classValue, "new", Position{})
	if err != nil {
		t.Fatalf("getMember(Person, new) error = %v", err)
	}
	clonedConstructor := cloneValueForHost(constructor)
	if clonedConstructor.Kind() != KindBuiltin {
		t.Fatalf("cloned constructor kind = %s, want builtin", clonedConstructor.Kind())
	}

	got, err := consumer.Call(context.Background(), "call_constructor", []Value{clonedConstructor}, CallOptions{})
	if err != nil {
		t.Fatalf("call_constructor(exported constructor) error = %v", err)
	}
	if !got.Equal(NewString("Ada")) {
		t.Fatalf("call_constructor(exported constructor) = %v, want Ada", got)
	}
}

func TestParenlessBareOptionsForMethodWrappers(t *testing.T) {
	t.Parallel()

	engine := MustNewEngine(Config{})
	producer := compileScriptWithEngine(t, engine, `
    class Person
      def configure(opts)
        @name = opts[:name]
        @name
      end

      def self.configure(opts)
        @@name = opts[:name]
        @@name
      end
    end

    def export_class
      Person
    end

    def export_person
      Person.new()
    end
    `)
	consumer := compileScriptWithEngine(t, engine, `
    def call_configure(configure)
      configure name: "Ada"
    end
    `)

	exec := newExecutionForCall(producer, context.Background(), newEnv(nil), CallOptions{})

	instanceValue, err := producer.Call(context.Background(), "export_person", nil, CallOptions{})
	if err != nil {
		t.Fatalf("export_person() error = %v", err)
	}
	instanceConfigure, err := exec.getMember(instanceValue, "configure", Position{})
	if err != nil {
		t.Fatalf("getMember(person, configure) error = %v", err)
	}
	got, err := consumer.Call(context.Background(), "call_configure", []Value{cloneValueForHost(instanceConfigure)}, CallOptions{})
	if err != nil {
		t.Fatalf("call_configure(instance configure) error = %v", err)
	}
	if !got.Equal(NewString("Ada")) {
		t.Fatalf("call_configure(instance configure) = %v, want Ada", got)
	}

	classValue, err := producer.Call(context.Background(), "export_class", nil, CallOptions{})
	if err != nil {
		t.Fatalf("export_class() error = %v", err)
	}
	classConfigure, err := exec.getMember(classValue, "configure", Position{})
	if err != nil {
		t.Fatalf("getMember(Person, configure) error = %v", err)
	}
	got, err = consumer.Call(context.Background(), "call_configure", []Value{cloneValueForHost(classConfigure)}, CallOptions{})
	if err != nil {
		t.Fatalf("call_configure(class configure) error = %v", err)
	}
	if !got.Equal(NewString("Ada")) {
		t.Fatalf("call_configure(class configure) = %v, want Ada", got)
	}
}
