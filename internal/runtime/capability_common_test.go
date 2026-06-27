package runtime

import (
	"testing"
)

func TestValidateCapabilityTypedValueUsesCompositeTypeChecks(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestValidateCapabilityTypedValueAcceptsEnumValues(t *testing.T) {
	t.Parallel()
	status := testEnumDef("Status", "Draft", "Published")
	reviewState := testEnumDef("ReviewState", "Draft")

	if err := validateCapabilityTypedValue("payload", NewEnumValue(status.Members["Draft"]), &TypeExpr{Name: "Status", Kind: TypeEnum}); err != nil {
		t.Fatalf("expected enum value validation to pass, got %v", err)
	}

	err := validateCapabilityTypedValue("payload", NewSymbol("draft"), &TypeExpr{Name: "Status", Kind: TypeEnum})
	if err == nil {
		t.Fatalf("expected symbol enum contract validation to fail")
	}
	requireErrorContains(t, err, "payload expected Status, got symbol")

	err = validateCapabilityTypedValue("payload", NewEnumValue(reviewState.Members["Draft"]), &TypeExpr{Name: "Status", Kind: TypeEnum})
	if err == nil {
		t.Fatalf("expected mismatched enum contract validation to fail")
	}
	requireErrorContains(t, err, "payload expected Status, got ReviewState")
}

func testEnumDef(name string, members ...string) *EnumDef {
	enum := &EnumDef{
		Name:         name,
		Members:      make(map[string]*EnumValueDef, len(members)),
		MembersByKey: make(map[string]*EnumValueDef, len(members)),
		Order:        append([]string(nil), members...),
	}
	for i, memberName := range members {
		member := &EnumValueDef{Enum: enum, Name: memberName, Symbol: enumMemberSymbol(memberName), Index: i}
		enum.Members[memberName] = member
		enum.MembersByKey[member.Symbol] = member
	}
	return enum
}
