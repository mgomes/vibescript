// Package value defines the runtime Value type and its supporting
// domain-shaped types (Money, Duration, Range, time helpers) used
// throughout Vibescript. The vibes package re-exports these via type
// aliases so the embedder-facing surface stays unchanged during the
// carve-out.
//
// Scope: this package intentionally houses both the runtime-value
// plumbing (Value, ValueKind, constructors, accessors, kind
// conversions) AND the domain-shaped scalar types (Money, Duration,
// Range, time helpers). They live together because each domain type
// is also a Value payload: NewMoney(m) wraps a Money, KindMoney tags
// it, and Value.Money() unwraps it. Splitting the domain scalars
// into a separate vibes/domain package would force value/ to import
// domain/ purely to define those payload kinds, then require a
// three-layer alias chain (domain -> value -> vibes) for every
// exposed type. The Value-payload coupling outweighs the
// organizational benefit of a standalone domain package, so the
// scalars stay here. See CHANGELOG entry for PR-3.4 for the
// decision record.
package value
