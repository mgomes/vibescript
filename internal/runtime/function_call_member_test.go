package runtime

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestFunctionValueCall covers Ruby-style fn.call(...) on script function
// values, which must mirror direct fn(...) invocation including args,
// kwargs, and block forwarding.
func TestFunctionValueCall(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def inc(n)
      n + 1
    end

    def greet(name:)
      "hello " + name
    end

    def apply(x)
      yield(x)
    end

    def call_positional(fn)
      fn.call(2)
    end

    def direct_positional(fn)
      fn(2)
    end

    def call_keyword(fn)
      fn.call(name: "Ada")
    end

    def call_block(fn)
      fn.call(10) do |value|
        value * 3
      end
    end

    def run_positional
      call_positional(inc)
    end

    def run_direct
      direct_positional(inc)
    end

    def run_keyword
      call_keyword(greet)
    end

    def run_block
      call_block(apply)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "positional parity with direct call", fn: "run_positional", want: NewInt(3)},
		{name: "direct call control", fn: "run_direct", want: NewInt(3)},
		{name: "keyword forwarding", fn: "run_keyword", want: NewString("hello Ada")},
		{name: "block forwarding", fn: "run_block", want: NewInt(30)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

// TestFunctionValueCallZeroArityFollowsIssue416 documents that fn.call does
// not change the zero-arity behavior tracked by #416: a bare reference to a
// zero-arity function is still auto-invoked, so the callee receives the return
// value, not a function value.
func TestFunctionValueCallZeroArityFollowsIssue416(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def answer
      42
    end

    def receive(value)
      value
    end

    def run
      receive(answer)
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run() = %#v, want 42 (zero-arity function auto-invoked per #416)", got)
	}
}

func TestFunctionValueCallPreservesStaticZeroArityReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def answer
      42
    end

    def run
      answer.call()
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run() = %#v, want 42", got)
	}
}

func TestFunctionValueCallPreservesStaticZeroArityReceiverWithBlock(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def around
      yield
    end

    def run
      around.call do
        42
      end
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run() = %#v, want 42", got)
	}
}

func TestFunctionValueCallAutoInvokesStaticZeroArityFactoryReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class CallableBox
      def call(value)
        value + 1
      end
    end

    def maker
      CallableBox.new
    end

    def run
      maker.call(41)
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run() = %#v, want 42", got)
	}
}

func TestFunctionTypedCallMemberArgumentUsesCallReceiverAutoInvoke(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class CallableBox
      def call(value)
        value + 1
      end
    end

    def maker
      CallableBox.new
    end

    def take_member_call(fn: function)
      fn(41)
    end

    def run
      take_member_call(maker.call)
    end
    `)
	if got := callFunc(t, script, "run", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run() = %#v, want 42", got)
	}
}

func TestFunctionValueCallUsesStoredInstanceAndClassCallableData(t *testing.T) {
	t.Parallel()
	instanceScript := compileScript(t, `
    def answer
      42
    end

    class CallbackBox
      def initialize(@cb: function)
      end
    end

    def run_instance_ivar
      CallbackBox.new(answer).cb.call
    end
    `)
	if got := callFunc(t, instanceScript, "run_instance_ivar", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run_instance_ivar() = %#v, want 42", got)
	}

	classScript := compileScript(t, `
    def run_class_var
      CallbackBox.cb.call
    end
    `)
	callback := NewFunction(&ScriptFunction{
		Name: "callback",
		Body: []Statement{&ReturnStmt{
			Value:    &IntegerLiteral{Value: 43},
			Position: Position{Line: 1, Column: 1},
		}},
	})
	callbackClass := NewClass(&ClassDef{
		Name:         "CallbackBox",
		Methods:      map[string]*ScriptFunction{},
		ClassMethods: map[string]*ScriptFunction{},
		ClassVars:    map[string]Value{"cb": callback},
	})
	got, err := classScript.Call(context.Background(), "run_class_var", nil, CallOptions{
		Globals: map[string]Value{"CallbackBox": callbackClass},
	})
	if err != nil {
		t.Fatalf("run_class_var() error = %v, want nil", err)
	}
	if !got.Equal(NewInt(43)) {
		t.Fatalf("run_class_var() = %#v, want 43", got)
	}
}

func TestFunctionValueCallPreservesImplicitSelfStoredCallableData(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class CallbackBox
      def run
        cb.call
      end
    end

    def new_box
      CallbackBox.new()
    end

    def run_box(box)
      box.run
    end
    `)
	box := callFunc(t, script, "new_box", nil)
	valueInstance(box).Ivars["cb"] = NewFunction(&ScriptFunction{
		Name: "callback",
		Body: []Statement{&ReturnStmt{
			Value:    &IntegerLiteral{Value: 42},
			Position: Position{Line: 1, Column: 1},
		}},
	})
	if got := callFunc(t, script, "run_box", []Value{box}); !got.Equal(NewInt(42)) {
		t.Fatalf("run_box() = %#v, want 42", got)
	}
}

