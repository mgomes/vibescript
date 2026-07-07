package runtime

import (
	"math/big"
	"testing"

	"github.com/mgomes/vibescript/vibes/value"
)

// bigIntTestValue returns a KindInt Value carrying a big payload of roughly
// words machine words.
func bigIntTestValue(words int) Value {
	bi := new(big.Int).Lsh(big.NewInt(1), uint(words*64))
	return value.AdoptBigInt(bi)
}

func TestMemoryEstimatorChargesBigIntPayload(t *testing.T) {
	t.Parallel()
	val := bigIntTestValue(1000)
	bi, ok := value.BigIntPayload(val)
	if !ok {
		t.Fatalf("expected a big payload")
	}

	est := newMemoryEstimator()
	got := est.value(val)
	want := estimatedValueBytes + estimatedBigIntStructBytes + cap(bi.Bits())*estimatedBigIntWordBytes
	if got != want {
		t.Fatalf("big payload charge = %d; want %d", got, want)
	}
	if got < estimatedValueBytes+1000*8 {
		t.Fatalf("big payload charge %d is below the word backing size", got)
	}
}

func TestMemoryEstimatorDeduplicatesAliasedBigIntPayload(t *testing.T) {
	t.Parallel()
	val := bigIntTestValue(1000)

	est := newMemoryEstimator()
	first := est.value(val)
	second := est.value(val)
	if second != estimatedValueBytes {
		t.Fatalf("aliased big payload should add only the Value size %d, got %d (first=%d)", estimatedValueBytes, second, first)
	}
}

func TestMemoryEstimatorChargesCompactIntNothingExtra(t *testing.T) {
	t.Parallel()
	est := newMemoryEstimator()
	if got := est.value(NewInt(12345)); got != estimatedValueBytes {
		t.Fatalf("compact int charge = %d; want %d", got, estimatedValueBytes)
	}
}

func TestMemoryEstimatorJournalRollsBackBigIntPayload(t *testing.T) {
	t.Parallel()
	val := bigIntTestValue(8)
	bi, _ := value.BigIntPayload(val)

	est := newMemoryEstimator()
	journal := &estimatorJournal{}
	est.journal = journal
	full := est.value(val)
	journal.rollback(est, nil)
	journal.clear()
	est.journal = nil

	if _, seen := est.seenBigInts[bi]; seen {
		t.Fatalf("rollback left the big payload committed")
	}
	if again := est.value(val); again != full {
		t.Fatalf("post-rollback walk charged %d; want the full %d", again, full)
	}
}
