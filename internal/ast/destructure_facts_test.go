package ast

import "testing"

func name(n string) Expression { return &Identifier{Name: n} }

func index(n string) Expression {
	return &IndexExpr{Object: &Identifier{Name: n}, Indices: []Expression{&IntegerLiteral{}}}
}

func elements(targets ...Expression) []DestructureElement {
	out := make([]DestructureElement, 0, len(targets))
	for _, target := range targets {
		out = append(out, DestructureElement{Target: target})
	}
	return out
}

// The facts a target carries must be the ones a fresh scan would produce, since
// the evaluator now trusts them instead of scanning.
func TestDestructureFactsMatchAFreshScan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		elements []DestructureElement
		readBack bool
		binds    bool
		writes   bool
	}{
		{name: "plain names never read back", elements: elements(name("a"), name("b")), binds: true},
		{name: "write then read", elements: elements(index("v"), name("b")), readBack: true, binds: true, writes: true},
		{name: "read then write", elements: elements(name("b"), index("v")), binds: true, writes: true},
		{
			name:     "write then anonymous rest only",
			elements: append(elements(index("v")), DestructureElement{Rest: true}),
			binds:    true,
			writes:   true,
		},
		{
			name:     "write then all-discard nested follower",
			elements: append(elements(index("v")), DestructureElement{Target: NewDestructureTarget([]DestructureElement{{Rest: true}}, Position{})}),
			binds:    true,
			writes:   true,
		},
		{
			name:     "write nested deep, then a reader",
			elements: elements(NewDestructureTarget(elements(NewDestructureTarget(elements(index("v")), Position{})), Position{}), name("b")),
			readBack: true,
			binds:    true,
			writes:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settled := NewDestructureTarget(tc.elements, Position{})
			// A bare node carries no facts, so its accessors take the scanning
			// fallback: the two must agree, or the parser's answer and a host's
			// hand-built target's would differ.
			bare := &DestructureTarget{Elements: tc.elements}

			for _, probe := range []struct {
				what string
				got  func(*DestructureTarget) bool
				want bool
			}{
				{"WriteIsReadBack", (*DestructureTarget).WriteIsReadBack, tc.readBack},
				{"BindsAnyValue", (*DestructureTarget).BindsAnyValue, tc.binds},
				{"WritesIntoContainer", (*DestructureTarget).WritesIntoContainer, tc.writes},
			} {
				if got := probe.got(settled); got != probe.want {
					t.Errorf("settled %s = %v, want %v", probe.what, got, probe.want)
				}
				if got := probe.got(bare); got != probe.want {
					t.Errorf("scanned %s = %v, want %v", probe.what, got, probe.want)
				}
			}
		})
	}
}

// A clone copies the facts and an equally shaped element list, so the copy's
// facts stay true of it. If cloning ever rebuilt elements into a different
// shape, the copied facts would be a stale answer about the wrong tree.
func TestClonedDestructureTargetKeepsTrueFacts(t *testing.T) {
	t.Parallel()

	target := NewDestructureTarget(elements(index("v"), name("b")), Position{})
	clone, ok := cloneExpression(target).(*DestructureTarget)
	if !ok {
		t.Fatalf("cloning a destructure target produced %T", cloneExpression(target))
	}
	bare := &DestructureTarget{Elements: clone.Elements}
	if clone.WriteIsReadBack() != bare.WriteIsReadBack() {
		t.Errorf("clone reports WriteIsReadBack = %v but a fresh scan of its own elements says %v",
			clone.WriteIsReadBack(), bare.WriteIsReadBack())
	}
}
