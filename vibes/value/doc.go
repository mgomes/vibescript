// Package value defines the runtime Value type and its supporting
// domain-shaped types (Money, Duration, Range, time helpers) used
// throughout Vibescript. Hosts import this package directly when
// passing arguments, reading results, building globals, or implementing
// first-party capability interfaces.
//
// Scope: this package intentionally houses both the runtime-value
// plumbing (Value, ValueKind, constructors, accessors, kind
// conversions) AND the domain-shaped scalar types (Money, Duration,
// Range, time helpers). They live together because each domain type
// is also a Value payload: NewMoney(m) wraps a Money, KindMoney tags
// it, and Value.Money() unwraps it. Splitting the domain scalars
// into a separate vibes/domain package would force value/ to import
// domain/ purely to define those payload kinds. The Value-payload
// coupling outweighs the organizational benefit of a standalone domain
// package, so the scalars stay here.
//
// # Ownership and isolation
//
// The Script.Call boundary copies in both directions, so hosts and
// scripts never share mutable state:
//
//   - Values returned from Script.Call are the host's to keep and
//     mutate. Every Call returns an independent copy; mutating a result
//     (directly or through this package's hash and array operations)
//     never changes what a later Call observes.
//   - Values passed INTO Script.Call — arguments and
//     CallOptions.Globals — are never mutated by the script.
//     Script-side mutation acts on the call's own copy; the host's
//     originals are untouched when Call returns.
//   - The host still owns the Values it passed in. Mutating them
//     BETWEEN calls is safe, and the next Call observes the new
//     contents (globals are re-read from the host's map at each call).
//
// # Concurrency
//
// Value wrappers are not synchronized. Two goroutines may read the
// same Value concurrently, but any mutation concurrent with another
// access is a data race. In particular, mutating a Value while a
// Script.Call that was given that Value is running is forbidden: the
// interpreter materializes globals lazily during the call, and the
// race is not merely stale data — concurrent map access makes the Go
// runtime terminate the whole host process ("fatal error: concurrent
// map read and map write"), which cannot be recovered. Hand a Value to
// a running call and leave it alone until the call returns.
//
// # Quotas
//
// Engine quotas (steps, memory) meter script execution only. Host-side
// construction and mutation through this package are unmetered: no
// quota observes a host growing a Value between calls, and host-only
// graphs are never charged against a later call's memory quota because
// the estimator walks only state reachable from the execution.
//
// # Supported API versus internal plumbing
//
// Some exported symbols exist only because the interpreter's runtime
// lives in a separate package (internal/runtime) and needs
// cross-package access to Value internals. Their doc comments say
// "intended for the interpreter's internal use"; they carry no
// compatibility promise and may change or disappear in any release.
// The tier assignment for every exported symbol is declared in
// docs/embedding-api-stability.md.
package value
