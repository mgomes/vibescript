package runtime

import (
	"context"
	"testing"
)

func hashTypeExpr(keyKind TypeKind, valueType *TypeExpr) *TypeExpr {
	return &TypeExpr{Kind: TypeHash, TypeArgs: []*TypeExpr{{Kind: keyKind}, valueType}}
}

// TestHashNormalizationNoChangeSharesBacking pins the no-copy contract for
// conforming hashes: when normalization against hash<K, V> changes nothing,
// the original container is returned with its backing shared, not a fresh
// copy. Sharing is the semantically correct outcome, not just a fast path:
// hashes are reference values in the language, so severing aliasing at a type
// annotation would silently break Ruby-style mutation visibility, and the
// host Call boundary already deep-clones values crossing it.
func TestHashNormalizationNoChangeSharesBacking(t *testing.T) {
	t.Parallel()

	ctx := typeContext{}

	stringKeyed := NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	got, err := normalizeValueForType(stringKeyed, hashTypeExpr(TypeString, &TypeExpr{Kind: TypeInt}), ctx)
	if err != nil {
		t.Fatalf("normalize string-keyed hash error = %v", err)
	}
	if got.Kind() != KindHash || hashIdentity(got) != hashIdentity(stringKeyed) {
		t.Fatalf("string-keyed no-op normalization must return the original hash, got kind %s identity %#x want %#x",
			got.Kind(), hashIdentity(got), hashIdentity(stringKeyed))
	}

	symbolKeyed := NewTypedHash(2)
	for i, key := range []string{"first", "second"} {
		if err := symbolKeyed.HashSet(NewSymbol(key), NewInt(int64(i))); err != nil {
			t.Fatalf("HashSet(:%s) error = %v", key, err)
		}
	}
	got, err = normalizeValueForType(symbolKeyed, hashTypeExpr(TypeSymbol, &TypeExpr{Kind: TypeInt}), ctx)
	if err != nil {
		t.Fatalf("normalize symbol-keyed hash error = %v", err)
	}
	if got.Kind() != KindHash || hashIdentity(got) != hashIdentity(symbolKeyed) {
		t.Fatalf("symbol-keyed no-op normalization must return the original hash, got kind %s identity %#x want %#x",
			got.Kind(), hashIdentity(got), hashIdentity(symbolKeyed))
	}

	object := NewObject(map[string]Value{"a": NewInt(1), "b": NewInt(2)})
	got, err = normalizeValueForType(object, hashTypeExpr(TypeString, &TypeExpr{Kind: TypeInt}), ctx)
	if err != nil {
		t.Fatalf("normalize object error = %v", err)
	}
	if got.Kind() != KindObject || !sameNormalizedValue(got, object) {
		t.Fatalf("object no-op normalization must return the original object, got kind %s", got.Kind())
	}
}

// TestTypedHashNormalizationCoercionCopiesAllEntries pins the copy-on-change
// path of typed-entry hash normalization: when exactly one value needs
// coercion (symbol -> enum member), the result is a fresh hash that carries
// the already-validated prefix, the coerced entry, and the suffix, in the
// original insertion order, regardless of where in the hash the change sits.
// The input hash must be left untouched.
func TestTypedHashNormalizationCoercionCopiesAllEntries(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
enum Status
  Active
  Retired
end
`)
	enumDef := script.enums["Status"]
	if enumDef == nil {
		t.Fatalf("script has no Status enum")
	}
	active := enumDef.MembersByKey["active"]
	retired := enumDef.MembersByKey["retired"]
	if active == nil || retired == nil {
		t.Fatalf("Status members missing: active=%v retired=%v", active, retired)
	}

	ctx := typeContext{owner: script}
	ty := hashTypeExpr(TypeSymbol, &TypeExpr{Kind: TypeEnum, Name: "Status"})
	keys := []string{"k0", "k1", "k2"}

	for changedAt := range keys {
		payload := NewTypedHash(len(keys))
		for i, key := range keys {
			val := NewEnumValue(active)
			if i == changedAt {
				val = NewSymbol("retired")
			}
			if err := payload.HashSet(NewSymbol(key), val); err != nil {
				t.Fatalf("HashSet(:%s) error = %v", key, err)
			}
		}

		got, err := normalizeValueForType(payload, ty, ctx)
		if err != nil {
			t.Fatalf("changedAt=%d normalize error = %v", changedAt, err)
		}
		if got.Kind() != KindHash {
			t.Fatalf("changedAt=%d result kind = %s, want hash", changedAt, got.Kind())
		}
		if hashIdentity(got) == hashIdentity(payload) {
			t.Fatalf("changedAt=%d coercion must produce a fresh hash, got the original", changedAt)
		}

		entries := got.HashEntries()
		if len(entries) != len(keys) {
			t.Fatalf("changedAt=%d result entries = %d, want %d", changedAt, len(entries), len(keys))
		}
		for i, entry := range entries {
			if entry.Key.Kind() != KindSymbol || entry.Key.String() != keys[i] {
				t.Fatalf("changedAt=%d entry %d key = %s %q, want :%s (insertion order preserved)",
					changedAt, i, entry.Key.Kind(), entry.Key.String(), keys[i])
			}
			if entry.Value.Kind() != KindEnumValue {
				t.Fatalf("changedAt=%d entry %d value kind = %s, want enum value", changedAt, i, entry.Value.Kind())
			}
			want := active
			if i == changedAt {
				want = retired
			}
			if member := valueEnumValue(entry.Value); member != want {
				t.Fatalf("changedAt=%d entry %d member = %v, want %v", changedAt, i, member, want)
			}
		}

		original, ok, err := payload.HashGet(NewSymbol(keys[changedAt]))
		if err != nil || !ok {
			t.Fatalf("changedAt=%d original lookup = %v, %v", changedAt, ok, err)
		}
		if original.Kind() != KindSymbol {
			t.Fatalf("changedAt=%d original entry mutated to %s, want symbol", changedAt, original.Kind())
		}
	}
}

// TestHashNormalizationCoercedDefaultBackfillsEntries pins the default-value
// arm of copy-on-change: when every entry already conforms but the Ruby-style
// hash default needs coercion, the copy still carries all entries and the
// normalized default, and the original hash keeps its raw default.
func TestHashNormalizationCoercedDefaultBackfillsEntries(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
enum Status
  Active
  Retired
end
`)
	enumDef := script.enums["Status"]
	active := enumDef.MembersByKey["active"]

	ctx := typeContext{owner: script}
	ty := hashTypeExpr(TypeString, &TypeExpr{Kind: TypeEnum, Name: "Status"})

	payload := NewHashWithDefault(map[string]Value{"a": NewEnumValue(active)}, NewSymbol("retired"), NewNil())
	got, err := normalizeValueForType(payload, ty, ctx)
	if err != nil {
		t.Fatalf("normalize error = %v", err)
	}
	if hashIdentity(got) == hashIdentity(payload) {
		t.Fatalf("coerced default must produce a fresh hash, got the original")
	}
	if val, ok, err := got.HashGet(NewString("a")); err != nil || !ok || val.Kind() != KindEnumValue {
		t.Fatalf("copied entry a = %v, %v, %v; want carried enum value", val, ok, err)
	}
	if def := hashDefaultValue(got); def.Kind() != KindEnumValue {
		t.Fatalf("normalized default kind = %s, want enum value", def.Kind())
	}
	if def := hashDefaultValue(payload); def.Kind() != KindSymbol {
		t.Fatalf("original default mutated to %s, want symbol", def.Kind())
	}
}

