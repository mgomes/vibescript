package vibes

import (
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// Type aliases for types that have moved to vibes/value. These keep the
// vibes package source-compatible with external embedders and with the
// rest of vibes during the transition. They will be removed in v0.29.0
// once direct vibes/value imports replace these forwarders.
type (
	Value     = value.Value
	ValueKind = value.ValueKind
	Money     = value.Money
	Duration  = value.Duration
	Range     = value.Range
)

// sliceIdentity is kept as a private alias so existing internal call
// sites compile without churn. New code should use value.SliceIdentity.
type sliceIdentity = value.SliceIdentity

// Kind constants re-exported from vibes/value.
const (
	KindNil       = value.KindNil
	KindBool      = value.KindBool
	KindInt       = value.KindInt
	KindFloat     = value.KindFloat
	KindString    = value.KindString
	KindArray     = value.KindArray
	KindHash      = value.KindHash
	KindFunction  = value.KindFunction
	KindBuiltin   = value.KindBuiltin
	KindMoney     = value.KindMoney
	KindDuration  = value.KindDuration
	KindTime      = value.KindTime
	KindSymbol    = value.KindSymbol
	KindObject    = value.KindObject
	KindRange     = value.KindRange
	KindBlock     = value.KindBlock
	KindEnum      = value.KindEnum
	KindEnumValue = value.KindEnumValue
	KindClass     = value.KindClass
	KindInstance  = value.KindInstance
)

// Constructors re-exported from vibes/value.
var (
	NewNil      = value.NewNil
	NewBool     = value.NewBool
	NewInt      = value.NewInt
	NewFloat    = value.NewFloat
	NewString   = value.NewString
	NewArray    = value.NewArray
	NewHash     = value.NewHash
	NewSymbol   = value.NewSymbol
	NewObject   = value.NewObject
	NewMoney    = value.NewMoney
	NewDuration = value.NewDuration
	NewTime     = value.NewTime
	NewRange    = value.NewRange
)

// Helper functions re-exported from vibes/value. These remain reachable
// under their original unexported names through thin wrappers below.
func valueToInt64(val Value) (int64, error) { return value.ValueToInt64(val) }

func parseMoneyLiteral(input string) (Money, error) { return value.ParseMoneyLiteral(input) }

func newMoneyFromCents(cents int64, currency string) (Money, error) {
	return value.NewMoneyFromCents(cents, currency)
}

func parseDurationString(input string) (Duration, error) { return value.ParseDurationString(input) }

func numericToSeconds(val Value) (int64, error) { return value.NumericToSeconds(val) }

func durationFromParts(weeks, days, hours, minutes, seconds int64) Duration {
	return value.DurationFromParts(weeks, days, hours, minutes, seconds)
}

func secondsDuration(v int64, unit string) Duration { return value.SecondsDuration(v, unit) }

func durationFromSeconds(seconds int64) Duration { return value.DurationFromSeconds(seconds) }

func parseLocation(val Value) (*time.Location, error) { return value.ParseLocation(val) }

func parseLocationString(spec string) (*time.Location, error) { return value.ParseLocationString(spec) }

func timeFromParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	return value.TimeFromParts(args, defaultLoc)
}

func timeFromEpoch(val Value, loc *time.Location) (time.Time, error) {
	return value.TimeFromEpoch(val, loc)
}

func parseTimeString(input, layout string, hasLayout bool, loc *time.Location) (time.Time, error) {
	return value.ParseTimeString(input, layout, hasLayout, loc)
}
