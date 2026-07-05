package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mgomes/vibescript/internal/ast"
)

const (
	minInt64Float          = -9223372036854775808.0
	maxInt64FloatExclusive = 9223372036854775808.0
)

// blockGivenName is the reserved Kernel-style predicate that reports whether the
// current call was supplied a block, mirroring Ruby's block_given?. It is
// resolved before ordinary variable lookup so it cannot be shadowed.
const blockGivenName = "block_given?"

func (exec *Execution) evalExpression(expr Expression, env *Env) (Value, error) {
	return exec.evalExpressionWithAuto(expr, env, true)
}

func (exec *Execution) evalExpressionWithAuto(expr Expression, env *Env, autoCall bool) (Value, error) {
	if err := exec.step(); err != nil {
		return NewNil(), err
	}
	switch e := expr.(type) {
	case *Identifier:
		if e.Name == blockGivenName {
			return NewBool(blockGivenInCurrentCall(env)), nil
		}
		var self Value
		hasSelf := false
		if isConstantIdentifier(e.Name) {
			if val, ok := env.getCallLocal(e.Name); ok {
				env.clearArrayAppendBuffer(e.Name)
				if autoCall {
					return exec.autoInvokeIfNeeded(e, val, NewNil())
				}
				return val, nil
			}
			self, hasSelf = env.Get("self")
			if hasSelf && (self.Kind() == KindInstance || self.Kind() == KindClass) {
				if val, ok := classConstant(self, e.Name); ok {
					return val, nil
				}
			}
		}
		val, ok := env.Get(e.Name)
		if !ok {
			// allow implicit self method lookup
			if !hasSelf {
				self, hasSelf = env.Get("self")
			}
			if hasSelf && (self.Kind() == KindInstance || self.Kind() == KindClass) {
				member, err := exec.getMember(self, e.Name, e.Pos())
				if err != nil {
					return NewNil(), err
				}
				if autoCall {
					return exec.autoInvokeIfNeeded(e, member, self)
				}
				return member, nil
			}
			return NewNil(), exec.errorAt(e.Pos(), "undefined variable %s%s", e.Name, didYouMean(e.Name, env.visibleNames()))
		}
		env.clearArrayAppendBuffer(e.Name)
		if autoCall {
			return exec.autoInvokeIfNeeded(e, val, NewNil())
		}
		return val, nil
	case *IntegerLiteral:
		return NewInt(e.Value), nil
	case *FloatLiteral:
		return NewFloat(e.Value), nil
	case *StringLiteral:
		return NewString(e.Value), nil
	case *RegexLiteral:
		result, err := compileRegexValue("regex literal", e.Pattern, e.Flags)
		if err != nil {
			return NewNil(), exec.wrapError(err, e.Pos())
		}
		return result, nil
	case *InterpolatedString:
		return exec.evalInterpolatedStringLiteral(e, env)
	case *InterpolatedSymbol:
		return exec.evalInterpolatedSymbolLiteral(e, env)
	case *BoolLiteral:
		return NewBool(e.Value), nil
	case *NilLiteral:
		return NewNil(), nil
	case *SymbolLiteral:
		return exec.symbolLiteralValue(e), nil
	case *ArrayLiteral:
		return exec.evalArrayLiteral(e, env)
	case *HashLiteral:
		return exec.evalHashLiteral(e, env)
	case *UnaryExpr:
		return exec.evalUnaryExpr(e, env)
	case *BinaryExpr:
		return exec.evalBinaryExpr(e, env)
	case *ConditionalExpr:
		return exec.evalConditionalExpr(e, env)
	case *RescueExpr:
		return exec.evalRescueExpr(e, env, autoCall)
	case *IfExpr:
		return exec.evalIfExpr(e, env)
	case *IfStmt:
		return exec.evalIfStatementExpression(e, env)
	case *RangeExpr:
		return exec.evalRangeExpr(e, env)
	case *CaseExpr:
		return exec.evalCaseExpr(e, env)
	case *MemberExpr:
		var obj Value
		var err error
		if _, objIsMember := e.Object.(*MemberExpr); e.Property == "call" && objIsMember {
			// Resolve a member-of-member receiver exactly like the
			// parenthesized call form so c.cb.call (no parens) sees the stored
			// callable or invoked getter value, not the raw getter builtin.
			// Non-member objects keep the plain no-auto-invoke path so a bare
			// stored callable (cb.call) is not invoked early.
			obj, err = exec.evalMemberCallReceiver(e, env, memberCallReceiverAutoInvokes)
		} else {
			obj, err = exec.evalExpressionWithAuto(e.Object, env, memberReceiverAutoInvokes(e.Object, e.Property, env))
		}
		if err != nil {
			return NewNil(), err
		}
		if e.Safe && obj.Kind() == KindNil {
			return NewNil(), nil
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return NewNil(), err
		}
		member, err := exec.getPublicMember(obj, e.Property, e.Pos())
		if err != nil {
			return NewNil(), err
		}
		if autoCall {
			return exec.autoInvokeIfNeeded(e, member, obj)
		}
		return member, nil
	case *ScopeExpr:
		obj, err := exec.evalExpressionWithAuto(e.Object, env, true)
		if err != nil {
			return NewNil(), err
		}
		member, err := exec.getScopedMember(obj, e.Property, e.Pos())
		if err != nil {
			return NewNil(), err
		}
		return member, nil
	case *IndexExpr:
		return exec.evalIndexExpr(e, env)
	case *IvarExpr:
		self, ok := env.Get("self")
		if !ok || self.Kind() != KindInstance {
			return NewNil(), exec.errorAt(e.Pos(), "no instance context for ivar")
		}
		val, ok := valueInstance(self).Ivars[e.Name]
		if !ok {
			return NewNil(), nil
		}
		return val, nil
	case *ClassVarExpr:
		self, ok := env.Get("self")
		if !ok {
			return NewNil(), exec.errorAt(e.Pos(), "no class context")
		}
		switch self.Kind() {
		case KindInstance:
			val, ok := valueInstance(self).Class.ClassVars[e.Name]
			if !ok {
				return NewNil(), nil
			}
			return val, nil
		case KindClass:
			val, ok := valueClass(self).ClassVars[e.Name]
			if !ok {
				return NewNil(), nil
			}
			return val, nil
		default:
			return NewNil(), exec.errorAt(e.Pos(), "no class context")
		}
	case *CallExpr:
		return exec.evalCallExpr(e, env)
	case *BlockLiteral:
		return exec.evalBlockLiteral(e, env)
	case *YieldExpr:
		return exec.evalYield(e, env)
	case *ForStmt:
		return exec.evalForExpression(e, env)
	case *WhileStmt:
		return exec.evalWhileExpression(e, env)
	case *UntilStmt:
		return exec.evalUntilExpression(e, env)
	case *TryStmt:
		return exec.evalTryExpression(e, env)
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported expression")
	}
}

func (exec *Execution) evalInterpolatedStringLiteral(lit *InterpolatedString, env *Env) (Value, error) {
	text, err := exec.buildInterpolatedString(lit.Parts, env)
	if err != nil {
		return NewNil(), err
	}
	return NewString(text), nil
}

func (exec *Execution) evalInterpolatedSymbolLiteral(lit *InterpolatedSymbol, env *Env) (Value, error) {
	name, err := exec.buildInterpolatedString(lit.Parts, env)
	if err != nil {
		return NewNil(), err
	}
	return NewSymbol(name), nil
}

// buildInterpolatedString materializes the text of an interpolated string or
// symbol from its parts, honoring the sandbox step and memory quotas.
func (exec *Execution) buildInterpolatedString(parts []StringPart, env *Env) (string, error) {
	var sb strings.Builder
	for _, part := range parts {
		switch p := part.(type) {
		case StringText:
			if err := exec.appendInterpolatedChunk(&sb, p.Text); err != nil {
				return "", err
			}
		case StringExpr:
			val, err := exec.evalExpressionWithAuto(p.Expr, env, true)
			if err != nil {
				return "", err
			}
			if err := exec.appendInterpolatedValue(&sb, val); err != nil {
				return "", err
			}
		}
	}
	return sb.String(), nil
}

// appendInterpolatedChunk writes a literal text chunk to the interpolation
// builder while keeping the partially built result inside the sandbox limits.
// step() honors a canceled context and the step quota during repeated or large
// interpolation, and checkProjectedStringBytes rejects the materialization
// before the builder grows past the memory quota. The projected check is keyed
// on the builder's projected backing capacity (see projectedBuilderCap) so a
// doubling interpolation such as "#{text}#{text}" fails fast instead of
// allocating the oversized backing array that the surrounding evaluator would
// only observe after it already exists. Small interpolations stay on the fast
// path: with no quotas the checks are O(1) no-ops.
//
// Charging the projected capacity rather than sb.Len()+len(chunk) matters
// because Builder.Grow does not reserve exactly the requested bytes once the
// current backing is exhausted: it reallocates to roundedAllocSize(2*cap+n).
// After a prefix or a prior interpolation the doubled-and-rounded term can
// exceed the running length plus the chunk, so charging only the final length
// would let the real reservation escape the memory quota. projectedBuilderCap
// reproduces Grow's reallocation, including the allocator's size-class rounding,
// so the quota check accounts for the backing array actually reserved.
func (exec *Execution) appendInterpolatedChunk(sb *strings.Builder, chunk string) error {
	if err := exec.step(); err != nil {
		return err
	}
	if err := exec.checkProjectedStringBytes(projectedBuilderCap(sb, len(chunk))); err != nil {
		return err
	}
	sb.Grow(len(chunk))
	sb.WriteString(chunk)
	return nil
}

// projectedBuilderCap reports the backing-array capacity sb will hold after
// sb.Grow(n), so a quota check can account for the bytes Grow actually reserves
// rather than the bytes the caller intends to write.
//
// Builder.Grow only reallocates when the free tail (Cap-Len) cannot hold n more
// bytes. When it does, strings.Builder requests 2*Cap+n bytes through
// bytealg.MakeNoZero, and the runtime rounds that request up to an allocator
// size class before reserving the backing array. The realized capacity is
// therefore roundedAllocSize(2*Cap+n), which can exceed 2*Cap+n: growing a
// 10 KiB builder by 10 KiB requests 30,720 bytes but reserves the 32,768-byte
// class. Charging only 2*Cap+n would leave a quota between the request and the
// rounded class, letting the check pass while Grow allocates over the limit.
// roundedAllocSize mirrors the runtime's rounding exactly (see sizeclass.go), so
// the projection equals the realized capacity. When the value already fits the
// free tail, no reallocation happens and the current capacity is returned
// unchanged, preserving the no-copy fast path. n must be non-negative, matching
// Grow.
func projectedBuilderCap(sb *strings.Builder, n int) int {
	capacity := sb.Cap()
	if capacity-sb.Len() >= n {
		return capacity
	}
	return roundedAllocSize(saturatingAdd(saturatingMul(2, capacity), n))
}

// appendInterpolatedValue renders val into the interpolation builder under the
// same sandbox limits as appendInterpolatedChunk. It projects the rendered byte
// length with Value.StringByteLenBounded before materializing, so an aggregate
// whose String representation expands far beyond its own footprint (for example
// an array holding many references to one large string) is rejected by the
// memory quota instead of allocating the oversized rendering first and only then
// failing the post-build check. StringByteLenBounded walks the aggregate without
// allocating the joined result, so the projection is the only work done for a
// value that overruns the quota, and it charges exec.step per visited node so
// the walk itself is bounded by the step quota (see the call site below).
//
// The projection also charges val's own footprint, not just the rendered output.
// An interpolated expression can produce a temporary that no environment holds —
// a function return, or an array/hash literal constructed inline — which stays
// live on the Go call stack while WriteStringTo copies its rendering. That
// temporary is invisible to the env-reachable base, so charging only the output
// would let base+value+output exceed the quota during the write even though
// base+output passes. checkProjectedValueRendering deduplicates val against the
// base, so a value already reachable from an environment is not double counted and
// the small-interpolation fast path is unchanged.
//
// Once the projection passes, the builder is grown by exactly the projected
// payload and Value.WriteStringTo streams the rendering straight into sb rather
// than building a temporary string and copying it in. A second full copy would
// transiently hold both the temporary rendering and the builder copy, so a quota
// close to the final output size could be exceeded even though the single-payload
// projection passed. Reserving the payload up front also keeps WriteStringTo's
// per-element writes from triggering the builder's doubling growth, which would
// overshoot the quota-checked size; the peak allocation stays a single rendering,
// matching what the projection accounted for.
//
// The projection charges the builder's projected backing capacity (see
// projectedBuilderCap), not sb.Len()+payload. Builder.Grow reallocates to
// roundedAllocSize(2*cap+payload) once the current backing is full, so after a
// prefix or a prior interpolation the reserved backing can exceed the running
// length plus the payload. Charging the projected capacity keeps that
// reservation inside the memory quota; for a value that fits the free tail no
// reallocation happens and the fast path is unchanged.
func (exec *Execution) appendInterpolatedValue(sb *strings.Builder, val Value) error {
	if err := exec.step(); err != nil {
		return err
	}
	// StringByteLenBounded charges exec.step once per node it visits, so the
	// projection walk itself is bounded by the step quota. A composite with a
	// compact but exponentially shared graph (for example a = [a, a] repeated)
	// has bounded memory and a bounded rendering — the cycle marker collapses
	// the repetition once it is on the recursion stack — yet projecting its
	// length re-walks every shared subtree, which is exponential in the nesting
	// depth. Charging steps during that walk (rather than only once per
	// interpolation part) trips the quota or honors a canceled context instead
	// of burning unbounded CPU before the memory check runs.
	payload, err := val.StringByteLenBounded(exec.step)
	if err != nil {
		return err
	}
	if err := exec.checkProjectedValueRendering(val, projectedBuilderCap(sb, payload)); err != nil {
		return err
	}
	// Grow only on a positive payload: StringByteLen sums byte counts without
	// saturating, so a rendering larger than the int range (physically
	// unreachable but not statically excluded) could wrap negative, and Grow
	// panics on a negative count.
	if payload > 0 {
		sb.Grow(payload)
	}
	// WriteStringTo streams the rendering straight into sb without materializing a
	// separate string, so the peak allocation stays the single reservation made
	// above. Writing into a strings.Builder never fails, so there is no error to
	// surface here.
	val.WriteStringTo(sb)
	return nil
}

// evalArrayLiteral evaluates an array literal element by element, charging each
// new element against the memory quota as it lands in the Go-local backing slice.
// Each evaluated element stays live in that slice until NewArray binds it, so the
// running footprint must include every element gathered so far; a literal whose
// elements are fresh temporaries (for example [big[0, n], big[0, n]], where each
// bracket slice is a fresh copy invisible to the base estimator) would otherwise
// stack several copies past the quota before any later statement check observed
// them. The accumulator snapshots the baseline once and charges each element
// incrementally, deduplicating elements aliased by an environment root.
func (exec *Execution) evalArrayLiteral(e *ArrayLiteral, env *Env) (Value, error) {
	return exec.evalArrayLiteralWithElementType(e, env, nil)
}

func (exec *Execution) evalArrayLiteralWithElementType(e *ArrayLiteral, env *Env, elementType *TypeExpr) (Value, error) {
	return exec.evalArrayLiteralWithElementExpectation(e, env, func(_, _ int) expressionExpectation {
		return typeExpressionExpectation(elementType)
	})
}

func (exec *Execution) evalArrayLiteralWithElementExpectation(e *ArrayLiteral, env *Env, elementExpectation func(int, int) expressionExpectation) (Value, error) {
	acc := newArrayBuildAccumulator(exec, NewNil(), nil, nil, NewNil())
	elems := make([]Value, 0, len(e.Elements))
	for i, el := range e.Elements {
		expectation := expressionExpectation{}
		if elementExpectation != nil {
			expectation = elementExpectation(i, len(e.Elements))
		}
		val, err := exec.evalExpressionWithExpectation(el, env, expectation)
		if err != nil {
			return NewNil(), err
		}
		elems = append(elems, val)
		if err := acc.add(val, cap(elems)); err != nil {
			return NewNil(), err
		}
	}
	return NewArray(elems), nil
}

// evalHashLiteral evaluates a hash literal pair by pair, charging each new entry
// against the memory quota as it lands in the Go-local map. The partially built
// map is reachable from no environment until NewHash binds it, so the running
// footprint must include every entry inserted so far; a literal whose values are
// fresh temporaries (for example {a: big[0, n], b: big[0, n]}) would otherwise
// stack several copies past the quota before any later statement check observed
// them. The accumulator reserves the entry backing up front and charges each
// entry's key and value payloads incrementally, deduplicating payloads aliased by
// an environment root.
func (exec *Execution) evalHashLiteral(e *HashLiteral, env *Env) (Value, error) {
	return exec.evalHashLiteralWithValueTypes(e, env, nil)
}

func (exec *Execution) evalHashLiteralWithValueTypes(e *HashLiteral, env *Env, valueTypeForKey func(Value) *TypeExpr) (Value, error) {
	var acc *hashLiteralBuildAccumulator
	if exec.memoryQuota > 0 {
		acc = newHashLiteralBuildAccumulator(exec)
		if err := acc.reserveBacking(len(e.Pairs)); err != nil {
			return NewNil(), err
		}
	}
	hash := NewHash(make(map[string]Value, len(e.Pairs)))
	// Pre-size the insertion-order backing to the pair count (the same bound
	// reserveBacking charges) so HashSet's appends do not grow it past the order
	// slots the memory projection accounts for.
	hash.ReserveHashOrder(len(e.Pairs))
	entries := make(map[string]hashLiteralEntry, len(e.Pairs))
	for _, pair := range e.Pairs {
		keyVal, err := exec.evalExpressionWithAuto(pair.Key, env, true)
		if err != nil {
			return NewNil(), err
		}
		key, err := canonicalHashKey(keyVal)
		if err != nil {
			return NewNil(), exec.errorAt(pair.Key.Pos(), "%s", err.Error())
		}
		lookupKey, err := hashLookupKey(keyVal)
		if err != nil {
			return NewNil(), exec.errorAt(pair.Key.Pos(), "%s", err.Error())
		}
		var valueType *TypeExpr
		if valueTypeForKey != nil {
			valueType = valueTypeForKey(keyVal)
		}
		val, err := exec.evalExpressionWithExpectedType(pair.Value, env, valueType)
		if err != nil {
			return NewNil(), err
		}
		if acc != nil {
			_, replacing := entries[key]
			if replacing || acc.replacing {
				err = acc.replaceEntry(key, lookupKey, keyVal, val, entries)
			} else {
				err = acc.addDistinctEntry(lookupKey, keyVal, val)
			}
			if err != nil {
				return NewNil(), err
			}
		}
		if err := hashSet(hash, keyVal, val); err != nil {
			return NewNil(), exec.errorAt(pair.Key.Pos(), "%s", err.Error())
		}
		entries[key] = hashLiteralEntry{key: keyVal, lookupKey: lookupKey, value: val}
	}
	return hash, nil
}

