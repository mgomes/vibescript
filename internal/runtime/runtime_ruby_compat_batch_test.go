package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func rubyBatchArray(values ...Value) Value {
	return NewArray(values)
}

func requireRubyBatchValue(t *testing.T, got, want Value) {
	t.Helper()
	if diff := valueDiff(want, got); diff != "" {
		t.Fatalf("value mismatch (-want +got):\n%s", diff)
	}
}

func rubyBatchDistinctStrings(count int) Value {
	values := make([]Value, count)
	for i := range count {
		values[i] = NewString(fmt.Sprintf("v%04d", i))
	}
	return NewArray(values)
}

func rubyBatchConcatStringBlock(suffix string) Value {
	pos := Position{Line: 1, Column: 1}
	target := &Identifier{Name: "item", Position: pos}
	body := []Statement{
		&ExprStmt{
			Expr: &BinaryExpr{
				Left:     &Identifier{Name: "item", Position: pos},
				Operator: tokenPlus,
				Right:    &StringLiteral{Value: suffix, Position: pos},
				Position: pos,
			},
			Position: pos,
		},
	}
	return NewBlock([]Param{{Kind: ParamNormal, Name: "item", Target: target}}, body, newEnv(nil))
}

func TestRubyBatchDynamicDispatchAndInitializePrivacy(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
class Account
  def initialize(name)
    @name = name
  end

  def name
    @name
  end

  def call_secret(prefix)
    send(:secret, prefix)
  end

  private def secret(prefix)
    prefix + @name
  end
end

def dispatch()
  account = Account.new("Ada")
  [
    account.send(:name),
    account.send("secret", "hi "),
    account.public_send("name"),
    account.call_secret("in "),
    [1, 2].send(:map) do |n|
      n * 2
    end,
    account.respond_to?(:initialize),
    account.respond_to?(:initialize, true)
  ]
end

def public_send_private()
  Account.new("Ada").public_send(:secret, "hi ")
end

def call_initialize()
  Account.new("Ada").initialize("Grace")
end
`)

	got := callFunc(t, script, "dispatch", nil)
	requireRubyBatchValue(t, got, rubyBatchArray(
		NewString("Ada"),
		NewString("hi Ada"),
		NewString("Ada"),
		NewString("in Ada"),
		rubyBatchArray(NewInt(2), NewInt(4)),
		NewBool(false),
		NewBool(true),
	))

	requireCallErrorContains(t, script, "public_send_private", nil, CallOptions{}, "private method secret")
	requireCallErrorContains(t, script, "call_initialize", nil, CallOptions{}, "private method initialize")
}

func TestRubyBatchComparableBetween(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  [
    3.between?(1, 3),
    0.between?(1, 3),
    2.5.between?(2.0, 3.0),
    "b".between?("a", "c"),
    2.seconds.between?(1.seconds, 3.seconds),
    Time.utc(2024, 1, 2).between?(Time.utc(2024, 1, 1), Time.utc(2024, 1, 3)),
    money("2.00 USD").between?(money("1.00 USD"), money("3.00 USD"))
  ]
end

def bad()
  1.between?("a", "z")
end
`)

	got := callFunc(t, script, "run", nil)
	compareArrays(t, got, []Value{
		NewBool(true),
		NewBool(false),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
	})
	requireCallErrorContains(t, script, "bad", nil, CallOptions{}, "unsupported comparison operands")
}

