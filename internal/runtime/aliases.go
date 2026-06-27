package runtime

import (
	"fmt"
	"reflect"
	"time"

	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes/source"
	"github.com/mgomes/vibescript/vibes/value"
)

// Position is an internal alias for source.Position so runtime code can
// use the short name. AST and other internal aliases below mirror the
// vibes facade re-exports.
type Position = source.Position

type (
	Node       = ast.Node
	Statement  = ast.Statement
	Expression = ast.Expression
	Program    = ast.Program

	Param     = ast.Param
	ParamKind = ast.ParamKind
	TypeExpr  = ast.TypeExpr
	TypeKind  = ast.TypeKind

	Token     = ast.Token
	TokenType = ast.TokenType

	FunctionStmt   = ast.FunctionStmt
	ReturnStmt     = ast.ReturnStmt
	RaiseStmt      = ast.RaiseStmt
	AssignStmt     = ast.AssignStmt
	ExprStmt       = ast.ExprStmt
	IfStmt         = ast.IfStmt
	ForStmt        = ast.ForStmt
	WhileStmt      = ast.WhileStmt
	UntilStmt      = ast.UntilStmt
	BreakStmt      = ast.BreakStmt
	NextStmt       = ast.NextStmt
	TryStmt        = ast.TryStmt
	PropertyDecl   = ast.PropertyDecl
	ClassStmt      = ast.ClassStmt
	EnumMemberStmt = ast.EnumMemberStmt
	EnumStmt       = ast.EnumStmt

	Identifier         = ast.Identifier
	IntegerLiteral     = ast.IntegerLiteral
	FloatLiteral       = ast.FloatLiteral
	StringLiteral      = ast.StringLiteral
	BoolLiteral        = ast.BoolLiteral
	NilLiteral         = ast.NilLiteral
	SymbolLiteral      = ast.SymbolLiteral
	ArrayLiteral       = ast.ArrayLiteral
	HashPair           = ast.HashPair
	HashLiteral        = ast.HashLiteral
	CallExpr           = ast.CallExpr
	KeywordArg         = ast.KeywordArg
	MemberExpr         = ast.MemberExpr
	ScopeExpr          = ast.ScopeExpr
	IndexExpr          = ast.IndexExpr
	DestructureElement = ast.DestructureElement
	DestructureTarget  = ast.DestructureTarget
	IvarExpr           = ast.IvarExpr
	ClassVarExpr       = ast.ClassVarExpr
	UnaryExpr          = ast.UnaryExpr
	BinaryExpr         = ast.BinaryExpr
	ConditionalExpr    = ast.ConditionalExpr
	IfExprBranch       = ast.IfExprBranch
	IfExpr             = ast.IfExpr
	RangeExpr          = ast.RangeExpr
	CaseWhenClause     = ast.CaseWhenClause
	CaseExpr           = ast.CaseExpr
	BlockLiteral       = ast.BlockLiteral
	YieldExpr          = ast.YieldExpr
	InterpolatedString = ast.InterpolatedString
	InterpolatedSymbol = ast.InterpolatedSymbol
	StringPart         = ast.StringPart
	StringText         = ast.StringText
	StringExpr         = ast.StringExpr
)

const (
	ParamNormal      = ast.ParamNormal
	ParamKeyword     = ast.ParamKeyword
	ParamRest        = ast.ParamRest
	ParamKeywordRest = ast.ParamKeywordRest
	ParamBlock       = ast.ParamBlock
)

const (
	TypeAny      = ast.TypeAny
	TypeInt      = ast.TypeInt
	TypeFloat    = ast.TypeFloat
	TypeNumber   = ast.TypeNumber
	TypeString   = ast.TypeString
	TypeBool     = ast.TypeBool
	TypeNil      = ast.TypeNil
	TypeDuration = ast.TypeDuration
	TypeTime     = ast.TypeTime
	TypeMoney    = ast.TypeMoney
	TypeArray    = ast.TypeArray
	TypeHash     = ast.TypeHash
	TypeFunction = ast.TypeFunction
	TypeShape    = ast.TypeShape
	TypeUnion    = ast.TypeUnion
	TypeEnum     = ast.TypeEnum
	TypeUnknown  = ast.TypeUnknown
)

