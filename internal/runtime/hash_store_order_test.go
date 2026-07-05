package runtime

import (
	"context"
	"testing"
)

// TestHashStoreKeepsLegacyReceiverKeyPosition pins position-preserving store
// on a bare host-map receiver: replacing an existing key keeps its sorted
// position instead of moving it to the end.
func TestHashStoreKeepsLegacyReceiverKeyPosition(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `def run(h)
  h.store("b", 9).keys
end`)

	host := NewHash(map[string]Value{"a": NewInt(1), "b": NewInt(2), "c": NewInt(3)})
	got := callScript(t, context.Background(), script, "run", []Value{host}, CallOptions{})
	compareArrays(t, got, []Value{NewString("a"), NewString("b"), NewString("c")})
}