func (exec *Execution) evalExpressionWithExpectedType(expr Expression, env *Env, ty *TypeExpr) (Value, error) {
	return exec.evalExpressionWithExpectation(expr, env, typeExpressionExpectation(ty))
}

func (exec *Execution) evalExpressionWithExpectation(expr Expression, env *Env, expectation expressionExpectation) (Value, error) {
	if expectation.empty() {
		return exec.evalExpressionWithAuto(expr, env, true)
	}
	return exec.evalCallArgumentForExpectation(expr, env, expectation)
}

func (exec *Execution) evalUnaryExpr(e *UnaryExpr, env *Env) (Value, error) {
	right, err := exec.evalExpressionWithAuto(e.Right, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(right); err != nil {
		return NewNil(), err
	}
	switch e.Operator {
	case tokenMinus:
		switch right.Kind() {
		case KindInt:
			return NewInt(-right.Int()), nil
		case KindFloat:
			return NewFloat(-right.Float()), nil
		default:
			return NewNil(), exec.errorAt(e.Pos(), "unsupported unary - operand")
		}
	case tokenPlus:
		// Unary plus mirrors Ruby: it is the identity on numbers and strings.
		// Vibescript strings are immutable values, so returning the same value
		// matches Ruby's "unfrozen copy" semantics observably.
		switch right.Kind() {
		case KindInt, KindFloat, KindString:
			return right, nil
		default:
			return NewNil(), exec.errorAt(e.Pos(), "unsupported unary + operand")
		}
	case tokenBang, tokenNot:
		return NewBool(!right.Truthy()), nil
	default:
		return NewNil(), exec.errorAt(e.Pos(), "unsupported unary operator")
	}
}

func (exec *Execution) evalIndexExpr(e *IndexExpr, env *Env) (Value, error) {
	obj, err := exec.evalExpressionWithAuto(e.Object, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(obj); err != nil {
		return NewNil(), err
	}
	if len(e.Indices) == 1 {
		idx, err := exec.evalIndexSelector(e, obj, e.Indices[0], env)
		if err != nil {
			return NewNil(), err
		}
		var indices [1]Value
		indices[0] = idx
		result, err := exec.evalIndexValue(e, obj, indices[:])
		if err != nil {
			return NewNil(), err
		}
		if exec.memoryQuota > 0 {
			if err := exec.checkMemoryWith(obj, idx, result); err != nil {
				return NewNil(), err
			}
		}
		return result, nil
	}
	indices, err := exec.evalIndexSelectors(e, obj, env)
	if err != nil {
		return NewNil(), err
	}
	result, err := exec.evalIndexValue(e, obj, indices)
	if err != nil {
		return NewNil(), err
	}
	// The start/length and range forms return a fresh subarray or substring that
	// lives only on the Go stack here; an enclosing literal keeps prior elements
	// in Go-local slots its own per-element check cannot see, so a form such as
	// [big[0, n], big[0, n]] could stack several slice copies past the quota
	// before a later statement check observed them. Charge the receiver, the live
	// selectors, and the fresh result together so the peak is rejected at the
	// source. The estimator deduplicates the receiver and any env-reachable
	// selectors, so a plain element read (which returns an aliased value, not a
	// fresh allocation) charges nothing beyond what the earlier checks already did.
	// Skip building the charge slice entirely when no quota is enforced, keeping
	// the common indexed read allocation-free.
	if exec.memoryQuota > 0 {
		chargeable := make([]Value, 0, len(indices)+2)
		chargeable = append(chargeable, obj)
		chargeable = append(chargeable, indices...)
		chargeable = append(chargeable, result)
		if err := exec.checkMemoryWith(chargeable...); err != nil {
			return NewNil(), err
		}
	}
	return result, nil
}

func (exec *Execution) evalIndexSelector(e *IndexExpr, obj Value, expr Expression, env *Env) (Value, error) {
	idx, err := exec.evalExpressionWithAuto(expr, env, true)
	if err != nil {
		return NewNil(), err
	}
	if exec.memoryQuota > 0 {
		if err := exec.checkMemoryWith(obj, idx); err != nil {
			return NewNil(), err
		}
	}
	return idx, nil
}

// evalIndexSelectors evaluates every selector between the brackets, charging
// the receiver together with the accumulated selectors against the memory quota
// as each materializes. The receiver stays live in the caller's obj while
// selectors are evaluated, yet a fresh receiver (a large literal hash/array
// indexed inline) is not reachable from any environment, so the base walk never
// sees it; charging it alongside the selectors here rejects a peak of receiver +
// selectors at the source rather than after evalIndexValue, whose arity/type
// error would otherwise skip the later combined check and mask the quota breach.
// Every evaluated selector also stays live in indices until dispatch, so each
// check must account for all selectors gathered so far; charging only the
// current one would undercount a multi-selector form whose earlier selectors are
// still resident (the parser permits arbitrary comma-separated selectors). The
// estimator deduplicates against the reachable roots, so the receiver and any
// selector already bound to an environment each contribute their footprint only
// once.
func (exec *Execution) evalIndexSelectors(e *IndexExpr, obj Value, env *Env) ([]Value, error) {
	indices := make([]Value, 0, len(e.Indices))
	// Reuse a single charge buffer (receiver followed by the live selectors) across
	// iterations so a multi-selector read allocates it at most once, and skip it
	// entirely when no quota is enforced to keep the common indexed read
	// allocation-free.
	var charge []Value
	if exec.memoryQuota > 0 {
		charge = make([]Value, 1, len(e.Indices)+1)
		charge[0] = obj
	}
	for _, expr := range e.Indices {
		idx, err := exec.evalIndexSelector(e, obj, expr, env)
		if err != nil {
			return nil, err
		}
		indices = append(indices, idx)
		if exec.memoryQuota > 0 {
			charge = append(charge[:1], indices...)
			if err := exec.checkMemoryWith(charge...); err != nil {
				return nil, err
			}
		}
	}
	return indices, nil
}

// evalIndexValue performs a bracket read against an already-evaluated receiver
// and selectors. Arrays and strings support Ruby's single-index (with negative
// indexing), start/length, and range forms; an out-of-range single index yields
// nil rather than raising, matching Array#[] and String#[]. Hashes and objects
// take exactly one key.
func (exec *Execution) evalIndexValue(e *IndexExpr, obj Value, indices []Value) (Value, error) {
	switch obj.Kind() {
	case KindString:
		return exec.indexString(e, obj.String(), indices)
	case KindArray:
		return exec.indexArray(e, obj, indices)
	case KindHash, KindObject:
		return exec.indexHash(e, obj, indices)
	case KindInstance:
		// obj[i, ...] dispatches to a user-defined [] method, mirroring Ruby's
		// index-read protocol for collection-like classes.
		fn, ok := instanceOperatorMethod(obj, "[]")
		if !ok {
			return NewNil(), exec.errorAt(e.Object.Pos(), "cannot index %s: %s does not define []", obj.Kind(), valueInstance(obj).Class.Name)
		}
		if fn.Private {
			return NewNil(), exec.errorAt(e.Position, "private method []")
		}
		return exec.callOperatorFunction(fn, obj, indices, e.Position)
	default:
		return NewNil(), exec.errorAt(e.Object.Pos(), "cannot index %s", obj.Kind())
	}
}

// indexArray implements arr[...] reads. The single-selector form mirrors
// Array#[]: a single integer (negative counts from the end) returns the element
// or nil when out of range, while a range returns a fresh subarray or nil when
// the begin bound falls outside the array. The two-selector form is Array#[]
// start/length, returning a fresh subarray or nil.
func (exec *Execution) indexArray(e *IndexExpr, receiver Value, indices []Value) (Value, error) {
	arr := receiver.Array()
	switch len(indices) {
	case 1:
		if indices[0].Kind() == KindRange {
			window, ok := arraySliceRangeWindow(len(arr), indices[0].Range())
			if !ok {
				return NewNil(), nil
			}
			if err := exec.reserveArraySliceSlots(receiver, indices, window.length); err != nil {
				return NewNil(), err
			}
			return NewArray(copyArraySliceWindow(arr, window)), nil
		}
		index, err := exec.indexSelectorToInt(e, indices[0], 0)
		if err != nil {
			return NewNil(), err
		}
		return arrayElementAt(arr, index), nil
	case 2:
		start, err := exec.indexSelectorToInt(e, indices[0], 0)
		if err != nil {
			return NewNil(), err
		}
		length, err := exec.indexSelectorToInt(e, indices[1], 1)
		if err != nil {
			return NewNil(), err
		}
		window, ok := arraySliceStartLengthWindow(len(arr), start, length)
		if !ok {
			return NewNil(), nil
		}
		if err := exec.reserveArraySliceSlots(receiver, indices, window.length); err != nil {
			return NewNil(), err
		}
		return NewArray(copyArraySliceWindow(arr, window)), nil
	default:
		return NewNil(), exec.errorAt(e.Position, "array index expects one index, a start and length, or a range")
	}
}

func (exec *Execution) reserveArraySliceSlots(receiver Value, indices []Value, slotCount int) error {
	if exec.memoryQuota <= 0 {
		return nil
	}
	return newArrayBuildAccumulator(exec, receiver, indices, nil, NewNil()).reserveSlots(slotCount)
}

// indexString implements str[...] reads as rune (character) operations. The
// single-selector form mirrors String#[]: a single integer (negative counts
// from the end) returns the one-character substring or nil when out of range,
// while a range returns a substring or nil. The two-selector form is String#[]
// start/length.
func (exec *Execution) indexString(e *IndexExpr, text string, indices []Value) (Value, error) {
	switch len(indices) {
	case 1:
		if indices[0].Kind() == KindRange {
			substr, ok := stringRuneRangeSlice(text, indices[0].Range())
			if !ok {
				return NewNil(), nil
			}
			return NewString(substr), nil
		}
		index, err := exec.indexSelectorToInt(e, indices[0], 0)
		if err != nil {
			return NewNil(), err
		}
		return stringSliceCharAt(text, index), nil
	case 2:
		start, err := exec.indexSelectorToInt(e, indices[0], 0)
		if err != nil {
			return NewNil(), err
		}
		length, err := exec.indexSelectorToInt(e, indices[1], 1)
		if err != nil {
			return NewNil(), err
		}
		substr, ok := stringRuneSlice(text, start, length)
		if !ok {
			return NewNil(), nil
		}
		return NewString(substr), nil
	default:
		return NewNil(), exec.errorAt(e.Position, "string index expects one index, a start and length, or a range")
	}
}

// indexHash implements hash[key] and object[key] reads. Hashes and objects take
// exactly one key; supplying multiple selectors is rejected because neither type
// has Ruby slicing semantics.
func (exec *Execution) indexHash(e *IndexExpr, obj Value, indices []Value) (Value, error) {
	if len(indices) != 1 {
		return NewNil(), exec.errorAt(e.Position, "%s index expects a single key", obj.Kind())
	}
	idx := indices[0]
	if obj.Kind() == KindObject {
		if val, handled, err := matchDataIndex(obj, idx); handled || err != nil {
			if err != nil {
				return NewNil(), exec.errorAt(e.IndexPos(0), "%s", err.Error())
			}
			return val, nil
		}
	}
	val, ok, err := hashGet(obj, idx)
	if err != nil {
		return NewNil(), exec.errorAt(e.IndexPos(0), "%s", err.Error())
	}
	if ok {
		return val, nil
	}
	// A missing key consults the hash's Ruby-style default. Only KindHash
	// carries default metadata (objects never do), so a missing object key
	// stays nil. A default proc takes precedence over a default value and is
	// invoked with (hash, key); the key keeps its original symbol/string
	// value so the proc can render it the way Ruby does.
	if obj.Kind() == KindHash {
		return exec.hashMissingKeyDefault(obj, idx, e.IndexPos(0))
	}
	return NewNil(), nil
}

// indexSelectorToInt converts the selector at position i to an integer index,
// reporting the selector's own source position on failure.
func (exec *Execution) indexSelectorToInt(e *IndexExpr, idx Value, i int) (int, error) {
	switch idx.Kind() {
	case KindInt, KindFloat:
		n, err := valueToInt(idx)
		if err != nil {
			return 0, exec.errorAt(e.IndexPos(i), "%s", err.Error())
		}
		return n, nil
	default:
		return 0, exec.errorAt(e.IndexPos(i), "index must be integer")
	}
}

func (exec *Execution) evalBinaryExpr(expr *BinaryExpr, env *Env) (Value, error) {
	left, err := exec.evalExpression(expr.Left, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(left); err != nil {
		return NewNil(), err
	}
	switch expr.Operator {
	case tokenAnd, tokenWordAnd:
		// Short-circuit and yield the operand value, not a coerced bool
		// (Ruby semantics): `a && b` is `a ? b : a`. A falsy left operand is
		// the result; otherwise the right operand is, whatever its value.
		if !left.Truthy() {
			return left, nil
		}
		right, err := exec.evalExpression(expr.Right, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(left, right); err != nil {
			return NewNil(), err
		}
		return right, nil
	case tokenOr, tokenWordOr:
		// Short-circuit and yield the operand value, not a coerced bool
		// (Ruby semantics): `a || b` is `a ? a : b`. This is what makes the
		// `value = optional || default` idiom work; previously it collapsed
		// to `true`/`false`.
		if left.Truthy() {
			return left, nil
		}
		right, err := exec.evalExpression(expr.Right, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(left, right); err != nil {
			return NewNil(), err
		}
		return right, nil
	}

	right, err := exec.evalExpression(expr.Right, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(left, right); err != nil {
		return NewNil(), err
	}

	result, err := exec.evalBinaryOperator(expr.Operator, left, right, expr.Pos())
	if err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) evalBinaryOperator(operator TokenType, left, right Value, pos Position) (Value, error) {
	if left.Kind() == KindInstance {
		if result, handled, err := exec.evalInstanceOperator(operator, left, right, pos); handled {
			return result, err
		}
	}
	var result Value
	var err error
	switch operator {
	case tokenPlus:
		result, err = addValues(left, right)
	case tokenMinus:
		result, err = subtractValues(left, right)
	case tokenAsterisk:
		result, err = multiplyValues(left, right)
	case tokenPower:
		result, err = powerValues(left, right)
	case tokenSlash:
		result, err = divideValues(left, right)
	case tokenPercent:
		if left.Kind() == KindString {
			values := []Value{right}
			if right.Kind() == KindArray {
				values = right.Array()
			}
			result, err = exec.formatStringValues(left.String(), values, left, []Value{right}, nil, NewNil())
		} else {
			result, err = moduloValues(left, right)
		}
	case tokenShovel:
		result, err = shovelValues(left, right)
	case tokenAmpersand:
		result, err = intersectValues(left, right)
	case tokenEQ:
		return NewBool(left.Equal(right)), nil
	case tokenCaseEQ:
		// Ruby's case equality operator: the left operand acts as the matcher and
		// the right operand is the value being tested. Ranges check membership;
		// every other value falls back to `==`. This mirrors `when` clause
		// matching, where the clause value is the matcher.
		matched, err := caseCandidateMatches(right, left)
		if err != nil {
			return NewNil(), exec.wrapError(err, pos)
		}
		return NewBool(matched), nil
	case tokenNotEQ:
		return NewBool(!left.Equal(right)), nil
	case tokenMatch, tokenNotMatch:
		return exec.evalRegexMatchOperator(operator, left, right, pos)
	case tokenLT:
		return compareValues(left, right, func(c int) bool { return c < 0 })
	case tokenLTE:
		return compareValues(left, right, func(c int) bool { return c <= 0 })
	case tokenGT:
		return compareValues(left, right, func(c int) bool { return c > 0 })
	case tokenGTE:
		return compareValues(left, right, func(c int) bool { return c >= 0 })
	case tokenSpaceship:
		order, ordered, err := compareValueOrder(left, right)
		if err != nil {
			// Incomparable operand pairs (different kinds, or money in different
			// currencies) make the spaceship operator return nil rather than
			// raising, matching Ruby's `1 <=> "a"`. Genuine errors still surface.
			if isIncomparable(err) {
				return NewNil(), nil
			}
			return NewNil(), exec.wrapError(err, pos)
		}
		// Unordered operands (a NaN on either side) make the spaceship operator
		// return nil, matching Ruby's `(0.0 / 0.0) <=> 1.0`.
		if !ordered {
			return NewNil(), nil
		}
		return NewInt(int64(order)), nil
	default:
		return NewNil(), exec.errorAt(pos, "unsupported operator")
	}

	if err != nil {
		return NewNil(), exec.wrapError(err, pos)
	}
	return result, nil
}

// instanceOperatorMethod resolves a user-defined operator method (def +,
// def ==, def []) on an instance receiver's class.
func instanceOperatorMethod(receiver Value, name string) (*ScriptFunction, bool) {
	if receiver.Kind() != KindInstance {
		return nil, false
	}
	fn, ok := valueInstance(receiver).Class.Methods[name]
	return fn, ok
}

// evalInstanceOperator dispatches a binary operator on an instance receiver to
// the class's operator method of the same name, mirroring Ruby where `a + b`
// is `a.+(b)`. Dispatch keys on the left operand only, like Ruby. == and !=
// keep their universal fallbacks when the class defines no method: != prefers
// a user !=, then negates a user ==, then falls back to built-in equality, so
// defining == alone gives a consistent inverse.
func (exec *Execution) evalInstanceOperator(operator TokenType, left, right Value, pos Position) (Value, bool, error) {
	switch operator {
	case tokenPlus, tokenMinus, tokenAsterisk, tokenSlash, tokenPercent, tokenPower,
		tokenShovel, tokenAmpersand, tokenLT, tokenLTE, tokenGT, tokenGTE, tokenSpaceship:
		fn, ok := instanceOperatorMethod(left, string(operator))
		if !ok {
			return NewNil(), false, nil
		}
		val, err := exec.callInstanceOperatorMethod(fn, string(operator), left, right, pos)
		return val, true, err
	case tokenEQ:
		fn, ok := instanceOperatorMethod(left, "==")
		if !ok {
			return NewNil(), false, nil
		}
		val, err := exec.callInstanceOperatorMethod(fn, "==", left, right, pos)
		return val, true, err
	case tokenNotEQ:
		if fn, ok := instanceOperatorMethod(left, "!="); ok {
			val, err := exec.callInstanceOperatorMethod(fn, "!=", left, right, pos)
			return val, true, err
		}
		if fn, ok := instanceOperatorMethod(left, "=="); ok {
			val, err := exec.callInstanceOperatorMethod(fn, "==", left, right, pos)
			if err != nil {
				return NewNil(), true, err
			}
			return NewBool(!val.Truthy()), true, nil
		}
		return NewNil(), false, nil
	default:
		return NewNil(), false, nil
	}
}

func (exec *Execution) callInstanceOperatorMethod(fn *ScriptFunction, name string, receiver, arg Value, pos Position) (Value, error) {
	if fn.Private {
		return NewNil(), exec.errorAt(pos, "private method %s", name)
	}
	return exec.callOperatorFunction(fn, receiver, []Value{arg}, pos)
}

// callOperatorFunction invokes an operator or index method and, like the
// normal call wrapper, converts a bare break/next escaping the method body
// into a call-boundary error. Without this, an operator evaluated inside a
// caller loop would let the signal silently break or continue that loop.
func (exec *Execution) callOperatorFunction(fn *ScriptFunction, receiver Value, args []Value, pos Position) (Value, error) {
	val, err := exec.callFunction(fn, receiver, args, nil, NewNil(), pos)
	if err != nil {
		if errors.Is(err, errLoopBreak) {
			return NewNil(), exec.localJumpErrorAt(pos, "break cannot cross call boundary")
		}
		if errors.Is(err, errLoopNext) {
			return NewNil(), exec.localJumpErrorAt(pos, "next cannot cross call boundary")
		}
	}
	return val, err
}

func (exec *Execution) evalConditionalExpr(expr *ConditionalExpr, env *Env) (Value, error) {
	return exec.evalConditionalExprWithExpectation(expr, env, expressionExpectation{})
}

func (exec *Execution) evalConditionalExprWithExpectation(expr *ConditionalExpr, env *Env, expectation expressionExpectation) (Value, error) {
	condition, err := exec.evalExpression(expr.Condition, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(condition); err != nil {
		return NewNil(), err
	}

	branch := expr.Alternate
	if condition.Truthy() {
		branch = expr.Consequent
	}
	result, err := exec.evalExpressionWithExpectation(branch, env, expectation)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) evalRescueExpr(expr *RescueExpr, env *Env, autoCall bool) (Value, error) {
	for {
		result, err := exec.evalExpressionWithAuto(expr.Body, env, autoCall)
		if err == nil {
			if err := exec.checkMemoryWith(result); err != nil {
				return NewNil(), err
			}
			return result, nil
		}
		if !canRescueRuntimeError(err, nil) {
			return NewNil(), err
		}

		fallback, fallbackErr := exec.evalRescueExprFallback(expr.Fallback, err, env, autoCall)
		if isRescueRetrySignal(fallbackErr) {
			// A retry from the fallback re-runs the rescued expression; charge
			// a step per attempt so a retry storm hits the step quota.
			if stepErr := exec.step(); stepErr != nil {
				return NewNil(), exec.wrapError(stepErr, expr.Pos())
			}
			continue
		}
		if fallbackErr != nil {
			return NewNil(), fallbackErr
		}
		if err := exec.checkMemoryWith(fallback); err != nil {
			return NewNil(), err
		}
		return fallback, nil
	}
}

func (exec *Execution) evalRescueExprFallback(expr Expression, err error, env *Env, autoCall bool) (Value, error) {
	exec.pushRescuedError(err)
	exec.rescueDepth++
	fallback, fallbackErr := exec.evalExpressionWithAuto(expr, env, autoCall)
	exec.rescueDepth--
	exec.popRescuedError()
	return fallback, fallbackErr
}

func (exec *Execution) evalIfExpr(expr *IfExpr, env *Env) (Value, error) {
	return exec.evalIfExprWithExpectation(expr, env, expressionExpectation{})
}

func (exec *Execution) evalIfExprWithExpectation(expr *IfExpr, env *Env, expectation expressionExpectation) (Value, error) {
	resultExpr, err := exec.matchIfExpressionBranch(expr, env)
	if err != nil {
		return NewNil(), err
	}
	if resultExpr == nil {
		return NewNil(), nil
	}

	result, err := exec.evalExpressionWithExpectation(resultExpr, env, expectation)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) matchIfExpressionBranch(expr *IfExpr, env *Env) (Expression, error) {
	condition, err := exec.evalExpression(expr.Condition, env)
	if err != nil {
		return nil, err
	}
	if err := exec.checkMemoryWith(condition); err != nil {
		return nil, err
	}
	if condition.Truthy() {
		return expr.Consequent, nil
	}

	for _, branch := range expr.ElseIf {
		condition, err := exec.evalExpression(branch.Condition, env)
		if err != nil {
			return nil, err
		}
		if err := exec.checkMemoryWith(condition); err != nil {
			return nil, err
		}
		if condition.Truthy() {
			return branch.Result, nil
		}
	}

	return expr.Alternate, nil
}

func (exec *Execution) evalBlockLiteral(block *BlockLiteral, env *Env) (Value, error) {
	blockValue := newBlock(block.Params, block.ImplicitParams, block.Body, env)
	blk := valueBlock(blockValue)
	blk.homeReturnToken = exec.currentBlockHomeToken()
	if ctx := exec.currentModuleContext(); ctx != nil && ctx.script != nil {
		blk.owner = ctx.script
	} else {
		blk.owner = exec.script
	}
	if ctx := exec.currentModuleContext(); ctx != nil {
		blk.moduleKey = ctx.key
		blk.modulePath = ctx.path
		blk.moduleRoot = ctx.root
	}
	return blockValue, nil
}

func ensureBlock(block Value, name string) error {
	if valueBlock(block) == nil {
		if name != "" {
			return fmt.Errorf("%s requires a block", name)
		}
		return fmt.Errorf("block required")
	}
	return nil
}

type blockCallRunner struct {
	exec          *Execution
	blk           *Block
	env           *Env
	charge        *blockBindCharge
	nextContinues bool
}

// newBlockCallRunner builds a runner for repeatedly invoking a block from an
// iterator. receiver, callArgs, and kwargs are the iterator's call roots: they seed
// the per-call bind charge that bounds the fresh backing a rest-collecting
// destructure parameter (|(k, *tail)|) allocates, so those roots -- live only on the
// Go stack during the loop, invisible to estimateMemoryUsageBase -- are counted
// alongside that backing. The charge snapshots them ONCE (no per-entry re-walk), so
// the loop stays O(total data). callArgs are the iterator's POSITIONAL roots (the
// other hashes a block-driven hash.merge holds, a grep pattern); pass nil for the
// pure iterators that reject positional arguments and so hold only the receiver.
func newBlockCallRunner(exec *Execution, block Value, name string, receiver Value, callArgs []Value, kwargs map[string]Value) (*blockCallRunner, error) {
	if err := ensureBlock(block, name); err != nil {
		return nil, err
	}
	blk := valueBlock(block)
	runner := &blockCallRunner{
		exec:   exec,
		blk:    blk,
		charge: newBlockBindCharge(exec, blk, receiver, callArgs, kwargs, block),
	}
	if blockCanReuseEnv(blk) {
		runner.env = newBlockAssignmentEnv(blk.Env)
	}
	return runner, nil
}

func (runner *blockCallRunner) call(args []Value) (Value, error) {
	return runner.callWithChargedRoots(args)
}

// callWithChargedRoots invokes the block, additionally charging per-call roots that
// live only in the iterator's Go frame and evolve every call -- the reduce
// accumulator, whose current payload (the seed on the first call, the previous
// call's result thereafter) is not in the runner's one-time baseline. The accumulator
// is probed against the snapshotted call roots, so a no-seed accumulator that is the
// receiver's first element deduplicates against the receiver and is charged only its
// structural slots, never a second copy of the receiver's data.
func (runner *blockCallRunner) callWithChargedRoots(args []Value, chargedRoots ...Value) (Value, error) {
	if err := runner.exec.checkContext(); err != nil {
		return NewNil(), err
	}

	env := runner.env
	if env == nil {
		env = newBlockAssignmentEnv(runner.blk.Env)
	} else {
		env.resetForBlockCall(runner.blk.Env)
		env.assignBoundary = true
		env.rebindOuter = true
	}
	val, err := runner.exec.callBlock(runner.blk, args, env, runner.charge, Position{}, chargedRoots...)
	if err != nil {
		if errors.Is(err, errLoopNext) && !runner.nextContinues {
			if nextVal, ok := loopNextValue(err); ok {
				return nextVal, nil
			}
			return NewNil(), nil
		}
		return NewNil(), err
	}
	if err := runner.exec.checkContext(); err != nil {
		return NewNil(), err
	}
	return val, nil
}

// blockWantsCollapsedPair reports whether a hash iterator should yield each entry
// as a single two-element [key, value] pair instead of two separate arguments. It
// mirrors Ruby, where a block declaring exactly one positional parameter receives
// the pair while a block with two or more positional parameters auto-splats into
// key and value. A lone destructuring parameter such as |(k, v)| still counts as
// one parameter, so it receives the pair and unpacks it. Any rest or keyword
// parameter opts out so the iterator keeps yielding key and value separately.
//
// It takes the block directly (rather than a runner) so a hash iterator can pick
// the yield shape -- and thus size its walk scratch -- before building the runner,
// letting the scratch be reserved before the runner snapshots its bind-charge
// baseline.
func blockWantsCollapsedPair(blk *Block) bool {
	return blockPositionalArity(blk) == 1
}

func blockPositionalArity(blk *Block) int {
	if blk == nil {
		return 0
	}
	if len(blk.Params) == 0 {
		return implicitBlockParamArity(blk.ImplicitParams)
	}
	positional := 0
	for i := range blk.Params {
		switch blk.Params[i].Kind {
		case ParamNormal:
			positional++
		default:
			return 0
		}
	}
	return positional
}

func implicitBlockParamArity(params []string) int {
	arity := 0
	for _, name := range params {
		index := implicitBlockParamIndex(name)
		if index+1 > arity {
			arity = index + 1
		}
	}
	return arity
}

// CallBlock invokes a block value with the provided arguments.
// This is the public entry point for capability adapters that need to
// call user-supplied blocks (e.g. db.each, db.tx).
func (exec *Execution) CallBlock(block Value, args []Value) (Value, error) {
	return exec.callBlockValue(block, args, Position{})
}

func (exec *Execution) callBlockValue(block Value, args []Value, pos Position) (Value, error) {
	if err := ensureBlock(block, ""); err != nil {
		return NewNil(), err
	}
	blk := valueBlock(block)
	// Capability adapters drive blocks with host-supplied arguments and no
	// receiver. Those arguments live only on the Go call stack for the duration of
	// the call, so include them in the bind-charge baseline: a rest-collecting
	// destructure parameter copying part of a large argument into a fresh backing
	// would otherwise be charged that copy against a baseline that omits the
	// argument it was copied from, letting (args) and (rest) each fit the quota
	// while the real peak (args + rest) exceeds it.
	charge := newBlockBindCharge(exec, blk, NewNil(), args, nil, block)
	val, err := exec.callBlock(blk, args, newBlockAssignmentEnv(blk.Env), charge, pos)
	if err != nil && errors.Is(err, errLoopNext) {
		if nextVal, ok := loopNextValue(err); ok {
			return nextVal, nil
		}
		return NewNil(), nil
	}
	return val, err
}

func (exec *Execution) callBlock(blk *Block, args []Value, blockEnv *Env, charge *blockBindCharge, pos Position, chargedRoots ...Value) (Value, error) {
	exec.pushModuleContext(moduleContext{
		key:    blk.moduleKey,
		path:   blk.modulePath,
		root:   blk.moduleRoot,
		script: blk.owner,
	})
	defer exec.popModuleContext()

	if err := charge.begin(args, chargedRoots...); err != nil {
		return NewNil(), err
	}
	bindArgs := rubyBlockBindArgs(blk.Params, args)
	for i, param := range blk.Params {
		var val Value
		if i < len(bindArgs) {
			val = bindArgs[i]
		} else {
			val = NewNil()
		}
		if param.Type != nil {
			normalized, err := normalizeValueForType(val, param.Type, typeContext{
				owner:    blk.owner,
				env:      blk.Env,
				fallback: exec.root,
				exec:     exec,
			})
			if err != nil {
				if isHostControlSignal(err) {
					return NewNil(), err
				}
				if isNormalizationLimitError(err) {
					return NewNil(), exec.wrapError(err, param.Type.Position)
				}
				return NewNil(), exec.errorAt(param.Type.Position, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
			val = normalized
		}
		if param.Target != nil {
			if err := exec.bindBlockParamTarget(blockEnv, param.Target, val, charge, blk); err != nil {
				return NewNil(), err
			}
			continue
		}
		blockEnv.Define(param.Name, val)
	}
	for _, name := range blk.ImplicitParams {
		index := implicitBlockParamIndex(name)
		val := NewNil()
		if index >= 0 && index < len(args) {
			val = args[index]
		}
		blockEnv.Define(name, val)
	}
	// The block's lexical home scopes any literal created while the body runs:
	// a nested block returns from the same method this block does, even when
	// yield executes the body inside a callee frame.
	exec.pushBlockHomeToken(blk.homeReturnToken)
	exec.blockDepth++
	val, returned, err := exec.evalLocalScopeStatements(blk.Body, blockEnv)
	exec.blockDepth--
	exec.popBlockHomeToken()
	val, returned, err = consumeFunctionReturnSignal(val, returned, err)
	if err != nil {
		if errors.Is(err, errRescueRetry) {
			return NewNil(), exec.localJumpErrorAt(pos, "retry cannot cross call boundary")
		}
		return NewNil(), err
	}
	if returned {
		// Ruby non-local return: an explicit return in a block body returns
		// from the method that created the block, not from the block call. A
		// dead home — the method already returned, the block was built outside
		// any method, or it runs in another execution — raises LocalJumpError
		// right here, where a surrounding rescue can catch it. A live home
		// travels the error path so drivers unwind and ensure blocks run; the
		// invocation whose token matches converts it into its return value.
		if !exec.returnTokenLive(blk.homeReturnToken) {
			return NewNil(), exec.localJumpErrorAt(blockBodyPos(blk), "unexpected return")
		}
		return NewNil(), &nonLocalReturnSignal{
			value: blockEnv.detachArrayAppendResult(val),
			token: blk.homeReturnToken,
		}
	}
	return blockEnv.detachArrayAppendResult(val), nil
}

// blockBodyPos anchors a block-level diagnostic to the block's first
// statement, the closest stable position a detached block value carries.
func blockBodyPos(blk *Block) Position {
	if len(blk.Body) > 0 {
		return blk.Body[0].Pos()
	}
	return Position{}
}

func rubyBlockBindArgs(params []Param, args []Value) []Value {
	if len(args) != 1 || args[0].Kind() != KindArray {
		return args
	}
	if rubyBlockPositionalBindCount(params) <= 1 {
		return args
	}
	return args[0].Array()
}

func rubyBlockPositionalBindCount(params []Param) int {
	positional := 0
	for _, param := range params {
		switch param.Kind {
		case ParamNormal, ParamRest:
			positional++
		case ParamKeyword, ParamKeywordRest, ParamBlock:
			continue
		}
	}
	return positional
}

func implicitBlockParamIndex(name string) int {
	if name == "it" {
		return 0
	}
	if len(name) == 2 && name[0] == '_' && name[1] >= '1' && name[1] <= '9' {
		return int(name[1] - '1')
	}
	return -1
}

func blockCanReuseEnv(blk *Block) bool {
	return !statementsCaptureCurrentEnv(blk.Body)
}

func statementsCaptureCurrentEnv(stmts []Statement) bool {
	for _, stmt := range stmts {
		if statementCapturesCurrentEnv(stmt) {
			return true
		}
	}
	return false
}

func statementCapturesCurrentEnv(stmt Statement) bool {
	switch s := stmt.(type) {
	case *FunctionStmt, *ClassStmt:
		return true
	case *ReturnStmt:
		return expressionCapturesCurrentEnv(s.Value)
	case *RaiseStmt:
		return expressionCapturesCurrentEnv(s.Value) || expressionCapturesCurrentEnv(s.Message)
	case *AssignStmt:
		return expressionCapturesCurrentEnv(s.Target) || expressionCapturesCurrentEnv(s.Value)
	case *LogicalStmt:
		return statementCapturesCurrentEnv(s.Left) || statementCapturesCurrentEnv(s.Right)
	case *ExprStmt:
		return expressionCapturesCurrentEnv(s.Expr)
	case *IfStmt:
		if expressionCapturesCurrentEnv(s.Condition) ||
			statementsCaptureCurrentEnv(s.Consequent) ||
			statementsCaptureCurrentEnv(s.Alternate) {
			return true
		}
		for _, branch := range s.ElseIf {
			if statementCapturesCurrentEnv(branch) {
				return true
			}
		}
		return false
	case *ForStmt:
		return expressionCapturesCurrentEnv(s.Target) || expressionCapturesCurrentEnv(s.Iterable) || statementsCaptureCurrentEnv(s.Body)
	case *WhileStmt:
		return expressionCapturesCurrentEnv(s.Condition) || statementsCaptureCurrentEnv(s.Body)
	case *UntilStmt:
		return expressionCapturesCurrentEnv(s.Condition) || statementsCaptureCurrentEnv(s.Body)
	case *BreakStmt:
		return expressionCapturesCurrentEnv(s.Value)
	case *NextStmt:
		return expressionCapturesCurrentEnv(s.Value)
	case *RetryStmt, *EnumStmt:
		return false
	case *TryStmt:
		for i := range s.Rescues {
			if statementsCaptureCurrentEnv(s.Rescues[i].Body) {
				return true
			}
		}
		return statementsCaptureCurrentEnv(s.Body) ||
			statementsCaptureCurrentEnv(s.Else) ||
			statementsCaptureCurrentEnv(s.Ensure)
	default:
		return true
	}
}

func expressionCapturesCurrentEnv(expr Expression) bool {
	switch e := expr.(type) {
	case nil:
		return false
	case *BlockLiteral:
		return true
	case *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return false
	case *ArrayLiteral:
		for _, elem := range e.Elements {
			if expressionCapturesCurrentEnv(elem) {
				return true
			}
		}
		return false
	case *HashLiteral:
		for _, pair := range e.Pairs {
			if expressionCapturesCurrentEnv(pair.Key) || expressionCapturesCurrentEnv(pair.Value) {
				return true
			}
		}
		return false
	case *CallExpr:
		if expressionCapturesCurrentEnv(e.Callee) || e.Block != nil {
			return true
		}
		for _, arg := range e.Args {
			if expressionCapturesCurrentEnv(arg) {
				return true
			}
		}
		for _, kw := range e.KwArgs {
			if expressionCapturesCurrentEnv(kw.Value) {
				return true
			}
		}
		return false
	case *MemberExpr:
		return expressionCapturesCurrentEnv(e.Object)
	case *ScopeExpr:
		return expressionCapturesCurrentEnv(e.Object)
	case *IndexExpr:
		if expressionCapturesCurrentEnv(e.Object) {
			return true
		}
		for _, index := range e.Indices {
			if expressionCapturesCurrentEnv(index) {
				return true
			}
		}
		return false
	case *DestructureTarget:
		for _, elem := range e.Elements {
			if expressionCapturesCurrentEnv(elem.Target) {
				return true
			}
		}
		return false
	case *UnaryExpr:
		return expressionCapturesCurrentEnv(e.Right)
	case *BinaryExpr:
		return expressionCapturesCurrentEnv(e.Left) || expressionCapturesCurrentEnv(e.Right)
	case *ConditionalExpr:
		return expressionCapturesCurrentEnv(e.Condition) ||
			expressionCapturesCurrentEnv(e.Consequent) ||
			expressionCapturesCurrentEnv(e.Alternate)
	case *RescueExpr:
		return expressionCapturesCurrentEnv(e.Body) ||
			expressionCapturesCurrentEnv(e.Fallback)
	case *IfExpr:
		if expressionCapturesCurrentEnv(e.Condition) ||
			expressionCapturesCurrentEnv(e.Consequent) ||
			expressionCapturesCurrentEnv(e.Alternate) {
			return true
		}
		for _, branch := range e.ElseIf {
			if expressionCapturesCurrentEnv(branch.Condition) || expressionCapturesCurrentEnv(branch.Result) {
				return true
			}
		}
		return false
	case *RangeExpr:
		return expressionCapturesCurrentEnv(e.Start) || expressionCapturesCurrentEnv(e.End)
	case *CaseExpr:
		if expressionCapturesCurrentEnv(e.Target) || expressionCapturesCurrentEnv(e.ElseExpr) {
			return true
		}
		for _, clause := range e.Clauses {
			for _, value := range clause.Values {
				if expressionCapturesCurrentEnv(value.Expr) {
					return true
				}
			}
			if expressionCapturesCurrentEnv(clause.Result) {
				return true
			}
		}
		return false
	case *YieldExpr:
		for _, arg := range e.Args {
			if expressionCapturesCurrentEnv(arg) {
				return true
			}
		}
		return false
	case *InterpolatedString:
		return stringPartsCaptureCurrentEnv(e.Parts)
	case *InterpolatedSymbol:
		return stringPartsCaptureCurrentEnv(e.Parts)
	case *IfStmt, *ForStmt, *WhileStmt, *UntilStmt, *TryStmt:
		return statementCapturesCurrentEnv(e.(Statement))
	default:
		return true
	}
}

func stringPartsCaptureCurrentEnv(parts []StringPart) bool {
	for _, part := range parts {
		stringExpr, ok := part.(StringExpr)
		if ok && expressionCapturesCurrentEnv(stringExpr.Expr) {
			return true
		}
	}
	return false
}

func (exec *Execution) bindBlockParamTarget(env *Env, target Expression, value Value, charge *blockBindCharge, blk *Block) error {
	switch t := target.(type) {
	case *Identifier:
		env.Define(t.Name, value)
		// Charge the bound leaf so a fresh rest backing a destructure collected
		// (the only binding that allocates beyond the call roots) counts toward the
		// quota even when the block body is empty. Pass-through bindings dedup
		// against the seeded arguments and charge essentially nothing.
		return charge.charge(value)
	case *DestructureTarget:
		// Walk the destructure with the block charge's own destructureCharge and charge
		// every bound leaf through the per-call bind charge. The destructureCharge
		// preflights a named rest's backing against the quota BEFORE assignDestructure
		// makes+copies it, so a single huge tail cannot materialize under a quota below
		// one copied window before the post-bind charge observes it. The post-bind
		// charge then probes each fresh leaf against the persistent root estimator --
		// which already holds the iterator's Go-stack receiver -- so a rest backing is
		// charged its real, dedup-aware footprint against the receiver the standalone
		// exec.assignDestructure charge cannot see (its liveRoot is only the single
		// yielded value, not the whole receiver).
		return assignDestructureWithNormalizer(t, value, func(target Expression, value Value) error {
			return exec.bindBlockParamTarget(env, target, value, charge, blk)
		}, charge.destructureCharge(), func(element DestructureElement, value Value) (Value, error) {
			return exec.normalizeBlockDestructureElement(blk, element, value)
		})
	default:
		return exec.errorAt(target.Pos(), "invalid block parameter target")
	}
}

func (exec *Execution) normalizeBlockDestructureElement(blk *Block, element DestructureElement, value Value) (Value, error) {
	if element.Type == nil {
		return value, nil
	}
	normalized, err := normalizeValueForType(value, element.Type, typeContext{
		owner:    blk.owner,
		env:      blk.Env,
		fallback: exec.root,
		exec:     exec,
	})
	if err != nil {
		if isHostControlSignal(err) {
			return NewNil(), err
		}
		if isNormalizationLimitError(err) {
			return NewNil(), exec.wrapError(err, element.Type.Position)
		}
		name := ast.FormatDestructureTarget(element.Target)
		if name == "" {
			name = "destructured value"
		}
		return NewNil(), exec.errorAt(element.Type.Position, "%s", formatArgumentTypeMismatch(name, err))
	}
	return normalized, nil
}

func (exec *Execution) evalYield(expr *YieldExpr, env *Env) (Value, error) {
	block, ok := env.lookupCallBlock()
	if !ok || block.Kind() == KindNil {
		return NewNil(), exec.localJumpErrorAt(expr.Pos(), "no block given")
	}
	blk := valueBlock(block)
	args := make([]Value, 0, len(expr.Args))
	for i, arg := range expr.Args {
		expectation := yieldArgumentExpectation(blk, i, len(expr.Args))
		val, err := exec.evalExpressionWithExpectation(arg, env, expectation)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), err
		}
		args = append(args, val)
	}
	if len(args) > 0 {
		if err := exec.checkMemoryWith(args...); err != nil {
			return NewNil(), err
		}
	}
	return exec.callBlockValue(block, args, expr.Pos())
}

func yieldArgumentExpectation(blk *Block, argIndex, argCount int) expressionExpectation {
	if blk == nil || len(blk.Params) == 0 {
		return expressionExpectation{}
	}
	return blockArgumentExpectation(blk.Params, argIndex, argCount)
}

func blockArgumentExpectation(params []Param, argIndex, argCount int) expressionExpectation {
	if len(params) == 0 {
		return expressionExpectation{}
	}
	if argCount == 1 {
		param, ok := positionalCallableParam(params, 0)
		if !ok {
			return expressionExpectation{}
		}
		expectation := positionalArgumentExpectation(param)
		if rubyBlockPositionalBindCount(params) > 1 {
			expectation.arrayElement = blockArrayElementExpectation(params)
		}
		return expectation
	}
	param, ok := positionalCallableParam(params, argIndex)
	if !ok {
		return expressionExpectation{}
	}
	return positionalArgumentExpectation(param)
}

func blockArrayElementExpectation(params []Param) func(int, int) expressionExpectation {
	return func(index, _ int) expressionExpectation {
		param, ok := positionalCallableParam(params, index)
		if !ok {
			return expressionExpectation{}
		}
		return positionalArgumentExpectation(param)
	}
}

func (exec *Execution) assignToMember(obj Value, property string, value Value, pos Position) error {
	setterName := property + "="
	var methods map[string]*ScriptFunction
	var vars map[string]Value

	switch obj.Kind() {
	case KindInstance:
		methods = valueInstance(obj).Class.Methods
		vars = valueInstance(obj).Ivars
	case KindClass:
		methods = valueClass(obj).ClassMethods
		vars = valueClass(obj).ClassVars
	default:
		return exec.errorAt(pos, "cannot assign to %s", obj.Kind())
	}

	if fn, ok := methods[setterName]; ok {
		if fn.Private {
			return exec.errorAt(pos, "private method %s", setterName)
		}
		_, err := exec.callFunction(fn, obj, []Value{value}, nil, NewNil(), pos)
		if err != nil {
			if ok, controlErr := exec.callBoundaryControlError(err, pos); ok {
				return controlErr
			}
		}
		return err
	}

	if _, hasGetter := methods[property]; hasGetter {
		return exec.errorAt(pos, "cannot assign to read-only property %s", property)
	}

	vars[property] = value
	return nil
}

func (exec *Execution) assign(target Expression, value Value, env *Env) error {
	switch t := target.(type) {
	case *Identifier:
		if self, ok := classConstantAssignmentSelf(t.Name, env); ok && !env.hasCallLocalBinding(t.Name) {
			valueClass(self).ClassVars[t.Name] = value
			return nil
		}
		env.Assign(t.Name, value)
		return nil
	case *DestructureTarget:
		return exec.assignDestructure(t, value, func(target Expression, value Value) error {
			return exec.assign(target, value, env)
		})
	case *MemberExpr:
		obj, err := exec.evalExpression(t.Object, env)
		if err != nil {
			return err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return err
		}
		return exec.assignToEvaluatedMember(t, obj, value)
	case *IvarExpr:
		self, ok := env.Get("self")
		if !ok || self.Kind() != KindInstance {
			return exec.errorAt(target.Pos(), "no instance context for ivar")
		}
		valueInstance(self).Ivars[t.Name] = value
		return nil
	case *ClassVarExpr:
		self, ok := env.Get("self")
		if !ok {
			return exec.errorAt(target.Pos(), "no class context for class var")
		}
		switch self.Kind() {
		case KindInstance:
			valueInstance(self).Class.ClassVars[t.Name] = value
			return nil
		case KindClass:
			valueClass(self).ClassVars[t.Name] = value
			return nil
		default:
			return exec.errorAt(target.Pos(), "no class context for class var")
		}
	case *IndexExpr:
		obj, err := exec.evalExpression(t.Object, env)
		if err != nil {
			return err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return err
		}
		indices, err := exec.evalIndexSelectors(t, obj, env)
		if err != nil {
			return err
		}
		return exec.assignToEvaluatedIndex(t, obj, indices, value)
	default:
		return exec.errorAt(target.Pos(), "invalid assignment target")
	}
}

func classConstant(self Value, name string) (Value, bool) {
	if !isConstantIdentifier(name) {
		return NewNil(), false
	}
	switch self.Kind() {
	case KindClass:
		val, ok := valueClass(self).ClassVars[name]
		return val, ok
	case KindInstance:
		inst := valueInstance(self)
		if inst == nil || inst.Class == nil {
			return NewNil(), false
		}
		val, ok := inst.Class.ClassVars[name]
		return val, ok
	default:
		return NewNil(), false
	}
}

func isConstantIdentifier(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return r != utf8.RuneError && unicode.IsUpper(r)
}

func isClassConstantAssignmentName(name string, env *Env) bool {
	_, ok := classConstantAssignmentSelf(name, env)
	return ok
}

func classConstantAssignmentSelf(name string, env *Env) (Value, bool) {
	if env == nil || !isConstantIdentifier(name) {
		return Value{}, false
	}
	self, ok := env.Get("self")
	if !ok || self.Kind() != KindClass {
		return Value{}, false
	}
	return self, true
}

func (exec *Execution) assignToEvaluatedMember(target *MemberExpr, obj, value Value) error {
	switch obj.Kind() {
	case KindHash, KindObject:
		key := NewString(target.Property)
		if obj.Kind() == KindHash {
			key = hashMemberAssignmentKey(obj, target.Property)
		}
		return hashSet(obj, key, value)
	case KindInstance, KindClass:
		return exec.assignToMember(obj, target.Property, value, target.Pos())
	default:
		return exec.errorAt(target.Pos(), "cannot assign to %s", obj.Kind())
	}
}

// assignToEvaluatedIndex writes value at a bracket target. Array assignment
// accepts a single integer index, counting a negative index back from the end
// (Ruby's arr[-1] = x); an index outside the array raises rather than
// auto-extending. Hash and object assignment store under a single key. Slice
// assignment (start/length or range targets) is not supported.
func (exec *Execution) assignToEvaluatedIndex(target *IndexExpr, obj Value, indices []Value, value Value) error {
	switch obj.Kind() {
	case KindArray:
		if len(indices) != 1 {
			return exec.errorAt(target.Position, "array index assignment expects a single index")
		}
		arr := obj.Array()
		i, err := exec.indexSelectorToInt(target, indices[0], 0)
		if err != nil {
			return err
		}
		pos := i
		if pos < 0 {
			pos += len(arr)
		}
		if pos < 0 || pos >= len(arr) {
			return exec.errorAt(target.IndexPos(0), "array index out of bounds")
		}
		arr[pos] = value
		return nil
	case KindHash, KindObject:
		if len(indices) != 1 {
			return exec.errorAt(target.Position, "%s index assignment expects a single key", obj.Kind())
		}
		if err := hashSet(obj, indices[0], value); err != nil {
			return exec.errorAt(target.IndexPos(0), "%s", err.Error())
		}
		return nil
	case KindInstance:
		// obj[i, ...] = value dispatches to a user-defined []= method with the
		// indices followed by the assigned value, mirroring Ruby's index-write
		// protocol. The method's return value is discarded, as in Ruby, where
		// the assignment expression always evaluates to the assigned value.
		fn, ok := instanceOperatorMethod(obj, "[]=")
		if !ok {
			return exec.errorAt(target.Object.Pos(), "cannot index %s: %s does not define []=", obj.Kind(), valueInstance(obj).Class.Name)
		}
		if fn.Private {
			return exec.errorAt(target.Position, "private method []=")
		}
		args := make([]Value, 0, len(indices)+1)
		args = append(args, indices...)
		args = append(args, value)
		_, err := exec.callOperatorFunction(fn, obj, args, target.Position)
		return err
	default:
		return exec.errorAt(target.Object.Pos(), "cannot index %s", obj.Kind())
	}
}

// AssignDestructure applies Vibescript's destructuring assignment rules and
// invokes assign for each concrete leaf target. It is the host-facing entry
// point used by tools that walk destructuring targets without a sandboxed
// Execution (such as the REPL extracting bound names from a result); it never
// charges memory because those callers run outside a quota. Sandboxed evaluation
// goes through Execution.assignDestructure, which charges every fresh slot array
// (the right-hand-side snapshot and any named rest window) against the memory
// quota before it is allocated.
func AssignDestructure(target *DestructureTarget, value Value, assign func(Expression, Value) error) error {
	return assignDestructure(target, value, assign, destructureCharge{check: noopDestructureCheck})
}

// destructureCharge meters the fresh int-value slot arrays a destructuring
// assignment allocates (the right-hand-side snapshot and any named rest
// window) against a memory quota before they materialize.
//
// liveRoot is the top-level evaluated right-hand side. While assignDestructure
// runs it is held only on this call's Go stack — a function or capability
// return, or an array literal — so the memory estimator's root walk never sees
// it, yet it is real memory live at the peak of every slot array this charge
// guards. Threading it into each check charges it as a root (deduplicated
// against the base, so a right-hand side already reachable from an environment
// contributes nothing extra). Every nested element is reachable through this
// root, so the top-level value alone covers all levels.
//
// liveSlots tracks the snapshot slots that are already allocated but reachable
// only from a Go-local slice on the call stack, so the root walk cannot see them
// either; the count threads down the recursion so a nested destructure charges
// its own arrays on top of every enclosing level's still-live snapshot,
// projecting the true peak rather than a single level's allocation.
type destructureCharge struct {
	check     func(count, liveSlots int, liveRoot Value) error
	liveRoot  Value
	liveSlots int
}

// noopDestructureCheck admits every allocation. AssignDestructure's host callers
// run outside a memory quota, so the arrays they may materialize are not metered.
func noopDestructureCheck(int, int, Value) error { return nil }

// assignDestructure applies Vibescript's destructuring assignment rules and
// invokes assign for each concrete leaf target. The returned charge meters every
// fresh slot array against the caller's memory quota before it is allocated,
// counting the live right-hand side held on the Go stack as a root so the peak
// is projected even when the right-hand side is not reachable from any
// environment (a function return or array literal).
func (exec *Execution) assignDestructure(target *DestructureTarget, value Value, assign func(Expression, Value) error) error {
	return assignDestructure(target, value, assign, destructureCharge{check: exec.checkProjectedIntArrayBytesWithLive, liveRoot: value})
}

func assignDestructure(target *DestructureTarget, value Value, assign func(Expression, Value) error, charge destructureCharge) error {
	return assignDestructureWithNormalizer(target, value, assign, charge, nil)
}

type destructureElementNormalizer func(DestructureElement, Value) (Value, error)

func assignDestructureWithNormalizer(target *DestructureTarget, value Value, assign func(Expression, Value) error, charge destructureCharge, normalize destructureElementNormalizer) error {
	values := destructureValues(value)
	// Ruby evaluates the whole right-hand side into an array before performing
	// any assignment, so every target reads its original value regardless of
	// LHS write order. When the RHS is an array, destructureValues aliases its
	// live backing store, so a target that writes into that array and is read
	// back by a later target (e.g. "values[1], *rest = values") would otherwise
	// let that later read observe the mutation. Snapshot the source only when a
	// writing target precedes a reading one; the snapshot's slot array is a fresh
	// allocation, so charge it against the memory quota and fail fast before
	// materializing it. The common case where every target only binds a name (and
	// the scalar RHS path, which already returns a fresh slice) keeps the alias,
	// as does a write whose only followers discard their window (e.g.
	// "values[0], * = values"), which reads nothing the write could corrupt.
	if value.Kind() == KindArray && destructureWriteIsReadBack(target) {
		if err := charge.check(len(values), charge.liveSlots, charge.liveRoot); err != nil {
			return err
		}
		values = append([]Value(nil), values...)
		// The snapshot stays live on this call's stack while every leaf (and any
		// nested destructure) runs, so fold it into the running baseline the
		// charge projects for the rest window and for nested snapshots.
		charge.liveSlots += len(values)
	}
	restIndex := -1
	for i, element := range target.Elements {
		if element.Rest {
			restIndex = i
			break
		}
	}

	if restIndex == -1 {
		for i, element := range target.Elements {
			val := valueAt(values, i)
			var err error
			if normalize != nil {
				val, err = normalize(element, val)
				if err != nil {
					return err
				}
			}
			if err := assignDestructureValue(element.Target, val, assign, charge, normalize); err != nil {
				return err
			}
		}
		return nil
	}

	trailing := len(target.Elements) - restIndex - 1
	// Clamp the rest window to the available values. When the target has more
	// fixed targets than the value provides, restIndex can exceed len(values);
	// the missing fixed targets bind to nil (via valueAt) and the rest is empty,
	// matching Ruby. Without clamping the low bound, values[restIndex:restEnd]
	// would panic the host (a sandbox DoS) on a slice-out-of-range.
	restStart := min(restIndex, len(values))
	restEnd := max(restStart, len(values)-trailing)
	// An anonymous rest target (bare "*") discards its window, so skip building
	// the rest array entirely. Copying and wrapping values[restStart:restEnd]
	// would otherwise allocate a full second slice and add O(n) work for a
	// segment no binding will ever read (e.g. *, last = huge_array).
	restAnonymous := target.Elements[restIndex].Target == nil
	for i, element := range target.Elements {
		var val Value
		switch {
		case i < restIndex:
			val = valueAt(values, i)
		case i == restIndex:
			if restAnonymous {
				continue
			}
			// The rest window is a second fresh slot array that coexists with the
			// snapshot above (charge.liveSlots already folds it in), so charge it
			// against the quota before materializing it. Without this a quota that
			// fits base+snapshot but not base+snapshot+rest would pass the snapshot
			// check, allocate the window anyway, and the snapshot would be gone by
			// the next per-statement check, letting execution exceed the quota.
			if err := charge.check(restEnd-restStart, charge.liveSlots, charge.liveRoot); err != nil {
				return err
			}
			// Allocate the rest backing with capacity exactly equal to the collected
			// element count. append([]Value(nil), src...) (and slices.Clone, which
			// wraps it) would let Go's growslice round the capacity up past len, so
			// the memory estimator -- which charges slice backings by cap -- would see
			// a larger array than the value's own length implies. A make+copy keeps
			// cap == len so the rest array's charged footprint matches what its
			// element count predicts.
			restSrc := values[restStart:restEnd]
			restValues := make([]Value, len(restSrc))
			copy(restValues, restSrc)
			val = NewArray(restValues)
		default:
			// Trailing targets bind to the values immediately after the rest
			// window, left-to-right. When the input is too short to fill them
			// all, the remaining targets pad with nil on the right, matching
			// Ruby (e.g. a, *, y, z = [1, 2] yields a=1, y=2, z=nil).
			val = valueAt(values, restEnd+(i-restIndex-1))
		}
		var err error
		if normalize != nil {
			val, err = normalize(element, val)
			if err != nil {
				return err
			}
		}
		if err := assignDestructureValue(element.Target, val, assign, charge, normalize); err != nil {
			return err
		}
	}
	return nil
}

func assignDestructureValue(target Expression, value Value, assign func(Expression, Value) error, charge destructureCharge, normalize destructureElementNormalizer) error {
	if target == nil {
		// Anonymous rest target ("*"): discard the captured values.
		return nil
	}
	if nested, ok := target.(*DestructureTarget); ok {
		return assignDestructureWithNormalizer(nested, value, assign, charge, normalize)
	}
	return assign(target, value)
}

func destructureValues(value Value) []Value {
	if value.Kind() == KindArray {
		return value.Array()
	}
	return []Value{value}
}

// destructureWriteIsReadBack reports whether the target list contains a leaf
// that assigns into an existing container (an index or member place) followed,
// in left-to-right execution order, by a leaf that reads a value out of the
// right-hand side. Only that ordering lets a later read observe an earlier
// write's mutation of the aliased RHS array, so it is the only case that needs
// the snapshot AssignDestructure would otherwise take unconditionally.
//
// A write whose only successors discard their window (e.g. "values[0], * =
// values", where the trailing anonymous rest reads nothing, or "values[0], (*)
// = values", where the nested follower destructures nothing) is safe to alias:
// no surviving read can observe the mutation, so snapshotting it would copy the
// whole backing slice for no observable effect. Plain identifiers, ivars, and
// class vars write to environment or instance slots that never alias the RHS
// array's backing store, so they count only as reads, never as container
// writes.
func destructureWriteIsReadBack(target *DestructureTarget) bool {
	sawWrite := false
	for _, element := range target.Elements {
		if sawWrite && destructureElementReads(element) {
			return true
		}
		if destructureElementWrites(element.Target) {
			sawWrite = true
		}
	}
	return false
}

// destructureElementReads reports whether an element binds at least one value
// out of the right-hand side. The anonymous rest ("*") has a nil target and
// discards its window without observing any value, so it never reads. A nested
// destructure target reads only if at least one of its own elements reads:
// an all-discard pattern such as "(*)" binds nothing, so it must be treated
// like the anonymous rest and not force a defensive snapshot of the RHS.
func destructureElementReads(element DestructureElement) bool {
	if element.Target == nil {
		return false
	}
	if nested, ok := element.Target.(*DestructureTarget); ok {
		for _, inner := range nested.Elements {
			if destructureElementReads(inner) {
				return true
			}
		}
		return false
	}
	return true
}

// destructureElementWrites reports whether a leaf (or any nested leaf) assigns
// into an existing container slot, which can mutate an aliased RHS array's
// backing store.
func destructureElementWrites(target Expression) bool {
	switch leaf := target.(type) {
	case *IndexExpr, *MemberExpr:
		return true
	case *DestructureTarget:
		for _, element := range leaf.Elements {
			if destructureElementWrites(element.Target) {
				return true
			}
		}
	}
	return false
}

func valueAt(values []Value, index int) Value {
	if index < 0 || index >= len(values) {
		return NewNil()
	}
	return values[index]
}

func (exec *Execution) evalArrayAppendAssignment(stmt *AssignStmt, env *Env) (Value, bool, error) {
	target, ok := stmt.Target.(*Identifier)
	if !ok {
		return NewNil(), false, nil
	}

	switch value := stmt.Value.(type) {
	case *CallExpr:
		member, ok := value.Callee.(*MemberExpr)
		// Only push uses the accumulator fast path. That path reuses the
		// receiver's hidden backing buffer across iterations, which is sound for
		// push because it is the canonical accumulator pattern. append is a
		// documented non-mutating helper: routing it through the shared buffer
		// would let escaped aliases (b = a) observe later appends, so it stays on
		// the normal copy path that always returns a fresh array.
		if !ok || member.Property != "push" || len(value.KwArgs) > 0 || value.Block != nil {
			return NewNil(), false, nil
		}
		receiver, ok := member.Object.(*Identifier)
		if !ok || receiver.Name != target.Name {
			return NewNil(), false, nil
		}
		return exec.evalArrayPushAssignment(target.Name, value, env)
	case *BinaryExpr:
		left, ok := value.Left.(*Identifier)
		if !ok || left.Name != target.Name {
			return NewNil(), false, nil
		}
		switch value.Operator {
		case tokenPlus:
			right, ok := value.Right.(*ArrayLiteral)
			if !ok {
				return NewNil(), false, nil
			}
			return exec.evalArrayConcatAppendAssignment(target.Name, value, right, env)
		case tokenShovel:
			return exec.evalArrayShovelAppendAssignment(target.Name, value, env)
		default:
			return NewNil(), false, nil
		}
	default:
		return NewNil(), false, nil
	}
}

func (exec *Execution) evalArrayPushAssignment(name string, call *CallExpr, env *Env) (Value, bool, error) {
	receiver, ok := env.Get(name)
	if !ok || receiver.Kind() != KindArray {
		return NewNil(), false, nil
	}
	if err := exec.checkMemoryWith(receiver); err != nil {
		return NewNil(), true, err
	}

	args, err := exec.evalCallArgs(call, env)
	if err != nil {
		return NewNil(), true, err
	}
	if err := exec.checkCallMemoryRoots(receiver, args, nil, NewNil()); err != nil {
		return NewNil(), true, err
	}

	return exec.assignArrayAppendResult(name, receiver.Array(), args, env), true, nil
}

func (exec *Execution) evalArrayConcatAppendAssignment(name string, expr *BinaryExpr, right *ArrayLiteral, env *Env) (Value, bool, error) {
	receiver, ok := env.Get(name)
	if !ok || receiver.Kind() != KindArray {
		return NewNil(), false, nil
	}
	if err := exec.checkMemoryWith(receiver); err != nil {
		return NewNil(), true, err
	}

	values, err := exec.evalArrayLiteralElements(right, env)
	if err != nil {
		return NewNil(), true, err
	}
	rightValue := arrayValueFromAppendBuffer(values)
	if err := exec.checkMemoryWith(receiver, rightValue); err != nil {
		return NewNil(), true, err
	}

	result := exec.assignArrayAppendResult(name, receiver.Array(), values, env)
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), true, exec.wrapError(err, expr.Pos())
	}
	return result, true, nil
}

// evalArrayShovelAppendAssignment handles the accumulator form
// `values = values << element`, appending the single right-hand value to the
// receiver through the shared backing buffer just like the push and concat
// fast paths. The shovel operator never mutates in place, so this reassignment
// is the idiomatic Vibescript accumulator and avoids re-copying the array on
// every iteration.
func (exec *Execution) evalArrayShovelAppendAssignment(name string, expr *BinaryExpr, env *Env) (Value, bool, error) {
	receiver, ok := env.Get(name)
	if !ok || receiver.Kind() != KindArray {
		return NewNil(), false, nil
	}
	if err := exec.checkMemoryWith(receiver); err != nil {
		return NewNil(), true, err
	}

	element, err := exec.evalExpressionWithAuto(expr.Right, env, true)
	if err != nil {
		return NewNil(), true, err
	}
	if err := exec.checkMemoryWith(receiver, element); err != nil {
		return NewNil(), true, err
	}

	result := exec.assignArrayAppendResult(name, receiver.Array(), []Value{element}, env)
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), true, exec.wrapError(err, expr.Pos())
	}
	return result, true, nil
}

func (exec *Execution) evalArrayLiteralElements(literal *ArrayLiteral, env *Env) ([]Value, error) {
	values := make([]Value, len(literal.Elements))
	for i, element := range literal.Elements {
		val, err := exec.evalExpressionWithAuto(element, env, true)
		if err != nil {
			return nil, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return nil, err
		}
		values[i] = val
	}
	return values, nil
}

func (exec *Execution) assignArrayAppendResult(name string, base, extras []Value, env *Env) Value {
	buffer, ok := env.arrayAppendBuffer(name)
	if !ok {
		buffer = make([]Value, len(base), len(base)+len(extras))
		copy(buffer, base)
	}
	buffer = append(buffer, extras...)
	result := arrayValueFromAppendBuffer(buffer)
	env.assignArrayAppendBuffer(name, result, buffer)
	return result
}

func arrayValueFromAppendBuffer(buffer []Value) Value {
	return NewArray(buffer[:len(buffer):len(buffer)])
}

func (exec *Execution) evalRangeExpr(expr *RangeExpr, env *Env) (Value, error) {
	startVal, err := exec.evalExpression(expr.Start, env)
	if err != nil {
		return NewNil(), err
	}
	endVal, err := exec.evalExpression(expr.End, env)
	if err != nil {
		return NewNil(), err
	}
	start, err := valueToInt64(startVal)
	if err != nil {
		return NewNil(), exec.errorAt(expr.Start.Pos(), "%s", err.Error())
	}
	end, err := valueToInt64(endVal)
	if err != nil {
		return NewNil(), exec.errorAt(expr.End.Pos(), "%s", err.Error())
	}
	return NewRange(Range{Start: start, End: end, Exclusive: expr.Exclusive}), nil
}

func (exec *Execution) evalCaseExpr(expr *CaseExpr, env *Env) (Value, error) {
	return exec.evalCaseExprWithExpectation(expr, env, expressionExpectation{})
}

func (exec *Execution) evalCaseExprWithExpectation(expr *CaseExpr, env *Env, expectation expressionExpectation) (Value, error) {
	var target Value
	hasTarget := expr.Target != nil
	if hasTarget {
		var err error
		target, err = exec.evalExpression(expr.Target, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(target); err != nil {
			return NewNil(), err
		}
	}

	for _, clause := range expr.Clauses {
		matched := false
		for _, candidateExpr := range clause.Values {
			candidate, err := exec.evalExpression(candidateExpr.Expr, env)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkMemoryWith(candidate); err != nil {
				return NewNil(), err
			}
			candidateMatched, err := exec.caseWhenValueMatches(hasTarget, target, candidate, candidateExpr.Splat, expr.Pos())
			if err != nil {
				return NewNil(), err
			}
			if candidateMatched {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		result, err := exec.evalExpressionWithExpectation(clause.Result, env, expectation)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}

	if expr.ElseExpr != nil {
		result, err := exec.evalExpressionWithExpectation(expr.ElseExpr, env, expectation)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}

	return NewNil(), nil
}

func (exec *Execution) caseWhenValueMatches(hasTarget bool, target, candidate Value, splat bool, pos Position) (bool, error) {
	if !splat {
		matched, err := caseWhenMatches(hasTarget, target, candidate)
		if err != nil {
			return false, exec.wrapError(err, pos)
		}
		return matched, nil
	}
	if candidate.Kind() != KindArray {
		return false, exec.errorAt(pos, "case when splat value must be an array")
	}
	for _, item := range candidate.Array() {
		if err := exec.step(); err != nil {
			return false, err
		}
		if err := exec.checkMemoryWith(item); err != nil {
			return false, err
		}
		matched, err := caseWhenMatches(hasTarget, target, item)
		if err != nil {
			return false, exec.wrapError(err, pos)
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func caseWhenMatches(hasTarget bool, target, candidate Value) (bool, error) {
	if !hasTarget {
		return candidate.Truthy(), nil
	}
	return caseCandidateMatches(target, candidate)
}

func caseCandidateMatches(target, candidate Value) (bool, error) {
	// A regex matcher tests the candidate pattern against a string target,
	// mirroring Ruby's Regexp#=== and `when /re/` clause matching.
	if candidate.Kind() == KindRegex {
		return regexCandidateMatches(target, candidate)
	}
	if candidate.Kind() != KindRange {
		return target.Equal(candidate), nil
	}

	switch target.Kind() {
	case KindInt:
		return rangeContainsInt(candidate.Range(), target.Int()), nil
	case KindFloat:
		return rangeContainsFloat(candidate.Range(), target.Float()), nil
	default:
		return target.Equal(candidate), nil
	}
}

func rangeContainsInt(rng Range, value int64) bool {
	if rng.Start <= rng.End {
		if rng.Exclusive {
			return value >= rng.Start && value < rng.End
		}
		return value >= rng.Start && value <= rng.End
	}
	if rng.Exclusive {
		return value <= rng.Start && value > rng.End
	}
	return value <= rng.Start && value >= rng.End
}

func rangeContainsFloat(rng Range, value float64) bool {
	if math.IsNaN(value) || value < minInt64Float || value >= maxInt64FloatExclusive {
		return false
	}

	floor := int64(math.Floor(value))
	ceil := int64(math.Ceil(value))
	if rng.Start <= rng.End {
		if floor < rng.Start {
			return false
		}
		if rng.Exclusive {
			return floor < rng.End
		}
		return ceil <= rng.End
	}
	if ceil > rng.Start {
		return false
	}
	if rng.Exclusive {
		return ceil > rng.End
	}
	return floor >= rng.End
}

type loopResultMode uint8

const (
	loopStatementResult loopResultMode = iota
	loopExpressionResult
)

func loopNormalResult(mode loopResultMode, expressionValue, last Value) Value {
	if mode == loopExpressionResult {
		return expressionValue
	}
	return last
}

func loopBreakResult(err error, mode loopResultMode, last Value) (Value, bool, error) {
	if breakVal, ok := loopBreakValue(err); ok {
		return breakVal, false, nil
	}
	if mode == loopExpressionResult {
		return NewNil(), false, nil
	}
	return last, false, nil
}

func expressionValueOrReturn(val Value, returned bool, err error) (Value, error) {
	if err != nil {
		return NewNil(), err
	}
	if returned {
		return NewNil(), newFunctionReturnValue(val)
	}
	return val, nil
}

func (exec *Execution) evalForStatement(stmt *ForStmt, env *Env) (Value, bool, error) {
	return exec.evalForLoop(stmt, env, loopStatementResult)
}

func (exec *Execution) evalForExpression(stmt *ForStmt, env *Env) (Value, error) {
	return expressionValueOrReturn(exec.evalForLoop(stmt, env, loopExpressionResult))
}

func (exec *Execution) evalForLoop(stmt *ForStmt, env *Env, mode loopResultMode) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

	iterable, err := exec.evalExpression(stmt.Iterable, env)
	if err != nil {
		return NewNil(), false, err
	}
	if err := exec.checkMemoryWith(iterable); err != nil {
		return NewNil(), false, err
	}
	predeclareTargetBindingNames(stmt.Target, env)
	last := NewNil()

	switch iterable.Kind() {
	case KindArray:
		for _, item := range iterable.Array() {
			if err := exec.step(); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Pos())
			}
			if err := exec.assign(stmt.Target, item, env); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Target.Pos())
			}
			val, returned, err := exec.evalStatements(stmt.Body, env)
			if err != nil {
				if errors.Is(err, errLoopBreak) {
					return loopBreakResult(err, mode, last)
				}
				if errors.Is(err, errLoopNext) {
					continue
				}
				return NewNil(), false, err
			}
			if returned {
				return val, true, nil
			}
			last = val
		}
	case KindHash:
		val, returned, err := exec.evalForHash(stmt, env, iterable, last, mode)
		if err != nil {
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		last = val
	case KindRange:
		r := iterable.Range()
		if r.Start <= r.End {
			for i := r.Start; rangeLoopAscendingContinues(i, r); i++ {
				if err := exec.step(); err != nil {
					return NewNil(), false, exec.wrapError(err, stmt.Pos())
				}
				if err := exec.assign(stmt.Target, NewInt(i), env); err != nil {
					return NewNil(), false, exec.wrapError(err, stmt.Target.Pos())
				}
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					if errors.Is(err, errLoopBreak) {
						return loopBreakResult(err, mode, last)
					}
					if errors.Is(err, errLoopNext) {
						continue
					}
					return NewNil(), false, err
				}
				if returned {
					return val, true, nil
				}
				last = val
			}
		} else {
			for i := r.Start; rangeLoopDescendingContinues(i, r); i-- {
				if err := exec.step(); err != nil {
					return NewNil(), false, exec.wrapError(err, stmt.Pos())
				}
				if err := exec.assign(stmt.Target, NewInt(i), env); err != nil {
					return NewNil(), false, exec.wrapError(err, stmt.Target.Pos())
				}
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					if errors.Is(err, errLoopBreak) {
						return loopBreakResult(err, mode, last)
					}
					if errors.Is(err, errLoopNext) {
						continue
					}
					return NewNil(), false, err
				}
				if returned {
					return val, true, nil
				}
				last = val
			}
		}
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "cannot iterate over %s", iterable.Kind())
	}

	return loopNormalResult(mode, iterable, last), false, nil
}

func (exec *Execution) evalForHash(stmt *ForStmt, env *Env, iterable, last Value, mode loopResultMode) (Value, bool, error) {
	if hashHasTypedEntries(iterable) {
		count := iterable.HashLen()
		reservePair := !exec.valueReachableFromLiveBase(iterable, NewNil())
		scratch := sortedHashEntryBufferBytes(count)
		if reservePair {
			scratch = saturatingAdd(scratch, exec.maxCollapsedPairBytes(iterable))
		}
		delta := exec.reserveLoopScratch(scratch)
		defer exec.releaseLoopScratch(delta)
		if !reservePair {
			if err := exec.checkCollapsedPairBytesWithLiveBase(iterable, NewNil()); err != nil {
				return NewNil(), false, err
			}
		}
		if err := exec.checkProjectedHashWalkBytes(iterable, nil, nil, NewNil()); err != nil {
			return NewNil(), false, err
		}
		var entryBuf [smallHashKeyBufferSize]HashEntry
		for _, entry := range orderedTypedHashEntriesInto(iterable, entryBuf[:]) {
			if err := exec.step(); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Pos())
			}
			pair := NewArray([]Value{entry.Key, entry.Value})
			if err := exec.assign(stmt.Target, pair, env); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Target.Pos())
			}
			val, returned, err := exec.evalStatements(stmt.Body, env)
			if err != nil {
				if errors.Is(err, errLoopBreak) {
					return loopBreakResult(err, mode, last)
				}
				if errors.Is(err, errLoopNext) {
					continue
				}
				return NewNil(), false, err
			}
			if returned {
				return val, true, nil
			}
			last = val
		}
		return loopNormalResult(mode, iterable, last), false, nil
	}

	entries := iterable.Hash()
	reservePair := !exec.valueReachableFromLiveBase(iterable, NewNil())
	scratch := sortedKeyBufferBytes(len(entries))
	if reservePair {
		scratch = saturatingAdd(scratch, exec.maxCollapsedPairBytes(iterable))
	}
	delta := exec.reserveLoopScratch(scratch)
	defer exec.releaseLoopScratch(delta)
	if !reservePair {
		if err := exec.checkCollapsedPairBytesWithLiveBase(iterable, NewNil()); err != nil {
			return NewNil(), false, err
		}
	}
	if err := exec.checkProjectedHashWalkBytes(iterable, nil, nil, NewNil()); err != nil {
		return NewNil(), false, err
	}
	var keyBuf [smallHashKeyBufferSize]string
	for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
		pair := NewArray([]Value{NewSymbol(key), entries[key]})
		if err := exec.assign(stmt.Target, pair, env); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Target.Pos())
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return loopBreakResult(err, mode, last)
			}
			if errors.Is(err, errLoopNext) {
				continue
			}
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		last = val
	}
	return loopNormalResult(mode, iterable, last), false, nil
}

