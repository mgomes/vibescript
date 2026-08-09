package runtime

import "testing"

// The estimator has two recursive descents: value, over the reachable value
// graph, and env, over the environment chain. The walk counter that
// chargeEstimatorWalk bills only incremented in value, and a frame that binds
// nothing calls value zero times, so an arbitrarily long environment chain was
// walked -- one seen-set probe and one parent step per frame -- and reported as
// no work at all. Under 64 nested zero-parameter block drivers a hash literal's
// accounting sessions walked 13,923,112 env frames against 38,431 values, and
// the nodes those sessions counted stayed flat at 480 however deep the chain
// went (#1).
func TestEstimatorCountsEmptyEnvironmentDescents(t *testing.T) {
	t.Parallel()

	const depth = 64
	env := newEnv(nil)
	for range depth {
		env = newEnv(env)
	}

	est := newMemoryEstimator()
	before := est.walked
	est.env(env)
	if walked := est.walked - before; walked < depth {
		t.Errorf("walking a chain of %d empty environments counted %d nodes; every frame entered "+
			"costs a seen-set probe and a parent step, so the counter chargeEstimatorWalk bills "+
			"must see at least one node per frame", depth+1, walked)
	}
}

// Counting a frame twice would bill a re-walk that the seen set already
// deduplicated away, so the second walk of the same chain must be nearly free:
// it still probes each frame, but it must not report the payload work it
// skipped.
func TestEstimatorCountsDeduplicatedEnvironmentsOnce(t *testing.T) {
	t.Parallel()

	const depth = 64
	env := newEnv(nil)
	for range depth {
		env = newEnv(env)
	}

	est := newMemoryEstimator()
	est.env(env)
	before := est.walked
	est.env(env)
	if walked := est.walked - before; walked != 1 {
		t.Errorf("re-walking an already-seen chain counted %d nodes, want 1: the seen set stops the "+
			"descent at the first frame, so only that probe is work", walked)
	}
}
