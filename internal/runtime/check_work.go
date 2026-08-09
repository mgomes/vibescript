package runtime

import "sync/atomic"

// checkWorkCounting and checkWorkUnits let a test count the elements the static
// check inspects, copies, or materializes at the sites whose cost was claimed
// to grow with the square of the source. Elapsed time would fold in scheduling,
// GC, and the race and coverage instrumentation this repository runs across
// three operating systems, and the clock is too coarse on Windows to compare
// runs this short. Each instrumented site adds the units it handles, so a test
// drives a source that reaches exactly one of them. Never set outside tests;
// when off this costs one relaxed load per site.
var (
	checkWorkCounting atomic.Bool
	checkWorkUnits    atomic.Uint64
)

func noteCheckWork(units int) {
	if checkWorkCounting.Load() {
		checkWorkUnits.Add(uint64(units))
	}
}
