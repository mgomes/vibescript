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