func TestZeroArityFunctionValuePreservedForFunctionTypedArguments(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def answer
      42
    end

    def fallback
      43
    end

    class CallbackBox
      @@last = nil
      property cb: function

      def initialize
        @@last = self
      end

      def self.last() -> CallbackBox
        @@last
      end

      def self.make() -> CallbackBox
        @@last = CallbackBox.new()
      end
    end

    class IntBox
      property value: int
    end

    class AssignmentHarness
      def switch_assignment_box
        @assignment_box = @assignment_second
        7
      end

      def run
        @assignment_first = IntBox.new()
        @assignment_second = IntBox.new()
        @assignment_box = @assignment_first
        @assignment_box.value = switch_assignment_box()
        @assignment_second.value
      end
    end

    def receive_untyped(value)
      value
    end

    def receive_callable(fn: function)
      fn.call
    end

    def receive_callable_paren(fn: function)
      fn.call()
    end

    def receive_callable_respond_to(fn: function)
      fn.respond_to?(:call)
    end

    def receive_callable_not_equal_result(fn: function)
      fn.equal?(answer)
    end

    def receive_callable_rest(*fns: array<function>)
      fns[0].call
    end

    def receive_callable_rest_union(*fns: array<function> | nil)
      fns[0].call
    end

    def receive_rest_array_or_function(*values: array<int> | function)
      values[0]
    end

    def receive_untyped_rest_array_or_function(*values: array | function)
      values[0]
    end

    def receive_handlers(fns: array<function>)
      fns[0].call
    end

    def receive_callable_shape(opts: { cb: function })
      opts[:cb].call
    end

    def receive_callable_shape_dot(opts: { cb: function })
      opts.cb.call()
    end

    def receive_callable_keyword_shape(opts: { cb: function })
      opts[:cb].call
    end

    def receive_callable_keyword_shape_dot(opts: { cb: function })
      opts.cb.call()
    end

    def receive_keyword_rest_object_or_function(**opts: object | function)
      opts[:cb]
    end

    def receive_nested_callable(containers: array<{ cb: function }>)
      containers[0][:cb].call
    end

    def receive_nested_callable_dot(containers: array<{ cb: function }>)
      containers[0].cb.call()
    end

    def receive_default_callable(fn: function = answer)
      fn.call
    end

    def receive_default_handlers(fns: array<function> = [answer])
      fns[0].call
    end

    def receive_default_shape(opts: { cb: function } = { cb: answer })
      opts[:cb].call
    end

    def receive_default_nested(containers: array<{ cb: function }> = [{ cb: answer }])
      containers[0][:cb].call
    end

    def make_handlers(*fns: array<function>) -> array<function>
      fns
    end

    def handlers
      make_handlers(answer)
    end

    def feed_block(&block)
      block.call(answer)
    end

    def feed_destructured_block(&block)
      block.call([answer])
    end

    def feed_scalar_destructured_block(&block)
      block.call(answer)
    end

    def feed_nested_destructured_block(&block)
      block.call([[answer]])
    end

    def feed_destructured_rest_block(&block)
      block.call([1, answer, 0])
    end

    def feed_splatted_array_block(&block)
      block.call([1, answer])
    end

    def yield_block
      yield(answer)
    end

    def yield_destructured_block
      yield(answer)
    end

    def yield_splatted_array_block
      yield([1, answer])
    end

    def yield_shape_block
      yield({ cb: answer })
    end

    def run_untyped
      receive_untyped(answer)
    end

    def run_typed
      receive_callable(answer)
    end

    def run_typed_paren
      receive_callable_paren(answer)
    end

    def run_typed_respond_to
      receive_callable_respond_to(answer)
    end

    def run_typed_not_equal_result
      receive_callable_not_equal_result(answer)
    end

    def run_call_alias
      receive_callable.call(answer)
    end

    def run_conditional_branch(flag)
      receive_callable(flag ? answer : fallback)
    end

    def run_conditional_branch_true
      run_conditional_branch(true)
    end

    def run_if_branch(flag)
      receive_callable(if flag then answer else fallback end)
    end

    def run_if_branch_true
      run_if_branch(true)
    end

    def run_case_branch(flag)
      receive_callable(case flag when true then answer else fallback end)
    end

    def run_case_branch_true
      run_case_branch(true)
    end

    def run_case_branch_false
      run_case_branch(false)
    end

    def run_rest
      receive_callable_rest(answer)
    end

    def run_rest_union
      receive_callable_rest_union(answer)
    end

    def run_rest_array_or_function
      receive_rest_array_or_function(answer)
    end

    def run_untyped_rest_array_or_function
      receive_untyped_rest_array_or_function(answer)
    end

    def run_array_factory
      receive_handlers(handlers)
    end

    def run_array_literal
      receive_handlers([answer])
    end

    def run_shape_literal
      receive_callable_shape({ cb: answer })
    end

    def run_shape_literal_dot
      receive_callable_shape_dot({ cb: answer })
    end

    def run_keyword_shape_literal
      receive_callable_keyword_shape(opts: { cb: answer })
    end

    def run_keyword_shape_literal_dot
      receive_callable_keyword_shape_dot(opts: { cb: answer })
    end

    def run_keyword_rest_object_or_function
      receive_keyword_rest_object_or_function(cb: answer)
    end

    def run_nested_literal
      receive_nested_callable([{ cb: answer }])
    end

    def run_nested_literal_dot
      receive_nested_callable_dot([{ cb: answer }])
    end

    def callable_box
      box = CallbackBox.new()
      box.cb = answer
      box
    end

    def run_property_setter
      box = callable_box()
      box.cb.call()
    end

    def run_property_setter_conditional(flag)
      box = CallbackBox.new()
      box.cb = flag ? answer : fallback
      box.cb.call()
    end

    def run_property_setter_conditional_true
      run_property_setter_conditional(true)
    end

    def run_property_setter_conditional_false
      run_property_setter_conditional(false)
    end

    def run_property_setter_if(flag)
      box = CallbackBox.new()
      box.cb = if flag then answer else fallback end
      box.cb.call()
    end

    def run_property_setter_if_true
      run_property_setter_if(true)
    end

    def run_property_setter_if_false
      run_property_setter_if(false)
    end

    def run_property_setter_case(flag)
      box = CallbackBox.new()
      box.cb = case flag when true then answer else fallback end
      box.cb.call()
    end

    def run_property_setter_case_true
      run_property_setter_case(true)
    end

    def run_property_setter_case_false
      run_property_setter_case(false)
    end

    def run_constructor_property_setter
      CallbackBox.new().cb = answer
      box = CallbackBox.last()
      box.cb.call()
    end

    def run_static_factory_property_setter
      CallbackBox.make().cb = answer
      box = CallbackBox.last()
      box.cb.call()
    end

    def make_callback_box() -> CallbackBox
      CallbackBox.new()
    end

    def run_bare_factory_property_setter
      make_callback_box.cb = answer
      box = CallbackBox.last()
      box.cb.call()
    end

    def run_member_assignment_rhs_order
      AssignmentHarness.new().run()
    end

    def run_property_getter_argument
      box = callable_box()
      receive_callable(box.cb)
    end

    def run_property_getter_array_literal
      box = callable_box()
      receive_handlers([box.cb])
    end

    def run_property_getter_shape_literal
      box = callable_box()
      receive_callable_shape({ cb: box.cb })
    end

    def run_block_param
      feed_block do |fn: function|
        fn.call
      end
    end

    def run_destructured_block_param
      feed_destructured_block do |(fn: function)|
        fn.call
      end
    end

    def run_scalar_destructured_block_param
      feed_scalar_destructured_block do |(fn: function)|
        fn.call
      end
    end

    def run_nested_destructured_block_param
      feed_nested_destructured_block do |((fn: function))|
        fn.call
      end
    end

    def run_destructured_block_rest_param
      feed_destructured_rest_block do |(head: int, *fns: array<function>, tail: int)|
        fns[0].call + head + tail
      end
    end

    def run_block_splatted_array_block_param
      feed_splatted_array_block do |head: int, fn: function|
        fn.call + head
      end
    end

    def run_yield_block_param
      yield_block do |fn: function|
        fn.call
      end
    end

    def run_yield_destructured_block_param
      yield_destructured_block do |(fn: function)|
        fn.call
      end
    end

    def run_yield_splatted_array_block_param
      yield_splatted_array_block do |head: int, fn: function|
        fn.call + head
      end
    end

    def run_yield_shape_block_param
      yield_shape_block do |opts: { cb: function }|
        opts[:cb].call
      end
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "untyped still auto invokes", fn: "run_untyped", want: NewInt(42)},
		{name: "typed positional keeps callable", fn: "run_typed", want: NewInt(42)},
		{name: "typed positional call member keeps callable receiver", fn: "run_typed_paren", want: NewInt(42)},
		{name: "typed callable receiver supports introspection", fn: "run_typed_respond_to", want: NewBool(true)},
		{name: "typed callable receiver stays distinct from result", fn: "run_typed_not_equal_result", want: NewBool(false)},
		{name: "function call alias keeps callable", fn: "run_call_alias", want: NewInt(42)},
		{name: "conditional branch keeps callable", fn: "run_conditional_branch_true", want: NewInt(42)},
		{name: "if branch keeps callable", fn: "run_if_branch_true", want: NewInt(42)},
		{name: "case branch keeps callable", fn: "run_case_branch_true", want: NewInt(42)},
		{name: "case fallback keeps callable", fn: "run_case_branch_false", want: NewInt(43)},
		{name: "typed rest keeps callable element", fn: "run_rest", want: NewInt(42)},
		{name: "union typed rest keeps callable element", fn: "run_rest_union", want: NewInt(42)},
		{name: "rest union ignores impossible callable arm", fn: "run_rest_array_or_function", want: NewInt(42)},
		{name: "untyped rest union ignores impossible callable arm", fn: "run_untyped_rest_array_or_function", want: NewInt(42)},
		{name: "array typed argument still auto invokes outer function", fn: "run_array_factory", want: NewInt(42)},
		{name: "array literal keeps callable elements", fn: "run_array_literal", want: NewInt(42)},
		{name: "shape literal keeps callable fields", fn: "run_shape_literal", want: NewInt(42)},
		{name: "shape literal dot-call keeps callable fields", fn: "run_shape_literal_dot", want: NewInt(42)},
		{name: "keyword shape literal keeps callable fields", fn: "run_keyword_shape_literal", want: NewInt(42)},
		{name: "keyword shape literal dot-call keeps callable fields", fn: "run_keyword_shape_literal_dot", want: NewInt(42)},
		{name: "keyword rest union ignores impossible callable arm", fn: "run_keyword_rest_object_or_function", want: NewInt(42)},
		{name: "nested typed literal keeps callable fields", fn: "run_nested_literal", want: NewInt(42)},
		{name: "nested typed literal dot-call keeps callable fields", fn: "run_nested_literal_dot", want: NewInt(42)},
		{name: "property setter stores callable", fn: "run_property_setter", want: NewInt(42)},
		{name: "conditional property setter stores true callable", fn: "run_property_setter_conditional_true", want: NewInt(42)},
		{name: "conditional property setter stores false callable", fn: "run_property_setter_conditional_false", want: NewInt(43)},
		{name: "if property setter stores true callable", fn: "run_property_setter_if_true", want: NewInt(42)},
		{name: "if property setter stores false callable", fn: "run_property_setter_if_false", want: NewInt(43)},
		{name: "case property setter stores true callable", fn: "run_property_setter_case_true", want: NewInt(42)},
		{name: "case property setter stores false callable", fn: "run_property_setter_case_false", want: NewInt(43)},
		{name: "constructor property setter stores callable", fn: "run_constructor_property_setter", want: NewInt(42)},
		{name: "static factory property setter stores callable", fn: "run_static_factory_property_setter", want: NewInt(42)},
		{name: "bare factory property setter stores callable", fn: "run_bare_factory_property_setter", want: NewInt(42)},
		{name: "member assignment preserves rhs-before-target order", fn: "run_member_assignment_rhs_order", want: NewInt(7)},
		{name: "property getter callable argument reads stored callable", fn: "run_property_getter_argument", want: NewInt(42)},
		{name: "property getter array literal reads stored callable", fn: "run_property_getter_array_literal", want: NewInt(42)},
		{name: "property getter shape literal reads stored callable", fn: "run_property_getter_shape_literal", want: NewInt(42)},
		{name: "typed callable default keeps callable", fn: "receive_default_callable", want: NewInt(42)},
		{name: "typed array default keeps callable elements", fn: "receive_default_handlers", want: NewInt(42)},
		{name: "typed shape default keeps callable fields", fn: "receive_default_shape", want: NewInt(42)},
		{name: "nested typed default keeps callable fields", fn: "receive_default_nested", want: NewInt(42)},
		{name: "typed block call parameter keeps callable", fn: "run_block_param", want: NewInt(42)},
		{name: "typed destructured block parameter keeps callable array element", fn: "run_destructured_block_param", want: NewInt(42)},
		{name: "typed destructured block parameter keeps scalar callable", fn: "run_scalar_destructured_block_param", want: NewInt(42)},
		{name: "nested typed destructured block parameter keeps callable", fn: "run_nested_destructured_block_param", want: NewInt(42)},
		{name: "typed destructured block rest parameter keeps callable", fn: "run_destructured_block_rest_param", want: NewInt(43)},
		{name: "block call splatted array keeps callable element", fn: "run_block_splatted_array_block_param", want: NewInt(43)},
		{name: "yield typed block parameter keeps callable", fn: "run_yield_block_param", want: NewInt(42)},
		{name: "yield typed destructured block parameter keeps callable", fn: "run_yield_destructured_block_param", want: NewInt(42)},
		{name: "yield splatted array keeps callable element", fn: "run_yield_splatted_array_block_param", want: NewInt(43)},
		{name: "yield typed shape block parameter keeps callable field", fn: "run_yield_shape_block_param", want: NewInt(42)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
	if got := callFunc(t, script, "run_conditional_branch", []Value{NewBool(false)}); !got.Equal(NewInt(43)) {
		t.Fatalf("run_conditional_branch(false) = %#v, want 43", got)
	}
	if got := callFunc(t, script, "run_if_branch", []Value{NewBool(false)}); !got.Equal(NewInt(43)) {
		t.Fatalf("run_if_branch(false) = %#v, want 43", got)
	}
}

