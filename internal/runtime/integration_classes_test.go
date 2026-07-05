package runtime

import (
	"context"
	"testing"
)

func TestClassPrivacyEnforced(t *testing.T) {
	t.Parallel()
	script := compileTestProgram(t, "classes/privacy.vibe")
	requireCallErrorContains(t, script, "violate", nil, CallOptions{}, "private method secret")
}

func TestPrivateMethodsRequireImplicitReceiver(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Helper
  private def secret
    42
  end

  private def secret=(value)
    @secret = value
  end

  def implicit_secret
    secret
  end

  def explicit_self_secret
    self.secret
  end

  def explicit_self_secret_set
    self.secret = 7
  end

  def explicit_self_secret_increment
    self.secret += 1
  end

  private def self.class_secret
    99
  end

  def self.implicit_class_secret
    class_secret
  end

  def self.explicit_self_class_secret
    self.class_secret
  end
end

def implicit_instance
  Helper.new.implicit_secret
end

def explicit_instance
  Helper.new.explicit_self_secret
end

def explicit_instance_setter
  Helper.new.explicit_self_secret_set
end

def explicit_instance_compound
  Helper.new.explicit_self_secret_increment
end

def implicit_class
  Helper.implicit_class_secret
end

def explicit_class
  Helper.explicit_self_class_secret
end

def external_private_class
  Helper.class_secret
end
`)

	if got := callFunc(t, script, "implicit_instance", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("implicit private instance call = %v, want 42", got)
	}
	if got := callFunc(t, script, "implicit_class", nil); !got.Equal(NewInt(99)) {
		t.Fatalf("implicit private class call = %v, want 99", got)
	}
	requireCallErrorContains(t, script, "explicit_instance", nil, CallOptions{}, "private method secret")
	requireCallErrorContains(t, script, "explicit_instance_setter", nil, CallOptions{}, "private method secret=")
	requireCallErrorContains(t, script, "explicit_instance_compound", nil, CallOptions{}, "private method secret")
	requireCallErrorContains(t, script, "explicit_class", nil, CallOptions{}, "private method class_secret")
	requireCallErrorContains(t, script, "external_private_class", nil, CallOptions{}, "private method class_secret")
}

func TestTypedAccessors(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class User
  property name: string
  getter age: int
  setter tag: string

  def initialize(name, age)
    @name = name
    @age = age
  end

  def set_tag(value)
    self.tag = value
  end

  def raw_tag
    @tag
  end
end

def good_name
  User.new("ada", 30).name
end

def good_age
  User.new("ada", 30).age
end

def good_tag
  u = User.new("ada", 30)
  u.tag = "vip"
  u.raw_tag
end

def bad_name_read
  User.new(1, 30).name
end

def bad_name_write
  u = User.new("ada", 30)
  u.name = 42
end

def bad_tag_write
  u = User.new("ada", 30)
  u.set_tag(99)
end
`)

	if got := callFunc(t, script, "good_name", nil); !got.Equal(NewString("ada")) {
		t.Fatalf("good_name = %v, want \"ada\"", got)
	}
	if got := callFunc(t, script, "good_age", nil); !got.Equal(NewInt(30)) {
		t.Fatalf("good_age = %v, want 30", got)
	}
	if got := callFunc(t, script, "good_tag", nil); !got.Equal(NewString("vip")) {
		t.Fatalf("good_tag = %v, want \"vip\"", got)
	}
	requireCallErrorContains(t, script, "bad_name_read", nil, CallOptions{}, "return value for name expected string")
	requireCallErrorContains(t, script, "bad_name_write", nil, CallOptions{}, "expected string")
	requireCallErrorContains(t, script, "bad_tag_write", nil, CallOptions{}, "expected string")
}

