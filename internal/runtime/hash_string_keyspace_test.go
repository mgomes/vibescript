package runtime

import (
	"testing"
)

// Hashes have one string keyspace (ADR-006 item 3). A symbol key normalizes to
// its string, so `h["name"]`, `h[:name]` and the literal label `name:` address
// one entry, `keys` returns strings, and nothing else is accepted as a key.

func TestHashSymbolAndStringKeysAddressOneEntry(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  h = { name: "Ada" }
  parts = []
  parts.push(h["name"])
  parts.push(h[:name])
  parts.push(h.keys.inspect)
  parts.push(h.size.to_s)
  h["name"] = "Bo"
  parts.push(h[:name])
  parts.push(h.size.to_s)
  h[:name] = "Cy"
  parts.push(h["name"])
  parts.push(h.size.to_s)
  parts.push(h.fetch("name", "missing"))
  parts.push(h.fetch(:name, "missing"))
  parts.push(h.key?("name").to_s)
  parts.push(h.key?(:name).to_s)
  parts.push(h.dig(:name))
  parts.push(h.merge({ name: "Di" })["name"])
  parts.push(h.delete("name"))
  parts.push(h.size.to_s)
  parts.join("|")
end
`)

	got := callFunc(t, script, "run", nil).String()
	want := `Ada|Ada|["name"]|1|Bo|1|Cy|1|Cy|Cy|true|true|Cy|Di|Cy|0`
	if got != want {
		t.Fatalf("one keyspace mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestHashRejectsNonStringKeys(t *testing.T) {
	t.Parallel()

	// Every surface through which a key can enter or probe a hash must reject a
	// key that is neither a string nor a symbol, and say what is accepted.
	writes := []struct {
		name string
		body string
	}{
		{"index assign int", `h = {}
  h[1] = "x"`},
		{"index assign float", `h = {}
  h[1.5] = "x"`},
		{"index assign bool", `h = {}
  h[true] = "x"`},
		{"index assign nil", `h = {}
  h[nil] = "x"`},
		{"index assign array", `h = {}
  h[[1, 2]] = "x"`},
		{"index assign range", `h = {}
  h[1..3] = "x"`},
		{"index assign bignum", `h = {}
  h[2 ** 100] = "x"`},
		{"store", `h = {}
  h.store(1, "x")`},
		{"index read", `h = {}
  h[1]`},
		{"fetch", `h = {}
  h.fetch(1, "fallback")`},
		{"key?", `h = {}
  h.key?(1)`},
		{"delete", `h = {}
  h.delete(1)`},
		{"dig", `h = {}
  h.dig(1)`},
		{"slice", `h = {}
  h.slice(1)`},
		{"except", `h = {}
  h.except(1)`},
		{"values_at", `h = {}
  h.values_at(1)`},
		{"transform_keys", `h = { a: 1 }
  h.transform_keys { |k| 1 }`},
		{"array to_h", `[[1, "x"]].to_h`},
		{"array tally", `[1, 2].tally`},
		{"array group_by", `[1, 2].group_by { |n| n }`},
		{"nested literal write", `h = {}
  h["outer"] = {}
  h["outer"][1] = "x"`},
	}

	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, "def run()\n  "+tc.body+"\nend\n")
			requireCallErrorContains(t, script, "run", nil, CallOptions{},
				"hash keys must be strings or symbols")
		})
	}
}

func TestHashKeyRejectionNamesAValidConversion(t *testing.T) {
	t.Parallel()

	// The rejection tells the author to convert the key with to_s; that advice
	// has to be executable for every key kind the message can be raised for.
	script := compileScriptDefault(t, `def run()
  h = {}
  h[1.to_s] = "int"
  h[1.5.to_s] = "float"
  h[true.to_s] = "bool"
  h[nil.to_s] = "nil"
  h[[1, 2].to_s] = "array"
  h[(1..3).to_s] = "range"
  h[(2 ** 100).to_s] = "bignum"
  h.size
end
`)

	if got := callFunc(t, script, "run", nil).Int(); got != 7 {
		t.Fatalf("to_s keys stored = %d, want 7", got)
	}
}

func TestHashJSONRoundTripPreservesLookup(t *testing.T) {
	t.Parallel()

	script := compileScriptDefault(t, `def run()
  h = { name: "Ada", nested: { role: "eng" } }
  back = JSON.parse(JSON.stringify(h))
  parts = []
  parts.push(back[:name])
  parts.push(back["name"])
  parts.push(back[:nested][:role])
  parts.push(back["nested"]["role"])
  parts.push(back.keys.inspect)
  parts.push((back == h).to_s)
  parts.join("|")
end
`)

	got := callFunc(t, script, "run", nil).String()
	want := `Ada|Ada|eng|eng|["name", "nested"]|true`
	if got != want {
		t.Fatalf("JSON round trip changed lookup:\n got %s\nwant %s", got, want)
	}
}