func rangeLoopAscendingContinues(value int64, rng Range) bool {
	if rng.Exclusive {
		return value < rng.End
	}
	return value <= rng.End
}

func rangeLoopDescendingContinues(value int64, rng Range) bool {
	if rng.Exclusive {
		return value > rng.End
	}
	return value >= rng.End
}

func (exec *Execution) evalWhileStatement(stmt *WhileStmt, env *Env) (Value, bool, error) {
	return exec.evalWhileLoop(stmt, env, loopStatementResult)
}

func (exec *Execution) evalWhileExpression(stmt *WhileStmt, env *Env) (Value, error) {
	return expressionValueOrReturn(exec.evalWhileLoop(stmt, env, loopExpressionResult))
}

func (exec *Execution) evalWhileLoop(stmt *WhileStmt, env *Env, mode loopResultMode) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

	if stmt.BodyFirst {
		predeclareLocalBindingsFromStatements(stmt.Body, env)
	}
	last := NewNil()
	for {
		if err := exec.step(); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Pos())
		}
		condition, err := exec.evalExpression(stmt.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(condition); err != nil {
			return NewNil(), false, err
		}
		if !condition.Truthy() {
			return loopNormalResult(mode, NewNil(), last), false, nil
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return loopBreakResult(err, mode, last)
			}
			if errors.Is(err, errLoopNext) {
				predeclareLocalBindingsFromStatements(stmt.Body, env)
				continue
			}
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		last = val
	}
}

