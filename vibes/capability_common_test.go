package vibes

import (
	"testing"
)

func TestValidateCapabilityTypedValueUsesCompositeTypeChecks(t *testing.T) {
	unionType := &TypeExpr{
		Kind: TypeUnion,
		Union: []*TypeExpr{
			{Kind: TypeInt},
			{Kind: TypeString},
		},
	}
	arrayType := &TypeExpr{
		Kind:     TypeArray,
		TypeArgs: []*TypeExpr{unionType},
	}

	if err := validateCapabilityTypedValue("payload", NewArray([]Value{NewInt(1), NewString("ok")}), arrayType); err != nil {
		t.Fatalf("expected composite type validation to pass, got %v", err)
	}

	err := validateCapabilityTypedValue("payload", NewArray([]Value{NewInt(1), NewBool(true)}), arrayType)
	if err == nil {
		t.Fatalf("expected composite type mismatch")
	}
	requireErrorContains(t, err, "payload expected array<int | string>, got array<bool | int>")
}

func TestValidateCapabilityTypedValueUsesShapeTypeChecks(t *testing.T) {
	shapeType := &TypeExpr{
		Kind: TypeShape,
		Shape: map[string]*TypeExpr{
			"id": {Kind: TypeString},
		},
	}

	if err := validateCapabilityTypedValue("payload", NewHash(map[string]Value{"id": NewString("p-1")}), shapeType); err != nil {
		t.Fatalf("expected shape validation to pass, got %v", err)
	}

	err := validateCapabilityTypedValue("payload", NewHash(map[string]Value{
		"id":    NewString("p-1"),
		"extra": NewInt(1),
	}), shapeType)
	if err == nil {
		t.Fatalf("expected shape type mismatch")
	}
	requireErrorContains(t, err, "payload expected { id: string }, got { extra: int, id: string }")
}
