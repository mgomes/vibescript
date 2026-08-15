//go:build !goexperiment.goroutineleakprofile

package runtime

import (
	"os"
	"testing"
)

// TestMain enables env-recycle verification for the whole package when
// VIBES_ENV_RECYCLE_VERIFY=1 is set, then runs the suite. Poisoning every
// recycled call frame turns any missed capture site into a panic on the next
// access, so running the full corpus under this flag is the primary gate on the
// call-frame recycler's correctness (see acquireCallEnv / recycleCallEnv). The
// flag is set once here, before any test starts, and never mutated afterward, so
// production code and tests only ever read it — there is no data race even under
// -race with parallel tests.
//
// A separate TestMain built under the goroutineleak experiment honors the same
// toggle; the build tags keep the two from colliding.
func TestMain(m *testing.M) {
	maybeEnableEnvRecycleVerify()
	maybeEnableEstimatorVerify()
	maybeEnableBuiltinContractVerify()
	os.Exit(m.Run())
}