func TestClassErrorCases(t *testing.T) {
	t.Parallel()
	script := compileTestProgram(t, "errors/classes.vibe")

	requireCallErrorContains(t, script, "undefined_method", nil, CallOptions{}, "unknown")
	requireCallErrorContains(t, script, "private_method_external", nil, CallOptions{}, "private method")
	requireCallErrorContains(t, script, "write_to_readonly", nil, CallOptions{}, "read-only property")
	requireCallErrorContains(t, script, "wrong_init_args", nil, CallOptions{}, "argument")

	// run function should work
	val := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	if val.Kind() != KindHash {
		t.Fatalf("run: expected hash, got %v", val.Kind())
	}
	h := val.Hash()
	if h["counter"].Int() != 7 {
		t.Fatalf("run: counter mismatch: %v", h["counter"])
	}
	if h["readonly"].String() != "hello" {
		t.Fatalf("run: readonly mismatch: %v", h["readonly"])
	}
	if h["writeonly"].Int() != 99 {
		t.Fatalf("run: writeonly mismatch: %v", h["writeonly"])
	}
}

func TestClassPropertyAndNominalTypeAnnotations(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class User
  property name: string
  property friend: User

  def initialize(@name: string)
  end

  def corrupt_name
    @name = 1
    name
  end
end

def user_name(user: User) -> string
  user.name
end

def set_friend
  ada = User.new("Ada")
  lin = User.new("Lin")
  ada.friend = lin
  user_name(ada.friend)
end

def bad_name_setter
  user = User.new("Ada")
  user.name = 1
end

def bad_friend_setter
  user = User.new("Ada")
  user.friend = "Lin"
end

def bad_nominal_arg
  user_name("Ada")
end

def bad_getter_return
  User.new("Ada").corrupt_name
end
`)

	if got := callFunc(t, script, "set_friend", nil); !got.Equal(NewString("Lin")) {
		t.Fatalf("set_friend() = %#v, want Lin", got)
	}
	requireCallErrorContains(t, script, "bad_name_setter", nil, CallOptions{}, "argument value expected string, got int")
	requireCallErrorContains(t, script, "bad_friend_setter", nil, CallOptions{}, "argument value expected User, got string")
	requireCallErrorContains(t, script, "bad_nominal_arg", nil, CallOptions{}, "argument user expected User, got string")
	requireCallErrorContains(t, script, "bad_getter_return", nil, CallOptions{}, "return value for name expected string, got int")
}

func TestExactClassTypeWinsOverCaseInsensitiveEnumFallback(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
class User
  def label
    "User"
  end
end

enum user
  Draft
end

def accept(user: User)
  user.label
end

def run()
  accept(User.new)
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewString("User")) {
		t.Fatalf("run() = %#v, want User", got)
	}
}

func TestExactNamedTypeLookupRespectsLexicalScopeOrder(t *testing.T) {
	t.Parallel()

	enumDef, err := compileEnumDef(&EnumStmt{
		Name: "Status",
		Members: []EnumMemberStmt{
			{Name: "Draft"},
		},
	})
	if err != nil {
		t.Fatalf("compile enum: %v", err)
	}
	classDef := &ClassDef{
		Name:         "Status",
		Methods:      map[string]*ScriptFunction{},
		ClassMethods: map[string]*ScriptFunction{},
		ClassVars:    map[string]Value{},
	}

	outer := newEnv(nil)
	outer.DefineStatic("Status", NewEnum(enumDef))
	inner := newEnv(outer)
	inner.Define("Status", NewClass(classDef))

	match, ok, err := lookupNamedTypeForType(&TypeExpr{Kind: TypeEnum, Name: "Status"}, typeContext{
		env:      inner,
		fallback: inner,
	})
	if err != nil {
		t.Fatalf("lookup Status: %v", err)
	}
	if !ok || match.class != classDef || match.enum != nil {
		t.Fatalf("lookup Status = %#v, ok=%v; want inner class", match, ok)
	}
}

func TestCaseInsensitiveClassEnumTypeFallbackIsAmbiguous(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
class User
end

enum user
  Draft
end

def accept(value: USER)
  value
end

def run()
  accept(User.new)
end
`)

	requireCallErrorContains(t, script, "run", nil, CallOptions{}, "ambiguous type USER matches enum user, class User")
}