const (
	tokenIllegal   = ast.TokenIllegal
	tokenEOF       = ast.TokenEOF
	tokenIdent     = ast.TokenIdent
	tokenInt       = ast.TokenInt
	tokenFloat     = ast.TokenFloat
	tokenString    = ast.TokenString
	tokenSymbol    = ast.TokenSymbol
	tokenAssign    = ast.TokenAssign
	tokenPlus      = ast.TokenPlus
	tokenMinus     = ast.TokenMinus
	tokenBang      = ast.TokenBang
	tokenNot       = ast.TokenNot
	tokenAsterisk  = ast.TokenAsterisk
	tokenPower     = ast.TokenPower
	tokenSlash     = ast.TokenSlash
	tokenPercent   = ast.TokenPercent
	tokenLT        = ast.TokenLT
	tokenShovel    = ast.TokenShovel
	tokenGT        = ast.TokenGT
	tokenLTE       = ast.TokenLTE
	tokenGTE       = ast.TokenGTE
	tokenSpaceship = ast.TokenSpaceship
	tokenEQ        = ast.TokenEQ
	tokenCaseEQ    = ast.TokenCaseEQ
	tokenNotEQ     = ast.TokenNotEQ
	tokenAnd       = ast.TokenAnd
	tokenOr        = ast.TokenOr
	tokenAmpersand = ast.TokenAmpersand
	tokenQuestion  = ast.TokenQuestion
	tokenComma     = ast.TokenComma
	tokenColon     = ast.TokenColon
	tokenScope     = ast.TokenScope
	tokenDot       = ast.TokenDot
	tokenRange     = ast.TokenRange
	tokenLParen    = ast.TokenLParen
	tokenRParen    = ast.TokenRParen
	tokenLBrace    = ast.TokenLBrace
	tokenRBrace    = ast.TokenRBrace
	tokenLBracket  = ast.TokenLBracket
	tokenRBracket  = ast.TokenRBracket
	tokenPipe      = ast.TokenPipe
	tokenArrow     = ast.TokenArrow
	tokenThinArrow = ast.TokenThinArrow
	tokenIvar      = ast.TokenIvar
	tokenClassVar  = ast.TokenClassVar
	tokenDef       = ast.TokenDef
	tokenClass     = ast.TokenClass
	tokenEnum      = ast.TokenEnum
	tokenExport    = ast.TokenExport
	tokenSelf      = ast.TokenSelf
	tokenPrivate   = ast.TokenPrivate
	tokenProperty  = ast.TokenProperty
	tokenGetter    = ast.TokenGetter
	tokenSetter    = ast.TokenSetter
	tokenBegin     = ast.TokenBegin
	tokenRescue    = ast.TokenRescue
	tokenEnsure    = ast.TokenEnsure
	tokenRaise     = ast.TokenRaise
	tokenEnd       = ast.TokenEnd
	tokenReturn    = ast.TokenReturn
	tokenYield     = ast.TokenYield
	tokenDo        = ast.TokenDo
	tokenFor       = ast.TokenFor
	tokenWhile     = ast.TokenWhile
	tokenUntil     = ast.TokenUntil
	tokenBreak     = ast.TokenBreak
	tokenNext      = ast.TokenNext
	tokenIn        = ast.TokenIn
	tokenIf        = ast.TokenIf
	tokenUnless    = ast.TokenUnless
	tokenCase      = ast.TokenCase
	tokenWhen      = ast.TokenWhen
	tokenElsif     = ast.TokenElsif
	tokenElse      = ast.TokenElse
	tokenTrue      = ast.TokenTrue
	tokenFalse     = ast.TokenFalse
	tokenNil       = ast.TokenNil
)

func cloneParams(params []Param) []Param            { return ast.CloneParams(params) }
func cloneTypeExpr(ty *TypeExpr) *TypeExpr          { return ast.CloneTypeExpr(ty) }
func cloneStatements(stmts []Statement) []Statement { return ast.CloneStatements(stmts) }

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

// NewHashWithDefault returns a hash Value carrying Ruby-style default metadata
// (a default value and/or a default proc consulted on missing-key lookup).
func NewHashWithDefault(h map[string]Value, defaultValue, defaultProc Value) Value {
	return value.NewHashWithDefault(h, defaultValue, defaultProc)
}

// hashDefaultValue returns the default value configured for a hash, or nil.
func hashDefaultValue(v Value) Value { return value.HashDefaultValue(v) }

// hashDefaultProc returns the default proc (a KindBlock value) configured for a
// hash, or nil.
func hashDefaultProc(v Value) Value { return value.HashDefaultProc(v) }

// hashIdentity returns a stable identity for a hash wrapper (entries plus
// default metadata), or 0 when v is not a hash. Scanners that must also visit
// hash defaults key their seen-set on this rather than the bare entry map.
func hashIdentity(v Value) uintptr { return value.HashIdentity(v) }

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

func formatFloat(f float64) string { return value.FormatFloat(f) }

// errStringRenderTruncated re-exports value.ErrStringRenderTruncated so runtime
// callers that render a composite under a byte budget (such as the String#sub /
// gsub block forms bounding a block result before it is spliced into the result)
// can recognize a truncated rendering with errors.Is without importing the value
// package directly.
var errStringRenderTruncated = value.ErrStringRenderTruncated

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

func timeFromCalendarParts(args []Value, defaultLoc *time.Location) (time.Time, error) {
	return value.TimeFromCalendarParts(args, defaultLoc)
}

func timeFromEpochParts(secVal Value, subsecVal, unitVal *Value, loc *time.Location) (time.Time, error) {
	return value.TimeFromEpochParts(secVal, subsecVal, unitVal, loc)
}

func parseTimeString(input, layout string, hasLayout bool, loc *time.Location) (time.Time, error) {
	return value.ParseTimeString(input, layout, hasLayout, loc)
}

