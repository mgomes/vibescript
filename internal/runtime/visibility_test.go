package runtime

import "testing"

func TestVisibilitySectionsAreSticky(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Secret
  private

  def hidden
    "hidden"
  end

  def also_hidden
    "also hidden"
  end

  public

  def shown
    hidden + " via " + also_hidden
  end
end

def show
  Secret.new.shown
end

def call_hidden
  Secret.new.hidden
end

def call_also_hidden
  Secret.new.also_hidden
end
`)

	if got := callFunc(t, script, "show", nil); !got.Equal(NewString("hidden via also hidden")) {
		t.Fatalf("show = %v", got)
	}
	requireCallErrorContains(t, script, "call_hidden", nil, CallOptions{}, "private method hidden")
	requireCallErrorContains(t, script, "call_also_hidden", nil, CallOptions{}, "private method also_hidden")
}

func TestVisibilitySymbolDirectives(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Secret
  def hidden
    "hidden"
  end

  private :hidden

  private

  def reopened
    "reopened"
  end

  public :reopened
end

def call_hidden
  Secret.new.hidden
end

def call_reopened
  Secret.new.reopened
end
`)

	requireCallErrorContains(t, script, "call_hidden", nil, CallOptions{}, "private method hidden")
	if got := callFunc(t, script, "call_reopened", nil); !got.Equal(NewString("reopened")) {
		t.Fatalf("call_reopened = %v", got)
	}
}

func TestVisibilitySymbolDirectiveUnknownMethod(t *testing.T) {
	t.Parallel()
	requireCompileErrorContainsDefault(t, `
class Secret
  private :missing
end
`, "private target method missing is not defined on class Secret")
}

func TestProtectedInstanceMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Account
  def initialize(balance)
    @balance = balance
  end

  def richer_than?(other)
    balance > other.balance
  end

  def self_balance
    self.balance
  end

  protected

  def balance
    @balance
  end
end

class Snoop
  def peek(account)
    account.balance
  end
end

def compare
  Account.new(10).richer_than?(Account.new(3))
end

def explicit_self
  Account.new(7).self_balance
end

def external
  Account.new(10).balance
end

def other_class
  Snoop.new.peek(Account.new(10))
end

def probe
  a = Account.new(1)
  [a.respond_to?("balance"), a.respond_to?("balance", true)]
end
`)

	if got := callFunc(t, script, "compare", nil); !got.Equal(NewBool(true)) {
		t.Fatalf("compare = %v, want true", got)
	}
	if got := callFunc(t, script, "explicit_self", nil); !got.Equal(NewInt(7)) {
		t.Fatalf("explicit_self = %v, want 7", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "protected method balance")
	requireCallErrorContains(t, script, "other_class", nil, CallOptions{}, "protected method balance")
	probe := callFunc(t, script, "probe", nil).Array()
	if !probe[0].Equal(NewBool(false)) || !probe[1].Equal(NewBool(true)) {
		t.Fatalf("respond_to? probe = %v, want [false, true]", probe)
	}
}

func TestProtectedClassMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Vault
  def self.open_with_key
    self.combination
  end

  protected def self.combination
    "1234"
  end
end

def sanctioned
  Vault.open_with_key
end

def external
  Vault.combination
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewString("1234")) {
		t.Fatalf("sanctioned = %v", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "protected method combination")
}

func TestProtectedOperatorAndIndexMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Bag
  def initialize(items)
    @items = items
  end

  def merged_with(other)
    Bag.new(@items + (other + []))
  end

  def first_of(other)
    other[0]
  end

  def size
    @items.size
  end

  protected

  def +(extra)
    @items + extra
  end

  def [](index)
    @items[index]
  end
end

def sanctioned
  Bag.new([1]).merged_with(Bag.new([2, 3])).size
end

def sanctioned_index
  Bag.new([1]).first_of(Bag.new([9, 8]))
end

def external_plus
  Bag.new([1]) + [2]
end

def external_index
  Bag.new([1])[0]
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewInt(3)) {
		t.Fatalf("sanctioned = %v, want 3", got)
	}
	if got := callFunc(t, script, "sanctioned_index", nil); !got.Equal(NewInt(9)) {
		t.Fatalf("sanctioned_index = %v, want 9", got)
	}
	requireCallErrorContains(t, script, "external_plus", nil, CallOptions{}, "protected method +")
	requireCallErrorContains(t, script, "external_index", nil, CallOptions{}, "protected method []")
}

func TestProtectedSetterMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Account
  def initialize
    @balance = 0
  end

  def transfer_to(other, amount)
    other.balance = amount
    other.balance
  end

  protected

  def balance
    @balance
  end

  def balance=(value)
    @balance = value
  end
end

def sanctioned
  Account.new.transfer_to(Account.new, 25)
end

def external
  a = Account.new
  a.balance = 99
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewInt(25)) {
		t.Fatalf("sanctioned = %v, want 25", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "protected method balance=")
}

func TestVisibilityOnAccessorDeclarations(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Token
  private property secret: string

  def initialize(secret)
    @secret = secret
  end

  def reveal
    secret
  end
end

def sanctioned
  Token.new("s3cr3t").reveal
end

def external
  Token.new("s3cr3t").secret
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewString("s3cr3t")) {
		t.Fatalf("sanctioned = %v", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "private method secret")
}

func TestVisibilitySectionCoversClassMethods(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Factory
  def self.build
    seed
  end

  private

  def self.seed
    41 + 1
  end
end

def sanctioned
  Factory.build
end

def external
  Factory.seed
end
`)

	if got := callFunc(t, script, "sanctioned", nil); !got.Equal(NewInt(42)) {
		t.Fatalf("sanctioned = %v, want 42", got)
	}
	requireCallErrorContains(t, script, "external", nil, CallOptions{}, "private method seed")
}

func TestVisibilityLocalReadIsNotASectionDirective(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
class Config
  protected = 5
  protected

  def shown
    "visible"
  end
end

def run
  Config.new.shown
end
`)

	if got := callFunc(t, script, "run", nil); !got.Equal(NewString("visible")) {
		t.Fatalf("run = %v, want visible", got)
	}
}

func TestVisibilityDirectiveCollidesWithScriptFunction(t *testing.T) {
	t.Parallel()
	want := "protected in class Acct is a visibility directive, but this script also defines a function named protected"

	requireCompileErrorContainsDefault(t, `
def protected(name)
  name
end

class Acct
  def b
    1
  end

  protected :b
end
`, want)

	requireCompileErrorContainsDefault(t, `
class Acct
  protected

  def b
    1
  end
end

def protected(name)
  name
end
`, want)

	requireCompileErrorContainsDefault(t, `
def protected(name)
  name
end

class Acct
  protected def b
    1
  end
end
`, want)

	requireCompileErrorContainsDefault(t, `
def public(name)
  name
end

class Acct
  def b
    1
  end

  public :b
end
`, "public in class Acct is a visibility directive, but this script also defines a function named public")
}

func TestParenthesizedVisibilityCallStaysACall(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
def public(name)
  "called with #{name}"
end

class Acct
  def b
    "b-result"
  end

  RESULT = public(:b)
end

def run
  [Acct::RESULT, Acct.new.b]
end
`)

	got := callFunc(t, script, "run", nil).Array()
	if !got[0].Equal(NewString("called with b")) {
		t.Fatalf("class body call = %v, want the user function result", got[0])
	}
	if !got[1].Equal(NewString("b-result")) {
		t.Fatalf("b = %v, want it to stay publicly callable", got[1])
	}
}