func (exec *Execution) evalUntilStatement(stmt *UntilStmt, env *Env) (Value, bool, error) {
	return exec.evalUntilLoop(stmt, env, loopStatementResult)
}

func (exec *Execution) evalUntilExpression(stmt *UntilStmt, env *Env) (Value, error) {
	return expressionValueOrReturn(exec.evalUntilLoop(stmt, env, loopExpressionResult))
}

func (exec *Execution) evalUntilLoop(stmt *UntilStmt, env *Env, mode loopResultMode) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

	if stmt.BodyFirst {
		predeclareLocalBindingsFromStatements(stmt.Body, env)
	}
	last := NewNil()
	for {
		if err := exec.step(); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Pos())
		}
		condition, err := exec.evalExpression(stmt.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(condition); err != nil {
			return NewNil(), false, err
		}
		if condition.Truthy() {
			return loopNormalResult(mode, NewNil(), last), false, nil
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return loopBreakResult(err, mode, last)
			}
			if errors.Is(err, errLoopNext) {
				predeclareLocalBindingsFromStatements(stmt.Body, env)
				continue
			}
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		last = val
	}
}

func (exec *Execution) evalLocalScopeStatements(stmts []Statement, env *Env) (Value, bool, error) {
	return exec.evalStatements(stmts, env)
}