func TestRubyBatchCollectionFiltersAndUniqBlock(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  {
    array_clear: [1, 2, 3].clear,
    array_delete_if: [1, 2, 3, 4].delete_if do |n|
      n % 2 == 0
    end,
    array_keep_if: [1, 2, 3, 4].keep_if do |n|
      n > 2
    end,
    hash_clear: { a: 1, b: 2 }.clear,
    hash_delete_if: { a: 1, b: 2, c: 3 }.delete_if do |key, value|
      value == 2
    end,
    hash_keep_if: { a: 1, b: 2, c: 3 }.keep_if do |key, value|
      key == :a || value == 3
    end,
    uniq_block: ["a", "A", "b", "B"].uniq do |word|
      word.downcase
    end
  }
end
`)

	result := callFunc(t, script, "run", nil)
	if result.Kind() != KindHash {
		t.Fatalf("run kind = %v, want hash", result.Kind())
	}
	got := result.Hash()

	compareArrays(t, got["array_clear"], []Value{})
	compareArrays(t, got["array_delete_if"], []Value{NewInt(1), NewInt(3)})
	compareArrays(t, got["array_keep_if"], []Value{NewInt(3), NewInt(4)})
	compareHash(t, got["hash_clear"].Hash(), map[string]Value{})
	compareHash(t, got["hash_delete_if"].Hash(), map[string]Value{
		"a": NewInt(1),
		"c": NewInt(3),
	})
	compareHash(t, got["hash_keep_if"].Hash(), map[string]Value{
		"a": NewInt(1),
		"c": NewInt(3),
	})
	compareArrays(t, got["uniq_block"], []Value{NewString("a"), NewString("b")})
}

func TestRubyBatchArrayUniqBlockChargesRetainedKeys(t *testing.T) {
	t.Parallel()

	const receiverSize = 1024
	const retainedKeyBytes = 512

	receiver := rubyBatchDistinctStrings(receiverSize)
	block := rubyBatchConcatStringBlock(strings.Repeat("x", retainedKeyBytes))

	exec := &Execution{
		ctx:   context.Background(),
		quota: 1 << 30,
	}
	base := exec.estimateMemoryUsageForCallRoots(NewNil(), receiver, nil, nil, block)
	resultSlots := estimatedValueBytes + estimatedSliceBaseBytes + receiverSize*estimatedValueBytes
	oneKey := newMemoryEstimator().value(NewString("v0000" + strings.Repeat("x", retainedKeyBytes)))
	exec.memoryQuota = base + resultSlots + valueSetScratchBytesForCounts(8, 0) + oneKey*8

	_, err := arrayUniq(exec, receiver, nil, nil, block, "array.uniq")
	requireErrorIs(t, err, errMemoryQuotaExceeded)
	if exec.steps >= receiverSize {
		t.Fatalf("steps = %d, want retained uniq keys to trip memory quota before traversing %d elements", exec.steps, receiverSize)
	}
}

func TestRubyBatchArrayBangTransforms(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  [
    [1, nil, 2].compact!,
    [1, 2].compact!,
    [1, 1, 2].uniq!,
    [1, 2].uniq!,
    [3, 1, 2].sort!,
    [1, 2].map! do |n|
      n * 3
    end,
    [1, 2, 3].select! do |n|
      n < 3
    end,
    [1, 2].select! do |n|
      n > 0
    end,
    [1, 2, 3].reject! do |n|
      n == 2
    end,
    [1, 2].reject! do |n|
      n == 9
    end,
    [1, 2, 3].reverse!
  ]
end
`)

	got := callFunc(t, script, "run", nil)
	requireRubyBatchValue(t, got, rubyBatchArray(
		rubyBatchArray(NewInt(1), NewInt(2)),
		NewNil(),
		rubyBatchArray(NewInt(1), NewInt(2)),
		NewNil(),
		rubyBatchArray(NewInt(1), NewInt(2), NewInt(3)),
		rubyBatchArray(NewInt(3), NewInt(6)),
		rubyBatchArray(NewInt(1), NewInt(2)),
		NewNil(),
		rubyBatchArray(NewInt(1), NewInt(3)),
		NewNil(),
		rubyBatchArray(NewInt(3), NewInt(2), NewInt(1)),
	))
}

func TestRubyBatchArraySampleAndShuffle(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  srand(1234)
  single_a = [1, 2, 3, 4].sample
  srand(1234)
  single_b = [1, 2, 3, 4].sample

  srand(5678)
  sample_a = [1, 2, 3, 4].sample(2)
  srand(5678)
  sample_b = [1, 2, 3, 4].sample(2)

  srand(9012)
  shuffle_a = [1, 2, 3, 4].shuffle
  srand(9012)
  shuffle_b = [1, 2, 3, 4].shuffle

  [
    single_a == single_b,
    single_a >= 1 && single_a <= 4,
    sample_a == sample_b,
    sample_a.length == 2,
    sample_a.uniq.length == sample_a.length,
    shuffle_a == shuffle_b,
    shuffle_a.sort == [1, 2, 3, 4],
    [].sample,
    [1, 2].sample(0),
    [1, 2].sample(5).length
  ]
end
`)

	got := callScript(t, context.Background(), script, "run", nil, CallOptions{})
	requireRubyBatchValue(t, got, rubyBatchArray(
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewBool(true),
		NewNil(),
		rubyBatchArray(),
		NewInt(2),
	))
}

func TestRubyBatchArrayCombinatorics(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  [
    [1, 2, 3].rotate,
    [1, 2, 3].rotate(2),
    [1, 2].product(["a", "b"]),
    [1, 2, 3].combination(2),
    [1, 2, 3].permutation(2),
    [1, 2].repeated_combination(2),
    [1, 2].repeated_permutation(2)
  ]
end
`)

	got := callFunc(t, script, "run", nil)
	requireRubyBatchValue(t, got, rubyBatchArray(
		rubyBatchArray(NewInt(2), NewInt(3), NewInt(1)),
		rubyBatchArray(NewInt(3), NewInt(1), NewInt(2)),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewString("a")),
			rubyBatchArray(NewInt(1), NewString("b")),
			rubyBatchArray(NewInt(2), NewString("a")),
			rubyBatchArray(NewInt(2), NewString("b")),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(1), NewInt(3)),
			rubyBatchArray(NewInt(2), NewInt(3)),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(1), NewInt(3)),
			rubyBatchArray(NewInt(2), NewInt(1)),
			rubyBatchArray(NewInt(2), NewInt(3)),
			rubyBatchArray(NewInt(3), NewInt(1)),
			rubyBatchArray(NewInt(3), NewInt(2)),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(1)),
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(2), NewInt(2)),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(1)),
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(2), NewInt(1)),
			rubyBatchArray(NewInt(2), NewInt(2)),
		),
	))
}

