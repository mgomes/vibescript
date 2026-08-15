package runtime

import (
	"context"
	"os"
	"strings"
	"testing"
)

// runWithContractVerification compiles and runs src against an engine carrying
// the given host builtin, with contract verification on, and reports whatever
// the run panicked with. A verifier that cannot be made to fire proves nothing,
// so every test below states which side of the check it is exercising.
func runWithContractVerification(t *testing.T, src, name string, builtin, arg Value) (recovered any) {
	t.Helper()

	prev := builtinContractVerify
	builtinContractVerify = true
	defer func() {
		builtinContractVerify = prev
		recovered = recover()
	}()

	engine := MustNewEngine(Config{MemoryQuotaBytes: 64 << 20, StepQuota: Unlimited})
	engine.registerHostBuiltin(name, builtin)
	script, err := engine.Compile(src)
	if err != nil {
		t.Fatalf("compile failed: %v", err)
	}
	if _, err := script.Call(context.Background(), "run", []Value{arg}, CallOptions{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	return nil
}

// A gate that never armed reports nothing and reads exactly like a clean run,
// so the wiring from the environment variable to the flag is pinned rather than
// assumed. Without this, a misspelling in either name would turn the whole
// verification run into a silent no-op.
func TestBuiltinContractVerifyEnablerWiring(t *testing.T) {
	prev := builtinContractVerify
	defer func() { builtinContractVerify = prev }()

	builtinContractVerify = false
	t.Setenv("VIBES_BUILTIN_CONTRACT_VERIFY", "1")
	maybeEnableBuiltinContractVerify()
	if !builtinContractVerify {
		t.Fatal("VIBES_BUILTIN_CONTRACT_VERIFY=1 did not turn the verifier on; a run under it " +
			"would report nothing and look identical to a clean one")
	}

	builtinContractVerify = false
	t.Setenv("VIBES_BUILTIN_CONTRACT_VERIFY", "0")
	maybeEnableBuiltinContractVerify()
	if builtinContractVerify {
		t.Fatal("the verifier turned on without being asked; it costs a full graph walk per " +
			"declared dispatch and must stay off by default")
	}
}

// When the suite is actually run under the variable, this asserts the flag
// reached production code, which the enabler test above cannot show on its own:
// it proves the TestMain call happens, not merely that the function works.
func TestBuiltinContractVerifyIsArmedUnderItsEnvVar(t *testing.T) {
	if os.Getenv("VIBES_BUILTIN_CONTRACT_VERIFY") != "1" {
		t.Skip("not running under VIBES_BUILTIN_CONTRACT_VERIFY=1")
	}
	if !builtinContractVerify {
		t.Fatal("running under VIBES_BUILTIN_CONTRACT_VERIFY=1 but the verifier is off; " +
			"TestMain did not arm it, so every declaration in this run went unchecked")
	}
}

// The instrument has to be shown returning a positive before its negatives mean
// anything. This builtin declares non-mutation and then writes straight through
// the argument's backing slice, which is precisely the unobserved write the
// declaration denies: no wrapper mutator runs, so nothing advances the epoch,
// and without the verifier the write is invisible to every later memory check.
func TestContractVerifierCatchesADeclaredBuiltinThatMutates(t *testing.T) {
	liar := DeclareNonMutating(NewBuiltin("liar", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		if len(args) > 0 && args[0].Kind() == KindArray {
			if backing := args[0].Array(); len(backing) > 0 {
				backing[0] = NewString(strings.Repeat("z", 512))
			}
		}
		return NewNil(), nil
	}))

	recovered := runWithContractVerification(t, "def run(a)\n  liar(a)\nend", "liar", liar, loopMemoArray(8))
	if recovered == nil {
		t.Fatal("a builtin that declared non-mutating and then wrote through its argument's " +
			"backing was not caught; the verifier cannot report a positive, so its " +
			"silence on any other builtin means nothing")
	}
	if msg, ok := recovered.(string); !ok || !strings.Contains(msg, "declares non-mutating") {
		t.Fatalf("verifier panicked with %v, want a message naming the broken declaration", recovered)
	}
}

// The replacement a byte total cannot see, and a count of identities cannot
// either. This builtin swaps one reachable inner array for a freshly allocated
// one holding the same number of equally sized elements: the estimate is
// identical, exactly one slice identity leaves and exactly one enters, so both
// totals match on either side of the call. Only the identities themselves
// differ.
//
// It is a real defect and not a technicality. The memo keeps the retired
// backing in its seen-set, so a later value that lands on that freed address
// deduplicates against it and is charged nothing, which is an under-count
// escaping the memory quota -- the one direction the whole design exists to
// exclude. A verifier comparing counts certifies it as clean.
func TestContractVerifierCatchesASameSizeReplacement(t *testing.T) {
	swapper := DeclareNonMutating(NewBuiltin("swapper", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		if len(args) == 0 || args[0].Kind() != KindArray {
			return NewNil(), nil
		}
		outer := args[0].Array()
		if len(outer) == 0 || outer[0].Kind() != KindArray {
			return NewNil(), nil
		}
		replacement := make([]Value, len(outer[0].Array()))
		for i := range replacement {
			replacement[i] = NewInt(int64(i))
		}
		// Same length, same element kinds, same estimated bytes, one identity
		// out and one in.
		outer[0] = NewArray(replacement)
		return NewNil(), nil
	}))

	inner := make([]Value, 4)
	for i := range inner {
		inner[i] = NewInt(int64(i))
	}
	arg := NewArray([]Value{NewArray(inner)})

	recovered := runWithContractVerification(t, "def run(a)\n  swapper(a)\nend", "swapper", swapper, arg)
	if recovered == nil {
		t.Fatal("a builtin that replaced a reachable container with an equally sized one was " +
			"not caught; the verifier compares totals rather than identities, so it certifies " +
			"the mutation it exists to expose")
	}
	if msg, ok := recovered.(string); !ok || !strings.Contains(msg, "identity digest") {
		t.Fatalf("verifier panicked with %v, want a message reporting the identity digest", recovered)
	}
}