func predeclareStatementLocalBindings(stmt Statement, env *Env) {
	switch s := stmt.(type) {
	case *AssignStmt:
		predeclareAssignmentLocalBindings(s, env)
	}
}

func predeclareStatementPostLocalBindings(stmt Statement, env *Env) {
	if !statementCanPostPredeclareLocalBindings(stmt) {
		return
	}
	predeclareLocalBindingsFromStatements([]Statement{stmt}, env)
}

func predeclareLocalBindingsFromStatements(stmts []Statement, env *Env) {
	var collector localBindingCollector
	collectLocalBindingNames(stmts, &collector)
	for _, name := range collector.names {
		if isClassConstantAssignmentName(name, env) {
			continue
		}
		env.PredeclareAssignmentLocal(name)
	}
}

func statementCanPostPredeclareLocalBindings(stmt Statement) bool {
	switch stmt.(type) {
	case *LogicalStmt, *IfStmt, *ForStmt, *WhileStmt, *UntilStmt, *TryStmt:
		return true
	default:
		return false
	}
}

func predeclareAssignmentLocalBindings(stmt *AssignStmt, env *Env) {
	if env.rebindOuter {
		predeclareTargetBindingNames(stmt.Target, env)
		return
	}
	predeclareDirectAssignmentTargetBindingNames(stmt.Target, stmt.Value, env)
}

