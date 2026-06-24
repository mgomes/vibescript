package runtime

import (
	"math"
	"strings"
	"testing"
)

// TestRoundedAllocSizeMatchesBuilderGrow pins roundedAllocSize to the actual
// backing-array capacity strings.Builder.Grow reserves. strings.Builder.grow
// requests 2*cap+n bytes through bytealg.MakeNoZero, which rounds the request up
// to an allocator size class; roundedAllocSize mirrors that rounding so the
// interpolation memory projection equals the realized reservation. Probing the
// live builder makes this a closed loop: if a future Go release changes the size
// classes, roundedAllocSize stops matching and this test fails rather than the
// projection silently under-charging.
func TestRoundedAllocSizeMatchesBuilderGrow(t *testing.T) {
	t.Parallel()

	// Collect a spread of realizable starting capacities, then for each one grow
	// by a wide range of n that forces a reallocation and compare the realized
	// capacity to roundedAllocSize(2*cap+n).
	starts := map[int]struct{}{0: {}}
	for _, n := range []int{1, 7, 8, 9, 16, 24, 33, 100, 500, 1000, 1024, 1025, 2000, 4096, 8192, 16384, 32760, 32768, 40000, 70000} {
		var b strings.Builder
		b.Grow(n)
		starts[b.Cap()] = struct{}{}
	}

	growBy := []int{1, 7, 8, 9, 17, 100, 1000, 4096, 5000, 8192, 16384, 33000, 50000, 80000}

	for start := range starts {
		for _, n := range growBy {
			var b strings.Builder
			if start > 0 {
				b.Grow(start)
			}
			base := b.Cap() // free tail == base because Len() == 0

			b.Grow(n)
			got := b.Cap()

			var want int
			if base >= n {
				// No reallocation: capacity is unchanged.
				want = base
			} else {
				request := 2*base + n
				want = roundedAllocSize(request)
				if want < request {
					t.Errorf("roundedAllocSize(%d)=%d under-charges the allocator request",
						request, want)
				}
			}

			if got != want {
				t.Errorf("Grow from cap %d by %d: realized cap %d, projected %d",
					base, n, got, want)
			}
		}
	}
}

// TestRoundedAllocSizeNeverUnderRequest checks the projection never reports fewer
// bytes than requested across the small-class boundary, the large/page-aligned
// boundary, and an int saturation case. An under-report would let a quota check
// pass while the real allocation exceeds it, the exact gap Codex flagged.
func TestRoundedAllocSizeNeverUnderRequest(t *testing.T) {
	t.Parallel()

	cases := []int{
		0, 1, 7, 8, 9,
		smallSizeMax - 8, smallSizeMax - 7, smallSizeMax,
		maxSmallSize - mallocHeaderSize, maxSmallSize - mallocHeaderSize + 1,
		maxSmallSize, maxSmallSize + 1,
		1 << 20,
		math.MaxInt, // saturated request: page rounding overflows, must return >= size
	}
	for _, size := range cases {
		got := roundedAllocSize(size)
		if got < size {
			t.Errorf("roundedAllocSize(%d) = %d, must be >= request", size, got)
		}
	}
}

// TestProjectedBuilderCapChargesRoundedCapacity pins the gap the rounding closes:
// for a builder grown past its backing, the un-rounded estimate 2*cap+n can fall
// below the size class the allocator actually reserves. projectedBuilderCap must
// charge the rounded (realized) capacity so a quota sitting between the un-rounded
// estimate and the real reservation rejects the growth instead of allowing the
// over-limit allocation.
func TestProjectedBuilderCapChargesRoundedCapacity(t *testing.T) {
	t.Parallel()

	// Fill a 10 KiB builder, then grow it by another 10 KiB: the request is
	// 2*10240+10240 = 30720 bytes, which the allocator rounds up to the
	// 32768-byte size class. The builder must be full so the next append cannot
	// fit the free tail and a reallocation is actually projected.
	const start = 10240
	const n = 10240

	var sb strings.Builder
	sb.Grow(start)
	sb.WriteString(strings.Repeat("a", start))
	base := sb.Cap()

	unrounded := 2*base + n
	projected := projectedBuilderCap(&sb, n)

	if projected != roundedAllocSize(unrounded) {
		t.Fatalf("projectedBuilderCap = %d, want roundedAllocSize(%d) = %d",
			projected, unrounded, roundedAllocSize(unrounded))
	}
	if projected <= unrounded {
		t.Fatalf("rounded projection %d must exceed the un-rounded estimate %d so the size-class gap is charged",
			projected, unrounded)
	}

	// The realized capacity after the same growth must equal what we charged.
	sb.Grow(n)
	if realized := sb.Cap(); realized != projected {
		t.Fatalf("projection %d does not match realized builder capacity %d", projected, realized)
	}
}

// TestProjectedBuilderCapFastPathNoRealloc verifies that when the value fits the
// builder's free tail no reallocation is projected: the current capacity is
// returned unchanged, leaving the small-interpolation fast path untouched.
func TestProjectedBuilderCapFastPathNoRealloc(t *testing.T) {
	t.Parallel()

	var sb strings.Builder
	sb.Grow(64)
	sb.WriteString("ab")
	base := sb.Cap()

	fits := base - sb.Len()
	if got := projectedBuilderCap(&sb, fits); got != base {
		t.Fatalf("projectedBuilderCap for a value fitting the free tail = %d, want unchanged cap %d", got, base)
	}
}
