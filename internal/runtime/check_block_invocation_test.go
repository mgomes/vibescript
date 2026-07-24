package runtime

import "testing"

func TestBlockDestructureElementTypeForScalarYield(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target *DestructureTarget
		want   []*TypeExpr
	}{
		{
			name: "rest receives scalar",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Rest: true},
			}},
			want: []*TypeExpr{exactBlockRestType([]*TypeExpr{checkTypeInt})},
		},
		{
			name: "leading value leaves empty rest",
			target: &DestructureTarget{Elements: []DestructureElement{
				{},
				{Rest: true},
			}},
			want: []*TypeExpr{checkTypeInt, exactBlockRestType(nil)},
		},
		{
			name: "trailing target receives scalar",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Rest: true},
				{},
			}},
			want: []*TypeExpr{exactBlockRestType(nil), checkTypeInt},
		},
		{
			name: "missing trailing target receives nil",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Rest: true},
				{},
				{},
			}},
			want: []*TypeExpr{exactBlockRestType(nil), checkTypeInt, checkTypeNil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for i, want := range tc.want {
				got := blockDestructureElementType(checkTypeInt, tc.target, i)
				if typeFactKey(got) != typeFactKey(want) {
					t.Fatalf(
						"blockDestructureElementType(int, target, %d) = %s, want %s",
						i,
						formatTypeExpr(got),
						formatTypeExpr(want),
					)
				}
			}
		})
	}
}

func TestBlockDestructureElementTypePreservesExactRestPositions(t *testing.T) {
	t.Parallel()

	singleton := exactBlockRestType([]*TypeExpr{checkTypeInt})
	empty := exactBlockRestType(nil)
	twoTargets := &DestructureTarget{Elements: []DestructureElement{{}, {}}}
	restTarget := &DestructureTarget{Elements: []DestructureElement{{Rest: true}}}

	if got := blockDestructureElementType(singleton, twoTargets, 0); typeFactKey(got) != typeFactKey(checkTypeInt) {
		t.Fatalf("singleton head = %s, want int", formatTypeExpr(got))
	}
	if got := blockDestructureElementType(singleton, twoTargets, 1); typeFactKey(got) != typeFactKey(checkTypeNil) {
		t.Fatalf("singleton missing value = %s, want nil", formatTypeExpr(got))
	}
	if got := blockDestructureElementType(empty, twoTargets, 0); typeFactKey(got) != typeFactKey(checkTypeNil) {
		t.Fatalf("empty head = %s, want nil", formatTypeExpr(got))
	}
	deeper := blockDestructureElementType(singleton, restTarget, 0)
	if typeFactKey(deeper) != typeFactKey(singleton) {
		t.Fatalf("nested rest = %s, want %s", formatTypeExpr(deeper), formatTypeExpr(singleton))
	}
}

func TestBlockParamTargetMayBindNestedExactRest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target *DestructureTarget
		want   bool
	}{
		{
			name: "singleton head nil rejects",
			target: nestedRestTarget(
				DestructureElement{Type: checkTypeNil},
			),
		},
		{
			name: "singleton head int accepts",
			target: nestedRestTarget(
				DestructureElement{Type: checkTypeInt},
			),
			want: true,
		},
		{
			name: "singleton missing nil accepts",
			target: nestedRestTarget(
				DestructureElement{Type: checkTypeInt},
				DestructureElement{Type: checkTypeNil},
			),
			want: true,
		},
		{
			name: "singleton missing int rejects",
			target: nestedRestTarget(
				DestructureElement{Type: checkTypeInt},
				DestructureElement{Type: checkTypeInt},
			),
		},
		{
			name: "empty rest head nil accepts",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Type: checkTypeInt},
				{
					Rest: true,
					Target: &DestructureTarget{Elements: []DestructureElement{
						{Type: checkTypeNil},
					}},
				},
			}},
			want: true,
		},
		{
			name: "empty rest head int rejects",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Type: checkTypeInt},
				{
					Rest: true,
					Target: &DestructureTarget{Elements: []DestructureElement{
						{Type: checkTypeInt},
					}},
				},
			}},
		},
		{
			name: "deeper rest head string rejects",
			target: nestedRestTarget(DestructureElement{
				Rest: true,
				Target: &DestructureTarget{Elements: []DestructureElement{
					{Type: checkTypeString},
				}},
			}),
		},
		{
			name: "deeper rest head int accepts",
			target: nestedRestTarget(DestructureElement{
				Rest: true,
				Target: &DestructureTarget{Elements: []DestructureElement{
					{Type: checkTypeInt},
				}},
			}),
			want: true,
		},
		{
			name: "deeper rest missing nil accepts",
			target: nestedRestTarget(DestructureElement{
				Rest: true,
				Target: &DestructureTarget{Elements: []DestructureElement{
					{Type: checkTypeInt},
					{Type: checkTypeNil},
				}},
			}),
			want: true,
		},
		{
			name: "deeper rest missing int rejects",
			target: nestedRestTarget(DestructureElement{
				Rest: true,
				Target: &DestructureTarget{Elements: []DestructureElement{
					{Type: checkTypeInt},
					{Type: checkTypeInt},
				}},
			}),
		},
	}
	checker := &scriptChecker{}
	resolve := checker.checkNamedTypeResolver()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := checker.blockParamTargetMayBind(tc.target, checkTypeInt, resolve); got != tc.want {
				t.Fatalf("blockParamTargetMayBind(target, int) = %t, want %t", got, tc.want)
			}
		})
	}
}

func nestedRestTarget(elements ...DestructureElement) *DestructureTarget {
	return &DestructureTarget{Elements: []DestructureElement{{
		Rest: true,
		Target: &DestructureTarget{
			Elements: elements,
		},
	}}}
}
