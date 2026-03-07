package vibes

import (
	"strings"
	"testing"
)

func TestTypeAllowsStringHashKeyDefersUnknownUnions(t *testing.T) {
	keyType := &TypeExpr{
		Kind: TypeUnion,
		Union: []*TypeExpr{
			{Name: "typo", Kind: TypeUnknown},
			{Name: "string", Kind: TypeString},
		},
	}

	decided, matches := typeAllowsStringHashKey(keyType)
	if decided {
		t.Fatalf("expected unknown key union to defer to full matcher")
	}
	if matches {
		t.Fatalf("unexpected string-key fast-path match for unknown key union")
	}
}

func TestTypeAllowsStringHashKeyDefersEnumUnions(t *testing.T) {
	for _, order := range [][]*TypeExpr{
		{{Name: "stauts", Kind: TypeEnum}, {Name: "string", Kind: TypeString}},
		{{Name: "string", Kind: TypeString}, {Name: "stauts", Kind: TypeEnum}},
	} {
		keyType := &TypeExpr{Kind: TypeUnion, Union: order}
		decided, matches := typeAllowsStringHashKey(keyType)
		if decided {
			t.Fatalf("expected enum key union to defer to full matcher (order: %v)", order)
		}
		if matches {
			t.Fatalf("unexpected string-key fast-path match for enum key union (order: %v)", order)
		}
	}
}

func TestValueMatchesTypeHashUnknownKeyUnionReturnsError(t *testing.T) {
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
