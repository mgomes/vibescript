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
			want: []*TypeExpr{{
				Kind:     TypeArray,
				Name:     literalElementsMarker,
				TypeArgs: []*TypeExpr{checkTypeInt},
			}},
		},
		{
			name: "leading value leaves empty rest",
			target: &DestructureTarget{Elements: []DestructureElement{
				{},
				{Rest: true},
			}},
			want: []*TypeExpr{checkTypeInt, checkTypeArray},
		},
		{
			name: "trailing target receives scalar",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Rest: true},
				{},
			}},
			want: []*TypeExpr{checkTypeArray, checkTypeInt},
		},
		{
			name: "missing trailing target receives nil",
			target: &DestructureTarget{Elements: []DestructureElement{
				{Rest: true},
				{},
				{},
			}},
			want: []*TypeExpr{checkTypeArray, checkTypeInt, checkTypeNil},
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