func TestBlockUniversalPredicateArgumentsDoNotUseBlockParamTypes(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def explode
      raise "ordinary equality argument auto-invoked"
    end

    def compare_block(&block)
      block.equal?(explode)
    end

    def run
      compare_block do |fn: function|
        fn.call
      end
    end
    `)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "ordinary equality argument auto-invoked")
}

func TestFunctionTypedMemberArgumentEvaluatesReceiverOnce(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    class Holder
      def cb
        42
      end
    end

    class Factory
      property calls

      def initialize
        @calls = 0
      end

      def make
        @calls = @calls + 1
        Holder.new()
      end
    end

    def take(fn: function)
      99
    end

    def run
      factory = Factory.new()
      value = take(factory.make.cb)
      [value, factory.calls]
    end
    `)

	compareArrays(t, callFunc(t, script, "run", nil), []Value{NewInt(99), NewInt(1)})
}

func TestFunctionTypedMemberLogicalAssignmentUsesSetterExpectation(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def answer
      42
    end

    def fallback
      43
    end

    class CallbackBox
      property cb: function | nil
    end

    def invoke(fn: function)
      fn.call
    end

    def run_or_assignment
      box = CallbackBox.new()
      box.cb ||= answer
      invoke(box.cb)
    end

    def run_and_assignment
      box = CallbackBox.new()
      box.cb = fallback
      box.cb &&= answer
      invoke(box.cb)
    end
    `)

	if got := callFunc(t, script, "run_or_assignment", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run_or_assignment() = %#v, want 42", got)
	}
	if got := callFunc(t, script, "run_and_assignment", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("run_and_assignment() = %#v, want 42", got)
	}
}

func TestTypedConditionalCallArgumentsChargeExpressionStep(t *testing.T) {
	t.Parallel()

	pos := Position{Line: 1, Column: 1}
	tests := []struct {
		name string
		expr Expression
	}{
		{
			name: "conditional",
			expr: &ConditionalExpr{
				Condition:  &BoolLiteral{Value: true, Position: pos},
				Consequent: &IntegerLiteral{Value: 1, Position: pos},
				Alternate:  &IntegerLiteral{Value: 2, Position: pos},
				Position:   pos,
			},
		},
		{
			name: "if",
			expr: &IfExpr{
				Condition:  &BoolLiteral{Value: true, Position: pos},
				Consequent: &IntegerLiteral{Value: 1, Position: pos},
				Alternate:  &IntegerLiteral{Value: 2, Position: pos},
				Position:   pos,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exec := &Execution{}
			got, err := exec.evalCallArgumentForExpectation(tt.expr, newEnv(nil), typeExpressionExpectation(&TypeExpr{Kind: TypeAny}))
			if err != nil {
				t.Fatalf("evalCallArgumentForExpectation(%s) error = %v, want nil", tt.name, err)
			}
			if !got.Equal(NewInt(1)) {
				t.Fatalf("evalCallArgumentForExpectation(%s) = %#v, want 1", tt.name, got)
			}
			if exec.steps != 3 {
				t.Fatalf("evalCallArgumentForExpectation(%s) steps = %d, want 3", tt.name, exec.steps)
			}
		})
	}
}

func TestCapturedBlockValuesAreCallable(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def invoke_direct(&block)
      block(2)
    end

    def invoke_call(&block)
      block.call(3)
    end

    def accept_typed_block(&block: function)
      yield(4)
    end

    def forward_typed_block(&block: function)
      require_callable(block)
    end

    def require_callable(fn: function)
      fn.call(5)
    end

    def run_direct
      invoke_direct do |value|
        value + 1
      end
    end

    def run_call
      invoke_call do |value|
        value * 2
      end
    end

    def run_typed_block
      accept_typed_block do |value|
        value + 6
      end
    end

    def run_forwarded_block
      forward_typed_block do |value|
        value + 7
      end
    end

    def run_respond_to
      capture_respond_to do
        1
      end
    end

    def capture_respond_to(&block)
      block.respond_to?(:call)
    end
    `)

	tests := []struct {
		name string
		fn   string
		want Value
	}{
		{name: "direct call", fn: "run_direct", want: NewInt(3)},
		{name: "call member", fn: "run_call", want: NewInt(6)},
		{name: "typed block parameter", fn: "run_typed_block", want: NewInt(10)},
		{name: "block value satisfies function type", fn: "run_forwarded_block", want: NewInt(12)},
		{name: "respond_to call", fn: "run_respond_to", want: NewBool(true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := callFunc(t, script, tt.fn, nil); !got.Equal(tt.want) {
				t.Fatalf("%s() = %#v, want %#v", tt.fn, got, tt.want)
			}
		})
	}
}