type hostValueCloneState struct {
	arrays map[sliceIdentity]Value
	// hashes caches cloned KindHash values keyed on the source hash's wrapper
	// identity, so a hash reachable through several paths in the returned graph
	// clones to one wrapper and keeps its identity. Caching only the entry map
	// would rebuild a fresh wrapper per path, and since hash identity is the
	// wrapper, the clones would wrongly compare not-identical. This also dedups a
	// hash that contains itself.
	hashes map[uintptr]Value
	// hashEntries caches the cloned entry map keyed on the source hash's entry map
	// pointer. Two distinct hash wrappers may intentionally share one mutable entry
	// map; index assignment mutates that map in place, so both cloned wrappers must
	// point at one cloned entry map to keep the host's aliasing. The wrapper cache
	// cannot do this because the wrappers have distinct identities.
	hashEntries map[uintptr]map[string]Value
	maps        map[uintptr]map[string]Value
	instances   map[*Instance]Value
	classes     map[*ClassDef]*ClassDef
	envs        map[*Env]*Env
	// boundBuiltins caches the clone of a receiver-bound predicate (a bound
	// eql?/equal?) keyed on the source builtin pointer. Cloning such a builtin
	// rebuilds a fresh *Builtin around the cloned receiver, so the same source
	// builtin reachable through several paths in the returned graph would otherwise
	// clone to distinct *Builtin values. equal? compares builtins by backing
	// pointer, so those distinct clones would wrongly report not-identical; caching
	// the clone keeps aliases of one bound predicate identical across the host
	// boundary.
	boundBuiltins map[*Builtin]Value
	// plainBuiltins caches the clone of a plain (non receiver-bound) builtin keyed
	// on the source builtin pointer. cloneBuiltinValue mints a fresh *Builtin for
	// each occurrence, so the same builtin reachable through several paths in the
	// returned graph (for example `p = JSON.parse; [p, p]`) would otherwise clone to
	// distinct *Builtin values. equal? compares builtins by backing pointer, so
	// those distinct clones would wrongly report not-identical; caching the clone
	// keeps aliases of one plain builtin identical across the host boundary, just
	// like the bound-builtin, function, and enum caches above.
	plainBuiltins map[*Builtin]Value
	// functions caches the clone of a script function keyed on the source
	// *ScriptFunction. cloneFunctionForHostWithState rebuilds a fresh
	// *ScriptFunction (with a cloned environment), so the same function reachable
	// through several paths in the returned graph (for example [inc, inc]) would
	// otherwise clone to distinct pointers. equal? compares functions by backing
	// pointer, so those distinct clones would wrongly report not-identical; caching
	// the clone keeps aliases of one function identical across the host boundary.
	// The clone is reserved before its environment is cloned so a function whose
	// captured environment reaches the function itself (a recursive closure)
	// dedups against the reserved clone instead of recursing forever.
	functions map[*ScriptFunction]*ScriptFunction
	// enums caches the clone of an enum definition keyed on the source *EnumDef.
	// cloneEnumDef rebuilds a fresh *EnumDef (and fresh *EnumValueDef members), so
	// the same enum or enum member reachable through several paths in the returned
	// graph (for example [Status::Draft, Status::Draft]) would otherwise clone to
	// distinct pointers. equal? compares enums and members by backing pointer, so
	// those distinct clones would wrongly report not-identical; caching the clone
	// keeps aliases of one enum member identical across the host boundary.
	enums map[*EnumDef]*EnumDef
}

type hostValueScanState struct {
	arrays map[sliceIdentity]struct{}
	maps   map[uintptr]struct{}
}

func valueNeedsHostClone(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindClass, KindInstance, KindEnum, KindEnumValue, KindBlock, KindBuiltin:
		return true
	case KindArray, KindHash, KindObject:
		return compositeValueNeedsHostClone(val)
	default:
		return false
	}
}

func compositeValueNeedsHostClone(val Value) bool {
	switch val.Kind() {
	case KindArray:
		for _, item := range val.Array() {
			if itemDirectlyNeedsHostClone(item) {
				return true
			}
			if itemCanContainHostClone(item) {
				return valueNeedsHostCloneWithFreshState(val)
			}
		}
		return false
	case KindHash, KindObject:
		// A hash carrying a Ruby-style default proc (a block) must be cloned even
		// when its entries do not, so the proc closes over the cloned environment
		// rather than the live one as it crosses the host boundary. A default
		// value may itself be (or reach) a clone-needing value through an
		// arbitrarily deep, possibly cyclic graph, so escalate to the stateful
		// scan rather than recursing without shared state here.
		if val.Kind() == KindHash {
			if !hashDefaultProc(val).IsNil() {
				return true
			}
			if !hashDefaultValue(val).IsNil() {
				return valueNeedsHostCloneWithFreshState(val)
			}
		}
		entries := val.Hash()
		if len(entries) == 0 {
			return false
		}
		for _, item := range entries {
			if itemDirectlyNeedsHostClone(item) {
				return true
			}
			if itemCanContainHostClone(item) {
				return valueNeedsHostCloneWithFreshState(val)
			}
		}
		return false
	default:
		return valueNeedsHostClone(val)
	}
}