type localBindingCollector struct {
	names []string
	seen  map[string]struct{}
}

func (c *localBindingCollector) add(name string) {
	if _, ok := c.seen[name]; ok {
		return
	}
	if c.seen == nil {
		c.seen = make(map[string]struct{})
	}
	c.seen[name] = struct{}{}
	c.names = append(c.names, name)
}

func collectLocalBindingNames(stmts []Statement, collector *localBindingCollector) {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *AssignStmt:
			collectTargetBindingNames(s.Target, collector)
		case *LogicalStmt:
			collectLocalBindingNames([]Statement{s.Left}, collector)
			collectLocalBindingNames([]Statement{s.Right}, collector)
		case *IfStmt:
			collectLocalBindingNames(s.Consequent, collector)
			for _, branch := range s.ElseIf {
				collectLocalBindingNames(branch.Consequent, collector)
			}
			collectLocalBindingNames(s.Alternate, collector)
		case *ForStmt:
			collectTargetBindingNames(s.Target, collector)
			collectLocalBindingNames(s.Body, collector)
		case *WhileStmt:
			collectLocalBindingNames(s.Body, collector)
		case *UntilStmt:
			collectLocalBindingNames(s.Body, collector)
		case *TryStmt:
			collectLocalBindingNames(s.Body, collector)
			for i := range s.Rescues {
				collectLocalBindingNames(s.Rescues[i].Body, collector)
			}
			collectLocalBindingNames(s.Else, collector)
			collectLocalBindingNames(s.Ensure, collector)
		}
	}
}

func collectTargetBindingNames(target Expression, collector *localBindingCollector) {
	switch t := target.(type) {
	case *Identifier:
		collector.add(t.Name)
	case *DestructureTarget:
		for _, element := range t.Elements {
			collectTargetBindingNames(element.Target, collector)
		}
	}
}

func predeclareTargetBindingNames(target Expression, env *Env) {
	switch t := target.(type) {
	case *Identifier:
		if isClassConstantAssignmentName(t.Name, env) {
			return
		}
		env.PredeclareLocal(t.Name)
	case *DestructureTarget:
		for _, element := range t.Elements {
			predeclareTargetBindingNames(element.Target, env)
		}
	}
}

func predeclareDirectAssignmentTargetBindingNames(target, value Expression, env *Env) {
	switch t := target.(type) {
	case *Identifier:
		if isClassConstantAssignmentName(t.Name, env) {
			return
		}
		env.PredeclareAssignmentLocal(t.Name)
	case *DestructureTarget:
		for _, element := range t.Elements {
			predeclareDirectAssignmentTargetBindingNames(element.Target, value, env)
		}
	}
}

func (exec *Execution) evalMemberAssignment(stmt *AssignStmt, env *Env) (Value, bool, error) {
	target, ok := stmt.Target.(*MemberExpr)
	if !ok {
		return NewNil(), false, nil
	}
	expectation, ok := exec.memberAssignmentValueExpectation(target, stmt.Value, env)
	if !ok {
		return NewNil(), false, nil
	}
	val, err := exec.evalAssignmentValueWithExpectation(stmt, env, expectation)
	if err != nil {
		return NewNil(), true, err
	}
	if err := exec.checkMemoryWith(val); err != nil {
		return NewNil(), true, err
	}
	if err := exec.assign(stmt.Target, val, env); err != nil {
		if errors.Is(err, errStepQuotaExceeded) || errors.Is(err, errMemoryQuotaExceeded) {
			return NewNil(), true, err
		}
		return NewNil(), true, exec.wrapError(err, stmt.Pos())
	}
	return val, true, nil
}

func (exec *Execution) memberAssignmentValueExpectation(target *MemberExpr, value Expression, env *Env) (expressionExpectation, bool) {
	if !memberAssignmentValueCanUseExpectation(value) {
		return expressionExpectation{}, false
	}
	if expectation, ok := exec.staticMemberAssignmentValueExpectation(target, env); ok {
		return expectation, true
	}
	// Assignment evaluates the RHS before the target. Only peek already-bound
	// receivers for side-effect-free RHS shapes, then let assign evaluate the
	// target normally after the value is ready.
	obj, ok := memberAssignmentReceiverValue(target.Object, env)
	if !ok {
		return expressionExpectation{}, false
	}
	expectation := memberSetterValueExpectation(obj, target.Property)
	return expectation, !expectation.empty()
}

func (exec *Execution) staticMemberAssignmentValueExpectation(target *MemberExpr, env *Env) (expressionExpectation, bool) {
	receiver, ok := exec.staticMemberAssignmentReceiverClass(target.Object, env)
	if !ok {
		return expressionExpectation{}, false
	}
	fn := receiver.setter(target.Property)
	if fn == nil {
		return expressionExpectation{}, false
	}
	expectation := setterFunctionValueExpectation(fn)
	return expectation, !expectation.empty()
}

type staticMemberAssignmentReceiver struct {
	class         *ClassDef
	classReceiver bool
}

func (receiver staticMemberAssignmentReceiver) setter(property string) *ScriptFunction {
	if receiver.class == nil {
		return nil
	}
	setterName := property + "="
	if receiver.classReceiver {
		return receiver.class.ClassMethods[setterName]
	}
	return receiver.class.Methods[setterName]
}

func (receiver staticMemberAssignmentReceiver) method(property string) *ScriptFunction {
	if receiver.class == nil {
		return nil
	}
	if receiver.classReceiver {
		return receiver.class.ClassMethods[property]
	}
	return receiver.class.Methods[property]
}

func (exec *Execution) staticMemberAssignmentReceiverClass(expr Expression, env *Env) (staticMemberAssignmentReceiver, bool) {
	switch e := expr.(type) {
	case *Identifier:
		val, ok := env.Get(e.Name)
		if !ok {
			return staticMemberAssignmentReceiver{}, false
		}
		if receiver, ok := staticMemberAssignmentReceiverForValue(val); ok {
			return receiver, true
		}
		if !isStaticZeroArityFunctionReceiver(e, env) {
			return staticMemberAssignmentReceiver{}, false
		}
		classDef, ok := exec.staticClassForType(valueFunction(val).ReturnTy, env)
		if !ok {
			return staticMemberAssignmentReceiver{}, false
		}
		return staticMemberAssignmentReceiver{class: classDef}, true
	case *IvarExpr, *ClassVarExpr:
		val, ok := memberAssignmentReceiverValue(expr, env)
		if !ok {
			return staticMemberAssignmentReceiver{}, false
		}
		return staticMemberAssignmentReceiverForValue(val)
	case *CallExpr:
		classDef, ok := exec.staticCallReturnClass(e, env)
		if !ok {
			return staticMemberAssignmentReceiver{}, false
		}
		return staticMemberAssignmentReceiver{class: classDef}, true
	case *MemberExpr:
		receiver, ok := exec.staticMemberAssignmentReceiverClass(e.Object, env)
		if !ok {
			return staticMemberAssignmentReceiver{}, false
		}
		fn := receiver.method(e.Property)
		if fn == nil {
			return staticMemberAssignmentReceiver{}, false
		}
		classDef, ok := exec.staticClassForType(fn.ReturnTy, env)
		if !ok {
			return staticMemberAssignmentReceiver{}, false
		}
		return staticMemberAssignmentReceiver{class: classDef}, true
	default:
		return staticMemberAssignmentReceiver{}, false
	}
}

func staticMemberAssignmentReceiverForValue(val Value) (staticMemberAssignmentReceiver, bool) {
	switch val.Kind() {
	case KindInstance:
		inst := valueInstance(val)
		if inst == nil || inst.Class == nil {
			return staticMemberAssignmentReceiver{}, false
		}
		return staticMemberAssignmentReceiver{class: inst.Class}, true
	case KindClass:
		classDef := valueClass(val)
		if classDef == nil {
			return staticMemberAssignmentReceiver{}, false
		}
		return staticMemberAssignmentReceiver{class: classDef, classReceiver: true}, true
	default:
		return staticMemberAssignmentReceiver{}, false
	}
}

func (exec *Execution) staticCallReturnClass(call *CallExpr, env *Env) (*ClassDef, bool) {
	switch callee := call.Callee.(type) {
	case *Identifier:
		val, ok := env.Get(callee.Name)
		if !ok || val.Kind() != KindFunction {
			return nil, false
		}
		return exec.staticClassForType(valueFunction(val).ReturnTy, env)
	case *MemberExpr:
		receiver, ok := exec.staticMemberAssignmentReceiverClass(callee.Object, env)
		if !ok {
			return nil, false
		}
		if receiver.classReceiver && callee.Property == "new" {
			return receiver.class, true
		}
		fn := receiver.method(callee.Property)
		if fn == nil {
			return nil, false
		}
		return exec.staticClassForType(fn.ReturnTy, env)
	default:
		return nil, false
	}
}

func (exec *Execution) staticClassForType(ty *TypeExpr, env *Env) (*ClassDef, bool) {
	if ty == nil {
		return nil, false
	}
	if ty.Kind == TypeUnion {
		var match *ClassDef
		for _, option := range ty.Union {
			if option.Kind == TypeNil {
				continue
			}
			classDef, ok := exec.staticClassForType(option, env)
			if !ok {
				return nil, false
			}
			if match != nil && match != classDef {
				return nil, false
			}
			match = classDef
		}
		return match, match != nil
	}
	classDef, ok, err := lookupClassTypeExact(ty, typeContext{
		owner:    exec.script,
		env:      env,
		fallback: exec.root,
	})
	if err != nil || !ok {
		return nil, false
	}
	return classDef, true
}

func memberAssignmentValueCanUseExpectation(expr Expression) bool {
	switch e := expr.(type) {
	case *Identifier, *IvarExpr, *ClassVarExpr, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *RegexLiteral:
		return true
	case *ArrayLiteral:
		for _, element := range e.Elements {
			if !memberAssignmentValueCanUseExpectation(element) {
				return false
			}
		}
		return true
	case *HashLiteral:
		for _, pair := range e.Pairs {
			if !memberAssignmentValueCanUseExpectation(pair.Key) || !memberAssignmentValueCanUseExpectation(pair.Value) {
				return false
			}
		}
		return true
	case *ConditionalExpr:
		return memberAssignmentValueCanUseExpectation(e.Condition) &&
			memberAssignmentValueCanUseExpectation(e.Consequent) &&
			memberAssignmentValueCanUseExpectation(e.Alternate)
	case *IfExpr:
		if !memberAssignmentValueCanUseExpectation(e.Condition) ||
			!memberAssignmentValueCanUseExpectation(e.Consequent) {
			return false
		}
		for _, branch := range e.ElseIf {
			if !memberAssignmentValueCanUseExpectation(branch.Condition) ||
				!memberAssignmentValueCanUseExpectation(branch.Result) {
				return false
			}
		}
		return memberAssignmentOptionalValueCanUseExpectation(e.Alternate)
	case *CaseExpr:
		if e.Target != nil && !memberAssignmentValueCanUseExpectation(e.Target) {
			return false
		}
		for _, clause := range e.Clauses {
			for _, value := range clause.Values {
				if !memberAssignmentValueCanUseExpectation(value.Expr) {
					return false
				}
			}
			if !memberAssignmentValueCanUseExpectation(clause.Result) {
				return false
			}
		}
		return memberAssignmentOptionalValueCanUseExpectation(e.ElseExpr)
	default:
		return false
	}
}

