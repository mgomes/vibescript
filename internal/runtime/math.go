package runtime

import (
	"fmt"
	"math"
)

// registerMathBuiltins installs the Ruby-style `Math` namespace: the
// transcendental constants `PI` and `E` plus the pure numeric helpers that
// Ruby exposes as module functions. Constants are read with either accessor
// (`Math::PI` or `Math.PI`); helpers are called like `Math.sqrt(9)`.
//
// The helpers are constant-time CPU work backed by Go's math package, so they
// need no host capability boundary. They still validate argument arity and
// type and raise on domain violations, mirroring Ruby's Math::DomainError for
// inputs outside a function's mathematical domain. Integer arguments are
// promoted to floats and every helper returns a float, matching Ruby where
// Math always yields a Float.
func registerMathBuiltins(engine *Engine) {
	engine.builtins["Math"] = NewObject(map[string]Value{
		"PI":    NewFloat(math.Pi),
		"E":     NewFloat(math.E),
		"sqrt":  mathUnary("Math.sqrt", math.Sqrt, domainAtLeast(0)),
		"cbrt":  mathUnary("Math.cbrt", math.Cbrt, nil),
		"sin":   mathUnary("Math.sin", math.Sin, nil),
		"cos":   mathUnary("Math.cos", math.Cos, nil),
		"tan":   mathUnary("Math.tan", math.Tan, nil),
		"asin":  mathUnary("Math.asin", math.Asin, domainBetween(-1, 1)),
		"acos":  mathUnary("Math.acos", math.Acos, domainBetween(-1, 1)),
		"atan":  mathUnary("Math.atan", math.Atan, nil),
		"exp":   mathUnary("Math.exp", math.Exp, nil),
		"log2":  mathUnary("Math.log2", math.Log2, domainAtLeast(0)),
		"log10": mathUnary("Math.log10", math.Log10, domainAtLeast(0)),
		"atan2": mathBinary("Math.atan2", math.Atan2),
		"hypot": mathBinary("Math.hypot", math.Hypot),
		"log":   NewBuiltin("Math.log", builtinMathLog),
	})
}

// mathDomain reports whether an input is outside a helper's mathematical
// domain. It mirrors Ruby's domain checks, which compare the raw argument
// against fixed bounds: because every comparison with NaN is false, a NaN
// argument is never rejected and instead propagates through as a NaN result,
// exactly as Ruby and IEEE 754 prescribe.
type mathDomain func(float64) bool

// domainAtLeast rejects inputs below min, matching Ruby's domain_check_min
// (used by sqrt, log, log2, and log10). A negative argument raises while
// +Infinity, which is in-domain, flows through to the Go math function.
func domainAtLeast(min float64) mathDomain {
	return func(x float64) bool { return x < min }
}

// domainBetween rejects inputs outside [min, max], matching Ruby's
// domain_check_range (used by asin and acos). An infinite argument falls
// outside the range and therefore raises, matching Ruby's Math::DomainError.
func domainBetween(min, max float64) mathDomain {
	return func(x float64) bool { return x < min || x > max }
}

// mathUnary builds a single-argument Math helper from a Go math function. The
// optional domain predicate is checked against the raw argument before the
// function runs, so inputs outside the helper's mathematical domain raise a
// domain error (e.g. sqrt(-1), asin(2)) while in-domain inputs, including the
// infinities and NaNs that trig functions accept, produce their IEEE result.
func mathUnary(name string, fn func(float64) float64, outOfDomain mathDomain) Value {
	return NewBuiltin(name, func(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := rejectMathKwargsBlock(name, kwargs, block); err != nil {
			return NewNil(), err
		}
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("%s expects 1 argument, got %d", name, len(args))
		}
		x, err := mathFloatArg(name, args[0])
		if err != nil {
			return NewNil(), err
		}
		if outOfDomain != nil && outOfDomain(x) {
			return NewNil(), fmt.Errorf("%s out of domain", name)
		}
		return NewFloat(fn(x)), nil
	})
}

// mathBinary builds a two-argument Math helper (atan2, hypot). Both are defined
// across the whole real plane, so no domain check is needed.
func mathBinary(name string, fn func(float64, float64) float64) Value {
	return NewBuiltin(name, func(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := rejectMathKwargsBlock(name, kwargs, block); err != nil {
			return NewNil(), err
		}
		if len(args) != 2 {
			return NewNil(), fmt.Errorf("%s expects 2 arguments, got %d", name, len(args))
		}
		x, err := mathFloatArg(name, args[0])
		if err != nil {
			return NewNil(), err
		}
		y, err := mathFloatArg(name, args[1])
		if err != nil {
			return NewNil(), err
		}
		return NewFloat(fn(x, y)), nil
	})
}

// builtinMathLog implements Ruby's `Math.log(x)` and `Math.log(x, base)`.
// With one argument it computes the natural logarithm; with a second it
// computes the logarithm in that base as `log(x) / log(base)`, exactly like
// Ruby. Ruby restricts both operands to the non-negative reals, so a negative
// x or base raises a domain error, while every other special value follows
// from IEEE 754 division. A base of exactly 1 makes log(base) zero, so the
// result is whatever dividing by zero yields: +Infinity for x > 1, -Infinity
// for 0 <= x < 1, and NaN for x == 1 (0/0). This matches Ruby's MRI, which
// also implements the two-argument form as a plain log(x)/log(base) division
// with no special case for base 1 (verified against the ruby binary:
// `Math.log(8, 1)` is Infinity, `Math.log(0.5, 1)` is -Infinity, and
// `Math.log(1, 1)` is NaN). We deliberately do not special-case base 1 to
// raise or return NaN, because that would diverge from real Ruby.
func builtinMathLog(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	const name = "Math.log"
	if err := rejectMathKwargsBlock(name, kwargs, block); err != nil {
		return NewNil(), err
	}
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s expects 1 or 2 arguments, got %d", name, len(args))
	}
	x, err := mathFloatArg(name, args[0])
	if err != nil {
		return NewNil(), err
	}
	if x < 0 {
		return NewNil(), fmt.Errorf("%s out of domain", name)
	}
	if len(args) == 1 {
		return NewFloat(math.Log(x)), nil
	}
	base, err := mathFloatArg(name, args[1])
	if err != nil {
		return NewNil(), err
	}
	if base < 0 {
		return NewNil(), fmt.Errorf("%s out of domain", name)
	}
	return NewFloat(math.Log(x) / math.Log(base)), nil
}

// mathFloatArg coerces a Math argument to a float. Integers are promoted and
// floats pass through (including NaN and Infinity); any other type raises,
// matching Ruby's TypeError for non-numeric Math arguments.
func mathFloatArg(name string, arg Value) (float64, error) {
	switch arg.Kind() {
	case KindInt:
		return float64(arg.Int()), nil
	case KindFloat:
		return arg.Float(), nil
	default:
		return 0, fmt.Errorf("%s expects a numeric argument, got %s", name, arg.Kind())
	}
}

func rejectMathKwargsBlock(name string, kwargs map[string]Value, block Value) error {
	if len(kwargs) > 0 {
		return fmt.Errorf("%s does not accept keyword arguments", name)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept a block", name)
	}
	return nil
}