// hashDefaultNeedsHostClone reports whether a hash's default metadata requires a
// host clone: a default proc is always a block (clone-needed), and a default
// value is clone-needed when it directly is or can contain a runtime value. It
// threads the caller's scan state so a default value that cycles back to its own
// hash (e.g. d = {}; h = Hash.new(d); d[:h] = h) terminates instead of
// recursing forever.
func hashDefaultNeedsHostClone(val Value, state hostValueScanState) bool {
	if !hashDefaultProc(val).IsNil() {
		return true
	}
	if def := hashDefaultValue(val); !def.IsNil() {
		return valueNeedsHostCloneWithState(def, state)
	}
	return false
}

func valueNeedsHostCloneWithFreshState(val Value) bool {
	state := hostValueScanState{
		arrays: make(map[sliceIdentity]struct{}),
		maps:   make(map[uintptr]struct{}),
	}
	return valueNeedsHostCloneWithState(val, state)
}

func itemDirectlyNeedsHostClone(val Value) bool {
	switch val.Kind() {
	case KindFunction, KindClass, KindInstance, KindEnum, KindEnumValue, KindBlock, KindBuiltin:
		return true
	default:
		return false
	}
}

func itemCanContainHostClone(val Value) bool {
	switch val.Kind() {
	case KindArray, KindHash, KindObject:
		return true
	default:
		return false
	}
}

