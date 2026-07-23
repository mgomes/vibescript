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

func TestFormatTypeExprMarksOptionalShapeFields(t *testing.T) {
	t.Parallel()

	ty := &TypeExpr{
		Kind: TypeShape,
		Shape: map[string]*TypeExpr{
			"name":  {Name: "string", Kind: TypeString},
			"age":   {Name: "int", Kind: TypeInt, Optional: true},
			"email": {Name: "string?", Kind: TypeString, Nullable: true, Optional: true},
		},
	}
	want := "{ age?: int, email?: string?, name: string }"
	if got := FormatTypeExpr(ty); got != want {
		t.Fatalf("FormatTypeExpr() = %q, want %q", got, want)
	}
}

func TestFormatTypeExprMarksOpenShapes(t *testing.T) {
	t.Parallel()

	open := &TypeExpr{
		Kind: TypeShape,
		Open: true,
		Shape: map[string]*TypeExpr{
			"name": {Name: "string", Kind: TypeString},
			"age":  {Name: "int", Kind: TypeInt, Optional: true},
		},
	}
	if got, want := FormatTypeExpr(open), "{ age?: int, name: string, ... }"; got != want {
		t.Fatalf("FormatTypeExpr(open) = %q, want %q", got, want)
	}

	empty := &TypeExpr{Kind: TypeShape, Open: true}
	if got, want := FormatTypeExpr(empty), "{ ... }"; got != want {
		t.Fatalf("FormatTypeExpr(empty open) = %q, want %q", got, want)
	}
	closed := &TypeExpr{Kind: TypeShape}
	if got, want := FormatTypeExpr(closed), "{}"; got != want {
		t.Fatalf("FormatTypeExpr(empty closed) = %q, want %q", got, want)
	}
}

// A required field whose name literally ends in `?` (a string-key field) must
// not render like the optional spelling of the shorter name: shape equality
// compares formatted text, so the two contracts have to stay distinguishable.
func TestFormatTypeExprQuotesLiteralQuestionFieldNames(t *testing.T) {
	t.Parallel()

	literal := &TypeExpr{
		Kind: TypeShape,
		Shape: map[string]*TypeExpr{
			"valid?": {Name: "bool", Kind: TypeBool},
		},
	}
	if got, want := FormatTypeExpr(literal), `{ "valid?": bool }`; got != want {
		t.Fatalf("FormatTypeExpr(literal) = %q, want %q", got, want)
	}

	optional := &TypeExpr{
		Kind: TypeShape,
		Shape: map[string]*TypeExpr{
			"valid": {Name: "bool", Kind: TypeBool, Optional: true},
		},
	}
	if got, want := FormatTypeExpr(optional), "{ valid?: bool }"; got != want {
		t.Fatalf("FormatTypeExpr(optional) = %q, want %q", got, want)
	}
	if FormatTypeExpr(literal) == FormatTypeExpr(optional) {
		t.Fatalf("literal and optional spellings format identically: %q", FormatTypeExpr(literal))
	}
}
