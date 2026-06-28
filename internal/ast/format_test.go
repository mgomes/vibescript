package ast

import "testing"

func TestFormatTypeExprPreservesHashObjectAlias(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ty   *TypeExpr
		want string
	}{
		{
			name: "object",
			ty:   &TypeExpr{Name: "object", Kind: TypeHash},
			want: "object",
		},
		{
			name: "object nullable",
			ty:   &TypeExpr{Name: "object?", Kind: TypeHash, Nullable: true},
			want: "object?",
		},
		{
			name: "object generic",
			ty: &TypeExpr{
				Name: "object",
				Kind: TypeHash,
				TypeArgs: []*TypeExpr{
					{Name: "string", Kind: TypeString},
					{Name: "int", Kind: TypeInt},
				},
			},
			want: "object<string, int>",
		},
		{
			name: "hash",
			ty:   &TypeExpr{Name: "hash", Kind: TypeHash},
			want: "hash",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := FormatTypeExpr(tc.ty); got != tc.want {
				t.Fatalf("FormatTypeExpr() = %q, want %q", got, tc.want)
			}
		})
	}
}
