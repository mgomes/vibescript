package runtime

// This file reproduces the part of the Go runtime's allocator size-class
// rounding that governs how large a backing array strings.Builder.Grow actually
// reserves. The interpolation memory checks (see projectedBuilderCap in eval.go)
// must charge the bytes the allocator will really reserve, not the bytes the
// caller asked for, or a quota that sits between the requested size and the
// rounded-up size would pass the check and then let Grow allocate over the
// sandbox limit.
//
// strings.Builder.grow requests its new backing array through
// internal/bytealg.MakeNoZero, which calls runtime.roundupsize(len, noscan=true)
// and returns a slice whose capacity is that rounded size. roundedAllocSize below
// mirrors roundupsize for the noscan path so the projection equals the realized
// capacity exactly. roundupsize_exact_test.go pins this mirror to the live
// strings.Builder.Grow capacity across the full size range, so any future change
// to Go's size classes is caught by CI rather than silently under-charging.
//
// The size-class tables are a stable, documented property of the Go runtime
// (internal/runtime/gc/sizeclasses.go), which cannot be imported from outside the
// standard library; mirroring them here is the only way to project the realized
// capacity without either importing runtime internals or over-charging the
// fast path with a loose upper bound.

const (
	// maxSmallSize is the largest object the runtime services from a size class;
	// requests above it are rounded up to a whole number of pages.
	maxSmallSize = 32768
	// smallSizeMax is the boundary between the 8-byte-granularity class table and
	// the 128-byte-granularity class table.
	smallSizeMax = 1024
	// smallSizeDiv and largeSizeDiv are the index strides of the two class
	// lookup tables.
	smallSizeDiv = 8
	largeSizeDiv = 128
	// mallocHeaderSize is the per-object header the runtime reserves for scannable
	// allocations. strings.Builder backing arrays are noscan, so no header is
	// added, but roundupsize still uses it to decide the small/large boundary.
	mallocHeaderSize = 8
	// allocPageSize is the runtime page size (1 << gc.PageShift); large objects
	// are rounded up to a multiple of it.
	allocPageSize = 8192
)

// sizeToSizeClass8 maps divRoundUp(size, smallSizeDiv) to a size class for
// requests up to smallSizeMax-8. Copied verbatim from
// internal/runtime/gc/sizeclasses.go.
var sizeToSizeClass8 = [smallSizeMax/smallSizeDiv + 1]uint8{0, 1, 2, 3, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16, 17, 17, 18, 18, 19, 19, 19, 19, 20, 20, 20, 20, 21, 21, 21, 21, 22, 22, 22, 22, 23, 23, 23, 23, 24, 24, 24, 24, 25, 25, 25, 25, 26, 26, 26, 26, 27, 27, 27, 27, 27, 27, 27, 27, 28, 28, 28, 28, 28, 28, 28, 28, 29, 29, 29, 29, 29, 29, 29, 29, 30, 30, 30, 30, 30, 30, 30, 30, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 31, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32, 32}

// sizeToSizeClass128 maps divRoundUp(size-smallSizeMax, largeSizeDiv) to a size
// class for requests above smallSizeMax-8 up to maxSmallSize. Copied verbatim
// from internal/runtime/gc/sizeclasses.go.
var sizeToSizeClass128 = [(maxSmallSize-smallSizeMax)/largeSizeDiv + 1]uint8{32, 33, 34, 35, 36, 37, 37, 38, 38, 39, 39, 40, 40, 40, 41, 41, 41, 42, 43, 43, 44, 44, 44, 44, 44, 45, 45, 45, 45, 45, 45, 46, 46, 46, 46, 47, 47, 47, 47, 47, 47, 48, 48, 48, 49, 49, 50, 51, 51, 51, 51, 51, 51, 51, 51, 51, 51, 52, 52, 52, 52, 52, 52, 52, 52, 52, 52, 53, 53, 54, 54, 54, 54, 55, 55, 55, 55, 55, 56, 56, 56, 56, 56, 56, 56, 56, 56, 56, 56, 57, 57, 57, 57, 57, 57, 57, 57, 57, 57, 58, 58, 58, 58, 58, 58, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 59, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 60, 61, 61, 61, 61, 61, 62, 62, 62, 62, 62, 62, 62, 62, 62, 62, 62, 63, 63, 63, 63, 63, 63, 63, 63, 63, 63, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 64, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 65, 66, 66, 66, 66, 66, 66, 66, 66, 66, 66, 66, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67, 67}

// sizeClassToSize maps a size-class index to the object size that class serves.
// Copied verbatim from internal/runtime/gc/sizeclasses.go.
var sizeClassToSize = [...]uint16{0, 8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240, 256, 288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024, 1152, 1280, 1408, 1536, 1792, 2048, 2304, 2688, 3072, 3200, 3456, 4096, 4864, 5376, 6144, 6528, 6784, 6912, 8192, 9472, 9728, 10240, 10880, 12288, 13568, 14336, 16384, 18432, 19072, 20480, 21760, 24576, 27264, 28672, 32768}

// divRoundUp returns ceil(n / a). It matches the runtime helper of the same name
// used to index the size-class tables.
func divRoundUp(n, a int) int {
	return (n + a - 1) / a
}

// roundedAllocSize reports the backing-array capacity the Go allocator reserves
// for a noscan request of size bytes, mirroring runtime.roundupsize(size, true)
// (the path strings.Builder.grow takes through bytealg.MakeNoZero). The returned
// value is always >= size. size must be non-negative.
//
// For requests the allocator services from a size class the result is the class
// size; for larger requests it is size rounded up to a whole number of pages.
// On page-alignment overflow the unrounded size is returned, which still bounds
// the projection conservatively because such a request cannot be satisfied at
// all.
func roundedAllocSize(size int) int {
	if size <= maxSmallSize-mallocHeaderSize {
		if size <= smallSizeMax-8 {
			return int(sizeClassToSize[sizeToSizeClass8[divRoundUp(size, smallSizeDiv)]])
		}
		return int(sizeClassToSize[sizeToSizeClass128[divRoundUp(size-smallSizeMax, largeSizeDiv)]])
	}
	rounded := size + allocPageSize - 1
	if rounded < size {
		return size
	}
	return rounded &^ (allocPageSize - 1)
}