func TestRubyBatchArrayChunkingHelpers(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  [
    [1, 1, 2, 2, 3].chunk do |n|
      n
    end,
    [1, 2, 3, 4].chunk do |n|
      n.even? ? :even : nil
    end,
    [1, 2, 3, 4].chunk do |n|
      n.even? ? :even : :_separator
    end,
    [1, 2, 3, 4].chunk do |n|
      n.even? ? :_alone : :odd
    end,
    [1, 2, 4, 5].slice_when do |left, right|
      right - left > 1
    end,
    [1, 2, 4, 5].chunk_while do |left, right|
      right - left == 1
    end,
    [1, 2, 3, 4].chunk(2)
  ]
end
`)

	got := callFunc(t, script, "run", nil)
	requireRubyBatchValue(t, got, rubyBatchArray(
		rubyBatchArray(
			rubyBatchArray(NewInt(1), rubyBatchArray(NewInt(1), NewInt(1))),
			rubyBatchArray(NewInt(2), rubyBatchArray(NewInt(2), NewInt(2))),
			rubyBatchArray(NewInt(3), rubyBatchArray(NewInt(3))),
		),
		rubyBatchArray(
			rubyBatchArray(NewSymbol("even"), rubyBatchArray(NewInt(2))),
			rubyBatchArray(NewSymbol("even"), rubyBatchArray(NewInt(4))),
		),
		rubyBatchArray(
			rubyBatchArray(NewSymbol("even"), rubyBatchArray(NewInt(2))),
			rubyBatchArray(NewSymbol("even"), rubyBatchArray(NewInt(4))),
		),
		rubyBatchArray(
			rubyBatchArray(NewSymbol("odd"), rubyBatchArray(NewInt(1))),
			rubyBatchArray(NewSymbol("_alone"), rubyBatchArray(NewInt(2))),
			rubyBatchArray(NewSymbol("odd"), rubyBatchArray(NewInt(3))),
			rubyBatchArray(NewSymbol("_alone"), rubyBatchArray(NewInt(4))),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(4), NewInt(5)),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(4), NewInt(5)),
		),
		rubyBatchArray(
			rubyBatchArray(NewInt(1), NewInt(2)),
			rubyBatchArray(NewInt(3), NewInt(4)),
		),
	))
}

func TestRubyBatchStringCharacterSets(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
def run()
  [
    "banana".count("an"),
    "hello".count("lo", "o"),
    "banana".delete("an"),
    "abc123".delete("^0-9"),
    "abc123".delete("a-c"),
    "abc^".count("^"),
    "abc^".delete("^"),
    "banana".tr("an", "AN"),
    "abc".tr("a-c", "x"),
    "abc".tr("b", ""),
    "abc123".tr("^0-9", "*"),
    "abc123".tr("^0-9", "XYZ"),
    "a".tr("aa", "XY"),
    "b".tr("a-cb", "WXYZ"),
    "hello   world".squeeze(" "),
    "bookkeeper".squeeze,
    "ruby".delete!("z"),
    "ruby".delete!("r"),
    "abc".tr!("z", "x"),
    "book".squeeze!("z")
  ]
end
`)

	got := callFunc(t, script, "run", nil)
	requireRubyBatchValue(t, got, rubyBatchArray(
		NewInt(5),
		NewInt(1),
		NewString("b"),
		NewString("123"),
		NewString("123"),
		NewInt(1),
		NewString("abc"),
		NewString("bANANA"),
		NewString("xxx"),
		NewString("ac"),
		NewString("***123"),
		NewString("ZZZ123"),
		NewString("Y"),
		NewString("Z"),
		NewString("hello world"),
		NewString("bokeper"),
		NewNil(),
		NewString("uby"),
		NewNil(),
		NewNil(),
	))
}

func TestRubyBatchStringCharSetParserScratchChargesQuota(t *testing.T) {
	t.Parallel()

	receiver := NewString("z")
	args := []Value{NewString(strings.Repeat("a", 4096))}

	probe := &Execution{ctx: context.Background(), quota: 1 << 30}
	base := probe.estimateMemoryUsageForCallRoots(NewNil(), receiver, args, nil, NewNil())
	scratch := stringCharSetArgsScratchBytes(args)
	if scratch == 0 {
		t.Fatal("string character set scratch estimate is zero")
	}

	reject := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: base + scratch - 1,
	}
	_, err := stringCountChars(reject, receiver, args, nil, NewNil())
	requireErrorIs(t, err, errMemoryQuotaExceeded)

	allow := &Execution{
		ctx:         context.Background(),
		quota:       1 << 30,
		memoryQuota: base + scratch + estimatedValueBytes,
	}
	count, err := stringCountChars(allow, receiver, args, nil, NewNil())
	if err != nil {
		t.Fatalf("string.count with parser scratch headroom failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("string.count = %d, want 0", count)
	}
}
