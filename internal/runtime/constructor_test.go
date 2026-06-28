package runtime

import "testing"

func TestInitializeReturnValueIgnoredDuringConstruction(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
class ImplicitReturn
  def initialize() -> nil
    1
  end

  def value
    "implicit"
  end
end

class ExplicitReturn
  def initialize() -> nil
    return 1
  end

  def value
    "explicit"
  end
end

def implicit
  ImplicitReturn.new.value
end

def explicit
  ExplicitReturn.new.value
end
`)

	if got := callFunc(t, script, "implicit", nil); !got.Equal(NewString("implicit")) {
		t.Fatalf("implicit initialize return = %v, want implicit instance", got)
	}
	if got := callFunc(t, script, "explicit", nil); !got.Equal(NewString("explicit")) {
		t.Fatalf("explicit initialize return = %v, want explicit instance", got)
	}
}