func TestShapeTypedKeywordRestPreservesCallableFields(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def answer
      42
    end

    def label
      "ok"
    end

    def accept(**opts: { cb: function, label: string })
      [opts[:cb].call, opts[:label]]
    end

    def run
      accept(cb: answer, label: label)
    end
    `)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{NewInt(42), NewString("ok")})
}

// TestFunctionValueCallErrors verifies that misuse of fn.call surfaces the
// same argument and type errors as direct invocation, anchored at the call
// site, and that unknown members suggest call.
func TestFunctionValueCallErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		source string
		errMsg string
	}{
		{
			name: "too many positional arguments",
			source: `
        def inc(n)
          n + 1
        end

        def run(fn)
          fn.call(1, 2)
        end
      `,
			errMsg: "unexpected positional arguments",
		},
		{
			name: "argument type mismatch",
			source: `
        def inc(n: int)
          n + 1
        end

        def run(fn)
          fn.call("x")
        end
      `,
			errMsg: "argument n expected int, got string",
		},
		{
			name: "missing required keyword",
			source: `
        def greet(name:)
          "hello " + name
        end

        def run(fn)
          fn.call()
        end
      `,
			errMsg: "missing keyword argument name",
		},
		{
			name: "unknown member suggests call",
			source: `
        def inc(n)
          n + 1
        end

        def run(fn)
          fn.cll(1)
        end
      `,
			errMsg: `unknown member cll (did you mean "call"?)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", []Value{exportedFunctionValue(t, script, "inc", "greet")}, CallOptions{}, tt.errMsg)
		})
	}
}

