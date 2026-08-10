package vibes_test

import (
	"context"
	"testing"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/value"
)

// builtinFactoryCapability publishes a callable that makes another callable.
// The one it makes did not exist when the adapter was bound, so nothing that
// walks the adapter's roots can have marked it.
type builtinFactoryCapability struct{}

func (builtinFactoryCapability) Bind(vibes.CapabilityBinding) (map[string]value.Value, error) {
	return map[string]value.Value{
		"fac": value.NewObject(map[string]value.Value{
			"make_walker": vibes.NewBuiltin("fac.make_walker",
				func(_ *vibes.Execution, _ value.Value, _ []value.Value, _ map[string]value.Value, _ value.Value) (value.Value, error) {
					return vibes.NewBuiltin("fac.walker",
						func(exec *vibes.Execution, _ value.Value, args []value.Value, _ map[string]value.Value, block value.Value) (value.Value, error) {
							for _, item := range args[0].Hash()["items"].Array() {
								if _, err := exec.CallBlock(block, []value.Value{item}); err != nil {
									return value.NewNil(), err
								}
							}
							return value.NewNil(), nil
						}), nil
				}),
		}),
	}, nil
}

// TestBuiltinFromHostFactoryClaimsWhatItWalks pins that a callable a host makes
// after its adapter was bound still gets a claim over the arrays it walks.
//
// A frame the runtime did not write is claimed so that a shrink inside the
// block it yields to leaves its storage alone. That classification was applied
// where callables are registered and where an adapter's roots are walked, both
// of which only see what exists at that moment, so a callable produced later
// went unclaimed and a pop cleared a slot the frame had not reached.
//
// It is written against the public API on purpose. A test inside the runtime
// package builds its callables with the constructor the runtime uses for its
// own, so it cannot tell a host's callable from a first-party one, and every
// host-driven case already here goes through capability binding, which marks
// what it can reach. None of them would fail on this.
func TestBuiltinFromHostFactoryClaimsWhatItWalks(t *testing.T) {
	t.Parallel()

	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`def run()
  a = [1, 2, 3]
  seen = []
  w = fac.make_walker()
  w({ items: a }) do |x|
    seen.push(x)
    a.pop
  end
  seen
end`)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	got, err := script.Call(context.Background(), "run", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{builtinFactoryCapability{}},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `[1, 2, 3]`; got.Inspect() != want {
		t.Fatalf("the factory's callable yielded %s, want %s", got.Inspect(), want)
	}
}
