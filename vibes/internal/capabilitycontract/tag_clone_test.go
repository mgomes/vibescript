package capabilitycontract

import (
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

// Capability boundaries copy values to isolate them, and the copy stands for
// the same value. Rebuilding an attribute bag with value.NewObject stripped
// the provenance the runtime attached, so a rescued error sent through a
// packaged capability came back as an ordinary bag.
func TestCloneHelpersPreserveObjectTags(t *testing.T) {
	t.Parallel()

	entries := map[string]value.Value{
		"to_s":      value.NewString("boom"),
		"message":   value.NewString("boom"),
		"class":     value.NewString("RuntimeError"),
		"type":      value.NewString("RuntimeError"),
		"backtrace": value.NewArray([]value.Value{}),
	}
	tagged := value.NewTaggedObject(entries, value.ObjectTagRescuedError)

	t.Run("DeepCloneValue", func(t *testing.T) {
		t.Parallel()
		if got := DeepCloneValue(tagged).ObjectTag(); got != value.ObjectTagRescuedError {
			t.Fatalf("DeepCloneValue produced tag %v, want the rescued-error tag", got)
		}
	})

	t.Run("CloneDataOnlyValue", func(t *testing.T) {
		t.Parallel()
		cloned, err := CloneDataOnlyValue("probe", tagged)
		if err != nil {
			t.Fatalf("CloneDataOnlyValue: %v", err)
		}
		if got := cloned.ObjectTag(); got != value.ObjectTagRescuedError {
			t.Fatalf("CloneDataOnlyValue produced tag %v, want the rescued-error tag", got)
		}
	})

	t.Run("untagged stays untagged", func(t *testing.T) {
		t.Parallel()
		if got := DeepCloneValue(value.NewObject(entries)).ObjectTag(); got != value.ObjectTagNone {
			t.Fatalf("an untagged wrapper cloned with tag %v", got)
		}
	})
}