func TestSingleNormalArgMemberCallBindsTypesAndIvarParams(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    class Recorder
      def store(@value: int)
        @value
      end

      def typed(value: int)
        value + 1
      end

      def value
        @value
      end
    end

    def run
      rec = Recorder.new()
      stored = rec.store(41)
      [stored, rec.value, rec.typed(1)]
    end

    def bad_type
      Recorder.new().typed("nope")
    end
    `)

	got := callFunc(t, script, "run", nil)
	want := NewArray([]Value{NewInt(41), NewInt(41), NewInt(2)})
	if !got.Equal(want) {
		t.Fatalf("run() = %#v, want %#v", got, want)
	}
	requireCallErrorContains(t, script, "bad_type", nil, CallOptions{}, "argument value expected int, got string")
}

// exportedFunctionValue resolves the first of the named functions defined in
// the script to a function value, so error-path tests can pass it straight
// to a run helper without an extra wrapper.
func exportedFunctionValue(t *testing.T, script *Script, names ...string) Value {
	t.Helper()
	for _, name := range names {
		if fn, ok := script.functions[name]; ok {
			return NewFunction(fn)
		}
	}
	t.Fatalf("none of %v defined in script", names)
	return NewNil()
}

// TestFunctionValueCallMemberSuggestion confirms the function member list is
// wired into editor completion metadata. The list carries the function-specific
// call member alongside the universal Object-level helpers (itself, nil?, eql?,
// equal?, tap, yield_self) and the introspection predicates (respond_to?, is_a?,
// kind_of?, instance_of?) exposed on every value kind.
func TestFunctionValueCallMemberSuggestion(t *testing.T) {
	t.Parallel()
	names, ok := MemberCompletionNames()["function"]
	if !ok {
		t.Fatalf("MemberCompletionNames missing function entry")
	}
	want := append([]string{"call"}, universalMemberNames...)
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("function member completion mismatch (-want +got):\n%s", diff)
	}
}

func TestBlockValueCallMemberSuggestion(t *testing.T) {
	t.Parallel()
	names, ok := MemberCompletionNames()["block"]
	if !ok {
		t.Fatalf("MemberCompletionNames missing block entry")
	}
	want := append([]string{"call"}, universalMemberNames...)
	if diff := cmp.Diff(want, names); diff != "" {
		t.Fatalf("block member completion mismatch (-want +got):\n%s", diff)
	}
}