func valueNeedsHostCloneWithState(val Value, state hostValueScanState) bool {
	switch val.Kind() {
	case KindFunction, KindClass, KindInstance, KindEnum, KindEnumValue, KindBlock, KindBuiltin:
		return true
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(items).Pointer(),
			Len: len(items),
			Cap: cap(items),
		}
		if id.Ptr != 0 {
			if _, ok := state.arrays[id]; ok {
				return false
			}
			state.arrays[id] = struct{}{}
		}
		for _, item := range items {
			if valueNeedsHostCloneWithState(item, state) {
				return true
			}
		}
		return false
	case KindHash, KindObject:
		entries := val.Hash()
		// Key on the whole hash wrapper (or the entry-map pointer for objects) so
		// two wrappers sharing an entry map but carrying distinct defaults are each
		// scanned: a second wrapper's clone-needing default is not skipped, and a
		// default cycling back to this wrapper terminates at the seen check.
		ptr := hashIdentity(val)
		if ptr == 0 {
			ptr = reflect.ValueOf(entries).Pointer()
		}
		if ptr != 0 {
			if _, ok := state.maps[ptr]; ok {
				return false
			}
			state.maps[ptr] = struct{}{}
		}
		// A default proc or clone-needing default value forces a clone even when
		// the entries do not need one; only KindHash carries defaults. The wrapper
		// is marked seen above first, so a default value cycling back to this hash
		// terminates at the seen check.
		if val.Kind() == KindHash && hashDefaultNeedsHostClone(val, state) {
			return true
		}
		for _, item := range entries {
			if valueNeedsHostCloneWithState(item, state) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func cloneValueForHost(val Value) Value {
	state := hostValueCloneState{
		arrays:        make(map[sliceIdentity]Value),
		hashes:        make(map[uintptr]Value),
		hashEntries:   make(map[uintptr]map[string]Value),
		maps:          make(map[uintptr]map[string]Value),
		instances:     make(map[*Instance]Value),
		classes:       make(map[*ClassDef]*ClassDef),
		envs:          make(map[*Env]*Env),
		boundBuiltins: make(map[*Builtin]Value),
		plainBuiltins: make(map[*Builtin]Value),
		functions:     make(map[*ScriptFunction]*ScriptFunction),
		enums:         make(map[*EnumDef]*EnumDef),
	}
	return cloneValueForHostWithState(val, state)
}

func cloneValueForHostWithState(val Value, state hostValueCloneState) Value {
	switch val.Kind() {
	case KindArray:
		items := val.Array()
		id := sliceIdentity{
			Ptr: reflect.ValueOf(items).Pointer(),
			Len: len(items),
			Cap: cap(items),
		}
		if id.Ptr != 0 {
			if clone, ok := state.arrays[id]; ok {
				return clone
			}
		}
		clonedItems := make([]Value, len(items))
		cloned := NewArray(clonedItems)
		if id.Ptr != 0 {
			state.arrays[id] = cloned
		}
		for i, item := range items {
			clonedItems[i] = cloneValueForHostWithState(item, state)
		}
		return cloned
	case KindHash:
		return cloneHostHashValue(val, state)
	case KindObject:
		return cloneHostMapValue(val, state, NewObject)
	case KindFunction:
		return NewFunction(cloneFunctionForHostWithState(valueFunction(val), state))
	case KindClass:
		return NewClass(cloneClassForHostWithState(valueClass(val), state))
	case KindInstance:
		inst := valueInstance(val)
		if inst == nil {
			return val
		}
		if clone, ok := state.instances[inst]; ok {
			return clone
		}
		clonedClass := inst.Class
		if inst.Class != nil {
			clonedClass = cloneClassForHostWithState(inst.Class, state)
		}
		clonedIvars := make(map[string]Value, len(inst.Ivars))
		cloned := NewInstance(&Instance{Class: clonedClass, Ivars: clonedIvars})
		state.instances[inst] = cloned
		for name, ivar := range inst.Ivars {
			clonedIvars[name] = cloneValueForHostWithState(ivar, state)
		}
		return cloned
	case KindEnum:
		enumDef := valueEnum(val)
		if enumDef == nil {
			return val
		}
		return NewEnum(cloneEnumForHost(enumDef, state))
	case KindEnumValue:
		member := valueEnumValue(val)
		if member == nil || member.Enum == nil {
			return val
		}
		enumClone := cloneEnumForHost(member.Enum, state)
		if memberClone, ok := enumClone.Members[member.Name]; ok {
			return NewEnumValue(memberClone)
		}
		if memberClone, ok := enumClone.MembersByKey[member.Symbol]; ok {
			return NewEnumValue(memberClone)
		}
		return val
	case KindBlock:
		block := valueBlock(val)
		if block == nil {
			return val
		}
		clone := *block
		clone.Params = cloneParams(block.Params)
		clone.Body = cloneStatements(block.Body)
		clone.Env = cloneEnvForHost(block.Env, state)
		return value.NewValue(KindBlock, &clone)
	case KindBuiltin:
		return cloneBuiltinForHost(val, state)
	default:
		return val
	}
}

// cloneBuiltinForHost clones a builtin for the host boundary. A receiver-bound
// predicate (one carrying BoundReceiver) is rebuilt around the clone of its
// captured receiver, walked through state so it dedups with the same receiver
// appearing elsewhere in the returned graph. The clone is reserved and cached
// before the receiver is cloned, so a receiver graph that reaches the predicate
// bound to it (for example `[p, a]` where `a` stores `p = a.eql?`) dedups against
// the reserved clone instead of minting a second one the outer call would then
// overwrite — which would make pre-clone aliases report not-identical after the
// boundary. Without rebinding at all the cloned predicate's Fn would keep
// comparing against the pre-clone receiver, so a re-entering probe(clonedReceiver)
// would wrongly report not-identical. Plain builtins have no runtime-cloneable
// state, so they fall back to the shallow copy, memoized on the source builtin so
// aliases of one callable (for example `p = JSON.parse; [p, p]`) stay identical
// across the boundary.
func cloneBuiltinForHost(val Value, state hostValueCloneState) Value {
	builtin := valueBuiltin(val)
	if builtin == nil {
		return cloneBuiltinValue(val)
	}
	if builtin.BoundReceiver == nil {
		if clone, ok := state.plainBuiltins[builtin]; ok {
			return clone
		}
		clone := cloneBuiltinValue(val)
		if state.plainBuiltins != nil {
			state.plainBuiltins[builtin] = clone
		}
		return clone
	}
	if clone, ok := state.boundBuiltins[builtin]; ok {
		return clone
	}
	clone, clonedCell := builtin.BoundReceiver.reserve()
	if state.boundBuiltins != nil {
		state.boundBuiltins[builtin] = clone
	}
	clonedReceiver := cloneValueForHostWithState(builtin.BoundReceiver.receiver.value, state)
	setBoundReceiver(valueBuiltin(clone), clonedCell, clonedReceiver)
	return clone
}

func cloneFunctionForHostWithState(fn *ScriptFunction, state hostValueCloneState) *ScriptFunction {
	if fn == nil {
		return nil
	}
	if clone, ok := state.functions[fn]; ok {
		return clone
	}
	clone := &ScriptFunction{}
	if state.functions != nil {
		state.functions[fn] = clone
	}
	*clone = *fn
	clone.Params = cloneParams(fn.Params)
	clone.ReturnTy = cloneTypeExpr(fn.ReturnTy)
	clone.Body = cloneStatements(fn.Body)
	clone.Env = cloneEnvForHost(fn.Env, state)
	return clone
}

func cloneClassForHostWithState(classDef *ClassDef, state hostValueCloneState) *ClassDef {
	if classDef == nil {
		return nil
	}
	if clone, ok := state.classes[classDef]; ok {
		return clone
	}
	classClone := &ClassDef{
		Name:         classDef.Name,
		Methods:      make(map[string]*ScriptFunction, len(classDef.Methods)),
		ClassMethods: make(map[string]*ScriptFunction, len(classDef.ClassMethods)),
		ClassVars:    make(map[string]Value, len(classDef.ClassVars)),
		Body:         cloneStatements(classDef.Body),
		owner:        classDef.owner,
	}
	state.classes[classDef] = classClone
	for name, val := range classDef.ClassVars {
		classClone.ClassVars[name] = cloneValueForHostWithState(val, state)
	}
	for methodName, method := range classDef.Methods {
		classClone.Methods[methodName] = cloneFunctionForHostWithState(method, state)
	}
	for methodName, method := range classDef.ClassMethods {
		classClone.ClassMethods[methodName] = cloneFunctionForHostWithState(method, state)
	}
	return classClone
}

func cloneEnvForHost(env *Env, state hostValueCloneState) *Env {
	if env == nil {
		return nil
	}
	if clone, ok := state.envs[env]; ok {
		return clone
	}
	clone := newEnvWithCapacity(nil, env.dynamicLen())
	clone.assignBoundary = env.assignBoundary
	clone.callRoot = env.callRoot
	state.envs[env] = clone
	clone.parent = cloneEnvForHost(env.parent, state)
	env.rangeDynamicBindings(func(name string, val Value) {
		clone.Define(name, cloneValueForHostWithState(val, state))
	})
	for name, val := range env.statics {
		clone.DefineStatic(name, cloneValueForHostWithState(val, state))
	}
	// A call frame captured by an escaped closure carries the block its method
	// received in a hidden slot; clone it so a closure or default proc that
	// crosses the host boundary still resolves yield and block_given? to that
	// block on re-entry instead of seeing no block.
	if env.hasCallBlock {
		clone.setCallBlock(cloneValueForHostWithState(env.callBlock, state))
	}
	return clone
}

// cloneHostHashValue clones a KindHash value, preserving and deep-cloning its
// Ruby-style default metadata (default value and default proc) so a hash that
// crosses the host boundary keeps its missing-key behavior and its proc closes
// over the cloned environment rather than the live one. The cloned hash is
// cached on its source wrapper identity so a hash reachable through several
// paths (or one that contains itself) clones to a single wrapper and keeps its
// object identity across the boundary.
func cloneHostHashValue(val Value, state hostValueCloneState) Value {
	id := hashIdentity(val)
	if id != 0 {
		if clone, ok := state.hashes[id]; ok {
			return clone
		}
	}
	entries := val.Hash()
	entriesPtr := reflect.ValueOf(entries).Pointer()
	// A distinct wrapper that shares this entry map already cloned it; reuse that
	// cloned map so both cloned wrappers mutate one map in place and the host's
	// intentional aliasing survives the boundary. The shared map is already fully
	// populated, so skip the fill loop -- only a fresh wrapper (with this wrapper's
	// own cloned defaults) is built around it.
	sharedEntries, sharedSeen := state.hashEntries[entriesPtr]
	clonedEntries := sharedEntries
	if !sharedSeen {
		clonedEntries = make(map[string]Value, len(entries))
	}
	defaultValue := hashDefaultValue(val)
	defaultProc := hashDefaultProc(val)
	hasDefault := !defaultValue.IsNil() || !defaultProc.IsNil()
	var cloned Value
	if hasDefault {
		cloned = NewHashWithDefault(clonedEntries, NewNil(), NewNil())
	} else {
		cloned = NewHash(clonedEntries)
	}
	// Register the wrapper before cloning defaults or entries so a hash that
	// contains itself -- whether through an entry or through a default that
	// reaches the hash (e.g. Hash.new { |_, _| h }) -- dedups against this clone
	// rather than recursing forever or cloning a second wrapper.
	if id != 0 {
		state.hashes[id] = cloned
	}
	if !sharedSeen && entriesPtr != 0 {
		state.hashEntries[entriesPtr] = clonedEntries
	}
	if hasDefault {
		clonedDefaultValue := NewNil()
		clonedDefaultProc := NewNil()
		if !defaultValue.IsNil() {
			clonedDefaultValue = cloneValueForHostWithState(defaultValue, state)
		}
		if !defaultProc.IsNil() {
			clonedDefaultProc = cloneValueForHostWithState(defaultProc, state)
		}
		cloned.SetHashDefaults(clonedDefaultValue, clonedDefaultProc)
	}
	if !sharedSeen {
		for key, item := range entries {
			clonedEntries[key] = cloneValueForHostWithState(item, state)
		}
	}
	return cloned
}

func cloneHostMapValue(val Value, state hostValueCloneState, construct func(map[string]Value) Value) Value {
	entries := val.Hash()
	ptr := reflect.ValueOf(entries).Pointer()
	if ptr != 0 {
		if clone, ok := state.maps[ptr]; ok {
			return construct(clone)
		}
	}
	clonedEntries := make(map[string]Value, len(entries))
	if ptr != 0 {
		state.maps[ptr] = clonedEntries
	}
	for key, item := range entries {
		clonedEntries[key] = cloneValueForHostWithState(item, state)
	}
	return construct(clonedEntries)
}

func enumOwner(enumDef *EnumDef) *Script {
	if enumDef == nil {
		return nil
	}
	return enumDef.owner
}

// cloneEnumForHost clones an enum definition for the host boundary, memoizing the
// clone on its source *EnumDef so the same enum (and therefore the same members)
// reachable through several paths in one returned graph clones once. equal?
// compares enums and members by backing pointer, so reusing the cached clone keeps
// aliases of one enum member identical after the boundary; cloning per occurrence
// would mint distinct pointers that wrongly report not-identical.
func cloneEnumForHost(enumDef *EnumDef, state hostValueCloneState) *EnumDef {
	if enumDef == nil {
		return nil
	}
	if clone, ok := state.enums[enumDef]; ok {
		return clone
	}
	clone := cloneEnumDef(enumDef, enumOwner(enumDef))
	if state.enums != nil {
		state.enums[enumDef] = clone
	}
	return clone
}

// Builtin represents a built-in function callable from Vibescript. It
// remains defined in the vibes package because BuiltinFunc references
// the runtime *Execution type.
type Builtin struct {
	Name       string
	Fn         BuiltinFunc
	AutoInvoke bool
	// OptionsHashTarget receives a collapsed keyword options hash for builtin
	// wrappers around script functions (method, constructor, and function-call
	// alias callers).
	OptionsHashTarget *ScriptFunction
	// DirectCallAlias marks a builtin that invokes a function value directly,
	// such as the `call` member exposed on function values. Direct-call aliases
	// follow plain function-call semantics, so they collapse a parenthesized
	// keyword options hash just like `fn(...)`. Method and constructor wrappers
	// leave this false to keep parenthesized keyword binding strict.
	DirectCallAlias bool
	// CapturedValues holds runtime values the builtin's Fn closes over and keeps
	// alive for as long as the builtin is reachable. The memory estimator charges
	// their payloads so a stored bound builtin (for example `probe = big.eql?`,
	// which captures its receiver) cannot retain arbitrarily large structures
	// outside the runtime memory quota. Builtins that close over no runtime values
	// leave this nil and stay free, as before.
	CapturedValues []Value
	// BoundReceiver, when non-nil, marks the builtin a receiver-bound predicate (a
	// bound eql?/equal?) and exposes a two-phase clone. The universal predicates
	// read the value they were resolved from through a mutable cell, so a plain
	// clone of the Fn keeps comparing against the pre-clone receiver. When
	// Script.Call host-clones a returned graph (or re-roots an inbound one) that
	// holds both a receiver and a predicate bound to it, the clone walk reserves an
	// empty clone, registers it, recurses to clone the receiver, then installs the
	// cloned receiver via this hook. Reserving before recursing keeps a receiver
	// graph that reaches the predicate bound to it (for example `[p, a]` where `a`
	// stores `p = a.eql?`) deduplicated to one clone, so a re-entering
	// `probe(clonedReceiver)` still reports identity. Builtins with no bound
	// receiver leave this nil.
	BoundReceiver *boundReceiverClone
	// Capability marks a builtin a capability adapter exposed for a single
	// Script.Call. Capability grants are per call: when a closure that captured
	// one (for example a `Hash.new { ... }` default proc copying a capability
	// into a local) escapes and re-enters a later call, the inbound rebinder
	// revokes the captured grant so a missing-key lookup cannot invoke a
	// capability the re-entering call never granted.
	Capability bool
}

// BuiltinFunc is the Go function signature for built-in Vibescript functions.
type BuiltinFunc func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error)

// Block represents a closure passed to a function at runtime. It stays
// in the vibes package because its fields reference parser AST and the
// runtime Env/Script types.
type Block struct {
	Params     []Param
	Body       []Statement
	Env        *Env
	owner      *Script
	moduleKey  string
	modulePath string
	moduleRoot string
}

// NewBlock returns a block (closure) Value.
func NewBlock(params []Param, body []Statement, env *Env) Value {
	return value.NewValue(KindBlock, &Block{Params: params, Body: body, Env: env})
}

// wrapBlock returns a block Value over an existing *Block, used by the inbound
// rebinder to surface a re-rooted block clone without re-deriving the block's
// module metadata.
func wrapBlock(blk *Block) Value {
	return value.NewValue(KindBlock, blk)
}

// NewEnum returns an enum definition Value.
func NewEnum(def *EnumDef) Value { return value.NewValue(KindEnum, def) }

// NewEnumValue returns an enum member Value.
func NewEnumValue(def *EnumValueDef) Value { return value.NewValue(KindEnumValue, def) }

// NewClass returns a class definition Value.
func NewClass(def *ClassDef) Value { return value.NewValue(KindClass, def) }

// NewInstance returns a class instance Value.
func NewInstance(inst *Instance) Value { return value.NewValue(KindInstance, inst) }

// NewFunction returns a script-defined function Value.
func NewFunction(fn *ScriptFunction) Value { return value.NewValue(KindFunction, fn) }

func newBuiltin(name string, fn BuiltinFunc, autoInvoke bool) Value {
	return value.NewValue(KindBuiltin, &Builtin{Name: name, Fn: fn, AutoInvoke: autoInvoke})
}

// NewBuiltin returns a builtin function Value.
func NewBuiltin(name string, fn BuiltinFunc) Value { return newBuiltin(name, fn, false) }

// NewCapturingBuiltin returns a builtin function Value whose Fn closes over the
// given runtime values. The captured values are recorded on the builtin so the
// memory estimator charges their payloads while the builtin is reachable,
// keeping closures such as a bound predicate's receiver inside the memory quota.
func NewCapturingBuiltin(name string, fn BuiltinFunc, captured ...Value) Value {
	val := newBuiltin(name, fn, false)
	valueBuiltin(val).CapturedValues = captured
	return val
}

// NewAutoBuiltin returns a builtin function Value that auto-invokes without parentheses.
func NewAutoBuiltin(name string, fn BuiltinFunc) Value { return newBuiltin(name, fn, true) }

// Marker methods bind the runtime payload types to the value.* payload
// interfaces so Value.Class, Value.Builtin, and so on return a typed
// result without forming an import cycle. The names are exported so the
// marker satisfies the interfaces from another package.

func (*Builtin) ValueBuiltinMarker()         {}
func (*Block) ValueBlockMarker()             {}
func (*ClassDef) ValueClassMarker()          {}
func (*Instance) ValueInstanceMarker()       {}
func (*ScriptFunction) ValueFunctionMarker() {}
func (*EnumDef) ValueEnumMarker()            {}
func (*EnumValueDef) ValueEnumValueMarker()  {}

// ClassOf returns the *ClassDef stored in v, or nil if v is not a class
// value. It is the typed companion to v.Class(), which returns the
// value.ClassPayload interface for cycle-free reach from outside vibes.
func ClassOf(v Value) *ClassDef {
	cl, _ := v.Class().(*ClassDef)
	return cl
}

// InstanceOf returns the *Instance stored in v, or nil.
func InstanceOf(v Value) *Instance {
	inst, _ := v.Instance().(*Instance)
	return inst
}

// BlockOf returns the *Block stored in v, or nil.
func BlockOf(v Value) *Block {
	blk, _ := v.Block().(*Block)
	return blk
}

// FunctionOf returns the *ScriptFunction stored in v, or nil.
func FunctionOf(v Value) *ScriptFunction {
	fn, _ := v.Function().(*ScriptFunction)
	return fn
}

// BuiltinOf returns the *Builtin stored in v, or nil.
func BuiltinOf(v Value) *Builtin {
	b, _ := v.Builtin().(*Builtin)
	return b
}

// EnumOf returns the *EnumDef stored in v, or nil.
func EnumOf(v Value) *EnumDef {
	e, _ := v.Enum().(*EnumDef)
	return e
}

// EnumValueOf returns the *EnumValueDef stored in v, or nil.
func EnumValueOf(v Value) *EnumValueDef {
	e, _ := v.EnumValue().(*EnumValueDef)
	return e
}

// The valueX helpers preserve the original short call sites used inside
// the vibes package; new external callers should prefer the exported
// XOf functions above.
func valueClass(v Value) *ClassDef          { return ClassOf(v) }
func valueInstance(v Value) *Instance       { return InstanceOf(v) }
func valueBlock(v Value) *Block             { return BlockOf(v) }
func valueFunction(v Value) *ScriptFunction { return FunctionOf(v) }
func valueBuiltin(v Value) *Builtin         { return BuiltinOf(v) }
func valueEnum(v Value) *EnumDef            { return EnumOf(v) }
func valueEnumValue(v Value) *EnumValueDef  { return EnumValueOf(v) }

// runtimeValueString renders runtime-only value kinds whose payloads live
// in the vibes package. Installed at init time on value.RuntimeStringer.
func runtimeValueString(v Value) (string, bool) {
	switch v.Kind() {
	case KindEnum:
		if enum := valueEnum(v); enum != nil {
			return fmt.Sprintf("<Enum %s>", enum.Name), true
		}
	case KindEnumValue:
		if member := valueEnumValue(v); member != nil && member.Enum != nil {
			return fmt.Sprintf("%s::%s", member.Enum.Name, member.Name), true
		}
	case KindClass:
		if cl := valueClass(v); cl != nil {
			return fmt.Sprintf("<Class %s>", cl.Name), true
		}
	case KindInstance:
		if inst := valueInstance(v); inst != nil && inst.Class != nil {
			return fmt.Sprintf("<%s instance>", inst.Class.Name), true
		}
	}
	return "", false
}

// runtimeValueEqual compares runtime-only value kinds whose payloads live
// in the vibes package. Installed at init time on value.RuntimeEqualer.
func runtimeValueEqual(left, right Value) (bool, bool) {
	switch left.Kind() {
	case KindFunction:
		return valueFunction(left) == valueFunction(right), true
	case KindBuiltin:
		return valueBuiltin(left) == valueBuiltin(right), true
	case KindBlock:
		return valueBlock(left) == valueBlock(right), true
	case KindClass:
		return valueClass(left) == valueClass(right), true
	case KindInstance:
		return valueInstance(left) == valueInstance(right), true
	case KindEnum:
		return enumDefsEqual(valueEnum(left), valueEnum(right)), true
	case KindEnumValue:
		return enumValueDefsEqual(valueEnumValue(left), valueEnumValue(right)), true
	}
	return false, false
}

// runtimeValueIdentical compares enum and enum-value kinds by backing-pointer
// identity, backing the Ruby-style equal? predicate. Their Equal comparison is
// structural (same owner script and name), so two distinct clones can be Equal
// without sharing storage; identity must instead require the same backing
// pointer. Installed at init time on value.RuntimeIdenticaler.
func runtimeValueIdentical(left, right Value) (bool, bool) {
	switch left.Kind() {
	case KindEnum:
		return valueEnum(left) == valueEnum(right), true
	case KindEnumValue:
		return valueEnumValue(left) == valueEnumValue(right), true
	}
	return false, false
}

func init() {
	value.RuntimeStringer = runtimeValueString
	value.RuntimeEqualer = runtimeValueEqual
	value.RuntimeIdenticaler = runtimeValueIdentical
}
