package runtime

import (
	"time"

	"github.com/mgomes/vibescript/vibes/value"
)

// Internal aliases for the value package types so runtime code can keep
// referring to short names (Value, Money, KindInt, NewNil, etc.) without
// repeating the value. prefix everywhere. These mirror the public
// re-exports in vibes/value_alias.go and exist purely to keep the
// runtime sources readable after the move out of package vibes.
type (
	Value     = value.Value
	ValueKind = value.ValueKind
	Money     = value.Money
	Duration  = value.Duration
	Range     = value.Range
)

type sliceIdentity = value.SliceIdentity

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

// NewNil returns a nil Value.
func NewNil() Value { return value.NewNil() }

// NewBool returns a boolean Value.
func NewBool(b bool) Value { return value.NewBool(b) }

// NewInt returns an integer Value.
func NewInt(i int64) Value { return value.NewInt(i) }

// NewFloat returns a floating-point Value.
func NewFloat(f float64) Value { return value.NewFloat(f) }

// NewString returns a string Value.
func NewString(s string) Value { return value.NewString(s) }

// NewArray returns an array Value.
func NewArray(a []Value) Value { return value.NewArray(a) }

// NewHash returns a hash (map) Value.
func NewHash(h map[string]Value) Value { return value.NewHash(h) }

// NewSymbol returns a symbol Value.
func NewSymbol(name string) Value { return value.NewSymbol(name) }

// NewObject returns an object Value with the given attributes.
func NewObject(attrs map[string]Value) Value { return value.NewObject(attrs) }

// NewMoney returns a money Value.
func NewMoney(m Money) Value { return value.NewMoney(m) }

// NewDuration returns a duration Value.
func NewDuration(d Duration) Value { return value.NewDuration(d) }

// NewTime returns a time Value.
func NewTime(t time.Time) Value { return value.NewTime(t) }

// NewRange returns a range Value.
func NewRange(r Range) Value { return value.NewRange(r) }

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
