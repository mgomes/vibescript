package runtime

import (
	"strings"
	"testing"
)

func TestTypeAllowsStringHashKeyDefersForAmbiguousUnions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		keyType *TypeExpr
	}{
		{
			name: "unknown_in_union",
			keyType: &TypeExpr{
				Kind: TypeUnion,
				Union: []*TypeExpr{
					{Name: "typo", Kind: TypeUnknown},
					{Name: "string", Kind: TypeString},
				},
			},
		},
		{
			name: "enum_before_string",
			keyType: &TypeExpr{
				Kind: TypeUnion,
				Union: []*TypeExpr{
					{Name: "stauts", Kind: TypeEnum},
					{Name: "string", Kind: TypeString},
				},
			},
		},
		{
			name: "string_before_enum",
			keyType: &TypeExpr{
				Kind: TypeUnion,
				Union: []*TypeExpr{
					{Name: "string", Kind: TypeString},
					{Name: "stauts", Kind: TypeEnum},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			decided, matches := typeAllowsStringHashKey(tc.keyType)
			if decided {
				t.Fatalf("expected union to defer to full matcher")
			}
			if matches {
				t.Fatalf("expected no fast-path match for deferring union")
			}
		})
	}
}

func TestValueMatchesTypeHashUnknownKeyUnionReturnsError(t *testing.T) {
	t.Parallel()
	hashType := &TypeExpr{
		Kind: TypeHash,
		TypeArgs: []*TypeExpr{
			{
				Kind: TypeUnion,
				Union: []*TypeExpr{
					{Name: "typo", Kind: TypeUnknown},
					{Name: "string", Kind: TypeString},
				},
			},
			{Name: "int", Kind: TypeInt},
		},
	}

	matches, err := valueMatchesType(NewHash(map[string]Value{
		"score": NewInt(1),
	}), hashType)
	if err == nil {
		t.Fatalf("expected unknown type error")
	}
	if matches {
		t.Fatalf("unknown key union should not report a match")
	}
	if !strings.Contains(err.Error(), "unknown type typo") {
		t.Fatalf("expected unknown type error, got %v", err)
	}
}