// TestHashNormalizationObjectKindPreservedOnCopy pins that an object receiver
// stays an object when coercion forces a copy.
func TestHashNormalizationObjectKindPreservedOnCopy(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
enum Status
  Active
  Retired
end
`)
	enumDef := script.enums["Status"]
	active := enumDef.MembersByKey["active"]

	ctx := typeContext{owner: script}
	ty := hashTypeExpr(TypeString, &TypeExpr{Kind: TypeEnum, Name: "Status"})

	payload := NewObject(map[string]Value{
		"raw":  NewSymbol("retired"),
		"done": NewEnumValue(active),
	})
	got, err := normalizeValueForType(payload, ty, ctx)
	if err != nil {
		t.Fatalf("normalize error = %v", err)
	}
	if got.Kind() != KindObject {
		t.Fatalf("result kind = %s, want object", got.Kind())
	}
	if sameNormalizedValue(got, payload) {
		t.Fatalf("coercion must produce a fresh object, got the original")
	}
	for key, wantKind := range map[string]ValueKind{"raw": KindEnumValue, "done": KindEnumValue} {
		if val, ok, err := got.HashGet(NewString(key)); err != nil || !ok || val.Kind() != wantKind {
			t.Fatalf("copied entry %s = %v, %v, %v; want %s", key, val, ok, err, wantKind)
		}
	}
	if raw := payload.Hash()["raw"]; raw.Kind() != KindSymbol {
		t.Fatalf("original entry mutated to %s, want symbol", raw.Kind())
	}
}

// TestTypedHashBoundaryMismatchMessage pins the user-visible error when a
// mid-map value fails a typed hash annotation; the lazy validation pass must
// keep reporting the same message the eager copy produced.
func TestTypedHashBoundaryMismatchMessage(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def typed(payload: hash<string, int>)
  payload
end`)

	payload := NewHash(map[string]Value{"a": NewInt(1), "b": NewString("x"), "c": NewInt(3)})
	requireCallErrorContains(t, script, "typed", []Value{payload}, CallOptions{},
		"argument payload expected hash<string, int>, got { a: int, b: string, c: int }")
}

// TestTypedHashBoundaryEnumCoercionEndToEnd exercises the copy-on-change path
// through the public Call surface: symbol values coerce to enum members while
// conforming entries pass through, and the container stays a hash.
func TestTypedHashBoundaryEnumCoercionEndToEnd(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
enum Status
  Active
  Retired
end

def typed(payload: hash<string, Status>)
  payload
end
`)

	payload := NewHash(map[string]Value{
		"first":  NewSymbol("active"),
		"second": NewSymbol("retired"),
	})
	got := callScript(t, context.Background(), script, "typed", []Value{payload}, CallOptions{})
	if got.Kind() != KindHash {
		t.Fatalf("result kind = %s, want hash", got.Kind())
	}
	if got.HashLen() != 2 {
		t.Fatalf("result entries = %d, want 2", got.HashLen())
	}
	for key, want := range map[string]string{"first": "Active", "second": "Retired"} {
		val, ok, err := got.HashGet(NewString(key))
		if err != nil || !ok {
			t.Fatalf("result[%q] = %v, %v", key, ok, err)
		}
		if val.Kind() != KindEnumValue {
			t.Fatalf("result[%q] kind = %s, want enum value", key, val.Kind())
		}
		if member := valueEnumValue(val); member.Name != want {
			t.Fatalf("result[%q] member = %s, want %s", key, member.Name, want)
		}
	}
	for _, key := range []string{"first", "second"} {
		if payload.Hash()[key].Kind() != KindSymbol {
			t.Fatalf("input %q mutated to %s, want symbol", key, payload.Hash()[key].Kind())
		}
	}
}
