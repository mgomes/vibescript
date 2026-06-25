package runtime

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/mgomes/vibescript/internal/ast"
)

const (
	minInt64Float          = -9223372036854775808.0
	maxInt64FloatExclusive = 9223372036854775808.0
)

func (exec *Execution) evalExpression(expr Expression, env *Env) (Value, error) {
	return exec.evalExpressionWithAuto(expr, env, true)
}

func (exec *Execution) evalExpressionWithAuto(expr Expression, env *Env, autoCall bool) (Value, error) {
	if err := exec.step(); err != nil {
		return NewNil(), err
	}
	switch e := expr.(type) {
	case *Identifier:
		val, ok := env.Get(e.Name)
		if !ok {
			// allow implicit self method lookup
			if self, hasSelf := env.Get("self"); hasSelf && (self.Kind() == KindInstance || self.Kind() == KindClass) {
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
	case *InterpolatedString:
		return exec.evalInterpolatedStringLiteral(e, env)
	case *BoolLiteral:
		return NewBool(e.Value), nil
	case *NilLiteral:
		return NewNil(), nil
	case *SymbolLiteral:
		return NewSymbol(e.Name), nil
	case *ArrayLiteral:
		elems := make([]Value, len(e.Elements))
		for i, el := range e.Elements {
			val, err := exec.evalExpressionWithAuto(el, env, true)
			if err != nil {
				return NewNil(), err
			}
			elems[i] = val
		}
		return NewArray(elems), nil
	case *HashLiteral:
		entries := make(map[string]Value, len(e.Pairs))
		for _, pair := range e.Pairs {
			keyVal, err := exec.evalExpressionWithAuto(pair.Key, env, true)
			if err != nil {
				return NewNil(), err
			}
			key, err := valueToHashKey(keyVal)
			if err != nil {
				return NewNil(), exec.errorAt(pair.Key.Pos(), "%s", err.Error())
			}
			val, err := exec.evalExpressionWithAuto(pair.Value, env, true)
			if err != nil {
				return NewNil(), err
			}
			entries[key] = val
		}
		return NewHash(entries), nil
	case *UnaryExpr:
		return exec.evalUnaryExpr(e, env)
	case *BinaryExpr:
		return exec.evalBinaryExpr(e, env)
	case *ConditionalExpr:
		return exec.evalConditionalExpr(e, env)
	case *IfExpr:
		return exec.evalIfExpr(e, env)
	case *RangeExpr:
		return exec.evalRangeExpr(e, env)
	case *CaseExpr:
		return exec.evalCaseExpr(e, env)
	case *MemberExpr:
		obj, err := exec.evalExpressionWithAuto(e.Object, env, true)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return NewNil(), err
		}
		member, err := exec.getMember(obj, e.Property, e.Pos())
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
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported expression")
	}
}

func (exec *Execution) evalInterpolatedStringLiteral(lit *InterpolatedString, env *Env) (Value, error) {
	var sb strings.Builder
	for _, part := range lit.Parts {
		switch p := part.(type) {
		case StringText:
			if err := exec.appendInterpolatedChunk(&sb, p.Text); err != nil {
				return NewNil(), err
			}
		case StringExpr:
			val, err := exec.evalExpressionWithAuto(p.Expr, env, true)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.appendInterpolatedValue(&sb, val); err != nil {
				return NewNil(), err
			}
		}
	}
	return NewString(sb.String()), nil
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
	idx, err := exec.evalExpressionWithAuto(e.Index, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(idx); err != nil {
		return NewNil(), err
	}
	return exec.evalIndexValue(e, obj, idx)
}

func (exec *Execution) evalIndexValue(e *IndexExpr, obj, idx Value) (Value, error) {
	switch obj.Kind() {
	case KindString:
		i, err := valueToInt(idx)
		if err != nil {
			return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
		}
		runes := []rune(obj.String())
		if i < 0 || i >= len(runes) {
			return NewNil(), exec.errorAt(e.Index.Pos(), "string index out of bounds")
		}
		return NewString(string(runes[i])), nil
	case KindArray:
		i, err := valueToInt(idx)
		if err != nil {
			return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
		}
		arr := obj.Array()
		if i < 0 || i >= len(arr) {
			return NewNil(), exec.errorAt(e.Index.Pos(), "array index out of bounds")
		}
		return arr[i], nil
	case KindHash, KindObject:
		key, err := valueToHashKey(idx)
		if err != nil {
			return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
		}
		val, ok := obj.Hash()[key]
		if ok {
			return val, nil
		}
		// A missing key consults the hash's Ruby-style default. Only KindHash
		// carries default metadata (objects never do), so a missing object key
		// stays nil. A default proc takes precedence over a default value and is
		// invoked with (hash, key); the key keeps its original symbol/string
		// value so the proc can render it the way Ruby does.
		if obj.Kind() == KindHash {
			return exec.hashMissingKeyDefault(obj, idx, e.Index.Pos())
		}
		return NewNil(), nil
	default:
		return NewNil(), exec.errorAt(e.Object.Pos(), "cannot index %s", obj.Kind())
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
	case tokenAnd:
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
	case tokenOr:
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
		result, err = moduloValues(left, right)
	case tokenEQ:
		return NewBool(left.Equal(right)), nil
	case tokenCaseEQ:
		// Ruby's case equality operator: the left operand acts as the matcher and
		// the right operand is the value being tested. Ranges check membership;
		// every other value falls back to `==`. This mirrors `when` clause
		// matching, where the clause value is the matcher.
		return NewBool(caseCandidateMatches(right, left)), nil
	case tokenNotEQ:
		return NewBool(!left.Equal(right)), nil
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

func (exec *Execution) evalConditionalExpr(expr *ConditionalExpr, env *Env) (Value, error) {
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
	result, err := exec.evalExpressionWithAuto(branch, env, true)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) evalIfExpr(expr *IfExpr, env *Env) (Value, error) {
	resultExpr, err := exec.matchIfExpressionBranch(expr, env)
	if err != nil {
		return NewNil(), err
	}
	if resultExpr == nil {
		return NewNil(), nil
	}

	result, err := exec.evalExpressionWithAuto(resultExpr, env, true)
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
	blockValue := NewBlock(block.Params, block.Body, env)
	blk := valueBlock(blockValue)
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
	exec   *Execution
	blk    *Block
	env    *Env
	charge *blockBindCharge
}

// newBlockCallRunner builds a runner for repeatedly invoking a block from an
// iterator. receiver, callArgs, and kwargs are the iterator's call roots: they seed
// the per-call bind charge that bounds the fresh backing a rest-collecting
// destructure parameter (|(k, *tail)|) allocates, so those roots -- live only on the
// Go stack during the loop -- are counted alongside that backing. callArgs are the
// iterator's POSITIONAL roots (the other hashes a block-driven hash.merge holds, for
// example); pass nil for the pure iterators that reject positional arguments and so
// hold only the receiver.
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
		runner.env = newEnv(blk.Env)
	}
	return runner, nil
}

func (runner *blockCallRunner) call(args []Value) (Value, error) {
	env := runner.env
	if env == nil {
		env = newEnv(runner.blk.Env)
	} else {
		env.resetForBlockCall(runner.blk.Env)
	}
	return runner.exec.callBlock(runner.blk, args, env, runner.charge)
}

// wantsCollapsedPair reports whether a hash iterator should yield each entry as a
// single two-element [key, value] pair instead of two separate arguments. It
// mirrors Ruby, where a block declaring exactly one positional parameter receives
// the pair while a block with two or more positional parameters auto-splats into
// key and value. A lone destructuring parameter such as |(k, v)| still counts as
// one parameter, so it receives the pair and unpacks it. Any rest or keyword
// parameter opts out so the iterator keeps yielding key and value separately.
func (runner *blockCallRunner) wantsCollapsedPair() bool {
	positional := 0
	for i := range runner.blk.Params {
		switch runner.blk.Params[i].Kind {
		case ParamNormal:
			positional++
		default:
			return false
		}
	}
	return positional == 1
}

// CallBlock invokes a block value with the provided arguments.
// This is the public entry point for capability adapters that need to
// call user-supplied blocks (e.g. db.each, db.tx).
func (exec *Execution) CallBlock(block Value, args []Value) (Value, error) {
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
	return exec.callBlock(blk, args, newEnv(blk.Env), charge)
}

func (exec *Execution) callBlock(blk *Block, args []Value, blockEnv *Env, charge *blockBindCharge) (Value, error) {
	exec.pushModuleContext(moduleContext{
		key:    blk.moduleKey,
		path:   blk.modulePath,
		root:   blk.moduleRoot,
		script: blk.owner,
	})
	defer exec.popModuleContext()

	charge.begin(args)
	for i, param := range blk.Params {
		var val Value
		if i < len(args) {
			val = args[i]
		} else {
			val = NewNil()
		}
		if param.Type != nil {
			normalized, err := normalizeValueForType(val, param.Type, typeContext{
				owner:    blk.owner,
				env:      blk.Env,
				fallback: exec.root,
			})
			if err != nil {
				return NewNil(), exec.errorAt(param.Type.Position, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
			val = normalized
		}
		if param.Target != nil {
			if err := exec.bindBlockParamTarget(blockEnv, param.Target, val, charge); err != nil {
				return NewNil(), err
			}
			continue
		}
		blockEnv.Define(param.Name, val)
	}
	val, returned, err := exec.evalStatements(blk.Body, blockEnv)
	if err != nil {
		return NewNil(), err
	}
	if returned {
		return blockEnv.detachArrayAppendResult(val), nil
	}
	return blockEnv.detachArrayAppendResult(val), nil
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
		return expressionCapturesCurrentEnv(s.Value)
	case *AssignStmt:
		return expressionCapturesCurrentEnv(s.Target) || expressionCapturesCurrentEnv(s.Value)
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
		return expressionCapturesCurrentEnv(s.Iterable) || statementsCaptureCurrentEnv(s.Body)
	case *WhileStmt:
		return expressionCapturesCurrentEnv(s.Condition) || statementsCaptureCurrentEnv(s.Body)
	case *UntilStmt:
		return expressionCapturesCurrentEnv(s.Condition) || statementsCaptureCurrentEnv(s.Body)
	case *BreakStmt, *NextStmt, *EnumStmt:
		return false
	case *TryStmt:
		return statementsCaptureCurrentEnv(s.Body) ||
			statementsCaptureCurrentEnv(s.Rescue) ||
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
		return expressionCapturesCurrentEnv(e.Object) || expressionCapturesCurrentEnv(e.Index)
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
				if expressionCapturesCurrentEnv(value) {
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
		for _, part := range e.Parts {
			stringExpr, ok := part.(StringExpr)
			if ok && expressionCapturesCurrentEnv(stringExpr.Expr) {
				return true
			}
		}
		return false
	default:
		return true
	}
}

func (exec *Execution) bindBlockParamTarget(env *Env, target Expression, value Value, charge *blockBindCharge) error {
	switch t := target.(type) {
	case *Identifier:
		env.Define(t.Name, value)
		// Charge the bound leaf so a fresh rest backing a destructure collected
		// (the only binding that allocates beyond the call roots) counts toward the
		// quota even when the block body is empty. Pass-through bindings dedup
		// against the seeded arguments and charge essentially nothing.
		return charge.charge(value)
	case *DestructureTarget:
		return AssignDestructure(t, value, func(target Expression, value Value) error {
			return exec.bindBlockParamTarget(env, target, value, charge)
		})
	default:
		return exec.errorAt(target.Pos(), "invalid block parameter target")
	}
}

func (exec *Execution) evalYield(expr *YieldExpr, env *Env) (Value, error) {
	block, ok := env.Get("__block__")
	if !ok || block.Kind() == KindNil {
		return NewNil(), exec.errorAt(expr.Pos(), "no block given")
	}
	args := make([]Value, 0, len(expr.Args))
	for _, arg := range expr.Args {
		val, err := exec.evalExpression(arg, env)
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
	return exec.CallBlock(block, args)
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
		if fn.Private && !exec.isCurrentReceiver(obj) {
			return exec.errorAt(pos, "private method %s", setterName)
		}
		_, err := exec.callFunction(fn, obj, []Value{value}, nil, NewNil(), pos)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return exec.errorAt(pos, "break cannot cross call boundary")
			}
			if errors.Is(err, errLoopNext) {
				return exec.errorAt(pos, "next cannot cross call boundary")
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
		env.Assign(t.Name, value)
		return nil
	case *DestructureTarget:
		return AssignDestructure(t, value, func(target Expression, value Value) error {
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
		idx, err := exec.evalExpression(t.Index, env)
		if err != nil {
			return err
		}
		if err := exec.checkMemoryWith(idx); err != nil {
			return err
		}
		return exec.assignToEvaluatedIndex(t, obj, idx, value)
	default:
		return exec.errorAt(target.Pos(), "invalid assignment target")
	}
}

func (exec *Execution) assignToEvaluatedMember(target *MemberExpr, obj, value Value) error {
	switch obj.Kind() {
	case KindHash, KindObject:
		obj.Hash()[target.Property] = value
		return nil
	case KindInstance, KindClass:
		return exec.assignToMember(obj, target.Property, value, target.Pos())
	default:
		return exec.errorAt(target.Pos(), "cannot assign to %s", obj.Kind())
	}
}

func (exec *Execution) assignToEvaluatedIndex(target *IndexExpr, obj, idx, value Value) error {
	switch obj.Kind() {
	case KindArray:
		arr := obj.Array()
		i, err := valueToInt(idx)
		if err != nil {
			return exec.errorAt(target.Index.Pos(), "%s", err.Error())
		}
		if i < 0 || i >= len(arr) {
			return exec.errorAt(target.Index.Pos(), "array index out of bounds")
		}
		arr[i] = value
		return nil
	case KindHash, KindObject:
		key, err := valueToHashKey(idx)
		if err != nil {
			return exec.errorAt(target.Index.Pos(), "%s", err.Error())
		}
		obj.Hash()[key] = value
		return nil
	default:
		return exec.errorAt(target.Object.Pos(), "cannot index %s", obj.Kind())
	}
}

// AssignDestructure applies Vibescript's destructuring assignment rules and
// invokes assign for each concrete leaf target.
func AssignDestructure(target *DestructureTarget, value Value, assign func(Expression, Value) error) error {
	values := destructureValues(value)
	restIndex := -1
	for i, element := range target.Elements {
		if element.Rest {
			restIndex = i
			break
		}
	}

	if restIndex == -1 {
		for i, element := range target.Elements {
			if err := assignDestructureValue(element.Target, valueAt(values, i), assign); err != nil {
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
	restEnd := len(values) - trailing
	if restEnd < restStart {
		restEnd = restStart
	}
	// Allocate the rest backing with capacity exactly equal to the collected
	// element count. append([]Value(nil), src...) (and slices.Clone, which wraps
	// it) would let Go's growslice round the capacity up past len, so the memory
	// estimator -- which charges slice backings by cap -- would see a larger array
	// than the value's own length implies. A make+copy keeps cap == len so the
	// rest array's charged footprint matches what its element count predicts.
	restSrc := values[restStart:restEnd]
	restValues := make([]Value, len(restSrc))
	copy(restValues, restSrc)
	for i, element := range target.Elements {
		var val Value
		switch {
		case i < restIndex:
			val = valueAt(values, i)
		case i == restIndex:
			val = NewArray(restValues)
		default:
			valueIndex := len(values) - trailing + i - restIndex - 1
			if valueIndex < restIndex {
				valueIndex = -1
			}
			val = valueAt(values, valueIndex)
		}
		if err := assignDestructureValue(element.Target, val, assign); err != nil {
			return err
		}
	}
	return nil
}

func assignDestructureValue(target Expression, value Value, assign func(Expression, Value) error) error {
	if nested, ok := target.(*DestructureTarget); ok {
		return AssignDestructure(nested, value, assign)
	}
	return assign(target, value)
}

func destructureValues(value Value) []Value {
	if value.Kind() == KindArray {
		return value.Array()
	}
	return []Value{value}
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
		if value.Operator != tokenPlus {
			return NewNil(), false, nil
		}
		left, ok := value.Left.(*Identifier)
		if !ok || left.Name != target.Name {
			return NewNil(), false, nil
		}
		right, ok := value.Right.(*ArrayLiteral)
		if !ok {
			return NewNil(), false, nil
		}
		return exec.evalArrayConcatAppendAssignment(target.Name, value, right, env)
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
			candidate, err := exec.evalExpression(candidateExpr, env)
			if err != nil {
				return NewNil(), err
			}
			if err := exec.checkMemoryWith(candidate); err != nil {
				return NewNil(), err
			}
			if caseWhenMatches(hasTarget, target, candidate) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		result, err := exec.evalExpressionWithAuto(clause.Result, env, true)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(result); err != nil {
			return NewNil(), err
		}
		return result, nil
	}

	if expr.ElseExpr != nil {
		result, err := exec.evalExpressionWithAuto(expr.ElseExpr, env, true)
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

func caseWhenMatches(hasTarget bool, target, candidate Value) bool {
	if !hasTarget {
		return candidate.Truthy()
	}
	return caseCandidateMatches(target, candidate)
}

func caseCandidateMatches(target, candidate Value) bool {
	if candidate.Kind() != KindRange {
		return target.Equal(candidate)
	}

	switch target.Kind() {
	case KindInt:
		return rangeContainsInt(candidate.Range(), target.Int())
	case KindFloat:
		return rangeContainsFloat(candidate.Range(), target.Float())
	default:
		return target.Equal(candidate)
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

func (exec *Execution) evalForStatement(stmt *ForStmt, env *Env) (Value, bool, error) {
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
	last := NewNil()

	switch iterable.Kind() {
	case KindArray:
		arr := iterable.Array()
		for _, item := range arr {
			if err := exec.step(); err != nil {
				return NewNil(), false, exec.wrapError(err, stmt.Pos())
			}
			env.Assign(stmt.Iterator, item)
			val, returned, err := exec.evalStatements(stmt.Body, env)
			if err != nil {
				if errors.Is(err, errLoopBreak) {
					return last, false, nil
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
		val, returned, err := exec.evalForHash(stmt, env, iterable, last)
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
				env.Assign(stmt.Iterator, NewInt(i))
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					if errors.Is(err, errLoopBreak) {
						return last, false, nil
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
				env.Assign(stmt.Iterator, NewInt(i))
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					if errors.Is(err, errLoopBreak) {
						return last, false, nil
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

	return last, false, nil
}

// evalForHash runs a `for` loop over a hash, mirroring Ruby's `for` over a hash,
// which iterates `each` and yields a two-element [key, value] pair. The returned
// bool reports whether the body returned (propagating an explicit `return`), and
// last seeds the loop's running value so the value of an empty loop matches the
// enclosing statement's last value.
//
// Like hash.each, the loop builds no output map but materializes a sorted key
// list to walk entries deterministically. The scratch slice is reserved against
// the memory quota for the loop's entire lifetime via reserveLoopScratch, so it
// is counted by every memory check inside the body -- not just preflighted once
// before the loop. Without that reservation a body that allocates near the quota
// could pass its own checks while the true peak (roots + scratch + body
// allocation) exceeded the quota by the scratch size. The reservation is released
// on every exit path through defer.
//
// The per-iteration [key, value] pair is reserved the same way: one constant pair
// (collapsedPairBytes) is folded into the baseline for the loop's lifetime. The pair
// the loop binds to the iterator stays in env (already counted by the env walk), and
// the next pair overlaps it only for the instant before the assignment overwrites it,
// so reserving one pair conservatively bounds the transient. Reserving it -- rather
// than charging it through checkMemoryWith every iteration -- keeps the walk O(n) in
// the receiver size instead of re-walking the receiver per entry.
func (exec *Execution) evalForHash(stmt *ForStmt, env *Env, iterable, last Value) (Value, bool, error) {
	entries := iterable.Hash()
	scratch := sortedKeyBufferBytes(len(entries))
	if len(entries) > 0 {
		scratch = saturatingAdd(scratch, collapsedPairBytes)
	}
	delta := exec.reserveLoopScratch(scratch)
	defer exec.releaseLoopScratch(delta)

	// The scratch and the reserved pair are now in the live baseline, so the preflight
	// charges them through the call roots. The iterable plays the receiver role here,
	// so an ephemeral hash literal is counted while a hash already bound to a variable
	// is deduplicated against the live base.
	if err := exec.checkProjectedHashWalkBytes(iterable, nil, nil, NewNil()); err != nil {
		return NewNil(), false, err
	}
	var keyBuf [smallHashKeyBufferSize]string
	for _, key := range sortedHashKeysInto(entries, keyBuf[:]) {
		if err := exec.step(); err != nil {
			return NewNil(), false, exec.wrapError(err, stmt.Pos())
		}
		// Hash keys round-trip as symbols, the same shape hash.each and hash.keys
		// expose.
		pair := NewArray([]Value{NewSymbol(key), entries[key]})
		env.Assign(stmt.Iterator, pair)
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return last, false, nil
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
	return last, false, nil
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
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

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
			return last, false, nil
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return last, false, nil
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

func (exec *Execution) evalUntilStatement(stmt *UntilStmt, env *Env) (Value, bool, error) {
	exec.loopDepth++
	defer func() {
		exec.loopDepth--
	}()

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
			return last, false, nil
		}
		val, returned, err := exec.evalStatements(stmt.Body, env)
		if err != nil {
			if errors.Is(err, errLoopBreak) {
				return last, false, nil
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
		val, returned, err := exec.evalStatement(stmt, env)
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

func (exec *Execution) evalCompoundAssignment(stmt *AssignStmt, env *Env) (Value, error) {
	current, assign, err := exec.prepareCompoundAssignmentTarget(stmt.Target, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(current); err != nil {
		return NewNil(), err
	}

	right, err := exec.evalExpression(stmt.Value, env)
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(current, right); err != nil {
		return NewNil(), err
	}

	result, err := exec.evalBinaryOperator(stmt.Operator, current, right, stmt.Pos())
	if err != nil {
		return NewNil(), err
	}
	if err := exec.checkMemoryWith(result); err != nil {
		return NewNil(), err
	}
	if err := assign(result); err != nil {
		return NewNil(), err
	}
	return result, nil
}

func (exec *Execution) prepareCompoundAssignmentTarget(target Expression, env *Env) (Value, func(Value) error, error) {
	switch t := target.(type) {
	case *Identifier:
		current, err := exec.evalExpression(t, env)
		if err != nil {
			return NewNil(), nil, err
		}
		return current, func(value Value) error {
			env.Assign(t.Name, value)
			return nil
		}, nil
	case *MemberExpr:
		obj, err := exec.evalExpressionWithAuto(t.Object, env, true)
		if err != nil {
			return NewNil(), nil, err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return NewNil(), nil, err
		}
		member, err := exec.getMember(obj, t.Property, t.Pos())
		if err != nil {
			return NewNil(), nil, err
		}
		current, err := exec.autoInvokeIfNeeded(t, member, obj)
		if err != nil {
			return NewNil(), nil, err
		}
		return current, func(value Value) error {
			return exec.assignToEvaluatedMember(t, obj, value)
		}, nil
	case *IndexExpr:
		obj, err := exec.evalExpressionWithAuto(t.Object, env, true)
		if err != nil {
			return NewNil(), nil, err
		}
		if err := exec.checkMemoryWith(obj); err != nil {
			return NewNil(), nil, err
		}
		idx, err := exec.evalExpressionWithAuto(t.Index, env, true)
		if err != nil {
			return NewNil(), nil, err
		}
		if err := exec.checkMemoryWith(idx); err != nil {
			return NewNil(), nil, err
		}
		current, err := exec.evalIndexValue(t, obj, idx)
		if err != nil {
			return NewNil(), nil, err
		}
		return current, func(value Value) error {
			return exec.assignToEvaluatedIndex(t, obj, idx, value)
		}, nil
	case *IvarExpr, *ClassVarExpr:
		current, err := exec.evalExpression(t, env)
		if err != nil {
			return NewNil(), nil, err
		}
		return current, func(value Value) error {
			return exec.assign(t, value, env)
		}, nil
	case *DestructureTarget:
		return NewNil(), nil, exec.errorAt(t.Pos(), "compound assignment is not supported for destructuring targets")
	default:
		return NewNil(), nil, exec.errorAt(target.Pos(), "invalid assignment target")
	}
}

func (exec *Execution) evalStatement(stmt Statement, env *Env) (Value, bool, error) {
	switch s := stmt.(type) {
	case *ExprStmt:
		val, err := exec.evalExpression(s.Expr, env)
		return val, false, err
	case *ReturnStmt:
		if s.Value == nil {
			return NewNil(), true, nil
		}
		val, err := exec.evalExpression(s.Value, env)
		return val, true, err
	case *RaiseStmt:
		return exec.evalRaiseStatement(s, env)
	case *AssignStmt:
		if s.Operator != "" {
			val, err := exec.evalCompoundAssignment(s, env)
			return val, false, err
		}
		if val, handled, err := exec.evalArrayAppendAssignment(s, env); handled || err != nil {
			return val, false, err
		}
		val, err := exec.evalExpression(s.Value, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), false, err
		}
		if err := exec.assign(s.Target, val, env); err != nil {
			return NewNil(), false, err
		}
		return val, false, nil
	case *IfStmt:
		val, err := exec.evalExpression(s.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), false, err
		}
		if val.Truthy() {
			return exec.evalStatements(s.Consequent, env)
		}
		for _, clause := range s.ElseIf {
			condVal, err := exec.evalExpression(clause.Condition, env)
			if err != nil {
				return NewNil(), false, err
			}
			if err := exec.checkMemoryWith(condVal); err != nil {
				return NewNil(), false, err
			}
			if condVal.Truthy() {
				return exec.evalStatements(clause.Consequent, env)
			}
		}
		if len(s.Alternate) > 0 {
			return exec.evalStatements(s.Alternate, env)
		}
		return NewNil(), false, nil
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
		return NewNil(), false, errLoopBreak
	case *NextStmt:
		if exec.loopDepth == 0 {
			return NewNil(), false, exec.errorAt(s.Pos(), "next used outside of loop")
		}
		return NewNil(), false, errLoopNext
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

func (exec *Execution) evalRaiseStatement(stmt *RaiseStmt, env *Env) (Value, bool, error) {
	if stmt.Value != nil {
		val, err := exec.evalExpression(stmt.Value, env)
		if err != nil {
			return NewNil(), false, err
		}
		return NewNil(), false, exec.errorAt(stmt.Pos(), "%s", val.String())
	}

	err := exec.currentRescuedError()
	if err == nil {
		return NewNil(), false, exec.errorAt(stmt.Pos(), "raise used outside of rescue")
	}
	return NewNil(), false, err
}

func (exec *Execution) evalTryStatement(stmt *TryStmt, env *Env) (Value, bool, error) {
	val, returned, err := exec.evalStatements(stmt.Body, env)
	runElse := err == nil && !returned

	if err != nil && !isLoopControlSignal(err) && !isHostControlSignal(err) && len(stmt.Rescue) > 0 && runtimeErrorMatchesRescueType(err, stmt.RescueTy) {
		rescueEnv := env
		if stmt.RescueBinding != "" {
			rescueEnv = newEnv(env)
			rescueEnv.Define(stmt.RescueBinding, rescuedErrorValue(err))
		}
		exec.pushRescuedError(err)
		rescueVal, rescueReturned, rescueErr := exec.evalStatements(stmt.Rescue, rescueEnv)
		exec.popRescuedError()
		if rescueErr != nil {
			val = NewNil()
			returned = false
			err = rescueErr
		} else {
			val = rescueVal
			returned = rescueReturned
			err = nil
		}
	}

	if runElse && len(stmt.Else) > 0 {
		val, returned, err = exec.evalStatements(stmt.Else, env)
	}

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

func rescuedErrorValue(err error) Value {
	fields := map[string]Value{
		"type":       NewString(classifyRuntimeErrorType(err)),
		"message":    NewString(err.Error()),
		"code_frame": NewString(""),
	}

	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		fields["type"] = NewString(classifyRuntimeErrorType(runtimeErr))
		fields["message"] = NewString(runtimeErr.Message)
		fields["code_frame"] = NewString(runtimeErr.CodeFrame)
	}

	return NewObject(fields)
}

func isLoopControlSignal(err error) bool {
	return errors.Is(err, errLoopBreak) || errors.Is(err, errLoopNext)
}

func isHostControlSignal(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
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
	return canonical == errKind
}