func memberAssignmentOptionalValueCanUseExpectation(expr Expression) bool {
	return expr == nil || memberAssignmentValueCanUseExpectation(expr)
}

func memberAssignmentReceiverValue(expr Expression, env *Env) (Value, bool) {
	switch e := expr.(type) {
	case *Identifier:
		val, ok := env.Get(e.Name)
		return val, ok
	case *IvarExpr:
		self, ok := env.Get("self")
		if !ok || self.Kind() != KindInstance {
			return NewNil(), false
		}
		val, ok := valueInstance(self).Ivars[e.Name]
		if !ok {
			return NewNil(), true
		}
		return val, true
	case *ClassVarExpr:
		self, ok := env.Get("self")
		if !ok {
			return NewNil(), false
		}
		switch self.Kind() {
		case KindInstance:
			val, ok := valueInstance(self).Class.ClassVars[e.Name]
			if !ok {
				return NewNil(), true
			}
			return val, true
		case KindClass:
			val, ok := valueClass(self).ClassVars[e.Name]
			if !ok {
				return NewNil(), true
			}
			return val, true
		default:
			return NewNil(), false
		}
	case *IndexExpr:
		// A literal-indexed element of an already-bound container resolves
		// without side effects, so arr[0].cb = five infers the same callable
		// setter expectation as c.cb = five. Dynamic indices stay uninferred.
		return literalIndexReceiverValue(e, env)
	default:
		return NewNil(), false
	}
}

func literalIndexReceiverValue(e *IndexExpr, env *Env) (Value, bool) {
	if len(e.Indices) != 1 {
		return NewNil(), false
	}
	base, ok := memberAssignmentReceiverValue(e.Object, env)
	if !ok {
		return NewNil(), false
	}
	switch idx := e.Indices[0].(type) {
	case *IntegerLiteral:
		if base.Kind() != KindArray {
			return NewNil(), false
		}
		arr := base.Array()
		i := int(idx.Value)
		if i < 0 {
			i += len(arr)
		}
		if i < 0 || i >= len(arr) {
			return NewNil(), false
		}
		return arr[i], true
	case *StringLiteral:
		if base.Kind() != KindHash && base.Kind() != KindObject {
			return NewNil(), false
		}
		val, ok, err := hashGet(base, NewString(idx.Value))
		if err != nil || !ok {
			return NewNil(), false
		}
		return val, true
	case *SymbolLiteral:
		if base.Kind() != KindHash && base.Kind() != KindObject {
			return NewNil(), false
		}
		val, ok, err := hashGet(base, NewSymbol(idx.Name))
		if err != nil || !ok {
			return NewNil(), false
		}
		return val, true
	default:
		return NewNil(), false
	}
}

func memberSetterValueExpectation(obj Value, property string) expressionExpectation {
	fn := memberSetterFunction(obj, property)
	if fn == nil {
		return expressionExpectation{}
	}
	return setterFunctionValueExpectation(fn)
}

func setterFunctionValueExpectation(fn *ScriptFunction) expressionExpectation {
	for _, param := range fn.Params {
		if param.Kind == ParamNormal {
			return positionalArgumentExpectation(param)
		}
	}
	return expressionExpectation{}
}

func memberSetterFunction(obj Value, property string) *ScriptFunction {
	setterName := property + "="
	switch obj.Kind() {
	case KindInstance:
		return valueInstance(obj).Class.Methods[setterName]
	case KindClass:
		return valueClass(obj).ClassMethods[setterName]
	default:
		return nil
	}
}

func assignmentLocalCallBypassNames(target, value Expression) map[string]struct{} {
	var names map[string]struct{}
	collectAssignmentLocalCallBypassNames(target, value, &names)
	return names
}

func assignmentLocalCallBypassBindings(target, value Expression, env *Env) map[string]*Env {
	names := assignmentLocalCallBypassNames(target, value)
	if len(names) == 0 {
		return nil
	}
	var bindings map[string]*Env
	for name := range names {
		scope, ok := env.lookupBindingScope(name)
		if !ok || scope.callRoot || scope.frozen {
			continue
		}
		if bindings == nil {
			bindings = make(map[string]*Env)
		}
		bindings[name] = scope
	}
	return bindings
}

func collectAssignmentLocalCallBypassNames(target, value Expression, names *map[string]struct{}) {
	switch t := target.(type) {
	case *Identifier:
		if expressionContainsBypassableIdentifierCall(value, t.Name) {
			if *names == nil {
				*names = make(map[string]struct{})
			}
			(*names)[t.Name] = struct{}{}
		}
	case *DestructureTarget:
		for _, element := range t.Elements {
			collectAssignmentLocalCallBypassNames(element.Target, value, names)
		}
	}
}

func expressionContainsBypassableIdentifierCall(expr Expression, name string) bool {
	switch t := expr.(type) {
	case nil, *Identifier, *IntegerLiteral, *FloatLiteral, *StringLiteral, *BoolLiteral, *NilLiteral, *SymbolLiteral, *IvarExpr, *ClassVarExpr:
		return false
	case *ArrayLiteral:
		for _, elem := range t.Elements {
			if expressionContainsBypassableIdentifierCall(elem, name) {
				return true
			}
		}
		return false
	case *HashLiteral:
		for _, pair := range t.Pairs {
			if expressionContainsBypassableIdentifierCall(pair.Key, name) ||
				expressionContainsBypassableIdentifierCall(pair.Value, name) {
				return true
			}
		}
		return false
	case *CallExpr:
		if ident, ok := t.Callee.(*Identifier); ok && callUsesBypassableIdentifierResolution(t) && ident.Name == name {
			return true
		}
		if expressionContainsBypassableIdentifierCall(t.Callee, name) {
			return true
		}
		for _, arg := range t.Args {
			if expressionContainsBypassableIdentifierCall(arg, name) {
				return true
			}
		}
		for _, kwarg := range t.KwArgs {
			if expressionContainsBypassableIdentifierCall(kwarg.Value, name) {
				return true
			}
		}
		return expressionContainsBypassableIdentifierCall(t.Block, name)
	case *MemberExpr:
		return expressionContainsBypassableIdentifierCall(t.Object, name)
	case *ScopeExpr:
		return expressionContainsBypassableIdentifierCall(t.Object, name)
	case *IndexExpr:
		if expressionContainsBypassableIdentifierCall(t.Object, name) {
			return true
		}
		for _, index := range t.Indices {
			if expressionContainsBypassableIdentifierCall(index, name) {
				return true
			}
		}
		return false
	case *DestructureTarget:
		for _, element := range t.Elements {
			if expressionContainsBypassableIdentifierCall(element.Target, name) {
				return true
			}
		}
		return false
	case *UnaryExpr:
		return expressionContainsBypassableIdentifierCall(t.Right, name)
	case *BinaryExpr:
		return expressionContainsBypassableIdentifierCall(t.Left, name) ||
			expressionContainsBypassableIdentifierCall(t.Right, name)
	case *ConditionalExpr:
		return expressionContainsBypassableIdentifierCall(t.Condition, name) ||
			expressionContainsBypassableIdentifierCall(t.Consequent, name) ||
			expressionContainsBypassableIdentifierCall(t.Alternate, name)
	case *RescueExpr:
		return expressionContainsBypassableIdentifierCall(t.Body, name) ||
			expressionContainsBypassableIdentifierCall(t.Fallback, name)
	case *IfExpr:
		if expressionContainsBypassableIdentifierCall(t.Condition, name) ||
			expressionContainsBypassableIdentifierCall(t.Consequent, name) {
			return true
		}
		for _, branch := range t.ElseIf {
			if expressionContainsBypassableIdentifierCall(branch.Condition, name) ||
				expressionContainsBypassableIdentifierCall(branch.Result, name) {
				return true
			}
		}
		return expressionContainsBypassableIdentifierCall(t.Alternate, name)
	case *RangeExpr:
		return expressionContainsBypassableIdentifierCall(t.Start, name) ||
			expressionContainsBypassableIdentifierCall(t.End, name)
	case *CaseExpr:
		if expressionContainsBypassableIdentifierCall(t.Target, name) {
			return true
		}
		for _, clause := range t.Clauses {
			for _, value := range clause.Values {
				if expressionContainsBypassableIdentifierCall(value.Expr, name) {
					return true
				}
			}
			if expressionContainsBypassableIdentifierCall(clause.Result, name) {
				return true
			}
		}
		return expressionContainsBypassableIdentifierCall(t.ElseExpr, name)
	case *BlockLiteral:
		if t == nil {
			return false
		}
		for _, param := range t.Params {
			if expressionContainsBypassableIdentifierCall(param.DefaultVal, name) {
				return true
			}
		}
		return statementsContainBypassableIdentifierCall(t.Body, name)
	case *YieldExpr:
		for _, arg := range t.Args {
			if expressionContainsBypassableIdentifierCall(arg, name) {
				return true
			}
		}
		return false
	case *InterpolatedString:
		return stringPartsContainBypassableIdentifierCall(t.Parts, name)
	case *InterpolatedSymbol:
		return stringPartsContainBypassableIdentifierCall(t.Parts, name)
	default:
		return false
	}
}

func callUsesBypassableIdentifierResolution(call *CallExpr) bool {
	return call.Parenthesized || len(call.Args) > 0 || len(call.KwArgs) > 0 || call.Block != nil
}

func stringPartsContainBypassableIdentifierCall(parts []StringPart, name string) bool {
	for _, part := range parts {
		if exprPart, ok := part.(StringExpr); ok && expressionContainsBypassableIdentifierCall(exprPart.Expr, name) {
			return true
		}
	}
	return false
}

func statementsContainBypassableIdentifierCall(statements []Statement, name string) bool {
	for _, stmt := range statements {
		if statementContainsBypassableIdentifierCall(stmt, name) {
			return true
		}
	}
	return false
}

func statementContainsBypassableIdentifierCall(stmt Statement, name string) bool {
	switch t := stmt.(type) {
	case nil:
		return false
	case *ExprStmt:
		return expressionContainsBypassableIdentifierCall(t.Expr, name)
	case *LogicalStmt:
		return statementContainsBypassableIdentifierCall(t.Left, name) ||
			statementContainsBypassableIdentifierCall(t.Right, name)
	case *ReturnStmt:
		return expressionContainsBypassableIdentifierCall(t.Value, name)
	case *RaiseStmt:
		return expressionContainsBypassableIdentifierCall(t.Value, name) ||
			expressionContainsBypassableIdentifierCall(t.Message, name)
	case *BreakStmt:
		return expressionContainsBypassableIdentifierCall(t.Value, name)
	case *NextStmt:
		return false
	case *AssignStmt:
		return expressionContainsBypassableIdentifierCall(t.Target, name) ||
			expressionContainsBypassableIdentifierCall(t.Value, name)
	case *IfStmt:
		if expressionContainsBypassableIdentifierCall(t.Condition, name) ||
			statementsContainBypassableIdentifierCall(t.Consequent, name) ||
			statementsContainBypassableIdentifierCall(t.Alternate, name) {
			return true
		}
		for _, branch := range t.ElseIf {
			if expressionContainsBypassableIdentifierCall(branch.Condition, name) ||
				statementsContainBypassableIdentifierCall(branch.Consequent, name) {
				return true
			}
		}
		return false
	case *ForStmt:
		return expressionContainsBypassableIdentifierCall(t.Target, name) ||
			expressionContainsBypassableIdentifierCall(t.Iterable, name) ||
			statementsContainBypassableIdentifierCall(t.Body, name)
	case *WhileStmt:
		return expressionContainsBypassableIdentifierCall(t.Condition, name) ||
			statementsContainBypassableIdentifierCall(t.Body, name)
	case *UntilStmt:
		return expressionContainsBypassableIdentifierCall(t.Condition, name) ||
			statementsContainBypassableIdentifierCall(t.Body, name)
	case *TryStmt:
		for i := range t.Rescues {
			if statementsContainBypassableIdentifierCall(t.Rescues[i].Body, name) {
				return true
			}
		}
		return statementsContainBypassableIdentifierCall(t.Body, name) ||
			statementsContainBypassableIdentifierCall(t.Else, name) ||
			statementsContainBypassableIdentifierCall(t.Ensure, name)
	case *ClassStmt:
		return false
	default:
		return false
	}
}

func (exec *Execution) evalStatements(stmts []Statement, env *Env) (Value, bool, error) {
	exec.pushEnv(env)
	defer exec.popEnv()

	result := NewNil()
	var lastPos Position
	for _, stmt := range stmts {
		lastPos = stmt.Pos()
		if err := exec.step(); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Pos())
		}
		val, returned, err := exec.evalStatementWithLocalBindings(stmt, env)
		if err != nil {
			return NewNil(), false, err
		}
		if _, isAssign := stmt.(*AssignStmt); isAssign {
			if err := exec.checkMemory(); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Pos())
			}
		} else {
			if err := exec.checkMemoryWith(val); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Pos())
			}
		}
		if returned {
			return val, true, nil
		}
		result = val
	}
	if err := exec.checkMemory(); err != nil {
		return NewNil(), false, exec.wrapError(err, lastPos)
	}
	return result, false, nil
}

func (exec *Execution) evalStatementWithLocalBindings(stmt Statement, env *Env) (Value, bool, error) {
	predeclareStatementLocalBindings(stmt, env)
	val, returned, err := exec.evalStatement(stmt, env)
	if err != nil {
		return val, returned, err
	}
	predeclareStatementPostLocalBindings(stmt, env)
	return val, returned, nil
}

