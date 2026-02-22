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