// The other direction: a builtin that keeps the promise must not be flagged,
// including when it allocates. Building a fresh container and returning it is
// explicitly not a mutation under the promise, because nothing else could
// already reach it.
func TestContractVerifierAcceptsADeclaredBuiltinThatOnlyAllocates(t *testing.T) {
	honest := DeclareNonMutating(NewBuiltin("honest", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		out := make([]Value, 0, 4)
		for i := range 4 {
			out = append(out, NewInt(int64(i)))
		}
		return NewArray(out), nil
	}))

	if recovered := runWithContractVerification(t, "def run(a)\n  honest(a).length\nend", "honest", honest, loopMemoArray(8)); recovered != nil {
		t.Fatalf("a builtin that allocated a fresh container and returned it was flagged: %v", recovered)
	}
}

// An undeclared builtin is not verified at all, so the same mutating body must
// pass: the conservative default keeps the epoch bump that covers it, and
// verifying it would report a difference that is not a defect.
func TestContractVerifierIgnoresUndeclaredBuiltins(t *testing.T) {
	mutator := NewBuiltin("mutator", func(_ *Execution, _ Value, args []Value, _ map[string]Value, _ Value) (Value, error) {
		if len(args) > 0 && args[0].Kind() == KindArray {
			if backing := args[0].Array(); len(backing) > 0 {
				backing[0] = NewString(strings.Repeat("z", 512))
			}
		}
		return NewNil(), nil
	})

	if recovered := runWithContractVerification(t, "def run(a)\n  mutator(a)\nend", "mutator", mutator, loopMemoArray(8)); recovered != nil {
		t.Fatalf("an undeclared builtin was verified and flagged: %v", recovered)
	}
}