func (exec *Execution) evalCompoundAssignment(stmt *AssignStmt, env *Env) (Value, error) {
	if stmt.Operator == tokenAndAssign || stmt.Operator == tokenOrAssign {
		return exec.evalLogicalAssignment(stmt, env)
	}
	target, err := exec.prepareCompoundAssignmentTarget(stmt.Target, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(target.current); err != nil {
		return NewNil(), err
	}

	right, err := exec.evalAssignmentValue(stmt, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(target.current, right); err != nil {
		return NewNil(), err
	}

	result, err := exec.evalBinaryOperator(stmt.Operator, target.current, right, stmt.Pos())
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	if err := target.assign(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) evalLogicalAssignment(stmt *AssignStmt, env *Env) (Value, error) {
	target, err := exec.prepareLogicalAssignmentTarget(stmt.Target, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(target.current); err != nil {
		return NewNil(), err
	}
	switch stmt.Operator {
	case tokenOrAssign:
		if target.current.Truthy() {
			return target.current, nil
		}
	case tokenAndAssign:
		if !target.current.Truthy() {
			return target.current, nil
		}
	default:
		return NewNil(), exec.errorAt(stmt.Pos(), "unsupported logical assignment operator")
	}

	right, err := exec.evalAssignmentValueWithExpectation(stmt, env, target.expectation)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(target.current, right); err != nil {
		return NewNil(), err
	}
	if err := target.assign(right); err != nil {
		return NewNil(), err
	}
	return right, nil
}

type compoundAssignmentTarget struct {
	current     Value
	assign      func(Value) error
	expectation expressionExpectation
}

func (exec *Execution) prepareLogicalAssignmentTarget(target Expression, env *Env) (compoundAssignmentTarget, error) {
	if ident, ok := target.(*Identifier); ok {
		assignLocal := func(value Value) error {
			env.Assign(ident.Name, value)
			return nil
		}
		assignClassConstant := func(value Value) error {
			return exec.assign(ident, value, env)
		}
		if current, exists := env.getOwn(ident.Name); exists {
			return compoundAssignmentTarget{current: current, assign: assignLocal}, nil
		}
		// Enclosing locals win only within the class-body boundary: a method
		// parameter or block local named LIMIT is the ||= target, but a
		// same-named local OUTSIDE the class body is not, so DEFAULT ||= x in
		// a class body creates the class constant (matching = and +=) instead
		// of clobbering a top-level DEFAULT.
		if env.hasCallLocalBinding(ident.Name) {
			current, exists := env.Get(ident.Name)
			if !exists {
				return compoundAssignmentTarget{}, exec.errorAt(ident.Pos(), "undefined variable %s", ident.Name)
			}
			return compoundAssignmentTarget{current: current, assign: assignLocal}, nil
		}
		if self, ok := classConstantAssignmentSelf(ident.Name, env); ok {
			current, _ := classConstant(self, ident.Name)
			return compoundAssignmentTarget{current: current, assign: assignClassConstant}, nil
		}
		if env.parent != nil && env.parent.hasEnclosingLocalBinding(ident.Name) {
			current, exists := env.Get(ident.Name)
			if !exists {
				return compoundAssignmentTarget{}, exec.errorAt(ident.Pos(), "undefined variable %s", ident.Name)
			}
			return compoundAssignmentTarget{current: current, assign: assignLocal}, nil
		}
		if env.parent != nil && env.parent.hasAmbientAssignmentBinding(ident.Name) {
			current, exists := env.Get(ident.Name)
			if !exists {
				return compoundAssignmentTarget{}, exec.errorAt(ident.Pos(), "undefined variable %s", ident.Name)
			}
			return compoundAssignmentTarget{current: current, assign: assignLocal}, nil
		}
		env.Define(ident.Name, NewNil())
		return compoundAssignmentTarget{current: NewNil(), assign: assignLocal}, nil
	}

	return exec.prepareCompoundAssignmentTarget(target, env)
}

func (exec *Execution) prepareCompoundAssignmentTarget(target Expression, env *Env) (compoundAssignmentTarget, error) {
	switch t := target.(type) {
	case *Identifier:
		current, err := exec.evalExpression(t, env)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		assign := func(value Value) error {
			return exec.assign(t, value, env)
		}
		return compoundAssignmentTarget{current: current, assign: assign}, nil
	case *MemberExpr:
		obj, err := exec.evalExpressionWithAuto(t.Object, env, true)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return compoundAssignmentTarget{}, err
		}
		member, err := exec.getPublicMember(obj, t.Property, t.Pos())
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		current, err := exec.autoInvokeIfNeeded(t, member, obj)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		assign := func(value Value) error {
			return exec.assignToEvaluatedMember(t, obj, value)
		}
		return compoundAssignmentTarget{
			current:     current,
			assign:      assign,
			expectation: memberSetterValueExpectation(obj, t.Property),
		}, nil
	case *IndexExpr:
		obj, err := exec.evalExpressionWithAuto(t.Object, env, true)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return compoundAssignmentTarget{}, err
		}
		indices, err := exec.evalIndexSelectors(t, obj, env)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		current, err := exec.evalIndexValue(t, obj, indices)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		assign := func(value Value) error {
			return exec.assignToEvaluatedIndex(t, obj, indices, value)
		}
		return compoundAssignmentTarget{current: current, assign: assign}, nil
	case *IvarExpr, *ClassVarExpr:
		current, err := exec.evalExpression(t, env)
		if err != nil {
			return compoundAssignmentTarget{}, err
		}
		assign := func(value Value) error {
			return exec.assign(t, value, env)
		}
		return compoundAssignmentTarget{current: current, assign: assign}, nil
	case *DestructureTarget:
		return compoundAssignmentTarget{}, exec.errorAt(t.Pos(), "compound assignment is not supported for destructuring targets")
	default:
		return compoundAssignmentTarget{}, exec.errorAt(target.Pos(), "invalid assignment target")
	}
}

func (exec *Execution) evalIfStatementExpression(stmt *IfStmt, env *Env) (Value, error) {
	return expressionValueOrReturn(exec.evalIfStatement(stmt, env))
}

func (exec *Execution) evalIfStatement(stmt *IfStmt, env *Env) (Value, bool, error) {
	if stmt.ModifierBodyFirst {
		predeclareLocalBindingsFromStatements(stmt.Consequent, env)
		predeclareLocalBindingsFromStatements(stmt.Alternate, env)
	}
	val, err := exec.evalExpression(stmt.Condition, env)
	if returnVal, ok := functionReturnValue(err); ok {
		return returnVal, true, nil
	}
	if err != nil {
		return NewNil(), false, err
	}
	if err := exec.checkMemoryWith(val); err != nil {
		return NewNil(), false, err
	}
	if val.Truthy() {
		if stmt.AlternateFirst {
			predeclareLocalBindingsFromStatements(stmt.Alternate, env)
		}
		return exec.evalStatements(stmt.Consequent, env)
	}
	if !stmt.AlternateFirst {
		predeclareLocalBindingsFromStatements(stmt.Consequent, env)
	}
	for _, clause := range stmt.ElseIf {
		condVal, err := exec.evalExpression(clause.Condition, env)
		if returnVal, ok := functionReturnValue(err); ok {
			return returnVal, true, nil
		}
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(condVal); err != nil {
			return NewNil(), false, err
		}
		if condVal.Truthy() {
			return exec.evalStatements(clause.Consequent, env)
		}
		predeclareLocalBindingsFromStatements(clause.Consequent, env)
	}
	if len(stmt.Alternate) > 0 {
		return exec.evalStatements(stmt.Alternate, env)
	}
	return NewNil(), false, nil
}

func (exec *Execution) evalStatement(stmt Statement, env *Env) (Value, bool, error) {
	switch s := stmt.(type) {
	case *ExprStmt:
		val, err := exec.evalExpression(s.Expr, env)
		if returnVal, ok := functionReturnValue(err); ok {
			return returnVal, true, nil
		}
		return val, false, err
	case *LogicalStmt:
		return exec.evalLogicalStatement(s, env)
	case *ReturnStmt:
		if s.Value == nil {
			return NewNil(), true, nil
		}
		val, err := exec.evalExpression(s.Value, env)
		if returnVal, ok := functionReturnValue(err); ok {
			return returnVal, true, nil
		}
		return val, true, err
	case *RaiseStmt:
		return exec.evalRaiseStatement(s, env)
	case *AssignStmt:
		if s.Operator != "" {
			val, err := exec.evalCompoundAssignment(s, env)
			if returnVal, ok := functionReturnValue(err); ok {
				return returnVal, true, nil
			}
			return val, false, err
		}
		if val, handled, err := exec.evalArrayAppendAssignment(s, env); handled || err != nil {
			if returnVal, ok := functionReturnValue(err); ok {
				return returnVal, true, nil
			}
			return val, false, err
		}
		if val, handled, err := exec.evalMemberAssignment(s, env); handled || err != nil {
			if returnVal, ok := functionReturnValue(err); ok {
				return returnVal, true, nil
			}
			return val, false, err
		}
		val, err := exec.evalAssignmentValue(s, env)
		if returnVal, ok := functionReturnValue(err); ok {
			return returnVal, true, nil
		}
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), false, err
		}
		if err := exec.assign(s.Target, val, env); err != nil {
			if errors.Is(err, errStepQuotaExceeded) || errors.Is(err, errMemoryQuotaExceeded) {
				return NewNil(), false, err
			}
			return NewNil(), false, exec.wrapError(err, s.Pos())
		}
		return val, false, nil
	case *IfStmt:
		return exec.evalIfStatement(s, env)
	case *ForStmt:
		return exec.evalForStatement(s, env)
	case *WhileStmt:
		return exec.evalWhileStatement(s, env)
	case *UntilStmt:
		return exec.evalUntilStatement(s, env)
	case *BreakStmt:
		if exec.loopDepth == 0 {
			return NewNil(), false, exec.errorAt(s.Pos(), "break used outside of loop")
		}
		if s.Value != nil {
			val, err := exec.evalExpression(s.Value, env)
			if returnVal, ok := functionReturnValue(err); ok {
				return returnVal, true, nil
			}
			if err != nil {
				return NewNil(), false, err
			}
			if err := exec.checkMemoryWith(val); err != nil {
				return NewNil(), false, err
			}
			return NewNil(), false, newLoopBreakValue(val)
		}
		return NewNil(), false, errLoopBreak
	case *NextStmt:
		if exec.loopDepth == 0 && exec.blockDepth == 0 {
			return NewNil(), false, exec.errorAt(s.Pos(), "next used outside of loop")
		}
		if s.Value != nil {
			val, err := exec.evalExpression(s.Value, env)
			if returnVal, ok := functionReturnValue(err); ok {
				return returnVal, true, nil
			}
			if err != nil {
				return NewNil(), false, err
			}
			if err := exec.checkMemoryWith(val); err != nil {
				return NewNil(), false, err
			}
			return NewNil(), false, newLoopNextValue(val)
		}
		return NewNil(), false, errLoopNext
	case *RetryStmt:
		if exec.rescueDepth == 0 {
			return NewNil(), false, exec.errorAt(s.Pos(), "retry used outside of rescue")
		}
		return NewNil(), false, errRescueRetry
	case *TryStmt:
		return exec.evalTryStatement(s, env)
	case *ClassStmt:
		classVal, ok := env.Get(s.Name)
		if !ok {
			return NewNil(), false, exec.errorAt(s.Pos(), "class %s is not bound", s.Name)
		}
		classDef := valueClass(classVal)
		if classDef == nil {
			return NewNil(), false, exec.errorAt(s.Pos(), "%s is not a class", s.Name)
		}
		if err := exec.initializeClassBody(classVal, classDef, env); err != nil {
			return NewNil(), false, err
		}
		return classVal, false, nil
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "unsupported statement")
	}
}

func (exec *Execution) evalAssignmentValue(stmt *AssignStmt, env *Env) (Value, error) {
	return exec.evalAssignmentValueWithExpectation(stmt, env, expressionExpectation{})
}

func (exec *Execution) evalAssignmentValueWithExpectation(stmt *AssignStmt, env *Env, expectation expressionExpectation) (Value, error) {
	bindings := assignmentLocalCallBypassBindings(stmt.Target, stmt.Value, env)
	if len(bindings) == 0 {
		return exec.evalExpressionWithExpectation(stmt.Value, env, expectation)
	}
	exec.localCallBypassStack = append(exec.localCallBypassStack, localCallBypass{bindings: bindings})
	defer func() {
		exec.localCallBypassStack = exec.localCallBypassStack[:len(exec.localCallBypassStack)-1]
	}()
	return exec.evalExpressionWithExpectation(stmt.Value, env, expectation)
}

func (exec *Execution) evalLogicalStatement(stmt *LogicalStmt, env *Env) (Value, bool, error) {
	left, returned, err := exec.evalStatementWithLocalBindings(stmt.Left, env)
	if err != nil || returned {
		return left, returned, err
	}
	if err := exec.checkMemoryWith(left); err != nil {
		return NewNil(), false, exec.wrapError(err, stmt.Left.Pos())
	}
	switch stmt.Operator {
	case tokenWordAnd:
		if !left.Truthy() {
			return left, false, nil
		}
	case tokenWordOr:
		if left.Truthy() {
			return left, false, nil
		}
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "unsupported statement operator")
	}
	return exec.evalStatementWithLocalBindings(stmt.Right, env)
}

func (exec *Execution) evalRaiseStatement(stmt *RaiseStmt, env *Env) (Value, bool, error) {
	if stmt.Value != nil {
		if stmt.Message != nil {
			errorType, staticErrorType := raiseErrorTypeName(stmt.Value, env)
			val := NewNil()
			if !staticErrorType {
				var err error
				val, err = exec.evalExpression(stmt.Value, env)
				if err != nil {
					return NewNil(), false, err
				}
			}
			message, err := exec.evalExpression(stmt.Message, env)
			if err != nil {
				return NewNil(), false, err
			}
			if message.Kind() != KindString {
				return NewNil(), false, exec.newRuntimeErrorWithType(runtimeErrorTypeType, "exception message must be string", stmt.Pos())
			}
			if !staticErrorType {
				var ok bool
				errorType, ok = raiseErrorType(val)
				if !ok {
					return NewNil(), false, exec.newRuntimeErrorWithType(runtimeErrorTypeType, "exception class/object expected", stmt.Pos())
				}
			}
			return NewNil(), false, exec.newRuntimeErrorWithType(errorType, message.String(), stmt.Pos())
		}

		val, err := exec.evalExpression(stmt.Value, env)
		if returnVal, ok := functionReturnValue(err); ok {
			return returnVal, true, nil
		}
		if err != nil {
			return NewNil(), false, err
		}
		if val.Kind() != KindString {
			message := "exception class/object expected"
			if val.Kind() == KindNil {
				message = "exception object expected"
			}
			return NewNil(), false, exec.newRuntimeErrorWithType(runtimeErrorTypeType, message, stmt.Pos())
		}
		return NewNil(), false, exec.errorAt(stmt.Pos(), "%s", val.String())
	}

	err := exec.currentRescuedError()
	if err == nil {
		return NewNil(), false, exec.errorAt(stmt.Pos(), "")
	}
	return NewNil(), false, err
}

func raiseErrorType(val Value) (string, bool) {
	if val.Kind() == KindClass {
		if class := valueClass(val); class != nil {
			if kind, ok := ast.CanonicalRuntimeErrorType(class.Name); ok {
				return kind, true
			}
		}
	}
	return "", false
}

func raiseErrorTypeName(expr Expression, env *Env) (string, bool) {
	ident, ok := expr.(*Identifier)
	if !ok || !isConstantIdentifier(ident.Name) {
		return "", false
	}
	if _, ok := env.Get(ident.Name); ok {
		return "", false
	}
	if self, ok := env.Get("self"); ok && (self.Kind() == KindInstance || self.Kind() == KindClass) {
		if _, ok := classConstant(self, ident.Name); ok {
			return "", false
		}
	}
	return ast.CanonicalRuntimeErrorType(ident.Name)
}

func (exec *Execution) evalTryExpression(stmt *TryStmt, env *Env) (Value, error) {
	return expressionValueOrReturn(exec.evalTryStatement(stmt, env))
}

func (exec *Execution) evalTryStatement(stmt *TryStmt, env *Env) (Value, bool, error) {
	for {
		val, returned, err := exec.evalStatements(stmt.Body, env)
		runElse := err == nil && !returned
		predeclareLocalBindingsFromStatements(stmt.Body, env)

		retried := false
		if err != nil {
			// Clauses are selected in source order: the first whose type matches
			// wins, mirroring Ruby's specific-to-general rescue dispatch, and later
			// clauses never see an error an earlier clause matched. A selected
			// clause with an empty body keeps the prior single-clause behavior —
			// the error propagates (after ensure) rather than being swallowed —
			// but it still consumes the match.
			for i := range stmt.Rescues {
				clause := &stmt.Rescues[i]
				if !canRescueRuntimeError(err, clause.Ty) {
					// A skipped clause's body locals must exist (as nil) before a
					// later handler runs: the parser treated its assignments as
					// surrounding-scope locals, so a matching clause reading such a
					// name sees the same nil it would after the block.
					predeclareRescueClauseLocalBindings(clause, env)
					continue
				}
				if len(clause.Body) == 0 {
					break
				}
				rescueEnv := env
				if clause.Binding != "" {
					rescueEnv = newEnv(env)
					rescueEnv.Define(clause.Binding, rescuedErrorValue(err))
				}
				exec.pushRescuedError(err)
				exec.rescueDepth++
				rescueVal, rescueReturned, rescueErr := exec.evalStatements(clause.Body, rescueEnv)
				exec.rescueDepth--
				exec.popRescuedError()
				if rescueEnv != env {
					copyRescueLocalAssignments(clause, rescueEnv, env)
				}
				if isRescueRetrySignal(rescueErr) {
					retried = true
					break
				}
				if rescueErr != nil {
					val = NewNil()
					returned = false
					err = rescueErr
				} else {
					val = rescueVal
					returned = rescueReturned
					err = nil
				}
				break
			}
		}
		if retried {
			// A retry re-runs the begin body without running ensure; charge a
			// step per attempt so a retry storm hits the step quota instead of
			// spinning forever.
			if stepErr := exec.step(); stepErr != nil {
				return NewNil(), false, exec.wrapError(stepErr, stmt.Pos())
			}
			continue
		}
		predeclareRescueLocalBindings(stmt, env)

		if runElse && len(stmt.Else) > 0 {
			val, returned, err = exec.evalStatements(stmt.Else, env)
		}
		predeclareLocalBindingsFromStatements(stmt.Else, env)

		if len(stmt.Ensure) > 0 {
			ensureVal, ensureReturned, ensureErr := exec.evalStatements(stmt.Ensure, env)
			if ensureErr != nil {
				return NewNil(), false, ensureErr
			}
			if ensureReturned {
				return ensureVal, true, nil
			}
		}

		if err != nil {
			return NewNil(), false, err
		}
		return val, returned, nil
	}
}

func copyRescueLocalAssignments(clause *RescueClause, from, to *Env) {
	var collector localBindingCollector
	collectLocalBindingNames(clause.Body, &collector)
	for _, name := range collector.names {
		if name == clause.Binding {
			continue
		}
		val, ok := from.getOwn(name)
		if !ok {
			continue
		}
		to.PredeclareLocal(name)
		to.Assign(name, val)
	}
}

func predeclareRescueLocalBindings(stmt *TryStmt, env *Env) {
	for i := range stmt.Rescues {
		predeclareRescueClauseLocalBindings(&stmt.Rescues[i], env)
	}
}

func predeclareRescueClauseLocalBindings(clause *RescueClause, env *Env) {
	var collector localBindingCollector
	collectLocalBindingNames(clause.Body, &collector)
	for _, name := range collector.names {
		if name == clause.Binding {
			continue
		}
		env.PredeclareLocal(name)
	}
}

func rescuedErrorValue(err error) Value {
	errType := classifyRuntimeErrorType(err)
	message := err.Error()
	codeFrame := ""
	var backtrace []Value

	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		errType = classifyRuntimeErrorType(runtimeErr)
		message = runtimeErr.Message
		codeFrame = runtimeErr.CodeFrame
		backtrace = runtimeErrorBacktrace(runtimeErr)
	}

	fields := map[string]Value{
		"type":       NewString(errType),
		"class":      NewString(errType),
		"message":    NewString(message),
		"to_s":       NewString(message),
		"code_frame": NewString(codeFrame),
		"backtrace":  NewArray(backtrace),
	}
	return NewObject(fields)
}

func runtimeErrorBacktrace(err *RuntimeError) []Value {
	if err == nil || len(err.Frames) == 0 {
		return nil
	}
	frames := make([]Value, 0, len(err.Frames))
	for _, frame := range err.Frames {
		frames = append(frames, NewString(formatRuntimeBacktraceFrame(frame)))
	}
	return frames
}

func formatRuntimeBacktraceFrame(frame StackFrame) string {
	location := ""
	switch {
	case frame.Source != "" && frame.Pos.Line > 0 && frame.Pos.Column > 0:
		location = fmt.Sprintf("%s:%d:%d", frame.Source, frame.Pos.Line, frame.Pos.Column)
	case frame.Source != "" && frame.Pos.Line > 0:
		location = fmt.Sprintf("%s:%d", frame.Source, frame.Pos.Line)
	case frame.Source != "":
		location = frame.Source
	case frame.Pos.Line > 0 && frame.Pos.Column > 0:
		location = fmt.Sprintf("%d:%d", frame.Pos.Line, frame.Pos.Column)
	case frame.Pos.Line > 0:
		location = fmt.Sprintf("line %d", frame.Pos.Line)
	default:
		location = "<script>"
	}
	return fmt.Sprintf("%s:in `%s`", location, frame.Function)
}

func isLoopControlSignal(err error) bool {
	return errors.Is(err, errLoopBreak) || errors.Is(err, errLoopNext)
}

func isRescueRetrySignal(err error) bool {
	return errors.Is(err, errRescueRetry)
}

func isFunctionReturnSignal(err error) bool {
	_, ok := functionReturnValue(err)
	return ok
}

func isHostControlSignal(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func canRescueRuntimeError(err error, rescueTy *TypeExpr) bool {
	return !isLoopControlSignal(err) &&
		!isRescueRetrySignal(err) &&
		!isHostControlSignal(err) &&
		!isNonLocalReturnSignal(err) &&
		!isFunctionReturnSignal(err) &&
		runtimeErrorMatchesRescueType(err, rescueTy)
}

func runtimeErrorMatchesRescueType(err error, rescueTy *TypeExpr) bool {
	if rescueTy == nil {
		var runtimeErr *RuntimeError
		return errors.As(err, &runtimeErr) && classifyRuntimeErrorType(runtimeErr) != runtimeErrorTypeLimit
	}
	errKind := classifyRuntimeErrorType(err)
	return rescueTypeMatchesErrorKind(rescueTy, errKind)
}

func rescueTypeMatchesErrorKind(ty *TypeExpr, errKind string) bool {
	if ty == nil {
		return false
	}
	if ty.Kind == TypeUnion {
		for _, option := range ty.Union {
			if rescueTypeMatchesErrorKind(option, errKind) {
				return true
			}
		}
		return false
	}
	canonical, ok := ast.CanonicalRuntimeErrorType(ty.Name)
	if !ok {
		return false
	}
	if canonical == runtimeErrorTypeBase {
		return true
	}
	if canonical == runtimeErrorTypeStandard {
		return errKind != runtimeErrorTypeLimit
	}
	return canonical == errKind
}
